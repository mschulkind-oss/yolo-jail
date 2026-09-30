package run

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// herdrCalls filters the recorded execs down to the herdr invocations, joined for reading.
func herdrCalls(rec [][]string) []string {
	var out []string
	for _, argv := range rec {
		if len(argv) > 0 && strings.HasSuffix(argv[0], "herdr") {
			out = append(out, strings.Join(argv, " "))
		}
	}
	return out
}

// herdrEnv layers a herdr pane's environment over a dispatchOptions Getenv.
func herdrEnv(o *Options, extra map[string]string) {
	base := o.Getenv
	o.Getenv = func(k string) string {
		if v, ok := extra[k]; ok {
			return v
		}
		return base(k)
	}
}

// TestALaunchInAHerdrPaneRegistersTheAgent is THE CALL-SITE PIN. It drives a real Run, so it
// fails if the registration is deleted from Run, if the release stops being deferred, or if
// the program match stops reading the selected packs.
func TestALaunchInAHerdrPaneRegistersTheAgent(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	var rec [][]string
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, &rec)
	herdrEnv(o, map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p2"})
	// dispatchOptions answers everything but `<rt> info` with "did not run", which the
	// registration reads as a failed report; this herdr succeeds.
	baseExec := o.Exec
	o.Exec = func(argv []string, dir string, env []string, d time.Duration) ExecResult {
		res := baseExec(argv, dir, env, d)
		if argv[0] == "herdr" {
			return ExecResult{Ran: true}
		}
		return res
	}
	o.Args = []string{"copilot", "chat"}
	var herdrAtLaunch []string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _ []string, _ []string,
		_, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		herdrAtLaunch = herdrCalls(rec)
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}

	wantReport := "herdr pane report-agent w1:p2 --source yolo-jail --agent copilot --state unknown"
	if len(herdrAtLaunch) != 1 || herdrAtLaunch[0] != wantReport {
		t.Fatalf("herdr calls before the backend ran = %q, want exactly [%q]", herdrAtLaunch, wantReport)
	}
	got := herdrCalls(rec)
	wantRelease := "herdr pane release-agent w1:p2 --source yolo-jail --agent copilot"
	if len(got) != 2 || got[1] != wantRelease {
		t.Fatalf("herdr calls after Run = %q, want the report then exactly one %q", got, wantRelease)
	}
	if !strings.Contains(stderr.String(), "herdr: pane w1:p2 registered as copilot") {
		t.Errorf("the registration was not disclosed:\n%s", stderr.String())
	}
}

// TestHerdrRegistrationIsSilentOutsideItsCase covers every launch that must NOT call herdr.
func TestHerdrRegistrationIsSilentOutsideItsCase(t *testing.T) {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	var copilot []*packload.Pack
	for _, p := range packs {
		if p.Name == "copilot" {
			copilot = append(copilot, p)
		}
	}
	pane := map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1"}
	for _, tc := range []struct {
		name   string
		env    map[string]string
		argv   []string
		dryRun bool
	}{
		{"not in a herdr pane", map[string]string{}, []string{"copilot"}, false},
		{"no pane id", map[string]string{"HERDR_ENV": "1"}, []string{"copilot"}, false},
		{"opted out", map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1", herdrOptOutEnv: "1"}, []string{"copilot"}, false},
		{"a shell, not a declared program", pane, []string{"bash", "-l"}, false},
		{"a bare yolo", pane, nil, false},
		{"a dry run", pane, []string{"copilot"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec [][]string
			var stderr bytes.Buffer
			o := &Options{
				DryRun: tc.dryRun,
				Stderr: &stderr,
				Getenv: func(k string) string { return tc.env[k] },
				Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
					rec = append(rec, argv)
					return ExecResult{Ran: true}
				},
			}
			if release := o.registerHerdrAgent(copilot, tc.argv); release != nil {
				t.Errorf("registered, want nothing")
			}
			if len(rec) != 0 || stderr.Len() != 0 {
				t.Errorf("calls %q, output %q; want none", rec, stderr.String())
			}
		})
	}
}

// TestHerdrReleaseRunsOnceFromEitherArm pins the signal-arm half: the release rides the
// terminal restore, runs before it, and a second call (the defer) reports nothing more.
func TestHerdrReleaseRunsOnceFromEitherArm(t *testing.T) {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	var order []string
	o := &Options{
		Stderr: &bytes.Buffer{},
		Getenv: func(k string) string {
			return map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1", "HERDR_BIN_PATH": "/opt/herdr"}[k]
		},
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			order = append(order, argv[0]+" "+argv[2])
			return ExecResult{Ran: true}
		},
		RestoreTerminal: func() { order = append(order, "terminal") },
	}
	release := o.registerHerdrAgent(packs, []string{"claude"})
	if release == nil {
		t.Fatal("claude in a herdr pane was not registered")
	}
	o.chainHerdrRelease(release)
	o.restoreTerminal() // the signal arm
	release()           // Run's defer
	want := "/opt/herdr report-agent,/opt/herdr release-agent,terminal"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// TestAFailedHerdrReportNeverBlocksTheLaunch: herdr is an observer, so a failure is one line.
func TestAFailedHerdrReportNeverBlocksTheLaunch(t *testing.T) {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	var stderr bytes.Buffer
	o := &Options{
		Stderr: &stderr,
		Getenv: func(k string) string { return map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1"}[k] },
		Exec: func([]string, string, []string, time.Duration) ExecResult {
			return ExecResult{Ran: true, RC: 1, Stderr: "no such pane\n"}
		},
	}
	if release := o.registerHerdrAgent(packs, []string{"claude"}); release != nil {
		t.Error("a failed report returned a release")
	}
	if !strings.Contains(stderr.String(), "no such pane") || !strings.Contains(stderr.String(), herdrOptOutEnv) {
		t.Errorf("the failure line should name herdr's reason and the opt-out:\n%s", stderr.String())
	}
}
