package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// freshHomeIn is the one fresh home ws holds, failing the test unless there is exactly one.
func freshHomeIn(t *testing.T, ws string) paths.FreshHome {
	t.Helper()
	homes := paths.WorkspaceFreshHomes(ws)
	if len(homes) != 1 {
		t.Fatalf("fresh homes in %s: %+v, want exactly one", ws, homes)
	}
	return homes[0]
}

// TestFreshHomeLaunchBindsANewEmptyHome is SD-D14's call site, driven through Run on the macos-user
// arm: a --fresh-home launch hands the backend a new, empty home under
// .yolo/setups/<setup>/home.fresh-<stamp>, tells the session that home, discloses it, and leaves the
// setup's usual home and the legacy home where they were, untouched (no legacy move either). Red if
// Run stops calling selectFreshHome: the arm is then handed the usual home.
func TestFreshHomeLaunchBindsANewEmptyHome(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	marker := seedLegacyHome(t, ws)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.FreshHome = true
	var got macosUserCall
	o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int { got = c; return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	fresh := freshHomeIn(t, ws)
	if fresh.Setup != testSetupForRuntime("macos-user") || !paths.IsFreshHomeName(filepath.Base(fresh.Dir)) {
		t.Errorf("fresh home %+v is not the selected setup's", fresh)
	}
	if got.overlay.Sidecar != fresh.Dir {
		t.Errorf("the macos-user arm was handed home %q, want the fresh home %q", got.overlay.Sidecar, fresh.Dir)
	}
	rel, _ := paths.HomeStateRel(ws, fresh.Dir)
	if v, _ := got.packEnv.Get(paths.HomeStateEnv); v != rel {
		t.Errorf("the session's %s = %v, want the fresh home %q", paths.HomeStateEnv, v, rel)
	}
	if _, err := os.Stat(filepath.Join(paths.LegacyHomeState(ws), marker)); err != nil {
		t.Errorf("a fresh-home launch moved or touched the legacy home: %v", err)
	}
	if _, err := os.Lstat(paths.SetupHomeState(ws, fresh.Setup)); !os.IsNotExist(err) {
		t.Errorf("a fresh-home launch made or touched the setup's usual home (err %v)", err)
	}
	if !strings.Contains(stderr.String(), "Fresh home:") || !strings.Contains(stderr.String(), fresh.Dir) ||
		!strings.Contains(stderr.String(), "yolo stores") {
		t.Errorf("the launch does not disclose its fresh home and where it is:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "Moved this project's home") {
		t.Errorf("a fresh-home launch moved the legacy home:\n%s", stderr.String())
	}
}

// TestFreshHomeContainerLaunchBindsTheFreshHome is the same call site on the podman arm: the
// container argv tells the session the fresh home, and every home bind's source is beneath it.
func TestFreshHomeContainerLaunchBindsTheFreshHome(t *testing.T) {
	ws := t.TempDir()
	argv, printed := fakePodmanLaunchIn(t, ws, "", func(o *Options) { o.FreshHome = true })
	if argv == nil {
		t.Fatalf("the launch never reached podman run:\n%s", printed)
	}
	fresh := freshHomeIn(t, ws)
	rel, _ := paths.HomeStateRel(ws, fresh.Dir)
	if v, ok := envValue(argv, paths.HomeStateEnv); !ok || v != rel {
		t.Errorf("the container's %s = %q (%v), want the fresh home %q", paths.HomeStateEnv, v, ok, rel)
	}
	usual := paths.SetupHomeState(ws, fresh.Setup) + string(filepath.Separator)
	beneath := 0
	for _, v := range argvValues(argv, "-v") {
		src, _, _ := strings.Cut(v, ":")
		if strings.HasPrefix(src, usual) {
			t.Errorf("a bind still comes from the usual home: %s", v)
		}
		if strings.HasPrefix(src, fresh.Dir+string(filepath.Separator)) {
			beneath++
		}
	}
	if beneath == 0 {
		t.Errorf("no bind comes from the fresh home %s:\n%s", fresh.Dir, strings.Join(argv, "\n"))
	}
	if !strings.Contains(printed, "Fresh home:") {
		t.Errorf("the container launch does not disclose its fresh home:\n%s", printed)
	}
}

// TestFreshHomeIsRefusedWhileAJailOfTheProjectRuns: a running jail has the home it booted on, and a
// launch beside it would join it there, so --fresh-home is refused naming `yolo stop`, before any
// home is made or the backend is reached. Red if selectFreshHome stops asking o.projectJails.
func TestFreshHomeIsRefusedWhileAJailOfTheProjectRuns(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	held, err := holdLivenessLock(keeperKey(yoloruntime.FromWorkspace(ws), keeperNotchMacosUser))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(held)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.FreshHome = true
	called := false
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { called = true; return 0 })
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want the refusal's 1\nstderr:\n%s", rc, stderr.String())
	}
	if called {
		t.Error("the backend ran despite the refusal")
	}
	if homes := paths.WorkspaceFreshHomes(ws); len(homes) != 0 {
		t.Errorf("a refused launch made a fresh home: %+v", homes)
	}
	if !strings.Contains(stderr.String(), "Refusing --fresh-home") || !strings.Contains(stderr.String(), "`yolo stop`") {
		t.Errorf("the refusal does not name the next step:\n%s", stderr.String())
	}
}

// TestFreshHomeIsRefusedWhenTheRuntimeCannotSay: "could not ask" is not "none runs", so a runtime
// that cannot answer refuses the flag too.
func TestFreshHomeIsRefusedWhenTheRuntimeCannotSay(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.FreshHome = true
	// podman is on PATH and answers the workspace's container probe with a failure.
	answerContainerOf(o, yoloruntime.FromWorkspace(ws), "podman", "", 125)
	called := false
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { called = true; return 0 })
	if rc := Run(*o); rc != 1 || called {
		t.Fatalf("Run() = %d (backend ran: %v), want a refusal\nstderr:\n%s", rc, called, stderr.String())
	}
	if !strings.Contains(stderr.String(), "could not ask") || !strings.Contains(stderr.String(), "`yolo stop`") {
		t.Errorf("the refusal does not say why and what next:\n%s", stderr.String())
	}
}

// freshAttach drives the real attachExisting through to its exec against a running container
// whose launch environment is frozenEnv, and returns its status, what it printed, and the options.
func freshAttach(t *testing.T, mutate func(*Options), frozenEnv string) (int, string, *Options, bool) {
	t.Helper()
	home := packHome(t)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
	selectRuntimeSetup(o, "podman")
	o.Setup = setup.MustBuiltIn("podman-self")
	var out bytes.Buffer
	o.Stdout, o.Stderr = &out, &out
	o.Getenv = func(string) string { return "" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "inspect" {
			return ExecResult{Ran: true, RC: 0, Stdout: frozenEnv}
		}
		return ExecResult{Ran: false}
	}
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shquote.Quote(record) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	mutate(o)
	cfg := newConfig()
	channel := channelFor(t, o, cfg, nil, nil)
	rc, _ := o.attachExisting(attachNoColorCname, "podman", "true", cfg, stagedPacks{}, channel, false, nil)
	_, err := os.Stat(record)
	return rc, out.String(), o, err == nil
}

// TestAnAttachRefusesFreshHome: --fresh-home never joins a running jail, on any attach path; the
// refusal names `yolo stop` and nothing is exec'd.
func TestAnAttachRefusesFreshHome(t *testing.T) {
	rc, printed, _, execd := freshAttach(t, func(o *Options) { o.FreshHome = true }, "")
	if rc != 1 || execd {
		t.Fatalf("attach rc = %d, exec'd %v; want a refusal before the exec\n%s", rc, execd, printed)
	}
	if !strings.Contains(printed, "Refusing --fresh-home") || !strings.Contains(printed, "`yolo stop`") {
		t.Errorf("the refusal does not name the next step:\n%s", printed)
	}
}

// TestAnAttachToAJailOnAFreshHomeSaysSoAndUsesThatHome: a plain launch that joins a jail running on
// a fresh home does not silently switch homes: it says the jail runs on a fresh home, names the way
// back to the usual one, and uses the jail's home for what it delivers.
func TestAnAttachToAJailOnAFreshHomeSaysSoAndUsesThatHome(t *testing.T) {
	const rel = "setups/podman-self/home.fresh-20261108T101500Z"
	rc, printed, o, execd := freshAttach(t, func(*Options) {}, podmanSetupNetworkEnv+paths.HomeStateEnv+"="+rel+"\n")
	if rc != 0 || !execd {
		t.Fatalf("attach rc = %d, exec'd %v\n%s", rc, execd, printed)
	}
	want := filepath.Join(paths.WorkspaceStateDir(o.Workspace), filepath.FromSlash(rel))
	if o.homeDir() != want {
		t.Errorf("the attach delivers into %q, want the jail's fresh home %q", o.homeDir(), want)
	}
	if !strings.Contains(printed, "Fresh home:") || !strings.Contains(printed, want) || !strings.Contains(printed, "`yolo stop`") {
		t.Errorf("the attach does not say the jail runs on a fresh home:\n%s", printed)
	}

	// A jail on the usual home changes nothing and says nothing.
	rc, printed, o, _ = freshAttach(t, func(*Options) {}, podmanSetupNetworkEnv+paths.HomeStateEnv+"=setups/podman-self/home\n")
	if rc != 0 || strings.Contains(printed, "Fresh home:") || paths.IsFreshHomeState(o.Workspace, o.homeDir()) {
		t.Errorf("an attach to a jail on the usual home: rc %d, home %q\n%s", rc, o.homeDir(), printed)
	}
}
