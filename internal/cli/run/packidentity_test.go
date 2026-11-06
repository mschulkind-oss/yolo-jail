package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE CALL SITE of the record move (docs/reference/pack-system.md#pack-identity): the refresh every launch and
// `yolo host apply` runs moves a renamed pack's pin to its new handle before anything reads it.
// Delete the ReconcilePackRecords call from refreshConfiguredPacks and this goes red.
func TestTheLaunchRefreshMovesARenamedPacksPin(t *testing.T) {
	home := hostRefreshEnv(t)
	src := filepath.Join(home, "src")
	if err := os.MkdirAll(filepath.Join(src, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "x", "pack.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	userConfig := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	write := func(key string) {
		t.Helper()
		body := "packs = ['" + key + "/x']\n[pack_sources]\n" + key + " = { path = '" + filepath.ToSlash(src) + "' }\n"
		if err := os.MkdirAll(filepath.Dir(userConfig), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(userConfig, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("acme")
	var said []string
	say := func(l string) { said = append(said, l) }
	RefreshConfiguredPacks(say, say)
	lockPath := packsrc.LockPath(userConfig)
	if err := packsrc.WithLock(paths.PacksDir(), lockPath, nil, func(l *packsrc.Lock) (bool, error) {
		l.Set(packsrc.LockEntry{Name: "acme/x", Source: "file://" + filepath.ToSlash(filepath.Join(src, "x"))})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	write("tools")
	RefreshConfiguredPacks(say, say)
	lock, err := packsrc.LoadLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Get("tools/x"); !ok {
		t.Errorf("the renamed pack's pin did not follow it: %+v (said %v)", lock.Packs, said)
	}
	if !strings.Contains(strings.Join(said, "\n"), "pack tools/x was acme/x") {
		t.Errorf("the refresh did not say the records moved: %v", said)
	}
}
