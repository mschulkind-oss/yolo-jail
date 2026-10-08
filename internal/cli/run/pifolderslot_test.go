package run

// pifolderslot_test.go pins the DELIVERY half of OQ-PR1 (docs/design/pack-pi-resources.md) against
// the SHIPPED pi pack: a pack's one pi folder is bound read-only at the slot's landing in the jail,
// written there at the host, and that landing is the very path pi's settings list
// (packload.Registrations), so a folder is never listed where it was not delivered.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
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

// stagedPiFolderPack stages the shipped pi pack and a pack `name` addressing pi with files/pi
// through the launch's own stager, `entry` being the pack's whole `packs` element with SRC standing
// for its source address. It returns the staged tree and the packs the launch loaded out of it.
func stagedPiFolderPack(t *testing.T, name, entry string) (string, []*packload.Pack) {
	t.Helper()
	home := packHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	src := filepath.Join(t.TempDir(), name)
	writeHostFileAt(t, filepath.Join(src, "pack.json"),
		`{"contributes":[{"kind":"files","agents":["pi"],"from":"files/pi"}]}`, 0o644)
	writeHostFileAt(t, filepath.Join(src, "files", "pi", "extensions", "tok-rate.ts"),
		"export default function () {}\n", 0o644)
	writeUserPacks(t, home, `["pi", `+strings.ReplaceAll(entry, "SRC", "file://"+src)+`]`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	tree, loaded, _, err := o.stagePacks("yolo-test-pifolder-" + strings.ReplaceAll(name, "_", "-"))
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	return tree, loaded
}

// bootedPiPackages is what the jail lists in pi's `packages` from the packs, the way the boot
// derives it: the packs loaded out of the staged tree as entrypoint.LoadJailPacks loads them, and
// the list entries packoverlay.Collect places over them.
func bootedPiPackages(t *testing.T, tree string) []any {
	t.Helper()
	strictPackloadReads(t)
	// LoadJailPacks switches the process to tolerant manifest reads; strictPackloadReads
	// leaves the host-side test process strict after cleanup.
	e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": tree})
	e.Stderr = discardBuf()
	booted, err := entrypoint.LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	set := packoverlay.Collect(booted, false, nil)
	if len(set.Problems) != 0 {
		t.Fatalf("collecting the booted packs: %v", set.Problems)
	}
	var out []any
	for _, l := range set.ListsFor("pi", "settings") {
		out = append(out, l.Add...)
	}
	return out
}

// A PACK WHOSE NAME IS NOT SLUG-CLEAN is listed where the launch mounts its folder. The launch
// mounts the folder under the pack's config name, while the jail names a configured pack by its
// staged directory, which is that name's slug (packload.Pack.StagedSlug: `my_pack` stages as
// `my_5fpack`). pi skips a listed path that does not exist without a word, so an entry spelled
// with the slug loads nothing and nothing says so. Each half is derived the way it really is:
// the mount packFilesTargets emits for the packs the launch staged, and the entry the boot places
// over the packs it loads out of that same tree.
func TestAPiFolderOfANonSlugCleanPackIsListedWhereTheLaunchMountsIt(t *testing.T) {
	tree, loaded := stagedPiFolderPack(t, "my_pack", `"SRC"`)
	var content *packload.Pack
	for _, p := range loaded {
		if p.Name == "my_pack" {
			content = p
		}
	}
	if content == nil || content.StagedSlug() == content.Name {
		t.Fatalf("fixture: want a pack named my_pack staged under its slug, got %+v", content)
	}

	var mounted []string
	for _, tg := range packFilesTargets(loaded, nil) {
		if tg.Pack == content.Name {
			mounted = append(mounted, "~/"+filepath.ToSlash(tg.Dest))
		}
	}
	if len(mounted) != 1 || mounted[0] != "~/.pi/agent/yolo-packs/my_pack" {
		t.Fatalf("the launch mounts the folder at %v, want ~/.pi/agent/yolo-packs/my_pack", mounted)
	}
	if got := bootedPiPackages(t, tree); !slices.Contains(got, any(mounted[0])) {
		t.Errorf("the jail lists %v in pi's packages, want %s, where the launch mounts the folder",
			got, mounted[0])
	}
}

// A FOLDER THAT IS NOT DELIVERED IS NOT LISTED. An only/exclude filter in `packs` that drops the
// folder, or a `from` naming nothing, leaves no source in the staged tree: the launch skips the
// mount with a warning, so pi's packages must not list the landing either.
func TestAPiFolderTheLaunchDoesNotDeliverIsNotListed(t *testing.T) {
	tree, loaded := stagedPiFolderPack(t, "matt", `{"source":"SRC","name":"matt","exclude":["files/pi/**"]}`)
	for _, tg := range packFilesTargets(loaded, nil) {
		if tg.Pack == "matt" && (isDir(tg.Src) || isFile(tg.Src)) {
			t.Fatalf("fixture: the filter left the folder in the staged tree at %s", tg.Src)
		}
	}
	if regs := packload.Registrations(loaded); len(regs) != 0 {
		t.Errorf("the launch's packs register %+v for a folder it does not deliver", regs)
	}
	if got := bootedPiPackages(t, tree); len(got) != 0 {
		t.Errorf("the jail lists %v in pi's packages for a folder it does not deliver", got)
	}
}
