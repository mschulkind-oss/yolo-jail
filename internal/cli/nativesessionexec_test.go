package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nativehome"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// This subprocess goes through the real internal dispatcher and syscall.Exec. Its shell
// and the shell's child inherit stock relocation and the existing 16-hex identity.
func TestNativeSessionExecHelper(t *testing.T) {
	dir := os.Getenv("YOLO_TEST_NATIVE_EXEC_DIR")
	if dir == "" {
		return
	}
	os.Exit(runInternal([]string{nativehome.ExecVerb, dir, "launch", "/bin/sh", "-c",
		`test "$YOLO_SESSION_ID" = 0123456789abcdef && test "$PI_CODING_AGENT_DIR" = "$1/agent" && /bin/sh -c 'printf "%s\n%s" "$YOLO_SESSION_ID" "$PI_CODING_AGENT_DIR"' > "$1/child-env" && touch "$1/ready" && read line < "$1/release"`, "native-child", dir}))
}

func TestNativeSessionExecExportsToShellAndChildrenAndHoldsLease(t *testing.T) {
	dir, err := nativehome.Prepare(t.TempDir(), t.TempDir(), "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if err := nativehome.Complete(dir, map[string]string{"YOLO_SESSION_ID": "0123456789abcdef", "PI_CODING_AGENT_DIR": filepath.Join(dir, "agent")}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "release"), 0600); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(os.Args[0], "-test.run=^TestNativeSessionExecHelper$")
	// TMPDIR: the child's TestMain staged tree is removed with this test's dir (reexectmpdir_test.go).
	// The helper's switch and the stale input ID go on THIS child's environment only, never through
	// t.Setenv: a process-wide variable is inherited by anything else this test binary spawns while
	// the test runs, and any such test-binary child that reaches TestNativeSessionExecHelper would
	// enter this session too and hold its lease past c.Wait. An inherited input ID cannot
	// substitute for bootstrap's identity (exec.Cmd keeps the last value of a duplicated key).
	c.Env = append(os.Environ(), "TMPDIR="+t.TempDir(),
		"YOLO_TEST_NATIVE_EXEC_DIR="+dir, "YOLO_SESSION_ID=fedcba9876543210")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	})
	waitNativeFile(t, filepath.Join(dir, "ready"))
	if state, release, _ := packload.ProbeLease(dir); state != packload.LeaseHeld {
		release()
		t.Fatalf("exec lease = %s", state)
	} else {
		release()
	}
	data, err := os.ReadFile(filepath.Join(dir, "child-env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "0123456789abcdef\n"+filepath.Join(dir, "agent") {
		t.Fatalf("child env = %q", data)
	}
	f, err := os.OpenFile(filepath.Join(dir, "release"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("done\n")
	_ = f.Close()
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	// The kernel drops a flock when the LAST descriptor of its open file description closes, so
	// once the exec'd shell is reaped only another holder of that description can keep it. The
	// production reader (sessioncapture.go) treats "held" as "not yet" and retries at the next
	// boot, so the bound here is generous, and a failure names which half of ClaimGone refused.
	deadline := time.Now().Add(5 * time.Second)
	for first := true; ; first = false {
		release, gone := nativehome.ClaimGone(dir)
		if gone {
			release()
			return
		}
		if first {
			// A transient holder passes below but still leaves its name in the -v log.
			t.Logf("first probe after reaping: %s", nativeGoneRefusal(dir))
		}
		if time.Now().After(deadline) {
			t.Fatal(nativeGoneRefusal(dir))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// nativeGoneRefusal says why ClaimGone refused: the launched marker is missing or not a regular
// file, or the lease is held or could not be probed, with whatever lsof can name as its holders.
func nativeGoneRefusal(dir string) string {
	if fi, err := os.Lstat(filepath.Join(dir, ".launched")); err != nil || !fi.Mode().IsRegular() {
		return fmt.Sprintf("ended exec not eligible: launched marker missing or not regular (%v)", err)
	}
	state, release, err := packload.ProbeLease(dir)
	release()
	msg := fmt.Sprintf("ended exec still holds the lease after it was reaped: probe %s (err %v)", state, err)
	if out, lerr := exec.Command("lsof", filepath.Join(dir, packload.EmbeddedLeaseName)).CombinedOutput(); lerr == nil || len(out) > 0 {
		msg += "\nlsof of the lease:\n" + string(out)
	}
	return msg
}

func waitNativeFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native command did not become ready")
}
