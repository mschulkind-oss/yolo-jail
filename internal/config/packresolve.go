package config

// packresolve.go is THE PACK RESOLVER: one function from a `packs` entry to a loaded pack, which
// every notch and every verb calls (docs/plans/notch-convergence.md item 5, rows B3, B6 and B7).
//
// There were eight. The launch staged configured packs through packstage.Stage and copied the
// embedded ones out of a scratch materialization of its own; the host read a local pack's files
// in place, staging only a fetched or filtered one and then pointing Root back at the source; the
// footer, `yolo check`, config validation, the lazy loophole resolver and UseProfileCLINames each
// spelled a third variant. So an entry's `exclude` removed a skill from every jail and delivered
// it to the real home, a local pack's escaping symlink was refused by the launch and rendered by
// `yolo host apply`, and an embedded pack's `only`/`exclude` was ignored everywhere.
//
// TWO MODES, chosen by whether the caller names a destination:
//
//   - STAGE (Spec.Dest set): the pack is staged into Dest through packstage.Stage — its
//     filters and its no-escape rule — and loaded from that copy, so Pack.Root names the staged
//     tree and every file a caller reads is one the filters kept. The launch passes its jail's
//     tree; a host verb reading a FILTERED entry passes a directory of the process pack tree
//     (packload.ProcessPackDir), which lives for the verb (ResolvePackForProcess).
//   - DECLARATION (no Dest): only the pack's declaration is read. A filtered entry is staged into
//     a temp dir removed before this returns, because the filters can drop the manifest itself;
//     an unfiltered one is read in place after packstage.Check applies the same no-escape rule
//     without a copy. A filtered entry's Pack.Root is then not for reading files; an unfiltered
//     one's is the pack's own tree, which is how ResolvePackForProcess reads it.
//
// FILTERS ALWAYS APPLY, embedded packs included: `{"source": "claude", "exclude": [...]}` used
// to be accepted and ignored at every notch.
//
// THE EMBEDDED PACKS COME FROM ONE MATERIALIZATION, packload.Embedded(), at every notch. The launch
// used to materialize its own scratch copy per launch, and treated a problem there as fatal, while
// the host read Embedded() and took its empty answer on a problem as "this build ships no pack by
// that name" — a yolo bug read as the user's typo. Both now ask EmbeddedProblems first.
//
// ReadOnlyStore is orthogonal to the mode, and each caller keeps the store posture it had: a
// verb that may check a fetched commit out of the local mirror (a launch, a host verb) resolves
// with Store.Resolve, and one that must write nothing (`yolo check`, config validation, the
// footer) with Store.ResolveExisting. Neither ever fetches.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ResolvePackSpec is how one caller wants an entry resolved.
type ResolvePackSpec struct {
	// Dest selects STAGE mode: the directory the pack is staged into and loaded from. Its
	// contents are cleared (packstage's rule). Empty selects DECLARATION mode.
	Dest string
	// ReadOnlyStore resolves a fetched pack with packsrc.Store.ResolveExisting, which writes
	// nothing into the pack store, instead of Store.Resolve, which may check a commit out of the
	// local mirror.
	ReadOnlyStore bool
	// Getenv is threaded to the pack store, whose staged-tree fallback reads YOLO_PACK_ROOT (how
	// a nested launch resolves the local packs its outer launch delivered). Nil reads the real
	// environment.
	Getenv func(string) string
}

// ResolvedPack is one entry the resolver loaded.
type ResolvedPack struct {
	// Pack is the loaded pack, nil when packload.LoadDir could not load one (Problems says why).
	Pack *packload.Pack
	// Problems are LoadDir's problems over the tree the filters leave, each prefixed
	// "pack <name>: ". A launch refuses a pack that has any; each caller keeps its own
	// disposition.
	Problems []string
	// Staged is what the filters kept and dropped: the copy's in STAGE mode, the check's in
	// DECLARATION mode. Nil only for an embedded pack read in place (unfiltered, declaration).
	Staged *packstage.Result
	// StagedFrom is packsrc.Resolved.StagedFrom: the delivered tree a nested resolution fell back
	// to, "" otherwise.
	StagedFrom string
}

// PackResolveError is a resolution the resolver could not complete: the address, the store, the
// embedded packs, or a staging refusal (an escaping symlink). Its text is "packs: <name>: <why>",
// the spelling every caller printed before there was one resolver.
type PackResolveError struct {
	Name string
	Err  error
}

func (e *PackResolveError) Error() string { return "packs: " + e.Name + ": " + e.Err.Error() }
func (e *PackResolveError) Unwrap() error { return e.Err }

// ResolvePack resolves one `packs` entry by the one rule every notch shares. See the file doc.
func ResolvePack(entry PackEntry, spec ResolvePackSpec) (ResolvedPack, error) {
	fail := func(err error) (ResolvedPack, error) {
		return ResolvedPack{}, &PackResolveError{Name: entry.Name, Err: err}
	}
	filtered := len(entry.Only) > 0 || len(entry.Exclude) > 0
	var out ResolvedPack
	var root string
	if entry.Embedded() {
		p, err := embeddedPackNamed(entry.Name)
		if err != nil {
			return fail(err)
		}
		if spec.Dest == "" && !filtered {
			// The one materialization, read in place: it is yolo's own immutable tree, leased for
			// the process, with nothing to filter and nothing a no-escape check could find.
			return ResolvedPack{Pack: p}, nil
		}
		root = p.Root
	} else {
		addr, err := packsrc.Parse(entry.Source)
		if err != nil {
			return fail(err)
		}
		store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: spec.Getenv}
		var res *packsrc.Resolved
		if spec.ReadOnlyStore {
			res, err = store.ResolveExisting(addr, entry.Slug())
		} else {
			res, err = store.Resolve(addr, entry.Slug())
		}
		if err != nil {
			return fail(err)
		}
		root, out.StagedFrom = res.Root, res.StagedFrom
	}

	stage := packstage.Spec{Root: root, Only: entry.Only, Exclude: entry.Exclude,
		FollowSymlinks: followsSymlinks(entry)}
	loadFrom := root
	var err error
	switch {
	case spec.Dest != "":
		stage.Dest = spec.Dest
		out.Staged, err = packstage.Stage(stage)
		loadFrom = spec.Dest
	case filtered:
		// The filters can drop the manifest itself, or a file whose presence is a problem, so the
		// declaration is the filtered copy's — deleted before return, since only the declaration
		// is wanted. <tmp>/<slug>, the shape a launch stages, so the loaded pack's StagedSlug
		// (the base name of its Root) reads the same here as there.
		tmp, terr := os.MkdirTemp("", "yolo-pack-declaration-")
		if terr != nil {
			return fail(terr)
		}
		defer os.RemoveAll(tmp)
		stage.Dest = filepath.Join(tmp, entry.Slug())
		out.Staged, err = packstage.Stage(stage)
		loadFrom = stage.Dest
	default:
		out.Staged, err = packstage.Check(stage)
	}
	if err != nil {
		return fail(err)
	}
	out.Pack, out.Problems = packload.LoadDir(loadFrom, entry.Name)
	if out.Pack != nil && loadFrom != root {
		out.Pack.SourceRoot = root
	}
	if out.Pack != nil {
		// A staged or filtered copy of an embedded pack is still yolo's own
		// (packload.Pack.Official); nothing else is. A file:// entry, the conventional local pack
		// included, is content at a path on this machine (packload.Pack.Local), and a staged or
		// filtered copy of it still is; a git+file:// entry is a fetched pack, whatever its
		// repository's path. The two decide whether a launch runs the pack's host code
		// (packload.Pack.MayRunHostHalf; docs/design/host-notch-services.md HS-D27).
		out.Pack.Official = entry.Embedded()
		out.Pack.Local = entry.IsLocal() && !entry.Embedded()
	}
	return out, nil
}

// followsSymlinks reports whether entry's pack is staged with packstage.Spec.FollowSymlinks: every
// symlink followed wherever it points, and a linked directory walked as one, instead of refusing a
// link that leaves the pack. It is the pack's ORIGIN that decides, never the caller, so every
// notch and every verb gives one answer (docs/plans/notch-convergence.md OQ-NC9, ruled A,
// 2026-09-28):
//
//   - A LOCAL pack (a file:// entry, the conventional local pack included) is FOLLOWED, filtered
//     or not. It is a directory the user named in their own user config, which `packs` alone can
//     say (workspace scope is inexpressible, packs.go's header), and OQ-TP9
//     (docs/design/trust-paths.md) already gives that act full trust. A dotfile manager (rcm,
//     stow, chezmoi) deploys exactly this shape: the pack's files, or whole directories of it, are
//     links into the dotfiles repo. The launch refused such a pack while `yolo host apply`
//     delivered it; the jail's staged copy holds the links' targets as plain files.
//   - A FETCHED pack is NOT followed, and neither is an embedded one: packstage's no-escape rule
//     refuses an escaping link at every notch, because the threat it was written for is someone
//     else's repository smuggling a host file into a jail or a real home.
//
// This replaced an input each caller set (FollowLocalSymlinks), which kept the host's reading and
// the launch's refusal apart. It is not the workspace skills reader's rule and does not reach it:
// that tree is agent-editable, and jailcontent's confined reader never follows a link.
func followsSymlinks(entry PackEntry) bool {
	return entry.IsLocal() && !entry.Embedded()
}

// ResolvePackForProcess resolves an entry for a caller with no tree of its own — the host verbs
// and the lazy read-only resolvers — into a Pack whose Root the caller may READ FILES from, every
// one of them a file the entry's filters kept and packstage's no-escape rule passed. spec.Dest is
// ignored.
//
// ONLY A FILTERED ENTRY IS COPIED, into a new directory of this process's pack tree
// (packload.ProcessPackDir), readable until the process releases its packs
// (packload.ReleaseEmbedded). An UNFILTERED one is read in place, in DECLARATION mode — the
// embedded packs' one materialization, a local pack's own directory, the pack store's checkout —
// after packstage.Check applied the no-escape rule, because a copy of it holds exactly the same
// files. That is what the host read before there was one resolver, and it matters beyond the copy's
// cost (paid on every host verb and every `yolo host --` launch): a loophole module's directory is
// where a host-scope daemon spawned by `yolo host-daemon start` resolves {loophole_dir}, and that
// daemon outlives the verb whose process tree would hold the copy.
func ResolvePackForProcess(entry PackEntry, spec ResolvePackSpec) (ResolvedPack, error) {
	spec.Dest = ""
	if len(entry.Only) == 0 && len(entry.Exclude) == 0 {
		return ResolvePack(entry, spec)
	}
	dir, err := packload.ProcessPackDir(entry.Slug())
	if err != nil {
		return ResolvedPack{}, &PackResolveError{Name: entry.Name, Err: err}
	}
	spec.Dest = dir
	return ResolvePack(entry, spec)
}

// EmbeddedPackEntry is the entry a bare `packs: ["<name>"]` lowers to, for a caller that selects an
// embedded pack no config line named: the selection closure's additions (`needs`, `via`).
func EmbeddedPackEntry(name string) PackEntry {
	return PackEntry{Source: embeddedSourceFor(name), Name: name, IsEmbedded: true}
}

// embeddedPackNamed is the embedded pack of this name from the one materialization, or why there
// is none. A materialization problem is reported AS ONE — a yolo bug — before the lookup, because
// on a problem Embedded() answers with an empty set, and "this build ships no pack by that name"
// would then send the user to fix a config line that is right.
func embeddedPackNamed(name string) (*packload.Pack, error) {
	if probs := packload.EmbeddedProblems(); len(probs) > 0 {
		return nil, fmt.Errorf("the packs this build of yolo ships could not be loaded (a yolo "+
			"bug, not your config): %s", strings.Join(probs, "; "))
	}
	for _, p := range packload.Embedded() {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, errors.New("this build of yolo ships no pack by that name")
}
