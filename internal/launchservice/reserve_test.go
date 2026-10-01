package launchservice

// reserve_test.go pins RESERVED PORTS (reserve.go): a port a launch picked for a service it
// starts stays bound from the pick until the service serves on it, so no other listener can be
// handed it in between.

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// doorwayServiceEnv names the service fakeDoorway serves, for a plan whose service is not "door".
const doorwayServiceEnv = "LAUNCHSERVICE_TEST_DOORWAY_SERVICE"

// THE PORT A PLAN PICKED IS STILL THE SERVICE'S WHEN ANOTHER LISTENER ASKS FOR IT FIRST. NewPlan
// used to bind port 0 and close the listener at once, so the kernel was free to hand the port to
// whatever bound next. Once that was the same launch's claude broker front, bound between the pick
// and the service's start, and the service's own bind then failed with "address already in use",
// which refused the launch (docs/plans/test-suite-speed.md). Here another listener asks for the
// exact port between the pick and the start, which makes that race deterministic: it must be
// refused, and the service must still start and answer on the address its clients were composed
// with. The service is ServeListener, the body every doorway runs.
func TestAPlannedPortStaysTheServicesWhenAnotherListenerAsksFirst(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "doorway:ok")
	t.Setenv(doorwayServiceEnv, "wire-bridge")
	p := packFrom(t, "wire-bridge", fmt.Sprintf(bridgeManifest, `["yolo", "internal", "daemon", "wire-bridge"]`), true)
	d, err := Admit([]*packload.Pack{p}, "wire-bridge")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan([]*packload.Pack{p}, d)
	if err != nil {
		t.Fatal(err)
	}
	addr := plan.Moved["127.0.0.1:8214"]
	if addr == "" {
		t.Fatalf("the plan moved nothing for 127.0.0.1:8214: %v", plan.Moved)
	}
	// fakeDoorway listens on its argv's last word, as a doorway's {listen} is.
	plan.Cmd = append(plan.Cmd, addr)

	if other, err := net.Listen("tcp", addr); err == nil {
		t.Cleanup(func() { _ = other.Close() })
		t.Errorf("another listener bound %s, the port the plan picked for its service, before the "+
			"service started: the pick let the port go", addr)
	}
	r, err := Start(plan, map[string]string{"UPSTREAM": "from-the-launch"})
	if err != nil {
		t.Fatalf("Start = %v: the service lost the port its clients were composed with", err)
	}
	t.Cleanup(r.Stop)
	if code, body := get(t, addr, plan.Token); code != http.StatusOK || body != "from-the-launch" {
		t.Errorf("the service at %s answered %d %q, want 200 and the input's value", addr, code, body)
	}
}

// A RESERVED PORT IS BOUND AND NOT LISTENING: each declared address gets a distinct port on its
// own host, never the declared one; nothing can bind a held port, explicitly or as a port-0 pick;
// a connection to it is refused rather than queued for an accept nobody makes; and once released
// the port is free.
func TestReservePortsHoldsDistinctPortsThatRefuseConnectionsUntilReleased(t *testing.T) {
	declared := []string{"127.0.0.1:1460", "127.0.0.1:1461", "127.0.0.1:8214"}
	held, err := ReservePorts(declared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ReleaseAll(held) })
	seen := map[string]bool{}
	for _, hp := range declared {
		r := held[hp]
		to := r.Addr()
		host, port, err := net.SplitHostPort(to)
		if err != nil || host != "127.0.0.1" || port == "0" || to == hp || seen[to] {
			t.Errorf("%s reserved %q: want a distinct picked port on 127.0.0.1", hp, to)
		}
		seen[to] = true
		if l, err := net.Listen("tcp", to); err == nil {
			_ = l.Close()
			t.Errorf("another listener bound %s while it was reserved", to)
		}
		if dials(to) {
			t.Errorf("a connection to the reserved %s was accepted; a reservation listens on nothing", to)
		}
	}
	// No port-0 pick is handed a held port either: what the launch's own fronts bind.
	for i := 0; i < 200; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if seen[l.Addr().String()] {
			t.Errorf("a port-0 listener was handed %s, which is reserved", l.Addr())
		}
		_ = l.Close()
	}
	for _, r := range held {
		r.Release()
		r.Release() // idempotent
		l, err := net.Listen("tcp", r.Addr())
		if err != nil {
			t.Errorf("%s is not free once released: %v", r.Addr(), err)
			continue
		}
		_ = l.Close()
	}
	var none *Reserved
	none.Release() // a nil reservation is one the launch never held
}

// A HANDED-OVER RESERVATION IS THE SERVICE'S LISTENER: Listen listens on the socket ListenFDsEnv
// names for the address, at that address; it binds an address it was handed nothing for; and it
// refuses a socket bound somewhere other than the address it is asked for.
func TestListenServesTheReservedSocketItWasHandedForTheAddress(t *testing.T) {
	held, err := ReservePorts([]string{"127.0.0.1:1", "127.0.0.1:2"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ReleaseAll(held) })
	one, two := held["127.0.0.1:1"], held["127.0.0.1:2"]
	// Each descriptor a duplicate, as the one a child is handed is: Listen takes it over.
	env := map[string]string{ListenFDsEnv: fmt.Sprintf("%s=%d,%s=%d", one.Addr(), dupFD(t, one),
		two.Addr(), dupFD(t, two))}
	getenv := func(k string) string { return env[k] }

	l, err := Listen(getenv, one.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if l.Addr().String() != one.Addr() {
		t.Errorf("Listen served %s, want the reserved %s", l.Addr(), one.Addr())
	}
	go func() {
		if c, err := l.Accept(); err == nil {
			_ = c.Close()
		}
	}()
	if !dials(one.Addr()) {
		t.Errorf("nothing accepts at %s after Listen", one.Addr())
	}

	free, err := ReservePorts([]string{"127.0.0.1:3"})
	if err != nil {
		t.Fatal(err)
	}
	unhanded := free["127.0.0.1:3"].Addr()
	free["127.0.0.1:3"].Release()
	l2, err := Listen(getenv, unhanded)
	if err != nil {
		t.Fatalf("Listen(%s), an address it was handed nothing for: %v", unhanded, err)
	}
	_ = l2.Close()

	mislabeled := map[string]string{ListenFDsEnv: fmt.Sprintf("127.0.0.1:9=%d", dupFD(t, two))}
	if l3, err := Listen(func(k string) string { return mislabeled[k] }, "127.0.0.1:9"); err == nil {
		_ = l3.Close()
		t.Errorf("Listen served a socket bound to %s as 127.0.0.1:9", two.Addr())
	}
}

// dupFD is a close-on-exec duplicate of r's socket, at a descriptor Listen accepts.
func dupFD(t *testing.T, r *Reserved) int {
	t.Helper()
	syscall.ForkLock.RLock()
	fd, err := syscall.Dup(int(r.File().Fd()))
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if fd < firstListenFD {
		t.Fatalf("the duplicate is fd %d, below the first a launch hands (%d)", fd, firstListenFD)
	}
	return fd
}
