package entrypoint

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
)

// iopriority.go puts the declared `resources.io.priority` on every thread and child of the
// jail (docs/design/io-priority.md §5.1; the mechanism is IO-D1, the failure paths IO-D4).
//
// # Why a pinned thread and a re-exec
//
// An I/O priority belongs to one THREAD, and a new thread or child copies the priority of
// the thread that created it. A Go program has many threads and no say over which one a
// goroutine runs on, so "set it once and every child inherits it" is false here: an
// unlocked set reached 1 of 10 threads and left 13 of 16 children unset, and a locked set
// with no re-exec reached 0 of 16 children started from other goroutines (both measured).
//
// So the entrypoint pins its goroutine, sets the priority on that one thread, and
// re-executes itself. The new image starts with exactly that thread, and every thread and
// child it ever creates descends from a holder — by construction, not by timing.
//
// # Why before the boot log
//
// attachBootLog renames boot.log to boot.log.prev before opening a fresh one. A re-exec
// after it would rotate twice and lose the previous boot's log, which is the one a user
// relaunching a broken jail needs. So the sequence runs before anything else in Main, and
// what it found is REPORTED after the log is attached (reportIOPriority).
//
// ⚠ Do not "simplify" this to a set just before execBash, to an unlocked set, or to a
// process-group call (setpgid, then IOPRIO_WHO_PGRP). The first misses every child Main
// starts before it; the second is racy; the third reaches every other process in the
// group, and needs a group of its own and a foreground-group guard to be safe
// (docs/design/io-priority.md §6).

// ioPriorityOutcome is what applyIOPriority found, held until the boot log exists. At most
// one of the two is set; neither is set when nothing was declared.
type ioPriorityOutcome struct {
	// warning goes to the boot stream: the terminal and boot.log.
	warning string
	// note goes to boot.log alone: the record that a declared priority was applied.
	note string
}

// The seams the failure paths are tested through. Production never replaces them.
var (
	ioSetThread = ioprio.SetCurrentThread
	ioGetThread = ioprio.GetThread
	ioExecSelf  = func(argv, env []string) error { return syscall.Exec("/proc/self/exe", argv, env) }
)

// applyIOPriority is the first thing Main does. On the path that applies a priority it
// does not return from the first image: it re-executes the entrypoint with argv (which must
// be exactly os.Args, since Main's arguments choose the command the shell runs) and the
// marker, and the second image returns the outcome instead.
//
// It is never fatal, and it touches no thread but its own pinned one.
func applyIOPriority(argv []string) ioPriorityOutcome {
	raw := os.Getenv(ioprio.EnvVar)
	if os.Getenv(ioprio.ReexecMarkerEnv) != "" {
		// The second image. The marker goes before anything reads the environment
		// (EnvFromOS copies it next), so no child ever sees it and nothing re-executes
		// twice.
		_ = os.Unsetenv(ioprio.ReexecMarkerEnv)
		return verifyIOPriority(ioprio.Priority(raw))
	}
	p := ioprio.Priority(raw)
	if raw == "" || p == ioprio.Normal {
		return ioPriorityOutcome{}
	}
	v, ok := p.KernelValue()
	if !ok {
		return ioPriorityOutcome{warning: fmt.Sprintf("the launcher passed %s=%q, which this "+
			"entrypoint does not recognize, so nothing was applied. The launcher and the "+
			"jail's binaries are from different yolo versions.", ioprio.EnvVar, raw)}
	}

	runtime.LockOSThread()
	if err := ioSetThread(v); err != nil {
		// Nothing was set anywhere, so nothing needs resetting.
		runtime.UnlockOSThread()
		return ioPriorityOutcome{warning: fmt.Sprintf("%q could not be set (%v), so every "+
			"process in this jail runs at the default disk priority.", p, err)}
	}
	env := append(os.Environ(), ioprio.ReexecMarkerEnv+"=1")
	execErr := ioExecSelf(argv, env)

	// Reached only when the exec failed. The one thread holding the priority is reset,
	// so the boot continues with every thread unset — except when the reset fails too,
	// and then that thread's later children inherit it, which the warning says.
	resetErr := ioSetThread(0)
	runtime.UnlockOSThread()
	msg := fmt.Sprintf("%q was set on one thread, but the re-exec that carries it to every "+
		"thread failed (%v).", p, execErr)
	if resetErr != nil {
		return ioPriorityOutcome{warning: msg + fmt.Sprintf(" Resetting that thread failed "+
			"too (%v), so processes it starts get %q and every other process runs at the "+
			"default disk priority.", resetErr, p)}
	}
	return ioPriorityOutcome{warning: msg + " It was reset, so every process in this jail " +
		"runs at the default disk priority."}
}

// verifyIOPriority is the second image's half: the priority was set before the exec, so
// this thread already holds it, and reading it back is what makes the boot log's record a
// measurement rather than an assumption.
func verifyIOPriority(p ioprio.Priority) ioPriorityOutcome {
	want, ok := p.KernelValue()
	if !ok {
		return ioPriorityOutcome{warning: fmt.Sprintf("the entrypoint re-executed with %s=%q, "+
			"which names no priority; nothing is known to be applied.", ioprio.EnvVar, string(p))}
	}
	got, err := ioGetThread(0)
	if err != nil {
		return ioPriorityOutcome{note: fmt.Sprintf("resources.io.priority: %q set on every "+
			"thread by one re-exec from a pinned thread; reading it back failed (%v).", p, err)}
	}
	if got != want {
		return ioPriorityOutcome{warning: fmt.Sprintf("%q was set before the re-exec, but "+
			"the re-executed entrypoint reads %s, so processes in this jail may run at "+
			"another disk priority.", p, ioprio.Describe(got))}
	}
	return ioPriorityOutcome{note: fmt.Sprintf("resources.io.priority: %q (%s) on every "+
		"thread and child of this entrypoint, set on one pinned thread and carried by one "+
		"re-exec.", p, ioprio.Describe(got))}
}

// reportIOPriority says what applyIOPriority found, once the boot log exists: a failure on
// the boot stream, naming the key; a success in the log alone, since a healthy launch
// should not print a line about it.
func reportIOPriority(e *Env, o ioPriorityOutcome) {
	if o.warning != "" {
		e.warn("Warning: resources.io.priority: " + o.warning)
	}
	if o.note != "" {
		e.note(o.note)
	}
}
