package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/go-kratos/kratos/v2/errors"
	modeldevcontractv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	modeldevv1 "go-wind-admin/api/gen/go/modeldev/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/pkg/middleware/auth"
)

// Dataset HTTP coupling: every method performs the same three steps in the same
// order — resolve the caller's current grant for this exact route, validate the
// public shape, then delegate one owner RPC. The owner re-checks the delegated
// method and the resource's tenant, so neither side trusts the other's words.
//
// Identity always comes from the verified principal; no dataset field can
// supply a tenant, actor, owner or scope.

// Dataset create update archive import permission actions are the five
// modeldev:dataset:* codes. They are checked through AuthorizeQuery because
// every Dataset delegation, writes included, is delivered as a current
// per-request authorization.

// modelDevDatasetScope resolves the caller's identity and current grant for one
// Dataset route. It mirrors modelDevQueryScope but asks for the dataset action
// instead of a path, because the public dataset paths are not unique on their
// own. Order matters and matches CreateExecution: identity, then the tenant
// user gate, then the maintenance gate for entries that start new work, then
// the current database grant, then the trusted tenant mapping. Putting the gate
// after identity keeps a paused deployment from telling an unauthenticated
// caller anything, and putting it before the grant keeps a paused deployment
// from spending a database transaction per refused request.
func (s *ModelDevService) modelDevDatasetScope(ctx context.Context, action data.ModelDevDatasetAction, gated bool) (data.ModelDevResolveScope, func(), error) {
	principal, err := auth.PrincipalFromContext(ctx)
	if err != nil || principal == nil || principal.ID == 0 {
		return data.ModelDevResolveScope{}, nil, errors.Unauthorized("INVALID_LOGIN", "user login required")
	}
	if principal.Type != auth.SubjectUser || principal.TenantID == 0 {
		return data.ModelDevResolveScope{}, nil, errors.Forbidden("FORBIDDEN", "tenant user required")
	}
	// Same order as CreateExecution: identity, then the maintenance gate, then
	// the wiring and grant checks. A paused deployment must refuse new work
	// without spending a database transaction, and it must refuse it as a
	// pause rather than as a generic dependency failure.
	finish := func() {}
	if gated && s != nil && s.createMaintenance != nil {
		release, allowed := s.createMaintenance.begin()
		if !allowed {
			return data.ModelDevResolveScope{}, nil, modelDevDatasetPaused()
		}
		finish = release
	}
	if s == nil || s.authorization == nil || s.tenants == nil || s.resolver == nil {
		finish()
		return data.ModelDevResolveScope{}, nil, modelDevQueryFailure(nil)
	}
	if err := s.authorization.AuthorizeDataset(ctx, principal.TenantID, principal.ID, action); err != nil {
		finish()
		return data.ModelDevResolveScope{}, nil, modelDevQueryFailure(err)
	}
	tenant, err := s.tenants.ResourceTenantID(ctx, principal.TenantID)
	if err != nil || !modelDevQueryUUID(tenant) {
		finish()
		return data.ModelDevResolveScope{}, nil, modelDevQueryFailure(err)
	}
	actor, err := principal.Actor()
	if err != nil {
		finish()
		return data.ModelDevResolveScope{}, nil, errors.Forbidden("FORBIDDEN", "tenant user required")
	}
	return data.ModelDevResolveScope{ResourceTenantID: tenant, Actor: actor}, finish, nil
}

func (s *ModelDevService) CreateDataset(ctx context.Context, in *modeldevv1.CreateDatasetRequest) (*modeldevv1.CreateDatasetResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionCreate, true)
	if err != nil {
		return nil, err
	}
	defer finish()
	name, description, key, ok := modelDevDatasetCreateInput(in)
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.CreateDataset(ctx, scope, &modeldevcontractv1.CreateDatasetRequest{
		IdempotencyKey: key, Name: name, Description: description,
	})
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.CreateDatasetResponse{Dataset: modelDevDatasetView(out.Dataset), Receipt: modelDevDatasetReceiptView(out.Receipt)}, nil
}

func (s *ModelDevService) GetDataset(ctx context.Context, in *modeldevv1.GetDatasetRequest) (*modeldevv1.GetDatasetResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionGet, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.DatasetId) {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.GetDataset(ctx, scope, in.DatasetId)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.GetDatasetResponse{Dataset: modelDevDatasetView(out.Dataset)}, nil
}

func (s *ModelDevService) ListDatasets(ctx context.Context, in *modeldevv1.ListDatasetsRequest) (*modeldevv1.ListDatasetsResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionList, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil {
		return nil, modelDevDatasetInvalid()
	}
	page, pageSize, err := resolveModelDevPage(in.Page, in.PageSize)
	if err != nil {
		return nil, err
	}
	state, name, ok := modelDevDatasetListFilter(in.State, in.Name)
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.ListDatasets(ctx, scope, page, pageSize, state, name)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	reply := &modeldevv1.ListDatasetsResponse{Total: uint64(out.Total)}
	for _, item := range out.Items {
		reply.Items = append(reply.Items, modelDevDatasetView(item))
	}
	return reply, nil
}

func (s *ModelDevService) UpdateDataset(ctx context.Context, in *modeldevv1.UpdateDatasetRequest) (*modeldevv1.UpdateDatasetResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionUpdate, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	name, description, expected, key, ok := modelDevDatasetUpdateInput(in)
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.UpdateDataset(ctx, scope, &modeldevcontractv1.UpdateDatasetRequest{
		IdempotencyKey: key, DatasetId: in.DatasetId, Name: name, Description: description, ExpectedRevision: expected,
	})
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.UpdateDatasetResponse{Dataset: modelDevDatasetView(out.Dataset), Receipt: modelDevDatasetReceiptView(out.Receipt)}, nil
}

func (s *ModelDevService) ArchiveDataset(ctx context.Context, in *modeldevv1.ArchiveDatasetRequest) (*modeldevv1.ArchiveDatasetResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionArchive, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	id, expected, key, ok := modelDevDatasetLifecycleInput(in.GetDatasetId(), in.GetIdempotencyKey(), in.GetData())
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.ArchiveDataset(ctx, scope, &modeldevcontractv1.ArchiveDatasetRequest{IdempotencyKey: key, DatasetId: id, ExpectedRevision: expected})
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.ArchiveDatasetResponse{Dataset: modelDevDatasetView(out.Dataset), Receipt: modelDevDatasetReceiptView(out.Receipt)}, nil
}

func (s *ModelDevService) RestoreDataset(ctx context.Context, in *modeldevv1.RestoreDatasetRequest) (*modeldevv1.RestoreDatasetResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionArchive, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	id, expected, key, ok := modelDevDatasetLifecycleInput(in.GetDatasetId(), in.GetIdempotencyKey(), in.GetData())
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.RestoreDataset(ctx, scope, &modeldevcontractv1.RestoreDatasetRequest{IdempotencyKey: key, DatasetId: id, ExpectedRevision: expected})
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.RestoreDatasetResponse{Dataset: modelDevDatasetView(out.Dataset), Receipt: modelDevDatasetReceiptView(out.Receipt)}, nil
}

// InspectDatasetSource is read-only even though it is a POST, so it carries no
// write idempotency key. Its two request shapes are mutually exclusive and an
// unknown mode is rejected rather than guessed from which fields are present.
func (s *ModelDevService) InspectDatasetSource(ctx context.Context, in *modeldevv1.InspectDatasetSourceRequest) (*modeldevv1.InspectDatasetSourceResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionInspectSource, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	request, ok := modelDevInspectSourceRequest(in)
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.InspectDatasetSource(ctx, scope, request)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return modelDevInspectSourceResponse(out), nil
}

func (s *ModelDevService) ImportDatasetVersion(ctx context.Context, in *modeldevv1.ImportDatasetVersionRequest) (*modeldevv1.ImportDatasetVersionResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionImport, true)
	if err != nil {
		return nil, err
	}
	defer finish()
	request, ok := modelDevImportRequest(in)
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.ImportDatasetVersion(ctx, scope, request)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.ImportDatasetVersionResponse{Receipt: modelDevDatasetReceiptView(out.Receipt), Task: modelDevDatasetTaskView(out.Task)}, nil
}

func (s *ModelDevService) CancelDatasetImport(ctx context.Context, in *modeldevv1.CancelDatasetImportRequest) (*modeldevv1.CancelDatasetImportResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionCancelImport, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.ImportTaskId) || !modelDevDatasetKey(in.IdempotencyKey) {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.CancelDatasetImport(ctx, scope, &modeldevcontractv1.CancelDatasetImportRequest{IdempotencyKey: in.IdempotencyKey, ImportTaskId: in.ImportTaskId})
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.CancelDatasetImportResponse{Receipt: modelDevDatasetReceiptView(out.Receipt)}, nil
}

func (s *ModelDevService) RetryDatasetImport(ctx context.Context, in *modeldevv1.RetryDatasetImportRequest) (*modeldevv1.RetryDatasetImportResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionRetryImport, true)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.ImportTaskId) || !modelDevDatasetKey(in.IdempotencyKey) {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.RetryDatasetImport(ctx, scope, &modeldevcontractv1.RetryDatasetImportRequest{IdempotencyKey: in.IdempotencyKey, ImportTaskId: in.ImportTaskId})
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.RetryDatasetImportResponse{Receipt: modelDevDatasetReceiptView(out.Receipt)}, nil
}

func (s *ModelDevService) ListDatasetVersions(ctx context.Context, in *modeldevv1.ListDatasetVersionsRequest) (*modeldevv1.ListDatasetVersionsResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionListVersions, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.DatasetId) {
		return nil, modelDevDatasetInvalid()
	}
	page, pageSize, err := resolveModelDevPage(in.Page, in.PageSize)
	if err != nil {
		return nil, err
	}
	out, err := s.resolver.ListDatasetVersions(ctx, scope, in.DatasetId, page, pageSize)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	reply := &modeldevv1.ListDatasetVersionsResponse{Total: uint64(out.Total)}
	for _, item := range out.Items {
		reply.Items = append(reply.Items, modelDevDatasetVersionView(item))
	}
	return reply, nil
}

// GetInputVersionPreview keeps the existing input-version read path: it is the
// same asset read as GET input-versions/{id}, so it also requires dataset:read.
func (s *ModelDevService) GetInputVersionPreview(ctx context.Context, in *modeldevv1.GetInputVersionPreviewRequest) (*modeldevv1.GetInputVersionPreviewResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionGetPreview, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.InputVersionId) {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.GetInputVersionPreview(ctx, scope, in.InputVersionId)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.GetInputVersionPreviewResponse{Preview: modelDevDatasetPreviewView(out)}, nil
}

func (s *ModelDevService) ListDatasetImportTasks(ctx context.Context, in *modeldevv1.ListDatasetImportTasksRequest) (*modeldevv1.ListDatasetImportTasksResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionListImportTasks, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.DatasetId) {
		return nil, modelDevDatasetInvalid()
	}
	page, pageSize, err := resolveModelDevPage(in.Page, in.PageSize)
	if err != nil {
		return nil, err
	}
	state, ok := modelDevDatasetStateFilter(in.State)
	if !ok {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.ListDatasetImportTasks(ctx, scope, in.DatasetId, page, pageSize, state)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	reply := &modeldevv1.ListDatasetImportTasksResponse{Total: uint64(out.Total)}
	for _, item := range out.Items {
		reply.Items = append(reply.Items, modelDevDatasetTaskView(item))
	}
	return reply, nil
}

func (s *ModelDevService) GetDatasetImportTask(ctx context.Context, in *modeldevv1.GetDatasetImportTaskRequest) (*modeldevv1.GetDatasetImportTaskResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionGetImportTask, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.ImportTaskId) {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.GetDatasetImportTask(ctx, scope, in.ImportTaskId)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	return &modeldevv1.GetDatasetImportTaskResponse{Task: modelDevDatasetTaskView(out.Task)}, nil
}

func (s *ModelDevService) ListDatasetOperations(ctx context.Context, in *modeldevv1.ListDatasetOperationsRequest) (*modeldevv1.ListDatasetOperationsResponse, error) {
	scope, finish, err := s.modelDevDatasetScope(ctx, data.ModelDevDatasetActionListOperations, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	if in == nil || !modelDevQueryUUID(in.DatasetId) {
		return nil, modelDevDatasetInvalid()
	}
	page, pageSize, err := resolveModelDevPage(in.Page, in.PageSize)
	if err != nil {
		return nil, err
	}
	eventType, eventOK := modelDevDatasetOperationTypeFilter(in.EventType)
	if !eventOK || (in.ImportTaskId != nil && !modelDevQueryUUID(in.GetImportTaskId())) {
		return nil, modelDevDatasetInvalid()
	}
	out, err := s.resolver.ListDatasetOperations(ctx, scope, in.DatasetId, page, pageSize, eventType, in.ImportTaskId)
	if err != nil {
		return nil, modelDevDatasetFailure(err)
	}
	reply := &modeldevv1.ListDatasetOperationsResponse{Total: uint64(out.Total)}
	for _, item := range out.Items {
		reply.Items = append(reply.Items, modelDevDatasetOperationView(item))
	}
	return reply, nil
}

// ---- request validation ----

func modelDevDatasetInvalid() error {
	return errors.BadRequest("INVALID_DATASET_REQUEST", "invalid dataset request")
}

// modelDevDatasetPaused is the shared maintenance refusal. It is 503 rather
// than a business error: the request is well-formed and authorized, the
// deployment is simply not accepting new work.
func modelDevDatasetPaused() error {
	return errors.ServiceUnavailable("MODELDEV_MAINTENANCE", "modeldev dataset writes are paused for maintenance")
}

// modelDevDatasetKey mirrors the owner's idempotency-key alphabet: it starts
// with an alphanumeric and continues with alphanumerics or . _ : - . Rejecting
// anything else here keeps the two layers from disagreeing about one key.
func modelDevDatasetKey(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == ':' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// modelDevDatasetText accepts plain text up to maxRunes without control
// characters; allowNewlines is set only for the description.
func modelDevDatasetText(value string, maxRunes int, allowNewlines bool) bool {
	if value == "" || len([]rune(value)) > maxRunes || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if allowNewlines && (r == '\n' || r == '\r') {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func modelDevDatasetCreateInput(in *modeldevv1.CreateDatasetRequest) (name, description, key string, ok bool) {
	if in == nil || in.Data == nil || !modelDevDatasetKey(in.IdempotencyKey) || !modelDevDatasetText(in.Data.Name, 128, false) {
		return "", "", "", false
	}
	if in.Data.Description != nil && !modelDevDatasetText(in.GetData().GetDescription(), 4000, true) {
		return "", "", "", false
	}
	return in.Data.Name, in.GetData().GetDescription(), in.IdempotencyKey, true
}

// modelDevDatasetUpdateInput accepts only the two editable fields and requires
// the mask to name exactly them, so a client cannot ask for a field that has no
// public setter and then be told the update succeeded.
func modelDevDatasetUpdateInput(in *modeldevv1.UpdateDatasetRequest) (name, description *string, expected int64, key string, ok bool) {
	if in == nil || in.Data == nil || !modelDevQueryUUID(in.DatasetId) || !modelDevDatasetKey(in.IdempotencyKey) {
		return nil, nil, 0, "", false
	}
	expected, err := strconv.ParseInt(in.Data.ExpectedRevision, 10, 64)
	if err != nil || expected <= 0 || strconv.FormatInt(expected, 10) != in.Data.ExpectedRevision {
		return nil, nil, 0, "", false
	}
	fields := map[string]bool{}
	var entries []string
	if in.UpdateMask != "" {
		entries = strings.Split(in.UpdateMask, ",")
	}
	// Reject a repeated field before collapsing the mask: "name,name" names one
	// field twice, which is a malformed mask even though the set has one entry.
	if len(entries) == 0 || len(entries) > 2 {
		return nil, nil, 0, "", false
	}
	for _, field := range entries {
		if fields[field] {
			return nil, nil, 0, "", false
		}
		fields[field] = true
		if field != "name" && field != "description" {
			return nil, nil, 0, "", false
		}
	}
	if fields["name"] == (in.Data.Name == nil) || fields["description"] == (in.Data.Description == nil) {
		return nil, nil, 0, "", false
	}
	if in.Data.Name != nil {
		if !modelDevDatasetText(in.GetData().GetName(), 128, false) {
			return nil, nil, 0, "", false
		}
		name = in.Data.Name
	}
	if in.Data.Description != nil {
		if !modelDevDatasetText(in.GetData().GetDescription(), 4000, true) {
			return nil, nil, 0, "", false
		}
		description = in.Data.Description
	}
	return name, description, expected, in.IdempotencyKey, true
}

func modelDevDatasetLifecycleInput(datasetID, key string, data *modeldevv1.DatasetRevisionData) (string, int64, string, bool) {
	if !modelDevQueryUUID(datasetID) || !modelDevDatasetKey(key) || data == nil {
		return "", 0, "", false
	}
	expected, err := strconv.ParseInt(data.ExpectedRevision, 10, 64)
	if err != nil || expected <= 0 || strconv.FormatInt(expected, 10) != data.ExpectedRevision {
		return "", 0, "", false
	}
	return datasetID, expected, key, true
}

func modelDevDatasetListFilter(state, name *string) (*string, *string, bool) {
	if state != nil && *state != "ACTIVE" && *state != "ARCHIVED" && *state != "ALL" {
		return nil, nil, false
	}
	filteredState := state
	if state != nil && *state == "ALL" {
		filteredState = nil
	}
	if name != nil {
		if !modelDevDatasetText(*name, 128, false) {
			return nil, nil, false
		}
	}
	return filteredState, name, true
}

func modelDevDatasetStateFilter(state *string) (*string, bool) {
	if state == nil {
		return nil, true
	}
	switch *state {
	case "QUEUED", "RUNNING", "FAILED", "CANCELING", "CANCELLED", "SUCCEEDED":
		return state, true
	}
	return nil, false
}

func modelDevDatasetOperationTypeFilter(eventType *string) (*string, bool) {
	if eventType == nil {
		return nil, true
	}
	value, ok := modeldevcontractv1.DatasetOperationEventType_value["DATASET_OPERATION_EVENT_TYPE_"+*eventType]
	if !ok || value == 0 {
		return nil, false
	}
	return eventType, true
}

// modelDevInspectSourceRequest maps the two mutually exclusive public shapes
// onto the owner's oneof. mode is explicit so a body carrying file_path cannot
// silently become an INSPECT_FILE, and LIST_FILES-only fields are refused on
// INSPECT_FILE.
func modelDevInspectSourceRequest(in *modeldevv1.InspectDatasetSourceRequest) (*modeldevcontractv1.InspectDatasetSourceRequest, bool) {
	if in == nil || in.Data == nil || in.Data.Source == nil {
		return nil, false
	}
	source := in.Data.Source
	provider, ok := modelDevDatasetProvider(source.Provider)
	if !ok || !modelDevDatasetText(source.RepoId, 255, false) || !modelDevDatasetText(source.Revision, 255, false) {
		return nil, false
	}
	switch in.Data.Mode {
	case "LIST_FILES":
		if source.FilePath != nil {
			return nil, false
		}
		directory := in.Data.GetDirectoryPath()
		if directory != "" && !modelDevDatasetText(directory, 1024, false) {
			return nil, false
		}
		if len(in.Data.GetPageToken()) > 4096 {
			return nil, false
		}
		list := &modeldevcontractv1.ListDatasetSourceFiles{Provider: provider, RepoId: source.RepoId, Revision: source.Revision, DirectoryPath: directory, PageToken: in.Data.GetPageToken()}
		if in.Data.PageSize != nil {
			size, ok := modelDevDatasetBoundedUint32(*in.Data.PageSize, 1, 100)
			if !ok {
				return nil, false
			}
			list.PageSize = size
		}
		return &modeldevcontractv1.InspectDatasetSourceRequest{Query: &modeldevcontractv1.InspectDatasetSourceRequest_ListFiles{ListFiles: list}}, true
	case "INSPECT_FILE":
		if source.FilePath == nil || !modelDevDatasetText(*source.FilePath, 1024, false) {
			return nil, false
		}
		if in.Data.DirectoryPath != nil || in.Data.PageToken != nil || in.Data.PageSize != nil {
			return nil, false
		}
		return &modeldevcontractv1.InspectDatasetSourceRequest{Query: &modeldevcontractv1.InspectDatasetSourceRequest_InspectFile{
			InspectFile: &modeldevcontractv1.InspectDatasetSourceFile{Provider: provider, RepoId: source.RepoId, Revision: source.Revision, FilePath: *source.FilePath},
		}}, true
	}
	return nil, false
}

// modelDevDatasetBoundedUint32 parses the canonical decimal spelling of a value
// inside [min, max]. A leading zero, a sign or a wider value is rejected so both
// layers read the same request the same way.
func modelDevDatasetBoundedUint32(raw string, min, max uint32) (uint32, bool) {
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || strconv.FormatUint(n, 10) != raw || uint32(n) < min || uint32(n) > max {
		return 0, false
	}
	return uint32(n), true
}

// modelDevDatasetProvider accepts only the two providers the owner publishes.
func modelDevDatasetProvider(value string) (modeldevcontractv1.DatasetSourceProvider, bool) {
	switch value {
	case "HUGGINGFACE":
		return modeldevcontractv1.DatasetSourceProvider_DATASET_SOURCE_PROVIDER_HUGGINGFACE, true
	case "MODELSCOPE":
		return modeldevcontractv1.DatasetSourceProvider_DATASET_SOURCE_PROVIDER_MODELSCOPE, true
	}
	return modeldevcontractv1.DatasetSourceProvider_DATASET_SOURCE_PROVIDER_UNSPECIFIED, false
}

// modelDevImportRequest builds the exact import intent. The selection
// fingerprint is the one InspectDatasetSource returned; the source fields are
// echoed unchanged, never re-derived, so a changed file cannot be confirmed by
// an old fingerprint.
func modelDevImportRequest(in *modeldevv1.ImportDatasetVersionRequest) (*modeldevcontractv1.ImportDatasetVersionRequest, bool) {
	if in == nil || in.Data == nil || in.Data.Source == nil || !modelDevQueryUUID(in.DatasetId) || !modelDevDatasetKey(in.IdempotencyKey) {
		return nil, false
	}
	if !modelDevDigestText(in.Data.SelectionFingerprint) {
		return nil, false
	}
	source := in.Data.Source
	provider, ok := modelDevDatasetProvider(source.Provider)
	if !ok || !modelDevDatasetText(source.RepoId, 255, false) || !modelDevDatasetText(source.RequestedRevision, 255, false) || !modelDevDatasetText(source.FilePath, 1024, false) {
		return nil, false
	}
	var lock modeldevcontractv1.DatasetSourceLockKind
	switch source.LockKind {
	case "IMMUTABLE_REVISION":
		lock = modeldevcontractv1.DatasetSourceLockKind_DATASET_SOURCE_LOCK_KIND_IMMUTABLE_REVISION
		if !modelDevDatasetText(source.ResolvedRevision, 255, false) || source.ExpectedFileSha256 != "" {
			return nil, false
		}
	case "FILE_SHA256":
		lock = modeldevcontractv1.DatasetSourceLockKind_DATASET_SOURCE_LOCK_KIND_FILE_SHA256
		if !modelDevDigestText(source.ExpectedFileSha256) || source.ResolvedRevision != "" {
			return nil, false
		}
	default:
		return nil, false
	}
	if source.ExpectedSizeBytes <= 0 {
		return nil, false
	}
	return &modeldevcontractv1.ImportDatasetVersionRequest{
		IdempotencyKey: in.IdempotencyKey, DatasetId: in.DatasetId, SelectionFingerprint: in.Data.SelectionFingerprint,
		Source: &modeldevcontractv1.DatasetSourceSelection{
			Provider: provider, RepoId: source.RepoId, RequestedRevision: source.RequestedRevision, FilePath: source.FilePath,
			LockKind: lock, ResolvedRevision: source.ResolvedRevision, ExpectedSizeBytes: source.ExpectedSizeBytes, ExpectedFileSha256: source.ExpectedFileSha256,
		},
	}, true
}

// modelDevDigestText accepts a lower-case hex SHA-256, matching the value the
// source inspection produced.
func modelDevDigestText(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
