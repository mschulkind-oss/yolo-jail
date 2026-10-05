package integration

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestMacosUserTerminateEndsTheSessionThroughItsTeardown is the macos-user arm on the hardware
// (internal/cli/run's macosuserarm.go; docs/design/jail-lifetime-last-session-wins.md JL-D40):
// a SIGTERM sent to the launcher alone, and the SIGHUP a closed window gives its whole process
// group, each end a running session THROUGH ITS TEARDOWN — the launcher exits within a bound, the
// session's env files (which hold its credentials) are gone, no guest supervisor of the sandbox
// account survives, and its host-services session dir is removed.
//
// Before the arm the session ran as a plain child with no handler, so the launcher took Go's
// default action and every one of those was left for the next launch's sweep. Measured on Linux
// only with a stand-in launcher; never run on a Mac before this test.
func TestMacosUserTerminateEndsTheSessionThroughItsTeardown(t *testing.T) {
	requireMacosUser(t)
	for _, tc := range []struct {
		name  string
		sig   syscall.Signal
		group bool
	}{
		{"SIGTERM to the launcher", syscall.SIGTERM, false},
		{"SIGHUP to its process group", syscall.SIGHUP, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packHome(t, `{"packs": []}`)
			writeLocalGuestAdapterPack(t) // a guest supervisor and its env file, for the teardown to end
			ws := macosUserWorkspace(t, `{}`)
			sync := macosUserSyncDir(t, ws, ".yolo-it-terminate")
			before := macosUserSessionEnvFiles(t)

			env := hd10LaunchEnv()
			awaitDetachedWriters(t, ws, launchHome(env))
			cmd := exec.Command(yoloBin, append(jailRunArgs(), "--", "bash", "-lc",
				heldSessionScript(sync, "", ""))...)
			cmd.Dir, cmd.Env = ws, env
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			diag := func() string {
				return "\n--- launch stdout:\n" + stdout.String() + "\n--- launch stderr:\n" + stderr.String()
			}

			macosUserAwaitFile(t, filepath.Join(sync, "up"), exited, diag)
			mine := macosUserNewNames(before, macosUserSessionEnvFiles(t))
			if len(mine) < 2 {
				t.Fatalf("the session wrote %v to %s; the fixture needs its env file and its daemons' env "+
					"file%s", mine, macosUserEnvDir(), diag())
			}

			target := cmd.Process.Pid
			if tc.group {
				target = -target
			}
			if err := syscall.Kill(target, tc.sig); err != nil {
				t.Fatal(err)
			}
			var rc int
			select {
			case err := <-exited:
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					rc = ee.ExitCode()
				}
			case <-time.After(90 * time.Second):
				t.Fatalf("the launcher did not exit within 90 s of %s%s", tc.name, diag())
			}
			if rc == 0 {
				t.Errorf("the signaled launch exited 0%s", diag())
			}
			t.Logf("the launch exited %d", rc)

			for _, name := range mine {
				f := filepath.Join(macosUserEnvDir(), name)
				if exec.Command("sudo", "-n", "test", "-e", f).Run() == nil {
					t.Errorf("the session's %s survives its teardown%s", f, diag())
				}
			}
			requireNoGuestSupervisorLeft(t, diag)
			sessions, _ := yoloruntime.ListSessions(paths.HostServicesBase(true))
			key := paths.JailShortHash(yoloruntime.FromWorkspace(ws))
			for _, s := range sessions {
				if s.Key == key {
					t.Errorf("the session's host-services dir %s survives its teardown%s", s.Dir, diag())
				}
			}
		})
	}
}

// macosUserEnvDir is the state dir's env/ directory, where each session's env files live.
func macosUserEnvDir() string { return filepath.Join(macosuser.StateDir(), "env") }

// macosUserSessionEnvFiles lists the env/ directory as root (it is 0700 root).
func macosUserSessionEnvFiles(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("sudo", "-n", "/bin/ls", "-1", macosUserEnvDir()).Output()
	if err != nil {
		return nil // absent before any session made it
	}
	return strings.Fields(string(out))
}

// macosUserNewNames is the names in after that before lacks.
func macosUserNewNames(before, after []string) []string {
	var out []string
	for _, n := range after {
		if !slices.Contains(before, n) {
			out = append(out, n)
		}
	}
	return out
}

// macosUserAwaitFile waits for path, failing if the launch exits first.
func macosUserAwaitFile(t *testing.T, path string, exited <-chan error, diag func() string) {
	t.Helper()
	deadline := time.Now().Add(macosUserTimeout())
	for time.Now().Before(deadline) {
		if exec.Command("test", "-e", path).Run() == nil {
			return
		}
		select {
		case err := <-exited:
			t.Fatalf("the launch ended (%v) before %s appeared%s", err, path, diag())
		case <-time.After(250 * time.Millisecond):
		}
	}
	t.Fatalf("%s never appeared%s", path, diag())
}
