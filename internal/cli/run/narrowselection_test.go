package run

// narrowselection_test.go pins a fork build's NARROWED SELECTION (Options.OnlyPacks, seal.go;
// docs/design/forked-programs-as-packs.md FP-D9): the launch stages only the named entries and
// what they join, and — because it carries a subset of what the user selected — never runs the
// loophole retirement pass, which would read every other selected pack as one that LEFT `packs`.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
)

func TestNarrowedPackEntriesKeepOnlyTheNamedOnes(t *testing.T) {
	entries := []config.PackEntry{{Name: "claude"}, {Name: "fork"}, {Name: "local", Implicit: true}}
	o := &Options{}
	if got := o.narrowedPackEntries(entries); len(got) != 3 {
		t.Errorf("no narrowing must keep every entry, the local pack included: %v", got)
	}
	o.OnlyPacks = []string{"fork"}
	got := o.narrowedPackEntries(entries)
	if len(got) != 1 || got[0].Name != "fork" {
		t.Errorf("narrowed to %v, want the fork alone", got)
	}
}

// The launch stages the narrowed set: a fork build selecting copilot out of claude and copilot
// stages no claude. Red if stagePacks stops narrowing.
func TestANarrowedLaunchStagesOnlyItsPacks(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `["claude", "copilot"]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.OnlyPacks = []string{"copilot"}
	staged, ok := o.stageRunPacks("yolo-narrowed")
	if !ok {
		t.Fatalf("stageRunPacks failed:\n%s", out.String())
	}
	var names []string
	for _, p := range staged.packs {
		names = append(names, p.Name)
	}
	if slices.Contains(names, "claude") || !slices.Contains(names, "copilot") {
		t.Errorf("a launch narrowed to copilot staged %v", names)
	}
}

// A narrowed launch retires nothing: openai-auth is active only through claude's `needs`, which
// this launch does not carry, and an unnarrowed pass would archive its state.
func TestANarrowedLaunchRetiresNoLoopholeState(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `["claude", "copilot"]`)
	rec := &packstage.LoopholeOwners{Owners: map[string]string{"acme-proxy": "acme-needed-by-claude"}}
	if err := rec.Save(packLoopholeOwnersPath()); err != nil {
		t.Fatal(err)
	}
	stateDir, _ := writeLoopholeState(t, "acme-proxy", "PRIVATE KEY")
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.OnlyPacks = []string{"copilot"}
	if _, ok := o.stageRunPacks("yolo-narrowed-retire"); !ok {
		t.Fatalf("stageRunPacks failed:\n%s", out.String())
	}
	if _, err := os.ReadFile(filepath.Join(stateDir, "ca.key")); err != nil {
		t.Errorf("a narrowed launch archived loophole state it does not carry: %v\n%s", err, out.String())
	}
}
