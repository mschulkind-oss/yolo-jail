package cli

// configsetpacksources_test.go pins the explicit-edit validator's knowledge of `[pack_sources]`
// (pack-naming review Q5): before the fix, any `yolo config set` on a user config holding a
// `[pack_sources]` table was refused as carrying an unknown key, so every generic edit of a
// converted config failed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

func TestConfigSetEditsAUserConfigThatHasPackSources(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "x", "pack.json"), []byte(`{"contributes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	user, ws := genericFixture(t, "# keep user\npacks = [\"work/x\"]\n\n[pack_sources]\nwork = { path = '"+
		filepath.ToSlash(src)+"', bare = true }\n", configdoc.TOML, "# keep project\n")
	// The write is the claim; the post-write check that follows reports this machine, not the edit.
	rc, out, errs := runConfigVerb(t, "set", "update_check", "false", "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	if !strings.Contains(out, "Written") {
		t.Fatalf("an edit of a config with [pack_sources] was refused: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := assertValidFile(t, user.Path, configdoc.TOML, "# keep user", ws.Root)
	if !strings.Contains(got, "update_check = false") || !strings.Contains(got, "[pack_sources]") {
		t.Fatalf("the edit did not land beside the sources:\n%s", got)
	}

	// And an edit INSIDE the table is validated as one, not refused as unknown.
	rc, out, errs = runConfigVerb(t, "set", "pack_sources.work.bare", "false", "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	if !strings.Contains(out, "Written") || !strings.Contains(readFile(t, user.Path), "bare = false") {
		t.Fatalf("an edit of pack_sources itself was refused: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}
