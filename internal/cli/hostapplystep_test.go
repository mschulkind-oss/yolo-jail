package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostapplystep_test.go pins hostApplyStep, the "`yolo host apply --assert` <does>" next step the
// host's floor and tree lines name. Under "none", the unset default since the `assert` retirement
// (OQ-CO14), that apply refuses before every stage and writes nothing, so the bare step would
// fail there: the step names the setting it needs.
func TestTheHostApplyStepNamesOwnWhereTheApplyWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, c := range []struct{ cfg, want string }{
		{`{}`, "`yolo host apply --assert` builds it once `\"host_management\": \"own\"` is set"},
		{`{"host_management":"none"}`, "`yolo host apply --assert` builds it once `\"host_management\": \"own\"` is set"},
		{`{"host_management":"own"}`, "`yolo host apply --assert` builds it"},
	} {
		writeFile(t, paths.UserConfigPath(), c.cfg)
		got := hostApplyStep("builds it")
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %q, want it to start %q", c.cfg, got, c.want)
		}
		if c.cfg != `{"host_management":"own"}` && got == "`yolo host apply --assert` builds it" {
			t.Errorf("%s: the bare apply is named where it writes nothing", c.cfg)
		}
	}
}
