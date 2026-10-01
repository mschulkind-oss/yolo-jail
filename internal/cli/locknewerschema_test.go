package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestANewerLockfileNamesYoloUpdate: a pack lockfile written by a newer yolo is refused
// rather than misread, and the refusal used to end at "upgrade yolo", which leaves the
// reader to work out how this copy was installed. `yolo update` already knows: it
// upgrades a Homebrew or from-source install, prints the download link for a release
// archive, names the binary's own updater for go install, pipx and uv, and in a jail says
// to run it on the host (docs/reference/happy-path-principle.md, rule 7). So the refusal
// names it, as `yolo pack status` shows it, and the command it names must be a command.
func TestANewerLockfileNamesYoloUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lock := packsrc.LockPath(paths.UserConfigPath())
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(`{"schema": 99, "packs": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	if rc := packStatus(&out, &errw, false); rc == 0 {
		t.Fatalf("`yolo pack status` accepted a lockfile from a newer yolo:\n%s", out.String())
	}
	got := errw.String()
	if !strings.Contains(got, "run `yolo update`") {
		t.Errorf("the refusal does not name `yolo update`:\n%s", got)
	}
	if strings.Contains(got, "upgrade yolo") {
		t.Errorf("the refusal still says only \"upgrade yolo\":\n%s", got)
	}
	if _, ok := registry["update"]; !ok {
		t.Error("the refusal names `yolo update`, which is not a command")
	}
}
