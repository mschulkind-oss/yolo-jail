package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInternalDispatchRoutesOpenAIAuthClient(t *testing.T) {
	t.Setenv("YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT", filepath.Join(t.TempDir(), "missing.endpoint"))
	// The client reports a connection failure as 1. A missing dispatch row
	// reports the internal command as unknown and returns 2.
	if rc := runInternal([]string{"openai-auth-client", "status"}); rc != 1 {
		t.Fatalf("openai-auth-client dispatch rc = %d, want client failure 1", rc)
	}
}

func TestInternalDaemonDispatchRoutesOpenAIAuthBroker(t *testing.T) {
	// Self-check is read-only and returns 1 for absent state. Falling through
	// the daemon dispatch returns 2, so this pins the production caller.
	state := filepath.Join(t.TempDir(), "missing.json")
	if rc := runInternalDaemon([]string{"openai-auth-broker", "--self-check", "--state-file", state}); rc != 1 {
		t.Fatalf("openai-auth-broker dispatch rc = %d, want self-check failure 1", rc)
	}
}

// THE HOST OPERATOR'S ROW, pinned the way the two above are: by a return code only a real
// dispatch can produce.
//
// `openai-auth` with no subcommand prints its own usage and returns 2 — the same 2 a MISSING
// dispatch row returns, so the code alone would not distinguish them. The stderr does: a
// missing row says `yolo internal: unknown command`, and the verb names its three
// subcommands. Asserted with no subcommand deliberately: every real one resolves the private
// credential socket, which would start the machine-wide daemon from a unit test.
func TestInternalDispatchRoutesTheOpenAIAuthOperator(t *testing.T) {
	rc, stderr := captureInternalStderr(t, []string{"openai-auth"})
	if rc != 2 {
		t.Fatalf("openai-auth dispatch rc = %d, want the verb's usage refusal 2", rc)
	}
	if !strings.Contains(stderr, "status|import|logout") {
		t.Fatalf("stderr = %q — a missing dispatch row would report an unknown internal "+
			"command instead of the verb's own usage", stderr)
	}
}

// captureInternalStderr runs one `yolo internal` argv with os.Stderr redirected to a file,
// the same swap captureDispatchRC uses for the dispatch tests one file over (runInternal
// writes to the process streams, so there is no writer to inject).
func captureInternalStderr(t *testing.T, args []string) (int, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	old := os.Stderr
	os.Stderr = f
	rc := func() int {
		defer func() { os.Stderr = old; f.Close() }()
		return runInternal(args)
	}()
	body, _ := os.ReadFile(path)
	return rc, string(body)
}
