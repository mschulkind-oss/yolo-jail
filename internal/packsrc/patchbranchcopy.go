package packsrc

// patchbranchcopy.go is a PATCH BRANCH GIVEN AS A LOCAL CHECKOUT — a relative locator or a git+file:// one
// naming a repository on this machine (docs/reference/patched-forks.md §3.5, PF-D99). Its rule, the
// maintainer's (2026-10-24): BUILD FROM WHICHEVER COPY OF THE BRANCH IS NEWEST, AND ALWAYS SAY WHICH COPY AND
// HOW IT RELATES TO THE OTHER.
//
//   - TWO COPIES. The checkout's local branch (refs/heads/<ref>, read through the patch branch's own mirror,
//     which is fetched from the checkout as before) and its remote's tip, which yolo fetches from the
//     checkout's remote (origin, else the first by name) INTO A MIRROR OF ITS OWN, by the same per-mirror
//     fetch a patch branch at a URL takes (non-interactive: GIT_TERMINAL_PROMPT=0, no askpass, the user's
//     ssh command left alone, so ssh may still prompt at a terminal in `yolo pack install`/`update`) in a
//     BUDGET OF ITS OWN, half of what the check has left (budget.share), so a hung fetch times out as a failed
//     fetch and the steps after it keep theirs. Nothing is ever fetched into, or written to, the user's
//     repository.
//   - WHEN. At every check, which is every fresh launch (LPC-D6 retired the hourly throttle and with it
//     the in-throttle refresh): a branch that moved is a new build key and the normal check and
//     build run. At most once per repository and ref per command (patchBranchFetches).
//   - WHICH COPY. No local branch: the remote tip. Equal: either. The local branch behind: the remote tip,
//     and the local branch is said to be stale. Ahead: the local branch, with its unpushed commits counted.
//     Diverged: the local branch, warned loudly with both commits and counts and the push that repairs a
//     rebase. A fetch that failed never refuses: the local branch, else the newer (by ancestry; diverged, the
//     tracking ref) of the remote tip yolo last fetched and the checkout's own refs/remotes/<remote>/<ref>,
//     so the `git fetch` the line suggests is what builds next, else the plain order (resolvePatchBranchRef),
//     said "not verified" with the last good fetch's age and the fetch to run by hand. A fetch that succeeded
//     and shows the remote no longer has the branch (RemoteGone): the local branch, "not on <remote>"; with
//     none, the checkout's stale tracking ref, said to be one, loudly, with the push that restores it.
//   - ONE LINE (PatchBranchResolved.Line) from the check record, so the launch, `yolo pack update`, `yolo
//     pack series check` and `yolo pack status` cannot disagree about which copy they judged.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// checkoutPlan is a local checkout's remote, read from its configuration with no network.
type checkoutPlan struct {
	dir         string // the checkout
	remote, url string // its remote (origin, else the first by name) and that remote's address; "" for none
	// remotes is every remote the checkout has (`git remote`), never nil: resolvePatchBranchRef matches
	// remote-tracking refs against these names only.
	remotes []string
	// clone is the registered local clone a patch branch URL resolved to (localclones.go), nil for a checkout
	// the locator names itself.
	clone *CloneMatch
}

// remoteAddr is the remote's address at the patch branch's ref, the key of its mirror and stamp.
func (p *checkoutPlan) remoteAddr(ref string) Addr {
	return Addr{Kind: KindGit, Raw: p.url, Repo: p.url, Ref: ref}
}

// checkoutPlanOf is c's checkout plan, nil for a patch branch whose repository is not a directory on this
// machine (a URL; a file:// path that is gone, which the patch branch's own fetch reports).
func (s *Store) checkoutPlanOf(c Addr) *checkoutPlan {
	dir, ok := strings.CutPrefix(c.Repo, "file://")
	if !ok || dir == "" {
		return nil
	}
	p := &checkoutPlan{dir: dir}
	out, err := s.run("", "-C", dir, "remote")
	if err != nil {
		return nil
	}
	remotes := strings.Fields(out)
	p.remotes = append([]string{}, remotes...)
	if len(remotes) == 0 {
		return p
	}
	p.remote = remotes[0]
	for _, r := range remotes {
		if r == "origin" {
			p.remote = r
			break
		}
		if r < p.remote {
			p.remote = r
		}
	}
	url, err := s.run("", "-C", dir, "remote", "get-url", "--", p.remote)
	if url = strings.TrimSpace(url); err != nil || url == "" {
		p.remote = ""
		return p
	}
	if !strings.Contains(url, "://") && !scpLike(url) && !filepath.IsAbs(url) {
		url = filepath.Join(dir, url) // a path relative to the checkout, as git reads it there
	}
	p.url = url
	return p
}

// scpLike reports git's scp-like address, "[user@]host:path", whose colon comes before any slash.
func scpLike(url string) bool {
	colon := strings.IndexByte(url, ':')
	slash := strings.IndexByte(url, '/')
	return colon > 0 && (slash < 0 || colon < slash)
}

// patchBranchFetchReuse is how long, on the check's clock, a checkout's REMOTE fetch answers for the same
// repository and ref again in this process: one command's length, so a launch or an update whose several
// owners name one patch branch fetches its remote once.
const patchBranchFetchReuse = time.Minute

type patchBranchFetched struct {
	at  time.Time
	err error
}

// patchBranchFetches is this PROCESS's record of the remote fetches it ran, per store, repository and ref
// (patchBranchFetchReuse).
var patchBranchFetches sync.Map

// fetchOnce is ONE CHECK CALL's record of the patch branch fetches it ran (CheckPatched): its refresh and the
// check that follows a moved branch share them. Keyed as patchBranchFetches.
type fetchOnce map[string]error

// patchBranchFetch is the per-mirror fetch step for one of a patch branch's repositories, always, unless this
// check call already ran it — or, acrossCalls (a checkout's remote, the network fetch), unless this process ran
// it within patchBranchFetchReuse. The checkout's own fetch, a local read, is asked again by every call, so a
// branch moved since the last call is seen. The caller holds the mirror's lock.
func (s *Store) patchBranchFetch(b budget, a Addr, mode fetchMode, now time.Time, once fetchOnce, acrossCalls bool) error {
	key := s.Dir + "\x00" + a.Repo + "\x00" + a.Ref
	if err, ok := once[key]; ok {
		return err
	}
	if v, ok := patchBranchFetches.Load(key); ok && acrossCalls {
		f := v.(patchBranchFetched)
		if age := now.Sub(f.at); age >= 0 && age < patchBranchFetchReuse && mirrorExists(s.mirrorPath(a.Repo)) {
			if once != nil {
				once[key] = f.err
			}
			return f.err
		}
	}
	mode.always = true
	step := s.fetchStep(b, a.Repo, []Addr{a}, mode, now)
	if once != nil {
		once[key] = step.fetchErr
	}
	if acrossCalls {
		patchBranchFetches.Store(key, patchBranchFetched{at: now, err: step.fetchErr})
	}
	return step.fetchErr
}

// stampAt is the ref's last successful fetch, in unix seconds; 0 for none.
func (s *Store) stampAt(a Addr) int64 {
	data, err := os.ReadFile(s.stampPath(a))
	if err != nil {
		return 0
	}
	secs, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return secs
}

// branchName is the branch a ref names for the two-copy rule: the ref itself, or a refs/heads/ ref's
// branch. false for anything else (a full commit, another refs/ spelling), which only the plain order reads.
func branchName(ref string) (string, bool) {
	if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return name, name != ""
	}
	if strings.HasPrefix(ref, "refs/") || isFullSHA(ref) {
		return "", false
	}
	return ref, true
}

// pickCopy is the two-copy rule for a checkout, filling r's copy fields: the ref kind, the fully qualified
// ref and commit that answered, and the repository whose mirror holds it. mirror is the patch branch's own
// (fetched from the checkout), the caller holds both mirrors' locks, and r.Verified says whether the
// remote's fetch succeeded.
func (s *Store) pickCopy(b budget, c Addr, p *checkoutPlan, mirror string, r *PatchBranchResolved) (refKind, string, string, error) {
	name, ok := branchName(c.Ref)
	if !ok {
		kind, ref, commit, err := s.resolvePatchBranchRef(mirror, c.Ref, p.remotes)
		r.Copy = ref
		return kind, ref, commit, err
	}
	local, err := s.revParse(mirror, "refs/heads/"+name)
	if err != nil {
		return refUnresolved, "", "", err
	}
	remoteTip, remoteRepo, remoteRef := "", "", ""
	if p.remote != "" {
		// The remote's tip two ways: yolo's own mirror of the remote (fetched just now when r.Verified), and the
		// checkout's refs/remotes/<remote>/<ref>, as the user last fetched it.
		tracking := "refs/remotes/" + p.remote + "/" + name
		trackTip, err := s.revParse(mirror, tracking)
		if err != nil {
			return refUnresolved, "", "", err
		}
		mirrorTip := ""
		if rm := s.mirrorPath(p.url); mirrorExists(rm) {
			if mirrorTip, err = s.revParse(rm, "refs/heads/"+name); err != nil {
				return refUnresolved, "", "", err
			}
		}
		fromMirror := func() { remoteTip, remoteRepo, remoteRef = mirrorTip, p.url, "refs/heads/"+name }
		fromTracking := func() { remoteTip, remoteRepo, remoteRef = trackTip, "", tracking }
		switch {
		case r.Verified && mirrorTip == "":
			// THE REMOTE NO LONGER HAS THE BRANCH (the fetch prunes): the checkout's tracking ref is stale and
			// is no remote tip. The local branch answers, "not on <remote>"; with none, the stale tracking ref
			// still builds (a copy exists, so nothing refuses), said to be one.
			r.RemoteGone = true
			if local == "" && trackTip != "" {
				r.Copy = p.remote
				return refBranch, tracking, trackTip, nil
			}
		case r.Verified, trackTip == "", trackTip == mirrorTip:
			if mirrorTip != "" {
				fromMirror()
			}
		case mirrorTip == "":
			fromTracking()
		default:
			// YOLO'S FETCH FAILED and the copies differ: the newer by ancestry, so a `git fetch` the user ran
			// (the line's next step) is what builds; diverged, the tracking ref, the user's latest fetch.
			ahead, behind, err := s.aheadBehind(b, mirror, p.url, trackTip, mirrorTip)
			if err != nil {
				return refUnresolved, "", "", err
			}
			if ahead == 0 && behind > 0 {
				fromMirror()
			} else {
				fromTracking()
			}
		}
	}
	r.Local, r.RemoteTip = local, remoteTip
	useLocal := func() (refKind, string, string, error) {
		r.Copy = "local"
		return refBranch, "refs/heads/" + name, local, nil
	}
	useRemote := func() (refKind, string, string, error) {
		r.Copy, r.MirrorRepo = p.remote, remoteRepo
		return refBranch, remoteRef, remoteTip, nil
	}
	switch {
	case local == "" && remoteTip == "":
		// NEITHER COPY: the plain order, whose other remotes' tracking branches and tags may still answer.
		kind, ref, commit, err := s.resolvePatchBranchRef(mirror, c.Ref, p.remotes)
		r.Copy = ref
		return kind, ref, commit, err
	case local == "":
		return useRemote()
	case remoteTip == "":
		return useLocal()
	case local == remoteTip:
		return useLocal()
	}
	ahead, behind, err := s.aheadBehind(b, mirror, remoteRepo, local, remoteTip)
	if err != nil {
		return refUnresolved, "", "", err
	}
	r.Ahead, r.Behind = ahead, behind
	if r.Verified && ahead == 0 && behind > 0 {
		return useRemote()
	}
	return useLocal()
}

// aheadBehind counts local's commits remote lacks and remote's local lacks, in a scratch repository that
// borrows the patch branch's mirror and, when remoteRepo is set, the remote's.
func (s *Store) aheadBehind(b budget, mirror, remoteRepo, local, remote string) (int, int, error) {
	mirrors := []string{mirror}
	if remoteRepo != "" {
		mirrors = append(mirrors, s.mirrorPath(remoteRepo))
	}
	sc, err := newScratchOf(s.git(), b, mirrors...)
	if err != nil {
		return 0, 0, err
	}
	defer sc.remove()
	out, err := sc.git("", "rev-list", "--left-right", "--count", local+"..."+remote)
	if err != nil {
		return 0, 0, fmt.Errorf("comparing the local branch with its remote: %w", err)
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("comparing the local branch with its remote: git answered %q", strings.TrimSpace(out))
	}
	ahead, _ := strconv.Atoi(f[0])
	behind, _ := strconv.Atoi(f[1])
	return ahead, behind, nil
}

// Diverged reports a local branch and a remote tip that each hold commits the other lacks.
func (r *PatchBranchResolved) Diverged() bool { return r.Ahead > 0 && r.Behind > 0 }

// NotVerified reports a checkout whose remote this resolution could not fetch.
func (r *PatchBranchResolved) NotVerified() bool {
	return r.Checkout != "" && r.Remote != "" && !r.Verified
}

// Loud reports a line every act prints as a warning: a diverged branch, a remote that could not be fetched, a
// build from a stale tracking ref of a branch the remote no longer has, a registered local clone that matched
// the URL and was not used (CloneSkipped), or one reached through the local clone map whose remote deleted the
// branch, so its local branch builds a pack that names the URL (PF-D104; a pack's own checkout stays quiet).
func (r *PatchBranchResolved) Loud() bool {
	return r.Diverged() || r.NotVerified() || (r.RemoteGone && r.Local == "") ||
		(r.Checkout == "" && r.CloneSkipped != "") || (r.RemoteGone && r.CloneVia != "")
}

// Line is the patch branch's provenance in one line, read from the check record with no git: the ref, the copy
// that answered, its commit, how it relates to the other copy, and the remote fetch's freshness as of now (the
// check's clock). For a patch branch at a URL, "" — whose label says all there is — unless a registered local
// clone matched it and was not used, when it is the commit and why (CloneSkipped).
//
//	mine  origin 4cd0bc6b  local branch is stale, 1 behind origin  fetched just now
//	mine  local 9e1f22aa  2 unpushed  not verified (fetch failed: …), last fetched 3 hours ago — run `git -C … fetch origin mine`
func (r *PatchBranchResolved) Line(now time.Time) string {
	if r != nil && r.Checkout == "" && r.CloneSkipped != "" {
		return "commit " + shortCommit(r.Commit) + "  " + r.CloneSkipped
	}
	if r == nil || r.Checkout == "" {
		return ""
	}
	copyName := r.Copy
	if copyName == "" {
		copyName = "commit"
	}
	parts := []string{r.Branch, copyName + " " + shortCommit(r.Commit)}
	if rel := r.relation(); rel != "" {
		parts = append(parts, rel)
	}
	if fresh := r.freshness(now); fresh != "" {
		parts = append(parts, fresh)
	}
	if r.CloneVia != "" {
		clone := "local clone " + r.Checkout + " (" + r.CloneVia
		if len(r.CloneOthers) > 0 {
			clone += "; also matched " + strings.Join(r.CloneOthers, ", ")
		}
		parts = append(parts, clone+")")
	}
	return strings.Join(parts, "  ")
}

// relation is how the copy that answered relates to the other.
func (r *PatchBranchResolved) relation() string {
	switch {
	case r.Remote == "":
		return "no remote to compare with"
	case r.RemoteGone && r.Local == "":
		return "not on " + r.Remote + " (stale tracking ref) — push it back with `" +
			shquote.JoinDisplay([]string{"git", "-C", r.Checkout, "push", "--", r.Remote, "refs/remotes/" + r.Remote + "/" + r.Branch + ":refs/heads/" + r.Branch}) +
			"`, or correct the patch branch's ?ref="
	case r.RemoteGone && r.CloneVia != "":
		// THROUGH THE MAP (PF-D104), not the pack's own checkout: the published URL deleted the branch, and the
		// clone's local branch, found through its stale tracking ref, now builds a pack that names that URL.
		return "not on " + r.Remote + ", which deleted it (the clone's tracking ref is stale), so this pack, which " +
			"names the published URL, builds your clone's local branch — push it back with `" +
			shquote.JoinDisplay([]string{"git", "-C", r.Checkout, "push", "--", r.Remote, r.Branch}) +
			"`, or check the pack's ?ref= with its author"
	case r.Local == "" && r.RemoteTip == "":
		return "neither the local branch nor " + r.Remote + " has it"
	case r.Local == "":
		return "no local branch"
	case r.RemoteTip == "":
		return "not on " + r.Remote
	case r.Local == r.RemoteTip:
		return "equal to " + r.Remote
	case r.Diverged():
		return fmt.Sprintf("diverged from %s: local %s has %d %s %s lacks, %s %s has %d the local branch lacks — "+
			"`%s` (if you rebased)", r.Remote, shortCommit(r.Local), r.Ahead, plural(r.Ahead, "commit", "commits"),
			r.Remote, r.Remote, shortCommit(r.RemoteTip), r.Behind,
			shquote.JoinDisplay([]string{"git", "-C", r.Checkout, "push", "--force-with-lease", "--", r.Remote, r.Branch}))
	case r.Ahead > 0:
		return fmt.Sprintf("%d unpushed", r.Ahead)
	case r.Behind > 0:
		return fmt.Sprintf("local branch is stale, %d behind %s", r.Behind, r.Remote)
	}
	return ""
}

// freshness is the remote fetch's, as of now.
func (r *PatchBranchResolved) freshness(now time.Time) string {
	if r.Remote == "" {
		return ""
	}
	last := "never fetched"
	if r.FetchedAt != 0 {
		last = "last fetched " + agoWords(now.Sub(time.Unix(r.FetchedAt, 0)))
	}
	if r.Verified {
		if r.FetchedAt != 0 {
			return strings.Replace(last, "last fetched", "fetched", 1)
		}
		return "fetched just now"
	}
	why := ""
	if r.FetchErr != "" {
		why = " (fetch failed: " + r.FetchErr + ")"
	}
	return "not verified" + why + ", " + last + " — run `" +
		shquote.JoinDisplay([]string{"git", "-C", r.Checkout, "fetch", "--", r.Remote, r.Branch}) + "` to fetch it by hand"
}

// agoWords is a duration as a line says how long ago: "just now", "N minutes ago", "N hours ago", "N days ago".
func agoWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d / time.Minute)
		return fmt.Sprintf("%d %s ago", n, plural(n, "minute", "minutes"))
	case d < 48*time.Hour:
		n := int(d / time.Hour)
		return fmt.Sprintf("%d %s ago", n, plural(n, "hour", "hours"))
	}
	n := int(d / (24 * time.Hour))
	return fmt.Sprintf("%d days ago", n)
}

// plural is one or many of a word, by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
