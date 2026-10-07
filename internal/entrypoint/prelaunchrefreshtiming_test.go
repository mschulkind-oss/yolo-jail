package entrypoint

// prelaunchrefreshtiming_test.go RUNS the pre-launch refresh's BACKGROUND arm
// (REFRESH_TIMING=next-launch; docs/design/program-delivery.md OQ-PD30, OQ-PD31): the launch
// starts the refresh as a detached job and execs the program without waiting, and the job runs
// the act under the launch path's own lock, bound and stamp.
//
// Every cell drives prelaunchrefresh_test.go's fake program through the production generators,
// with this test binary as the jail's `yolo` (yoloStandIn), so the detach and the bound are the
// real notty.Main's. No agent is started and nothing reaches a network (AGENTS.md, "No agent
// tests"). None needs a terminal, so macOS's check runs them all, under its /bin/bash 3.2.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const nextLaunch = "next-launch"

// refreshTemplates is every launcher template the refresh is spliced into. The fork's source
// launcher is the one a patched pi runs (forklauncher.go).
var refreshTemplates = []struct {
	name         string
	native, fork bool
}{{"npm", false, false}, {"native", true, false}, {"fork", true, true}}

// newTimingProbe is a probe whose refresh runs in the background, with this test binary as yolo.
func newTimingProbe(t *testing.T, native, fork bool) *prelaunchProbe {
	t.Helper()
	p := newPrelaunchProbe(t, native)
	p.fork, p.timing = fork, nextLaunch
	p.path = yoloStandIn(t) + ":" + pathWithout(t, "yolo")
	return p
}

func (p *prelaunchProbe) jobLog() string {
	return filepath.Join(p.home, ".local", "state", "yolo", "refresh", "tool.log")
}

func (p *prelaunchProbe) jobResult() string {
	return filepath.Join(p.home, ".local", "state", "yolo", "refresh", "tool.result")
}

// jobEnds counts the jobs a log says have ended: each job writes exactly one of these lines last.
func jobEnds(log string) int {
	n := 0
	for _, l := range strings.Split(log, "\n") {
		for _, end := range []string{"background refresh ended (status", "this one stops.",
			"nothing to do.", "--- cannot take the refresh lock"} {
			if strings.Contains(l, end) {
				n++
				break
			}
		}
	}
	return n
}

// waitForJobs waits until p's job log says n jobs have ended, and returns the log. Every cell
// that starts a job waits for it, so none outlives the test's temporary directory.
func waitForJobs(t *testing.T, p *prelaunchProbe, n int) string {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for {
		got, _ := os.ReadFile(p.jobLog())
		if jobEnds(string(got)) >= n {
			return string(got)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the background job never ended (want %d endings); its log:\n%s", n, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mustAppear is waitForPath (updatebound_test.go) that fails the cell, naming what never happened.
func mustAppear(t *testing.T, path, what string) {
	t.Helper()
	if !waitForPath(t, path, 30*time.Second) {
		t.Fatalf("%s never happened (%s)", what, path)
	}
}

// TestNextLaunchRefreshRunsBehindTheLaunch is the arm's whole claim, in all three templates: the
// launch execs the program while the refresh is still running, and the refresh then finishes on
// its own — under the lock, stamped, its output in the job's log and none of it in the launch's
// stdout or stderr, its stdin empty. The refresh blocks until the cell releases it, so a launcher
// that ran it in front (the background arm's call site deleted) would still be inside the refresh
// when the release is written, and this cell would see the launch take the fake's whole 20 s.
func TestNextLaunchRefreshRunsBehindTheLaunch(t *testing.T) {
	for _, tc := range refreshTemplates {
		t.Run(tc.name, func(t *testing.T) {
			p := newTimingProbe(t, tc.native, tc.fork)
			release := filepath.Join(p.home, "release")
			t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })
			begun := time.Now()
			stdout, stderr := p.run(t, "", "FAKE_REFRESH_WAIT="+release)
			if took := time.Since(begun); took > 15*time.Second {
				t.Errorf("the launch took %s: it waited for the refresh, or something the job "+
					"holds kept its output open", took)
			}
			if _, err := os.Stat(release); err == nil {
				t.Fatal("the release was written before the launch returned")
			}
			assertProgramLaunched(t, p.logLines(t), stdout)
			if n := strings.Count(stderr, "refreshing in the background"); n != 1 ||
				!strings.Contains(stderr, p.jobLog()) {
				// Fatal: with no job started there is nothing below to wait for.
				t.Fatalf("the launch must say once that the refresh runs in the background, "+
					"naming its log %s:\n%s", p.jobLog(), stderr)
			}

			// The refresh is running now, behind the program, and holds the lock.
			mustAppear(t, release+".started", "the background refresh starting")
			if _, err := os.Stat(p.lockPath()); err != nil {
				t.Errorf("the running background refresh must hold the lock: %v", err)
			}
			if _, err := os.Stat(p.stampPath()); !os.IsNotExist(err) {
				t.Errorf("the stamp was written before the refresh ended (err=%v)", err)
			}
			if err := os.WriteFile(release, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			jobLog := waitForJobs(t, p, 1)

			log := p.logLines(t)
			if countLine(log, "REFRESH") != 1 || countLine(log, "LOCKED") != 1 ||
				!hasExactArg(log, "ARG:--extensions") {
				t.Errorf("the background refresh must run the declared argv once, under the lock:\n%v", log)
			}
			for _, l := range log {
				if strings.HasPrefix(l, "STDIN:") {
					t.Errorf("the background refresh read %q: its stdin must be empty", l)
				}
			}
			if _, err := os.Stat(p.stampPath()); err != nil {
				t.Errorf("the background refresh must stamp when it ends: %v", err)
			}
			if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
				t.Errorf("the background refresh must release the lock (err=%v)", err)
			}
			if !strings.Contains(jobLog, "REFRESH-STDOUT") || !strings.Contains(jobLog, "ended (status 0)") {
				t.Errorf("the refresh's output and its ending belong in the job's log:\n%s", jobLog)
			}
			if strings.Contains(stdout, "REFRESH-STDOUT") || strings.Contains(stderr, "REFRESH-STDOUT") {
				t.Errorf("the background refresh wrote into the launch:\nstdout=%q\nstderr=%q", stdout, stderr)
			}
			if _, err := os.Stat(p.jobResult()); !os.IsNotExist(err) {
				t.Errorf("a refresh that succeeded must leave nothing for the next launch to say (err=%v)", err)
			}
		})
	}
}

// TestNextLaunchIsThrottledAndNeverRunsTwoAtOnce: a launch while the job runs starts no second
// job (the live lock says one is running, and the launch says so), and once the job has stamped,
// a launch inside the hour says nothing about a refresh at all.
func TestNextLaunchIsThrottledAndNeverRunsTwoAtOnce(t *testing.T) {
	p := newTimingProbe(t, false, false)
	release := filepath.Join(p.home, "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })
	p.run(t, "", "FAKE_REFRESH_WAIT="+release)
	mustAppear(t, release+".started", "the background refresh starting")

	stdout, stderr := p.run(t, "")
	if !strings.Contains(stderr, "another refresh holds") || strings.Contains(stderr, "in the background") {
		t.Errorf("a launch while the job runs must start no second one, and say why:\n%s", stderr)
	}
	// The job's provisional "did not finish" is on disk now, carrying the token its live lock
	// holds: a second terminal of the same jail must not say it while the job runs.
	if strings.Contains(stderr, "did not finish") {
		t.Errorf("a launch while the job runs must not report it as unfinished:\n%s", stderr)
	}
	if !strings.Contains(stdout, "RAN") {
		t.Errorf("the second launch must still run the program: %q", stdout)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	jobLog := waitForJobs(t, p, 1)
	if n := strings.Count(jobLog, ": background refresh ("); n != 1 {
		t.Errorf("one job must have run, the log shows %d:\n%s", n, jobLog)
	}
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Errorf("the refresh ran %d times; two launches inside one job's run must refresh once", n)
	}

	_, stderr = p.run(t, "")
	if strings.Contains(strings.ToLower(stderr), "refresh") {
		t.Errorf("a launch inside the hour must say nothing about a refresh:\n%s", stderr)
	}
}

// TestTheBackgroundJobNeverWaitsAndNeverRepeats drives the JOB'S DISPATCH directly — the launcher
// re-entered with _YOLO_REFRESH_JOB, as the detach starts it — through the three answers it can
// have: a lock another holds (it stops, at once), a refresh another launch finished since the job
// was started (it releases the lock and does nothing), and a refresh still due (it runs it). In no
// case does the job run the program: it ends where the launch path would go on to the exec.
func TestTheBackgroundJobNeverWaitsAndNeverRepeats(t *testing.T) {
	p := newTimingProbe(t, false, false)
	job := "_YOLO_REFRESH_JOB=tool"

	if err := os.Mkdir(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	begun := time.Now()
	stdout, _ := p.run(t, "", job)
	if took := time.Since(begun); took > 10*time.Second {
		t.Errorf("the job waited %s on a held lock; it must stop at once", took)
	}
	if !strings.Contains(stdout, "this one stops") || countLine(p.logLines(t), "REFRESH") != 0 {
		t.Errorf("a job that finds the lock held must stop without refreshing:\n%s\n%v", stdout, p.logLines(t))
	}
	if _, err := os.Stat(p.lockPath()); err != nil {
		t.Errorf("the holder's lock must be left alone: %v", err)
	}

	if err := os.Remove(p.lockPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.stampPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.stampPath(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _ = p.run(t, "", job)
	if !strings.Contains(stdout, "nothing to do") || countLine(p.logLines(t), "REFRESH") != 0 {
		t.Errorf("a job whose refresh another launch has since finished must do nothing:\n%s", stdout)
	}
	if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
		t.Errorf("a job with nothing to do must release the lock it took (err=%v)", err)
	}

	backdatePath(t, p.stampPath(), 2*time.Hour)
	stdout, _ = p.run(t, "", job)
	log := p.logLines(t)
	if countLine(log, "REFRESH") != 1 || countLine(log, "LOCKED") != 1 || !strings.Contains(stdout, "ended (status 0)") {
		t.Errorf("a job whose refresh is due must run it under the lock:\n%s\n%v", stdout, log)
	}
	if countLine(log, "LAUNCH:") != 0 || strings.Contains(stdout, "RAN") {
		t.Errorf("the job must never run the program: %v", log)
	}
}

// TestNextLaunchKeepsANewInstallInFront: when the watched settings name content no refresh has
// succeeded for, the launch is about to install what they name, and that stays in front of the
// program whatever the timing (OQ-PD31) — the first-install race DueOnChange exists to close. Once
// that content is seen, the hourly refresh goes to the background.
func TestNextLaunchKeepsANewInstallInFront(t *testing.T) {
	p := newTimingProbe(t, false, false)
	p.refresh.DueOnChange = []string{".tool/settings.json"}
	if err := os.MkdirAll(filepath.Join(p.home, ".tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.home, ".tool", "settings.json"), []byte(`{"packages":["a"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := p.run(t, "")
	log := p.logLines(t)
	want := []string{"REFRESH", "ARG:update", "ARG:--extensions", "LOCKED", "LAUNCH:"}
	if strings.Join(log, "\n") != strings.Join(want, "\n") || strings.Contains(stderr, "in the background") {
		t.Errorf("new settings content must be refreshed BEFORE the launch:\n got %q\nwant %q\n%s", log, want, stderr)
	}
	assertProgramLaunched(t, log, stdout)
	if _, err := os.Stat(p.jobLog()); !os.IsNotExist(err) {
		t.Errorf("no job may have been started for it (err=%v)", err)
	}

	backdatePath(t, p.stampPath(), 2*time.Hour)
	_, stderr = p.run(t, "")
	if !strings.Contains(stderr, "refreshing in the background") {
		t.Errorf("with the content seen, the hourly refresh must go to the background:\n%s", stderr)
	}
	waitForJobs(t, p, 1)
}

// TestAFailedBackgroundRefreshIsSaidOnceAtTheNextLaunch: the job cannot speak to the terminal, so
// a refresh that failed leaves one line for the NEXT launch, naming the status, the log and how to
// retry now; that launch says it once and the one after says nothing. The stamp still throttles:
// the failed job stamped, as the launch path does.
func TestAFailedBackgroundRefreshIsSaidOnceAtTheNextLaunch(t *testing.T) {
	p := newTimingProbe(t, false, false)
	p.run(t, "", "FAKE_REFRESH_RC=3")
	waitForJobs(t, p, 1)
	if _, err := os.Stat(p.jobResult()); err != nil {
		t.Fatalf("a failed background refresh must leave its result for the next launch: %v", err)
	}
	if _, err := os.Stat(p.stampPath()); err != nil {
		t.Errorf("a failed background refresh must still stamp (§4.1 invariant 3): %v", err)
	}

	stdout, stderr := p.run(t, "")
	for _, want := range []string{"⚠ tool: the background refresh (update --extensions) failed (status 3)",
		p.jobLog(), "to retry now, run: tool update --extensions"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the next launch must say %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "background refresh") {
		t.Errorf("the report belongs on stderr, never in the program's stdout: %q", stdout)
	}
	if countLine(p.logLines(t), "LAUNCH:") != 2 || !strings.Contains(stdout, "RAN") {
		t.Errorf("the launch that reports must still run the program: %v %q", p.logLines(t), stdout)
	}
	if strings.Contains(stderr, "refreshing in the background") {
		t.Errorf("inside the hour the failed refresh must not be retried:\n%s", stderr)
	}

	_, stderr = p.run(t, "")
	if strings.Contains(stderr, "background refresh") {
		t.Errorf("the failure must be said once, not on every launch:\n%s", stderr)
	}
}

// TestABackgroundRefreshThatTimesOutIsSaidAtTheNextLaunch: a job whose act outlives the bound
// stamps, like any other outcome, and leaves the timeout for the next launch to say once — the
// bound, the log and the way to retry — and the launch after says nothing.
func TestABackgroundRefreshThatTimesOutIsSaidAtTheNextLaunch(t *testing.T) {
	p := newTimingProbe(t, false, false)
	p.bodyPatch = map[string]string{"\nUPDATE_TIMEOUT=60 ": "\nUPDATE_TIMEOUT=1 ", "\nUPDATE_GRACE=5\n": "\nUPDATE_GRACE=1\n"}
	p.run(t, "", "FAKE_REFRESH_WAIT="+filepath.Join(p.home, "never"))
	jobLog := waitForJobs(t, p, 1)
	if !strings.Contains(jobLog, "ended (status 124)") {
		t.Errorf("the job's act must have been ended by the bound:\n%s", jobLog)
	}
	if _, err := os.Stat(p.stampPath()); err != nil {
		t.Errorf("a timed-out background refresh must still stamp (§4.1 invariant 3): %v", err)
	}

	_, stderr := p.run(t, "")
	for _, want := range []string{"⚠ tool: the background refresh (update --extensions) timed out after 1s",
		p.jobLog(), "to retry now, run: tool update --extensions"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the next launch must say %q:\n%s", want, stderr)
		}
	}
	_, stderr = p.run(t, "")
	if strings.Contains(stderr, "background refresh") {
		t.Errorf("the timeout must be said once, not on every launch:\n%s", stderr)
	}
}

// TestABackgroundJobThatCannotTakeItsLockSaysSoOnce: a store the job cannot take its lock in —
// here a FILE where the lock directory goes, which passes the launch's checks and fails the
// job's mkdir — is "cannot take", never "another holds". The job stamps, so the store says so
// once an hour rather than on every launch, and leaves that for the next launch to say once.
func TestABackgroundJobThatCannotTakeItsLockSaysSoOnce(t *testing.T) {
	p := newTimingProbe(t, false, false)
	if err := os.WriteFile(p.lockPath(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr := p.run(t, "")
	if !strings.Contains(stderr, "refreshing in the background") {
		t.Fatalf("the launch must start the job, which is what finds the lock unusable:\n%s", stderr)
	}
	jobLog := waitForJobs(t, p, 1)
	if !strings.Contains(jobLog, "cannot take the refresh lock") || countLine(p.logLines(t), "REFRESH") != 0 {
		t.Errorf("a job that cannot take its lock must say so and refresh nothing:\n%s\n%v", jobLog, p.logLines(t))
	}
	if _, err := os.Stat(p.stampPath()); err != nil {
		t.Errorf("a job that cannot take its lock must stamp, so it is not retried every launch: %v", err)
	}

	_, stderr = p.run(t, "")
	if n := strings.Count(stderr, "could not take its lock"); n != 1 ||
		!strings.Contains(stderr, "is a directory this jail can write") {
		t.Errorf("the next launch must say once that the job could not take its lock, and what to check:\n%s", stderr)
	}
	if strings.Contains(stderr, "in the background") || strings.Contains(stderr, "another refresh holds") {
		t.Errorf("inside the hour no second job may start, and the lock is not HELD:\n%s", stderr)
	}
	_, stderr = p.run(t, "")
	if strings.Contains(stderr, "refresh") {
		t.Errorf("the unusable lock must be said once, not on every launch:\n%s", stderr)
	}
}

// startBlockedJob starts a background job whose act blocks until the cell writes the returned
// release file, and returns the release and the job's process group: the job is a session leader
// (the detach), and the pid is the first field of the owner token it wrote into the lock.
func startBlockedJob(t *testing.T, p *prelaunchProbe) (release string, pgid int) {
	t.Helper()
	release = filepath.Join(p.home, "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })
	p.run(t, "", "FAKE_REFRESH_WAIT="+release)
	mustAppear(t, release+".started", "the background refresh starting")
	tok, err := os.ReadFile(filepath.Join(p.lockPath(), probeOwner))
	if err != nil {
		t.Fatalf("the running job's lock has no owner token: %v", err)
	}
	pgid, err = strconv.Atoi(strings.SplitN(strings.TrimSpace(string(tok)), ".", 2)[0])
	if err != nil || pgid <= 1 {
		t.Fatalf("the owner token %q does not start with the job's pid", tok)
	}
	return release, pgid
}

// TestAStoppedBackgroundJobReleasesItsLockAndIsSaid: a SIGTERM to the job's process group (a
// `kill` of the job, or a runtime stopping it politely) goes through _shielded's arm, which
// releases the lock before the job ends; the job never reaches its own ending, so it stamps
// nothing and the next launch says it did not finish, once — even while another launcher now
// holds the lock, whose token is not the job's — and the launch after that starts it again.
func TestAStoppedBackgroundJobReleasesItsLockAndIsSaid(t *testing.T) {
	p := newTimingProbe(t, false, false)
	_, pgid := startBlockedJob(t, p)
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		t.Fatalf("signal the job's group %d: %v", pgid, err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(p.lockPath()); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(p.jobLog())
			t.Fatalf("a stopped job must release its lock before it ends; its log:\n%s", log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(p.stampPath()); !os.IsNotExist(err) {
		t.Errorf("a stopped job must not stamp: its refresh did not happen (err=%v)", err)
	}

	// Another launcher takes the free lock before this home launches again.
	if err := os.Mkdir(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.lockPath(), probeOwner), []byte("someone-else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr := p.run(t, "")
	for _, want := range []string{"⚠ tool: the background refresh (update --extensions) did not finish",
		p.jobLog(), "to retry now, run: tool update --extensions", "another refresh holds"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the next launch must say %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "pending") {
		t.Errorf("the provisional result's own marker line must never be said:\n%s", stderr)
	}

	if err := os.RemoveAll(p.lockPath()); err != nil {
		t.Fatal(err)
	}
	_, stderr = p.run(t, "")
	if !strings.Contains(stderr, "refreshing in the background") {
		t.Errorf("the refresh the stopped job did not do is still due, so a launch starts it:\n%s", stderr)
	}
	if strings.Contains(stderr, "did not finish") {
		t.Errorf("the stopped job must be said once, not on every launch:\n%s", stderr)
	}
	waitForJobs(t, p, 1)
}

// TestABackgroundJobKilledWithItsJailIsSaidOnceItsLockIsStale: a podman or Apple Container jail
// that exits SIGKILLs the job, so no trap runs — here the job's whole process group is killed
// the same way. Its lock stays, carrying its token. While that lock is younger than STALE_LOCK a
// launch in the same home reads it as held and says nothing of the job (it may be running, in
// another terminal of this jail). Once the lock is stale, a launch says the job did not finish,
// once, and its own job breaks the lock and runs the refresh.
func TestABackgroundJobKilledWithItsJailIsSaidOnceItsLockIsStale(t *testing.T) {
	p := newTimingProbe(t, false, false)
	release, pgid := startBlockedJob(t, p)
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill the job's group %d: %v", pgid, err)
	}
	// The act runs in a session of its own (_bounded's detach), which a jail exit kills too; here
	// it is released, so nothing of the killed job is left running.
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.lockPath(), probeOwner)); err != nil {
		t.Fatalf("a killed job leaves its lock and token behind, which this cell is about: %v", err)
	}

	_, stderr := p.run(t, "")
	if strings.Contains(stderr, "did not finish") || !strings.Contains(stderr, "another refresh holds") {
		t.Errorf("while the killed job's lock is young it reads as held, and the job as maybe running:\n%s", stderr)
	}

	backdatePath(t, p.lockPath(), 11*time.Minute)
	_, stderr = p.run(t, "")
	if n := strings.Count(stderr, "did not finish (the jail exited, or the job was stopped, while it ran)"); n != 1 ||
		!strings.Contains(stderr, "frees itself after 10 minutes") || strings.Contains(stderr, "pending") {
		t.Errorf("once the killed job's lock is stale, the launch must say once that it did not finish:\n%s", stderr)
	}
	if !strings.Contains(stderr, "refreshing in the background") {
		t.Fatalf("the launch must start a job, which breaks the stale lock:\n%s", stderr)
	}
	jobLog := waitForJobs(t, p, 1)
	if !strings.Contains(jobLog, "ended (status 0)") || countLine(p.logLines(t), "REFRESH") != 2 {
		t.Errorf("the new job must break the stale lock and run the refresh:\n%s\n%v", jobLog, p.logLines(t))
	}
	_, stderr = p.run(t, "")
	if strings.Contains(stderr, "did not finish") {
		t.Errorf("the killed job must be said once, not on every launch:\n%s", stderr)
	}
}

// TestNextLaunchFallsBackToTheLaunchWhenYoloCannotDetach: a launcher whose yolo has no detach —
// none on PATH, or one older than this launcher, which refuses the flag — runs the refresh where
// it always ran, in front, and says why. Nothing is left running unbounded behind the program.
func TestNextLaunchFallsBackToTheLaunchWhenYoloCannotDetach(t *testing.T) {
	older := func(t *testing.T) string {
		dir := t.TempDir()
		body := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = --detach ] && { echo usage >&2; exit 2; }; done\n" +
			"exec " + shellQuoteForTest(filepath.Join(yoloStandIn(t), "yolo")) + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir + ":" + pathWithout(t, "yolo")
	}
	for _, tc := range []struct {
		name string
		path func(t *testing.T) string
	}{
		{"no yolo", func(t *testing.T) string { return pathWithout(t, "yolo") }},
		{"an older yolo", older},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTimingProbe(t, false, false)
			p.path = tc.path(t)
			stdout, stderr := p.run(t, "")
			log := p.logLines(t)
			want := []string{"REFRESH", "ARG:update", "ARG:--extensions", "LOCKED", "LAUNCH:"}
			if strings.Join(log, "\n") != strings.Join(want, "\n") {
				t.Errorf("with no detach the refresh must run in front:\n got %q\nwant %q\n%s", log, want, stderr)
			}
			if !strings.Contains(stderr, "yolo cannot run this refresh in the background here, so it runs now") {
				t.Errorf("the fallback must say why the refresh runs now:\n%s", stderr)
			}
			assertProgramLaunched(t, log, stdout)
		})
	}
}

// TestABackgroundRefreshOutlivesTheLaunchersSignals: the job is no child the program inherits a
// signal from. The launch runs in a process group of its own, as a terminal's foreground job does;
// once it has exec'd the program, its group gets one terminal signal and the program dies of it.
// Each signal uses a fresh launch: after Ctrl-C the old group may already have no live members,
// so a second signal could target a group that has already exited.
// The refresh behind each launch must survive: it finishes, stamps and releases the lock.
// A job started with a plain `&` would share the group, and hangup would end it without stamping.
func TestABackgroundRefreshOutlivesTheLaunchersSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			p := newTimingProbe(t, false, false)
			release := filepath.Join(p.home, "release")
			launchGate := filepath.Join(p.home, "launch-gate")
			t.Cleanup(func() {
				_ = os.WriteFile(release, nil, 0o644)
				_ = os.WriteFile(launchGate, nil, 0o644)
			})
			p.write(t)
			cmd := exec.Command(p.script)
			cmd.Dir = p.home
			cmd.Env = []string{"HOME=" + p.home, "PATH=" + p.path,
				"FAKE_REFRESH_WAIT=" + release, "FAKE_LAUNCH_WAIT=" + launchGate}
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			mustAppear(t, launchGate+".started", "the program starting")
			mustAppear(t, release+".started", "the background refresh starting")

			if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
				t.Fatalf("signal the launch's group with %v: %v", sig, err)
			}
			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }()
			select {
			case err := <-waited:
				if err == nil {
					t.Fatalf("the program exited successfully after %v instead of being interrupted", sig)
				}
				var exited *exec.ExitError
				if !errors.As(err, &exited) {
					t.Fatalf("wait for the program after %v: %v", sig, err)
				}
				status, ok := exited.Sys().(syscall.WaitStatus)
				if !ok || !(status.Signaled() && status.Signal() == sig || status.Exited() && status.ExitStatus() == 128+int(sig)) {
					t.Fatalf("the program must end from %v, got %v", sig, exited)
				}
			case <-time.After(20 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				t.Fatalf("the program outlived its group's %v:\n%s", sig, out.String())
			}

			if _, err := os.Stat(p.lockPath()); err != nil {
				t.Fatalf("%v ended the background refresh: its lock is gone (%v)\n%s", sig, err, out.String())
			}
			if err := os.WriteFile(release, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			jobLog := waitForJobs(t, p, 1)
			if !strings.Contains(jobLog, "ended (status 0)") {
				t.Errorf("the background refresh must finish after the program is gone:\n%s", jobLog)
			}
			if _, err := os.Stat(p.stampPath()); err != nil {
				t.Errorf("the surviving refresh must stamp: %v", err)
			}
			if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
				t.Errorf("the surviving refresh must release the lock (err=%v)", err)
			}
		})
	}
}

// TestBackgroundRefreshTakesAHostileLauncherPath: the job is started by the launcher's own path,
// "$0", and a path with a space, a quote and a "$" in it must reach the detach as one word that
// runs nothing.
func TestBackgroundRefreshTakesAHostileLauncherPath(t *testing.T) {
	p := newTimingProbe(t, false, false)
	dir := filepath.Join(p.home, "launch dir 'q' $(touch "+filepath.Join(p.home, "WITNESS")+")")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p.script = filepath.Join(dir, "launch-tool")
	_, stderr := p.run(t, "")
	if !strings.Contains(stderr, "refreshing in the background") {
		t.Fatalf("the background job did not start:\n%s", stderr)
	}
	jobLog := waitForJobs(t, p, 1)
	if !strings.Contains(jobLog, "ended (status 0)") {
		t.Errorf("the job started from a hostile path must run the refresh:\n%s", jobLog)
	}
	if _, err := os.Stat(filepath.Join(p.home, "WITNESS")); err == nil {
		t.Error("the launcher path was run as shell text")
	}
}

// TestAtLaunchIsTheDefaultTiming: a timing the generator did not set, or set to "launch", bakes
// "launch" and runs the refresh in front, as every launcher did before the option existed.
func TestAtLaunchIsTheDefaultTiming(t *testing.T) {
	for _, timing := range []string{"", "launch", "later"} {
		t.Run("timing="+timing, func(t *testing.T) {
			p := newTimingProbe(t, false, false)
			p.timing = timing
			_, stderr := p.run(t, "")
			body, err := os.ReadFile(p.script)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "\nREFRESH_TIMING=launch\n") {
				t.Errorf("timing %q must bake REFRESH_TIMING=launch", timing)
			}
			want := []string{"REFRESH", "ARG:update", "ARG:--extensions", "LOCKED", "LAUNCH:"}
			if got := p.logLines(t); strings.Join(got, "\n") != strings.Join(want, "\n") ||
				!strings.Contains(stderr, "Refreshing tool") {
				t.Errorf("timing %q must refresh in front:\n got %q\nwant %q\n%s", timing, got, want, stderr)
			}
		})
	}
}
