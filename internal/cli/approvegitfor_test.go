package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestApproveGitForAndAddCLI drives `yolo approve <id> --for <duration>` and `--add` over the
// git store (GB-D26): each records working-set entries on the refs that needed approval and no
// command grant, lists them (a timed one with its expiry), and refuses what cannot apply with
// the answer that does. GitHub's per-resource answers are TestApproveGitHubForAndAddCLI's.
func TestApproveGitForAndAddCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	const remote = "git@fixture.test:o/r.git"
	sf := brokerscope.File{Source: gitbroker.Source, LaunchID: "registry-git-for", PID: os.Getpid(), Container: "registry-git-for-jail",
		Workspace: "/fixture", Form: brokerscope.FormRemoteURL, Repos: []string{remote},
		Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "*"}}}
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	const start = "0123456789abcdef0123456789abcdef"
	_, lease, err := brokercore.BindLease(gitbroker.Source, path, sf, start)
	if err != nil {
		t.Fatal(err)
	}
	defer brokercore.CloseLease(gitbroker.Source, lease)

	const zero = "0000000000000000000000000000000000000000"
	const old, updated = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	empty := sha256.Sum256(nil)
	s := gitbroker.ApprovalStore()
	file := func(needs string, lines ...string) brokerstore.Request {
		t.Helper()
		q, err := s.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: start, Workspace: sf.Workspace,
			LaunchID: sf.LaunchID, Argv: append([]string{"git-receive-pack"}, lines...),
			Repo: "fixture.test/o/r", Set: "push", StdinDigest: hex.EncodeToString(empty[:]),
			Policy:        brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 24 * time.Hour, Uses: 1},
			NeedsApproval: []string{needs}})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	run := func(args ...string) (int, string, string) {
		return captureStd(t, func() int { return registry["approve"](append([]string{"approve"}, args...)) })
	}

	// --for: a timed entry on the ref, no grant, and a decision naming its end.
	forReq := file("refs/heads/main", old+" "+updated+" refs/heads/main")
	rc, out, errs := run(forReq.ID, "--for", "10m")
	if rc != 0 || !strings.Contains(out, "pushes to refs/heads/main") || !strings.Contains(out, "until ") {
		t.Fatalf("--for: rc %d\n%s%s", rc, out, errs)
	}
	rc, out, _ = run()
	if rc != 0 || !strings.Contains(out, "working set") || !strings.Contains(out, "branch refs/heads/main") || !strings.Contains(out, "allowed until ") {
		t.Fatalf("listing of the timed entry: rc %d\n%s", rc, out)
	}
	if _, gs, err := s.List(); err != nil || len(gs) != 0 {
		t.Fatalf("--for recorded a command grant: %+v %v", gs, err)
	}
	if rc, _, errs = run(forReq.ID, "--for", "10m"); rc != approveExitAlreadyDecided {
		t.Fatalf("a decided request: rc %d %q", rc, errs)
	}

	// A duration beyond the ceiling, and a session, are policy refusals.
	tooLong := file("refs/heads/dev", old+" "+updated+" refs/heads/dev")
	if rc, _, errs = run(tooLong.ID, "--for", "25h"); rc != approveExitPolicy || !strings.Contains(errs, "ceiling") {
		t.Fatalf("--for 25h: rc %d %q", rc, errs)
	}

	// --add: the branch joins the working set for good (no expiry).
	rc, out, errs = run(tooLong.ID, "--add")
	if rc != 0 || !strings.Contains(out, "added refs/heads/dev to the working set") {
		t.Fatalf("--add: rc %d\n%s%s", rc, out, errs)
	}
	ws, err := s.Working()
	if err != nil {
		t.Fatal(err)
	}
	var lasting bool
	for _, e := range ws {
		if e.Resource == "refs/heads/dev" && e.Live() && !e.Timed() && e.By == "human" {
			lasting = true
		}
	}
	if !lasting {
		t.Fatalf("working set %+v", ws)
	}

	// --add on a tag and on a creation refuses, names --for, and files nothing.
	tag := file("refs/tags/v1", old+" "+updated+" refs/tags/v1")
	created := file("refs/heads/agent/new", zero+" "+updated+" refs/heads/agent/new")
	for _, q := range []brokerstore.Request{tag, created} {
		rc, _, errs = run(q.ID, "--add")
		if rc != approveExitPolicy || !strings.Contains(errs, "--for 15m") || strings.Count(errs, "Next:") != 1 {
			t.Fatalf("--add on %v: rc %d %q", q.Argv, rc, errs)
		}
	}
	if rc, _, errs = run(tag.ID, "--for", "session"); rc != approveExitPolicy || !strings.Contains(errs, "--for 15m") {
		t.Fatalf("--for session: rc %d %q", rc, errs)
	}
	if rc, out, errs = run(tag.ID, "--for", "15m"); rc != 0 {
		t.Fatalf("--for on a tag: rc %d\n%s%s", rc, out, errs)
	}
}
