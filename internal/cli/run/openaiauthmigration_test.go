package run

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
)

func TestLegacyOpenAIStateIsMovedOutOfTheWorkspace(t *testing.T) {
	retireHome(t)
	workspace := t.TempDir()
	legacyDir := filepath.Join(workspace, "{state}")
	if err := os.Mkdir(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := openaiauth.State{
		Version: 1, AccessToken: "access", IDToken: "id", RefreshToken: "refresh",
		ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), Generation: 1,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, openaiauth.StateFileName)
	if err := os.WriteFile(legacy, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "refresh.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deps := broker.SingletonDeps(openaiauth.LoopholeName, []string{"/bin/true"})
	deps.PIDFilePath = filepath.Join(t.TempDir(), "absent.pid")

	afterStop, err := prepareLegacyOpenAIAuthState(workspace, deps)()
	if err != nil {
		t.Fatal(err)
	}
	if afterStop == nil {
		t.Fatal("recovery did not request replacement of the daemon using the legacy path")
	}
	if err := afterStop(); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(loopholes.StateDirFor(openaiauth.LoopholeName), openaiauth.StateFileName)
	if _, err := openaiauth.ReadState(canonical); err != nil {
		t.Fatalf("canonical recovered state: %v", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("canonical state mode = %v, %v; want 0600", info, err)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy credential still exists in workspace: %v", err)
	}
	if _, err := os.Lstat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("empty literal state directory still exists: %v", err)
	}
}

func TestLivingLegacyOpenAIDaemonIsDetectedBeforeItCanCreateRelativeState(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30", "openai-auth-broker", "--state-file", "{state}/credentials.json")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deps := broker.SingletonDeps(openaiauth.LoopholeName, nil)
	deps.PIDFilePath = filepath.Join(t.TempDir(), "singleton.pid")
	if err := os.WriteFile(deps.PIDFilePath, []byte(fmt.Sprint(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if !singletonUsesLegacyOpenAIStatePath(deps) {
		t.Fatal("living singleton with literal {state} argv was not detected for replacement")
	}
}
