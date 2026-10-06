package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// THE macos-user BOOTSTRAP KEEPS THE CONTAINER'S BOOT LOG, asked of a real sandbox.
//
// The bootstrap runs as the sandbox account outside Seatbelt and writes
// <workspace>/.yolo/boot.log through the workspace's inherited grant, beneath a root on `.yolo`
// (entrypoint.attachDarwinBootLog; the unit tests in internal/entrypoint/darwinbootlog_test.go
// pin the shape on Linux). What only a Mac can answer: that the account can create the file
// there, that the host user can read it afterwards, and that it records the whole bootstrap.
func TestMacosUserBootstrapKeepsABootLog(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{"mcp_presets": ["chrome-devtools"]}`)
	r := macosUserRunProbe(t, "boot log", ws, `echo "=== END ==="`)

	raw, err := os.ReadFile(entrypoint.BootLogPath(ws))
	if err != nil {
		t.Fatalf("the macos-user bootstrap kept no boot log at %s: %v\nlaunch output:\n%s",
			entrypoint.BootLogPath(ws), err, r.combined())
	}
	log := string(raw)
	if !strings.HasPrefix(log, "=== yolo entrypoint ") || !strings.Contains(log, "\n  macos-user bootstrap, yolo ") {
		t.Errorf("boot.log's header does not say what booted:\n%s", log)
	}
	// A terminal warning the bootstrap printed reaches the log too: the mcp_presets refusal,
	// which this workspace's config asks for on purpose.
	if !strings.Contains(log, "mcp_presets are not delivered on macos-user") {
		t.Errorf("boot.log lacks the bootstrap's own warning:\n%s", log)
	}
	if !strings.HasSuffix(log, "=== boot complete, handing over ===\n") {
		t.Errorf("boot.log does not record how the bootstrap ended:\n%s", log)
	}
}
