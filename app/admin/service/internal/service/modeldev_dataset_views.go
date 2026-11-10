package service

import (
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	modeldevcontractv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	modeldevv1 "go-wind-admin/api/gen/go/modeldev/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// modelDevDatasetFailure turns the sanitized data-layer failure into the public
// shape. The reason is the owner's own member name, so a reader matches a
// Task.failure.code and a synchronous rejection against one vocabulary. HTTP
// codes follow the design: conflict 409, not found 404, invalid 400,
// precondition 422, busy 429, deadline 504.
func modelDevDatasetFailure(err error) error {
	var failure *data.ModelDevDatasetFailure
	if stderrors.As(err, &failure) {
		reason := modelDevDatasetReasonName(failure.Reason)
		switch failure.Code {
		case codes.NotFound:
			return errors.New(404, reason, reason)
		case codes.AlreadyExists, codes.Aborted:
			return errors.New(409, reason, reason)
		case codes.InvalidArgument:
			return errors.New(400, reason, reason)
		case codes.FailedPrecondition:
			return errors.New(422, reason, reason)
		case codes.ResourceExhausted:
			return errors.New(429, reason, reason)
		case codes.DeadlineExceeded:
			return errors.New(504, reason, reason)
		}
	}
	switch status.Code(err) {
	case codes.Canceled:
		return errors.New(499, "DATASET_REQUEST_CANCELED", "dataset request canceled")
	case codes.DeadlineExceeded:
		return errors.New(504, "DATASET_REQUEST_TIMEOUT", "dataset request timed out")
	case codes.PermissionDenied, codes.Unauthenticated:
		return errors.New(403, "DATASET_FORBIDDEN", "dataset request forbidden")
	}
	return errors.ServiceUnavailable("MODELDEV_DATASET_UNAVAILABLE", "modeldev dataset unavailable")
}

func modelDevDatasetReasonName(reason modeldevcontractv1.ErrorReason) string {
	name := reason.String()
	if trimmed := strings.TrimPrefix(name, "ERROR_REASON_"); trimmed != name {
		return trimmed
	}
	return name
}

func modelDevDatasetView(in *modeldevcontractv1.DatasetView) *modeldevv1.DatasetView {
	if in == nil {
		return nil
	}
	return &modeldevv1.DatasetView{
		DatasetId: in.DatasetId, Name: in.Name, Description: in.Description,
		Modality: in.Modality, SchemaKind: in.SchemaKind,
		State:    strings.TrimPrefix(in.State.String(), "DATASET_STATE_"),
		Revision: strconv.FormatInt(in.Revision, 10), CreatedBy: in.CreatedBy,
		CreatedAt:                modelDevDatasetTime(in.CreatedAt),
		UpdatedAt:                modelDevDatasetTime(in.UpdatedAt),
		ReadyVersionCount:        strconv.FormatInt(in.ReadyVersionCount, 10),
		LatestReadyVersionNumber: strconv.FormatInt(in.LatestReadyVersionNumber, 10),
		LatestImport:             modelDevDatasetTaskView(in.LatestImport),
	}
}

func modelDevDatasetReceiptView(in *modeldevcontractv1.CommandReceipt) *modeldevv1.DatasetReceiptView {
	if in == nil {
		return nil
	}
	return &modeldevv1.DatasetReceiptView{
		ReceiptId: in.ReceiptId, Operation: in.Operation, Outcome: in.Outcome, DatasetId: in.DatasetId,
		ImportTaskId: in.ImportTaskId, ReservedInputVersionId: in.ReservedInputVersionId,
		VersionNumber: strconv.FormatInt(in.VersionNumber, 10), AcceptedAt: modelDevDatasetTime(in.AcceptedAt),
		Revision: strconv.FormatInt(in.Revision, 10),
	}
}

func modelDevDatasetTaskView(in *modeldevcontractv1.DatasetImportTaskView) *modeldevv1.DatasetImportTaskView {
	if in == nil {
		return nil
	}
	return &modeldevv1.DatasetImportTaskView{
		ImportTaskId: in.ImportTaskId, DatasetId: in.DatasetId,
		ReservedInputVersionId: in.ReservedInputVersionId,
		VersionNumber:          strconv.FormatInt(in.VersionNumber, 10),
		State:                  strings.TrimPrefix(in.State.String(), "DATASET_IMPORT_TASK_STATE_"),
		Phase:                  strings.TrimPrefix(in.Phase.String(), "DATASET_IMPORT_PHASE_"),
		Source:                 modelDevDatasetSourceView(in.Source),
		AcceptedAt:             modelDevDatasetTime(in.AcceptedAt),
		AttemptCount:           strconv.FormatInt(in.AttemptCount, 10),
		RecoveryGeneration:     strconv.FormatInt(in.RecoveryGeneration, 10),
		Progress:               modelDevDatasetProgressView(in.Progress),
		Failure:                modelDevDatasetFailureView(in.Failure),
		InputVersionId:         in.InputVersionId,
	}
}

func modelDevDatasetProgressView(in *modeldevcontractv1.DatasetImportProgress) *modeldevv1.DatasetImportProgressView {
	if in == nil {
		return nil
	}
	return &modeldevv1.DatasetImportProgressView{
		Stage:      strings.TrimPrefix(in.Stage.String(), "DATASET_IMPORT_PHASE_"),
		BytesTotal: strconv.FormatInt(in.BytesTotal, 10), BytesDone: strconv.FormatInt(in.BytesDone, 10),
		FilesTotal: strconv.FormatInt(in.FilesTotal, 10), FilesDone: strconv.FormatInt(in.FilesDone, 10),
		UpdatedAt: modelDevDatasetTime(in.UpdatedAt),
	}
}

// modelDevDatasetFailureView is the asynchronous carrier. The owner reports the
// importer's own code and its retryability verdict; the remaining fields stay
// empty because this surface must not become a channel for owner prose.
func modelDevDatasetFailureView(in *modeldevcontractv1.DatasetTaskFailure) *modeldevv1.TaskFailureView {
	if in == nil {
		return nil
	}
	return &modeldevv1.TaskFailureView{
		Code:      in.Code,
		Retryable: in.Retryable,
	}
}

func modelDevDatasetSourceView(in *modeldevcontractv1.DatasetSourceSnapshot) *modeldevv1.DatasetSourceSnapshotView {
	if in == nil {
		return nil
	}
	return &modeldevv1.DatasetSourceSnapshotView{
		Selection:      modelDevDatasetSelectionView(in.Selection),
		ResolvedAt:     modelDevDatasetTime(in.ResolvedAt),
		SnapshotSha256: in.SnapshotSha256,
	}
}

func modelDevDatasetSelectionView(in *modeldevcontractv1.DatasetSourceSelection) *modeldevv1.DatasetSourceSelectionView {
	if in == nil {
		return nil
	}
	return &modeldevv1.DatasetSourceSelectionView{
		Provider: strings.TrimPrefix(in.Provider.String(), "DATASET_SOURCE_PROVIDER_"),
		RepoId:   in.RepoId, RequestedRevision: in.RequestedRevision, FilePath: in.FilePath,
		LockKind:           strings.TrimPrefix(in.LockKind.String(), "DATASET_SOURCE_LOCK_KIND_"),
		ResolvedRevision:   in.ResolvedRevision,
		ExpectedSizeBytes:  in.ExpectedSizeBytes,
		ExpectedFileSha256: in.ExpectedFileSha256,
	}
}

func modelDevDatasetVersionView(in *modeldevcontractv1.DatasetVersionView) *modeldevv1.DatasetVersionView {
	if in == nil {
		return nil
	}
	view := &modeldevv1.DatasetVersionView{
		InputVersionId: in.InputVersionId, DatasetId: in.DatasetId, ImportTaskId: in.ImportTaskId,
		VersionNumber: strconv.FormatInt(in.VersionNumber, 10), PublishedAt: modelDevDatasetTime(in.PublishedAt),
		PreviewAvailable: in.PreviewAvailable, Source: modelDevDatasetSourceView(in.Source),
	}
	if in.InputVersion != nil {
		view.InputVersion = &modeldevv1.InputVersionView{
			InputVersionId: in.InputVersion.InputVersionId,
			State:          strings.TrimPrefix(in.InputVersion.State.String(), "INPUT_STATE_"),
			Format:         in.InputVersion.Format, SizeBytes: in.InputVersion.SizeBytes, Sha256: in.InputVersion.Sha256,
			RowCount: in.InputVersion.RowCount, FeatureCount: in.InputVersion.FeatureCount,
			CreatedAt: modelDevDatasetTime(in.InputVersion.CreatedAt),
		}
	}
	return view
}

func modelDevDatasetPreviewView(in *modeldevcontractv1.GetInputVersionPreviewResponse) *modeldevv1.DatasetPreviewView {
	if in == nil {
		return nil
	}
	view := &modeldevv1.DatasetPreviewView{
		InputVersionId: in.InputVersionId, TotalRows: strconv.FormatInt(in.TotalRows, 10),
		PreviewTruncated:   in.PreviewTruncated,
		MaxRecords:         strconv.FormatInt(int64(in.MaxRecords), 10),
		MaxFieldCharacters: strconv.FormatInt(int64(in.MaxFieldCharacters), 10),
	}
	for _, record := range in.Records {
		if record == nil {
			continue
		}
		view.Records = append(view.Records, &modeldevv1.DatasetPreviewRecordView{
			SampleIndex: strconv.FormatInt(record.SampleIndex, 10), Instruction: record.Instruction,
			Input: record.Input, Output: record.Output, TruncatedFields: record.TruncatedFields,
		})
	}
	return view
}

func modelDevDatasetOperationView(in *modeldevcontractv1.DatasetOperationView) *modeldevv1.DatasetOperationView {
	if in == nil {
		return nil
	}
	return &modeldevv1.DatasetOperationView{
		OperationId: in.OperationId,
		EventType:   strings.TrimPrefix(in.EventType.String(), "DATASET_OPERATION_EVENT_TYPE_"),
		Actor:       in.Actor, OccurredAt: modelDevDatasetTime(in.OccurredAt), Result: in.Result,
		ImportTaskId: in.ImportTaskId, ImportAttemptId: in.ImportAttemptId, ReceiptId: in.ReceiptId,
		Revision: strconv.FormatInt(in.Revision, 10), RecoveryGeneration: strconv.FormatInt(in.RecoveryGeneration, 10),
		FailureCode: in.FailureCode,
	}
}

func modelDevInspectSourceResponse(in *modeldevcontractv1.InspectDatasetSourceResponse) *modeldevv1.InspectDatasetSourceResponse {
	if in == nil {
		return nil
	}
	reply := &modeldevv1.InspectDatasetSourceResponse{
		ResolvedRevision: in.ResolvedRevision, Selection: modelDevDatasetSelectionView(in.Selection),
		SelectionFingerprint: in.SelectionFingerprint,
	}
	if page := in.FilesPage; page != nil {
		reply.FilesPage = &modeldevv1.DatasetSourceFilesPageView{NextPageToken: page.NextPageToken, HasMore: page.HasMore}
		for _, item := range page.Items {
			if item == nil {
				continue
			}
			reply.FilesPage.Items = append(reply.FilesPage.Items, &modeldevv1.DatasetSourceFileView{
				Path: item.Path, SizeBytes: strconv.FormatInt(item.SizeBytes, 10), Directory: item.Directory,
			})
		}
	}
	return reply
}

// modelDevDatasetTime renders the owner's UTC timestamp in the same RFC 3339
// form every other ModelDev view uses. An absent timestamp stays an empty
// string rather than the zero instant.
func modelDevDatasetTime(in *timestamppb.Timestamp) string {
	if in == nil || in.CheckValid() != nil {
		return ""
	}
	return in.AsTime().UTC().Format(time.RFC3339Nano)
}
