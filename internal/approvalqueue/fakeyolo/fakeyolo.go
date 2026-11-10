// Package fakeyolo lets a test binary stand in for the yolo executable. A package's TestMain
// calls MaybeRun first; when FAKE_YOLO_SCRIPT is set the process acts as yolo, replaying the
// files a Script names, and exits. Install puts a link named "yolo" to the test binary on PATH.
// It is test support only; the queue binary never imports it.
package fakeyolo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const envGrandchild = "FAKE_YOLO_GRANDCHILD"

func startGrandchild() int {
	self, err := os.Executable()
	if err != nil {
		return 0
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), envGrandchild+"=1")
	if cmd.Start() != nil {
		return 0
	}
	return cmd.Process.Pid
}

// blockForever waits to be killed. A pending timer, unlike a bare select, keeps the runtime's
// deadlock detector from ending the process early with "all goroutines are asleep".
func blockForever() {
	for {
		time.Sleep(time.Hour)
	}
}

// EnvScript names the JSON file holding the Script.
const EnvScript = "FAKE_YOLO_SCRIPT"

// Run is one replayed invocation.
type Run struct {
	Stdout      string `json:"stdout,omitempty"`      // file whose bytes are printed
	StdoutText  string `json:"stdout_text,omitempty"` // or literal text
	Stderr      string `json:"stderr,omitempty"`
	Exit        int    `json:"exit,omitempty"`
	Hang        bool   `json:"hang,omitempty"`         // block before printing anything
	Block       bool   `json:"block,omitempty"`        // after printing, block until killed (watch)
	WaitFile    string `json:"wait_file,omitempty"`    // block until this file exists
	StderrAfter string `json:"stderr_after,omitempty"` // printed after stdout
	Grandchild  bool   `json:"grandchild,omitempty"`   // start a child of its own, in the same process group
}

// Script is what the fake does. The nth call of a mode uses entry n, the last entry repeating.
type Script struct {
	Record   string `json:"record,omitempty"`   // jsonl file: one {mode,args,env} per call
	Counters string `json:"counters,omitempty"` // directory for per-mode call counters
	JSON     []Run  `json:"json,omitempty"`     // "approve --json"
	Watch    []Run  `json:"watch,omitempty"`    // "approve --watch --json"
}

// Call is one recorded invocation.
type Call struct {
	Pid      int      `json:"pid"`
	GrandPid int      `json:"grand_pid,omitempty"`
	Mode     string   `json:"mode"`
	Args     []string `json:"args"`
	Env      []string `json:"env"`
}

// MaybeRun acts as yolo and returns true when the environment says so.
func MaybeRun() bool {
	if os.Getenv(envGrandchild) != "" {
		blockForever() // a grandchild only waits to be killed
	}
	path := os.Getenv(EnvScript)
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake yolo: cannot read script:", err)
		os.Exit(99)
	}
	var s Script
	if err := json.Unmarshal(b, &s); err != nil {
		fmt.Fprintln(os.Stderr, "fake yolo: bad script:", err)
		os.Exit(99)
	}
	os.Exit(play(s, os.Args[1:]))
	return true
}

func play(s Script, args []string) int {
	mode, runs := "", []Run(nil)
	switch {
	case len(args) == 2 && args[0] == "approve" && args[1] == "--json":
		mode, runs = "json", s.JSON
	case len(args) == 3 && args[0] == "approve" && args[1] == "--watch" && args[2] == "--json":
		mode, runs = "watch", s.Watch
	default:
		fmt.Fprintln(os.Stderr, "fake yolo: unexpected arguments", args)
		return 2
	}
	n := count(s, mode)
	grand := 0
	if len(runs) > 0 && runs[min(n, len(runs)-1)].Grandchild {
		grand = startGrandchild()
	}
	record(s, mode, args, grand)
	if len(runs) == 0 {
		fmt.Fprintln(os.Stderr, "fake yolo: no script for", mode)
		return 98
	}
	if n >= len(runs) {
		n = len(runs) - 1
	}
	r := runs[n]
	if r.Hang {
		blockForever()
	}
	for r.WaitFile != "" {
		if _, err := os.Stat(r.WaitFile); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.Stderr != "" {
		fmt.Fprint(os.Stderr, r.Stderr)
	}
	switch {
	case r.Stdout != "":
		b, err := os.ReadFile(r.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake yolo:", err)
			return 99
		}
		os.Stdout.Write(b)
	case r.StdoutText != "":
		fmt.Fprint(os.Stdout, r.StdoutText)
	}
	if r.StderrAfter != "" {
		fmt.Fprint(os.Stderr, r.StderrAfter)
	}
	if r.Block {
		blockForever()
	}
	return r.Exit
}

func record(s Script, mode string, args []string, grand int) {
	if s.Record == "" {
		return
	}
	line, _ := json.Marshal(Call{Pid: os.Getpid(), GrandPid: grand, Mode: mode, Args: args, Env: os.Environ()})
	f, err := os.OpenFile(s.Record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func count(s Script, mode string) int {
	if s.Counters == "" {
		return 0
	}
	f, err := os.OpenFile(filepath.Join(s.Counters, mode), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0
	}
	defer f.Close()
	f.Write([]byte{'x'})
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	return int(st.Size()) - 1
}

// Install writes the script, links the running test binary as "yolo" in a temp directory and
// points FAKE_YOLO_SCRIPT and PATH at them for the test. It returns the path of the fake.
func Install(t testing.TB, s Script) string {
	t.Helper()
	dir := t.TempDir()
	if s.Counters == "" {
		s.Counters = filepath.Join(dir, "counters")
		if err := os.MkdirAll(s.Counters, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := json.Marshal(s)
	script := filepath.Join(dir, "script.json")
	if err := os.WriteFile(script, b, 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, "yolo")
	if err := os.Symlink(self, fake); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvScript, script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fake
}

// Calls reads back the recorded invocations.
func Calls(t testing.TB, record string) []Call {
	t.Helper()
	b, err := os.ReadFile(record)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []Call
	for _, l := range splitLines(b) {
		var c Call
		if err := json.Unmarshal(l, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	return out
}
