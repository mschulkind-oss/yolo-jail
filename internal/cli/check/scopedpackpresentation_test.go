package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// scopedPresentationCheck runs the real check section over one typed local
// import whose pack declares an audience-limited tree and an audience-limited
// manifest-sourced row, and returns what it printed.
func scopedPresentationCheck(t *testing.T) string {
	t.Helper()
	user := testconfig.User(t, "packs=[{pack='local/corpus',skills={only=['marked','remote']}}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	source := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for path, text := range map[string]string{
		"pack.toml":              "[[skills]]\nfrom='skills'\nagents=['claude']\n[[skills]]\nsource='git+https://example.invalid/other//skills/remote?ref=main'\nas='remote'\nagents=['pi']\n",
		"skills/marked/SKILL.md": "marked\n",
		"skills/folder/notes.md": "folder\n",
	} {
		full := filepath.Join(source, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 {
		t.Fatalf("check failed: %s", out.String())
	}
	return out.String()
}

func TestSectionPacksScopedInspectionPrintsRetainedAudience(t *testing.T) {
	out := scopedPresentationCheck(t)
	for _, want := range []string{"row marked", "row remote"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "row marked") && !strings.Contains(line, "audience=claude") {
			t.Fatalf("marked row lacks audience: %s", line)
		}
		if strings.Contains(line, "row remote") && !strings.Contains(line, "audience=pi") {
			t.Fatalf("remote row lacks audience: %s", line)
		}
	}
}
