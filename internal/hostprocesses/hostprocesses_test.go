package hostprocesses

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestLoadSettingsMissingFile: a missing file -> empty visible + DEFAULT fields.
//
// The three unreadable cases (absent, unparseable, not an object) share this answer
// deliberately — see disabled(). An empty allowlist is the fail-closed state, and
// it is the SAME state the feature has always had before anyone configured it.
func TestLoadSettingsMissingFile(t *testing.T) {
	cfg := LoadSettings(filepath.Join(t.TempDir(), "nope.json"))
	if len(cfg.Visible) != 0 {
		t.Errorf("missing-file visible = %v, want empty", cfg.Visible)
	}
	if !reflect.DeepEqual(cfg.Fields, DefaultFields) {
		t.Errorf("missing-file fields = %v, want DEFAULT", cfg.Fields)
	}
}

// TestLoadSettingsEmptyPath covers the daemon started with no --settings at all:
// same fail-closed answer, and reached without touching the filesystem.
func TestLoadSettingsEmptyPath(t *testing.T) {
	if cfg := LoadSettings(""); len(cfg.Visible) != 0 {
		t.Errorf("empty-path visible = %v, want empty", cfg.Visible)
	}
}

// TestLoadSettingsReadsTheFlatFile pins the SHAPE of what yolo writes. The daemon
// reads a flat map of values, NOT a yolo-jail.jsonc — so a file still spelling
// `host_processes.visible` must resolve to the fail-closed empty allowlist rather
// than to whatever it nests. That is the check that makes the retired --config flag's
// refusal necessary rather than merely tidy: a silent flag alias would land exactly
// this input here and report an empty allowlist as a working daemon.
func TestLoadSettingsReadsTheFlatFile(t *testing.T) {
	dir := t.TempDir()
	flat := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(flat,
		[]byte(`{"visible":["sway","waykeeper"],"fields":["pid","comm"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadSettings(flat)
	if !reflect.DeepEqual(cfg.Visible, []string{"sway", "waykeeper"}) {
		t.Errorf("visible = %v", cfg.Visible)
	}
	if !reflect.DeepEqual(cfg.Fields, []string{"pid", "comm"}) {
		t.Errorf("fields = %v", cfg.Fields)
	}

	nested := filepath.Join(dir, "old.json")
	if err := os.WriteFile(nested,
		[]byte(`{"host_processes":{"visible":["sway"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if old := LoadSettings(nested); len(old.Visible) != 0 {
		t.Errorf("a config-shaped file resolved to %v — the daemon reads a FLAT settings "+
			"file, so nesting must yield the fail-closed empty allowlist, never a "+
			"partially-honored config", old.Visible)
	}
}

// TestLoadSettingsEmptyFieldsFallsBackToDefaults pins the one place an empty list is
// not taken literally: an empty `ps -o` column list is a broken invocation, not a
// narrower view.
// `visible` has the opposite rule and empty means OFF, which is why the two keys do
// not share a helper.
func TestLoadSettingsEmptyFieldsFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"visible":["sway"],"fields":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadSettings(p)
	if !reflect.DeepEqual(cfg.Fields, DefaultFields) {
		t.Errorf("empty fields = %v, want DEFAULT", cfg.Fields)
	}
	if len(cfg.Visible) != 1 {
		t.Errorf("visible = %v, want the one declared name", cfg.Visible)
	}
}

// TestLoadSettingsUnreadableFileIsNotADIAGNOSIS pins loadSettings' second return for
// the branch nothing else reaches: a path that EXISTS and cannot be read.
//
// loadSettings splits its failures into "absence, which is not a fault" (ok=true) and
// "present and unparseable, which is" (ok=false), and SelfCheck is the only consumer of
// that bit. But SelfCheck screens with isFile() first, so the read-error branch is
// unreachable from there — flipping `return disabled(), true` to `false` on os.ReadFile's
// error left the whole unit gate green (measured 2026-08-18), which would turn a
// transient read failure into a `yolo check` FAIL claiming the file does not parse.
//
// A DIRECTORY at the path is the failure mode chosen because the suite runs as UID 0: a
// permission-bit fixture would simply be readable and the test would pass vacuously,
// whereas os.ReadFile on a directory returns EISDIR for root too.
func TestLoadSettingsUnreadableFileIsNotADIAGNOSIS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, ok := loadSettings(dir)
	if !ok {
		t.Error("a settings path that could not be READ is reported as a parse fault. " +
			"ok=false is `yolo check`'s FAIL line, and it says the file is present and " +
			"corrupt — a claim a read error does not support")
	}
	if len(cfg.Visible) != 0 {
		t.Errorf("visible = %v — every failure resolves to the fail-closed allowlist, "+
			"whatever it is reported as", cfg.Visible)
	}
}

// TestSelfCheckMissingFileIsNotAFailure guards the one thing that would make `yolo
// check` red on every fresh machine. The settings file is written when a jail LAUNCHES
// this loophole, so before the first launch it is absent — a normal state, not a fault,
// and not one the user can act on.
func TestSelfCheckMissingFileIsNotAFailure(t *testing.T) {
	if rc := SelfCheck(filepath.Join(t.TempDir(), "settings.json")); rc != 0 {
		t.Errorf("SelfCheck on an unwritten settings file = %d, want 0 — yolo writes it at "+
			"launch, so its absence is the state of every machine that has not launched "+
			"a jail yet", rc)
	}
	if rc := SelfCheck(""); rc != 0 {
		t.Errorf("SelfCheck with no path = %d, want 0", rc)
	}
}

// TestSelfCheckUnparseableFileFails is the case that IS a fault, and the only one this
// check can see that nothing else reports: the daemon collapses an unreadable settings
// file to an empty allowlist and keeps running, so yolo-ps shows nothing while
// everything looks healthy.
func TestSelfCheckUnparseableFileFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := SelfCheck(p); rc != 1 {
		t.Errorf("SelfCheck on a corrupt settings file = %d, want 1", rc)
	}
	// A well-formed file with an empty allowlist is NOT a failure: an empty allowlist
	// is a configuration, and it is what the feature defaults to.
	if err := os.WriteFile(p, []byte(`{"visible":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := SelfCheck(p); rc != 0 {
		t.Errorf("SelfCheck on an empty allowlist = %d, want 0", rc)
	}
}

// TestTheDialectIsTheHosts pins the production default: the daemon speaks the ps of
// the OS it was built for, and only darwin's is BSD. A hostOS hardcoded to either
// value would pass every forced-dialect test in blackbox_test.go and break one host.
func TestTheDialectIsTheHosts(t *testing.T) {
	if hostOS != runtime.GOOS {
		t.Fatalf("hostOS = %q, want runtime.GOOS %q", hostOS, runtime.GOOS)
	}
	for goos, want := range map[string]dialect{"darwin": bsdPS, "linux": gnuPS, "freebsd": gnuPS} {
		if got := dialectFor(goos); got != want {
			t.Errorf("dialectFor(%q) = %v, want %v", goos, got, want)
		}
	}
}

// TestBSDFieldsShowTheMatchedName: on darwin `comm` is asked for as `ucomm`, header
// and all, so the column is the name the allowlist matched; nothing else is touched.
func TestBSDFieldsShowTheMatchedName(t *testing.T) {
	got := bsdFields([]string{"pid", "comm", "comm=NAME", "args", "ucomm", "command"})
	want := []string{"pid", "ucomm", "ucomm=NAME", "args", "ucomm", "command"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bsdFields = %v, want %v", got, want)
	}
}

// TestParseBSDSnapshot: the name is the rest of the line (it may hold spaces, and BSD
// ps may pad it), rows sort by pid, and a row whose numbers do not parse is dropped.
func TestParseBSDSnapshot(t *testing.T) {
	out := "  300 bash\n    1 launchd         \n  200 Google Chrome He\nPID UCOMM\n\n  x7 junk\n"
	got := parseBSDSnapshot([]byte(out), false)
	want := []bsdProc{{pid: 1, comm: "launchd"}, {pid: 200, comm: "Google Chrome He"}, {pid: 300, comm: "bash"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("list snapshot = %+v, want %+v", got, want)
	}
	tree := parseBSDSnapshot([]byte("  101   100 waybar\n  102  zz waybar\n    0     0 kernel_task\n"), true)
	wantTree := []bsdProc{{pid: 0, ppid: 0, comm: "kernel_task"}, {pid: 101, ppid: 100, comm: "waybar"}}
	if !reflect.DeepEqual(tree, wantTree) {
		t.Errorf("tree snapshot = %+v, want %+v", tree, wantTree)
	}
}

// TestForestPrefixIsGNUs pins the glyph runs against what procps-ng 4.0.7 drew for
// `ps -eo pid,ppid,comm,args --forest` (measured 2026-10-04), cell for cell.
func TestForestPrefixIsGNUs(t *testing.T) {
	for _, tc := range []struct {
		hasSibling []bool
		want       string
	}{
		{nil, ""},
		{[]bool{true}, " \\_ "},
		{[]bool{false}, " \\_ "},
		{[]bool{true, false}, " |   \\_ "},
		{[]bool{true, false, true}, " |       \\_ "},
		{[]bool{false, false}, "     \\_ "},
	} {
		if got := forestPrefix(tc.hasSibling); got != tc.want {
			t.Errorf("forestPrefix(%v) = %q, want %q", tc.hasSibling, got, tc.want)
		}
	}
}

// TestKeptPidsSurvivesAParentCycle: a racy snapshot can leave two kept processes each
// naming the other as parent, so neither is a root. Both are still drawn, once each.
func TestKeptPidsSurvivesAParentCycle(t *testing.T) {
	procs := []bsdProc{{pid: 5, ppid: 1, comm: "sway"}, {pid: 7, ppid: 8, comm: "a"}, {pid: 8, ppid: 7, comm: "sway"}}
	kept := keptPids(procs, map[string]struct{}{"sway": {}})
	if !kept[5] || !kept[7] || !kept[8] {
		t.Fatalf("kept = %v, want 5, 7 and 8", kept)
	}
	out := renderForest(procs, kept, map[int]string{5: "sway", 7: "a", 8: "sway"})
	for _, pid := range []string{"5", "7", "8"} {
		n := 0
		for _, line := range strings.Split(out, "\n")[1:] {
			if f := strings.Fields(line); len(f) > 0 && f[0] == pid {
				n++
			}
		}
		if n != 1 {
			t.Errorf("pid %s drawn %d times, want once:\n%s", pid, n, out)
		}
	}
}

// TestTimeoutMessagesKeepTheFrozenForm: the production formatter, not a copy of it,
// yields the frozen GNU bytes TestTreeTimeoutStderrGolden spells out, and the same form
// for the BSD snapshot. TestBlackboxTreeDeadlineKeepsTheFrozenMessage shortens the
// deadline to reach the call sites, so the production 15 is asserted here.
func TestTimeoutMessagesKeepTheFrozenForm(t *testing.T) {
	if treeDeadlineSeconds != 15 {
		t.Fatalf("treeDeadlineSeconds = %d, want the frozen 15", treeDeadlineSeconds)
	}
	gnu := (&psTimeoutError{argv: gnuTreeArgv, secs: treeDeadlineSeconds}).Error()
	if want := "Command '['ps', '-eo', 'pid,ppid,comm,args', '--forest']' timed out after 15 seconds"; gnu != want {
		t.Errorf("GNU timeout = %q, want %q", gnu, want)
	}
	bsd := (&psTimeoutError{argv: bsdTreeSnapshotArgv, secs: treeDeadlineSeconds}).Error()
	if want := "Command '['ps', '-ax', '-o', 'pid=,ppid=,ucomm=']' timed out after 15 seconds"; bsd != want {
		t.Errorf("BSD timeout = %q, want %q", bsd, want)
	}
}

// TestSelfCheckAsksTheHostsPS: the self-check passes only when the ps on PATH answers
// the daemon's own first question in this host's dialect WITH THIS PROCESS, and a FAIL
// names the next step. The fakes answer with $PPID, which is this test process.
func TestSelfCheckAsksTheHostsPS(t *testing.T) {
	for _, tc := range []struct {
		name   string
		d      dialect
		script string
		rc     int
		want   string
	}{
		{"bsd answers", bsdPS, `printf '%s hostprocesses.t\n' "$PPID"`, 0, "answers the BSD queries"},
		{"bsd silent", bsdPS, "exit 1\n", 1, "Put /bin ahead of any other ps"},
		{"bsd lists others", bsdPS, "echo '1 launchd'\n", 1, "this process was not in its answer"},
		// List mode reads every snapshot against the name-free pid listing, so the check
		// asks for that listing too.
		{"bsd refuses the pid listing", bsdPS, "case \"$*\" in\n'-ax -o pid=') echo 'ps: no' >&2; exit 1 ;;\n" +
			"*) printf '%s hostprocesses.t\\n' \"$PPID\" ;;\nesac\n", 1, "ps: no"},
		{"gnu answers", gnuPS, `echo "$PPID"`, 0, "answers the GNU procps queries"},
		{"gnu refuses -C", gnuPS, "echo 'ps: illegal argument' >&2\nexit 1\n", 1, "ps: illegal argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.d == gnuPS && runtime.GOOS != "linux" {
				t.Skip("the GNU probe names this process from /proc/self/comm")
			}
			t.Setenv("PATH", fakePS(t, tc.script+"\n")+":"+os.Getenv("PATH"))
			var out strings.Builder
			if rc := selfCheck("", tc.d, &out); rc != tc.rc || !strings.Contains(out.String(), tc.want) {
				t.Errorf("selfCheck = %d\n%s\nwant %d and %q", rc, out.String(), tc.rc, tc.want)
			}
		})
	}
}

// TestMainSelfCheckAsksTheHostsPS pins the PRODUCTION path rather than the callee:
// `--self-check`, which the manifest's doctor_cmd runs, must ask the ps on PATH, so a ps
// that cannot answer is rc 1 on either dialect (GNU's probe fails on -C, BSD's on the
// snapshot). Reducing SelfCheck to the settings check alone kept every other test green.
func TestMainSelfCheckAsksTheHostsPS(t *testing.T) {
	t.Setenv("PATH", fakePS(t, "exit 1\n"))
	if rc, out := mainSelfCheck(t); rc != 1 {
		t.Errorf("--self-check with a ps that answers nothing = %d\n%s\nwant 1: the doctor must "+
			"ask the host's ps, not only read the settings file", rc, out)
	}
}

// mainSelfCheck runs Main --self-check, the doctor_cmd's own path, with stdout captured.
func mainSelfCheck(t *testing.T) (int, string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	prev := os.Stdout
	os.Stdout = f
	rc := Main([]string{"--self-check"})
	os.Stdout = prev
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return rc, string(out)
}

// TestMainSelfCheckAsksGNUOnLinux and TestMainSelfCheckAsksBSDOnDarwin pin WHICH question
// the production self-check asks, which no other test does: each fake ps answers only its
// own dialect's queries, with $PPID (this test process), and refuses the other's. GNU
// procps also answers `ps -ax -o pid=,ucomm=`, so a self-check that always asked the BSD
// question would pass on Linux while never testing the `-C` GNU list mode is built on.
func TestMainSelfCheckAsksGNUOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the GNU probe names this process from /proc/<pid>/comm")
	}
	withHostOS(t, "linux")
	t.Setenv("PATH", fakePS(t, "case \"$*\" in\n  '-o pid= -C '*) echo \"$PPID\" ;;\n  *) exit 1 ;;\nesac\n"))
	if rc, out := mainSelfCheck(t); rc != 0 || !strings.Contains(out, "answers the GNU procps queries") {
		t.Errorf("--self-check on Linux = %d\n%s\nwant 0 and the GNU OK line: the probe must ask -C", rc, out)
	}
}

func TestMainSelfCheckAsksBSDOnDarwin(t *testing.T) {
	withHostOS(t, "darwin")
	t.Setenv("PATH", fakePS(t, "case \"$*\" in\n"+
		"  '-ax -o pid=') echo \"$PPID\" ;;\n"+
		"  '-ax -o pid=,ucomm=') printf '%s hostprocesses.t\\n' \"$PPID\" ;;\n"+
		"  *) exit 1 ;;\nesac\n"))
	if rc, out := mainSelfCheck(t); rc != 0 || !strings.Contains(out, "answers the BSD queries") {
		t.Errorf("--self-check on darwin = %d\n%s\nwant 0 and the BSD OK line: the probe must ask the BSD snapshot", rc, out)
	}
}

// TestParseBSDSnapshotDropsAPidOnTwoRows: BSD ps prints ucomm raw, so a file name holding
// a newline prints a forged row. The kernel holds a pid once, so a pid on two rows means
// one of them is forged, and neither can be told from the other: both go.
func TestParseBSDSnapshotDropsAPidOnTwoRows(t *testing.T) {
	got := parseBSDSnapshot([]byte("    1 launchd\n  500 a\n600 sway       \n  600 secret-holder\n"), false)
	want := []bsdProc{{pid: 1, comm: "launchd"}, {pid: 500, comm: "a"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot with pid 600 twice = %+v, want %+v", got, want)
	}
	tree := parseBSDSnapshot([]byte("    1     0 launchd\n  500     1 a\n1 0 sway\n  600     1 b\n"), true)
	wantTree := []bsdProc{{pid: 500, ppid: 1, comm: "a"}, {pid: 600, ppid: 1, comm: "b"}}
	if !reflect.DeepEqual(tree, wantTree) {
		t.Errorf("tree snapshot with pid 1 twice = %+v, want %+v", tree, wantTree)
	}
}

// TestListNameIsDashCs: GNU -C compares a name's first 15 bytes (procps-ng 4.0.7: `-C
// abcdefghijklmnoZZZ` finds a process whose comm is `abcdefghijklmno`, and `-C
// abcdefghijklmn` does not), and BSD list mode compares listName of both sides.
func TestListNameIsDashCs(t *testing.T) {
	for in, want := range map[string]string{
		"sway":                "sway",
		"chrome-devtool":      "chrome-devtool",
		"chrome-devtools":     "chrome-devtools",
		"chrome-devtools-":    "chrome-devtools",
		"chrome-devtools-mcp": "chrome-devtools",
	} {
		if got := listName(in); got != want {
			t.Errorf("listName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTreeFailedNamesTheNextStepButNotOnTheDeadline: a tree whose ps could not start
// names `yolo check` (the call sites are pinned by the no-ps tests in blackbox_test.go),
// and the deadline keeps its frozen bytes, with nothing appended.
func TestTreeFailedNamesTheNextStepButNotOnTheDeadline(t *testing.T) {
	if got := treeFailed(errors.New("exec: \"ps\": executable file not found in $PATH")); got !=
		"tree mode failed: exec: \"ps\": executable file not found in $PATH\n"+checkHostPS {
		t.Errorf("spawn failure = %q, want the failure and then checkHostPS", got)
	}
	got := treeFailed(&psTimeoutError{argv: gnuTreeArgv, secs: treeDeadlineSeconds})
	if want := "tree mode failed: Command '['ps', '-eo', 'pid,ppid,comm,args', '--forest']' timed out after 15 seconds\n"; got != want {
		t.Errorf("deadline = %q, want the frozen %q", got, want)
	}
}
