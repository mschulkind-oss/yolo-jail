package integration

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHostRunDeliversAHostCommandToTheJail drives yolo host-run end to end
// (docs/reference/host-run-mailbox.md): in the jail the line prints a ready line
// naming the host's path for the workspace; on the host (this test process, with YOLO_VERSION
// unset) the same line runs at a terminal and delivers into the mailbox; and back in the jail
// `yolo host-run show` reads the delivered run.
//
// The host half runs in this test process, so in a nested jail it exercises the mailbox through
// the same bind mount a real host would.
func TestHostRunDeliversAHostCommandToTheJail(t *testing.T) {
	requireJail(t)
	dir, err := filepath.EvalSymlinks(writeProject(t, "{}"))
	if err != nil {
		t.Fatal(err)
	}

	// 1. In the jail: nothing runs; the ready line carries `-C <host path>`.
	r := runYolo(t, dir, "yolo host-run --name it-1 -- printf hi")
	if r.rc != 64 || !strings.Contains(r.combined(), "-C "+dir) {
		t.Fatalf("in the jail, want exit 64 and a ready line with -C %s; got rc %d:\n%s", dir, r.rc, r.combined())
	}
	if fi, err := os.Stat(filepath.Join(dir, ".yolo")); err != nil || !fi.IsDir() {
		t.Fatalf("the launch left no %s/.yolo: %v", dir, err)
	}

	// 2. On the host: the same line at a terminal, with a PATH no jail can write.
	printf, err := exec.LookPath("printf")
	if err != nil {
		t.Skipf("no printf on PATH: %v", err)
	}
	printf, _ = filepath.EvalSymlinks(printf)
	binDir := filepath.Dir(printf)
	for d := binDir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".yolo")); err == nil {
			t.Skipf("printf's directory %s sits under a .yolo holder at %s, which host-run refuses as a PATH entry", binDir, d)
		}
	}
	master, slave := openTestPty(t)
	cmd := exec.Command(yoloBin, "host-run", "--name", "it-1", "--", "printf", "hi")
	cmd.Dir = dir
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "YOLO_VERSION=") || strings.HasPrefix(kv, "PATH=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "PATH="+binDir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	awaitDetachedWriters(t, dir, launchHome(cmd.Env))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	var shown bytes.Buffer
	drained := make(chan struct{})
	go func() { io.Copy(&shown, master); close(drained) }()
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the host form: %v", err)
	}
	slave.Close()
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err = <-waitErr:
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatalf("the host form did not finish:\n%s", shown.String())
	}
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
	}
	if err != nil || !strings.Contains(shown.String(), "Nothing to paste back.") {
		t.Fatalf("on the host, want exit 0 and a delivery line; got %v:\n%s", err, shown.String())
	}

	// 3. Back in the jail: show reads the delivered run.
	r = runYolo(t, dir, "yolo host-run show --name it-1")
	if r.rc != 0 || !strings.Contains(r.stdout, "hi") || !strings.Contains(r.stdout, "exit:  0") {
		t.Fatalf("in the jail, want show to print the run; got rc %d:\n%s", r.rc, r.combined())
	}
}
