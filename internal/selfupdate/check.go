package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

var sourceUpdatePaths = append(append([]string{}, version.ImageSourcePaths...),
	"Justfile",
	"scripts/build-go.sh",
	"scripts/stage-source-bundle.sh",
)

// GitRunner runs git in dir and returns its trimmed stdout.
type GitRunner func(ctx context.Context, dir string, args ...string) (string, error)

// CheckDeps are Check's side effects, injected so it is testable offline.
type CheckDeps struct {
	HTTP       *http.Client
	ReleaseURL string // LatestReleaseAPI unless a test points it elsewhere
	Git        GitRunner
	Now        func() time.Time
}

// DefaultCheckDeps are the real network, git and clock.
func DefaultCheckDeps() CheckDeps {
	return CheckDeps{
		HTTP:       &http.Client{Timeout: 15 * time.Second},
		ReleaseURL: LatestReleaseAPI,
		Git:        runGit,
		Now:        time.Now,
	}
}

// Check asks whether something newer than ch exists and returns the answer as
// the State to cache. A failure is recorded in State.Error rather than
// returned, because a failed check is an answer too: it decides when the next
// one runs (see State.Fresh). prev carries the user's earlier "no" forward when
// it was given for this same binary.
func Check(ctx context.Context, ch Channel, prev State, deps CheckDeps) State {
	st := State{
		CheckedAt: deps.Now().UTC(),
		Identity:  ch.Identity(),
		Kind:      ch.Kind,
		Current:   ch.Version,
	}
	if prev.Identity == st.Identity {
		st.DeclinedFor = prev.DeclinedFor
	}
	// The disclosure is about the machine, not the binary: an update must not
	// make it repeat.
	st.Disclosed = prev.Disclosed
	var err error
	switch ch.Kind {
	case KindSource:
		st.Upstream, st.Latest, st.Behind, err = checkSource(ctx, deps.Git, ch)
		st.Available = err == nil && st.Behind > 0
	case KindUnknown:
		err = fmt.Errorf("cannot tell how this yolo was installed")
	default:
		st.Latest, err = latestRelease(ctx, deps.HTTP, deps.ReleaseURL)
		st.Available = err == nil && Newer(st.Latest, ch.Version)
	}
	if err != nil {
		st.Error = err.Error()
	}
	return st.sanitized()
}

// SourceBranchMismatch is the refusal for a checkout that has left the branch
// the binary was built from. Check and Apply share it word for word.
func SourceBranchMismatch(ch Channel, current string) error {
	return fmt.Errorf("the checkout at %s is on %q, but this yolo was built from %q; switch back (or run `yolo update --from %s` to deploy %q deliberately)",
		ch.SourceDir, current, ch.Branch, shquote.Quote(ch.SourceDir), current)
}

// sourceUpstream resolves the checkout's branch and what it tracks, refusing a
// checkout that has left the branch the binary was built from.
func sourceUpstream(ctx context.Context, git GitRunner, ch Channel) (branch, remote, mergeRef, display string, err error) {
	branch, err = git(ctx, ch.SourceDir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		return "", "", "", "", fmt.Errorf("the checkout at %s has a detached HEAD, so there is no branch to update from", ch.SourceDir)
	}
	if ch.Branch != "" && branch != ch.Branch {
		return "", "", "", "", SourceBranchMismatch(ch, branch)
	}
	remote, rerr := git(ctx, ch.SourceDir, "config", "--get", "branch."+branch+".remote")
	mergeRef, merr := git(ctx, ch.SourceDir, "config", "--get", "branch."+branch+".merge")
	if rerr != nil || merr != nil || remote == "" || mergeRef == "" {
		return "", "", "", "", fmt.Errorf("the checkout at %s: branch %q has no upstream branch to update from", ch.SourceDir, branch)
	}
	// Both values come from the checkout's own git config, which whatever can
	// write the checkout controls. The `--` before them in checkSource's git
	// calls already stops git reading one as an option (`--upload-pack=<cmd>`
	// runs <cmd>); refusing the spelling as well means no git command ever
	// receives it.
	if strings.HasPrefix(remote, "-") || strings.HasPrefix(mergeRef, "-") {
		return "", "", "", "", fmt.Errorf("the checkout at %s: branch %q has an upstream that starts with \"-\" (remote %q, merge %q), which yolo refuses to pass to git", ch.SourceDir, branch, remote, mergeRef)
	}
	display = remote + "/" + strings.TrimPrefix(mergeRef, "refs/heads/")
	return branch, remote, mergeRef, display, nil
}

// checkSource asks the checkout's upstream for its tip and counts the commits
// the binary was not built from, among those that can change the binary, the
// jail image, or the source install/bundle path. A docs-only commit is not an
// update.
//
// It runs only for an explicit `yolo update` request and never prompts for
// credentials. `git ls-remote` reads the tip; the fetch that makes the tip's
// commits countable writes no ref at all — not the remote-tracking branch
// (`--refmap=` turns off git's opportunistic update of it), not FETCH_HEAD
// (`--no-write-fetch-head`), no tags. It does write commit objects into the
// checkout; git's own gc collects them. The fetch is skipped entirely when the
// tip is already present. It compares against the binary's stamped commit, not
// HEAD: a `git pull` without a redeploy leaves the binary exactly as out of date
// as before.
func checkSource(ctx context.Context, git GitRunner, ch Channel) (upstream, latest string, behind int, err error) {
	_, remote, mergeRef, upstream, err := sourceUpstream(ctx, git, ch)
	if err != nil {
		return "", "", 0, err
	}
	out, err := git(ctx, ch.SourceDir, "ls-remote", "--exit-code", "--", remote, mergeRef)
	if err != nil {
		return "", "", 0, fmt.Errorf("git ls-remote %s %s: %w", remote, mergeRef, err)
	}
	tip, _, _ := strings.Cut(out, "\t")
	if tip == "" {
		return "", "", 0, fmt.Errorf("git ls-remote %s %s: unexpected output %q", remote, mergeRef, out)
	}
	if _, err := git(ctx, ch.SourceDir, "cat-file", "-e", tip+"^{commit}"); err != nil {
		if _, err := git(ctx, ch.SourceDir, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--refmap=", "--", remote, mergeRef); err != nil {
			return "", "", 0, fmt.Errorf("git fetch %s %s: %w", remote, mergeRef, err)
		}
	}
	base := ch.Version
	if _, err := git(ctx, ch.SourceDir, "cat-file", "-e", base+"^{commit}"); base == "" || err != nil {
		base = "HEAD" // no stamp (--from), or the stamped commit is gone: the tree is the best evidence left
	}
	args := append([]string{"rev-list", "--count", base + ".." + tip, "--"}, sourceUpdatePaths...)
	out, err = git(ctx, ch.SourceDir, args...)
	if err != nil {
		return "", "", 0, err
	}
	behind, err = strconv.Atoi(out)
	if err != nil {
		return "", "", 0, fmt.Errorf("git rev-list --count: unexpected output %q", out)
	}
	short := tip
	if len(short) > 8 {
		short = short[:8]
	}
	return upstream, short, behind, nil
}

// latestRelease returns the newest published release's version, without the
// tag's leading "v".
func latestRelease(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "yolo-jail-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("%s: %w", url, err)
	}
	if body.TagName == "" {
		return "", fmt.Errorf("%s: no tag_name in the response", url)
	}
	return strings.TrimPrefix(body.TagName, "v"), nil
}

// runGit is the real GitRunner. It describes dir and nothing else — a git
// hook's GIT_DIR must not redirect it (packsrc.CleanGitEnv) — and never prompts,
// so an explicit check fails cleanly instead of opening a GUI helper.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := gitCommand(ctx, dir, args...).Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("git %s in %s: %w", firstArg(args), dir, ctx.Err())
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCommand builds the command runGit runs, split out so its hygiene is
// testable as a property of the command. packsrc.DetachGit runs it in a new
// session whose whole process group dies with ctx, with a bounded wait on the
// output pipe: without it a remote that accepts the connection and never
// answers hangs `yolo update --check` past its timeout, because git's
// transport helper keeps the pipe open after git itself is killed.
func gitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"-C", dir,
		"-c", "credential.interactive=false",
		"-c", "core.askPass=",
	}, args...)...)
	cmd.Env = unattendedGitEnv(packsrc.CleanGitEnv(os.Environ()))
	packsrc.DetachGit(cmd)
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// unattendedGitEnv strips and sets what it takes for git to FAIL rather than
// ask, on every path it can ask by: its own terminal prompt
// (GIT_TERMINAL_PROMPT), an askpass helper that would pop a GUI dialog
// (GIT_ASKPASS and SSH_ASKPASS forced empty, core.askPass overridden above,
// SSH_ASKPASS_REQUIRE=never for ssh
// itself), and Git Credential Manager's own UI (GCM_INTERACTIVE). The check is
// deliberately non-interactive even when its caller has a terminal. A key
// already loaded in an ssh agent, or a stored credential, still works: only
// asking is refused.
func unattendedGitEnv(env []string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GIT_ASKPASS", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", "GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE":
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"SSH_ASKPASS_REQUIRE=never",
		"GCM_INTERACTIVE=never",
	)
}

// Newer reports whether version a is newer than version b. Both are compared
// on their MAJOR.MINOR.PATCH core; a pre-release ("0.11.0-rc1") sorts before
// its release, and build metadata after a "+" (a from-HEAD Homebrew build's
// "+510.gcfefa8bf") is ignored. Anything unparsable is never newer, so a
// malformed tag cannot produce a notice.
func Newer(a, b string) bool {
	cmp, ok := compareVersions(a, b)
	return ok && cmp > 0
}

// AtLeast reports whether version a is the same as or newer than b.
func AtLeast(a, b string) bool {
	cmp, ok := compareVersions(a, b)
	return ok && cmp >= 0
}

func compareVersions(a, b string) (int, bool) {
	ac, apre, aok := parseVersion(a)
	bc, bpre, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range ac {
		if ac[i] != bc[i] {
			if ac[i] > bc[i] {
				return 1, true
			}
			return -1, true
		}
	}
	switch {
	case apre == bpre:
		return 0, true
	case !apre && bpre:
		return 1, true
	default:
		return -1, true
	}
}

func parseVersion(v string) (core [3]int, pre, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], true
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return core, false, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return core, false, false
		}
		core[i] = n
	}
	return core, pre, true
}
