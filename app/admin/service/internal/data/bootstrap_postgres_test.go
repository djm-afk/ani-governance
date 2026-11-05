package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	permissionV1 "go-wind-admin/api/gen/go/permission/service/v1"
	"go-wind-admin/app/admin/service/cmd/server/assets"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/tenant"
	"go-wind-admin/pkg/authorizer"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	entCrud "go-wind-admin/pkg/localdeps/go-crud/entgo"
	"go-wind-admin/pkg/localdeps/go-utils/trans"
	authzEngine "go-wind-admin/pkg/localdeps/kratos-authz/engine"
	conf "go-wind-admin/pkg/localdeps/kratos-bootstrap/api/gen/go/conf/v1"
	bBootstrap "go-wind-admin/pkg/localdeps/kratos-bootstrap/bootstrap"
	bLogger "go-wind-admin/pkg/localdeps/kratos-bootstrap/logger"
	dbbootstrap "go-wind-admin/sql/bootstrap"
)

// The DSN must point to a disposable database ending in _bootstrap_test.
func TestBootstrapPostgres(t *testing.T) {
	dsn := os.Getenv("GOV_BOOTSTRAP_TEST_DSN")
	if dsn == "" {
		t.Skip("GOV_BOOTSTRAP_TEST_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	var name string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name))
	require.True(t, strings.HasSuffix(name, "_bootstrap_test"), "refuse to initialize a non-test database")
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	// Test setup only. The application has no schema migration call.
	require.NoError(t, client.Schema.Create(ctx))
	catalog, err := dbbootstrap.Catalog(assets.OpenApiData)
	require.NoError(t, err)
	require.Error(t, dbbootstrap.Check(ctx, db))
	count := func(table string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n))
		return n
	}
	snapshot := func() string {
		var result string
		for _, table := range []string{"sys_users", "sys_user_credentials", "sys_user_roles", "sys_roles", "sys_role_metadata", "sys_permissions", "sys_permission_groups", "sys_role_permissions", "sys_permission_apis", "sys_permission_menus", "sys_plans", "sys_plan_modules", "sys_configs", "sys_menus", "sys_languages", "sys_apis"} {
			var s string
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COALESCE(json_agg(t ORDER BY id)::text,'[]') FROM "+table+" t").Scan(&s))
			result += s
		}
		return result
	}
	_, err = dbbootstrap.Initialize(ctx, db, catalog, "admin", "weak")
	require.Error(t, err)
	require.Zero(t, count("sys_users"))
	// Omit one API required by 001_initial.sql without changing the original catalog.
	const requiredMethod, requiredPath = "GET", "/api/v1/admin/me"
	originalCatalog := append([]dbbootstrap.API(nil), catalog...)
	incompleteCatalog := make([]dbbootstrap.API, 0, len(catalog))
	missingCount := 0
	for _, api := range catalog {
		if api.Method == requiredMethod && api.Path == requiredPath {
			missingCount++
			continue
		}
		incompleteCatalog = append(incompleteCatalog, api)
	}
	require.Equal(t, 1, missingCount)
	require.Len(t, incompleteCatalog, len(catalog)-1)
	for _, api := range incompleteCatalog {
		require.False(t, api.Method == requiredMethod && api.Path == requiredPath)
	}
	require.Equal(t, originalCatalog, catalog)
	_, err = dbbootstrap.Initialize(ctx, db, incompleteCatalog, "admin", "Valid-bootstrap@2026")
	require.Error(t, err)
	require.ErrorContains(t, err, "API catalog is incomplete")
	require.Zero(t, count("sys_users"))
	require.Zero(t, count("sys_apis"))
	require.Zero(t, count("sys_roles"))
	// An API-only database can still contain out-of-release routes. Do not keep
	// them just because the base fresh-install guard allows an API pre-sync.
	_, err = db.ExecContext(ctx, `INSERT INTO sys_apis(path,method,operation,module,business_module,status,scope) VALUES('/api/v1/out-of-release','GET','Extra_Get','UserService','OPM','ON','ADMIN')`)
	require.NoError(t, err)
	withExtraAPI := snapshot()
	_, err = dbbootstrap.Initialize(ctx, db, catalog, "owner", "Valid-bootstrap@2026")
	require.ErrorContains(t, err, "exactly 482 APIs")
	require.Equal(t, withExtraAPI, snapshot(), "out-of-release API rejection must roll back the whole seed")
	_, err = db.ExecContext(ctx, `DELETE FROM sys_apis WHERE path='/api/v1/out-of-release' AND method='GET'`)
	require.NoError(t, err)
	// Prove that role/user/API bindings do not depend on a sequence beginning at 1.
	_, err = db.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('sys_roles','id'),100,false),setval(pg_get_serial_sequence('sys_users','id'),200,false),setval(pg_get_serial_sequence('sys_permissions','id'),300,false)`)
	require.NoError(t, err)
	created, err := dbbootstrap.Initialize(ctx, db, catalog, "owner", "Valid-bootstrap@2026")
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, dbbootstrap.Check(ctx, db))
	require.Equal(t, 482, count("sys_apis"))
	var vmImageAPIs int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sys_apis WHERE path LIKE '/api/v1/virtual-machine-images%'`).Scan(&vmImageAPIs))
	require.Zero(t, vmImageAPIs, "VM image APIs are intentionally excluded until frontend integration")
	require.Equal(t, 370, count("sys_permissions"))
	require.Equal(t, 49, count("sys_menus"))
	require.Equal(t, 5, count("sys_roles"))
	require.Equal(t, 2, count("sys_plans"))
	require.Zero(t, count("sys_tenants"), "the full plan is a template and must not be assigned")
	require.Zero(t, count("sys_plan_quotas"), "never invent customer resource quotas")
	queryCount := func(query string) int {
		t.Helper()
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query).Scan(&n))
		return n
	}
	require.Equal(t, 19, queryCount(`SELECT count(*) FROM sys_plan_modules pm JOIN sys_plans p ON p.id=pm.plan_id WHERE p.name='完整功能模板'`))
	require.Zero(t, queryCount(`SELECT count(*) FROM sys_user_roles ur JOIN sys_roles r ON r.id=ur.role_id WHERE r.type='TEMPLATE'`), "templates must not be automatically assigned")
	require.Equal(t, 3, queryCount(`SELECT count(*) FROM sys_role_metadata m JOIN sys_roles r ON r.id=m.role_id WHERE r.code IN ('template:platform:operator','template:tenant:developer','template:tenant:viewer') AND r.type='TEMPLATE' AND m.is_template AND m.sync_policy='MANUAL' AND m.template_for=substring(r.code from 10) AND m.scope=CASE WHEN r.code LIKE 'template:platform:%' THEN 'PLATFORM' ELSE 'TENANT' END`))
	require.Zero(t, queryCount(`SELECT count(*) FROM sys_permissions p LEFT JOIN sys_permission_groups g ON g.id=p.group_id WHERE g.id IS NULL OR g.status<>'ON'`))
	require.Equal(t, 10, queryCount(`SELECT count(*) FROM sys_apis a WHERE NOT EXISTS(SELECT 1 FROM sys_permission_apis pa WHERE pa.api_id=a.id)`))
	require.Zero(t, queryCount(`SELECT count(*) FROM sys_apis a WHERE a.path NOT LIKE '/api/v1/auth/%' AND NOT EXISTS(SELECT 1 FROM sys_permission_apis pa WHERE pa.api_id=a.id)`))
	require.Zero(t, queryCount(`SELECT count(*) FROM sys_role_permissions rp JOIN sys_roles r ON r.id=rp.role_id JOIN sys_permission_apis pa ON pa.permission_id=rp.permission_id JOIN sys_apis a ON a.id=pa.api_id WHERE r.code='template:tenant:manager' AND (a.path LIKE '/api/v1/admin/networks/%' OR a.path IN ('/api/v1/admin/apis','/api/v1/admin/permissions','/api/v1/admin/tenants','/api/v1/admin/compute-presets'))`))
	require.Zero(t, queryCount(`SELECT count(*) FROM sys_role_permissions rp JOIN sys_roles r ON r.id=rp.role_id JOIN sys_permission_menus pm ON pm.permission_id=rp.permission_id JOIN sys_menus m ON m.id=pm.menu_id WHERE r.code='template:tenant:manager' AND m.remark LIKE 'boss:%'`))
	require.Equal(t, 1, queryCount(`SELECT count(*) FROM sys_role_permissions rp JOIN sys_roles r ON r.id=rp.role_id JOIN sys_permissions p ON p.id=rp.permission_id WHERE r.code='template:tenant:manager' AND p.code='modeldev:compute-preset:read'`))
	require.Equal(t, 1, queryCount(`SELECT count(*) FROM sys_permission_apis pa JOIN sys_permissions p ON p.id=pa.permission_id JOIN sys_apis a ON a.id=pa.api_id WHERE p.code='network:platform:intranet-pool:auto-allocate' AND a.path='/api/v1/admin/networks/intranet-address-pools/{pool_id}/auto-allocation'`))
	t.Run("template clone and policy chain", func(t *testing.T) {
		bootstrapRolePolicyChain(t, db, client)
	})
	require.Equal(t, 5, count("sys_roles"))
	require.Zero(t, count("sys_tenants"))
	// Fresh seed must grant logout to the existing tenant-manager permission.
	const logoutGrant = `SELECT COUNT(*) FROM sys_permission_apis pa
 JOIN sys_permissions p ON p.id=pa.permission_id JOIN sys_apis a ON a.id=pa.api_id
 WHERE p.code='sys:tenant_manager' AND a.method='POST' AND a.path='/api/v1/auth/logout'`
	var logoutGrants int
	require.NoError(t, db.QueryRowContext(ctx, logoutGrant).Scan(&logoutGrants))
	require.Equal(t, 1, logoutGrants)
	patch, err := os.ReadFile("../../../../../scripts/ops/sql/repair-tenant-logout.sql")
	require.NoError(t, err)
	// Reproduce the old seed omission; the patch may only add this one binding.
	_, err = db.ExecContext(ctx, `DELETE FROM sys_permission_apis WHERE permission_id IN
 (SELECT id FROM sys_permissions WHERE code='sys:tenant_manager') AND api_id IN
 (SELECT id FROM sys_apis WHERE method='POST' AND path='/api/v1/auth/logout')`)
	require.NoError(t, err)
	grantsWithoutLogout := count("sys_permission_apis")
	_, err = db.ExecContext(ctx, string(patch))
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, logoutGrant).Scan(&logoutGrants))
	require.Equal(t, 1, logoutGrants)
	require.Equal(t, grantsWithoutLogout+1, count("sys_permission_apis"))
	afterPatch := snapshot()
	_, err = db.ExecContext(ctx, string(patch))
	require.NoError(t, err)
	require.Equal(t, afterPatch, snapshot(), "repeat patch must preserve all existing rows and IDs")
	// A missing API must abort, without creating any grants or replacement APIs.
	_, err = db.ExecContext(ctx, `UPDATE sys_apis SET path='/test-missing-logout' WHERE path='/api/v1/auth/logout' AND method='POST'`)
	require.NoError(t, err)
	withoutAPI := snapshot()
	_, err = db.ExecContext(ctx, string(patch))
	require.Error(t, err)
	require.Equal(t, withoutAPI, snapshot())
	_, err = db.ExecContext(ctx, `UPDATE sys_apis SET path='/api/v1/auth/logout' WHERE path='/test-missing-logout' AND method='POST'`)
	require.NoError(t, err)
	var hash string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credential FROM sys_user_credentials WHERE identifier='owner'`).Scan(&hash))
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("Valid-bootstrap@2026")))
	before := snapshot()
	created, err = dbbootstrap.Initialize(ctx, db, catalog, "another-owner", "")
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, before, snapshot(), "repeat initialization must not refresh any row")
	// Read-only preflight must work under PostgreSQL's actual read-only enforcement.
	_, err = db.ExecContext(ctx, `SET default_transaction_read_only=on`)
	require.NoError(t, err)
	require.NoError(t, dbbootstrap.Check(ctx, db))
	_, err = db.ExecContext(ctx, `SET default_transaction_read_only=off`)
	require.NoError(t, err)
	require.Equal(t, before, snapshot())
	var apiID int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM sys_apis WHERE path='/api/v1/admin/users' AND method='GET'`).Scan(&apiID))
	grantsBefore := count("sys_permission_apis")
	_, err = db.ExecContext(ctx, `UPDATE sys_apis SET status='OFF',description='old' WHERE id=$1`, apiID)
	require.NoError(t, err)
	newCatalog := append(append([]dbbootstrap.API{}, catalog...), dbbootstrap.API{Path: "/api/v1/new-test-api", Method: "GET", Operation: "Test_New", Module: "UserService", BusinessModule: "OPM"})
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = dbbootstrap.SyncAPIs(ctx, tx, newCatalog, false)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	var actualID int
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id,status FROM sys_apis WHERE path='/api/v1/admin/users' AND method='GET'`).Scan(&actualID, &status))
	require.Equal(t, apiID, actualID)
	require.Equal(t, "OFF", status)
	require.Equal(t, grantsBefore, count("sys_permission_apis"))
	var newGrants int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys_permission_apis pa JOIN sys_apis a ON a.id=pa.api_id WHERE a.path='/api/v1/new-test-api'`).Scan(&newGrants))
	require.Zero(t, newGrants)
	// Existing installations without the new marker must never be reset or claimed.
	_, err = db.ExecContext(ctx, `DELETE FROM sys_configs WHERE key=$1`, dbbootstrap.MarkerKey)
	require.NoError(t, err)
	before = snapshot()
	_, err = dbbootstrap.Initialize(ctx, db, catalog, "owner", "Different-password@2026")
	require.ErrorContains(t, err, "existing installation")
	require.Equal(t, before, snapshot())
}

// Verify the seed through the production template copier, policy provider and
// tenant plan checker. The tenant is a fixture, not an onboarding acceptance.
func bootstrapRolePolicyChain(t *testing.T, db *sql.DB, client *ent.Client) {
	t.Helper()
	ctx := appViewer.NewSystemViewerContext(context.Background())
	db.SetMaxOpenConns(4) // The template reader and its destination transaction use separate connections.
	defer db.SetMaxOpenConns(1)
	ec := entCrud.NewEntClient(client, entsql.OpenDB(dialect.Postgres, db))
	bctx := bBootstrap.NewContextWithParam(ctx, &conf.AppInfo{}, &conf.Bootstrap{Authz: &conf.Authorization{Type: "casbin"}}, bLogger.NopLogger())
	roles := NewRoleRepo(bctx, ec, NewRolePermissionRepo(bctx, ec), NewRoleOrgUnitRepo(bctx, ec),
		NewPermissionRepo(bctx, ec, NewPermissionApiRepo(bctx, ec), NewPermissionMenuRepo(bctx, ec)),
		NewRoleMetadataRepo(bctx, ec), NewRoleFieldPermissionRepo(bctx, ec))
	var planID uint32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM sys_plans WHERE name='完整功能模板'`).Scan(&planID))
	fixture, err := client.Tenant.Create().SetName("Bootstrap policy fixture").SetCode("bootstrap-policy-fixture").SetStatus(tenant.StatusOn).SetPlanID(planID).Save(ctx)
	require.NoError(t, err)
	defer func() {
		_, err := db.ExecContext(ctx, `DELETE FROM sys_tenants WHERE id=$1`, fixture.ID)
		require.NoError(t, err)
	}()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	role, err := roles.CreateTenantRoleFromTemplate(ctx, tx, fixture.ID, 0)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	defer func() {
		for _, query := range []string{`DELETE FROM sys_role_permissions WHERE role_id=$1`, `DELETE FROM sys_role_metadata WHERE role_id=$1`, `DELETE FROM sys_roles WHERE id=$1`} {
			_, err := db.ExecContext(ctx, query, role.GetId())
			require.NoError(t, err)
		}
	}()
	// Apply the stored permission lists through the real role creation path.
	// Template records themselves remain unassignable and never become policies.
	for _, spec := range []struct {
		code        string
		tenantID    uint32
		typ         permissionV1.Role_Type
		permissions int
	}{
		{"platform:operator", 0, permissionV1.Role_SYSTEM, 106},
		{"tenant:developer", fixture.ID, permissionV1.Role_TENANT, 182},
		{"tenant:viewer", fixture.ID, permissionV1.Role_TENANT, 95},
	} {
		template, err := roles.GetTemplateRole(ctx, spec.code)
		require.NoError(t, err)
		require.Len(t, template.GetPermissions(), spec.permissions)
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		created, createErr := roles.CreateWithTx(ctx, tx, &permissionV1.Role{
			Name: trans.Ptr("Bootstrap " + spec.code), Code: trans.Ptr(spec.code),
			TenantId: trans.Ptr(spec.tenantID), Type: spec.typ.Enum(), Status: permissionV1.Role_ON.Enum(),
			Permissions: template.GetPermissions(),
		})
		if createErr != nil {
			require.NoError(t, tx.Rollback())
			require.NoError(t, createErr)
		}
		require.NoError(t, tx.Commit())
		defer func() {
			for _, query := range []string{`DELETE FROM sys_role_permissions WHERE role_id=$1`, `DELETE FROM sys_role_metadata WHERE role_id=$1`, `DELETE FROM sys_roles WHERE id=$1`} {
				_, err := db.ExecContext(ctx, query, created.GetId())
				require.NoError(t, err)
			}
		}()
	}
	policy := authorizer.NewAuthorizer(bctx, NewAuthorizerProvider(bctx, roles, NewApiRepo(bctx, ec)))
	require.NotNil(t, policy.Engine())
	require.NoError(t, policy.ResetPolicies(ctx))
	for _, check := range []struct {
		role, domain, path, method string
		allowed                    bool
	}{
		{"platform:admin", "0", "/api/v1/admin/plans", "GET", true},
		{"platform:admin", "0", "/api/v1/admin/networks/virtual-switches", "POST", true},
		{role.GetCode(), fmt.Sprint(fixture.ID), "/api/v1/virtual-machines", "GET", true},
		{role.GetCode(), fmt.Sprint(fixture.ID), "/api/v1/admin/users", "GET", true},
		{role.GetCode(), fmt.Sprint(fixture.ID), "/api/v1/admin/networks/virtual-switches", "POST", false},
		{role.GetCode(), "0", "/api/v1/virtual-machines", "GET", false},
		{"template:tenant:manager", "0", "/api/v1/virtual-machines", "GET", false},
		{"platform:operator", "0", "/api/v1/admin/networks/virtual-switches", "POST", true},
		{"platform:operator", "0", "/api/v1/admin/roles", "POST", false},
		{"platform:operator", "0", "/api/v1/admin/plans", "POST", false},
		{"tenant:developer", fmt.Sprint(fixture.ID), "/api/v1/virtual-machines", "POST", true},
		{"tenant:developer", fmt.Sprint(fixture.ID), "/api/v1/admin/users", "POST", false},
		{"tenant:developer", fmt.Sprint(fixture.ID), "/api/v1/auth/api-keys", "POST", false},
		{"tenant:viewer", fmt.Sprint(fixture.ID), "/api/v1/virtual-machines", "GET", true},
		{"tenant:viewer", fmt.Sprint(fixture.ID), "/api/v1/virtual-machines", "POST", false},
		{"tenant:viewer", fmt.Sprint(fixture.ID), "/api/v1/inference-keys", "GET", false},
		{"tenant:viewer", fmt.Sprint(fixture.ID), "/api/v1/auth/logout", "POST", true},
		{"tenant:viewer", "0", "/api/v1/virtual-machines", "GET", false},
		{"template:platform:operator", "0", "/api/v1/admin/networks/virtual-switches", "POST", false},
		{"template:tenant:viewer", "0", "/api/v1/virtual-machines", "GET", false},
	} {
		allowed, err := policy.Engine().IsAuthorized(ctx, authzEngine.Subject(check.role), authzEngine.Action(check.method), authzEngine.Resource(check.path), authzEngine.Project(check.domain))
		require.NoError(t, err)
		require.Equal(t, check.allowed, allowed, "%s in %s: %s %s", check.role, check.domain, check.method, check.path)
	}
	checker := NewTenantAccessCheckerImpl(bctx, ec)
	require.NoError(t, checker.CheckTenantAccess(ctx, fixture.ID, "/api/v1/virtual-machines", "GET"))
	_, err = db.ExecContext(ctx, `UPDATE sys_tenants SET plan_id=(SELECT id FROM sys_plans WHERE name='基础管理') WHERE id=$1`, fixture.ID)
	require.NoError(t, err)
	require.Error(t, checker.CheckTenantAccess(ctx, fixture.ID, "/api/v1/virtual-machines", "GET"), "a role grant cannot bypass the selected plan")
}
