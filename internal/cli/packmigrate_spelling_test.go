package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RC-D69 through the verb: a pack.toml still writing `[[provider]] name =` and a `[[profile]]` is
// rewritten in place, each fold said, and the next step named.
func TestPackMigrateRewritesALegacyProviderSpellingInPlace(t *testing.T) {
	newMigrateEnv(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "fixture-pack")
	writeMigrateFile(t, filepath.Join(dir, "pack.toml"), "name = \"fixture\"\n\n[[provider]]\nname = \"anthropic\"\nplatform = \"anthropic\"\n\n[[profile]]\nname = \"claude\"\nprovider = \"anthropic\"\n")

	rc, out, errw := runPackMigrateVerb(t, dir, "--dry-run")
	if rc != 0 || !strings.Contains(out, "rewritten in place") ||
		!strings.Contains(out, `Folded: [[profile]] "claude" is now an alias of [providers.anthropic].`) {
		t.Fatalf("--dry-run: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	rc, out, errw = runPackMigrateVerb(t, dir, "--yes")
	if rc != 0 || !strings.Contains(out, "`yolo pack lint "+dir+"`") {
		t.Fatalf("--yes: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pack.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "name = \"fixture\"\n\n[providers.anthropic]\nalias = \"claude\"\nplatform = \"anthropic\"\n"; string(data) != want {
		t.Fatalf("pack.toml:\n%s\nwant:\n%s", data, want)
	}
}
