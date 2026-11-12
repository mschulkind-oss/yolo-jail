package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// retiredReposFixture is a project that still holds the retired `requests.github.repos` list
// (GW-D75) under a user config that selects the github broker and grants it read.
func retiredReposFixture(t *testing.T) string {
	t.Helper()
	ws := testconfig.Workspace(t, "[requests.github]\nrepos = [\"you/dotfiles\"]\n")
	user := testconfig.User(t, "packs = [\"git\", \"github\"]\n\n[allow.github]\nalways = [\"read\"]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)
	return ws.Root
}

// The launch refuses the retired key: its config-change gate collects the broker scope through
// run.CollectScopeCheck, whose readable resolution fails, and config.ApproveWorkspaceRead returns
// that failure before anything is asked or recorded (preflight's approval step stops there).
// `yolo config check` must report the same problem rather than call the config valid, naming the
// key and the next step, `yolo config migrate`.
func TestConfigCheckReportsTheRetiredReposKeyALaunchRefuses(t *testing.T) {
	ws := retiredReposFixture(t)

	merged, err := config.LoadReadableConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	scope := run.CollectScopeCheck(run.ScopeInput{Workspace: ws, Merged: merged, Runtime: "podman", Read: read})
	if scope == nil || scope.Fatal == nil {
		t.Fatalf("the launch's gate does not refuse the retired key, so there is nothing for the check to predict: %+v", scope)
	}
	ok, _, _, gateErr := config.ApproveWorkspaceRead(ws, read, scope, false, true, nil)
	var authority *config.ReadableAuthorityError
	if ok || !errors.As(gateErr, &authority) || !strings.Contains(gateErr.Error(), "requests.github.repos is retired") {
		t.Fatalf("the launch's approval step did not refuse the retired key: ok=%v err=%v", ok, gateErr)
	}

	rc, out, errs := captureStd(t, func() int {
		return registry["config"]([]string{"config", "check"})
	})
	if rc == 0 {
		t.Fatalf("`yolo config check` passed a config the launch refuses: stdout=%q stderr=%q", out, errs)
	}
	if strings.Contains(out, "Merged config is semantically valid") {
		t.Fatalf("`yolo config check` called a config the launch refuses valid: %q", out)
	}
	for _, want := range []string{"[FAIL]", "requests.github.repos is retired", "yolo config migrate"} {
		if !strings.Contains(out, want) {
			t.Fatalf("`yolo config check` output lacks %q: stdout=%q stderr=%q", want, out, errs)
		}
	}
}
