package packsrc

// patchbranch.go is a PATCH BRANCH (docs/reference/patched-forks.md §3.5, OQ-PFK6): a commit graph a patched fork
// or extension names in place of a `patches` series — a repository and a branch whose ancestry
// includes the upstream's — merged against each upstream candidate by `git merge-tree
// --write-tree` instead of replayed.
//
// ONE MACHINERY WITH THE SERIES (PF-D89). A patch branch is a Series whose members are not files but one
// commit, so the check, its record and throttle, the walk, the ratchet, the store key and every
// explicit act read it through the same types:
//
//   - THE CHECK FETCHES IT. Whenever a check runs (its own throttle, an explicit act, or a retry of
//     a failed fetch with nothing to run, PF-D88), after the upstream's fetch step and in the same
//     budget, the patch branch's repository is fetched INTO ITS OWN MIRROR by the per-mirror fetch step a
//     pack's repository takes (blobless, fsck'd, its tags put back), under both mirrors' locks taken
//     in one order (lockMirrors). A blobless mirror cannot take another repository's objects: a
//     fetch from a remote that is not its promisor, under transfer.fsckObjects, refuses every tree
//     naming a blob it lacks (MEASURED, git 2.55: "did not receive expected object"). The replay's
//     scratch repository borrows both mirrors' objects instead (newScratchOf). A fetch that fails is
//     the check's failed fetch, classified by TransportFailure, and the copy the patch branch's mirror
//     already holds keeps answering, as an upstream's does.
//   - THE CHECK RESOLVES IT: the branch to a commit, and the commit's MERGE BASE with the upstream
//     ref's tip, which is the patch branch's base — the newest upstream commit it already contains. The
//     walk's list holds only versions containing it, and the first advance falls back to it, where
//     the merge is the patch branch's own tree. A patch branch that shares no history with the upstream names no
//     base: the check's problem, never a conflict.
//   - THE RECORD KEEPS IT (CheckRecord.Patch branch), apart from the last check's findings, so a later
//     check with a problem does not lose what the good build is keyed by; every reader rebuilds
//     the series from it with no git (PatchBranchSeries), so a launch inside the hour runs no git (P4).
//   - THE DIGEST is PatchBranchDigest(commit), which takes the series digest's place in the recipe: a
//     build is keyed on the upstream commit and the patch branch's commit, so a moved branch is a new
//     build and two machines that resolve one commit build the same bytes.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// SeriesPatchBranch is the patch branch a Series stands for.
type SeriesPatchBranch struct {
	// Source is the patch branch's address as the manifest writes it; Repo and Ref are its parts.
	Source, Repo, Ref string
	// Commit is the commit the branch resolved to at the last check that fetched it, and ResolvedRef the fully
	// qualified ref that answered (PatchBranchResolved.Ref).
	Commit, ResolvedRef string
	// Resolved is the whole resolution the check record keeps (copy, relation, freshness), nil before any.
	Resolved *PatchBranchResolved
}

// Label is the patch branch as a line names it: "<ref>@<short commit>", and in parentheses the copy that
// answered when it is not the local branch — a checkout's remote ("origin", patchbranchcheckout.go), or the
// fully qualified remote-tracking branch or tag that answered (resolvePatchBranchRef).
func (c *SeriesPatchBranch) Label() string {
	label := c.Ref + "@" + shortCommit(c.Commit)
	switch {
	case c.Resolved != nil && c.Resolved.Copy == "local":
	case c.Resolved != nil && c.Resolved.Copy != "":
		label += " (" + c.Resolved.Copy + ")"
	case c.ResolvedRef != "" && c.ResolvedRef != "refs/heads/"+c.Ref && c.ResolvedRef != c.Ref:
		label += " (" + c.ResolvedRef + ")"
	}
	return label
}

// mirrorRepo is the repository whose mirror holds Commit: the checkout's remote when its copy answered
// (PatchBranchResolved.MirrorRepo), else the patch branch's own.
func (c *SeriesPatchBranch) mirrorRepo() string {
	if c.Resolved != nil && c.Resolved.MirrorRepo != "" {
		return c.Resolved.MirrorRepo
	}
	return c.Repo
}

// PatchBranchResolved is what a check resolved a patch branch to, kept in the check record.
type PatchBranchResolved struct {
	// Source is the patch branch as the manifest wrote it, and Upstream the upstream repository it was
	// resolved against: a record whose either differs from what is asked names nothing. Repository is the
	// filesystem identity of the declaring repository for a relative Source, empty for absolute locators.
	Source     string `json:"source"`
	Upstream   string `json:"upstream"`
	Repository string `json:"repository,omitempty"`
	// Ref is the fully qualified ref of the patch branch's repository that answered ?ref=
	// (resolvePatchBranchRef): refs/heads/<ref>, refs/remotes/<remote>/<ref> or refs/tags/<ref>, empty
	// for a full commit and in a record written before it was kept.
	Ref string `json:"ref,omitempty"`
	// Commit is the branch's commit; Base its merge base with the upstream ref's tip.
	Commit string `json:"commit"`
	Base   string `json:"base"`
	// At is when it was resolved, in unix seconds.
	At int64 `json:"at,omitempty"`

	// A LOCAL CHECKOUT'S COPIES (patchbranchcopy.go), empty for a patch branch at a URL. Checkout is the
	// checkout's path and Branch the ?ref= it names. Copy is the copy that answered: "local" (the checkout's
	// refs/heads/<ref>), the checkout's remote by name (yolo's own mirror of it, or the checkout's
	// refs/remotes/<remote>/<ref> when yolo has never fetched it), or the fully qualified ref of a fallback
	// (another remote's tracking branch, a tag). MirrorRepo is the repository whose mirror holds Commit when it
	// is not the patch branch's own.
	Checkout   string `json:"checkout,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Copy       string `json:"copy,omitempty"`
	MirrorRepo string `json:"mirror_repo,omitempty"`
	// CloneVia is set when Checkout is a registered LOCAL CLONE a patch branch URL resolved to (localclones.go):
	// how the clone was registered ("local_clones entry …", "the path of pack …"). CloneOthers are the further
	// registered clones that matched the URL and lost (an explicit entry wins, then config order).
	CloneVia    string   `json:"clone_via,omitempty"`
	CloneOthers []string `json:"clone_others,omitempty"`
	// CloneSkipped is set when a registered local clone matched a patch branch URL and did not answer for its
	// ref (PF-D104: a refs/... spelling, or a branch the clone does not hold as the matched remote's tracking
	// ref): the sentence naming the clone and why, and that the build is the URL's.
	CloneSkipped string `json:"clone_skipped,omitempty"`
	// Remote is the checkout's remote compared against (origin, else the first by name) and RemoteURL its
	// address; Local the checkout's local branch and RemoteTip the remote's, "" where there is none; Ahead and
	// Behind count the local branch's commits the remote tip lacks and the remote tip's the local lacks.
	Remote    string `json:"remote,omitempty"`
	RemoteURL string `json:"remote_url,omitempty"`
	Local     string `json:"local,omitempty"`
	RemoteTip string `json:"remote_tip,omitempty"`
	Ahead     int    `json:"ahead,omitempty"`
	Behind    int    `json:"behind,omitempty"`
	// Verified is true when this resolution's fetch of the remote succeeded; FetchErr is why it did not, and
	// FetchedAt the last successful fetch of the remote, in unix seconds on the check's clock (0: never).
	Verified  bool   `json:"verified,omitempty"`
	FetchErr  string `json:"fetch_error,omitempty"`
	FetchedAt int64  `json:"fetched_at,omitempty"`
	// RemoteGone is true when the remote's fetch succeeded and the remote no longer has the branch (deleted, or
	// never pushed): RemoteTip is then "", and with no local branch Commit is the checkout's stale
	// refs/remotes/<remote>/<ref>.
	RemoteGone bool `json:"remote_gone,omitempty"`
	// Rewritten is true when the local branch and the remote tip diverged and the local branch's tip WAS ONCE the
	// remote's (PF-D99, 2026-11-27): the remote was rewritten elsewhere, the local branch holds nothing
	// unpushed, and the remote tip builds.
	Rewritten bool `json:"rewritten,omitempty"`
}

// PatchBranchDigest is a patch branch's SERIES DIGEST: the sha256, in hex, of the JSON array ["patch_branch", commit].
// The tag means no patch branch's digest can equal a patch series', whose array holds name pairs.
func PatchBranchDigest(commit string) string {
	canonical, _ := json.Marshal([]any{"patch_branch", commit})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// PatchBranchUnresolvedError is a patch branch no check has resolved on this machine yet: the next check fetches
// it. Readers that need the commit say so with its next step.
type PatchBranchUnresolvedError struct{ Source string }

func (e *PatchBranchUnresolvedError) Error() string {
	return "the patch branch " + e.Source + " has not been fetched on this machine yet — the next launch's check " +
		"fetches it, or `yolo pack update` fetches it now"
}

// UnresolvedPatchBranchSeries is the series a patch branch stands for before any check resolved it: no commit,
// no base, and a digest no build carries. An advance starts from it and takes the resolved one once
// its check has run (PatchBranchSeries); no walk runs on it. dir is the declaring pack's directory, which
// a relative locator is resolved against (ParseFrom).
func UnresolvedPatchBranchSeries(source, dir string) *Series {
	a, _ := ParseFrom(source, dir)
	return &Series{PatchBranch: &SeriesPatchBranch{Source: source, Repo: a.Repo, Ref: a.Ref}, Digest: PatchBranchDigest("")}
}

// PatchBranchSeries is the series the patch branch source stands for by rec, the check record of an owner whose
// upstream is the source upstream: the commit and base the last check that resolved it found.
// PatchBranchUnresolvedError when rec names no resolution of this patch branch against this upstream.
func PatchBranchSeries(source, dir, upstream string, rec *CheckRecord) (*Series, error) {
	return PatchBranchSeriesWithIdentity(source, dir, "", upstream, rec)
}

// PatchBranchSeriesWithIdentity reads a resolution using the host-established repository identity when the
// declaring pack was mapped into a guest with a different filesystem identity.
func PatchBranchSeriesWithIdentity(source, dir, identity, upstream string, rec *CheckRecord) (*Series, error) {
	declaringRepo := identity
	if IsRelativeLocator(source) && declaringRepo == "" {
		var err error
		declaringRepo, err = RelativeRepositoryIdentity(source, dir)
		if err != nil {
			return nil, err
		}
	}
	if rec == nil || rec.PatchBranch == nil || rec.PatchBranch.Source != source || rec.PatchBranch.Upstream != CheckRepo(upstream) ||
		rec.PatchBranch.Commit == "" || rec.PatchBranch.Repository != declaringRepo ||
		rec.Read.PatchBranchRepo != declaringRepo {
		return nil, &PatchBranchUnresolvedError{Source: source}
	}
	s := UnresolvedPatchBranchSeries(source, dir)
	s.PatchBranch.ResolvedRef = rec.PatchBranch.Ref
	resolved := *rec.PatchBranch
	s.PatchBranch.Resolved = &resolved
	s.PatchBranch.Commit, s.Base, s.Digest = rec.PatchBranch.Commit, rec.PatchBranch.Base, PatchBranchDigest(rec.PatchBranch.Commit)
	return s, nil
}

// ReadPatchBranch is PatchBranchSeries from the owner's check record in this store: a file read, no git.
func (s *Store) ReadPatchBranch(owner, source, dir, upstream string) (*Series, error) {
	return s.ReadPatchBranchWithIdentity(owner, source, dir, "", upstream)
}

// ReadPatchBranchWithIdentity reads a relative patch branch whose declaring repository identity was
// established by the host and carried through a selected pack-tree mapping.
func (s *Store) ReadPatchBranchWithIdentity(owner, source, dir, identity, upstream string) (*Series, error) {
	rec, err := s.LoadCheckRecord(owner)
	if err != nil {
		if errors.Is(err, ErrNoCheckRecord) {
			return nil, &PatchBranchUnresolvedError{Source: source}
		}
		return nil, &PatchBranchRecordError{Source: source, Err: err}
	}
	return PatchBranchSeriesWithIdentity(source, dir, identity, upstream, rec)
}

// PatchBranchRecordError is a check record that holds a patch branch's commit and cannot be read:
// not "not fetched yet", since a check did resolve it. A check starts the record over (§6.2), so an
// act that checks reads it as unresolved and goes on; every other reader says it.
type PatchBranchRecordError struct {
	Source string
	Err    error
}

func (e *PatchBranchRecordError) Error() string {
	return "the check record holding the commit of the patch branch " + e.Source + " cannot be read (" +
		oneLine(e.Err) + ") — `yolo pack update` checks it again and starts the record over"
}

func (e *PatchBranchRecordError) Unwrap() error { return e.Err }

// IsPatchBranchRecordError reports whether err is a PatchBranchRecordError.
func IsPatchBranchRecordError(err error) bool {
	var r *PatchBranchRecordError
	return errors.As(err, &r)
}

// localRetry is the next step after a git that could not answer about a mirror this machine holds (a
// timeout, a lock, a busy disk): the next check asks again, and `yolo pack update` asks now.
const localRetry = " — the next check asks again, or `yolo pack update` now"

// lockMirrors takes the mirror locks of repos, each once, in one order (sorted by repository), so
// two processes taking an upstream's and a patch branch's never wait on each other the other way round.
// Its release drops them all.
func (s *Store) lockMirrors(repos []string, waiting func(string)) (func(), error) {
	sorted := slices.Clone(repos)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	var unlocks []func()
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	for _, r := range sorted {
		unlock, err := s.lockMirror(r, waiting)
		if err != nil {
			release()
			return nil, err
		}
		unlocks = append(unlocks, unlock)
	}
	return release, nil
}

// mirrorsOf is the repositories a check or a walk holds the mirror locks of: the upstream's, and the
// patch branch's when there is one.
func mirrorsOf(upstream string, patchBranch *Addr, p *checkoutPlan) []string {
	if patchBranch == nil {
		return []string{upstream}
	}
	if p != nil && p.url != "" {
		return []string{upstream, patchBranch.Repo, p.url}
	}
	return []string{upstream, patchBranch.Repo}
}

// resolvePatchBranch is the check's patch branch step (findCandidates), under both mirrors' locks and the
// check's budget: the per-mirror fetch step for the patch branch's own repository, always (the check is
// the throttle), then its ref read in its mirror and the commit's merge base with the upstream's
// tip, asked in a scratch repository that borrows both mirrors' objects. It returns the resolution,
// or the check's problem, and a fetch that failed (one line), which the caller records as the
// check's failed fetch: the copy the patch branch's mirror holds answers meanwhile.
//
// A LOCAL CHECKOUT (p non-nil, patchbranchcopy.go) has its remote fetched into yolo's own mirror of it too, in
// the same budget, and the copy that answers is the two-copy rule's (pickCopy). That fetch failing is never the
// check's failed fetch nor its problem: the resolution records it (Verified, FetchErr) and builds the best copy.
func (s *Store) resolvePatchBranch(b budget, c Addr, p *checkoutPlan, upstream, tip string, now time.Time, once fetchOnce) (*PatchBranchResolved, string, string) {
	fetchErr := ""
	if err := s.patchBranchFetch(b, c, fetchMode{keepTags: true, remoteTracking: true}, now, once, false); err != nil {
		fetchErr = "fetching the patch branch " + c.Raw + ": " + oneLine(err)
	}
	mirror := s.mirrorPath(c.Repo)
	if !mirrorExists(mirror) {
		return nil, "this machine has never fetched the patch branch " + c.Raw + ", and " + fetchFailed(fetchErr), fetchErr
	}
	resolved := &PatchBranchResolved{Source: c.Raw, Upstream: upstream}
	var kind refKind
	var ref, commit string
	var err error
	if p == nil {
		kind, ref, commit, err = s.resolvePatchBranchRef(mirror, c.Ref, nil)
	} else {
		resolved.Checkout, resolved.Branch = p.dir, c.Ref
		if p.clone != nil {
			resolved.CloneVia, resolved.CloneOthers = p.clone.Clone.Via(), p.clone.Others
		}
		if p.remote != "" {
			ra := p.remoteAddr(c.Ref)
			resolved.Remote, resolved.RemoteURL = p.remote, p.url
			// THE REMOTE'S FETCH HAS A BUDGET OF ITS OWN, half of what is left, so a fetch that hangs times out
			// as a failed fetch ("not verified") and the comparison and merge base after it keep theirs.
			fb, cancel := b.share()
			ferr := s.patchBranchFetch(fb, ra, fetchMode{keepTags: true}, now, once, true)
			cancel()
			if ferr != nil {
				resolved.FetchErr = oneLine(ferr)
			} else {
				resolved.Verified = true
			}
			resolved.FetchedAt = s.stampAt(ra)
		}
		kind, ref, commit, err = s.pickCopy(b, c, p, mirror, resolved)
		if p.clone != nil && resolved.MirrorRepo == "" {
			// A REGISTERED CLONE stands in for the URL the manifest names, so a copy read from the clone's
			// own mirror is held there, not in the URL's: every reader (replay) takes the mirror from here.
			resolved.MirrorRepo = c.Repo
		}
	}
	switch {
	case err != nil:
		return nil, "could not read the patch branch " + c.Raw + ": " + oneLine(err) + localRetry, fetchErr
	case kind == refUnresolved && fetchErr != "":
		return nil, "the patch branch's ?ref=" + c.Ref + " is not in this machine's copy of " + c.Repo + ", and " +
			fetchFailed(fetchErr), fetchErr
	case kind == refUnresolved:
		return nil, "the patch branch's ?ref=" + c.Ref + " names no local branch, remote-tracking branch, tag or commit of " +
			c.Repo + " — " + s.patchBranchDelivery(c), ""
	case kind == refOther:
		return nil, "the patch branch's ?ref=" + c.Ref + " names neither a branch, a tag nor a full commit of " + c.Repo +
			" — name the branch", ""
	}
	held := mirror
	if resolved.MirrorRepo != "" {
		held = s.mirrorPath(resolved.MirrorRepo)
	}
	sc, err := newScratchOf(s.git(), b, s.mirrorPath(upstream), held)
	if err != nil {
		return nil, "could not ask for the patch branch's merge base: " + oneLine(err) + localRetry, fetchErr
	}
	defer sc.remove()
	out, code, err := sc.gitStatus("", "merge-base", commit, tip)
	switch {
	case code == 1:
		return nil, "the patch branch " + c.Raw + " (" + shortCommit(commit) + ") shares no history with " + upstream +
			" — git refuses to merge unrelated histories; a patch branch is a branch whose history includes the " +
			"upstream's: start it from an upstream commit and merge the upstream into it", fetchErr
	case err != nil:
		return nil, "could not ask for the patch branch's merge base with " + upstream + ": " + oneLine(err) + localRetry, fetchErr
	}
	resolved.Ref, resolved.Commit, resolved.Base = ref, commit, strings.TrimSpace(out)
	return resolved, "", fetchErr
}

// resolvePatchBranchRef is THE ONE RESOLUTION of a patch branch's ?ref=, read in the patch branch's mirror with no
// network: the kind that answered, the fully qualified ref that answered ("" for a commit) and its commit. Every
// act that resolves a patch branch — a launch's check, `yolo pack update`, `yolo pack series check --online`,
// `yolo pack lint --online` — reaches it through resolvePatchBranch, so a verdict and a build cannot disagree
// about which ref they judged.
//
// THE ORDER, each asked by its fully qualified name so git's own rules for a bare name (which read
// refs/remotes/<ref> as a remote named after the ref's first segment, never refs/remotes/origin/<ref>) decide
// nothing:
//
//  1. the local branch, refs/heads/<ref>;
//  2. a remote-tracking branch, refs/remotes/<remote>/<ref>: origin's first, then the other remotes' in byte
//     order of the remote's name, so two remotes holding the ref always answer the same way. The remotes are
//     the checkout's own (remotes, its `git remote`; remoteTrackingFor), whose tracking refs the mirror fetches
//     (fetchMirror) and `git remote remove` deletes with the remote. A fetched ref is a delivered ref: no local
//     branch is made for it;
//  3. a tag, refs/tags/<ref>;
//  4. a full commit;
//
// and then anything else git resolves (an abbreviated commit, HEAD) as refOther, which the caller refuses. A ref
// already spelled refs/... is asked by that name alone.
func (s *Store) resolvePatchBranchRef(mirror, ref string, remotes []string) (refKind, string, string, error) {
	ask := func(kind refKind, name string) (refKind, string, string, bool, error) {
		sha, err := s.revParse(mirror, name)
		if err != nil {
			return refUnresolved, "", "", false, fmt.Errorf("reading %s in the pack mirror: %w", name, err)
		}
		if sha == "" {
			return refUnresolved, "", "", false, nil
		}
		return kind, name, sha, true, nil
	}
	if strings.HasPrefix(ref, "refs/") {
		kind := refOther
		switch {
		case strings.HasPrefix(ref, "refs/heads/"), strings.HasPrefix(ref, "refs/remotes/"):
			kind = refBranch
		case strings.HasPrefix(ref, "refs/tags/"):
			kind = refTag
		}
		k, name, sha, _, err := ask(kind, ref)
		return k, name, sha, err
	}
	if k, name, sha, ok, err := ask(refBranch, "refs/heads/"+ref); ok || err != nil {
		return k, name, sha, err
	}
	tracking, err := s.remoteTrackingFor(mirror, ref, remotes)
	if err != nil {
		return refUnresolved, "", "", err
	}
	for _, t := range tracking {
		if k, name, sha, ok, err := ask(refBranch, t); ok || err != nil {
			return k, name, sha, err
		}
	}
	if k, name, sha, ok, err := ask(refTag, "refs/tags/"+ref); ok || err != nil {
		return k, name, sha, err
	}
	if isFullSHA(ref) {
		if k, _, sha, ok, err := ask(refCommit, ref); ok || err != nil {
			return k, "", sha, err
		}
	}
	k, _, sha, _, err := ask(refOther, ref)
	return k, "", sha, err
}

// remoteTrackingFor is every refs/remotes/<remote>/<ref> the mirror holds, origin's first and then by the remote's
// name (resolvePatchBranchRef). A remote is one of remotes, the names the checkout's `git remote` lists, so
// ?ref=mine never matches refs/remotes/origin/alice/mine (remote "origin", branch "alice/mine"). With remotes nil
// (a repository not on this machine, whose remotes cannot be asked) a remote is a name with no slash, git's
// common case, for the same reason.
func (s *Store) remoteTrackingFor(mirror, ref string, remotes []string) ([]string, error) {
	type candidate struct{ remote, name string }
	var found []candidate
	if remotes != nil {
		for _, r := range remotes {
			found = append(found, candidate{r, "refs/remotes/" + r + "/" + ref})
		}
	} else {
		out, err := s.run(mirror, "for-each-ref", "--format=%(refname)", "refs/remotes/")
		if err != nil {
			return nil, fmt.Errorf("listing the remote-tracking refs in the pack mirror: %w", err)
		}
		for _, name := range strings.Split(out, "\n") {
			name = strings.TrimSpace(name)
			rest, ok := strings.CutPrefix(name, "refs/remotes/")
			if !ok {
				continue
			}
			if remote, ok := strings.CutSuffix(rest, "/"+ref); ok && remote != "" && !strings.Contains(remote, "/") {
				found = append(found, candidate{remote, name})
			}
		}
	}
	slices.SortFunc(found, func(a, b candidate) int {
		switch {
		case a.remote == b.remote:
			return 0
		case a.remote == "origin":
			return -1
		case b.remote == "origin":
			return 1
		}
		return strings.Compare(a.remote, b.remote)
	})
	names := make([]string, len(found))
	for i, c := range found {
		names[i] = c.name
	}
	return names, nil
}

// patchBranchDelivery is the next step for a patch branch whose ?ref= nothing answers: for a repository on this
// machine, the fetch that delivers the ref from its remote (origin, else the first by name); for one elsewhere,
// publishing the branch there.
func (s *Store) patchBranchDelivery(c Addr) string {
	dir, local := strings.CutPrefix(c.Repo, "file://")
	if !local {
		return "push the branch to " + c.Repo + ", or correct the patch branch's ?ref="
	}
	var remotes []string
	if out, err := s.run("", "-C", dir, "remote"); err == nil {
		remotes = strings.Fields(out)
	}
	if len(remotes) == 0 {
		return "create the branch in " + dir + " (it has no remote to fetch it from), or correct the patch branch's ?ref="
	}
	slices.Sort(remotes)
	remote := remotes[0]
	if slices.Contains(remotes, "origin") {
		remote = "origin"
	}
	return "fetch it: `" + shquote.JoinDisplay([]string{"git", "-C", dir, "fetch", "--", remote, c.Ref}) +
		"`, or correct the patch branch's ?ref="
}
