package cli

// probeservices_test.go drives `yolo internal probe-services` in-process against real
// loopback-TLS endpoints (internal/svcendpoint), the way the macos-user witness stage runs it
// inside the sandbox: an environment carrying YOLO_SERVICE_*_ENDPOINT variables, the
// host-loopback disposition and, sometimes, the hatch.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// probeServicesDir is a 0700 directory svcendpoint will publish into.
func probeServicesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// liveProbeEndpoint stands an authenticated loopback listener that accepts and drops, the shape
// of a healthy host daemon's front.
func liveProbeEndpoint(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+paths.ServiceEndpointExt)
	ln, err := svcendpoint.Listen(path, "127.0.0.1")
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
	return path
}

func probeEnviron(kv map[string]string) []string {
	out := []string{"HOME=" + macosuser.SandboxHome(), "USER=" + macosuser.SandboxUser}
	for k, v := range kv {
		out = append(out, k+"="+v)
	}
	return out
}

func runProbe(t *testing.T, kv map[string]string) (int, string) {
	t.Helper()
	var stderr strings.Builder
	rc := runProbeServices(nil, probeEnviron(kv), &stderr)
	return rc, stderr.String()
}

// A REACHABLE SERVICE: status 0, and silence, as at a healthy container boot.
func TestProbeServicesPassesAReachableService(t *testing.T) {
	dir := probeServicesDir(t)
	rc, out := runProbe(t, map[string]string{
		paths.HostLoopbackEnvVar:       paths.HostLoopbackShared,
		paths.SerialEndpointEnv:        liveProbeEndpoint(t, dir, "serial"),
		paths.HostProcessesEndpointEnv: liveProbeEndpoint(t, dir, "host-processes"),
	})
	if rc != 0 || out != "" {
		t.Errorf("rc = %d, stderr:\n%s\nwant 0 and silence for reachable services", rc, out)
	}
}

// AN UNPUBLISHED ENDPOINT ON A `shared` SANDBOX REFUSES, with the dedicated status, the native
// wording and the hatch, and never a container's --net=host.
func TestProbeServicesRefusesAnUnusableServiceWithTheNativeWording(t *testing.T) {
	missing := filepath.Join(probeServicesDir(t), "serial.endpoint")
	rc, out := runProbe(t, map[string]string{
		paths.HostLoopbackEnvVar: paths.HostLoopbackShared,
		paths.SerialEndpointEnv:  missing,
	})
	if rc != provision.RefusedStatus {
		t.Fatalf("rc = %d, want provision.RefusedStatus (%d)\n%s", rc, provision.RefusedStatus, out)
	}
	for _, want := range []string{"'serial'", missing, "Mac's own network stack",
		paths.AllowUnreachableServicesEnv + "=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, out)
		}
	}
	for _, never := range []string{"--net=host", "podman", "namespace of whatever launched it"} {
		if strings.Contains(out, never) {
			t.Errorf("the macos-user refusal speaks of a container (%q):\n%s", never, out)
		}
	}
}

// THE HATCH LETS IT THROUGH, LOUDLY: status 0, with the notice that nothing was repaired.
func TestProbeServicesHonorsTheHatch(t *testing.T) {
	rc, out := runProbe(t, map[string]string{
		paths.HostLoopbackEnvVar:          paths.HostLoopbackShared,
		paths.SerialEndpointEnv:           filepath.Join(probeServicesDir(t), "serial.endpoint"),
		paths.AllowUnreachableServicesEnv: "1",
	})
	if rc != 0 {
		t.Fatalf("rc = %d with the hatch set, want 0\n%s", rc, out)
	}
	if !strings.Contains(out, paths.AllowUnreachableServicesEnv+" is set") || !strings.Contains(out, "Nothing was repaired") {
		t.Errorf("the hatch is honored silently:\n%s", out)
	}
}

// NO DISPOSITION, NO REFUSAL: an environment that does not say `shared` (an older launcher)
// gets the warning and status 0, never the refusal.
func TestProbeServicesWithNoDispositionOnlyWarns(t *testing.T) {
	rc, out := runProbe(t, map[string]string{
		paths.SerialEndpointEnv: filepath.Join(probeServicesDir(t), "serial.endpoint"),
	})
	if rc != 0 {
		t.Fatalf("rc = %d with no disposition, want 0\n%s", rc, out)
	}
	if !strings.Contains(out, "'serial'") || strings.Contains(out, "Refusing to start") {
		t.Errorf("want the per-service warning and no refusal:\n%s", out)
	}
}

// MISUSE IS 2, and the usage names the verb.
func TestProbeServicesRefusesArguments(t *testing.T) {
	var stderr strings.Builder
	if rc := runProbeServices([]string{"extra"}, nil, &stderr); rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
	if !strings.Contains(stderr.String(), macosuser.ProbeServicesVerb) {
		t.Errorf("the usage does not name the verb: %q", stderr.String())
	}
}

// THE HIDDEN VERB IS DISPATCHED. runInternal with the verb runs the witness, which, with every
// endpoint variable of this process emptied, has nothing to probe and answers 0; an unknown verb
// answers 2. Deleting the dispatch arm fails this.
func TestRunInternalDispatchesProbeServices(t *testing.T) {
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, paths.ServiceEnvVarPrefix) && strings.HasSuffix(k, paths.ServiceEnvVarSuffix) {
			t.Setenv(k, "")
		}
	}
	if rc := runInternal([]string{macosuser.ProbeServicesVerb}); rc != 0 {
		t.Errorf("`yolo internal %s` with no endpoint = %d, want 0", macosuser.ProbeServicesVerb, rc)
	}
	if rc := runInternal([]string{macosuser.ProbeServicesVerb, "extra"}); rc != 2 {
		t.Errorf("`yolo internal %s extra` = %d, want the usage's 2", macosuser.ProbeServicesVerb, rc)
	}
}
