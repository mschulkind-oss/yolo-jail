package run

// filesslotparity_test.go is THE ALIAS-ROOT PIN: one addressed `files` contribution must land at
// the SAME path at both notches, and that path must be a SUBDIRECTORY of the slot — never the slot
// root, and never the home.
//
// Why a test and not a comment. The alias-root bug is the reason the slot mechanism has the shape
// it has, and it has been real twice:
//
//   - IN THE TREE, briefly. At 056faeb9 an addressed contribution's mount was emitted at
//     `Dest: c.Into`, and an addressed contribution has no `into` by the into-xor-agents rule — so
//     the argv was `-v <pack tree>:/home/agent/:ro`, the pack's own tree bound read-only over the
//     entire jail home. 1de253d7 added the `<slot>/<pack>` join two minutes later. Nothing shipped
//     with it because no manifest in the corpus declares a files slot at all, which is exactly why
//     it survived review: the unit gate was green either way.
//   - AT THE HOST NOTCH, until 2026-09-21, in the softer form. The jail joined the contributing
//     pack's name onto the slot; destination borrowing did not, so the host wrote the contributor's
//     tree AT the slot root — one pack.json, two destinations, and at the host the layout
//     pi-pack-extensions.md §8 invariant 2 forbids (measured, and the divergence
//     hostfilestree.go's own comment says must not exist).
//
// Both notches are exercised through their REAL resolvers — packFilesTargets for the jail,
// destination borrowing plus entrypoint.RenderHostFiles for the host — rather than through
// packload.SlotLanding. Pinning the helper alone would leave either call site free to stop calling
// it, which is how the two came to disagree in the first place.

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// slotFixtureRoot is the path the fixture's agent pack exposes as its files slot.
const slotFixtureRoot = ".pi/agent/extensions"

// slotFixture builds the two-pack arrangement the whole file is about: an agent pack declaring a
// bare files DESTINATION for `pi`, and a content pack addressing it with a tree of its own.
func slotFixture(t *testing.T, contentPack string) (owner, content *packload.Pack) {
	t.Helper()
	ownerRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(ownerRoot, "pack.json"), []byte(
		`{"name":"pi","contributes":[`+
			`{"kind":"files","agent":"pi","into":"`+slotFixtureRoot+`"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	owner, probs := packload.LoadDir(ownerRoot, "pi")
	if len(probs) != 0 {
		t.Fatalf("loading the slot fixture: %v", probs)
	}

	contentRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(contentRoot, "pi-extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "pi-extensions", "compact-tools.ts"),
		[]byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "pack.json"), []byte(
		`{"name":"`+contentPack+`","contributes":[`+
			`{"kind":"files","agents":["pi"],"from":"pi-extensions"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	content, probs = packload.LoadDir(contentRoot, contentPack)
	if len(probs) != 0 {
		t.Fatalf("loading the content fixture: %v", probs)
	}
	return owner, content
}

// jailFilesDests is every home-relative destination the JAIL notch delivers for `set`.
func jailFilesDests(set []*packload.Pack) []string {
	var out []string
	for _, tg := range packFilesTargets(set) {
		out = append(out, filepath.ToSlash(tg.Dest))
	}
	sort.Strings(out)
	return out
}

// hostFilesDests is every home-relative path the HOST notch would write for `set`, observed
// (nothing is written). Per FILE, because that is the unit the host renderer works in.
func hostFilesDests(t *testing.T, set []*packload.Pack, home string) []string {
	t.Helper()
	resolved, _ := packload.ResolveDestinations(set)
	var out []string
	for _, p := range resolved {
		results, err := entrypoint.RenderHostFiles(p, home, entrypoint.HostFilesRequest{}, true)
		if err != nil {
			t.Fatalf("rendering host files for %q: %v", p.Name, err)
		}
		for _, r := range results {
			rel, err := filepath.Rel(home, r.Path)
			if err != nil {
				t.Fatalf("host path %q is not under the home %q: %v", r.Path, home, err)
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

// TestAddressedFilesLandAtTheSamePathAtBothNotches: the jail's mount destination and the host's
// written path are ONE path, `<slot>/<pack>`. A pack.json that delivers to two places depending on
// where it is rendered is the defect this pins — and the host half of it was live.
func TestAddressedFilesLandAtTheSamePathAtBothNotches(t *testing.T) {
	owner, content := slotFixture(t, "matt")
	set := []*packload.Pack{owner, content}
	wantDir := slotFixtureRoot + "/matt"

	if got := jailFilesDests(set); !slices.Equal(got, []string{wantDir}) {
		t.Errorf("jail `files` destinations = %v, want %v", got, []string{wantDir})
	}
	wantFiles := []string{wantDir + "/compact-tools.ts"}
	if got := hostFilesDests(t, set, t.TempDir()); !slices.Equal(got, wantFiles) {
		t.Errorf("host `files` paths = %v, want %v — the host notch must namespace by the "+
			"CONTRIBUTING pack exactly as the jail does (pi-pack-extensions.md OQ-4)",
			got, wantFiles)
	}
}

// TestNoAddressedFilesDeliveryReachesTheSlotRootOrTheHome is the catastrophic form, stated as the
// property rather than as the path: every delivery is strictly UNDER the slot, so nothing is ever
// bound or written at the slot root (where the owner's own claims live) and nothing is ever bound
// at the home root (the 056faeb9 argv). It fails if either notch drops the join.
func TestNoAddressedFilesDeliveryReachesTheSlotRootOrTheHome(t *testing.T) {
	owner, content := slotFixture(t, "matt")
	set := []*packload.Pack{owner, content}

	var got []string
	got = append(got, jailFilesDests(set)...)
	got = append(got, hostFilesDests(t, set, t.TempDir())...)
	if len(got) == 0 {
		t.Fatal("neither notch delivered the addressed tree at all — the fixture no longer " +
			"exercises the thing this file pins")
	}
	for _, dest := range got {
		switch {
		case dest == "" || dest == "." || dest == "/":
			t.Errorf("a `files` delivery landed at the HOME ROOT (%q) — that is the argv that "+
				"bound a pack's tree read-only over the whole jail home", dest)
		case dest == slotFixtureRoot:
			t.Errorf("a `files` delivery landed AT the slot root %q — the slot root is the "+
				"owner's, every contributor's tree goes under it, and mounting it makes the "+
				"nested-mount conflict the alias-root bug is named after", dest)
		case !strings.HasPrefix(dest, slotFixtureRoot+"/"):
			t.Errorf("a `files` delivery landed OUTSIDE the slot: %q is not under %q",
				dest, slotFixtureRoot)
		}
	}
}

// TestTheSlotItselfDeliversNothingAtEitherNotch: a destination is a bare slot — it ships no
// content, so it makes no mount in the jail and writes no file at the host. The owner pack alone
// must be inert (pi-pack-extensions.md OQ-6).
func TestTheSlotItselfDeliversNothingAtEitherNotch(t *testing.T) {
	owner, _ := slotFixture(t, "matt")
	set := []*packload.Pack{owner}
	if got := jailFilesDests(set); len(got) != 0 {
		t.Errorf("the slot alone produced jail mounts %v — a destination ships nothing", got)
	}
	if got := hostFilesDests(t, set, t.TempDir()); len(got) != 0 {
		t.Errorf("the slot alone produced host writes %v — a destination ships nothing", got)
	}
}

// TestTwoPacksAddressingOneSlotLandInTheirOwnSubdirectories is invariant 2 end to end
// ("namespacing is the contributing pack's name; collisions are impossible"): two contributors,
// two subdirectories, at both notches. Without the join at either notch they would be the same
// path, which for a CombineExclusive kind means one silently shadowing the other.
func TestTwoPacksAddressingOneSlotLandInTheirOwnSubdirectories(t *testing.T) {
	owner, first := slotFixture(t, "matt")
	_, second := slotFixture(t, "acme")
	set := []*packload.Pack{owner, first, second}

	wantJail := []string{slotFixtureRoot + "/acme", slotFixtureRoot + "/matt"}
	if got := jailFilesDests(set); !slices.Equal(got, wantJail) {
		t.Errorf("jail destinations for two contributors = %v, want %v", got, wantJail)
	}
	wantHost := []string{
		slotFixtureRoot + "/acme/compact-tools.ts",
		slotFixtureRoot + "/matt/compact-tools.ts",
	}
	if got := hostFilesDests(t, set, t.TempDir()); !slices.Equal(got, wantHost) {
		t.Errorf("host paths for two contributors = %v, want %v", got, wantHost)
	}
}
