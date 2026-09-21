package basehome

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hookSharedCredentials is the hook name whose declarations name a credential file.
//
// SPELLED, not imported: the constant lives in internal/entrypoint (packhooks.go) and this
// package is on the host launch path. TestCredentialHookNameIsAKnownHook pins the spelling
// against packdecl.KnownHooks, which is the same list entrypoint's own constants are
// pinned against.
//
// ⚠ THE NAME FILTER IS LOAD-BEARING. HookContributions returns BOTH hook kinds in one
// slice, and the other one — per_jail_history — declares `.claude/history.jsonl`, the
// single purest runtime artifact in the base home. Drop the filter and the migration
// PRESERVES it while reporting success. There is no compile error and no symptom.
const hookSharedCredentials = "shared_credentials"

// shadowedHomeDirs are the cache-ish home subtrees a jail shadows per workspace.
//
// They are NOT provisioned by EnsureGlobalStorage (a tool creates them on first use), so
// paths.BaseHomeCoreDirs does not carry them and the unknown-top-level sweep would
// otherwise propose archiving the npm cache — which §8 rules out twice ("not a disk
// reclaimer", "not PruneShadowedHome").
//
// The authority is prune.ShadowedHomePaths, and it is spelled here rather than imported so
// a leaf classifier on the launch path does not pull in the prune package's image and
// container-builder dependencies. TestEveryShadowedHomeDirIsExcluded (an external test
// package, so the edge is test-only) fails if the two lists drift.
var shadowedHomeDirs = []string{".cache", ".npm", ".npm-global", ".local", "go"}

// ShippedDecls is the declaration set of the packs yolo SHIPS — the embedded set, not the
// selected one, because a state dir's legacy bytes do not stop existing when its pack is
// deselected (§5.1 bullet 1 says the union on purpose).
func ShippedDecls() Decls { return DeclsFromPacks(packload.Embedded()) }

// DeclsFromPacks derives the classification inputs from loaded packs. This is the ONE
// place that reads real manifests; everything else in this package takes Decls as data.
//
// Every read here is a projection of packdecl.Contributions, and each has a trap the
// design or the recon measured:
//
//   - the hook loop must filter on the hook NAME (see hookSharedCredentials);
//   - the destination loop must read the resolved pack's own contributions, NOT
//     Destinations.Inferred — over the shipped set Inferred is EMPTY (every agent pack
//     declares its own `into`), so a classifier written against it classifies all 15
//     shipped destinations as RUNTIME and archives `.claude/CLAUDE.md` and `.claude/skills`;
//   - Surfaces() is the AUTONOMOUS posture (SurfacesFor(true)). Harmless here and stated
//     so the next reader does not copy surfaceManifest's posture bug: the autonomy fold
//     patches a surface's Managed layer and never its Path, and Path is all we read.
func DeclsFromPacks(packs []*packload.Pack) Decls {
	d := Decls{
		Redirects:            redirects(),
		NonPackDirs:          nonPackDirs(),
		SweepUnknownTopLevel: true,
	}
	d.StateDirs = packload.WritableDirs(packs)
	d.SharedDirs = packload.SharedDirs(packs)

	// ResolveDestinations is called for the destinations a pack did NOT declare: a local
	// or zero-ceremony pack inherits an agent pack's. Over the shipped set it infers
	// nothing, which is why its result is read through the returned PACKS.
	resolved, _ := packload.ResolveDestinations(packs)
	for _, p := range resolved {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, h := range p.Decl.HookContributions() {
			if h.Name != hookSharedCredentials || h.File == "" {
				continue
			}
			if rel, ok := HomeRel(h.File); ok {
				d.CredentialFiles = append(d.CredentialFiles, rel)
			}
		}
		for _, c := range p.Decl.Contributions() {
			switch c.Kind {
			case packdecl.KindSkills, packdecl.KindBriefing, packdecl.KindFiles:
				if c.Into == "" {
					continue
				}
				if rel, ok := HomeRel(c.Into); ok {
					d.ContentDests = append(d.ContentDests, rel)
				}
			}
		}
		surfaces, problems := p.Surfaces()
		if len(problems) > 0 {
			// A pack whose surfaces will not decode has no CONFIG paths, so its config
			// files would classify RUNTIME. That is a misclassification the disclosure has
			// to carry rather than a detail to swallow.
			d.Problems = append(d.Problems,
				"pack "+p.Name+" declares config surfaces that could not be read, so its config files are not recognised")
			continue
		}
		for _, s := range surfaces {
			if rel, ok := HomeRel(s.Path); ok {
				d.ConfigSurfaces = append(d.ConfigSurfaces, rel)
			}
		}
	}

	d.CredentialFiles = dedupe(d.CredentialFiles)
	d.ConfigSurfaces = dedupe(d.ConfigSurfaces)
	d.ContentDests = dedupe(d.ContentDests)

	if len(d.StateDirs) == 0 {
		// packload.Embedded returns an EMPTY set on any materialization problem, which for
		// a reservation list is conservative and for a walk is zero roots and zero
		// candidates — a silent no-op that looks exactly like a clean host. It is said out
		// loud instead.
		d.Problems = append(d.Problems, "no pack state dirs are declared, so there is nothing to walk")
	}
	return d
}

// redirects converts paths.HomeFileRedirects into this package's plain-data input. Only
// one of the three matters to the walk — `.claude.json` → `.claude/claude.json`, the file
// holding `oauthAccount` — and it is the whole reason §5.2 step 3 has a redirect clause.
// The other two target `.config`, which is core's and never a walk root.
func redirects() []Redirect {
	var out []Redirect
	for _, r := range paths.HomeFileRedirects() {
		out = append(out, Redirect{Name: r.Name, Target: r.Target})
	}
	return out
}

func nonPackDirs() []string {
	out := append([]string(nil), paths.BaseHomeCoreDirs()...)
	out = append(out, shadowedHomeDirs...)
	for _, r := range paths.HomeFileRedirects() {
		// A redirect's NAME is a home-root file yolo created; its TARGET names the dir that
		// holds the bytes. Both are core's.
		out = append(out, r.Name, r.Target)
	}
	return dedupe(out)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
