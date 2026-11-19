package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackExplainScopedRefusesMalformedTupleInsteadOfDroppingIt(t *testing.T) {
	project := testconfig.Workspace(t, "")
	original := "packs=[{pack='local/corpus',skills={dir='one'}},{pack='local/corpus',skills={}}]\n"
	user := testconfig.User(t, original)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	scopedCLIWrite(t, filepath.Join(root, "one/chosen/SKILL.md"), "chosen\n")
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "local/corpus"})
	if rc == 0 || !strings.Contains(errw, "packs[1]") || !strings.Contains(errw, "yolo config set") || strings.Contains(out, "selected:") {
		t.Fatalf("malformed authored tuple silently dropped: %d %s %s", rc, out, errw)
	}
	if after, err := os.ReadFile(user.Path); err != nil || string(after) != original {
		t.Fatalf("refusal changed config: %q %v", after, err)
	}
}
