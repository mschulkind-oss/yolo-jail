package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A LAUNCH NEVER RECORDS A LOCAL PACK, so `pack status` must not promise that "the next launch"
// fetches one or rewrites its drifted entry: for a local pack that remedy never arrives, and
// install is what records it. A git pack keeps the launch remedy — the control.
func TestPackStatusGivesALocalPackTheInstallRemedy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	mine, moved := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(`{"packs": [
		{"name": "mine", "source": "file://`+mine+`"},
		{"name": "drifted", "source": "file://`+moved+`"},
		{"name": "remote", "source": "git+https://example.invalid/p.git"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	seed := &packsrc.Lock{Packs: map[string]packsrc.LockEntry{
		"drifted": {Name: "drifted", Source: "file://" + t.TempDir()},
	}}
	if err := seed.Save(packsrc.LockPath(paths.UserConfigPath())); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	packMain([]string{"status"}, &out, &errw, false)
	report := out.String()

	if l := statusLineFor(t, report, "mine"); strings.Contains(l, "next") || !strings.Contains(l, "yolo pack install") {
		t.Errorf("a local pack with no entry must be sent to install, not to a launch:\n  %s", l)
	}
	if l := statusLineFor(t, report, "remote"); !strings.Contains(l, "the next host launch fetches it") {
		t.Errorf("control: a git pack with no entry keeps the launch remedy:\n  %s", l)
	}
	if !strings.Contains(report, "a local pack: run `yolo pack install` to record the config address") ||
		strings.Contains(report, "next host launch fetches the config address") {
		t.Errorf("a drifted local pack must be given the install remedy only:\n%s", report)
	}
}
