package data

import (
	"context"
	"fmt"
	"slices"

	modeldevv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ModelDevDatasetFailure is the sanitized outcome of one Dataset RPC. The
// owner's free text is dropped; only the gRPC code and the typed reason survive
// so the service layer can map them onto the public vocabulary. The design
// classifies Dataset failures by where they happen: a synchronous acceptance
// rejection carries ErrorDetail and is answered here, while a failure recorded
// after acceptance stays on the task row and is read from Task GET.
type ModelDevDatasetFailure struct {
	Code   codes.Code
	Reason modeldevv1.ErrorReason
}

func (failure *ModelDevDatasetFailure) Error() string {
	return fmt.Sprintf("modeldev dataset request failed: %s/%s", failure.Code, datasetReasonName(failure.Reason))
}

// datasetReasonName is the public spelling without the ERROR_REASON_ prefix.
func datasetReasonName(reason modeldevv1.ErrorReason) string {
	name := reason.String()
	const prefix = "ERROR_REASON_"
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return name[len(prefix):]
	}
	return name
}

// modelDevDatasetReasons binds each owner reason to the gRPC codes the owner
// contract allows for it. A reason arriving with another code, an unknown
// reason, or any owner prose is a contract violation and degrades to the
// generic upstream failure instead of reaching the public mapping.
//
// COMMAND_CONFLICT carries two distinct facts: a duplicate dataset name is
// AlreadyExists, while a stale expected_revision is Aborted. Both are the same
// public 409, so the reason alone cannot pick one code.
var modelDevDatasetReasons = map[modeldevv1.ErrorReason][]codes.Code{
	modeldevv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT:               {codes.InvalidArgument},
	modeldevv1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND:             {codes.NotFound},
	modeldevv1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT:           {codes.AlreadyExists},
	modeldevv1.ErrorReason_ERROR_REASON_COMMAND_CONFLICT:               {codes.AlreadyExists, codes.Aborted},
	modeldevv1.ErrorReason_ERROR_REASON_INPUT_NOT_READY:                {codes.FailedPrecondition},
	modeldevv1.ErrorReason_ERROR_REASON_INPUT_KIND_FORMAT_MISMATCH:     {codes.FailedPrecondition},
	modeldevv1.ErrorReason_ERROR_REASON_FORBIDDEN:                      {codes.PermissionDenied},
	modeldevv1.ErrorReason_ERROR_REASON_ENVIRONMENT_NOT_READY:          {codes.FailedPrecondition},
	modeldevv1.ErrorReason_ERROR_REASON_ENVIRONMENT_NOT_REGISTERED:     {codes.FailedPrecondition},
	modeldevv1.ErrorReason_ERROR_REASON_INTEGRITY_MISMATCH:             {codes.FailedPrecondition},
	modeldevv1.ErrorReason_ERROR_REASON_DATASET_INVALID:                {codes.FailedPrecondition},
	modeldevv1.ErrorReason_ERROR_REASON_DATASET_RECORD_TOO_LARGE:       {codes.FailedPrecondition},
	modeldevv1.ErrorReason_ERROR_REASON_INPUT_VALIDATION_BUSY:          {codes.ResourceExhausted},
	modeldevv1.ErrorReason_ERROR_REASON_DEADLINE_EXCEEDED:              {codes.DeadlineExceeded},
	modeldevv1.ErrorReason_ERROR_REASON_UPSTREAM_UNAVAILABLE:           {codes.Unavailable},
	modeldevv1.ErrorReason_ERROR_REASON_FINETUNING_RUNTIME_UNAVAILABLE: {codes.Unavailable},
}

// modelDevDatasetContext builds the delegated call context. Every Dataset
// method, writes included, is reached with the same current per-request
// authorization the query service uses: the owner's receiver accepts exactly
// this method name with tenant-all scope and nothing else.
func (c *ModelDevClient) modelDevDatasetContext(ctx context.Context, scope ModelDevResolveScope, method string) (context.Context, context.CancelFunc, error) {
	budget := c.timeout
	switch method {
	case modeldevv1.ModelDevDatasetService_ImportDatasetVersion_FullMethodName,
		modeldevv1.ModelDevDatasetService_InspectDatasetSource_FullMethodName:
		// Both re-resolve the source before answering, so they share the import
		// budget rather than the three-second call budget.
		budget = c.importTimeout
	}
	return c.queryContextBudget(ctx, scope, method, budget)
}

// modelDevDatasetError keeps only a recognized typed reason from a synchronous
// rejection. Everything else, including any owner text, is the generic
// upstream answer so a new owner reason cannot silently widen the surface.
func modelDevDatasetError(err error) error {
	failure, ok := status.FromError(err)
	if !ok || len(failure.Proto().ProtoReflect().GetUnknown()) != 0 {
		return modelDevQueryUnavailable()
	}
	if failure.Code() == codes.Canceled {
		return status.FromContextError(context.Canceled).Err()
	}
	if len(failure.Proto().Details) == 0 {
		if failure.Code() == codes.DeadlineExceeded {
			return status.FromContextError(context.DeadlineExceeded).Err()
		}
		return modelDevQueryUnavailable()
	}
	if len(failure.Proto().Details) != 1 || failure.Proto().Details[0] == nil || len(failure.Proto().Details[0].ProtoReflect().GetUnknown()) != 0 {
		return modelDevQueryUnavailable()
	}
	details := failure.Details()
	if len(details) != 1 {
		return modelDevQueryUnavailable()
	}
	detail, ok := details[0].(*modeldevv1.ErrorDetail)
	if !ok || detail == nil || modelDevUnknownFields(detail.ProtoReflect()) || detail.Kind != "" || len(detail.Violations) != 0 || detail.LimitName != "" || detail.Actual != nil || detail.Limit != nil {
		return modelDevQueryUnavailable()
	}
	expected, known := modelDevDatasetReasons[detail.Reason]
	if !known || !slices.Contains(expected, failure.Code()) {
		return modelDevQueryUnavailable()
	}
	return &ModelDevDatasetFailure{Code: failure.Code(), Reason: detail.Reason}
}

// ---- delegated calls ----

func (c *ModelDevClient) CreateDataset(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.CreateDatasetRequest) (*modeldevv1.CreateDatasetResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_CreateDataset_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.CreateDatasetRequest) (*modeldevv1.CreateDatasetResponse, error) {
			return client.CreateDataset(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetView(out.Dataset) || !modelDevValidDatasetReceipt(out.Receipt, out.Dataset.GetDatasetId()) {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) GetDataset(ctx context.Context, scope ModelDevResolveScope, id string) (*modeldevv1.GetDatasetResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_GetDataset_FullMethodName, &modeldevv1.GetDatasetRequest{DatasetId: id},
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.GetDatasetRequest) (*modeldevv1.GetDatasetResponse, error) {
			return client.GetDataset(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetView(out.Dataset) || out.Dataset.DatasetId != id {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) ListDatasets(ctx context.Context, scope ModelDevResolveScope, page, pageSize uint32, state, name *string) (*modeldevv1.ListDatasetsResponse, error) {
	var filter modeldevv1.DatasetState
	if state != nil {
		value, ok := modeldevv1.DatasetState_value["DATASET_STATE_"+*state]
		if !ok || value == 0 {
			return nil, modelDevQueryUnavailable()
		}
		filter = modeldevv1.DatasetState(value)
	}
	request := &modeldevv1.ListDatasetsRequest{
		Page: &modeldevv1.PageRequest{Page: &page, PageSize: &pageSize},
		Name: name,
	}
	if state != nil {
		request.State = &filter
	}
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_ListDatasets_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.ListDatasetsRequest) (*modeldevv1.ListDatasetsResponse, error) {
			return client.ListDatasets(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || out.Total < 0 || len(out.Items) > 100 {
		return nil, modelDevQueryUnavailable()
	}
	seen := map[string]bool{}
	for _, item := range out.Items {
		if !modelDevValidDatasetView(item) || seen[item.DatasetId] || (state != nil && item.State != filter) {
			return nil, modelDevQueryUnavailable()
		}
		seen[item.DatasetId] = true
	}
	return out, nil
}

func (c *ModelDevClient) UpdateDataset(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.UpdateDatasetRequest) (*modeldevv1.UpdateDatasetResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_UpdateDataset_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.UpdateDatasetRequest) (*modeldevv1.UpdateDatasetResponse, error) {
			return client.UpdateDataset(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetView(out.Dataset) || out.Dataset.DatasetId != request.GetDatasetId() || !modelDevValidDatasetReceipt(out.Receipt, request.GetDatasetId()) {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) ArchiveDataset(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.ArchiveDatasetRequest) (*modeldevv1.ArchiveDatasetResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_ArchiveDataset_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.ArchiveDatasetRequest) (*modeldevv1.ArchiveDatasetResponse, error) {
			return client.ArchiveDataset(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	// An archive is idempotent on a replayed receipt, so the returned state is
	// whatever the owner already stored rather than necessarily ARCHIVED here.
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetView(out.Dataset) || out.Dataset.DatasetId != request.GetDatasetId() || !modelDevValidDatasetReceipt(out.Receipt, request.GetDatasetId()) {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) RestoreDataset(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.RestoreDatasetRequest) (*modeldevv1.RestoreDatasetResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_RestoreDataset_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.RestoreDatasetRequest) (*modeldevv1.RestoreDatasetResponse, error) {
			return client.RestoreDataset(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetView(out.Dataset) || out.Dataset.DatasetId != request.GetDatasetId() || !modelDevValidDatasetReceipt(out.Receipt, request.GetDatasetId()) {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) InspectDatasetSource(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.InspectDatasetSourceRequest) (*modeldevv1.InspectDatasetSourceResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_InspectDatasetSource_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.InspectDatasetSourceRequest) (*modeldevv1.InspectDatasetSourceResponse, error) {
			return client.InspectDatasetSource(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || out.SelectionFingerprint != "" && !modelDevDigest(out.SelectionFingerprint) {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) ImportDatasetVersion(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.ImportDatasetVersionRequest) (*modeldevv1.ImportDatasetVersionResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_ImportDatasetVersion_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.ImportDatasetVersionRequest) (*modeldevv1.ImportDatasetVersionResponse, error) {
			return client.ImportDatasetVersion(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetImportTask(out.Task) || out.Task.DatasetId != request.GetDatasetId() || !modelDevValidDatasetReceipt(out.Receipt, request.GetDatasetId()) {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) CancelDatasetImport(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.CancelDatasetImportRequest) (*modeldevv1.CancelDatasetImportResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_CancelDatasetImport_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.CancelDatasetImportRequest) (*modeldevv1.CancelDatasetImportResponse, error) {
			return client.CancelDatasetImport(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetReceipt(out.Receipt, "") {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) RetryDatasetImport(ctx context.Context, scope ModelDevResolveScope, request *modeldevv1.RetryDatasetImportRequest) (*modeldevv1.RetryDatasetImportResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_RetryDatasetImport_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.RetryDatasetImportRequest) (*modeldevv1.RetryDatasetImportResponse, error) {
			return client.RetryDatasetImport(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetReceipt(out.Receipt, "") {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) ListDatasetVersions(ctx context.Context, scope ModelDevResolveScope, id string, page, pageSize uint32) (*modeldevv1.ListDatasetVersionsResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_ListDatasetVersions_FullMethodName,
		&modeldevv1.ListDatasetVersionsRequest{DatasetId: id, Page: &modeldevv1.PageRequest{Page: &page, PageSize: &pageSize}},
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.ListDatasetVersionsRequest) (*modeldevv1.ListDatasetVersionsResponse, error) {
			return client.ListDatasetVersions(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || out.Total < 0 || len(out.Items) > 100 {
		return nil, modelDevQueryUnavailable()
	}
	seen := map[string]bool{}
	for _, item := range out.Items {
		if item == nil || !modelDevCanonicalUUID(item.InputVersionId) || item.DatasetId != id || seen[item.InputVersionId] || item.VersionNumber <= 0 || !modelDevValidTimestamp(item.PublishedAt) {
			return nil, modelDevQueryUnavailable()
		}
		seen[item.InputVersionId] = true
	}
	return out, nil
}

func (c *ModelDevClient) GetInputVersionPreview(ctx context.Context, scope ModelDevResolveScope, inputVersionID string) (*modeldevv1.GetInputVersionPreviewResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_GetInputVersionPreview_FullMethodName,
		&modeldevv1.GetInputVersionPreviewRequest{InputVersionId: inputVersionID},
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.GetInputVersionPreviewRequest) (*modeldevv1.GetInputVersionPreviewResponse, error) {
			return client.GetInputVersionPreview(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || out.InputVersionId != inputVersionID || out.TotalRows < 0 || len(out.Records) > 100 {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) ListDatasetImportTasks(ctx context.Context, scope ModelDevResolveScope, id string, page, pageSize uint32, state *string) (*modeldevv1.ListDatasetImportTasksResponse, error) {
	request := &modeldevv1.ListDatasetImportTasksRequest{DatasetId: id, Page: &modeldevv1.PageRequest{Page: &page, PageSize: &pageSize}}
	var filter modeldevv1.DatasetImportTaskState
	if state != nil {
		value, ok := modeldevv1.DatasetImportTaskState_value["DATASET_IMPORT_TASK_STATE_"+*state]
		if !ok || value == 0 {
			return nil, modelDevQueryUnavailable()
		}
		filter = modeldevv1.DatasetImportTaskState(value)
		request.State = &filter
	}
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_ListDatasetImportTasks_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.ListDatasetImportTasksRequest) (*modeldevv1.ListDatasetImportTasksResponse, error) {
			return client.ListDatasetImportTasks(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || out.Total < 0 || len(out.Items) > 100 {
		return nil, modelDevQueryUnavailable()
	}
	seen := map[string]bool{}
	for _, item := range out.Items {
		if !modelDevValidDatasetImportTask(item) || item.DatasetId != id || seen[item.ImportTaskId] || (state != nil && item.State != filter) {
			return nil, modelDevQueryUnavailable()
		}
		seen[item.ImportTaskId] = true
	}
	return out, nil
}

func (c *ModelDevClient) GetDatasetImportTask(ctx context.Context, scope ModelDevResolveScope, id string) (*modeldevv1.GetDatasetImportTaskResponse, error) {
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_GetDatasetImportTask_FullMethodName, &modeldevv1.GetDatasetImportTaskRequest{ImportTaskId: id},
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.GetDatasetImportTaskRequest) (*modeldevv1.GetDatasetImportTaskResponse, error) {
			return client.GetDatasetImportTask(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || !modelDevValidDatasetImportTask(out.Task) || out.Task.ImportTaskId != id {
		return nil, modelDevQueryUnavailable()
	}
	return out, nil
}

func (c *ModelDevClient) ListDatasetOperations(ctx context.Context, scope ModelDevResolveScope, id string, page, pageSize uint32, eventType, importTaskID *string) (*modeldevv1.ListDatasetOperationsResponse, error) {
	request := &modeldevv1.ListDatasetOperationsRequest{DatasetId: id, Page: &modeldevv1.PageRequest{Page: &page, PageSize: &pageSize}, ImportTaskId: importTaskID}
	var filter modeldevv1.DatasetOperationEventType
	if eventType != nil {
		value, ok := modeldevv1.DatasetOperationEventType_value["DATASET_OPERATION_EVENT_TYPE_"+*eventType]
		if !ok || value == 0 {
			return nil, modelDevQueryUnavailable()
		}
		filter = modeldevv1.DatasetOperationEventType(value)
		request.EventType = &filter
	}
	out, err := modelDevDatasetCall(ctx, c, scope, modeldevv1.ModelDevDatasetService_ListDatasetOperations_FullMethodName, request,
		func(call context.Context, client modeldevv1.ModelDevDatasetServiceClient, in *modeldevv1.ListDatasetOperationsRequest) (*modeldevv1.ListDatasetOperationsResponse, error) {
			return client.ListDatasetOperations(call, in, grpc.WaitForReady(true))
		})
	if err != nil {
		return nil, err
	}
	if out == nil || modelDevUnknownFields(out.ProtoReflect()) || out.Total < 0 || len(out.Items) > 100 {
		return nil, modelDevQueryUnavailable()
	}
	seen := map[string]bool{}
	for _, item := range out.Items {
		if item == nil || !modelDevCanonicalUUID(item.OperationId) || seen[item.OperationId] || item.EventType == 0 || !modelDevValidTimestamp(item.OccurredAt) ||
			(importTaskID != nil && item.ImportTaskId != *importTaskID) || (eventType != nil && item.EventType != filter) {
			return nil, modelDevQueryUnavailable()
		}
		seen[item.OperationId] = true
	}
	return out, nil
}

// modelDevValidDatasetView accepts the owner's dataset facts. Only the two
// published lifecycle states exist, so an unknown enum is a contract error
// rather than a row that happens to render oddly.
func modelDevValidDatasetView(view *modeldevv1.DatasetView) bool {
	if view == nil || modelDevUnknownFields(view.ProtoReflect()) || !modelDevCanonicalUUID(view.DatasetId) || view.Name == "" || view.Revision <= 0 ||
		(view.State != modeldevv1.DatasetState_DATASET_STATE_ACTIVE && view.State != modeldevv1.DatasetState_DATASET_STATE_ARCHIVED) ||
		!modelDevValidTimestamp(view.CreatedAt) || !modelDevValidTimestamp(view.UpdatedAt) || view.ReadyVersionCount < 0 || view.LatestReadyVersionNumber < 0 ||
		view.LatestImport != nil && !modelDevValidDatasetImportTask(view.LatestImport) {
		return false
	}
	return true
}

// modelDevValidDatasetReceipt accepts one command receipt. datasetID, when
// non-empty, binds the receipt to the object the caller named so a replayed
// receipt for another resource cannot be returned in its place.
func modelDevValidDatasetReceipt(receipt *modeldevv1.CommandReceipt, datasetID string) bool {
	if receipt == nil || modelDevUnknownFields(receipt.ProtoReflect()) || !modelDevCanonicalUUID(receipt.ReceiptId) || receipt.Operation == "" || receipt.Outcome == "" ||
		!modelDevValidTimestamp(receipt.AcceptedAt) {
		return false
	}
	if receipt.DatasetId != "" && !modelDevCanonicalUUID(receipt.DatasetId) {
		return false
	}
	if datasetID != "" && receipt.DatasetId != "" && receipt.DatasetId != datasetID {
		return false
	}
	if receipt.ImportTaskId != "" && (!modelDevCanonicalUUID(receipt.ImportTaskId) || !modelDevCanonicalUUID(receipt.ReservedInputVersionId) || receipt.VersionNumber <= 0) {
		return false
	}
	return true
}

// modelDevValidDatasetImportTask accepts one task row. A SUCCEEDED task is the
// only state where input_version_id is populated, and it must then equal the
// reserved version the acceptance already promised.
func modelDevValidDatasetImportTask(task *modeldevv1.DatasetImportTaskView) bool {
	if task == nil || modelDevUnknownFields(task.ProtoReflect()) || !modelDevCanonicalUUID(task.ImportTaskId) || !modelDevCanonicalUUID(task.DatasetId) ||
		!modelDevCanonicalUUID(task.ReservedInputVersionId) || task.VersionNumber <= 0 || !modelDevValidTimestamp(task.AcceptedAt) ||
		task.State == modeldevv1.DatasetImportTaskState_DATASET_IMPORT_TASK_STATE_UNSPECIFIED || task.AttemptCount < 0 || task.RecoveryGeneration < 0 {
		return false
	}
	if task.Source != nil && (task.Source.Selection == nil || !modelDevValidTimestamp(task.Source.ResolvedAt) || !modelDevDigest(task.Source.SnapshotSha256)) {
		return false
	}
	// The failure carrier exists only for a FAILED task, and it must name a code:
	// an empty code would leave the page with nothing to branch on.
	if task.Failure != nil && (task.State != modeldevv1.DatasetImportTaskState_DATASET_IMPORT_TASK_STATE_FAILED || task.Failure.Code == "") {
		return false
	}
	if task.State == modeldevv1.DatasetImportTaskState_DATASET_IMPORT_TASK_STATE_SUCCEEDED {
		return task.InputVersionId == task.ReservedInputVersionId
	}
	return task.InputVersionId == ""
}

// modelDevDatasetCall runs one delegated RPC. The fifteen methods differ only in
// their request, their client method and their reply checks, so the delegated
// authorization, timeout and error sanitization live in exactly one place.
func modelDevDatasetCall[Req any, Resp any](ctx context.Context, c *ModelDevClient, scope ModelDevResolveScope, method string, request Req,
	invoke func(context.Context, modeldevv1.ModelDevDatasetServiceClient, Req) (Resp, error)) (Resp, error) {
	var zero Resp
	if c == nil || c.connection == nil {
		return zero, modelDevQueryUnavailable()
	}
	call, cancel, err := c.modelDevDatasetContext(ctx, scope, method)
	if err != nil {
		return zero, err
	}
	defer cancel()
	out, err := invoke(call, modeldevv1.NewModelDevDatasetServiceClient(c.connection), request)
	if err != nil {
		return zero, modelDevDatasetError(err)
	}
	return out, nil
}
