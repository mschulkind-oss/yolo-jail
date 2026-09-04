package entrypoint

// versionprune_test.go RUNS A7, the V-axis prune
// (docs/design/agent-cli-copies.md §5.1, adopted by
// docs/plans/evergreen-agent-updates.md's A7 section).
//
// WHAT IT IS FOR, in one measurement: this development jail's one workspace held five
// claude builds totalling 1223.4 MiB, of which 1018.6 MiB — 83.3 % — were referenced by
// nothing. The vendor's own updater wrote them; nothing ever removed them. Under evergreen
// that is recurring rather than one-off, and under OQ-PD18 (auto-capture default-on) every
// NEW workspace is additionally seeded with a superseded version the vendor updater will
// never touch. This is what deletes it, at the moment the update creates it.
//
// EVERY CELL HERE IS ABOUT AN `rm -rf`, so each one is written to catch a different way of
// deleting too much: the live version, a version inside the keep window, a tree the live
// symlink does not point into, and a directory that is not a version directory at all.

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// seedVersions lays out the shape claude's installer produces: N version directories under
// ~/.local/share/<bin>/versions, each holding one executable, with ~/.local/bin/<bin> an
// ABSOLUTE symlink naming exactly one of them. Ages are staggered so "newest" is decidable.
//
// It returns the version dir paths, oldest first.
func seedVersions(t *testing.T, home, bin string, names []string, liveIdx int) []string {
	t.Helper()
	vdir := filepath.Join(home, ".local", "share", bin, "versions")
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i, name := range names {
		dir := filepath.Join(vdir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " +
			shellQuoteForTest(filepath.Join(home, "argv.log")) + "\n"
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		// Oldest first: names[0] is the most stale.
		age := time.Duration(len(names)-i) * time.Hour
		when := time.Now().Add(-age)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, dir)
	}
	link := filepath.Join(binDir, bin)
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join(paths[liveIdx], bin), link); err != nil {
		t.Fatal(err)
	}
	return paths
}

// remaining lists the version-directory basenames still on disk, sorted.
func remaining(t *testing.T, home, bin string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(home, ".local", "share", bin, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// pruneProbe builds a launcher whose update verb is a no-op, seeds a version tree, and runs
// it once — so the only thing that can have changed the tree is the prune.
func pruneProbe(t *testing.T, names []string, liveIdx int) (*updateProbe, []string, string, int) {
	t.Helper()
	p := newUpdateProbe(t, []string{"noop"}, true, false /* seed no REAL_BIN; seedVersions does */)
	paths := seedVersions(t, p.home, "probetool", names, liveIdx)
	out, rc := p.run(t)
	return p, paths, out, rc
}

// TestVersionPruneKeepsNewestKAndTheLiveOne is A7's whole rule, run.
//
// Five versions in, two out — and the SPECIFIC two: the newest K by mtime. It fails if the
// prune is deleted (five remain), if K is wrong, or if the sort is inverted (which is the
// mutation that would delete the live build and every recent one).
func TestVersionPruneKeepsNewestKAndTheLiveOne(t *testing.T) {
	names := []string{"2.1.165", "2.1.218", "2.1.219", "2.1.220", "2.1.260"}
	p, _, out, rc := pruneProbe(t, names, 4 /* the newest is live */)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := remaining(t, p.home, "probetool")
	want := []string{"2.1.220", "2.1.260"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("keep-newest-2 left %v, want %v\n%s", got, want, out)
	}
	// The prune must SAY what it removed. A silent deletion of 200 MB is not something a
	// user should have to diff a directory to discover.
	if !strings.Contains(out, "removed superseded version 2.1.165") {
		t.Errorf("the prune must name what it removed:\n%s", out)
	}
}

// TestVersionPruneNeverRemovesTheLiveVersion is the cell that matters most, because the
// failure it catches is unrecoverable: an old symlink target that the mtime sort would
// otherwise class as stale.
//
// The live version here is the OLDEST of five — the shape a rollback leaves behind, and the
// one where "keep the newest K" and "keep the live one" disagree.
func TestVersionPruneNeverRemovesTheLiveVersion(t *testing.T) {
	names := []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0", "5.0.0"}
	p, _, out, rc := pruneProbe(t, names, 0 /* the OLDEST is live */)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := remaining(t, p.home, "probetool")
	if !slices.Contains(got, "1.0.0") {
		t.Fatalf("the LIVE version was deleted — the launcher now points at nothing: %v\n%s",
			got, out)
	}
	// And the program still runs, which is the property the assertion above is a proxy
	// for: the launcher must have been able to exec through the symlink afterwards.
	if log := p.argvLog(t); len(log) == 0 || log[len(log)-1] != "RAN:" {
		t.Errorf("the program must still be runnable after a prune, argv log %v\n%s", log, out)
	}
}

// TestVersionPruneDoesNothingWithoutASymlinkIntoTheTree: no symlink, no known referrer set,
// no deletion. This is what makes the prune safe to call for every native program rather
// than only the ones whose layout it recognises — a vendor that installs a REAL FILE at
// ~/.local/bin/<bin> and keeps a versions/ directory for its own reasons must be left alone.
func TestVersionPruneDoesNothingWithoutASymlinkIntoTheTree(t *testing.T) {
	p := newUpdateProbe(t, []string{"noop"}, true, true /* a real file at REAL_BIN */)
	names := []string{"a", "b", "c", "d"}
	vdir := filepath.Join(p.home, ".local", "share", "probetool", "versions")
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(vdir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := remaining(t, p.home, "probetool"); len(got) != len(names) {
		t.Errorf("nothing may be deleted when the live version cannot be named: %v", got)
	}
}

// TestVersionPruneIgnoresASymlinkPointingElsewhere is the same guard from the other side: a
// ~/.local/bin/<bin> that resolves OUTSIDE the versions tree (a hand-made symlink to a
// checkout, say) means the referrer set is not the one this rule assumes.
func TestVersionPruneIgnoresASymlinkPointingElsewhere(t *testing.T) {
	p := newUpdateProbe(t, []string{"noop"}, true, false)
	seedVersions(t, p.home, "probetool", []string{"a", "b", "c"}, 2)
	// Repoint the launcher's binary at something outside the tree.
	elsewhere := filepath.Join(p.home, "elsewhere", "probetool")
	if err := os.MkdirAll(filepath.Dir(elsewhere), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(elsewhere, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.home, ".local", "bin", "probetool")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}

	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := remaining(t, p.home, "probetool"); len(got) != 3 {
		t.Errorf("a symlink resolving outside the versions tree must prune nothing: %v", got)
	}
}

// TestVersionPruneIsANoOpUnderTheKeepWindow: two versions and K=2 means nothing happens.
// Without this cell, a prune that deleted everything but the live one would still pass the
// keep-newest-K cell above (which uses five).
func TestVersionPruneIsANoOpUnderTheKeepWindow(t *testing.T) {
	p, _, out, rc := pruneProbe(t, []string{"1.0.0", "2.0.0"}, 1)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	if got := remaining(t, p.home, "probetool"); len(got) != 2 {
		t.Errorf("K=2 with two versions must delete nothing, left %v\n%s", got, out)
	}
	if strings.Contains(out, "removed superseded version") {
		t.Errorf("nothing should have been removed:\n%s", out)
	}
}

// TestVersionPruneHandlesTheSingleFileLayout is the shape that was actually MEASURED:
// ~/.local/share/claude/versions/<version> is a FILE, not a directory, and
// ~/.local/bin/claude is an absolute symlink naming one of them.
//
// It is a separate cell from the directory layout because the two put the live entry at
// different depths, and the guard that keeps the running version alive has to resolve the
// symlink's target back to a directory ENTRY either way. Written against the directory
// layout alone, that guard compares a target three segments deep against entries two deep,
// never matches, and deletes the live build — which is what this pair caught.
func TestVersionPruneHandlesTheSingleFileLayout(t *testing.T) {
	p := newUpdateProbe(t, []string{"noop"}, true, false)
	vdir := filepath.Join(p.home, ".local", "share", "probetool", "versions")
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"2.1.165", "2.1.218", "2.1.219", "2.1.220", "2.1.260"}
	body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " +
		shellQuoteForTest(filepath.Join(p.home, "argv.log")) + "\n"
	for i, n := range names {
		f := filepath.Join(vdir, n)
		if err := os.WriteFile(f, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Duration(len(names)-i) * time.Hour)
		if err := os.Chtimes(f, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(p.home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(vdir, "2.1.260"),
		filepath.Join(p.home, ".local", "bin", "probetool")); err != nil {
		t.Fatal(err)
	}

	out, rc := p.run(t)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := remaining(t, p.home, "probetool")
	want := []string{"2.1.220", "2.1.260"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("keep-newest-2 over single-file builds left %v, want %v\n%s", got, want, out)
	}
}

// TestVersionPruneRunsOnTheCOLDINSTALLPathToo is OQ-PD18's prerequisite, and it is the half
// that is easy to leave out.
//
// Auto-capture materializes whatever the store holds into a NEW workspace; the first
// evergreen update then installs past it. If the prune only ran on the update path this
// would still be right — but the cold path can also produce a second version (a capture
// entry plus whatever a re-run installs), and the rule is "whoever installed the new one
// prunes", not "whoever updated". Asserted by driving the cold arm: nothing at REAL_BIN, an
// installer that lands a new version and repoints the symlink.
func TestVersionPruneRunsOnTheColdInstallPathToo(t *testing.T) {
	p := newUpdateProbe(t, nil, true, false)
	seedVersions(t, p.home, "probetool", []string{"1.0.0", "2.0.0", "3.0.0"}, 2)
	// Remove the symlink so the launcher takes the cold-install arm, and serve an
	// "installer" that recreates it pointing at a fresh version.
	if err := os.Remove(filepath.Join(p.home, ".local", "bin", "probetool")); err != nil {
		t.Fatal(err)
	}
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		"set -eu",
		`v="$HOME/.local/share/probetool/versions/4.0.0"`,
		`mkdir -p "$v"`,
		`printf '#!/bin/bash\necho INSTALLED_RAN\n' > "$v/probetool"`,
		`chmod +x "$v/probetool"`,
		`ln -sf "$v/probetool" "$HOME/.local/bin/probetool"`,
	}, "\n")+"\n")
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url},
		p.stamps, filepath.Join(p.home, "ws", ".yolo", "receipts.jsonl"), "", true)
	if err := os.WriteFile(p.script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	out, rc := p.run(t)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := remaining(t, p.home, "probetool")
	want := []string{"3.0.0", "4.0.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("a cold install must prune too, left %v want %v\n%s", got, want, out)
	}
}
