package run

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

const perJailReasonChildModeEnv = "YJ_PER_JAIL_REASON_CHILD_MODE"
const perJailReasonChildDelayEnv = "YJ_PER_JAIL_REASON_CHILD_DELAY"
const perJailReasonChildClassEnv = "YJ_PER_JAIL_REASON_CHILD_CLASS"

// TestPerJailReasonChild is the harmless subprocess fixture used by the actual
// per-jail start path below. The parent filters the child to this test only.
func TestPerJailReasonChild(t *testing.T) {
	mode := os.Getenv(perJailReasonChildModeEnv)
	if mode == "" {
		return
	}
	if delay := os.Getenv(perJailReasonChildDelayEnv); delay != "" {
		duration, err := time.ParseDuration(delay)
		if err != nil {
			t.Fatalf("invalid fixture delay: %v", err)
		}
		time.Sleep(duration)
	}
	// The record-less modes (hostservicelifecycle_test.go) never write a reason.
	if code, ok := perJailSilentChildMain(mode); ok {
		os.Exit(code)
	}
	class := os.Getenv(perJailReasonChildClassEnv)
	if class == "" {
		class = "configuration"
	}
	if err := hostservice.WriteStartupReasonFromEnv(hostservice.StartupReason{
		Class: class, Reason: "fixture settings were refused",
		Remedy: "correct the user settings and retry",
	}); err != nil {
		t.Fatalf("write fixture startup reason: %v", err)
	}
	switch mode {
	case "exit":
		os.Exit(2)
	case "exit0":
		os.Exit(0)
	case "signal":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	time.Sleep(time.Hour)
}

func TestWaitServiceReadyCompatibilityWrapperKeepsConfiguredTimeout(t *testing.T) {
	const timeout = 20 * time.Millisecond
	o := &Options{ServiceReadyTimeout: timeout}
	started := time.Now()
	failure := o.waitServiceReady(func() bool { return false }, make(chan struct{}), &exec.Cmd{})
	if !strings.Contains(failure, timeout.String()) {
		t.Fatalf("compatibility wrapper failure = %q, want its configured timeout %s", failure, timeout)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("compatibility wrapper exceeded its bounded timeout: %s", elapsed)
	}
}

func TestPerJailStartupReasonAvailableWithinReadinessBudgetIsReturnedByProductionStart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and binds an AF_UNIX socket")
	}
	const timeout = 180 * time.Millisecond
	started := time.Now()
	o, handles, refusal, output := runPerJailReasonFixture(t, "reason", timeout, false, 0)
	if elapsed := time.Since(started); elapsed < timeout-30*time.Millisecond || elapsed > timeout+160*time.Millisecond {
		t.Fatalf("actual start elapsed %s for %s readiness bound; want one bounded readiness wait", elapsed, timeout)
	}
	if len(handles) != 0 || refusal == nil {
		t.Fatalf("startup reason was not returned through the actual per-jail start path: handles=%d refusal=%+v output=%s", len(handles), refusal, output)
	}
	if refusal.class != "configuration" || refusal.reason != "fixture settings were refused" ||
		refusal.remedy != "correct the user settings and retry" {
		t.Fatalf("current-attempt reason/remedy was lost: %+v", refusal)
	}
	if refusal.Error() == "" || !strings.Contains(refusal.Error(), "correct the user settings and retry") ||
		!strings.Contains(refusal.Error(), "run `yolo check --no-build`, then retry the launch") {
		t.Fatalf("production refusal omitted its remedy or next step: %s", refusal.Error())
	}
	if strings.Contains(output, "did not become reachable") {
		t.Fatalf("derived readiness symptom obscured the cooperative refusal: %s", output)
	}
	if o.startupRefusal == nil {
		t.Fatal("production Options did not retain the startup refusal")
	}
}

func TestPerJailStartupReasonAfterReadinessDeadlineIsNotAccepted(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	const timeout = 180 * time.Millisecond
	const afterDeadline = 80 * time.Millisecond
	started := time.Now()
	o, handles, refusal, output := runPerJailReasonFixture(t, "reason", timeout, false, timeout+afterDeadline)
	elapsed := time.Since(started)
	if len(handles) != 0 || refusal != nil || o.startupRefusal != nil {
		t.Fatalf("a reason sent after the readiness deadline was accepted: elapsed=%s bound=%s handles=%d refusal=%+v startup=%+v output=%s",
			elapsed, timeout, len(handles), refusal, o.startupRefusal, output)
	}
	if elapsed > timeout+70*time.Millisecond {
		t.Fatalf("per-jail start waited beyond its readiness bound: elapsed=%s bound=%s", elapsed, timeout)
	}
	if !strings.Contains(output, "did not become reachable") {
		t.Fatalf("ordinary readiness failure was not retained after the late reason was rejected: handles=%d refusal=%+v startup=%+v output=%s", len(handles), refusal, o.startupRefusal, output)
	}
}

func TestPerJailStartupReasonZeroReadinessBudgetDoesNotAcceptLaterEvidence(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	started := time.Now()
	o, handles, refusal, output := runPerJailReasonFixture(t, "reason", time.Nanosecond, false, 30*time.Millisecond)
	elapsed := time.Since(started)
	if len(handles) != 0 || refusal != nil || o.startupRefusal != nil {
		t.Fatalf("zero-budget start accepted later reason evidence: elapsed=%s refusal=%+v startup=%+v output=%s",
			elapsed, refusal, o.startupRefusal, output)
	}
}

func TestPerJailStartupReasonReadyWithoutReasonKeepsReadinessAuthority(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process and binds an AF_UNIX socket")
	}
	o, handles, refusal, output := runPerJailReasonFixture(t, "ready", time.Second, true, 0)
	if refusal != nil || o.startupRefusal != nil || len(handles) != 1 {
		t.Fatalf("reachable service without a reason was not accepted as ready: handles=%d refusal=%+v startup=%+v output=%s",
			len(handles), refusal, o.startupRefusal, output)
	}
}

func TestPerJailStartupReasonEarlyExitRetainsQueuedRefusal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	_, handles, refusal, output := runPerJailReasonFixture(t, "exit", 2*time.Second, false, 0)
	if len(handles) != 0 || refusal == nil ||
		refusal.reason != "fixture settings were refused" ||
		refusal.remedy != "correct the user settings and retry" {
		t.Fatalf("early child exit discarded an already-sent reason: handles=%d refusal=%+v output=%s", len(handles), refusal, output)
	}
}

// TestPerJailNonConfigurationRefusalIsPrintedBeforeTheDerivedSymptom: a cooperative refusal
// of another class is not fatal, but it is the daemon's own cause, so the launch prints it
// ahead of the generic exit warning instead of keeping it only in the typed outcome.
func TestPerJailNonConfigurationRefusalIsPrintedBeforeTheDerivedSymptom(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	t.Setenv(perJailReasonChildClassEnv, "dependency")
	_, handles, refusal, output := runPerJailReasonFixture(t, "exit", 2*time.Second, false, 0)
	if len(handles) != 0 || refusal != nil {
		t.Fatalf("a dependency refusal changed severity: handles=%d refusal=%+v", len(handles), refusal)
	}
	cause := strings.Index(output, "refused startup: fixture settings were refused Fix: correct the user settings and retry")
	symptom := strings.Index(output, "cannot reach it")
	if cause < 0 || symptom < 0 || cause > symptom {
		t.Fatalf("the daemon's own cause is not printed before the derived symptom:\n%s", output)
	}
}

func runPerJailReasonFixture(t *testing.T, mode string, timeout time.Duration, ready bool,
	delay time.Duration) (*Options, []loopholeDaemon, *hostStartupRefusal, string) {
	t.Helper()
	t.Setenv(perJailReasonChildModeEnv, mode)
	if delay > 0 {
		t.Setenv(perJailReasonChildDelayEnv, delay.String())
	} else {
		t.Setenv(perJailReasonChildDelayEnv, "")
	}
	module := filepath.Join(t.TempDir(), "per-jail-reason")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	command := []any{os.Args[0], "-test.run=^TestPerJailReasonChild$", "{socket}"}
	if ready {
		command = []any{os.Args[0], "-front-upstream-child", "line", "{socket}"}
		// The front child dispatches directly in TestMain and does not write a reason.
		t.Setenv(perJailReasonChildModeEnv, "")
	}
	manifest := fmt.Sprintf(`{
  "name": "per-jail-reason",
  "default_enabled": true,
  "host_daemon": {
    "cmd": %s,
    "startup_reason": true,
    "publishes": "socket"
  }
}`, jsonString(t, command))
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	if enabled := set.Enabled(); len(enabled) != 1 || enabled[0].Name != "per-jail-reason" {
		t.Fatalf("fixture loophole was not selected: %+v", enabled)
	}
	cname := "yolo-perjail-ready"
	if !ready {
		cname = "yolo-perjail-" + strings.ReplaceAll(t.Name(), "/", "-")
	}
	var output strings.Builder
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.ServiceReadyTimeout = timeout
	o.Stdout, o.Stderr = &output, &output
	o.PathExists = func(string) bool { return false }
	started := o.startLoopholesMatching(set, cname, "podman", jsonx.NewOrderedMap(), func(string) bool { return true })
	if len(started) > 0 {
		t.Cleanup(func() {
			o.stopLoopholes(started, hostServiceSocketsDir(cname, false), cname, "podman")
		})
	}
	return o, started, o.startupRefusal, output.String()
}

func jsonString(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
