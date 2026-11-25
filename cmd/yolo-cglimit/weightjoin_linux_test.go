//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cgd"
)

func TestWeightIsParsedAndRangeChecked(t *testing.T) {
	for _, bad := range []string{"0", "-1", "10001", "abc", "1.5"} {
		var out, errOut bytes.Buffer
		_, _, done := parseArgs([]string{"--weight", bad, "--", "true"}, &out, &errOut)
		if !done || !strings.Contains(errOut.String(), "1 to 10000") {
			t.Errorf("--weight %s: done=%v stderr=%q, want a refusal naming the range", bad, done, errOut.String())
		}
	}
	for _, good := range []string{"1", "100", "10000"} {
		var out, errOut bytes.Buffer
		o, _, done := parseArgs([]string{"--weight", good, "--", "true"}, &out, &errOut)
		n, _ := strconv.Atoi(good)
		if done || o.weight == nil || *o.weight != n {
			t.Errorf("--weight %s: done=%v weight=%v", good, done, o.weight)
		}
	}
}

func TestWeightTravelsAloneAsANumber(t *testing.T) {
	socket, got := fakeDaemon(t, `{"ok": true}`+"\n")
	useSocket(t, socket)
	var out, errOut bytes.Buffer
	code, command := run([]string{"--weight", "800", "--name", "gates", "--", "true"}, &out, &errOut)
	if code != 0 || len(command) != 1 {
		t.Fatalf("run = %d/%v stderr %q", code, command, errOut.String())
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(got()), &req); err != nil {
		t.Fatal(err)
	}
	if v, ok := req["weight"].(float64); !ok || v != 800 || req["op"] != "create_and_join" {
		t.Errorf("request = %v, want create_and_join with the number 800", req)
	}
	for _, k := range []string{"cpu_pct", "memory", "pids"} {
		if _, present := req[k]; present {
			t.Errorf("a weight-only request carries %s", k)
		}
	}
}

func TestHelpDescribesWeightAndJoin(t *testing.T) {
	var out, errOut bytes.Buffer
	run([]string{"--help"}, &out, &errOut)
	for _, want := range []string{"--weight N", "--join PID", "1-10000", "only matters while processes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help is missing %q", want)
		}
	}
}

// fdDaemon is fakeDaemon that also receives SCM_RIGHTS, returning the request
// line and the descriptors that came with it.
func fdDaemon(t *testing.T, reply string) (socket string, got func() (string, []int)) {
	t.Helper()
	socket = filepath.Join(shortSocketDir(t), "cgroup-delegate.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatalf("bind %s: %v", socket, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	type rec struct {
		line string
		fds  []int
	}
	recorded := make(chan rec, 1)
	go func() {
		conn, aerr := ln.AcceptUnix()
		if aerr != nil {
			return
		}
		defer conn.Close()
		buf, oob := make([]byte, 4096), make([]byte, syscall.CmsgSpace(4))
		n, oobn, _, _, _ := conn.ReadMsgUnix(buf, oob)
		var fds []int
		if msgs, err := syscall.ParseSocketControlMessage(oob[:oobn]); err == nil {
			for _, m := range msgs {
				if f, err := syscall.ParseUnixRights(&m); err == nil {
					fds = append(fds, f...)
				}
			}
		}
		recorded <- rec{string(buf[:n]), fds}
		_, _ = conn.Write([]byte(reply))
	}()
	return socket, func() (string, []int) {
		select {
		case r := <-recorded:
			return r.line, r.fds
		default:
			return "", nil
		}
	}
}

func TestJoinSendsThePidfdOfTheTargetAndNoPidNumber(t *testing.T) {
	socket, got := fdDaemon(t, `{"ok": true}`+"\n")
	useSocket(t, socket)
	var out, errOut bytes.Buffer
	code, command := run([]string{"--name", "gates", "--weight", "300", "--join", strconv.Itoa(os.Getpid())}, &out, &errOut)
	if code != 0 || command != nil {
		t.Fatalf("run = %d/%v stderr %q", code, command, errOut.String())
	}
	line, fds := got()
	if len(fds) != 1 {
		t.Fatalf("daemon received %d descriptors, want exactly the target's pidfd", len(fds))
	}
	f := os.NewFile(uintptr(fds[0]), "received")
	defer f.Close()
	if l, _ := os.Readlink("/proc/self/fd/" + strconv.Itoa(fds[0])); l != "anon_inode:[pidfd]" {
		t.Errorf("the descriptor sent is %q, not a pidfd", l)
	}
	if info, _ := os.ReadFile("/proc/self/fdinfo/" + strconv.Itoa(fds[0])); !strings.Contains(string(info), "Pid:\t"+strconv.Itoa(os.Getpid())+"\n") {
		t.Errorf("the pidfd does not name process %d:\n%s", os.Getpid(), info)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		t.Fatal(err)
	}
	if req["op"] != "join" || req["name"] != "gates" || req["weight"] != float64(300) {
		t.Errorf("request = %v", req)
	}
	if _, present := req["pid"]; present {
		t.Error("a pid number rode in the request body; the daemon must not be offered one")
	}
	if cgd.RequestOp([]byte(line)) != "join" {
		t.Error("the daemon's own parser does not read op join from this client's request")
	}
}

func TestJoinNeedsANameAndTakesNoCommand(t *testing.T) {
	socket, got := fdDaemon(t, `{"ok": true}`+"\n")
	useSocket(t, socket)
	pid := strconv.Itoa(os.Getpid())
	for args, want := range map[string][]string{
		"--name":  {"--join", pid},
		"COMMAND": {"--name", "g", "--join", pid, "--", "true"},
	} {
		var out, errOut bytes.Buffer
		code, command := run(want, &out, &errOut)
		if code != 1 || command != nil || !strings.Contains(errOut.String(), args) {
			t.Errorf("run(%v) = %d/%v stderr %q, want a refusal naming %s", want, code, command, errOut.String(), args)
		}
	}
	if line, _ := got(); line != "" {
		t.Errorf("a refused join still contacted the daemon: %q", line)
	}
}

func TestJoinReportsADaemonRefusalAndFailsClosed(t *testing.T) {
	socket, _ := fdDaemon(t, `{"ok": false, "error": "Refusing to join PID 9: it is not inside this container's cgroup"}`+"\n")
	useSocket(t, socket)
	var out, errOut bytes.Buffer
	code, _ := run([]string{"--name", "g", "--join", strconv.Itoa(os.Getpid())}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "not inside this container's cgroup") {
		t.Errorf("code %d stderr %q", code, errOut.String())
	}
}
