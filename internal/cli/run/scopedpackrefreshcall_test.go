package run

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestRunRefreshesDirectScopedHandleOnceBeforeStage(t *testing.T) {
	repo := refreshGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "skills/other"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills/other/SKILL.md"), []byte("other\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "--all"}, {"-c", "commit.gpgsign=false", "commit", "-qm", "second raw directory"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, data)
		}
	}
	user := testconfig.User(t, "packs = []\n")
	// A named source's handle has exactly two components; repository root relocation
	// makes two literal raw roots share one handle and one authored lock source.
	body := fmt.Sprintf("packs = [{pack='demo/skills', skills={dir='gpskill'}}, {pack='demo/skills', skills={dir='other'}}]\n[pack_sources.demo]\ngit='file://%s'\nref='main'\n", filepath.ToSlash(repo))
	if err := os.WriteFile(user.Path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	var out, errw bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &out, &errw, nil)
	called := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, root string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		called = true
		packs, err := loadPackTree(root)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		pin := ""
		for _, p := range packs {
			if p.Projection == nil {
				continue
			}
			count++
			if pin == "" {
				pin = p.Projection.Commit
			}
			if p.Projection.Commit != pin {
				t.Fatal("same handle bound different commits")
			}
		}
		if count != 2 {
			t.Fatalf("collapsed scoped stage: %d", count)
		}
		return 0
	}
	if rc := Run(*o); rc != 0 || !called {
		t.Fatalf("refresh callsite missing: rc=%d called=%v %s", rc, called, errw.String())
	}
	if count := strings.Count(errw.String(), "Fetched pack demo/skills:"); count != 1 {
		t.Fatalf("handle refreshed/disclosed %d times: %s", count, errw.String())
	}
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	identified := configmigrate.IdentifiedPacks(entries)
	count := 0
	for _, p := range identified {
		if p.Handle == "demo/skills" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reconciliation repeated one handle: %v", identified)
	}
	lock, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	pin, ok := lock.Get("demo/skills")
	if !ok || strings.Contains(pin.Source, "gpskill") {
		t.Fatalf("projection root replaced base lock source: %+v", pin)
	}
}
