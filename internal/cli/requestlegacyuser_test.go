package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// `yolo request`'s ceiling check is the gate's own predicate (config.LoopholeRequestAdmitted), so
// a legacy user config's admission of a legacy_enable kind (docs/design/legacy-config-compat.md
// LC-D14) holds for the verb as it does at the gate: a request for host-processes view under a
// legacy config.jsonc that selects the pack is not reported skipped. The verb's former private
// check read only the TOML ceiling and called it skipped.
func TestRequestUnderALegacyUserConfigIsNotReportedSkipped(t *testing.T) {
	ws := testconfig.Workspace(t, "")
	user := testconfig.UserFormat(t, configdoc.JSONC, `{"packs": ["host-processes"]}`)
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)

	rc, out, errs := captureStd(t, func() int {
		return registry["request"]([]string{"request", "host-processes", "view", "postgres"})
	})
	if got := readFile(t, ws.Path); !strings.Contains(got, `view = ["postgres"]`) {
		t.Fatalf("the request was not written:\n%s\nrc=%d stdout=%q stderr=%q", got, rc, out, errs)
	}
	if strings.Contains(out, "runs without it") {
		t.Fatalf("a request a legacy user config admits was reported skipped: %q", out)
	}
	if !strings.Contains(out, "yolo config apply") {
		t.Fatalf("the verb did not name the host step: %q", out)
	}
}
