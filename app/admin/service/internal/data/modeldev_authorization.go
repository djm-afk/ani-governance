package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/api"
	"go-wind-admin/app/admin/service/internal/data/ent/permission"
	"go-wind-admin/app/admin/service/internal/data/ent/permissionapi"
	"go-wind-admin/app/admin/service/internal/data/ent/role"
	"go-wind-admin/app/admin/service/internal/data/ent/rolepermission"
	"go-wind-admin/app/admin/service/internal/data/ent/tenant"
	"go-wind-admin/app/admin/service/internal/data/ent/user"
	"go-wind-admin/app/admin/service/internal/data/ent/userrole"
	"go-wind-admin/pkg/constants"
	entCrud "go-wind-admin/pkg/localdeps/go-crud/entgo"
)

var (
	ErrModelDevAuthorizationDenied      = errors.New("modeldev create authorization denied")
	ErrModelDevAuthorizationUnavailable = errors.New("modeldev create authorization unavailable")
)

const ModelDevManageEnvironmentPermissionCode = "modeldev:manage_environment"
const ModelDevViewEnvironmentPermissionCode = "modeldev:view_environment"
const ModelDevEnsureEnvironmentPath = "/api/v1/modeldev/environment:ensure"
const ModelDevGetEnvironmentPath = "/api/v1/modeldev/environment"

const ModelDevManageInputPermissionCode = "modeldev:manage_input"
const ModelDevManageExecutionPermissionCode = "modeldev:manage_execution"

type modelDevAuthorizationAction uint8

const (
	modelDevCreateAction modelDevAuthorizationAction = iota + 1
	modelDevManageInputAction
	modelDevGetExecutionAction
	modelDevListArtifactsAction
	modelDevDownloadArtifactAction
	modelDevStopAction
	modelDevListExecutionsAction
	modelDevGetExecutionLogsAction
	modelDevListExecutionPodsAction
	modelDevListExecutionEventsAction
	modelDevExecutionLogStreamAction
	modelDevExecutionMetricsAction
	modelDevListImplementationsAction
	modelDevGetInputVersionAction
	modelDevListInputVersionsAction
	modelDevManageExecutionAction
	modelDevFinetuningExistingModelAction
	modelDevFinetuningNewModelAction
	modelDevResumeRegistrationAction
	modelDevFinetuningArtifactsAction
	modelDevFinetuningReadinessAction
	modelDevEnsureEnvironmentAction
	modelDevGetEnvironmentAction

	modelDevCreateDatasetAction
	modelDevListDatasetsAction
	modelDevGetDatasetAction
	modelDevUpdateDatasetAction
	modelDevArchiveDatasetAction
	modelDevInspectDatasetSourceAction
	modelDevImportDatasetAction
	modelDevListDatasetVersionsAction
	modelDevGetInputVersionPreviewAction
	modelDevListDatasetImportTasksAction
	modelDevGetDatasetImportTaskAction
	modelDevCancelDatasetImportAction
	modelDevRetryDatasetImportAction
	modelDevListDatasetOperationsAction
)

// Dataset capabilities live under the MODELDEV business module: they are
// ModelDev's own entries, not model-repository assets. The two InputVersion
// reads move to dataset:read in one step; the deployed database had them bound
// to modeldev:input:list/get, so the seed carries those grants over first.
const (
	ModelDevDatasetReadPermissionCode    = "modeldev:dataset:read"
	ModelDevDatasetCreatePermissionCode  = "modeldev:dataset:create"
	ModelDevDatasetUpdatePermissionCode  = "modeldev:dataset:update"
	ModelDevDatasetArchivePermissionCode = "modeldev:dataset:archive"
	ModelDevDatasetImportPermissionCode  = "modeldev:dataset:import"
)

const (
	ModelDevGetExecutionPath        = "/api/v1/modeldev/executions/{execution_id}"
	ModelDevListArtifactsPath       = "/api/v1/modeldev/executions/{execution_id}/artifacts"
	ModelDevDownloadArtifactPath    = "/api/v1/modeldev/artifacts/{artifact_id}/content"
	ModelDevListExecutionsPath      = "/api/v1/modeldev/executions"
	ModelDevGetExecutionLogsPath    = "/api/v1/modeldev/executions/{execution_id}/logs"
	ModelDevListExecutionPodsPath   = "/api/v1/modeldev/executions/{execution_id}/pods"
	ModelDevListExecutionEventsPath = "/api/v1/modeldev/executions/{execution_id}/events"
	ModelDevExecutionLogStreamPath  = "/api/v1/modeldev/executions/{execution_id}/logs/stream"
	ModelDevExecutionMetricsPath    = "/api/v1/modeldev/executions/{execution_id}/resource-metrics"
	ModelDevListImplementationsPath = "/api/v1/modeldev/implementations"
	ModelDevGetInputVersionPath     = "/api/v1/modeldev/input-versions/{input_version_id}"
	ModelDevReadinessPath           = "/api/v1/modeldev/finetuning/readiness/check"
	ModelDevListInputVersionsPath   = "/api/v1/modeldev/input-versions"

	ModelDevCreateDatasetPath          = "/api/v1/modeldev/datasets"
	ModelDevListDatasetsPath           = "/api/v1/modeldev/datasets"
	ModelDevGetDatasetPath             = "/api/v1/modeldev/datasets/{dataset_id}"
	ModelDevUpdateDatasetPath          = "/api/v1/modeldev/datasets/{dataset_id}"
	ModelDevArchiveDatasetPath         = "/api/v1/modeldev/datasets/{dataset_id}:archive"
	ModelDevRestoreDatasetPath         = "/api/v1/modeldev/datasets/{dataset_id}:restore"
	ModelDevInspectDatasetSourcePath   = "/api/v1/modeldev/dataset-sources:inspect"
	ModelDevImportDatasetVersionPath   = "/api/v1/modeldev/datasets/{dataset_id}:import"
	ModelDevListDatasetVersionsPath    = "/api/v1/modeldev/datasets/{dataset_id}/versions"
	ModelDevGetInputVersionPreviewPath = "/api/v1/modeldev/input-versions/{input_version_id}/preview"
	ModelDevListDatasetImportTasksPath = "/api/v1/modeldev/datasets/{dataset_id}/import-tasks"
	ModelDevGetDatasetImportTaskPath   = "/api/v1/modeldev/dataset-import-tasks/{import_task_id}"
	ModelDevCancelDatasetImportPath    = "/api/v1/modeldev/dataset-import-tasks/{import_task_id}:cancel"
	ModelDevRetryDatasetImportPath     = "/api/v1/modeldev/dataset-import-tasks/{import_task_id}:retry"
	ModelDevListDatasetOperationsPath  = "/api/v1/modeldev/datasets/{dataset_id}/operations"
)

// ModelDevDatasetAction names one Dataset route. The public dataset paths are
// not unique on their own — POST and GET share /api/v1/modeldev/datasets, and so
// do GET detail and PATCH — so the caller names the action it is serving instead
// of the data layer guessing from a path.
type ModelDevDatasetAction string

const (
	ModelDevDatasetActionCreate          ModelDevDatasetAction = "create"
	ModelDevDatasetActionList            ModelDevDatasetAction = "list"
	ModelDevDatasetActionGet             ModelDevDatasetAction = "get"
	ModelDevDatasetActionUpdate          ModelDevDatasetAction = "update"
	ModelDevDatasetActionArchive         ModelDevDatasetAction = "archive"
	ModelDevDatasetActionInspectSource   ModelDevDatasetAction = "inspect_source"
	ModelDevDatasetActionImport          ModelDevDatasetAction = "import"
	ModelDevDatasetActionListVersions    ModelDevDatasetAction = "list_versions"
	ModelDevDatasetActionGetPreview      ModelDevDatasetAction = "get_preview"
	ModelDevDatasetActionListImportTasks ModelDevDatasetAction = "list_import_tasks"
	ModelDevDatasetActionGetImportTask   ModelDevDatasetAction = "get_import_task"
	ModelDevDatasetActionCancelImport    ModelDevDatasetAction = "cancel_import"
	ModelDevDatasetActionRetryImport     ModelDevDatasetAction = "retry_import"
	ModelDevDatasetActionListOperations  ModelDevDatasetAction = "list_operations"
)

// AuthorizeDataset checks the current grant for one Dataset route: the current
// tenant/user/role/data-scope chain, the dataset capability that governs it and
// the exact catalog API row. It is the only Dataset entry point, so every route
// gets the same checks in the same order.
func (r *ModelDevAuthorizationRepo) AuthorizeDataset(ctx context.Context, tenantID, userID uint32, action ModelDevDatasetAction) error {
	var internal modelDevAuthorizationAction
	switch action {
	case ModelDevDatasetActionCreate:
		internal = modelDevCreateDatasetAction
	case ModelDevDatasetActionList:
		internal = modelDevListDatasetsAction
	case ModelDevDatasetActionGet:
		internal = modelDevGetDatasetAction
	case ModelDevDatasetActionUpdate:
		internal = modelDevUpdateDatasetAction
	case ModelDevDatasetActionArchive:
		internal = modelDevArchiveDatasetAction
	case ModelDevDatasetActionInspectSource:
		internal = modelDevInspectDatasetSourceAction
	case ModelDevDatasetActionImport:
		internal = modelDevImportDatasetAction
	case ModelDevDatasetActionListVersions:
		internal = modelDevListDatasetVersionsAction
	case ModelDevDatasetActionGetPreview:
		internal = modelDevGetInputVersionPreviewAction
	case ModelDevDatasetActionListImportTasks:
		internal = modelDevListDatasetImportTasksAction
	case ModelDevDatasetActionGetImportTask:
		internal = modelDevGetDatasetImportTaskAction
	case ModelDevDatasetActionCancelImport:
		internal = modelDevCancelDatasetImportAction
	case ModelDevDatasetActionRetryImport:
		internal = modelDevRetryDatasetImportAction
	case ModelDevDatasetActionListOperations:
		internal = modelDevListDatasetOperationsAction
	default:
		return ErrModelDevAuthorizationDenied
	}
	return r.authorizeCurrent(ctx, tenantID, userID, internal)
}

// AuthorizeQuery checks the current independent GET grant and ALL data scope.
// Only these fixed route templates can produce downstream query delegation.
func (r *ModelDevAuthorizationRepo) AuthorizeQuery(ctx context.Context, tenantID, userID uint32, path string) error {
	var action modelDevAuthorizationAction
	switch path {
	case ModelDevGetExecutionPath:
		action = modelDevGetExecutionAction
	case ModelDevListArtifactsPath:
		action = modelDevListArtifactsAction
	case ModelDevDownloadArtifactPath:
		action = modelDevDownloadArtifactAction
	case ModelDevListExecutionsPath:
		action = modelDevListExecutionsAction
	case ModelDevGetExecutionLogsPath:
		action = modelDevGetExecutionLogsAction
	case ModelDevListExecutionPodsPath:
		action = modelDevListExecutionPodsAction
	case ModelDevListExecutionEventsPath:
		action = modelDevListExecutionEventsAction
	case ModelDevExecutionLogStreamPath:
		action = modelDevExecutionLogStreamAction
	case ModelDevExecutionMetricsPath:
		action = modelDevExecutionMetricsAction
	case ModelDevListImplementationsPath:
		action = modelDevListImplementationsAction
	case ModelDevGetInputVersionPath:
		action = modelDevGetInputVersionAction
	case ModelDevListInputVersionsPath:
		action = modelDevListInputVersionsAction
	case ModelDevReadinessPath:
		if err := r.authorizeCurrent(ctx, tenantID, userID, modelDevFinetuningReadinessAction); err != nil {
			return err
		}
		// Reuse the existing TRAINING-specific code and MODELDEV entitlement.
		// The shared compute directory API itself belongs to SYSTEM.
		err := NewComputeCatalogAuthorizationRepo(r.entClient).AuthorizeComputeCatalog(ctx, tenantID, userID, "TRAINING")
		switch {
		case errors.Is(err, ErrActionAuthorizationDenied):
			return ErrModelDevAuthorizationDenied
		case errors.Is(err, ErrActionAuthorizationUnavailable):
			return ErrModelDevAuthorizationUnavailable
		default:
			return err
		}
	default:
		return ErrModelDevAuthorizationDenied
	}
	return r.authorizeCurrent(ctx, tenantID, userID, action)
}

// ModelDevAuthorizationRepo checks current database grants for the fixed
// CPU-P01 create and input management actions. It does not
// authenticate a user or enable assets. Create's HTTP authentication,
// TenantAccess and Casbin checks remain required before AuthorizeCreate.
type ModelDevAuthorizationRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
}

func NewModelDevAuthorizationRepo(client *entCrud.EntClient[*ent.Client]) *ModelDevAuthorizationRepo {
	return &ModelDevAuthorizationRepo{entClient: client}
}

// AuthorizeEnvironment always reads current action grants, including retries.
func (r *ModelDevAuthorizationRepo) AuthorizeEnvironment(ctx context.Context, tenantID, userID uint32, ensure bool) error {
	action := modelDevGetEnvironmentAction
	if ensure {
		action = modelDevEnsureEnvironmentAction
	}
	return r.authorizeCurrent(ctx, tenantID, userID, action)
}

// AuthorizeCreate requires trusted tenant/user IDs from the verified Principal.
// It must run before FindAccepted for both new requests and original-key replay.
func (r *ModelDevAuthorizationRepo) AuthorizeCreate(ctx context.Context, tenantID, userID uint32) error {
	return r.authorizeCurrent(ctx, tenantID, userID, modelDevCreateAction)
}

const ModelDevStopExecutionPath = "/api/v1/modeldev/executions/{execution_id}:stop"

func (r *ModelDevAuthorizationRepo) AuthorizeStop(ctx context.Context, tenantID, userID uint32) error {
	return r.authorizeCurrent(ctx, tenantID, userID, modelDevStopAction)
}

// AuthorizeManageInput requires trusted tenant/user IDs from the
// verified Principal and the current, separate management permission. A create
// API grant or a JWT's cached roles cannot authorize this action.
func (r *ModelDevAuthorizationRepo) AuthorizeManageInput(ctx context.Context, tenantID, userID uint32) error {
	return r.authorizeCurrent(ctx, tenantID, userID, modelDevManageInputAction)
}

func (r *ModelDevAuthorizationRepo) AuthorizeManageExecution(ctx context.Context, tenantID, userID uint32) error {
	return r.authorizeCurrent(ctx, tenantID, userID, modelDevManageExecutionAction)
}

func (r *ModelDevAuthorizationRepo) AuthorizeResume(ctx context.Context, tenantID, userID uint32) error {
	if err := r.authorizeCurrent(ctx, tenantID, userID, modelDevResumeRegistrationAction); err != nil {
		return err
	}
	return r.AuthorizeQuery(ctx, tenantID, userID, ModelDevGetExecutionPath)
}

// AuthorizeFinetuning complements the current ModelDev create grant with the
// existing Model API grants. A trusted ModelDev service identity cannot grant
// its submitting user permission to read model bytes or register a version.
func (r *ModelDevAuthorizationRepo) AuthorizeFinetuning(ctx context.Context, tenantID, userID uint32, newModel bool) error {
	action := modelDevFinetuningExistingModelAction
	if newModel {
		action = modelDevFinetuningNewModelAction
	}
	return r.authorizeCurrent(ctx, tenantID, userID, action)
}

// AuthorizeFinetuningArtifacts requires current access to the real base model
// bytes without granting any target-model write authority.
func (r *ModelDevAuthorizationRepo) AuthorizeFinetuningArtifacts(ctx context.Context, tenantID, userID uint32) error {
	return r.authorizeCurrent(ctx, tenantID, userID, modelDevFinetuningArtifactsAction)
}

func (r *ModelDevAuthorizationRepo) authorizeCurrent(ctx context.Context, tenantID, userID uint32, action modelDevAuthorizationAction) (result error) {
	// Cancellation remains distinguishable from a denied grant or failed store,
	// including cancellation during transaction completion or cleanup.
	defer func() {
		if err := ctx.Err(); err != nil {
			result = err
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenantID == 0 || userID == 0 {
		return ErrModelDevAuthorizationDenied
	}
	if r == nil || r.entClient == nil || r.entClient.Client() == nil {
		return ErrModelDevAuthorizationUnavailable
	}
	tx, err := r.entClient.Client().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			result = ErrModelDevAuthorizationUnavailable
		}
	}()

	active, err := tx.Tenant.Query().Where(
		tenant.IDEQ(tenantID), tenant.StatusEQ(tenant.StatusOn), tenant.DeletedAtIsNil(),
	).Exist(ctx)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	if !active {
		return ErrModelDevAuthorizationDenied
	}
	active, err = tx.User.Query().Where(
		user.IDEQ(userID), user.TenantIDEQ(tenantID), user.StatusEQ(user.StatusNormal), user.DeletedAtIsNil(),
	).Exist(ctx)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	if !active {
		return ErrModelDevAuthorizationDenied
	}

	// The current OneToOne tenant model uses UserRole, not Membership. All
	// membership times and subsequent grants are evaluated in this RR snapshot.
	now := time.Now().UTC()
	var memberRoleIDs []uint32
	err = tx.UserRole.Query().Where(
		userrole.TenantIDEQ(tenantID), userrole.UserIDEQ(userID), userrole.RoleIDGT(0),
		userrole.StatusEQ(userrole.StatusActive), userrole.DeletedAtIsNil(),
		userrole.Or(userrole.StartAtIsNil(), userrole.StartAtLTE(now)),
		userrole.Or(userrole.EndAtIsNil(), userrole.EndAtGT(now)),
	).Select(userrole.FieldRoleID).Scan(ctx, &memberRoleIDs)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	if len(memberRoleIDs) == 0 {
		return ErrModelDevAuthorizationDenied
	}
	roles, err := tx.Role.Query().Where(
		role.IDIn(memberRoleIDs...), role.TenantIDEQ(tenantID), role.StatusEQ(role.StatusOn),
		role.TypeEQ(role.TypeTenant), role.CodeNEQ(""), role.DeletedAtIsNil(),
		role.Not(role.CodeHasPrefix(constants.TemplateRoleCodePrefix)),
	).All(ctx)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	roleIDs := make([]uint32, 0, len(roles))
	hasAll := false
	for _, current := range roles {
		roleIDs = append(roleIDs, current.ID)
		if current.DataScope != nil && *current.DataScope == role.DataScopeAll {
			hasAll = true
		}
	}
	// Existing data scopes form a union: one effective ALL grants the tenant
	// range. A separate current action grant is still mandatory below.
	if !hasAll {
		return ErrModelDevAuthorizationDenied
	}

	var permissionIDs []uint32
	err = tx.RolePermission.Query().Where(
		rolepermission.TenantIDEQ(tenantID), rolepermission.RoleIDIn(roleIDs...),
		rolepermission.PermissionIDGT(0), rolepermission.StatusEQ(rolepermission.StatusOn),
		rolepermission.EffectEQ(rolepermission.EffectAllow), rolepermission.DeletedAtIsNil(),
	).Select(rolepermission.FieldPermissionID).Scan(ctx, &permissionIDs)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	if len(permissionIDs) == 0 {
		return ErrModelDevAuthorizationDenied
	}
	permissionQuery := tx.Permission.Query().Where(
		permission.IDIn(permissionIDs...), permission.StatusEQ(permission.StatusOn), permission.DeletedAtIsNil(),
	)
	switch action {
	case modelDevCreateAction, modelDevGetExecutionAction, modelDevListArtifactsAction, modelDevDownloadArtifactAction, modelDevStopAction, modelDevListExecutionsAction, modelDevGetExecutionLogsAction, modelDevListExecutionPodsAction, modelDevListExecutionEventsAction, modelDevExecutionLogStreamAction, modelDevExecutionMetricsAction, modelDevListImplementationsAction, modelDevFinetuningExistingModelAction, modelDevFinetuningNewModelAction, modelDevFinetuningArtifactsAction, modelDevFinetuningReadinessAction:
		// Create retains its permission-to-API check below.
	case modelDevEnsureEnvironmentAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevManageEnvironmentPermissionCode))
	case modelDevGetEnvironmentAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevViewEnvironmentPermissionCode))
	case modelDevManageInputAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevManageInputPermissionCode))
	case modelDevManageExecutionAction, modelDevResumeRegistrationAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevManageExecutionPermissionCode))
	// Asset reads, including the two InputVersion routes that used to be bound
	// to modeldev:input:list/get. They share one capability, so a role granted
	// dataset:read can read either and nothing else follows from it.
	case modelDevGetInputVersionAction, modelDevListInputVersionsAction,
		modelDevListDatasetsAction, modelDevGetDatasetAction, modelDevListDatasetVersionsAction,
		modelDevGetInputVersionPreviewAction, modelDevListDatasetImportTasksAction,
		modelDevGetDatasetImportTaskAction, modelDevListDatasetOperationsAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevDatasetReadPermissionCode))
	case modelDevCreateDatasetAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevDatasetCreatePermissionCode))
	case modelDevUpdateDatasetAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevDatasetUpdatePermissionCode))
	case modelDevArchiveDatasetAction:
		permissionQuery.Where(permission.CodeEQ(ModelDevDatasetArchivePermissionCode))
	case modelDevInspectDatasetSourceAction, modelDevImportDatasetAction,
		modelDevCancelDatasetImportAction, modelDevRetryDatasetImportAction:
		// Source inspection is a read that only precedes an import, so it needs
		// the import capability: a caller who may not import must not be able to
		// enumerate a repository through it.
		permissionQuery.Where(permission.CodeEQ(ModelDevDatasetImportPermissionCode))
	default:
		return ErrModelDevAuthorizationDenied
	}
	permissionIDs, err = permissionQuery.IDs(ctx)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	if len(permissionIDs) == 0 {
		return ErrModelDevAuthorizationDenied
	}
	if action != modelDevManageInputAction && action != modelDevManageExecutionAction {
		path, method := "/api/v1/modeldev/executions", "POST"
		// 模型开发自身入口按 MODELDEV 套餐资格校验；引用模型仓库资产时按 MODEL 校验。
		expectedModule := api.BusinessModuleModelDev
		switch action {
		case modelDevEnsureEnvironmentAction:
			path, method = ModelDevEnsureEnvironmentPath, "POST"
		case modelDevGetEnvironmentAction:
			path, method = ModelDevGetEnvironmentPath, "GET"
		case modelDevFinetuningReadinessAction:
			for _, required := range []string{"/api/v1/model-versions/{model_version_id}", "/api/v1/model-versions/{model_version_id}/download-url"} {
				if err := authorizeModelDevAPI(ctx, tx, permissionIDs, required, "GET", api.BusinessModuleModel); err != nil {
					return err
				}
			}
			path = ModelDevReadinessPath
		case modelDevResumeRegistrationAction:
			path = ModelDevResumeModelRegistrationPath
		case modelDevStopAction:
			path = ModelDevStopExecutionPath
		case modelDevGetExecutionAction:
			path, method = ModelDevGetExecutionPath, "GET"
		case modelDevListArtifactsAction:
			path, method = ModelDevListArtifactsPath, "GET"
		case modelDevDownloadArtifactAction:
			path, method = ModelDevDownloadArtifactPath, "GET"
		case modelDevListExecutionsAction:
			path, method = ModelDevListExecutionsPath, "GET"
		case modelDevGetExecutionLogsAction:
			path, method = ModelDevGetExecutionLogsPath, "GET"
		case modelDevListExecutionPodsAction:
			path, method = ModelDevListExecutionPodsPath, "GET"
		case modelDevListExecutionEventsAction:
			path, method = ModelDevListExecutionEventsPath, "GET"
		case modelDevExecutionLogStreamAction:
			path, method = ModelDevExecutionLogStreamPath, "GET"
		case modelDevExecutionMetricsAction:
			path, method = ModelDevExecutionMetricsPath, "GET"
		case modelDevListImplementationsAction:
			path, method = ModelDevListImplementationsPath, "GET"
		case modelDevGetInputVersionAction:
			path, method = ModelDevGetInputVersionPath, "GET"
		case modelDevListInputVersionsAction:
			path, method = ModelDevListInputVersionsPath, "GET"
		case modelDevCreateDatasetAction:
			path, method = ModelDevCreateDatasetPath, "POST"
		case modelDevListDatasetsAction:
			path, method = ModelDevListDatasetsPath, "GET"
		case modelDevGetDatasetAction:
			path, method = ModelDevGetDatasetPath, "GET"
		case modelDevUpdateDatasetAction:
			path, method = ModelDevUpdateDatasetPath, "PATCH"
		case modelDevArchiveDatasetAction:
			path, method = ModelDevArchiveDatasetPath, "POST"
		case modelDevInspectDatasetSourceAction:
			path, method = ModelDevInspectDatasetSourcePath, "POST"
		case modelDevImportDatasetAction:
			path, method = ModelDevImportDatasetVersionPath, "POST"
		case modelDevListDatasetVersionsAction:
			path, method = ModelDevListDatasetVersionsPath, "GET"
		case modelDevGetInputVersionPreviewAction:
			path, method = ModelDevGetInputVersionPreviewPath, "GET"
		case modelDevListDatasetImportTasksAction:
			path, method = ModelDevListDatasetImportTasksPath, "GET"
		case modelDevGetDatasetImportTaskAction:
			path, method = ModelDevGetDatasetImportTaskPath, "GET"
		case modelDevCancelDatasetImportAction:
			path, method = ModelDevCancelDatasetImportPath, "POST"
		case modelDevRetryDatasetImportAction:
			path, method = ModelDevRetryDatasetImportPath, "POST"
		case modelDevListDatasetOperationsAction:
			path, method = ModelDevListDatasetOperationsPath, "GET"
		case modelDevFinetuningArtifactsAction:
			if err := authorizeModelDevAPI(ctx, tx, permissionIDs, "/api/v1/model-versions/{model_version_id}", "GET", api.BusinessModuleModel); err != nil {
				return err
			}
			path, method = "/api/v1/model-versions/{model_version_id}/download-url", "GET"
			expectedModule = api.BusinessModuleModel
		case modelDevFinetuningExistingModelAction, modelDevFinetuningNewModelAction:
			for _, required := range []struct{ path, method string }{
				{"/api/v1/model-versions/{model_version_id}", "GET"},
				{"/api/v1/model-versions/{model_version_id}/download-url", "GET"},
				{"/api/v1/model-versions", "POST"},
			} {
				if err := authorizeModelDevAPI(ctx, tx, permissionIDs, required.path, required.method, api.BusinessModuleModel); err != nil {
					return err
				}
			}
			if action == modelDevFinetuningNewModelAction {
				path, method = "/api/v1/models", "POST"
			} else {
				path, method = "/api/v1/models/{model_id}", "GET"
			}
			expectedModule = api.BusinessModuleModel
		}
		if err := authorizeModelDevAPI(ctx, tx, permissionIDs, path, method, expectedModule); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	return nil
}

func authorizeModelDevAPI(ctx context.Context, tx *ent.Tx, permissionIDs []uint32, path, method string, businessModule api.BusinessModule) error {
	var apiIDs []uint32
	err := tx.PermissionApi.Query().Where(
		permissionapi.PermissionIDIn(permissionIDs...), permissionapi.APIIDGT(0), permissionapi.DeletedAtIsNil(),
	).Select(permissionapi.FieldAPIID).Scan(ctx, &apiIDs)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	if len(apiIDs) == 0 {
		return ErrModelDevAuthorizationDenied
	}
	allowed, err := tx.Api.Query().Where(
		api.IDIn(apiIDs...), api.StatusEQ(api.StatusOn), api.DeletedAtIsNil(),
		api.ScopeEQ(api.ScopeAdmin), api.BusinessModuleEQ(businessModule),
		api.PathEQ(path), api.MethodEQ(method),
	).Exist(ctx)
	if err != nil {
		return ErrModelDevAuthorizationUnavailable
	}
	if !allowed {
		return ErrModelDevAuthorizationDenied
	}
	return nil
}
