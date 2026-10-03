package run

// fakeprocs_test.go pins that a process a test's fake runtime starts never outlives the test.
//
// Several fakes here are shells that hold until a file appears: a fake jail's main process holds
// until its stop file, a fake socat until its death file. The file used to be written by a cleanup
// and its directory removed by t.TempDir's own cleanup, which was registered earlier and so ran an
// instant later (cleanups run last-registered-first). A loop polling every 20 ms usually missed the
// file, then polled a path that could no longer exist, and ran on after its test, forking a sleep 50
// times a second: 247 of them were found in one jail on 2026-10-03, some 37 hours old, together
// forking about 10,700 processes a second.
//
// So a fake process that holds on a file now does two things, and the helpers below are the only way
// this package's tests write one (TestEveryHoldOnAFileGoesThroughHoldUntil):
//
//  1. It holds on a HELD DIRECTORY (heldDir), a term this file coins for a t.TempDir whose fake
//     processes record their pids in it (recordPID) and whose own cleanup kills each one that still
//     runs and waits for it. That cleanup is registered after t.TempDir registered the directory's
//     removal, so it runs first, whatever else the test registers.
//  2. Its loop ends once the file's directory is gone (holdUntil), so even a process that cleanup
//     never learned of (one that had not yet recorded itself) cannot poll a removed path forever.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// heldPIDsName is the file in a held directory where each fake process holding on it records its pid,
// one per line.
const heldPIDsName = ".pids"

// heldDir is a new held directory: a t.TempDir, and the cleanup that ends every fake process recorded
// in it. The t.TempDir call that made the test's temporary root registered its removal, the removal
// of every t.TempDir of the test, at or before this call, so this cleanup runs before the directory
// goes, whatever the test registers in between or later.
func heldDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { endHeld(t, dir) })
	return dir
}

// recordPID is the shell, to run before anything else, that records the running shell's pid in the
// held directory dir. The process's command line must name dir, as a shell's script that spells a
// path in it does, or as a script that lives in it does by its own path: that is how endHeld tells it
// from a later process the pid was handed to.
func recordPID(dir string) string {
	return "echo $$ >> " + shquote.Quote(filepath.Join(dir, heldPIDsName)) + "; "
}

// holdUntil is the shell that holds until file exists, and ends as well once file's directory is
// gone: nothing can write the file into a removed directory, so a hold that missed the moment it was
// written would otherwise poll for it forever.
func holdUntil(file string) string {
	return "while [ -d " + shquote.Quote(filepath.Dir(file)) + " ] && [ ! -e " + shquote.Quote(file) +
		" ]; do sleep 0.02; done"
}

// endHeld ends every process recorded in the held directory dir that still runs: a SIGKILL to its
// process group when it leads one, so the sleep its loop waits on goes too, and to it alone
// otherwise; then a bounded wait for it to end. A recorded pid whose process's command line does not
// name dir ended already, and the pid is left alone, whoever has it now.
func endHeld(t testing.TB, dir string) {
	raw, err := os.ReadFile(filepath.Join(dir, heldPIDsName))
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 || !namesDir(pid, dir) {
			continue
		}
		target := pid
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
			target = -pid
		}
		_ = syscall.Kill(target, syscall.SIGKILL)
		for deadline := time.Now().Add(10 * time.Second); namesDir(pid, dir); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Errorf("fake process %d, holding on %s, still runs 10s after its SIGKILL", pid, dir)
				break
			}
		}
	}
}

// namesDir reports whether pid is a running process whose command line names dir. An exited process
// not yet reaped names nothing: Linux's cmdline of one is empty, and ps shows no arguments for one.
func namesDir(pid int, dir string) bool {
	if cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		return bytes.Contains(cmdline, []byte(dir))
	} else if _, err := os.Stat("/proc/self/cmdline"); err == nil {
		return false // a /proc, and no such process in it
	}
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && strings.Contains(string(out), dir)
}

// pidGoneWithin reports whether pid has exited and been reaped within bound.
func pidGoneWithin(pid int, bound time.Duration) bool {
	for deadline := time.Now().Add(bound); ; time.Sleep(10 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// runsNow reports whether pid is running at this instant, a zombie (exited, not yet reaped) counting
// as ended. It takes no time, so a hold about to see its stop file still counts as running. Off
// Linux there is no /proc to read the state from, and known is false.
func runsNow(pid int) (runs, known bool) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if _, perr := os.Stat("/proc/self/stat"); perr != nil {
			return false, false
		}
		return false, true
	}
	// The state is the field after the command, which is in parentheses and may hold anything.
	rest := stat[bytes.LastIndexByte(stat, ')')+1:]
	fields := strings.Fields(string(rest))
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X", true
}

// TestAFakeJailsMainProcessEndsWithItsTest: a test that leaves its fake jail's main process holding,
// with nothing stopping it, still leaves nothing running once its cleanups are done. The process
// must have ENDED before the directory it holds on is removed, so its end rests neither on its loop
// noticing the removal nor on the moment it next polls a stop file a cleanup wrote: the subtest's
// first t.TempDir is taken before the fake, as every keeper test's HOME is, so its removal runs
// after the fake's cleanups, and a cleanup registered between the two looks, at once, while the
// directory still exists.
func TestAFakeJailsMainProcessEndsWithItsTest(t *testing.T) {
	for _, tc := range []struct {
		name string
		// start makes the fake and returns its main process's argv, and settle waits, once the
		// process has started, for what the fake does on its own before a test's end.
		start func(t *testing.T) (argv []string, settle func())
	}{
		{"fakeJail", func(t *testing.T) ([]string, func()) {
			j := newFakeJail(t, "yolo-fake-jail-ends")
			return j.mainArgv(true), func() {}
		}},
		{"lateJail", func(t *testing.T) ([]string, func()) {
			j := newLateJail(t, lateJailLag)
			// The container comes up, as it does in every test that ran past the lag, so the fake's
			// own cleanup, which waits for that, waits for nothing here.
			return j.mainArgv(), func() {
				for deadline := time.Now().Add(lateJailLag + 5*time.Second); !j.has("created") &&
					time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid := 0
			t.Run("a test that never stops its jail", func(t *testing.T) {
				_ = t.TempDir()
				t.Cleanup(func() {
					if pid == 0 {
						return
					}
					runs, known := runsNow(pid)
					if !known {
						runs = !pidGoneWithin(pid, 5*time.Second)
					}
					if runs {
						t.Errorf("the fake's main process (pid %d) still ran after the fake's cleanups, "+
							"with its directory not yet removed", pid)
					}
				})
				argv, settle := tc.start(t)
				var stderr lockedBuffer
				m, err := startJailMain(argv, io.Discard, &stderr, nil)
				if err != nil {
					t.Fatal(err)
				}
				pid = m.cmd.Process.Pid
				for deadline := time.Now().Add(10 * time.Second); !strings.Contains(stderr.String(), "a boot line"); {
					if time.Now().After(deadline) {
						t.Fatalf("the fake's main process never booted: %q", stderr.String())
					}
					time.Sleep(10 * time.Millisecond)
				}
				settle()
			})
			if pid == 0 {
				t.Fatal("the subtest started no main process")
			}
			if !pidGoneWithin(pid, 5*time.Second) {
				// This test leaves nothing of its own running, whatever it found.
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				t.Errorf("the fake's main process (pid %d) outlived its test", pid)
			}
		})
	}
}

// TestAHoldEndsOnceItsDirectoryIsGone is the second half on its own: a hold that no cleanup ends, and
// whose file is never written, still ends once its directory is removed, rather than polling a path
// that can no longer exist.
func TestAHoldEndsOnceItsDirectoryIsGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "held")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("sh", "-c", holdUntil(filepath.Join(dir, "stop")))
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = c.Wait(); close(exited) }()
	select {
	case <-exited:
		t.Fatal("the hold ended before its directory went, with no file in it")
	case <-time.After(200 * time.Millisecond):
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		<-exited
		t.Error("the hold still polled 5s after its directory was removed")
	}
}

// TestEveryHoldOnAFileGoesThroughHoldUntil keeps the shape that leaked out of this package's tests:
// a fake that holds on a file spells its loop through holdUntil, and records itself in a held
// directory, rather than writing the loop out by hand.
func TestEveryHoldOnAFileGoesThroughHoldUntil(t *testing.T) {
	handWritten := regexp.MustCompile(`while \[ ! -e|while ! \[ -e|until \[ -e|while ! tes[t] -e`)
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files to read here (%v)", err)
	}
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if handWritten.MatchString(line) {
				t.Errorf("%s:%d holds on a file with a loop of its own, which can outlive its test; "+
					"use recordPID in a heldDir and holdUntil (fakeprocs_test.go):\n\t%s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
