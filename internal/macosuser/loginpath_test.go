package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// loginpath_test.go pins the export half of the login-rc indirection.
//
// The rc files entrypoint.WriteLoginRC generates re-prepend $YOLO_DARWIN_LOGIN_PATH rather
// than a baked value, because they sit at the root of a home every workspace shares. That
// makes this export load-bearing in a way it was not before: without it the rc files are
// inert, macOS path_helper's order stands, and a `packages:` tool loses to a Homebrew one
// of the same name in any login shell — silently, since the non-login launch PATH is fine.
func TestLaunchArgvExportsThePathTheLoginRCReadsBack(t *testing.T) {
	store := []string{"/nix/store/abc-env/bin"}
	argv := LaunchArgv([]string{"claude"}, "/var/yolo-jail/p.sb", jsonx.NewOrderedMap(),
		"/Users/Shared/yolo/proj", "", "", store)
	want := SandboxPath(SandboxHome(), store)

	var pathVal, loginVal string
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, "PATH="); ok {
			pathVal = v
		}
		if v, ok := strings.CutPrefix(a, entrypoint.DarwinLoginPathEnv+"="); ok {
			loginVal = v
		}
	}
	if loginVal == "" {
		t.Fatalf("the launch exports no %s, so the generated login rc files re-prepend "+
			"nothing and path_helper's order wins:\n%v", entrypoint.DarwinLoginPathEnv, argv)
	}
	// The SAME value as PATH, from the same call: the rc is restoring an order, and a
	// second opinion about what that order is would restore the wrong one.
	if loginVal != want || pathVal != want {
		t.Errorf("PATH=%q and %s=%q, want both to be SandboxPath's %q",
			pathVal, entrypoint.DarwinLoginPathEnv, loginVal, want)
	}
}
