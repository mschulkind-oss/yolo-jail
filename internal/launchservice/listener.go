package launchservice

// listener.go is the DAEMON SIDE of a launch-owned listener: the body a host half runs when its
// whole job is one loopback listener, which a credential service's DOORWAY is
// (docs/design/host-notch-services.md HS-D15; "doorway" is that ruling's word for the thin
// adapter an agent's client talks to, which checks the launch's caller token and forwards to the
// service's host daemon). The launch side is Start, and the two halves speak this package's one
// contract: the input file InputEnv names, the readiness line on paths.JailDaemonReadyFDEnv, and
// the lifeline on LifelineFDEnv.
//
// ONE BODY FOR EVERY DOORWAY, so the input, the readiness answer and the two ways a doorway
// ends (the launch's SIGTERM, the lifeline's EOF) are written once rather than once per adapter.
// Each adapter hands it only what is its own: how it reads its inputs and what it serves.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Prepare reads a doorway's inputs through getenv (the launch's input file over this process's
// environment) and returns what serves a bound listener, or why it cannot serve. It runs before
// the listener is bound, so a doorway missing an input reports `failed` and binds nothing.
type Prepare func(getenv func(string) string) (serve func(net.Listener) error, err error)

// ServeListener is a launch-owned listener's whole run: it reads the launch's input (ReadInput,
// which removes the file), checks it names service, prepares, listens on listen (on the port the
// launch reserved for it, when the launch handed one over: Listen), reports `ready
// <service>` on the readiness descriptor, and serves until the launch stops it (SIGTERM or
// SIGINT) or dies (the lifeline's EOF). Any failure before it serves is reported as `failed
// <service> <why>`, so the launch refuses before its command starts. It returns the exit code.
func ServeListener(service, listen string, prepare Prepare) int {
	return serveListener(service, listen, prepare, os.Getenv)
}

func serveListener(service, listen string, prepare Prepare, getenv func(string) string) int {
	fail := func(why string) int {
		fmt.Fprintf(os.Stderr, "%s: the doorway cannot start: %s\n", service, why)
		reportReadiness(getenv, "failed "+service+" "+oneLine(why))
		return 1
	}
	if listen == "" {
		return fail("no --listen address: the launch hands a doorway the port it picked")
	}
	in, err := ReadInput(getenv)
	if err != nil {
		return fail(err.Error())
	}
	if in.Service != service {
		return fail(fmt.Sprintf("the launch's input names service %q, not %q", in.Service, service))
	}
	lookup := func(k string) string {
		if v, ok := in.Env[k]; ok {
			return v
		}
		return getenv(k)
	}
	serve, err := prepare(lookup)
	if err != nil {
		return fail(err.Error())
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	ctx, cancel := Lifeline(ctx, getenv)
	defer cancel()
	// THE PORT THE LAUNCH RESERVED, when it handed one over (reserve.go): the address the
	// doorway's clients were composed with, held from the pick so nothing else could bind it.
	l, err := Listen(getenv, listen)
	if err != nil {
		return fail(err.Error())
	}
	done := make(chan error, 1)
	go func() { done <- serve(l) }()
	fmt.Fprintf(os.Stderr, "%s: serving on %s for this launch; every request must carry its caller token\n",
		service, l.Addr())
	reportReadiness(getenv, "ready "+service)
	select {
	case <-ctx.Done():
		_ = l.Close()
		<-done
		return 0
	case err := <-done:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintf(os.Stderr, "%s: %v\n", service, err)
			return 1
		}
		return 0
	}
}

// reportReadiness writes line, once, on the readiness descriptor the launch handed this process,
// and closes it, so the launch reading it sees the answer and then EOF. Nothing when no
// descriptor was handed (a doorway run by hand).
func reportReadiness(getenv func(string) string, line string) {
	fd, err := strconv.Atoi(getenv(paths.JailDaemonReadyFDEnv))
	if err != nil || fd < 3 {
		return
	}
	f := os.NewFile(uintptr(fd), "launch-service-ready")
	if f == nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

// oneLine flattens s to one line: the readiness protocol is line-framed, so a newline inside a
// reason would end the answer early.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
