package buf

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestScanYAMLFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"a.gen.yaml":                       "version: v1\n",
		"b.GEN.YAML":                       "version: v1\n",
		"c.yaml":                           "version: v1\n",
		"gen.yaml.txt":                     "not yaml\n",
		filepath.Join("sub", "d.gen.yaml"): "version: v1\n",
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := scanYAMLFiles(root)
	if err != nil {
		t.Fatalf("scanYAMLFiles: %v", err)
	}
	sort.Strings(got)
	want := []string{
		filepath.Join(root, "a.gen.yaml"),
		filepath.Join(root, "b.GEN.YAML"),
		filepath.Join(root, "sub", "d.gen.yaml"),
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("scanYAMLFiles = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scanYAMLFiles[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}

func TestSelectActiveTemplatesRejectsMissingDuplicateAndFormerModel(t *testing.T) {
	root := t.TempDir()
	found := make([]string, 0, len(activeGenConfigs))
	for _, name := range activeGenConfigs {
		found = append(found, filepath.Join(root, name))
	}
	selected, err := selectActiveTemplates(root, found)
	if err != nil || len(selected) != len(found) {
		t.Fatalf("active templates: %v, %v", selected, err)
	}
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"missing", found[:len(found)-1], "approved template is missing"},
		{"duplicate", append(append([]string{}, found...), filepath.Join(root, "nested", activeGenConfigs[0])), "duplicate template name"},
		{"former-model", append(append([]string{}, found...), filepath.Join(root, "buf.model.gen.yaml")), "unclassified template"},
		{"unknown", append(append([]string{}, found...), filepath.Join(root, "buf.unknown.gen.yaml")), "unclassified template"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := selectActiveTemplates(root, tc.files); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestRepositoryGenerationTemplatesAreAccepted(t *testing.T) {
	api := filepath.Join(repoRoot(t), "api")
	found, err := scanYAMLFiles(api)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selectActiveTemplates(api, found); err != nil {
		t.Fatalf("repository API generation is blocked: %v", err)
	}
}

func TestScanYAMLFilesMissingRoot(t *testing.T) {
	got, err := scanYAMLFiles(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("scanYAMLFiles on missing root: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("scanYAMLFiles on missing root = %v; want empty", got)
	}
}

func TestBufConfigAndLockExistence(t *testing.T) {
	dir := t.TempDir()

	if isBufConfigExists(dir) || isBufLockExists(dir) {
		t.Fatal("fresh directory must have neither buf.yaml nor buf.lock")
	}

	for _, name := range []string{defaultBufConfigFile, bufLockFile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("version: v2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !isBufConfigExists(dir) || !isBufLockExists(dir) {
		t.Fatal("both files must be reported as present")
	}

	// 同名目录不算文件存在。
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, defaultBufConfigFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if isBufConfigExists(dir2) {
		t.Fatal("a directory named buf.yaml must not count as the config file")
	}
}
