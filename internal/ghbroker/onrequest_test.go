package ghbroker

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The broker's own process runs the user's on_request (BB-D75): with the key in the user
// config, a request this broker lifetime files runs the hook with the request on stdin and
// YOLO_BIN naming the broker's own executable, with no watch kept open. A trusted hook may run
// the host CLI itself, but its output or exit status is not consent; this hook only prints
// "approve", so the request stays pending. It runs the real daemon process (Main, through
// brokercore.Daemon.Main), so deleting the daemon's brokercore.StartOnRequest call, or Main's
// handing it the broker's store and start id, fails this test.
func TestTheBrokerRunsTheUsersOnRequest(t *testing.T) {
	dir := resolvedDir(t)
	stdin := filepath.Join(dir, "stdin.json")
	binFile := filepath.Join(dir, "bin")
	hook := filepath.Join(dir, "hook")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf '%s' \"$YOLO_BIN\" >'"+binFile+"'\ncat >'"+stdin+"'\necho approve\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := startBrokerProcessWith(t, `{"on_request": `+strconv.Quote(shquote.Quote(hook))+`}`)
	f, err := brokerscope.ReadFile(p.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the broker to bind its lifetime", 10*time.Second, func() bool {
		f, err = brokerscope.ReadFile(p.scopeFile)
		return err == nil && f.BrokerStart != ""
	})
	a := &Authority{path: p.scopeFile, scope: f, start: f.BrokerStart}
	d, policy, err := a.Classify([]string{"pr", "comment", "1", "--body", "hi"}, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	s := &brokerstore.Store{Dir: paths.BrokerSourceDir(Source)}
	q, err := s.File(brokerstore.Request{Jail: paths.JailShortHash(f.Container), Start: f.BrokerStart,
		Workspace: p.workspace, LaunchID: f.LaunchID, Argv: d.Argv, Repo: "o/r", Set: d.Set, Policy: policy,
		StdinDigest: "stdin"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hook to run", 20*time.Second, func() bool {
		b, err := os.ReadFile(stdin)
		return err == nil && bytes.Contains(b, []byte(q.ID))
	})
	if b, _ := os.ReadFile(stdin); !bytes.Contains(b, []byte(`"event":"request-filed"`)) {
		t.Fatalf("the hook's stdin: %q", b)
	}
	exe, _ := os.Executable()
	if b, _ := os.ReadFile(binFile); string(b) != exe {
		t.Fatalf("YOLO_BIN = %q, want the broker's own executable %q", b, exe)
	}
	time.Sleep(1500 * time.Millisecond) // past a poll, in case anything read the hook's output
	if got, err := s.Status(q.Jail, q.Start, q.ID); err != nil || got.State != "pending" {
		t.Fatalf("the hook's output decided the request: %+v %v", got, err)
	}
}

// With no on_request set, the broker runs nothing for a request.
func TestNoOnRequestRunsNothing(t *testing.T) {
	var log bytes.Buffer
	b := &Broker{store: &brokerstore.Store{Dir: t.TempDir()}, startID: "s", log: &log}
	done := b.startOnRequest(t.Context(), "")
	select {
	case <-done:
	default:
		t.Fatal("an unset hook started a runner")
	}
	if log.Len() != 0 {
		t.Fatalf("%q", log.String())
	}
}
