package entrypoint

// catalog.go is OQ-PD4's INFORMATIONAL half (docs/design/program-delivery.md §10 step
// four): "dropping a pack does not auto-delete its program. Orphans are cataloged
// informationally at boot; removal happens only on an explicit act; autoprune exists as an
// option, default off." Nothing in this file decides to write, unlink or move anything — it
// reads the install directories and prints what it found. The one unlink here is the
// confined filesystem's at the end of the file, which carries out on macos-user the removals
// the act (orphanremove.go) planned, beneath roots the catalog opened (catalogConfinedOrphans).
//
// THE ACT IS orphanremove.go, and it is reachable from here only through the option OQ-PD4
// rules default off: CatalogInstalledOrphans ends by calling autopruneOrphans, which returns
// immediately unless the user turned the knob on. A boot with the knob off is byte for byte
// the boot this file described before the act existed. What the two files SHARE is the
// candidate set — InstalledOrphans below — and sharing it is the whole design: an act with
// its own idea of what an orphan is would be a second implementation of the one judgement
// that decides which files get deleted.
//
// IT OBSERVES THE PREVIOUS BOOT'S STATE, deliberately. Main runs before ~/.yolo-bootstrap.sh
// and before any lazy launcher is ever invoked, so what is on disk here is what the LAST
// launch installed, compared against the declarations THIS launch carries. That is exactly
// the question an orphan is: a package installed under a declaration that is no longer
// there. Running it after the bootstrap would instead catalog a set the same boot had just
// re-installed, which answers nothing.
//
// IT RUNS ON BOTH BOOTS. The macos-user bootstrap left it out twice, for two different
// reasons that are both gone: first on the premise that that backend stages no pack tree (it
// stages one, named by YOLO_PACK_ROOT, and the gate on InstalledOrphans — nothing without
// YOLO_PACK_ROOT — keeps the rule that a boot unable to state what it declared is not asked
// what is undeclared), and then because every place the summary line points was missing there
// (notch-convergence.md, NC-D26). Since then the bootstrap keeps the container's boot.log, so
// the names e.note writes land where the line says; the session names the staged tree and the
// workspace, so `yolo programs ls` reads this jail from inside the sandbox; and the launch
// relays YOLO_PROGRAMS_AUTOPRUNE from the user's config as the container launch does. There it
// is CONFINED (catalogConfinedOrphans, at the end of this file), because that bootstrap runs
// outside the sandbox and reaches every directory below through the agent-writable sidecar.
//
// NO LSP RECIPE DECLARES ANYTHING ANY MORE. The three-entry table that mapped `lsp_servers`
// names to packages (and the ~/.yolo-installed-lsps sentinel recording what it installed) is
// deleted (docs/reference/mcp-configuration.md#oq-lsp1), so the packages it installed before
// the deletion — pyright, typescript, typescript-language-server under the npm prefix, gopls
// under $GOBIN — are exactly what this catalog now names as orphans, and `yolo programs
// remove` (or `programs.autoprune`) is how they are collected. That is the migration: no
// one-shot uninstall was written for them, because a record-keyed removal is the thing
// orphanremove.go's header measured losing its record.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// catalogPrefix heads every line so the lines read as one report rather than as unrelated
// warnings. The wording states the finding and not a recommendation: nothing removes these,
// by ruling.
const catalogPrefix = "boot catalog: "

// OrphanClass names WHICH of the three finders produced an orphan, and with it the
// mechanism that would have to remove it: an npm package is a directory under the global
// prefix with symlinks pointing into it, while the other two are a single directory entry.
// The class is carried rather than re-derived from the path, because the removal act
// (orphanremove.go) branches on exactly this and a prefix match on a path is how the two
// would come to disagree about what a `~/.local/bin` entry is.
type OrphanClass string

const (
	// OrphanNpm is a package directory under $NPM_CONFIG_PREFIX/lib/node_modules.
	OrphanNpm OrphanClass = "npm"
	// OrphanLocalBin is an entry of ~/.local/bin — where a native installer lands.
	OrphanLocalBin OrphanClass = "local-bin"
	// OrphanGoBin is an entry of $GOPATH/bin — where the deleted LSP recipe's go arm landed.
	OrphanGoBin OrphanClass = "go-bin"
)

// Orphan is one installed thing that no selected pack or MCP preset declares.
//
// IT CARRIES THE BYTES' ABSOLUTE PATH, which the boot report never prints, and that is the
// point of the type: the catalog is a report, but the removal act OQ-PD4 rules is an act on
// FILES, and the whole reason the three surviving LSP packages in this jail could not be
// removed by the sentinel loop is that the only thing naming them was a RECORD, which was
// gone. An orphan is derived from the bytes on disk minus the declarations, so it can be
// removed whether or not anything ever recorded installing it.
type Orphan struct {
	// Class is which finder found it — see OrphanClass.
	Class OrphanClass
	// Name is the npm package name (scope included) or the directory entry's name.
	Name string
	// Path is the ABSOLUTE path of the bytes: the package directory, or the entry.
	Path string
	// Display is how a report names it: the package name for npm (node_modules is
	// indexed by name and a path would say nothing a reader can act on), the
	// home-relative path for the two directory finders.
	Display string
	// Size is the rendered size of a REGULAR FILE, empty for anything else — the boot
	// report's own rule, kept here so the boot line is composed from this struct rather
	// than from a second walk. The removal act measures directories itself; it is a
	// user-invoked act where a walk is affordable and the number is the point.
	Size string
}

// InstalledOrphans returns every installed package, ~/.local/bin entry and $GOBIN binary
// that no selected pack or MCP preset declares. It reads the directory trees and the pack
// manifests; it writes nothing.
//
// THE THREE FINDERS ARE THE THREE PLACES A yolo-RUN INSTALL LANDS OR LANDED, and the set is
// closed by the mechanisms rather than by taste: an npm program resolves under
// $NPM_CONFIG_PREFIX/lib/node_modules, a native installer's program under ~/.local/bin, and
// the deleted LSP recipe's go arm under $GOBIN (the bootstrap's `go install`, until the
// recipe table went). A finder missing for one of them does not make that class clean — it
// makes it INVISIBLE, which is worse the moment an explicit removal act reads this list: the
// act's candidates would be the two classes someone happened to walk.
//
// It answers EMPTY unless YOLO_PACK_ROOT is set. Without a staged pack tree the declared set
// is empty for a reason that has nothing to do with what is installed (an older host
// launcher, a backend that stages nothing), and a comparison against an empty declaration is
// not a catalog — it is a list of everything, reported as a problem. That gate is on the
// SHARED function rather than on the boot renderer because the removal act reads this same
// list: a gate only the reporter honoured would leave the act computing "everything
// installed" as its candidate set on exactly the launches where the declarations are
// unknowable.
func InstalledOrphans(e *Env) []Orphan {
	if e.Getenv("YOLO_PACK_ROOT") == "" {
		return nil
	}
	packs, err := LoadJailPacks(e)
	if err != nil {
		// The load failure is already fatal via load_packs in the boot path; a second
		// report of the same fact would only bury it.
		return nil
	}
	var out []Orphan
	nodeModules := filepath.Join(e.NpmPrefix, "lib", "node_modules")
	for _, name := range catalogNpmOrphans(e, packs) {
		out = append(out, Orphan{
			Class:   OrphanNpm,
			Name:    name,
			Path:    filepath.Join(nodeModules, name),
			Display: name,
		})
	}
	for _, orphan := range catalogLocalBinOrphans(e, packs) {
		out = append(out, orphan.orphan(OrphanLocalBin))
	}
	for _, orphan := range catalogGoBinOrphans(e, packs) {
		out = append(out, orphan.orphan(OrphanGoBin))
	}
	return out
}

// CatalogInstalledOrphans reports every installed package, ~/.local/bin entry and $GOBIN
// binary that no selected pack or MCP preset declares: ONE line on the launch
// terminal stating how many there are, and the list — one line each, naming the orphan and,
// for a file, its size — in the boot log.
//
// It is the boot's RENDERER for InstalledOrphans and, when the user has turned the option
// on, the caller of the removal act. Autoprune is OQ-PD4's third clause and it is DEFAULT
// OFF: without the option this function is exactly what it was before the act existed —
// three loops that report. See autopruneOrphans for what turning it on costs.
//
// # Why the list moved off the terminal
//
// These are docs/reference/report-tiers.md's NOTCH FACTS WITH A STATE DEPENDENCY: true until
// the user does something, and repeated until then. This jail has printed the same eight lines
// at every launch since the packages were installed (measured 2026-09-10) — a launch stream
// where a third of the boot's lines never change is one where the lines that DO change are
// skimmed past. The launch stream rules the compression: one line, with the list going to
// boot.log through the split e.note already exists for (Env.LogOnly).
//
// THE SET IS NOT COMPRESSED, only the lines. Every orphan is still named, in the file that
// the very same launch writes, and the summary says where — which is what keeps this a
// change of density rather than a deletion. It is also why the count is the summary's
// subject: a number the reader can compare against the last launch's is the one fact eight
// invariant lines were failing to deliver.
//
// NO QUIET FLAG FOLLOWS FROM THIS (OQ-RO3). The compression IS the density control for the
// launch stream; a flag that could hide a line is refused by P4, which the launch stream
// promotes from three docstrings that each reached it independently to the written rule.
func CatalogInstalledOrphans(e *Env) {
	orphans := InstalledOrphans(e)
	reportOrphans(e, orphans)
	autopruneOrphans(e, orphans)
}

// reportOrphans is the catalog's report half: the one terminal line and the boot-log list.
func reportOrphans(e *Env, orphans []Orphan) {
	if len(orphans) == 0 {
		return
	}
	e.warn(catalogSummary(len(orphans)))
	for _, o := range orphans {
		e.note(catalogPrefix + catalogLine(o))
	}
}

// catalogSummary is the one line the launch terminal gets. It states the finding, the count
// and where the names are — never a recommendation, because nothing removes these by ruling
// (OQ-PD4) — and it names the two verbs that act, so a reader who wants the list is one
// command away rather than one file away.
func catalogSummary(n int) string {
	return fmt.Sprintf("%s%d installed %s %s declared by no selected pack or preset — "+
		"boot.log names them (`yolo programs ls` for sizes; `programs.autoprune` "+
		"removes them)", catalogPrefix, n,
		catalogPlural(n, "program", "programs"), catalogPlural(n, "is", "are"))
}

// catalogPlural picks a word for n. Local to this file: the entrypoint is the boot's
// dependency-light half and one call site does not earn a shared helper.
func catalogPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// catalogLine is one orphan's line, in the boot log. The classes state DIFFERENT declaring
// sets — an npm package can be claimed by a pack or an MCP preset, a ~/.local/bin entry only
// by a pack, and a $GOBIN entry by nothing at all now that the LSP recipe is gone — and
// saying so per class is the half of the report that tells a reader which declaration they
// would have to add to keep it.
func catalogLine(o Orphan) string {
	switch o.Class {
	case OrphanNpm:
		return "npm package installed but not declared by any selected pack or preset: " +
			o.Display
	case OrphanLocalBin:
		return o.Display + " installed but not declared by any selected pack" + o.Size
	case OrphanGoBin:
		return o.Display + " installed but not declared by anything" + o.Size
	}
	return o.Display
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
			// A FORK's build owns the npm packages its `produces` names under the prefix: a
			// source build installed with `npm install -g` lands its package there, and an
			// undeclared one would be deleted at every boot under `programs.autoprune`.
			for _, name := range forkOutputsUnder(in, ".npm-global/lib/node_modules", true) {
				declared[name] = struct{}{}
			}
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
	fsys := e.orphanFiles()
	root := filepath.Join(e.NpmPrefix, "lib", "node_modules")
	entries, err := fsys.ReadDir(root)
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
			scoped, err := fsys.ReadDir(filepath.Join(root, name))
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

// pathOrphan is one file-tree finding: the path as a reader would type it, plus a rendered
// size (empty when there is none to state). Shared by the two directory finders — a
// ~/.local/bin entry and a $GOBIN entry are the same kind of report about different
// resolvers, and one struct is what keeps the two lines rendering alike.
type pathOrphan struct {
	path string
	size string
	// abs is the path the report does NOT print — the one the removal act unlinks. The
	// rendered `path` cannot be reversed into it: catalogPath collapses the home to a
	// tilde, and re-expanding a tilde is how an act comes to delete the wrong file on a
	// jail whose GOPATH sits outside the home.
	abs string
}

// orphan lifts a directory finding into the shared Orphan shape. The two finders differ only
// in which class they are, which is why they share this and the struct above.
func (p pathOrphan) orphan(class OrphanClass) Orphan {
	return Orphan{
		Class:   class,
		Name:    filepath.Base(p.abs),
		Path:    p.abs,
		Display: p.path,
		Size:    p.size,
	}
}

// catalogPath renders an absolute path the way a reader would type it: home-relative with a
// tilde when it is under the jail home, verbatim otherwise.
//
// The tilde is not cosmetic for $GOBIN specifically. GOPATH is an ordinary environment
// variable, so $GOPATH/bin need not be under the home at all, and a hardcoded "~/go/bin/"
// prefix would print a path that does not exist on a jail whose GOPATH was set elsewhere —
// in a report whose whole value is that a reader can go look at the file.
func catalogPath(e *Env, abs string) string {
	if rel, ok := strings.CutPrefix(abs, e.Home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rel)
	}
	return abs
}

// catalogLocalBinOrphans compares ~/.local/bin against everything that has an owner: a
// pack's native installer, the two MCP wrapper surfaces, the macOS log helper, and the
// stale in-jail clients RemoveStaleGeneratedClients is already unlinking this boot.
func catalogLocalBinOrphans(e *Env, packs []*packload.Pack) []pathOrphan {
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
			// And a FORK's build, whichever of its outputs land here (its program, when that
			// is .local/bin/<bin>, and anything else its `produces` names in this directory).
			for _, name := range forkOutputsUnder(in, ".local/bin", false) {
				declared[name] = struct{}{}
			}
		}
	}

	return catalogDirOrphans(e, e.LocalBin(), declared)
}

// forkOutputsUnder lists the entries directly under dir (home-relative) that a FORK's build
// declares in its `produces`, for the declared sets above: `.local/bin/pi` under `.local/bin` is
// "pi". scoped reads a `@scope/name` two levels down as one name, npm's layout. Nothing for any
// program that is not a fork's.
func forkOutputsUnder(in packdecl.Install, dir string, scoped bool) []string {
	if in.Kind != packdecl.InstallKindSource {
		return nil
	}
	var out []string
	for _, p := range in.Produces {
		rest, ok := strings.CutPrefix(p, dir+"/")
		if !ok || rest == "" {
			continue
		}
		parts := strings.Split(rest, "/")
		name := parts[0]
		if scoped && strings.HasPrefix(name, "@") && len(parts) > 1 {
			name += "/" + parts[1]
		}
		out = append(out, name)
	}
	return out
}

// catalogGoBinOrphans lists every $GOBIN entry no FORK's build declares.
//
// THE DECLARED SET WAS EMPTY until the fork route, and that was the deletion of the LSP recipe
// table rather than an oversight (docs/reference/mcp-configuration.md#oq-lsp1). Its go arm was
// the only thing in yolo that ever ran `go install` into this directory, and the launcher
// templates land under the npm prefix and ~/.local/bin. The one declaration that can own a
// $GOBIN file now is a fork whose `produces` names `go/bin/<name>`
// (docs/design/forked-programs-as-packs.md) — a forked Go program built with `go install`. The
// finder stays for what the LSP arm left behind too: a gopls installed before the deletion has no
// record anywhere now (the ~/.yolo-installed-lsps sentinel went with the table), and without this
// class it would be invisible to the catalog and to `yolo programs remove` for the life of the
// home.
func catalogGoBinOrphans(e *Env, packs []*packload.Pack) []pathOrphan {
	declared := map[string]struct{}{}
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			for _, name := range forkOutputsUnder(in, "go/bin", false) {
				declared[name] = struct{}{}
			}
		}
	}
	return catalogDirOrphans(e, e.GoBin(), declared)
}

// catalogDirOrphans lists every entry of dir whose name is not in declared, rendered as a
// finding. A dir that does not exist reads as empty — a jail that installed nothing there
// has nothing to report, which is not the same as a finding.
func catalogDirOrphans(e *Env, dir string, declared map[string]struct{}) []pathOrphan {
	fsys := e.orphanFiles()
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []pathOrphan
	for _, ent := range entries {
		if _, ok := declared[ent.Name()]; ok {
			continue
		}
		full := filepath.Join(dir, ent.Name())
		out = append(out, pathOrphan{
			path: catalogPath(e, full),
			size: catalogSize(fsys, full),
			abs:  full,
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
func catalogSize(fsys orphanFS, path string) string {
	fi, err := fsys.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return " (" + RenderSize(fi.Size()) + ")"
}

// RenderSize is the scale itself, split out of catalogSize for the removal act, which
// measures DIRECTORIES (catalogSize deliberately does not) and prints the number in a
// sentence rather than in parentheses after a path. One ladder, so a 181 MB orphan reads the
// same in the boot catalog line, in the line that says it was deleted, and in
// `yolo programs ls` — which is why it is exported: the CLI renders the same numbers, and a
// second ladder there would make the same orphan two different sizes.
func RenderSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

// ---------------------------------------------------------------------------
// The macos-user bootstrap's catalog, confined beneath the workspace sidecar
// ---------------------------------------------------------------------------

// catalogConfinedOrphans is the catalog step on the macos-user bootstrap: CatalogInstalledOrphans
// with every read the finders make, and every unlink its autoprune makes, done beneath a root
// opened on the directory the finder names, never through a path.
//
// WHY. This bootstrap runs as the sandbox account OUTSIDE Seatbelt (RunDarwinBootstrap), and on
// this backend each directory the finders read is reached through a home-layout link into the
// workspace sidecar (~/.local -> <workspace>/.yolo/home/local), which the agent writes. A link
// the agent left below one — <sidecar>/local/bin pointing at a sibling workspace under the
// shared root, which this account can write and the session's profile denies the agent — made
// the catalog list that workspace's files as orphans and autoprune delete them (reproduced: a
// sibling's src/main.go, deleted). In a container the boot sees only what the agent sees, so
// only this boot needs it; `yolo programs` runs as the agent, inside the sandbox here, and keeps
// the plain filesystem.
//
// HOW. Each finder directory is opened one component at a time (paths.OpenStateDirRoot, then
// paths.OpenStateSubdirRoot), each refusing a symbolic link, from the workspace's `.yolo` along
// the physical path the layout's own link names, and only when that link is the one this launch
// laid (confinedOrphanChain). A launch that named no sidecar (an install capture's staging home,
// a test) has no layout links, and is opened from the home the same way. The finders then read,
// and the act unlinks, beneath those roots (confinedOrphanFS), so nothing swapped in after the
// open redirects them. An absent directory is an empty one, the ordinary first-boot state.
//
// A REFUSED DIRECTORY IS SAID, naming the link and the next step, and while any is refused this
// boot REMOVES NOTHING, autoprune or not: an npm package's bin links live in a second directory,
// and an act that could reach only part of what its plan announces is not the act the plan
// names. The other finders still catalog.
func catalogConfinedOrphans(e *Env) {
	if e.Getenv("YOLO_PACK_ROOT") == "" {
		return // InstalledOrphans' own gate: no staged tree, nothing declared, no catalog
	}
	fsys := openDarwinOrphanFS(e)
	defer fsys.close()
	refusals := fsys.refusals(e)
	for _, line := range refusals {
		e.warnOnce(line)
	}
	saved := e.orphanFS
	e.orphanFS = fsys
	defer func() { e.orphanFS = saved }()

	orphans := InstalledOrphans(e)
	reportOrphans(e, orphans)
	if len(refusals) == 0 {
		autopruneOrphans(e, orphans)
		return
	}
	if autopruneEnabled(e) && len(orphans) > 0 {
		e.warn(autoprunePrefix + "programs.autoprune is ON, and this boot removes nothing: a " +
			"symbolic link sits on the way to a directory it reads (above). Remove the link and " +
			"relaunch, or remove the orphans with `yolo programs remove` inside a session")
	}
}

// openDarwinOrphanFS opens every directory the finders and the act read, confined
// (catalogConfinedOrphans says how).
func openDarwinOrphanFS(e *Env) *confinedOrphanFS {
	// The layout without the packs: every finder directory sits under a core link
	// (paths.HomeSurfaces), laid for every launch whatever it selects, as gitGlobalConfigFile's
	// ~/.config is.
	layout, _ := darwinHomeLayoutFor(e, nil)
	c := &confinedOrphanFS{}
	for _, dir := range []string{filepath.Join(e.NpmPrefix, "lib", "node_modules"), e.NpmBin(),
		e.LocalBin(), e.GoBin()} {
		d := &confinedOrphanDir{path: dir}
		d.root, d.err = openConfinedOrphanDir(e, layout, dir)
		c.dirs = append(c.dirs, d)
	}
	return c
}

// openConfinedOrphanDir opens dir along confinedOrphanChain, one component at a time, refusing a
// symbolic link at each. It returns a nil root and a nil error when a component is absent.
func openConfinedOrphanDir(e *Env, layout DarwinHomeLayout, dir string) (*os.Root, error) {
	base, chain, err := confinedOrphanChain(e, layout, dir)
	if err != nil {
		return nil, err
	}
	r, err := paths.OpenStateDirRoot(base)
	if err != nil {
		return nil, absentIsEmpty(err)
	}
	cur := base
	for _, name := range chain {
		cur = filepath.Join(cur, name)
		next, err := paths.OpenStateSubdirRoot(r, name, cur)
		r.Close()
		if err != nil {
			return nil, absentIsEmpty(err)
		}
		r = next
	}
	return r, nil
}

// absentIsEmpty maps a missing component to no error: a finder directory nothing has installed
// into yet reads as empty, which is a state rather than a finding.
func absentIsEmpty(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// confinedOrphanChain is where dir physically is: the directory to open first, and the
// components below it, in order. Under the layout that is the workspace's `.yolo`, then the
// sidecar's own name and the link target's subtree (home/local for ~/.local), then the rest of
// dir — and only when the home's link is the one this launch laid, to the target it laid, the
// rule DarwinHomeLayout.homeFileThroughLayout applies to a write. Under no layout link, the home
// and the path below it. A dir outside the home cannot be confined, and is refused.
func confinedOrphanChain(e *Env, layout DarwinHomeLayout, dir string) (string, []string, error) {
	if layout.Sidecar != "" {
		yolo := filepath.Dir(layout.Sidecar)
		for _, ln := range layout.Links {
			rest, ok := pathUnder(dir, ln.Path)
			if !ok {
				continue
			}
			if got, err := os.Readlink(ln.Path); err != nil || got != ln.Target {
				return "", nil, fmt.Errorf("%s is not this launch's layout link to %s (the "+
					"darwin_home_layout step says why)", ln.Path, ln.Target)
			}
			target, ok := pathUnder(ln.Target, yolo)
			if !ok {
				return "", nil, fmt.Errorf("%s links outside %s", ln.Path, yolo)
			}
			return yolo, append(pathParts(target), pathParts(rest)...), nil
		}
	}
	rel, ok := pathUnder(dir, e.Home)
	if !ok {
		return "", nil, fmt.Errorf("%s is outside the sandbox home %s, so this bootstrap, which "+
			"runs outside the sandbox, cannot confine what it reads there", dir, e.Home)
	}
	return e.Home, pathParts(rel), nil
}

// pathUnder reports whether p is dir or below it, and the part below ("" for dir itself).
func pathUnder(p, dir string) (string, bool) {
	if p == dir {
		return "", true
	}
	rest, ok := strings.CutPrefix(p, dir+string(filepath.Separator))
	return rest, ok && rest != ""
}

// pathParts splits a relative path into its components; "" has none.
func pathParts(rel string) []string {
	if rel == "" {
		return nil
	}
	return strings.Split(filepath.ToSlash(rel), "/")
}

// confinedOrphanFS is the orphanFS the macos-user bootstrap's catalog reads and unlinks
// through: a set of roots, each opened confined on one finder directory. A path below none of
// them is refused, so the act cannot unlink anything the finders did not read.
type confinedOrphanFS struct {
	dirs []*confinedOrphanDir
}

// confinedOrphanDir is one finder directory: the path the finders name it by, and the root
// opened on it, or why there is none (root and err both nil: the directory is absent).
type confinedOrphanDir struct {
	path string
	root *os.Root
	err  error
}

// errOutsideOrphanDirs refuses a path no finder directory holds.
var errOutsideOrphanDirs = errors.New("not below any directory the boot catalog reads")

// resolve finds the finder directory holding name, and name's path beneath its root.
func (c *confinedOrphanFS) resolve(name string) (*confinedOrphanDir, string, error) {
	var best *confinedOrphanDir
	var rest string
	for _, d := range c.dirs {
		if r, ok := pathUnder(name, d.path); ok && (best == nil || len(d.path) > len(best.path)) {
			best, rest = d, r
		}
	}
	switch {
	case best == nil:
		return nil, "", &fs.PathError{Op: "open", Path: name, Err: errOutsideOrphanDirs}
	case best.err != nil:
		return nil, "", &fs.PathError{Op: "open", Path: name, Err: best.err}
	case best.root == nil:
		return nil, "", &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if rest == "" {
		rest = "."
	}
	return best, rest, nil
}

func (c *confinedOrphanFS) ReadDir(name string) ([]fs.DirEntry, error) {
	d, rel, err := c.resolve(name)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(d.root.FS(), filepath.ToSlash(rel))
}

func (c *confinedOrphanFS) Stat(name string) (fs.FileInfo, error) {
	d, rel, err := c.resolve(name)
	if err != nil {
		return nil, err
	}
	return d.root.Stat(rel)
}

func (c *confinedOrphanFS) Lstat(name string) (fs.FileInfo, error) {
	d, rel, err := c.resolve(name)
	if err != nil {
		return nil, err
	}
	return d.root.Lstat(rel)
}

func (c *confinedOrphanFS) Readlink(name string) (string, error) {
	d, rel, err := c.resolve(name)
	if err != nil {
		return "", err
	}
	return d.root.Readlink(rel)
}

// RemoveAll unlinks name beneath its root: a link there is removed, never followed, and a path
// leaving the root through one is refused (os.Root's own rule). An absent directory holds
// nothing to remove, which is os.RemoveAll's contract too. A finder directory itself is never
// removed.
func (c *confinedOrphanFS) RemoveAll(name string) error {
	d, rel, err := c.resolve(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if rel == "." {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrInvalid}
	}
	return d.root.RemoveAll(rel)
}

func (c *confinedOrphanFS) WalkDir(name string, fn fs.WalkDirFunc) error {
	d, rel, err := c.resolve(name)
	if err != nil {
		return fn(name, nil, err)
	}
	return fs.WalkDir(d.root.FS(), filepath.ToSlash(rel), func(p string, de fs.DirEntry, err error) error {
		return fn(filepath.Join(d.path, filepath.FromSlash(p)), de, err)
	})
}

// close closes every root.
func (c *confinedOrphanFS) close() {
	for _, d := range c.dirs {
		if d.root != nil {
			d.root.Close()
		}
	}
}

// refusals renders each refused directory as one terminal line, grouped by what refused it, so
// a linked `.yolo` reads as one finding rather than four.
func (c *confinedOrphanFS) refusals(e *Env) []string {
	// why is the sentence a refusal ends with, keyed by itself so dirs sharing it group.
	var whys []string
	dirsBy := map[string][]string{}
	for _, d := range c.dirs {
		if d.err == nil {
			continue
		}
		why := d.err.Error() + "."
		var linked *paths.LinkedStateDirError
		if errors.As(d.err, &linked) {
			why = linked.Path + " is a symbolic link, and this bootstrap runs outside the " +
				"sandbox, so reading or removing through it could reach a directory the " +
				"session's sandbox profile protects. Remove the link (what it points at is left " +
				"alone): sudo rm " + shquote.Quote(linked.Path) + "."
		}
		if _, seen := dirsBy[why]; !seen {
			whys = append(whys, why)
		}
		dirsBy[why] = append(dirsBy[why], catalogPath(e, d.path))
	}
	out := make([]string, 0, len(whys))
	for _, why := range whys {
		dirs := dirsBy[why]
		out = append(out, catalogPrefix+strings.Join(dirs, " and ")+" "+
			catalogPlural(len(dirs), "is", "are")+" not cataloged, and this boot removes nothing: "+
			why+" Inside a session, `yolo programs ls` and `yolo programs remove` still read and "+
			"remove there, confined by its sandbox profile.")
	}
	return out
}
