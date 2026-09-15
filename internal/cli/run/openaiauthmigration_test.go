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
	// ⚠ THE SCRIPT IS A LIST (`sleep 30; :`) AND THE WAIT BELOW IS NOT OPTIONAL. Both exist
	// because this test was racing the shell, and check-macos is where it lost (`91de11d1`).
	//
	// `sh -c` EXECs a single simple command instead of forking it, replacing its own process
	// image — so with `sleep 30` the live argv becomes just "sleep 30" and the `{state}` marker
	// this test exists to detect is GONE. MEASURED on Linux 400ms after Start(): `sleep 30` →
	// no marker; `sleep 30; :` → the full argv, because a shell cannot exec away a list it
	// still has to finish. (`while :; do sleep 1; done` also works and respawns a child every
	// second; a list costs one child.)
	//
	// WHY IT PASSED ON LINUX AND FAILED ON DARWIN: the detector reads /proc/<pid>/cmdline and
	// falls back to `ps` where there is no /proc. Against `sleep 30`, the /proc read takes
	// microseconds and beat sh's exec; darwin has to fork `ps`, which takes milliseconds and
	// lost every time. 20/20 green here was winning a race by a wide margin, not determinism.
	//
	// AND THE OTHER EDGE, which a list alone does not fix: cmd.Start() returns after the FORK,
	// before the exec of sh has necessarily landed, so an immediate read can see the go test
	// binary's own argv. Measured: asserting straight after Start() failed intermittently at
	// -count=10. So wait for the argv to be observable, then assert — which is also the shape
	// the sibling tests in these packages use for "published nothing yet".
	cmd := exec.Command("sh", "-c", "sleep 30; :",
		"openai-auth-broker", "--state-file", "{state}/credentials.json")
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
	deadline := time.Now().Add(5 * time.Second)
	for !singletonUsesLegacyOpenAIStatePath(deps) {
		if time.Now().After(deadline) {
			t.Fatal("living singleton with literal {state} argv was not detected for " +
				"replacement within 5s — the detector never saw the marker in this process's " +
				"argv. On a platform with no /proc it reads `ps`; check that the shell did not " +
				"exec-optimize the script away (see the comment above).")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
