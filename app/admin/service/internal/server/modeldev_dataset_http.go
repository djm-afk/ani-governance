package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	nethttp "net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
	adminv1 "go-wind-admin/api/gen/go/admin/service/v1"
	modeldevv1 "go-wind-admin/api/gen/go/modeldev/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/internal/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Dataset routes use a handwritten strict adapter, for the same reason the
// environment routes do: the public contract has presence-sensitive fields
// (update_mask, expected_revision as a decimal string) and per-operation status
// codes, and the generated binder would neither reject unknown keys nor pick
// the right code. Each handler validates the body here, then calls exactly one
// service method, which owns identity and delegation.

func invalidModelDevDatasetHTTP() error {
	return errors.BadRequest("INVALID_DATASET_REQUEST", "invalid dataset request")
}

// decodeModelDevDatasetBody reads one bounded JSON object, rejects unknown keys
// and duplicate keys, and refuses a body whose top level is not an object.
func decodeModelDevDatasetBody(r *nethttp.Request, limit int64) (map[string]json.RawMessage, bool) {
	if r == nil || r.URL.ForceQuery || r.URL.RawPath != "" && r.URL.RawPath != r.URL.Path {
		return nil, false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(raw)) > limit || !utf8.Valid(raw) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, false
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		fields[name] = value
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}

// decodeModelDevDatasetObject decodes one nested object into message, keeping
// the same unknown/duplicate-key rules the top level uses, and then refuses any
// proto field the parser would have silently dropped.
func decodeModelDevDatasetObject(raw json.RawMessage, message proto.Message) bool {
	if len(raw) == 0 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	for decoder.More() {
		if token, err := decoder.Token(); err != nil || token == nil {
			return false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return false
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, message); err != nil {
		return false
	}
	return !modelDevDatasetHasUnknown(message)
}

func modelDevDatasetHasUnknown(message proto.Message) bool {
	if message == nil {
		return true
	}
	return len(message.ProtoReflect().GetUnknown()) != 0
}

// stripModelDevDatasetCallerHeaders removes the delegated authorization headers
// a caller might try to smuggle in. The service mints them itself.
func stripModelDevDatasetCallerHeaders(r *nethttp.Request) {
	for name := range r.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-ani-") || lower == "idempotency-key" {
			r.Header.Del(name)
		}
	}
}

// datasetPathID extracts one canonical UUID path segment.
func datasetPathID(ctx khttp.Context, name string) (string, bool) {
	value := ctx.Vars().Get(name)
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.String() != value {
		return "", false
	}
	return value, true
}

func registerModelDevDatasetHTTP(server *khttp.Server, modeldev *service.ModelDevService) {
	route := server.Route("/")

	// ---- collections ----
	route.POST(data.ModelDevCreateDatasetPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		stripModelDevDatasetCallerHeaders(ctx.Request())
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceCreateDataset)
		fields, ok := decodeModelDevDatasetBody(ctx.Request(), 16384)
		if !ok || len(fields) != 2 || fields["idempotency_key"] == nil || fields["data"] == nil {
			return invalidModelDevDatasetHTTP()
		}
		in := &modeldevv1.CreateDatasetRequest{Data: &modeldevv1.CreateDatasetData{}}
		if json.Unmarshal(fields["idempotency_key"], &in.IdempotencyKey) != nil || !decodeModelDevDatasetObject(fields["data"], in.Data) {
			return invalidModelDevDatasetHTTP()
		}
		if modelDevDatasetHasUnknown(in) || modelDevDatasetHasUnknown(in.Data) {
			return invalidModelDevDatasetHTTP()
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.CreateDataset(c, in) })
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusCreated, out)
	})

	route.GET(data.ModelDevListDatasetsPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceListDatasets)
		in := &modeldevv1.ListDatasetsRequest{}
		query, err := modelDevBoundedQuery(ctx.Request())
		if err != nil {
			return err
		}
		for name, values := range query {
			switch name {
			case "page":
				in.Page, err = modelDevPagePointer(values[0])
			case "pageSize":
				in.PageSize, err = modelDevPageSizePointer(values[0])
			case "state":
				in.State, err = modelDevDatasetStatePointer(values[0])
			case "name":
				in.Name, err = modelDevNamePointer(values[0])
			default:
				return invalidModelDevDatasetHTTP()
			}
			if err != nil {
				return err
			}
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.ListDatasets(c, in) })
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	route.GET(data.ModelDevGetDatasetPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceGetDataset)
		id, ok := datasetPathID(ctx, "dataset_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		if raw, err := io.ReadAll(io.LimitReader(ctx.Request().Body, 1)); err != nil || len(raw) != 0 || ctx.Request().URL.RawQuery != "" {
			return invalidModelDevDatasetHTTP()
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) {
			return modeldev.GetDataset(c, &modeldevv1.GetDatasetRequest{DatasetId: id})
		})
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	route.PATCH(data.ModelDevUpdateDatasetPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		stripModelDevDatasetCallerHeaders(ctx.Request())
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceUpdateDataset)
		id, ok := datasetPathID(ctx, "dataset_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		fields, ok := decodeModelDevDatasetBody(ctx.Request(), 16384)
		if !ok || len(fields) != 3 || fields["idempotency_key"] == nil || fields["update_mask"] == nil || fields["data"] == nil {
			return invalidModelDevDatasetHTTP()
		}
		in := &modeldevv1.UpdateDatasetRequest{DatasetId: id, Data: &modeldevv1.UpdateDatasetData{}}
		if json.Unmarshal(fields["idempotency_key"], &in.IdempotencyKey) != nil || json.Unmarshal(fields["update_mask"], &in.UpdateMask) != nil || !decodeModelDevDatasetObject(fields["data"], in.Data) {
			return invalidModelDevDatasetHTTP()
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.UpdateDataset(c, in) })
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	for _, lifecycle := range []struct {
		path      string
		operation string
		archive   bool
	}{
		{data.ModelDevArchiveDatasetPath, adminv1.OperationModelDevServiceArchiveDataset, true},
		{data.ModelDevRestoreDatasetPath, adminv1.OperationModelDevServiceRestoreDataset, false},
	} {
		lifecycle := lifecycle
		route.POST(lifecycle.path, func(ctx khttp.Context) error {
			modelDevQueryHeaders(ctx)
			stripModelDevDatasetCallerHeaders(ctx.Request())
			khttp.SetOperation(ctx, lifecycle.operation)
			id, ok := datasetPathID(ctx, "dataset_id")
			if !ok {
				return invalidModelDevDatasetHTTP()
			}
			fields, ok := decodeModelDevDatasetBody(ctx.Request(), 4096)
			if !ok || len(fields) != 2 || fields["idempotency_key"] == nil || fields["data"] == nil {
				return invalidModelDevDatasetHTTP()
			}
			data := &modeldevv1.DatasetRevisionData{}
			var key string
			if json.Unmarshal(fields["idempotency_key"], &key) != nil || !decodeModelDevDatasetObject(fields["data"], data) {
				return invalidModelDevDatasetHTTP()
			}
			out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) {
				if lifecycle.archive {
					return modeldev.ArchiveDataset(c, &modeldevv1.ArchiveDatasetRequest{DatasetId: id, IdempotencyKey: key, Data: data})
				}
				return modeldev.RestoreDataset(c, &modeldevv1.RestoreDatasetRequest{DatasetId: id, IdempotencyKey: key, Data: data})
			})
			if err != nil {
				return err
			}
			return ctx.Result(nethttp.StatusOK, out)
		})
	}

	// ---- source inspection and import ----
	route.POST(data.ModelDevInspectDatasetSourcePath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		stripModelDevDatasetCallerHeaders(ctx.Request())
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceInspectDatasetSource)
		fields, ok := decodeModelDevDatasetBody(ctx.Request(), 16384)
		if !ok || len(fields) != 1 || fields["data"] == nil {
			return invalidModelDevDatasetHTTP()
		}
		in := &modeldevv1.InspectDatasetSourceRequest{Data: &modeldevv1.InspectDatasetSourceData{}}
		if !decodeModelDevDatasetObject(fields["data"], in.Data) {
			return invalidModelDevDatasetHTTP()
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.InspectDatasetSource(c, in) })
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	route.POST(data.ModelDevImportDatasetVersionPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		stripModelDevDatasetCallerHeaders(ctx.Request())
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceImportDatasetVersion)
		id, ok := datasetPathID(ctx, "dataset_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		fields, ok := decodeModelDevDatasetBody(ctx.Request(), 32768)
		if !ok || len(fields) != 2 || fields["idempotency_key"] == nil || fields["data"] == nil {
			return invalidModelDevDatasetHTTP()
		}
		in := &modeldevv1.ImportDatasetVersionRequest{DatasetId: id, Data: &modeldevv1.ImportDatasetVersionData{}}
		if json.Unmarshal(fields["idempotency_key"], &in.IdempotencyKey) != nil || !decodeModelDevDatasetObject(fields["data"], in.Data) {
			return invalidModelDevDatasetHTTP()
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.ImportDatasetVersion(c, in) })
		if err != nil {
			return err
		}
		// 202: acceptance with a stable receipt does not mean the version is ready.
		return ctx.Result(nethttp.StatusAccepted, out)
	})

	// ---- versions, preview and tasks ----
	route.GET(data.ModelDevListDatasetVersionsPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceListDatasetVersions)
		id, ok := datasetPathID(ctx, "dataset_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		in := &modeldevv1.ListDatasetVersionsRequest{DatasetId: id}
		query, err := modelDevBoundedQuery(ctx.Request())
		if err != nil {
			return err
		}
		for name, values := range query {
			switch name {
			case "page":
				in.Page, err = modelDevPagePointer(values[0])
			case "pageSize":
				in.PageSize, err = modelDevPageSizePointer(values[0])
			default:
				return invalidModelDevDatasetHTTP()
			}
			if err != nil {
				return err
			}
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.ListDatasetVersions(c, in) })
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	route.GET(data.ModelDevGetInputVersionPreviewPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceGetInputVersionPreview)
		id, ok := datasetPathID(ctx, "input_version_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		if raw, err := io.ReadAll(io.LimitReader(ctx.Request().Body, 1)); err != nil || len(raw) != 0 || ctx.Request().URL.RawQuery != "" {
			return invalidModelDevDatasetHTTP()
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) {
			return modeldev.GetInputVersionPreview(c, &modeldevv1.GetInputVersionPreviewRequest{InputVersionId: id})
		})
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	route.GET(data.ModelDevListDatasetImportTasksPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceListDatasetImportTasks)
		id, ok := datasetPathID(ctx, "dataset_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		in := &modeldevv1.ListDatasetImportTasksRequest{DatasetId: id}
		query, err := modelDevBoundedQuery(ctx.Request())
		if err != nil {
			return err
		}
		for name, values := range query {
			switch name {
			case "page":
				in.Page, err = modelDevPagePointer(values[0])
			case "pageSize":
				in.PageSize, err = modelDevPageSizePointer(values[0])
			case "state":
				in.State, err = modelDevDatasetStatePointer(values[0])
			default:
				return invalidModelDevDatasetHTTP()
			}
			if err != nil {
				return err
			}
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.ListDatasetImportTasks(c, in) })
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	route.GET(data.ModelDevGetDatasetImportTaskPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceGetDatasetImportTask)
		id, ok := datasetPathID(ctx, "import_task_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		if raw, err := io.ReadAll(io.LimitReader(ctx.Request().Body, 1)); err != nil || len(raw) != 0 || ctx.Request().URL.RawQuery != "" {
			return invalidModelDevDatasetHTTP()
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) {
			return modeldev.GetDatasetImportTask(c, &modeldevv1.GetDatasetImportTaskRequest{ImportTaskId: id})
		})
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})

	for _, command := range []struct {
		path      string
		operation string
		retry     bool
	}{
		{data.ModelDevCancelDatasetImportPath, adminv1.OperationModelDevServiceCancelDatasetImport, false},
		{data.ModelDevRetryDatasetImportPath, adminv1.OperationModelDevServiceRetryDatasetImport, true},
	} {
		command := command
		route.POST(command.path, func(ctx khttp.Context) error {
			modelDevQueryHeaders(ctx)
			stripModelDevDatasetCallerHeaders(ctx.Request())
			khttp.SetOperation(ctx, command.operation)
			id, ok := datasetPathID(ctx, "import_task_id")
			if !ok {
				return invalidModelDevDatasetHTTP()
			}
			fields, ok := decodeModelDevDatasetBody(ctx.Request(), 4096)
			if !ok || len(fields) != 1 || fields["idempotency_key"] == nil {
				return invalidModelDevDatasetHTTP()
			}
			var key string
			if json.Unmarshal(fields["idempotency_key"], &key) != nil {
				return invalidModelDevDatasetHTTP()
			}
			out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) {
				if command.retry {
					return modeldev.RetryDatasetImport(c, &modeldevv1.RetryDatasetImportRequest{ImportTaskId: id, IdempotencyKey: key})
				}
				return modeldev.CancelDatasetImport(c, &modeldevv1.CancelDatasetImportRequest{ImportTaskId: id, IdempotencyKey: key})
			})
			if err != nil {
				return err
			}
			return ctx.Result(nethttp.StatusAccepted, out)
		})
	}

	route.GET(data.ModelDevListDatasetOperationsPath, func(ctx khttp.Context) error {
		modelDevQueryHeaders(ctx)
		khttp.SetOperation(ctx, adminv1.OperationModelDevServiceListDatasetOperations)
		id, ok := datasetPathID(ctx, "dataset_id")
		if !ok {
			return invalidModelDevDatasetHTTP()
		}
		in := &modeldevv1.ListDatasetOperationsRequest{DatasetId: id}
		query, err := modelDevBoundedQuery(ctx.Request())
		if err != nil {
			return err
		}
		for name, values := range query {
			switch name {
			case "page":
				in.Page, err = modelDevPagePointer(values[0])
			case "pageSize":
				in.PageSize, err = modelDevPageSizePointer(values[0])
			case "event_type":
				in.EventType, err = modelDevDatasetStatePointer(values[0])
			case "import_task_id":
				in.ImportTaskId, err = modelDevQueryUUIDPointer(values[0])
			default:
				return invalidModelDevDatasetHTTP()
			}
			if err != nil {
				return err
			}
		}
		out, err := callModelDevDataset(ctx, func(c context.Context) (interface{}, error) { return modeldev.ListDatasetOperations(c, in) })
		if err != nil {
			return err
		}
		return ctx.Result(nethttp.StatusOK, out)
	})
}

// callModelDevDataset runs one handler through the server middleware chain, so
// authentication, tenant access and audit apply exactly as they do to the
// generated routes.
func callModelDevDataset(ctx khttp.Context, call func(context.Context) (interface{}, error)) (interface{}, error) {
	handler := ctx.Middleware(func(c context.Context, _ interface{}) (interface{}, error) {
		return call(c)
	})
	return handler(ctx, nil)
}

// modelDevDatasetStatePointer accepts one upper-case enum member name without
// its prefix, matching the vocabulary every other ModelDev list returns.
func modelDevDatasetStatePointer(value string) (*string, error) {
	return modelDevStatePointer(value)
}
