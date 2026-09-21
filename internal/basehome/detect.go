package basehome

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxDepth bounds the walk. Symlinks are never followed, so there is no cycle to hit; the
// bound is for a pathological tree, and hitting it makes the enclosing directory opaque
// (never a candidate) rather than half-examined.
const maxDepth = 64

// Entry is one candidate: a path the apply would move.
type Entry struct {
	// Root is the home-relative walk root this entry was found under — a state dir name.
	// It is the ONLY part of an entry the one-line disclosure may print (§5.8).
	Root string
	// Rel is the home-relative path. It can encode a workspace name
	// (`.claude/projects/-home-user-code-thing`), which is why the summary never prints it
	// and the manifest, printed by an opt-in verb, is where it belongs.
	Rel string
	// Class is Runtime, or Unclassified when the tree could not be read.
	Class Class
	// Dir reports a WHOLE directory moving as one rename — §5.2's granularity rule: a
	// directory with no kept leaf beneath it.
	Dir bool
	// Bytes is the candidate byte total, from Lstat sizes only. No file is opened.
	Bytes int64
	// Leaves is how many leaves this entry covers (1 for a leaf, the subtree count for a
	// whole directory). An empty directory covers none and still moves whole.
	Leaves int
	// Note says why an entry is Unclassified. Empty otherwise.
	Note string
}

// readDir is the one directory-listing seam in this package, a package var so a test can
// make a listing FAIL.
//
// IT EXISTS BECAUSE THE FAILURE PATHS ARE THE INTERESTING ONES and the filesystem will not
// produce them on demand: the two invariants a failed listing decides — an unreadable root is
// refused rather than proposed (§6), and a failed stat costs a leaf its SIZE rather than its
// CLASS (P5) — are unreachable from a fixture running as uid 0, where a mode-0000 directory
// lists happily (MEASURED 2026-09-21 in this jail). This is not a fixture asserting a property
// of the OS; it is a stub for one call so the package's own branch can be exercised.
var readDir = os.ReadDir

// RootProblem is a walk root the design REFUSES: a symlink (following it would sweep the
// user's real ~/.claude) or a regular file (archiving it would break the next launch's
// mountpoint). Refused, not classified, and disclosed.
type RootProblem struct {
	Root   string
	Reason string
}

// Report is what one detection pass saw. It is data only: nothing here can move a byte.
type Report struct {
	// Roots are the roots actually walked, home-relative and sorted.
	Roots []string
	// Candidates are the entries the apply would move, in walk order.
	Candidates []Entry
	// Refused are the roots refused by the root constraint.
	Refused []RootProblem
	// Excluded are roots dropped because they are machine-scope shared dirs — rule 1
	// enforced at root admission, so the structural claim holds even for a caller that
	// hands the walk a credential dir on purpose.
	Excluded []string
	// Kept counts the CREDENTIAL/CONFIG/CONTENT leaves left in place, for the ratio the
	// design's taxonomy verification wants.
	Kept int
	// Problems are the declaration-side degradations (Decls.Problems, plus anything the
	// walk learned about the declarations).
	Problems []string
}

// Detect walks the base home and reports what the apply WOULD move. It moves nothing,
// opens nothing, follows nothing, and never returns an error: §5.1 says detection never
// aborts, so every failure becomes a reported fact instead of a return path that a caller
// could turn into a launch refusal.
//
// globalHome is passed rather than resolved so the whole walk is testable over a fixture
// with no environment (the shape internal/prune.PruneShadowedHome uses).
func Detect(globalHome string, d Decls) Report {
	rep := Report{Problems: append([]string(nil), d.Problems...)}
	if globalHome == "" {
		rep.Problems = append(rep.Problems, "no base home to walk")
		return rep
	}

	roots, excluded, unknown := d.roots(globalHome)
	rep.Excluded = excluded
	if len(unknown) > 0 {
		// THE LIMIT OF THE CLASSIFICATION, reported where it bites rather than left for a
		// reader to infer. Decls is derived from the SHIPPED packs, so a top-level dir that
		// no shipped pack and no core list declares is walked with none of its own
		// declarations in hand: its config surfaces and any credential it holds are
		// invisible, and every leaf in it therefore classifies RUNTIME.
		//
		// Harmless while this build only observes. It is a HARD PRECONDITION for the move:
		// two known sources put such a dir in the base — a config `writable_home_dirs`
		// entry (§5.1's second bullet, unimplemented here because the config is not loaded
		// at this trigger) and a selected non-shipped pack's state dir.
		rep.Problems = append(rep.Problems, "classified without their own declarations "+
			"(derived from shipped packs only), so every leaf in them reads as runtime: "+
			strings.Join(unknown, " "))
	}

	for _, rel := range roots {
		info, err := os.Lstat(filepath.Join(globalHome, rel))
		switch {
		case err != nil && os.IsNotExist(err):
			// A missing root is a no-op: the mountpoint simply has not been provisioned.
			continue
		case err != nil:
			rep.Refused = append(rep.Refused, RootProblem{Root: rel, Reason: "could not be read"})
			continue
		case info.Mode()&fs.ModeSymlink != 0:
			rep.Refused = append(rep.Refused, RootProblem{Root: rel, Reason: "is a symlink, so it is not followed"})
			continue
		case !info.IsDir():
			rep.Refused = append(rep.Refused, RootProblem{Root: rel, Reason: "is not a directory"})
			continue
		}
		// A root that cannot be LISTED is refused here, and this is not the same check as
		// the Lstat above: a directory can be perfectly stat-able and still deny ReadDir
		// (mode 0600, or a root-owned leftover from prior container UID mapping).
		//
		// IT MUST NOT FALL THROUGH TO scan. §5.1 says an unreadable ENTRY is a reported
		// candidate, and scan implements exactly that — but at depth 0 the entry IS the
		// root, so the general rule would emit a whole-directory candidate naming a walk
		// root. §6 forbids that: the OCI runtime cannot mkdir inside the :ro base, so a
		// later apply acting on it would rename a mountpoint the next podman launch cannot
		// recreate. Two rules meet here and the narrower one wins.
		if _, err := readDir(filepath.Join(globalHome, rel)); err != nil {
			rep.Refused = append(rep.Refused, RootProblem{Root: rel, Reason: "could not be listed"})
			continue
		}

		w := &walk{home: globalHome, decls: d}
		res := w.scan(rel, 0)
		for i := range res.cands {
			res.cands[i].Root = rel
		}
		rep.Roots = append(rep.Roots, rel)
		rep.Candidates = append(rep.Candidates, res.cands...)
		rep.Kept += res.kept
	}
	return rep
}

// roots resolves §5.1's root set, and enforces rule 1 at admission: a machine-scope
// shared dir is never a root, whoever named it.
//
// §5.1's SECOND bullet — the base mountpoints a config `writable_home_dirs` entry creates
// — IS NOT IMPLEMENTED, and cannot be where the design puts the trigger: the config is not
// loaded at either host call site yet (ensureStorage runs before loadAndValidateConfig,
// and check's EnsureGlobalStorage runs long before config.LoadConfig), so
// config.WritableHomeDirs has no cfg to read. It is mostly subsumed rather than missing: a
// TOP-LEVEL writable_home_dirs mountpoint is reached by the third bullet, because an
// unknown top-level directory is exactly what one looks like from here (measured — a live
// host's `.pi-lens` is found this way). A NESTED entry is still invisible. Closing it means
// moving the trigger after the config load at both sites.
func (d Decls) roots(globalHome string) (roots, excluded, unknown []string) {
	seen := map[string]bool{}
	add := func(rel string) {
		rel = normRel(rel)
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		if d.underSharedDir(rel) {
			excluded = append(excluded, rel)
			return
		}
		roots = append(roots, rel)
	}
	for _, s := range d.StateDirs {
		add(s)
	}

	if d.SweepUnknownTopLevel {
		known := map[string]bool{}
		for _, list := range [][]string{d.StateDirs, d.SharedDirs, d.NonPackDirs} {
			for _, p := range list {
				// The TOP-LEVEL segment: `.config/git` is core's for what it provisions,
				// and `.config` is what a top-level walk sees.
				if rel := normRel(p); rel != "" {
					known[firstSegment(rel)] = true
				}
			}
		}
		ents, err := readDir(globalHome)
		if err != nil {
			// Not fatal and not a refusal: the third bullet simply contributes nothing.
			ents = nil
		}
		for _, e := range ents {
			// IsDir is false for a symlink to a directory, which is the wanted answer:
			// an unknown top-level SYMLINK is not a mountpoint yolo lost track of, so it
			// is passed over silently rather than refused. A DECLARED root that is a
			// symlink is a different fact and is refused loudly, above.
			if !e.IsDir() || known[e.Name()] {
				continue
			}
			before := len(roots)
			add(e.Name())
			if len(roots) > before {
				unknown = append(unknown, e.Name())
			}
		}
	}

	sort.Strings(roots)
	sort.Strings(excluded)
	sort.Strings(unknown)
	return roots, excluded, unknown
}

func firstSegment(rel string) string {
	if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
		return rel[:i]
	}
	return rel
}

type walk struct {
	home  string
	decls Decls
}

// scanResult is what one directory holds, from the apply's point of view.
type scanResult struct {
	cands []Entry
	// kept counts kept leaves and kept subtrees beneath. ZERO is what licenses the
	// whole-directory collapse, so it must count a kept SUBTREE (a declared content dir
	// left in place) as well as a kept leaf.
	kept int
	// bytes and leaves are CANDIDATE totals, not the directory's size.
	bytes  int64
	leaves int
	// opaque means something beneath could not be read. It vetoes the collapse: an
	// unreadable entry has to be named on its own, and a directory containing one cannot
	// honestly be reported as "no kept leaf beneath it".
	opaque bool
}

func (w *walk) scan(rel string, depth int) scanResult {
	var res scanResult
	ents, err := readDir(filepath.Join(w.home, rel))
	if err != nil {
		// §5.1: an unreadable entry is counted as a candidate and reported. It is NOT
		// silently skipped (the apply would then think it had emptied the dir) and NOT an
		// abort.
		res.opaque = true
		res.cands = append(res.cands, Entry{Rel: rel, Class: Unclassified, Dir: true, Note: "unreadable"})
		return res
	}

	for _, e := range ents {
		childRel := filepath.Join(rel, e.Name())

		// Rule 1 at the DESCEND decision. It is deliberately redundant with Classify's
		// first rule, which answers CREDENTIAL for the same set — MEASURED: delete this
		// guard and no fixture changes its outcome, because a directory the classifier
		// keeps is never descended either.
		//
		// The redundancy is the point, and it is cheap: rule 1 being FIRST is what makes
		// the classifier agree, and a later reordering (a new rule consulted before the
		// shared tier, say) would open the descent with nothing in its way. The claim
		// "the walk must never descend into one" belongs to the walk, so the walk states
		// it. Where the exclusion IS observable, and tested, is root admission (roots()),
		// and TestTheSharedTierWinsOverEveryLaterRule pins the ordering this backs up.
		if w.decls.underSharedDir(childRel) {
			res.kept++
			continue
		}

		if e.IsDir() {
			class := w.decls.Classify(childRel)
			if !class.Candidate() {
				// A declared subtree owner — a directory config surface or a content
				// destination such as `.claude/skills`. Kept whole, not descended.
				res.kept++
				continue
			}
			if depth+1 >= maxDepth {
				res.opaque = true
				res.cands = append(res.cands, Entry{Rel: childRel, Class: Unclassified, Dir: true, Note: "nested too deeply to examine"})
				continue
			}
			sub := w.scan(childRel, depth+1)
			res.kept += sub.kept
			res.bytes += sub.bytes
			res.leaves += sub.leaves
			if sub.opaque {
				res.opaque = true
			}
			if sub.kept == 0 && !sub.opaque && !w.decls.ProvisionedDir(childRel) {
				// §5.2's granularity rule: no kept leaf beneath, so the directory is ONE
				// candidate and moves in one rename. An empty directory lands here too,
				// which is the design's "empty directories move whole" — EXCEPT for a
				// directory core provisions, which is a mountpoint and is never renamed
				// (Decls.ProvisionedDir). Its contents still move.
				res.cands = append(res.cands, Entry{Rel: childRel, Class: Runtime, Dir: true, Bytes: sub.bytes, Leaves: sub.leaves})
				continue
			}
			// Mixed: descend and take only the runtime leaves, leaving the kept ones.
			res.cands = append(res.cands, sub.cands...)
			continue
		}

		// A leaf: a regular file, a symlink, or anything else that is not a directory.
		class := w.decls.Classify(childRel)
		note := ""
		var size int64
		if info, err := e.Info(); err == nil {
			// Lstat size. A symlink's size is the length of its target, which is the
			// honest answer for a link that moves as itself.
			size = info.Size()
		} else if !os.IsNotExist(err) {
			// It is there and cannot be stat'd. THE CLASS IS UNAFFECTED: Classify is a
			// function of the PATH and the declarations, so a failed stat costs the SIZE
			// and nothing else. Overwriting the class here — which this did until
			// 2026-09-21 — inverted P5 for exactly the files P5 exists to protect: a
			// declared credential whose Info() failed became Unclassified, and
			// Unclassified means RUNTIME means archive. A stat error is not evidence
			// against a declaration.
			note = "size unknown"
			// Only a CANDIDATE leaf blocks its parent's collapse. A kept leaf already
			// blocks it by being kept, and marking the parent opaque on a kept leaf would
			// suppress a legitimate whole-directory candidate elsewhere in the tree.
			if class.Candidate() {
				note = "unreadable"
				res.opaque = true
			}
		} else {
			// It vanished between ReadDir and Info. Nothing to move.
			continue
		}

		if e.Type()&fs.ModeSymlink != 0 && class.Candidate() {
			// §5.1: "a link into a machine-scope credential dir is a CREDENTIAL". Readlink
			// is not following — the target is read as a STRING and never traversed — and
			// this is the one rule the base home actually needs: EnsureGlobalStorage
			// removes the old `.claude/.credentials.json` only when it is a regular file,
			// so a host whose copy is a symlink into the shared dir still has one.
			if target, err := os.Readlink(filepath.Join(w.home, childRel)); err == nil {
				if tRel, ok := w.linkTargetRel(childRel, target); ok && !w.decls.Classify(tRel).Candidate() {
					res.kept++
					continue
				}
			}
		}

		if !class.Candidate() {
			res.kept++
			continue
		}
		res.bytes += size
		res.leaves++
		res.cands = append(res.cands, Entry{Rel: childRel, Class: class, Bytes: size, Leaves: 1, Note: note})
	}
	return res
}

// linkTargetRel makes a symlink's target home-relative WITHOUT resolving it. A relative
// target is joined against the link's own directory (the semantics the kernel would use);
// an absolute target is made relative to the home. A target outside the home has no
// home-relative spelling and reports false — §5.1's "a stale link elsewhere is RUNTIME
// and the link itself moves".
func (w *walk) linkTargetRel(linkRel, target string) (string, bool) {
	var abs string
	if filepath.IsAbs(target) {
		abs = filepath.Clean(target)
	} else {
		abs = filepath.Join(w.home, filepath.Dir(linkRel), target)
	}
	rel, err := filepath.Rel(w.home, abs)
	if err != nil {
		return "", false
	}
	rel = normRel(rel)
	return rel, rel != ""
}
