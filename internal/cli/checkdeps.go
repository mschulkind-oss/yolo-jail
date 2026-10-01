package cli

// checkdeps.go is `yolo check-deps` — the standalone entry point to the shared
// dep-checker (env-manager plan Phase 6). It probes the host for every binary the
// configured packs declare install_hints for, reports present/missing, and — because a
// wall of `→ install X` lines is one step short of useful — writes the package
// manager's own manifest (a Brewfile and kin) so the user tunes the host up in one step.
//
// It NEVER installs anything (BACKLOG's detect-vs-apply split): it detects and hands off
// with the command. The offer-to-run (behind a batched, sudo-shown-through confirm,
// OQ-9) belongs to `apply` at a lower notch — this verb is the probe half, usable by a
// project's own doctor over the same declared hints.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func runCheckDeps(args []string) int {
	return checkDepsMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

func checkDepsMain(args []string, out, errw io.Writer, color bool) int {
	writeManifest := true
	for _, a := range args {
		switch {
		case isHelpToken(a):
			io.WriteString(out, checkDepsUsage+"\n")
			return 0
		case a == "--no-manifest":
			writeManifest = false
		default:
			fmt.Fprintf(errw, "yolo check-deps: unexpected argument %q\n\n%s\n", a, checkDepsUsage)
			return 2
		}
	}

	reqs, unresolved, floor, declarers := configuredDepRequirements()
	pr := richtext.Printer{W: out, Color: color}
	// NAMED, NEVER SKIPPED: a pack this probe could not resolve declares deps nobody looked
	// at, so "nothing missing" would be a claim about binaries it never checked.
	for _, u := range unresolved {
		pr.Printf("[red]✗[/red] pack %s could not be resolved, so its deps were not probed: %s",
			u.Name, u.Reason)
	}
	// The floor's programs, each by its floor entry: never missing in the sense this verb exits 1
	// over, because the floor installs one that is not there yet (HP-D3).
	floorBins := make([]string, 0, len(floor))
	for bin := range floor {
		floorBins = append(floorBins, bin)
	}
	sort.Strings(floorBins)
	for _, bin := range floorBins {
		pr.Printf("[green]✓[/green] %-16s %s", bin, floorDepClause(floor[bin]))
	}
	if len(reqs) == 0 && len(floor) > 0 {
		if len(unresolved) > 0 {
			return 1
		}
		return 0
	}
	if len(reqs) == 0 {
		fmt.Fprintln(out, "no host-dep hints declared by the resolved packs — nothing to check.")
		if len(unresolved) > 0 {
			return 1
		}
		return 0
	}
	// THE LAUNCH PATH (host-launch-environment.md §2.2): the PATH this command was started with,
	// then `host_path`'s folders — the PATH a `yolo host` launch from the same shell checks, read
	// through the one resolver's lookup, so check-deps and the launch cannot disagree about a
	// binary. The package manager each remedy names is found on it too.
	lp := hostLaunchPath()
	results := depcheck.Check(reqs, lp.LookPath)

	missing := depcheck.Missing(results)
	for _, r := range results {
		switch {
		case r.Present:
			pr.Printf("[green]✓[/green] %-16s %s", r.Bin, r.Path)
		case r.Unpublished != "":
			// Not missing, and not an exit-1: nothing could install it (depcheck.Missing
			// leaves it out), so the line is the reason and no command.
			pr.Printf("[yellow]–[/yellow] %-16s no build for this host — %s", r.Bin, r.Unpublished)
		case r.Remedy != "":
			pr.Printf("[red]✗[/red] %-16s MISSING → %s", r.Bin, r.Remedy)
			// The package-manager alternative for a dep whose primary remedy is the tool's
			// own installer. Shown because a user who would rather go through their package
			// manager should not have to read pack.json to find the token — but shown SECOND,
			// since the first-party installer is the one that stays current.
			if r.Fallback != "" {
				pr.Printf("  [dim]or via %s: %s[/dim]", r.Manager, r.Fallback)
			}
		case r.Manager == "":
			// No manager on this PATH, so no hint could be the remedy: say that, rather than
			// blame the pack's hints or name a manager the host does not have.
			pr.Printf("[yellow]?[/yellow] %-16s MISSING, %s", r.Bin, richtext.Escape(depcheck.NoManager))
		default:
			pr.Printf("[yellow]?[/yellow] %-16s MISSING, no install hint for this host", r.Bin)
		}
		// THE MISS LINE (HE-D2) under every binary the lookup did not find: the whole PATH
		// searched and the `host_path` fix, since from a bare launcher "missing" may only mean "not
		// on this PATH". A program with no build for this host too: it is not missing (nothing
		// could install it), but `yolo host -- <it>` runs the copy this PATH holds, if any.
		if !r.Present {
			if line := lp.MissLine(declarers[r.Bin].miss(r.Bin, false)); line != "" {
				pr.Printf("  %s", richtext.Escape(line))
			}
		}
	}
	if len(missing) == 0 {
		if len(unresolved) > 0 {
			return 1 // an unprobed pack is not a clean bill of health
		}
		return 0
	}

	if writeManifest {
		if name, body := depcheck.Manifest(results); name != "" {
			p := filepath.Join(depManifestDir(), name)
			if err := os.MkdirAll(depManifestDir(), 0o755); err == nil &&
				os.WriteFile(p, []byte(body), 0o644) == nil {
				pr.Printf("")
				pr.Printf("[dim]wrote %s — install with the command for your manager[/dim]", p)
			}
		}
	}
	// Missing deps are a non-zero exit so a CI or a caller can gate on it.
	return 1
}

// depManifestDir is the fixed, user-scoped home for the generated dep manifest
// (~/.config/yolo). Env-manager plan Phase 6 wants this to become a composed surface
// regenerated every apply; this standalone verb writes it directly for now.
func depManifestDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "yolo")
}

// configuredDepRequirements collects DepRequirements across every configured pack,
// adapted to depcheck.Requirement, plus every configured pack it could not resolve.
// Embedded/local/fetched all contribute — a dep is a dep regardless of origin, and a git
// pack resolves from the store the way a launch resolves it (resolveConfiguredPack). A
// pack that does not resolve is RETURNED rather than dropped: this verb is a probe, not a
// gate on loading, but a probe that silently skipped a pack would report "nothing missing"
// about deps it never looked at.
//
// The per-pack adaptation lives in packDepRequirements (applyhostdeps.go) because
// `yolo host apply` needs the same projection one pack at a time; keeping one adapter is what
// stops the two commands from disagreeing about what counts as a requirement.
//
// The third return is the programs the HOST AGENT FLOOR answers for, left out of the first: their
// answer is the floor entry — the copy `yolo host` runs — never whatever a PATH holds
// (host-launch-environment.md §3). Empty in a jail.
//
// The fourth is which packs declare each binary, the "required by" of a miss line.
func configuredDepRequirements() ([]depcheck.Requirement, []unresolvedPack, map[string]hostfloor.Status,
	map[string]*depDeclarers) {
	// The one selection function (selectHostPacks, notch-convergence item 6): a pack the
	// selection closure joins is one a launch delivers, so its deps are probed too.
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return nil, nil, nil, nil
	}
	floor := floorDeliveredBins(sel.packs)
	var reqs []depcheck.Requirement
	for _, p := range sel.packs {
		reqs = append(reqs, withoutFloorBins(packDepRequirements(p), floor)...)
	}
	return reqs, sel.problems(), floor, declarersOf(sel.packs)
}

// unresolvedPack is one configured pack that could not be resolved, and the resolver's own
// words for why, which name what is missing (a git pack the store does not have, a ref its
// mirror lacks, the path of a missing local dir).
type unresolvedPack struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// NeedsInstall is whether the fix is a FETCH: a git pack whose address parsed but whose
	// mirror or ref is not in the store (packsrc.ErrNotFetched). The next host launch's pack refresh step
	// fetches it, and `yolo pack install` fetches it now; the field keeps its name (and its
	// JSON key) from when install was the only way. Carried as a fact decided where the
	// failure happened, never recovered from the reason's wording.
	NeedsInstall bool `json:"needs_install"`
	// ManifestProblems are the pack's manifest problems when THAT is why it is unresolvable
	// (manifestProblemsError): the tree is there and loads, and the fix is an edit IN THE PACK,
	// never a fetch — to its manifest, or to a file whose presence is a problem (packload's
	// reservedBriefingFiles). Each is stated without LoadDir's "pack <name>: " prefix, since
	// the record already carries the name. Empty for every other failure, so it doubles as
	// the class a report groups the remedy by.
	ManifestProblems []string `json:"manifest_problems,omitempty"`
	// Implicit is config.PackEntry.Implicit: the conventional local pack, which no `packs` list
	// names, so a remedy may not offer "remove it from `packs`" for it. Not on the wire, as on
	// the entry.
	Implicit bool `json:"-"`
}

// newUnresolvedPack records a resolution failure from resolveConfiguredPack for entry e. The
// resolver's "packs: <name>: " prefix is dropped from the reason, because every report prints
// the name beside it.
func newUnresolvedPack(e config.PackEntry, err error) unresolvedPack {
	var miss storeMissError
	var malformed manifestProblemsError
	name := e.Name
	u := unresolvedPack{Name: name, Reason: strings.TrimPrefix(err.Error(), "packs: "+name+": "),
		NeedsInstall: errors.As(err, &miss), Implicit: e.Implicit}
	if errors.As(err, &malformed) {
		u.ManifestProblems = append([]string(nil), malformed.problems...)
	}
	return u
}

// manifestProblemsError is a configured pack whose manifest HAS PROBLEMS — the ones `yolo pack
// lint`, `yolo check` and every launch refuse it over (run's stagePacks returns the first as
// the launch's error). The pack still LOADS: packload.LoadDir returns it beside its problems,
// with whatever part of the manifest decoded. Reading that part is the defect this type ends:
// `yolo host apply --assert` wrote a pack with two `autonomy` contributions into a real home at
// rc=0 (notch-scoped-config-contributions.md NS-D14). problems carry no "pack <name>: " prefix.
type manifestProblemsError struct {
	name     string
	problems []string
}

func (e manifestProblemsError) Error() string {
	return fmt.Sprintf("packs: %s: manifest %s: %s", e.name,
		plural(len(e.problems), "problem", "problems"), strings.Join(e.problems, "; "))
}

// storeMissError marks a git pack the pack store cannot supply YET — never fetched, or a ref
// the mirror lacks that no successful fetch has come back without (packsrc.ErrNotFetched). A
// fetch is the remedy for exactly this class and no other: the next host launch's pack
// refresh step fetches such a pack, and `yolo pack install` re-fetches every configured git
// pack on demand. A subpath absent at the resolved commit, or a ref a successful fetch did
// not find, is NOT this class — no fetch repairs either, so they are reported as an address
// to fix in the config. This resolver itself never fetches.
type storeMissError struct{ err error }

func (e storeMissError) Error() string { return e.err.Error() }
func (e storeMissError) Unwrap() error { return e.err }

// unresolvedNames is the names alone, for the reports that list them.
func unresolvedNames(list []unresolvedPack) []string {
	out := make([]string, 0, len(list))
	for _, u := range list {
		out = append(out, u.Name)
	}
	return out
}

// describeUnresolved is the one-line human form: every pack with its reason.
func describeUnresolved(list []unresolvedPack) string {
	parts := make([]string, 0, len(list))
	for _, u := range list {
		parts = append(parts, u.Name+" ("+u.Reason+")")
	}
	return strings.Join(parts, "; ")
}

// resolveConfiguredPack loads one configured pack from wherever a LAUNCH would find it, staged
// the way a launch stages it, or says why it cannot.
//
// ONE RESOLVER, THE LAUNCH'S: config.ResolvePack, which the launch's staging calls too
// (docs/plans/notch-convergence.md item 5). A git pack resolves OFFLINE from the pack store (a
// launch's pack refresh step, or `yolo pack install`, is what puts it there; this function never
// fetches), a local one from its path, an embedded one from the build's one materialization, with
// the same staged-tree fallback a nested launch relies on.
//
// A FILTERED PACK IS STAGED, into this process's pack tree (config.ResolvePackForProcess), and the
// returned Pack.Root is that copy. The host notch used to read the declaration from a filtered copy
// and then point Root back at the source, so an entry's `exclude` removed a skill from every jail
// and still delivered it to the real home (rows B3 and B6). Now every file a host verb reads —
// skills, briefings, `files`, plugins — is one the entry's filters kept. An UNFILTERED pack is read
// in place, after packstage.Check, since a copy would hold the same files. A FETCHED pack's
// escaping symlink refuses it exactly as it fails the launch, and a LOCAL pack's symlinks are
// followed, filtered or not, exactly as the launch follows them (OQ-NC9; the resolver decides by
// the pack's origin, config.followsSymlinks). A copy lives until the process
// releases its packs (packload.ReleaseEmbedded); a message naming a file in it names the source
// instead (packload.Pack.SourcePath).
//
// A MANIFEST WITH PROBLEMS MAKES THE PACK UNRESOLVABLE (manifestProblemsError), as it fails the
// launch, and the problems are the FILTERED tree's (NS-D15): a file the entry excludes is no
// problem, and a manifest it filters out is not read. They used to be discarded here whenever the
// pack still loaded, so every host verb read whatever part of a malformed manifest decoded, and
// `yolo host apply --assert` applied it — partially, at rc=0 — while `yolo pack lint`, `yolo check`
// and the launch refused the same pack (NS-D14). Every host verb reads it through the one
// selection function (selectHostPacks), and each keeps its own disposition for an unresolvable
// pack, which is where the per-verb decision lives: `yolo host --` and `yolo host env` refuse, as
// a jail launch does (NC-D5, which overrides NS-D14's "compose without it and warn" for them),
// `host apply --assert` refuses the whole set and writes nothing (its dry run and the launch gate
// say so), `--revert` leaves its keys recorded, capture does not search it, check-deps exits 1,
// the read-only `config` verbs report it as not folded, and `config promote` refuses to write
// into it as a destination. None reads a malformed manifest.
// packload's host-notch containment guards stay, for a manifest no decoder checked.
func resolveConfiguredPack(e config.PackEntry) (*packload.Pack, error) {
	res, err := config.ResolvePackForProcess(e, hostPackResolveSpec(false))
	if err != nil {
		if errors.Is(err, packsrc.ErrNotFetched) && !e.IsLocal() && !e.Embedded() {
			return nil, storeMissError{err}
		}
		return nil, err
	}
	return resolvedOrProblems(e, res)
}

// hostPackResolveSpec is how every host verb asks the one resolver for a pack: writing nothing
// into the pack store when readOnly. One constructor so the footer's declaration read and the
// verbs' reads cannot disagree about which packs resolve. Whether a pack's symlinks are followed
// is not the caller's to say: the resolver follows a local pack's and refuses a fetched pack's
// escaping one, at every notch (OQ-NC9).
func hostPackResolveSpec(readOnly bool) config.ResolvePackSpec {
	return config.ResolvePackSpec{ReadOnlyStore: readOnly}
}

// resolvedOrProblems is a resolution's pack, or the error its manifest problems make it: an
// error naming them when LoadDir loaded nothing, a manifestProblemsError when it loaded a pack
// beside problems. Shared by the staging resolver above and the footer's declaration read, so
// the two agree about which packs a host launch composes.
func resolvedOrProblems(e config.PackEntry, res config.ResolvedPack) (*packload.Pack, error) {
	if res.Pack == nil {
		return nil, fmt.Errorf("packs: %s: %s", e.Name, strings.Join(res.Problems, "; "))
	}
	if len(res.Problems) > 0 {
		stated := make([]string, len(res.Problems))
		for i, prob := range res.Problems {
			stated[i] = strings.TrimPrefix(prob, "pack "+e.Name+": ")
		}
		return nil, manifestProblemsError{name: e.Name, problems: stated}
	}
	return res.Pack, nil
}

// packForCheckDeps is resolveConfiguredPack for a caller that has nothing to say about a pack
// it cannot resolve — today only test helpers. Every production caller takes the error, because
// a pack skipped in silence is the half state `yolo host apply` refuses.
func packForCheckDeps(e config.PackEntry) *packload.Pack {
	p, _ := resolveConfiguredPack(e)
	return p
}

const checkDepsUsage = `yolo check-deps — probe the host for binaries the configured packs need

Below the jail notch yolo bakes no image, so a pack's tools become a question about the
host. This probes for each declared binary and, for the missing ones, prints the install
command for your package manager and writes a bundle manifest (~/.config/yolo/Brewfile
and kin) you can run in one step.

  yolo check-deps               probe + write the manifest for missing deps
  yolo check-deps --no-manifest probe only, write nothing

Packs resolve the way a launch resolves them: a git pack from the pack store, a
local one from its path. check-deps never fetches: a git pack not in the store yet
is fetched by the next launch, or now by ` + "`yolo pack install`" + `. A configured pack
that cannot be resolved, or whose manifest has problems, is named with the reason, and
its deps are not probed.

It never installs anything — it detects and hands off. Exit is non-zero when a declared
dep is missing, or when a configured pack could not be resolved.

Examples:
  yolo check-deps                     # what is missing, and write the bundle manifest
  yolo check-deps --no-manifest       # just tell me, write nothing`
