package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A project's request outside the user's ceiling is skipped and named by `yolo check`
// (docs/reference/loophole-grants.md §5): the request at its line, that the project runs
// without it, and the one command that allows it. The fixture `journal` pack declares `read`
// requestable by default and `read_all` not, as the journal will once it is flipped; the user
// config selects it and has no `[allow.journal]`. Deleting the validator's call to the grant
// reader, or its skipped-request warning, fails this.
func TestARequestOutsideTheCeilingIsSkippedAndNamed(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	// The fixture is the shipped set, so `packs = ['journal']` selects it by its bare name.
	shipped := packload.EmbeddedFS()
	packload.ReleaseEmbedded()
	t.Cleanup(func() {
		packload.ReleaseEmbedded()
		packload.SetEmbeddedFS(shipped)
	})
	packload.SetEmbeddedFS(fstest.MapFS{
		"journal/pack.toml": {Data: []byte("name = \"journal\"\n\n[[loophole]]\nfrom = \"loopholes/journal\"\n")},
		"journal/loopholes/journal/loophole.toml": {Data: []byte(`name = "journal"
description = "a grant-declaring fixture"
version = 1
transport = "loopback-tls"
access = "read the host's journal"

[host_daemon]
cmd = ["yolo", "internal", "daemon", "journal", "--socket", "{socket}"]
publishes = "socket"

[[grants]]
kind = "read"
text = "read your user journal"
requestable_by_default = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
`)},
	})
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, paths.UserConfigTOMLName), []byte("packs = ['journal']\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := runCheckOverStandardWorkspaceConfig(t, workspace, paths.WorkspaceConfigTOMLName,
		"[requests.journal]\nread = true\nread_all = true\n", false)
	for _, want := range []string{
		paths.WorkspaceConfigTOMLName + ":3: this project requests journal read_all.",
		"Your config does not allow read_all on request, so this project runs without it.",
		"To allow it for every project you work on (in your config, not this file):",
		"yolo allow journal on_request read_all",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("yolo check does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "requests journal read.") {
		t.Errorf("read is requestable by default, so its request is not skipped:\n%s", out)
	}
}
