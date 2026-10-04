package entrypoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catalog_test.go covers OQ-PD4's informational half: the boot catalog NAMES what is
// installed and undeclared, and touches nothing.

// catalogHome stages a jail home plus a one-pack tree, and returns (home, packRoot).
//
// The pack declares one npm program and one native program, which is the shape that makes
// the two halves of the catalog separable: an npm program lands under the npm prefix and a
// native one under ~/.local/bin, so a declared-set bug that crossed the two would show up
// as an orphan on one side and a miss on the other.
func catalogHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "toolpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"toolpack","contributes":[` +
		`{"kind":"program","bin":"declared-npm","via":"npm","package":"@scope/declared@1.2.3"},` +
		`{"kind":"program","bin":"declared-native","via":"installer","url":"https://x.invalid/i.sh"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, packRoot
}

// seedNpm materializes package dirs under the global prefix, scoped names included.
func seedNpm(t *testing.T, home string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(home, ".npm-global", "lib", "node_modules", n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// seedLocalBin writes size-byte files into ~/.local/bin.
func seedLocalBin(t *testing.T, home string, size int, names ...string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), make([]byte, size), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// runCatalog returns the whole report — the terminal line AND the boot-log list — because
// what the tests below are about is the FINDING SET: which bytes the catalog calls orphaned
// and which it spares. Which sink each half lands in is one property, pinned once by
// TestBootCatalogSaysHowManyAndLogsWhich; asserting it in every test here would be nine
// copies of one fact and would make a spared-package regression read as a routing change.
func runCatalog(t *testing.T, vars map[string]string) string {
	t.Helper()
	term, logOnly := runCatalogSplit(t, vars)
	return term + logOnly
}

// runCatalogSplit returns the two halves separately: what the launch terminal sees, and
// what lands in boot.log through Env.LogOnly.
func runCatalogSplit(t *testing.T, vars map[string]string) (string, string) {
	t.Helper()
	var term, logOnly strings.Builder
	e := NewEnv(vars)
	e.Stderr = &term
	e.LogOnly = &logOnly
	CatalogInstalledOrphans(e)
	return term.String(), logOnly.String()
}

// TestCatalogNamesNpmOrphansAndSparesEveryDeclaredSource walks the whole declared union in
// one pass, because the union is the finding: each source it forgets turns a package with
// an owner into a reported orphan, and a catalog that cries wolf is one nobody reads.
func TestCatalogNamesNpmOrphansAndSparesEveryDeclaredSource(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home,
		"@scope/declared", // the pack's npm program (NAME half of a pinned spec)
		"pnpm",            // GeneratePackageManagerLaunchers
		"@modelcontextprotocol/server-sequential-thinking", // an enabled MCP preset
		"leftover-agent",             // an orphan
		"@dropped/scoped-orphan",     // an orphan, two levels down
		".bin", ".package-lock.json", // npm's own bookkeeping, never packages
	)

	got := runCatalog(t, map[string]string{
		"JAIL_HOME":        home,
		"YOLO_PACK_ROOT":   packRoot,
		"YOLO_MCP_PRESETS": `["sequential-thinking"]`,
	})

	for _, want := range []string{"leftover-agent", "@dropped/scoped-orphan"} {
		if !strings.Contains(got, want) {
			t.Errorf("orphan %q was not cataloged:\n%s", want, got)
		}
	}
	for _, spared := range []string{
		"@scope/declared", "pnpm", "@modelcontextprotocol/server-sequential-thinking",
		".bin", ".package-lock.json",
	} {
		if strings.Contains(got, spared) {
			t.Errorf("%q has an owner and must not be cataloged as an orphan:\n%s", spared, got)
		}
	}
	// A scope is a directory, not a package: reporting "@dropped" alone would name
	// something no declaration could ever match.
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.HasSuffix(line, "@dropped") {
			t.Errorf("a scope was cataloged as if it were a package: %s", line)
		}
	}
}

// THE MIGRATION PATH for the deleted LSP recipe table (docs/reference/mcp-configuration.md#oq-lsp1):
// what its install loop put on disk before the deletion has no owner now, so the catalog must
// NAME it — that is what lets `yolo programs remove` collect it, since nothing uninstalls it
// on its own any more. The two retired sources are both still present here, exactly as an
// upgraded jail has them: a leftover ~/.yolo-installed-lsps sentinel in the home, and an older
// host launcher's YOLO_LSP_*_INSTALL in the environment. Neither may make a package owned.
func TestCatalogNamesWhatTheDeletedLSPRecipeInstalled(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home, "pyright", "typescript-language-server")
	seedGoBin(t, home, 64, "gopls")
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:pyright\nnpm:typescript-language-server\ngo:golang.org/x/tools/gopls@latest\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	got := runCatalog(t, map[string]string{
		"JAIL_HOME":            home,
		"YOLO_PACK_ROOT":       packRoot,
		"YOLO_LSP_NPM_INSTALL": "pyright\n",
		"YOLO_LSP_GO_INSTALL":  "golang.org/x/tools/gopls@latest\n",
	})
	for _, want := range []string{"pyright", "typescript-language-server", "~/go/bin/gopls"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was installed by the deleted LSP recipe and nothing declares it now, "+
				"so the catalog must name it for `yolo programs remove` to collect:\n%s", want, got)
		}
	}
	if strings.Contains(got, "LSP recipe") {
		t.Errorf("the catalog still names an LSP recipe as a declaring source:\n%s", got)
	}
}

// TestCatalogSkipsNpmStagingDirsAtBothLevels: npm stages an install at `.<name>-<hash>`
// beside its destination and renames it into place, so an interrupted install leaves
// `node_modules/.tool-a1b2c3` — or `node_modules/@scope/.tool-d4e5f6` two levels down. Both
// are npm's bookkeeping, not packages: no declaration can ever match one, so the catalog
// reported them as orphans forever, starting on the boot after any launch someone ctrl-C'd.
// The two-name denylist this replaced only knew the entries a SUCCESSFUL npm leaves.
func TestCatalogSkipsNpmStagingDirsAtBothLevels(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home,
		".staged-a1b2c3",        // an interrupted top-level install
		"@scope/.staged-d4e5f6", // ...and an interrupted scoped one
		"leftover-agent",        // a real orphan, so silence here is not the wrong pass
	)

	term, listed := runCatalogSplit(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	got := term + listed

	if !strings.Contains(got, "leftover-agent") {
		t.Errorf("the real orphan was not cataloged:\n%s", got)
	}
	for _, staging := range []string{".staged-a1b2c3", ".staged-d4e5f6"} {
		if strings.Contains(got, staging) {
			t.Errorf("%q is npm's interrupted-install staging dir, not a package — no "+
				"declaration can ever match it:\n%s", staging, got)
		}
	}
	// The LIST is where one-line-per-orphan lives now (the launch stream moved it to boot.log), so
	// this is the half that says a staging dir produced no finding — and the count on the terminal
	// is the other half of the same claim.
	if lines := strings.Split(strings.TrimSpace(listed), "\n"); len(lines) != 1 {
		t.Errorf("want exactly the one real orphan, got %d lines:\n%s", len(lines), listed)
	}
	if !strings.Contains(term, "1 installed program is") {
		t.Errorf("the count must agree with the list: %s", term)
	}
}

// TestCatalogSizeScalesItsUnit: a fixed MB rendered every small orphan as "(0.0 MB)", and
// most of this list is small — a wrapper script, a shim, a stub. A reader scanning for the
// 1 GB one (§5.3) saw a column of identical zeroes, which carries no more information than
// no size at all while still reading as a measurement.
func TestCatalogSizeScalesItsUnit(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		size int64
		want string
	}{
		{0, " (0 B)"},
		{84, " (84 B)"},
		{1023, " (1023 B)"},
		{1024, " (1.0 KB)"},
		{1536, " (1.5 KB)"},
		{3 * 1024 * 1024, " (3.0 MB)"},
		{2 * 1024 * 1024 * 1024, " (2.0 GB)"},
	} {
		path := filepath.Join(dir, fmt.Sprintf("f%d", tc.size))
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		// Truncate, not a real write: the GB case is a sparse file, so this measures the
		// rendering without spending a gigabyte of the runner's disk on it.
		if err := f.Truncate(tc.size); err != nil {
			f.Close()
			t.Skipf("cannot size a %d-byte file here: %v", tc.size, err)
		}
		f.Close()
		if got := catalogSize(hostOrphanFS{}, path); got != tc.want {
			t.Errorf("catalogSize(%d bytes) = %q, want %q", tc.size, got, tc.want)
		}
	}

	// Anything that is not a regular file states no size at all — a directory's st_size is
	// an implementation detail of the filesystem, not a thing to report to a user.
	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := catalogSize(hostOrphanFS{}, sub); got != "" {
		t.Errorf("catalogSize(dir) = %q, want no size", got)
	}
}

// TestCatalogNamesLocalBinOrphansWithTheirSize: a name alone does not tell anyone which
// orphan is worth an explicit removal act — §5.3 measured one vendor's leftovers at just
// over 1 GB per workspace.
func TestCatalogNamesLocalBinOrphansWithTheirSize(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedLocalBin(t, home, 3*1024*1024, "huge-orphan")
	seedLocalBin(t, home, 16, "declared-native", "yolo-log", "chrome-devtools-mcp-wrapper",
		"yolo-cglimit")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin", "mcp-wrappers"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := runCatalog(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})

	if !strings.Contains(got, "~/.local/bin/huge-orphan") {
		t.Errorf("the orphan was not cataloged:\n%s", got)
	}
	if !strings.Contains(got, "(3.0 MB)") {
		t.Errorf("the orphan's size must be stated, in MB:\n%s", got)
	}
	// And a small one is stated in a unit that says something: "(0.0 MB)" is what the
	// whole tail of this list used to render as.
	seedLocalBin(t, home, 84, "tiny-orphan")
	got = runCatalog(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if !lineWithBoth(got, "~/.local/bin/tiny-orphan", "(84 B)") {
		t.Errorf("a small orphan must not render as a rounded zero:\n%s", got)
	}
	for _, spared := range []string{
		"declared-native",             // the pack's native program
		"yolo-log",                    // InstallYoloLog
		"chrome-devtools-mcp-wrapper", // GenerateMCPWrappers
		"mcp-wrappers",                // its sibling directory
		"yolo-cglimit",                // staleGeneratedClients — this boot is already unlinking it
	} {
		if strings.Contains(got, spared) {
			t.Errorf("%q has an owner and must not be cataloged:\n%s", spared, got)
		}
	}
}

// seedGoBin writes size-byte files into $GOBIN ($GOPATH/bin).
func seedGoBin(t *testing.T, home string, size int, names ...string) {
	t.Helper()
	dir := filepath.Join(home, "go", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), make([]byte, size), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCatalogNamesEveryGoBinEntryWithItsSize is the third orphan CLASS, which was
// invisible: the catalog walked node_modules and ~/.local/bin and never $GOBIN, so a go tool
// the bootstrap's LSP arm installed under a declaration that has since gone had no line
// anywhere. MEASURED in this jail on 2026-09-02: ~/go/bin held gopls AND mcp-language-server,
// the latter's only consumer deleted with the gemini agent, and the boot catalog named five
// orphans — none of them either one.
//
// NOTHING IS SPARED here any more. The LSP recipe's go arm was the only declaration that
// could own a $GOBIN file, and it is deleted (docs/reference/mcp-configuration.md#oq-lsp1),
// so every entry is named — the recipe's own gopls included, which is how an upgraded jail's
// leftover is found at all.
//
// A missing finder is worse than an unreported directory once an explicit removal act reads
// this list (OQ-PD4's other half): the act's candidates would be whichever classes someone
// happened to walk, which is a removal list that is silently wrong rather than short.
func TestCatalogNamesEveryGoBinEntryWithItsSize(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedGoBin(t, home, 2*1024*1024, "gopls", "mcp-language-server")

	got := runCatalog(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})

	for _, want := range []string{"~/go/bin/gopls", "~/go/bin/mcp-language-server"} {
		if !lineWithBoth(got, want, "(2.0 MB)") {
			t.Errorf("the $GOBIN orphan %s must be named with its size:\n%s", want, got)
		}
	}
}

// TestCatalogGoBinFinderIsReachedFromTheProductionPath is the CALL-SITE half for Part A,
// one level down from the boot: CatalogInstalledOrphans is what boot.go calls, so a
// catalogGoBinOrphans nobody calls from THERE leaves the whole finder dead with its own unit
// test green. Driven through the exported entry point with a $GOBIN orphan present and
// nothing else installed, so the only line it can produce is that finder's.
func TestCatalogGoBinFinderIsReachedFromTheProductionPath(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedGoBin(t, home, 32, "unowned-go-tool")

	got := runCatalog(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if !strings.Contains(got, "~/go/bin/unowned-go-tool") {
		t.Fatalf("CatalogInstalledOrphans must reach the $GOBIN finder — without this the "+
			"finder's own tests pass against a catalog that never walks it:\n%s", got)
	}
}

// TestCatalogRendersAGoBinOutsideTheHomeVerbatim: GOPATH is an ordinary environment
// variable, so $GOPATH/bin need not sit under the jail home — and a hardcoded "~/go/bin/"
// prefix would print a path that does not exist, in a report whose only value is that the
// reader can go look at the file.
func TestCatalogRendersAGoBinOutsideTheHomeVerbatim(t *testing.T) {
	home, packRoot := catalogHome(t)
	gopath := t.TempDir()
	gobin := filepath.Join(gopath, "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gobin, "elsewhere-tool"), make([]byte, 8), 0o755); err != nil {
		t.Fatal(err)
	}

	got := runCatalog(t, map[string]string{
		"JAIL_HOME":      home,
		"GOPATH":         gopath,
		"YOLO_PACK_ROOT": packRoot,
	})
	if !strings.Contains(got, filepath.Join(gobin, "elsewhere-tool")) {
		t.Errorf("a $GOBIN outside the home must be named by its real path:\n%s", got)
	}
	if strings.Contains(got, "~/go/bin") {
		t.Errorf("nothing may assume $GOBIN is ~/go/bin:\n%s", got)
	}
}

// lineWithBoth reports whether ONE line of out carries both substrings — the catalog states
// a finding and its size on the same line, and asserting on the whole blob would pass with
// the size attached to a different orphan.
func lineWithBoth(out, a, b string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, a) && strings.Contains(line, b) {
			return true
		}
	}
	return false
}

// TestCatalogTouchesNothing is the ruling, and the only property that separates this from
// the removal step nobody has agreed to yet: "dropping a pack does not auto-delete its
// program" (OQ-PD4). A catalog that quietly pruned would be indistinguishable from a
// working one until the day it removed something a user wanted.
func TestCatalogTouchesNothing(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home, "leftover-agent")
	seedLocalBin(t, home, 8, "huge-orphan")
	seedGoBin(t, home, 8, "orphan-go-tool")

	before := treeSnapshot(t, home)
	runCatalog(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if after := treeSnapshot(t, home); after != before {
		t.Errorf("the catalog changed the home tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// treeSnapshot renders every path under root with its size, for an exact before/after.
func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b.WriteString(rel + " " + fi.Mode().String())
		if !fi.IsDir() {
			fmt.Fprintf(&b, " %d", fi.Size())
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestCatalogIsSilentWithoutAStagedPackTree: with no YOLO_PACK_ROOT the declared set is
// empty for a reason that has nothing to do with what is installed — an older host
// launcher, a backend that stages nothing — and comparing against it would report every
// installed package as an orphan. That is not a stricter catalog, it is a broken one.
func TestCatalogIsSilentWithoutAStagedPackTree(t *testing.T) {
	home := t.TempDir()
	seedNpm(t, home, "leftover-agent")
	seedLocalBin(t, home, 8, "huge-orphan")
	seedGoBin(t, home, 8, "orphan-go-tool")

	if got := runCatalog(t, map[string]string{"JAIL_HOME": home}); got != "" {
		t.Errorf("no pack root means no declared set and therefore no catalog, got:\n%s", got)
	}
}

// TestCatalogLinesReadAsACatalog: these land in the boot log beside `requires` warnings and
// pack-skew notices, so a reader has to be able to tell at a glance that they are one
// report about installed-and-kept content rather than a boot problem.
func TestCatalogLinesReadAsACatalog(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home, "leftover-agent")
	seedLocalBin(t, home, 8, "huge-orphan")
	seedGoBin(t, home, 8, "orphan-go-tool")

	term, logOnly := runCatalogSplit(t,
		map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	lines := strings.Split(strings.TrimSpace(logOnly), "\n")
	if len(lines) != 3 {
		t.Fatalf("want one logged line per orphan, got %d:\n%s", len(lines), logOnly)
	}
	for _, line := range append(lines, strings.TrimSpace(term)) {
		if !strings.HasPrefix(line, catalogPrefix) {
			t.Errorf("every line must be prefixed so the report reads as one thing: %q", line)
		}
		if !strings.Contains(line, "declared by no") && !strings.Contains(line, "not declared") {
			t.Errorf("every line must say what the finding IS: %q", line)
		}
	}
}

// TestBootCatalogSaysHowManyAndLogsWhich is docs/reference/report-tiers.md's compression,
// and the ONE test that pins which sink each half goes to.
//
// The eight lines this jail printed at every launch are notch facts with a state dependency:
// true until the user acts, repeated until then. The launch stream compresses them to one — the
// count, and where the names are — with the list going to boot.log through the split
// Env.LogOnly already exists for.
//
// BOTH HALVES ARE ASSERTED, because each is a way to get this wrong that the other cannot
// catch. A terminal that names an orphan is the repetition coming back; a boot log that does
// not name one is the compression having DELETED the set rather than the lines, which the
// launch stream forbids in as many words.
//
// MUTATION: change the loop body back to e.warn and this goes red on the terminal half;
// change it to e.warn AND drop the summary and it goes red on the log half.
func TestBootCatalogSaysHowManyAndLogsWhich(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home, "leftover-agent")
	seedLocalBin(t, home, 8, "huge-orphan")
	seedGoBin(t, home, 8, "orphan-go-tool")

	term, logOnly := runCatalogSplit(t,
		map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})

	if got := strings.Count(strings.TrimSpace(term), "\n"); got != 0 {
		t.Errorf("the launch terminal gets ONE line, got %d:\n%s", got+1, term)
	}
	if !strings.Contains(term, "3 installed programs") {
		t.Errorf("the one line must state the count — it is the fact eight invariant lines "+
			"could not deliver:\n%s", term)
	}
	if !strings.Contains(term, "boot.log") {
		t.Errorf("the one line must say where the names went, or the compression reads as a "+
			"deletion:\n%s", term)
	}
	for _, name := range []string{"leftover-agent", "huge-orphan", "orphan-go-tool"} {
		if strings.Contains(term, name) {
			t.Errorf("%q is named on the launch terminal — the list is the log's:\n%s", name, term)
		}
		if !strings.Contains(logOnly, name) {
			t.Errorf("%q is in neither the line nor the log: the set was compressed, not the "+
				"lines:\n%s", name, logOnly)
		}
	}
}

// THE macos-user BOOTSTRAP CATALOGS, INTO ITS BOOT LOG, driven through RunDarwinBootstrap
// with the Env built the way the production caller builds it (internal/cli/internal.go:
// DarwinEnvFrom, then Stderr and nothing else). The one terminal line points at boot.log for
// the names, and on this backend boot.log now exists (attachDarwinBootLog): the line reaches
// the terminal, and the orphan's name reaches the log through e.note.
//
// MUTATION: restore the catalog step's notDarwin, or delete the bootstrap's boot-log attach,
// and this goes red.
func TestTheDarwinBootstrapCatalogsOrphansIntoItsBootLog(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home, "leftover-agent")
	ws := t.TempDir()
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, "YOLO_DARWIN_WORKSPACE": ws,
	}, home)
	var term strings.Builder
	e.Stderr = &term

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	if !strings.Contains(term.String(), catalogSummary(1)) {
		t.Errorf("the macos-user bootstrap did not catalog the orphan on the terminal:\n%s",
			term.String())
	}
	log, err := os.ReadFile(BootLogPath(ws))
	if err != nil {
		t.Fatalf("the macos-user bootstrap kept no boot log for the names: %v\n%s", err, term.String())
	}
	line := catalogPrefix + "npm package installed but not declared by any selected pack or preset: leftover-agent"
	if !strings.Contains(string(log), line) {
		t.Errorf("boot.log does not name the orphan the terminal line counted:\n%s", log)
	}
	if strings.Contains(term.String(), line) {
		t.Errorf("the per-orphan line reached the terminal; it belongs in boot.log alone:\n%s", term.String())
	}
}

// AND programs.autoprune REMOVES THROUGH THE HOME LAYOUT'S LINK. On macos-user ~/.npm-global is
// a symlink into the workspace sidecar (<workspace>/.yolo/home/npm-global), laid by the
// bootstrap's own first step, so the orphan's bytes and its bin link live in the sidecar while
// the Env names them through the home. The relayed variable is what turns the act on
// (macosuser.BuildRunPlanWithDaemons sets it from the user's config). The act unlinks beneath
// roots on the sidecar (catalogConfinedOrphans), a scoped package's emptied scope included.
func TestTheDarwinBootstrapAutoprunesThroughTheHomeLayoutLink(t *testing.T) {
	home, packRoot := catalogHome(t)
	ws := t.TempDir()
	sidecar := filepath.Join(ws, ".yolo", "home")
	pkg := filepath.Join(sidecar, "npm-global", "lib", "node_modules", "leftover-agent")
	if err := os.MkdirAll(filepath.Join(pkg, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	scoped := filepath.Join(sidecar, "npm-global", "lib", "node_modules", "@gone", "agent")
	if err := os.MkdirAll(scoped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "bin", "cli.js"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(sidecar, "npm-global", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/node_modules/leftover-agent/bin/cli.js", filepath.Join(bin, "leftover")); err != nil {
		t.Fatal(err)
	}
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, "YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv: sidecar, OrphanAutopruneEnv: "1",
	}, home)
	var term strings.Builder
	e.Stderr = &term

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	if fi, err := os.Lstat(filepath.Join(home, ".npm-global")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture is not the layout this test is about: ~/.npm-global is not a link (err=%v)\n%s",
			err, term.String())
	}
	if !strings.Contains(term.String(), autoprunePrefix+"removing leftover-agent") {
		t.Errorf("autoprune did not announce the removal:\n%s", term.String())
	}
	if _, err := os.Lstat(pkg); !os.IsNotExist(err) {
		t.Errorf("the orphan's package survived autoprune in the sidecar (err=%v)\n%s", err, term.String())
	}
	if _, err := os.Lstat(filepath.Join(bin, "leftover")); !os.IsNotExist(err) {
		t.Errorf("the orphan's bin link survived autoprune (err=%v)", err)
	}
	if _, err := os.Lstat(filepath.Dir(scoped)); !os.IsNotExist(err) {
		t.Errorf("the scoped orphan, or the scope it emptied, survived autoprune (err=%v)\n%s", err, term.String())
	}
}

// TestBootCatalogIsSilentOnBothSinksWithNoOrphans: a clean home says nothing at all, which
// is what keeps the one line above worth reading. The old shape got this for free (an empty
// loop prints nothing); a summary line does not, and "0 installed programs are declared by
// no pack" on every healthy launch is exactly the noise the launch stream is removing.
func TestBootCatalogIsSilentOnBothSinksWithNoOrphans(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedNpm(t, home, "@scope/declared") // declared by the fixture pack: not an orphan
	seedLocalBin(t, home, 8, "declared-native")

	term, logOnly := runCatalogSplit(t,
		map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if term != "" || logOnly != "" {
		t.Errorf("a home with nothing undeclared must produce no report at all\nterminal:\n%s\nlog:\n%s",
			term, logOnly)
	}
}

// TestBootCatalogsOrphansBesideTheOtherInformationalSteps pins the catalog's place in the boot
// step table (bootsteps.go), on the container boot:
//
//   - it is a `run` step, NOT a generator — an installed-but-undeclared package is not a
//     broken generator, and running it through genStep would make a jail with an orphan
//     refuse to start;
//   - it runs AFTER the other informational step that reads pack declarations
//     (assert_required_bins): both read the same declarations against the same disk, and
//     the missing-bin finding comes first. The table runs above the exec that hands control
//     away (TestBothBootsRunTheTable), so it reads the PREVIOUS launch's state, the only state
//     in which "undeclared" means anything.
//
// The macos-user bootstrap runs it in the same slot (TestTheDarwinBootstrapCatalogsOrphansIntoItsBootLog).
// The step's body is located with callIndex, which skips a commented-out mention.
func TestBootCatalogsOrphansBesideTheOtherInformationalSteps(t *testing.T) {
	s := mustBootStep(t, "catalog_installed_orphans")
	if s.gen != nil || s.run == nil {
		t.Error("the catalog must be a run step, not a generator: it generates nothing, and a " +
			"fatal there would mean a jail with one orphaned package refuses to START")
	}
	for _, target := range []bootTarget{bootContainer, bootDarwin} {
		assertStepBefore(t, target, "assert_required_bins", "catalog_installed_orphans",
			"the two informational steps read the same declarations, and the missing-bin finding comes first")
	}
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "entrypoint", "bootsteps.go"))
	if err != nil {
		t.Fatal(err)
	}
	if callIndex(string(src), "CatalogInstalledOrphans(b.e)") < 0 {
		t.Fatal("the boot step table never calls CatalogInstalledOrphans — the catalog is " +
			"unreachable, and every test in this file passes anyway")
	}
	if callIndex(string(src), "catalogConfinedOrphans(b.e)") < 0 {
		t.Fatal("the boot step table never calls catalogConfinedOrphans — the macos-user " +
			"bootstrap catalogs, and autoprunes, through links the agent can leave")
	}
}

// callIndex is strings.Index restricted to occurrences that are not commented out: it skips
// any hit whose line already contains a `//` before it. A source-reading test is only as
// good as its ability to tell a CALL from a MENTION — boot.go comments name the functions it
// calls, so the naive search stays green against a call site someone deleted and explained.
func callIndex(src, needle string) int {
	for off := 0; off < len(src); {
		i := strings.Index(src[off:], needle)
		if i < 0 {
			return -1
		}
		i += off
		lineStart := strings.LastIndexByte(src[:i], '\n') + 1
		if !strings.Contains(src[lineStart:i], "//") {
			return i
		}
		off = i + len(needle)
	}
	return -1
}

// darwinSidecarFixture is a macos-user workspace for the catalog: a workspace whose sidecar
// (<ws>/.yolo/home) the bootstrap lays the home's links into, and a SIBLING workspace beside
// it, the directory the session's sandbox profile denies the agent. Returns the workspace, its
// sidecar and the sibling.
func darwinSidecarFixture(t *testing.T) (ws, sidecar, sibling string) {
	t.Helper()
	root := resolvedDir(t)
	ws, sibling = filepath.Join(root, "ws"), filepath.Join(root, "otherws")
	sidecar = filepath.Join(ws, ".yolo", "home")
	for _, d := range []string{sidecar, filepath.Join(sibling, "src")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return ws, sidecar, sibling
}

// THE macos-user BOOTSTRAP NEITHER LISTS NOR DELETES THROUGH A LINK THE AGENT LEFT IN THE
// SIDECAR. The bootstrap runs as the sandbox account OUTSIDE Seatbelt, and the sidecar is in the
// workspace the agent writes, so a link at <sidecar>/local/bin to a sibling workspace once made
// the catalog list that workspace's files as orphans and autoprune delete them — files the
// session's profile denies the agent. The finders now read beneath a root opened on the sidecar
// itself, and a link on the way refuses the directory, saying which link and what to do.
//
// MUTATION: make the darwin catalog step call CatalogInstalledOrphans unconfined, and the
// sibling's file is deleted.
func TestTheDarwinBootstrapRemovesNothingThroughALinkInTheSidecar(t *testing.T) {
	home, packRoot := catalogHome(t)
	ws, sidecar, sibling := darwinSidecarFixture(t)
	outside := filepath.Join(sibling, "src", "main.go")
	if err := os.WriteFile(outside, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sidecar, "local"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sidecar, "local", "bin")
	if err := os.Symlink(filepath.Join(sibling, "src"), link); err != nil {
		t.Fatal(err)
	}
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, "YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv: sidecar, OrphanAutopruneEnv: "1",
	}, home)
	var term strings.Builder
	e.Stderr = &term

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	if fi, err := os.Lstat(filepath.Join(home, ".local")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture is not the layout this test is about: ~/.local is not a link (err=%v)\n%s",
			err, term.String())
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "package main\n" {
		t.Fatalf("the bootstrap deleted or changed a file in another workspace through %s (err=%v)\n%s",
			link, err, term.String())
	}
	log, _ := os.ReadFile(BootLogPath(ws))
	if strings.Contains(term.String()+string(log), "main.go") {
		t.Errorf("the catalog listed a file in another workspace:\nterminal:\n%s\nlog:\n%s", term.String(), log)
	}
	refusal := lineWith(term.String(), link)
	for _, want := range []string{catalogPrefix, "~/.local/bin", "symbolic link", "sudo rm", "yolo programs"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal does not say %q — it must name the directory, the link and the next "+
				"step:\n%s", want, term.String())
		}
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the refusal removed the link itself; nothing is removed for the user (err=%v)", err)
	}
}

// The npm finder too, through a link one level deeper (<sidecar>/npm-global/lib): the package
// directory in the sibling workspace is neither listed nor removed, and while a link sits on the
// way nothing at all is removed this boot, the other finders' orphans included.
func TestTheDarwinBootstrapRemovesNoNpmPackageThroughALinkInTheSidecar(t *testing.T) {
	home, packRoot := catalogHome(t)
	ws, sidecar, sibling := darwinSidecarFixture(t)
	victim := filepath.Join(sibling, "src", "node_modules", "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sidecar, "npm-global"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "src"), filepath.Join(sidecar, "npm-global", "lib")); err != nil {
		t.Fatal(err)
	}
	ownOrphan := filepath.Join(sidecar, "local", "bin", "leftover")
	if err := os.MkdirAll(filepath.Dir(ownOrphan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownOrphan, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, "YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv: sidecar, OrphanAutopruneEnv: "1",
	}, home)
	var term strings.Builder
	e.Stderr = &term

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("autoprune removed a package directory in another workspace (err=%v)\n%s", err, term.String())
	}
	if strings.Contains(term.String(), "victim") {
		t.Errorf("the catalog listed a package in another workspace:\n%s", term.String())
	}
	if _, err := os.Stat(ownOrphan); err != nil {
		t.Errorf("autoprune removed an orphan while a link sat on the way of another finder (err=%v)\n%s",
			err, term.String())
	}
	if log, _ := os.ReadFile(BootLogPath(ws)); !strings.Contains(string(log), "~/.local/bin/leftover") {
		t.Errorf("the finders with no link on the way stopped cataloging:\n%s", log)
	}
}

// lineWith returns the first line of s containing sub, or "".
func lineWith(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// THE CATALOG READS AND THE ACT UNLINKS BENEATH THE ROOTS IT OPENED, not along a path, so a
// link swapped into the sidecar after the open cannot redirect either. Here the directory the
// root was opened on is moved aside and a link to the sibling workspace takes its name: the
// listing still names the directory that was opened, the unlink lands there, and the sibling's
// file of the same name survives. And a path below no finder directory is refused outright.
//
// MUTATION: make confinedOrphanFS.ReadDir call os.ReadDir(name), and the listing names the
// sibling's file; make its RemoveAll call os.RemoveAll(name), and that file is deleted.
func TestTheConfinedOrphanActUnlinksBeneathItsRoots(t *testing.T) {
	ws, sidecar, sibling := darwinSidecarFixture(t)
	home := resolvedDir(t)
	bin := filepath.Join(sidecar, "local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "leftover"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "src", "leftover"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME": home, "YOLO_DARWIN_WORKSPACE": ws, DarwinHomeSidecarEnv: sidecar,
	}, home)
	if err := InstallDarwinHomeLayout(e, nil); err != nil {
		t.Fatal(err)
	}
	fsys := openDarwinOrphanFS(e)
	defer fsys.close()
	if lines := fsys.refusals(e); len(lines) != 0 {
		t.Fatalf("a clean sidecar was refused: %v", lines)
	}

	if err := os.Rename(bin, bin+".read"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "src"), bin); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := fsys.ReadDir(e.LocalBin())
	if err != nil {
		t.Fatalf("the confined listing failed: %v", err)
	}
	var names []string
	for _, ent := range entries {
		names = append(names, ent.Name())
	}
	if strings.Join(names, ",") != "leftover" {
		t.Errorf("the listing followed a link swapped in after the open: %v", names)
	}
	if err := fsys.RemoveAll(filepath.Join(e.LocalBin(), "leftover")); err != nil {
		t.Fatalf("the confined unlink failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(sibling, "src", "leftover")); err != nil || string(got) != "keep" {
		t.Errorf("the unlink followed a link swapped in after the read (err=%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(bin+".read", "leftover")); !os.IsNotExist(err) {
		t.Errorf("the unlink did not land in the directory the catalog read (err=%v)", err)
	}
	outside := filepath.Join(sibling, "src", "leftover")
	if err := fsys.RemoveAll(outside); err == nil {
		t.Errorf("the confined act removed %s, below no directory the catalog reads", outside)
	}
}
