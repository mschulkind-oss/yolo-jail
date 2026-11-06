package callbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCanonicalCLIExecutorNeverStartsPreCancelledCall(t *testing.T) {
	marker := filepath.Join(canonicalTempDir(t), "started")
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	if err := os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nprintf started > %q\n", marker)), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := false
	originalStart := startOwnedCommand
	startOwnedCommand = func(cmd *exec.Cmd) error { started = true; return cmd.Start() }
	t.Cleanup(func() { startOwnedCommand = originalStart })
	result, err := p.run(ctx, Classification{Argv: []string{"write"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
	if started {
		t.Fatal("pre-cancelled canonical CLI reached cmd.Start")
	}
	if !errors.Is(err, context.Canceled) || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("pre-cancelled call: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pre-cancelled host CLI executed: %v", err)
	}
}

func TestCanonicalCLIExecutorPinsHostProgramAndRedactsResponse(t *testing.T) {
	dir := canonicalTempDir(t)
	log := filepath.Join(dir, "calls")
	stdinLog := filepath.Join(dir, "stdin")
	binary := filepath.Join(dir, "fixture")
	body := fmt.Sprintf(`#!/bin/sh
[ "$PWD" = / ] && [ -z "$JAIL_ONLY" ] && [ ! -t 0 ] && [ ! -t 1 ] || exit 19
printf '%%s|%%s|%%s' "$1" "$2" "$HOME" > %q
cat > %q
printf 'out:%%s' "$HOST_TOKEN"
printf 'err:%%s' "$HOST_TOKEN" >&2
`, log, stdinLog)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "/host-home")
	t.Setenv("HOST_TOKEN", "synthetic-only-secret")
	t.Setenv("JAIL_ONLY", "not-forwarded")
	if _, err := pinCLI(binary, []string{"HOST_TOKEN\nJAIL_ONLY"}); err == nil {
		t.Fatal("invalid environment declaration was accepted")
	}
	p, err := pinCLI(binary, []string{"HOST_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	stdin := []byte("original stdin")
	canonical := Classification{Argv: []string{"update", "a1"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(stdin))}
	wrong := canonical
	wrong.StdinSHA256 = strings.Repeat("0", 64)
	if _, err := p.run(context.Background(), wrong, stdin); err == nil {
		t.Fatal("changed canonical digest executed")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("host program ran before canonical digest check: %v", err)
	}
	result, err := p.run(context.Background(), canonical, stdin)
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "out:[redacted]" || string(result.Stderr) != "err:[redacted]" {
		t.Fatalf("redacted canonical result=%+v err=%v", result, err)
	}
	actual, err := os.ReadFile(log)
	if err != nil || string(actual) != "update|a1|/host-home" {
		t.Fatalf("canonical invocation=%q err=%v", actual, err)
	}
	actual, err = os.ReadFile(stdinLog)
	if err != nil || !bytes.Equal(actual, stdin) {
		t.Fatalf("original stdin=%q err=%v", actual, err)
	}
}

func TestCanonicalCLIExecutorRedactsAndBoundsCompletedExitError(t *testing.T) {
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'out:%s' \"$HOST_TOKEN\"\nprintf 'err:%s' \"$HOST_TOKEN\" >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOST_TOKEN", "synthetic-secret")
	p, err := pinCLI(binary, []string{"HOST_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	canonical := Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}
	result, err := p.run(context.Background(), canonical, nil)
	if err != nil || result.ExitCode != 7 || string(result.Stdout) != "out:[redacted]" || string(result.Stderr) != "err:[redacted]" {
		t.Fatalf("completed nonzero exit: result=%+v err=%v", result, err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nhead -c 33554433 /dev/zero | tr '\\000' x\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// This test is about output bounds, not a changed pinned CLI. Re-pin the
	// synthetic program after replacing its contents.
	p, err = pinCLI(binary, []string{"HOST_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	result, err = p.run(context.Background(), canonical, nil)
	if err == nil || !strings.Contains(err.Error(), "32 MiB") || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("completed nonzero oversized response: stdout=%d stderr=%d err=%v", len(result.Stdout), len(result.Stderr), err)
	}
}

func TestCanonicalCLIExecutorBoundsResponse(t *testing.T) {
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nhead -c 33554433 /dev/zero | tr '\\000' x\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.run(context.Background(), Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
	if err == nil || !strings.Contains(err.Error(), "32 MiB") || len(result.Stdout) != 0 {
		t.Fatalf("oversized response crossed to jail: result=%d err=%v", len(result.Stdout), err)
	}
}

func TestCanonicalCLIExecutorBoundsRedactedResponseWithoutReturningPartialSecrets(t *testing.T) {
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	// Exactly 32 MiB pre-redaction. The one-byte declared synthetic value would
	// expand the result beyond the cap; no prefix may be returned to the caller.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nhead -c 33554432 /dev/zero | tr '\\000' x\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNTHETIC_HOST_VALUE", "x")
	p, err := pinCLI(binary, []string{"SYNTHETIC_HOST_VALUE"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.run(context.Background(), Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
	if err == nil || !strings.Contains(err.Error(), "32 MiB") || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("expanded response crossed boundary: stdout=%d stderr=%d err=%v", len(result.Stdout), len(result.Stderr), err)
	}
	// Longest value first avoids exposing the suffix of a declared value when
	// declared values share a prefix.
	t.Setenv("SYNTHETIC_HOST_VALUE", "ab")
	t.Setenv("SYNTHETIC_LONGER_VALUE", "abc")
	p, err = pinCLI(binary, []string{"SYNTHETIC_HOST_VALUE", "SYNTHETIC_LONGER_VALUE"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := redactBounded([]byte("abc ab"), p.secrets)
	if err != nil || string(got) != "[redacted] [redacted]" {
		t.Fatalf("overlap redaction=%q err=%v", got, err)
	}
}

func TestCanonicalCLIExecutorStopsOwnedDescendantAfterSuccessfulWrapperExit(t *testing.T) {
	marker := filepath.Join(canonicalTempDir(t), "survived")
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	body := fmt.Sprintf("#!/bin/sh\n(sleep 0.4; printf alive > %q) &\nprintf ok\n", marker)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.run(context.Background(), Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
	if err != nil || string(result.Stdout) != "ok" {
		t.Fatalf("normal wrapper exit: output=%q err=%v", result.Stdout, err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("owned child survived normal exit: %v", err)
	}
}

// TestCanonicalCLIExecutorCancelsLiveLeaderAfterClosingOutput: a leader that closed its output
// and stays live, with a live descendant, is ended by cancellation, descendant and all. Every
// step waits on an event: the descendant saying it is live, the call returning after the
// cancel, and the descendant's exit closing the one write end of the alive FIFO it holds. The
// leader and descendant block on hold, which nothing writes, so neither ends by itself.
func TestCanonicalCLIExecutorCancelsLiveLeaderAfterClosingOutput(t *testing.T) {
	hold := fixtureFIFO(t, "hold")
	ready := fixtureFIFO(t, "ready")
	alive := fixtureFIFO(t, "alive")
	descendantGone := awaitFIFO(alive)
	descendantLive := awaitFIFO(ready)
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	body := fmt.Sprintf("#!/bin/sh\nexec 1>&- 2>&-\n(exec 3>%q; printf live > %q; read x < %q) &\nread x < %q\n",
		alive, ready, hold, hold)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result CLIResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := p.run(ctx, Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
		finished <- outcome{result, err}
	}()
	select {
	case <-descendantLive:
	case got := <-finished:
		t.Fatalf("the call ended before its descendant was live: %v", got.err)
	case <-time.After(testEventGuard):
		t.Fatal("the fixture's descendant never reported live")
	}
	cancel()
	var got outcome
	select {
	case got = <-finished:
	case <-time.After(testEventGuard):
		t.Fatal("cancellation did not end the call while its leader was live")
	}
	if err := got.err; err == nil || !strings.Contains(err.Error(), "cancelled") || len(got.result.Stdout) != 0 {
		t.Fatalf("live closed-pipe leader was not cancelled: output=%d err=%v", len(got.result.Stdout), err)
	}
	// Only the descendant holds the alive FIFO open for writing, so its EOF is that exit.
	select {
	case <-descendantGone:
	case <-time.After(testEventGuard):
		t.Fatal("owned child survived cancellation")
	}
}

// testEventGuard is a failure guard only: it turns a hang into a red test, and no assertion
// depends on beating it.
const testEventGuard = time.Minute

// fixtureFIFO makes a named pipe a fixture script and its test meet at. It is released at
// cleanup (releaseFIFO), so a fixture or a reader still blocked on it ends.
func fixtureFIFO(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(canonicalTempDir(t), name)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseFIFO(path) })
	return path
}

// releaseFIFO ends, with EOF, every reader of path still waiting for a writer. None waiting is
// not an error.
func releaseFIFO(path string) {
	if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
		_ = f.Close()
	}
}

// awaitFIFO opens path for reading and returns a channel closed once every writer has opened and
// closed it again: the event of a fixture writing to it, or of the last process holding it open
// for writing exiting.
func awaitFIFO(path string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = os.ReadFile(path)
	}()
	return done
}
