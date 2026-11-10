package service

import (
	"strings"
	"testing"

	modeldevv1 "go-wind-admin/api/gen/go/modeldev/service/v1"
)

// The request validators are the second half of the contract: the HTTP adapter
// proves the JSON shape, and these prove the semantic rules the adapter cannot
// know (which mask fields exist, which enum members exist, which provider
// strings map onto the owner enum). Both layers must reject the same inputs.

func ptr(value string) *string { return &value }

func TestModelDevDatasetCreateInputIsStrict(t *testing.T) {
	valid := &modeldevv1.CreateDatasetRequest{IdempotencyKey: "k-1", Data: &modeldevv1.CreateDatasetData{Name: "客服问答"}}
	if _, _, _, ok := modelDevDatasetCreateInput(valid); !ok {
		t.Fatal("a valid create was rejected")
	}
	cases := map[string]*modeldevv1.CreateDatasetRequest{
		"nil request":            nil,
		"nil data":               {IdempotencyKey: "k-1"},
		"no key":                 {Data: &modeldevv1.CreateDatasetData{Name: "s"}},
		"key with a slash":       {IdempotencyKey: "k/1", Data: &modeldevv1.CreateDatasetData{Name: "s"}},
		"key starting with dash": {IdempotencyKey: "-k", Data: &modeldevv1.CreateDatasetData{Name: "s"}},
		"empty name":             {IdempotencyKey: "k-1", Data: &modeldevv1.CreateDatasetData{Name: ""}},
		"name with a newline":    {IdempotencyKey: "k-1", Data: &modeldevv1.CreateDatasetData{Name: "a\nb"}},
		"name too long":          {IdempotencyKey: "k-1", Data: &modeldevv1.CreateDatasetData{Name: strings.Repeat("名", 129)}},
		"description with a tab": {IdempotencyKey: "k-1", Data: &modeldevv1.CreateDatasetData{Name: "s", Description: ptr("a\tb")}},
	}
	for name, request := range cases {
		if _, _, _, ok := modelDevDatasetCreateInput(request); ok {
			t.Errorf("%s was accepted", name)
		}
	}
	// A description may contain newlines: it is plain text, not a single line.
	withNewline := &modeldevv1.CreateDatasetRequest{IdempotencyKey: "k-1", Data: &modeldevv1.CreateDatasetData{Name: "s", Description: ptr("第一行\n第二行")}}
	if _, _, _, ok := modelDevDatasetCreateInput(withNewline); !ok {
		t.Error("a multi-line description was rejected")
	}
}

func TestModelDevDatasetUpdateMaskMustMatchTheEditableFields(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	base := func(mask string) *modeldevv1.UpdateDatasetRequest {
		return &modeldevv1.UpdateDatasetRequest{
			DatasetId: id, IdempotencyKey: "k-2", UpdateMask: mask,
			Data: &modeldevv1.UpdateDatasetData{Name: ptr("s2"), ExpectedRevision: "3"},
		}
	}
	if _, _, expected, _, ok := modelDevDatasetUpdateInput(base("name")); !ok || expected != 3 {
		t.Fatalf("a valid rename was rejected: ok=%v expected=%d", ok, expected)
	}
	for name, mask := range map[string]string{
		"empty mask":      "",
		"duplicate field": "name,name",
		"read-only field": "revision",
		"field not sent":  "description",
		"no fields":       ",",
	} {
		if _, _, _, _, ok := modelDevDatasetUpdateInput(base(mask)); ok {
			t.Errorf("%s was accepted", name)
		}
	}
	// expected_revision must be the canonical decimal spelling.
	for _, revision := range []string{"", "0", "-1", "03", "3.0"} {
		request := base("name")
		request.Data.ExpectedRevision = revision
		if _, _, _, _, ok := modelDevDatasetUpdateInput(request); ok {
			t.Errorf("expected_revision %q was accepted", revision)
		}
	}
	// A mask naming a field whose value is absent, or a value with no mask
	// entry, is a mismatch rather than a partial update.
	mismatch := base("name")
	mismatch.Data.Name = nil
	mismatch.Data.Description = ptr("d")
	if _, _, _, _, ok := modelDevDatasetUpdateInput(mismatch); ok {
		t.Error("a mask that does not match the fields sent was accepted")
	}
}

func TestModelDevDatasetListFilterVocabulary(t *testing.T) {
	if state, _, ok := modelDevDatasetListFilter(ptr("ALL"), nil); !ok || state != nil {
		t.Errorf("ALL must widen to no filter: state=%v ok=%v", state, ok)
	}
	if state, _, ok := modelDevDatasetListFilter(ptr("ARCHIVED"), nil); !ok || state == nil || *state != "ARCHIVED" {
		t.Error("ARCHIVED must stay a filter")
	}
	for _, value := range []string{"", "archived", "DELETED", "UNSPECIFIED"} {
		if _, _, ok := modelDevDatasetListFilter(ptr(value), nil); ok {
			t.Errorf("state %q was accepted", value)
		}
	}
	if _, ok := modelDevDatasetStateFilter(ptr("RUNNING")); !ok {
		t.Error("RUNNING must be a task filter")
	}
	for _, value := range []string{"running", "DONE", ""} {
		if _, ok := modelDevDatasetStateFilter(ptr(value)); ok {
			t.Errorf("task state %q was accepted", value)
		}
	}
	for _, value := range []string{"DATASET_CREATED", "IMPORT_SUCCEEDED", "IMPORT_CANCEL_REQUESTED"} {
		if _, ok := modelDevDatasetOperationTypeFilter(ptr(value)); !ok {
			t.Errorf("event type %q was rejected", value)
		}
	}
	for _, value := range []string{"UNSPECIFIED", "dataset_created", "CREATED"} {
		if _, ok := modelDevDatasetOperationTypeFilter(ptr(value)); ok {
			t.Errorf("event type %q was accepted", value)
		}
	}
}

func TestModelDevInspectSourceKeepsTheTwoShapesApart(t *testing.T) {
	list := &modeldevv1.InspectDatasetSourceRequest{Data: &modeldevv1.InspectDatasetSourceData{
		Mode: "LIST_FILES", Source: &modeldevv1.DatasetSourceRef{Provider: "HUGGINGFACE", RepoId: "a/b", Revision: "main"},
	}}
	out, ok := modelDevInspectSourceRequest(list)
	if !ok || out.GetListFiles() == nil {
		t.Fatal("a valid LIST_FILES was rejected")
	}
	// file_path belongs to INSPECT_FILE only.
	mixed := &modeldevv1.InspectDatasetSourceRequest{Data: &modeldevv1.InspectDatasetSourceData{
		Mode: "LIST_FILES", Source: &modeldevv1.DatasetSourceRef{Provider: "HUGGINGFACE", RepoId: "a/b", Revision: "main", FilePath: ptr("x.json")},
	}}
	if _, ok := modelDevInspectSourceRequest(mixed); ok {
		t.Error("LIST_FILES with file_path was accepted")
	}
	inspect := &modeldevv1.InspectDatasetSourceRequest{Data: &modeldevv1.InspectDatasetSourceData{
		Mode: "INSPECT_FILE", Source: &modeldevv1.DatasetSourceRef{Provider: "MODELSCOPE", RepoId: "a/b", Revision: "main", FilePath: ptr("x.json")},
	}}
	out, ok = modelDevInspectSourceRequest(inspect)
	if !ok || out.GetInspectFile() == nil || out.GetInspectFile().FilePath != "x.json" {
		t.Fatal("a valid INSPECT_FILE was rejected")
	}
	// Paging fields belong to LIST_FILES only.
	paged := &modeldevv1.InspectDatasetSourceRequest{Data: &modeldevv1.InspectDatasetSourceData{
		Mode: "INSPECT_FILE", Source: inspect.Data.Source, PageSize: ptr("20"),
	}}
	if _, ok := modelDevInspectSourceRequest(paged); ok {
		t.Error("INSPECT_FILE with a page size was accepted")
	}
	for name, mode := range map[string]string{"missing mode": "", "unknown mode": "LIST", "lowercase mode": "list_files"} {
		request := &modeldevv1.InspectDatasetSourceRequest{Data: &modeldevv1.InspectDatasetSourceData{
			Mode: mode, Source: &modeldevv1.DatasetSourceRef{Provider: "HUGGINGFACE", RepoId: "a/b", Revision: "main"},
		}}
		if _, ok := modelDevInspectSourceRequest(request); ok {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, ok := modelDevInspectSourceRequest(&modeldevv1.InspectDatasetSourceRequest{Data: &modeldevv1.InspectDatasetSourceData{
		Mode: "LIST_FILES", Source: &modeldevv1.DatasetSourceRef{Provider: "OTHER", RepoId: "a/b", Revision: "main"},
	}}); ok {
		t.Error("an unknown provider was accepted")
	}
}

func TestModelDevImportRequiresOneCompleteLockShape(t *testing.T) {
	fingerprint := strings.Repeat("ab", 32)
	base := func() *modeldevv1.ImportDatasetVersionRequest {
		return &modeldevv1.ImportDatasetVersionRequest{
			DatasetId: "11111111-2222-4333-8444-555555555555", IdempotencyKey: "k-5",
			Data: &modeldevv1.ImportDatasetVersionData{
				SelectionFingerprint: fingerprint,
				Source: &modeldevv1.DatasetSourceSelectionView{
					Provider: "HUGGINGFACE", RepoId: "a/b", RequestedRevision: "main", FilePath: "x.json",
					LockKind: "IMMUTABLE_REVISION", ResolvedRevision: "abc123", ExpectedSizeBytes: 1024,
				},
			},
		}
	}
	if _, ok := modelDevImportRequest(base()); !ok {
		t.Fatal("a valid immutable-revision import was rejected")
	}
	sha := base()
	sha.Data.Source.LockKind = "FILE_SHA256"
	sha.Data.Source.ResolvedRevision = ""
	sha.Data.Source.ExpectedFileSha256 = strings.Repeat("cd", 32)
	if _, ok := modelDevImportRequest(sha); !ok {
		t.Fatal("a valid file-sha import was rejected")
	}
	// Each lock kind requires its own facts and forbids the other's.
	both := sha
	both.Data.Source.ResolvedRevision = "abc123"
	if _, ok := modelDevImportRequest(both); ok {
		t.Error("FILE_SHA256 carrying a resolved revision was accepted")
	}
	inconsistent := base()
	inconsistent.Data.Source.ExpectedFileSha256 = strings.Repeat("cd", 32)
	if _, ok := modelDevImportRequest(inconsistent); ok {
		t.Error("IMMUTABLE_REVISION carrying a file digest was accepted")
	}
	noSize := base()
	noSize.Data.Source.ExpectedSizeBytes = 0
	if _, ok := modelDevImportRequest(noSize); ok {
		t.Error("an import without a proven size was accepted")
	}
	badFingerprint := base()
	badFingerprint.Data.SelectionFingerprint = strings.ToUpper(fingerprint)
	if _, ok := modelDevImportRequest(badFingerprint); ok {
		t.Error("an upper-case fingerprint was accepted")
	}
	unknownLock := base()
	unknownLock.Data.Source.LockKind = "BRANCH"
	if _, ok := modelDevImportRequest(unknownLock); ok {
		t.Error("an unknown lock kind was accepted")
	}
}
