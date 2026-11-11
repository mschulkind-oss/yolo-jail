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
	if !strings.Contains(step, "`yolo config set allow.cloud-login.profile \"\" --user`") || strings.Contains(step, "allow.cloud-pack") {
		t.Errorf("the empty-profile step must edit only the user-scope setting through the CLI: %s", step)
	}
}
