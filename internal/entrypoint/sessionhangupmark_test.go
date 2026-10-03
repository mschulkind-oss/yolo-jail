package entrypoint

// sessionhangupmark_test.go pins a hangup that reaches the jail before the session it names has
// named itself there (sessionhangup.go; docs/design/jail-lifetime-last-session-wins.md JL-D77). A
// launcher signalled just after it started its session's exec runs its hangup while that exec is
// still being made, and the hangup used to find no record, end nothing and leave nothing, so the
// session named itself a moment later and ran on in the jail with no terminal: killing its client
// does not end it. Now the hangup leaves its mark first, and a session that finds its id marked
// as it names itself ends there.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
)

// TestASessionHungUpBeforeItNamedItselfDoesNotBegin: the hangup comes first and finds no session;
// the session then names itself and must learn it was hung up, keeping no record a later hangup
// could be asked about. Another session's id is untouched.
func TestASessionHungUpBeforeItNamedItselfDoesNotBegin(t *testing.T) {
	proc, sent := withSessionState(t)
	self := os.Getpid()
	fakeProc(t, proc, self, 1, self, 5555)
	const id = "0123456789abcdef0123456789abcdef"
	if err := hangUpSession(id); err != nil {
		t.Fatalf("a hangup of a session that has not named itself yet: %v", err)
	}
	if len(*sent) != 0 {
		t.Errorf("the hangup signalled %v with no session named", *sent)
	}
	if err := registerSession(id); err == nil {
		t.Fatal("the session named itself after its launcher hung it up and was let begin: it runs on in the " +
			"jail with no terminal, since killing its client does not end it")
	} else if !errors.Is(err, errHungUpBeforeItBegan) {
		t.Fatalf("the hung-up session's registration failed for another reason: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, id)); !os.IsNotExist(err) {
		t.Errorf("the session that did not begin left its record: %v", err)
	}
	const other = "fedcba9876543210fedcba9876543210"
	if err := registerSession(other); err != nil {
		t.Errorf("another session was refused by this one's hangup: %v", err)
	}
}

// TestAHangupAndItsSessionNamingItselfAtOnceAlwaysMeet: the two race in the jail, each in an exec
// of its own, and whichever order they land in, one sees the other: the hangup reads the session's
// record (and hangs it up, removing the record), or the session finds the hangup's mark and does
// not begin. Neither seeing the other is the defect: a named session, left running, whose hangup
// has come and gone.
func TestAHangupAndItsSessionNamingItselfAtOnceAlwaysMeet(t *testing.T) {
	proc, _ := withSessionState(t)
	self := os.Getpid()
	fakeProc(t, proc, self, 1, self, 5555)
	missed := 0
	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("%032x", i+1)
		var regErr, hupErr error
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		go func() { defer wg.Done(); <-start; regErr = registerSession(id) }()
		go func() { defer wg.Done(); <-start; hupErr = hangUpSession(id) }()
		close(start)
		wg.Wait()
		if hupErr != nil {
			t.Fatalf("hangup %d: %v", i, hupErr)
		}
		if regErr != nil && !errors.Is(regErr, errHungUpBeforeItBegan) {
			t.Fatalf("registration %d: %v", i, regErr)
		}
		if _, err := os.Stat(filepath.Join(sessionsDir, id)); regErr == nil && err == nil {
			missed++
		}
	}
	if missed > 0 {
		t.Errorf("in %d of 2000 races the session named itself and began while its hangup ended nothing", missed)
	}
}

// TestEnteringAHungUpSessionEndsItWithAHangupsStatus is Main's half (enterSession): a session its
// launcher hung up before it named itself ends with 128+SIGHUP, as a hung-up session's process
// does, and says why; a session that could not be named for any other reason warns and goes on, as
// it always did, since it can still run and only its hangup is lost. A session that is not the
// jail's first leaves provisioning alone: the first session may still begin it.
func TestEnteringAHungUpSessionEndsItWithAHangupsStatus(t *testing.T) {
	proc, _ := withSessionState(t)
	self := os.Getpid()
	fakeProc(t, proc, self, 1, self, 5555)
	withJailMainDir(t)
	const id = "00000000feedbeef"
	if err := hangUpSession(id); err != nil {
		t.Fatal(err)
	}
	var warned bytes.Buffer
	err := enterSession(id, false, &warned)
	var status *ExitStatus
	if !errors.As(err, &status) || status.Code != 128+int(syscall.SIGHUP) {
		t.Fatalf("entering a hung-up session returned %v, want an exit status of %d", err, 128+int(syscall.SIGHUP))
	}
	if !strings.Contains(status.Message, "signalled before the session began") {
		t.Errorf("the session's end does not say why: %q", status.Message)
	}
	if warned.Len() != 0 {
		t.Errorf("a hung-up session warned as well: %s", warned.String())
	}
	if claim, claimed := readMainState(provisionClaimFile); claimed {
		t.Errorf("a hung-up session that is not the jail's first claimed provisioning (%q), which the first "+
			"session may still begin", claim)
	}

	if err := enterSession("NOT-AN-ID", false, &warned); err != nil {
		t.Errorf("a session that could not be named was refused (%v); it can still run", err)
	}
	if !strings.Contains(warned.String(), "could not record this session") {
		t.Errorf("a session that could not be named did not say so: %q", warned.String())
	}
	warned.Reset()
	if err := enterSession("00000000cafebabe", false, &warned); err != nil || warned.Len() != 0 {
		t.Errorf("an ordinary session: err %v, warned %q; want neither", err, warned.String())
	}
}

// TestAFirstSessionHungUpBeforeItBeganHandsProvisioningOnAtOnce is Main's half for the jail's FIRST
// session (JL-D80): the one every other session waits for to begin provisioning. Hung up before it
// named itself, it ends as any session does, and so never begins provisioning; a session waiting for
// it used to wait out claimWaitLimit (two minutes) before it took the run over, or was refused, as
// for a first session that never arrived. Now the first session's end says provisioning is
// abandoned, and a waiter acts on that at once: one with no terminal is refused, saying to run yolo
// in one, and one with a terminal runs the stage itself.
func TestAFirstSessionHungUpBeforeItBeganHandsProvisioningOnAtOnce(t *testing.T) {
	proc, _ := withSessionState(t)
	self := os.Getpid()
	fakeProc(t, proc, self, 1, self, 5555)
	withJailMainDir(t)
	claimWaitLimit = 3 * time.Second
	// Main's first statement re-executes the process for a declared disk I/O priority; none here.
	t.Setenv(ioprio.EnvVar, "")
	t.Setenv(ioprio.ReexecMarkerEnv, "")
	t.Setenv(JailMainEnv, JailMainHold)
	const id = "0000000000000000000000000000f125"
	announceReady("stage", &bytes.Buffer{})

	// An attach hung up before it began is no first session: the first may still begin provisioning.
	const attach = "0000000000000000000000000000a77a"
	t.Setenv(SessionIDEnv, attach)
	if err := hangUpSession(attach); err != nil {
		t.Fatal(err)
	}
	var status *ExitStatus
	if err := Main([]string{"bash"}); !errors.As(err, &status) || status.Code != 128+int(syscall.SIGHUP) {
		t.Fatalf("the hung-up attach returned %v, want an exit status of %d", err, 128+int(syscall.SIGHUP))
	}
	if claim, claimed := readMainState(provisionClaimFile); claimed {
		t.Fatalf("a hung-up attach claimed provisioning (%q), which the first session may still begin", claim)
	}

	t.Setenv(SessionIDEnv, id)
	if err := hangUpSession(id); err != nil {
		t.Fatal(err)
	}
	err := Main([]string{FirstSessionArg, "bash"})
	if !errors.As(err, &status) || status.Code != 128+int(syscall.SIGHUP) {
		t.Fatalf("the hung-up first session returned %v, want an exit status of %d", err, 128+int(syscall.SIGHUP))
	}

	type res struct {
		provisioner bool
		err         error
		took        time.Duration
	}
	await := func(tty bool) res {
		start := time.Now()
		p, err := newSessionGate(tty, &bytes.Buffer{}).await()
		return res{p, err, time.Since(start)}
	}
	quick := claimWaitLimit / 3
	got := await(false)
	var refused *ExitStatus
	if !errors.As(got.err, &refused) || !strings.Contains(refused.Message, "run `yolo` in a terminal") {
		t.Errorf("a waiter with no terminal got provisioner=%v err=%v, want the refusal that names a terminal",
			got.provisioner, got.err)
	}
	if got.took >= quick {
		t.Errorf("a waiter with no terminal waited %s for a first session that had already ended (the bound is %s)",
			got.took.Round(time.Millisecond), claimWaitLimit)
	}
	got = await(true)
	if got.err != nil || !got.provisioner {
		t.Errorf("a waiter with a terminal got provisioner=%v err=%v, want it to run provisioning itself",
			got.provisioner, got.err)
	}
	if got.took >= quick {
		t.Errorf("a waiter with a terminal waited %s for a first session that had already ended (the bound is %s)",
			got.took.Round(time.Millisecond), claimWaitLimit)
	}
}
