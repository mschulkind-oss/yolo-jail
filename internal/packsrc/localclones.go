package packsrc

// localclones.go is the LOCAL CLONE MAP (a term coined here; docs/reference/patched-forks.md §3.5, PF-D100):
// the git clones on this machine the user's config reaches, each keyed by the URLs of its remotes, so a patch
// branch a pack names by its PUBLISHED URL is resolved against the user's own clone of that repository with
// the newest-copy-wins rule of a local checkout (patchbranchcopy.go, PF-D99).
//
// The maintainer's rulings (2026-10-24): a pack file always names published URLs; "if you have a path into
// any local clone of a repository, that adds that repository to the mapping, and because of the newest wins,
// this is safe even for other packs"; and "we can just also have a user level config to register them
// regardless" — `local_clones`.
//
//   - WHO REGISTERS. config (localclones.go there) gathers the map from the USER config: every path a pack is
//     read from that lands inside a git clone (a pack named by path or by git+file://, the conventional local
//     pack), and every `local_clones` entry. A plain-files pack in no repository registers nothing. A broken
//     `local_clones` value is the source's error, and the check refuses with it.
//   - WHEN IT APPLIES. A patch branch whose repository is a URL (not a directory on this machine) that names
//     the same repository as a remote of a registered clone is resolved as that clone's local checkout, with
//     ONLY THAT REMOTE compared against (PF-D104). The record still keys the resolution by the locator as
//     written, so a reader with no map (a jail) reads the same record.
//   - ONLY A REF THE URL HAS (PF-D104). Newest-wins is safe only for a branch the published repository has, so
//     a clone answers only for a branch name (never a refs/... spelling, a tag or a commit) that the clone holds
//     as that remote's tracking ref, refs/remotes/<remote>/<ref>, from a remote whose fetch address (its first url,
//     after insteadOf) names that repository. The clone then answers as any checkout does: ahead, diverged (loud)
//     or after a failed fetch (loud) its local branch builds. Anything else — a local-only branch, another remote's branch —
//     is built from the URL as if no clone existed, and the line names the clone and why it was not used.
//   - WHICH CLONE. An explicit `local_clones` entry before an implicit one, then config order (the order the
//     source returns them in). The provenance line names the clone it used and why.
//   - ONCE PER CHECK. CheckPatched asks the source once, and only for a patch branch at a URL (chooseClone).
//   - READ ONLY. Gathering runs `git rev-parse --show-toplevel` and reads the clone's remote URLs from its
//     configuration; choosing reads the tracking ref; nothing is ever written to the clone.

import (
	"errors"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// LocalClone is one clone the local clone map holds.
type LocalClone struct {
	// Dir is the clone's top level, as `git rev-parse --show-toplevel` names it.
	Dir string
	// Explicit is true for a clone a `local_clones` entry registered, false for one a pack's path did.
	Explicit bool
	// From is the config entry that registered it, as written: the `local_clones` path, or the pack's name.
	From string
	// Remotes are the clone's remotes, by name and URL as its configuration writes them.
	Remotes []CloneRemote
}

// CloneRemote is one remote of a LocalClone.
type CloneRemote struct{ Name, URL string }

// Via is how the clone was registered, as a line says it.
func (c LocalClone) Via() string {
	if c.Explicit {
		return "local_clones entry " + c.From
	}
	return "the path of pack " + c.From
}

var (
	localCloneMu     sync.Mutex
	localCloneSource func() ([]LocalClone, error)
)

// SetLocalCloneSource installs the function that gathers the local clone map, which config installs (it
// reads the user config, which this package does not), and returns the one it replaced. Nil means no map.
func SetLocalCloneSource(f func() ([]LocalClone, error)) func() ([]LocalClone, error) {
	localCloneMu.Lock()
	defer localCloneMu.Unlock()
	old := localCloneSource
	localCloneSource = f
	return old
}

func localClones() ([]LocalClone, error) {
	localCloneMu.Lock()
	f := localCloneSource
	localCloneMu.Unlock()
	if f == nil {
		return nil, nil
	}
	return f()
}

// RepoIdentity is the identity a repository URL is matched by: gitproto.Identity for ssh://, https:// and
// scp-style spellings (host lower-cased, user, a trailing / and .git dropped), so
// git@github.com:o/r.git, ssh://git@github.com/o/r and https://github.com/o/r are one repository; for a
// file:// URL or a local path, "file:" and the cleaned absolute path. "" for anything else.
func RepoIdentity(raw string) string {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "git+")
	if raw == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(raw, "file://"); ok {
		if u, err := url.Parse(raw); err == nil && u.Path != "" {
			rest = u.Path
		}
		return localIdentity(rest)
	}
	if filepath.IsAbs(raw) {
		return localIdentity(raw)
	}
	if strings.HasPrefix(strings.ToLower(raw), "http://") {
		raw = "https://" + raw[len("http://"):]
	}
	id, err := gitproto.Identity(raw)
	if err != nil {
		return ""
	}
	return id
}

func localIdentity(p string) string {
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return "file:" + strings.TrimSuffix(strings.TrimSuffix(p, "/"), "/.git")
}

// CloneMatch is the registered clone a patch branch URL resolved to, and every other clone that matched.
type CloneMatch struct {
	Clone  LocalClone
	Remote CloneRemote
	// Others are the further matching clones' directories, in the order they lost.
	Others []string
}

// matchLocalClone is the registered clone whose remote names repo, nil for none: an explicit clone first,
// then the map's order.
func matchLocalClone(repo string, clones []LocalClone) *CloneMatch {
	want := RepoIdentity(repo)
	if want == "" {
		return nil
	}
	var m *CloneMatch
	for _, explicit := range []bool{true, false} {
		for _, c := range clones {
			if c.Explicit != explicit {
				continue
			}
			for _, r := range c.Remotes {
				if RepoIdentity(r.URL) != want {
					continue
				}
				if m == nil {
					m = &CloneMatch{Clone: c, Remote: r}
				} else if c.Dir != m.Clone.Dir {
					m.Others = append(m.Others, c.Dir)
				}
				break
			}
		}
	}
	return m
}

// cloneChoice is what one check decided about a patch branch URL and the local clone map: the clone it resolves
// against (match), or, when a clone matched and cannot answer for the ref, why not (skip). Zero for a patch
// branch that is no URL, or that no clone matches.
type cloneChoice struct {
	match *CloneMatch
	skip  string
	// why is skip's reason alone, as the launch line of a `local` pack says it (localcopy.go).
	why string
	// fetchURL is the matched remote's address as git fetches it (`git remote get-url`: its first url value,
	// after insteadOf), which chooseClone checked names the patch branch's repository.
	fetchURL string
}

// chooseClone is c's cloneChoice: the map asked once (the source's error is the check's), the matching clone,
// and whether it may answer for c's ref (PF-D104).
func (s *Store) chooseClone(c Addr) (cloneChoice, error) {
	if c.Repo == "" || strings.HasPrefix(c.Repo, "file://") {
		return cloneChoice{}, nil
	}
	clones, err := localClones()
	if err != nil {
		return cloneChoice{}, err
	}
	return s.chooseCloneFrom(c, clones), nil
}

// chooseCloneFrom is chooseClone over a list of clones the caller supplies: the map's, or the one clone a
// pack's `local` key names (localcopy.go).
func (s *Store) chooseCloneFrom(c Addr, clones []LocalClone) cloneChoice {
	m := matchLocalClone(c.Repo, clones)
	if m == nil {
		return cloneChoice{}
	}
	notUsed := func(why string) cloneChoice {
		return cloneChoice{why: why, skip: "local clone " + m.Clone.Dir + " (" + m.Clone.Via() + ") not used for ?ref=" + c.Ref +
			": " + why + "; built from " + c.Repo + " as if there were no clone"}
	}
	name, ok := branchName(c.Ref)
	if !ok || strings.HasPrefix(c.Ref, "refs/") {
		return notUsed("a clone answers only for a branch name its remote " + m.Remote.Name + " has, and this is " +
			"no branch name")
	}
	// THE ADDRESS GIT FETCHES must name the same repository: a remote can carry several url values, matched on
	// any, while git fetches the first, rewritten by any insteadOf. A fork listed first, or a rewrite elsewhere,
	// would otherwise answer for the published URL.
	out, err := s.run("", "-C", m.Clone.Dir, "remote", "get-url", "--", m.Remote.Name)
	fetchURL := strings.TrimSpace(out)
	switch {
	case err != nil || fetchURL == "":
		return notUsed("the address of its remote " + m.Remote.Name + " could not be read (" + oneLine(err) + ")")
	case RepoIdentity(fetchURL) != RepoIdentity(c.Repo):
		return notUsed("its remote " + m.Remote.Name + " fetches " + fetchURL + " (its first url value, after any " +
			"insteadOf), which is not that repository, so the clone's copy may not be the published one — make " +
			"the published URL the remote's only url, or remove the rewrite")
	}
	tracking := "refs/remotes/" + m.Remote.Name + "/" + name
	sha, err := s.run("", "-C", m.Clone.Dir, "rev-parse", "--verify", "--quiet", tracking+"^{commit}")
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return notUsed("its " + tracking + " could not be read (" + oneLine(err) + ")")
	}
	if strings.TrimSpace(sha) == "" {
		return notUsed("the clone has no " + tracking + ", so the branch is not one the published repository " +
			"has as far as the clone knows (a local-only branch, or another remote's, never stands in for the " +
			"URL) — if it is pushed, `git -C " + shquote.QuoteDisplay(m.Clone.Dir) + " fetch " + m.Remote.Name + "` and the next launch uses " +
			"the clone")
	}
	return cloneChoice{match: m, fetchURL: fetchURL}
}

// patchBranchPlan is the address a check fetches and resolves a patch branch at, and its checkout plan: a
// checkout's own (checkoutPlanOf), or, for a URL a registered clone may answer for (ch.match), that clone's, with
// only the matched remote compared against and read. The returned address keeps c.Raw, which keys the record.
func (s *Store) patchBranchPlan(c Addr, ch cloneChoice) (Addr, *checkoutPlan) {
	m := ch.match
	if m == nil {
		return c, s.checkoutPlanOf(c)
	}
	local := Addr{Kind: KindGit, Raw: c.Raw, Repo: "file://" + filepath.ToSlash(m.Clone.Dir), Ref: c.Ref}
	p := s.checkoutPlanOf(local)
	if p == nil {
		return c, s.checkoutPlanOf(c)
	}
	// The address chooseClone checked names the published repository; the fetch reads exactly it.
	p.remote, p.remotes, p.url = m.Remote.Name, []string{m.Remote.Name}, ch.fetchURL
	p.clone = m
	return local, p
}
