package run

// briefingaudience_test.go guards the jail notch's briefing mount against the shape
// briefing-audiences.md makes legal: a `briefing` that names an AUDIENCE and no destination.
//
// The defect this pins was LATENT rather than hypothetical. The mount loop filtered on `Kind`
// alone while both of its host-side siblings (entrypoint.ComposeHostBriefings and
// hostBriefingPaths) filtered on `Kind` AND `Into != ""`, and the line it guards builds
// `staged + ":/home/agent/" + c.Into + ":ro"`. With an empty `into` that is a single staged FILE
// bind-mounted over `/home/agent` — the jail's entire home, replaced by one markdown file, on a
// manifest the validator now accepts.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// addressedBriefingPack loads a real content pack through LoadDir — the same path a launch
// takes — declaring prose for an audience and no destination. It goes through the loader on
// purpose: the point is that this manifest is now ACCEPTED, so a hand-built struct would prove
// less than the fixture does.
func addressedBriefingPack(t *testing.T, name string, agents ...string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# house rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"briefing","agents":["` + strings.Join(agents, `","`) + `"]}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name)
	if len(problems) != 0 {
		t.Fatalf("loading the %s fixture pack: %v — an `agents`-only briefing must LOAD, or "+
			"this test is asserting against a manifest no launch can reach", name, problems)
	}
	return p
}

// AN INTO-LESS BRIEFING EMITS NO MOUNT AT ALL. Compared against the frozen argv rather than by
// substring, because "no mount over /home/agent" and "no mount" are different claims and only
// the second one is true: routing an addressed contribution at the jail notch is a later step,
// so today its correct contribution to the argv is exactly nothing.
func TestAssembleSkipsABriefingWithNoDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.packs = append(in.packs, addressedBriefingPack(t, "house", "claude"))

	got := o.assembleRunCmd(in)
	for _, arg := range got {
		if strings.Contains(arg, ":/home/agent/:ro") {
			t.Fatalf("a briefing with no `into` was mounted over the jail's HOME: %q", arg)
		}
	}
	if !slices.Equal(got, podmanLinuxGolden(home)) {
		t.Errorf("argv drifted from the golden with an addressed briefing selected:\ngot:  %v\nwant: %v",
			got, podmanLinuxGolden(home))
	}
}

// THE APPLE CONTAINER BRANCH IS DELIBERATELY NOT TESTED HERE, and that is a measurement rather
// than an omission. That branch does not build a `-v` pair — it calls
// `acMaterialize(staged, c.Into, wsState)`, which copies to `filepath.Join(wsState, c.Into)`.
// With an empty `into` that resolves to ws_state ITSELF, an existing directory, so copyFile2
// fails EISDIR and helpers.go:108 discards the error: nothing is written, nothing is destroyed,
// and no assertion can tell the guarded run from the unguarded one. Measured by deleting the
// guard and running the AC fixture — it stayed green, which is precisely the shape of test this
// repo has shipped five times. The damage is the podman branch's alone.
