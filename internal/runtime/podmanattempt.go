package runtime

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// attemptOutputCap bounds how much of an attempt's stdout or stderr is read back. A
// `podman info --format json` answer is a few kilobytes; the cap only keeps a runaway
// stream from being read into memory whole.
const attemptOutputCap = 4 << 20

// RunPodmanAttempt is the real AttemptRunner (PR-D3 of podman-reboot-readiness.md). Three
// properties, each for a measured reason:
//
//   - OUTPUT GOES TO UNLINKED TEMP FILES, NEVER PIPES. With pipes, os/exec's Wait does not
//     return until every process holding them has closed them, which includes a podman
//     child that outlived its parent (measured: a 1 s timeout on `sh -c 'sleep 30 & sleep
//     100'` returned after 100 s). A file has no reader to wait for, and closing yolo's end
//     can never SIGPIPE a refresh half-way.
//   - ITS OWN PROCESS GROUP (Setpgid), so a Ctrl-C at the terminal reaches yolo, which stops
//     waiting, and not podman, which may be doing the refresh every later client needs.
//   - NEVER KILLED. At the deadline or on an interrupt this returns with Exited false and
//     the pid, and the process runs on. A goroutine keeps waiting for it so it is reaped
//     if it ends while yolo is still alive, and closes yolo's copies of the files then.
func RunPodmanAttempt(argv []string, deadline time.Time, interrupt <-chan struct{}) Attempt {
	start := time.Now()
	if len(argv) == 0 {
		return Attempt{StartErr: errEmptyArgv}
	}
	stdout, err := unlinkedTemp("yolo-podman-ready-*.out")
	if err != nil {
		return Attempt{StartErr: &ProbeScratchError{Err: err}}
	}
	stderr, err := unlinkedTemp("yolo-podman-ready-*.err")
	if err != nil {
		_ = stdout.Close()
		return Attempt{StartErr: &ProbeScratchError{Err: err}}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return Attempt{StartErr: err}
	}
	done := make(chan struct{})
	var rc int
	go func() {
		_ = cmd.Wait()
		if cmd.ProcessState != nil {
			rc = cmd.ProcessState.ExitCode()
		}
		close(done)
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-done:
		a := Attempt{Exited: true, RC: rc, Pid: cmd.Process.Pid, Duration: time.Since(start),
			Stdout: readBack(stdout), Stderr: readBack(stderr)}
		_ = stdout.Close()
		_ = stderr.Close()
		return a
	case <-timer.C:
		go closeWhenDone(done, stdout, stderr)
		return Attempt{Pid: cmd.Process.Pid, Duration: time.Since(start)}
	case <-interrupt:
		go closeWhenDone(done, stdout, stderr)
		return Attempt{Pid: cmd.Process.Pid, Duration: time.Since(start), Interrupted: true}
	}
}

// errEmptyArgv is the probe asked to run nothing: a yolo bug, which nothing will clear.
var errEmptyArgv = errors.New("empty argv")

// ProbeScratchError is a start error of yolo's own, not podman's: the attempt could not
// create the scratch files that hold podman's answer (unlinkedTemp), so podman never ran.
// ClassifyStartError reports it as that, with its own fix, never as a podman that could not
// be started.
type ProbeScratchError struct{ Err error }

func (e *ProbeScratchError) Error() string {
	return "yolo could not create the scratch file for podman's answer: " + e.Err.Error()
}

func (e *ProbeScratchError) Unwrap() error { return e.Err }

// unlinkedTemp creates a temp file and removes its name at once: only the two descriptors
// (yolo's and the child's) keep it alive, so nothing is left behind whichever way either
// process ends.
func unlinkedTemp(pattern string) (*os.File, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, err
	}
	_ = os.Remove(f.Name())
	return f, nil
}

// readBack reads a file from its start, up to attemptOutputCap. ReadAt, because the child
// shared this descriptor's offset and left it at the end.
func readBack(f *os.File) string {
	b, _ := io.ReadAll(io.NewSectionReader(f, 0, attemptOutputCap))
	return string(b)
}

func closeWhenDone(done <-chan struct{}, files ...*os.File) {
	<-done
	for _, f := range files {
		_ = f.Close()
	}
}
