package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// hostOnlyPackFixture is a user config that selects a local pack whose directory exists only on
// the host (a jail never mounts ~/.dotfiles), with a workspace a `yolo request` writes into.
// It also selects a fetched pack, whose tree is in the host's pack store, which a jail has none of.
// staged, when set, is the copies the launch delivered under YOLO_PACK_ROOT, as a real jail has.
func hostOnlyPackFixture(t *testing.T, inJail, staged bool) namedVerbFixture {
	t.Helper()
	ws := testconfig.Workspace(t, "")
	// Legacy JSONC, as the reporter's user config is: its bare-path spelling of a local pack.
	user := testconfig.UserFormat(t, configdoc.JSONC, `{
  "packs": ["git", "github", "~/.dotfiles/yolo-packs/hostonly", "git+https://example.invalid/acme/craft?ref=main"],
  "allow": {"github": {"always": ["read"], "on_request": ["dispatch_workflows"]}}
}
`)
	if _, err := os.Stat(filepath.Join(user.Root, ".dotfiles", "yolo-packs", "hostonly")); !os.IsNotExist(err) {
		t.Fatalf("the host-only pack directory must be absent: %v", err)
	}
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_HOST_DIR", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv(config.UserLayerEnv, "")
	// In a jail the verb edits the jail's OWN workspace, /workspace unless YOLO_WORKSPACE names
	// another, whatever the cwd. Point it at the temporary workspace, and refuse to run if it
	// does not, so a jail running this test never edits its real workspace config.
	t.Setenv("YOLO_WORKSPACE", ws.Root)
	if inJail {
		t.Setenv("YOLO_VERSION", "0.0.0-test")
		if config.JailWorkspace() != ws.Root || !config.IsJailOwnWorkspace(ws.Root) {
			t.Fatalf("the jail's own workspace is %s, not the temporary %s", config.JailWorkspace(), ws.Root)
		}
	} else {
		t.Setenv("YOLO_VERSION", "")
	}
	if staged {
		root := t.TempDir()
		for _, name := range []string{"hostonly", "craft"} {
			dir := filepath.Join(root, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "pack.toml"), []byte("description = \"delivered copy\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("YOLO_PACK_ROOT", root)
	}
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)
	return namedVerbFixture{workspace: ws.Root, userConfig: user.Path, project: ws.Path}
}

func requestDispatch(t *testing.T) (int, string, string) {
	t.Helper()
	return captureStd(t, func() int {
		return registry["request"]([]string{"request", "github", "dispatch_workflows", "x.yml"})
	})
}

// In a jail, a local pack whose source is a host path the jail cannot see must not refuse a
// workspace-config edit that does not touch packs: the edit validates against the copy the
// launch delivered, as `yolo check` in the jail does.
func TestRequestInJailToleratesHostOnlyLocalPackWithDeliveredCopy(t *testing.T) {
	fx := hostOnlyPackFixture(t, true, true)
	rc, out, errs := requestDispatch(t)
	if strings.Contains(errs, "config edit refused") {
		t.Fatalf("in-jail request refused over a host-only pack: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	assertPublicPostWriteCheck(t, rc, out, errs)
	if got := readFile(t, fx.project); !strings.Contains(got, `dispatch_workflows = ["x.yml"]`) {
		t.Fatalf("project file lacks the grant:\n%s\nstdout=%q stderr=%q", got, out, errs)
	}
	// The delivered copies were read and checked, not stepped around.
	if strings.Contains(errs, "was not checked") {
		t.Fatalf("a delivered pack was reported unchecked: stderr=%q", errs)
	}
}

// With no delivered copy either, the pack cannot be checked from the jail at all; the edit still
// lands, and says the pack's declarations went unchecked and where they are checked.
func TestRequestInJailToleratesHostOnlyLocalPackWithNoCopy(t *testing.T) {
	fx := hostOnlyPackFixture(t, true, false)
	rc, out, errs := requestDispatch(t)
	if strings.Contains(errs, "config edit refused") {
		t.Fatalf("in-jail request refused over a host-only pack: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, fx.project); !strings.Contains(got, `dispatch_workflows = ["x.yml"]`) {
		t.Fatalf("project file lacks the grant:\n%s\nstdout=%q stderr=%q", got, out, errs)
	}
	if !strings.Contains(errs, "pack hostonly was not checked") || !strings.Contains(errs, "pack craft was not checked") ||
		!strings.Contains(errs, "`yolo check` on the host") {
		t.Fatalf("the edit did not say the host-only pack went unchecked: stdout=%q stderr=%q", out, errs)
	}
}

// On the host the pack's directory is visible, so a missing one is a real fault and the edit is
// still refused before anything is written.
func TestRequestOnHostRefusesMissingLocalPack(t *testing.T) {
	fx := hostOnlyPackFixture(t, false, false)
	before := readFile(t, fx.project)
	rc, out, errs := requestDispatch(t)
	if rc == 0 || !strings.Contains(errs, "config edit refused") || !strings.Contains(errs, "hostonly") ||
		!strings.Contains(errs, "yolo pack remove hostonly") {
		t.Fatalf("host request with a missing local pack was not refused: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if readFile(t, fx.project) != before {
		t.Fatal("a refused edit wrote the project file")
	}
}
