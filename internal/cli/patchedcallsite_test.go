package cli

// patchedcallsite_test.go pins call sites of a PATCHED fork's advance that only its callers reach
// (patchedadvance.go, capturehost.go; docs/design/patched-forks.md §3.4, §7, §8.3, PF-D19, PF-D25,
// PF-D38, PF-D42): a Ctrl-C in the check's fetch ends the check, clears its stamp and starts the jail
// on the good build; a Ctrl-C in the wait for another build's lock ends the wait; `agent_updates` off
// for the fork pack or its base runs no check at a launch, and an edit under that hold is built at
// the good build's commit; and `yolo capture <bin>` in a jail names the host and builds nothing.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// interruptWhen sends this process a SIGINT once ready reports the advance is where the test wants
// it — inside its interrupt scope, so the signal ends the advance and not the test binary.
func interruptWhen(t *testing.T, what string, ready func() bool) {
	t.Helper()
	waitFor(t, what, ready)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
}

// A CTRL-C IN THE CHECK'S FETCH (PF-D25, PF-D38, PF-D42) ends the fetch at once — the scope's context
// reaches the store's git — clears the check stamp so the next launch checks again, and the jail
// starts on the good build.
func TestACtrlCDuringTheChecksFetchStartsTheJailOnTheGoodBuild(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	started := filepath.Join(t.TempDir(), "started")
	patchedGitWrapper(t, "for a in \"$@\"; do if [ \"$a\" = fetch ]; then touch "+shquote.Quote(started)+
		"; sleep 60; fi; done")
	fx.later(2 * time.Hour)
	done := make(chan struct{})
	var got advanceResult
	var out string
	began := time.Now()
	go func() { defer close(done); got, out, _ = fx.launch(t, "podman") }()
	interruptWhen(t, "the check's fetch", func() bool { _, err := os.Stat(started); return err == nil })
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a Ctrl-C in the check's fetch did not end it: the scope's context does not reach the store's git")
	}
	if took := time.Since(began); took > 20*time.Second {
		t.Errorf("the interrupted check took %s", took)
	}
	if got.delivery.Key != r.delivery.Key || !strings.Contains(out, "the advance was interrupted — this jail starts on the good build") {
		t.Errorf("after the Ctrl-C the jail is handed %+v\n%s", got.delivery, out)
	}
	if rec := fx.record(t); rec.CheckedAt != 0 {
		t.Errorf("the interrupted check left its stamp (%d), so the next launch would not check for an hour", rec.CheckedAt)
	}
}

// A CTRL-C WHILE WAITING FOR ANOTHER BUILD'S LOCK (PF-D25) ends the wait at once, records nothing,
// and the jail starts on the good build.
func TestACtrlCWhileWaitingForAnotherBuildStartsTheJailOnTheGoodBuild(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	f := fx.fork(t)
	b := forkBuild{Fork: f, Commit: v13, Platform: patchedTestPlatform, Series: mustSeries(t, fx)}
	holder, err := pidlock.Acquire(b.lockPath(), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	var out syncBuffer
	var got advanceResult
	done := make(chan struct{})
	go func() { defer close(done); got = fx.concurrentLaunch(t, "/ws", &out, nil) }()
	interruptWhen(t, "the wait on the build lock", func() bool { return strings.Contains(out.String(), "waiting for pid") })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		holder.Release() // let the advance end, so the test does
		<-done
		t.Fatal("a Ctrl-C did not end the wait for the build lock: the lock's wait is not the scope's")
	}
	if got.delivery.Key != r.delivery.Key || len(fx.builds) != 1 || !strings.Contains(out.String(), "the advance was interrupted") {
		t.Errorf("after the Ctrl-C the jail is handed %+v after %d builds\n%s", got.delivery, len(fx.builds), out.String())
	}
	for _, o := range fx.record(t).Outcomes {
		if o.Commit == v13 && o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("an interrupted wait was recorded as a failed build: %+v", o)
		}
	}
}

// `agent_updates` OFF HOLDS A PATCHED FORK AT A LAUNCH (PF-D19), through its own pack or its base: no
// check runs — no git at all — past the hour with a new tag published, and the good build is handed;
// an edit under the hold is built at the good build's commit, never at the newer tag.
func TestAgentUpdatesHoldsAPatchedForkAtALaunch(t *testing.T) {
	for _, pack := range []string{"forkpack", "basepack"} {
		t.Run(pack, func(t *testing.T) {
			fx, v11, _, r, _, _ := firstAdvance(t)
			fx.writeUserConfig(t, `,"agent_updates":{"`+pack+`":false}`)
			fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
			logPath := patchedGitWrapper(t, "")
			fx.later(2 * time.Hour)
			held, out, _ := fx.launch(t, "podman")
			if held.delivery.Key != r.delivery.Key || len(fx.builds) != 1 {
				t.Fatalf("the held fork handed %+v after %d builds\n%s", held.delivery, len(fx.builds), out)
			}
			if logged, err := os.ReadFile(logPath); err == nil && len(logged) > 0 {
				t.Errorf("a launch of a held fork ran git:\n%s", logged)
			}
			writeFile(t, fx.manifest, strings.Replace(mustRead(t, fx.manifest), `"build":"sh build.sh"`,
				`"build":"sh build2.sh"`, 1))
			edited, out, _ := fx.launch(t, "podman")
			g := fx.record(t).Good
			if len(fx.builds) != 2 || fx.builds[1] != fx.builds[0] || g.Commit != v11 || g.Entry != edited.delivery.Key ||
				g.Recipe != fx.recipe(t) {
				t.Errorf("the edit under the hold built %q, good build %+v, want v1.1.0 rebuilt\n%s", fx.builds, g, out)
			}
			if strings.Contains(out, "checking fork") {
				t.Errorf("the edit under the hold checked the upstream:\n%s", out)
			}
		})
	}
}

// `yolo capture <bin>` OF A PATCHED FORK IN A JAIL names the host, where its mirror, series and
// record live, and builds nothing.
func TestCaptureOfAPatchedForkInAJailNamesTheHost(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc == 0 || len(fx.builds) != 0 {
		t.Fatalf("an in-jail capture rc=%d after %d builds", rc, len(fx.builds))
	}
	if !strings.Contains(errw.String(), "fork forkpack/tool is a patched fork, checked, replayed and built on the host") ||
		!strings.Contains(errw.String(), "run `yolo capture tool` there") {
		t.Errorf("the in-jail capture does not name the host:\n%s%s", out.String(), errw.String())
	}
}
