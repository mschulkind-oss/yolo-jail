package cli

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// TestTheFrontDoorInstallsTheHostKeychainRead: a macos-user launch reads a program's host login
// only through Options.HostKeychainRead, which a hand-built Options leaves nil so no test reaches
// a keychain (docs/design/keychain-from-a-jail.md OQ-KC5). So the front door must install the
// production read, or a real launch would never pass Copilot its login. It fails with the front
// door's assignment removed.
func TestTheFrontDoorInstallsTheHostKeychainRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	captureBoth(t, func() {
		if rc := Main([]string{"yolo", "--", "true"}); rc != 0 {
			t.Errorf("Main rc = %d with the pipeline stubbed", rc)
		}
	})
	if seen.HostKeychainRead == nil ||
		reflect.ValueOf(seen.HostKeychainRead).Pointer() != reflect.ValueOf(run.DefaultHostKeychainRead).Pointer() {
		t.Error("the front door did not install run.DefaultHostKeychainRead")
	}
}
