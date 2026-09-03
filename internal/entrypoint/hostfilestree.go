package entrypoint

// hostfilestree.go renders a pack's `files` contribution into a REAL home — the host half
// of the kind whose jail half is a :ro bind mount.
//
// The refusal this replaces read "files binds a pack tree into a jail — nothing to bind into
// off-container", which is true of the MECHANISM and false of the INTENT. A pack that owns
// ~/.claude/file-suggestion.sh, pi's models.json, or a theme set is expressing "these files
// are mine to maintain", and off-container the way to honor that is to write the tree. The
// bind mount was never the point; it is how a jail gets an immutable copy.
//
// What does NOT carry over from the jail is the ownership posture. `files` is
// CombineExclusive, so in a jail the pack owns the path outright and the mount replaces
// whatever was there. In a real home that same claim cannot mean "overwrite what the user
// has": exclusivity is a rule about which PACK may claim a path, not a licence over the
// user's own files. So the render:
//
//   - writes a file that is absent, or that yolo wrote before (proved by the manifest);
//   - REFUSES a path the user owns, reporting it by name, and touches nothing;
//   - archives its own previous copy before replacing it, so an in-place edit is recoverable.
//
// This is the same discipline internal/hostskills applies to tier-B skills, for the same
// reason: a real $HOME is not a jail home, and being wrong about ownership must cost a `mv`
// back rather than the user's work.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// HostFilesRequest carries what a host `files` render needs beyond the pack itself: the
// ownership record and the archive, both of which belong to the caller (the CLI owns path
// layout; this package must stay pointed at whatever dirs it is given so tests can use
// temps).
type HostFilesRequest struct {
	// Manifest records which destinations yolo wrote, so a later apply can update its own
	// output without ever touching the user's. Required: without it every existing path
	// reads as the user's, which is safe but means yolo can never update its own file.
	Manifest *hostskills.Manifest
	// ArchiveRoot is where a replaced or retired copy is moved.
	ArchiveRoot hostskills.ArchiveRoot
	// Stamp names the archive generation, so one apply groups its moves together.
	Stamp string
}

// RenderHostFiles writes every `files` contribution of one pack into homeDir, returning one
// result per file considered. observe computes without writing.
//
// Per-FILE rather than per-tree, deliberately: ownership is a question about each path, and
// a tree where the user has edited one file must still deliver the other nine. A whole-tree
// verdict would either refuse everything over one file or overwrite the one the user cared
// about.
func RenderHostFiles(p *packload.Pack, homeDir string, req HostFilesRequest, observe bool) ([]HostRenderResult, error) {
	var out []HostRenderResult
	for _, c := range p.Decl.Contributions() {
		if c.Kind != packdecl.KindFiles || c.From == "" || c.Into == "" {
			continue
		}
		src := filepath.Join(p.Root, filepath.FromSlash(c.From))
		if _, err := os.Stat(src); err != nil {
			// A missing source is a pack-authoring problem (an `only`/`exclude` filter that
			// removed the tree, a typo'd `from`), not grounds to fail the whole apply. Warn
			// by name — the jail path treats this the same way.
			out = append(out, HostRenderResult{
				Surface: p.Name + "/files", Path: src,
				Action: "refused: source is missing from the pack (check `from` and any only/exclude filters)",
			})
			continue
		}
		destRoot := filepath.Join(homeDir, filepath.FromSlash(c.Into))
		files, err := relFilesUnder(src)
		if err != nil {
			return out, err
		}
		for _, rel := range files {
			// rel is "" for a single-FILE source, in which case `into` names the file
			// itself — matching the jail path, where packFilesMountArgs binds src onto
			// /home/agent/<into> directly. Diverging would make one pack.json deliver to
			// different paths at each notch.
			out = append(out, renderOneHostFile(p.Name,
				filepath.Join(src, rel), filepath.Join(destRoot, rel), req, observe))
		}
	}
	return out, nil
}

// renderOneHostFile is the per-path ownership decision plus the write.
func renderOneHostFile(pack, srcPath, dest string, req HostFilesRequest, observe bool) HostRenderResult {
	id := pack + "/files"
	res := HostRenderResult{Surface: id, Path: dest}

	_, statErr := os.Lstat(dest)
	occupied := statErr == nil
	owned := req.Manifest != nil && req.Manifest.OwnedBy(dest, pack)

	if occupied && !owned {
		// THE host-notch rule. `files` being sole-owned settles which PACK may claim the
		// path; it says nothing about a file the user put there first. Report and stop.
		res.Action = "refused: exists and yolo has no record of writing it — left untouched"
		if req.Manifest != nil {
			if owner, recorded := req.Manifest.Owner(dest); recorded {
				res.Action = fmt.Sprintf("refused: belongs to pack %q — left untouched", owner)
			}
		}
		return res
	}

	// ALREADY WHAT THE PACK SHIPS: say so and touch nothing. Without this the kind archived
	// its destination on EVERY apply — one timestamped copy per file per run, forever, of a
	// file byte-identical to its source (field finding F7: 10 spurious lines and 6.4M of
	// archive from an ordinary repeated apply).
	//
	// The cost was not disk. `archived to <path>` is the load-bearing safety signal at this
	// notch — it is what makes overwriting something in a real $HOME recoverable — so firing
	// it when nothing changed trains the reader to skip the one line that should stop them,
	// and buries a REAL archive among the noise. Compared BEFORE the observe branch so a
	// dry-run predicts the same no-op rather than promising a render it would not do.
	if occupied && owned && !hostskills.Changed(srcPath, dest) {
		res.Action = "unchanged"
		return res
	}

	// Past the two no-op branches above, the write is real: an absent path gets created, and an
	// owned-but-differing one gets replaced. So the change predicate is simply "we got here"
	// (HostRenderResult.WouldChange) — the content comparison that decides it is
	// hostskills.Changed, three lines up, and it is the same digest the archive gate reads.
	res.WouldChange = true

	if observe {
		res.Action = "would render"
		return res
	}

	if occupied {
		// Ours from a previous apply AND genuinely different: archive before replacing, so an
		// edit the user made to a yolo-written file survives as a recoverable copy rather than
		// being lost to a silent overwrite.
		if at, err := hostskills.Archive(req.ArchiveRoot, req.Stamp, pack, dest); err == nil {
			res.Action = "rendered (previous copy archived to " + at + ")"
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		// A refusal is not a pending change, whatever the predicate concluded before the
		// write was attempted — see HostRenderResult.WouldChange's last paragraph.
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res
	}
	mode, err := hostFileTreeMode(srcPath)
	if err != nil {
		// A refusal is not a pending change, whatever the predicate concluded before the
		// write was attempted — see HostRenderResult.WouldChange's last paragraph.
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		// A refusal is not a pending change, whatever the predicate concluded before the
		// write was attempted — see HostRenderResult.WouldChange's last paragraph.
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res
	}
	// A prior render left the file read-only (see hostFileTreeMode), and a non-root user
	// cannot reopen a 0o444 file for writing — restore a writable mode first, exactly as the
	// host_files readonly path does.
	if occupied {
		_ = os.Chmod(dest, 0o644)
	}
	if err := os.WriteFile(dest, data, mode); err != nil {
		// A refusal is not a pending change, whatever the predicate concluded before the
		// write was attempted — see HostRenderResult.WouldChange's last paragraph.
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res
	}
	if err := os.Chmod(dest, mode); err != nil {
		// A refusal is not a pending change, whatever the predicate concluded before the
		// write was attempted — see HostRenderResult.WouldChange's last paragraph.
		res.Action, res.WouldChange = "refused: "+err.Error(), false
		return res
	}
	if req.Manifest != nil {
		req.Manifest.Record(dest, pack)
	}
	if res.Action == "" {
		res.Action = "rendered"
	}
	return res
}

// hostFileTreeMode returns the mode a delivered file gets: read-only, and executable when
// the pack's copy is.
//
// READ-ONLY (0o444/0o555) mirrors the jail's `:ro` mount, which is the closest a plain
// filesystem gets to the same statement — "this file is yolo's; edit the pack, not this".
// It is weaker than a mount (a `chmod` defeats it), so it is a signal rather than an
// enforcement, and the archive-before-replace above is what makes ignoring the signal
// non-destructive.
//
// The exec bit comes from the pack's staged copy and is honored as found. It used to be
// justified by a staging-time gate (a consumer's `allow_exec` opt-in) that no longer
// exists; the behavior is unchanged and the reason is now simpler — a pack that ships a
// script ships it runnable, which is the fzf-script case and every skill that tells an
// agent to run its own tool. What a pack may not do is put that file on PATH, which is
// refused at the manifest (packdecl.appendJailPathProblems) rather than by mode bits.
func hostFileTreeMode(srcPath string) (os.FileMode, error) {
	fi, err := os.Stat(srcPath)
	if err != nil {
		return 0, err
	}
	if fi.Mode().Perm()&0o111 != 0 {
		return 0o555, nil
	}
	return 0o444, nil
}

// relFilesUnder lists the regular files under root, as slash-separated relative paths,
// sorted for deterministic output.
//
// A single-FILE source yields ONE EMPTY relative path, so the caller joins nothing and the
// contribution's `into` names the file itself — the same meaning the jail gives it.
func relFilesUnder(root string) ([]string, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return []string{""}, nil
	}
	var out []string
	err = filepath.Walk(root, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
