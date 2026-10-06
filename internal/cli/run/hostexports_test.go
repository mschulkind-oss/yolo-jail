package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAssembleNeverMountsTheHostNotchsOwnStores pins the property paths.go claims for the three
// stores `yolo host --` keeps (HE-D11, WS-D23, CL-D27): the blocked-tool shims it runs with the
// user's authority, the records that make a workspace link yolo's, and the managed Claude store the
// host broker writes a login into. A jail that could write any of them could plant a script the
// host runs, claim a repository's path as yolo's, or hand host Claude a credential of its choosing,
// so no launch may bind one, anything under one, or an ancestor carrying one in.
func TestAssembleNeverMountsTheHostNotchsOwnStores(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	guarded := map[string]string{
		paths.HostBlockDir():              "the host launch's blocked-tool shims",
		paths.HostWorkspaceSkillsDir():    "the host's workspace skills records",
		paths.HostAgentStoreDir("claude"): "the host's managed Claude store",
	}
	for dir := range guarded {
		if !strings.HasPrefix(dir, paths.GlobalStorageUnder(home)+string(filepath.Separator)) {
			t.Fatalf("%s is not under this test's state dir; the assertion would be vacuous", dir)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "unknown",
		mountTargets: map[string]struct{}{},
	})
	under := func(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) }
	sawState := false
	for _, m := range mountSources(argv) {
		if m.src == "" || !under(m.src, home) {
			continue
		}
		if under(m.src, paths.GlobalStorageUnder(home)) {
			sawState = true
		}
		for dir, what := range guarded {
			if under(m.src, dir) || under(dir, m.src) {
				t.Errorf("the launch bind-mounts %s (ro=%v), which reaches %s %s", m.src, m.ro, what, dir)
			}
		}
	}
	if !sawState {
		t.Fatalf("no mount under the state dir was parsed out of the argv — the assertion is vacuous")
	}
}
