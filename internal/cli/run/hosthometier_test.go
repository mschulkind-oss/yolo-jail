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

// THE CLASS behind the pack-`mount` defect, pinned the same way
// machinetierparity_test.go pins issue #39's.
//
// The invariant: on Apple Container, NO bind may have its source under the user's
// real host home. Every such bind is a host GRANT — the user's nvim config, a pack's
// approved `mount`, a `host_files` source, a config `mounts` entry — and the one
// thing they all promise is `:ro`. Apple Container accepts the suffix and ignores it,
// so each of them is a silent upgrade from "the agent may read this" to "the agent
// may overwrite this", in the user's actual home directory.
//
// Two legitimate escapes, and the test permits exactly them: materialize a COPY
// host-side (acMaterialize, which is why the source stops being the host path), or
// refuse the mount with a printed reason (roBindsUnsupported). What it forbids is the
// third thing, which is what every instance of this defect has been: emit the bind
// anyway and rely on a `:ro` that isn't honored.
//
// WHY A CENSUS RATHER THAN A LIST OF KNOWN SITES. There were four emitters of this
// shape when the rule was written down, and the rule reached three of them — the
// fourth (the host nvim config) was found by this test rather than by the sweep that
// wrote it. A new emitter fails here; a list would have to be remembered.
func TestNoHostHomeBindSurvivesOnAppleContainer(t *testing.T) {
	podman := hostHomeSourcedMounts(t, "podman")
	if len(podman) == 0 {
		t.Fatal("podman emitted no host-home-sourced mounts at all — the fixture no longer " +
			"exercises the class, so the Apple Container half below would pass vacuously")
	}
	t.Logf("podman host-home grants: %v", sortedKeys(podman))

	ac := hostHomeSourcedMounts(t, "container")
	for dest, src := range ac {
		t.Errorf("Apple Container binds %s from the user's host home (%s).\n\n"+
			"That backend IGNORES :ro, so this hands the agent write access to a path the\n"+
			"user granted read-only. Two ways out, both already used elsewhere in this\n"+
			"package: acMaterialize a copy into wsState (what `reads-host`, `host_files`\n"+
			"and the git excludes file do), or skip it with the reason roBindsUnsupported\n"+
			"returns (what config `mounts` and pack `mount` do). Emitting the bind and\n"+
			"trusting the suffix is the one option that is not available.", dest, src)
	}
}

// hostHomeSourcedMounts returns dest→src for every `-v` whose host side lies under
// HOME but is neither yolo's own machine-wide store nor anything under the workspace.
// The fixture keeps HOME and the workspace in separate temp dirs precisely so this
// distinction is expressible; in a real launch the workspace usually IS under the
// home, and then only the exclusion below separates a grant from ordinary plumbing.
func hostHomeSourcedMounts(t *testing.T, rt string) map[string]string {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	// One grant that always exists, so the census is never empty: the host nvim
	// config, which the boot copies into the jail home (entrypoint/boot.go).
	if err := os.MkdirAll(filepath.Join(home, ".config", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}

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

	store := paths.GlobalStorage()
	out := map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		src, dest, ok := strings.Cut(argv[i+1], ":")
		if !ok || !strings.HasPrefix(src, home+string(filepath.Separator)) {
			continue
		}
		if strings.HasPrefix(src, store) || strings.HasPrefix(src, ws+string(filepath.Separator)) {
			continue
		}
		out[strings.TrimSuffix(dest, ":ro")] = src
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
