package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestTheLaunchDisclosesABuildLineByItsDigest is OQ-RO9 at the launch's disclosure block
// (notePackHostAccess): a fork's build line is named by its recipe digest and
// `yolo pack status <key>`, never printed again there, since every act that builds prints it whole
// first. Deleting the disclosure's use of LaunchDisclosureSentence fails here.
func TestTheLaunchDisclosesABuildLineByItsDigest(t *testing.T) {
	const build = "node -e 'require(\"./build\")' && cp -r dist $HOME/.local/lib/tool"
	root := t.TempDir()
	body := `{"contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"basepack",` +
		`"source":"git+https://example.invalid/tool.git?ref=main","build":` + quoteJSONForTest(build) +
		`,"produces":[".local/bin/tool"]}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "forkpack")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.notePackHostAccess([]*packload.Pack{p}, nil, nil)
	got := errBuf.String()
	if !strings.Contains(got, "forkpack:") {
		t.Fatalf("the launch block does not disclose the fork at all:\n%s", got)
	}
	if strings.Contains(got, "require(") {
		t.Errorf("the launch block printed the build line again:\n%s", got)
	}
	for _, want := range []string{packload.BuildLineDigest(build), "yolo pack status forkpack/tool"} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch block lacks %q:\n%s", want, got)
		}
	}
}

// quoteJSONForTest quotes s as a JSON string.
func quoteJSONForTest(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
