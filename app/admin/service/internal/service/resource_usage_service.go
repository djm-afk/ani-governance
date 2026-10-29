package service

import (
	"context"
	stderrors "errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	errors "github.com/go-kratos/kratos/v2/errors"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	gpu "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/gpu/v1"
	obs "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/usage/v1"
	admin "go-wind-admin/api/gen/go/admin/service/v1"
	view "go-wind-admin/api/gen/go/usage/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ResourceUsageReader interface {
	QueryUsage(context.Context, string, *obs.QueryUsageRequest) (*obs.QueryUsageResponse, error)
}
type ResourceUsageTenantReader interface {
	ResourceUsageTenants(context.Context, uint32) ([]data.ResourceUsageTenant, error)
}

type ResourceUsageAdminService struct {
	admin.UnimplementedResourceUsageAdminServiceServer
	tenants    ResourceUsageTenantReader
	monitoring *GpuMonitoringService
	reader     ResourceUsageReader
	// One short-lived result reuses the expensive trusted scope/metrics snapshot
	// across the overview and ranking requests with the same exact window.
	mu    sync.Mutex
	cache *resourceUsageSnapshot
}

type resourceUsageSnapshot struct {
	start, end string
	actor      string
	tenantID   uint32
	expires    time.Time
	tenants    []data.ResourceUsageTenant
	result     *obs.QueryUsageResponse
}

func NewResourceUsageAdminService(t ResourceUsageTenantReader, m *GpuMonitoringService, r ResourceUsageReader) *ResourceUsageAdminService {
	return &ResourceUsageAdminService{tenants: t, monitoring: m, reader: r}
}
func resourceUsageInvalid() error {
	return errors.BadRequest("RESOURCE_USAGE_INVALID_QUERY", "invalid resource usage query")
}
func resourceUsageUnavailable() error {
	return errors.ServiceUnavailable("RESOURCE_USAGE_UNAVAILABLE", "resource usage dependency unavailable")
}
func resourceUsageBudget() error {
	return errors.New(429, "RESOURCE_USAGE_BUDGET_EXCEEDED", "resource usage query exceeds budget")
}
func resourceUsageDependency(err error) error {
	if stderrors.Is(err, context.DeadlineExceeded) {
		return errors.GatewayTimeout("RESOURCE_USAGE_TIMEOUT", "resource usage query timed out")
	}
	if stderrors.Is(err, context.Canceled) {
		return errors.New(499, "RESOURCE_USAGE_CANCELED", "resource usage query canceled")
	}
	switch status.Code(err) {
	case codes.InvalidArgument:
		return resourceUsageInvalid()
	case codes.ResourceExhausted:
		return resourceUsageBudget()
	case codes.DeadlineExceeded:
		return errors.GatewayTimeout("RESOURCE_USAGE_TIMEOUT", "resource usage query timed out")
	case codes.Canceled:
		return errors.New(499, "RESOURCE_USAGE_CANCELED", "resource usage query canceled")
	default:
		return resourceUsageUnavailable()
	}
}
func resourceUsageWindow(start, end string) (*timestamppb.Timestamp, *timestamppb.Timestamp, error) {
	a, e1 := time.Parse(time.RFC3339, start)
	b, e2 := time.Parse(time.RFC3339, end)
	now := time.Now().UTC().Truncate(time.Second)
	if e1 != nil || e2 != nil || a.Nanosecond() != 0 || b.Nanosecond() != 0 || !b.After(a) || b.Sub(a) > 30*24*time.Hour || a.Before(now.Add(-30*24*time.Hour-time.Minute)) || b.After(now.Add(5*time.Second)) {
		return nil, nil, resourceUsageInvalid()
	}
	if b.After(now) {
		b = now
	}
	if !b.After(a) {
		return nil, nil, resourceUsageInvalid()
	}
	return timestamppb.New(a), timestamppb.New(b), nil
}
func resourceUsageMetric(metric string) (string, error) {
	if metric == "" {
		return "gpu", nil
	}
	if metric != "gpu" && metric != "cpu" && metric != "memory" {
		return "", resourceUsageInvalid()
	}
	return metric, nil
}

func (s *ResourceUsageAdminService) clusters(ctx context.Context) ([]string, error) {
	if s.monitoring == nil || s.monitoring.accelerator == nil || s.monitoring.accelerator.client == nil || s.monitoring.accelerator.client.Admin == nil {
		return nil, resourceUsageUnavailable()
	}
	a := s.monitoring.accelerator
	c, err := a.adminContext(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	ids := map[string]bool{}
	tokens := map[string]bool{"": true}
	token := ""
	for page := 0; ; page++ {
		if page >= 64 {
			return nil, resourceUsageBudget()
		}
		reply, err := a.client.Admin.ListClusters(ctx, &acc.ListClustersRequest{Context: c, Page: &acc.Page{Size: 100, Token: token}})
		if err != nil {
			return nil, resourceUsageDependency(err)
		}
		if reply == nil {
			return nil, resourceUsageUnavailable()
		}
		for _, v := range reply.Items {
			if v == nil || !metricUUID(v.ClusterId) || ids[v.ClusterId] {
				return nil, resourceUsageUnavailable()
			}
			ids[v.ClusterId] = true
			out = append(out, v.ClusterId)
			if len(out) > 16 {
				return nil, resourceUsageBudget()
			}
		}
		if reply.NextToken == "" {
			break
		}
		if tokens[reply.NextToken] {
			return nil, resourceUsageUnavailable()
		}
		token = reply.NextToken
		tokens[token] = true
	}
	if len(out) == 0 {
		return nil, resourceUsageUnavailable()
	}
	sort.Strings(out)
	return out, nil
}

func (s *ResourceUsageAdminService) query(ctx context.Context, start, end string, tenantID uint32) (*resourceUsageSnapshot, error) {
	p, err := acceleratorPrincipal(ctx, true)
	if err != nil {
		return nil, err
	}
	from, to, err := resourceUsageWindow(start, end)
	if err != nil {
		return nil, err
	}
	if s.tenants == nil || s.reader == nil || s.monitoring == nil {
		return nil, resourceUsageUnavailable()
	}
	actor := gpuActor(p)
	// Serialize only this small cache population: concurrent identical requests
	// cannot multiply ACC/Prometheus work, and canceled waiters leave promptly.
	for !s.mu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, resourceUsageDependency(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer s.mu.Unlock()
	if s.cache != nil && s.cache.start == start && s.cache.end == end && s.cache.actor == actor && s.cache.tenantID == tenantID && time.Now().Before(s.cache.expires) {
		return s.cache, nil
	}
	batch, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	tenants, err := s.tenants.ResourceUsageTenants(batch, tenantID)
	if err != nil {
		return nil, resourceUsageUnavailable()
	}
	if len(tenants) > 128 {
		return nil, resourceUsageBudget()
	}
	if tenantID != 0 {
		var selected []data.ResourceUsageTenant
		for _, t := range tenants {
			if t.ID == tenantID {
				selected = append(selected, t)
			}
		}
		if len(selected) == 0 {
			return nil, errors.NotFound("RESOURCE_USAGE_TENANT_NOT_FOUND", "tenant not found")
		}
		tenants = selected
	}
	clusters, err := s.clusters(batch)
	if err != nil {
		return nil, err
	}
	scopes := make([]*obs.TenantScope, len(tenants))
	identities := map[uint32]string{}
	resourceIDs := map[string]bool{}
	for _, t := range tenants {
		if t.ID == 0 || identities[t.ID] != "" || !metricUUID(t.ResourceTenantID) || resourceIDs[t.ResourceTenantID] {
			return nil, resourceUsageUnavailable()
		}
		identities[t.ID] = t.ResourceTenantID
		resourceIDs[t.ResourceTenantID] = true
	}
	batch = context.WithValue(batch, gpuTenantListIdentityKey{}, identities)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, t := range tenants {
		i, t := i, t
		select {
		case sem <- struct{}{}:
		case <-batch.Done():
			return nil, resourceUsageDependency(batch.Err())
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			target, _, _, e := s.monitoring.tenantTarget(batch, t.ID, "", "", from, to, true)
			scope := &obs.TenantScope{TenantId: t.ID, ResourceTenantId: t.ResourceTenantID, GpuTarget: target}
			if e != nil {
				scope.GpuTarget = &gpu.GpuTarget{Kind: "TENANT", TenantId: t.ResourceTenantID, IndexComplete: false}
				scope.GpuUnavailableReason = "GPU_SCOPE_UNAVAILABLE"
			}
			scopes[i] = scope
		}()
	}
	wg.Wait()
	if batch.Err() != nil {
		return nil, resourceUsageDependency(batch.Err())
	}
	result, err := s.reader.QueryUsage(batch, actor, &obs.QueryUsageRequest{Start: from, End: to, Tenants: scopes, ClusterIds: clusters, Metrics: []string{"gpu", "cpu", "memory"}})
	if err != nil {
		return nil, resourceUsageDependency(err)
	}
	if err = validateResourceUsageResult(result, from, to, tenants); err != nil {
		return nil, err
	}
	out := &resourceUsageSnapshot{start: start, end: end, actor: actor, tenantID: tenantID, expires: time.Now().Add(15 * time.Second), tenants: tenants, result: result}
	s.cache = out
	return out, nil
}
func validateResourceUsageResult(r *obs.QueryUsageResponse, start, end *timestamppb.Timestamp, tenants []data.ResourceUsageTenant) error {
	if r == nil || !proto.Equal(r.Start, start) || !proto.Equal(r.End, end) || r.StepSeconds == 0 || r.DataCutoff == nil || !r.DataCutoff.IsValid() || len(r.Tenants) != len(tenants) {
		return resourceUsageUnavailable()
	}
	ids := map[uint32]bool{}
	for _, t := range tenants {
		ids[t.ID] = true
	}
	if !validResourceUsageMetrics(r.Metrics) {
		return resourceUsageUnavailable()
	}
	for _, t := range r.Tenants {
		if t == nil || !ids[t.TenantId] || !validResourceUsageMetrics(t.Metrics) {
			return resourceUsageUnavailable()
		}
		delete(ids, t.TenantId)
	}
	return nil
}
func validResourceUsageMetrics(metrics []*obs.MetricUsage) bool {
	seen := map[string]bool{}
	for _, m := range metrics {
		if m == nil || seen[m.Metric] || (m.Metric != "gpu" && m.Metric != "cpu" && m.Metric != "memory") || m.Quality == nil {
			return false
		}
		seen[m.Metric] = true
		unit := "GiB"
		if m.Metric == "cpu" {
			unit = "cores"
		}
		if m.Unit != unit {
			return false
		}
		if m.Quality.Status != "OK" && m.Quality.Status != "PARTIAL" && m.Quality.Status != "NO_DATA" && m.Quality.Status != "UNAVAILABLE" {
			return false
		}
		for _, v := range []*float64{m.Mean, m.Peak, m.KnownMean, m.KnownPeak, m.Quality.Coverage} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
				return false
			}
		}
		if m.Quality.Coverage != nil && *m.Quality.Coverage > 1 {
			return false
		}
		if m.Quality.Status != "OK" && (m.Mean != nil || m.Peak != nil) {
			return false
		}
	}
	return len(seen) == 3
}
func resourceUsageMetricView(m *obs.MetricUsage, points bool) (*view.MetricUsage, error) {
	if m == nil {
		return nil, resourceUsageUnavailable()
	}
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil, resourceUsageUnavailable()
	}
	out := new(view.MetricUsage)
	if err = protojson.Unmarshal(raw, out); err != nil {
		return nil, resourceUsageUnavailable()
	}
	if !points {
		out.Points = []*view.UsagePoint{}
	}
	return out, nil
}
func resourceUsageViews(metrics []*obs.MetricUsage, selected string) ([]*view.MetricUsage, error) {
	out := make([]*view.MetricUsage, 0, len(metrics))
	for _, m := range metrics {
		v, e := resourceUsageMetricView(m, m.Metric == selected)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *ResourceUsageAdminService) GetResourceUsageOverview(ctx context.Context, r *view.OverviewRequest) (*view.Overview, error) {
	if r == nil {
		return nil, resourceUsageInvalid()
	}
	metric, e := resourceUsageMetric(r.Metric)
	if e != nil {
		return nil, e
	}
	snap, e := s.query(ctx, r.Start, r.End, 0)
	if e != nil {
		return nil, e
	}
	metrics, e := resourceUsageViews(snap.result.Metrics, metric)
	if e != nil {
		return nil, e
	}
	return &view.Overview{Start: snap.result.Start, End: snap.result.End, StepSeconds: snap.result.StepSeconds, DataCutoff: snap.result.DataCutoff, Metrics: metrics}, nil
}
func resourceUsageFindMetric(metrics []*obs.MetricUsage, metric string) *obs.MetricUsage {
	for _, m := range metrics {
		if m.Metric == metric {
			return m
		}
	}
	return nil
}
func (s *ResourceUsageAdminService) ListResourceUsageTenants(ctx context.Context, r *view.TenantsRequest) (*view.Ranking, error) {
	if r == nil {
		return nil, resourceUsageInvalid()
	}
	metric, e := resourceUsageMetric(r.Metric)
	if e != nil {
		return nil, e
	}
	order := r.Order
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return nil, resourceUsageInvalid()
	}
	page, size := uint32(1), uint32(20)
	if r.Page != nil {
		page = *r.Page
	}
	if r.PageSize != nil {
		size = *r.PageSize
	}
	if page == 0 || size == 0 || size > 100 || len(r.Keyword) > 256 {
		return nil, resourceUsageInvalid()
	}
	snap, e := s.query(ctx, r.Start, r.End, 0)
	if e != nil {
		return nil, e
	}
	names := map[uint32]string{}
	for _, t := range snap.tenants {
		names[t.ID] = t.Name
	}
	rows := make([]*view.TenantUsageRow, 0, len(snap.result.Tenants))
	total := resourceUsageFindMetric(snap.result.Metrics, metric)
	complete := total != nil && total.Quality.Status == "OK" && total.Mean != nil
	for _, t := range snap.result.Tenants {
		m := resourceUsageFindMetric(t.Metrics, metric)
		v, e := resourceUsageMetricView(m, false)
		if e != nil {
			return nil, e
		}
		row := &view.TenantUsageRow{TenantId: t.TenantId, TenantName: names[t.TenantId], Usage: v}
		if !resourceUsageComplete(v) {
			complete = false
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		ac, bc := resourceUsageComplete(a.Usage), resourceUsageComplete(b.Usage)
		if ac != bc {
			return ac
		}
		if ac && *a.Usage.Mean != *b.Usage.Mean {
			return *a.Usage.Mean > *b.Usage.Mean
		}
		return a.TenantId < b.TenantId
	})
	// Rank always means global descending usage rank, independent of the
	// requested display order, keyword and pagination.
	for i, row := range rows {
		if complete {
			rank := uint64(i + 1)
			row.Rank = &rank
			if *total.Mean > 0 {
				share := *row.Usage.Mean / *total.Mean
				if share >= 0 && share <= 1 {
					row.Share = &share
				}
			}
		}
	}
	if order == "asc" {
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			ac, bc := resourceUsageComplete(a.Usage), resourceUsageComplete(b.Usage)
			if ac != bc {
				return ac
			}
			if ac && *a.Usage.Mean != *b.Usage.Mean {
				return *a.Usage.Mean < *b.Usage.Mean
			}
			return a.TenantId < b.TenantId
		})
	}
	keyword := strings.ToLower(strings.TrimSpace(r.Keyword))
	matched := make([]*view.TenantUsageRow, 0, len(rows))
	for _, row := range rows {
		if keyword == "" || strings.Contains(strings.ToLower(row.TenantName), keyword) || strings.Contains(strconv.FormatUint(uint64(row.TenantId), 10), keyword) {
			matched = append(matched, row)
		}
	}
	metrics, e := resourceUsageViews(snap.result.Metrics, "")
	if e != nil {
		return nil, e
	}
	out := &view.Ranking{Start: snap.result.Start, End: snap.result.End, StepSeconds: snap.result.StepSeconds, DataCutoff: snap.result.DataCutoff, Metrics: metrics, Total: uint64(len(matched)), Items: []*view.TenantUsageRow{}}
	offset := uint64(page-1) * uint64(size)
	for i := offset; i < uint64(len(matched)) && i < offset+uint64(size); i++ {
		out.Items = append(out.Items, matched[i])
	}
	return out, nil
}
func resourceUsageComplete(m *view.MetricUsage) bool {
	return m != nil && m.Quality != nil && m.Quality.Status == "OK" && m.Mean != nil
}
func (s *ResourceUsageAdminService) GetResourceUsageTenant(ctx context.Context, r *view.TenantRequest) (*view.Detail, error) {
	if r == nil || r.TenantId == 0 {
		return nil, resourceUsageInvalid()
	}
	metric, e := resourceUsageMetric(r.Metric)
	if e != nil {
		return nil, e
	}
	snap, e := s.query(ctx, r.Start, r.End, r.TenantId)
	if e != nil {
		return nil, e
	}
	metrics, e := resourceUsageViews(snap.result.Metrics, metric)
	if e != nil {
		return nil, e
	}
	return &view.Detail{Start: snap.result.Start, End: snap.result.End, StepSeconds: snap.result.StepSeconds, DataCutoff: snap.result.DataCutoff, TenantId: r.TenantId, TenantName: snap.tenants[0].Name, Metrics: metrics}, nil
}
