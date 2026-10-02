package run

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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

// herdrPaneEnv is a herdr pane's environment: the two variables the registration reads, and
// the two handles HR-D2 keeps out of the jail.
var herdrPaneEnv = map[string]string{
	"HERDR_ENV":         "1",
	"HERDR_PANE_ID":     "w1:p2",
	"HERDR_SOCKET_PATH": "/run/user/1000/herdr.sock",
	"HERDR_BIN_PATH":    "/opt/herdr",
}

// herdrOptions is an Options for driving the registration directly: a herdr pane's
// environment, an Exec that records every call and answers each herdr call with res, and the
// slot Run makes for every launch.
func herdrOptions(res ExecResult) (*Options, *[][]string, *bytes.Buffer) {
	var rec [][]string
	var stderr bytes.Buffer
	o := &Options{
		Stderr: &stderr,
		Getenv: func(k string) string { return herdrPaneEnv[k] },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			rec = append(rec, argv)
			return res
		},
		herdr: &herdrPane{},
	}
	return o, &rec, &stderr
}

// herdrID is the identity every herdr call carries for claude in pane w1:p2.
const herdrID = " w1:p2 --source yolo-jail --agent claude"

// herdrRegistered and herdrReleased are the calls a registration and its release make.
var (
	herdrRegistered = []string{
		"herdr pane report-agent" + herdrID + " --state unknown",
		"herdr pane report-metadata" + herdrID + " --applies-to-source yolo-jail --title 🔒 JAIL ws",
	}
	herdrReleased = []string{
		"herdr pane report-metadata" + herdrID + " --clear-title",
		"herdr pane release-agent" + herdrID,
	}
)

// TestALaunchInAHerdrPaneRegistersTheAgent is THE CALL-SITE PIN for the macos-user arm. It
// drives a real Run, so it fails if the registration is deleted from that arm, if Run stops
// making the slot it registers into or stops deferring the release, or if the program match
// stops reading the selected packs.
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

	id := " w1:p2 --source yolo-jail --agent copilot"
	wantLaunch := []string{
		"herdr pane report-agent" + id + " --state unknown",
		"herdr pane report-metadata" + id + " --applies-to-source yolo-jail --title 🔒 JAIL " + filepath.Base(ws),
	}
	if !slices.Equal(herdrAtLaunch, wantLaunch) {
		t.Fatalf("herdr calls before the backend ran = %q, want exactly %q", herdrAtLaunch, wantLaunch)
	}
	got := herdrCalls(rec)
	wantExit := []string{
		"herdr pane report-metadata" + id + " --clear-title",
		"herdr pane release-agent" + id,
	}
	if !slices.Equal(got, append(wantLaunch, wantExit...)) {
		t.Fatalf("herdr calls after Run = %q, want the launch's then exactly %q", got, wantExit)
	}
	if !strings.Contains(stderr.String(), "herdr: pane w1:p2 registered as copilot") {
		t.Errorf("the registration was not disclosed:\n%s", stderr.String())
	}
}

// TestALaunchRefusedBeforeItsSessionRegistersNothing: a launch that ends before its session
// starts, here at the config-change prompt, never told herdr anything. The registration used to
// sit above the dispatch, where a Ctrl-C at that prompt or during the image build ends the
// launcher by Go's default action, with no arm to release it, and herdr then kept reading a
// host shell as the jailed agent under "🔒 JAIL".
func TestALaunchRefusedBeforeItsSessionRegistersNothing(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot"]`)
	ws := t.TempDir()
	// A first launch with a declared workspace config asks for approval, and with no terminal
	// that ask is a refusal.
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"packages": []}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	var rec [][]string
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, &rec)
	herdrEnv(o, map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p2"})
	baseExec := o.Exec
	o.Exec = func(argv []string, dir string, env []string, d time.Duration) ExecResult {
		res := baseExec(argv, dir, env, d)
		if argv[0] == "herdr" {
			return ExecResult{Ran: true}
		}
		return res
	}
	o.Args = []string{"copilot"}
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _ []string, _ []string,
		_, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		t.Error("the backend ran a launch the config prompt refused")
		return 0
	}
	if rc := Run(*o); rc == 0 {
		t.Fatalf("Run() = 0, want the config prompt's refusal\nstdout:\n%s", stdout.String())
	}
	if got := herdrCalls(rec); len(got) != 0 {
		t.Errorf("a launch refused before its session called herdr: %q", got)
	}
}

// TestTheHerdrPaneIsRegisteredOnlyUnderASignalArm is the call-site pin for the container arms,
// which no unit test drives to a session. Each registers only once its signal arm is installed,
// and releases before the arm is disarmed, so no signal can end the launcher by its default
// action while herdr holds the pane: the fresh launch between armLaunchSignals and the keeper's
// relay, and before each disarm; an attach between its arm and its exec, and before its disarm.
// The macos-user arm, which has no signal arm, registers after its config prompt and just before
// the backend. Both teardowns a signal runs release, and Run makes the slot and defers the
// release for every other return.
func TestTheHerdrPaneIsRegisteredOnlyUnderASignalArm(t *testing.T) {
	type call struct {
		name string
		pos  token.Pos
	}
	callsOf := func(fd *ast.FuncDecl) []call {
		var out []call
		ast.Inspect(fd, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				out = append(out, call{skelCallee(c), c.Pos()})
			}
			return true
		})
		return out
	}
	first := func(calls []call, name string) token.Pos {
		for _, c := range calls {
			if c.name == name {
				return c.pos
			}
		}
		return token.NoPos
	}
	ordered := func(fn string, calls []call, names ...string) {
		t.Helper()
		last := token.NoPos
		for _, name := range names {
			p := first(calls, name)
			if p == token.NoPos {
				t.Errorf("%s no longer calls %s", fn, name)
				return
			}
			if p < last {
				t.Errorf("%s calls %s out of order; want %s", fn, name, strings.Join(names, " < "))
				return
			}
			last = p
		}
	}
	// releasedBeforeEachDisarm: every disarm has a release between it and the one before it
	// (or the arm's install), so no exit path lets go of the arm with herdr still holding the pane.
	releasedBeforeEachDisarm := func(fn string, calls []call, armedAt token.Pos) {
		t.Helper()
		from := armedAt
		disarms := 0
		for _, c := range calls {
			if c.name != "disarm" || c.pos < armedAt {
				continue
			}
			disarms++
			released := false
			for _, r := range calls {
				if r.name == "releaseHerdrAgent" && r.pos > from && r.pos < c.pos {
					released = true
				}
			}
			if !released {
				t.Errorf("%s disarms its signal arm with herdr still registered", fn)
			}
			from = c.pos
		}
		if disarms == 0 {
			t.Errorf("%s no longer disarms its arm; this pin is vacuous", fn)
		}
	}

	// releasedAfter: a release between after's first call and before's, so the session's own
	// end lets go of the pane before the launch goes on to wait out its jail's teardown.
	releasedAfter := func(fn string, calls []call, after, before string) {
		t.Helper()
		from, to := first(calls, after), first(calls, before)
		for _, c := range calls {
			if c.name == "releaseHerdrAgent" && c.pos > from && c.pos < to {
				return
			}
		}
		t.Errorf("%s does not release the herdr pane between %s and %s", fn, after, before)
	}

	fresh := callsOf(funcDecl(t, "run.go", "runContainer"))
	ordered("runContainer", fresh, "armLaunchSignals", "registerHerdrAgent", "relay",
		"runArmedSession", "endSession")
	releasedAfter("runContainer", fresh, "runArmedSession", "endSession")
	releasedBeforeEachDisarm("runContainer", fresh, first(fresh, "armLaunchSignals"))

	attach := callsOf(funcDecl(t, "run.go", "attachExisting"))
	ordered("attachExisting", attach, "attachSignalArm", "registerHerdrAgent", "runArmedSession",
		"releaseHerdrAgent", "endSession")
	releasedBeforeEachDisarm("attachExisting", attach, first(attach, "attachSignalArm"))

	run := callsOf(funcDecl(t, "run.go", "Run"))
	ordered("Run", run, "checkConfigChanges", "registerHerdrAgent", "MacosUserRun")
	slot, deferred := false, false
	ast.Inspect(funcDecl(t, "run.go", "Run"), func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if sel, ok := s.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "herdr" {
				slot = true
			}
		case *ast.DeferStmt:
			if skelCallee(s.Call) == "releaseHerdrAgent" {
				deferred = true
			}
		}
		return true
	})
	if !slot || !deferred {
		t.Errorf("Run makes the herdr slot: %v; defers its release: %v; want both", slot, deferred)
	}

	for _, tc := range []struct{ file, fn string }{
		{"keeperspawn.go", "keeperPreReadyTeardown"},
		{"sessionhangup.go", "attachTeardown"},
	} {
		calls := callsOf(funcDecl(t, tc.file, tc.fn))
		ordered(tc.fn, calls, "releaseHerdrAgent", "restoreTerminal")
	}
}

// TestEverySignalTeardownReleasesTheHerdrPaneOnce drives the two teardowns a signal arm runs on
// a registered pane: each clears the label and releases the agent, and the release Run defers,
// which runs after it on every other path, sends nothing more.
func TestEverySignalTeardownReleasesTheHerdrPaneOnce(t *testing.T) {
	claude := []*packload.Pack{officialPack(t, "claude")}
	for _, tc := range []struct {
		name     string
		teardown func(o *Options) func()
	}{
		{"before ready", func(o *Options) func() {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close() })
			exited := make(chan struct{})
			close(exited)
			return o.keeperPreReadyTeardown(&keeperProcess{lifeline: w, exited: exited}, "yolo-ws-1", "podman")
		}},
		{"from ready on, and on an attach", func(o *Options) func() {
			return o.attachTeardown("podman", "yolo-ws-1", "abc12345")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, rec, _ := herdrOptions(ExecResult{Ran: true})
			o.Workspace = "/ws"
			restored := 0
			o.RestoreTerminal = func() { restored++ }
			o.registerHerdrAgent(claude, []string{"claude"})
			tc.teardown(o)()
			want := append(slices.Clone(herdrRegistered), herdrReleased...)
			if got := herdrCalls(*rec); !slices.Equal(got, want) {
				t.Errorf("herdr calls after the teardown = %q, want the registration then the release %q", got, herdrReleased)
			}
			o.releaseHerdrAgent() // Run's defer
			if got := herdrCalls(*rec); len(got) != len(want) {
				t.Errorf("Run's deferred release, after the teardown's, called herdr again: %q", got[len(want):])
			}
			if restored != 1 {
				t.Errorf("the terminal was put back %d times, want 1", restored)
			}
		})
	}
}

// TestASignalBeforeTheRegistrationLeavesNothingToRelease: a teardown that ran before the arm's
// registration closes the slot, so the registration the launch's own goroutine reaches afterwards
// tells herdr nothing that no release would follow.
func TestASignalBeforeTheRegistrationLeavesNothingToRelease(t *testing.T) {
	o, rec, _ := herdrOptions(ExecResult{Ran: true})
	o.releaseHerdrAgent() // the teardown
	o.registerHerdrAgent([]*packload.Pack{officialPack(t, "claude")}, []string{"claude"})
	if got := herdrCalls(*rec); len(got) != 0 {
		t.Errorf("a registration after the teardown called herdr: %q", got)
	}
}

// TestATeardownDuringTheRegistrationWaitsAndReleases: a signal whose teardown starts while the
// registration is still talking to herdr waits for it, and then releases what it reported,
// rather than finding nothing reported yet and leaving the report behind. Run it under -race:
// the two goroutines share the slot.
func TestATeardownDuringTheRegistrationWaitsAndReleases(t *testing.T) {
	var mu sync.Mutex
	var rec [][]string
	inReport, unblock := make(chan struct{}), make(chan struct{})
	o := &Options{
		Stderr:    &bytes.Buffer{},
		Workspace: "/ws",
		Getenv:    func(k string) string { return herdrPaneEnv[k] },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			mu.Lock()
			rec = append(rec, argv)
			mu.Unlock()
			if argv[2] == "report-agent" {
				close(inReport)
				<-unblock
			}
			return ExecResult{Ran: true}
		},
		herdr: &herdrPane{},
	}
	registered := make(chan struct{})
	go func() {
		defer close(registered)
		o.registerHerdrAgent([]*packload.Pack{officialPack(t, "claude")}, []string{"claude"})
	}()
	<-inReport
	released := make(chan struct{})
	go func() {
		defer close(released)
		o.releaseHerdrAgent() // the signal arm's teardown
	}()
	close(unblock)
	<-registered
	<-released
	mu.Lock()
	defer mu.Unlock()
	if got := herdrCalls(rec); !slices.Equal(got, append(slices.Clone(herdrRegistered), herdrReleased...)) {
		t.Errorf("herdr calls = %q, want the registration then its release", got)
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
				herdr: &herdrPane{},
			}
			o.registerHerdrAgent(copilot, tc.argv)
			o.releaseHerdrAgent()
			if len(rec) != 0 || stderr.Len() != 0 {
				t.Errorf("calls %q, output %q; want none", rec, stderr.String())
			}
			if len(o.runtimeClientEnv) != 0 {
				t.Errorf("runtime client env = %q, want none", o.runtimeClientEnv)
			}
		})
	}
}

// TestHerdrRunsTheHerdrOnPath pins the narrower variant of §4.3: argv[0] is `herdr`, looked up
// on PATH, never the ambient HERDR_BIN_PATH.
func TestHerdrRunsTheHerdrOnPath(t *testing.T) {
	o, rec, _ := herdrOptions(ExecResult{Ran: true})
	o.registerHerdrAgent([]*packload.Pack{officialPack(t, "claude")}, []string{"claude"})
	o.releaseHerdrAgent()
	if len(*rec) != 4 {
		t.Fatalf("herdr calls = %q, want a registration and a release", *rec)
	}
	for _, argv := range *rec {
		if argv[0] != "herdr" {
			t.Errorf("ran %q, want the herdr on PATH", argv[0])
		}
	}
}

// TestAFailedHerdrReportNeverBlocksTheLaunch: herdr is an observer, so a failure is one line,
// which says why in words of its own (report-tiers P2: a loss names its cause and its remedy).
// herdr's stderr is shown as text: an unclosed `[` in a clap usage line used to swallow the
// line's closing tag.
func TestAFailedHerdrReportNeverBlocksTheLaunch(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  ExecResult
		why  string
	}{
		{"herdr refused", ExecResult{Ran: true, RC: 1, Stderr: "no such pane\n"}, "(no such pane)"},
		{"herdr is not on PATH", ExecResult{Ran: false}, "(herdr could not be run from PATH)"},
		{"herdr hung", ExecResult{Ran: true, Timeout: true}, "(herdr did not answer within 2s)"},
		{"herdr said nothing", ExecResult{Ran: true, RC: 3}, "(herdr exited 3)"},
		{"a usage error", ExecResult{Ran: true, RC: 2,
			Stderr: "error: unexpected argument '--source' found\n\nUsage: herdr pane report-agent <PANE> [OPTIONS\n"},
			"(error: unexpected argument '--source' found)"},
		{"markup in herdr's words", ExecResult{Ran: true, RC: 2, Stderr: "usage: herdr pane [OPTIONS\n"},
			"(usage: herdr pane [OPTIONS)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, rec, stderr := herdrOptions(tc.res)
			o.registerHerdrAgent([]*packload.Pack{officialPack(t, "claude")}, []string{"claude"})
			o.releaseHerdrAgent()
			if got := herdrCalls(*rec); len(got) != 1 {
				t.Errorf("herdr calls = %q, want the one failed report and no label or release", got)
			}
			// The hint needs no herdr binary, so a failed report keeps it.
			if !slices.Equal(o.runtimeClientEnv, []string{"HERDR_AGENT=claude"}) {
				t.Errorf("runtime client env = %q, want the hint alone", o.runtimeClientEnv)
			}
			// Escaped text carries a word joiner after each `[` it defuses; read past it.
			out := strings.ReplaceAll(stderr.String(), "\u2060", "")
			if !strings.Contains(out, "could not register this pane as claude "+tc.why+";") ||
				!strings.Contains(out, herdrOptOutEnv+"=1 turns this off.") {
				t.Errorf("the failure line should say %s and name the opt-out:\n%s", tc.why, out)
			}
			if strings.Contains(out, "[/") || strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
				t.Errorf("the failure is not one plain line:\n%q", out)
			}
		})
	}
}

// TestAWedgedHerdrCostsTheLaunchItsTimeoutAlone is the call-site pin for herdrTimeout's
// promise. The report runs through the Exec a launch defaults to, against a herdr on PATH that
// forks a child holding its output and then wedges, the shape a wrapper script or a CLI that
// starts a server takes. The kill at the deadline reaches herdr alone, and the call used to
// wait for its child to close the pipes; the report must now give up within herdrTimeout and
// realExec's drain grace, and say herdr did not answer.
func TestAWedgedHerdrCostsTheLaunchItsTimeoutAlone(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pids")
	killRecordedPids(t, pidFile)
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nsleep 60 &\necho $! >> " + shquote.Quote(pidFile) + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stderr bytes.Buffer
	o := &Options{
		Stderr:            &stderr,
		Getenv:            func(k string) string { return herdrPaneEnv[k] },
		PerfLoggingConfig: func() bool { return false },
		Workspace:         dir,
		herdr:             &herdrPane{},
	}
	fillDefaults(o) // the Exec every launch runs herdr through
	packs := []*packload.Pack{officialPack(t, "claude")}

	start := time.Now()
	done := make(chan struct{})
	go func() {
		o.registerHerdrAgent(packs, []string{"claude"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(herdrTimeout + execDrainGrace + 5*time.Second):
		t.Fatalf("the herdr report had not returned after %s: a wedged herdr holds the launch",
			time.Since(start))
	}
	if took, limit := time.Since(start), herdrTimeout+execDrainGrace+time.Second; took > limit {
		t.Errorf("the herdr report took %s, want at most herdrTimeout and the drain grace (%s, with a second's slack)",
			took, herdrTimeout+execDrainGrace)
	}
	if out := stderr.String(); !strings.Contains(out, "(herdr did not answer within 2s)") {
		t.Errorf("the failure line should say herdr did not answer:\n%s", out)
	}
}

// TestTheHerdrHintReachesTheSessionClientAlone pins HR-D3 at the spawn: runtimeClientEnv is on
// the environment of the client runArmedSession starts, and not on this process's own.
func TestTheHerdrHintReachesTheSessionClientAlone(t *testing.T) {
	t.Setenv("HERDR_AGENT", "")
	out := filepath.Join(t.TempDir(), "agent")
	arm := armLaunchSignalsWith(nil, func(int) {})
	o := &Options{runtimeClientEnv: []string{"HERDR_AGENT=claude"}}
	rc, err := runArmedSession([]string{"sh", "-c", `printf %s "$HERDR_AGENT" > "$0"`, out}, arm, o)
	arm.detach()
	arm.disarm()
	if err != nil || rc != 0 {
		t.Fatalf("runArmedSession = %d, %v", rc, err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "claude" {
		t.Errorf("the client saw HERDR_AGENT=%q, want claude", got)
	}
	if v := os.Getenv("HERDR_AGENT"); v != "" {
		t.Errorf("the launcher's own environment gained HERDR_AGENT=%q", v)
	}
}

// TestAHerdrPaneLeavesNoHerdrVariableOnTheContainerArgv is HR-D2's pin for the fresh launch: in a
// herdr pane, with the registration made, the container's argv is the golden one byte for byte,
// and neither it nor the first session's exec names any HERDR_ variable.
func TestAHerdrPaneLeavesNoHerdrVariableOnTheContainerArgv(t *testing.T) {
	ws := "/ws"
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.Getenv = func(k string) string { return herdrPaneEnv[k] }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: argv[0] == "herdr"}
	}
	o.herdr = &herdrPane{}
	packs := claudePackFixture(t)
	o.registerHerdrAgent(packs, []string{"claude"})
	if !slices.Equal(o.runtimeClientEnv, []string{"HERDR_AGENT=claude"}) {
		t.Fatalf("the fixture did not register: runtime client env = %q", o.runtimeClientEnv)
	}

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	got := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        packs,
		agentsPath:   "/agents/yolo-ws-abcd1234",
		homeSkeleton: goldenHomeSkeleton,
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	if want := podmanLinuxGolden(home); !slices.Equal(got, want) {
		t.Errorf("a herdr pane changed the container argv:\ngot:  %q\nwant: %q", got, want)
	}
	exec := o.firstSessionExecCmd("podman", "yolo-ws-abcd1234", "claude", "abc12345")
	for _, a := range append(got, exec...) {
		if strings.Contains(a, "HERDR") {
			t.Errorf("a herdr variable reached the container's argv: %q", a)
		}
	}
}

// TestAnAttachInAHerdrPaneHintsItsExecClientAndNotTheJail drives attachExisting to its exec in a
// herdr pane, against a fake runtime that records its argv and its environment: the exec client
// carries HERDR_AGENT, which only herdr reads from the host's process table, and the exec's argv,
// which is all that reaches the jail, names no HERDR_ variable (HR-D2, HR-D3). The pane is
// registered before the exec and released once it returns.
func TestAnAttachInAHerdrPaneHintsItsExecClientAndNotTheJail(t *testing.T) {
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, zaiSelected(t), hydratedKey(), nil)
	o.Getenv = func(k string) string { return herdrPaneEnv[k] }
	baseExec := o.Exec
	var rec [][]string
	o.Exec = func(argv []string, dir string, env []string, d time.Duration) ExecResult {
		if argv[0] == "herdr" {
			rec = append(rec, argv)
			return ExecResult{Ran: true}
		}
		return baseExec(argv, dir, env, d)
	}
	o.Args = []string{"claude"}
	o.herdr = &herdrPane{}

	bin := t.TempDir()
	dir := t.TempDir()
	argvFile, envFile := filepath.Join(dir, "argv"), filepath.Join(dir, "env")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + shquote.Quote(argvFile) +
		"\nprintf 'HERDR_AGENT=%s\\n' \"$HERDR_AGENT\" > " + shquote.Quote(envFile) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	rc, restarted := o.attachExisting("yolo-ws-abcd1234", "podman", "claude", cfg,
		stagedPacks{root: "/ctx/packs", packs: zaiSelected(t)}, channel, false, nil)
	if rc != 0 || restarted {
		t.Fatalf("rc=%d restarted=%v\n%s", rc, restarted, stderr)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the fake runtime never ran: %v", err)
	}
	if !strings.HasPrefix(string(argv), "exec\n") {
		t.Fatalf("the fake runtime saw %q, want an exec", argv)
	}
	if strings.Contains(string(argv), "HERDR") {
		t.Errorf("a herdr variable reached the exec's argv:\n%s", argv)
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(env) != "HERDR_AGENT=claude\n" {
		t.Errorf("the exec client's environment has no HERDR_AGENT=claude:\n%s", env)
	}
	if got := herdrCalls(rec); len(got) != 4 || !strings.Contains(got[0], "report-agent") ||
		!strings.Contains(got[3], "release-agent") {
		t.Errorf("herdr calls = %q, want the registration and then the release", got)
	}
}
