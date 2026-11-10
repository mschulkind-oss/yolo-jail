package gitbroker

import (
	"strings"
	"testing"
)

// A broker outlives the yolo that started it: the jail keeps running while the host installs a
// newer yolo, and that newer `yolo allow` (or `yolo request`) rewrites the user config with the
// capability names IT declares. On 2026-11-09 the owner's `allow.github.on_request` was
// rewritten from `dispatch_workflows` to `runs` while a git broker started by an older yolo,
// which declares no `runs`, kept serving the jail. That broker resolved the WHOLE `allow` table
// at every push, failed on the GitHub entry, and so treated `allow.git` as unvalidated: the
// declared `branch:main` cover vanished and every fast-forward of main asked for approval.
//
// The git broker's write authority is `allow.git` alone. A GitHub table it cannot read, however
// it came to be, must not withdraw a branch grant the git table still allows; a git table it
// cannot read still fails closed, and the push's refusal names what failed instead of guessing.
func TestAnotherSourcesUnreadableAllowanceDoesNotWithdrawTheGitBranchGrant(t *testing.T) {
	const gitAllow = "always = ['fetch']\non_request = ['push']\n"
	f := readableGitRequestFixtureWithConfig(t, "push", "push = ['main']", func() {
		writeReadableGitUserTOMLWithPacks(t, "['git', 'github']", gitAllow+"\n[allow.github]\nalways = ['read']\n", "")
	})
	f.commit("before the newer yolo's rewrite")
	f.mustPush("origin", "HEAD:main")

	// What a newer yolo's `yolo allow github on_request …` writes: a capability this yolo's
	// github pack does not declare.
	writeReadableGitUserTOMLWithPacks(t, "['git', 'github']",
		gitAllow+"\n[allow.github]\nalways = ['read']\non_request = ['a_capability_a_newer_yolo_declares']\n", "")
	f.commit("after the newer yolo's rewrite")
	if out, err := f.git("push", "origin", "HEAD:main"); err != nil {
		t.Fatalf("an unreadable allow.github withdrew the git branch grant: %v\n%s", err, out)
	}
	if got, want := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("upstream main is %q, want %q", got, want)
	}
	if p := lastPush(t); p.Set != setWorkingSet || len(p.Refs) != 1 || p.Refs[0].CoveredBy != "branch:main" {
		t.Fatalf("the push was not covered by the declared branch grant: %+v", p)
	}

	// The git table itself naming a capability this yolo does not declare still covers nothing,
	// and the refusal says which entry failed.
	writeReadableGitUserTOMLWithPacks(t, "['git', 'github']",
		"always = ['fetch']\non_request = ['push', 'a_git_capability_a_newer_yolo_declares']\n", "")
	f.commit("after an unreadable allow.git")
	before := f.upstreamRef("refs/heads/main")
	out, err := f.git("push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("an unreadable allow.git still covered the push: err=%v\n%s", err, out)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("a push under an unreadable allow.git reached upstream")
	}
	if !strings.Contains(out, "a_git_capability_a_newer_yolo_declares") {
		t.Fatalf("the refusal does not name the entry that could not be validated:\n%s", out)
	}

	// The error is the user config's words, carried to the jail's terminal: a key naming a
	// terminal control sequence reaches it only as visible text (termsafe.Visible).
	writeReadableGitUserTOMLWithPacks(t, "['git', 'github']",
		"always = ['fetch']\non_request = ['push']\n\"k\\u001b]0;owned\\u0007\" = true\n", "")
	f.commit("after a control sequence in allow.git")
	out, err = f.git("push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("an unreadable allow.git still covered the push: err=%v\n%s", err, out)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("the refusal carried a raw control sequence from the user config:\n%q", out)
	}
}
