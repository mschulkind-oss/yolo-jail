package packload

// packtree.go is the pack-set half of a PACK-TREE PROGRAM (docs/design/pack-buildable-programs.md,
// a `program` delivered `via: "pack"`): which ones a selection carries, as Fork values, and the
// digest of the source directory that is its build's revision.
//
// A PACK-TREE PROGRAM IS A Fork WITH A Run, for the patched extension's reason (patchedtrees.go):
// the fork route's build act, its per-build lock, its store admission and its launch pool take a
// Fork, and a pack-tree program is built by that act with a different source (P1, BG-D2). It is
// not a fork: it owns its bin (Base is ""), it has no source address and no pin, and its revision
// is the content of its pack's own tree. So nothing that pins, checks or rewrites forks ever
// lists one — Forks and ApplyForks read only contributions with a `fork_of` — and PackTreePrograms
// is the one list that does.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/treedigest"
)

// IsPackTree reports whether f is a PACK-TREE PROGRAM's build, not a fork's or an extension's.
func (f Fork) IsPackTree() bool { return f.Run != "" }

// BuildSource is a pack-tree build's source string, "pack:<pack>/<from>"
// (packdecl.PackTreeBuildSource), "" for a fork, whose source is its address (Source).
func (f Fork) BuildSource() string {
	if !f.IsPackTree() {
		return ""
	}
	if f.Checkout != "" {
		return packdecl.PackTreeCheckoutBuildSource(f.Pack, f.Checkout)
	}
	return packdecl.PackTreeBuildSource(f.Pack, f.From)
}

// SourceSaid is the source a pack-tree build's lines name: "your checkout <dir>" when it builds the
// user's checkout (Checkout), else the pack's `from`.
func (f Fork) SourceSaid() string {
	if f.Checkout != "" {
		return "your checkout " + f.Checkout
	}
	return f.From
}

// PackTreeRecipe is packdecl.PackTreeRecipe for f's declaration, "" for a fork.
func (f Fork) PackTreeRecipe() string {
	if !f.IsPackTree() {
		return ""
	}
	if f.IsPrebuilt() {
		return packdecl.PackTreePrebuiltRecipe(f.Bin)
	}
	return packdecl.PackTreeRecipe(f.From, f.Build, f.Run)
}

// IsPrebuilt reports whether f is a pack-tree program delivered from its PREBUILT artifact
// (docs/design/pack-buildable-programs.md §7): the resolver (PackTreeResolve) chose that rung, so
// the act fetches and verifies Prebuilt in place of building From.
func (f Fork) IsPrebuilt() bool { return f.IsPackTree() && f.Prebuilt != nil }

// ProgramRun is the program file inside its directory that the link names: `run` for a build or a
// copy, the artifact's own name (packdecl.PackTreePrebuiltRun) for a prebuilt. "" for a fork.
func (f Fork) ProgramRun() string {
	if f.IsPrebuilt() {
		return packdecl.PackTreePrebuiltRun(f.Bin)
	}
	return f.Run
}

// PrebuiltPlatforms are the platforms f's `prebuilt` declares, sorted; nil for none.
func (f Fork) PrebuiltPlatforms() []string { return packdecl.PrebuiltPlatforms(f.Prebuilts) }

// PrebuiltOptInStep is packdecl.PrebuiltOptInStep for f on platform: the explicit opt-in every
// refusal of a pack-tree build names (BG-D21), "" when f declares no artifact for platform. Also ""
// for a dev override's checkout, whose `path` the prebuilt opt-in cannot be combined with
// (config.ProgramOverride), and for a prebuilt itself, which is no build.
func (f Fork) PrebuiltOptInStep(platform string) string {
	if f.Checkout != "" || f.IsPrebuilt() {
		return ""
	}
	return packdecl.PrebuiltOptInStep(f.Pack, f.Bin, platform, f.Prebuilts)
}

// PrebuiltOptInClause is "; " plus PrebuiltOptInStep, or "" where that is "": what a refusal's
// sentence appends.
func (f Fork) PrebuiltOptInClause(platform string) string {
	if step := f.PrebuiltOptInStep(platform); step != "" {
		return "; " + step
	}
	return ""
}

// PackTreeResolve is the resolver's build-or-prebuilt half for one pack-tree program on platform
// (docs/design/pack-buildable-programs.md §4; the dev override, the rung above, is decided
// elsewhere): which bytes this delivery takes, as the pin the fork route's build act keys on.
// usePrebuilt is the user's explicit opt-in, `programs.overrides.<bin>.prebuilt` (BG-D18).
//
// By OQ-BG2's ruling (2026-10-23) a prebuilt is NEVER substituted for a present source: not where
// a builder is permanently unavailable, not where it is busy. A build that cannot run is the
// caller's fatal refusal, which names the setup step and, when one is declared, the opt-in
// (Fork.PrebuiltOptInStep).
//
//   - usePrebuilt: the PREBUILT when `prebuilt` declares platform, whether or not the source is
//     present; otherwise a reason naming the setting and the platforms `prebuilt` does declare.
//   - The source directory is present: the BUILD, its revision the tree digest. The artifact is
//     never consulted (P5).
//   - The source directory is absent: the PREBUILT when `prebuilt` declares platform, its revision
//     the artifact's sha256 — the only bytes the pack offers, so nothing is substituted; otherwise
//     a reason naming the pack, `from`, the platform and the platforms `prebuilt` does declare.
//
// A CHECKOUT (Checkout set: the user's `programs.overrides.<bin>.path`, BG-D19) is always the
// BUILD, its revision the checkout's tree digest; an absent or unreadable checkout is a reason,
// never the prebuilt. Validation keeps `path` and `prebuilt` apart, so usePrebuilt is never set
// beside it.
//
// A prebuilt pin's Fork carries the artifact (Prebuilt) and why it was chosen (PrebuiltWhy).
func PackTreeResolve(f Fork, platform string, usePrebuilt bool) ForkPin {
	p := ForkPin{Fork: f}
	if f.Checkout != "" {
		digest, err := f.Digest()
		switch {
		case err != nil:
			p.Reason = "your checkout " + f.Checkout + " could not be read (" + err.Error() + ") — fix it, or remove " +
				"programs.overrides." + f.Bin + " from " + paths.UserConfigDisplayPath() + " to build pack " + f.Pack + "'s own " + f.From
		case digest == "":
			p.Reason = "your checkout " + f.Checkout + " is gone — restore it, or remove programs.overrides." + f.Bin +
				" from " + paths.UserConfigDisplayPath() + " to build pack " + f.Pack + "'s own " + f.From
		default:
			p.Commit = digest
		}
		return p
	}
	pb, declared := f.Prebuilts[platform]
	have := "none"
	if ps := f.PrebuiltPlatforms(); len(ps) > 0 {
		have = strings.Join(ps, ", ")
	}
	if usePrebuilt {
		if !declared {
			p.Reason = "programs.overrides." + f.Bin + ".prebuilt is set in " + paths.UserConfigDisplayPath() + ", but pack " +
				f.Pack + "'s `prebuilt` declares no artifact for " + platform + " (it declares: " + have +
				") — remove that setting to build " + f.Bin + " from the pack's " + f.From + ", or ask the pack's " +
				"author for a `prebuilt` entry for " + platform
			return p
		}
		f.PrebuiltWhy = "programs.overrides." + f.Bin + ".prebuilt is set"
		f.Prebuilt = &pb
		p.Fork, p.Commit = f, pb.SHA256
		return p
	}
	digest, err := f.Digest()
	if err != nil {
		p.Reason = "pack " + f.Pack + "'s source directory " + f.From + " could not be read (" + err.Error() +
			") — fix the pack's tree and launch again"
		return p
	}
	switch {
	case digest != "":
		p.Commit = digest
		return p
	case !declared:
		p.Reason = "pack " + f.Pack + " ships no " + f.From + " to build " + f.Bin + " from, and its `prebuilt` " +
			"declares no artifact for " + platform + " (it declares: " + have + ") — add that directory to the " +
			"pack, or a `prebuilt` entry for " + platform
		return p
	}
	f.PrebuiltWhy = "no " + f.From + " in the pack"
	f.Prebuilt = &pb
	p.Fork, p.Commit = f, pb.SHA256
	return p
}

// clonePrebuilts copies a `prebuilt` map, nil for none.
func clonePrebuilts(in map[string]packdecl.PrebuiltBuild) map[string]packdecl.PrebuiltBuild {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]packdecl.PrebuiltBuild, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// SourceDir is the absolute source directory a pack-tree build copies and digests: the user's
// checkout when one replaces From (Checkout), else From under the pack's root. "" for a fork.
func (f Fork) SourceDir() string {
	if !f.IsPackTree() {
		return ""
	}
	if f.Checkout != "" {
		return f.Checkout
	}
	return filepath.Join(f.Root, filepath.FromSlash(f.From))
}

// PackTreePrograms lists every pack-tree program the packs carry, in pack order and then
// declaration order. Root is each pack's root as loaded: at a launch, the staged tree nothing
// edits afterwards, which is the tree the digest must read (the plan's "don't digest the original
// pack directory").
func PackTreePrograms(packs []*Pack) []Fork {
	var out []Fork
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindProgram || c.Via != packdecl.ViaPack {
				continue
			}
			out = append(out, Fork{
				Pack: p.Name, Bin: c.Bin, Build: c.Build,
				Produces:  packdecl.PackTreeProduces(c.Bin),
				Platforms: append([]string(nil), c.Platforms...),
				Root:      p.Root, From: c.From, Run: c.Run,
				NodeFloor: c.NodeFloor,
				Prebuilts: clonePrebuilts(c.Prebuilt),
			})
		}
	}
	return out
}

// packTreeDigestSkip is what a pack-tree digest leaves out: the source directory's own `.git`,
// which a `file://` pack checked out in place carries and which reaches no build (BG-D3), and the
// pack store's completion marker, which a fetched pack's checkout keeps at its root and a launch's
// staging copies with it, so a `from: "."` source holds it. The build's staging copies the same
// set, so the build is handed exactly the digested bytes.
var packTreeDigestSkip = map[string]bool{".git": true, packsrc.TreeCompleteMarker: true}

// PackTreeDigestSkip is packTreeDigestSkip, copied, for the staging copy that must leave out
// exactly what the digest does.
func PackTreeDigestSkip() map[string]bool {
	out := make(map[string]bool, len(packTreeDigestSkip))
	for k, v := range packTreeDigestSkip {
		out[k] = v
	}
	return out
}

// PackTreeDigest is a pack-tree build's REVISION: treedigest's canonical digest of dir, the
// program's source directory, with its `.git` left out. An ABSENT directory is "" and no error:
// the manifest may name one the pack does not ship, and the resolver then turns to the prebuilt
// artifact (docs/design/pack-buildable-programs.md §4). A path that exists and is not a directory
// is an error, since nothing could be built from it, and so is a symlink: the walk follows no link
// (treedigest, internal/nofollow), the root included. dir's ancestors are yolo's or the user's own
// (a pack's staged tree, a build's staging) and are followed as the OS does; a dev override's
// checkout, whose ancestors an agent may write, is digested by Fork.Digest from / instead, and a
// pack's own source below the pack's root (BG-D23).
func PackTreeDigest(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return packTreeDigest(filepath.Dir(abs), abs)
}

// packTreeDigest is PackTreeDigest reached as SourceBase says: base's ancestors and base are followed
// as the OS does and nothing from base down to dir; an empty base is a checkout, reached from /.
func packTreeDigest(base, dir string) (string, error) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("the source directory %s is not a directory", dir)
	}
	if base == "" {
		return treedigest.OfCheckout(dir, packTreeDigestSkip)
	}
	return treedigest.OfBelow(base, dir, packTreeDigestSkip)
}

// Digest is PackTreeDigest of f's source directory reached from SourceBase, "" and no error for a
// fork.
func (f Fork) Digest() (string, error) {
	if !f.IsPackTree() {
		return "", nil
	}
	return packTreeDigest(f.SourceBase(), f.SourceDir())
}

// SourceBase is where SourceDir's path stops being the user's own, the TRUST ROOT its readers
// (Digest, the build's copy) reach it from (BG-D23): the pack's root, so a link inside the pack
// between its root and `from` is refused while the user's links above it (a moved state
// directory) are followed; "" for a dev override's checkout, any of whose ancestors an agent may
// write, which is reached from / following only root's links (BG-D22).
func (f Fork) SourceBase() string {
	if f.Checkout != "" {
		return ""
	}
	return f.Root
}
