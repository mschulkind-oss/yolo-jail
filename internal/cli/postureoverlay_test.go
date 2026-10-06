package cli

// postureoverlay_test.go pins the POSTURE OVERLAY (OQ-3's build,
// docs/design/notch-scoped-config-contributions.md NS-D19 to NS-D24) through the verbs a user
// runs — `yolo host apply`, `yolo config render` and `yolo config ls` — each at the notch it
// describes. Every test runs the real verb over a real file:// pack in a temp HOME, so each
// fails if its verb stops handing its own notch's autonomy bit to packoverlay.Collect, or if
// Collect stops placing a posture's config patch on another pack's surface.
//
// The pack is the extension point's example: a personal pack owning no surface, whose guarded
// posture sets a scalar only the host gets in pi's settings.
//
// Every user config declares `host_management: "own"`: the unset key is `none` since the
// `assert` retirement (OQ-CO14), under which the host notch composes no config surface for a
// posture overlay to be placed on. pi/settings declares no mode, so `own` composes it whole
// through `stateful`, whose provenance record attributes the scalar as the rmw one did.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostOnlyScalarPack is the contribution, spelled as a pack author writes it.
const hostOnlyScalarPack = `{"kind":"autonomy","guarded":{"config":[{"agent":"pi",` +
	`"name":"settings","codec":"json","path":"~/.pi/agent/settings.json",` +
	`"managed":{"hostOnlyScalar":"on-the-host"}}]}}`

// hostPiSettings reads the real home's pi settings file.
func hostPiSettings(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("the host apply wrote no pi settings file: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("pi settings is not JSON: %v\n%s", err, data)
	}
	return m
}

// THE HOST NOTCH WRITES IT. `yolo host apply --assert` renders at the host, whose profile is
// guarded, so the scalar lands in ~/.pi/agent/settings.json, attributed in the provenance
// record every later host verb reads, and the report names the pack. Flip the bit apply.go
// passes to Collect, or drop the posture overlays from Collect, and the scalar is gone from
// the one place it is for.
func TestHostApplyAssertWritesAGuardedPostureOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectPacksWith(t, home, `"pi",`+listPack(t, home, "matt", hostOnlyScalarPack),
		`,"host_management":"own"`)
	verboseReport(t)

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	if got := hostPiSettings(t, home)["hostOnlyScalar"]; got != "on-the-host" {
		t.Errorf("hostOnlyScalar = %v, want the guarded posture's value in the real home", got)
	}
	rec, err := os.ReadFile(render.Host(home, nil, render.OwnershipOwn).ProvenancePath("pi", "settings"))
	if err != nil || !strings.Contains(string(rec), "hostOnlyScalar\tconfig-overlay:matt") {
		t.Errorf("the provenance record does not attribute the scalar to matt (err=%v):\n%s", err, rec)
	}
	if strings.Contains(report, "folded nowhere") || strings.Contains(report, "does not declare") {
		t.Errorf("the apply still reports the placed patch as dead:\n%s", report)
	}
}

// THE NOTCH LINE COUNTS A PLACED POSTURE OVERLAY AS A FOLD, and an orphaned one as none — the
// posture list's rule (NS-D12), applied to the config half (NS-D24). The owner is a fixture
// rather than pi, whose own guarded posture patches its own surface and would make the line
// fold without the posture overlay.
func TestHostApplyNotchLineCountsOnlyAPlacedPostureOverlay(t *testing.T) {
	t.Setenv("YOLO_VERBOSE", "1") // the fold clause is the --verbose line's (printNotchFacts)
	contributor := `{"kind":"autonomy","guarded":{"config":[{"agent":"acme","name":"settings",` +
		`"codec":"json","path":"~/.acme/settings.json","managed":{"hostOnlyScalar":true}}]}}`
	for _, c := range []struct {
		name  string
		packs func(home string) string
		folds bool
	}{
		{"placed", func(home string) string {
			owner := listPack(t, home, "acme", `{"kind":"config","config":[{"agent":"acme",`+
				`"name":"settings","codec":"json","mode":"rmw","path":"~/.acme/settings.json",`+
				`"managed":{"k":"v"}}]}`)
			return owner + "," + listPack(t, home, "matt", contributor)
		}, true},
		{"orphaned", func(home string) string {
			return listPack(t, home, "matt", contributor)
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			selectPacksWith(t, home, c.packs(home), `,"host_management":"own"`)
			for _, p := range loadedPacksForTest(t) {
				if p.PosturePatchesOwnSurface(render.ProfileFor(render.KindHost).AgentAutonomy) {
					t.Fatalf("fixture bug: pack %s patches its own surface at the guarded posture, "+
						"so the line folds without the posture overlay", p.Name)
				}
			}
			rc, report := applyWith(t, false, nil)
			if rc != 0 {
				t.Fatalf("host apply rc=%d\n%s", rc, report)
			}
			if got := strings.Contains(report, "folded into the config surfaces below"); got != c.folds {
				t.Errorf("the notch line says folded=%v, want %v:\n%s", got, c.folds, report)
			}
			if !c.folds && !hasLine(report, "autonomy", "no effect", "acme/settings", "pack matt") {
				t.Errorf("the ownerless posture overlay is not reported as an orphan:\n%s", report)
			}
		})
	}
}

// A POSTURE OVERLAY CARRYING WHAT A CONFIG-OVERLAY MAY NOT SAY is refused under its own kind:
// the apply fails, and the refusal line leads with `autonomy` (collectProblemKind).
func TestHostApplyRefusesAPostureOverlaysRefusedFieldUnderItsOwnKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	bogus := listPack(t, home, "bogus", `{"kind":"autonomy","guarded":{"config":[{"agent":"pi",`+
		`"name":"settings","codec":"json","path":"~/.pi/agent/settings.json","mode":"rmw",`+
		`"managed":{"k":1}}]}}`)
	selectPacksWith(t, home, `"pi",`+bogus, `,"host_management":"own"`)
	rc, report := applyWith(t, false, nil)
	if rc == 0 {
		t.Errorf("a posture overlay setting `mode` must fail the apply:\n%s", report)
	}
	if !hasLine(report, "autonomy refused", "pack bogus: autonomy guarded.config") {
		t.Errorf("the refused posture overlay is not refused under its own kind:\n%s", report)
	}
}

// `yolo config promote` reads the fold at the JAIL's posture (loadPromoteFold), so a later
// pack's AUTONOMOUS posture overlay on the key outranks a promotion into an earlier pack — the
// captured value would lose to it in every jail — and a GUARDED one, which no jail places, does
// not. Invert the bit loadPromoteFold passes to Collect and the two answers swap.
func TestPromoteIsOutrankedByAJailPostureOverlay(t *testing.T) {
	for _, c := range []struct {
		posture string
		want    string
	}{{"autonomous", promotionOutranked}, {"guarded", promotionPromotable}} {
		t.Run(c.posture, func(t *testing.T) {
			w := newPromoteWorld(t, `[]`)
			early := w.pack("early", `{"name":"early","contributes":[]}`)
			later := w.pack("zlater", `{"name":"zlater","contributes":[{"kind":"autonomy","`+
				c.posture+`":{"config":[{"agent":"claude","name":"settings","codec":"json",`+
				`"path":"~/.claude/settings.json","managed":{"model":"theirs"}}]}}]}`)
			writeFile(t, filepath.Join(w.home, ".config", "yolo-jail", "config.jsonc"),
				`{"packs":["claude",`+early+`,`+later+`]}`)
			w.capture("claude", "settings", `{"model":"mine"}`, `{}`)

			out, errw, rc := w.run("claude", "--plan", "--to", "pack:early")
			if rc != 0 {
				t.Fatalf("rc=%d\n%s%s", rc, out, errw)
			}
			if got := dispositionOf(t, out, "model"); got != c.want {
				t.Errorf("model --to pack:early under zlater's %s posture overlay = %s, want %s:\n%s",
					c.posture, got, c.want, out)
			}
		})
	}
}

// INSPECTION FOLLOWS THE NOTCH. `yolo config render` and `yolo config ls` each pass
// render.ProfileFor(<the notch they describe>).AgentAutonomy, so `--at host` shows the
// guarded scalar and `--at jail` does not.
func TestConfigRenderAndLsFollowAPostureOverlaysNotch(t *testing.T) {
	listWorldUnder(t, "own", func(home string) string {
		return `"pi",` + listPack(t, home, "matt", hostOnlyScalarPack)
	})
	for _, c := range []struct {
		notch string
		want  bool
	}{{"host", true}, {"jail", false}} {
		rc, out, errw := runConfigVerb(t, "render", "pi/settings", "--at", c.notch)
		if rc != 0 {
			t.Fatalf("render --at %s rc=%d\n%s%s", c.notch, rc, out, errw)
		}
		if _, has := renderedJSON(t, out)["hostOnlyScalar"]; has != c.want {
			t.Errorf("config render --at %s: hostOnlyScalar present=%v, want %v\n%s", c.notch, has,
				c.want, out)
		}

		rc, out, errw = runConfigVerb(t, "ls", "--at", c.notch)
		if rc != 0 {
			t.Fatalf("ls --at %s rc=%d\n%s%s", c.notch, rc, out, errw)
		}
		if has := hasLine(out, "hostOnlyScalar", "matt"); has != c.want {
			t.Errorf("config ls --at %s: matt's key listed=%v, want %v\n%s", c.notch, has, c.want, out)
		}
	}
}
