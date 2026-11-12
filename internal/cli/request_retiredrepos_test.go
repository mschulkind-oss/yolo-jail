package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// A config that still holds the retired `requests.github.repos` list (GW-D75) cannot be checked
// semantically, so a verb over it is refused. The refusal names that problem and its next step,
// `yolo config migrate`, never the validator's internal "typed consumer did not run" sentinel,
// which named no step at all (the owner's own workspace hit it on 2026-11-12).
func TestAnEditOverTheRetiredReposKeyNamesItAndItsMigration(t *testing.T) {
	ws := testconfig.Workspace(t, "[requests.github]\nrepos = [\"you/dotfiles\"]\n")
	user := testconfig.User(t, "packs = [\"git\", \"github\"]\n\n[allow.github]\nalways = [\"read\"]\non_request = [\"runs\"]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)
	before := readFile(t, ws.Path)

	rc, out, errs := captureStd(t, func() int {
		return registry["request"]([]string{"request", "repos", "lib", "github.com/you/lib"})
	})
	if rc == 0 {
		t.Fatalf("an edit over the retired key was accepted: stdout=%q stderr=%q", out, errs)
	}
	if strings.Contains(errs, "typed consumer did not run") {
		t.Fatalf("the refusal names the validator's internal sentinel, not the problem: %q", errs)
	}
	for _, want := range []string{"requests.github.repos is retired", "[repos.<name>]", "yolo config migrate"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("the refusal lacks %q: %q", want, errs)
		}
	}
	if got, err := os.ReadFile(ws.Path); err != nil || string(got) != before {
		t.Fatalf("a refused edit wrote the project file: err=%v\n%s", err, got)
	}
}
