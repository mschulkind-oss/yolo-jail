package run

// forkpins_test.go pins the launch's disclosure of the fork PIN (docs/design/forked-programs-as-packs.md
// OQ-FP6): a launch names the revision each fork is pinned to, or why it has none, and leaves a
// standing pin's lock entry alone. The pin a launch MAKES is forklaunchpin_test.go's (FP-D18).

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

// forkPinSource is a fork source no launch can fetch, and a LOCAL one, so a launch that tries (to pin
// it, or to fetch a standing pin's commit) fails at once instead of reaching the network.
const forkPinSource = "git+file:///nonexistent/yolo-test/tool-fork?ref=main"

// forkLaunchHome selects a base pack and a fork of it, and returns the home. The launch is a HOST
// one (no YOLO_VERSION, no staged tree), which is where a fork is pinned: inside a jail there is no
// pack store and no fork lock to pin into.
func forkLaunchHome(t *testing.T, source string) string {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
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

func TestALaunchNamesTheForksPinnedRevisionAndLeavesItsPinAlone(t *testing.T) {
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
}

// A fork the launch could not pin is named on the fork line with why, and the step after it.
func TestALaunchNamesAForkItCouldNotPin(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	out := launchToDispatch(t)
	if !strings.Contains(out, "tool (in place of pack basepack's) — it has no pin, and pinning it failed") ||
		!strings.Contains(out, "fix what that names and launch again") {
		t.Errorf("a fork the launch could not pin is not named with its next step:\n%s", out)
	}
}
