package run

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestDirectScopedPackRecordedAttachIgnoresDeletedSource(t *testing.T) {
	user := testconfig.User(t, `packs = [{ pack = "local/corpus", skills = { only = ["*"] } }]`+"\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	source := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for name, data := range map[string]string{"with-marker/SKILL.md": "chosen\n", "no-marker/notes.md": "folder\n"} {
		file := filepath.Join(source, "skills", name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var report bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stderr: &report, Stdout: &report}
	const cname = "yolo-test-direct-scoped-record"
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { discardPackTree(cname, root) })
	staged := stagedPacks{root: root, packs: packs, briefings: briefings}
	if !o.prepareFreshSkillPayloads(cname, "podman", staged) {
		t.Fatal(report.String())
	}
	copyRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer copyRoot.Close()
	if err := copyPackTreeBeneath(root, copyRoot, "copied"); err != nil {
		t.Fatal(err)
	}
	copied, err := loadPackTree(filepath.Join(copyRoot.Name(), "copied"))
	if err != nil || len(copied) != len(packs) {
		t.Fatalf("copied scoped tree: %v %v", copied, err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := packsrc.RemoveSealedSkillTree(user.Root, paths.PacksDir()); err != nil {
		t.Fatal(err)
	}
	if err := writeLivePackTree(cname, root); err != nil {
		t.Fatal(err)
	}
	view, skew := o.runningJailPackView(cname, "podman", jsonx.NewOrderedMap(), staged, nil, "true")
	if skew != nil || view.staged.root != root {
		t.Fatalf("source-free adopted attach: %+v %+v", view, skew)
	}
	host := view.staged.packs
	packload.MapSkillPayloadTargets(host, root)

	guest, err := entrypoint.LoadJailPacks(&entrypoint.Env{Home: user.Root, Workspace: o.Workspace, Vars: map[string]string{"YOLO_PACK_ROOT": root}})
	if err != nil {
		t.Fatal(err)
	}
	for _, loaded := range [][]*packload.Pack{host, guest} {
		found := false
		for _, p := range loaded {
			if p.Name != "local/corpus" {
				continue
			}
			found = true
			if len(p.SourcedSkillRows()) != 2 || len(p.Plugins()) != 0 || len(p.InstallBins()) != 0 {
				t.Fatalf("record lost projection: %+v", p)
			}
			rows := p.SourcedSkillRows()
			if rows[0].Plan == nil || rows[0].Plan.MarkerState != "absent" || rows[1].Plan == nil || rows[1].Plan.MarkerState != "present" {
				t.Fatalf("record lost explicit marker state: %+v", rows)
			}
		}
		if !found {
			t.Fatal("record lost scoped entry")
		}
		reader := &packload.Pack{Name: "reader", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Agent: "fixture", Into: ".fixture/skills"}}}}
		loaded = append(loaded, reader)
		for i, p := range loaded {
			loaded[i] = p.ResolveDestinations(loaded).Pack
		}
		home := t.TempDir()
		destinations := hostskills.ComposeHostSkillsFor(loaded, home, func(string, packdecl.Kind) bool { return true })
		_, err := hostskills.RenderHostSkills(destinations, hostskills.ComposeRequest{Composed: &hostskills.Manifest{}, ArchiveRoot: hostskills.ArchiveRoot(t.TempDir()), Stamp: "fixture", PackSetComplete: true, Configured: map[string]bool{"local/corpus": true, "reader": true}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(filepath.Join(home, ".fixture/skills/corpus-no-marker/notes.md")); err != nil || string(data) != "folder\n" {
			t.Fatalf("source-free folder composition: %q %v", data, err)
		}
		if _, err := os.Lstat(filepath.Join(home, ".fixture/skills/corpus-no-marker/SKILL.md")); !os.IsNotExist(err) {
			t.Fatalf("replay invented marker: %v", err)
		}
	}
}
