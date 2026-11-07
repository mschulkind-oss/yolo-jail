package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const onceSlackUserConfig = `{"packs":["claude"],"mcp_servers":{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}}`

// onceCall is one observed launcher pass or poll of the one-sign-in tier.
type onceCall struct {
	kind  string // "pass" or "serve"
	share mcpauth.Share
}

// recordOnceTier replaces the tier's two daemon halves with recorders, for the test's length.
func recordOnceTier(t *testing.T) (func() []onceCall, <-chan struct{}) {
	t.Helper()
	served := make(chan struct{}, 1)
	var mu sync.Mutex
	var calls []onceCall
	prevPass, prevServe := onceBoundaryPass, onceServe
	t.Cleanup(func() { onceBoundaryPass, onceServe = prevPass, prevServe })
	onceBoundaryPass = func(_ context.Context, _ string, share mcpauth.Share, _ func(string)) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, onceCall{"pass", share})
		return nil
	}
	onceServe = func(_ string, share mcpauth.Share, _ func(string), stop <-chan struct{}) error {
		mu.Lock()
		calls = append(calls, onceCall{"serve", share})
		mu.Unlock()
		served <- struct{}{}
		<-stop
		return nil
	}
	return func() []onceCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]onceCall(nil), calls...)
	}, served
}

func onceKinds(calls []onceCall) string {
	kinds := make([]string, 0, len(calls))
	for _, c := range calls {
		kinds = append(kinds, c.kind)
	}
	return strings.Join(kinds, ",")
}

// assertSlackFrom checks a call carries the one slack server and the machine store at dir.
func assertSlackFrom(t *testing.T, c onceCall, dir string) {
	t.Helper()
	if len(c.share.Once) != 1 || c.share.Once[0].Name != "slack" {
		t.Fatalf("%s carried once = %+v, want slack alone", c.kind, c.share.Once)
	}
	if len(c.share.Sources) != 1 || c.share.Sources[0].Dir != dir {
		t.Fatalf("%s carried sources = %+v, want Claude's store at %s", c.kind, c.share.Sources, dir)
	}
}

func setOnceGOOS(t *testing.T, goos string) {
	t.Helper()
	prev := onceTierGOOS
	t.Cleanup(func() { onceTierGOOS = prev })
	onceTierGOOS = goos
}

// Which backend the launcher keeps a one-sign-in server on itself, and how (ID-19, ID-20).
func TestOnceTierModePerBackend(t *testing.T) {
	setOnceGOOS(t, "linux")
	for rt, want := range map[string]string{"podman": "", "container": onceModeBoundary, "macos-user": onceModeSession, "host": ""} {
		if got := onceTierMode(rt); got != want {
			t.Errorf("on Linux, onceTierMode(%q) = %q, want %q", rt, got, want)
		}
	}
	setOnceGOOS(t, "darwin")
	if got := onceTierMode("podman"); got != onceModeBoundary {
		t.Errorf("on macOS, onceTierMode(podman) = %q, want %q: no broker is admitted there", got, onceModeBoundary)
	}
}

// ID-19: an Apple Container launch takes a sign_in = "once" server's sign-in over before its
// container starts, from the machine-wide Claude store every jail bridges, and says so. Deleting
// runOnceBoundary's call in runContainer fails here.
func TestAppleContainerLaunchRunsTheOneSignInPassAtStart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses an injected Apple Container runtime on Linux")
	}
	calls, _ := recordOnceTier(t)
	home := packHome(t)
	writeUserConfigJSON(t, home, onceSlackUserConfig)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "container", &stdout, &stderr, nil)
	o.IsMacOS, o.IsLinux = true, false
	o.NeverAttach = true
	o.AcceptConfigChanges = true
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.CapturesDir = func() string { return t.TempDir() }
	defaultLookPath := o.LookPath
	o.LookPath = func(name string) (string, bool) {
		if name == "container" {
			return "/usr/bin/container", true
		}
		return defaultLookPath(name)
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "--version" {
			return ExecResult{Ran: true, RC: 0, Stdout: "Apple container CLI version test\n"}
		}
		if len(argv) >= 3 && argv[0] == "container" && argv[1] == "system" && argv[2] == "status" {
			return ExecResult{Ran: true, RC: 0, Stdout: "running\n"}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	previousSpawner := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = previousSpawner })
	defaultKeeperSpawner = func(*Options, string, *os.File, *os.File, *os.File, []*os.File) (func() int, error) {
		return nil, errors.New("fixture stopped before container spawn")
	}
	_ = Run(*o)
	got := calls()
	if len(got) != 1 || got[0].kind != "pass" {
		t.Fatalf("calls = %s, want one start pass\nstderr:\n%s", onceKinds(got), stderr.String())
	}
	assertSlackFrom(t, got[0], filepath.Join(paths.GlobalHome(), ".claude-shared-credentials"))
	if out := stderr.String(); !strings.Contains(out, "No broker runs on Apple Container") || !strings.Contains(out, "yolo audit --service mcp-auth") {
		t.Errorf("the launch does not say who keeps the sign-in:\n%s", out)
	}
}

// ID-20: a macos-user session runs the daemon's poll for its whole length and one last pass
// after it, both on the session's bridged store. Deleting startOnceSession's call, or the
// stopOnce after MacosUserRun, fails here.
//
// The session's store is its shared home's: the yolo account's own home in the account mode,
// which SharedCredentialHome carries to the share (deleting that, the account mode adopts nothing).
func TestMacosUserSessionKeepsTheOneSignInForItsLength(t *testing.T) {
	for name, mode := range map[string]macosuser.Mode{"self": macosuser.ModeSelf, "account": macosuser.ModeAccount} {
		t.Run(name, func(t *testing.T) {
			calls, served := recordOnceTier(t)
			home := packHome(t)
			writeUserConfigJSON(t, home, onceSlackUserConfig)
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
			selectMacosUserMode(o, mode)
			o.AcceptConfigChanges = true
			var during string
			o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int {
				// The poll starts on its own goroutine: wait for it, with a guard that only turns
				// a poll that never starts into a red test.
				select {
				case <-served:
				case <-time.After(30 * time.Second):
				}
				during = onceKinds(calls())
				return 0
			})
			if rc := Run(*o); rc != 0 {
				t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
			}
			if during != "serve" {
				t.Fatalf("while the session ran, calls = %q, want the poll alone\nstderr:\n%s", during, stderr.String())
			}
			got := calls()
			if onceKinds(got) != "serve,pass" {
				t.Fatalf("calls = %s, want the poll then a last pass", onceKinds(got))
			}
			want := filepath.Join(paths.GlobalHome(), ".claude-shared-credentials")
			if mode == macosuser.ModeAccount {
				want = filepath.Join(macosuser.SandboxHome(), ".claude-shared-credentials")
			}
			assertSlackFrom(t, got[0], want)
			assertSlackFrom(t, got[1], want)
			if !strings.Contains(stderr.String(), "every session on this machine") {
				t.Errorf("the launch does not name the session tier:\n%s", stderr.String())
			}
		})
	}
}

// ID-19: the keeper of a jail on a no-broker backend takes the sign-in over once its container
// is gone. Deleting the exit pass in endJail fails here.
func TestKeeperRunsTheOneSignInPassOnceTheJailIsGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	calls, _ := recordOnceTier(t)
	tree := t.TempDir()
	if err := os.CopyFS(filepath.Join(tree, "claude"), os.DirFS(filepath.Join("..", "..", "..", "packs", "claude"))); err != nil {
		t.Fatal(err)
	}
	slack := `{"slack":{"url":"https://mcp.slack.com/mcp","sign_in":"once"}}`
	store := filepath.Join(paths.GlobalHome(), ".claude-shared-credentials")
	plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: t.TempDir(), Cname: "yolo-once-exit", Runtime: "container",
		PackTree: tree, LaunchInputMCP: &frozenLaunchMCP{EffectiveJSON: slack, UserOnlyJSON: slack, Proven: true, SharedCredentialDir: store}}
	k := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil)
	k.o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	k.endJail("test", false)
	got := calls()
	if len(got) != 1 || got[0].kind != "pass" {
		t.Fatalf("calls = %s, want one exit pass", onceKinds(got))
	}
	assertSlackFrom(t, got[0], store)
}
