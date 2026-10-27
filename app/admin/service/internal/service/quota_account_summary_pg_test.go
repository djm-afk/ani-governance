//go:build quota_pg

package service

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	admin "go-wind-admin/api/gen/go/admin/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/tests/testutil"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	"go-wind-admin/pkg/middleware/auth"
)

func TestQuotaAccountSummariesPlatformSnapshot(t *testing.T) {
	c := testutil.NewQuotaPGClient(t)
	testutil.ResetQuotaFixture(t, c)
	sys := appViewer.NewSystemViewerContext(context.Background())
	repo := data.NewQuotaAdminRepo(testutil.NewBootstrapContext(nil), c)
	svc := NewQuotaAdminService(testutil.NewBootstrapContext(nil), repo)
	platform := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 0})
	cases := []struct {
		name      string
		limit     *uint64
		occupied  *int64
		available string
		over      bool
	}{
		{"configured-zero-no-account", proto.Uint64(0), nil, "0", false},
		{"configured-over-limit", proto.Uint64(10), proto.Int64(12), "0", true},
		{"unconfigured-no-account", nil, nil, "0", false},
		{"unconfigured-occupied", nil, proto.Int64(5), "0", true},
		{"configured-no-account", proto.Uint64(10), nil, "10", false},
		{"decimal-exact", proto.Uint64(math.MaxInt64), proto.Int64(math.MaxInt64 - 1), "1", false},
	}
	var ids []uint32
	for _, tc := range cases {
		var planID *uint32
		if tc.limit != nil {
			p, err := c.Client().Plan.Create().SetName(tc.name).Save(sys)
			require.NoError(t, err)
			require.NoError(t, c.Client().PlanQuota.Create().SetPlanID(p.ID).SetQuotaCode(GpuPhysicalQuotaCode).SetQuotaValue(*tc.limit).Exec(sys))
			planID = &p.ID
		}
		tenant, err := c.Client().Tenant.Create().SetName(tc.name).SetCode(tc.name).SetNillablePlanID(planID).Save(sys)
		require.NoError(t, err)
		ids = append(ids, tenant.ID)
		if tc.occupied != nil {
			require.NoError(t, c.Client().QuotaAccount.Create().SetTenantID(tenant.ID).SetQuotaCode(GpuPhysicalQuotaCode).SetOccupiedUnits(*tc.occupied).Exec(sys))
		}
	}
	deleted, err := c.Client().Tenant.Create().SetName("deleted-summary").SetCode("deleted-summary").SetDeletedAt(time.Now()).Save(sys)
	require.NoError(t, err)
	result, err := svc.ListTenantQuotaAccountSummaries(platform, &admin.ListTenantQuotaAccountSummariesRequest{QuotaCode: proto.String(GpuPhysicalQuotaCode)})
	require.NoError(t, err)
	require.EqualValues(t, len(cases), result.Total, "total counts tenants and excludes soft-deleted rows")
	require.Len(t, result.Items, len(cases))
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := result.Items[i]
			require.Equal(t, ids[i], summary.TenantId)
			require.Equal(t, tc.name, summary.TenantName)
			require.Len(t, summary.Accounts, 1)
			account := summary.Accounts[0]
			require.Equal(t, tc.limit != nil, account.Configured)
			limit, occupied := "0", "0"
			if tc.limit != nil {
				limit = strconv.FormatUint(*tc.limit, 10)
			}
			if tc.occupied != nil {
				occupied = strconv.FormatInt(*tc.occupied, 10)
			}
			require.Equal(t, limit, account.Limit)
			require.Equal(t, occupied, account.Occupied)
			require.Equal(t, tc.available, account.Available)
			require.Equal(t, tc.over, account.OverLimit)
			require.Equal(t, data.DeriveEnforcement(GpuPhysicalQuotaCode), account.Enforcement, "configuration cannot enable enforcement")
			single, err := svc.ListTenantQuotaAccounts(platform, &admin.GetTenantQuotaAccountsRequest{Id: ids[i]})
			require.NoError(t, err)
			found := false
			for _, item := range single.Items {
				if item.QuotaCode == GpuPhysicalQuotaCode {
					found = true
					require.True(t, proto.Equal(item, account), "existing single-tenant API uses the same calculation")
				}
			}
			require.True(t, found)
		})
	}
	for _, page := range []uint32{2, math.MaxUint32} {
		got, err := svc.ListTenantQuotaAccountSummaries(platform, &admin.ListTenantQuotaAccountSummariesRequest{Page: proto.Uint32(page), PageSize: proto.Uint32(100)})
		require.NoError(t, err)
		require.Empty(t, got.Items)
		require.Equal(t, result.Total, got.Total)
	}
	second, err := svc.ListTenantQuotaAccountSummaries(platform, &admin.ListTenantQuotaAccountSummariesRequest{Page: proto.Uint32(2), PageSize: proto.Uint32(2)})
	require.NoError(t, err)
	require.Equal(t, ids[2], second.Items[0].TenantId, "direct page access and stable ordering")
	require.Equal(t, ids[3], second.Items[1].TenantId)
	filtered, err := svc.ListTenantQuotaAccountSummaries(platform, &admin.ListTenantQuotaAccountSummariesRequest{TenantId: &ids[0]})
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered.Total)
	require.Greater(t, len(filtered.Items[0].Accounts), 1)
	for i := 1; i < len(filtered.Items[0].Accounts); i++ {
		require.Less(t, filtered.Items[0].Accounts[i-1].QuotaCode, filtered.Items[0].Accounts[i].QuotaCode)
	}
	empty, err := svc.ListTenantQuotaAccountSummaries(platform, &admin.ListTenantQuotaAccountSummariesRequest{TenantId: &deleted.ID})
	require.NoError(t, err)
	require.Empty(t, empty.Items)
	require.Zero(t, empty.Total)
	json, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(empty)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":[],"total":"0"}`, string(json))

	for _, request := range []*admin.ListTenantQuotaAccountSummariesRequest{nil, {Page: proto.Uint32(0)}, {PageSize: proto.Uint32(0)}, {PageSize: proto.Uint32(101)}, {TenantId: proto.Uint32(0)}, {QuotaCode: proto.String("")}, {QuotaCode: proto.String("not.registered")}, {QuotaCode: proto.String("' OR 1=1")}} {
		_, err := svc.ListTenantQuotaAccountSummaries(platform, request)
		require.Equal(t, "INVALID_QUOTA_REQUEST", kerrors.FromError(err).Reason)
	}
	for _, principal := range []*auth.Principal{{Type: auth.SubjectUser, ID: 8, TenantID: ids[0]}, {Type: auth.SubjectAPIKey, ID: 9, TenantID: 0}} {
		_, err := svc.ListTenantQuotaAccountSummaries(auth.NewPrincipalContext(context.Background(), principal), &admin.ListTenantQuotaAccountSummariesRequest{TenantId: &ids[1]})
		require.Equal(t, "QUOTA_NOT_FOUND", kerrors.FromError(err).Reason)
	}
	_, err = svc.ListTenantQuotaAccountSummaries(context.Background(), &admin.ListTenantQuotaAccountSummariesRequest{})
	require.Error(t, err)
}
