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

// The negative: under a TOML user config whose ceiling does not list host-processes view, the
// same request is written and reported skipped, naming the `yolo allow` line that admits it.
func TestRequestOutsideATOMLUsersCeilingIsReportedSkipped(t *testing.T) {
	ws := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs = [\"host-processes\"]\n")
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
	// The verb's own report (reportRequestSkipped), not only the post-write check's.
	for _, want := range []string{"This project requests host-processes view postgres, but your user config does not allow view on request",
		"yolo allow host-processes on_request view", "yolo config apply"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the skipped report lacks %q: %q", want, out)
		}
	}
}

// In a jail, the skipped report's `yolo allow` is the human's own answer, run directly in a host
// terminal: the report says so and never wraps it in a `yolo host-run` line (the jail briefing's
// "Approvals are not host-run").
func TestRequestSkippedInAJailNeverRoutesTheAllowThroughHostRun(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	t.Setenv("YOLO_HOST_DIR", "/home/someone/code/proj")
	var out strings.Builder
	reportRequestSkipped(&out, "host-processes", "view", "view postgres")
	got := out.String()
	if !strings.Contains(got, "\n    yolo allow host-processes on_request view\n") ||
		!strings.Contains(got, "directly in a host terminal, not through `yolo host-run`") {
		t.Fatalf("the in-jail report does not hand the allow over to run directly: %q", got)
	}
	if strings.Contains(got, "yolo host-run -C") {
		t.Fatalf("the in-jail report routes the allow through host-run: %q", got)
	}
}
