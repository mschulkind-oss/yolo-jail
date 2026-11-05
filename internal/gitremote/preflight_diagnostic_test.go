package gitremote

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Each case enters through the real access producer, not a callee-only assertion.
// The fake Git progresses through earlier probes before blocking at the selected
// callsite. A missing typed propagation would replace the diagnostic with refusal.
func TestForwardAccessDisclosesDeniedGitGroupTerminationAtEveryPreflightProbe(t *testing.T) {
	const stdoutSentinel = "synthetic-credential-like-stdout-sentinel-never-real"
	const stderrSentinel = "synthetic-credential-like-stderr-sentinel-never-real"
	for _, target := range []string{"remote", "rewrite", "check-ref", "push-option", "rev-parse", "dry-run", "ls-remote"} {
		t.Run(target, func(t *testing.T) {
			bin := t.TempDir()
			script := `#!/bin/sh
case "$*" in
  "remote get-url --push --all origin") step=remote ;;
  "config --null --get-regexp "*) step=rewrite ;;
  "check-ref-format refs/heads/main") step=check-ref ;;
  "config --null --get-all push.pushOption") step=push-option ;;
  "rev-parse --verify --end-of-options HEAD^{object}") step=rev-parse ;;
  "push --dry-run --porcelain "*) step=dry-run ;;
  "ls-remote --refs "*) step=ls-remote ;;
  *) exit 2 ;;
esac
if [ "$step" = "$TARGET_STEP" ]; then
  echo entered > "$MARKER"
  printf '%s\n' "$STDOUT_SENTINEL"
  printf '%s\n' "$STDERR_SENTINEL" >&2
  : > "$OUTPUT_WITNESS"
  exec sleep 30
fi
case "$step" in
 remote) echo yolo::https://example.invalid/o/r ;;
 rewrite|check-ref) exit 0 ;;
 push-option) exit 1 ;;
 rev-parse) printf '%040d\n' 1 ;;
 dry-run) printf 'To yolo::https://example.invalid/o/r\n*\tHEAD:refs/heads/main\t[new branch]\nDone\n' ;;
 ls-remote) exit 0 ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(bin, "entered")
			outputWitness := filepath.Join(bin, "sentinels-emitted-before-blocking")
			t.Setenv("TARGET_STEP", target)
			t.Setenv("MARKER", marker)
			t.Setenv("OUTPUT_WITNESS", outputWitness)
			t.Setenv("STDOUT_SENTINEL", stdoutSentinel)
			t.Setenv("STDERR_SENTINEL", stderrSentinel)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			oldSignal := gitPreflightGroupSignal
			pid := 0
			gitPreflightGroupSignal = func(group int, sig syscall.Signal) error { pid = -group; return syscall.EPERM }
			t.Cleanup(func() {
				gitPreflightGroupSignal = oldSignal
				if pid == 0 {
					return
				}
				// The direct child is ours and remains unreaped after Process.Release.
				if exited, err := gitExitedUnreaped(pid); err == nil && !exited {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
				var status syscall.WaitStatus
				_, _ = syscall.Wait4(pid, &status, 0, nil)
			})
			var stderr bytes.Buffer
			start := time.Now()
			_, code := ForwardAccess("evaluate", []string{"git", "push", "origin", "HEAD:refs/heads/main"}, Env{
				Getenv: func(key string) string {
					if key == EndpointEnv {
						return filepath.Join(bin, "no-broker-endpoint")
					}
					return ""
				},
				Stderr: &stderr, accessTimeout: 120 * time.Millisecond,
			})
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("Git %s was not reached: %v; stderr=%s", target, err, stderr.String())
			}
			if _, err := os.Stat(outputWitness); err != nil {
				t.Fatalf("selected fake Git %s did not emit output before blocking: %v", target, err)
			}
			msg := stderr.String()
			if strings.Contains(msg, stdoutSentinel) || strings.Contains(msg, stderrSentinel) {
				t.Fatalf("captured fake Git output leaked to ForwardAccess stderr: %q", msg)
			}
			if code != exitUsage || pid == 0 || time.Since(start) > time.Second || !strings.Contains(msg, "group signal failed (operation not permitted)") || !strings.Contains(msg, "birth identity ") || !strings.Contains(msg, "descendants may remain") || !strings.Contains(msg, "before retrying") || strings.Contains(msg, "cannot reach git-broker") || strings.Contains(msg, "example.invalid") {
				t.Fatalf("denial must refuse before broker contact, without URL or generic refusal: code=%d pid=%d stderr=%q", code, pid, msg)
			}
		})
	}
}

func TestGitPreflightDeniedGroupAfterLeaderExitReportsUnconfirmedDescendants(t *testing.T) {
	bin := t.TempDir()
	childFile := filepath.Join(bin, "child")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nsleep 30 &\necho $! > \"$CHILD_FILE\"\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHILD_FILE", childFile)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	pid := 0
	t.Cleanup(func() {
		b, err := os.ReadFile(childFile)
		if err == nil {
			child, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil {
				_ = syscall.Kill(child, syscall.SIGKILL)
			}
		}
		if pid != 0 {
			var status syscall.WaitStatus
			_, _ = syscall.Wait4(pid, &status, 0, nil)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_, err := gitOutputWithGroupSignal(ctx, bin, func(group int, sig syscall.Signal) error { pid = -group; return syscall.EPERM })
	var denied *GitGroupTerminationError
	if !errors.As(err, &denied) || !errors.Is(err, syscall.EPERM) || !errors.Is(err, context.DeadlineExceeded) || denied.PID != pid || denied.BirthIdentity == "" || !strings.Contains(denied.LastObservation, "reaped") || !strings.Contains(denied.Error(), "descendants may remain") {
		t.Fatalf("exited parent must not claim group quiescence: pid=%d err=%v", pid, err)
	}
}
