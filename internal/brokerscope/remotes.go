// Package brokerscope is the repository scope a brokered loophole's daemon is fenced to,
// everywhere core handles it (docs/design/boundary-broker.md §5.6): reading it from a
// workspace's git remotes, the per-launch scope file a fresh launch hands the daemon, and
// the host paths a `mounts` entry may not reach.
//
// It knows repositories, git remotes and a forge's hostname, and nothing about any tool:
// which forge and which credential paths are the loophole manifest's `brokered` block
// (internal/loopholedecl/brokered.go).
package brokerscope

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// Remote is one remote on the scope's forge.
type Remote struct {
	// Name is the remote's name in the git config ("origin").
	Name string
	// Repo is `owner/repo`.
	Repo string
}

// Read is what reading one workspace's remotes found.
//
// GitConfig and Problem are FOR DISPLAY, and every control character in them is escaped
// (termsafe.Visible): both can carry a path the agent chose, a worktree's common directory,
// and host yolo prints them on the human's terminal while asking about the scope.
type Read struct {
	// GitConfig is the file the remotes were read from, "" when none was found.
	GitConfig string
	// Remotes are every remote on the forge, sorted by repository then name.
	Remotes []Remote
	// Problem says why the scope read as empty although the workspace has a .git: an
	// unreadable config, a symlink, or a worktree `.git` file of the wrong shape. The
	// launch says it; against a non-empty approved scope the empty read is a change.
	Problem string
}

// Repos is the read's repositories, each once, sorted.
func (r Read) Repos() []string {
	seen := map[string]bool{}
	var out []string
	for _, rm := range r.Remotes {
		key := strings.ToLower(rm.Repo)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, rm.Repo)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// RemoteNames returns the names of the remotes naming repo.
func (r Read) RemoteNames(repo string) []string {
	var out []string
	for _, rm := range r.Remotes {
		if strings.EqualFold(rm.Repo, repo) {
			out = append(out, rm.Name)
		}
	}
	return out
}

// ReadRemotes reads a workspace's remotes on host (BB-D19): the workspace's git config
// AS TEXT, with no host git process, because git in an agent-writable checkout can run
// code through its config (§9.5). It ignores `include` and `includeIf`, follows no symlink
// at the files it reads, and follows a worktree's `.git` file only when the target has
// git's worktree shape.
func ReadRemotes(workspace, host string) Read {
	r := readRemotes(workspace, host)
	r.GitConfig, r.Problem = termsafe.Visible(r.GitConfig), termsafe.Visible(r.Problem)
	return r
}

func readRemotes(workspace, host string) Read {
	cfgPath, problem := gitConfigPath(workspace)
	if cfgPath == "" {
		return Read{Problem: problem}
	}
	data, err := readCapped(cfgPath, GitConfigCap)
	switch {
	case errors.Is(err, errNotRegular):
		return Read{GitConfig: cfgPath, Problem: cfgPath + " is not a regular file (a symlink is not followed)"}
	case errors.Is(err, errTooLarge):
		return Read{GitConfig: cfgPath, Problem: fmt.Sprintf("%s is larger than %d bytes, more than any git "+
			"config git writes, so it is not read", cfgPath, GitConfigCap)}
	case err != nil:
		return Read{GitConfig: cfgPath, Problem: "cannot read " + cfgPath + ": " + err.Error()}
	}
	r := Read{GitConfig: cfgPath}
	for _, u := range parseRemoteURLs(string(data)) {
		if repo := RepoFromRemoteURL(u.url, host); repo != "" {
			r.Remotes = append(r.Remotes, Remote{Name: u.name, Repo: repo})
		}
	}
	sort.SliceStable(r.Remotes, func(i, j int) bool {
		a, b := strings.ToLower(r.Remotes[i].Repo), strings.ToLower(r.Remotes[j].Repo)
		if a != b {
			return a < b
		}
		return r.Remotes[i].Name < r.Remotes[j].Name
	})
	return r
}

// gitConfigPath finds the config file a workspace's remotes live in: .git/config for an
// ordinary checkout, or the common directory's config for a worktree. "" with a problem
// when the shape is not one git lays out; "" with no problem when there is no .git.
func gitConfigPath(workspace string) (string, string) {
	dotGit := filepath.Join(workspace, ".git")
	fi, err := os.Lstat(dotGit)
	switch {
	case os.IsNotExist(err):
		return "", ""
	case err != nil:
		return "", "cannot read " + dotGit + ": " + err.Error()
	case fi.Mode()&os.ModeSymlink != 0:
		return "", dotGit + " is a symlink, which the scope reader does not follow"
	case fi.IsDir():
		return filepath.Join(dotGit, "config"), ""
	case !fi.Mode().IsRegular():
		return "", dotGit + " is neither a directory nor a file"
	}
	return worktreeConfig(dotGit)
}

// worktreeConfig validates a worktree's `.git` file (BB-D19). The file is agent-written, so
// its `gitdir:` line is an agent-chosen host path; followed blindly it would point the
// scope at another project's remotes. So the target G must hold a `gitdir` back-pointer
// naming THIS `.git` file, G/commondir must name the common directory C, and G must be
// C/worktrees/<name>. Anything else reads as an empty scope, and says why.
func worktreeConfig(dotGit string) (string, string) {
	bad := func(why string) (string, string) {
		return "", dotGit + " is a worktree pointer of the wrong shape (" + why + "), so the " +
			"repository scope reads as empty"
	}
	line, err := readSmallRegular(dotGit)
	if err != nil {
		return bad(err.Error())
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return bad("no gitdir: line")
	}
	g := strings.TrimSpace(target)
	if !filepath.IsAbs(g) {
		g = filepath.Join(filepath.Dir(dotGit), g)
	}
	g = filepath.Clean(g)
	if fi, err := os.Lstat(g); err != nil || !fi.IsDir() {
		return bad("its gitdir is not a directory")
	}
	back, err := readSmallRegular(filepath.Join(g, "gitdir"))
	if err != nil {
		return bad("its gitdir has no gitdir back-pointer")
	}
	backPath := strings.TrimSpace(back)
	if !filepath.IsAbs(backPath) {
		backPath = filepath.Join(g, backPath)
	}
	if !samePath(backPath, dotGit) {
		return bad("its gitdir's back-pointer names another checkout")
	}
	common, err := readSmallRegular(filepath.Join(g, "commondir"))
	if err != nil {
		return bad("its gitdir has no commondir")
	}
	c := strings.TrimSpace(common)
	if !filepath.IsAbs(c) {
		c = filepath.Join(g, c)
	}
	c = filepath.Clean(c)
	if termsafe.HasUnsafe(c) {
		// The common directory's config is the path the scope prompt shows, and git never
		// names one with a control character.
		return bad("its commondir names a path with a control character")
	}
	if !samePath(filepath.Dir(g), filepath.Join(c, "worktrees")) {
		return bad("its gitdir is not under the common directory's worktrees/")
	}
	return filepath.Join(c, "config"), ""
}

// samePath compares two paths after resolving symlinks in both, so a macOS /var →
// /private/var or a symlinked home does not read as two different checkouts.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

func readSmallRegular(path string) (string, error) {
	b, err := readCapped(path, 64<<10)
	return string(b), err
}

// GitConfigCap bounds the git config the scope reader loads: 1 MiB, far past any config git
// writes. The file is agent-writable, and a sparse `truncate -s 64G .git/config` costs the
// agent no disk while every host launch would read it whole.
const GitConfigCap = 1 << 20

var (
	errNotRegular = errors.New("not a regular file")
	errTooLarge   = errors.New("larger than the cap")
)

// readCapped reads a regular file of at most limit bytes, deciding on the file it OPENED
// rather than on a path it looked at first: it opens with O_NOFOLLOW, so a last component
// swapped for a symlink after any check fails to open, and with O_NONBLOCK, so a FIFO
// cannot hang the launch; then it fstats what it holds and reads through a limit. An agent
// in the jail can replace the file while the host reads it, which a Lstat-then-ReadFile
// pair cannot see.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) {
		return nil, errNotRegular
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errTooLarge
	}
	return data, nil
}

type remoteURL struct{ name, url string }

var (
	sectionRE    = regexp.MustCompile(`^\[\s*([A-Za-z0-9.-]+)(?:\s+"((?:[^"\\]|\\.)*)")?\s*\]\s*(.*)$`)
	keyValueRE   = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*)\s*(?:=\s*(.*))?$`)
	subsectionRE = regexp.MustCompile(`\\(.)`)
)

// parseRemoteURLs reads every `url` of every `[remote "<name>"]` section from git config
// text. It handles what git writes and what people hand-edit — comments, quoting, escapes,
// continuation lines, the deprecated `[remote.name]` spelling — and deliberately nothing
// that would make the file reach elsewhere: `include`, `includeIf` and `insteadOf` are not
// honored, so a remote spelled through a rewrite is not a remote on the forge.
func parseRemoteURLs(text string) []remoteURL {
	var out []remoteURL
	section, sub := "", ""
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		for strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\") && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, "\\") + lines[i]
		}
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			m := sectionRE.FindStringSubmatch(line)
			if m == nil {
				section, sub = "", ""
				continue
			}
			section = strings.ToLower(m[1])
			sub = subsectionRE.ReplaceAllString(m[2], "$1")
			if m[2] == "" {
				if name, rest, ok := strings.Cut(m[1], "."); ok {
					section, sub = strings.ToLower(name), strings.ToLower(rest)
				}
			}
			line = strings.TrimSpace(m[3])
			if line == "" {
				continue
			}
		}
		if section != "remote" || sub == "" {
			continue
		}
		m := keyValueRE.FindStringSubmatch(line)
		if m == nil || !strings.EqualFold(m[1], "url") {
			continue
		}
		out = append(out, remoteURL{name: sub, url: configValue(m[2])})
	}
	return out
}

// configValue unquotes a git config value and strips a trailing comment outside quotes.
func configValue(raw string) string {
	var b strings.Builder
	quoted := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '\\' && i+1 < len(raw):
			i++
			switch raw[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(raw[i])
			}
		case c == '"':
			quoted = !quoted
		case (c == '#' || c == ';') && !quoted:
			return strings.TrimSpace(b.String())
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}

var segmentRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidRepo reports whether s is exactly `owner/repo`: two segments, each a forge name of
// letters, digits, `_`, `.` and `-`, neither "." nor "..". It is the one shape a remote
// reduces to (RepoFromRemoteURL) and the one a user-scope widening entry may list
// (config.BrokeredWidening), so a repository reaches a scope file in no other spelling.
func ValidRepo(s string) bool {
	owner, repo, ok := strings.Cut(s, "/")
	if !ok || strings.Contains(repo, "/") {
		return false
	}
	for _, seg := range []string{owner, repo} {
		if seg == "." || seg == ".." || !segmentRE.MatchString(seg) {
			return false
		}
	}
	return true
}

// RepoFromRemoteURL reduces a remote URL on host to `owner/repo`, in the https, `ssh://`
// and scp-like `git@host:` forms, and returns "" for any other URL.
func RepoFromRemoteURL(raw, host string) string {
	raw = strings.TrimSpace(raw)
	var h, path string
	switch {
	case strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "ssh://"):
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		h, path = u.Hostname(), u.Path
	case !strings.Contains(raw, "://"):
		// scp-like: [user@]host:path, where path does not begin with a slash.
		hostPart, p, ok := strings.Cut(raw, ":")
		if !ok || strings.Contains(hostPart, "/") {
			return ""
		}
		if _, after, found := strings.Cut(hostPart, "@"); found {
			hostPart = after
		}
		h, path = hostPart, p
	default:
		return ""
	}
	if !strings.EqualFold(h, host) {
		return ""
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		return ""
	}
	r := parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if !ValidRepo(r) {
		return ""
	}
	return r
}
