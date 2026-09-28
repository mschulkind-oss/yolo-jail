package cli

// hostpackresolve_test.go pins the HOST notch's call sites of the one pack resolver
// (config.ResolvePack, docs/plans/notch-convergence.md item 5): every configured pack a host verb
// reads is STAGED, through packstage.Stage and the entry's filters, into this process's pack tree
// (resolveConfiguredPack), and the embedded packs come from the one materialization and say so
// when it is broken. The launch's call sites are pinned in internal/cli/run
// (packresolvelaunch_test.go), the resolver itself in internal/config.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// withEmbeddedFS swaps the embedded pack filesystem for one test, releasing the loaded packs on
// both sides so the next Embedded() reads the swapped one.
func withEmbeddedFS(t *testing.T, f fstest.MapFS) {
	t.Helper()
	packload.ReleaseEmbedded()
	packload.SetEmbeddedFS(f)
	t.Cleanup(func() {
		packload.ReleaseEmbedded()
		packload.SetEmbeddedFS(packs.FS)
	})
}

// AN EXCLUDED SKILL IS ABSENT AT THE HOST, as it is in every jail. The host used to read the
// declaration from the filtered tree and then point Pack.Root back at the source, so host apply
// composed skills out of the UNFILTERED pack: the entry's `exclude` removed the skill from every
// jail and still delivered it to the real home (row B3).
func TestHostApplyDeliversNoSkillItsEntryExcludes(t *testing.T) {
	kept := map[string]string{
		"skills/keptskill/SKILL.md": "---\nname: keptskill\ndescription: d\n---\nKept.\n",
	}
	home := filteredPackHome(t, cleanFltManifest, kept, `,"exclude":["skills/fltskill"]`, "")
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "fltskill")); !os.IsNotExist(err) {
		t.Errorf("host apply delivered a skill the pack's entry excludes (stat err=%v)\n%s", err, report)
	}
	mustExist(t, filepath.Join(home, ".claude", "skills", "keptskill", "SKILL.md"),
		"the skill the filter keeps")
}

// EVERY HOST READ IS OF THE STAGED COPY: resolveConfiguredPack hands back a pack whose Root is in
// this process's pack tree, never the source, and whose SourcePath maps back to the source for a
// message. Fails if the resolver goes back to reading a local pack in place.
func TestHostResolverStagesALocalPackIntoTheProcessTree(t *testing.T) {
	filteredPackHome(t, cleanFltManifest, nil, "", "")
	entries, err := config.LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	var flt config.PackEntry
	for _, e := range entries {
		if e.Name == "flt" {
			flt = e
		}
	}
	p, err := resolveConfiguredPack(flt)
	if err != nil {
		t.Fatal(err)
	}
	tree := packload.ProcessTreeLocation()
	if tree == "" || !strings.HasPrefix(p.Root, tree+string(filepath.Separator)) {
		t.Fatalf("the host read pack flt at %s, want a staged copy under the process tree %q", p.Root, tree)
	}
	src := strings.TrimPrefix(flt.Source, "file://")
	if got := p.SourcePath(filepath.Join(p.Root, "skills", "fltskill")); got != filepath.Join(src, "skills", "fltskill") {
		t.Errorf("SourcePath = %s, want the path under the source %s", got, src)
	}
}

// AN EMBEDDED ENTRY'S FILTERS APPLY AT THE HOST TOO, where the pack used to be handed out whole.
func TestHostResolverAppliesAnEmbeddedEntrysFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectPacks(t, home, `{"source":"hello-daemon","exclude":["README.md"]}`)
	entries, err := config.LoadPacks(nil)
	if err != nil || len(entries) == 0 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	p, err := resolveConfiguredPack(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "README.md")); !os.IsNotExist(err) {
		t.Errorf("the host resolver handed out an embedded pack with the file its entry excludes (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "pack.json")); err != nil {
		t.Errorf("control: the rest of the pack must be there: %v", err)
	}
}

// A BROKEN EMBEDDED MATERIALIZATION IS A YOLO BUG AT THE HOST, as it is fatal at the launch —
// never "this build ships no pack by that name", which is how the host read Embedded()'s empty
// answer (row B7) and which sent the user to fix a config line that was right.
func TestHostResolverNamesABrokenEmbeddedMaterializationAsAYoloBug(t *testing.T) {
	withEmbeddedFS(t, fstest.MapFS{"broken/pack.json": {Data: []byte("{not json")}})
	_, err := resolveConfiguredPack(config.EmbeddedPackEntry("broken"))
	if err == nil || !strings.Contains(err.Error(), "yolo bug") || strings.Contains(err.Error(), "ships no pack") {
		t.Fatalf("resolveConfiguredPack = %v, want a yolo bug named", err)
	}
}

// `yolo pack explain` GOES THROUGH THE ONE RESOLVER, so an embedded entry's filters are explained
// rather than refused ("its content is fixed, so there are no only/exclude filters to explain",
// which stopped being true when the filters began to apply).
func TestPackExplainExplainsAnEmbeddedEntrysFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectPacks(t, home, `{"source":"hello-daemon","exclude":["README.md"]}`)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"explain", "hello-daemon"}, &out, &errw, false); rc != 0 {
		t.Fatalf("explain rc=%d: %s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "filtered out 1 file") || !strings.Contains(got, "README.md") ||
		!strings.Contains(got, "pack.json") {
		t.Errorf("explain must list what stages and what the filter dropped:\n%s", got)
	}
}
