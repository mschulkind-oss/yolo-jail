package run

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE CLASS, not the instance. Issue #39 was one mechanism (pack SharedDirs) reaching
// podman and not Apple Container. sharedtier_test.go pins that mechanism; this pins the
// SHAPE, so the next one fails here instead of in a user's jail.
//
// The invariant: every mount whose HOST side is under the machine-wide store and whose
// CONTAINER side is nested below /home/agent must be emitted by both container backends.
// That is the tier that cannot be recovered any other way — a per-workspace path is
// already covered on Apple Container by its single wsState → /home/agent bind, which is
// why WritableDirs needs no AC handling and SharedDirs does.
//
// WHY A DIFF RATHER THAN A LIST. A test that enumerated the expected mounts would pass
// for a mechanism nobody added to it, which is the failure mode being pinned: #39 was not
// a wrong mount, it was an ABSENT one, and absence is invisible to a list you also forgot
// to update. Comparing the two backends' own output means the assertion is maintained by
// whoever adds the mount, not by whoever remembers this file.
//
// THE ONE WAIVER is podman's `/home/agent` base itself. That is not a gap: it is the
// defining difference between the backends (podman layers a :ro GlobalHome base with
// per-workspace overlays; AC binds wsState whole), and it is why the nested mounts have
// to be compared rather than the whole set.
func TestMachineWideMountsReachBothContainerBackends(t *testing.T) {
	podman := nestedMachineTierMounts(t, "podman")
	ac := nestedMachineTierMounts(t, "container")

	for dest, src := range podman {
		if _, ok := ac[dest]; !ok {
			t.Errorf("podman mounts %s from the machine-wide store (%s) and Apple Container does not.\n\n"+
				"This is the shape of issue #39: a mechanism wired into the podman branch of\n"+
				"assembleRunCmd only. The AC single wsState→/home/agent bind covers PER-WORKSPACE\n"+
				"paths, so a missing per-workspace mount is harmless there — but it cannot supply\n"+
				"anything machine-wide, so this one silently degrades to per-workspace with no\n"+
				"error and no warning.\n\n"+
				"Fix it in appleContainerBaseMounts, or — if the outcome genuinely is achieved\n"+
				"another way on that backend — say so here and in that function's header, the way\n"+
				"WritableDirs is.", dest, src)
		}
	}
}

// nestedMachineTierMounts returns dest→src for every `-v` whose host side is under the
// machine-wide store and whose container side is strictly below /home/agent. The base
// /home/agent mount itself is excluded (see the waiver above).
func nestedMachineTierMounts(t *testing.T, rt string) map[string]string {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.IsMacOS = rt == "container"
	o.IsLinux = !o.IsMacOS

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("security", sec),
		rt:           rt,
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   filepath.Join(ws, "agents"),
		wsState:      wsState,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})

	// The machine-wide store is GlobalStorage; GlobalHome and GlobalCache both live
	// under it, so one prefix covers the tier without naming each accessor.
	store := paths.GlobalStorage()
	out := map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		src, dest, ok := strings.Cut(argv[i+1], ":")
		if !ok {
			continue
		}
		dest = strings.TrimSuffix(dest, ":ro")
		if !strings.HasPrefix(src, store) {
			continue
		}
		if dest == "/home/agent" || !strings.HasPrefix(dest, "/home/agent/") {
			continue
		}
		out[dest] = src
	}
	if len(out) == 0 {
		t.Fatalf("runtime %q emitted no machine-wide nested mounts at all — the extractor is "+
			"probably matching nothing, which would make this test pass vacuously.\nargv: %s",
			rt, strings.Join(argv, " "))
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("%s machine-wide nested mounts: %v", rt, keys)
	return out
}
