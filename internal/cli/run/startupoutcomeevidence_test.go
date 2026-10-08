package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A per-jail child the launch reaped carries its known exit status in the collected outcome; a
// signalled child, which has no exit code, and a child still alive at the deadline do not invent
// one. Driven through the actual selected start loop with the self-exec fixture child.
func TestPerJailStartupOutcomeKeepsTheReapedExitStatus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	for _, tc := range []struct {
		mode    string
		timeout time.Duration
		process hostservice.StartupProcessState
		known   bool
		status  int
	}{
		{"exit", 2 * time.Second, hostservice.StartupProcessExited, true, 2},
		{"exit0", 2 * time.Second, hostservice.StartupProcessExited, true, 0},
		{"signal", 2 * time.Second, hostservice.StartupProcessExited, false, 0},
		{"reason", 150 * time.Millisecond, hostservice.StartupProcessAlive, false, 0},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			o, handles, _, output := runPerJailReasonFixture(t, tc.mode, tc.timeout, false, 0)
			if len(handles) != 0 || len(o.startupOutcomes) != 1 {
				t.Fatalf("selected start not observed: handles=%d outcomes=%+v\n%s", len(handles), o.startupOutcomes, output)
			}
			got := o.startupOutcomes[0]
			if got.ReasonRead.Kind != hostservice.StartupReasonReadRecord {
				t.Fatalf("the child's record was not read: %+v", got)
			}
			if got.Process != tc.process || got.ProcessExitStatusKnown != tc.known || got.ProcessExitStatus != tc.status {
				t.Fatalf("process evidence = %s known=%v status=%d, want %s known=%v status=%d (%+v)",
					got.Process, got.ProcessExitStatusKnown, got.ProcessExitStatus, tc.process, tc.known, tc.status, got)
			}
		})
	}
}

// A fronted per-jail service is ready only once its socket accepts a connect, so its outcome says
// accepted. (A legacy no-transport entry is ready on bare file existence and reports observed.)
func TestPerJailFrontedReadinessIsAccepted(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and binds an AF_UNIX socket")
	}
	o, handles, _, output := runPerJailReasonFixture(t, "ready", time.Second, true, 0)
	if len(handles) != 1 || len(o.startupOutcomes) != 1 {
		t.Fatalf("ready fixture did not start: handles=%d outcomes=%+v\n%s", len(handles), o.startupOutcomes, output)
	}
	if got := o.startupOutcomes[0]; got.Kind != hostservice.StartupKindReady || got.Readiness != hostservice.StartupReadinessAccepted ||
		got.ReasonRead.Kind != hostservice.StartupReasonReadCancelled {
		t.Fatalf("fronted readiness = %s/%s read=%s, want ready/accepted/owner-cancelled: %+v",
			got.Kind, got.Readiness, got.ReasonRead.Kind, got)
	}
}

// When the singleton's ensure never saw a socket and the accepting connect fails, the transport
// failure must not claim the socket was observed.
func TestSingletonEndpointFailureClaimsNoUnseenSocket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	old := paths.HostSingletonDir
	dir := fmt.Sprintf("/tmp/o1ep-%d", os.Getpid())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths.HostSingletonDir = dir
	t.Cleanup(func() { paths.HostSingletonDir = old; _ = os.RemoveAll(dir) })
	var output bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: &output, Stderr: &output}
	fillDefaults(o)
	spawns := 0
	o.singletonDepsForStart = func(name string, args []string) broker.Deps {
		d := broker.SingletonDeps(name, args)
		now := time.Now()
		d.Now = func() time.Time { return now }
		d.Sleep = func(dt time.Duration) { now = now.Add(dt) }
		d.PathExists = func(string) bool { return false }
		d.Alive = func(int) bool { return false }
		d.Spawn = func([]string, string) (int, func() bool, error) {
			spawns++
			return 77, func() bool { return false }, nil
		}
		return d
	}
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{os.Args[0], "-test.run=^$", "{socket}"})
	hd := &loopholes.HostDaemon{Scope: loopholes.ScopeHost, Publishes: "socket"}
	got, ok := o.startHostSingleton("o1-no-socket", spec, t.TempDir(), "", hd)
	if ok || spawns != 1 {
		t.Fatalf("want one failed spawned attempt: ok=%v spawns=%d\n%s", ok, spawns, output.String())
	}
	if got.startupOutcome.Kind != hostservice.StartupKindTransportFailed ||
		got.startupOutcome.Readiness != hostservice.StartupReadinessNotReady {
		t.Fatalf("never-seen socket reported as %s/%s: %+v", got.startupOutcome.Kind, got.startupOutcome.Readiness, got.startupOutcome)
	}
}
