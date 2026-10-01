package launchservice

// reserve.go is the one loopback-port picker a launch uses, and the hand-over that makes the port
// it picked the port its service serves on.
//
// A RESERVED PORT (a term coined here) is a loopback port a launch picked, held by a socket the
// launch bound to it and never listened on. Bound, so nothing else can bind the port: the kernel
// refuses an explicit bind of it and never hands it out for port 0. Not listening, so a connection
// to it is refused, exactly as to a free port, rather than queued for an accept that never comes.
//
// WHY. A launch composes a service's clients before the service starts, so it picks the port first.
// The pick used to bind port 0 and close the listener at once: the port was free when picked and
// not held, so the kernel could hand it to whatever bound port 0 next, and the service's own bind
// then failed with "address already in use", refusing the launch. What bound next was usually the
// same launch: it fronts its host services, each on a port-0 listener, between the pick and the
// service's start. Seen as a flake in this package's callers' tests, and once in a macos-user
// launch, whose Codex doorway lost its port to the claude broker's front
// (docs/plans/test-suite-speed.md).
//
// THE PORT CHANGES HANDS WITHOUT BEING FREE:
//
//   - A process the launch starts itself (Start: a pack service's host half, a credential doorway)
//     is handed the reserved socket as a descriptor, named in ListenFDsEnv, and listens on it
//     (Listen). Nothing binds after the pick at all.
//   - A process the launch does not start (a jail daemon, which its container's or sandbox's
//     supervisor starts) cannot be handed a descriptor, so the launch closes the reservation
//     immediately before that start, after every listener of its own is bound (internal/cli/run's
//     served addresses). Only a process outside the launch, binding that port in the moment
//     between, can still take it, and that fails closed (docs/plans/notch-convergence.md NC-D43).

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// ListenFDsEnv names the reserved sockets a launch hands the service it starts: comma-separated
// `<address>=<fd>` pairs, one per served address the launch reserved. Start always sets it, empty
// when it hands none, so a value inherited from the launch's own environment never reaches the
// service.
const ListenFDsEnv = "YOLO_HOST_SERVICE_LISTEN_FDS"

// firstListenFD is the descriptor Start hands the first reserved socket at: after the readiness
// pipe (3) and the lifeline (4).
const firstListenFD = 5

// Reserved is one reserved port: the socket a launch bound to it, held until the process serving
// the port has it.
type Reserved struct {
	addr string
	f    *os.File
	once sync.Once
}

// Addr is the reserved `host:port`.
func (r *Reserved) Addr() string {
	if r == nil {
		return ""
	}
	return r.addr
}

// File is the reserved socket, for a process the launch hands it to as a descriptor
// (exec.Cmd.ExtraFiles). It stays the launch's: Release still closes it.
func (r *Reserved) File() *os.File {
	if r == nil {
		return nil
	}
	return r.f
}

// Release closes the reservation, so the port is free again. Safe on nil and more than once.
func (r *Reserved) Release() {
	if r == nil {
		return
	}
	r.once.Do(func() { _ = r.f.Close() })
}

// ReservePorts reserves a port on each declared address's host and returns the reservations keyed
// by declared address, every one distinct. On an error none is held.
func ReservePorts(declared []string) (map[string]*Reserved, error) {
	out := make(map[string]*Reserved, len(declared))
	for _, hp := range declared {
		host, _, err := net.SplitHostPort(hp)
		if err != nil {
			ReleaseAll(out)
			return nil, err
		}
		r, err := reserve(host)
		if err != nil {
			ReleaseAll(out)
			return nil, err
		}
		out[hp] = r
	}
	return out, nil
}

// ReleaseAll releases every reservation in held.
func ReleaseAll(held map[string]*Reserved) {
	for _, r := range held {
		r.Release()
	}
}

// reserve binds a TCP socket to port 0 on host, an IP literal, and does not listen on it.
// Without SO_REUSEADDR, so no other socket can share the port either.
func reserve(host string) (*Reserved, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("reserve a port on %q: not an IP literal", host)
	}
	family, sa := syscall.AF_INET6, syscall.Sockaddr(nil)
	if ip4 := ip.To4(); ip4 != nil {
		family = syscall.AF_INET
		v4 := &syscall.SockaddrInet4{}
		copy(v4.Addr[:], ip4)
		sa = v4
	} else {
		v6 := &syscall.SockaddrInet6{}
		copy(v6.Addr[:], ip.To16())
		sa = v6
	}
	// Close-on-exec from the start, under the fork lock, as the net package makes its own sockets:
	// a reservation crosses into a process only where it is handed on purpose.
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(family, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("reserve a port on %s: socket: %w", host, err)
	}
	if err := syscall.Bind(fd, sa); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("reserve a port on %s: bind: %w", host, err)
	}
	bound, err := syscall.Getsockname(fd)
	if err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("reserve a port on %s: getsockname: %w", host, err)
	}
	port := 0
	switch a := bound.(type) {
	case *syscall.SockaddrInet4:
		port = a.Port
	case *syscall.SockaddrInet6:
		port = a.Port
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	return &Reserved{addr: addr, f: os.NewFile(uintptr(fd), "reserved port "+addr)}, nil
}

// handOver is plan's reservations as Start hands them: the sockets for ExtraFiles from
// firstListenFD, in address order, and ListenFDsEnv's value naming each.
func (p *Plan) handOver() ([]*os.File, string) {
	var addrs []string
	for a, r := range p.reserved {
		if r != nil {
			addrs = append(addrs, a)
		}
	}
	sort.Strings(addrs)
	files := make([]*os.File, 0, len(addrs))
	pairs := make([]string, 0, len(addrs))
	for i, a := range addrs {
		files = append(files, p.reserved[a].File())
		pairs = append(pairs, a+"="+strconv.Itoa(firstListenFD+i))
	}
	return files, strings.Join(pairs, ",")
}

// Release releases every reservation of the plan's that Start has not handed over: a plan the
// launch never starts. Safe on nil and more than once.
func (p *Plan) Release() {
	if p == nil {
		return
	}
	ReleaseAll(p.reserved)
}

// handedFDs parses ListenFDsEnv's value. A malformed pair is skipped: it names nothing a launch
// handed.
func handedFDs(value string) map[string]int {
	out := map[string]int{}
	for _, pair := range strings.Split(value, ",") {
		addr, fd, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(fd)
		if err != nil || n < firstListenFD {
			continue
		}
		out[addr] = n
	}
	return out
}

var handedOnce sync.Once

// Listen is the listener a launch-owned service serves address on: the reserved socket its launch
// handed it for that address (ListenFDsEnv), now listening, or else a bind of address, for a
// service run by hand or a plan that reserved nothing. getenv is the process's own environment,
// where Start names the descriptors.
//
// Every handed descriptor is made close-on-exec at the first call, so one the service never
// listens on (an address of its plan it does not serve) stays reserved for the service's life and
// reaches none of its children.
func Listen(getenv func(string) string, address string) (net.Listener, error) {
	handed := handedFDs(getenv(ListenFDsEnv))
	handedOnce.Do(func() {
		for _, fd := range handed {
			syscall.CloseOnExec(fd)
		}
	})
	fd, ok := handed[address]
	if !ok {
		return net.Listen("tcp", address)
	}
	failed := func(err error) (net.Listener, error) {
		return nil, fmt.Errorf("listen on the socket the launch reserved for %s (fd %d): %w", address, fd, err)
	}
	if err := syscall.Listen(fd, syscall.SOMAXCONN); err != nil {
		return failed(err)
	}
	f := os.NewFile(uintptr(fd), "reserved port "+address)
	if f == nil {
		return failed(errors.New("not an open descriptor"))
	}
	l, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return failed(err)
	}
	if got := l.Addr().String(); got != address {
		_ = l.Close()
		return failed(fmt.Errorf("it is bound to %s", got))
	}
	return l, nil
}
