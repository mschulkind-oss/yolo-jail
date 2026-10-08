package run

// armstack.go is this process's LAUNCH ARMS, innermost last, and the one signal handler that
// routes a signal to them (docs/design/jail-lifetime-last-session-wins.md JL-D75).
//
// A launch can run inside another launch's window, in this process: an auto-capture's and a fork
// build's capture jails are whole launches the outer launch runs through this same pipeline
// (internal/cli's runCaptureJail) before its own keeper exists, and each installs arms of its own.
// Two arms acting on one signal would each run a teardown and race to exit, so a signal goes to
// the INNERMOST arm alone, the one the launch running right now installed. Its exit ends the outer
// launches too, so before it exits it runs each outer arm's outer hook: what that launch needs done
// when a launch inside it ends the process (a launch guard's abandon, launchguard.go).
//
// One signal.Notify serves every arm. It is made when the first arm is installed and stopped when
// the last one goes, so a signal after the last disarm has its default effect, as it always had,
// and a signal is routed under the stack's lock, so an arm installed or removed while one is in
// flight cannot leave it with two arms or none.
//
// THE HANDLER CATCHES SIGCONT TOO, AND ROUTES IT NOWHERE: catching it is what makes a signal sent
// while this process was STOPPED reach it on macOS. XNU's psignal hands a caught signal sent to a
// stopped process to one thread and returns without waking that thread (bsd/kern/kern_sig.c, "else
// if (sig_proc->p_stat == SSTOP) goto sigout_locked;"), and a SIGCONT left at its default resumes
// the task without waking any thread either ("If it's sleeping on an event, it remains so"). A Go
// thread parked in the kernel then holds the signal until something else wakes it, which for an
// idle one can be never: a SIGINT sent to a ^Z'd launch, or bash's `kill %1` on it (a SIGTERM and
// then a SIGCONT), was lost, and the launch ran its session to the end and exited 0 (the Nightly
// macOS Integration run that failed TestASIGINTInTheReadyWindowLeavesTheJailUpForAnotherSession).
// A CAUGHT SIGCONT takes psignal's other path, which aborts the chosen thread's wait, and the thread
// it chooses is the one the pending signal went to, the first thread whose mask admits it; the
// thread then takes both on its way back to user space. Linux wakes a stopped task's pending signal
// on SIGCONT itself, so there this changes nothing.

import (
	"os"
	"os/signal"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
)

// launchArms is the stack and the channel its one Notify feeds.
var launchArms struct {
	mu   sync.Mutex
	arms []*launchSignalArm
	sigs chan os.Signal
	stop chan struct{}
}

// pushLaunchArm installs a as the innermost arm, starting the process's handler with the first.
func pushLaunchArm(a *launchSignalArm) {
	launchArms.mu.Lock()
	defer launchArms.mu.Unlock()
	if launchArms.sigs == nil {
		sigs, stop := make(chan os.Signal, 4), make(chan struct{})
		launchArms.sigs, launchArms.stop = sigs, stop
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGCONT)
		go routeLaunchSignals(sigs, stop)
	}
	launchArms.arms = append(launchArms.arms, a)
}

// launchArmsResumed counts the SIGCONTs the handler took, which it routes to no arm (the file's
// comment says why it takes them at all). Read by a test only.
var launchArmsResumed atomic.Int64

// routeLaunchSignals hands each signal to the innermost arm, until stop. An arm whose channel is
// full has signals it has not read: it is ending the process already, and one more changes nothing.
// A SIGCONT goes to no arm: it is taken only for the wakeup its being caught buys.
func routeLaunchSignals(sigs <-chan os.Signal, stop <-chan struct{}) {
	for {
		select {
		case s := <-sigs:
			if s == syscall.SIGCONT {
				launchArmsResumed.Add(1)
				continue
			}
			launchArms.mu.Lock()
			if n := len(launchArms.arms); n > 0 {
				select {
				case launchArms.arms[n-1].signals <- s:
				default:
				}
			}
			launchArms.mu.Unlock()
		case <-stop:
			return
		}
	}
}

// popLaunchArm removes a, wherever it is, and stops the handler with the last arm. Idempotent.
func popLaunchArm(a *launchSignalArm) {
	launchArms.mu.Lock()
	defer launchArms.mu.Unlock()
	i := slices.Index(launchArms.arms, a)
	if i < 0 {
		return
	}
	launchArms.arms = slices.Delete(launchArms.arms, i, i+1)
	if len(launchArms.arms) == 0 && launchArms.sigs != nil {
		signal.Stop(launchArms.sigs)
		close(launchArms.stop)
		launchArms.sigs, launchArms.stop = nil, nil
	}
}

// outerArms is every arm installed before a and still installed: the launches a's launch runs
// inside, outermost first.
func outerArms(a *launchSignalArm) []*launchSignalArm {
	launchArms.mu.Lock()
	defer launchArms.mu.Unlock()
	i := slices.Index(launchArms.arms, a)
	if i <= 0 {
		return nil
	}
	return slices.Clone(launchArms.arms[:i])
}
