package gitbroker

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The user part of an SSH remote reaches ssh's argv, where an ssh config's %r expands it: one
// outside [A-Za-z0-9._-], or an SSH URL whose percent-decoding could disagree with the broker's
// exact-scope identity and upstream path, is refused before ssh runs.
func TestSSHUserAndEncodingRefused(t *testing.T) {
	f := newFixture(t, "")
	for _, bad := range []string{"ssh://a%0Ab@fixture.test/o/r", "ssh://%24%28id%29@fixture.test/o/r", "a;id@fixture.test:o/r",
		"ssh://git@fixture.test/o/%72.git", "ssh://git@fixtur%65.test/o/r.git"} {
		f.setScope(bad)
		rc, stdout, stderr := f.helper(bad, serviceUploadPack, nil)
		if rc == 0 || stdout != "connect\n\n" {
			t.Errorf("%q: rc %d stdout %q stderr %q", bad, rc, stdout, stderr)
		}
		if f.sshRan() {
			t.Fatalf("%q: the fake ssh ran", bad)
		}
	}
}

// A call that ends (the jail went away, or the daemon was told to stop) ends the upstream
// and everything it started, even a child that keeps ssh's stderr open.
func TestCancelEndsTheUpstreamsChildren(t *testing.T) {
	f := newFixture(t, "sleep 30 &\nsleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	done := make(chan int, 1)
	go func() {
		done <- f.broker.serveRequest(ctx, request{Service: serviceUploadPack, URL: origin}, "fixture", pr, io.Discard, io.Discard)
	}()
	for i := 0; !f.sshRan(); i++ {
		if i > 300 {
			t.Fatal("the fake ssh never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the call did not end within 3s of its cancellation")
	}
}

// A push of nothing forwards the judged flush and never a byte after it.
func TestEmptyPushForwardsOnlyTheJudgedBytes(t *testing.T) {
	var adv bytes.Buffer
	aw := gitproto.NewWriter(&adv)
	aw.Line("0000000000000000000000000000000000000000 capabilities^{}\x00report-status\n")
	aw.Flush()
	root := t.TempDir()
	advFile, stdinFile := filepath.Join(root, "adv"), filepath.Join(root, "stdin")
	if err := os.WriteFile(advFile, adv.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, "cat "+shquote.Quote(advFile)+"\ncat > "+shquote.Quote(stdinFile))
	f.helper(origin, serviceReceivePack, []byte("0000unjudged-bytes"))
	got, err := os.ReadFile(stdinFile)
	if err != nil || string(got) != "0000" {
		t.Fatalf("the upstream received %q (%v), want exactly \"0000\"", got, err)
	}
}

// A fetch whose upstream fails after answering is audited as failed, not ran.
func TestFetchFailingAfterBytesAuditedAsFailed(t *testing.T) {
	f := newFixture(t, "printf '0008abcd'\nexit 1")
	rc, _, _ := f.helper(origin, serviceUploadPack, nil)
	if rc == 0 {
		t.Fatalf("rc %d", rc)
	}
	evs := auditEvents(t)
	if len(evs) != 1 || evs[0].Outcome != "failed" || evs[0].Exit == nil || *evs[0].Exit == 0 {
		t.Fatalf("audit %+v", evs)
	}
}

// The broker will not run a host ssh an agent can rewrite: one inside the workspace.
func TestSSHInsideTheWorkspaceRefused(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{sshInWorkspace: true})
	rc, stdout, stderr := f.helper(origin, serviceUploadPack, nil)
	if rc != 69 || stdout != "connect\n\n" || !strings.Contains(stderr, "placement rule") {
		t.Fatalf("rc %d stdout %q stderr %q", rc, stdout, stderr)
	}
	if f.sshRan() {
		t.Fatal("the broker ran an ssh inside the workspace")
	}
}

// A signed push cannot be judged; the refusal says to push without --signed.
func TestSignedPushNamesTheFix(t *testing.T) {
	f := newFixture(t, "")
	var push bytes.Buffer
	gitproto.NewWriter(&push).Line("push-cert\x00report-status\n")
	_, _, stderr := f.helper(origin, serviceReceivePack, push.Bytes())
	if !strings.Contains(stderr, "push without --signed") || strings.Contains(stderr, "Run the push again") {
		t.Fatalf("stderr %q", stderr)
	}
}
