package cli

// sessionguard_dispatch_test.go pins the call site the macos-user launch argv reaches: `yolo
// internal session-guard` (macosuser.SessionGuardVerb) runs the command after `--` and returns
// its exit status (internal/macosuser/sessionguard.go). An unknown verb exits 2, so the case
// runs a whole, valid invocation whose child exits 5, which only the guard can report.

import (
	"os/exec"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

func TestYoloInternalSessionGuardRunsTheCommand(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if rc := runInternal([]string{macosuser.SessionGuardVerb, "--memory", "1073741824", "--",
		"sh", "-c", "exit 5"}); rc != 5 {
		t.Fatalf("yolo internal %s = %d, want the command's 5: the verb did not reach the guard",
			macosuser.SessionGuardVerb, rc)
	}
	if rc := runInternal([]string{macosuser.SessionGuardVerb, "--", "sh", "-c", "exit 5"}); rc != 2 {
		t.Errorf("a guard with no --memory = %d, want 2 (misuse)", rc)
	}
}
