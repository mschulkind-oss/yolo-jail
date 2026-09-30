package run

// forkpins_test.go pins the launch's half of the fork PIN (docs/design/forked-programs-as-packs.md
// FP-D7, OQ-FP6): a launch READS the fork lock and names the revision each fork is pinned to, or why
// it has none; it never resolves a fork's ref, writes the lock or fetches the fork's source.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const forkPinSource = "git+https://example.invalid/tool-fork?ref=main"

// forkLaunchHome selects a base pack and a fork of it, and returns the home.
func forkLaunchHome(t *testing.T, source string) string {
	t.Helper()
	home := packHome(t)
	packs := t.TempDir()
	write := func(name, manifest string) {
		if err := os.MkdirAll(filepath.Join(packs, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packs, name, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("basepack", `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
	write("forkpack", `{"contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"basepack",`+
		`"source":"`+source+`","build":"make install","produces":[".local/bin/tool"]}]}`)
	writeUserPacks(t, home, `[{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"}]`)
	return home
}

// launchToDispatch runs one macos-user launch to its dispatch and returns what it printed.
func launchToDispatch(t *testing.T) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay,
		macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	return stdout.String() + stderr.String()
}

func TestALaunchNamesTheForksPinnedRevisionAndTouchesNothing(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	lockPath := packsrc.ForkLockPath(paths.UserConfigPath())
	commit := strings.Repeat("c0ffee", 6) + "abcd"
	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: "forkpack/tool", Source: forkPinSource, Ref: "main", Commit: commit})
	if err := l.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	stat, _ := os.Stat(lockPath)

	out := launchToDispatch(t)
	want := "fork forkpack: tool (in place of pack basepack's) is built from " + forkPinSource + " at commit " + commit
	if !strings.Contains(out, want) {
		t.Errorf("the launch does not name the pinned revision:\nwant %q\n%s", want, out)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("the launch rewrote the fork lock (err %v)", err)
	}
	if s, _ := os.Stat(lockPath); !s.ModTime().Equal(stat.ModTime()) {
		t.Error("the launch touched the fork lock")
	}
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "mirrors")); !os.IsNotExist(err) {
		t.Errorf("the launch fetched a mirror (err %v) — a fork's source is resolved only by `yolo pack install`", err)
	}
}

// An unpinned fork, and one whose source changed since its pin, each name the command that pins it.
func TestALaunchNamesAnUnpinnedAndADriftedFork(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	out := launchToDispatch(t)
	if !strings.Contains(out, "tool (in place of pack basepack's) — it has no pin yet — run `yolo pack install`") {
		t.Errorf("an unpinned fork is not named with its remedy:\n%s", out)
	}

	forkLaunchHome(t, forkPinSource)
	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: "forkpack/tool", Source: "git+https://example.invalid/old?ref=main",
		Commit: strings.Repeat("a", 40)})
	if err := l.Save(packsrc.ForkLockPath(paths.UserConfigPath())); err != nil {
		t.Fatal(err)
	}
	out = launchToDispatch(t)
	if !strings.Contains(out, "its source changed since it was pinned (pinned for git+https://example.invalid/old?ref=main)") {
		t.Errorf("a drifted fork is not named:\n%s", out)
	}
}
