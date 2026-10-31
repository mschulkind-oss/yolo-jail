package jailcontent

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// TestMain keeps the package's tests off the running user's config: staging the built-in suite reads
// the user's shipped_skill_prefix, and a test that wants one reads it from a fake HOME through
// useRealShippedSkillPrefix.
func TestMain(m *testing.M) {
	shippedSkillPrefix = func() string { return "" }
	os.Exit(m.Run())
}

// useRealShippedSkillPrefix makes this test read shipped_skill_prefix from the user config, as a
// launch does; the test points HOME at a fake one first.
func useRealShippedSkillPrefix(t *testing.T) {
	t.Helper()
	saved := shippedSkillPrefix
	shippedSkillPrefix = config.ShippedSkillPrefix
	t.Cleanup(func() { shippedSkillPrefix = saved })
}
