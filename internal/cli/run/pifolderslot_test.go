package run

// pifolderslot_test.go pins the DELIVERY half of OQ-PR1 (docs/design/pack-pi-resources.md) against
// the SHIPPED pi pack: a pack's one pi folder is bound read-only at the slot's landing in the jail,
// written there at the host, and that landing is the very path pi's settings list
// (packload.Registrations), so a folder is never listed where it was not delivered.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const piFolderLanding = ".pi/agent/yolo-packs/matt"

func shippedPiPack(t *testing.T) *packload.Pack {
	t.Helper()
	for _, p := range packload.Embedded() {
		if p.Name == "pi" {
			return p
		}
	}
	t.Fatal("no embedded pi pack")
	return nil
}

// piFolderContentPack addresses pi with one folder laid out as a pi package.
func piFolderContentPack(t *testing.T) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"pack.json":                       `{"name":"matt","contributes":[{"kind":"files","agents":["pi"],"from":"files/pi"}]}`,
		"files/pi/extensions/tok-rate.ts": "export default function () {}\n",
		"files/pi/themes/nord.json":       "{}\n",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, probs := packload.LoadDir(root, "matt")
	if len(probs) != 0 {
		t.Fatalf("loading the folder pack: %v", probs)
	}
	return p
}

func TestAPiFolderIsDeliveredWherePiListsIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	content := piFolderContentPack(t)
	set := []*packload.Pack{shippedPiPack(t), content}

	regs := packload.Registrations(set)
	if len(regs) != 1 || regs[0].Landing != piFolderLanding || regs[0].Entry != "~/"+piFolderLanding ||
		regs[0].Surface != "pi/settings" || regs[0].Path != "/packages" {
		t.Fatalf("registrations = %+v, want the folder listed in pi/settings /packages at %s",
			regs, piFolderLanding)
	}

	// THE JAIL: one read-only bind of the folder at that landing.
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.packs = append(in.packs, set...)
	want := []string{filepath.Join(content.Root, "files", "pi") + ":/home/agent/" + piFolderLanding + ":ro"}
	if got := filesMounts(o.assembleRunCmd(in), piFolderLanding); !slices.Equal(got, want) {
		t.Errorf("jail binds of the folder = %v, want %v", got, want)
	}

	// THE HOST: the folder's files are written under the same landing.
	got := hostFilesDests(t, set, t.TempDir())
	for _, f := range []string{piFolderLanding + "/extensions/tok-rate.ts", piFolderLanding + "/themes/nord.json"} {
		if !slices.Contains(got, f) {
			t.Errorf("host writes %v, want %s among them", got, f)
		}
	}
}
