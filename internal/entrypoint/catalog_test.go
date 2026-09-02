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

func runCatalog(t *testing.T, vars map[string]string) string {
	t.Helper()
	var out strings.Builder
	e := NewEnv(vars)
	e.Stderr = &out
	CatalogInstalledOrphans(e)
	return out.String()
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
		"pyright",                    // this launch's YOLO_LSP_NPM_INSTALL
		"bash-language-server",       // the PREVIOUS boot's sentinel
		"leftover-agent",             // an orphan
		"@dropped/scoped-orphan",     // an orphan, two levels down
		".bin", ".package-lock.json", // npm's own bookkeeping, never packages
	)
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:bash-language-server\ngo:github.com/x/y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runCatalog(t, map[string]string{
		"JAIL_HOME":            home,
		"YOLO_PACK_ROOT":       packRoot,
		"YOLO_MCP_PRESETS":     `["sequential-thinking"]`,
		"YOLO_LSP_NPM_INSTALL": "pyright\n",
	})

	for _, want := range []string{"leftover-agent", "@dropped/scoped-orphan"} {
		if !strings.Contains(got, want) {
			t.Errorf("orphan %q was not cataloged:\n%s", want, got)
		}
	}
	for _, spared := range []string{
		"@scope/declared", "pnpm", "@modelcontextprotocol/server-sequential-thinking",
		"pyright", "bash-language-server", ".bin", ".package-lock.json",
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

	got := runCatalog(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})

	if !strings.Contains(got, "leftover-agent") {
		t.Errorf("the real orphan was not cataloged:\n%s", got)
	}
	for _, staging := range []string{".staged-a1b2c3", ".staged-d4e5f6"} {
		if strings.Contains(got, staging) {
			t.Errorf("%q is npm's interrupted-install staging dir, not a package — no "+
				"declaration can ever match it:\n%s", staging, got)
		}
	}
	if lines := strings.Split(strings.TrimSpace(got), "\n"); len(lines) != 1 {
		t.Errorf("want exactly the one real orphan, got %d lines:\n%s", len(lines), got)
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
		if got := catalogSize(path); got != tc.want {
			t.Errorf("catalogSize(%d bytes) = %q, want %q", tc.size, got, tc.want)
		}
	}

	// Anything that is not a regular file states no size at all — a directory's st_size is
	// an implementation detail of the filesystem, not a thing to report to a user.
	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := catalogSize(sub); got != "" {
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

// TestCatalogNamesGoBinOrphansAndSparesTheDeclaredRecipe is the third orphan CLASS, which
// was invisible: the catalog walked node_modules and ~/.local/bin and never $GOBIN, so a go
// tool the bootstrap's LSP arm installed under a declaration that has since gone had no line
// anywhere. MEASURED in this jail on 2026-09-02: ~/go/bin held gopls AND mcp-language-server,
// the latter's only consumer deleted with the gemini agent (internal/cli/run/lsp.go:50-55),
// and the boot catalog named five orphans — none of them either one.
//
// A missing finder is worse than an unreported directory once an explicit removal act reads
// this list (OQ-PD4's other half): the act's candidates would be whichever classes someone
// happened to walk, which is a removal list that is silently wrong rather than short.
func TestCatalogNamesGoBinOrphansAndSparesTheDeclaredRecipe(t *testing.T) {
	home, packRoot := catalogHome(t)
	seedGoBin(t, home, 2*1024*1024,
		"gopls",               // this launch's YOLO_LSP_GO_INSTALL (the `go` recipe)
		"tool",                // the PREVIOUS boot's sentinel
		"mcp-language-server", // the live orphan: nothing declares it any more
	)
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("go:github.com/example/tool@v1.4.2\nnpm:pyright\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runCatalog(t, map[string]string{
		"JAIL_HOME":           home,
		"YOLO_PACK_ROOT":      packRoot,
		"YOLO_LSP_GO_INSTALL": "golang.org/x/tools/gopls@latest\n",
	})

	if !lineWithBoth(got, "~/go/bin/mcp-language-server", "(2.0 MB)") {
		t.Errorf("the $GOBIN orphan must be named with its size:\n%s", got)
	}
	for _, spared := range []string{"gopls", "~/go/bin/tool"} {
		if strings.Contains(got, spared) {
			t.Errorf("%q has an owner and must not be cataloged as an orphan:\n%s", spared, got)
		}
	}
	// The declared set is indexed by the BIN NAME, not the module path: comparing
	// `golang.org/x/tools/gopls@latest` against the filename `gopls` matches nothing, so a
	// path-keyed set would report every installed go tool — the recipe's own included.
	if strings.Contains(got, "golang.org/x/tools") {
		t.Errorf("a declaration's module path is not a $GOBIN filename:\n%s", got)
	}
}

// TestGoModuleBinName is the reduction shell.go's LSP go arm makes
// (`base=${pkg%@*}; bin=${base##*/}`), which is the only thing that lets a declaration and a
// file in $GOBIN be compared at all.
func TestGoModuleBinName(t *testing.T) {
	for _, tc := range []struct{ pkg, want string }{
		{"golang.org/x/tools/gopls@latest", "gopls"},
		{"github.com/isaacphi/mcp-language-server@v0.1.0", "mcp-language-server"},
		{"github.com/example/tool", "tool"},
		{"tool@v1.2.3", "tool"},
		{"  golang.org/x/tools/gopls@latest  ", "gopls"},
		{"", ""},
	} {
		if got := goModuleBinName(tc.pkg); got != tc.want {
			t.Errorf("goModuleBinName(%q) = %q, want %q", tc.pkg, got, tc.want)
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
	sentinel := filepath.Join(home, ".yolo-installed-lsps")
	if err := os.WriteFile(sentinel, []byte("npm:pyright\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	got := runCatalog(t, map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("want one line per orphan, got %d:\n%s", len(lines), got)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, catalogPrefix) {
			t.Errorf("every line must be prefixed so the report reads as one thing: %q", line)
		}
		if !strings.Contains(line, "not declared") {
			t.Errorf("every line must say what the finding IS: %q", line)
		}
	}
}

// TestBootCatalogsOrphansBesideTheOtherInformationalSteps is the CALL SITE.
//
// Main cannot be called from a test — it ends in execBash, which replaces the process — so
// nothing here can observe whether the boot path uses any of this. Every test above would
// pass in full against a boot.go that never calls it, which is the exact shape this repo
// has shipped five times: the callee pinned, the call site unpinned, the feature switchable
// off with the unit gate green.
//
// Pinned by reading the source, the same way reachability_test.go pins the witness ordering
// and bootlog_test.go pins the log wiring. Two properties, both of which a one-line move
// would break invisibly:
//
//   - it is called at all, and NOT through genStep — an installed-but-undeclared package is
//     not a broken generator, and routing it through genStep would make a jail with an
//     orphan refuse to start;
//   - it runs BESIDE the other informational step that reads pack declarations
//     (AssertRequiredBins) and BEFORE the bootstrap can reinstall anything, which in Main
//     means before the exec at the bottom. The state it reads is the PREVIOUS launch's, and
//     that is the only state in which "undeclared" means anything.
//
// Every landmark is located with callIndex, not strings.Index: this file's own prose names
// all three functions, and boot.go's does too, so a plain substring search is satisfied by a
// COMMENT — including the comment that would be left behind if the call itself were removed.
func TestBootCatalogsOrphansBesideTheOtherInformationalSteps(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "entrypoint", "boot.go"))
	if err != nil {
		t.Fatalf("reading boot.go: %v", err)
	}
	got := string(src)

	call := callIndex(got, "CatalogInstalledOrphans(e)")
	if call < 0 {
		t.Fatal("boot.go never calls CatalogInstalledOrphans — the catalog is unreachable, " +
			"and every test in this file passes anyway")
	}
	if strings.Contains(got, `genStep(e, "catalog`) {
		t.Error("the catalog must not be a genStep: it generates nothing, and a fatal there " +
			"would mean a jail with one orphaned package refuses to START")
	}
	// Beside the other informational step that reads pack declarations, and above the
	// exec that hands control away.
	requires := callIndex(got, "AssertRequiredBins(e)")
	execCall := callIndex(got, "return execBash(e, command)")
	if requires < 0 || execCall < 0 {
		t.Fatalf("boot.go no longer contains the landmarks this ordering is about "+
			"(requires=%d, exec=%d)", requires, execCall)
	}
	// AFTER `requires`, which is what "beside" means here and is not cosmetic: both read
	// the same declarations against the same disk, and `requires` is the one that says a
	// DECLARED binary is missing. Naming the undeclared leftovers first would put the
	// answer above the question.
	if call < requires {
		t.Error("the catalog must run after AssertRequiredBins — the two informational " +
			"steps read the same declarations, and the missing-bin finding comes first")
	}
	if call > execCall {
		t.Error("the catalog must run before the exec that replaces this process")
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
