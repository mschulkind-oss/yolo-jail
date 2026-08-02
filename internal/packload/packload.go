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
	return p.SurfacesFor(true)
}

// SurfacesFor is Surfaces with the §4.2 autonomy policy applied: it decodes the pack's
// config surfaces, then folds the selected autonomy posture's config-managed keys into
// the matching surface's Managed layer (deep-merged, posture wins). autonomy=true selects
// the autonomous posture, false the guarded one. A pack with no autonomy contribution, or
// whose selected posture is empty, gets its surfaces unchanged.
func (p *Pack) SurfacesFor(autonomy bool) ([]manifest.Surface, []string) {
	rawSurfaces := p.Decl.SurfaceContributions()
	if len(rawSurfaces) == 0 {
		return nil, nil
	}
	surfaces, problems := manifest.DecodeSurfaces(rawSurfaces)
	for i, prob := range problems {
		problems[i] = "pack " + p.Name + ": " + prob
	}
	granted, _ := p.HonoredHostFiles()
	for i := range surfaces {
		surfaces[i].HostSource = p.hostSourceFor(surfaces[i].Path, granted)
	}
	// Fold the selected autonomy posture's config patch into the matching surfaces.
	if posture := p.Decl.PostureFor(autonomy); posture != nil && len(posture.Config) > 0 {
		patches, probs := manifest.DecodeSurfaces(posture.Config)
		for _, prob := range probs {
			problems = append(problems, "pack "+p.Name+" (autonomy): "+prob)
		}
		surfaces = foldPostureManaged(surfaces, patches)
	}
	return surfaces, problems
}

// foldPostureManaged deep-merges each patch surface's Managed map into the base surface
// with the same (agent, name), the patch winning per key. A patch that names no existing
// base surface is ignored (the posture may only touch a subset). This is how an autonomy
// posture asserts its permission keys onto the pack's OWN surface without being a second
// config writer.
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

// EnvVars merges the env contributions of every pack into one map, later packs
// winning a key. Static values only, so this is not origin-gated. Deterministic is
// not guaranteed across a key set two packs both write; a collision is reported by
// the footprint's env-key claims rather than resolved here.
func EnvVars(packs []*Pack) map[string]string {
	var out map[string]string
	for _, p := range packs {
		for k, v := range p.Decl.EnvContributions() {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	return out
}

// HonoredInstall returns the pack's install declaration if its origin permits it.
//
// A native installer is a URL whose contents run as a shell script, so it is gated the
// same way a host file is: a fetched pack introducing one would let a git ref execute
// arbitrary code in the jail. An npm install names a registry package and is not
// origin-gated — it is the same trust as any dependency the user already installs.
func (p *Pack) HonoredInstall() (*packdecl.Install, string) {
	in := p.Decl.InstallContribution()
	if in == nil {
		return nil, ""
	}
	if in.InstallerURL != "" && !p.MayAccessHost {
		return nil, fmt.Sprintf(
			"pack %s: refused installer %q — a FETCHED pack cannot run a curl-piped "+
				"installer in the jail.", p.Name, in.InstallerURL)
	}
	return in, ""
}

// LoadDir reads a pack from a directory. A missing pack.json is fine and yields an
// empty declaration.
// tolerateUnknownFields makes LoadDir ignore manifest fields this build does not know,
// instead of refusing the manifest. Set once, by the IN-JAIL entrypoint (TolerateSkew).
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
	var problems []string
	data, err := os.ReadFile(filepath.Join(root, packdecl.ManifestName))
	if err == nil {
		decode := packdecl.Decode
		if tolerateUnknownFields {
			decode = packdecl.DecodeTolerant
		}
		decl, problems = decode(data)
		if decl == nil {
			decl = &packdecl.Manifest{}
		}
		for i, prob := range problems {
			problems[i] = "pack " + name + ": " + prob
		}
	} else if !os.IsNotExist(err) {
		return nil, []string{"pack " + name + ": " + err.Error()}
	}
	if name == "" {
		name = decl.Name
	}
	if name == "" {
		name = filepath.Base(root)
	}
	return &Pack{Name: name, Root: root, Decl: decl, MayAccessHost: mayAccessHost}, problems
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

// LaunchFlags merges every pack's launchFlags, keyed by binary name. A later pack wins
// on a conflicting binary, matching the "later entries win" rule packs already use.
// It applies autonomy ON (the jail/guest default); the host path calls LaunchFlagsFor(false).
func LaunchFlags(packs []*Pack) map[string][]string {
	return LaunchFlagsFor(packs, true)
}

// LaunchFlagsFor is LaunchFlags with the §4.2 autonomy policy applied: on top of each
// pack's plain `launch` contributions it folds the selected autonomy posture's per-binary
// launch flags. So the `--dangerously-*` flags live in the autonomous posture and vanish
// at the host notch (autonomy=false), where the guarded posture (usually no flags) applies.
func LaunchFlagsFor(packs []*Pack, autonomy bool) map[string][]string {
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
// Moved out of internal/agents unchanged in behavior: flags are inserted in reverse
// (each at index 1) so their declared order is preserved, and a flag already present —
// or a declared alias of one — is skipped, so a user who passed `-y` does not also get
// `--yolo`. A binary no pack declares is returned untouched.
func InjectLaunchFlags(packs []*Pack, fullCommand []string) []string {
	if len(fullCommand) == 0 {
		return fullCommand
	}
	flags := LaunchFlags(packs)[filepath.Base(fullCommand[0])]
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
