package run

// keeperwatch_test.go pins the keeper's record of a host service that ends while its jail is up
// (keeperwatch.go; docs/design/jail-lifetime-last-session-wins.md JL-D19, JL-D71): the keeper sees
// the end of each service it runs, logs it, so a session's quit prints it, and keeps it in its start
// record, so an arrival prints it; and a service its own teardown stops is never recorded down.

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// awaitKeeperLog waits, bounded, for the keeper's log to hold want.
func (f *keeperFixture) awaitKeeperLog(want string) string {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		log := f.keeperLog()
		if strings.Contains(log, want) {
			return log
		}
		if !time.Now().Before(deadline) {
			f.t.Fatalf("the keeper's log never said %q:\n%s", want, log)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// configLoopholes is a keeper plan's config declaring a command-only loophole per name, each the
// package's daemon child (frontUpstreamChildMain) in the mode named, with die as the file a "dies"
// child exits 3 on.
func configLoopholes(t *testing.T, modes map[string]string, die string) []byte {
	t.Helper()
	cfg := jsonx.NewOrderedMap()
	lp := jsonx.NewOrderedMap()
	for name, mode := range modes {
		spec := jsonx.NewOrderedMap()
		spec.Set("command", []any{os.Args[0], "-front-upstream-child", mode, "{socket}"})
		env := jsonx.NewOrderedMap()
		env.Set("YJ_FRONT_CHILD_DIE", die)
		spec.Set("env", env)
		lp.Set(name, spec)
	}
	cfg.Set("loopholes", lp)
	raw, err := encodeConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestTheKeeperRecordsAHostServiceThatDiesAfterReady is JL-D19's unbuilt half, through the keeper's
// own life: a host service that ends on its own after ready is logged with how it ended and its log,
// a session that was in when it died is shown it at its quit, and the keeper's start record keeps
// it for an arrival. The service the keeper's teardown stops is not recorded down.
func TestTheKeeperRecordsAHostServiceThatDiesAfterReady(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	die := filepath.Join(t.TempDir(), "die")
	var first, other *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		p.Config = configLoopholes(t, map[string]string{"flaky": "dies", "steady": "line"}, die)
		p.Services = []string{"flaky", "steady"}
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		first = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	// A second session, so the first one's quit leaves the jail up and prints what was recorded.
	lock, _, err := takeSessionLock(f.cname)
	if err != nil {
		t.Fatal(err)
	}
	other = lock
	logFrom := keeperLogSize(f.cname)
	if err := os.WriteFile(die, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	log := f.awaitKeeperLog("host service 'flaky' went down")
	for _, want := range []string{"exit status 3", "host-service-flaky.log", "Nothing restarts it"} {
		if !strings.Contains(log, want) {
			t.Errorf("the keeper's record of the death lacks %q:\n%s", want, log)
		}
	}
	rec, ok := readKeeperRecord(f.cname)
	if !ok || len(rec.Down) != 1 || rec.Down[0].What != "host service 'flaky'" ||
		!strings.Contains(rec.Down[0].How, "exit status 3") || rec.Down[0].At.IsZero() ||
		!strings.HasSuffix(rec.Down[0].Log, "host-service-flaky.log") {
		t.Fatalf("the start record does not keep the death for an arrival: %+v (%v)", rec.Down, ok)
	}

	// The first session's quit, while the other is still in: it was in when the service died.
	o := goldenOptions(f.plan.Workspace, t.TempDir())
	var errBuf bytes.Buffer
	o.Stderr = &errBuf
	o.sessionLock = first
	o.Exec = f.jail.exec
	if rc := o.endSession(f.cname, "podman", 0, time.Now(), logFrom, false); rc != 0 {
		t.Errorf("rc %d", rc)
	}
	if !strings.Contains(errBuf.String(), "host service 'flaky' went down") {
		t.Errorf("the quit of a session that was in when the service died did not say so:\n%s", errBuf.String())
	}

	// The last session leaves: the teardown stops 'steady', which is the keeper's own act.
	other.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	if log := f.keeperLog(); strings.Contains(log, "'steady' went down") || strings.Count(log, "went down") != 1 {
		t.Errorf("the keeper recorded a service its own teardown stopped as down:\n%s", log)
	}
}

// TestTheKeeperRecordsNoDeathForADaemonizingWrapper: a host service whose command exits 0 once it
// has handed the service to a child of its own, the daemonizing wrapper waitServiceReady accepts,
// is not down: its service is still there, and an arrival told to stop and relaunch the jail for it
// would be sent to repeat the same start. Nothing is logged or kept in the start record.
func TestTheKeeperRecordsNoDeathForADaemonizingWrapper(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	var first *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		cfg := jsonx.NewOrderedMap()
		lp := jsonx.NewOrderedMap()
		spec := jsonx.NewOrderedMap()
		// The wrapper starts the daemon in the background and exits 0 at once; the daemon, in the
		// wrapper's process group, goes with the keeper's group kill at its teardown.
		spec.Set("command", []any{"sh", "-c", `"$1" -front-upstream-child line "$0" & exit 0`,
			"{socket}", os.Args[0]})
		lp.Set("wrapped", spec)
		cfg.Set("loopholes", lp)
		raw, err := encodeConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		p.Config = raw
		p.Services = []string{"wrapped"}
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		first = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	// The wrapper exited before its service was reachable, so before the watch began: a record of
	// it would already be in the log. A moment's grace for the watch's goroutine all the same.
	time.Sleep(300 * time.Millisecond)
	if log := f.keeperLog(); strings.Contains(log, "went down") {
		t.Errorf("the keeper recorded a daemonizing wrapper's exit as its service going down:\n%s", log)
	}
	if rec, _ := readKeeperRecord(f.cname); len(rec.Down) != 0 {
		t.Errorf("the start record keeps a wrapper's exit for an arrival: %+v", rec.Down)
	}
	first.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
}

// TestTheKeeperRecordsAPortForwardThatDies: a port forward's socat that ends is recorded down too,
// naming both ports and the socat log.
func TestTheKeeperRecordsAPortForwardThatDies(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	die := filepath.Join(t.TempDir(), "die")
	bin := t.TempDir()
	script := `#!/bin/sh
arg="$1"
p="${arg#UNIX-LISTEN:}"
p="${p%%,*}"
: > "$p"
while [ ! -e "` + die + `" ]; do sleep 0.02; done
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "socat"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	var first *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		p.Forwards = []PortForward{{LocalPort: 8080, HostPort: 5432}}
		p.ForwardDir = filepath.Join(t.TempDir(), "yolo-fwd")
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		first = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	if err := os.WriteFile(die, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	log := f.awaitKeeperLog("the port forward from jail port 8080 to host port 5432 went down")
	if !strings.Contains(log, "exit status 1") || !strings.Contains(log, f.cname+"-socat.log") {
		t.Errorf("the forward's record does not say how it ended or where its log is:\n%s", log)
	}
	if rec, _ := readKeeperRecord(f.cname); len(rec.Down) != 1 {
		t.Errorf("the start record keeps %d downs, want 1: %+v", len(rec.Down), rec.Down)
	}
	first.release()
	f.wait()
}

// TestAnArrivalSaysWhichHostServicesItsKeeperRecordedDown is the arrival's half, at attachExisting's
// call site: an entry into a jail whose live keeper recorded a service down says which, since when,
// how, what restarts it and where its log is; an entry into one with none says nothing of it.
func TestAnArrivalSaysWhichHostServicesItsKeeperRecordedDown(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	for _, tc := range []struct {
		name string
		down []keeperServiceDown
	}{
		{"one down", []keeperServiceDown{{What: "host service 'flaky'", At: time.Now(),
			How: "its process ended (exit status 3)", Log: "/state/logs/host-service-flaky.log"}}},
		{"none down", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, currentJailEnv, zaiSelected(t), hydratedKey(), nil)
			live, err := holdLivenessLock(cname)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseLock(live)
			if err := writeKeeperRecord(cname, keeperRecord{PID: 4242, Started: time.Now(), Down: tc.down}); err != nil {
				t.Fatal(err)
			}
			exitWith(t, "0")
			if rc, _ := o.attachExisting(cname, "podman", "true", cfg,
				stagedPacks{root: "/ctx/packs", packs: zaiSelected(t)}, channel, false, nil); rc != 0 {
				t.Fatalf("rc %d:\n%s", rc, stderr.String())
			}
			out := stderr.String()
			if tc.down == nil {
				if strings.Contains(out, "has been down since") {
					t.Errorf("an arrival with nothing down said something was:\n%s", out)
				}
				return
			}
			for _, want := range []string{"Host service 'flaky' of this jail has been down since",
				"its process ended (exit status 3)", "restarts nothing", "'yolo stop' from this workspace",
				"/state/logs/host-service-flaky.log"} {
				if !strings.Contains(out, want) {
					t.Errorf("the arrival's notice lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestEachHostServiceKindReportsItsEnd: every kind of service the keeper runs hands it an end to
// watch, which closes when the service goes: a spawned daemon's process, a host-wide daemon's front,
// a forward's socat. Deleting the assignment at any start site fails it.
func TestEachHostServiceKindReportsItsEnd(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	t.Run("a spawned daemon", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		var buf strings.Builder
		o := &Options{Stdout: &buf}
		fillDefaults(o)
		o.ServiceTermGrace = 250 * time.Millisecond
		die := filepath.Join(t.TempDir(), "die")
		spec := jsonx.NewOrderedMap()
		spec.Set("command", []any{"sh", "-c",
			`: > "{socket}"; while [ ! -e "` + die + `" ]; do sleep 0.02; done; exit 7`})
		h, ok := o.startExternalService("ends", spec, t.TempDir(), "", "", nil)
		if !ok {
			t.Fatalf("the daemon never became reachable: %q", buf.String())
		}
		defer h.stop()
		if h.end.done == nil || !strings.HasSuffix(h.log, "host-service-ends.log") {
			t.Fatalf("a spawned daemon hands the keeper no end to watch, or no log: %+v", h)
		}
		_ = os.WriteFile(die, nil, 0o644)
		select {
		case <-h.end.done:
		case <-time.After(10 * time.Second):
			t.Fatal("the daemon's end never closed")
		}
		if got := h.end.how(); !strings.Contains(got, "exit status 7") {
			t.Errorf("how = %q, want the exit status", got)
		}
	})
	t.Run("a fronted daemon's front", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		socketsDir := t.TempDir()
		if err := os.Chmod(socketsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		var buf strings.Builder
		o := &Options{}
		fillDefaults(o)
		o.Stdout = &buf
		// The wrapper ignores SIGTERM, so the process outlives its front by the whole grace: the
		// front, which the stop closes first, is the end seen.
		o.ServiceTermGrace = time.Second
		spec := jsonx.NewOrderedMap()
		spec.Set("command", []any{"sh", "-c",
			`trap "" TERM; "$1" -front-upstream-child line "$0" & while :; do sleep 0.05; done`,
			"{socket}", os.Args[0]})
		h, ok := o.startExternalService("frontend", spec, socketsDir, loopholes.TransportLoopbackTLS,
			"127.0.0.1", &loopholes.HostDaemon{Publishes: loopholes.PublishesSocket,
				RequestEnd: loopholes.RequestEndFramed})
		if !ok {
			t.Fatalf("the fronted daemon did not come up: %q", buf.String())
		}
		stopped := make(chan struct{})
		go func() { h.stop(); close(stopped) }()
		defer func() { <-stopped }()
		select {
		case <-h.end.done:
		case <-time.After(10 * time.Second):
			t.Fatal("the fronted daemon's end never closed")
		}
		if got := h.end.how(); !strings.Contains(got, "front") {
			t.Errorf("how = %q, want the front's end, which came first", got)
		}
	})
	t.Run("a host-wide daemon's front", func(t *testing.T) {
		name := "yjtest-singleton-end"
		singletonFixture(t, name)
		socketsDir := t.TempDir()
		if err := os.Chmod(socketsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		var buf strings.Builder
		o := &Options{}
		fillDefaults(o)
		o.Stdout = &buf
		h, ok := o.startHostSingleton(name, hostScopedSpec(filepath.Join(t.TempDir(), "x")),
			socketsDir, "127.0.0.1", hostScopedDaemon())
		if !ok {
			t.Fatalf("did not come up: %q", buf.String())
		}
		if h.end.done == nil || h.log == "" {
			t.Fatalf("a host-wide daemon's front hands the keeper no end to watch, or no log: %+v", h)
		}
		h.stop()
		select {
		case <-h.end.done:
		case <-time.After(10 * time.Second):
			t.Fatal("the front's end never closed")
		}
		if !strings.Contains(h.end.how(), "front") {
			t.Errorf("how = %q", h.end.how())
		}
	})
	t.Run("the cgroup delegate", func(t *testing.T) {
		if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
			t.Skip("no cgroup v2 here")
		}
		var buf strings.Builder
		o := &Options{Stdout: &buf}
		fillDefaults(o)
		o.IsMacOS = false
		o.PathExists = func(string) bool { return true }
		h, ok := o.startCgroupDelegate("yolo-ws-cgdend0000", "podman", t.TempDir())
		if !ok {
			t.Skipf("the delegate did not start here: %q", buf.String())
		}
		if h.end.done == nil {
			t.Fatal("the cgroup delegate hands the keeper no end to watch")
		}
		h.stop()
		select {
		case <-h.end.done:
		case <-time.After(10 * time.Second):
			t.Fatal("the delegate's end never closed")
		}
		if !strings.Contains(h.end.how(), "listener") {
			t.Errorf("how = %q", h.end.how())
		}
	})
	t.Run("a port forward", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		fakeSocatOnPath(t)
		o := &Options{}
		fillDefaults(o)
		dir := filepath.Join(t.TempDir(), "yolo-fwd")
		procs := o.startPortForwards([]PortForward{{LocalPort: 8080, HostPort: 8080}}, "x", dir)
		if len(procs) != 1 {
			t.Fatalf("started %d forwards", len(procs))
		}
		_ = procs[0].cmd.Process.Kill()
		end := procs[0].end()
		select {
		case <-end.done:
		case <-time.After(10 * time.Second):
			t.Fatal("the forward's end never closed")
		}
		if !strings.Contains(end.how(), "killed") {
			t.Errorf("how = %q", end.how())
		}
		cleanupPortForwarding(procs, dir)
	})
}

// TestAKeeperWhoseJailNeverStartsRecordsNoServiceDown: a keeper whose container cannot start
// (unwindUnstarted) stops the services it started, and their ends are its own act, never a record.
func TestAKeeperWhoseJailNeverStartsRecordsNoServiceDown(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		p.Config = configLoopholes(t, map[string]string{"steady": "line"}, "")
		p.Services = []string{"steady"}
		p.RunCmd = append([]string{filepath.Join(t.TempDir(), "no-such-runtime")}, p.RunCmd[1:]...)
	})
	if f.relay() {
		t.Fatal("the relay reached ready with no runtime to start the container")
	}
	if rc := f.wait(); rc != 1 {
		t.Errorf("the keeper ended %d, want 1", rc)
	}
	// The stop's end wakes the watch at once; a moment's grace for its goroutine to write.
	time.Sleep(300 * time.Millisecond)
	if log := f.keeperLog(); strings.Contains(log, "went down") {
		t.Errorf("a keeper that never started its jail recorded its own stop of a service as down:\n%s", log)
	}
}

// TestTheKeepersOwnStopIsNeverADeath: once the keeper has begun ending its jail, a service's end is
// its own act, not a record.
func TestTheKeepersOwnStopIsNeverADeath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var logBuf bytes.Buffer
	k := &keeper{sink: &keeperSink{log: &logBuf}, plan: &keeperPlan{Cname: "yolo-own-stop", Runtime: "podman"},
		ending: make(chan struct{})}
	k.beginStopping()
	done := make(chan struct{})
	close(done)
	k.awaitServiceEnd("host service 'x'", serviceEnd{done: done, how: func() string { return "stopped" }}, "")
	if strings.Contains(logBuf.String(), "went down") {
		t.Errorf("a service the keeper stopped was recorded down:\n%s", logBuf.String())
	}
}
