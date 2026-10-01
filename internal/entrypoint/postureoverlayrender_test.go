package entrypoint

// postureoverlayrender_test.go is the BEHAVIORAL proof of OQ-3's ruling ("do it now. extension
// point.", docs/design/notch-scoped-config-contributions.md): an autonomy posture's `config`
// patch on a surface another pack owns — a POSTURE OVERLAY — renders at the posture its notch
// selects and nowhere else, at both render boundaries, as a config-overlay does: the jail boot
// (ConfigurePackSurfaces, which derives the bit from its own target) and the host apply
// (RenderHostPack, driven as `yolo host apply` drives it). Each test reads the FILE the agent
// reads, and the host test reads the provenance record that makes a host-only scalar removable.
//
// The fixture is the extension point's own example: a personal pack owning no surface, whose
// guarded posture sets a scalar and an object in pi's settings (host-only), and whose
// autonomous posture sets another scalar (jail-only).

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// postureOverlayOwner is pi's settings surface with a managed key the contributor also sets,
// so the precedence the posture overlay folds at is observable.
func postureOverlayOwner(t *testing.T) *packload.Pack {
	return listOwnerPack(t, "", map[string]any{"managed": map[string]any{"owned": "by-pi"}})
}

// piSettingsPatch is one posture `config` entry on pi/settings.
func piSettingsPatch(t *testing.T, managed map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{"agent": "pi", "name": "settings",
		"codec": "json", "path": "~/" + listSettings, "managed": managed}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// postureOverlayContributor is the personal pack. guarded or autonomous may be nil.
func postureOverlayContributor(t *testing.T, autonomous, guarded map[string]any) *packload.Pack {
	t.Helper()
	c := packdecl.Contribution{Kind: packdecl.KindAutonomy}
	if autonomous != nil {
		c.Autonomous = &packdecl.AutonomyPosture{Config: piSettingsPatch(t, autonomous)}
	}
	if guarded != nil {
		c.Guarded = &packdecl.AutonomyPosture{Config: piSettingsPatch(t, guarded)}
	}
	return &packload.Pack{Name: "matt", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{c}}}
}

var (
	hostOnlyKeys = map[string]any{"hostOnly": true, "gate": map[string]any{"mode": "ask"},
		"owned": "by-matt"}
	jailOnlyKeys = map[string]any{"jailOnly": "yes"}
)

// THE JAIL HALF. The boot's target is a jail, whose profile is autonomous: the autonomous
// posture's key lands in pi's file and neither guarded key does, and the owner's managed key
// beats the contributor's (config-overlay's slot, below managed). Delete the posture overlays
// from Collect, or derive the boot's bit from anything but its target, and one of them moves.
func TestJailBootRendersOnlyTheAutonomousPostureOverlay(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	bootJail(t, e, postureOverlayOwner(t), postureOverlayContributor(t, jailOnlyKeys, hostOnlyKeys))
	got := readRenderedJSON(t, e.Home, listSettings)
	if got["jailOnly"] != "yes" {
		t.Errorf("the autonomous posture's key is absent from the jail's pi settings: %#v", got)
	}
	for _, k := range []string{"hostOnly", "gate"} {
		if _, leaked := got[k]; leaked {
			t.Errorf("the guarded posture's %q reached a jail: %#v", k, got)
		}
	}
	if got["owned"] != "by-pi" {
		t.Errorf("owned = %v: a posture overlay must fold below the owner's managed layer", got["owned"])
	}
	if !strings.Contains(errw.String(), "pi/settings: config-overlay keys from matt") {
		t.Errorf("the boot did not name the pack whose posture overlay applied:\n%s", errw.String())
	}
	if strings.Contains(errw.String(), "folded nowhere") {
		t.Errorf("the boot still calls a placed posture overlay dead:\n%s", errw.String())
	}
}

// THE HOST HALF, under both writing contracts: the guarded scalar and object land in the real
// file and are recorded `config-overlay:matt`, the record every later host verb reads; the
// jail-only key does not land. Then the posture stops selecting them — the author moves the
// keys to the autonomous posture — and each contract disposes of them the way it disposes of
// every key yolo force-wrote: `own` regenerates without it; `assert` keeps it in the user's file
// as `retired:config-overlay:matt`, and `yolo host apply --revert` (RevertHostRender) removes it.
func TestHostApplyWritesAGuardedPostureOverlayAndCanRemoveIt(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			home := t.TempDir()
			owner := postureOverlayOwner(t)
			applyHostPacks(t, home, ownership, false, owner,
				postureOverlayContributor(t, jailOnlyKeys, hostOnlyKeys))
			got := readRenderedJSON(t, home, listSettings)
			if got["hostOnly"] != true || !reflect.DeepEqual(got["gate"], map[string]any{"mode": "ask"}) {
				t.Fatalf("the guarded posture's keys are not in the host file: %#v", got)
			}
			if _, leaked := got["jailOnly"]; leaked {
				t.Errorf("the autonomous posture's key reached the host: %#v", got)
			}
			if got["owned"] != "by-pi" {
				t.Errorf("owned = %v: the owner's managed key must win at the host too", got["owned"])
			}
			provPath := render.Host(home, nil, ownership).ProvenancePath("pi", "settings")
			if rec := readSidecar(t, provPath); !strings.Contains(rec, "hostOnly\tconfig-overlay:matt") ||
				!strings.Contains(rec, "gate\tconfig-overlay:matt") {
				t.Errorf("the provenance record does not attribute the posture overlay's keys:\n%s", rec)
			}

			// The posture stops selecting the keys.
			moved := postureOverlayContributor(t, hostOnlyKeys, nil)
			applyHostPacks(t, home, ownership, false, owner, moved)
			got = readRenderedJSON(t, home, listSettings)
			if ownership == render.OwnershipOwn {
				if _, kept := got["hostOnly"]; kept {
					t.Errorf("own: the next apply kept a key no layer claims: %#v", got)
				}
				return
			}
			if rec := readSidecar(t, provPath); !strings.Contains(rec, "hostOnly\tretired:config-overlay:matt") {
				t.Fatalf("assert: the unselected key is not recorded as yolo's retired output:\n%s", rec)
			}
			if _, err := RevertHostRender([]*packload.Pack{owner, moved}, home, false); err != nil {
				t.Fatalf("RevertHostRender: %v", err)
			}
			if data, err := os.ReadFile(home + "/" + listSettings); err != nil ||
				strings.Contains(string(data), "hostOnly") {
				t.Errorf("assert: --revert left the retired posture overlay key (err=%v):\n%s", err, data)
			}
		})
	}
}

// THE PACK DROP (R3). A pack that wrote a host-only scalar through its guarded posture and is
// then dropped from `packs` leaves a key PruneHostOverlayKeys removes, because its record reads
// `config-overlay:matt` exactly as a config-overlay's does.
func TestADroppedPacksPostureOverlayKeyIsPruned(t *testing.T) {
	home := t.TempDir()
	owner := postureOverlayOwner(t)
	applyHostPacks(t, home, render.OwnershipAssert, false, owner,
		postureOverlayContributor(t, nil, hostOnlyKeys))
	remaining := []*packload.Pack{owner}
	applyHostPacks(t, home, render.OwnershipAssert, false, remaining...)
	removed, err := PruneHostOverlayKeys(remaining, map[string]bool{"pi": true},
		packoverlay.Collect(remaining, false, nil), home, false)
	if err != nil {
		t.Fatalf("PruneHostOverlayKeys: %v", err)
	}
	var keys []string
	for _, o := range removed {
		if o.Pack == "matt" {
			keys = append(keys, o.Key)
		}
	}
	if !reflect.DeepEqual(keys, []string{"gate", "hostOnly"}) {
		t.Errorf("pruned keys from matt = %v, want gate and hostOnly (removed=%+v)", keys, removed)
	}
	if got := readRenderedJSON(t, home, listSettings); got["hostOnly"] != nil || got["gate"] != nil {
		t.Errorf("the dropped pack's posture overlay keys are still in the host file: %#v", got)
	}
}

// AN OWNERLESS POSTURE OVERLAY AT THE HOST is the collector's orphan now, reported where the
// apply reports every orphan (apply.go) — so the host render no longer turns it into an
// "ignored: … folded nowhere" row of its own.
func TestHostRenderNoLongerRowsAPostureOverlayAsFoldedNowhere(t *testing.T) {
	home := t.TempDir()
	// The contributor declares a surface of its own, which is what put a foreign patch through
	// the posture fold's miss path at all.
	contributor := postureOverlayContributor(t, nil, hostOnlyKeys)
	own, err := json.Marshal([]any{map[string]any{"agent": "matt", "name": "notes",
		"codec": "json", "path": "~/.matt/notes.json", "managed": map[string]any{"k": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	contributor.Decl.Contributes = append(contributor.Decl.Contributes,
		packdecl.Contribution{Kind: packdecl.KindConfig, Raw: own})
	results := applyHostPacks(t, home, render.OwnershipAssert, false, contributor)
	for _, r := range results {
		if strings.Contains(r.Action, "does not declare") {
			t.Errorf("a posture overlay became a dead-patch row: %+v", r)
		}
	}
}

// A GUARDED POSTURE OVERLAY'S ARRAY AND THE USER'S OWN LIST, under each writing contract
// (docs/reference/pack-system.md#autonomy). An array replaces the file's array whole (RFC 7386),
// and the overlay folds BELOW capture: under `assert` rmw writes it over the user's list, at the
// first apply and after a later edit alike; under `own` the user's list is the capture, which
// outranks it, so the user's list stays and an edit to it survives the next apply. Contrast
// claude's guarded `managed` array, which outranked both (internal/cli/hostguardeddirs_test.go).
func TestAGuardedPostureOverlayArrayAndTheUsersOwnList(t *testing.T) {
	for _, tc := range []struct {
		ownership    render.HostOwnership
		first, later []any
	}{
		{render.OwnershipAssert, []any{"from-the-pack"}, []any{"from-the-pack"}},
		{render.OwnershipOwn, []any{"users-first"}, []any{"users-later"}},
	} {
		t.Run(tc.ownership.String(), func(t *testing.T) {
			home := t.TempDir()
			path := home + "/" + listSettings
			writeSettings := func(body string) {
				t.Helper()
				if err := os.MkdirAll(home+"/.pi/agent", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writeSettings(`{"dirs":["users-first"]}`)
			owner := postureOverlayOwner(t)
			contributor := postureOverlayContributor(t, nil,
				map[string]any{"dirs": []any{"from-the-pack"}})

			applyHostPacks(t, home, tc.ownership, false, owner, contributor)
			if got := readRenderedJSON(t, home, listSettings)["dirs"]; !reflect.DeepEqual(got, tc.first) {
				t.Errorf("first apply: dirs = %#v, want %#v", got, tc.first)
			}

			// The user edits the file the apply wrote.
			edited := readRenderedJSON(t, home, listSettings)
			edited["dirs"] = []any{"users-later"}
			body, err := json.Marshal(edited)
			if err != nil {
				t.Fatal(err)
			}
			writeSettings(string(body))
			applyHostPacks(t, home, tc.ownership, false, owner, contributor)
			if got := readRenderedJSON(t, home, listSettings)["dirs"]; !reflect.DeepEqual(got, tc.later) {
				t.Errorf("after the user's edit: dirs = %#v, want %#v", got, tc.later)
			}
		})
	}
}
