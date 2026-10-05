package cli

// hostworkspaceskills.go is the workspace skills layer's HOST half (docs/design/workspace-skills.md,
// OQ-WS5's mechanism B, built 2026-10-04 under the maintainer's delegation as WS-D19 to WS-D23):
// `yolo host -- <agent>` puts ONE relative symlink into the workspace, at the first project skills
// path the agent's pack declares, pointing at the first skills directory the repository already
// keeps under another agent's path — `.codex/skills -> ../.claude/skills` for a repository that
// committed `.claude/skills` and a reader who runs codex.
//
// WHY A LINK AND NOT THE MIRROR. A host render's content is a function of the user's config and
// packs, never of the directory it runs from (OQ-WS5's answer), so the staged mirror a jail gets
// is closed here: the agent reads the user's real home. The link is the other mechanism the design
// weighed, and it writes into the repository instead, which P4 allows only where a diff shows it:
// so the link is added to the workspace's .gitignore once (OQ-WS6, decided on its leaning (a)),
// and every launch that writes, keeps or removes one says so.
//
// WHAT IT NEVER DOES:
//
//   - Touch a path the repository has. If any project skills path the agent's pack declares
//     exists, in any form (a directory, a committed link, a file, a link that leaves the tree),
//     the agent reads the repository's own opinion there and yolo writes nothing.
//   - Point a link at a tree the jail's reader would not deliver whole (P5, WS-D21): the source is
//     checked by that reader (jailcontent.CheckWorkspaceSkillSource) and any refusal — an escaping
//     link, a FIFO, the cap — writes no link and removes the one yolo wrote before.
//   - Follow a link while writing. Every component of the link's path is opened from the
//     workspace root with O_NOFOLLOW, so a parent the repository made a link is refused, not
//     written through; the .gitignore is opened O_NOFOLLOW|O_NONBLOCK and must be a regular file.
//   - Run git. Whether the workspace is in a work tree is an Lstat of `.git` upward: `.git/config`
//     can name a program git runs (core.fsmonitor), and a repository is not handed a command on the
//     user's machine by being launched in.
//   - Decide ownership by inspecting a link. A link is yolo's only when a record under yolo's own
//     state dir (paths.HostWorkspaceSkillsDir, never the workspace's .yolo/) says yolo wrote that
//     path with that target; anything else at the path is the repository's.
//   - Claim the agent reads the link. Whether codex, copilot, opencode or agy load skills through
//     a symlinked project directory is unmeasured (only pi's row says it follows links, §2.1), so
//     the lines say where the link is, never that the agent uses it.
//
// SILENT, AND A NO-OP, in a jail (OQ-WS4: the mirror alone there), in a directory that is or holds
// the credential boundary (paths.WorkspaceScopeBreach: the home itself, say), and for a program no
// selected pack gives a skills destination declaring project_dirs.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostSkillsDest is the launched agent's skills destination: where it reads at home scope, and
// the project paths its pack declares, in the agent's own precedence order.
type hostSkillsDest struct {
	agent string
	into  string   // home-relative
	dirs  []string // workspace-relative, cleaned, slash-separated
}

// hostSkillsRecord is what yolo remembers of one link it wrote into one workspace. Target "" is
// a record whose link is gone but whose ignore line yolo wrote, kept so the line is not added
// back after the user removed it.
type hostSkillsRecord struct {
	Workspace     string   `json:"workspace"`
	Link          string   `json:"link"`
	Target        string   `json:"target,omitempty"`
	CreatedDirs   []string `json:"created_dirs,omitempty"`
	IgnoreWritten bool     `json:"ignore_written,omitempty"`
}

// hostWorkspaceSkills is the one call hostLaunch makes: the link for agent in the current
// directory, and its lines on errw.
func hostWorkspaceSkills(packs []*packload.Pack, agent string, errw io.Writer) {
	if config.InJail() {
		return
	}
	cwd, err := os.Getwd()
	if err != nil || paths.WorkspaceScopeBreach(cwd) != nil {
		return
	}
	dest, ok := hostSkillsDestination(packs, agent)
	if !ok {
		return
	}
	ws := cwd
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		ws = real // the record's key: one workspace, however the shell spelled it
	}
	for _, line := range hostWorkspaceSkillsIn(ws, dest, run.WorkspaceSkillDirs(packs), paths.Home()) {
		fmt.Fprintf(errw, "yolo host: workspace skills: %s\n", line)
	}
}

// hostSkillsDestination finds agent's skills destination among the selected packs: a `skills`
// contribution whose `agent` is the launched program's name and that declares project_dirs.
func hostSkillsDestination(packs []*packload.Pack, agent string) (hostSkillsDest, bool) {
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindSkills || c.Agent != agent || len(c.ProjectDirs) == 0 {
				continue
			}
			d := hostSkillsDest{agent: agent, into: c.Into}
			for _, pd := range c.ProjectDirs {
				d.dirs = append(d.dirs, path.Clean(filepath.ToSlash(pd)))
			}
			return d, true
		}
	}
	return hostSkillsDest{}, false
}

// hostWorkspaceSkillsIn is the mechanism over one resolved workspace: the lines to print.
func hostWorkspaceSkillsIn(ws string, d hostSkillsDest, sourceSet []string, home string) []string {
	root, err := os.OpenRoot(ws)
	if err != nil {
		return nil
	}
	defer root.Close()
	rootFD, err := unix.Open(ws, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(rootFD)

	link := d.dirs[0]
	rec := loadHostSkillsRecord(ws, link)
	ours := rec != nil && rec.Target != "" && readlinkNoFollow(rootFD, link) == rec.Target
	var lines []string
	removeOurs := func(why string) {
		if !ours {
			return
		}
		if err := removeOwnLink(rootFD, link, rec); err != nil {
			lines = append(lines, fmt.Sprintf("could not remove the link yolo put at %s (%s); "+
				"remove it by hand", link, errnoText(err)))
			return
		}
		lines = append(lines, fmt.Sprintf("removed the link yolo had put at %s: %s", link, why))
		rec.Target, rec.CreatedDirs = "", nil
		saveOrDropHostSkillsRecord(rec)
	}

	// 1. The repository's own opinion: any declared path present, in any form, writes nothing.
	for _, p := range d.dirs {
		if p == link && ours {
			continue
		}
		if presentInRoot(root, p) {
			removeOurs(fmt.Sprintf("%s reads %s in this repository itself", d.agent, p))
			return lines
		}
	}

	// 2. The source: the first skills directory present under another agent's path, in the jail's
	// order, less this agent's own paths and less any link yolo itself put there.
	own := map[string]bool{}
	for _, p := range d.dirs {
		own[p] = true
	}
	var chosen string
	var chosenInfo fs.FileInfo
	var dropped []string
	for _, s := range sourceSet {
		s = path.Clean(filepath.ToSlash(s))
		if own[s] || !presentInRoot(root, s) || isRecordedHostLink(rootFD, ws, s) {
			continue
		}
		fi, statErr := root.Stat(s)
		if chosen == "" {
			chosen, chosenInfo = s, fi
			continue
		}
		if statErr == nil && chosenInfo != nil && os.SameFile(fi, chosenInfo) {
			continue // a second spelling of the chosen directory
		}
		dropped = append(dropped, s)
	}
	if chosen == "" {
		removeOurs("no skills directory it could point at is left in this repository")
		return lines
	}

	// 3. P5: the jail's reader, over the chosen directory alone. Fail closed.
	check, err := jailcontent.CheckWorkspaceSkillSource(ws, chosen)
	if err != nil {
		lines = append(lines, fmt.Sprintf("could not check %s (%s), so yolo puts no link at %s; "+
			"yolo checks a copy under %s; make it writable, or point TMPDIR at a folder that is, and "+
			"the next launch checks again", chosen, errnoText(err), link, os.TempDir()))
		removeOurs("its source could not be checked")
		return lines
	}
	if len(check.Refused) > 0 {
		for _, f := range check.Refused {
			lines = append(lines, "refused "+run.DisplaySafe(f.Path)+" — "+run.DisplaySafe(f.Reason))
		}
		lines = append(lines, fmt.Sprintf("no link at %s: %s holds an entry yolo will not hand %s "+
			"through a link yolo makes (each is named above; fix or remove it and the next launch "+
			"links it)", link, chosen, d.agent))
		removeOurs("its source holds an entry yolo refuses")
		return lines
	}

	// 4. The link: kept, refreshed, or placed.
	target := relativeLinkTarget(link, chosen)
	if rec == nil {
		rec = &hostSkillsRecord{Workspace: ws, Link: link}
	}
	state := "kept"
	switch {
	case ours && rec.Target == target:
	case ours:
		if err := removeOwnLink(rootFD, link, &hostSkillsRecord{Target: rec.Target}); err != nil {
			lines = append(lines, fmt.Sprintf("could not refresh the link yolo put at %s (%s); "+
				"remove %s and the next launch places it again", link, errnoText(err), link))
			return lines
		}
		if err := symlinkBeside(rootFD, link, target); err != nil {
			lines = append(lines, fmt.Sprintf("could not refresh the link yolo put at %s (%s), so "+
				"it is gone; the next launch places it again, or create %s yourself", link,
				errnoText(err), link))
			rec.Target = ""
			saveOrDropHostSkillsRecord(rec)
			return lines
		}
		state = "refreshed"
	default:
		created, err := placeLinkNoFollow(rootFD, link, target)
		if err != nil {
			step := "once that is fixed the next launch places it"
			var lp errLinkedParent
			if errors.As(err, &lp) {
				step = "make " + lp.rel + " a real directory and the next launch places it"
			}
			lines = append(lines, fmt.Sprintf("put no link at %s: %s; %s, or create %s yourself",
				link, err, step, link))
			return lines
		}
		rec.CreatedDirs = created
		state = "placed"
	}
	rec.Target = target

	// 5. The ignore line, inside a git work tree only.
	ignoreLine := ""
	if insideGitWorkTree(ws) {
		var wrote bool
		ignoreLine, wrote = ensureHostSkillsIgnoreLine(ws, "/"+link, rec.IgnoreWritten)
		if wrote {
			rec.IgnoreWritten = true
		}
	}
	saveOrDropHostSkillsRecord(rec)

	names := make([]string, len(check.Skills))
	for i, n := range check.Skills {
		names[i] = run.DisplaySafe(n)
	}
	what := "no skills yet"
	if len(names) > 0 {
		what = strings.Join(names, ", ")
	}
	if state != "kept" || ignoreLine != "" || len(dropped) > 0 {
		lines = append(lines, fmt.Sprintf("%s the link %s -> %s (a project skills path declared "+
			"for %s), so the skills this repository keeps in %s (%s) are at that path too",
			state, link, target, d.agent, chosen, what))
	} else {
		lines = append(lines, fmt.Sprintf("kept the link %s -> %s (%s)", link, target, what))
	}
	for _, s := range dropped {
		lines = append(lines, fmt.Sprintf("%s also holds skills, and a link points at one "+
			"directory, so %s is not handed %s", s, d.agent, s))
	}
	if ignoreLine != "" {
		lines = append(lines, ignoreLine)
	}
	if home != "" && d.into != "" {
		for _, n := range check.Skills {
			if _, err := os.Lstat(filepath.Join(home, d.into, n)); err == nil {
				lines = append(lines, fmt.Sprintf("~/%s also has a skill named %s; which of the "+
					"two %s uses is %s's own rule", d.into, run.DisplaySafe(n), d.agent, d.agent))
			}
		}
	}
	return lines
}

// presentInRoot reports whether anything is at rel inside root, a link of any kind included. A
// path the root cannot answer for (it climbs out through a link) counts as present: something is
// there, and it is not yolo's to write over.
func presentInRoot(root *os.Root, rel string) bool {
	_, err := root.Lstat(rel)
	return err == nil || !(errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR))
}

// isRecordedHostLink reports whether rel is a link yolo itself put into ws for another agent:
// not the repository's content, so never a source.
func isRecordedHostLink(rootFD int, ws, rel string) bool {
	r := loadHostSkillsRecord(ws, rel)
	return r != nil && r.Target != "" && readlinkNoFollow(rootFD, rel) == r.Target
}

// relativeLinkTarget is the target a link at link needs to name source, both workspace-relative:
// `.codex/skills` to `.claude/skills` is `../.claude/skills`.
func relativeLinkTarget(link, source string) string {
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(link)), filepath.FromSlash(source))
	if err != nil {
		return source
	}
	return filepath.ToSlash(rel)
}

// noFollowDir is the flag set for every directory open on the way to the link.
const noFollowDir = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC

// errLinkedParent is a component on the way to the link that is not a real directory.
type errLinkedParent struct{ rel string }

func (e errLinkedParent) Error() string {
	return e.rel + " is a symbolic link or not a directory, and yolo writes nothing through it"
}

// openParentNoFollow opens the directory holding rel by a walk from rootFD, one component at a
// time, following no link. The caller closes what it returns.
func openParentNoFollow(rootFD int, rel string, create bool) (fd int, created []string, err error) {
	parts := strings.Split(rel, "/")
	fd, err = unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, nil, err
	}
	for i, c := range parts[:len(parts)-1] {
		next, oerr := unix.Openat(fd, c, noFollowDir, 0)
		if errors.Is(oerr, unix.ENOENT) && create {
			if merr := unix.Mkdirat(fd, c, 0o755); merr != nil && !errors.Is(merr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, created, merr
			}
			created = append(created, strings.Join(parts[:i+1], "/"))
			next, oerr = unix.Openat(fd, c, noFollowDir, 0)
		}
		_ = unix.Close(fd)
		if oerr != nil {
			if errors.Is(oerr, unix.ELOOP) || errors.Is(oerr, unix.ENOTDIR) {
				return -1, created, errLinkedParent{rel: strings.Join(parts[:i+1], "/")}
			}
			return -1, created, oerr
		}
		fd = next
	}
	return fd, created, nil
}

// symlinkBeside writes a link at rel naming target into rel's existing parent, reached by
// openParentNoFollow: the refresh, whose parents yolo already made or found.
func symlinkBeside(rootFD int, rel, target string) error {
	fd, _, err := openParentNoFollow(rootFD, rel, false)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.Symlinkat(target, fd, path.Base(rel))
}

// placeLinkNoFollow creates rel's missing parents (0755) and a symlink at rel naming target,
// through openParentNoFollow, and returns the parents it made, outermost first. A parent that is
// a link refuses the whole write; parents made before the refusal are removed again.
func placeLinkNoFollow(rootFD int, rel, target string) ([]string, error) {
	fd, created, err := openParentNoFollow(rootFD, rel, true)
	if err != nil {
		removeCreatedDirs(rootFD, created)
		return nil, err
	}
	defer unix.Close(fd)
	if err := unix.Symlinkat(target, fd, path.Base(rel)); err != nil {
		removeCreatedDirs(rootFD, created)
		return nil, fmt.Errorf("%s", errnoText(err))
	}
	return created, nil
}

// readlinkNoFollow is rel's link target, reached by a walk that follows no link, or "" when rel
// is not a link there.
func readlinkNoFollow(rootFD int, rel string) string {
	fd, _, err := openParentNoFollow(rootFD, rel, false)
	if err != nil {
		return ""
	}
	defer unix.Close(fd)
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(fd, path.Base(rel), buf)
	if err != nil || n <= 0 || n >= len(buf) {
		return ""
	}
	return string(buf[:n])
}

// removeOwnLink removes the link at rel when its target is still rec.Target, then each directory
// yolo made for it that is now empty.
func removeOwnLink(rootFD int, rel string, rec *hostSkillsRecord) error {
	fd, _, err := openParentNoFollow(rootFD, rel, false)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(fd, path.Base(rel), buf)
	if err != nil || string(buf[:max(n, 0)]) != rec.Target {
		return nil // not the link yolo wrote: left alone
	}
	if err := unix.Unlinkat(fd, path.Base(rel), 0); err != nil {
		return err
	}
	removeCreatedDirs(rootFD, rec.CreatedDirs)
	return nil
}

// removeCreatedDirs removes dirs, innermost first, each only when empty.
func removeCreatedDirs(rootFD int, dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		fd, _, err := openParentNoFollow(rootFD, dirs[i], false)
		if err != nil {
			continue
		}
		_ = unix.Unlinkat(fd, path.Base(dirs[i]), unix.AT_REMOVEDIR)
		_ = unix.Close(fd)
	}
}

// insideGitWorkTree reports whether ws is inside a git work tree: a `.git` (a directory, or the
// file a worktree or submodule has) at ws or any directory above it. Lstat only; git never runs.
func insideGitWorkTree(ws string) bool {
	for dir := ws; ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// maxIgnoreBytes bounds the .gitignore read: it is the repository's file.
const maxIgnoreBytes = 1 << 20

// ensureHostSkillsIgnoreLine appends line to ws/.gitignore once, and returns what to say about it
// and whether it wrote it. wroteBefore is the record's: a line yolo added once and that is gone
// now was removed by someone, and is not added back.
func ensureHostSkillsIgnoreLine(ws, line string, wroteBefore bool) (string, bool) {
	p := filepath.Join(ws, ".gitignore")
	notRegular := fmt.Sprintf(".gitignore is not a regular file yolo can read whole (it is a link, "+
		"a special file, unreadable, or over 1 MiB), so yolo did not add `%s` to it and `git status` "+
		"will list the link; add `%s` to your ignore rules yourself to quiet it", line, line)
	current, err := readRegularNoFollow(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		current = nil
	case err != nil:
		return notRegular, false
	}
	for _, l := range strings.Split(string(current), "\n") {
		if strings.TrimSpace(l) == line {
			return "", false
		}
	}
	if wroteBefore {
		return fmt.Sprintf("yolo added `%s` to .gitignore once and it has been removed, so yolo "+
			"leaves it out and `git status` will list the link; add it back yourself to quiet it",
			line), false
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o644)
	if err != nil {
		return fmt.Sprintf("could not open .gitignore to add `%s` (%s), so `git status` will list "+
			"the link; add the line yourself to quiet it", line, errnoText(err)), false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return notRegular, false
	}
	add := line + "\n"
	if len(current) > 0 && !bytes.HasSuffix(current, []byte("\n")) {
		add = "\n" + add
	}
	if _, err := f.WriteString(add); err != nil {
		return fmt.Sprintf("could not add `%s` to .gitignore (%s), so `git status` will list the "+
			"link; add the line yourself to quiet it", line, errnoText(err)), false
	}
	return fmt.Sprintf("added `%s` to .gitignore, so git does not list the link; commit that "+
		"line once and every clone is quiet", line), true
}

// readRegularNoFollow reads p when it is a regular file, opening it O_NOFOLLOW|O_NONBLOCK.
func readRegularNoFollow(p string) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxIgnoreBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxIgnoreBytes {
		return nil, errors.New("too large")
	}
	return data, nil
}

// hostSkillsRecordPath names the record of the link at link in ws: a digest, so a workspace path
// is never spelled as a file name.
func hostSkillsRecordPath(ws, link string) string {
	sum := sha256.Sum256([]byte(ws + "\x00" + link))
	return filepath.Join(paths.HostWorkspaceSkillsDir(), hex.EncodeToString(sum[:8])+".json")
}

// loadHostSkillsRecord reads the record of the link at link in ws, nil when there is none or it
// names another workspace or link.
func loadHostSkillsRecord(ws, link string) *hostSkillsRecord {
	data, err := os.ReadFile(hostSkillsRecordPath(ws, link))
	if err != nil {
		return nil
	}
	var r hostSkillsRecord
	if json.Unmarshal(data, &r) != nil || r.Workspace != ws || r.Link != link {
		return nil
	}
	return &r
}

// saveOrDropHostSkillsRecord writes rec, or removes it when it remembers nothing: no link and no
// ignore line yolo wrote. Best effort: a record that cannot be written leaves a link yolo will
// treat as the repository's next time, which writes nothing over it.
func saveOrDropHostSkillsRecord(rec *hostSkillsRecord) {
	p := hostSkillsRecordPath(rec.Workspace, rec.Link)
	if rec.Target == "" && !rec.IgnoreWritten {
		_ = os.Remove(p)
		return
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".record-")
	if err != nil {
		return
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), p) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// errnoText is an error's errno description alone, never its message: a message can carry a path
// the workspace spelled (WS-D16's rule, for the host's lines too).
func errnoText(err error) string {
	var errno unix.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	var lp errLinkedParent
	if errors.As(err, &lp) {
		return lp.Error()
	}
	return "error"
}
