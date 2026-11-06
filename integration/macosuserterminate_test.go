package integration

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
				t.Fatalf("the launcher did not exit within 90 s of %s%s\n--- processes, taken at the timeout:\n%s",
					tc.name, diag(), macosUserProcessSnapshot(cmd.Process.Pid))
			}
			if rc == 0 {
				t.Errorf("the signaled launch exited 0%s", diag())
			}
			t.Logf("the launch exited %d", rc)

			for _, name := range mine {
				f := filepath.Join(macosUserEnvDir(), name)
				if _, err := os.Lstat(f); err == nil {
					t.Errorf("the session's %s survives its teardown%s", f, diag())
				}
			}
			requireNoGuestSupervisorLeft(t, diag)
			sessions, _ := yoloruntime.ListSessions(paths.HostServicesBase(true))
			key := paths.JailShortHash(jailNameOn(ws, macosUserSetupFor("account")))
			for _, s := range sessions {
				if s.Key == key {
					t.Errorf("the session's host-services dir %s survives its teardown%s", s.Dir, diag())
				}
			}
		})
	}
}

// macosUserProcessSnapshot is the process table a timed-out terminate leaves behind, for the
// failure message: every process in the launcher's group (pgid, the launcher's pid, since the test
// starts it with Setpgid) and every process of the sandbox account, which a signal from this uid
// cannot reach. The ignored and caught masks (hex, bit N-1 for signal N, so SIGHUP is 0x1 and
// SIGTERM 0x4000) say whether the session's shell could have died of the signal at all. Without
// it, a hang reads the same whether the launcher, its sudo or the sandboxed shell is the one still
// running (the 2026-10-16 macOS run of 8e65f3444, which printed only the launch's output).
func macosUserProcessSnapshot(pgid int) string {
	out, err := exec.Command("/bin/ps", "-axo",
		"pid=,ppid=,pgid=,user=,stat=,sigignore=,sigcatch=,etime=,command=").Output()
	if err != nil {
		return "(ps failed: " + err.Error() + ")"
	}
	return "PID PPID PGID USER STAT IGNORED CAUGHT ELAPSED COMMAND\n" +
		strings.Join(launchGroupOrSandboxRows(string(out), pgid, macosuser.SandboxUser), "\n")
}

// launchGroupOrSandboxRows is the rows of a `ps -o pid,ppid,pgid,user,…` listing whose pgid is
// pgid or whose user is user.
func launchGroupOrSandboxRows(listing string, pgid int, user string) []string {
	var rows []string
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		if f[2] == strconv.Itoa(pgid) || f[3] == user {
			rows = append(rows, strings.TrimSpace(line))
		}
	}
	return rows
}

// TestLaunchGroupOrSandboxRowsKeepsTheGroupAndTheAccount pins the timeout snapshot's filter, which
// only a Mac's timeout ever runs: the launcher's group and the sandbox account's processes are
// kept, everything else on the machine is not.
func TestLaunchGroupOrSandboxRowsKeepsTheGroupAndTheAccount(t *testing.T) {
	listing := "  100     1   100 runner   Ss  0 0 01:00 yolo run -- bash\n" +
		"  101   100   100 runner   S   0 0 00:59 /usr/bin/sudo -n --user=_yolojail /usr/bin/env -i\n" +
		"  102   101   100 _yolojail S   1 0 00:59 bash -lc touch up\n" +
		"  200     1   200 _yolojail Ss  0 0 00:58 yolo-jaild supervise\n" +
		"  300     1   300 runner   S   0 0 10:00 /usr/sbin/unrelated\n" +
		"\n"
	got := launchGroupOrSandboxRows(listing, 100, "_yolojail")
	if len(got) != 4 {
		t.Fatalf("kept %d rows, want the group's three and the account's one: %q", len(got), got)
	}
	for _, row := range got {
		if strings.Contains(row, "unrelated") {
			t.Errorf("kept a process of neither the group nor the account: %q", row)
		}
	}
}

// macosUserEnvDir is the state dir's env/ directory, where each session's env files live.
func macosUserEnvDir() string { return filepath.Join(macosuser.StateDir(), "env") }

// macosUserSessionEnvFiles lists the env/ directory as this user, with no sudo: the launch
// stages it host-owned (0700 plus the sandbox account's search ACE), so it is ours to read
// (docs/design/macos-user-privilege-lifecycle-plan.md PL-2).
func macosUserSessionEnvFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(macosUserEnvDir())
	if err != nil {
		return nil // absent before any session made it
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
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
