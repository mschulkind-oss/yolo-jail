package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// mockDeps returns Deps wired to no-op probes with recording hooks. Callers
// override fields per test. The default is a clean macOS host with a resolved
// interpreter (so the plan is viable), recording every Run/RunBash/RunWithProxy.
func mockDeps(rec *[]string) Deps {
	return Deps{
		IsMacOS:           func() bool { return true },
		Geteuid:           func() int { return 501 },
		Which:             func(string) bool { return true },
		SandboxUserExists: func() bool { return true },
		SelfExe:           func() string { return "/opt/yolo-jail/dist-go/darwin-arm64/yolo" },
		GitConfig:         func(string) (string, bool) { return "", false },
		Getenv:            func(string) string { return "" },
		HostUser:          func() string { return "matt" },
		Run: func(argv []string) int {
			if rec != nil {
				*rec = append(*rec, "run:"+strings.Join(argv, " "))
			}
			return 0
		},
		RunBash: func(s string) int {
			if rec != nil {
				*rec = append(*rec, "bash:"+s)
			}
			return 0
		},
		RunWithProxy: func(argv []string) int {
			if rec != nil {
				*rec = append(*rec, "proxy:"+strings.Join(argv, " "))
			}
			return 42
		},
		InstallRootFile: func(path, content, mode string) bool {
			if rec != nil {
				*rec = append(*rec, "install:"+path)
			}
			return true
		},
		MaterializeDarwin: func(string, []any) (*Darwin, bool, error) { return nil, true, nil },
		TakenIDs:          func() map[int]struct{} { return map[int]struct{}{} },
		SetRandomPassword: func() bool { return true },
		PathIsDir:         func(string) bool { return true },
		PathExists:        func(string) bool { return true },
	}
}

func newOpts(ws string) Options {
	return Options{
		Workspace: ws,
		Config:    jsonx.NewOrderedMap(),
		Agents:    []string{"claude"},
		AgentArgv: []string{"claude"},
		RepoRoot:  "/opt/yolo-jail",
	}
}

func TestRunMacosUserFailsClosedOffMacOS(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	rc := RunMacosUser(d, newOpts("/tmp/ws"))
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if len(rec) != 0 {
		t.Errorf("must not shell out: %v", rec)
	}
}

func TestRunMacosUserRefusesRoot(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.Geteuid = func() int { return 0 }
	var buf bytes.Buffer
	d.Out = &buf
	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/ws"))
	if rc != 1 || len(rec) != 0 {
		t.Errorf("rc=%d rec=%v (want refuse before any subprocess)", rc, rec)
	}
}

func TestDryRunPrintsAndExecutesNothing(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false } // dry-run works off-macOS
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.DryRun = true
	rc := RunMacosUser(d, opts)
	if rc != 0 {
		t.Errorf("clean plan rc = %d, want 0", rc)
	}
	if len(rec) != 0 {
		t.Errorf("dry-run must not shell out: %v", rec)
	}
	if !strings.Contains(buf.String(), "macos-user run plan") {
		t.Error("dry-run should print the plan")
	}
}

func TestDryRunNonzeroWhenPlanBroken(t *testing.T) {
	d := mockDeps(nil)
	d.IsMacOS = func() bool { return false }
	// An empty SelfExe yields an unstaged bootstrap binary — the B2 plan
	// invariant fires, so the dry-run exits non-zero.
	d.SelfExe = func() string { return "" }
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.DryRun = true
	if rc := RunMacosUser(d, opts); rc != 1 {
		t.Errorf("broken plan rc = %d, want 1", rc)
	}
}

func TestDryRunRejectsHomeWorkspace(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/matt/code/proj")
	opts.DryRun = true
	rc := RunMacosUser(d, opts)
	if rc != 1 || len(rec) != 0 {
		t.Errorf("home workspace rc=%d rec=%v", rc, rec)
	}
}

func TestRunHappyPathLaunches(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj"))
	if rc != 42 {
		t.Errorf("rc = %d, want 42 (proxy exit)", rc)
	}
	// Must install profile + bootstrap, stage entrypoint (3 cmds), run
	// bootstrap, launch under proxy.
	joined := strings.Join(rec, "\n")
	if !strings.Contains(joined, "install:/var/yolo-jail/profile-") {
		t.Error("profile not installed")
	}
	if !strings.Contains(joined, "proxy:sudo --login --set-home") {
		t.Errorf("proxy launch missing:\n%s", joined)
	}
}

func TestRunMaterializeFailureAborts(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return nil, false, errFake("boom")
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("packages", []any{"ripgrep"})
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.Config = cfg
	rc := RunMacosUser(d, opts)
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Error("must not launch when materialize failed")
	}
	if !strings.Contains(buf.String(), "Could not materialize packages natively: boom") {
		t.Errorf("abort message missing:\n%s", buf.String())
	}
}

func TestMacosSetupRefusesRootAndOffMacOS(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 1 || len(rec) != 0 {
		t.Errorf("off-macOS rc=%d rec=%v", rc, rec)
	}

	rec = nil
	d = mockDeps(&rec)
	d.Geteuid = func() int { return 0 }
	d.Out = &buf
	if rc := MacosSetup(d); rc != 1 || len(rec) != 0 {
		t.Errorf("root rc=%d rec=%v", rc, rec)
	}
}

func TestMacosSetupCreatesWhenMissing(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	joined := strings.Join(rec, "\n")
	if !strings.Contains(joined, "run:sudo dscl . -create /Users/_yolojail") {
		t.Errorf("user creation not run:\n%s", joined)
	}
	if !strings.Contains(joined, "run:sudo mkdir -p /Users/Shared/yolo") {
		t.Error("shared root not provisioned")
	}
}

// TestMacosSetupAbortsOnPasswordFailure is the finding-6 fix: a failed
// SetRandomPassword must abort setup loudly (was silently dropped, leaving the
// account potentially password-less).
func TestMacosSetupAbortsOnPasswordFailure(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return false }
	d.SetRandomPassword = func() bool { return false } // password apply fails
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 1 {
		t.Errorf("password failure should abort setup (rc=1), got %d", rc)
	}
	if !strings.Contains(buf.String(), "could not set a random password") {
		t.Errorf("expected a loud password-failure message:\n%s", buf.String())
	}
}

func TestMacosTeardownNoUser(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosTeardown(d); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if len(rec) != 0 {
		t.Errorf("nothing to delete: %v", rec)
	}
}

func TestMacosUnshareRunsStrip(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosUnshare(d, "/Users/Shared/yolo/proj"); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if !strings.Contains(strings.Join(rec, "\n"), "bash:set -euo pipefail\nws='/Users/Shared/yolo/proj'") {
		t.Errorf("strip script not run: %v", rec)
	}
}

func TestMacosFixPermissionsRejectsHome(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosFixPermissions(d, "/Users/matt/proj"); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if len(rec) != 0 {
		t.Errorf("must reject before shelling out: %v", rec)
	}
}

func TestMacosFixPermissionsDefaultRoot(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosFixPermissions(d, ""); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if !strings.Contains(strings.Join(rec, "\n"), "bash:") {
		t.Error("fix script not run")
	}
}

// TestPrinterColorRendersANSI verifies the printer routes through the shared
// richtext renderer: color=true renders known style tags to ANSI escapes,
// color=false strips them to plain text, and a literal bracketed token (not a
// style tag) is preserved verbatim in both modes.
func TestPrinterColorRendersANSI(t *testing.T) {
	const ansiGreen = "\x1b[32m"
	const ansiReset = "\x1b[0m"

	var col bytes.Buffer
	printer{w: &col, color: true}.print("[green]ok[/green] see [y/N] and [path]")
	got := col.String()
	if !strings.Contains(got, ansiGreen) || !strings.Contains(got, ansiReset) {
		t.Errorf("color=true should emit ANSI escapes, got %q", got)
	}
	if !strings.Contains(got, "[y/N]") || !strings.Contains(got, "[path]") {
		t.Errorf("color=true must preserve literal bracketed tokens verbatim, got %q", got)
	}
	if strings.Contains(got, "[green]") || strings.Contains(got, "[/green]") {
		t.Errorf("color=true must consume known style tags, got %q", got)
	}

	var plain bytes.Buffer
	printer{w: &plain, color: false}.print("[green]ok[/green] see [y/N] and [path]")
	gotPlain := plain.String()
	if strings.Contains(gotPlain, "\x1b[") {
		t.Errorf("color=false must not emit ANSI escapes, got %q", gotPlain)
	}
	if strings.Contains(gotPlain, "[green]") || strings.Contains(gotPlain, "[/green]") {
		t.Errorf("color=false must strip known style tags, got %q", gotPlain)
	}
	if gotPlain != "ok see [y/N] and [path]\n" {
		t.Errorf("color=false plain text mismatch, got %q", gotPlain)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
