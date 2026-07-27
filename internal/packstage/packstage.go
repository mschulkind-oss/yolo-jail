// Package packstage stages a pack's file tree for delivery into a jail (C2).
//
// It is the executor half of the `packs` feature: config resolves WHAT to stage
// (internal/config.PackEntry), this walks a pack directory and copies the selected
// files into a staging dir the CLI then mounts read-only.
//
// It lives in its own package, with no dependency on internal/config or
// internal/agents, for two reasons: the tree walk is the part most worth unit
// testing in isolation (globs, symlinks, the exec-bit refusal), and keeping it
// dependency-free means it can be reused by `yolo pack lint` host-side without
// dragging the config loader in.
//
// THREE RULES, each chosen because the alternative fails quietly:
//
//  1. EXEC-BIT REFUSAL. A file carrying any execute bit is refused unless the entry
//     sets allow_exec. A pack is CONTENT — skills, prose, config fragments — and an
//     executable arriving through a content channel is a materially different trust
//     question. Refusing is an ERROR, not a skip: silently dropping the one file a
//     pack author cared about is worse than failing.
//  2. NO ESCAPE. A symlink pointing outside the pack root is refused rather than
//     dereferenced. A pack is fetched from someone else's repo, so `ln -s
//     ~/.ssh/id_ed25519 skills/innocuous.md` must not exfiltrate a key into a
//     mounted tree. (Skills staging DOES dereference symlinks, deliberately, because
//     its source is the user's OWN home — different source, different rule.)
//  3. CLEAR CONTENTS, NEVER THE DIR. A running jail's bind mount captured the
//     staging dir's inode; recreating it silently detaches the mount. Same invariant
//     PrepareSkills documents.
package packstage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Spec is what the executor needs to stage one pack: where the tree is, where it
// goes, and the filters.
type Spec struct {
	// Root is the pack directory on the host (already fetched).
	Root string
	// Dest is the staging directory to populate. Its CONTENTS are cleared; the dir
	// itself is preserved (rule 3).
	Dest string
	// Only and Exclude are slash-separated globs matched against each file's
	// pack-relative path, applied in that order. Empty Only means "everything".
	Only    []string
	Exclude []string
	// AllowExec permits staging files with an execute bit (rule 1).
	AllowExec bool
}

// Result reports what a Stage call did, for `yolo pack ls`/`lint` and for the
// no-silent-caps rule: a pack whose filters excluded everything is a likely
// authoring mistake, and the caller can only say so if it knows the counts.
type Result struct {
	// Staged is the pack-relative path of every file copied, sorted.
	Staged []string
	// Excluded is the pack-relative path of every file a filter rejected, sorted.
	// Reported rather than dropped so `only`/`exclude` typos are diagnosable.
	Excluded []string
}

// Stage copies the selected files from spec.Root into spec.Dest.
//
// Returns an error on a refused file (exec bit, escaping symlink) rather than
// skipping it: those are authoring or trust problems the user must see, and under
// the A12 fatal-generator policy the caller turns them into a halt.
func Stage(spec Spec) (*Result, error) {
	rootAbs, err := filepath.Abs(spec.Root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("pack root %s: %w", spec.Root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("pack root %s is not a directory", spec.Root)
	}
	if err := os.MkdirAll(spec.Dest, 0o755); err != nil {
		return nil, err
	}
	if err := clearContents(spec.Dest); err != nil {
		return nil, err
	}

	res := &Result{}
	walkErr := filepath.Walk(rootAbs, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		// Skip the pack's own VCS metadata: it is never content, and copying it
		// would put a whole second .git tree inside the jail.
		if fi.IsDir() && (rel == ".git" || strings.HasSuffix(rel, "/.git")) {
			return filepath.SkipDir
		}
		if fi.IsDir() {
			return nil // directories are created lazily, per staged file
		}

		// Rule 2: a symlink is inspected, never followed blindly.
		if fi.Mode()&os.ModeSymlink != 0 {
			ok, terr := targetInsideRoot(rootAbs, path)
			if terr != nil {
				return fmt.Errorf("pack file %s: %w", rel, terr)
			}
			if !ok {
				return fmt.Errorf("pack file %s is a symlink pointing outside the pack "+
					"(refusing: a pack comes from someone else's repo, so an escaping "+
					"symlink could stage a host secret into the jail)", rel)
			}
			// An in-pack symlink is resolved so the staged tree is plain files —
			// the jail must not depend on the pack's internal link layout.
			resolved, rerr := os.Stat(path)
			if rerr != nil {
				return fmt.Errorf("pack file %s: %w", rel, rerr)
			}
			if resolved.IsDir() {
				return nil
			}
			fi = resolved
		}

		if !matches(rel, spec.Only, spec.Exclude) {
			res.Excluded = append(res.Excluded, rel)
			return nil
		}
		// Rule 1: exec bit.
		if fi.Mode().Perm()&0o111 != 0 && !spec.AllowExec {
			return fmt.Errorf("pack file %s is executable (mode %o) but the pack does "+
				"not set \"allow_exec\": true — a pack is content, so shipping an "+
				"executable is an explicit opt-in", rel, fi.Mode().Perm())
		}
		if err := copyFile(path, filepath.Join(spec.Dest, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("staging %s: %w", rel, err)
		}
		res.Staged = append(res.Staged, rel)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(res.Staged)
	sort.Strings(res.Excluded)
	return res, nil
}

// matches applies the only/exclude globs to a pack-relative path.
//
// A glob matches if it matches the whole path OR any leading directory prefix of
// it, so `only: ["skills/rust-*"]` takes the entire rust-* skill DIRECTORY rather
// than only files literally named that. That is what a user means, and the literal
// reading would silently stage nothing.
func matches(rel string, only, exclude []string) bool {
	if len(only) > 0 && !matchAny(rel, only) {
		return false
	}
	return !matchAny(rel, exclude)
}

func matchAny(rel string, globs []string) bool {
	for _, g := range globs {
		g = strings.Trim(g, "/")
		if globMatch(g, rel) {
			return true
		}
		// Prefix match: the glob names a directory containing rel.
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			if globMatch(g, strings.Join(parts[:i], "/")) {
				return true
			}
		}
	}
	return false
}

// globMatch is filepath.Match with a malformed pattern treated as no-match rather
// than as an error, so one bad glob cannot fail an entire stage. `yolo pack lint`
// is where a bad pattern gets reported.
func globMatch(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}

// targetInsideRoot reports whether a symlink at path resolves inside root. Uses
// EvalSymlinks so a chain of links cannot smuggle an escape past a single check.
func targetInsideRoot(root, path string) (bool, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// A dangling link stages nothing useful and is very likely a mistake; treat
		// it as outside rather than guessing.
		return false, nil
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// clearContents removes every entry INSIDE dir, leaving dir itself intact (rule 3).
func clearContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies src to dst, creating parent dirs. Mode is forced to 0o644: a
// staged pack file is content, and the exec bit is exactly what rule 1 gates, so it
// must not be carried through even when allow_exec permitted the copy.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
