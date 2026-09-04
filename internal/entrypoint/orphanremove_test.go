package entrypoint

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// orphanremove_test.go covers OQ-PD4's REMOVAL half: the act removes what the catalog named,
// says what it will remove before removing it, and is reachable from a boot only through an
// option that is off by default.

// seedNpmPackage writes a package directory with one file of the given size, and links each
// bin name into $NPM_CONFIG_PREFIX/bin the way npm does — relatively, `../lib/node_modules/…`.
func seedNpmPackage(t *testing.T, home, name string, size int, bins ...string) string {
	t.Helper()
	pkg := filepath.Join(home, ".npm-global", "lib", "node_modules", name)
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "index.js"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, ".npm-global", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range bins {
		target := filepath.Join("..", "lib", "node_modules", name, "index.js")
		if err := os.Symlink(target, filepath.Join(binDir, b)); err != nil {
			t.Fatal(err)
		}
	}
	return pkg
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return err == nil
}

// THE HEADLINE CASE: an orphan whose RECORD IS GONE.
//
// pyright, typescript and typescript-language-server are installed in this very jail from a
// since-unconfigured `lsp_servers`, and the sentinel that is supposed to be able to uninstall
// them is one byte long (program-delivery.md §10 step four; re-measured 2026-09-04). The
// sentinel loop cannot remove them because its input is its own record. This act's input is
// the DISK minus the DECLARATIONS, so the missing record changes nothing about what it can
// reach — and the seeded jail below has no sentinel and no receipts at all, which is exactly
// that jail.
func TestRemovalActReachesAnOrphanWithNoRecordAtAll(t *testing.T) {
	home, packRoot := catalogHome(t)
	orphan := seedNpmPackage(t, home, "pyright", 4096, "pyright", "pyright-langserver")
	declared := seedNpmPackage(t, home, "@scope/declared", 128, "declared-npm")

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if _, err := os.Stat(filepath.Join(home, ".yolo-installed-lsps")); err == nil {
		t.Fatal("this case is about a jail with NO sentinel; one was seeded")
	}

	plan := PlanOrphanRemovals(e, InstalledOrphans(e))
	var got *OrphanRemoval
	for i := range plan {
		if plan[i].Orphan.Name == "pyright" {
			got = &plan[i]
		}
		if plan[i].Orphan.Name == "@scope/declared" {
			t.Fatal("a DECLARED package must never enter a removal plan — the candidate " +
				"set is InstalledOrphans, and a declaration is what makes something not one")
		}
	}
	if got == nil {
		t.Fatalf("pyright is installed and nothing declares it, so the act must reach it; "+
			"plan was %+v", plan)
	}
	if got.Bytes < 4096 {
		t.Errorf("Bytes = %d, want at least the 4096-byte file — a directory orphan's size "+
			"is the whole reason a reader can judge the act", got.Bytes)
	}
	binDir := filepath.Join(home, ".npm-global", "bin")
	for _, want := range []string{
		orphan,
		filepath.Join(binDir, "pyright"),
		filepath.Join(binDir, "pyright-langserver"),
	} {
		if !containsPath(got.Paths, want) {
			t.Errorf("the plan does not name %s — a package removed without its global-bin "+
				"symlinks leaves a dangling command at the FRONT of PATH\nplan: %v",
				want, got.Paths)
		}
	}

	ApplyOrphanRemovals(plan)
	for _, gone := range got.Paths {
		if exists(t, gone) {
			t.Errorf("%s survived the act", gone)
		}
	}
	if !exists(t, declared) || !exists(t, filepath.Join(binDir, "declared-npm")) {
		t.Error("the declared package (or its link) was removed — the act must only ever " +
			"touch what the plan named")
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// PLANNING IS READ-ONLY, pinned the way TestReconcileTouchesNothing pins the reconcile: the
// dry run is the DEFAULT on both surfaces, so a plan that quietly unlinked anything would
// make `yolo programs remove` destructive without --apply.
func TestPlanningARemovalTouchesNothing(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpmPackage(t, home, "leftover", 2048, "leftover")
	seedLocalBin(t, home, 512, "orphan-bin")
	seedGoBin(t, home, 64, "orphan-go")

	before := treeSnapshot(t, home)
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if plan := PlanOrphanRemovals(e, InstalledOrphans(e)); len(plan) == 0 {
		t.Fatal("nothing was planned, so this proves nothing")
	}
	if after := treeSnapshot(t, home); after != before {
		t.Errorf("planning changed the home:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// AUTOPRUNE IS OFF BY DEFAULT — OQ-PD4's third clause, and the one that decides whether this
// feature is safe to ship. Driven through CatalogInstalledOrphans, which is what boot.go
// calls, so this is the boot with no option set.
//
// MUTATION: change autopruneEnabled's default arm to `return true` and this goes red.
func TestBootRemovesNothingWithoutTheOption(t *testing.T) {
	home, packRoot := catalogHome(t)
	pkg := seedNpmPackage(t, home, "leftover", 2048, "leftover")
	seedLocalBin(t, home, 512, "orphan-bin")

	var out strings.Builder
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	e.Stderr = &out
	CatalogInstalledOrphans(e)

	if !exists(t, pkg) || !exists(t, filepath.Join(home, ".local", "bin", "orphan-bin")) {
		t.Fatal("a boot with no autoprune option removed an orphan — dropping a pack must " +
			"not auto-delete its program (OQ-PD4)")
	}
	if strings.Contains(out.String(), autoprunePrefix) {
		t.Errorf("the default boot announced an act it must not perform:\n%s", out.String())
	}
	if !strings.Contains(out.String(), catalogPrefix) {
		t.Errorf("the informational catalog stopped reporting:\n%s", out.String())
	}
}

// AND IT REMOVES WHEN THE OPTION IS ON, through the same boot entry point.
//
// MUTATION (the call site): delete `autopruneOrphans(e, orphans)` from
// CatalogInstalledOrphans and this goes red — every other test in this file drives Plan/Apply
// directly and would stay green with the option wired to nothing, which is precisely the
// callee-pinned/call-site-unpinned shape AGENTS.md says this repo has shipped five times.
func TestBootRemovesOrphansWhenTheOptionIsOn(t *testing.T) {
	home, packRoot := catalogHome(t)
	pkg := seedNpmPackage(t, home, "leftover", 2048, "leftover")
	localOrphan := filepath.Join(home, ".local", "bin", "orphan-bin")
	seedLocalBin(t, home, 512, "orphan-bin")
	declared := seedNpmPackage(t, home, "@scope/declared", 128, "declared-npm")

	var out strings.Builder
	e := NewEnv(map[string]string{
		"JAIL_HOME":            home,
		"YOLO_PACK_ROOT":       packRoot,
		orphanAutopruneEnv:     "1",
		"NPM_CONFIG_PREFIX":    filepath.Join(home, ".npm-global"),
		"YOLO_LSP_NPM_INSTALL": "",
	})
	e.Stderr = &out
	CatalogInstalledOrphans(e)

	if exists(t, pkg) {
		t.Error("the npm orphan survived an ON autoprune")
	}
	if exists(t, localOrphan) {
		t.Error("the ~/.local/bin orphan survived an ON autoprune")
	}
	if exists(t, filepath.Join(home, ".npm-global", "bin", "leftover")) {
		t.Error("the orphan's global-bin symlink survived — a dangling link at the front " +
			"of PATH is worse than the package it points at")
	}
	if !exists(t, declared) {
		t.Fatal("autoprune removed a DECLARED package")
	}
	if !strings.Contains(out.String(), autoprunePrefix+"removing leftover") {
		t.Errorf("the act did not name what it removed:\n%s", out.String())
	}
}

// bytesWatcher records, for each line written, whether the watched path still existed at the
// moment of the write. That is what "says what it will remove before removing it" means as a
// property of the system rather than as a claim in a comment.
type bytesWatcher struct {
	watch   string
	present []bool
	lines   []string
}

func (w *bytesWatcher) Write(p []byte) (int, error) {
	_, err := os.Lstat(w.watch)
	w.present = append(w.present, err == nil)
	w.lines = append(w.lines, strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// THE ANNOUNCEMENT PRECEDES THE ACT. A destructive step that prints afterwards is a step a
// user cannot interrupt and cannot audit if the boot dies mid-way.
//
// MUTATION: move the ApplyOrphanRemovals call above the announcement loop in autopruneOrphans
// and this goes red.
func TestAutopruneAnnouncesBeforeItRemoves(t *testing.T) {
	home, packRoot := catalogHome(t)
	pkg := seedNpmPackage(t, home, "leftover", 2048)

	w := &bytesWatcher{watch: pkg}
	e := NewEnv(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, orphanAutopruneEnv: "true",
	})
	e.Stderr = w
	CatalogInstalledOrphans(e)

	if len(w.present) == 0 {
		t.Fatal("nothing was written, so the ordering is untested")
	}
	for i, line := range w.lines {
		if !strings.HasPrefix(line, autoprunePrefix+"removing ") {
			continue
		}
		if !w.present[i] {
			t.Errorf("line %d (%q) claims a removal that had ALREADY happened — the act "+
				"must say what it will remove before removing it", i, line)
		}
	}
	if exists(t, pkg) {
		t.Fatal("the orphan was never removed, so the ordering assertion is vacuous")
	}
}

// A scope directory is not a package (installedNpmPackages' rule), so removing its last
// member leaves a directory nothing will ever report and nothing will ever remove.
func TestRemovingTheLastScopedPackageTakesTheScopeDirectory(t *testing.T) {
	home, packRoot := catalogHome(t)
	pkg := seedNpmPackage(t, home, "@dropped/only-child", 256)
	scope := filepath.Dir(pkg)

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	ApplyOrphanRemovals(PlanOrphanRemovals(e, InstalledOrphans(e)))
	if exists(t, scope) {
		t.Errorf("%s survived as an empty scope — invisible to the catalog, so nothing "+
			"would ever name it again", scope)
	}
}

// ...and it stays when a sibling survives, which is the case that makes the check worth
// having: a scope holding one orphan and one declared package must keep the directory.
func TestAScopeWithASurvivingPackageIsKept(t *testing.T) {
	home, packRoot := catalogHome(t)
	orphan := seedNpmPackage(t, home, "@scope/orphan", 256)
	declared := seedNpmPackage(t, home, "@scope/declared", 256)
	scope := filepath.Dir(orphan)

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	ApplyOrphanRemovals(PlanOrphanRemovals(e, InstalledOrphans(e)))
	if exists(t, orphan) {
		t.Error("the orphan survived")
	}
	if !exists(t, declared) || !exists(t, scope) {
		t.Error("the scope (or its declared package) was removed with its sibling")
	}
}

// The global bin directory holds files nobody else's act may touch. Only a SYMLINK RESOLVING
// INTO the package is that package's.
func TestOnlySymlinksIntoThePackageAreUnlinked(t *testing.T) {
	home, packRoot := catalogHome(t)
	pkg := seedNpmPackage(t, home, "leftover", 128, "leftover")
	binDir := filepath.Join(home, ".npm-global", "bin")
	// A REGULAR FILE that happens to share the package's name, and a symlink pointing at
	// something else entirely. Neither is npm's, and neither is this act's.
	regular := filepath.Join(binDir, "leftover-handwritten")
	if err := os.WriteFile(regular, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(binDir, "points-away")
	if err := os.Symlink("/bin/true", elsewhere); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	plan := PlanOrphanRemovals(e, InstalledOrphans(e))
	ApplyOrphanRemovals(plan)

	if exists(t, pkg) {
		t.Fatal("the package survived, so nothing here is proven")
	}
	if !exists(t, regular) {
		t.Error("a REGULAR FILE in the npm bin dir was deleted — it was not npm's to link " +
			"and is not ours to remove")
	}
	if !exists(t, elsewhere) {
		t.Error("a symlink pointing outside the package was deleted")
	}
}

// A per-removal failure must not abandon the rest, because on the boot path the usual cause
// (an unwritable mount) is a whole class of orphan rather than one entry.
func TestOneFailedRemovalDoesNotStopTheOthers(t *testing.T) {
	home, packRoot := catalogHome(t)
	good := seedNpmPackage(t, home, "removable", 64)

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	plan := PlanOrphanRemovals(e, InstalledOrphans(e))
	// A path that cannot be removed, spliced in ahead of the real one: a non-empty
	// directory whose parent denies write.
	locked := filepath.Join(home, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	plan = append([]OrphanRemoval{{
		Orphan: Orphan{Class: OrphanLocalBin, Name: "child", Display: "~/locked/child"},
		Paths:  []string{filepath.Join(locked, "child")},
	}}, plan...)

	got := ApplyOrphanRemovals(plan)
	if got[0].Err == nil && os.Geteuid() != 0 {
		t.Fatal("the unremovable entry reported no error, so this case is not exercised")
	}
	if exists(t, good) {
		t.Error("a later removal was skipped because an earlier one failed")
	}
}

// pathBytes measures a directory (which catalogSize deliberately does not) and never follows
// a symlink out of it — a link into a package would otherwise count the same bytes twice.
func TestPathBytesSumsDirectoriesAndIgnoresSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b"), make([]byte, 24), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(big, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if got := pathBytes(dir); got != 1024 {
		t.Errorf("pathBytes = %d, want 1024 (1000 + 24, and NOT the 1 MB behind the "+
			"symlink)", got)
	}
	if got := pathBytes(filepath.Join(dir, "link")); got != 0 {
		t.Errorf("pathBytes(symlink) = %d, want 0", got)
	}
}

// The option's vocabulary, including everything that must read as OFF. A knob whose default
// is the destructive one is the failure this whole file is arranged around.
func TestAutopruneOptionVocabulary(t *testing.T) {
	for _, on := range []string{"1", "true", "TRUE", "yes", "on", " 1 "} {
		if !autopruneEnabled(NewEnv(map[string]string{orphanAutopruneEnv: on})) {
			t.Errorf("%q should turn the option on", on)
		}
	}
	for _, off := range []string{"", "0", "false", "no", "off", "maybe"} {
		if autopruneEnabled(NewEnv(map[string]string{orphanAutopruneEnv: off})) {
			t.Errorf("%q must NOT turn the option on — anything this cannot interpret is "+
				"the safe default", off)
		}
	}
	if autopruneEnabled(NewEnv(nil)) {
		t.Error("an ABSENT variable must be off: an older launcher and a backend that " +
			"emits no env both look like this")
	}
}

// Nothing to remove means nothing said. An empty jail that printed an autoprune header every
// launch would train the reader to skim exactly the launch where it removed something.
func TestAutopruneSaysNothingWhenThereIsNothingToRemove(t *testing.T) {
	home, packRoot := catalogHome(t)
	var out strings.Builder
	e := NewEnv(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, orphanAutopruneEnv: "1",
	})
	e.Stderr = &out
	CatalogInstalledOrphans(e)
	if strings.Contains(out.String(), autoprunePrefix) {
		t.Errorf("an empty jail announced an autoprune:\n%s", out.String())
	}
}

var _ io.Writer = (*bytesWatcher)(nil)
