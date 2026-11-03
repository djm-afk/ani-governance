//go:build quota_pg

package data

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/accesskey"
	"go-wind-admin/app/admin/service/internal/data/ent/api"
	"go-wind-admin/app/admin/service/internal/data/ent/permission"
	"go-wind-admin/app/admin/service/internal/data/ent/planmodule"
	"go-wind-admin/app/admin/service/internal/data/ent/role"
	"go-wind-admin/app/admin/service/internal/data/ent/rolepermission"
	"go-wind-admin/app/admin/service/internal/data/ent/tenant"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	"go-wind-admin/pkg/middleware/auth"
	"testing"
	"time"
)

func TestNetworkInterfaceKeyAuthorizationUsesCurrentBoundRolePostgres(t *testing.T) {
	for _, scenario := range []string{"allowed-without-user-membership", "revoked-key", "expired-key", "role-off", "permission-off", "foreign-tenant", "module-revoked", "wrong-route"} {
		t.Run(scenario, func(t *testing.T) {
			c := newLedgerPGClient(t)
			ctx := appViewer.NewSystemViewerContext(context.Background())
			name := "vnic-" + uuid.NewString()
			plan := c.Client().Plan.Create().SetName(name).SaveX(ctx)
			tn := c.Client().Tenant.Create().SetName(name).SetCode(name).SetPlanID(plan.ID).SetStatus(tenant.StatusOn).SaveX(ctx)
			module := c.Client().PlanModule.Create().SetPlanID(plan.ID).SetModule(planmodule.ModuleVirtualMachine).SaveX(ctx)
			bound := c.Client().Role.Create().SetTenantID(tn.ID).SetName(name).SetCode("tenant:" + name).SetType(role.TypeTenant).SetStatus(role.StatusOn).SetDataScope(role.DataScopeAll).SaveX(ctx)
			key := c.Client().AccessKey.Create().SetTenantID(tn.ID).SetRoleID(bound.ID).SetAccessKey("ak-" + name).SetSecretCiphertext("fixture-not-used-for-signature").SetStatus(accesskey.StatusOn).SaveX(ctx)
			p := c.Client().Permission.Create().SetName(name).SetCode(name).SetStatus(permission.StatusOn).SaveX(ctx)
			route, routeErr := c.Client().Api.Query().Where(api.PathEQ("/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}"), api.MethodEQ("PUT"), api.ModuleEQ("VMService"), api.ScopeEQ(api.ScopeAdmin)).Only(ctx)
			if ent.IsNotFound(routeErr) {
				route = c.Client().Api.Create().SetPath("/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}").SetMethod("PUT").SetModule("VMService").SetBusinessModule(api.BusinessModuleVirtualMachine).SetScope(api.ScopeAdmin).SetStatus(api.StatusOn).SaveX(ctx)
			} else {
				require.NoError(t, routeErr)
			}
			c.Client().PermissionApi.Create().SetPermissionID(p.ID).SetAPIID(route.ID).SaveX(ctx)
			c.Client().RolePermission.Create().SetTenantID(tn.ID).SetRoleID(bound.ID).SetPermissionID(p.ID).SetStatus(rolepermission.StatusOn).SetEffect(rolepermission.EffectAllow).SaveX(ctx)
			principal := &auth.Principal{Type: auth.SubjectAPIKey, ID: key.ID, TenantID: tn.ID}
			path := "/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}"
			switch scenario {
			case "revoked-key":
				c.Client().AccessKey.UpdateOne(key).SetStatus(accesskey.StatusOff).SaveX(ctx)
			case "expired-key":
				c.Client().AccessKey.UpdateOne(key).SetExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
			case "role-off":
				c.Client().Role.UpdateOne(bound).SetStatus(role.StatusOff).SaveX(ctx)
			case "permission-off":
				c.Client().Permission.UpdateOne(p).SetStatus(permission.StatusOff).SaveX(ctx)
			case "foreign-tenant":
				other := c.Client().Tenant.Create().SetCode(name + "-other").SetPlanID(plan.ID).SetStatus(tenant.StatusOn).SaveX(ctx)
				principal.TenantID = other.ID
			case "module-revoked":
				c.Client().PlanModule.DeleteOne(module).ExecX(ctx)
			case "wrong-route":
				path = "/api/v1/virtual-machines/{data.vm_id}/restart"
			}
			err := NewVMAuthorizationRepo(c).AuthorizeNetworkPrincipal(ctx, principal, path, "PUT")
			if scenario == "allowed-without-user-membership" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrActionAuthorizationDenied)
			}
		})
	}
}
