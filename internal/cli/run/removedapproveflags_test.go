package run

import (
	"strings"
	"testing"

	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// TestNoShippedPackOffersTheRemovedApproveFlags: `yolo approve --add` and `--remember` are
// removed (git-broker.md GB-D28, github-write-scopes.md OQ-GW2), and the CLI refuses both. The
// text the git and github packs ship into every jail, their briefings and the github-broker's
// loophole description, must not tell an agent to relay either to the human.
func TestNoShippedPackOffersTheRemovedApproveFlags(t *testing.T) {
	for _, file := range []string{
		"git/briefing/git.md",
		"github/briefing/gh.md",
		"github/loopholes/github-broker/loophole.toml",
		"github/README.md",
	} {
		b, err := officialpacks.FS.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, removed := range []string{"--add", "--remember"} {
			if strings.Contains(string(b), removed) {
				t.Errorf("%s still offers the removed `yolo approve %s`", file, removed)
			}
		}
	}
}
