package packsrc

// localclonesnarrow_test.go pins PF-D104: a registered local clone answers for a patch branch URL only for a
// branch name it holds as the MATCHED remote's tracking ref, compared against that remote alone. Anything else —
// another remote's branch, a local-only branch, a refs/... spelling — is built from the URL as if there were no
// clone, and the check says which clone was not used and why.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// publishedRemote makes the machine's origin the PUBLISHED address, as a user's clone of a published branch has
// it (git@github.com:o/r.git), with ssh stood in so that address fetches the fixture's origin with no network.
func (f *checkoutFixture) publishedRemote(t *testing.T) {
	t.Helper()
	gitIn(t, f.machine, "remote", "set-url", "origin", "git@github.com:o/r.git")
	fakeSSH(t, f.origin)
}

// fakeSSH stands in ssh for every git this test runs: whatever host and path it is asked for, it serves repo.
func fakeSSH(t *testing.T, repo string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-ssh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec git-upload-pack '"+repo+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	t.Setenv("GIT_SSH_VARIANT", "simple")
}

// urlReachesOrigin makes the published URL fetch the fixture's origin, as a URL the machine reaches does, so a
// patch branch built from the URL (no clone) builds in a test with no network.
func (f *checkoutFixture) urlReachesOrigin(t *testing.T) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	body := "[url \"" + f.origin + "\"]\n\tinsteadOf = https://github.com/o/r\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
}

// checkRef runs a fresh launch's check of the published URL at ref.
func (f *checkoutFixture) checkRef(t *testing.T, ref string) CheckResult {
	t.Helper()
	w := f.want()
	w.PatchBranch = "git+https://github.com/o/r?ref=" + ref
	res := f.store.CheckPatched(w, CheckOptions{Now: func() time.Time { return f.now }})
	if res.Err != nil {
		t.Fatalf("CheckPatched: %v", res.Err)
	}
	if res.Record == nil || res.Record.Check == nil {
		t.Fatal("no check record")
	}
	return res
}

// machineClone is the fixture's machine registered with its origin written as the published URL, and, when
// fork is set, a second remote.
func (f *checkoutFixture) machineClone(fork string) LocalClone {
	c := LocalClone{Dir: f.machine, Explicit: true, From: "~/src/machine",
		Remotes: []CloneRemote{{Name: "origin", URL: "git@github.com:o/r.git"}}}
	if fork != "" {
		c.Remotes = append(c.Remotes, CloneRemote{Name: "fork", URL: fork})
	}
	return c
}

// A branch the clone holds only on ANOTHER remote (its fork) is not the published repository's, so the clone
// does not answer for it: the check reads the URL, which lacks it, and says the clone was not used and why.
func TestACloneDoesNotAnswerForAnotherRemotesBranch(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	f.urlReachesOrigin(t)
	fork := filepath.Join(filepath.Dir(f.machine), "fork.git")
	gitIn(t, filepath.Dir(f.machine), "clone", "-q", "--bare", f.origin, fork)
	gitIn(t, f.machine, "remote", "add", "fork", fork)
	gitIn(t, f.machine, "checkout", "-q", "-b", "theirs", gitIn(t, f.pusher, "rev-parse", "mine"))
	commitFile(t, f.machine, "theirs.txt", "theirs\n")
	gitIn(t, f.machine, "push", "-q", "fork", "theirs")
	gitIn(t, f.machine, "checkout", "-q", "main")
	useClones(t, f.machineClone(fork))
	res := f.checkRef(t, "theirs")
	if pb := res.Record.PatchBranch; pb != nil && pb.Checkout != "" {
		t.Fatalf("built %s from the clone %s, whose branch is another remote's", pb.Commit, pb.Checkout)
	}
	p := res.Record.Check.Problem
	for _, want := range []string{"local clone " + f.machine, "not used", "refs/remotes/origin/theirs", "as if there were no clone"} {
		if !strings.Contains(p, want) {
			t.Errorf("the check's problem %q lacks %q", p, want)
		}
	}
}

// A branch the clone has only locally (not fetched as the matched remote's) is built from the URL, which has it,
// and the line names the clone, why it was not used, and the fetch that would let it answer.
func TestALocalOnlyBranchThroughACloneBuildsFromTheURL(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	f.urlReachesOrigin(t)
	gitIn(t, f.pusher, "checkout", "-q", "-b", "later", "mine")
	pushed := commitFile(t, f.pusher, "later.txt", "pushed\n")
	gitIn(t, f.pusher, "push", "-q", "origin", "later")
	gitIn(t, f.machine, "checkout", "-q", "-b", "later", gitIn(t, f.pusher, "rev-parse", "mine"))
	local := commitFile(t, f.machine, "later.txt", "local one\n")
	commitFile(t, f.machine, "later.txt", "local two\n")
	gitIn(t, f.machine, "checkout", "-q", "main")
	useClones(t, f.machineClone(""))
	res := f.checkRef(t, "later")
	if p := res.Record.Check.Problem; p != "" {
		t.Fatalf("check problem: %s", p)
	}
	pb := res.Record.PatchBranch
	if pb == nil || pb.Checkout != "" || pb.Commit != pushed {
		t.Fatalf("resolved %+v, want the URL's tip %s and no clone (the clone's local-only branch is past %s)", pb, pushed, local)
	}
	if !pb.Loud() {
		t.Error("a clone that matched and was not used is said quietly")
	}
	wantLine(t, pb, f.now, "local clone "+f.machine, "not used", "refs/remotes/origin/later",
		"git -C "+f.machine+" fetch origin")
}

// A refs/... spelling never goes through a clone: the URL answers.
func TestARefsSpellingThroughACloneBuildsFromTheURL(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	f.urlReachesOrigin(t)
	f.localMine(t, gitIn(t, f.pusher, "rev-parse", "mine"), 1)
	useClones(t, f.machineClone(""))
	res := f.checkRef(t, "refs/heads/mine")
	pb := res.Record.PatchBranch
	if pb == nil || pb.Checkout != "" || pb.Commit != gitIn(t, f.pusher, "rev-parse", "mine") {
		t.Fatalf("resolved %+v, want the URL's mine and no clone (problem %q)", pb, res.Record.Check.Problem)
	}
	wantLine(t, pb, f.now, "local clone "+f.machine, "no branch name")
}

// Inside the throttle a fresh launch re-reads a clone the URL resolved to, as it does a checkout, so a commit made
// in the clone since the last check builds now (isCheckoutLocator's clone arm).
func TestAFreshLaunchPicksUpANewCloneCommitInsideTheThrottle(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	f.localMine(t, gitIn(t, f.pusher, "rev-parse", "mine"), 1)
	useClones(t, f.machineClone(""))
	f.cloneLaunch(t)
	gitIn(t, f.machine, "checkout", "-q", "mine")
	tip := commitFile(t, f.machine, "local.txt", "newer\n")
	gitIn(t, f.machine, "checkout", "-q", "main")
	res := f.cloneLaunch(t)
	if got := res.Record.PatchBranch.Commit; got != tip {
		t.Fatalf("the launch inside the throttle built %s, want the clone's new commit %s", got, tip)
	}
}

// A remote with two url values matches on either, but git fetches the first: a fork listed first must not answer
// for the published URL listed second. The URL builds, and the line says why the clone was not used.
func TestARemoteWhoseFirstURLIsAnotherRepositoryDoesNotAnswer(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	f.urlReachesOrigin(t)
	f.localMine(t, gitIn(t, f.pusher, "rev-parse", "mine"), 1)
	gitIn(t, f.machine, "remote", "set-url", "origin", "git@github.com:someone/fork.git")
	gitIn(t, f.machine, "remote", "set-url", "--add", "origin", "git@github.com:o/r.git")
	useClones(t, f.machineClone(""))
	res := f.checkRef(t, "mine")
	pb := res.Record.PatchBranch
	if pb == nil || pb.Checkout != "" || pb.Commit != gitIn(t, f.pusher, "rev-parse", "mine") {
		t.Fatalf("resolved %+v, want the URL's mine and no clone (problem %q)", pb, res.Record.Check.Problem)
	}
	wantLine(t, pb, f.now, "local clone "+f.machine, "not used", "fetches git@github.com:someone/fork.git")
}

// The published URL deleted the branch while the clone's tracking ref still holds it: the clone's local branch
// builds, as for any checkout, but through the map it changes the build of a pack that names the URL, so it is
// said loudly. The pack's own checkout of the same state stays quiet (request 28).
func TestABranchThePublishedURLDeletedBuildsTheCloneLoudly(t *testing.T) {
	f := newCheckoutFixture(t)
	f.publishedRemote(t)
	tip := f.localMine(t, gitIn(t, f.pusher, "rev-parse", "mine"), 1)
	gitIn(t, f.pusher, "push", "-q", "origin", "--delete", "mine")
	useClones(t, f.machineClone(""))
	pb := f.cloneLaunch(t).Record.PatchBranch
	if pb.Commit != tip || pb.Checkout != f.machine || !pb.RemoteGone {
		t.Fatalf("resolved %+v, want the clone's local branch %s with the remote gone", pb, tip)
	}
	if !pb.Loud() {
		t.Error("a clone reached through the map built a branch the published URL deleted, quietly")
	}
	wantLine(t, pb, f.now, "which deleted it", "local clone "+f.machine)

	own := newCheckoutFixture(t)
	own.localMine(t, gitIn(t, own.pusher, "rev-parse", "mine"), 1)
	gitIn(t, own.pusher, "push", "-q", "origin", "--delete", "mine")
	if r := own.launch(t).Record.PatchBranch; !r.RemoteGone || r.Loud() {
		t.Errorf("the pack's own checkout: RemoteGone=%v Loud=%v, want gone and quiet", r.RemoteGone, r.Loud())
	}
}
