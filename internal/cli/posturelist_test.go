package cli

// posturelist_test.go pins the POSTURE LIST (docs/design/notch-scoped-config-contributions.md
// §4.1) through the verbs a user runs — `yolo host apply`, `yolo config render` and
// `yolo config ls` — each with the notch it describes. Every test runs the real verb
// (applyHost, applyHostSurveyed, configRunW) over a real file:// pack in a temp HOME, so each
// one fails if its verb stops passing its own notch's autonomy bit to packoverlay.Collect:
// the posture list is the one thing that bit now visibly decides.
//
// The pack is the design's motivating manifest, verbatim: a personal pack owning no surface,
// whose guarded posture adds pi-automode to pi's `packages`.
//
// Every user config declares `host_management: "own"`: the unset key is `none` since the
// `assert` retirement (OQ-CO14), under which the host notch composes no config surface, so no
// posture list has a surface to land in or to be counted as folding into. pi/settings declares
// no mode, so `own` composes it whole through `stateful`, which keeps the insert record too.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

const automodeEntry = "npm:@czottmann/pi-automode@1.17.0"

// automodePack is the motivating contribution, spelled as a pack author writes it.
const automodePack = `{"kind":"autonomy","guarded":{"lists":[{"surface":"pi/settings",` +
	`"path":"/packages","add":["` + automodeEntry + `"]}]}}`

// hostPiPackages reads `packages` out of the real home's pi settings file.
func hostPiPackages(t *testing.T, home string) []any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("the host apply wrote no pi settings file: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("pi settings is not JSON: %v\n%s", err, data)
	}
	got, _ := m["packages"].([]any)
	return got
}

// THE HOST NOTCH WRITES IT. `yolo host apply --assert` renders at the host, whose profile is
// guarded, so the entry lands in ~/.pi/agent/settings.json and in the insert record that lets
// a later apply withdraw it. Flip the bit apply.go passes to Collect and the entry is gone
// from the one place it is for.
func TestHostApplyAssertWritesAGuardedPostureList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectPacksWith(t, home, `"pi",`+listPack(t, home, "matt", automodePack),
		`,"host_management":"own"`)
	verboseReport(t) // the per-surface contributor line is the --verbose view's

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	// Named at the moment it applies, like any list (pack-system.md#config-list-visibility).
	if !hasLine(report, "config-list entries from: matt") {
		t.Errorf("the apply does not name the pack whose posture list it wrote:\n%s", report)
	}
	if got := hostPiPackages(t, home); !reflect.DeepEqual(got, []any{automodeEntry}) {
		t.Errorf("packages = %#v, want exactly the guarded posture's entry", got)
	}
	rec, err := os.ReadFile(render.Host(home, nil, render.OwnershipOwn).ListRecordPath("pi", "settings"))
	if err != nil || !strings.Contains(string(rec), automodeEntry) {
		t.Errorf("the insert record does not name the entry (err=%v):\n%s", err, rec)
	}
}

// THE NOTCH LINE COUNTS A POSTURE LIST AS A FOLD (NS-D2). No pack here has a guarded config
// patch — the owner declares no autonomy at all — so before posture lists counted, this pack
// set printed "no selected pack's guarded posture patches a config surface here" above a
// surface that was about to receive the posture's entry.
func TestHostApplyNotchLineCountsAPostureListAsAFold(t *testing.T) {
	t.Setenv("YOLO_VERBOSE", "1") // the fold clause is the --verbose line's (printNotchFacts)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	owner := listPack(t, home, "acme", `{"kind":"config","config":[{"agent":"acme",`+
		`"name":"settings","codec":"json","mode":"rmw","path":"~/.acme/settings.json",`+
		`"managed":{"k":"v"}}]}`)
	lister := listPack(t, home, "matt", `{"kind":"autonomy","guarded":{"lists":[`+
		`{"surface":"acme/settings","path":"/extras","add":["host-only"]}]}}`)
	selectPacksWith(t, home, owner+","+lister, `,"host_management":"own"`)

	// Fixture guard: the whole test is vacuous if any selected pack's guarded posture has a
	// config patch, because that alone makes the line say "folded".
	for _, p := range loadedPacksForTest(t) {
		if g := p.Decl.PostureFor(render.ProfileFor(render.KindHost).AgentAutonomy); g != nil && len(g.Config) > 0 {
			t.Fatalf("fixture bug: pack %s has a guarded config patch, so the line folds without "+
				"the posture list", p.Name)
		}
	}

	_, report := surveyApply(t)
	if !strings.Contains(report, "folded into the config surfaces below") {
		t.Errorf("the guarded posture's list lands in acme/settings below, and the notch line "+
			"says nothing folds:\n%s", report)
	}
}

// …AND ONLY A POSTURE LIST THE COLLECTOR PLACED (NS-D12). The motivating pack without `pi`: its
// guarded list names a surface nothing selected owns, so the line above it says "no effect",
// and no surface is listed below. Counting the declaration rather than the placement printed
// "folded into the config surfaces below" directly under that orphan line.
func TestHostApplyNotchLineDoesNotCountAnOrphanedPostureList(t *testing.T) {
	t.Setenv("YOLO_VERBOSE", "1") // the fold clause is the --verbose line's (printNotchFacts)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectPacksWith(t, home, listPack(t, home, "matt", automodePack),
		`,"host_management":"own"`)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("host apply rc=%d\n%s", rc, report)
	}
	// Fixture guard: the case is the orphan, reported as one.
	if !hasLine(report, "autonomy", "no effect", "pi/settings", "pack matt") {
		t.Fatalf("fixture bug: the posture list is not reported as an orphan:\n%s", report)
	}
	if strings.Contains(report, "folded into the config surfaces below") {
		t.Errorf("the notch line promises a fold for a posture list that has no owner:\n%s", report)
	}
	if !strings.Contains(report, "no selected pack's guarded posture patches a config surface here") {
		t.Errorf("the notch line does not say nothing folds:\n%s", report)
	}
}

// A MALFORMED POSTURE LIST IS REFUSED UNDER ITS OWN KIND. packdecl cannot parse a surface
// identity (the engine's), so an unparseable one reaches Collect, and the apply's refusal line
// must lead with `autonomy` — the kind the author wrote — rather than `config-overlay`
// (collectProblemKind).
func TestHostApplyRefusesAMalformedPostureListUnderItsOwnKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	bogus := listPack(t, home, "bogus", `{"kind":"autonomy","guarded":{"lists":[`+
		`{"surface":"noslash","path":"/packages","add":["x"]}]}}`)
	selectPacksWith(t, home, `"pi",`+bogus, `,"host_management":"own"`)

	rc, report := applyWith(t, false, nil)
	if rc == 0 {
		t.Errorf("a malformed posture list must fail the apply:\n%s", report)
	}
	if !hasLine(report, "autonomy refused", "pack bogus: autonomy guarded.lists") {
		t.Errorf("the malformed posture list is not refused under its own kind:\n%s", report)
	}
}

// INSPECTION FOLLOWS THE NOTCH (OQ-4). `yolo config render` and `yolo config ls` each pass
// render.ProfileFor(<the notch they describe>).AgentAutonomy, so `--at host` shows the
// guarded entry and `--at jail` does not — with no change to either verb, which is what this
// pins: a verb that fixed its bit would print one answer at both.
func TestConfigRenderAndLsFollowAPostureListsNotch(t *testing.T) {
	listWorldUnder(t, "own", func(home string) string {
		return `"pi",` + listPack(t, home, "matt", automodePack)
	})

	for _, c := range []struct {
		notch string
		want  bool
	}{{"host", true}, {"jail", false}} {
		rc, out, errw := runConfigVerb(t, "render", "pi/settings", "--at", c.notch)
		if rc != 0 {
			t.Fatalf("render --at %s rc=%d\n%s%s", c.notch, rc, out, errw)
		}
		pkgs, _ := json.Marshal(renderedJSON(t, out)["packages"])
		if has := strings.Contains(string(pkgs), automodeEntry); has != c.want {
			t.Errorf("config render --at %s: packages = %s, want the guarded entry present=%v",
				c.notch, pkgs, c.want)
		}

		rc, out, errw = runConfigVerb(t, "ls", "--at", c.notch)
		if rc != 0 {
			t.Fatalf("ls --at %s rc=%d\n%s%s", c.notch, rc, out, errw)
		}
		if has := hasLine(out, "/packages", "matt (1 entry)"); has != c.want {
			t.Errorf("config ls --at %s: matt's entry listed=%v, want %v\n%s", c.notch, has,
				c.want, out)
		}
	}
}
