package packsrc

// workingtree.go is the WORKING-TREE STAGING of a `local` pack (docs/design/local-pack-clones.md §6.4-§6.5,
// LPC-D12..LPC-D14, slice 6): when the launch's decision (localcopy.go) says the clone runs, the bytes the
// pack stages from are the clone's files as they are on disk.
//
//   - THE FILE SET is what git would add (LPC-D12): `git ls-files` tracked plus untracked-not-ignored, limited
//     to the pack's directory, never `.git`, never a gitlink (a submodule, which the origin copy does not
//     carry either). A tracked file deleted on disk is absent. Read once, here, at staging.
//   - LINKS ARE COPIED AS LINKS when they stay inside the pack, and a link that leaves it is not followed:
//     the clone is a FETCHED pack's content, not a `file://` pack's (LPC-D13), so the same pack stages the same
//     way whichever copy supplies it. Staging falls to origin's held copy and says which link (§6.5).
//   - THE COPY IS A SNAPSHOT in a directory the caller supplies (config.ResolvePack: the process pack tree), so
//     the staged tree is stable for the launch and the ordinary stager (packstage) runs over it as over any
//     fetched tree: its own no-escape rule is a second line, not the first.
//   - THE RECORD IS THE ONLY INPUT (LPC-D14): the launch check's record says whether the clone runs; nothing
//     here decides a copy. A record that is missing, unreadable or for another clone or branch is origin's.
//   - READ ONLY toward the clone: `git -C <clone>` with GIT_OPTIONAL_LOCKS=0 and fsmonitor off.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// WorkingTreeSpec asks for a `local` pack's working tree to be staged.
type WorkingTreeSpec struct {
	// Name is the pack's handle, the key of its launch record; Clone is the `local` path.
	Name, Clone string
	// Addr is the pack's parsed git address: its ref names the branch, its Path the in-repo directory.
	Addr Addr
	// Subdir is the entry's `subdir`, relative to the address's directory; the file set is limited to it.
	Subdir string
	// Dir returns a new, empty directory to snapshot into. It is called only when there is something to stage.
	Dir func() (string, error)
}

// WorkingTree is a staged working-tree copy.
type WorkingTree struct {
	// Root is the snapshot, laid out like the address's directory of origin's tree.
	Root string
	// Files counts the files staged and Untracked the ones git does not track yet.
	Files, Untracked int
}

type wtFile struct {
	// rel is the slash path below the address's directory; abs the file in the clone.
	rel, abs  string
	link      string
	isLink    bool
	untracked bool
	mode      os.FileMode
}

// wtPlan is the file set and the links that leave the pack.
type wtPlan struct {
	files   []wtFile
	escapes []string
}

// scopeOf is the in-repo directory the file set is limited to: the address's directory, then `subdir`.
func scopeOf(addrPath, subdir string) string {
	return strings.Trim(path.Join(addrPath, subdir), "/")
}

// ResolveWorkingTree stages the pack's working tree when its launch record says the clone runs. It returns
// (nil, "") when origin's copy runs (no record, another clone or branch, an origin decision), and (nil, why)
// when the clone was to run and could not be staged, so the caller falls to origin's held copy and says why.
func (s *Store) ResolveWorkingTree(spec WorkingTreeSpec) (*WorkingTree, string) {
	rec, err := s.LoadLocalCopyRecord(spec.Name)
	if err != nil {
		return nil, ""
	}
	ref := strings.TrimPrefix(spec.Addr.Ref, "refs/heads/")
	if rec == nil || rec.Copy != CopyWorkingTree || rec.Clone != spec.Clone || rec.Ref != ref {
		return nil, ""
	}
	origin := "origin " + ref
	if rec.OriginCommit != "" {
		origin += " " + shortCommit(rec.OriginCommit)
	}
	fail := func(why string) (*WorkingTree, string) {
		return nil, "pack " + spec.Name + ": the working tree of " + spec.Clone + " was not staged (" + why + ") — " +
			origin + " runs instead"
	}
	ctx, cancel := context.WithTimeout(s.parentCtx(), LocalCopyDeadline)
	defer cancel()
	plan, err := s.planWorkingTree(budget{ctx: ctx, d: LocalCopyDeadline}, spec.Clone, spec.Addr.Path, spec.Subdir)
	if err != nil {
		return fail(oneLine(err))
	}
	if len(plan.escapes) > 0 {
		return fail(strings.Join(plan.escapes, "; ") + " — remove the link or point it inside the pack")
	}
	dir, err := spec.Dir()
	if err != nil {
		return fail(oneLine(err))
	}
	wt := &WorkingTree{Root: dir}
	for _, f := range plan.files {
		if err := copyWorkingFile(dir, f); err != nil {
			return fail(oneLine(err))
		}
		wt.Files++
		if f.untracked {
			wt.Untracked++
		}
	}
	return wt, ""
}

// planWorkingTree reads the file set of the clone limited to addrPath/subdir, each file checked on disk.
func (s *Store) planWorkingTree(b budget, clone, addrPath, subdir string) (wtPlan, error) {
	var plan wtPlan
	env := []string{"GIT_OPTIONAL_LOCKS=0"}
	topOut, err := s.runEnv(b, "", env, "-C", clone, "-c", "core.fsmonitor=false", "rev-parse", "--show-toplevel")
	top := strings.TrimSpace(topOut)
	if err != nil || top == "" {
		return plan, errors.New("it is no longer a git clone")
	}
	top = filepath.Clean(top)
	scope := scopeOf(addrPath, subdir)
	spec := []string{"--"}
	if scope != "" {
		spec = append(spec, ":(literal)"+scope)
	}
	base := []string{"-C", top, "-c", "core.fsmonitor=false", "ls-files", "-z"}
	tracked, err := s.runEnv(b, "", env, append(append(append([]string{}, base...), "-s"), spec...)...)
	if err != nil {
		return plan, fmt.Errorf("its files could not be listed: %w", err)
	}
	others, err := s.runEnv(b, "", env, append(append(append([]string{}, base...), "--others", "--exclude-standard"), spec...)...)
	if err != nil {
		return plan, fmt.Errorf("its untracked files could not be listed: %w", err)
	}
	type cand struct {
		p         string
		untracked bool
	}
	var cands []cand
	var gitlinks []string
	for _, e := range strings.Split(tracked, "\x00") {
		meta, p, ok := strings.Cut(e, "\t")
		if !ok {
			continue
		}
		if strings.HasPrefix(meta, "160000 ") {
			gitlinks = append(gitlinks, p)
			continue
		}
		cands = append(cands, cand{p: p})
	}
	for _, p := range strings.Split(others, "\x00") {
		if p != "" && !strings.HasSuffix(p, "/") { // a nested repository is listed as "dir/": not this pack's files
			cands = append(cands, cand{p: p, untracked: true})
		}
	}
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c.p] || underAny(c.p, gitlinks) {
			continue
		}
		seen[c.p] = true
		rel := c.p
		if ap := strings.Trim(addrPath, "/"); ap != "" {
			rel = strings.TrimPrefix(c.p, ap+"/")
		}
		abs := filepath.Join(top, filepath.FromSlash(c.p))
		fi, err := os.Lstat(abs)
		if err != nil {
			continue // a tracked file deleted on disk is absent
		}
		f := wtFile{rel: rel, abs: abs, untracked: c.untracked, mode: 0o644}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			if err != nil {
				return plan, err
			}
			f.isLink, f.link = true, target
			if leavesPack(rel, target, subdir) {
				plan.escapes = append(plan.escapes, fmt.Sprintf("%s is a link that leaves the pack (-> %s)", rel, target))
				continue
			}
		case fi.Mode().IsRegular():
			if fi.Mode()&0o111 != 0 {
				f.mode = 0o755
			}
		default:
			continue // a socket or a device is not pack content
		}
		plan.files = append(plan.files, f)
	}
	return plan, nil
}

// underAny reports whether p is a gitlink's path or below one.
func underAny(p string, links []string) bool {
	for _, l := range links {
		if p == l || strings.HasPrefix(p, l+"/") {
			return true
		}
	}
	return false
}

// leavesPack reports whether the link at rel (below the address's directory) pointing at target leaves the pack
// directory, which is subdir below that: an absolute target, or one that climbs out of it.
func leavesPack(rel, target, subdir string) bool {
	if filepath.IsAbs(target) || strings.HasPrefix(target, "/") {
		return true
	}
	resolved := path.Clean(path.Join(path.Dir(rel), target))
	sd := strings.Trim(path.Clean("/"+subdir), "/")
	if sd == "" {
		return resolved == ".." || strings.HasPrefix(resolved, "../")
	}
	return resolved != sd && !strings.HasPrefix(resolved, sd+"/")
}

// copyWorkingFile writes f into dir: a link as a link, a file by its bytes with only its exec bit kept.
func copyWorkingFile(dir string, f wtFile) error {
	dst := filepath.Join(dir, filepath.FromSlash(f.rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if f.isLink {
		return os.Symlink(f.link, dst)
	}
	in, err := os.Open(f.abs)
	if err != nil {
		return err
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return errors.New(f.rel + " changed while it was read")
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// recordLocalCopy writes the outcome's decision as the pack's launch record (LPC-D14). A record that cannot be
// written must not leave an older one for the staging to trust, so the decision is DOWNGRADED to origin's copy,
// said on the warned line with the next step, and the old record removed when it can be. The lockfile step then
// pins origin's commit, which is what runs.
func (s *Store) recordLocalCopy(o *Outcome) {
	d := o.Local
	err := s.SaveLocalCopyRecord(*d)
	if err == nil {
		return
	}
	_ = os.Remove(s.localCopyRecordPath(d.Name))
	d.Copy, d.Relation, d.Warn = CopyOrigin, relUnusable, true
	d.Line = "origin " + d.Ref + " " + shortCommit(d.OriginCommit) + " (the launch record of the clone " + d.Clone +
		" could not be written: " + oneLine(err) + " — fix the permissions of " + filepath.Dir(s.localCopyRecordPath(d.Name)) + ")"
}
