package cli

import (
	"path/filepath"
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
