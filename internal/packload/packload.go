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
// identical" is marketing. The only difference is ORIGIN, and origin decides exactly
// one thing — whether a host-access declaration is honored.
package packload

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Pack is a discovered pack: its declaration plus where its files are.
type Pack struct {
	// Name is the pack's effective name (config override, else manifest, else dir).
	Name string
	// Root is the directory its files live in. For an embedded pack this is the
	// materialized copy, so every consumer sees a real path either way.
	Root string
	// Decl is the parsed manifest. Never nil — a pack with no pack.json gets an empty
	// one, because a skills-only pack must stay zero-ceremony.
	Decl *packdecl.Manifest
	// MayAccessHost records whether this pack's origin permits host-reading
	// declarations. Decided by the caller from config.PackEntry.MayGrantHostFiles.
	MayAccessHost bool
	// SkewNotes are the version-skew reports from a TOLERANT manifest read
	// (TolerateSkew): one line per contribution skipped because this build does not
	// know its kind, each naming the pack and the kind (loophole-packaging §3.3a).
	// NOT problems — a problem fails the boot (A12), and surviving exactly that is
	// why the skip exists — but never silent either: the boot path reports each one,
	// so a degraded jail (a contribution the baked entrypoint cannot render) is
	// visible. Always empty on the strict authoring path, where the same manifest is
	// refused as a load problem instead.
	SkewNotes []string
}

// Surfaces decodes the pack's surface declarations, resolving each one's host layer to
// the /ctx path the host CLI mounts it at.
//
// The RESOLUTION is here rather than in the manifest schema because it is a fact about
// how the two sides agree, not about the surface: a pack says "I want ~/.claude/
// settings.json from the host", the CLI mounts it under /ctx, and both sides derive the
// same path from the same declaration. Keeping the derivation in ONE function is what
// makes that agreement checkable — a second copy would be a silent-empty-host-layer bug
// waiting to happen, and the symptom (a config file missing the user's own settings)
// looks nothing like a path mismatch.
//
// A surface is matched to a host file by BASENAME. That is sufficient because a pack's
// grants land in one flat /ctx dir, so two grants with the same basename would already
// collide there; HostFileConflicts reports that as a pack error.
func (p *Pack) Surfaces() ([]manifest.Surface, []string) {
	// The jail/guest default is autonomy ON — so the boot path (which calls Surfaces)
	// renders the autonomous posture, keeping boot output byte-identical after packs
	// move their bypass keys into the autonomy kind. The host path calls SurfacesFor(false).
	// No profile table: the callers that have one call SurfacesFor directly.
	return p.SurfacesFor(true, nil)
}

// SurfacesFor is Surfaces with the §4.2 autonomy policy applied: it decodes the pack's
// config surfaces, then folds the selected autonomy posture's config-managed keys into
// the matching surface's Managed layer (deep-merged, posture wins). autonomy=true selects
// the autonomous posture, false the guarded one. A pack with no autonomy contribution, or
// whose selected posture is empty, gets its surfaces unchanged.
//
// profiles is the launch's CLI-keyed profile table (pack_profiles + the flags): after the
// posture, any variant this pack declares for one of ITS OWN installed CLI names folds on
// top, later-wins (§3.4). A nil table — the host render's, which selects no variant — is
// the pre-profile behavior exactly.
//
// The notes a fold produces are dropped here. Callers that report them call
// SurfacesForReport; this signature stays the one every reader of surfaces wants, because
// most of them (the footprint, the overlay collector, the pruning pass) read identities and
// would only be re-plumbing a third slice to `_`.
func (p *Pack) SurfacesFor(autonomy bool, profiles map[string]string) ([]manifest.Surface, []string) {
	surfaces, problems, _ := p.SurfacesForReport(autonomy, profiles)
	return surfaces, problems
}

// SurfacesForReport is SurfacesFor with the fold's dead patches carried out as notes.
//
// foldPostureManaged drops a patch that names no surface of this pack — it has nothing to
// merge into — and until the notes existed that drop was SILENT, which is the OQ-Z5 shape:
// a patch written for a claude surface and moved into a pack owning no claude surface reads,
// to its author, exactly like a patch that folded. The report is the config-overlay orphan's
// posture for a declaration with no effect (ruling R2), and deliberately NOT a problem: a
// problem is fatal at every render path, and an inert patch breaks nothing — it writes
// nothing. The disposition stays "ignored"; what changed is that "ignored" is now said.
//
// Nothing is reported for a patch that was never folded: an unselected variant merges
// nothing, so it misses nothing, and a host render (no profile table) can only ever produce
// a posture's note.
func (p *Pack) SurfacesForReport(autonomy bool, profiles map[string]string) ([]manifest.Surface, []string, []FoldNote) {
	rawSurfaces := p.Decl.SurfaceContributions()
	if len(rawSurfaces) == 0 {
		return nil, nil, nil
	}
	surfaces, problems := manifest.DecodeSurfaces(rawSurfaces)
	for i, prob := range problems {
		problems[i] = "pack " + p.Name + ": " + prob
	}
	granted, _ := p.HonoredHostFiles()
	for i := range surfaces {
		surfaces[i].HostSource = p.hostSourceFor(surfaces[i].Path, granted)
	}
	var notes []FoldNote
	// Fold the selected autonomy posture's config patch into the matching surfaces.
	if posture := p.Decl.PostureFor(autonomy); posture != nil && len(posture.Config) > 0 {
		patches, probs := manifest.DecodeSurfaces(posture.Config)
		for _, prob := range probs {
			problems = append(problems, "pack "+p.Name+" (autonomy): "+prob)
		}
		var missed []manifest.SurfaceKey
		surfaces, missed = foldPostureManaged(surfaces, patches)
		notes = append(notes, foldNotes(p.Name, postureName(autonomy), missed, surfaces)...)
	}
	// THEN the selected profile's patch, so a key both touch reads the variant's value.
	// Same fold, same carrying-out of a patch naming no base surface — a profile is a
	// variant of the pack's own surfaces, not a second writer of them.
	for _, prof := range p.ActiveProfiles(profiles) {
		if len(prof.Config) == 0 {
			continue
		}
		patches, probs := manifest.DecodeSurfaces(prof.Config)
		for _, prob := range probs {
			problems = append(problems, "pack "+p.Name+" (profile "+prof.Name+"): "+prob)
		}
		var missed []manifest.SurfaceKey
		surfaces, missed = foldPostureManaged(surfaces, patches)
		notes = append(notes, foldNotes(p.Name, "profile "+prof.Name, missed, surfaces)...)
	}
	return surfaces, problems, notes
}

// postureName is the label a note carries for the fold's autonomy half — which of the two
// postures the dead patch rode, so a reader knows which half of the manifest to fix.
func postureName(autonomy bool) string {
	if autonomy {
		return "autonomous posture"
	}
	return "guarded posture"
}

// FoldNote is one config patch that merged into nothing: it named a surface identity its own
// pack does not declare. A NOTE, never a problem and never a refusal — see
// SurfacesForReport for why the disposition does not change.
type FoldNote struct {
	// Pack is the pack that declared the patch.
	Pack string
	// Source names the declaration the patch rode: "autonomous posture", "guarded
	// posture", or "profile <name>".
	Source string
	// Target is the (agent, name) the patch named — the identity nothing matched.
	Target manifest.SurfaceKey
	// Declared lists the surface identities the pack DOES declare, so the fix is in the
	// message rather than one manifest-open away. Never empty: a pack with no config
	// contributions has no fold and so produces no note.
	Declared []string
}

// reason is the body both renderings share, so the two notches cannot disagree about what
// happened to the patch.
func (n FoldNote) reason() string {
	return fmt.Sprintf("%s patches %s, which pack %s does not declare (declares: %s)",
		n.Source, n.Target, n.Pack, strings.Join(n.Declared, ", "))
}

// String renders the note as the one line a render path warns with — shaped like the
// config-overlay notice it is modelled on: a kind label, what has no effect, and the
// declaration that went nowhere.
func (n FoldNote) String() string {
	return fmt.Sprintf("config patch  %s — folded nowhere", n.reason())
}

// Action renders the same finding the way a host render result line states it: what the
// render did (nothing), since that notch's report is a row per surface, not a boot notice.
func (n FoldNote) Action() string {
	return "ignored: " + n.reason()
}

// foldNotes turns one fold's misses into notes, each naming the pack, the declaration the
// patch rode, and every surface the pack actually declares.
func foldNotes(pack, source string, missed []manifest.SurfaceKey, base []manifest.Surface) []FoldNote {
	if len(missed) == 0 {
		return nil
	}
	declared := make([]string, 0, len(base))
	for _, s := range base {
		declared = append(declared, s.Key().String())
	}
	out := make([]FoldNote, 0, len(missed))
	for _, key := range missed {
		out = append(out, FoldNote{Pack: pack, Source: source, Target: key, Declared: declared})
	}
	return out
}

// ActiveProfiles returns the variants THIS pack has selected, in the order they must
// fold: sorted by the CLI name that selected them, so a pack owning two bins with two
// active names is deterministic rather than map-order. A name is active for this pack only
// when it sits at a CLI name the pack installs (§3.3) — a key for some other pack's CLI
// gates that pack, not this one.
//
// Exported because the fold has one more consumer than the pack's own render paths: the
// host notch composes the selected variant's `env` into the process it execs
// (hostEnvVars), and the jail does the same through EnvVarsFor — both go through here, so
// the two notches cannot disagree about WHICH variants are active.
func (p *Pack) ActiveProfiles(profiles map[string]string) []packdecl.ProfileContribution {
	if len(profiles) == 0 {
		return nil
	}
	var out []packdecl.ProfileContribution
	for _, bin := range p.InstallBins() {
		name, selected := profiles[bin]
		if !selected || name == "" {
			continue
		}
		if prof := p.Decl.ProfileFor(name); prof != nil {
			out = append(out, *prof)
		}
	}
	return out
}

// ProfileTable lowers a decoded profile table — YOLO_PACK_PROFILES in the jail, the
// config's `pack_profiles` on the host — into the map the three folds above take.
//
// THE one lowering, and not a convenience: a JSON null at a key REMOVES that profile
// (the merge-patch convention the table uses), and a null decoded into map[string]string
// would arrive as "" and read as a selection of an empty name. Dropping the key here is
// what keeps "no profile" and "profile removed" the same fact at every fold.
func ProfileTable(m *jsonx.OrderedMap) map[string]string {
	if m == nil {
		return nil
	}
	var out map[string]string
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		name, ok := v.(string)
		if !ok || name == "" {
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
// with the same (agent, name), the patch winning per key, and returns alongside the merged
// set the identities that matched NOTHING. The caller reports those (foldNotes) — the loop
// knows which patches missed, and discarding that knowledge here is what made a dead patch
// indistinguishable from a live one. This is how an autonomy posture asserts its permission
// keys onto the pack's OWN surface without being a second config writer.
func foldPostureManaged(base, patches []manifest.Surface) ([]manifest.Surface, []manifest.SurfaceKey) {
	var missed []manifest.SurfaceKey
	for _, patch := range patches {
		pm := patch.ManagedMap()
		if pm == nil {
			continue
		}
		matched := false
		for i := range base {
			if base[i].Agent != patch.Agent || base[i].Name != patch.Name {
				continue
			}
			matched = true
			base[i].Managed = mergeManagedMap(base[i].ManagedMap(), pm)
		}
		if !matched {
			missed = append(missed, patch.Key())
		}
	}
	return base, missed
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

// hostSourceFor finds the granted host file whose basename matches this surface's file,
// and returns the /ctx path it is mounted at. Empty when the pack granted none — the
// surface then has no host layer, which is the common case.
func (p *Pack) hostSourceFor(surfacePath string, granted []packdecl.HostFile) string {
	want := path.Base(surfacePath)
	for _, hf := range granted {
		if path.Base(hf.From) != want {
			continue
		}
		return CtxPath(p.Name, hf)
	}
	return ""
}

// CtxPath is the in-jail /ctx path a granted host file is mounted at. THE one definition
// both sides use: the CLI emits this mount destination, the entrypoint reads the host
// layer from it.
func CtxPath(pack string, hf packdecl.HostFile) string {
	if hf.To != "" {
		return CtxRoot + "/" + hf.To
	}
	return CtxRoot + "/host-" + pack + "/" + path.Base(hf.From)
}

// CtxRoot is where host-file mounts land in the jail.
const CtxRoot = "/ctx"

// HostFileConflicts reports grants that would collide at the same /ctx destination.
//
// Two grants landing on one path would mean one silently shadows the other, and the
// surface reading that path would compose the wrong user file into its output — a wrong
// config that looks right. Reported so it fails at load instead.
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

// HonoredHostFiles returns the host-file grants this pack is ALLOWED to make, and a
// notice per grant that was refused.
//
// A refusal is REPORTED, never silent. A pack asking for a host file and not getting
// one changes what the jail contains, so a user who installed it must be told rather
// than left to discover the absence.
func (p *Pack) HonoredHostFiles() (granted []packdecl.HostFile, refused []string) {
	hostFiles := p.Decl.HostFileContributions()
	if len(hostFiles) == 0 {
		return nil, nil
	}
	if p.MayAccessHost {
		return hostFiles, nil
	}
	for _, hf := range hostFiles {
		refused = append(refused, fmt.Sprintf(
			"pack %s: refused host file %q — a FETCHED pack cannot read your host home. "+
				"Installing a pack approves distributing content, not handing that "+
				"repository your host config.", p.Name, hf.From))
	}
	return nil, refused
}

// HonoredMounts returns the pack's mount contributions if its origin permits them,
// else refuses each with a reported message. A mount reads the host home (the
// source may be a whole directory), so it is gated exactly like a host file: a
// fetched pack gets none.
func (p *Pack) HonoredMounts() (granted []packdecl.HostFile, refused []string) {
	mounts := p.Decl.HostMountContributions()
	if len(mounts) == 0 {
		return nil, nil
	}
	if p.MayAccessHost {
		return mounts, nil
	}
	for _, mt := range mounts {
		refused = append(refused, fmt.Sprintf(
			"pack %s: refused mount %q — a FETCHED pack cannot read your host home. "+
				"Installing a pack approves distributing content, not handing that "+
				"repository your host files.", p.Name, mt.From))
	}
	return nil, refused
}

// EnvFoldEntry is one operation of the pack env fold, in the order it applies: an
// assignment, or (Unset) a removal.
type EnvFoldEntry struct {
	Key   string
	Value string
	Unset bool
}

// EnvFold is the pack env fold as the ORDERED OPERATION SEQUENCE both notches consume:
// for each pack in delivery order, its static `kind: "env"` keys sorted, then each
// variant it has active (ActiveProfiles' order — sorted by the CLI name that selected it)
// with that variant's `env` keys sorted.
//
// It is the one definition of the OQ-8 order, and the order is the whole point: static
// then variants PER PACK, so a later pack's static value beats an earlier pack's variant —
// the cross-pack rule is unchanged by the variant kind. EnvVarsFor is this sequence
// reduced over a map (the jail notch's form: the env starts empty, so a removal is a
// delete), and the host notch composes the process env it will exec from the same
// sequence (internal/cli host.go). Reducing it twice, once per notch, is what keeps a key
// that pack A's variant and pack B's static both write resolving to ONE winner.
//
// Static values only, so this is not origin-gated. Which pack wins a key TWO packs write
// is delivery order, not something this fold resolves; a collision is reported by the
// footprint's env-key claims.
func EnvFold(packs []*Pack, profiles map[string]string) []EnvFoldEntry {
	var out []EnvFoldEntry
	for _, p := range packs {
		static := p.Decl.EnvContributions()
		for _, k := range sortedMapKeys(static) {
			out = append(out, EnvFoldEntry{Key: k, Value: static[k]})
		}
		for _, prof := range p.ActiveProfiles(profiles) {
			for _, k := range sortedEnvValueKeys(prof.Env) {
				if v := prof.Env[k]; v.Unset() {
					out = append(out, EnvFoldEntry{Key: k, Unset: true})
					continue
				}
				out = append(out, EnvFoldEntry{Key: k, Value: prof.Env[k].Value})
			}
		}
	}
	return out
}

// sortedEnvValueKeys is sortedMapKeys for a profile's `env` map, whose values carry the
// null bit a plain string map cannot.
func sortedEnvValueKeys(m map[string]packdecl.EnvValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// EnvVarsFor is the pack env fold as a map — the launch's CLI-keyed profile table
// applied (§3.4, OQ-8), so each pack's own variants fold AFTER its static `env` and a
// variant later-wins over its own pack's default: a variant is the more specific intent,
// declared after the baseline, and overriding it is not a collision.
//
// A null in a profile's env UNSETS the key (OQ-7), and the merge-patch semantics mean it
// removes the key outright rather than setting it empty: a caller composing an environment
// cannot tell "absent" from "removed" here, and for the jail notch that distinction does
// not exist (the jail starts from an empty env). A caller that must REMOVE a real value —
// the host notch, where the process env is inherited — walks EnvFold itself, whose entries
// carry the bit this map's value type cannot.
//
// THE REDUCTION, not a second fold: applied in order over a map, EnvFold's operations
// yield exactly this result, which is why the jail and the host cannot disagree about who
// wrote a key last.
func EnvVarsFor(packs []*Pack, profiles map[string]string) map[string]string {
	var out map[string]string
	for _, e := range EnvFold(packs, profiles) {
		if e.Unset {
			delete(out, e.Key) // a no-op on a nil map, which is the empty-fold case
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[e.Key] = e.Value
	}
	return out
}

// HonoredInstalls returns the install declarations this pack's origin permits, and a
// notice per declaration that was refused.
//
// A native installer is a URL whose contents run as a shell script, so it is gated the
// same way a host file is: a fetched pack introducing one would let a git ref execute
// arbitrary code in the jail. An npm install names a registry package and is not
// origin-gated — it is the same trust as any dependency the user already installs.
//
// THE GATE IS PER CONTRIBUTION, and that is the load-bearing part of the plural form: a
// pack may mix an npm install with a curl-to-shell installer, and only the second is
// gated. Deciding once for the whole pack would either refuse the innocent npm install or
// — far worse — let a fetched pack smuggle an installer URL through beside one.
func (p *Pack) HonoredInstalls() (granted []packdecl.Install, refused []string) {
	for _, in := range p.Decl.InstallContributions() {
		if in.InstallerURL != "" && !p.MayAccessHost {
			refused = append(refused, fmt.Sprintf(
				"pack %s: refused installer %q — a FETCHED pack cannot run a curl-piped "+
					"installer in the jail.", p.Name, in.InstallerURL))
			continue
		}
		granted = append(granted, in)
	}
	return granted, refused
}

// InstallBins lists the binaries this pack installs, sorted — the CLI names it puts on
// PATH, one per `program` contribution with a bin.
//
// "CLI name" is the namespace a `pack_profiles` key resolves in
// (profiles-as-pack-variants.md §2.5): `program` is CombineExclusive by bin, so a CLI
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

// RefusedBriefingOverlays names every `briefing` contribution whose `after: "host:<path>"`
// this pack's origin does not permit — the FIFTH gated claim, and the only one that had no
// reporter at all.
//
// `briefing host:<path>` is an approvable claim like any other (contributes.go's
// HostAccessClaims emits it, `pack install` prompts for it, the lockfile records it), and the
// launch already withheld it for an unapproved pack — silently, in one `&& p.MayAccessHost`
// inside run/prepare.go's briefing loop. So a pack whose ONLY host claim was "prepend the
// user's own AGENTS.md before my prose" produced a jail with the pack's prose and none of the
// user's, and not one line anywhere said so. Under OQ-TP6 that is a partial pack, so it needs
// a refusal to be fatal ABOUT.
//
// A REPORTER, not a gate, which is the one asymmetry with the Honored* family above. The gate
// stays where it is: prepare.go composes the briefing body and is the only place that knows
// the host home, and moving the composition here to justify a `granted` return nobody would
// read would be a worse trade than the asymmetry. What this owes the family is the sentence a
// user reads, and that is all it returns.
func (p *Pack) RefusedBriefingOverlays() []string {
	if p.MayAccessHost {
		return nil
	}
	var refused []string
	for _, c := range p.Decl.Contributions() {
		if c.Kind != packdecl.KindBriefing || !strings.HasPrefix(c.After, "host:") {
			continue
		}
		src := strings.TrimPrefix(c.After, "host:")
		refused = append(refused, fmt.Sprintf(
			"pack %s: refused briefing overlay %q — a FETCHED pack cannot read your host "+
				"home, so your own %s would not be prepended to this pack's prose.",
			p.Name, src, src))
	}
	return refused
}

// LoadDir reads a pack from a directory. A missing pack.json is fine and yields an
// empty declaration.
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

func LoadDir(root, name string, mayAccessHost bool) (*Pack, []string) {
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
	return &Pack{
		Name: name, Root: root, Decl: decl, MayAccessHost: mayAccessHost,
		SkewNotes: skewNotes,
	}, problems
}

// MaterializeEmbedded copies the embedded official packs into dest and returns them.
//
// Copied out rather than read from the embed.FS directly so every consumer — the tree
// stager, the mount assembler, `yolo pack lint` — sees an ordinary directory. One code
// path for embedded and on-disk packs is the point; special-casing embedded reads would
// reintroduce the "official packs are different" split this design removes.
func MaterializeEmbedded(embedded fs.FS, dest string) ([]*Pack, []string) {
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		return nil, []string{"embedded packs: " + err.Error()}
	}
	var packs []*Pack
	var problems []string
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		root := filepath.Join(dest, name)
		if err := copyEmbeddedTree(embedded, name, root); err != nil {
			problems = append(problems, "embedded pack "+name+": "+err.Error())
			continue
		}
		// Embedded packs ship with yolo, so their declarations carry yolo's own
		// authority: mayAccessHost is true.
		p, probs := LoadDir(root, name, true)
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
		// 0o644 always: pack content is content, and an exec bit arriving through a
		// content channel is a different trust question (packstage enforces the same
		// rule for configured packs).
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

// LaunchFlagsFor merges every pack's launch contributions, keyed by binary name — a later
// pack wins on a conflicting binary, matching the "later entries win" rule packs already
// use — with the §4.2 autonomy policy applied: on top of each pack's plain `launch`
// contributions it folds the selected autonomy posture's per-binary launch flags. So the
// `--dangerously-*` flags live in the autonomous posture and vanish at the host notch
// (autonomy=false), where the guarded posture (usually no flags) applies.
//
// profiles is the launch's CLI-keyed profile table: after the posture, each pack's own
// selected variants fold in, later-wins per bin (§3.4, OQ-8) — the same order the env fold
// applies, so the two halves of one variant cannot disagree about who beat the static
// baseline. A nil table is the pre-profile behavior exactly.
func LaunchFlagsFor(packs []*Pack, autonomy bool, profiles map[string]string) map[string][]string {
	out := map[string][]string{}
	for _, p := range packs {
		for bin, flags := range p.Decl.LaunchFlagContributions() {
			out[bin] = flags
		}
		if posture := p.Decl.PostureFor(autonomy); posture != nil {
			for _, l := range posture.Launch {
				if l.Bin != "" {
					out[l.Bin] = l.Flags
				}
			}
		}
		for _, prof := range p.ActiveProfiles(profiles) {
			for _, l := range prof.Launch {
				if l.Bin != "" {
					out[l.Bin] = l.Flags
				}
			}
		}
	}
	return out
}

// FlagAliases merges every pack's flagAliases.
func FlagAliases(packs []*Pack) map[string][]string {
	out := map[string][]string{}
	for _, p := range packs {
		for flag, aliases := range p.Decl.FlagAliasContributions() {
			out[flag] = aliases
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

// InjectLaunchFlags returns fullCommand with the flags declared for its leading binary
// injected right after it.
//
// profiles is the same CLI-keyed table LaunchFlagsFor folds: a selected variant's launch
// contribution is injected exactly as a static one is. Callers that hold the launch's
// effective table must pass it — the direct `yolo -- <bin>` invocation is one of TWO
// spellings of the same launch, and the other (the interactive alias the entrypoint writes)
// already folds the table. A nil table here is how the two spellings diverge: a pack's
// variant flags appear on the alias and vanish from the direct command, silently, which is
// why the run pipeline passes what effectivePackProfiles merged.
//
// Moved out of internal/agents unchanged in behavior: flags are inserted in reverse
// (each at index 1) so their declared order is preserved, and a flag already present —
// or a declared alias of one — is skipped, so a user who passed `-y` does not also get
// `--yolo`. A binary no pack declares is returned untouched.
func InjectLaunchFlags(packs []*Pack, profiles map[string]string, fullCommand []string) []string {
	if len(fullCommand) == 0 {
		return fullCommand
	}
	flags := LaunchFlagsFor(packs, true, profiles)[filepath.Base(fullCommand[0])]
	if len(flags) == 0 {
		return fullCommand
	}
	aliases := FlagAliases(packs)
	out := append([]string{}, fullCommand...)
	for i := len(flags) - 1; i >= 0; i-- {
		flag := flags[i]
		if hasFlag(out, flag) {
			continue
		}
		skip := false
		for _, alias := range aliases[flag] {
			if hasFlag(out, alias) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		out = append(out[:1], append([]string{flag}, out[1:]...)...)
	}
	return out
}

func hasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}
