package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// shortSocketDir is a scratch dir directly under /tmp: a socket under a TMPDIR-rooted path
// overruns darwin's sun_path (cmd/yolo-journalctl carries the same helper and note).
func shortSocketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yj-ylog-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func frame(stream byte, payload []byte) []byte {
	hdr := make([]byte, 5)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	return append(hdr, payload...)
}

func exitFrame(rc int) []byte {
	var p [4]byte
	binary.BigEndian.PutUint32(p[:], uint32(int32(rc)))
	return frame(frameExit, p[:])
}

type conversation struct {
	reply io.Reader
	sent  bytes.Buffer
}

func (c *conversation) Read(p []byte) (int, error)  { return c.reply.Read(p) }
func (c *conversation) Write(p []byte) (int, error) { return c.sent.Write(p) }

func TestRequestIsOneJSONLineOfArgs(t *testing.T) {
	c := &conversation{reply: bytes.NewReader(exitFrame(0))}
	var out, errOut bytes.Buffer
	converse(c, []string{"stream", "--level", "debug"}, &out, &errOut)
	line := c.sent.String()
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("request %q is not one newline-terminated line", line)
	}
	var got map[string][]string
	if err := json.Unmarshal([]byte(line), &got); err != nil || len(got) != 1 ||
		strings.Join(got["args"], " ") != "stream --level debug" {
		t.Fatalf("request %q decoded to %v (%v)", line, got, err)
	}
}

func TestFramesRouteAndTheExitCodeRoundTrips(t *testing.T) {
	reply := append(append(frame(frameStdout, []byte("entry\n")), frame(frameStderr, []byte("warn\n"))...), exitFrame(-15)...)
	c := &conversation{reply: bytes.NewReader(reply)}
	var out, errOut bytes.Buffer
	if rc := converse(c, nil, &out, &errOut); rc != -15 {
		t.Errorf("rc = %d, want -15", rc)
	}
	if out.String() != "entry\n" || errOut.String() != "warn\n" {
		t.Errorf("stdout %q stderr %q", out.String(), errOut.String())
	}
	if !strings.Contains(c.sent.String(), `"args":[]`) {
		t.Errorf("no args sent %q, want an empty list", c.sent.String())
	}
	c = &conversation{reply: bytes.NewReader(frame(frameStdout, []byte("x")))}
	if rc := converse(c, nil, &out, &errOut); rc != 1 {
		t.Errorf("a stream with no exit frame gave %d, want 1", rc)
	}
}

func TestFrameIDsAndEndpointMatchTheDaemon(t *testing.T) {
	if frameStdout != journald.FrameStdout || frameStderr != journald.FrameStderr || frameExit != journald.FrameExit {
		t.Fatal("client frame IDs have drifted from the daemon's")
	}
	if paths.MacosLogEndpointEnv != "YOLO_SERVICE_MACOS_LOG_ENDPOINT" {
		t.Fatalf("endpointEnv %q is not the macos-log loophole's variable", endpointEnv)
	}
}

// The client's exact bytes go through the daemon's own parser, which applies the user scope.
func TestTheDaemonParsesThisClientsRequest(t *testing.T) {
	c := &conversation{reply: bytes.NewReader(exitFrame(0))}
	var out, errOut bytes.Buffer
	converse(c, []string{"--last", "2m"}, &out, &errOut)
	p := journald.ParseMacosLogRequest([]byte(strings.TrimSuffix(c.sent.String(), "\n")), journald.ModeUser)
	if p.ErrText != "" || strings.Join(p.Args, " ") != "show --last 2m --style ndjson" || !p.Filter {
		t.Fatalf("daemon resolved %+v", p)
	}
}

func TestUnsetEndpointNamesHowToEnableIt(t *testing.T) {
	t.Setenv(endpointEnv, "")
	var out, errOut bytes.Buffer
	if rc := run([]string{"show"}, &out, &errOut); rc != 1 {
		t.Fatalf("rc = %d", rc)
	}
	for _, want := range []string{"add \"macos-log\" to \"packs\"", `"loopholes": {"macos-log": {"enabled": true}}`,
		`"settings": {"full": true}`, "relaunch"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("the off message does not name %q:\n%s", want, errOut.String())
		}
	}
	if strings.Contains(errOut.String(), `"packs": [`) {
		t.Errorf("the off message spells a whole packs list, which would replace the user's agents:\n%s", errOut.String())
	}
	out.Reset()
	if rc := run([]string{"--help"}, &out, &errOut); rc != 0 || !strings.Contains(out.String(), "off in this jail") {
		t.Errorf("--help: rc %d %q", rc, out.String())
	}
}

func TestAMissingEndpointFileSaysRelaunch(t *testing.T) {
	t.Setenv(endpointEnv, filepath.Join(shortSocketDir(t), "macos-log.endpoint"))
	var out, errOut bytes.Buffer
	if rc := run(nil, &out, &errOut); rc != 1 || !strings.Contains(errOut.String(), "relaunch the jail") {
		t.Errorf("rc %d stderr %q", rc, errOut.String())
	}
}

// TestEndToEndOverLoopbackTLS drives the real bridge — journald.ServeMacosLogFrontedUnix behind
// svcendpoint's front — with a fake `log`, in the user scope: only the sandbox account's entry
// reaches the client.
func TestEndToEndOverLoopbackTLS(t *testing.T) {
	dir := shortSocketDir(t)
	fake := filepath.Join(dir, "log")
	script := "#!/bin/sh\n" +
		"echo '{\"userID\":401,\"eventMessage\":\"mine\"}'\n" +
		"echo '{\"userID\":501,\"eventMessage\":\"host\"}'\n" +
		"echo \"$*\" >&2\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	endpoint := filepath.Join(dir, "macos-log.endpoint")
	upstream := filepath.Join(dir, "up.sock")
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = journald.ServeMacosLogFrontedUnix(upstream, journald.MacosLogConfig{
			Bin: fake, Mode: journald.ModeUser, SandboxUID: 401,
			Owner: func(int) (uint32, time.Time, bool) { return 0, time.Time{}, false },
		}, stop)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if fi, err := os.Lstat(upstream); err == nil && fi.Mode()&os.ModeSocket != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bridge never bound its socket")
		}
		time.Sleep(20 * time.Millisecond)
	}
	frontStop, frontDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(frontDone); _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", upstream, frontStop) }()
	t.Cleanup(func() { close(frontStop); <-frontDone; close(stop); <-done })
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("no usable endpoint")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Setenv(endpointEnv, endpoint)
	var out, errOut bytes.Buffer
	if rc := run([]string{"stream"}, &out, &errOut); rc != 0 {
		t.Fatalf("rc %d stderr %q", rc, errOut.String())
	}
	if out.String() != `{"userID":401,"eventMessage":"mine"}`+"\n" {
		t.Errorf("stdout %q, want the sandbox account's entry alone", out.String())
	}
	if errOut.String() != "stream --style ndjson\n" {
		t.Errorf("the bridge ran log with %q", errOut.String())
	}
}
