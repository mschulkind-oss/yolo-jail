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

// Surfaces decodes the pack's surface declarations.
func (p *Pack) Surfaces() ([]manifest.Surface, []string) {
	if len(p.Decl.Surfaces) == 0 {
		return nil, nil
	}
	surfaces, problems := manifest.DecodeSurfaces(p.Decl.Surfaces)
	for i, prob := range problems {
		problems[i] = "pack " + p.Name + ": " + prob
	}
	return surfaces, problems
}

// HonoredHostFiles returns the host-file grants this pack is ALLOWED to make, and a
// notice per grant that was refused.
//
// A refusal is REPORTED, never silent. A pack asking for a host file and not getting
// one changes what the jail contains, so a user who installed it must be told rather
// than left to discover the absence.
func (p *Pack) HonoredHostFiles() (granted []packdecl.HostFile, refused []string) {
	if len(p.Decl.HostFiles) == 0 {
		return nil, nil
	}
	if p.MayAccessHost {
		return p.Decl.HostFiles, nil
	}
	for _, hf := range p.Decl.HostFiles {
		refused = append(refused, fmt.Sprintf(
			"pack %s: refused host file %q — a FETCHED pack cannot read your host home. "+
				"Installing a pack approves distributing content, not handing that "+
				"repository your host config.", p.Name, hf.From))
	}
	return nil, refused
}

// HonoredInstall returns the pack's install declaration if its origin permits it.
//
// A native installer is a URL whose contents run as a shell script, so it is gated the
// same way a host file is: a fetched pack introducing one would let a git ref execute
// arbitrary code in the jail. An npm install names a registry package and is not
// origin-gated — it is the same trust as any dependency the user already installs.
func (p *Pack) HonoredInstall() (*packdecl.Install, string) {
	in := p.Decl.Install
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
func LoadDir(root, name string, mayAccessHost bool) (*Pack, []string) {
	decl := &packdecl.Manifest{}
	var problems []string
	data, err := os.ReadFile(filepath.Join(root, packdecl.ManifestName))
	if err == nil {
		decl, problems = packdecl.Decode(data)
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
	return union(packs, func(p *Pack) []string { return p.Decl.WritableDirs })
}

// SharedDirs is the union of every pack's sharedDirs — the machine-wide tier.
func SharedDirs(packs []*Pack) []string {
	return union(packs, func(p *Pack) []string { return p.Decl.SharedDirs })
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
func LaunchFlags(packs []*Pack) map[string][]string {
	out := map[string][]string{}
	for _, p := range packs {
		for bin, flags := range p.Decl.LaunchFlags {
			out[bin] = flags
		}
	}
	return out
}

// FlagAliases merges every pack's flagAliases.
func FlagAliases(packs []*Pack) map[string][]string {
	out := map[string][]string{}
	for _, p := range packs {
		for flag, aliases := range p.Decl.FlagAliases {
			out[flag] = aliases
		}
	}
	return out
}

// RetireMiseTools is the union of every pack's retireMiseTools.
func RetireMiseTools(packs []*Pack) []string {
	return union(packs, func(p *Pack) []string { return p.Decl.RetireMiseTools })
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
