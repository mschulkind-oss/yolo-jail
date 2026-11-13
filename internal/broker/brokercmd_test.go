package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
)

// fixtureDaemon is the host daemon the command-layer tests act on. It is NOT
// claude-oauth-broker, so a body that names the broker rather than the name it was handed fails
// here rather than passing by coincidence.
const fixtureDaemon = "fixture-daemon"

// verbState is what the stubbed seams of CLIDeps saw.
type verbState struct {
	running    []Instance
	stragglers []int
	terminated []int
	killed     []int
	hostWide   bool // StopHostWide's answer
	hostWideAt []string
	tailArgv   []string
	removed    []string
	// marked is each MarkStopped call, as "<socket> <pids>"; markedLate is set when one came
	// after a Terminate; markErr is MarkStopped's answer.
	marked     []string
	markedLate bool
	markErr    error
}

func newDeps(t *testing.T, st *verbState) (CLIDeps, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logDir := t.TempDir()
	return CLIDeps{
		Out: &buf, Err: &buf,
		Running: func(name string) []Instance {
			var out []Instance
			for _, i := range st.running {
				if name == "" || i.Name == name {
					out = append(out, i)
				}
			}
			return out
		},
		Stragglers: func(string) []int { return st.stragglers },
		MarkStopped: func(socket string, pids []int) error {
			st.marked = append(st.marked, fmt.Sprintf("%s %v", socket, pids))
			st.markedLate = st.markedLate || len(st.terminated) > 0
			return st.markErr
		},
		Terminate: func(pids []int, _ time.Duration) []int {
			st.terminated = append(st.terminated, pids...)
			return st.killed
		},
		StopHostWide: func(name string) bool {
			st.hostWideAt = append(st.hostWideAt, name)
			return st.hostWide
		},
		RemoveStale: func(s string) { st.removed = append(st.removed, s) },
		LogPath:     func(name string) string { return filepath.Join(logDir, name+".log") },
		LogIsFile:   func(p string) bool { info, err := os.Stat(p); return err == nil && info.Mode().IsRegular() },
		RunTail:     func(argv []string) error { st.tailArgv = argv; return nil },
	}, &buf
}

func jailCopy(jail string, pid int, accepting bool) Instance {
	return Instance{Name: fixtureDaemon, Jail: jail, Socket: "/tmp/yolo-front-" + jail + "-" + fixtureDaemon + ".sock",
		PIDs: []int{pid}, Accepting: accepting}
}

// STATUS LISTS EVERY JAIL'S OWN COPY, by its jail, and exits 0 while each accepts.
func TestStatusListsEachJailsCopy(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, true), jailCopy("bbbbbbbb", 9, true)}}
	deps, buf := newDeps(t, st)
	if rc := PrintStatus(deps, fixtureDaemon); rc != 0 {
		t.Errorf("rc = %d, want 0 for copies that all accept\n%s", rc, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"Host daemon: " + fixtureDaemon, "jail aaaaaaaa: pid 7",
		"jail bbbbbbbb: pid 9", "accepting"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

// A COPY THAT DOES NOT ACCEPT fails the status and names the next step: relaunch that jail.
func TestStatusNamesTheRelaunchForACopyThatDoesNotAccept(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, false)}}
	deps, buf := newDeps(t, st)
	if rc := PrintStatus(deps, fixtureDaemon); rc != 1 {
		t.Errorf("rc = %d, want 1 for a copy that does not accept", rc)
	}
	for _, want := range []string{"not accepting", "leave the jail and launch it again",
		"yolo host-daemon logs " + fixtureDaemon, "yolo host-daemon stop " + fixtureDaemon + "` clears"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, buf.String())
		}
	}
}

// NOTHING RUNNING IS AN ANSWER, exit 0, and it says why: no copy runs while no jail uses it.
func TestStatusOfNothingRunningSaysWhy(t *testing.T) {
	deps, buf := newDeps(t, &verbState{})
	if rc := PrintStatus(deps, fixtureDaemon); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(buf.String(), "No "+fixtureDaemon+" is running") ||
		!strings.Contains(buf.String(), "starts a copy of its own") {
		t.Errorf("the empty status does not say why:\n%s", buf.String())
	}
	buf.Reset()
	if rc := PrintStatus(deps, ""); rc != 0 || !strings.Contains(buf.String(), "No host daemon is running") {
		t.Errorf("the bare status of nothing: rc %d\n%s", rc, buf.String())
	}
}

// A HOST-WIDE COPY AN OLDER YOLO LEFT is reported as that, informationally, and only with what
// yolo knows (HD-D31): jails this yolo launches do not use it, only a jail an older yolo launched
// can, and the stop once none is running is the command to copy. It does not claim the copy still
// serves anything, which status cannot see. An accepting one is no failure, so status exits 0.
func TestStatusReportsAnOlderYolosHostWideCopy(t *testing.T) {
	st := &verbState{running: []Instance{{Name: fixtureDaemon, HostWide: true,
		Socket: "/tmp/yolo-" + fixtureDaemon + ".sock", PIDs: []int{3}, Accepting: true}}}
	deps, buf := newDeps(t, st)
	if rc := PrintStatus(deps, fixtureDaemon); rc != 0 {
		t.Errorf("status of an accepting older copy exited %d, want 0", rc)
	}
	for _, want := range []string{"host-wide, left by an older yolo: pid 3",
		"Jails this yolo launches do not use it", "only a jail an older yolo launched can",
		"\n      yolo host-daemon stop --host-wide " + fixtureDaemon + "\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "still serv") || strings.Contains(buf.String(), "nothing needs doing") {
		t.Errorf("status claims the older copy still serves jails, which it cannot know:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "refuses") {
		t.Errorf("status says a launch refuses beside an older yolo's copy, which none does:\n%s", buf.String())
	}
}

// THE JSON IS THE INSTANCES, an empty array for none, and the exit code is the same verdict.
func TestStatusJSONIsTheInstances(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, false)}}
	deps, buf := newDeps(t, st)
	deps.Format = outfmt.JSON
	if rc := PrintStatus(deps, ""); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	var got []Instance
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil || len(got) != 1 || got[0].Jail != "aaaaaaaa" ||
		got[0].Accepting {
		t.Errorf("json = %s (%v)", buf.String(), err)
	}
	empty, buf2 := newDeps(t, &verbState{})
	empty.Format = outfmt.JSON
	PrintStatus(empty, fixtureDaemon)
	if strings.TrimSpace(buf2.String()) != "[]" {
		t.Errorf("json for nothing running = %q, want []", buf2.String())
	}
}

// STOP ENDS EVERY JAIL'S COPY, and the stragglers still finishing after their jail, and names
// what those jails must do next.
func TestStopEndsEveryCopyAndNamesTheRelaunch(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, true), jailCopy("bbbbbbbb", 9, true)},
		stragglers: []int{9, 11}}
	deps, buf := newDeps(t, st)
	if rc := Stop(deps, fixtureDaemon); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !reflect.DeepEqual(st.terminated, []int{7, 9, 11}) {
		t.Errorf("terminated %v, want every copy's pid once, stragglers included", st.terminated)
	}
	for _, want := range []string{"Stopped " + fixtureDaemon + " for jails aaaaaaaa, bbbbbbbb",
		"leave that jail and launch it again"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("stop lacks %q:\n%s", want, buf.String())
		}
	}
	if len(st.hostWideAt) != 0 {
		t.Errorf("stop reached for a host-wide copy that is not running: %v", st.hostWideAt)
	}
}

// A COPY THAT OUTLIVES THE WAIT IS KILLED, and the stop says so.
func TestStopSaysWhichCopiesItHadToKill(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, true)}, killed: []int{7}}
	deps, buf := newDeps(t, st)
	Stop(deps, fixtureDaemon)
	if !strings.Contains(buf.String(), "pid 7 did not exit within") {
		t.Errorf("stop does not say it killed pid 7:\n%s", buf.String())
	}
}

// AN OLDER YOLO'S HOST-WIDE COPY is stopped by its own PID file, and the stop says whose jails lose it.
func TestStopEndsAnOlderYolosHostWideCopy(t *testing.T) {
	st := &verbState{running: []Instance{{Name: fixtureDaemon, HostWide: true, PIDs: []int{3}}}, hostWide: true}
	deps, buf := newDeps(t, st)
	Stop(deps, fixtureDaemon)
	if !reflect.DeepEqual(st.hostWideAt, []string{fixtureDaemon}) || len(st.terminated) != 0 {
		t.Errorf("host-wide stop %v, terminated %v; want the legacy stop alone", st.hostWideAt, st.terminated)
	}
	if !strings.Contains(buf.String(), "Stopped the host-wide "+fixtureDaemon+" an older yolo left") ||
		!strings.Contains(buf.String(), "relaunch them with this yolo") {
		t.Errorf("stop lacks the host-wide lines:\n%s", buf.String())
	}
}

// THE UPGRADE STEP STOPS ONLY THE OLDER YOLO'S COPY. Status, check and the changelog name it for
// a host-wide copy, and a jail some launch of this yolo already started runs its own copy by then:
// a stop that ended those too would break jails that never used the older copy.
func TestStopHostWideLeavesEveryJailsOwnCopy(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("87b254a5", 4242, true),
		{Name: fixtureDaemon, HostWide: true, PIDs: []int{3}, Accepting: true}}, stragglers: []int{77},
		hostWide: true}
	deps, buf := newDeps(t, st)
	if rc := StopHostWide(deps, fixtureDaemon); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if len(st.terminated) != 0 || len(st.removed) != 0 {
		t.Errorf("terminated %v, removed %v: the host-wide stop reached a jail's own copy",
			st.terminated, st.removed)
	}
	if !reflect.DeepEqual(st.hostWideAt, []string{fixtureDaemon}) {
		t.Errorf("host-wide stop %v, want the older yolo's copy stopped", st.hostWideAt)
	}
	out := buf.String()
	if !strings.Contains(out, "Stopped the host-wide "+fixtureDaemon+" an older yolo left") {
		t.Errorf("stop lacks the host-wide line:\n%s", out)
	}
	if strings.Contains(out, "87b254a5") || strings.Contains(out, "fails its") {
		t.Errorf("the host-wide stop speaks of a jail's copy it did not touch:\n%s", out)
	}
}

func TestStopHostWideOfNoneRunningSaysSo(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("87b254a5", 4242, true)}}
	deps, buf := newDeps(t, st)
	if rc := StopHostWide(deps, fixtureDaemon); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if len(st.terminated) != 0 || len(st.hostWideAt) != 0 {
		t.Errorf("terminated %v, host-wide %v, with no host-wide copy running", st.terminated, st.hostWideAt)
	}
	if !strings.Contains(buf.String(), "No host-wide "+fixtureDaemon+" an older yolo left is running") {
		t.Errorf("stop does not say none was running:\n%s", buf.String())
	}
}

// A SOCKET NOTHING SERVES — no process names it, nothing accepts on it — is what a dead daemon or
// a jail that ended without its teardown left, and stop removes it, saying so, so the status line
// it caused clears.
func TestStopRemovesASocketNothingServes(t *testing.T) {
	st := &verbState{running: []Instance{{Name: fixtureDaemon, Jail: "aaaaaaaa", Socket: "/s"}}}
	deps, buf := newDeps(t, st)
	Stop(deps, fixtureDaemon)
	if !reflect.DeepEqual(st.removed, []string{"/s"}) || len(st.terminated) != 0 {
		t.Errorf("removed %v, terminated %v; want the socket removed and nothing signaled",
			st.removed, st.terminated)
	}
	for _, want := range []string{"Removed " + fixtureDaemon + "'s socket for jail aaaaaaaa",
		"leave it and launch it again"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("stop lacks %q:\n%s", want, buf.String())
		}
	}
}

// A SOCKET THAT ACCEPTS WITH NO PROCESS FOUND BEHIND IT (no pgrep on this host) is neither
// stopped, removed, nor "nothing running": the stop says what it found and what to do.
func TestStopOfASocketWithNoProcessSaysWhatItFound(t *testing.T) {
	st := &verbState{running: []Instance{{Name: fixtureDaemon, Jail: "aaaaaaaa", Socket: "/s", Accepting: true}}}
	deps, buf := newDeps(t, st)
	Stop(deps, fixtureDaemon)
	if len(st.terminated) != 0 || len(st.removed) != 0 {
		t.Errorf("terminated %v, removed %v, for a socket something accepts on", st.terminated, st.removed)
	}
	for _, want := range []string{"no process serving it", "next launch of that workspace replaces it",
		"install procps"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("stop lacks %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "is running on this machine") {
		t.Errorf("stop says nothing runs beside a socket status lists:\n%s", buf.String())
	}
}

// STOP MEANS STOP (HD-D28): before it signals, stop marks each jail's copy as stopped on purpose,
// naming the pids it signals, so the jail's supervisor does not read the death as a crash and
// restart it. A socket nothing serves and a straggler get no marker: no supervisor watches either.
func TestStopMarksEachJailsCopyBeforeItSignals(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, true), jailCopy("bbbbbbbb", 9, true),
		{Name: fixtureDaemon, Jail: "cccccccc", Socket: "/dead"}}, stragglers: []int{11}}
	deps, buf := newDeps(t, st)
	Stop(deps, fixtureDaemon)
	want := []string{"/tmp/yolo-front-aaaaaaaa-" + fixtureDaemon + ".sock [7]",
		"/tmp/yolo-front-bbbbbbbb-" + fixtureDaemon + ".sock [9]"}
	if !reflect.DeepEqual(st.marked, want) {
		t.Errorf("marked %v, want %v", st.marked, want)
	}
	if st.markedLate {
		t.Error("a copy was marked after the signal: its supervisor could read the death as a crash first")
	}
	if strings.Contains(buf.String(), "may restart") {
		t.Errorf("stop warns of a restart though every marker was written:\n%s", buf.String())
	}
}

// A MARKER THAT CANNOT BE WRITTEN still stops the copy, and says the jail's supervisor may start
// it again, naming what to do.
func TestStopSaysSoWhenAMarkerCannotBeWritten(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, true)}, markErr: errors.New("denied")}
	deps, buf := newDeps(t, st)
	Stop(deps, fixtureDaemon)
	if !reflect.DeepEqual(st.terminated, []int{7}) {
		t.Errorf("terminated %v, want [7]", st.terminated)
	}
	for _, want := range []string{"could not mark", "denied", "may restart it", "`yolo stop`"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("stop lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestStopOfNothingRunning(t *testing.T) {
	deps, buf := newDeps(t, &verbState{})
	if rc := Stop(deps, fixtureDaemon); rc != 0 || !strings.Contains(buf.String(), "No "+fixtureDaemon+" is running") {
		t.Errorf("rc = %d\n%s", rc, buf.String())
	}
}

// RESTART RESTARTS NOTHING (HD-R1: a jail's daemon restarts with its jail), refuses with exit 1,
// and names both next steps: the relaunch, and stop for the copies running now.
func TestRestartRefusesWithTheRelaunch(t *testing.T) {
	st := &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, true)}}
	deps, buf := newDeps(t, st)
	if rc := Restart(deps, fixtureDaemon); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if len(st.terminated) != 0 || len(st.hostWideAt) != 0 {
		t.Errorf("restart stopped something: terminated %v, host-wide %v", st.terminated, st.hostWideAt)
	}
	for _, want := range []string{"Nothing to restart", "it restarts with its jail",
		"leave that jail and launch it again", "1 copies run now", "yolo host-daemon stop " + fixtureDaemon} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("restart lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestLogsNoFile(t *testing.T) {
	st := &verbState{}
	deps, buf := newDeps(t, st)
	if rc := Logs(deps, fixtureDaemon, 50, false); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if !strings.Contains(buf.String(), "No log file yet for "+fixtureDaemon) || st.tailArgv != nil {
		t.Errorf("logs of no file: %q, tail %v", buf.String(), st.tailArgv)
	}
}

func TestLogsTailsTheSharedLog(t *testing.T) {
	st := &verbState{}
	deps, _ := newDeps(t, st)
	path := deps.LogPath(fixtureDaemon)
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Logs(deps, fixtureDaemon, 100, true)
	if want := []string{"tail", "-n100", "-f", path}; !reflect.DeepEqual(st.tailArgv, want) {
		t.Errorf("tail argv = %v, want %v", st.tailArgv, want)
	}
}

func TestBuildTailArgv(t *testing.T) {
	cases := []struct {
		lines  int
		follow bool
		want   []string
	}{
		{50, false, []string{"tail", "-n50", "/l"}},
		{50, true, []string{"tail", "-n50", "-f", "/l"}},
		{0, false, []string{"tail", "-n0", "/l"}},
	}
	for _, c := range cases {
		if got := BuildTailArgv(c.lines, c.follow, "/l"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("BuildTailArgv(%d,%v) = %v, want %v", c.lines, c.follow, got, c.want)
		}
	}
}

// TestColorMarkupHonorsNoColor: the status report, requested and on a terminal, carries ANSI,
// and stays plain when NO_COLOR asks. The unset run is the control.
func TestColorMarkupHonorsNoColor(t *testing.T) {
	for _, tc := range []struct {
		noColor  string
		wantANSI bool
	}{{"", true}, {"1", false}} {
		t.Setenv("NO_COLOR", tc.noColor)
		deps, buf := newDeps(t, &verbState{running: []Instance{jailCopy("aaaaaaaa", 7, true)}})
		deps.Color = true
		deps.IsTTYStdout = func() bool { return true }
		PrintStatus(deps, fixtureDaemon)
		if got := strings.Contains(buf.String(), "\x1b["); got != tc.wantANSI {
			t.Errorf("NO_COLOR=%q: status carries ANSI = %v, want %v:\n%q",
				tc.noColor, got, tc.wantANSI, buf.String())
		}
		if strings.Contains(buf.String(), "[green]") {
			t.Errorf("raw markup leaked:\n%q", buf.String())
		}
	}
}

// TestStatusColorsItsSocketPathsAdditively pins the Group C polish of
// docs/plans/cli-visual-polish.md: each socket path, and the older yolo's stop command, is cyan
// on a terminal, and the colored report without its escapes is the plain one byte for byte.
func TestStatusColorsItsSocketPathsAdditively(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	older := jailCopy("", 3, true)
	older.HostWide, older.Socket = true, "/tmp/yolo-old-"+fixtureDaemon+".sock"
	running := []Instance{jailCopy("aaaaaaaa", 7, true), older}
	run := func(color bool) string {
		deps, buf := newDeps(t, &verbState{running: running})
		deps.Color = color
		deps.IsTTYStdout = func() bool { return true }
		PrintStatus(deps, fixtureDaemon)
		return buf.String()
	}
	plain, colored := run(false), run(true)
	for _, sock := range []string{running[0].Socket, older.Socket, StopHostWideCommand(fixtureDaemon)} {
		if !strings.Contains(colored, "\x1b[36m"+sock+"\x1b[0m") {
			t.Errorf("%q is not cyan:\n%q", sock, colored)
		}
	}
	if got := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(colored, ""); got != plain {
		t.Errorf("stripped colored status differs from the plain one:\n%q\n%q", got, plain)
	}
}

// TestColorGate exercises the TTY gate on the printer directly: markup renders to ANSI only when
// Color AND IsTTYStdout() are both true; otherwise style tags are stripped, and literal brackets
// like [y/N] survive in both modes.
func TestColorGate(t *testing.T) {
	const msg = "[green]ready[/green] proceed? [y/N]"
	cases := []struct {
		name     string
		color    bool
		tty      func() bool
		wantANSI bool
	}{
		{"color+tty", true, func() bool { return true }, true},
		{"color-no-tty", true, func() bool { return false }, false},
		{"no-color-tty", false, func() bool { return true }, false},
		{"nil-tty-seam", true, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			out := newPrinter(CLIDeps{Out: &buf, Color: c.color, IsTTYStdout: c.tty})
			out.print(msg)
			got := buf.String()
			if hasANSI := strings.Contains(got, "\x1b["); hasANSI != c.wantANSI {
				t.Errorf("ANSI present = %v, want %v:\n%q", hasANSI, c.wantANSI, got)
			}
			if strings.Contains(got, "[green]") || !strings.Contains(got, "[y/N]") {
				t.Errorf("markup handling wrong:\n%q", got)
			}
		})
	}
}
