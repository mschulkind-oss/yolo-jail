package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestMacosUserServiceProbeRefusesAnEndpointTheSandboxCannotRead is the macos-user host-service
// witness on the hardware (internal/macosuser/serviceprobe.go), through the REAL probe argv
// (macosuser.ProbeServicesArgv): sudo as the sandbox account, `env -i`, sandbox-exec, the env-file
// reader, the staged yolo's `internal probe-services`.
//
// THE SUBJECT IS AN ENDPOINT WITH NO ACL GRANT: a live authenticated listener of this test's,
// published 0600 in a 0700 directory, the shape every host daemon publishes, with the sandbox
// account's read entry left out. The witness must refuse with the dedicated status and call the
// file UNREADABLE (faultUnreadable), never a network fault. Then the launch's own grant
// (macosuser.EndpointGrantCommands) is applied and the same argv must pass.
//
// WHAT ONLY THIS TEST CAN SEE: that an ungranted endpoint really comes back as a permission error
// on a PATH from inside the profile (EACCES from the DAC, or EPERM from Seatbelt) rather than
// something the classifier files elsewhere; that the staged yolo runs the verb under
// sandbox-exec; and that the `user:` ACE really admits the dial.
//
// The profile is a permissive one of this test's, so the outcome is the DAC's alone; the
// session's real profile is the launch's (every endpoint-publishing launch test now runs the
// stage under it). The staged yolo is put in place by one ordinary launch first.
func TestMacosUserServiceProbeRefusesAnEndpointTheSandboxCannotRead(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)
	// One launch stages the yolo under test at macosuser.StagedYoloPath, which the probe execs.
	macosUserRunProbe(t, "service-probe staging", ws, `echo "=== END ==="`)

	shared, err := os.MkdirTemp("/private/tmp", "yolo-it-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shared) })
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	// The endpoint's own directory, 0700 as svcendpoint requires and as every services dir is.
	svcDir := filepath.Join(shared, "services")
	if err := os.Mkdir(svcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(svcDir, "yolo-it-probe"+paths.ServiceEndpointExt)
	ln, err := svcendpoint.Listen(endpoint, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	profile := filepath.Join(shared, "probe.sb")
	if err := os.WriteFile(profile, []byte("(version 1)\n(allow default)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := jsonx.NewOrderedMap()
	// The workspace, named as a launch that staged a pack tree names it, so the witness appends its
	// record to that workspace's boot.log as the sandbox account (entrypoint's attachWitnessLog).
	env.Set("YOLO_DARWIN_WORKSPACE", ws)
	env.Set(paths.HostLoopbackEnvVar, paths.HostLoopbackShared)
	env.Set(paths.ServiceEnvVarPrefix+"YOLO_IT_PROBE"+paths.ServiceEnvVarSuffix, endpoint)
	envFile := filepath.Join(shared, "probe.env")
	if err := os.WriteFile(envFile, []byte(macosuser.SandboxEnvFileContent(env)), 0o644); err != nil {
		t.Fatal(err)
	}
	// THE REAL ARGV, plain sudo and all: the runner's sudo needs no password, and the stage's sudo
	// is deliberately not `-n` (ProbeServicesArgv says why).
	argv := macosuser.ProbeServicesArgv(macosuser.StagedYoloPath(""), profile, envFile, "", "", nil)
	run := func() (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = "/"
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		if err != nil {
			t.Fatalf("the probe argv did not run: %v\n%s", err, out)
		}
		return 0, string(out)
	}

	rc, out := run()
	if rc != provision.RefusedStatus {
		t.Fatalf("an ungranted endpoint: the probe exited %d, want the refusal status %d\nargv: %s\n%s",
			rc, provision.RefusedStatus, strings.Join(argv, " "), out)
	}
	for _, want := range []string{"UNREADABLE", endpoint, "ls -le", paths.AllowUnreachableServicesEnv} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "UNREACHABLE from") || strings.Contains(out, "--net=host") {
		t.Errorf("an unreadable endpoint is reported as a network fault:\n%s", out)
	}
	// THE REFUSAL IS ON RECORD, appended after the staging launch's bootstrap record: the sandbox
	// account can append to the workspace's boot.log from inside a profile.
	bootLog := func() string {
		b, err := os.ReadFile(entrypoint.BootLogPath(ws))
		if err != nil {
			t.Fatalf("reading %s: %v", entrypoint.BootLogPath(ws), err)
		}
		return string(b)
	}
	if log := bootLog(); !strings.Contains(log, "=== boot complete, handing over ===") ||
		!strings.Contains(log, "UNREADABLE") || !strings.HasSuffix(log, " ===\n") ||
		!strings.Contains(lastWitnessRecord(log), "=== WITNESS REFUSED: ") {
		t.Errorf("the boot log does not keep the bootstrap's record followed by the witness's refusal:\n%s", log)
	}

	for _, cmd := range macosuser.EndpointGrantCommands(endpoint, "") {
		if b, err := exec.Command("sudo", append([]string{"-n"}, cmd...)...).CombinedOutput(); err != nil {
			t.Fatalf("the launch's grant `sudo %s` failed: %v\n%s", strings.Join(cmd, " "), err, b)
		}
	}
	if rc, out := run(); rc != 0 {
		t.Errorf("with the launch's ACL grant the probe still exits %d, want 0:\n%s%s", rc, out,
			fmt.Sprintf("\n--- ls -le: %s", lsACL(endpoint)))
	}
	if log := bootLog(); !strings.HasSuffix(log, "=== witness passed, launching ===\n") ||
		!strings.Contains(lastWitnessRecord(log), "1/1 enabled service(s) reachable") {
		t.Errorf("the boot log does not record the healthy verdict:\n%s", log)
	}
}

// lastWitnessRecord is the last witness section of a boot log, "" when it has none.
func lastWitnessRecord(log string) string {
	i := strings.LastIndex(log, "=== macos-user host-service witness ")
	if i < 0 {
		return ""
	}
	return log[i:]
}

// lsACL is `ls -le` of a path and its directory, for a failure message.
func lsACL(path string) string {
	out, _ := exec.Command("/bin/ls", "-led", path, filepath.Dir(path)).CombinedOutput()
	return string(out)
}
