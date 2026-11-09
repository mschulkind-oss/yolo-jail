package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A loophole module dir that does not exist (a typo in `from`) is said to not exist. It was
// reported as "outside selected pack ... staged tree", because resolving a missing path fails
// and that failure was read as leaving the tree.
func TestAMissingLoopholeModuleDirIsSaidToNotExist(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "loopholes"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "loopholes", "typo")
	for _, module := range []PackModule{
		{Dir: missing, Pack: "tools", PackRoot: root},
		{Dir: missing, Pack: "tools"},
	} {
		got := captureWarnings(t)
		loadModuleDirs([]PackModule{module}, SourcePack)
		joined := strings.Join(*got, "\n")
		if !strings.Contains(joined, missing+" does not exist") || !strings.Contains(joined, "in pack tools's manifest") {
			t.Errorf("PackRoot %q: the warning does not say the dir is missing:\n%s", module.PackRoot, joined)
		}
		if strings.Contains(joined, "outside selected pack") || strings.Contains(joined, "is not a directory") {
			t.Errorf("PackRoot %q: the warning names the wrong cause:\n%s", module.PackRoot, joined)
		}
	}
}
