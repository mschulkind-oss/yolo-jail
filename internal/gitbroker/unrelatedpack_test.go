package gitbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An unrelated selected pack whose local source is gone must not withdraw the git broker's
// standing branch grant (2026-11-24: 316 config-covered pushes, then one asked for approval
// because a removed `tillr` source made the selection unresolvable). This pins the CALL SITE:
// the push's cover reads the user config through coverForSnapshot, so it fails if that read
// regresses to a selection-wide failure. The broker rereads the config on every push, so
// repairing or deselecting the pack restores the grant with no restart.
func TestAVanishedUnrelatedPackDoesNotWithdrawTheGitBranchGrant(t *testing.T) {
	const gitAllow = "always = ['fetch']\non_request = ['push']\n"
	f := readableGitRequestFixtureWithConfig(t, "push", "push = ['main']", func() {
		writeReadableGitUserTOMLWithPacks(t, "['git']", gitAllow, "")
	})
	f.commit("before the pack vanishes")
	f.mustPush("origin", "HEAD:main")

	root := t.TempDir()
	withGone := "['git', 'src/gone']\n\n[pack_sources]\nsrc = '" + root + "'\n"
	writeReadableGitUserTOMLWithPacks(t, withGone, gitAllow, "")
	f.commit("while the pack is gone")
	out, err := f.git("push", "origin", "HEAD:main")
	if err != nil {
		t.Fatalf("a vanished unrelated pack withdrew the git branch grant: %v\n%s", err, out)
	}
	if p := lastPush(t); p.Set != setWorkingSet || len(p.Refs) != 1 || p.Refs[0].CoveredBy != "branch:main" {
		t.Fatalf("the push was not covered by the declared branch grant: %+v", p)
	}
	if !strings.Contains(out, "src/gone") || !strings.Contains(out, filepath.Join(root, "gone")) || !strings.Contains(out, "yolo pack remove src/gone") {
		t.Fatalf("the push does not name the skipped pack, its source and the next step:\n%s", out)
	}

	// A manifest nobody can read could declare git itself: the grant is withdrawn again, the
	// refusal names the cause, and nothing reaches upstream.
	if err := os.MkdirAll(filepath.Join(root, "gone"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gone", "pack.toml"), []byte("this is [not toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.commit("while the pack is unreadable")
	before := f.upstreamRef("refs/heads/main")
	if out, err := f.git("push", "origin", "HEAD:main"); err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("an unreadable unrelated pack still covered the push: err=%v\n%s", err, out)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("a push under an unresolvable selection reached upstream")
	}

	// Repaired: the very next push, same broker, is covered again.
	if err := os.Remove(filepath.Join(root, "gone", "pack.toml")); err != nil {
		t.Fatal(err)
	}
	if out, err := f.git("push", "origin", "HEAD:main"); err != nil {
		t.Fatalf("repairing the pack did not restore the grant on the next push: %v\n%s", err, out)
	}
	// Deselected: likewise, with nothing to disclose.
	writeReadableGitUserTOMLWithPacks(t, "['git']", gitAllow, "")
	f.commit("after deselecting the pack")
	if out, err := f.git("push", "origin", "HEAD:main"); err != nil || strings.Contains(out, "was skipped") {
		t.Fatalf("deselecting the pack: err=%v\n%s", err, out)
	}
}
