package journald

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The grant tests drive each bridge through its production entry point (journalMain,
// macosLogMain: the flag parse, the --settings read, the fronted socket) behind yolo's real
// front, so they fail if the entry point stops handing the grant file to the handler, not
// only if the handler's check goes (design §11, TestEachShippedDaemonReadsOnlyItsGrantFile).

// isolateFromConfig points HOME and XDG_CONFIG_HOME at empty dirs: whatever the daemon
// decides, it decides from the grant file, because there is no config to read.
func isolateFromConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// writeGrantFile writes the grant file the launch would: settings plus the reserved block.
func writeGrantFile(t *testing.T, path, pack string, settings string, kinds map[string]loopholegrant.Standing) {
	t.Helper()
	b := loopholegrant.Block{
		Pack: pack, Workspace: "/home/u/proj", Container: "yolo-proj", LaunchID: "l1",
		PID: os.Getpid(), Kinds: map[string]loopholegrant.Kind{},
	}
	for k, s := range kinds {
		b.Kinds[k] = loopholegrant.Kind{Standing: s}
	}
	data, err := loopholegrant.Merge([]byte(settings), b)
	if err != nil {
		t.Fatal(err)
	}
	writeAtomically(t, path, data)
}

// grantedPolicy is the journal bridge's policy over a grant file that grants mode: ModeUser
// grants `read`, ModeFull `read_all`. Neither bridge has a settings mode since its flip, so a
// test that drives a handler for its transport hands it a granted file.
func grantedPolicy(t *testing.T, mode string) Policy {
	t.Helper()
	return LoadPolicy(grantedFile(t, "journal", mode))
}

// grantedFile writes pack's grant file granting mode (grantedPolicy's mapping) and returns it.
func grantedFile(t *testing.T, pack, mode string) string {
	t.Helper()
	kinds := map[string]loopholegrant.Standing{"read": loopholegrant.Granted, "read_all": loopholegrant.NotAllowed}
	if mode == ModeFull {
		kinds["read_all"] = loopholegrant.Granted
	}
	path := filepath.Join(t.TempDir(), pack+".settings.json")
	writeGrantFile(t, path, pack, `{}`, kinds)
	return path
}

// writeAtomically replaces path in one rename, as a live apply does, so a request racing
// the rewrite never reads half a file.
func writeAtomically(t *testing.T, path string, data []byte) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// startFrontedMain runs serve (an entry point bound to its own --socket) under yolo's real
// front and returns the published endpoint.
func startFrontedMain(t *testing.T, dir string, serve func(sock string, stop <-chan struct{}) int) string {
	t.Helper()
	sock := filepath.Join(dir, "upstream.sock")
	endpoint := filepath.Join(dir, "bridge.endpoint")
	daemonStop := make(chan struct{})
	daemonDone := make(chan struct{})
	go func() {
		defer close(daemonDone)
		if rc := serve(sock, daemonStop); rc != 0 {
			t.Errorf("the bridge exited %d", rc)
		}
	}()
	waitForUnixSocket(t, sock)
	frontStop := make(chan struct{})
	frontDone := make(chan struct{})
	go func() {
		defer close(frontDone)
		_ = svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, frontStop)
	}()
	waitForPublishedEndpoint(t, endpoint)
	t.Cleanup(func() {
		close(frontStop)
		<-frontDone
		close(daemonStop)
		<-daemonDone
	})
	return endpoint
}

// ask sends one request through the endpoint and returns stdout, stderr and the exit code.
func ask(t *testing.T, endpoint, request string) (string, string, int) {
	t.Helper()
	conn, err := svcendpoint.DialLocal(endpoint, 5*time.Second)
	if err != nil {
		t.Fatalf("dialing the published endpoint: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := conn.Write([]byte(request + "\n")); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			t.Fatalf("stream ended before the exit frame: %v (stdout %q stderr %q)", err, out.String(), errb.String())
		}
		payload := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatal(err)
		}
		switch hdr[0] {
		case FrameStdout:
			out.Write(payload)
		case FrameStderr:
			errb.Write(payload)
		case FrameExit:
			return out.String(), errb.String(), int(int32(binary.BigEndian.Uint32(payload)))
		}
	}
}

// bridgeDir is a 0700 dir under /tmp: svcendpoint refuses to publish into t.TempDir()'s
// 0755, and an AF_UNIX path must stay short.
func bridgeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-grant-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// writeArgvJournalctl puts a fake journalctl on PATH that prints its argv.
func writeArgvJournalctl(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte("#!/bin/sh\necho \"ARGV:$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func journalEntry(grantFile string) func(string, <-chan struct{}) int {
	return func(sock string, stop <-chan struct{}) int {
		return journalMain([]string{"--socket", sock, "--settings", grantFile},
			func() <-chan struct{} { return stop })
	}
}

func TestJournaldReadsOnlyItsGrantFile(t *testing.T) {
	isolateFromConfig(t)
	dir := bridgeDir(t)
	writeArgvJournalctl(t, dir)
	grantFile := filepath.Join(dir, "journal.settings.json")
	// `full: true` beside a grants block is ignored: the block alone decides (PD-11).
	writeGrantFile(t, grantFile, "journal", `{"full": true}`, map[string]loopholegrant.Standing{
		"read": loopholegrant.Requestable, "read_all": loopholegrant.NotAllowed,
	})
	endpoint := startFrontedMain(t, dir, journalEntry(grantFile))

	out, errText, rc := ask(t, endpoint, `{"args":["-n","1"]}`)
	want := loopholegrant.Refusal{
		Client: "yolo-journalctl", Pack: "journal", Access: "read the host's journal",
		Kind: "read", Dir: "/home/u/proj", Standing: loopholegrant.Requestable,
	}.Text()
	if rc != 64 || errText != want || out != "" {
		t.Fatalf("with read requestable: rc %d stdout %q stderr\n%s\nwant rc 64 and\n%s", rc, out, errText, want)
	}

	// The file is read at each request, so a rewrite (a live apply) is taken at the next one.
	writeGrantFile(t, grantFile, "journal", `{}`, map[string]loopholegrant.Standing{
		"read": loopholegrant.Granted, "read_all": loopholegrant.NotAllowed,
	})
	out, errText, rc = ask(t, endpoint, `{"args":["-n","1"]}`)
	if rc != 0 || out != "ARGV:--user -n 1\n" {
		t.Fatalf("with read granted: rc %d stdout %q stderr %q, want the user journal (--user forced)", rc, out, errText)
	}

	writeGrantFile(t, grantFile, "journal", `{}`, map[string]loopholegrant.Standing{
		"read": loopholegrant.NotAllowed, "read_all": loopholegrant.Granted,
	})
	out, errText, rc = ask(t, endpoint, `{"args":["-n","1"]}`)
	if rc != 0 || out != "ARGV:-n 1\n" {
		t.Fatalf("with read_all granted: rc %d stdout %q stderr %q, want the args unchanged", rc, out, errText)
	}

	// A kind the block does not list is outside the ceiling: not_allowed, naming `yolo allow`.
	writeGrantFile(t, grantFile, "journal", `{}`, map[string]loopholegrant.Standing{})
	_, errText, rc = ask(t, endpoint, `{"args":["-n","1"]}`)
	if rc != 64 || !strings.Contains(errText, "yolo allow journal on_request read") ||
		!strings.Contains(errText, "yolo host-run -C /home/u/proj") {
		t.Fatalf("with no kind listed: rc %d stderr\n%s", rc, errText)
	}

	// A grant file that goes away fails closed; it never falls back to the startup mode.
	if err := os.Remove(grantFile); err != nil {
		t.Fatal(err)
	}
	out, errText, rc = ask(t, endpoint, `{"args":["-n","1"]}`)
	if rc == 0 || out != "" || !strings.Contains(errText, "nothing ran") {
		t.Fatalf("with the grant file gone: rc %d stdout %q stderr %q, want a refusal", rc, out, errText)
	}
}

// PD-12, the journal's flip: a settings file with no `grants` key grants nothing, and neither
// does no settings file. The old `full: true` setting no longer widens anything; both fail
// closed with the not_allowed text and spawn no journalctl.
func TestJournaldWithoutAGrantsKeyRefuses(t *testing.T) {
	isolateFromConfig(t)
	notAllowed := loopholegrant.Refusal{
		Client: "yolo-journalctl", Pack: "journal", Access: "read the host's journal",
		Kind: "read", Standing: loopholegrant.NotAllowed,
	}.Text()

	dir := bridgeDir(t)
	writeArgvJournalctl(t, dir)
	settings := filepath.Join(dir, "journal.settings.json")
	if err := os.WriteFile(settings, []byte(`{"full": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := startFrontedMain(t, dir, journalEntry(settings))
	out, errText, rc := ask(t, endpoint, `{"args":["-n","1"]}`)
	if rc != 64 || out != "" || errText != notAllowed {
		t.Fatalf("legacy full, no grants key: rc %d stdout %q stderr\n%s\nwant rc 64 and\n%s", rc, out, errText, notAllowed)
	}

	noneDir := bridgeDir(t)
	writeArgvJournalctl(t, noneDir)
	noneEndpoint := startFrontedMain(t, noneDir, journalEntry(""))
	out, errText, rc = ask(t, noneEndpoint, `{"args":["-n","1"]}`)
	if rc != 64 || out != "" || errText != notAllowed {
		t.Fatalf("no settings file: rc %d stdout %q stderr\n%s\nwant rc 64 and\n%s", rc, out, errText, notAllowed)
	}
}

// The refusal prints the `access` the launch wrote into the grant file, the manifest's own
// line; the bridge's constant is only the fallback for a file that carries none.
func TestJournaldRefusalTakesAccessFromTheGrantFile(t *testing.T) {
	isolateFromConfig(t)
	dir := bridgeDir(t)
	writeArgvJournalctl(t, dir)
	grantFile := filepath.Join(dir, "journal.settings.json")
	b := loopholegrant.Block{
		Pack: "journal", Workspace: "/home/u/proj", Container: "yolo-proj", LaunchID: "l1",
		PID: os.Getpid(), Access: "read this manifest's journal",
		Kinds: map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Requestable}},
	}
	data, err := loopholegrant.Merge(nil, b)
	if err != nil {
		t.Fatal(err)
	}
	writeAtomically(t, grantFile, data)
	endpoint := startFrontedMain(t, dir, journalEntry(grantFile))
	_, errText, rc := ask(t, endpoint, `{"args":["-n","1"]}`)
	if rc != 64 || !strings.Contains(errText, "no grant to read this manifest's journal") {
		t.Fatalf("rc %d stderr\n%s\nwant the grant file's access line", rc, errText)
	}
}

func macosLogEntry(grantFile string) func(string, <-chan struct{}) int {
	return func(sock string, stop <-chan struct{}) int {
		return macosLogMain([]string{"--socket", sock, "--settings", grantFile},
			func() <-chan struct{} { return stop })
	}
}

func TestMacosLogReadsOnlyItsGrantFile(t *testing.T) {
	isolateFromConfig(t)
	savedBin, savedUID := macosLogBin, lookupSandboxUID
	t.Cleanup(func() { macosLogBin, lookupSandboxUID = savedBin, savedUID })
	macosLogBin = writeFakeLog(t)
	lookupSandboxUID = func() (uint32, error) { return 401, nil }

	dir := bridgeDir(t)
	grantFile := filepath.Join(dir, "macos-log.settings.json")
	writeGrantFile(t, grantFile, "macos-log", `{"full": true}`, map[string]loopholegrant.Standing{
		"read": loopholegrant.Pending, "read_all": loopholegrant.Requestable,
	})
	endpoint := startFrontedMain(t, dir, macosLogEntry(grantFile))

	out, errText, rc := ask(t, endpoint, `{"args":["show","--last","1m"]}`)
	want := loopholegrant.Refusal{
		Client: "yolo-log", Pack: "macos-log", Access: "read the Mac's unified log",
		Kind: "read", Dir: "/home/u/proj", Standing: loopholegrant.Pending,
	}.Text()
	if rc != 64 || errText != want || out != "" {
		t.Fatalf("with read pending: rc %d stdout %q stderr\n%s\nwant rc 64 and\n%s", rc, out, errText, want)
	}

	writeGrantFile(t, grantFile, "macos-log", `{}`, map[string]loopholegrant.Standing{
		"read": loopholegrant.Granted, "read_all": loopholegrant.Requestable,
	})
	out, errText, _ = ask(t, endpoint, `{"args":["show","--last","1m"]}`)
	if out != `{"userID":401,"processID":1,"eventMessage":"from-sandbox"}`+"\n" ||
		!strings.Contains(errText, "--style ndjson") {
		t.Fatalf("with read granted: stdout %q stderr %q, want the sandbox account's entry alone", out, errText)
	}

	writeGrantFile(t, grantFile, "macos-log", `{}`, map[string]loopholegrant.Standing{
		"read_all": loopholegrant.Granted,
	})
	out, _, _ = ask(t, endpoint, `{"args":["show","--style","compact"]}`)
	for _, w := range []string{"from-sandbox", "from-host-user", "from-root"} {
		if !strings.Contains(out, w) {
			t.Fatalf("with read_all granted the bridge dropped %q: %q", w, out)
		}
	}
}

// PD-12, macos-log's flip: a settings file with no `grants` key grants nothing, and neither
// does no settings file. The old `full: true` setting no longer widens anything; both fail
// closed with the not_allowed text and run no `log`.
func TestMacosLogWithoutAGrantsKeyRefuses(t *testing.T) {
	isolateFromConfig(t)
	savedBin, savedUID := macosLogBin, lookupSandboxUID
	t.Cleanup(func() { macosLogBin, lookupSandboxUID = savedBin, savedUID })
	macosLogBin = writeFakeLog(t)
	lookupSandboxUID = func() (uint32, error) { return 401, nil }
	notAllowed := loopholegrant.Refusal{
		Client: "yolo-log", Pack: "macos-log", Access: "read the Mac's unified log",
		Kind: "read", Standing: loopholegrant.NotAllowed,
	}.Text()

	dir := bridgeDir(t)
	settings := filepath.Join(dir, "macos-log.settings.json")
	if err := os.WriteFile(settings, []byte(`{"full": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := startFrontedMain(t, dir, macosLogEntry(settings))
	out, errText, rc := ask(t, endpoint, `{"args":["show"]}`)
	if rc != 64 || out != "" || errText != notAllowed {
		t.Fatalf("legacy full, no grants key: rc %d stdout %q stderr\n%s\nwant rc 64 and\n%s", rc, out, errText, notAllowed)
	}

	noneEndpoint := startFrontedMain(t, bridgeDir(t), macosLogEntry(""))
	out, errText, rc = ask(t, noneEndpoint, `{"args":["show"]}`)
	if rc != 64 || out != "" || errText != notAllowed {
		t.Fatalf("no settings file: rc %d stdout %q stderr\n%s\nwant rc 64 and\n%s", rc, out, errText, notAllowed)
	}
}
