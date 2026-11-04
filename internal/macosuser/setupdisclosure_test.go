package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

func TestRunMacosUserPrintsTheResolvedSetupDisclosure(t *testing.T) {
	d := mockDeps(nil)
	var out bytes.Buffer
	d.Out = &out
	s := setup.MustBuiltIn("seatbelt-yolo-account")
	o := newOpts("/Users/Shared/yolo/proj")
	o.Setup = s
	o.SetupSource = setup.FromEnv
	o.DryRun = true

	RunMacosUser(d, o)
	want := setup.LaunchDisclosure(s, "host", setup.FromEnv)
	if !strings.Contains(out.String(), want) {
		t.Fatalf("macos-user launch omitted the shared setup disclosure %q:\n%s", want, out.String())
	}
}
