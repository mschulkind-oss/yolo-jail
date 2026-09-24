//go:build linux

package ttyproxy

import (
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type inputRec struct {
	mu     sync.Mutex
	sizes  []int
	keys   []string
	stages []string
}

func (r *inputRec) input(n int, key string) {
	r.mu.Lock()
	r.sizes = append(r.sizes, n)
	r.keys = append(r.keys, key)
	r.mu.Unlock()
}

func (r *inputRec) stage(s string) {
	r.mu.Lock()
	r.stages = append(r.stages, s)
	r.mu.Unlock()
}

func (r *inputRec) snapshot() ([]int, []string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.sizes...), append([]string(nil), r.keys...), append([]string(nil), r.stages...)
}

// The Observer sees each forwarded stdin chunk's SIZE and, for a lone ^C, the
// key's name — through the real RunWithProxyObserved on a real pty, so a
// deleted call in the pump fails here. It also reads the child's pty mode
// through the master, and that reader goes dead once the master is closed.
func TestObserverSeesInputSizesCtrlCAndThePtyMode(t *testing.T) {
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin = os.NewFile(uintptr(slave), "pty-slave-stdin")
	os.Stdout = os.NewFile(uintptr(slave), "pty-slave-stdout")
	defer func() { os.Stdin, os.Stdout = origIn, origOut; unix.Close(slave) }()

	var rec inputRec
	var mode func() string
	gotMode := make(chan struct{})
	done := make(chan int, 1)
	go func() {
		rc, _ := RunWithProxyObserved([]string{"sh", "-c",
			"stty raw -echo; dd bs=1 count=6 of=/dev/null 2>/dev/null; exit 42"}, nil, nil,
			Observer{Input: rec.input, Pty: func(m func() string) { mode = m; close(gotMode) }})
		done <- rc
	}()
	select {
	case <-gotMode:
	case <-time.After(5 * time.Second):
		t.Fatal("Observer.Pty was never called: the pty-mode reader is not handed out")
	}
	time.Sleep(300 * time.Millisecond) // the child's stty has run
	during := mode()
	if _, err := unix.Write(master, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // two reads, not one chunk
	if _, err := unix.Write(master, []byte{interruptByte}); err != nil {
		t.Fatal(err)
	}
	select {
	case rc := <-done:
		if rc != 42 {
			t.Fatalf("rc = %d", rc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child never read its six bytes")
	}

	sizes, keys, _ := rec.snapshot()
	if len(sizes) != 2 || sizes[0] != 5 || sizes[1] != 1 {
		t.Errorf("input sizes = %v, want [5 1]", sizes)
	}
	if len(keys) != 2 || keys[0] != "" || keys[1] != KeyCtrlC {
		t.Errorf("input keys = %q, want [\"\" %q] — only a lone ^C is named", keys, KeyCtrlC)
	}
	if during != "icanon=off isig=off echo=off" {
		t.Errorf("pty mode while the child held it raw = %q", during)
	}
	// Reuse the master's fd number (the lowest free fd is the one just closed):
	// an unguarded reader would now answer for SOMEONE ELSE'S pty.
	if m2, s2, err := openPty(); err == nil {
		defer unix.Close(m2)
		defer unix.Close(s2)
	}
	if after := mode(); after != "" {
		t.Errorf("the mode reader still answers after the master closed: %q", after)
	}
}

func TestClassifyInputNamesOnlyALoneCtrlC(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"\x03", KeyCtrlC},
		{"\x1b[99;5u", KeyCtrlC},    // kitty keyboard protocol press
		{"\x1b[99;5:2u", KeyCtrlC},  // kitty repeat
		{"\x1b[27;5;99~", KeyCtrlC}, // xterm modifyOtherKeys
		{"\x1b[99;5:3u", ""},        // a RELEASE is not a keypress
		{"\x1b[99;6u", ""},          // ctrl+shift
		{"a\x03", ""},               // content around it: not a lone key
		{"\x03\x03", ""},            // two of them: still content-shaped, say nothing
		{"c", ""},
		{"", ""},
	} {
		if got := classifyInput([]byte(tc.in)); got != tc.want {
			t.Errorf("classifyInput(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A ^Z self-suspend is bracketed by stage marks, written by the real pump —
// the suspend is the last thing a later `kill -9 %1` leaves on record.
func TestProxyLoopMarksTheSuspend(t *testing.T) {
	signal.Ignore(syscall.SIGTSTP) // so selfSuspend returns instead of stopping the test
	defer signal.Reset(syscall.SIGTSTP)

	hostMaster, hostSlave, err := openPty()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer unix.Close(hostMaster)
	defer unix.Close(hostSlave)
	childMaster, childSlave, err := openPty()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer unix.Close(childMaster)
	slave := os.NewFile(uintptr(childSlave), "pty-slave")
	c := exec.Command("sleep", "10")
	c.Stdin, c.Stdout, c.Stderr = slave, slave, slave
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Process.Kill() }()
	unix.Close(childSlave)
	cooked, err := unix.IoctlGetTermios(hostSlave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	setRaw(hostSlave, cooked)

	var rec inputRec
	go proxyLoop(hostSlave, childMaster, c, cooked, Observer{Stage: rec.stage, Input: rec.input})
	if _, err := unix.Write(hostMaster, []byte{suspByte}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, st := rec.snapshot(); len(st) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	sizes, _, stages := rec.snapshot()
	if len(stages) < 2 || stages[0] != StageSuspended || stages[1] != StageResumed {
		t.Errorf("stages = %v, want [%s %s ...]", stages, StageSuspended, StageResumed)
	}
	if len(sizes) != 1 || sizes[0] != 1 {
		t.Errorf("the ^Z chunk was not observed as one byte: %v", sizes)
	}
}
