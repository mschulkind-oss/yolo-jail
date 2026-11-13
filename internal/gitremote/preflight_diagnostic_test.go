package gitremote

import (
	"bytes"
	"context"
	"errors"
	"io"
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
//
// The session ends on an EVENT, not a wall budget: the fake Git announces entry at
// the selected probe through a FIFO (after its output, just before it blocks), and
// only then is the session's context
// canceled. A short accessTimeout covered every earlier probe too, so on a slow
// runner (macOS CI, run 42423928526) it expired during an earlier probe that had
// already exited, and the injected EPERM reported that probe instead of this one.
//
// Nor is the return timed. The selected probe sleeps an hour, so ForwardAccess
// returning at all is the proof that it did not wait for the denied group; one that
// waited hangs the test into go test's timeout, which is red. A 20s bound on the
// return used to stand in for that against a 30s sleep, and measured the machine.
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
  echo entered > "$ENTERED_FIFO"
  exec sleep 3600
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
			enteredFIFO := filepath.Join(bin, "entered.fifo")
			if err := syscall.Mkfifo(enteredFIFO, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ENTERED_FIFO", enteredFIFO)
			session, endSession := context.WithCancel(context.Background())
			defer endSession()
			announced := make(chan struct{})
			go func() {
				defer close(announced)
				// Blocks until the selected probe opens the FIFO to write.
				f, err := os.OpenFile(enteredFIFO, os.O_RDONLY, 0)
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, f)
				_ = f.Close()
				endSession()
			}()
			t.Cleanup(func() {
				// Unblock the reader when the selected probe was never reached.
				if f, err := os.OpenFile(enteredFIFO, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
					_ = f.Close()
				}
				<-announced
			})
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
			_, code := ForwardAccess("evaluate", []string{"git", "push", "origin", "HEAD:refs/heads/main"}, Env{
				Getenv: func(key string) string {
					if key == EndpointEnv {
						return filepath.Join(bin, "no-broker-endpoint")
					}
					return ""
				},
				Stderr: &stderr, accessContext: session,
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
			if code != exitUsage || pid == 0 || !strings.Contains(msg, "group signal failed (operation not permitted)") || !strings.Contains(msg, "birth identity ") || !strings.Contains(msg, "descendants may remain") || !strings.Contains(msg, "before retrying") || strings.Contains(msg, "cannot reach git-broker") || strings.Contains(msg, "example.invalid") {
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
