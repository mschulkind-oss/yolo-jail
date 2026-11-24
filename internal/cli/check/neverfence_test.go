package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A source never the running broker cannot see is a failed config check with the stop-and-relaunch
// step, not a config the broker silently ignores (design section 8: the governing rule).
func TestConfigCheckFailsOnANeverARunningOlderBrokerIgnores(t *testing.T) {
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	user := "packs = [\"aws\"]\n[allow.aws]\nalways = [\"read\", \"all\"]\nnever = [\"all.put_objects\"]\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(older []string) (int, string) {
		old := brokersIgnoringNeverSeam
		t.Cleanup(func() { brokersIgnoringNeverSeam = old })
		brokersIgnoringNeverSeam = func(source string) []string {
			if source != "aws" {
				t.Errorf("asked about %q", source)
			}
			return older
		}
		var out bytes.Buffer
		opts := baseOptions(t, &out)
		opts.Workspace = canonicalTempDir(t)
		opts.Getenv = func(string) string { return "" }
		rc := ConfigCheck(opts)
		return rc, stripANSI(out.String())
	}
	if rc, got := run([]string{"launch-1"}); rc == 0 || !strings.Contains(got, "allow.aws.never is not seen by the running aws broker") || !strings.Contains(got, "yolo stop") {
		t.Fatalf("rc %d: an older broker's blindness was not reported with its next step:\n%s", rc, got)
	}
	if rc, got := run(nil); strings.Contains(got, "is not seen by the running") {
		t.Fatalf("rc %d: a broker that honors never was reported:\n%s", rc, got)
	}
}
