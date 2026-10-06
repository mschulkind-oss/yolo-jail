package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// `yolo config drift` AND `yolo config dump` INSIDE A MACOS-USER SANDBOX. Both read files the
// launch writes for the session into <workspace>/.yolo: the frozen workspace baseline, and the
// merged config. The unit tests pin the writes and the readers on Linux
// (internal/cli/run/macosuserconfigartifacts_test.go); this asks the sandbox, whose session env,
// home and cwd are the inputs those readers take.
//
// The user config carries `update_check`, a host-only key the inherited user scope does not
// carry, so a dump that re-assembled under the sandbox account's home instead of reading the
// launch's merged config would lose it — which is what makes the dump comparison mean something.
func TestMacosUserConfigDriftAndDump(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"update_check": false}`)
	ws := macosUserWorkspace(t, `{}`)
	r := runMacosUser(t, ws, strings.Join([]string{
		`cd ` + shquote.Quote(ws),
		`echo "=== DRIFT ==="`,
		`yolo config drift; echo "RC=$?"`,
		`echo "=== DUMP ==="`,
		`yolo config dump`,
		`echo "=== EDITED ==="`,
		`printf '{"packages": ["jq"]}\n' > yolo-jail.jsonc`,
		`yolo config drift; echo "RC=$?"`,
		`echo "=== END ==="`,
	}, "\n"))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("the macos-user launch did not run its probe (rc %d).\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	if got := section(r.stdout, "=== DRIFT ===", "=== DUMP ==="); !strings.Contains(got, "RC=0") {
		t.Errorf("`yolo config drift` in a fresh sandbox did not report in sync (exit 0):\n%s", got)
	}
	assembled, err := os.ReadFile(filepath.Join(ws, ".yolo", "config-assembled.json"))
	if err != nil {
		t.Fatalf("the launch wrote no config-assembled.json: %v", err)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== DUMP ===", "=== EDITED ===")); got != strings.TrimSpace(string(assembled)) {
		t.Errorf("`yolo config dump` in the sandbox is not the merged config the launch wrote:\n"+
			" got: %s\nwant: %s", got, assembled)
	}
	if got := section(r.stdout, "=== EDITED ===", "=== END ==="); !strings.Contains(got, "RC=3") {
		t.Errorf("`yolo config drift` after editing the workspace config did not report drift (exit 3):\n%s", got)
	}
}
