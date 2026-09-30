package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(c Catalog) []string {
	var out []string
	for id := range c.IDs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Every `id` string at any depth of every matched file is a known id, one segment matched at a
// time: pi 0.99.1's shape, {"<api>": {"chat:<id>": {"id": "<id>", …}}}, and a list of objects.
func TestReadCollectsEveryIDTheMatchedFilesName(t *testing.T) {
	pkg := t.TempDir()
	writeFile(t, filepath.Join(pkg, "package.json"), `{"name": "agent", "version": "1.2.3"}`)
	data := filepath.Join(pkg, "node_modules", "@s", "ai", "dist", "providers", "data")
	writeFile(t, filepath.Join(data, "zai.json"),
		`{"openai-completions": {"chat:glm-5.3": {"id": "glm-5.3", "compat": {"x": 1}}}}`)
	writeFile(t, filepath.Join(data, "list.json"), `[{"id": "alpha-1"}, {"nested": [{"id": "beta-2"}]}]`)
	writeFile(t, filepath.Join(data, "notes.txt"), `{"id": "not-a-catalog-file"}`)
	writeFile(t, filepath.Join(data, "broken.json"), `{"id": `)
	c := Read(pkg, []string{"node_modules/@s/ai/dist/providers/data/*.json"})
	if want := []string{"alpha-1", "beta-2", "glm-5.3"}; !reflect.DeepEqual(ids(c), want) {
		t.Errorf("ids = %v, want %v (only matched files, a broken one skipped)", ids(c), want)
	}
	if c.Files != 2 {
		t.Errorf("Files = %d, want 2: the broken file does not parse and is not counted", c.Files)
	}
	if !Installed(pkg) || Version(pkg) != "1.2.3" {
		t.Errorf("Installed = %v, Version = %q, want true and 1.2.3", Installed(pkg), Version(pkg))
	}
}

// A glob that matches nothing is a catalog that could not be read: zero files, whatever else is
// on disk, which is what tells "could not ask" from "not found".
func TestReadOfAMovedCatalogReadsNothing(t *testing.T) {
	pkg := t.TempDir()
	writeFile(t, filepath.Join(pkg, "dist", "models.generated.js"), `export const M = {id: "x"}`)
	c := Read(pkg, []string{"node_modules/@s/ai/dist/providers/data/*.json"})
	if c.Files != 0 || len(c.IDs) != 0 {
		t.Errorf("a glob matching nothing read %d files and %v", c.Files, ids(c))
	}
	if Installed(filepath.Join(pkg, "absent")) {
		t.Error("a directory with no package.json is not an installed package")
	}
}

// A pattern character in the package directory's own path is never read as one: only the
// declared segments are patterns.
func TestReadMatchesOnlyTheDeclaredSegments(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "odd[dir]")
	writeFile(t, filepath.Join(pkg, "data", "a.json"), `{"id": "one"}`)
	if got := ids(Read(pkg, []string{"data/*.json"})); !reflect.DeepEqual(got, []string{"one"}) {
		t.Errorf("ids = %v, want [one]", got)
	}
}
