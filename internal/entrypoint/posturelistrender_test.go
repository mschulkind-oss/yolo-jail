package entrypoint

// posturelistrender_test.go is the BEHAVIORAL proof that a POSTURE LIST
// (docs/design/notch-scoped-config-contributions.md §4.1) renders at the posture its notch
// selects and nowhere else, at both render boundaries: the jail boot (ConfigurePackSurfaces,
// the loop the entrypoint runs, which derives the bit from its own render target) and the
// host apply (RenderHostPack, driven the way `yolo host apply` drives it). Each test reads
// the FILE the agent would read.
//
// The fixture is the design's motivating case: a personal pack — owning no surface — whose
// guarded posture adds pi-automode to pi's `packages`, and whose autonomous posture adds a
// second entry so the jail half has something to find.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

const (
	postureHostEntry = "npm:@czottmann/pi-automode@1.17.0"
	postureJailEntry = "npm:jail-only-extension"
)

// postureListContributor is the personal pack: one autonomy contribution, lists only.
func postureListContributor(t *testing.T) *packload.Pack {
	t.Helper()
	list := func(entry string) []packdecl.PostureList {
		raw, err := json.Marshal([]any{entry})
		if err != nil {
			t.Fatal(err)
		}
		return []packdecl.PostureList{{Surface: "pi/settings", Path: "/packages", Add: raw}}
	}
	return &packload.Pack{Name: "matt", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindAutonomy,
			Autonomous: &packdecl.AutonomyPosture{Lists: list(postureJailEntry)},
			Guarded:    &packdecl.AutonomyPosture{Lists: list(postureHostEntry)}}},
	}}
}

// THE JAIL HALF. The boot's own target is a jail, whose profile is autonomous, so the
// autonomous posture's entry lands and the guarded one — the permission gate that costs tokens
// and prompts in here — does not. Delete the posture gate in packoverlay.Collect, or derive
// the boot's bit from anything but its target, and the host entry appears in the jail.
func TestJailBootRendersOnlyTheAutonomousPostureList(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	bootJail(t, e, listOwnerPack(t, "", nil), postureListContributor(t))
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", postureJailEntry)
	// Named at the moment it applies, like any config-list (pack-system.md#config-list-visibility).
	if !strings.Contains(errw.String(), "pi/settings: config-list entries from matt") {
		t.Errorf("the boot did not name the pack whose posture list applied:\n%s", errw.String())
	}
}

// THE HOST HALF, at an owned host (`host_management: "own"`) and through both arms it renders
// with: a `stateful` owner (the insert record written beside the list capture,
// writeStatefulInsertRecord) and an owner declaring `rmw` (the rmw arm's own record,
// writeListRecord — the arm the retired `assert` ran every surface through). The host's profile
// is guarded, so the automode entry is inserted into the real file, the jail-only one is not, and
// the insert record — what lets a later apply withdraw the entry, and never remove a matching one
// the user wrote — names exactly the inserted entry. The result row names the contributing pack.
func TestHostApplyInsertsOnlyTheGuardedPostureList(t *testing.T) {
	for _, mode := range []string{manifest.ModeStateful, manifest.ModeRMW} {
		t.Run(mode, func(t *testing.T) {
			ownership := render.OwnershipOwn
			home := t.TempDir()
			results := applyHostPacks(t, home, ownership, false, listOwnerPack(t, mode, nil),
				postureListContributor(t))
			wantPackages(t, home, "npm:owner-a", "npm:owner-b", postureHostEntry)
			if r, ok := listResultFor(results, "pi/settings"); !ok || !reflect.DeepEqual(r.Lists, []string{"matt"}) {
				t.Errorf("the host result does not name the contributing pack: %+v", r)
			}

			rec := readSidecar(t, render.Host(home, nil, ownership).ListRecordPath("pi", "settings"))
			var recs map[string]struct {
				Inserted []any `json:"inserted"`
			}
			if err := json.Unmarshal([]byte(rec), &recs); err != nil {
				t.Fatalf("insert record: %v\n%s", err, rec)
			}
			if got := recs["/packages"].Inserted; !reflect.DeepEqual(got, []any{postureHostEntry}) {
				t.Errorf("insert record for /packages = %#v, want exactly the guarded entry:\n%s", got, rec)
			}

			// And the drop half the record exists for: the contributor leaves, the entry leaves.
			applyHostPacks(t, home, ownership, false, listOwnerPack(t, mode, nil))
			wantPackages(t, home, "npm:owner-a", "npm:owner-b")
		})
	}
}
