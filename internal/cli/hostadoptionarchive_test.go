package cli

// hostadoptionarchive_test.go is the CROSS-PACKAGE half of OQ-CO7's adoption archive
// (docs/design/config-ownership-and-promotion.md §6.3.3): the three facts that are true of the
// archive only in relation to code outside internal/entrypoint, and that a test inside it
// therefore cannot state.
//
//  1. The `config` bucket sits under the SAME archive root the kind buckets do — one place a
//     user looks for "what did yolo move out of my way", not two.
//  2. `yolo prune` leaves it alone. That is the whole reason it is keyed by SURFACE rather than
//     by the stamp the other buckets use, and it would otherwise be untested: the layout
//     decision lives in render.Target and the reaper lives in internal/prune, and nothing in
//     either package knows the other exists.
//  3. `yolo host apply` NAMES THE COPY in its report. entrypoint can only populate
//     HostRenderResult.Archived; whether anyone ever prints it is decided here, and at this
//     notch the report is the archive's only disclosure channel (Env.Stderr is nil on the host
//     path by design).
//
// Both drive the REAL adoption (entrypoint.RenderHostPack under `own`) rather than writing a
// fixture that looks like an archive — a hand-built tree would keep passing after adoption
// stopped producing one, which is the shape this repo has shipped five times.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// adoptedHome renders a one-surface pack into a fresh home under `host_management: own` over a
// hand-written file, and returns the home plus the archive that adoption left.
func adoptedHome(t *testing.T) (home, archive string) {
	t.Helper()
	home = t.TempDir()
	path := filepath.Join(home, ".acme", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"apiKeyHelper":"/usr/local/bin/acme-key.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`[{"agent":"acme","name":"settings","codec":"json",` +
		`"path":"~/.acme/settings.json","defaults":{"theme":"system"}}]`)
	pack := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
	results, err := entrypoint.RenderHostPack(pack, home, render.OwnershipOwn, false, nil)
	if err != nil {
		t.Fatalf("`own` apply: %v", err)
	}
	if len(results) != 1 || results[0].Archived == "" {
		t.Fatalf("the owned render archived nothing: %+v", results)
	}
	return home, results[0].Archived
}

// ONE ARCHIVE ROOT. The config bucket is a bucket beside `skills`, `files`, `briefing` and
// `retired` — not a new tree — which is what hostArchiveRoot's docstring means by "a bucket
// that lies about its contents is worse than no bucket": the user looks in one place.
//
// It compares the two INDEPENDENT path builders rather than asserting a literal: internal/cli
// joins its buckets through hostArchiveRoot, render.Target joins the config one itself, and
// SidecarDir's docstring names exactly this hazard ("two hand-copied path builders that agree
// only by inspection"). This is the inspection, run.
func TestConfigBucketSitsUnderTheHostArchiveRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	fromCLI := string(hostArchiveRoot("config"))
	fromTarget := render.Host(home, nil, render.OwnershipOwn).ArchiveDir()
	if fromCLI != fromTarget {
		t.Errorf("the adoption archive and the kind buckets disagree about the archive root:\n"+
			"  hostArchiveRoot(\"config\") = %s\n  Target.ArchiveDir()       = %s\n\n"+
			"They must be the same directory, or a user who lost a key looks under the state "+
			"dir and does not find the copy the report named.", fromCLI, fromTarget)
	}
	// And it really is under the state dir, not beside the rendered file.
	if want := filepath.Join(paths.GlobalStorageUnder(home), "archive", "config"); fromTarget != want {
		t.Errorf("ArchiveDir() = %s, want %s", fromTarget, want)
	}
}

// PRUNE LEAVES IT ALONE, and this is the test the layout decision rests on.
//
// The other buckets hold content yolo REPLACED and can regenerate, so keep-newest-3 is right
// for them: what a user wants back is "the last few applies". The adoption archive holds the
// user's own pre-yolo file, which nothing regenerates, and there is exactly one per surface
// ever. Under the stamped layout the other buckets use, a home that adopted a dozen surfaces on
// a dozen days would have nine originals swept — by yolo's own reaper, for a retention policy
// nobody chose, out of the one directory that exists to prevent exactly that loss.
//
// Keying by SURFACE instead means prune cannot parse the directory as a generation, and its own
// stated rule — "prune does not delete what it cannot explain" — already covers it. So this
// needs no exemption in internal/prune, and this test is what says so out loud.
func TestPruneLeavesTheAdoptionArchiveAlone(t *testing.T) {
	home, archive := adoptedHome(t)
	root := filepath.Join(paths.GlobalStorageUnder(home), "archive")

	bytesRemoved, removed, names := prune.PruneHostArchiveBuckets(root, 0, true)
	if removed != 0 || bytesRemoved != 0 {
		t.Errorf("prune removed %d generation(s) (%d bytes) from the adoption archive: %v\n\n"+
			"Even at keep=0 — the most aggressive sweep there is — the config bucket must "+
			"survive: it is not an undo buffer of applies, it is the one copy of the user's "+
			"pre-yolo file. If this went red because the bucket moved to the stamped layout, "+
			"that layout hands keep-newest-N the originals of every surface but the newest few.",
			removed, bytesRemoved, names)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("the archived file is gone after a prune: %v", err)
	}
}

// ownApplyFixture is a real home under `host_management: own` with a hand-written surface file
// the render will adopt, ready for `yolo host apply`. The seed is compact and unsorted so the
// render visibly rewrites it — an adoption that changed nothing would make the assertions below
// pass against a home yolo never touched.
func ownApplyFixture(t *testing.T) (home, settings, seed string) {
	t.Helper()
	home = t.TempDir()
	selectPacks(t, home, `"claude"`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_management":"own"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	seed = `{"apiKeyHelper":"/usr/local/bin/acme-key.sh","permissions":{"ask":["Bash(rm:*)"]}}`
	settings = filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, seed)
	return home, settings, seed
}

// THE APPLY SAYS WHERE THE COPY WENT, through the real report `yolo host apply --assert` prints.
//
// ⚠ THIS IS THE CALL-SITE HALF OF THE DISCLOSURE, and without it the feature is pinned exactly
// one level short: entrypoint's tests assert HostRenderResult.Archived is POPULATED, and nothing
// asserts anyone ever prints it. Deleting the `if r.Archived != ""` block from
// applyHostSurveyed left the whole suite green — the callee-pinned-call-site-unpinned shape
// AGENTS.md says this repo has shipped five times.
//
// It is not a cosmetic line. At the host notch entrypoint's Env.Stderr is nil by design, so this
// report is the ONLY channel that names the archive; and by OQ-RO3 a disclosure is never
// suppressible, which is why the line is not behind detail(). An archive the user cannot find is
// a deletion from where they stand.
func TestHostApplyReportsWhereTheAdoptedFileWent(t *testing.T) {
	_, settings, seed := ownApplyFixture(t)

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("`own` apply rc=%d\n%s", rc, report)
	}
	if rendered, err := os.ReadFile(settings); err != nil || string(rendered) == seed {
		t.Fatalf("the apply did not adopt the file (err=%v) — nothing was archived, so this "+
			"test is not measuring the report\n%s", err, report)
	}
	archive := filepath.Join(paths.GlobalStorageUnder(filepath.Dir(filepath.Dir(settings))),
		"archive", "config", "claude-settings", "settings.json")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("the apply adopted %s and archived nothing: %v\n%s", settings, err, report)
	}
	if !strings.Contains(report, archive) {
		t.Errorf("the apply archived the user's file and never named the copy:\n%s\n\nwant a "+
			"line naming %s — HostRenderResult.Archived is carried all the way to the report or "+
			"it is carried nowhere.", report, archive)
	}
	// And the report is TRUE: the path it names holds the file as yolo found it, not yolo's
	// own output. A report naming a copy of the render would be worse than none.
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != seed {
		t.Errorf("the reported archive holds:\n%s\nwant the file as yolo found it:\n%s", got, seed)
	}
}

// A DRY RUN NAMES NO ARCHIVE, because it made none. The line reports a copy that WAS taken, not
// one an --assert would take, so printing it here would point the user at a path that does not
// exist — the one lie a net cannot afford.
func TestHostApplyDryRunNamesNoArchive(t *testing.T) {
	home, _, _ := ownApplyFixture(t)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("dry run rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "archived your file") {
		t.Errorf("the dry run announced an archive it did not write:\n%s", report)
	}
	if _, err := os.Stat(filepath.Join(paths.GlobalStorageUnder(home), "archive")); err == nil {
		t.Errorf("the dry run created an archive root under %s", home)
	}
}
