package journald

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPlanMacosLogResolvesArgumentsPerScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		mode   string
		want   []string
		filter bool
		refuse string // substring of the refusal, "" for none
	}{
		{name: "empty is the last five minutes, user", mode: ModeUser,
			want: []string{"show", "--last", "5m", "--style", "ndjson"}, filter: true},
		{name: "empty is the last five minutes, full", mode: ModeFull,
			want: []string{"show", "--last", "5m"}},
		{name: "a leading flag is a show flag", args: []string{"--last", "1m"}, mode: ModeUser,
			want: []string{"show", "--last", "1m", "--style", "ndjson"}, filter: true},
		{name: "stream with a predicate", mode: ModeUser,
			args: []string{"stream", "--predicate", `process == "x"`, "--level", "debug"},
			want: []string{"stream", "--predicate", `process == "x"`, "--level", "debug", "--style", "ndjson"}, filter: true},
		{name: "ndjson asked for is dropped and forced once", mode: ModeUser,
			args: []string{"show", "--style", "ndjson", "--info"},
			want: []string{"show", "--info", "--style", "ndjson"}, filter: true},
		{name: "full passes everything through", mode: ModeFull,
			args: []string{"collect", "--output", "/tmp/x.logarchive"},
			want: []string{"collect", "--output", "/tmp/x.logarchive"}},
		{name: "an unknown mode is the user scope", mode: "usr",
			args: []string{"config"}, refuse: "`log config` is not available"},
		{name: "collect is refused", mode: ModeUser, args: []string{"collect"},
			refuse: "`log collect` is not available"},
		{name: "erase is refused", mode: ModeUser, args: []string{"erase", "--all"},
			refuse: "`log erase` is not available"},
		{name: "an archive path is refused", mode: ModeUser,
			args: []string{"show", "/Users/someone/x.logarchive"}, refuse: "positional argument"},
		{name: "--archive is refused", mode: ModeUser,
			args: []string{"show", "--archive", "/x.logarchive"}, refuse: "`--archive` is not one of the flags"},
		{name: "another style is refused", mode: ModeUser,
			args: []string{"show", "--style", "compact"}, refuse: "`--style ndjson` only"},
		{name: "a value flag with no value", mode: ModeUser,
			args: []string{"show", "--last"}, refuse: "needs a value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := PlanMacosLog(tc.args, tc.mode)
			if tc.refuse != "" {
				if !strings.Contains(p.ErrText, tc.refuse) || p.ExitCode != 2 || p.Args != nil {
					t.Fatalf("plan %+v, want a refusal containing %q", p, tc.refuse)
				}
				return
			}
			if p.ErrText != "" || !slices.Equal(p.Args, tc.want) || p.Filter != tc.filter {
				t.Fatalf("plan %+v, want args %q filter %v", p, tc.want, tc.filter)
			}
		})
	}
}

// Every user-scope refusal names the next step: the `full` setting, and where it is written.
func TestMacosLogUserRefusalNamesTheFullSetting(t *testing.T) {
	p := PlanMacosLog([]string{"collect"}, ModeUser)
	for _, want := range []string{`"loopholes": {"macos-log": {"settings": {"full": true}}}`,
		"~/.config/yolo-jail/config.jsonc", "user config only"} {
		if !strings.Contains(p.ErrText, want) {
			t.Errorf("refusal %q does not name %q", p.ErrText, want)
		}
	}
}

func TestParseMacosLogRequestValidatesTheHeader(t *testing.T) {
	if p := ParseMacosLogRequest([]byte(`{"args": 5}`), ModeUser); p.ExitCode != 2 ||
		!strings.HasPrefix(p.ErrText, "yolo-log: ") {
		t.Errorf("a non-list args gave %+v", p)
	}
	if p := ParseMacosLogRequest([]byte(`{"args": ["stream"]}`), ModeUser); !p.Filter ||
		!slices.Equal(p.Args, []string{"stream", "--style", "ndjson"}) {
		t.Errorf("stream gave %+v", p)
	}
}

func TestMacosLogKeepAttributesEachEntryToItsOwner(t *testing.T) {
	const sandbox = 401
	owners := map[int]uint32{100: sandbox, 200: 501}
	asked := 0
	// A stream: the only read that may fall back to a live process's owner.
	keep := macosLogKeep(sandbox, true, func(pid int) (uint32, time.Time, bool) {
		asked++
		uid, ok := owners[pid]
		return uid, time.Unix(0, 0), ok
	})
	ts := `"timestamp": "` + ndjsonTime(time.Now()) + `"`
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`{"userID": 401, "processID": 200, "eventMessage": "a"}`, true}, // userID wins
		{`{"userID": 501, "processID": 100, "eventMessage": "b"}`, false},
		{`{"processID": 100, ` + ts + `, "eventMessage": "c"}`, true},
		{`{"processID": 200, ` + ts + `, "eventMessage": "d"}`, false},
		{`{"processID": 300, ` + ts + `, "eventMessage": "gone"}`, false},
		{`{"eventMessage": "no owner"}`, false},
		{`Filtering the log data using "x"`, false},
		{`[1, 2]`, false},
		{`{"userID": "401"}`, false},
		{`{"userID": -1}`, false},
	} {
		if got := keep([]byte(tc.line)); got != tc.want {
			t.Errorf("keep(%s) = %v, want %v", tc.line, got, tc.want)
		}
	}
	before := asked
	keep([]byte(`{"processID": 100, ` + ts + `}`))
	if asked != before {
		t.Errorf("a pid seen within the TTL was asked again (%d → %d)", before, asked)
	}
}

// writeFakeLog writes a fake `log` printing its argv to stderr, a header line that is not
// JSON, then one entry per uid in uids, each tagged by its uid in eventMessage.
func writeFakeLog(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\n" +
		"echo \"ARGS:$*\" >&2\n" +
		"echo 'Filtering the log data using \"composedMessage CONTAINS x\"'\n" +
		"echo '{\"userID\":401,\"processID\":1,\"eventMessage\":\"from-sandbox\"}'\n" +
		"echo '{\"userID\":501,\"processID\":2,\"eventMessage\":\"from-host-user\"}'\n" +
		"echo '{\"userID\":0,\"processID\":3,\"eventMessage\":\"from-root\"}'\n" +
		"exit 3\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// driveMacosLog serves one connection through handleMacosLogConn — the function MacosLogMain
// hands every fronted connection to — and returns stdout, stderr and the exit code.
func driveMacosLog(t *testing.T, request, mode string, uidErr error) (string, string, int) {
	t.Helper()
	server, client := net.Pipe()
	go handleMacosLogConn(server, MacosLogConfig{Bin: macosLogBin, Mode: mode, SandboxUID: 401,
		SandboxUIDErr: uidErr, Owner: func(int) (uint32, time.Time, bool) { return 0, time.Time{}, false }})
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := client.Write([]byte(request + "\n")); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	rc := -999
	for rc == -999 {
		hdr := make([]byte, 5)
		if _, err := readFull(client, hdr); err != nil {
			t.Fatalf("stream ended before the exit frame: %v (stdout %q stderr %q)", err, out.String(), errb.String())
		}
		n := int(hdr[1])<<24 | int(hdr[2])<<16 | int(hdr[3])<<8 | int(hdr[4])
		payload := make([]byte, n)
		if _, err := readFull(client, payload); err != nil {
			t.Fatal(err)
		}
		switch hdr[0] {
		case FrameStdout:
			out.Write(payload)
		case FrameStderr:
			errb.Write(payload)
		case FrameExit:
			rc = int(int32(uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])))
		}
	}
	_ = client.Close()
	return out.String(), errb.String(), rc
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func TestMacosLogBridgeUserScopeSendsOnlyTheSandboxAccountsEntries(t *testing.T) {
	saved := macosLogBin
	t.Cleanup(func() { macosLogBin = saved })
	macosLogBin = writeFakeLog(t)

	out, errText, rc := driveMacosLog(t, `{"args":["--last","1m"]}`, ModeUser, nil)
	if rc != 3 {
		t.Errorf("rc = %d, want the fake log's 3", rc)
	}
	if !strings.Contains(errText, "ARGS:show --last 1m --style ndjson") {
		t.Errorf("the fake log ran with %q, want the user scope's argv", errText)
	}
	if out != `{"userID":401,"processID":1,"eventMessage":"from-sandbox"}`+"\n" {
		t.Errorf("user scope sent %q, want the sandbox account's entry alone", out)
	}
}

func TestMacosLogBridgeFullScopePassesEverythingThrough(t *testing.T) {
	saved := macosLogBin
	t.Cleanup(func() { macosLogBin = saved })
	macosLogBin = writeFakeLog(t)

	out, errText, _ := driveMacosLog(t, `{"args":["show","--style","compact"]}`, ModeFull, errors.New("no account"))
	if !strings.Contains(errText, "ARGS:show --style compact") {
		t.Errorf("the fake log ran with %q, want the client's argv unchanged", errText)
	}
	for _, want := range []string{"Filtering the log data", "from-sandbox", "from-host-user", "from-root"} {
		if !strings.Contains(out, want) {
			t.Errorf("full scope dropped %q: %q", want, out)
		}
	}
}

func TestMacosLogBridgeRefusesBeforeRunningAnything(t *testing.T) {
	saved := macosLogBin
	t.Cleanup(func() { macosLogBin = saved })
	macosLogBin = writeFakeLog(t)

	out, errText, rc := driveMacosLog(t, `{"args":["collect"]}`, ModeUser, nil)
	if rc != 2 || out != "" || strings.Contains(errText, "ARGS:") || !strings.Contains(errText, "full") {
		t.Errorf("collect in the user scope: rc %d stdout %q stderr %q", rc, out, errText)
	}
	_, errText, rc = driveMacosLog(t, `{"args":[]}`, ModeUser, errors.New("id: _yolojail: no such user"))
	if rc != 1 || strings.Contains(errText, "ARGS:") || !strings.Contains(errText, "macos-user") {
		t.Errorf("no sandbox account: rc %d stderr %q", rc, errText)
	}
}

func TestMacosLogBridgeNamesAMissingLog(t *testing.T) {
	saved := macosLogBin
	t.Cleanup(func() { macosLogBin = saved })
	macosLogBin = filepath.Join(t.TempDir(), "no-such-log")
	_, errText, rc := driveMacosLog(t, `{"args":[]}`, ModeFull, nil)
	if rc != 127 || !strings.Contains(errText, "not found on the host") {
		t.Errorf("rc %d stderr %q", rc, errText)
	}
}

func TestMacosLogMainRequiresItsSocket(t *testing.T) {
	got := captureStderr(t, func() {
		if rc := MacosLogMain(nil); rc != 2 {
			t.Errorf("rc = %d, want 2", rc)
		}
	})
	if !strings.Contains(got, "--socket is required") {
		t.Errorf("stderr %q", got)
	}
}

// TestMacosLogKeepReadsAMeasuredEntry pins the user scope against the shape a real Mac printed
// (macos-user CI run 37986991379, `log show --style ndjson` as the bridge runs it): the
// sandbox account's entry carries a numeric `userID`, and `timestamp` is spelled
// "2026-10-09 22:16:08.389816+0000", which the stream fallback must parse.
func TestMacosLogKeepReadsAMeasuredEntry(t *testing.T) {
	const measured = `{"timezoneName":"","messageType":"Default","eventType":"logEvent","source":null,` +
		`"formatString":"%s","userID":600,"activityIdentifier":0,"subsystem":"","category":"",` +
		`"threadID":92890,"processImagePath":"\/usr\/bin\/perl",` +
		`"timestamp":"2026-10-09 22:16:08.389816+0000","processID":4242,"eventMessage":"x"}`
	none := func(int) (uint32, time.Time, bool) { return 0, time.Time{}, false }
	if !macosLogKeep(600, false, none)([]byte(measured)) {
		t.Errorf("the user scope dropped the sandbox account's measured entry:\n%s", measured)
	}
	if macosLogKeep(501, true, none)([]byte(measured)) {
		t.Errorf("the user scope kept another account's measured entry:\n%s", measured)
	}
	got, ok := parseEntryTime("2026-10-09 22:16:08.389816+0000")
	if want := time.Date(2026, 10, 9, 22, 16, 8, 389816000, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("parseEntryTime(measured) = %v, %v; want %v", got, ok, want)
	}
}
