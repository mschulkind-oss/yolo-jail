package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This fixture test drives catrustInstall through a synthetic security executable. It never
// invokes macOS security or changes a keychain; the helper only stalls the subprocess or leaves
// a child holding its inherited output pipe open.
func TestCatrustInstallBoundsSecurityCommandAndOutputWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the security fixture is Unix-only")
	}
	for _, mode := range []string{"stalled-command", "inherited-output"} {
		t.Run(mode, func(t *testing.T) {
			binDir := t.TempDir()
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			fakeSecurity := filepath.Join(binDir, "security")
			script := `#!/bin/sh
if [ "$1" = "add-certificates" ]; then
  case "$CATRUST_FIXTURE_MODE" in
    stalled-command) sleep 2 ;;
    inherited-output) sleep 2 & echo $! > "$CATRUST_FIXTURE_PIDFILE"; exit 0 ;;
  esac
fi
exit 0
`
			if err := os.WriteFile(fakeSecurity, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CATRUST_FIXTURE_MODE", mode)
			t.Setenv("CATRUST_FIXTURE_PIDFILE", pidFile)
			// The inherited-output case leaves sleep holding output descriptors. Reap it after
			// the runner returns, including when an earlier assertion fails.
			t.Cleanup(func() {
				contents, err := os.ReadFile(pidFile)
				if err != nil {
					return
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
				if err != nil {
					t.Errorf("parsing synthetic child pid: %v", err)
					return
				}
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Kill()
				}
			})

			var calls [][]string
			run := catrustSecurityRunner(func(args []string, stdin []byte, stdoutOnly bool) ([]byte, error) {
				calls = append(calls, append([]string(nil), args...))
				return runCatrustCommand(fakeSecurity, args, stdin, stdoutOnly, 200*time.Millisecond, 100*time.Millisecond)
			})
			t.Cleanup(func() {
				if countCatrustCalls(calls, "delete-certificate") != 2 {
					t.Errorf("fixture caller did not run both bounded certificate cleanups: %v", calls)
				}
			})

			dir := t.TempDir()
			ca, _ := catrustCA(t, "fixture trusted CA")
			control, _ := catrustCA(t, "fixture control CA")
			start := time.Now()
			err := catrustInstall(t, dir, filepath.Join(dir, "ca.pem"), ca,
				filepath.Join(dir, "control.pem"), control, run)
			if err == nil {
				t.Fatal("synthetic stalled security operation succeeded")
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("catrustInstall waited %s for a synthetic %s security command/output pipe; want a bounded return",
					elapsed, mode)
			}
			if len(calls) == 0 || calls[0][0] != "add-certificates" {
				t.Fatalf("fixture caller did not route installation through the bounded runner: %v", calls)
			}
		})
	}
}

// TestCatrustInstallRoutesTheWholeLifecycleThroughItsRunner pins installation, authorization
// read/write, retry, restoration, trust removal and certificate cleanup at the fixture caller.
func TestCatrustInstallRoutesTheWholeLifecycleThroughItsRunner(t *testing.T) {
	t.Setenv(macosUserDeclareEnv, "1")
	var calls [][]string
	var stdinByCall [][]byte
	var stdoutOnlyByCall []bool
	trustedAttempts := 0
	run := catrustSecurityRunner(func(args []string, stdin []byte, stdoutOnly bool) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		stdinByCall = append(stdinByCall, append([]byte(nil), stdin...))
		stdoutOnlyByCall = append(stdoutOnlyByCall, stdoutOnly)
		if args[0] == "authorizationdb" && args[1] == "read" && !stdoutOnly {
			return nil, os.ErrInvalid
		}
		if args[0] != "authorizationdb" && stdoutOnly {
			return nil, os.ErrInvalid
		}
		switch args[0] {
		case "add-certificates":
			return nil, nil
		case "add-trusted-cert":
			trustedAttempts++
			if trustedAttempts == 1 {
				return []byte("authorization required"), os.ErrPermission
			}
			return nil, nil
		case "authorizationdb":
			if args[1] == "read" {
				return []byte("fixture authorization rule"), nil
			}
			return nil, nil
		case "remove-trusted-cert", "delete-certificate":
			return nil, nil
		default:
			return nil, os.ErrInvalid
		}
	})

	var lifecycle []string
	var restoredRule []byte
	t.Cleanup(func() {
		for i, call := range calls {
			label := call[0]
			readRule := call[0] == "authorizationdb" && len(call) > 1 && call[1] == "read"
			if stdoutOnlyByCall[i] != readRule {
				t.Errorf("security operation %v used stdoutOnly=%t, want %t", call, stdoutOnlyByCall[i], readRule)
			}
			if call[0] == "authorizationdb" && len(call) > 1 {
				label += " " + call[1]
			}
			lifecycle = append(lifecycle, label)
		}
		for i, call := range calls {
			if len(call) > 1 && call[0] == "authorizationdb" && call[1] == "write" && len(stdinByCall[i]) > 0 {
				restoredRule = stdinByCall[i]
			}
		}
		want := []string{
			"add-certificates", "add-trusted-cert", "authorizationdb read", "authorizationdb write",
			"add-trusted-cert", "remove-trusted-cert", "authorizationdb write", "delete-certificate",
			"delete-certificate",
		}
		if strings.Join(lifecycle, "|") != strings.Join(want, "|") {
			t.Errorf("security lifecycle calls = %v, want %v", lifecycle, want)
		}
		if string(restoredRule) != "fixture authorization rule" {
			t.Errorf("authorizationdb restore stdin = %q, want the saved rule", restoredRule)
		}
	})

	dir := t.TempDir()
	ca, _ := catrustCA(t, "fixture trusted CA")
	control, _ := catrustCA(t, "fixture control CA")
	if err := catrustInstall(t, dir, filepath.Join(dir, "ca.pem"), ca,
		filepath.Join(dir, "control.pem"), control, run); err != nil {
		t.Fatalf("synthetic trust fixture: %v", err)
	}
}

func countCatrustCalls(calls [][]string, operation string) int {
	count := 0
	for _, call := range calls {
		if len(call) > 0 && call[0] == operation {
			count++
		}
	}
	return count
}
