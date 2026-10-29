//go:build quota_pg

package service

import (
	"context"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/stretchr/testify/require"
	obs "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/usage/v1"
	view "go-wind-admin/api/gen/go/usage/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/tests/testutil"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/protobuf/proto"
)

// Only the external ACC and Obs RPC boundaries are probes. Tenant persistence,
// authenticated scope mapping, page/filter/rank/share and DTOs are real Gov code.
type resourceUsageObsProbe struct {
	calls   int
	request *obs.QueryUsageRequest
	partial bool
}

func (p *resourceUsageObsProbe) QueryUsage(_ context.Context, actor string, r *obs.QueryUsageRequest) (*obs.QueryUsageResponse, error) {
	p.calls++
	p.request = proto.Clone(r).(*obs.QueryUsageRequest)
	if actor != "governance:user:7" {
		panic("invalid actor")
	}
	result := &obs.QueryUsageResponse{Start: r.Start, End: r.End, StepSeconds: 300, DataCutoff: r.End}
	build := func(value float64, partial bool) []*obs.MetricUsage {
		out := []*obs.MetricUsage{}
		for _, metric := range []string{"gpu", "cpu", "memory"} {
			unit := "GiB"
			if metric == "cpu" {
				unit = "cores"
			}
			m := &obs.MetricUsage{Metric: metric, Unit: unit, Mean: proto.Float64(value), Peak: proto.Float64(value * 2), KnownMean: proto.Float64(value), Quality: &obs.Quality{Status: "OK", Coverage: proto.Float64(1), ObservedPoints: 2, ExpectedPoints: 2}, Points: []*obs.UsagePoint{{Timestamp: r.Start, End: r.End, Value: proto.Float64(value)}}}
			if partial {
				m.Mean = nil
				m.Peak = nil
				m.Quality.Status = "PARTIAL"
			}
			out = append(out, m)
		}
		return out
	}
	var sum float64
	for i, tenant := range r.Tenants {
		value := float64(i + 1)
		sum += value
		result.Tenants = append(result.Tenants, &obs.TenantUsage{TenantId: tenant.TenantId, Metrics: build(value, p.partial && i == 0)})
	}
	result.Metrics = build(sum, p.partial)
	return result, nil
}
func newResourceUsageFixture(t *testing.T) (*ResourceUsageAdminService, *resourceUsageObsProbe, context.Context, string, string) {
	t.Helper()
	_, f, _, _, _, platform := newGpuTenantListFixture(t, 3)
	repo := data.NewTenantRepo(testutil.NewBootstrapContext(nil), testutil.NewQuotaPGClient(t))
	peer := &resourceUsageObsProbe{}
	svc := NewResourceUsageAdminService(repo, f.svc, peer)
	end := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	return svc, peer, platform, end.Add(-time.Hour).Format(time.RFC3339), end.Format(time.RFC3339)
}
func TestResourceUsageTrustedScopePagingAndShare(t *testing.T) {
	svc, peer, ctx, start, end := newResourceUsageFixture(t)
	page, e := svc.ListResourceUsageTenants(ctx, &view.TenantsRequest{Start: start, End: end, Page: proto.Uint32(2), PageSize: proto.Uint32(1)})
	require.NoError(t, e)
	require.EqualValues(t, 3, page.Total)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 2, page.Items[0].GetRank())
	require.InDelta(t, 1.0/3, page.Items[0].GetShare(), 1e-12)
	require.Equal(t, 1, peer.calls)
	require.Len(t, peer.request.ClusterIds, 1)
	require.Len(t, peer.request.Tenants, 3)
	for _, scope := range peer.request.Tenants {
		require.NotZero(t, scope.TenantId)
		require.True(t, metricUUID(scope.ResourceTenantId))
		require.Equal(t, scope.ResourceTenantId, scope.GpuTarget.TenantId)
		require.Equal(t, "VERIFIED", scope.GpuTarget.Scopes[0].OwnershipState)
	}
	overview, e := svc.GetResourceUsageOverview(ctx, &view.OverviewRequest{Start: start, End: end, Metric: "cpu"})
	require.NoError(t, e)
	require.Len(t, overview.Metrics, 3)
	require.Equal(t, 1, peer.calls)
	filtered, e := svc.ListResourceUsageTenants(ctx, &view.TenantsRequest{Start: start, End: end, Keyword: page.Items[0].TenantName})
	require.NoError(t, e)
	require.EqualValues(t, 1, filtered.Total)
	require.Equal(t, page.Items[0].GetShare(), filtered.Items[0].GetShare())
	require.Equal(t, 6.0, filtered.Metrics[0].GetMean())
	empty, e := svc.ListResourceUsageTenants(ctx, &view.TenantsRequest{Start: start, End: end, Page: proto.Uint32(4294967295)})
	require.NoError(t, e)
	require.Empty(t, empty.Items)
	require.EqualValues(t, 3, empty.Total)
	asc, e := svc.ListResourceUsageTenants(ctx, &view.TenantsRequest{Start: start, End: end, Order: "asc"})
	require.NoError(t, e)
	require.EqualValues(t, 3, asc.Items[0].GetRank())
	require.EqualValues(t, 1, asc.Items[2].GetRank())
	detail, e := svc.GetResourceUsageTenant(ctx, &view.TenantRequest{TenantId: page.Items[0].TenantId, Start: start, End: end, Metric: "gpu"})
	require.NoError(t, e)
	require.Len(t, peer.request.Tenants, 1)
	require.Equal(t, page.Items[0].TenantId, detail.TenantId)
	require.NotEmpty(t, detail.Metrics[0].Points)
}
func TestResourceUsageUnknownAndPlatformGuard(t *testing.T) {
	svc, peer, ctx, start, end := newResourceUsageFixture(t)
	peer.partial = true
	page, e := svc.ListResourceUsageTenants(ctx, &view.TenantsRequest{Start: start, End: end})
	require.NoError(t, e)
	require.Len(t, page.Items, 3)
	for _, row := range page.Items {
		require.Nil(t, row.Share)
		require.Nil(t, row.Rank)
	}
	unknown := page.Items[len(page.Items)-1]
	require.Nil(t, unknown.Usage.Mean)
	require.NotNil(t, unknown.Usage.KnownMean)
	_, e = svc.GetResourceUsageOverview(context.Background(), &view.OverviewRequest{Start: start, End: end})
	require.EqualValues(t, 401, kerrors.Code(e))
	for _, p := range []*auth.Principal{{Type: auth.SubjectUser, ID: 7, TenantID: 1}, {Type: auth.SubjectAPIKey, ID: 7}} {
		_, e = svc.GetResourceUsageOverview(auth.NewPrincipalContext(context.Background(), p), &view.OverviewRequest{Start: start, End: end})
		require.EqualValues(t, 403, kerrors.Code(e))
	}
}
