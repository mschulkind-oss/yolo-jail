// Package packload discovers packs and turns their declarations into the things core
// acts on: mounts, writable dirs, host-file grants, surfaces, launch flags.
//
// It is the piece that was missing. The manifest schema (internal/packdecl), the
// surface decoder (internal/agentcfg/manifest.DecodeSurfaces) and the tree stager
// (internal/packstage) all existed with no production caller between them — a
// capability nobody used. This connects them.
//
// ONE DISCOVERY PATH for embedded and configured packs, deliberately: a user pack and
// an official pack must be the same kind of thing, or "official packs are structurally
// identical" is marketing. Since OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) ORIGIN
// decides NOTHING here: it names the delivery route — a fetched pack must be `yolo pack
// install`ed to reach the store — and a pack's host-crossing declarations are honored the
// same whoever shipped it, because selecting a pack means writing `packs` in the user
// config as the host user, which already exceeds anything a gate here could withhold.
package packload

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// Pack is a discovered pack: its declaration plus where its files are.
type Pack struct {
	// Name is the pack's effective name, and it comes from the CALLER — never from
	// pack.json. For a configured pack it is config.PackEntry.Name (the entry's explicit
	// `name`, else the last segment of its source address); for an embedded one it is the
	// directory under packs/; in the jail it is the staged directory's name.
	//
	// THIS FIELD SAID "config override, else manifest, else dir" UNTIL 2026-09-05, and
	// that was wrong in a way a pack author could act on. LoadDir does have that ladder,
	// but no production caller reaches past its first rung: every one of them passes a
	// name, and config lowering fills one in from the address before anything is staged
	// (config.TestEveryLoweredPackEntryCarriesAName). A `file://` entry with no `name`
	// therefore reports its source DIRECTORY even when its pack.json declares one — which
	// made an audience refusal read `pack 002` and is the bug report this comment closes.
	//
	// It is that way because the name must be two things at once, and only an
	// address-derived string can be both:
	//
	//   - THE STAGING DIR'S SOURCE. config.PackEntry.Slug escapes it into the tree the CLI
	//     stages to and the pack-drop prune sweeps (packstage rule 3), both of which run
	//     from the config list alone — before any pack.json exists to read, and for a git
	//     source before anything is fetched.
	//   - THE HANDLE THE USER TYPES. `yolo pack ls` prints it and `yolo pack explain`
	//     matches on it, so it has to be the string in their config, not one hidden in a
	//     pack they may not have opened.
	//
	// IT IS NOT THE /ctx MOUNT KEY, and it was until 2026-09-05. A `reads-host` grant with
	// no `into` lands under the pack's STAGED DIRECTORY (StagedSlug), which is Slug's
	// ESCAPING of this name and is the only string the jail can name a pack by — the two
	// are equal exactly when the name was already slug-clean, which every shipped pack is
	// and a user pack need not be. That third bullet used to be written here as if it were
	// a property of the name; the mount is keyed on the directory now, and both halves
	// evaluate the same expression over it.
	//
	// So a pack.json `name` is informational: accepted (the strict decoder would refuse
	// the key otherwise), shown by nothing, and for the packs yolo ships pinned equal to
	// the directory name so it cannot drift into a second spelling
	// (TestEmbeddedPackManifestNamesMatchTheirDirs).
	Name string
	// Root is the directory its files live in. For an embedded pack this is the
	// materialized copy, so every consumer sees a real path either way.
	Root string
	// SourceRoot is the directory Root was STAGED FROM, when Root is a staged copy
	// (config.ResolvePack), and "" when the pack was loaded in place. Read by messages alone
	// (SourcePath): a refusal a user fixes by editing a file must name the file they edit, not
	// the throwaway copy the verb read.
	SourceRoot string
	// Decl is the parsed manifest. Never nil — a pack with no pack.json gets an empty
	// one, because a skills-only pack must stay zero-ceremony.
	Decl *packdecl.Manifest
	// SkewNotes are the version-skew reports from a TOLERANT manifest read
	// (TolerateSkew): one line per contribution skipped because this build does not
	// know its kind, each naming the pack and the kind
	// (docs/reference/loophole-system.md#strict-and-tolerant-and-why-both).
	// NOT problems — a problem fails the boot (A12), and surviving exactly that is
	// why the skip exists — but never silent either: the boot path reports each one,
	// so a degraded jail (a contribution the baked entrypoint cannot render) is
	// visible. Always empty on the strict authoring path, where the same manifest is
	// refused as a load problem instead.
	SkewNotes []string
	// Official reports that this pack is one yolo ships: loaded from the embedded set
	// (loadEmbeddedPack), or resolved from a `packs` entry that names an embedded pack
	// (config.ResolvePack, which also stages such a pack and keeps the mark on the copy). A
	// fetched or local pack is never official, whatever its name. With Local it is the one
	// place a pack's ORIGIN, rather than its declaration, decides anything: whether a launch
	// runs the pack's host code as its own child (MayRunHostHalf).
	Official bool
	// Local reports that this pack's content is at a path on this machine: resolved from a
	// file:// `packs` entry, the conventional local pack included (config.ResolvePack). Only
	// the user's own config can select one, so a local pack's host half and doorway run at
	// `yolo host` and on macos-user as an official pack's do, and a fetched pack's do not
	// (docs/design/host-notch-services.md HS-D27, OQ-HS4).
	Local bool

	// origDecl is the declaration this pack was CLONED FROM by ResolveDestinations, or nil for a
	// pack that is not a clone. The clone's Decl appends a synthesized `{into, from}` copy of each
	// borrower, and per-file governance must never be computed from that list: every explicit
	// `from` would have two governors, and the implicit borrower would read as an omitted-`from`
	// declaration that switches the implicit broadcast off (governance.go's header). Unexported
	// because nothing outside governance needs to know a pack was resolved.
	origDecl *packdecl.Manifest
}

// MayRunHostHalf reports whether a launch runs this pack's HOST CODE as its own child, outside
// every sandbox: a service's host half (`host_daemon`) and a loophole doorway's host argv
// (`jail_daemon.host_cmd`). True for a pack yolo ships (Official) and for one at a path on this
// machine (Local), which only the user's own config can select; false for a FETCHED pack, whose
// host argv no launch runs until the maintainer rules on third-party host code
// (docs/design/host-notch-services.md OQ-HS4, HS-D27). The one predicate every reader of that
// question asks: internal/launchservice's admission, and the footprint's host-execution claim
// (moduleClaims), so the footprint of a pack resolved as a launch resolves it (config.ResolvePack)
// never claims execution the launch refuses, nor hides one it runs. `yolo pack footprint <path>`
// loads its argument without that resolver, so nothing sets Local on it, and its footprint omits a
// local doorway's host argv (HS-D27 says what is left to change).
func (p *Pack) MayRunHostHalf() bool {
	return p != nil && (p.Official || p.Local)
}

// Surfaces decodes the pack's surface declarations, resolving each one's host layer to
// the /ctx path the host CLI mounts it at.
//
// The RESOLUTION is here rather than in the manifest schema because it is a fact about
// how the two sides agree, not about the surface: a surface says "I am also a file in the
// user's own home" (`readsHost`), the CLI mounts that file under /ctx, and both sides
// derive the same path from the same declaration. Keeping the derivation in ONE function
// is what makes that agreement checkable — a second copy would be a silent-empty-host-layer
// bug waiting to happen, and the symptom (a config file missing the user's own settings)
// looks nothing like a path mismatch.
//
// A SURFACE'S HOST LAYER IS ITS OWN DECLARATION, not a match against something else. It was
// a `reads-host` contribution bound to the surface by BASENAME until 2026-09-12, which is
// the un-binding OQ-CO10 removed: see manifest.Surface.ReadsHost for what the field says and
// SurfaceHostFile for the derivation both halves run.
func (p *Pack) Surfaces() ([]manifest.Surface, []string) {
	// The jail/guest default is autonomy ON — so the boot path (which calls Surfaces)
	// renders the autonomous posture, keeping boot output byte-identical after packs
	// move their bypass keys into the autonomy kind. The host path calls SurfacesFor(false).
	return p.SurfacesFor(true)
}

// SurfacesFor is Surfaces with the §4.2 autonomy policy applied: it decodes the pack's
// config surfaces, then folds the selected autonomy posture's config-managed keys into
// the matching surface's Managed layer (deep-merged, posture wins). autonomy=true selects
// the autonomous posture, false the guarded one. A pack with no autonomy contribution, or
// whose selected posture is empty, gets its surfaces unchanged.
//
// A profile contributes NO surface here, and that is the OQ-PT8 shrink rather than an
// omission: the variant patch this fold used to take for a selected profile moved to
// `config-overlay` contributions with `profile` set, which compose where every other
// overlay does (packoverlay.Collect). The fold's profile half was the one place a
// profile touched a surface, and it was unreachable for any pack that installs no CLI —
// the defect the modifier form does not have.
//
// A posture patch naming a surface this pack does NOT declare is not this fold's: it is a
// POSTURE OVERLAY, which packoverlay.Collect places on the owner's surface as a config-overlay
// or reports as an orphan (OQ-3, docs/design/notch-scoped-config-contributions.md NS-D19).
func (p *Pack) SurfacesFor(autonomy bool) ([]manifest.Surface, []string) {
	surfaces, problems, _ := p.SurfacesForReport(autonomy)
	return surfaces, problems
}

// SurfacesForReport is SurfacesFor with a third result that is always empty since OQ-3.
//
// It carried the fold's dead patches as notes: a posture patch naming no surface of this pack
// merged into nothing, and the note said so (the OQ-Z5 shape, where a patch moved into a pack
// owning no such surface read, to its author, exactly like one that folded). Since the ruling
// such a patch is a posture overlay — packoverlay.Collect places it on the surface another
// pack owns, or reports it as an orphan (R2) at a notch that selects its posture — so there is
// nothing left for this fold to note, and a second report beside the collector's would be two
// answers to one question.
//
// THE SIGNATURE STAYS, and that is a contract rather than inertia: packs/releasedecode_test.go
// compiles against it inside the last release's tree (TestReleaseDecodeProbeAPIIsStable).
func (p *Pack) SurfacesForReport(autonomy bool) ([]manifest.Surface, []string, []FoldNote) {
	rawSurfaces := p.Decl.SurfaceContributions()
	if len(rawSurfaces) == 0 {
		return nil, nil, nil
	}
	surfaces, problems := manifest.DecodeSurfaces(rawSurfaces)
	for i, prob := range problems {
		problems[i] = "pack " + p.Name + ": " + prob
	}
	// The /ctx path each surface's own `readsHost` declaration lands at. DERIVED FROM THE
	// SURFACE, so a surface either declares a host layer and gets one, or does neither;
	// there is no third state where the declaration is present and the binding is not.
	//
	// TWO SURFACES OF ONE PACK MAY NOT DERIVE THE SAME /ctx PATH, and that check is here
	// because the destination is still keyed on the file's BASENAME (packload.CtxPath):
	// `~/.a/settings.json` and `~/.b/settings.json` in one pack would land on one mount,
	// and the second would compose the first's bytes — a wrong config that looks right.
	// Under the retired basename BINDING this was unrepresentable-and-wrong in a different
	// way (both surfaces bound to whichever grant matched first, silently); it is a
	// refusal now. Nothing shipped is anywhere near it, and a pack that gets here has
	// asked two surfaces to read one host file.
	// A `whenListed` condition reads a list another surface of THIS pack renders, from that
	// surface's file as this render left it, so the named surface has to render first: it
	// must be declared earlier in the same pack. A later one would be read as the PREVIOUS
	// boot left it, one boot stale; another pack's has no order relative to this one at all.
	declared := map[manifest.SurfaceKey]bool{}
	for _, s := range surfaces {
		if c := s.WhenListed; c != nil && !declared[c.Key()] {
			problems = append(problems, fmt.Sprintf("pack %s: surface %s: \"whenListed\" names "+
				"%s, which is not a surface this pack declares before it — the list is read "+
				"from that surface's file as this render leaves it, so it must render first",
				p.Name, s.Key(), c.Surface))
		}
		declared[s.Key()] = true
	}
	claimed := map[string]manifest.SurfaceKey{}
	for i := range surfaces {
		surfaces[i].HostSource = p.surfaceHostSource(surfaces[i])
		if surfaces[i].HostSource == "" {
			continue
		}
		if first, taken := claimed[surfaces[i].HostSource]; taken {
			problems = append(problems, fmt.Sprintf("pack %s: surfaces %s and %s both read "+
				"the host home at %s — a host layer's /ctx destination is keyed on the file's "+
				"basename, so two surfaces whose paths share one cannot both declare "+
				"\"readsHost\"", p.Name, first, surfaces[i].Key(), surfaces[i].HostSource))
			continue
		}
		claimed[surfaces[i].HostSource] = surfaces[i].Key()
	}
	// Fold the selected autonomy posture's config patch into the matching surfaces. A patch
	// naming no surface of this pack matches nothing here and is left to packoverlay.Collect,
	// whose posture overlay it is.
	if posture := p.Decl.PostureFor(autonomy); posture != nil && len(posture.Config) > 0 {
		patches, probs := manifest.DecodeSurfaces(posture.Config)
		for _, prob := range probs {
			problems = append(problems, "pack "+p.Name+" (autonomy): "+prob)
		}
		surfaces = foldPostureManaged(surfaces, patches)
	}
	return surfaces, problems, nil
}

// PosturePatchesOwnSurface reports whether the posture autonomy selects carries a config patch
// naming a surface THIS pack declares — the half of a posture's `config` that folds into the
// pack's own managed layer, and so always lands in a surface below. A patch naming another
// pack's surface is a posture overlay, and whether it lands is packoverlay.Collect's answer
// (OverlaySet.PlacesPostureConfigFrom), not the manifest's: `yolo host apply`'s notch line
// asks the two questions separately for that reason (surveyNotchFacts, NS-D24).
func (p *Pack) PosturePatchesOwnSurface(autonomy bool) bool {
	posture := p.Decl.PostureFor(autonomy)
	if posture == nil || len(posture.Config) == 0 {
		return false
	}
	own, _ := manifest.DecodeSurfaces(p.Decl.SurfaceContributions())
	patches, _ := manifest.DecodeSurfaces(posture.Config)
	for _, patch := range patches {
		for _, s := range own {
			if s.Key() == patch.Key() {
				return true
			}
		}
	}
	return false
}

// FoldNote was one config patch that merged into nothing: a posture patch naming a surface its
// own pack does not declare. No fold produces one since OQ-3 made that patch a posture overlay
// (SurfacesForReport says why); the type stays because SurfacesForReport's signature is the
// release probe's contract (TestReleaseDecodeProbeAPIIsStable), and its fields are kept so a
// value the last release's tree builds still has a type to be.
type FoldNote struct {
	// Pack is the pack that declared the patch.
	Pack string
	// Source names the declaration the patch rode: "autonomous posture" or "guarded posture".
	Source string
	// Target is the (agent, name) the patch named — the identity nothing matched.
	Target manifest.SurfaceKey
	// Declared lists the surface identities the pack DOES declare.
	Declared []string
}

// ProfileTable lowers a decoded profile table — YOLO_USE_PROFILES in the jail, the
// config's `profile` on the host — into the map the folds below take.
//
// THE one lowering, and not a convenience: a JSON null at a key REMOVES that profile
// (the merge-patch convention the table uses), and a null decoded into map[string]string
// would arrive as "" and read as a selection of an empty name. Dropping the key here is
// what keeps "no profile" and "profile removed" the same fact at every fold.
//
// A LIST VALUE LOWERS TO ITS FIRST ENTRY, the set's PRIMARY (docs/design/active-provider-sets.md
// AP-D1): every fold that predates active sets reads "the selected profile" as the one a fresh
// session starts on, so a derive written before sets sees a one-entry set exactly as today
// (AP-P1). The whole set is ProfileSets'; only a set-capable agent is ever handed one longer
// than one, which is what makes this lowering safe for every other reader.
func ProfileTable(m *jsonx.OrderedMap) map[string]string {
	if m == nil {
		return nil
	}
	var out map[string]string
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		set, ok := ProfileSetValue(v)
		if !ok {
			continue
		}
		name := set[0]
		if name == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = name
	}
	return out
}

// foldPostureManaged deep-merges each patch surface's Managed map into the base surface
// with the same (agent, name), the patch winning per key. This is how an autonomy posture
// asserts its permission keys onto the pack's OWN surface without being a second config
// writer. A patch matching no base surface is skipped: it names another pack's surface, and
// packoverlay.Collect places it there as a posture overlay (or reports it as an orphan).
func foldPostureManaged(base, patches []manifest.Surface) []manifest.Surface {
	for _, patch := range patches {
		pm := patch.ManagedMap()
		if pm == nil {
			continue
		}
		for i := range base {
			if base[i].Agent != patch.Agent || base[i].Name != patch.Name {
				continue
			}
			base[i].Managed = mergeManagedMap(base[i].ManagedMap(), pm)
		}
	}
	return base
}

// mergeManagedMap deep-merges over into base (over wins), returning a new map. A nil base
// yields a copy of over. Object values recurse; scalars and arrays replace wholesale —
// the same managed semantics the render engine's deepMerge uses.
func mergeManagedMap(base, over map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if ov, ok := v.(map[string]any); ok {
			if bv, ok := out[k].(map[string]any); ok {
				out[k] = mergeManagedMap(bv, ov)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// SurfaceHostFile is the host-file grant a config surface's own `readsHost` declaration
// makes: the same path, in the user's real home. THE derivation, in one place, because the
// mount and the read are two halves of one agreement and a second copy of this expression
// is a silently-empty host layer waiting to happen.
//
// `To` is deliberately left empty so CtxPath supplies `/ctx/host-<staged dir>/<basename>`.
// One expression over one string, on both sides of the container wall — see StagedSlug for
// the incident that established that rule.
//
// (false) for a surface that declares nothing, and for one whose path is not `~/`-relative:
// that combination is refused at decode (manifest.SurfaceDTO.Surface), so reaching it means
// a Surface built in Go rather than loaded, and there is no host twin to name.
func SurfaceHostFile(s manifest.Surface) (packdecl.HostFile, bool) {
	if !s.ReadsHost {
		return packdecl.HostFile{}, false
	}
	rel, ok := strings.CutPrefix(s.Path, "~/")
	if !ok {
		return packdecl.HostFile{}, false
	}
	return packdecl.HostFile{From: rel}, true
}

// SurfaceHostFiles is every grant this pack's own config surfaces imply — what
// HonoredHostFiles folds in beside the manifest's `reads-host` contributions.
//
// It decodes the surfaces rather than reading a cached list because that is the only
// statement of the declaration there is: the surfaces are DATA, and the pack's manifest is
// re-read by both halves. SurfacesFor(true) is the posture the boot path renders, and the
// choice cannot matter here — the autonomy fold merges `managed` keys and touches no other
// field — but it is named rather than left implicit so a fold that grows a reach is a
// deliberate change to this line.
func (p *Pack) SurfaceHostFiles() []packdecl.HostFile {
	surfaces, _ := p.Surfaces()
	var out []packdecl.HostFile
	for _, s := range surfaces {
		if hf, ok := SurfaceHostFile(s); ok {
			out = append(out, hf)
		}
	}
	return out
}

// surfaceHostSource is the /ctx path this pack's copy of that grant lands at, or "" when
// the surface declares no host layer.
//
// KEYED ON THE STAGED DIRECTORY, not the name: this is the READ side of the mount the
// CLI's hostFileArgs emits. See StagedSlug.
func (p *Pack) surfaceHostSource(s manifest.Surface) string {
	hf, ok := SurfaceHostFile(s)
	if !ok {
		return ""
	}
	return CtxPath(p.StagedSlug(), hf)
}

// retiredSurfaceHostGrants reports a `reads-host` contribution that names one of this
// pack's OWN config surfaces — the spelling that bound a surface to a grant by basename
// until 2026-09-12 (OQ-CO10). The migration is one field, and the message is the whole of
// it.
//
// STRICT PATH ONLY, which is LoadDir's caller-facing half: this is a version-skew fact, and
// the jail must boot across the version boundary rather than refuse a manifest some other
// build wrote (packdecl.retiredFieldProblems states that rule and the incident behind it).
// So an author and a host launch hear it, and a jail that meets an unconverted pack renders
// the surface without a host layer — which the jail's own read then reports, because the
// surface does not claim one.
func retiredSurfaceHostGrants(decl *packdecl.Manifest) []string {
	surfaces, _ := manifest.DecodeSurfaces(decl.SurfaceContributions())
	own := map[string]manifest.Surface{}
	for _, s := range surfaces {
		if rel, ok := strings.CutPrefix(s.Path, "~/"); ok {
			own[rel] = s
		}
	}
	var problems []string
	for _, hf := range decl.HostFileContributions() {
		s, isOwn := own[hf.From]
		if !isOwn {
			continue
		}
		problems = append(problems, fmt.Sprintf("reads-host %q names this pack's own config "+
			"surface %s/%s — a surface's host layer is declared ON THE SURFACE now: set "+
			"\"readsHost\": true in its `config` body and drop this contribution. The kind "+
			"itself stays, for host files that are not a surface's own twin.",
			hf.From, s.Agent, s.Name))
	}
	return problems
}

// StagedSlug is the name of the DIRECTORY this pack was loaded from, and it is the key
// the two halves mount a `reads-host` grant under (CtxPath).
//
// IT IS NOT Pack.Name, and the difference is the bug this method exists to close. `Name`
// is the user's handle — the string in their `packs` line, what `yolo pack ls` prints and
// `yolo pack explain` matches — and the CLI stages a pack into a directory named by
// config.PackEntry.Slug, which ESCAPES every byte outside [A-Za-z0-9.-] as `_<hex>` so the
// pack and host_files staging namespaces cannot collide. The jail has only that directory
// to name a pack from (entrypoint.LoadJailPacks), so a mount path keyed on `Name` agrees
// with the jail only for a name that was already slug-clean: measured 2026-09-05, a pack
// named `my_pack` had its host file mounted at /ctx/host-my_pack/ while the entrypoint
// read /ctx/host-my_5fpack/, and the host-layer read is fail-open, so the surface composed
// from its defaults in silence while the disclosure banner still printed the grant.
//
// Escaping inside CtxPath instead does NOT work: Slug is not idempotent (`_` is itself
// outside the safe set, so slug("my_5fpack") is "my_5f5fpack"), and the jail's string is
// already a slug. Keying on the staged directory makes BOTH sides evaluate one expression
// over one string, rather than two strings that happen to agree for the names yolo ships.
//
// ONLY MEANINGFUL FOR A PACK LOADED FROM ITS STAGED DIRECTORY. That is every loader on
// the mount path — the host stages configured packs to <staging>/<PackEntry.Slug> and
// embedded ones to _official/<name>, and the jail walks those same directories — but it is
// NOT every loader in the tree, so this is a precondition rather than a type-level
// guarantee. What happens when it is broken: this returns the basename of whatever root
// was loaded, a coherent-looking string that is the wrong mount path, and nothing warns.
//
// Two things keep that from being a live hazard, and both are load-bearing:
//
//   - THE UNSTAGED LOADERS DO NOT REACH A MOUNT. `yolo check-deps` and config's install-bin
//     scan load a local pack straight from its SOURCE dir and read only InstallBins();
//     run/packs.go's loophole-module and supersession scans do the same and read only
//     those declarations. Nothing there consults CtxPath, and the /ctx path a surface
//     carries (Surface.HostSource) is opened by exactly one caller —
//     entrypoint.hostSurfaceBytes, over packs entrypoint.LoadJailPacks read out of
//     YOLO_PACK_ROOT, which are staged by construction.
//   - THE THROWAWAY-STAGING LOADERS STAGE INTO A NAMED DIR. `yolo pack lint`, `yolo pack
//     footprint` and `yolo check` all copy a pack into a temp tree before loading it, and
//     each stages into <temp>/<the pack's directory or slug> rather than into the temp dir
//     itself, precisely so this method does not name `yolo-pack-lint-1234567`. A new
//     throwaway stager must do the same.
//
// TestStagedSlugIsTheDirectoryNotTheName pins the distinction, and
// TestEveryEmbeddedPackStagedSlugEqualsItsName pins that no pack yolo ships is affected by
// keying on it (all of them are slug-clean, so their two strings are equal).
// filepath.Base, not path.Base: Root is a real host filesystem path (LoadDir's caller
// built it with filepath.Join), while CtxPath's other half reads a slash-separated
// manifest string.
func (p *Pack) StagedSlug() string { return filepath.Base(p.Root) }

// SourcePath maps a path under Root to the same path under SourceRoot, for a message naming a
// file the user edits. A path outside Root, or a pack loaded in place, comes back unchanged.
func (p *Pack) SourcePath(path string) string {
	if p == nil || p.SourceRoot == "" {
		return path
	}
	rel, err := filepath.Rel(p.Root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(p.SourceRoot, rel)
}

// CtxPath is the in-jail /ctx path a granted host file is mounted at. THE one definition
// both sides use: the CLI emits this mount destination, the entrypoint reads the host
// layer from it.
//
// The `pack` argument is the pack's STAGED DIRECTORY (Pack.StagedSlug), not its name. See
// that method for why the two differ and which bug the difference caused.
func CtxPath(pack string, hf packdecl.HostFile) string {
	if hf.To != "" {
		return CtxRoot + "/" + hf.To
	}
	return CtxRoot + "/host-" + pack + "/" + path.Base(hf.From)
}

// CtxRoot is where host-file mounts land in the jail.
const CtxRoot = "/ctx"

// MountCtxPath is the in-jail path a pack `mount` grant (an entry of HonoredMounts) lands at:
// /ctx/<into>. THE one spelling, for the three readers that must agree on it — the argv
// and briefing decider (run.packCtxMounts), the macos-user refusal naming the grant, and the
// `mounts` duplicate-destination check (config.validateMountDestinations) — so a grant is
// never bound at one path, briefed at another and collision-checked at a third.
func MountCtxPath(mt packdecl.HostFile) string {
	return CtxRoot + "/" + strings.TrimPrefix(mt.To, "/")
}

// HostFileConflicts reports grants that would collide at the same /ctx destination.
//
// Two grants landing on one path would mean one silently shadows the other, and the
// surface reading that path would compose the wrong user file into its output — a wrong
// config that looks right. Reported so it fails at load instead.
//
// KEYED ON Name, deliberately, and it is the one CtxPath call site that is: this is
// REPORTING, not a mount. Both sides of its own comparison use the same key, so the
// collisions it finds are identical whichever string is passed — CtxPath is injective in
// the grant for a fixed pack — and what the choice decides is only what the message
// PRINTS. `Name` is the right thing to print (it is the handle in the user's config), and
// it is the only safe thing to print here: this check is lint-shaped, so its natural
// caller is `yolo pack lint`, which loads a pack out of a throwaway staging dir. The
// staged slug there would name the temp dir, and the message would tell the author their
// files collide at a path that exists nowhere.
func (p *Pack) HostFileConflicts() []string {
	granted, _ := p.HonoredHostFiles()
	seen := map[string]string{}
	var problems []string
	for _, hf := range granted {
		dest := CtxPath(p.Name, hf)
		if prev, dup := seen[dest]; dup {
			problems = append(problems, fmt.Sprintf(
				"pack %s: host files %q and %q both mount at %s — one would silently "+
					"shadow the other; set a distinct \"to\" on one of them",
				p.Name, prev, hf.From, dest))
			continue
		}
		seen[dest] = hf.From
	}
	return problems
}

// HonoredHostFiles returns this pack's host-file grants. Nothing is refused.
//
// THE ORIGIN REFUSAL IS GONE (OQ-TP9, docs/design/trust-paths.md, 2026-09-04). A fetched
// pack's grants used to be withheld with a printed notice; the gate refused an actor who
// had already passed a stronger one, since naming the pack at all means writing `packs` in
// the user config as the host user. What replaced it is DISCLOSURE, not consent: the launch
// banner (FootprintOf, run.notePackHostAccess) lists every host file that crosses.
//
// THE `refused` RETURN IS RETAINED AND IS ALWAYS NIL, across the whole Honored* family
// (HonoredHostFiles, HonoredMounts, HonoredInstalls, HonoredLoopholes, HonoredPlugins).
// Most callers discard it (`granted, _ :=`), so collapsing the shape is a mechanical
// follow-up rather than part of the ruling. The one caller that still READS it —
// cli/capturehost.go's checkDeps target lookup, which folds `refused` into its "no selected
// pack installs %q" error — therefore folds a slice that is always empty; run/packrefusal.go,
// the consumer the gate had, was deleted with it.
//
// A future refusal source must not quietly refill these: it needs its own design ruling.
// Two tests go red if one appears without it — TestNoFetchedPackHostAccessGateExists (this
// package, hostaccessgates_test.go) scans production code for the retired gate identifiers,
// and TestFetchedPackHostClaimsAreHonoredWithNoApproval (internal/cli/run) pins the
// behaviour end to end.
// EVERY host file this pack reads, from BOTH declarations that can ask for one: its
// `reads-host` contributions, and the `readsHost` field of its own config surfaces
// (OQ-CO10, 2026-09-12). ONE accessor rather than two, because every consumer wants the
// same thing — what this pack asks to read off the host — and a second accessor is a call
// site that will be written against the wrong half. Two consumers ship: the container
// path's `:ro` mount argv (run.hostFileArgs) and the macos-user COPY that puts the same
// bytes at the same /ctx destinations with no mount at all (run.buildMacosCtxTree).
//
// ⚠ THIS USED TO NAME TWO MORE — "the macos-user deficiency notice" and "the briefing that
// names what did not cross" — and DP-L1 removed both on 2026-09-13 rather than renaming
// them. Each existed only to say the bytes could not cross on that backend; the copy above
// is what they were replaced by. The footprint is the one exception, and only
// because it reports per DECLARATION (FootprintOf walks the contributions and the surfaces
// separately, and emits the same reads-host claim from either).
func (p *Pack) HonoredHostFiles() (granted []packdecl.HostFile, refused []string) {
	return append(p.Decl.HostFileContributions(), p.SurfaceHostFiles()...), nil
}

// HonoredMounts returns this pack's mount contributions. Nothing is refused — a mount reads
// the host home exactly like a host file, and OQ-TP9 retired that gate for both. See
// HonoredHostFiles.
func (p *Pack) HonoredMounts() (granted []packdecl.HostFile, refused []string) {
	return p.Decl.HostMountContributions(), nil
}

// EnvFoldEntry is one operation of the pack env fold, in the order it applies. Always an
// assignment today: both maps the fold reads are `vars` maps of plain strings, the only
// removal spelling (the profile body's null) having died with that body — so the host
// notch's removals come from env_sources alone.
type EnvFoldEntry struct {
	Key   string
	Value string
	// ServedBy is the jail daemon the entry's `env` contribution points at (`served_by`), ""
	// when it points at none. The credential gate withholds an entry whose daemon is not
	// served at the notch it composes for (ScopeInput.Served).
	ServedBy string
	// RegionProfileSetting is the entry's contribution's `region_profile_setting`: the setting
	// of the ServedBy loophole naming the profile the credential it points at comes from, which
	// picks the section of the platform's region file an agent reached by it reads
	// (regionfill.go). "" when the contribution names none.
	RegionProfileSetting string
	// Pack is the pack whose contribution the entry is, so a reader of the fold can say which
	// pack declared a value: the host's OpenAI prelaunch keys its managed home on it.
	Pack string
}

// PointersAt is the variables of fold that point at daemon (EnvFoldEntry.ServedBy), sorted and
// each once: what a launch-owned service's start line names as the way an agent reaches it.
func PointersAt(fold []EnvFoldEntry, daemon string) []string {
	var out []string
	for _, e := range fold {
		if daemon != "" && e.ServedBy == daemon && !slices.Contains(out, e.Key) {
			out = append(out, e.Key)
		}
	}
	sort.Strings(out)
	return out
}

// GateSelection is what a contribution's GATE asks about a launch's selection, per agent (CLI
// name): the profile it selected, which a `profile` gate matches by name, and the platform of
// the provider that profile resolves to, which a `platform` gate matches (OQ-BR8,
// docs/design/providers-and-profiles-redesign.md, ruled 2026-09-29: a provider fact keys on the
// provider, never on the profile's name). The zero value selects nothing, so no gate fires.
//
// ONE VALUE FOR EVERY GATE READER: the credential gate (ScopeCredentials) builds it from its own
// inputs and hands it out (CredentialScope.Selection), so the env-override pre-flight and the
// fold every vehicle delivers read one answer to "which gates fire for whom".
type GateSelection struct {
	// Profiles is the CLI-keyed effective selection (ProfileTable): each agent's PRIMARY.
	Profiles map[string]string
	// Platforms maps each agent with a selected profile to the `platform` its provider's
	// composed entry declares; an agent whose provider declares none has no entry.
	Platforms map[string]string
	// Sets is each agent's whole active set (ProfileSets), when the caller composed one: a gate
	// is satisfied by ANY entry of the set (docs/design/active-provider-sets.md AP-P1, "each
	// provider in the set"), so a `platform` gate fires for pi on [zai, bedrock] as it would for
	// pi on bedrock alone. Nil reads each agent's set as its one Profiles entry.
	Sets map[string][]string
	// SetPlatforms is, index for index with Sets, the platform of each entry's provider, ""
	// for one that declares none.
	SetPlatforms map[string][]string
}

// SelectionOfSets is SelectionOf over a whole active-set table: Profiles and Platforms answer
// for each agent's primary, and Sets and SetPlatforms carry every entry, so a gate reads the
// set. A table of one-entry sets builds exactly SelectionOf's answer plus the two set fields.
func SelectionOfSets(sets map[string][]string, resolved map[string]ResolvedProfile,
	providers *jsonx.OrderedMap) GateSelection {
	primary := map[string]string{}
	for agent, set := range sets {
		if len(set) > 0 && set[0] != "" {
			primary[agent] = set[0]
		}
	}
	sel := SelectionOf(primary, resolved, providers)
	for agent, set := range sets {
		if len(set) == 0 {
			continue
		}
		if sel.Sets == nil {
			sel.Sets, sel.SetPlatforms = map[string][]string{}, map[string][]string{}
		}
		sel.Sets[agent] = set
		platforms := make([]string, len(set))
		for i, name := range set {
			platforms[i] = entryString(providerEntry(providers, ProviderFor(resolved, name)), "platform")
		}
		sel.SetPlatforms[agent] = platforms
	}
	return sel
}

// platformSelected reports whether agent's selection resolves to a provider of platform: any
// entry of its set when the selection carries sets, else its primary's.
func (sel GateSelection) platformSelected(agent, platform string) bool {
	if platforms, ok := sel.SetPlatforms[agent]; ok {
		return slices.Contains(platforms, platform)
	}
	return sel.Platforms[agent] == platform
}

// profileSelected reports whether agent's selection names profile: any entry of its set when
// the selection carries sets, else its primary.
func (sel GateSelection) profileSelected(agent, profile string) bool {
	if set, ok := sel.Sets[agent]; ok {
		return slices.Contains(set, profile)
	}
	return sel.Profiles[agent] == profile
}

// SelectionOf builds the gate's view of a selection: profiles as selected, and each agent's
// platform read off the composed table the launch carries (pack default under user override)
// through the resolved profile table (ProviderFor), so a user's own profile over a shipped
// provider, and a user's own provider that declares a platform, are answered as the shipped
// profile is. A nil table or resolution yields profiles only, which fires no platform gate.
func SelectionOf(profiles map[string]string, resolved map[string]ResolvedProfile,
	providers *jsonx.OrderedMap) GateSelection {
	sel := GateSelection{Profiles: profiles}
	for agent, profile := range profiles {
		if profile == "" {
			continue
		}
		platform := entryString(providerEntry(providers, ProviderFor(resolved, profile)), "platform")
		if platform == "" {
			continue
		}
		if sel.Platforms == nil {
			sel.Platforms = map[string]string{}
		}
		sel.Platforms[agent] = platform
	}
	return sel
}

// ProfilesOnly is a selection carrying profile names and no platforms: all a caller that has
// composed no provider table can say. It fires `profile` gates and never a `platform` one, so a
// caller composing for a launch uses SelectionOf.
func ProfilesOnly(profiles map[string]string) GateSelection {
	return GateSelection{Profiles: profiles}
}

// EnvFold is the pack env fold ONE AGENT receives, as the ORDERED OPERATION SEQUENCE both
// notches consume: for each pack in delivery order, its unconditional `kind: "env"` keys
// sorted, then the keys of each gated env contribution whose gate fires for `agent`, that
// pack's in declaration order, each map sorted. agent "" is the SHARED fold — what every
// process of the launch receives — and no gate fires for it.
//
// THE GATE IS PER AGENT (OQ-BR4, ruled 2026-09-25; docs/reference/providers.md
// §2.6): a satisfied gate delivers its variables to each agent whose selection satisfies it,
// and to no other. gateFiresFor is the rule. It REPLACED a launch-wide answer whose second,
// "wide" pass fired when the profile was active for any bin at all: that pass existed so a
// CLI-less pack's gated env could fire, and it made `-p codex=bedrock` fire claude's gated
// CLAUDE_CODE_USE_BEDROCK jail-wide (trap D2). The CLI-less reach survives, scoped: aws-auth's
// pointer goes to every agent whose selected provider is Bedrock, and only to them. An env
// still has no surface to name an agent, which is why the caller names one — the vehicle that
// delivers per agent (OQ-CN6) is what gives the config-overlay gate's `profiles[key.Agent]`
// (packoverlay.go) an env counterpart.
//
// A GATE IS A PROFILE NAME OR A PROVIDER PLATFORM (OQ-BR8, ruled 2026-09-29). A shipped
// provider fact keys on the platform, so a second profile over one provider (a user's
// `bedrock-sso` over `bedrock`, trap D5) and a user's own provider declaring the platform get
// what the shipped profile gets; the name gate stays for a variant that really is a name.
//
// It is the one definition of the OQ-8 order (providers.md#pv-oq-8), and the order is the
// whole point: unconditional then gated PER PACK, so a later pack's unconditional value
// beats an earlier pack's gated one — the cross-pack rule is unchanged by the gate.
// EnvVarsFor is this sequence reduced over a map, and the host notch composes the process
// env it will exec from the same sequence (internal/cli host.go), so a key that pack A's
// gated env and pack B's static both write resolves to ONE winner at both notches.
//
// Literal strings only, so this is not origin-gated. Which pack wins a key TWO packs write
// is delivery order, not something this fold resolves; a collision is reported by the
// footprint's env-key claims.
func EnvFold(packs []*Pack, sel GateSelection, agent string) []EnvFoldEntry {
	var out []EnvFoldEntry
	for _, p := range packs {
		static := p.Decl.EnvContributions()
		servedBy := p.Decl.EnvServedBy()
		for _, k := range sortedMapKeys(static) {
			out = append(out, EnvFoldEntry{Key: k, Value: static[k], ServedBy: servedBy[k], Pack: p.Name})
		}
		for _, gated := range p.Decl.GatedEnvContributions() {
			if !gateFiresFor(packs, p, gated.Profile, gated.Platform, sel, agent) {
				continue
			}
			for _, k := range sortedMapKeys(gated.Vars) {
				out = append(out, EnvFoldEntry{Key: k, Value: gated.Vars[k], ServedBy: gated.ServedBy,
					RegionProfileSetting: gated.RegionProfileSetting, Pack: p.Name})
			}
		}
	}
	return out
}

// gateFiresFor answers the env gate for ONE agent. The gate holds when `platform` is the
// platform of the provider agent's profile resolves to, or, for a name gate, when `profile` is
// the profile agent selected; and p must be either the pack that installs agent's CLI or a pack
// that installs no CLI at all. The second arm is the CLI-less reach (aws-auth: a pack whose
// gated env serves whichever agent's selection satisfies it); the first is what keeps an agent
// pack's gated env on its own agent, so `-p codex=bedrock` gives codex nothing of claude's. A
// table key naming a CLI no pack of the launch installs is no activation for either arm — the
// rule the launch-wide gate this replaced also kept, and the one the host notch leans on,
// since it keys its one-agent table by whatever basename it was asked to run.
func gateFiresFor(packs []*Pack, p *Pack, profile, platform string, sel GateSelection, agent string) bool {
	if agent == "" {
		return false
	}
	switch {
	case platform != "":
		if !sel.platformSelected(agent, platform) {
			return false
		}
	case profile != "":
		if !sel.profileSelected(agent, profile) {
			return false
		}
	default:
		return false
	}
	bins := p.InstallBins()
	if len(bins) == 0 {
		for _, other := range packs {
			for _, bin := range other.InstallBins() {
				if bin == agent {
					return true
				}
			}
		}
		return false
	}
	for _, bin := range bins {
		if bin == agent {
			return true
		}
	}
	return false
}

// gateDelivered reports whether p's gate fires for SOME agent of the launch — whether the
// contribution reaches any process at all. The env-override pre-flight asks it, because an
// override between two variables nobody receives overrides nothing.
func gateDelivered(packs []*Pack, p *Pack, profile, platform string, sel GateSelection) bool {
	for agent := range sel.Profiles {
		if gateFiresFor(packs, p, profile, platform, sel, agent) {
			return true
		}
	}
	return false
}

// EnvVarsFor is one agent's pack env fold as a map (agent "" for the shared fold) — the
// launch's CLI-keyed profile table applied (providers.md#pv-oq-8), so each pack's gated env
// folds AFTER its unconditional `env` and a gated value later-wins over its own pack's
// default: the gate is the more specific intent, declared after the baseline, and
// overriding it is not a collision.
//
// The fold carries no UNSET any more, and that is the OQ-PT8 shrink, not a shortcut:
// the only env map that could spell one was the profile body's, whose null-means-unset
// decoder died with the body. What remains is unconditional literals and gated
// literals — both `vars` maps of plain strings — so the sequence is assignments only.
//
// THE REDUCTION, not a second fold: applied in order over a map, EnvFold's operations
// yield exactly this result, which is why the jail and the host cannot disagree about who
// wrote a key last.
func EnvVarsFor(packs []*Pack, sel GateSelection, agent string) map[string]string {
	var out map[string]string
	for _, e := range EnvFold(packs, sel, agent) {
		if out == nil {
			out = map[string]string{}
		}
		out[e.Key] = e.Value
	}
	return out
}

// HonoredInstalls returns this pack's install declarations. Nothing is refused.
//
// A `program via installer` — a URL whose contents run as a shell script in the jail — used
// to be refused for a FETCHED pack, on the ground that a git ref must not execute arbitrary
// code there. OQ-TP9 deleted that (docs/design/trust-paths.md, 2026-09-04), and the
// in-house refutation came first: `npm install -g` from the very same fetched tree runs
// `postinstall`, ungated, so the set refused one path to arbitrary in-jail execution while
// permitting another (pack-execution-trust.md §2). Two cases that should be treated alike,
// decided oppositely — now treated alike.
//
// The `refused` return is always nil — see HonoredHostFiles for the family note.
func (p *Pack) HonoredInstalls() (granted []packdecl.Install, refused []string) {
	return p.Decl.InstallContributions(), nil
}

// InstallBins lists the binaries this pack installs, sorted — the CLI names it puts on
// PATH, one per `program` contribution with a bin.
//
// "CLI name" is the namespace a `profile` key resolves in
// (docs/reference/providers.md#the-profile-modifier): `program` is CombineExclusive by bin, so a CLI
// name resolves to at most one pack and the agents a config yields ARE the bins its
// packs install. Config validation and the launch pre-flight both answer "does this key
// name an installed CLI" through this one method, so the two cannot disagree about what
// is installed — and a global `-p` keys each selected pack's profile by the same list,
// so every key in the effective table is a key the namespace would have accepted.
func (p *Pack) InstallBins() []string {
	var bins []string
	for _, in := range p.Decl.InstallContributions() {
		if in.Bin == "" {
			continue
		}
		bins = append(bins, in.Bin)
	}
	sort.Strings(bins)
	return bins
}

// LoadDir reads a pack from a directory. A missing pack.json is fine and yields an
// empty declaration.
//
// name is the pack's effective name and EVERY production caller supplies it — see the
// Pack.Name field comment for the three jobs it has to do and why the manifest cannot do
// them. The two fallbacks below (the manifest's `name`, then the directory) are for a
// caller that has no name to give, which today means only a test.
// tolerateUnknownFields makes LoadDir ignore manifest fields — and skip contribution
// kinds — this build does not know, instead of refusing the manifest. Set once, by the
// IN-JAIL entrypoint (TolerateSkew).
//
// A package-level switch rather than a parameter because the choice is a property of WHERE
// the code is running, not of any individual call: every read on the host is an authoring
// read (be strict — a typo must be loud) and every read in the jail is a cross-version read
// (be tolerant — the host CLI and the baked entrypoint legitimately differ in age). Threading
// it through ten call sites would invite getting one wrong, and the wrong one is the boot
// path, where the cost is a jail that will not start.
var tolerateUnknownFields bool

// TolerateSkew switches this process's manifest reads to the version-tolerant decoder. The
// entrypoint calls it at startup; the host CLI never does.
func TolerateSkew() { tolerateUnknownFields = true }

// OverrideSkewTolerance sets the switch TolerateSkew sets and returns the function that
// restores the previous value. It exists for a HOST-side test that drives the jail's own
// generators (entrypoint.GenerateAgentLaunchers reads the staged tree through
// LoadJailPacks, which calls TolerateSkew): without the restore, every later test in that
// process would read manifests tolerantly and a strict-load refusal it pins would vanish.
// OverrideEmbeddedCacheDir is the same shape for the same reason. Production code never
// calls it.
func OverrideSkewTolerance(tolerant bool) (restore func()) {
	prev := tolerateUnknownFields
	tolerateUnknownFields = tolerant
	return func() { tolerateUnknownFields = prev }
}

func LoadDir(root, name string) (*Pack, []string) {
	decl := &packdecl.Manifest{}
	var problems, skewNotes []string
	var data []byte
	var manifestFound string
	for _, mName := range []string{"pack.jsonc", packdecl.ManifestName} {
		d, err := os.ReadFile(filepath.Join(root, mName))
		if err == nil {
			data = d
			manifestFound = mName
			break
		} else if !os.IsNotExist(err) {
			return nil, []string{"pack " + name + ": " + err.Error()}
		}
	}
	if manifestFound != "" {
		if tolerateUnknownFields {
			decl, problems, skewNotes = packdecl.DecodeTolerant(data)
		} else {
			decl, problems = packdecl.Decode(data)
			if decl != nil {
				problems = append(problems, retiredSurfaceHostGrants(decl)...)
			}
		}
		if decl == nil {
			decl = &packdecl.Manifest{}
		}
		for i, prob := range problems {
			problems[i] = "pack " + name + ": " + prob
		}
		for i, note := range skewNotes {
			skewNotes[i] = "pack " + name + ": " + note
		}
	}
	if name == "" {
		name = decl.Name
	}
	if name == "" {
		name = filepath.Base(root)
	}
	problems = append(problems, reservedBriefingFiles(root, name)...)
	return &Pack{
		Name: name, Root: root, Decl: decl, SkewNotes: skewNotes,
	}, problems
}

// reservedBriefingFiles refuses a file inside the pack's briefing/ directory whose basename is a
// repository's own agent-instruction file — `briefing/AGENTS.md`, `briefing/CLAUDE.md`,
// `briefing/GEMINI.md` (docs/reference/pack-system.md#oq-pb2). Agent tools read those names at any
// depth, so one inside briefing/ is the dual-reader file P1 removes, one directory down.
//
// HERE, and at no second site, because a LoadDir problem is already fatal at every launch site
// (run's stagePacks), at `yolo pack lint` and at `yolo check`, whose Packs section fails on every
// LoadDir problem (check/packs.go, over the tree config.ResolvePack staged). Check used to drop a pack with problems without
// reporting them, so a pack carrying this file passed check and was refused at launch; that gap
// was closed in check, where it was, rather than answered with a second site.
// The message is packdecl's, so the manifest's refusal of `from: "AGENTS.md"` and this one spell
// the same move. Checked with or without a manifest: a manifest-less pack's briefing/ is the
// convention too. Governance never reads such a file even for a caller that discards this
// problem.
func reservedBriefingFiles(root, name string) []string {
	// Only inside the directory spelled exactly `briefing` — the one governance reads — so a
	// `Briefing/AGENTS.md` on a case-insensitive filesystem is not refused as a source it is not.
	dir, ok := conventionalBriefingDir(root)
	if !ok {
		return nil
	}
	entries, err := readDir(dir)
	if err != nil {
		return nil
	}
	var problems []string
	for _, e := range entries {
		rel := packdecl.DefaultBriefingDir + "/" + e.Name()
		if !packdecl.RepositoryInstructionFile(rel) {
			continue
		}
		problems = append(problems, "pack "+name+": "+
			packdecl.ReservedBriefingSourceProblem("file", rel))
	}
	return problems
}

// MaterializeEmbedded copies the embedded official packs into dest and returns them.
//
// Copied out rather than read from the embed.FS directly so every consumer — the tree
// stager, the mount assembler, `yolo pack lint` — sees an ordinary directory. One code
// path for embedded and on-disk packs is the point; special-casing embedded reads would
// reintroduce the "official packs are different" split this design removes.
func MaterializeEmbedded(embedded fs.FS, dest string) ([]*Pack, []string) {
	names, err := embeddedPackDirs(embedded)
	if err != nil {
		return nil, []string{"embedded packs: " + err.Error()}
	}
	var packs []*Pack
	var problems []string
	for _, name := range names {
		root := filepath.Join(dest, name)
		if err := copyEmbeddedTree(embedded, name, root); err != nil {
			problems = append(problems, "embedded pack "+name+": "+err.Error())
			continue
		}
		p, probs := loadEmbeddedPack(root, name)
		problems = append(problems, probs...)
		if p != nil {
			packs = append(packs, p)
		}
	}
	return packs, problems
}

// embeddedPackDirs lists the top-level directories of the embedded FS, sorted — the packs
// MaterializeEmbedded writes, one directory each. A top-level FILE is not a pack and is
// never written.
func embeddedPackDirs(embedded fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// loadEmbeddedPack reads one already-written embedded pack. Split out of MaterializeEmbedded
// for the content-addressed cache (embeddedcache.go), which writes the tree in one place and
// loads it from another — the final name it was renamed to.
//
// Embedded packs ship with yolo, so their declarations carry yolo's own authority:
// mayAccessHost is true. That is why WHERE such a tree lives is a security decision
// (paths.EmbeddedPacksDir states it).
func loadEmbeddedPack(root, name string) (*Pack, []string) {
	p, probs := LoadDir(root, name)
	if p != nil {
		p.Official = true
	}
	return p, probs
}

// loadEmbeddedPacks loads every pack of embedded from an already-written tree at dest.
func loadEmbeddedPacks(embedded fs.FS, dest string) ([]*Pack, []string) {
	names, err := embeddedPackDirs(embedded)
	if err != nil {
		return nil, []string{"embedded packs: " + err.Error()}
	}
	var packs []*Pack
	var problems []string
	for _, name := range names {
		p, probs := loadEmbeddedPack(filepath.Join(dest, name), name)
		problems = append(problems, probs...)
		if p != nil {
			packs = append(packs, p)
		}
	}
	return packs, problems
}

// copyEmbeddedTree writes one embedded pack dir to disk.
func copyEmbeddedTree(embedded fs.FS, sub, dest string) error {
	return fs.WalkDir(embedded, sub, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(sub, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := fs.ReadFile(embedded, p)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// 0o644 always, and it is NOT a policy choice any more — it is the only
		// reachable value. embed.FS cannot represent an exec bit: an embedded file
		// reads back as 0444 whatever its mode in the tree (measured 2026-09-17), so
		// there is no bit here to carry even if we wanted to.
		//
		// ⚠ The old justification — "packstage enforces the same rule for configured
		// packs" — is false and was the misleading half: packstage.copyFile carries
		// 0o111 now, as does run.copyTree. A pack shipping an executable therefore
		// works when it is CONFIGURED BY PATH and silently does not when it is
		// EMBEDDED, which is what packs/hello-daemon exists to pin
		// (internal/packload/packshippedjailbinary_test.go). Nothing here can fix
		// that; closing it means not shipping such a binary through embed.FS.
		return os.WriteFile(target, data, 0o644)
	})
}

// WritableDirs is the union of every pack's writableDirs, sorted and deduped. These
// become per-workspace overlay mounts.
func WritableDirs(packs []*Pack) []string {
	return union(packs, func(p *Pack) []string { return p.Decl.WritableDirContributions() })
}

// SharedDirs is the union of every pack's sharedDirs — the machine-wide tier.
func SharedDirs(packs []*Pack) []string {
	return union(packs, func(p *Pack) []string { return p.Decl.SharedDirContributions() })
}

func union(packs []*Pack, pick func(*Pack) []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range packs {
		for _, d := range pick(p) {
			if _, dup := seen[d]; dup {
				continue
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// LaunchFlagsFor merges every pack's launch flags for one notch, keyed by binary name — a
// later pack wins on a conflicting binary, matching the "later entries win" rule packs
// already use.
//
// EVERY FLAG IS NOTCH-GATED, because the §4.2 autonomy posture is the only place a flag
// can be declared: the `--dangerously-*` flags live in the autonomous posture and vanish
// at the host notch (autonomy=false), where the guarded posture — usually no flags —
// applies. There is no ungated channel left to escape that policy through.
//
// No profile folds here either, which is the OQ-PT8 shrink: a profile is a SELECTION over
// a provider and carries no body, so it contributes no launch flag at all. Taking the
// table as a parameter it could not read would be the accepted-and-ignored plumbing this
// package refuses everywhere else.
func LaunchFlagsFor(packs []*Pack, autonomy bool) map[string][]string {
	out := map[string][]string{}
	for bin, c := range launchFlagClaims(packs, autonomy) {
		out[bin] = c.flags
	}
	return out
}

// launchFlagClaim is one binary's launch flags AND the pack that declared them.
//
// The provenance half exists for the disclosure: a launch that rewrites the user's argv
// says which pack asked for the flags, and "which pack" is the fact the merge below
// destroys — later pack wins, and by the time the map is a map[string][]string nothing
// can say who won. So the walk records it and LaunchFlagsFor PROJECTS the record down,
// rather than a second walk deriving the same answer beside this one: two walks over
// packs × postures is exactly the pair that drifts, and the one that drifted would be the
// one nothing executes except at print time.
type launchFlagClaim struct {
	pack  string
	flags []string
}

// launchFlagClaims folds every pack's launch flags for the given notch, later packs
// winning a repeated binary.
//
// ONE SOURCE: the selected autonomy posture's nested `launch` block. There used to be a
// second — a top-level `kind: "launch"` contribution, folded first and then REPLACED by
// any posture entry for the same binary — and its removal is the point rather than a
// simplification. A flag declared outside a posture was a flag no notch could withhold,
// which is exactly what the confinement policy exists to prevent, and copilot's `--yolo`
// spent a while declared that way. The kind is retired with the migration named
// (packdecl.RetiredKind).
func launchFlagClaims(packs []*Pack, autonomy bool) map[string]launchFlagClaim {
	out := map[string]launchFlagClaim{}
	for _, p := range packs {
		posture := p.Decl.PostureFor(autonomy)
		if posture == nil {
			continue
		}
		for _, l := range posture.Launch {
			if l.Bin != "" {
				out[l.Bin] = launchFlagClaim{pack: p.Name, flags: l.Flags}
			}
		}
	}
	return out
}

// RetiredMiseTools is the fixed list of mise tool tokens yolo used to install for
// its shipped agents and no longer does — a one-shot cleanup of yolo's OWN past
// (OQ11). It is a CORE constant, not a pack manifest field: it describes yolo's
// history, not what a pack contributes, and giving transitional cleanup a manifest
// field (or a contribution kind) would bake a temporary job into the format
// forever. The boot path strips these tokens from a workspace mise.toml and
// `mise uninstall`s them; when no supported jail can still carry one, delete the
// entry (and eventually this whole list).
var RetiredMiseTools = []string{
	`"npm:@anthropic-ai/claude-code"`, // claude was installed via mise before the pack installer
	`"npm:@github/copilot"`,           // copilot, likewise
}

// RetireMiseTools returns the core retired-tool list (the packs argument is
// accepted for call-site compatibility and ignored — the list is no longer
// per-pack).
func RetireMiseTools(_ []*Pack) []string {
	return append([]string(nil), RetiredMiseTools...)
}

// LaunchInjection is the record of ONE argv rewrite: the pack whose declaration asked for
// it, the flags actually added, and the argv on either side.
//
// It is RETURNED rather than re-derived by whoever discloses the rewrite. "Which flags did
// yolo add" is derivable from Before and After by anyone willing to diff two slices — and a
// diff written at the print site is a second implementation of this function's skip rules,
// which would agree with it right up until one of them changed. The injector is the only
// thing that knows; it says so.
//
// Nil means NOTHING WAS REWRITTEN, which is a different fact from "the binary declares no
// flags": a user who already typed every declared flag gets a nil record too, because
// nothing about their command line changed and a disclosure of an empty rewrite is the
// line-on-every-launch noise a disclosure surface dies of.
type LaunchInjection struct {
	// Pack is the name of the pack whose declaration won this binary.
	Pack string
	// Flags are the flags yolo added, in the order they now appear in After.
	Flags []string
	// Before and After are the whole argv on either side of the rewrite. There is
	// deliberately no `Bin` beside them: the binary is Before[0], and a field carrying a
	// second copy of it is one the two could disagree about.
	Before []string
	After  []string
	// Why, when set, is what the flags are for, closing the line that names the pack. Empty for
	// a pack's launch flags, which say what they are for in their own words; set by a rewrite
	// whose flag names a file yolo made, such as `yolo host --`'s model menu
	// (docs/design/model-lists-and-pickers.md MM-D26), whose path alone would not say why.
	Why string
}

// InjectLaunchFlags returns fullCommand with the flags declared for its leading binary
// injected right after it, and the record of what it did (nil when it did nothing).
//
// autonomy is the TARGET's policy bit (render.Target.Profile().AgentAutonomy, or
// render.ProfileFor(notch) where a caller has a notch and no target): true selects each
// pack's autonomous posture, false its guarded one — exactly LaunchFlagsFor's parameter,
// and the same bit packoverlay.Collect takes. It used to be hardcoded true here, which made
// a `guarded.launch` entry unreachable at every notch: the jail callers wanted the
// autonomous posture anyway, and the host never called this at all
// (docs/plans/notch-convergence.md item 20, row D9). Every notch now calls this one
// function and passes its own bit; the notch is an input, never a second fold.
//
// The direct `yolo -- <bin>` invocation and the interactive alias the entrypoint writes
// are two spellings of one launch, and they agree BY CONSTRUCTION rather than by both
// folding the same table: LaunchFlagsFor reads the selected autonomy posture and nothing
// else, so there is no per-launch input left for either spelling to disagree about.
// (Whoever gives a flag a per-launch source again re-introduces this function's old
// `profiles` parameter and BOTH callers' threading of it in the same commit.)
//
// Flags are inserted in reverse (each at index 1) so their declared order is preserved,
// a flag ALREADY PRESENT in the argv is skipped, and a binary no pack declares is
// returned untouched.
//
// THE ONLY SUPPRESSION IS AN IDENTICAL FLAG. A manifest could once declare an alias map —
// `{"--yolo": ["-y"]}` — and a flag whose alias the user had typed was skipped too. That
// is a pack restating a fact about a TOOL'S OWN FLAG PARSER, in a second place, where it
// drifts the day the tool renames a short option and nothing here can notice: the map is
// read only to NOT do something, so a stale entry produces silence rather than an error.
// The identical-flag skip needs no such knowledge — it compares the flag yolo is about to
// add against the ones already in the argv — which is why it stays and the map does not.
//
// The cost is named rather than hidden: a user who types `copilot -y` now gets
// `copilot --yolo -y`. Both spell one switch, so copilot's own parser — the only parser
// that has ever actually known that — resolves them, and the duplication stands in the
// argv rather than being absorbed here by a table that only guessed. The launch prints
// both argvs (run.noteLaunchFlagInjection), so the pair is something the user sees rather
// than something they discover from copilot.
func InjectLaunchFlags(packs []*Pack, autonomy bool, fullCommand []string) ([]string, *LaunchInjection) {
	if len(fullCommand) == 0 {
		return fullCommand, nil
	}
	claim := launchFlagClaims(packs, autonomy)[filepath.Base(fullCommand[0])]
	if len(claim.flags) == 0 {
		return fullCommand, nil
	}
	out := append([]string{}, fullCommand...)
	var added []string
	for i := len(claim.flags) - 1; i >= 0; i-- {
		flag := claim.flags[i]
		if hasFlag(out, flag) {
			continue
		}
		out = append(out[:1], append([]string{flag}, out[1:]...)...)
		added = append([]string{flag}, added...)
	}
	if len(added) == 0 {
		return out, nil
	}
	return out, &LaunchInjection{
		Pack:   claim.pack,
		Flags:  added,
		Before: append([]string{}, fullCommand...),
		After:  append([]string{}, out...),
	}
}

// DisclosureLines is the one wording of an argv rewrite, for every notch that performs one:
// the jail launcher (run.noteLaunchFlagInjection, which colors it) and `yolo host --`
// (hostExec, which prefixes it). The first line is the verdict, the next two the before and
// after argvs one above the other, quoted with shquote.Join so a line is copy-pasteable and
// an argument containing a space cannot masquerade as two, and the last names the pack.
//
// Nil for a nil record, which is "nothing was rewritten": a disclosure printing nothing on
// every launch is how a disclosure surface becomes wallpaper.
func (inj *LaunchInjection) DisclosureLines() []string {
	if inj == nil {
		return nil
	}
	added := "  added by pack " + inj.Pack + ": " + strings.Join(inj.Flags, " ")
	if inj.Why != "" {
		added += " (" + inj.Why + ")"
	}
	return []string{
		"yolo CHANGED the command you asked for:",
		"  you asked for: " + shquote.Join(inj.Before),
		"  yolo will run: " + shquote.Join(inj.After),
		added,
	}
}

func hasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}
