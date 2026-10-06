package launchservice

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// RunAgent runs the agent as this process's child while services serve it, supervises each one
// while the agent runs (Running.Supervise), and stops every service once the agent exits, by
// any route (§4.4 item 3). It returns the agent's exit status,
// 128 plus the signal number for a signal death, and 126 when the agent cannot be started, the
// codes the managed host Codex launch returns (openaiauthhost.Launch.Run).
//
// SIGNALS. The launch stays resident as the parent of both, so it must not die of a signal meant
// for the agent, or the service would go with it while the agent runs on. A terminal's SIGINT and
// SIGQUIT reach the agent directly, since it shares the launch's foreground process group (the
// service does not: Start puts it in its own), so this process absorbs them and forwards nothing,
// which would deliver a second Ctrl-C. SIGTERM and SIGHUP, which come from a kill or a hangup, are
// forwarded to the agent, whose exit then ends the service. signals is the channel they arrive
// on, nil for this process's own.
func RunAgent(target string, argv, environ []string, stdin io.Reader, stdout, stderr io.Writer,
	services []*Running, signals chan os.Signal, prefix string) int {
	defer func() {
		for _, s := range services {
			s.Stop()
		}
	}()
	if signals == nil {
		signals = make(chan os.Signal, 4)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
		defer signal.Stop(signals)
	}
	cmd := exec.Command(target, argv[1:]...)
	cmd.Args = argv
	cmd.Env = environ
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "%srun %s: %v\n", prefix, target, err)
		return 126
	}
	// SUPERVISED FROM HERE, while the agent runs (HS-D28): a service that dies is named and
	// restarted under its policy on the same sockets; the deferred Stop above is the launch's own
	// teardown and says nothing.
	for _, s := range services {
		s.Supervise(argv[0], stderr, prefix)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for {
		select {
		case sig := <-signals:
			if sig == syscall.SIGTERM || sig == syscall.SIGHUP {
				_ = cmd.Process.Signal(sig)
			}
		case err := <-exited:
			return exitStatus(err, target, stderr, prefix)
		}
	}
}

func exitStatus(err error, target string, stderr io.Writer, prefix string) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exitErr.ExitCode()
	}
	fmt.Fprintf(stderr, "%srun %s: %v\n", prefix, target, err)
	return 126
}
