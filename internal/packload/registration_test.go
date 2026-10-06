package packload

// registration_test.go pins what a REGISTERING files slot asks for (registration.go;
// docs/design/pack-pi-resources.md §3.3): one entry per landed tree, at the landing delivery uses,
// attributed to the tree's pack — and the advisory `expects` check beside it.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const registerSlot = ".pi/agent/yolo-packs"

// registeringOwner is an agent pack declaring a registering slot for `pi`.
func registeringOwner(expects ...string) *Pack {
	return pk("pi", &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindFiles, Agent: "pi", Into: registerSlot, Expects: expects,
		Register: &packdecl.FilesRegister{Surface: "pi/settings", Path: "/packages"},
	}}})
}

func TestRegistrationsListEveryLandedTreeInPackOrder(t *testing.T) {
	set := []*Pack{registeringOwner(), treePack(t, "matt", "files/pi"), treePack(t, "acme", "pi")}
	got := Registrations(set)
	want := []Registration{
		{Pack: "matt", Owner: "pi", Slot: registerSlot, Landing: registerSlot + "/matt",
			Surface: "pi/settings", Path: "/packages", Entry: "~/" + registerSlot + "/matt"},
		{Pack: "acme", Owner: "pi", Slot: registerSlot, Landing: registerSlot + "/acme",
			Surface: "pi/settings", Path: "/packages", Entry: "~/" + registerSlot + "/acme"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Registrations = %+v\nwant %+v", got, want)
	}
	// THE SAME ANSWER OVER A RESOLVED SET, which is what `yolo host apply` collects over: the
	// synthesized landings carry no `agent`, so none reads as a slot, and the addressed
	// declarations are still the pack's own.
	resolved, _ := ResolveDestinations(set)
	if again := Registrations(resolved); !reflect.DeepEqual(again, want) {
		t.Fatalf("Registrations over the resolved set = %+v\nwant %+v", again, want)
	}
}

// A registration names exactly the landing delivery synthesizes, so a tree is never listed where
// it was not delivered.
func TestARegistrationNamesTheLandingDeliveryUses(t *testing.T) {
	set := []*Pack{registeringOwner(), treePack(t, "matt", "files/pi")}
	resolved, _ := ResolveDestinations(set)
	var delivered []string
	for _, p := range resolved {
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindFiles && c.Agent == "" && c.Into != "" {
				delivered = append(delivered, c.Into)
			}
		}
	}
	regs := Registrations(set)
	if len(regs) != 1 || !reflect.DeepEqual(delivered, []string{regs[0].Landing}) {
		t.Fatalf("delivered %v, registered %+v — the two must be one path", delivered, regs)
	}
}

func TestNoRegistrationWithoutARegisteringSlotOrATree(t *testing.T) {
	plain := pk("pi", &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Agent: "pi", Into: registerSlot},
	}})
	for name, set := range map[string][]*Pack{
		"a slot without register": {plain, addressedFilesPack("matt", "files/pi")},
		"no tree addresses it":    {registeringOwner()},
		"the tree is for another": {registeringOwner(), pk("matt", &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindFiles, Agents: []string{"claude"}, From: "files/claude"},
		}})},
	} {
		if got := Registrations(set); len(got) != 0 {
			t.Errorf("%s: Registrations = %+v, want none", name, got)
		}
	}
}

func TestExpectsWarnsOnlyOnATreeHoldingNoneOfTheNames(t *testing.T) {
	owner := registeringOwner("extensions", "themes")
	tree := func(entries ...string) *Pack {
		root := t.TempDir()
		for _, e := range entries {
			if err := os.MkdirAll(filepath.Join(root, "files", "pi", e), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		p := addressedFilesPack("matt", "files/pi")
		p.Root = root
		return p
	}

	bad := tree("ext") // a typo: none of the expected names
	notes := ExpectsNotes([]*Pack{bad}, []*Pack{owner, bad})
	if len(notes) != 1 {
		t.Fatalf("want one note for a tree with none of the names, got %+v", notes)
	}
	for _, want := range []string{"pack matt", `"files/pi"`, "extensions, themes", "~/" + registerSlot + "/matt"} {
		if !strings.Contains(notes[0].Msg, want) {
			t.Errorf("note %q does not name %q", notes[0].Msg, want)
		}
	}
	if notes[0].Fix == "" {
		t.Error("the note names no next step")
	}

	good := tree("themes")
	if notes := ExpectsNotes([]*Pack{good}, []*Pack{owner, good}); len(notes) != 0 {
		t.Errorf("a tree holding one expected name was warned about: %+v", notes)
	}
	missing := addressedFilesPack("matt", "files/pi")
	missing.Root = t.TempDir()
	if notes := ExpectsNotes([]*Pack{missing}, []*Pack{owner, missing}); len(notes) != 0 {
		t.Errorf("a missing tree, which every renderer already reports, was warned about: %+v", notes)
	}
}

// treePack is addressedFilesPack over a real tree holding <from>/extensions/, so its `files`
// source is delivered.
func treePack(t *testing.T, name, from string) *Pack {
	t.Helper()
	p := addressedFilesPack(name, from)
	p.Root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(p.Root, filepath.FromSlash(from), "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A tree whose source is not in its pack's tree is NOT DELIVERED — the jail skips its mount with a
// warning, `yolo host apply` refuses it as missing — so it is not listed either: a typo'd `from`,
// or an only/exclude filter in `packs` that dropped the folder, lists nothing.
func TestNoRegistrationForATreeThatIsNotDelivered(t *testing.T) {
	missing := addressedFilesPack("matt", "files/pi")
	missing.Root = t.TempDir()
	if got := Registrations([]*Pack{registeringOwner(), missing}); len(got) != 0 {
		t.Errorf("Registrations for a tree with no source = %+v, want none", got)
	}
	file := addressedFilesPack("acme", "pi.ts")
	file.Root = t.TempDir()
	if err := os.WriteFile(filepath.Join(file.Root, "pi.ts"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Registrations([]*Pack{registeringOwner(), file}); len(got) != 1 || got[0].Pack != "acme" {
		t.Errorf("Registrations for a delivered single-file source = %+v, want its one entry", got)
	}
}

// A PACK THE JAIL LOADED lands under the name the LAUNCH loaded it by (LaunchName), not its own
// Name, which in the jail is its staged directory: the launch mounted the tree under that name, so
// the landing and the entry naming it must spell it too.
func TestALandingIsSpelledWithTheLaunchsName(t *testing.T) {
	jailSide := treePack(t, "my_5fpack", "files/pi")
	jailSide.LaunchName = "my_pack"
	set := []*Pack{registeringOwner(), jailSide}
	const want = registerSlot + "/my_pack"

	regs := Registrations(set)
	if len(regs) != 1 || regs[0].Landing != want || regs[0].Entry != "~/"+want {
		t.Fatalf("Registrations = %+v, want the landing %s", regs, want)
	}
	// Attributed to the pack as the jail names it, which is every other entry's attribution there.
	if regs[0].Pack != "my_5fpack" {
		t.Errorf("the entry is attributed to %q, want the jail's name for the pack, my_5fpack", regs[0].Pack)
	}
	resolved, _ := ResolveDestinations(set)
	var delivered []string
	for _, p := range resolved {
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindFiles && c.Agent == "" && c.Into != "" {
				delivered = append(delivered, c.Into)
			}
		}
	}
	if !reflect.DeepEqual(delivered, []string{want}) {
		t.Errorf("delivery lands the tree at %v, want %s", delivered, want)
	}
}
