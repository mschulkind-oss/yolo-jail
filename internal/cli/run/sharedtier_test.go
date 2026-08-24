package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE TWO HOME TIERS MUST REACH EVERY CONTAINER BACKEND, and this test is driven by
// packload.SharedDirs rather than by a literal list so a pack adding a machine-wide dir
// tomorrow is covered without anyone remembering to come back here.
//
// The bug it pins (issue #39): packload.SharedDirs was called once in the whole run
// package, inside the podman `else` branch of assembleRunCmd, so Apple Container mounted
// nothing for it. Nothing errored — the entrypoint's shared_credentials hook MkdirAlls
// the directory (packhooks.go) and the symlink resolves — so the failure was a silent
// DEGRADATION: ~/.claude-shared-credentials lived in wsState, cross-jail credential
// sharing quietly became per-workspace, and every new workspace demanded a fresh /login.
//
// WHY THIS SHAPE OF TEST. The per-backend argv is assembled by two functions that do not
// share a list, so "is this mechanism on both branches" is not checkable by reading
// either one. The assertion is therefore about the OUTCOME both branches owe — a mount
// whose HOST side is under GlobalHome — which is what makes it fail for a mechanism
// added to the podman branch alone. A test that hardcoded the container-side path would
// pass on the buggy code the moment AC mounted the same destination out of wsState,
// which is exactly the wrong thing to be relaxed about: the destination was never the
// bug, the source tier was.
func TestSharedDirsAreMountedFromGlobalHomeOnEveryContainerBackend(t *testing.T) {
	shared := packload.SharedDirs(claudePackFixture(t))
	if len(shared) == 0 {
		t.Fatal("fixture declares no shared dirs — this test would pass vacuously; " +
			"the official claude pack is supposed to declare .claude-shared-credentials")
	}

	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			ws := t.TempDir()
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions(ws, home)
			// Apple Container is a macOS-only runtime; podman is exercised on its
			// Linux shape. Both read the same SharedDirs list, which is the point.
			o.IsMacOS = rt == "container"
			o.IsLinux = !o.IsMacOS

			wsState := filepath.Join(ws, ".yolo", "home")
			if err := os.MkdirAll(wsState, 0o755); err != nil {
				t.Fatal(err)
			}
			sec := jsonx.NewOrderedMap()
			sec.Set("blocked_tools", []any{})

			in := &assembleInput{
				cfg:          newConfig("security", sec),
				rt:           rt,
				cname:        "yolo-ws-abcd1234",
				packs:        claudePackFixture(t),
				agentsPath:   filepath.Join(ws, "agents"),
				wsState:      wsState,
				miseStore:    "/mise-store",
				yoloVersion:  "9.9.9-test",
				mountTargets: map[string]struct{}{},
			}
			argv := o.assembleRunCmd(in)

			for _, dir := range shared {
				want := filepath.Join(paths.GlobalHome(), dir) + ":/home/agent/" + dir
				if !hasMountArg(argv, want) {
					t.Errorf("runtime %q does not mount the machine-wide dir %q from GlobalHome.\n"+
						"want a -v of: %s\n"+
						"This is issue #39: the dir still WORKS (the entrypoint creates it and the\n"+
						"symlink resolves) but it lands in the per-workspace tier, so a credential\n"+
						"stops being shared across workspaces and nothing says so.\n"+
						"argv: %s", rt, dir, want, strings.Join(argv, " "))
				}
			}
		})
	}
}

// hasMountArg reports whether argv contains `-v <spec>` as an adjacent pair, rather than
// the spec merely appearing somewhere. A substring match over the joined argv would also
// accept the spec inside an unrelated flag's value.
func hasMountArg(argv []string, spec string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && argv[i+1] == spec {
			return true
		}
	}
	return false
}
