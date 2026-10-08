package run

// macosuserkeeperedges_test.go pins the EDGES of the keeper at macos-user
// (docs/design/jail-lifetime-last-session-wins.md §9.9; JL-D40, JL-D41, JL-D44, JL-D86, JL-D89): a
// keeper whose teardown ends before its last session's first look, an arrival while a reaper holds
// a dead keeper's liveness lock, a keeper ending or finishing as a launch arrives, the reap a fresh
// launch makes of a dead keeper's records, a death a session's quit is told while another session
// runs, `yolo stop` of a session no keeper holds, what a dry run says of the keeper, and the
// wording of the lines that name a key's notch and its sessions. Driven as macosuserkeeper_test.go
// drives the keeper: through Run(), the sandbox stubbed and the keeper in-process.

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// waitFor polls cond every few milliseconds until it holds or within passes, and reports which.
func waitFor(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// plantRoster writes a ready roster of key naming pid as its keeper, at this build's contract.
func plantRoster(t *testing.T, ws, key string, pid int, edit func(*keeperRecord)) {
	t.Helper()
	rec := keeperRecord{PID: pid, Started: time.Now(), Workspace: ws, Runtime: "macos-user",
		Contract: keeperRosterContract, Notch: keeperNotchMacosUser, Build: keeperBuildStamp(), Ready: true}
	if edit != nil {
		edit(&rec)
	}
	if err := writeKeeperRecord(key, rec); err != nil {
		t.Fatal(err)
	}
}

// A READY SESSION REPLAYS ITS KEEPER EVEN IF IT FINISHED BEFORE THE QUIT PROBE BEGINS: the
// roster and liveness hold are already gone here, not removed by a racing goroutine. Drive the
// session's real quit, including its deferred second call, so deleting the already-finished
// replay loses the post-ready marker and replaying the whole log repeats the pre-ready line.
func TestEndMacosUserSessionReplaysAKeeperAlreadyGoneBeforeTheProbe(t *testing.T) {
	for _, tc := range []struct {
		name       string
		joined     bool
		spawned    bool
		ready      bool
		wantReplay bool
	}{
		{name: "joined-ready", joined: true, wantReplay: true},
		{name: "spawned-ready", spawned: true, ready: true, wantReplay: true},
		{name: "spawned-before-ready", spawned: true},
		{name: "no-keeper"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packHome(t)
			ws := t.TempDir()
			key := macosUserKeyOf(ws)
			var output lockedBuffer
			o := &Options{Workspace: ws, Stdout: &output, Stderr: &output}
			fillDefaults(o)
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				t.Errorf("quit ran an unexpected stop or reap command: %v", argv)
				return ExecResult{RC: 1}
			}
			m := &macosUserKeying{key: key}
			o.macosUserKey = m
			if tc.joined {
				m.joined = &keeperRecord{PID: 4260, Ready: true}
			}
			if tc.spawned {
				m.kp = &keeperProcess{ready: make(chan struct{})}
				if tc.ready {
					close(m.kp.ready)
				}
			}
			o.holdSessionLock(key)
			if o.sessionLock == nil {
				t.Fatal("fixture could not count its session")
			}
			o.recordKeyedSession(key, tc.wantReplay)
			if m.record == nil {
				t.Fatal("fixture could not record its session")
			}
			t.Cleanup(func() {
				closeKeyedSessionRecord(m.record)
				o.releaseSessionLock()
			})

			const before = "pre-ready line already relayed\n"
			const marker = "yolo: the \"fixture\" service died while the command runs"
			logPath := keeperLogPath(key)
			if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(logPath, []byte(before+marker+"\nkeeper: done\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			m.logFrom = int64(len(before))
			if tc.wantReplay {
				live, err := holdLivenessLock(key)
				if err != nil {
					t.Fatal(err)
				}
				plantRoster(t, ws, key, 4260, nil)
				removeKeeperRecord(key, 4260)
				releaseLock(live)
			}
			if keeperRosterPresent(key) || probeKeeper(key) != keeperGone {
				t.Fatal("fixture keeper must have removed its roster and released liveness before quit")
			}
			// Gone is not an instruction to reap any other state left at this key.
			grantPath := keeperGrantedPath(key)
			if err := os.MkdirAll(filepath.Dir(grantPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(grantPath, []byte("untouched grant\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			start := time.Now()
			if rc := o.endMacosUserSession("macos-user", 7); rc != 7 {
				t.Errorf("quit returned %d, want the command's status 7", rc)
			}
			if took := time.Since(start); took > time.Second {
				t.Errorf("quit of an already-finished keeper took %s; want prompt return", took)
			}
			o.endMacosUserKeying()
			o.endMacosUserSession("macos-user", 7)
			got := output.String()
			if tc.wantReplay {
				if n := strings.Count(got, marker); n != 1 {
					t.Errorf("post-ready marker replayed %d times, want exactly once:\n%s", n, got)
				}
				if n := strings.Count(got, "keeper: done"); n != 1 {
					t.Errorf("completed teardown replayed %d times, want exactly once:\n%s", n, got)
				}
			} else if got != "" {
				t.Errorf("a session with no ready keeper must stay silent:\n%s", got)
			}
			for _, not := range []string{strings.TrimSpace(before), "removing", "stays up", "is gone"} {
				if strings.Contains(got, not) {
					t.Errorf("quit replayed an old line or initiated another teardown (%q):\n%s", not, got)
				}
			}
			if data, err := os.ReadFile(grantPath); err != nil || string(data) != "untouched grant\n" {
				t.Errorf("quit reaped unrelated state: grant=%q error=%v", data, err)
			}
			assertKeyEnded(t, key, "")
		})
	}
}

// A KEEPER CAN END AFTER THE PROBE'S INITIAL LIVENESS CHECK (JL-D40): at macos-user its teardown
// holds no container, so it can take the session lock, end its services and let both locks go before
// the quitting session's next read of the session lock, which then finds nobody. The probe asks the
// liveness lock again on each such read: a keeper gone that removed its record is the last session's
// teardown done (quitLast, whose stream replays the log), and one gone leaving its record died, read
// as a probe begun then reads it, its roster marked ending before the reap (markKeyEnding). Asked
// only at the start, the probe said it could not tell, after its whole wait; deleting the look again,
// or quitAtGoneKeeper's mark, fails this.
func TestAQuitProbeSeesAKeeperThatEndedBeforeItsFirstLook(t *testing.T) {
	packHome(t)
	o := &Options{}
	fillDefaults(o)
	for _, tc := range []struct {
		name        string
		keepsRecord bool
		want        quitState
	}{
		{"teardown-done", false, quitLast},
		{"keeper-died", true, quitUnkeptLast},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := keeperKey("yolo-probe-"+tc.name, keeperNotchMacosUser)
			live, err := holdLivenessLock(key)
			if err != nil {
				t.Fatal(err)
			}
			plantRoster(t, "/ws", key, 4260, nil)
			t.Cleanup(func() { _ = os.Remove(keeperRecordPath(key)) })
			// The keeper ends while the probe is already looking, its session lock never held.
			go func() {
				time.Sleep(150 * time.Millisecond)
				if !tc.keepsRecord {
					removeKeeperRecord(key, 4260)
				}
				releaseLock(live)
			}()
			start := time.Now()
			state, locks := o.probeAfterQuitWithin(key, 5*time.Second)
			took := time.Since(start)
			defer locks.release()
			if state != tc.want {
				t.Fatalf("the probe read %v after %s, want %v", state, took, tc.want)
			}
			if took > 3*time.Second {
				t.Errorf("the probe took %s to see the keeper gone", took)
			}
			if tc.keepsRecord {
				if rec, ok := readKeeperRecord(key); !ok || !rec.Ending {
					t.Errorf("the reap did not mark the dead keeper's roster ending first: %+v (%v)", rec, ok)
				}
			}
		})
	}
}

// AN ARRIVAL WHILE `yolo stop` REAPS AN UNKEPT KEY WAITS FOR THE REAP (JL-D44): the stop holds the dead
// keeper's liveness lock while it waits for the sessions it signalled, so the arrival finds the lock
// held, as it would a keeper's. The stop marks the roster ending first, and the arrival, which reads
// the roster only once it has counted itself, waits instead of joining services that are gone, then
// launches fresh once the reap is done. A session that counted itself while the stop waited is
// signalled too. Deleting reapStoppedKey's markKeyEnding, or its signal of a late session, fails this.
func TestAnArrivalWhileYoloStopReapsAnUnkeptKeyWaitsForTheReap(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	dir, count, record := plantDeadKeeper(t, ws, key, paths.IsMacOS)
	spawns := countKeeperSpawns(t)
	orig := keeperSessionsWait
	keeperSessionsWait = 10 * time.Second
	t.Cleanup(func() { keeperSessionsWait = orig })

	var sawEnding, lateSignalled atomic.Bool
	var arrivalOut *lockedBuffer
	arrivalRC, reached := -1, false
	var lateCount *sessionLock
	var lateRecord *os.File
	var lateOnce sync.Once
	releaseLate := func() {
		lateOnce.Do(func() {
			closeKeyedSessionRecord(lateRecord)
			lateCount.release()
		})
	}
	reaped := make(chan struct{})
	signalsSent(t, map[int]func(syscall.Signal){
		// The session the stop finds: it is let go only once the arrival has been seen waiting.
		4248: func(syscall.Signal) {
			go func() {
				defer close(reaped)
				if !waitFor(10*time.Second, func() bool { rec, ok := readKeeperRecord(key); return ok && rec.Ending }) {
					closeKeyedSessionRecord(record)
					count.release()
					return
				}
				sawEnding.Store(true)
				// A session that counted itself as the stop began waiting, which the stop must end too.
				var err error
				if lateCount, _, err = takeSessionLock(key); err != nil {
					t.Error(err)
				}
				if lateRecord, err = openKeyedSessionRecord(key, 4249, time.Now(), true); err != nil {
					t.Error(err)
				}
				o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
				arrivalOut = out
				done := make(chan struct{})
				go func() {
					defer close(done)
					arrivalRC = Run(*o)
				}()
				waitFor(10*time.Second, func() bool {
					return strings.Contains(out.String(), "still shutting down; waiting for")
				})
				closeKeyedSessionRecord(record)
				count.release()
				// The late session, unless the stop ends it, holds the count until the stop gives up.
				waitFor(3*time.Second, func() bool { return lateSignalled.Load() })
				releaseLate()
				<-done
			}()
		},
		4249: func(syscall.Signal) {
			lateSignalled.Store(true)
			releaseLate()
		},
	})
	var stopOut lockedBuffer
	stopRC := StopMacosUser(&stopOut, &stopOut, ws)
	<-reaped
	t.Cleanup(releaseLate)
	if !sawEnding.Load() {
		t.Fatalf("while the stop held the dead keeper's liveness lock, its roster was never marked ending\nstop:\n%s",
			stopOut.String())
	}
	if stopRC != 0 || !lateSignalled.Load() {
		t.Errorf("the stop returned %d and signalled the late session %v; want 0 and the late session "+
			"signalled\n%s", stopRC, lateSignalled.Load(), stopOut.String())
	}
	if arrivalRC != 0 || !reached {
		t.Fatalf("the arrival returned %d (reached %v), want it launched once the reap was done\n%s",
			arrivalRC, reached, arrivalOut.String())
	}
	if strings.Contains(arrivalOut.String(), "keeper: joined") {
		t.Errorf("an arrival during the reap joined the dead keeper:\n%s", arrivalOut.String())
	}
	if n := spawns.Load(); n != 0 {
		t.Errorf("%d keepers were spawned; the arrival plans nothing a keeper holds", n)
	}
	assertKeyEnded(t, key, dir)
}

// AN ARRIVAL AT A KEEPER ENDING ITS SERVICES, OR FINISHING, WAITS FOR IT (JL-D28 at macos-user): a
// live keeper whose roster is marked ending, or is already gone (a keeper removes it just before it
// lets its liveness lock go), is waited for with the arrival lock released, and the launch then
// starts fresh; a roster that is there and cannot be read is refused, naming `yolo stop`. Deleting
// arriveMacosUser's ending check, or its tell of a missing roster from an unreadable one, fails this.
func TestAnArrivalWaitsForAKeeperThatIsEndingOrFinishing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		roster func(t *testing.T, ws, key string)
		waits  bool
	}{
		{"ending", func(t *testing.T, ws, key string) {
			plantRoster(t, ws, key, 4261, func(r *keeperRecord) { r.Ending = true })
		}, true},
		{"finishing", func(*testing.T, string, string) {}, true},
		{"unreadable", func(t *testing.T, _, key string) {
			if err := os.MkdirAll(filepath.Dir(keeperRecordPath(key)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(keeperRecordPath(key), []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserConfigJSON(t, home, `{"packs": []}`)
			ws := t.TempDir()
			key := macosUserKeyOf(ws)
			live, err := holdLivenessLock(key)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			end := func() {
				once.Do(func() {
					_ = os.Remove(keeperRecordPath(key))
					releaseLock(live)
				})
			}
			t.Cleanup(end)
			tc.roster(t, ws, key)
			reached := false
			o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
			rc := -1
			done := make(chan struct{})
			go func() {
				defer close(done)
				rc = Run(*o)
			}()
			if tc.waits {
				if !waitFor(10*time.Second, func() bool {
					return strings.Contains(out.String(), "still shutting down; waiting for")
				}) {
					end()
					<-done
					t.Fatalf("the arrival did not wait for the keeper (rc %d):\n%s", rc, out.String())
				}
				end()
				<-done
				if rc != 0 || !reached || strings.Contains(out.String(), "keeper: joined") {
					t.Errorf("Run() = %d (reached %v); want the launch fresh once the keeper was gone:\n%s",
						rc, reached, out.String())
				}
				return
			}
			<-done
			if rc != 1 || reached {
				t.Fatalf("Run() = %d (reached %v), want the unreadable roster's refusal\n%s", rc, reached, out.String())
			}
			for _, want := range []string{"and its roster " + keeperRecordPath(key) + " cannot be read.",
				"'yolo stop' from this workspace ends it, and the next launch starts fresh."} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("the refusal must say %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// A FRESH ARRIVAL REMOVES WHAT A DEAD KEEPER LEFT (JL-D44): a keeper that died with no session left
// leaves its roster and host-services dir, and the next launch, finding no keeper and no count, is
// the fresh launch and removes them before anything else. Deleting arriveMacosUser's reapKeyRecords
// call fails this.
func TestAFreshMacosUserArrivalRemovesWhatADeadKeeperLeft(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	dir, count, record := plantDeadKeeper(t, ws, key, false)
	// No session is left of it.
	closeKeyedSessionRecord(record)
	count.release()
	var rosterLeft, dirLeft bool
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int {
		_, rosterLeft = readKeeperRecord(key)
		dirLeft = fileExists(dir)
		return 0
	})
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, out.String())
	}
	if rosterLeft || dirLeft {
		t.Errorf("the fresh launch's session ran beside the dead keeper's roster (%v) or its host-services "+
			"dir %s (%v)\n%s", rosterLeft, dir, dirLeft, out.String())
	}
}

// A SESSION'S QUIT IS TOLD WHAT THE KEEPER RECORDED WHILE IT WAS IN, EVEN WHEN IT IS NOT THE LAST
// (JL-D19 at macos-user): a held service dies while two sessions run, and the first to quit, the
// keeper's services staying up for the other, prints the death from the keeper's log beside its
// stay-up line. Deleting endMacosUserSession's printKeeperRecords at a quit that leaves others fails
// this.
func TestAQuitThatLeavesOthersIsToldOfAServiceThatDied(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, keeperBridgeConfig)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	_, held := observeHeldServices(t)
	bIn, releaseB, bDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oB, outB := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int {
		close(bIn)
		<-releaseB
		return 0
	})
	oB.Args, oB.ProfileName = []string{"claude"}, "cerebras"
	rcB := -1
	oA, outA := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int {
		go func() {
			defer close(bDone)
			rcB = Run(*oB)
		}()
		select {
		case <-bIn:
		case <-bDone:
			return 0
		case <-time.After(30 * time.Second):
			return 0
		}
		close(held.done)
		waitFor(5*time.Second, func() bool { rec, ok := readKeeperRecord(key); return ok && len(rec.Down) > 0 })
		return 0
	})
	oA.Args, oA.ProfileName = []string{"claude"}, "cerebras"
	rcA := Run(*oA)
	close(releaseB)
	<-bDone
	if rcA != 0 || rcB != 0 {
		t.Fatalf("Run() = %d, %d\nA:\n%s\nB:\n%s", rcA, rcB, outA.String(), outB.String())
	}
	if !strings.Contains(outA.String(), "This workspace's macos-user host services stay up (keeper pid ") {
		t.Fatalf("the first to quit did not leave the services up for the other session:\n%s", outA.String())
	}
	if !strings.Contains(outA.String(), `keeper: the "wire-bridge" service's host half went down at `) {
		t.Errorf("the first to quit must be told of the death the keeper recorded while it was in:\n%s", outA.String())
	}
	assertKeyEnded(t, key, "")
}

// A KEEPER THAT SAID READY WITHOUT A ROSTER THE LAUNCH CAN READ REFUSES IT, NAMING THE NEXT STEP: the
// launch counted itself, so its quit ends the keeper it started, and the refusal says to launch again
// and which command ends a keeper that still holds the workspace. The roster is removed as the ready
// frame crosses. Deleting the refusal's remedy line fails this.
func TestAKeeperReadyWithoutARosterRefusesTheLaunchSayingWhatToDo(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	keeperInProcess(t, inProcessSpec{onFrame: func(tag byte) {
		if tag == frameReady {
			_ = os.Remove(keeperRecordPath(key))
		}
	}})
	reached := false
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
	if rc := Run(*o); rc != 1 || reached {
		t.Fatalf("Run() = %d (reached %v), want the refusal\n%s", rc, reached, out.String())
	}
	for _, want := range []string{"Refusing the macos-user launch: its keeper said ready, and its roster ",
		"Launch again: this launch's quit ends the keeper it started when no other session joined it; if one " +
			"still holds this workspace's macos-user host services, 'yolo stop' from this workspace ends it."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the launch must say %q:\n%s", want, out.String())
		}
	}
	assertKeyEnded(t, key, "")
}

// `yolo stop` ENDS A SESSION NO KEEPER HOLDS TOO (JL-D44, JL-D42): a launch that planned nothing
// outside the sandbox runs with no keeper and no count, and its record names it, so the stop signals
// it and says it stopped only once the session has quit. Deleting recordKeeperlessSession's call,
// or the stop's wait for the sessions it signalled, fails this.
func TestYoloStopEndsAMacosUserSessionNoKeeperHolds(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	const sessionPID = 4262
	ends, quit := make(chan struct{}), make(chan struct{})
	var once sync.Once
	signalsSent(t, map[int]func(syscall.Signal){
		sessionPID: func(syscall.Signal) { once.Do(func() { close(ends) }) },
	})
	var stopOut lockedBuffer
	stopRC := -1
	stopped := make(chan struct{})
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int {
		go func() {
			defer close(stopped)
			stopRC = StopMacosUser(&stopOut, &stopOut, ws)
		}()
		select {
		case <-ends:
		case <-time.After(10 * time.Second):
			return 0
		}
		// The session takes a moment to end after its signal; the stop waits for it.
		time.Sleep(200 * time.Millisecond)
		select {
		case <-stopped:
			t.Error("the stop returned before the session it signalled had quit")
		default:
		}
		close(quit)
		return 143
	})
	o.Getpid = func() int { return sessionPID }
	rc := Run(*o)
	select {
	case <-stopped:
	case <-time.After(30 * time.Second):
		t.Fatalf("`yolo stop` did not return\nstop:\n%s\nlaunch:\n%s", stopOut.String(), out.String())
	}
	select {
	case <-quit:
	default:
		t.Fatalf("the stop never signalled the session (rc %d)\nstop:\n%s\nlaunch:\n%s", rc, stopOut.String(), out.String())
	}
	if rc != 143 || stopRC != 0 {
		t.Fatalf("Run() = %d, stop = %d, want 143 and 0\nstop:\n%s", rc, stopRC, stopOut.String())
	}
	for _, want := range []string{"sent SIGTERM to a session (pid 4262)", "Stopped this workspace's macos-user sessions."} {
		if !strings.Contains(stopOut.String(), want) {
			t.Errorf("the stop must say %q:\n%s", want, stopOut.String())
		}
	}
	if strings.Contains(stopOut.String(), "Nothing to stop") {
		t.Errorf("the stop said nothing was running beside a session:\n%s", stopOut.String())
	}
	assertKeyEnded(t, macosUserKeyOf(ws), "")
}

// A DRY RUN SAYS WHAT THE KEEPER WOULD HOLD (§9.9.7, JL-D41): with no keeper running, each service and
// doorway it would start is named as held by the keeper this launch would start, until the
// workspace's last session leaves, and then that keeper's line, all of it starting nothing. Deleting
// Run's noteMacosUserKeeperDryRun call fails this.
func TestAMacosUserDryRunSaysTheKeeperItWouldStartHoldsWhat(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, keeperBridgeConfig)
	ws := t.TempDir()
	spawns := countKeeperSpawns(t)
	starts, _ := observeHeldServices(t)
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { return 0 })
	o.Args, o.ProfileName = []string{"claude"}, "cerebras"
	o.DryRun = true
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run(--dry-run) = %d\n%s", rc, out.String())
	}
	if spawns.Load() != 0 || starts.Load() != 0 {
		t.Errorf("a dry run spawned %d keepers and started %d services", spawns.Load(), starts.Load())
	}
	for _, want := range []string{
		`Would start the "wire-bridge" service (pack "wire-bridge") on [`,
		"] for this launch's keeper to hold, outside the sandbox, until this workspace's last macos-user session leaves.",
		"Would start: keeper: yolo internal daemon jail-keeper will hold this workspace's macos-user host services (",
		"the wire-bridge service's host half) until its last macos-user session leaves; log: ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the dry run must say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "until the command exits") {
		t.Errorf("the dry run says a service ends with the command, which the keeper holds:\n%s", out.String())
	}
}

// A DRY RUN BESIDE A LIVE KEEPER DESCRIBES THE JOIN (§9.9.7): it reads the keeper's roster as the
// launch would, so it names the keeper it would join and what that keeper holds, on the keeper's own
// addresses, and composes the plan against the keeper's tokens and addresses rather than ones picked
// for nothing; it starts nothing and spawns no keeper. Deleting peekMacosUserKey's join fails this.
func TestAMacosUserDryRunBesideALiveKeeperDescribesTheJoin(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("spawns a host daemon")
	}
	home := packHome(t)
	writeKeeperProxy(t, home)
	writeUserConfigJSON(t, home, keeperBridgeConfig)
	ws := t.TempDir()
	spawns := countKeeperSpawns(t)
	starts, _ := observeHeldServices(t)
	type seen struct{ endpoint, url, token string }
	look := func(env *jsonx.OrderedMap) seen {
		return seen{envString(env, hostServiceEnvVar(keeperProxy)), envString(env, "ANTHROPIC_BASE_URL"),
			envString(env, "ANTHROPIC_AUTH_TOKEN")}
	}
	var a, b seen
	oB, outB := keeperLaunch(t, ws, func(env *jsonx.OrderedMap) int { b = look(env); return 0 })
	oB.Args, oB.ProfileName = []string{"claude"}, "cerebras"
	oB.DryRun = true
	rcB := -1
	oA, outA := keeperLaunch(t, ws, func(env *jsonx.OrderedMap) int {
		a = look(env)
		rcB = Run(*oB)
		return 0
	})
	oA.Args, oA.ProfileName = []string{"claude"}, "cerebras"
	if rc := Run(*oA); rc != 0 || rcB != 0 {
		t.Fatalf("Run() = %d, Run(--dry-run) = %d\nA:\n%s\nB:\n%s", rc, rcB, outA.String(), outB.String())
	}
	if spawns.Load() != 1 || starts.Load() != 1 {
		t.Errorf("%d keepers spawned and %d services started, want the first launch's one each\nB:\n%s",
			spawns.Load(), starts.Load(), outB.String())
	}
	if a.url == "" || a != b {
		t.Errorf("the dry run's plan names other host services than the keeper's:\nkeeper's %+v\ndry run's %+v", a, b)
	}
	for _, want := range []string{"Would join: keeper: joined yolo internal daemon jail-keeper (pid ",
		`Held by the keeper (pid `, `the "wire-bridge" service (pack "wire-bridge") on `} {
		if !strings.Contains(outB.String(), want) {
			t.Errorf("the dry run must say %q:\n%s", want, outB.String())
		}
	}
	for _, not := range []string{"Would start the ", "Would start: keeper:"} {
		if strings.Contains(outB.String(), not) {
			t.Errorf("a dry run beside a live keeper says %q:\n%s", not, outB.String())
		}
	}
	assertKeyEnded(t, macosUserKeyOf(ws), "")
}

// A DRY RUN AT A KEY THE LAUNCH WOULD BE REFUSED AT IS REFUSED (§9.9.7): its keeper is dead and a
// session runs on, so the plan would describe a launch that cannot happen, and the dry run says what
// the launch would, `yolo stop` included, and removes nothing. Deleting peekMacosUserKey's refusal
// fails this.
func TestAMacosUserDryRunAtAnUnkeptKeyIsRefused(t *testing.T) {
	home := packHome(t)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	ws := t.TempDir()
	key := macosUserKeyOf(ws)
	dir, _, _ := plantDeadKeeper(t, ws, key, false)
	reached := false
	o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { reached = true; return 0 })
	o.DryRun = true
	if rc := Run(*o); rc != 1 || reached {
		t.Fatalf("Run(--dry-run) = %d (reached %v), want the unkept key's refusal\n%s", rc, reached, out.String())
	}
	for _, want := range []string{"Refusing to launch: this workspace's macos-user host services are gone",
		"'yolo stop' from this workspace ends them"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the dry run must say %q:\n%s", want, out.String())
		}
	}
	if _, ok := readKeeperRecord(key); !ok || !fileExists(dir) {
		t.Error("a dry run removed what the dead keeper left")
	}
}

// A DRY RUN AT A KEEPER WHOSE ROSTER THIS BUILD CANNOT READ IS REFUSED (§9.9.7, JL-D45), as the
// launch is, rather than describing a join it would not make; and one at a keeper that is ending says
// it would wait for it. Deleting peekMacosUserKey's contract check, or its wait note, fails this.
func TestAMacosUserDryRunAtAKeeperItCannotJoinSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*keeperRecord)
		rc   int
		want string
	}{
		{"unknown-contract", func(r *keeperRecord) { r.Contract, r.Build = keeperRosterContract+98, "99.0.0@future" }, 1,
			"held by a keeper (pid 4264) of yolo 99.0.0@future, whose roster (contract 99)"},
		{"ending", func(r *keeperRecord) { r.Ending = true }, 0,
			"Would wait for its previous keeper (pid 4264) to finish ending this workspace's macos-user host services first."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserConfigJSON(t, home, `{"packs": []}`)
			ws := t.TempDir()
			key := macosUserKeyOf(ws)
			live, err := holdLivenessLock(key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { releaseLock(live) })
			plantRoster(t, ws, key, 4264, tc.edit)
			o, out := keeperLaunch(t, ws, func(*jsonx.OrderedMap) int { return 0 })
			o.DryRun = true
			if rc := Run(*o); rc != tc.rc {
				t.Fatalf("Run(--dry-run) = %d, want %d\n%s", rc, tc.rc, out.String())
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("the dry run must say %q:\n%s", tc.want, out.String())
			}
			if strings.Contains(out.String(), "Would join") {
				t.Errorf("the dry run would join a keeper the launch cannot:\n%s", out.String())
			}
		})
	}
}

// THE KEEPER'S LINES NAME THE KEY'S NOTCH (JL-D41): a guest-notch launch's key is its own (JL-D37),
// and its keeper and joined lines say so; a jail-notch launch's say macos-user alone.
func TestTheKeeperLinesNameTheNotch(t *testing.T) {
	packHome(t)
	for _, tc := range []struct {
		notch      config.Confinement
		services   string
		guestNamed bool
	}{
		{config.ConfinementJail, "this workspace's macos-user host services", false},
		{config.ConfinementGuest, "this workspace's guest-notch macos-user host services", true},
	} {
		o := &Options{}
		fillDefaults(o)
		o.atNotch = tc.notch
		key := keeperKey("yolo-notch-0000aaaa", o.macosUserKeeperNotch())
		start := o.macosUserKeeperLine(key, &keeperPlan{Services: []string{"x"}})
		joined := o.joinedKeeperLine(key, keeperRecord{PID: 4263})
		for _, line := range []string{start, joined} {
			if !strings.Contains(line, tc.services) || strings.Contains(line, "guest") != tc.guestNamed {
				t.Errorf("at the %s notch the keeper line %q does not name %q alone", tc.notch, line, tc.services)
			}
		}
		if tc.guestNamed && !strings.Contains(start, "until its last guest-notch macos-user session leaves") {
			t.Errorf("the guest notch's keeper line does not name the notch's sessions: %q", start)
		}
	}
}

// A LINE NAMING A KEY'S SESSIONS AGREES WITH THEM: one session takes the singular verb, two the
// plural, and sessions none of which recorded itself are named as such.
func TestSessionsThatAgreeWithTheirCount(t *testing.T) {
	at := time.Date(2026, 10, 5, 21, 14, 3, 0, time.Local)
	one := []keyedSession{{PID: 4248, Started: at}}
	two := []keyedSession{{PID: 4248, Started: at}, {PID: 4249, Started: at}}
	for _, tc := range []struct {
		sessions []keyedSession
		want     string
	}{
		{nil, "sessions yolo cannot name still run"},
		{one, "1 session (pid 4248, since 21:14:03) still runs"},
		{two, "2 sessions (pid 4248, since 21:14:03; pid 4249, since 21:14:03) still run"},
	} {
		if got := sessionsThat(tc.sessions, "still runs", "still run"); got != tc.want {
			t.Errorf("sessionsThat(%d) = %q, want %q", len(tc.sessions), got, tc.want)
		}
	}
}
