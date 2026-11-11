package service

import (
	"context"
	stderrors "errors"
	"sync"
	"time"

	errors "github.com/go-kratos/kratos/v2/errors"
	gpu "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/gpu/v1"
	admin "go-wind-admin/api/gen/go/admin/service/v1"
	view "go-wind-admin/api/gen/go/gpu/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
)

type GpuTenantAdminService struct {
	admin.UnimplementedGpuTenantAdminServiceServer
	quotas     *data.QuotaAdminRepo
	monitoring *GpuMonitoringService
}

func NewGpuTenantAdminService(quotas *data.QuotaAdminRepo, monitoring *GpuMonitoringService) *GpuTenantAdminService {
	return &GpuTenantAdminService{quotas: quotas, monitoring: monitoring}
}

type gpuTenantListBudgetKey struct{}
type gpuTenantListIdentityKey struct{}
type gpuTenantListBudget struct {
	mu     sync.Mutex
	curves int
}

func gpuTenantMonitoringIdentity(ctx context.Context, resolver ResourceTenantResolver, tenantID uint32) (string, error) {
	if mappings, ok := ctx.Value(gpuTenantListIdentityKey{}).(map[uint32]string); ok {
		value, found := mappings[tenantID]
		if !found || !metricUUID(value) {
			return "", errors.NotFound("GPU_NOT_FOUND", "GPU tenant mapping unavailable")
		}
		return value, nil
	}
	return resolver.ResourceTenantID(ctx, tenantID)
}

// gpuTenantListReserve runs at the common execution boundary, after trusted
// tenant authorization and before Obs. Obs bounds the actual returned sources.
func gpuTenantListReserve(ctx context.Context, target *gpu.GpuTarget, curves int) error {
	b, ok := ctx.Value(gpuTenantListBudgetKey{}).(*gpuTenantListBudget)
	if !ok {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if target == nil || curves < 1 || b.curves+curves > 12 {
		return gpuBudget()
	}
	b.curves += curves
	return nil
}

func (s *GpuTenantAdminService) ListGpuTenants(ctx context.Context, req *view.ListGpuTenantsRequest) (*view.ListGpuTenantsResponse, error) {
	if _, err := acceleratorPrincipal(ctx, true); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, gpuInvalid()
	}
	page, size := uint32(1), uint32(6)
	if req.Page != nil {
		page = *req.Page
	}
	if req.PageSize != nil {
		size = *req.PageSize
	}
	if page == 0 || size == 0 || size > 6 || req.TenantId != nil && *req.TenantId == 0 {
		return nil, gpuInvalid()
	}
	if s.quotas == nil || s.monitoring == nil {
		return nil, errors.ServiceUnavailable("GPU_SOURCE_UNAVAILABLE", "GPU tenant summary unavailable")
	}
	batch, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	batch = context.WithValue(batch, gpuTenantListBudgetKey{}, &gpuTenantListBudget{})
	quotaPage, err := s.quotas.ListTenantAccountSummaryPage(batch, data.TenantQuotaAccountSummaryFilter{Page: page, PageSize: size, TenantID: req.TenantId})
	if err != nil {
		return nil, err
	}
	batch = context.WithValue(batch, gpuTenantListIdentityKey{}, quotaPage.ResourceTenantIDs)
	quotas := quotaPage.Items
	out := &view.ListGpuTenantsResponse{Items: make([]*view.GpuTenantCurrentSummary, len(quotas)), Total: quotaPage.Total}
	if len(quotas) == 0 {
		return out, nil
	}
	for i, quota := range quotas {
		out.Items[i] = &view.GpuTenantCurrentSummary{Quota: quota, ObservationBasis: "EXPORTER_OBSERVED", OwnerScope: "ani-resource-service"}
	}

	jobs := make(chan int, len(quotas))
	for i := range quotas {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	var failure error
	var failureOnce sync.Once
	fail := func(err error) { failureOnce.Do(func() { failure = err; cancel() }) }
	for n := 0; n < min(2, len(quotas)); n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if batch.Err() != nil {
					return
				}
				usage, err := s.monitoring.GetTenantGpuLatest(batch, &view.GpuTenantLatestRequest{TenantId: quotas[i].TenantId, MetricCodes: []string{"gpu.resource.memory_used_bytes", "gpu.resource.memory_limit_bytes"}})
				if err != nil {
					code := errors.Code(err)
					if code == 429 {
						fail(gpuBudget())
						return
					}
					if code == 504 || stderrors.Is(err, context.DeadlineExceeded) || batch.Err() != nil {
						fail(errors.GatewayTimeout("GPU_QUERY_TIMEOUT", "GPU tenant page deadline exceeded"))
						return
					}
					out.Items[i].UsageStatus = "UNAVAILABLE"
					out.Items[i].UsageReason = gpuTenantUsageReason(err)
					continue
				}
				out.Items[i].UsageStatus, out.Items[i].UsageReason = gpuTenantUsageStatus(usage)
				if out.Items[i].UsageStatus != "UNAVAILABLE" {
					out.Items[i].Usage = usage
				}
			}
		}()
	}
	workers.Wait()
	if failure != nil {
		return nil, failure
	}
	if err := batch.Err(); err != nil {
		return nil, errors.GatewayTimeout("GPU_QUERY_TIMEOUT", "GPU tenant page deadline exceeded")
	}
	return out, nil
}

func gpuTenantUsageReason(err error) string {
	switch errors.Reason(err) {
	case "GPU_SCOPE_CHANGED":
		return "GPU_SCOPE_CHANGED"
	case "GPU_NOT_FOUND", "TENANT_NOT_FOUND", "QUOTA_NOT_FOUND":
		return "GPU_TENANT_MAPPING_UNAVAILABLE"
	case "GPU_INVALID_RESPONSE", "ACCELERATOR_INVALID_RESPONSE":
		return "GPU_INVALID_RESPONSE"
	default:
		return "GPU_SOURCE_UNAVAILABLE"
	}
}

func gpuTenantUsageStatus(usage *view.GpuQueryResponse) (string, string) {
	if usage == nil || len(usage.Series) != 2 {
		return "UNAVAILABLE", "GPU_INVALID_RESPONSE"
	}
	allOK, noData := usage.IndexComplete, true
	codes := map[string]bool{}
	for _, series := range usage.Series {
		if series == nil || series.Quality == nil || len(series.Points) > 1 || codes[series.MetricCode] || series.MetricCode != "gpu.resource.memory_used_bytes" && series.MetricCode != "gpu.resource.memory_limit_bytes" {
			return "UNAVAILABLE", "GPU_INVALID_RESPONSE"
		}
		codes[series.MetricCode] = true
		allOK = allOK && series.Quality.Status == "OK"
		noData = noData && series.Quality.Status == "NO_DATA"
	}
	if allOK {
		return "AVAILABLE", ""
	}
	if noData {
		return "NO_DATA", "GPU_NO_DATA"
	}
	return "PARTIAL", "GPU_PARTIAL_DATA"
}
