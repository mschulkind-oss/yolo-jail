package check

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A DEV OVERRIDE of a program no pack builds from its own tree fails `yolo check`, as it refuses the
// launch (docs/design/pack-buildable-programs.md §5, BG-D19), naming the fork-pack next step; one
// naming no selected program stays information. Red if sectionPacks drops its NonPackTreeOverrideLines
// check.
func TestCheckFailsADevOverrideOfANonPackTreeProgram(t *testing.T) {
	dir := t.TempDir()
	packsFixture(t, `{"packs": ["claude"], "programs": {"overrides": {"claude": {"path": "`+dir+`"}}}}`)
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	got := stripANSI(out.String())
	if r.failed == 0 || !strings.Contains(got, "programs.overrides.claude") || !strings.Contains(got, `fork_of = "claude"`) {
		t.Errorf("an override of claude (an npm program) must fail check, naming the key and a fork pack; "+
			"failed=%d:\n%s", r.failed, got)
	}

	packsFixture(t, `{"packs": ["claude"], "programs": {"overrides": {"nosuchbin": {"path": "`+dir+`"}}}}`)
	out.Reset()
	r = newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 {
		t.Errorf("an override naming no selected program must not fail check:\n%s", out.String())
	}
}
