package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestApproveGitHubForAndAddCLI drives `yolo approve <id> --add` and `--for <duration>` over the
// GitHub store as the CLI opens it (docs/design/boundary-broker.md §6.4, BB-D79, BB-D80,
// BB-D92): --add puts the PR a write targets in the workspace's working set, --for records a
// timed entry on the request's resource, the listing shows both as w- entries, --revoke ends
// one, and the answers that cannot apply (--add on a merge, --for session, either on a write
// naming no PR number) are refused naming the one that does.
func TestApproveGitHubForAndAddCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	sf := brokerscope.File{Source: ghbroker.Source, LaunchID: "registry-gh-for", PID: os.Getpid(), Container: "registry-gh-for-jail",
		Workspace: "/fixture", Repos: []string{"o/r"}, Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "*"}}}
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	const start = "0123456789abcdef0123456789abcdef"
	_, lease, err := brokercore.BindLease(ghbroker.Source, path, sf, start)
	if err != nil {
		t.Fatal(err)
	}
	defer brokercore.CloseLease(ghbroker.Source, lease)

	s := ghbroker.ApprovalStore()
	file := func(argv ...string) brokerstore.Request {
		t.Helper()
		// The broker files the argv of its second parse, as Authority.Classify does.
		d := ghbroker.Classify(argv, "o/r", ghbroker.NewScope(sf.Repos))
		d = ghbroker.Classify(d.Argv, "o/r", ghbroker.NewScope(sf.Repos))
		if d.Outcome != ghbroker.OutcomeWindowed {
			t.Fatalf("%v is not a write: %+v", argv, d)
		}
		policy, err := ghbroker.ApprovalAuthority(brokerstore.Request{LaunchID: sf.LaunchID, Start: start, Workspace: sf.Workspace,
			Jail: paths.JailShortHash(sf.Container), Argv: d.Argv, Repo: strings.Join(d.Repos, ","), Set: d.Set})
		if err != nil {
			t.Fatal(err)
		}
		q, err := s.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: start, Workspace: sf.Workspace,
			LaunchID: sf.LaunchID, Argv: d.Argv, Repo: strings.Join(d.Repos, ","), Set: d.Set, StdinDigest: "stdin", Policy: policy})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	run := func(args ...string) (int, string, string) {
		return captureStd(t, func() int { return registry["approve"](append([]string{"approve"}, args...)) })
	}

	// --add: the PR joins the working set for good.
	add := file("pr", "comment", "12", "--body", "x")
	rc, out, errs := run(add.ID, "--add")
	if rc != 0 || !strings.Contains(out, "added PR github.com/o/r#12 to the working set of /fixture") {
		t.Fatalf("--add: rc %d\n%s%s", rc, out, errs)
	}
	e, ok, err := s.LiveWorkingKind("/fixture", "github.com/o/r", "#12", brokerstore.WorkingPR)
	if err != nil || !ok || e.Timed() || e.By != "human" {
		t.Fatalf("--add entry %+v %v %v", e, ok, err)
	}

	// --for: a timed merge grant; --add and session are refused for a merge.
	merge := file("pr", "merge", "12", "--squash")
	if rc, _, errs = run(merge.ID, "--add"); rc != approveExitPolicy || !strings.Contains(errs, "a merge asks per PR") {
		t.Fatalf("--add on a merge: rc %d %q", rc, errs)
	}
	if rc, _, errs = run(merge.ID, "--for", "session"); rc != approveExitPolicy || !strings.Contains(errs, "--for 15m") {
		t.Fatalf("--for session: rc %d %q", rc, errs)
	}
	rc, out, errs = run(merge.ID, "--for", "15m")
	if rc != 0 || !strings.Contains(out, "merges of github.com/o/r#12 for every jail of /fixture until ") {
		t.Fatalf("--for on a merge: rc %d\n%s%s", rc, out, errs)
	}
	if _, gs, err := s.List(); err != nil || len(gs) != 0 {
		t.Fatalf("--add or --for recorded a command grant: %+v %v", gs, err)
	}

	// A write naming no PR number takes Allow once alone.
	branchSel := file("pr", "comment", "agent/x", "--body", "x")
	if rc, _, errs = run(branchSel.ID, "--add"); rc != approveExitPolicy || !strings.Contains(errs, "names no PR or issue by number") {
		t.Fatalf("--add on an unresolved write: rc %d %q", rc, errs)
	}

	// The listing shows both entries as the git source's do.
	rc, out, _ = run()
	if rc != 0 || !strings.Contains(out, e.ID+"  working set  github") || !strings.Contains(out, "pr #12") ||
		!strings.Contains(out, "merge #12") || !strings.Contains(out, "allowed until ") {
		t.Fatalf("listing: rc %d\n%s", rc, out)
	}

	// --revoke ends the PR's entry.
	if rc, out, errs = run("--revoke", e.ID); rc != 0 {
		t.Fatalf("--revoke: rc %d\n%s%s", rc, out, errs)
	}
	if _, ok, _ := s.LiveWorkingKind("/fixture", "github.com/o/r", "#12", brokerstore.WorkingPR); ok {
		t.Fatal("a revoked entry stayed live")
	}
}
