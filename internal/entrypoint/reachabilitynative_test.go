package entrypoint

// reachabilitynative_test.go covers the witness's macos-user half: the faultUnreadable class (an
// endpoint the sandbox account may not open), the native-sandbox wording a `shared` disposition
// gets on a DarwinEnvFrom Env, and RunServiceProbe, the stage the macos-user launch runs it as
// (internal/macosuser/serviceprobe.go, through `yolo internal probe-services`).

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A PERMISSION ERROR ON A PATH IS faultUnreadable, and only on a path. svcendpoint passes the
// stat's or open's EACCES/EPERM through unattributed (readPathError), and it used to land in the
// transport default, reported as a network fault on the one backend with no network hop. The
// same errno on a SOCKET — a sandbox denying connect(2) — is still a transport failure.
func TestClassifierFilesAPathPermissionErrorAsUnreadable(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EACCES, syscall.EPERM} {
		pathErr := &fs.PathError{Op: "open", Path: "/private/tmp/x/serial.endpoint", Err: errno}
		for name, err := range map[string]error{
			"bare":    pathErr,
			"wrapped": fmt.Errorf("svcendpoint: reading: %w", pathErr),
		} {
			if got := classifyReachability(err); got != faultUnreadable {
				t.Errorf("%s %v classified %d, want faultUnreadable", name, errno, got)
			}
		}
		connect := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
		if got := classifyReachability(connect); got != faultUnreachable {
			t.Errorf("a connect(2) %v classified %d, want faultUnreachable: it is the network, "+
				"not a file", errno, got)
		}
	}
	if got := classifyReachability(errors.New("dial tcp 127.0.0.1:1: connection refused")); got != faultUnreachable {
		t.Errorf("an unattributed dial error left the transport default: %d", got)
	}
}

// AN ENDPOINT THIS USER MAY NOT READ, END TO END: the probe reports it UNREADABLE (never
// unreachable), names the file, gives no network paragraph, and escalates under `shared`.
//
// Skipped as root, which reads a 0000 file regardless — and the jail this repo is developed in
// runs as root, so this half runs in CI (uid 1001) and on a Mac.
func TestAnUnreadableEndpointIsReportedAsUnreadableAndRefuses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file; run as a non-root uid (CI, or `sudo -u nobody`) to exercise EACCES")
	}
	shrinkReachabilityBudget(t)
	path := liveEndpoint(t, servicesDir(t), "serial")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	out, err := runProbe(t, map[string]string{
		"JAIL_HOME":              t.TempDir(),
		paths.HostLoopbackEnvVar: paths.HostLoopbackShared,
		paths.SerialEndpointEnv:  path,
	})
	if !strings.Contains(out, "UNREADABLE") || !strings.Contains(out, path) {
		t.Errorf("an unreadable endpoint is not reported as unreadable:\n%s", out)
	}
	if strings.Contains(out, "UNREACHABLE from") || strings.Contains(out, "rootless network stack") {
		t.Errorf("an unreadable endpoint is reported as a network fault:\n%s", out)
	}
	if err == nil {
		t.Errorf("an unreadable endpoint under `shared` did not refuse (OQ-R4):\n%s", out)
	}
}

// THE UNREADABLE REMEDY IS THE BACKEND'S. On macos-user it names the ACL entry the launch
// stages for the sandbox account (macosuser.EndpointGrantCommands) and the macOS command that
// shows it; in a container it names the mode and `ls -l`.
func TestTheUnreadableRemedyNamesTheACLEntryOnAMac(t *testing.T) {
	res := reachabilityResult{
		svc:   serviceEndpoint{name: "serial", path: "/private/tmp/yolo-host-services-x/serial.endpoint"},
		fault: faultUnreadable,
		err:   &fs.PathError{Op: "open", Path: "/private/tmp/yolo-host-services-x/serial.endpoint", Err: syscall.EACCES},
	}
	res.native, res.user = true, "_yolojail"
	mac := reachabilityWarning(res)
	for _, want := range []string{"UNREADABLE", "user:_yolojail allow read", "allow search",
		"ls -le /private/tmp/yolo-host-services-x/serial.endpoint", "ls -led /private/tmp/yolo-host-services-x"} {
		if !strings.Contains(mac, want) {
			t.Errorf("the macos-user remedy does not carry %q:\n%s", want, mac)
		}
	}
	res.native = false
	linux := reachabilityWarning(res)
	if strings.Contains(linux, "ls -le") || strings.Contains(linux, "_yolojail") ||
		!strings.Contains(linux, "ls -l /private/tmp/yolo-host-services-x/serial.endpoint") {
		t.Errorf("the container remedy is not the container's:\n%s", linux)
	}
}

// probeDarwin runs the witness over a DarwinEnvFrom Env — the Env `yolo internal
// probe-services` builds — and returns what it said and the gate's verdict.
func probeDarwin(t *testing.T, vars map[string]string) (string, error) {
	t.Helper()
	var out strings.Builder
	e := DarwinEnvFrom(vars, t.TempDir())
	e.Stderr = &out
	err := RunServiceProbe(e)
	return out.String(), err
}

// `shared` ON A NATIVE SANDBOX SPEAKS OF THE MAC, NOT OF A CONTAINER, and still names the hatch.
// The container's shared paragraph begins with --net=host and podman-in-podman, which a Mac user
// would go looking for; the native one says the address is the Mac's own loopback and points at
// `yolo check` on that Mac. And the container keeps its own (a regression guard on the switch).
func TestASharedNativeSandboxGetsTheMacWordingAndTheHatch(t *testing.T) {
	shrinkReachabilityBudget(t)
	out, err := probeDarwin(t, brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackShared,
	}))
	if err == nil {
		t.Fatalf("an unreachable service under `shared` did not refuse:\n%s", out)
	}
	for _, want := range []string{"Mac's own network stack", "Mac's own loopback", "`yolo check` on this Mac",
		paths.AllowUnreachableServicesEnv + "=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the native refusal does not carry %q:\n%s", want, out)
		}
	}
	for _, never := range []string{"--net=host", "podman", "namespace of whatever launched it", "container mounted"} {
		if strings.Contains(out, never) {
			t.Errorf("the native refusal speaks of a container (%q):\n%s", never, out)
		}
	}

	container, cerr := runProbe(t, brokenServiceVars(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackShared,
	}))
	if cerr == nil || !strings.Contains(container, "--net=host") || strings.Contains(container, "Mac's own") {
		t.Errorf("the container's shared wording changed:\n%s", container)
	}
}

// RunServiceProbe IS THE BOOT'S DECISION: nil for a healthy service, an error for an unusable
// one under `shared`, nil again with the hatch (which it says so).
func TestRunServiceProbeIsTheBootsDecision(t *testing.T) {
	shrinkReachabilityBudget(t)
	dir := servicesDir(t)
	if out, err := probeDarwin(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackShared,
		paths.SerialEndpointEnv:  liveEndpoint(t, dir, "serial"),
	}); err != nil || out != "" {
		t.Errorf("a reachable service: err %v, output:\n%s", err, out)
	}
	missing := map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackShared,
		paths.SerialEndpointEnv:  dir + "/absent.endpoint",
	}
	if _, err := probeDarwin(t, missing); err == nil {
		t.Error("an unpublished endpoint under `shared` did not refuse")
	}
	missing[paths.AllowUnreachableServicesEnv] = "1"
	if out, err := probeDarwin(t, missing); err != nil || !strings.Contains(out, "Nothing was repaired") {
		t.Errorf("the hatch: err %v, output:\n%s", err, out)
	}
}
