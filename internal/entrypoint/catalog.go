package entrypoint

// catalog.go is OQ-PD4's INFORMATIONAL half, and only that half
// (docs/design/program-delivery.md §10 step four): "dropping a pack does not auto-delete
// its program. Orphans are cataloged informationally at boot; removal happens only on an
// explicit act." Nothing here writes, unlinks or moves anything — it reads two directories
// and prints what it found.
//
// IT OBSERVES THE PREVIOUS BOOT'S STATE, deliberately. Main runs before ~/.yolo-bootstrap.sh
// and before any lazy launcher is ever invoked, so what is on disk here is what the LAST
// launch installed, compared against the declarations THIS launch carries. That is exactly
// the question an orphan is: a package installed under a declaration that is no longer
// there. Running it after the bootstrap would instead catalog a set the same boot had just
// re-installed, which answers nothing.
//
// IT IS NOT WIRED INTO RunDarwinBootstrap, and that is a fact about the backend rather than
// an omission. macos-user passes no YOLO_LSP_*_INSTALL (its launcher builds no LSP env at
// all) and stages no pack tree to compare against, so every declared-set input this needs
// would read as empty there — and an empty declared set turns a catalog into a boot that
// calls every installed package an orphan. A backend that cannot state what it declared
// must not be asked what is undeclared.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// catalogPrefix heads every line so the lines read as one report rather than as unrelated
// warnings. The wording states the finding and not a recommendation: nothing removes these,
// by ruling.
const catalogPrefix = "boot catalog: "

// CatalogInstalledOrphans reports every installed package and ~/.local/bin entry that no
// selected pack, preset or LSP recipe declares — one line each, naming the orphan and, for
// a file in ~/.local/bin, its size.
//
// It runs only when YOLO_PACK_ROOT is set. Without a staged pack tree the declared set is
// empty for a reason that has nothing to do with what is installed (an older host launcher,
// a backend that stages nothing), and a comparison against an empty declaration is not a
// catalog — it is a list of everything, reported as a problem.
func CatalogInstalledOrphans(e *Env) {
	if e.Getenv("YOLO_PACK_ROOT") == "" {
		return
	}
	packs, err := LoadJailPacks(e)
	if err != nil {
		// The load failure is already fatal via load_packs in the boot path; a second
		// report of the same fact would only bury it.
		return
	}
	for _, name := range catalogNpmOrphans(e, packs) {
		e.warn(catalogPrefix + "npm package installed but not declared by any selected " +
			"pack, preset or LSP recipe: " + name)
	}
	for _, orphan := range catalogLocalBinOrphans(e, packs) {
		e.warn(catalogPrefix + orphan.path + " installed but not declared by any " +
			"selected pack" + orphan.size)
	}
}

// catalogNpmOrphans compares the global node_modules tree against every npm package name
// this launch can account for.
func catalogNpmOrphans(e *Env, packs []*packload.Pack) []string {
	declared := map[string]struct{}{
		// GeneratePackageManagerLaunchers hardcodes exactly one package manager, so the
		// declared set does too. Deriving it would mean exporting that list for one reader.
		"pnpm": {},
	}
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if in.Kind != "npm" {
				continue
			}
			// The NAME half only: node_modules is indexed by name, and a declaration
			// carrying a selector (`foo@1.2.3`) would otherwise never match its own
			// installed directory. Same split the launcher makes, for the same reason.
			if name, _ := splitNpmSpec(in.Package); name != "" {
				declared[name] = struct{}{}
			}
		}
	}
	for _, pkg := range strings.Fields(mcpPresetNpmPackages(e)) {
		declared[pkg] = struct{}{}
	}
	// Both halves of the LSP set: the env var is what THIS launch asked for, the sentinel
	// is what the last bootstrap installed. A package the workspace dropped between boots
	// is in the sentinel and not the env, and the uninstall loop in ~/.yolo-bootstrap.sh
	// is about to remove it — reporting it here would name an orphan that has an owner.
	for _, pkg := range splitLSPInstallList(e.Getenv("YOLO_LSP_NPM_INSTALL")) {
		declared[pkg] = struct{}{}
	}
	for _, entry := range readLSPSentinel(e) {
		if pkg, ok := strings.CutPrefix(entry, "npm:"); ok && pkg != "" {
			declared[pkg] = struct{}{}
		}
	}

	var orphans []string
	for _, name := range installedNpmPackages(e) {
		if _, ok := declared[name]; !ok {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	return orphans
}

// installedNpmPackages lists the global prefix's package names, scoped ones included.
//
// A scope is a DIRECTORY, not a package: `@modelcontextprotocol/server-sequential-thinking`
// lives two levels down, so a one-level walk would report every scope as an orphan named
// `@modelcontextprotocol` and never see the package the pack actually declared.
//
// A DOT-PREFIXED NAME IS NEVER A PACKAGE, at either level, and the two-name denylist this
// replaced ("`.bin`", "`.package-lock.json`") named only the entries npm leaves behind when
// it SUCCEEDS. The ones that matter here are the ones it leaves when it is interrupted: an
// install stages the tree at `.<name>-<hash>` beside its destination and renames it into
// place, so a killed npm leaves `node_modules/.tool-a1b2c3` — or, for a scoped package,
// `node_modules/@scope/.tool-a1b2c3` two levels down, which the scoped walk emitted verbatim.
// Both got cataloged as orphaned packages under a name no declaration could ever match, on
// exactly the boot after a launch someone ctrl-C'd. The bootstrap's own ENOTEMPTY cleanup
// already uses this predicate (`find … -maxdepth 2 -name '.*' -type d`); this is the same
// rule, read-only.
func installedNpmPackages(e *Env) []string {
	root := filepath.Join(e.NpmPrefix, "lib", "node_modules")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		name := ent.Name()
		// npm's own bookkeeping and its interrupted-install staging dirs, not packages.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasPrefix(name, "@") {
			scoped, err := os.ReadDir(filepath.Join(root, name))
			if err != nil {
				continue
			}
			for _, s := range scoped {
				if strings.HasPrefix(s.Name(), ".") {
					continue
				}
				out = append(out, name+"/"+s.Name())
			}
			continue
		}
		out = append(out, name)
	}
	return out
}

// localBinOrphan is one ~/.local/bin finding: the path as a reader would type it, plus a
// rendered size (empty when there is none to state).
type localBinOrphan struct {
	path string
	size string
}

// catalogLocalBinOrphans compares ~/.local/bin against everything that has an owner: a
// pack's native installer, the two MCP wrapper surfaces, the macOS log helper, and the
// stale in-jail clients RemoveStaleGeneratedClients is already unlinking this boot.
func catalogLocalBinOrphans(e *Env, packs []*packload.Pack) []localBinOrphan {
	declared := map[string]struct{}{
		"chrome-devtools-mcp-wrapper": {}, // GenerateMCPWrappers
		"mcp-wrappers":                {}, // its sibling directory
		"yolo-log":                    {}, // InstallYoloLog (macOS)
	}
	for _, name := range staleGeneratedClients {
		declared[name] = struct{}{}
	}
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			// Native installers are the only kind that lands here: an npm program
			// resolves under $NPM_CONFIG_PREFIX/bin.
			if in.Kind == "native" && in.Bin != "" {
				declared[in.Bin] = struct{}{}
			}
		}
	}

	entries, err := os.ReadDir(e.LocalBin())
	if err != nil {
		return nil
	}
	var out []localBinOrphan
	for _, ent := range entries {
		if _, ok := declared[ent.Name()]; ok {
			continue
		}
		out = append(out, localBinOrphan{
			path: "~/.local/bin/" + ent.Name(),
			size: catalogSize(filepath.Join(e.LocalBin(), ent.Name())),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// catalogSize renders " (N unit)" for a regular file, or "" for anything else.
//
// The size is the whole reason this half of the catalog is worth reading: §5.3 measured a
// single vendor installer's leftovers at just over 1 GB per workspace, and a name on its
// own does not tell anyone which orphan is worth an explicit removal act.
//
// THE UNIT SCALES, because a fixed MB rendered every small orphan as "(0.0 MB)" — and the
// list this prints is mostly small: a wrapper script, a shim, a symlink target. A reader
// scanning for the 1 GB one saw a column of identical zeroes, which is the same as printing
// no size at all, except that it also reads as a measurement. Sub-KB sizes are whole bytes
// (a one-decimal 0.1 KB says less than "84 B"); above that one decimal is plenty, since
// nothing here turns on the second.
func catalogSize(path string) string {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	n := fi.Size()
	switch {
	case n < 1024:
		return fmt.Sprintf(" (%d B)", n)
	case n < 1024*1024:
		return fmt.Sprintf(" (%.1f KB)", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf(" (%.1f MB)", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf(" (%.1f GB)", float64(n)/(1024*1024*1024))
	}
}

// splitLSPInstallList splits a YOLO_LSP_*_INSTALL value, which is newline-separated with
// empty lines allowed (internal/cli/run/lsp.go joins the recipe packages with "\n").
func splitLSPInstallList(v string) []string {
	var out []string
	for _, line := range strings.Split(v, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// readLSPSentinel returns the lines of ~/.yolo-installed-lsps — what the LAST bootstrap
// installed, one `kind:identifier` per line. Absent reads as empty; this never writes it.
func readLSPSentinel(e *Env) []string {
	data, err := os.ReadFile(filepath.Join(e.Home, ".yolo-installed-lsps"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range splitLines(string(data)) {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}
