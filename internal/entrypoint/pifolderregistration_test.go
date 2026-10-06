package entrypoint

// pifolderregistration_test.go is the behavioral proof of OQ-PR1 (docs/design/pack-pi-resources.md):
// a content pack that addresses pi with ONE folder is listed in pi's `packages` beside the user's
// own entries, in a jail and at `yolo host apply`, and leaves that list when the pack is dropped
// or yolo is reverted out of the home. The slot owner is the SHIPPED pi pack, so deleting its
// `register` declaration, or the emission in packoverlay.Collect, turns these red.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// piFolderEntry is what pi's settings must list for the fixture's folder: the landing the jail
// mounts and the host writes, under `~` so one entry names it at both notches (PR-D3).
const piFolderEntry = "~/.pi/agent/yolo-packs/matt"

// piFolderPack is the maintainer's pack after the migration (design §4): one entry addressing pi,
// over a folder laid out as a pi package.
func piFolderPack(t *testing.T) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"pack.json":                       `{"name":"matt","contributes":[{"kind":"files","agents":["pi"],"from":"files/pi"}]}`,
		"files/pi/extensions/tok-rate.ts": "export default function () {}\n",
		"files/pi/themes/nord.json":       "{}\n",
	} {
		plant(t, root, rel, body)
	}
	p, problems := packload.LoadDir(root, "matt")
	if len(problems) != 0 {
		t.Fatalf("loading the folder pack: %v", problems)
	}
	return p
}

// THE JAIL: the folder is listed, after the pi pack's own entries, attributed to the pack whose
// folder it is.
func TestJailListsAPacksPiFolderInPiPackages(t *testing.T) {
	e, home := piMCPEnv(t)
	var errw bytes.Buffer
	e.Stderr = &errw
	bootJail(t, e, shippedPi(t), piFolderPack(t))
	if got := packagesAt(t, home); !containsString(got, piFolderEntry) {
		t.Fatalf("pi's packages = %#v, want the folder %q listed", got, piFolderEntry)
	}
	if !bytes.Contains(errw.Bytes(), []byte("pi/settings: config-list entries from matt")) {
		t.Errorf("the boot did not name the pack the entry is from:\n%s", errw.String())
	}
}

// The user's own `pi install` survives beside the folder, and dropping the pack takes exactly
// the folder's entry out on the next boot.
func TestJailKeepsAPiInstallAndDropsTheFolderWithItsPack(t *testing.T) {
	e, home := piMCPEnv(t)
	pi, folder := shippedPi(t), piFolderPack(t)
	bootJail(t, e, pi, folder)
	editSettings(t, home, func(m map[string]any) {
		list, _ := m["packages"].([]any) // nil when the folder was never listed
		m["packages"] = append(list, listInstalled)
	})
	bootJail(t, e, pi, folder)
	wantPackages(t, home, piFolderEntry, listInstalled)

	bootJail(t, e, pi) // the folder pack left `packs`
	wantPackages(t, home, listInstalled)
}

// No pack addressing pi: the registering slot changes nothing in the file pi reads, byte for
// byte, against the same pack with no slot at all.
func TestASlotNobodyAddressesLeavesPiSettingsByteIdentical(t *testing.T) {
	withSlot := shippedPi(t)
	if !declaresRegisteringSlot(withSlot) {
		t.Fatal("the shipped pi pack declares no registering files slot, so this compares nothing")
	}
	decl := *withSlot.Decl
	decl.Contributes = nil
	for _, c := range withSlot.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.Register != nil {
			continue
		}
		decl.Contributes = append(decl.Contributes, c)
	}
	without := *withSlot
	without.Decl = &decl

	render := func(p *packload.Pack) []byte {
		e, home := piMCPEnv(t)
		bootJail(t, e, p)
		data, err := os.ReadFile(filepath.Join(home, listSettings))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if a, b := render(withSlot), render(&without); !bytes.Equal(a, b) {
		t.Fatalf("a slot no pack addresses changed pi's settings:\nwith:    %s\nwithout: %s", a, b)
	}
}

func declaresRegisteringSlot(p *packload.Pack) bool {
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.Agent == "pi" && c.Register != nil {
			return true
		}
	}
	return false
}

// THE HOST: `yolo host apply` lists the folder beside an entry the user already had, dropping
// the pack takes the folder's entry out and keeps the user's, and --revert takes out what yolo
// inserted.
func TestHostApplyListsThePiFolderAndForgetsItWithItsPack(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			plant(t, home, listSettings, `{"packages":["npm:mine"]}`)
			pis := testPacksForAgent(t, "pi")
			withFolder := append(append([]*packload.Pack(nil), pis...), piFolderPack(t))

			// Membership, not order: under `own` the first render adopts the file, and an adopted
			// entry folds after the contributions (config-list's rule, not this one's).
			hostApplyPi(t, home, ownership, withFolder)
			if got := packagesAt(t, home); len(got) != 2 || !containsString(got, "npm:mine") ||
				!containsString(got, piFolderEntry) {
				t.Fatalf("packages = %#v, want your npm:mine and the folder %q", got, piFolderEntry)
			}

			hostApplyPi(t, home, ownership, pis) // the folder pack left `packs`
			wantPackages(t, home, "npm:mine")
		})
	}
}

func TestHostRevertTakesThePiFolderEntryOut(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	plant(t, home, listSettings, `{"packages":["npm:mine"]}`)
	pis := testPacksForAgent(t, "pi")
	hostApplyPi(t, home, render.OwnershipOwn, append(append([]*packload.Pack(nil), pis...), piFolderPack(t)))
	// Membership, not order: under `own`, the one writing mode since the `assert` retirement, the
	// first render adopts the file, and an adopted entry folds after the contributions.
	if got := packagesAt(t, home); len(got) != 2 || !containsString(got, "npm:mine") ||
		!containsString(got, piFolderEntry) {
		t.Fatalf("packages = %#v, want your npm:mine and the folder %q", got, piFolderEntry)
	}

	if _, err := RevertHostRender(pis, home, false); err != nil {
		t.Fatalf("revert: %v", err)
	}
	wantPackages(t, home, "npm:mine")
}

// hostApplyPi renders pi's settings the way `yolo host apply` does: the overlay set collected
// across the whole selection, then the pi pack rendered against it.
func hostApplyPi(t *testing.T, home string, ownership render.HostOwnership, packs []*packload.Pack) {
	t.Helper()
	in := hostTestInputs(t, packs, nil, nil, nil)
	var pi *packload.Pack
	for _, p := range packs {
		if p.Name == "pi" {
			pi = p
		}
	}
	results, err := RenderHostPack(pi, home, ownership, false, packoverlay.Collect(packs, false, nil), in)
	if err != nil {
		t.Fatalf("RenderHostPack(pi): %v", err)
	}
	if r := resultFor(t, results, "pi/settings"); r.Action != "rendered" && r.Action != "unchanged" {
		t.Fatalf("pi/settings at the host: %q", r.Action)
	}
}
