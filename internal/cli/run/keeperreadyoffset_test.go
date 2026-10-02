package run

// keeperreadyoffset_test.go pins the BOUNDARY between what a fresh launch's relay shows of its
// keeper and what its first session's quit replays from the keeper's log
// (docs/design/jail-lifetime-last-session-wins.md JL-D19, JL-D78). The relay prints every keeper
// line up to the ready frame and stops there; the quit prints the log from an offset. A line the
// keeper wrote after its ready frame and before that offset reached neither, and the launch used
// to take the offset itself, by a stat of the log once its relay was done: a host service that
// died in between was never named to the first terminal. The keeper now puts the log's length in
// the ready frame, taken under the same lock as every line's pipe and log writes, so each line is
// either before the frame (relayed) or after the offset (replayed), never both and never neither.

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestADeathBetweenTheReadyFrameAndTheFirstSessionIsNamedOnceByItsQuit drives Run down the podman
// fresh-launch path to a real ready keeper (TestMain's in-process one) holding one port forward,
// whose socat is a fake that ends on a file. The forward dies AFTER the launch's relay has read
// the ready frame and BEFORE its first session begins: at runContainer's one clock read between the
// two, which waits until the keeper has logged the death. A second session enters there too, so the
// first one's quit leaves the jail up and prints what the keeper logged from its offset; that
// answer rests on the session lock alone, where a last session's would rest on how soon the keeper
// takes the lock, which a loaded machine can push past the quit's bound. The death must be named
// exactly once in everything the launch printed: by that quit, since the relay was over. Taking the
// offset by a stat after the relay, as the launch did, names it never; deleting the relay's
// hand-over of the frame's offset falls back to that stat and fails the same way. Nothing the
// keeper logged before its frame may be replayed either: its ready line logged after the frame
// rather than before it, or an offset asked for before the relay read the frame, replays one.
func TestADeathBetweenTheReadyFrameAndTheFirstSessionIsNamedOnceByItsQuit(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	home := packHome(t)
	writeUserConfig(t, home, `{
  "packs": [],
  "network": {"forward_host_ports": [5432]}
}
`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	jail := newFakeJail(t, cname)
	started := filepath.Join(jail.dir, "started")
	die := filepath.Join(t.TempDir(), "die")
	bin := t.TempDir()
	// The runtime: `run` is the main process, its boot and a hold until the fake stop; `exec` is the
	// first session, which ends at once. Every other runtime call is o.Exec's.
	podman := "#!/bin/sh\ncase \"$1\" in\nrun)\n  : > '" + started + "'\n" +
		"  echo 'a boot line' >&2\n  echo " + shellQuoteForTest(entrypoint.BootReadyLine) + " >&2\n" +
		"  while [ ! -e '" + filepath.Join(jail.dir, "stop") + "' ]; do sleep 0.02; done\n  exit 143;;\n" +
		"esac\nexit 0\n"
	// The forward's socat: its socket, then a hold until the death file.
	socat := "#!/bin/sh\narg=\"$1\"\np=\"${arg#UNIX-LISTEN:}\"\np=\"${p%%,*}\"\n: > \"$p\"\n" +
		"while [ ! -e '" + die + "' ]; do sleep 0.02; done\nexit 1\n"
	for name, body := range map[string]string{"podman": podman, "socat": socat} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(jail.dir, "stop"), nil, 0o644)
		_ = os.WriteFile(die, nil, 0o644)
	})

	o := dispatchOptions(t, ws, "podman", new(bytes.Buffer), new(bytes.Buffer), nil)
	var stdout, stderr lockedBuffer
	o.Stdout, o.Stderr = &stdout, &stderr
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	// No container of the name until the main process has started, so the launch is a fresh one;
	// from then on the fake jail answers, its stop ending the main process.
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "ps" {
			if _, err := os.Stat(started); err != nil {
				return ExecResult{Ran: true}
			}
		}
		return jail.exec(argv, dir, env, timeout)
	}
	o.PIDAlive = func(int) bool { return false }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.CapturesDir = func() string { return "" }
	o.AcceptConfigChanges = true
	t.Cleanup(func() {
		_ = os.RemoveAll(hostServiceSocketsDir(cname, false))
		_ = os.RemoveAll(o.fwdSocketDir(cname))
	})

	// THE WINDOW: runContainer reads the clock once, for its first session's start, after its relay
	// has read the ready frame and before the session runs. Another session enters there, the
	// forward dies, and the clock read returns once the keeper has logged the death.
	var fired atomic.Bool
	logged := make(chan string, 1)
	var other *sessionLock
	t.Cleanup(func() {
		if other != nil {
			other.release()
		}
	})
	o.Now = func() time.Time {
		if pc, _, _, ok := goruntime.Caller(1); ok && strings.HasSuffix(goruntime.FuncForPC(pc).Name(), ".runContainer") &&
			fired.CompareAndSwap(false, true) {
			lock, _, err := takeSessionLock(cname)
			if err != nil {
				t.Error(err)
			}
			other = lock
			if err := os.WriteFile(die, nil, 0o644); err != nil {
				t.Error(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				body, _ := os.ReadFile(keeperLogPath(cname))
				if strings.Contains(string(body), "went down") || !time.Now().Before(deadline) {
					logged <- string(body)
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		return time.Unix(0, 0)
	}

	rc := -1
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		rc = Run(*o)
	}()
	select {
	case <-returned:
	case <-time.After(sealedLaunchBound):
		t.Fatalf("the launch had not returned after %s\nstdout:\n%s\nstderr:\n%s",
			sealedLaunchBound, stdout.String(), stderr.String())
	}
	printed := stdout.String() + stderr.String()
	if !fired.Load() {
		t.Fatalf("the launch never reached its first session's start, so this test says nothing about the "+
			"window (rc %d):\n%s", rc, printed)
	}
	if log := <-logged; !strings.Contains(log, "went down") {
		t.Fatalf("the keeper never logged the forward's death in the window, so this test says nothing "+
			"about it:\n%s\nthe launch printed:\n%s", log, printed)
	}
	if !strings.Contains(printed, "stays up for") {
		t.Fatalf("the first session's quit did not leave the jail up for the session that entered in the "+
			"window, so it printed nothing the keeper logged (rc %d):\n%s", rc, printed)
	}
	const death = "the port forward from jail port 5432 to host port 5432 went down"
	if n := strings.Count(printed, death); n != 1 {
		t.Errorf("a forward that died between the keeper's ready frame and the first session was named %d "+
			"times, want once, by the session's quit: the relay stops at the frame, so the quit must replay "+
			"the keeper's log from the frame's own place in it (rc %d):\n%s", n, rc, printed)
	}
	// And the quit replays nothing from before the frame: neither the line the keeper logs for
	// itself alone just before it (logOnlyf, which no quit replays), nor pid 1's boot, which the
	// relay already printed to the process's own stderr. Either one in the launch's own output is
	// an offset ahead of the frame: the keeper's ready line logged after its frame, or the launch
	// asking for the offset before its relay read the frame.
	for _, before := range []string{"holds " + cname + " until its last session leaves", "a boot line"} {
		if strings.Contains(printed, before) {
			t.Errorf("the first session's quit replayed %q, which the keeper logged before its ready frame "+
				"(rc %d):\n%s", before, rc, printed)
		}
	}
	if rc != 0 {
		t.Errorf("the launch exited %d, want its session's 0:\n%s", rc, printed)
	}

	// The other session leaves, and the keeper ends the jail.
	other.release()
	other = nil
	for deadline := time.Now().Add(30 * time.Second); probeKeeper(cname) != keeperGone; time.Sleep(20 * time.Millisecond) {
		if !time.Now().After(deadline) {
			continue
		}
		body, _ := os.ReadFile(keeperLogPath(cname))
		t.Fatalf("the keeper did not end the jail once its last session left:\n%s", body)
	}
	if n := jail.stopCount(); n != 1 {
		t.Errorf("the jail was stopped %d times, want once", n)
	}
}

// TestTheReadyFrameCarriesTheLogsLengthSoEachLineReachesOneSide is the boundary at the sink: the
// keeper's ready (sayReady) puts its log's length in the frame, and a line is on exactly one side
// of it. Every line before the frame is relayed and every line after it is past the offset, so the
// quit's follower reads it; none is in both, none in neither. The keeper's own lines after it are
// in the launch.log mirror, which goes on in the same hold, and nothing crosses the pipe after it.
func TestTheReadyFrameCarriesTheLogsLengthSoEachLineReachesOneSide(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-ready-offset"
	logFile, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	var pipe, mirror bytes.Buffer
	s := &keeperSink{pipe: &pipe, log: logFile}
	s.logf("keeper: before the frame")
	keeperStream{s, frameJailStderr}.Write([]byte("a boot line\n"))
	s.sayReady(&mirror)
	s.logf("keeper: after the frame")
	keeperStream{s, frameJailStderr}.Write([]byte("pid 1 after the frame\n"))

	var out, errOut, jailOut, jailErr bytes.Buffer
	var at int64
	known := false
	if !relayKeeper(&pipe, &out, &errOut, &jailOut, &jailErr, keeperEvents{
		ready: func(n int64, ok bool) { at, known = n, ok },
	}) {
		t.Fatal("the relay never read the ready frame")
	}
	if !known {
		t.Fatal("the ready frame carried no log length from a keeper whose log is a file")
	}
	if pipe.Len() != 0 {
		t.Errorf("%d bytes crossed the pipe after the ready frame", pipe.Len())
	}
	relayed := out.String() + errOut.String() + jailOut.String() + jailErr.String()
	replayed := strings.Join((&keeperLogFollower{path: keeperLogPath(cname), offset: at}).next(), "\n")
	for _, line := range []string{"keeper: before the frame", "a boot line"} {
		if !strings.Contains(relayed, line) || strings.Contains(replayed, line) {
			t.Errorf("%q, before the frame, was relayed=%v and replayed=%v; want relayed only",
				line, strings.Contains(relayed, line), strings.Contains(replayed, line))
		}
	}
	for _, line := range []string{"keeper: after the frame", "pid 1 after the frame"} {
		if strings.Contains(relayed, line) || !strings.Contains(replayed, line) {
			t.Errorf("%q, after the frame, was relayed=%v and replayed=%v; want replayed only",
				line, strings.Contains(relayed, line), strings.Contains(replayed, line))
		}
	}
	if m := mirror.String(); !strings.Contains(m, "keeper: after the frame") ||
		strings.Contains(m, "before the frame") || strings.Contains(m, "pid 1") {
		t.Errorf("the launch.log mirror holds %q; want the keeper's own lines after the frame alone", m)
	}
}

// TestAReadyFrameWithNoLengthFallsBackToTheLogsSize: a keeper with no log it can measure sends an
// empty ready frame, and the first session's offset is then the log's length when it asks, as the
// launch took it before the frame carried one. A frame that carries a length is taken as given.
func TestAReadyFrameWithNoLengthFallsBackToTheLogsSize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-ready-no-offset"
	logFile, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logFile.WriteString("a line some keeper wrote\n"); err != nil {
		t.Fatal(err)
	}
	_ = logFile.Close()
	// A sink with no log sends the empty frame.
	var unmeasured bytes.Buffer
	(&keeperSink{pipe: &unmeasured}).sayReady(nil)
	var measured bytes.Buffer
	if err := writeFrame(&measured, frameReady, []byte("7")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		frame []byte
		want  int64
	}{
		{"no length", unmeasured.Bytes(), keeperLogSize(cname)},
		{"a length", measured.Bytes(), 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := w.Write(tc.frame); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			kp := &keeperProcess{progress: r, ready: make(chan struct{})}
			var sink bytes.Buffer
			if !kp.relay(&sink, &sink, &sink, &sink, keeperEvents{}) {
				t.Fatal("the relay never read the ready frame")
			}
			if got := kp.sessionLogFrom(cname); got != tc.want || tc.want == 0 {
				t.Errorf("the first session's offset is %d, want %d (non-zero)", got, tc.want)
			}
		})
	}
}
