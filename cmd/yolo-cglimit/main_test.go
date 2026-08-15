package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cgd"
)

// shortSocketDir returns a scratch dir directly under /tmp.
//
// NOT t.TempDir(): a socket bound under a TMPDIR-rooted path overruns darwin's
// sun_path (104 bytes including the NUL) and that shipped as a CI break once
// (internal/cli/run/brokerrelay_test.go carries the same helper and the same
// note). Nothing here skips on a bind failure either — a skip would turn a
// platform failure into invisible non-coverage.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-cglimit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// fakeDaemon binds an AF_UNIX socket, records the first request line it is
// sent, and answers with reply. It is the cgroup delegate's newline-JSON
// contract and nothing else, which is exactly what the client is being pinned
// against.
func fakeDaemon(t *testing.T, reply string) (socket string, got func() string) {
	t.Helper()
	socket = filepath.Join(shortSocketDir(t), "cgroup-delegate.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("bind %s: %v", socket, err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	recorded := make(chan string, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		recorded <- string(buf[:n])
		_, _ = conn.Write([]byte(reply))
	}()
	return socket, func() string {
		select {
		case s := <-recorded:
			return s
		default:
			return ""
		}
	}
}

// useSocket points the client at socket for the duration of one test.
func useSocket(t *testing.T, socket string) {
	t.Helper()
	prev := cgdSocket
	cgdSocket = socket
	t.Cleanup(func() { cgdSocket = prev })
}

// TestParseArgs pins the retired script's hand-rolled option loop, quirks
// included. The quirks matter: a flag whose value is missing must fall through
// to the unknown-option branch rather than being accepted with an empty value,
// and `--` must stop parsing even when what follows looks like a flag.
func TestParseArgs(t *testing.T) {
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }

	cases := []struct {
		name     string
		args     []string
		wantCode int
		wantDone bool
		want     options
	}{
		{
			name: "all flags then command",
			args: []string{"--cpu", "75", "--memory", "2g", "--pids", "100", "--name", "job", "--", "echo", "hi"},
			want: options{cpuPct: num(75), memory: str("2g"), pids: num(100), name: "job", command: []string{"echo", "hi"}},
		},
		{
			name: "bare command after --",
			args: []string{"--", "make", "-j8"},
			want: options{command: []string{"make", "-j8"}},
		},
		{
			name: "flag-looking words after -- are the command",
			args: []string{"--", "--cpu", "9"},
			want: options{command: []string{"--cpu", "9"}},
		},
		{
			name:     "missing value falls through to unknown option",
			args:     []string{"--cpu"},
			wantCode: 1,
			wantDone: true,
		},
		{
			name:     "unknown option",
			args:     []string{"--bogus", "--", "echo"},
			wantCode: 1,
			wantDone: true,
		},
		{
			name:     "non-integer cpu",
			args:     []string{"--cpu", "lots", "--", "echo"},
			wantCode: 1,
			wantDone: true,
		},
		{
			name:     "non-integer pids",
			args:     []string{"--pids", "many", "--", "echo"},
			wantCode: 1,
			wantDone: true,
		},
		{
			name:     "-h stops",
			args:     []string{"-h"},
			wantDone: true,
		},
		{
			name:     "--help stops",
			args:     []string{"--help"},
			wantDone: true,
		},
		{
			name: "no args at all",
			args: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			got, code, done := parseArgs(tc.args, &out, &errOut)
			if code != tc.wantCode || done != tc.wantDone {
				t.Fatalf("code/done = %d/%v, want %d/%v (stderr %q)", code, done, tc.wantCode, tc.wantDone, errOut.String())
			}
			if tc.wantDone {
				return
			}
			assertIntPtr(t, "cpuPct", got.cpuPct, tc.want.cpuPct)
			assertIntPtr(t, "pids", got.pids, tc.want.pids)
			assertStrPtr(t, "memory", got.memory, tc.want.memory)
			if got.name != tc.want.name {
				t.Errorf("name = %q, want %q", got.name, tc.want.name)
			}
			if strings.Join(got.command, "\x00") != strings.Join(tc.want.command, "\x00") {
				t.Errorf("command = %q, want %q", got.command, tc.want.command)
			}
		})
	}
}

func assertIntPtr(t *testing.T, field string, got, want *int) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", field, got, want)
	case *got != *want:
		t.Errorf("%s = %d, want %d", field, *got, *want)
	}
}

func assertStrPtr(t *testing.T, field string, got, want *string) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", field, got, want)
	case *got != *want:
		t.Errorf("%s = %q, want %q", field, *got, *want)
	}
}

// TestHelpTextIsTheParitySurface: --help must print the usage the retired
// script printed. The integration suite greps this output for "--cpu", so a
// reworded help is a red integration test, not a cosmetic change.
func TestHelpTextIsTheParitySurface(t *testing.T) {
	var out, errOut bytes.Buffer
	code, command := run([]string{"--help"}, &out, &errOut)
	if code != 0 || command != nil {
		t.Fatalf("run(--help) = %d/%v, want 0/nil", code, command)
	}
	for _, want := range []string{"yolo-cglimit", "--cpu", "--memory", "--pids", "--name", "cgroup v2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help text is missing %q:\n%s", want, out.String())
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("help wrote to stderr: %q", errOut.String())
	}
}

// TestNoCommandIsReportedBeforeSocketAvailability pins the ORDER of the two
// preflight checks. The retired script checked for a command first, so a bad
// invocation in a jail with no delegate reports the usage error rather than
// blaming the delegate — and that is the message that tells the user what to fix.
func TestNoCommandIsReportedBeforeSocketAvailability(t *testing.T) {
	useSocket(t, filepath.Join(shortSocketDir(t), "does-not-exist.sock"))
	var out, errOut bytes.Buffer
	code, command := run([]string{"--cpu", "50"}, &out, &errOut)
	if code != 1 || command != nil {
		t.Fatalf("run = %d/%v, want 1/nil", code, command)
	}
	if !strings.Contains(errOut.String(), "no command specified") {
		t.Fatalf("stderr = %q, want the usage error", errOut.String())
	}
	if strings.Contains(errOut.String(), "not available") {
		t.Fatalf("stderr blamed the delegate for a usage error: %q", errOut.String())
	}
}

// TestMissingSocketFailsClosed: no delegate means the command does NOT run.
// Exit 1 with the three-line explanation, and crucially no argv returned — a
// client that ran the command unlimited would silently ignore the limits it was
// asked to enforce.
func TestMissingSocketFailsClosed(t *testing.T) {
	useSocket(t, filepath.Join(shortSocketDir(t), "does-not-exist.sock"))
	var out, errOut bytes.Buffer
	code, command := run([]string{"--cpu", "50", "--", "echo", "hi"}, &out, &errOut)
	if code != 1 || command != nil {
		t.Fatalf("run = %d/%v, want 1/nil — the command must NOT run without limits", code, command)
	}
	for _, want := range []string{
		"cgroup delegation not available",
		"started with the yolo CLI",
		"cgroup delegate daemon automatically",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr is missing %q:\n%s", want, errOut.String())
		}
	}
}

// TestRequestWireShape pins the exact request the daemon receives: the op, the
// explicit name, cpu_pct/pids as JSON NUMBERS and memory as a JSON STRING
// (internal/cgd parses memory with the human-readable "2g" grammar, so a number
// there is a different request), and no key at all for a flag not given.
func TestRequestWireShape(t *testing.T) {
	socket, got := fakeDaemon(t, `{"ok": true}`+"\n")
	useSocket(t, socket)

	var out, errOut bytes.Buffer
	code, command := run([]string{"--cpu", "75", "--memory", "2g", "--pids", "100", "--name", "test-cgd", "--", "echo", "ok"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run = %d, stderr %q", code, errOut.String())
	}
	if strings.Join(command, " ") != "echo ok" {
		t.Fatalf("command = %q, want [echo ok]", command)
	}

	line := got()
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("request is not newline-terminated: %q", line)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		t.Fatalf("request is not JSON: %v (%q)", err, line)
	}
	if req["op"] != "create_and_join" {
		t.Errorf("op = %v, want create_and_join", req["op"])
	}
	if req["name"] != "test-cgd" {
		t.Errorf("name = %v, want test-cgd", req["name"])
	}
	if v, ok := req["cpu_pct"].(float64); !ok || v != 75 {
		t.Errorf("cpu_pct = %#v, want the number 75", req["cpu_pct"])
	}
	if v, ok := req["pids"].(float64); !ok || v != 100 {
		t.Errorf("pids = %#v, want the number 100", req["pids"])
	}
	if v, ok := req["memory"].(string); !ok || v != "2g" {
		t.Errorf("memory = %#v, want the STRING \"2g\"", req["memory"])
	}
}

// TestOmittedFlagsAreAbsentKeys: internal/cgd branches on key PRESENCE
// (Request.present), and it range-checks the values it finds — so sending
// cpu_pct:0 for "no --cpu given" would turn an unlimited run into an error.
func TestOmittedFlagsAreAbsentKeys(t *testing.T) {
	socket, got := fakeDaemon(t, `{"ok": true}`+"\n")
	useSocket(t, socket)

	var out, errOut bytes.Buffer
	if code, _ := run([]string{"--", "echo", "ok"}, &out, &errOut); code != 0 {
		t.Fatalf("run = %d, stderr %q", code, errOut.String())
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(got()), &req); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cpu_pct", "memory", "pids"} {
		if _, present := req[key]; present {
			t.Errorf("key %q is present for a flag that was not given: %#v", key, req)
		}
	}
	// The default name is derived from this process's PID.
	name, _ := req["name"].(string)
	if !strings.HasPrefix(name, "job-") {
		t.Errorf("default name = %q, want a job-<pid> form", name)
	}
}

// TestDaemonErrorIsReportedAndFailsClosed: ok:false means the command does not
// run, and the daemon's own message is what the user sees.
func TestDaemonErrorIsReportedAndFailsClosed(t *testing.T) {
	socket, _ := fakeDaemon(t, `{"ok": false, "error": "Invalid cgroup name: '..'"}`+"\n")
	useSocket(t, socket)

	var out, errOut bytes.Buffer
	code, command := run([]string{"--", "echo", "ok"}, &out, &errOut)
	if code != 1 || command != nil {
		t.Fatalf("run = %d/%v, want 1/nil", code, command)
	}
	if want := "Error: Invalid cgroup name: '..'"; !strings.Contains(errOut.String(), want) {
		t.Fatalf("stderr = %q, want %q", errOut.String(), want)
	}
}

// TestDaemonErrorWithoutAMessage falls back to "unknown error" rather than
// printing an empty "Error: ".
func TestDaemonErrorWithoutAMessage(t *testing.T) {
	socket, _ := fakeDaemon(t, `{"ok": false}`+"\n")
	useSocket(t, socket)

	var out, errOut bytes.Buffer
	if code, _ := run([]string{"--", "echo", "ok"}, &out, &errOut); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if want := "Error: unknown error"; !strings.Contains(errOut.String(), want) {
		t.Fatalf("stderr = %q, want %q", errOut.String(), want)
	}
}

// TestWarningsAreNonFatal: a limit that could not be applied is reported, and
// the command still runs — matching the daemon, which returns ok:true with a
// warnings list when only SOME of the limits stuck.
func TestWarningsAreNonFatal(t *testing.T) {
	socket, _ := fakeDaemon(t, `{"ok": true, "warnings": ["memory.max: permission denied"]}`+"\n")
	useSocket(t, socket)

	var out, errOut bytes.Buffer
	code, command := run([]string{"--memory", "2g", "--", "echo", "ok"}, &out, &errOut)
	if code != 0 || command == nil {
		t.Fatalf("run = %d/%v, want 0 and the command", code, command)
	}
	if want := "Warning: memory.max: permission denied"; !strings.Contains(errOut.String(), want) {
		t.Fatalf("stderr = %q, want %q", errOut.String(), want)
	}
}

// TestUnreachableDaemonFailsClosed: the socket exists but nothing answers on
// it (a dead predecessor's file). Exit 1, no command.
func TestUnreachableDaemonFailsClosed(t *testing.T) {
	dead := filepath.Join(shortSocketDir(t), "cgroup-delegate.sock")
	if err := os.WriteFile(dead, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	useSocket(t, dead)

	var out, errOut bytes.Buffer
	code, command := run([]string{"--", "echo", "ok"}, &out, &errOut)
	if code != 1 || command != nil {
		t.Fatalf("run = %d/%v, want 1/nil", code, command)
	}
	if !strings.Contains(errOut.String(), "failed to contact cgroup daemon") {
		t.Fatalf("stderr = %q, want the contact failure", errOut.String())
	}
}

// TestTruthy pins Python's `if not resp.get("ok")` semantics on a decoded JSON
// value. The daemon only ever sends a bool, but the client must not treat a
// malformed or absent field as success.
func TestTruthy(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{nil, false},
		{false, false},
		{true, true},
		{float64(0), false},
		{float64(1), true},
		{"", false},
		{"yes", true},
		{[]any{}, true},
	}
	for _, tc := range cases {
		if got := truthy(tc.v); got != tc.want {
			t.Errorf("truthy(%#v) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// TestSocketPathIsTheContractPath: the path this client dials must be the one
// the run pipeline binds. They are spelled once, in internal/paths, precisely
// because a drift here silently disabled the delegate in every jail before.
func TestSocketPathIsTheContractPath(t *testing.T) {
	if want := "/run/yolo-services/cgroup-delegate.sock"; cgdSocket != want {
		t.Fatalf("cgdSocket = %q, want %q", cgdSocket, want)
	}
}

// TestTheDaemonParsesThisClientsRequest closes the loop a fake daemon cannot:
// the exact bytes this client writes are fed to internal/cgd's OWN parser,
// which must accept them and read back the operation it dispatches on. A
// hand-rolled fixture would only prove the test agrees with the test.
func TestTheDaemonParsesThisClientsRequest(t *testing.T) {
	socket, got := fakeDaemon(t, `{"ok": true}`+"\n")
	useSocket(t, socket)

	var out, errOut bytes.Buffer
	if code, _ := run([]string{"--cpu", "75", "--memory", "2g", "--pids", "8", "--name", "job", "--", "true"}, &out, &errOut); code != 0 {
		t.Fatalf("run = %d, stderr %q", code, errOut.String())
	}
	line := []byte(got())
	if _, ok := cgd.ParseRequest(line); !ok {
		t.Fatalf("the daemon's parser rejected this client's request: %q", line)
	}
	if op := cgd.RequestOp(line); op != "create_and_join" {
		t.Fatalf("the daemon read op %q, want create_and_join", op)
	}
}
