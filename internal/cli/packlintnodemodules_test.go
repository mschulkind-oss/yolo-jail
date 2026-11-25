package cli

// A dangling link inside node_modules no longer aborts `yolo pack lint` or `yolo pack series
// check` (requests #29); both pass and NAME the skipped tree, and a broken link outside it still
// refuses.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackLintSkipsNodeModulesAndNamesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "probe")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"probe","contributes":[{"kind":"briefing"}]}`)
	writeFile(t, filepath.Join(dir, "briefing", "note.md"), "hello\n")
	nm := filepath.Join(dir, "sub", "node_modules", ".pnpm", "pkg")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(nm, "broken")); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	rc := packMain([]string{"lint", dir}, &out, &errw, false)
	got := out.String() + errw.String()
	if rc != 0 || !strings.Contains(got, "pack ok") || !strings.Contains(got, "sub/node_modules") {
		t.Fatalf("lint = %d, want ok naming the skipped sub/node_modules:\n%s", rc, got)
	}

	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "sub", "broken")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("a broken link outside node_modules passed lint:\n%s%s", out.String(), errw.String())
	}
}
