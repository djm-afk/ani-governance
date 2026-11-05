//go:build quota_pg

package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/api"
	"go-wind-admin/app/admin/service/internal/data/ent/permission"
	"go-wind-admin/app/admin/service/internal/data/ent/permissionapi"
	"go-wind-admin/app/admin/service/internal/data/ent/role"
	"go-wind-admin/app/admin/service/internal/data/ent/rolepermission"
	"go-wind-admin/app/admin/service/internal/data/ent/user"
	"go-wind-admin/app/admin/service/internal/data/ent/userrole"
	"go-wind-admin/app/admin/service/tests/testutil"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	"go-wind-admin/pkg/middleware/auth"
)

// The dataset:read capability must gate the asset itself, not one route that
// happens to read it. This test builds a real tenant/role/permission graph and
// checks that AuthorizeDataset is an independent decision: a role holding the
// create capability cannot read, and one holding dataset:read cannot create.
// It proves the check the admission path calls, so a caller who knows an
// InputVersion ID cannot bypass the query routes.
func TestModelDevDatasetAuthorizationIsIndependentOfCreate(t *testing.T) {
	c := testutil.NewQuotaPGClient(t)
	testutil.ResetQuotaFixture(t, c)
	sys := appViewer.NewSystemViewerContext(context.Background())
	name := "dataset-auth-" + uuid.NewString()

	tenant, err := c.Client().Tenant.Create().SetName(name).SetCode(name).Save(sys)
	require.NoError(t, err)
	operator, err := c.Client().User.Create().SetTenantID(tenant.ID).SetUsername(name).SetStatus(user.StatusNormal).Save(sys)
	require.NoError(t, err)
	memberRole, err := c.Client().Role.Create().SetTenantID(tenant.ID).SetName(name).SetCode("tenant:" + name).
		SetType(role.TypeTenant).SetStatus(role.StatusOn).SetDataScope(role.DataScopeAll).Save(sys)
	require.NoError(t, err)
	require.NoError(t, c.Client().UserRole.Create().SetTenantID(tenant.ID).SetUserID(operator.ID).
		SetRoleID(memberRole.ID).SetStatus(userrole.StatusActive).Exec(sys))

	// This test creates the capability rows and their catalog links itself, so it
	// asserts the authorization graph rather than depending on whether the
	// deployment seed has been applied to this database. The seed is separately
	// idempotent; here the route binding is what proves create != read.
	grant := func(code, path, method string) *ent.Permission {
		row, err := c.Client().Permission.Query().Where(permission.CodeEQ(code), permission.DeletedAtIsNil()).Only(sys)
		if ent.IsNotFound(err) {
			row, err = c.Client().Permission.Create().SetName(code).SetCode(code).SetStatus(permission.StatusOn).Save(sys)
		}
		require.NoError(t, err)
		route, err := c.Client().Api.Query().Where(api.PathEQ(path), api.MethodEQ(method), api.ScopeEQ(api.ScopeAdmin)).Only(sys)
		if ent.IsNotFound(err) {
			route, err = c.Client().Api.Create().SetModule("ModelDevService").SetPath(path).SetMethod(method).
				SetScope(api.ScopeAdmin).SetBusinessModule(api.BusinessModuleModelDev).SetStatus(api.StatusOn).Save(sys)
		}
		require.NoError(t, err)
		linked, err := c.Client().PermissionApi.Query().Where(
			permissionapi.PermissionIDEQ(row.ID), permissionapi.APIIDEQ(route.ID), permissionapi.DeletedAtIsNil()).Exist(sys)
		require.NoError(t, err)
		if !linked {
			require.NoError(t, c.Client().PermissionApi.Create().SetPermissionID(row.ID).SetAPIID(route.ID).Exec(sys))
		}
		require.NoError(t, c.Client().RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(memberRole.ID).
			SetPermissionID(row.ID).SetStatus(rolepermission.StatusOn).SetEffect(rolepermission.EffectAllow).Exec(sys))
		return row
	}

	ctx := auth.NewPrincipalContext(sys, &auth.Principal{Type: auth.SubjectUser, ID: operator.ID, TenantID: tenant.ID})
	repo := data.NewModelDevAuthorizationRepo(c)

	// Only create. The read capability must still be denied.
	grant("modeldev:dataset:create", "/api/v1/modeldev/datasets", "POST")
	require.NoError(t, repo.AuthorizeDataset(ctx, tenant.ID, operator.ID, data.ModelDevDatasetActionCreate))
	require.ErrorIs(t, repo.AuthorizeDataset(ctx, tenant.ID, operator.ID, data.ModelDevDatasetActionGet), data.ErrModelDevAuthorizationDenied,
		"create capability must not imply the asset read")

	// Add the read capability on the InputVersion route the admission path
	// checks. The same caller can now read, and still cannot update or import.
	grant("modeldev:dataset:read", "/api/v1/modeldev/datasets/{dataset_id}", "GET")
	require.NoError(t, repo.AuthorizeDataset(ctx, tenant.ID, operator.ID, data.ModelDevDatasetActionGet))
	require.ErrorIs(t, repo.AuthorizeDataset(ctx, tenant.ID, operator.ID, data.ModelDevDatasetActionUpdate), data.ErrModelDevAuthorizationDenied)
	require.ErrorIs(t, repo.AuthorizeDataset(ctx, tenant.ID, operator.ID, data.ModelDevDatasetActionImport), data.ErrModelDevAuthorizationDenied)

	// Disabling the role revokes both, including on a current call.
	require.NoError(t, c.Client().Role.UpdateOneID(memberRole.ID).SetStatus(role.StatusOff).Exec(sys))
	require.ErrorIs(t, repo.AuthorizeDataset(ctx, tenant.ID, operator.ID, data.ModelDevDatasetActionGet), data.ErrModelDevAuthorizationDenied)

	// Another tenant's ID never rescues the call.
	other, err := c.Client().Tenant.Create().SetName(name + "-b").SetCode(name + "-b").Save(sys)
	require.NoError(t, err)
	require.ErrorIs(t, repo.AuthorizeDataset(ctx, other.ID, operator.ID, data.ModelDevDatasetActionGet), data.ErrModelDevAuthorizationDenied)
}
