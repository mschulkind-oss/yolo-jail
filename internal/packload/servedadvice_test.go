package packload

import (
	"strings"
	"testing"
)

// A TOML user's `[allow.<name>]` table reaches the loophole of that name
// (loopholes.ConfigBlock), so the advice for a login loophole names the loophole, not its pack:
// with the two names different, `allow.<pack>` is a setting nothing reads.
func TestLoginLoopholeAdviceNamesTheLoopholeTable(t *testing.T) {
	l := LoginLoophole{Pack: "cloud-pack", Loophole: "cloud-login", Setting: "profile"}
	step := l.EmptyProfileStep()
	if !strings.Contains(step, "under [allow.cloud-login]") || strings.Contains(step, "allow.cloud-pack") {
		t.Errorf("the empty-profile step does not name the loophole's table: %s", step)
	}
}
