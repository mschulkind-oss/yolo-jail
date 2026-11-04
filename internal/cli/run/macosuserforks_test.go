package run

// macosuserforks_test.go pins what a macos-user launch says and hands its sandbox for a fork, plain
// or PATCHED, that it has NO darwin build of (docs/design/forked-programs-as-packs.md FP-D3, FP-D25;
// docs/reference/patched-forks.md PF-D97): here, a launch with no build act wired, so the slot
// readies nothing. The launch says so with the ways out (a container backend, or starting without
// it), and the sandbox's own launcher for the program repeats that reason. A launch whose slot does
// build is pinned in macosuserforkslot_test.go.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// macosUserLaunchEnv runs one macos-user launch to its dispatch and returns what it printed and the
// launch environment it handed the backend.
func macosUserLaunchEnv(t *testing.T) (string, *jsonx.OrderedMap) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var env *jsonx.OrderedMap
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	// A Mac set up for this backend, so the arm's preflight admits the fork slot (macosUserPreflight).
	o.MacosUserLaunchProbes = setUpMacProbes
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		env = launchEnv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	return stdout.String() + stderr.String(), env
}

// handedOnMacosUser decodes the fork decisions a macos-user launch handed its sandbox.
func handedOnMacosUser(t *testing.T, env *jsonx.OrderedMap) map[string]entrypoint.ForkDelivery {
	t.Helper()
	if env == nil {
		t.Fatal("the backend was handed no launch environment")
	}
	raw, ok := env.Get(entrypoint.ForkBuildsEnv)
	if !ok {
		t.Fatalf("the macos-user launch handed its sandbox no %s, so its launcher for a forked program blames a "+
			"launch that never meant to build it", entrypoint.ForkBuildsEnv)
	}
	var d map[string]entrypoint.ForkDelivery
	if err := json.Unmarshal([]byte(raw.(string)), &d); err != nil {
		t.Fatalf("%s = %q: %v", entrypoint.ForkBuildsEnv, raw, err)
	}
	return d
}

// A PLAIN FORK ON macos-user: the launch's warning names the backend that runs it, and the sandbox is
// handed, for the program, no build and that same reason — so `tool` typed in the sandbox says why,
// in the launch's words.
func TestAMacosUserLaunchHandsItsSandboxTheForksReason(t *testing.T) {
	forkLaunchHome(t, forkPinSource)
	pinFork(t, strings.Repeat("ef", 20))
	out, env := macosUserLaunchEnv(t)
	for _, w := range []string{"tool is not delivered on macos-user",
		"this launch builds no fork; run it on a container setup with `YOLO_SETUP=apple-container-self yolo` or `YOLO_SETUP=podman-self yolo`",
		"or start without it with `YOLO_ALLOW_MISSING_PROGRAMS=1 yolo`"} {
		if !strings.Contains(out, w) {
			t.Errorf("the macos-user launch lacks %q:\n%s", w, out)
		}
	}
	for _, stale := range []string{"capture store yet", "hand-off H4"} {
		if strings.Contains(out, stale) {
			t.Errorf("the macos-user launch's fork line still says %q:\n%s", stale, out)
		}
	}
	d := handedOnMacosUser(t, env)["tool"]
	if d.Key != "" ||
		!strings.Contains(d.Reason, "tool is not delivered on macos-user: this launch builds no fork") ||
		!strings.Contains(d.Reason, "YOLO_SETUP=apple-container-self yolo") {
		t.Errorf("the sandbox is handed %+v for tool, want no build and the launch's reason", d)
	}
}

// A PATCHED FORK ON macos-user: its line in the fork block names a fresh launch as what builds an
// edited series, which on this backend now builds it for darwin (PF-D97), never only a container
// backend's; and a launch that readies no build of it says so with the ways out, in the launch's
// words and on the wire.
func TestAMacosUserLaunchNamesWhatRunsAPatchedFork(t *testing.T) {
	forkDir := patchedLaunchHome(t)
	f := packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Source: patchedSource, Build: "make install",
		Produces: []string{".local/bin/tool"}, Root: forkDir, Patches: "patches"}
	// A good build of ANOTHER recipe on the record: the user edited the series since.
	if err := patchedPacksStore().WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		r.Good = &packsrc.GoodBuild{Commit: patchedBase, Tag: "v1.0.0", Version: "1.0.0", Series: "old", Recipe: "old",
			Patches: 1, Entry: "k1", At: time.Now().Unix()}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	out, env := macosUserLaunchEnv(t)
	for _, w := range []string{"tool is not delivered on macos-user",
		"this launch builds no fork; run it on a container setup with " +
			"`YOLO_SETUP=apple-container-self yolo` or `YOLO_SETUP=podman-self yolo`, or start without it with " +
			"`YOLO_ALLOW_MISSING_PROGRAMS=1 yolo`",
		"with another series or build recipe — a fresh launch builds the edited one"} {
		if !strings.Contains(out, w) {
			t.Errorf("the macos-user launch lacks %q:\n%s", w, out)
		}
	}
	for _, stale := range []string{"on a container backend builds the edited one", "has no macOS build"} {
		if strings.Contains(out, stale) {
			t.Errorf("the macos-user launch still says %q:\n%s", stale, out)
		}
	}
	if d := handedOnMacosUser(t, env)["tool"]; d.Key != "" || !strings.Contains(d.Reason, "YOLO_ALLOW_MISSING_PROGRAMS=1 yolo") {
		t.Errorf("the sandbox is handed %+v for the patched tool", d)
	}
}
