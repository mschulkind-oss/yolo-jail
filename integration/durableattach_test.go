package integration

// durableattach_test.go is the end-to-end half of DS-D36 (docs/design/durable-scratch-space.md):
// the durable dir's launch line is the launch's, said once by the jail's own boot, and an attach
// neither walks the dir again nor prints the line on its terminal. The unit tier
// (internal/entrypoint durablereportpass_test.go) drives the step table's session-pass rule; only
// a real jail shows which pass's lines reach which terminal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// TestTheDurableLineIsTheLaunchsAndAnAttachDoesNotRepeatIt: a durable dir holding one note, a
// launch that prints the line once (the main process's boot; the first session's pass skips the
// step), and an attach whose terminal carries no durable line at all.
func TestTheDurableLineIsTheLaunchsAndAnAttachDoesNotRepeatIt(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	ddir, err := durable.Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ddir, "notes.md"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const releaseName = "release-durable-attach"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-SESSION-UP; `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
	release := func() { _ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644) }
	t.Cleanup(release)

	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(first.combined(), "FIRST-SESSION-UP\n") {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before its session ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first session never started within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := strings.Count(first.combined(), "Durable dir: "); n != 1 {
		t.Errorf("the launch printed the durable line %d times, want once (the jail's own boot):\n%s",
			n, first.combined())
	}
	awaitLaunchLockReleased(t, dir, first)

	r := runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc", `echo ATTACHED-OK`))
	if r.rc != 0 {
		t.Fatalf("the attach failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "Attaching to existing jail") {
		t.Fatalf("the launch did not attach, so it tested a fresh launch:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "ATTACHED-OK") {
		t.Fatalf("the attached session did not run:\n%s", r.combined())
	}
	if strings.Contains(r.combined(), "Durable dir: ") {
		t.Errorf("the attach printed the durable line, which the launch already said (DS-D11), "+
			"so its pass walked the dir again:\n%s", r.combined())
	}

	release()
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("first launch rc = %d, want 0:\n%s", rc, first.combined())
	}
}
