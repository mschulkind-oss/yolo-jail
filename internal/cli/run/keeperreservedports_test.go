package run

// keeperreservedports_test.go pins the container half of RESERVED PORTS (servedaddresses.go,
// docs/plans/notch-convergence.md NC-D70): the ports a fresh shared-namespace launch reserved for
// the jail's daemons are handed to its keeper, which holds them while it starts the jail's host
// services and lets them go just before it starts the container whose daemons bind them.

import (
	"fmt"
	"go/ast"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// bindProbeChildMain binds addr once and says whether it could, on stderr.
func bindProbeChildMain(addr string) int {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not bind %s: %v\n", addr, err)
		return 0
	}
	_ = l.Close()
	fmt.Fprintf(os.Stderr, "bound %s\n", addr)
	return 0
}

// THE JAIL'S PORTS ARE THE KEEPER'S UNTIL THE CONTAINER STARTS. The fresh launch used to let them
// go before it spawned its keeper, and the keeper then fronts the jail's host services, each on a
// port-0 listener, before it starts the container: on a shared namespace one of those fronts could
// be handed a port the launch had composed a jail daemon's clients with, the daemon's bind failed,
// and the boot refused the launch. Through startKeeper and the in-process keeper (TestMain's
// inProcessKeeper): while the keeper waits for its liveness lock, before any of its own work, the
// port must still be held, which only the keeper can be doing once the launch has let its own copy
// go; and the container's main process, which stands in for the jail daemon, must find it free.
func TestTheKeeperHoldsTheJailsReservedPortsUntilItStartsTheContainer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	cname := "yolo-keeper-reserved-ports"
	jail := newFakeJail(t, cname)

	// The launch's pick, as settleServedAddresses makes it on a shared namespace.
	picked, err := launchservice.ReservePorts([]string{"127.0.0.1:8214"})
	if err != nil {
		t.Fatal(err)
	}
	addr := picked["127.0.0.1:8214"].Addr()
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.served = servedAddressState{moved: map[string]string{"127.0.0.1:8214": addr},
		held: map[string]*launchservice.Reserved{addr: picked["127.0.0.1:8214"]}}
	t.Cleanup(o.releaseReservedPorts)
	o.Exec = jail.exec
	o.PIDAlive = func(int) bool { return false }
	o.StartDetached = func([]string, *os.File) error { return errTestBinarySelfExec }

	// The keeper's first act waits on this.
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	saved := keeperLivenessWait
	keeperLivenessWait = 30 * time.Second
	t.Cleanup(func() { keeperLivenessWait = saved })

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stop := filepath.Join(jail.dir, "stop")
	script := recordPID(jail.dir) + shquote.Quote(exe) + " -bind-probe-child " + addr + "; echo \"" +
		entrypoint.BootReadyLine + "\" >&2; " + holdUntil(stop) + "; exit 143"
	cfg, err := encodeConfig(jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	plan := &keeperPlan{Build: keeperBuildStamp(), Workspace: o.Workspace, Cname: cname, Runtime: "podman",
		Config: cfg, SocketsDir: hostServiceSocketsDir(cname, false), RunCmd: []string{"sh", "-c", script},
		ImageRef: "the-image"}
	kp, err := o.startKeeper(plan)
	if err != nil {
		releaseLock(live)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(stop, nil, 0o644)
		kp.closeLifeline()
		select {
		case <-kp.exited:
		case <-time.After(30 * time.Second):
			t.Error("the keeper did not end")
		}
	})

	if other, err := net.Listen("tcp", addr); err == nil {
		_ = other.Close()
		t.Errorf("another listener bound %s, a port the launch reserved for the jail's daemon, after "+
			"the launch spawned its keeper and before the keeper started anything: nothing held it", addr)
	}
	releaseLock(live)

	var out, errOut, jailOut, jailErr lockedBuffer
	if !relayKeeper(kp.progress, &out, &errOut, &jailOut, &jailErr, keeperEvents{}) {
		t.Fatalf("the keeper ended before ready:\n%s\n%s", errOut.String(), jailErr.String())
	}
	if !strings.Contains(jailErr.String(), "bound "+addr) {
		t.Errorf("the container's main process could not bind %s, the port its daemon was composed "+
			"with: the keeper still held it when it started the container\n%s", addr, jailErr.String())
	}
}

// THE KEEPER'S ARGV NAMES EACH RESERVED PORT AT THE DESCRIPTOR THE SPAWN HANDS IT: realSpawnKeeper
// appends them to ExtraFiles after the launch lock, or after the lifeline when there is no lock.
func TestTheKeepersArgvNamesEachReservedPortWhereTheSpawnHandsIt(t *testing.T) {
	for _, tc := range []struct {
		lock     bool
		reserved int
		want     string
	}{
		{true, 2, "--lock-fd 5 --reserved-fd 6 --reserved-fd 7"},
		{false, 1, "--lifeline-fd 4 --reserved-fd 5"},
		{true, 0, "--lock-fd 5"},
	} {
		got := strings.Join(keeperArgv("/plan", tc.lock, tc.reserved), " ")
		if !strings.HasSuffix(got, tc.want) {
			t.Errorf("keeperArgv(lock=%v, reserved=%d) = %q, want it to end %q", tc.lock, tc.reserved, got, tc.want)
		}
	}
}

// THE KEEPER LETS THE PORTS GO BETWEEN ITS HOST SERVICES AND THE CONTAINER: in keeper.run, after
// every host service and the credential view are started and before the container's main process,
// so no front the keeper binds can be handed one, and the jail's daemons find them free.
func TestTheKeeperReleasesTheReservedPortsAfterItsHostServicesAndBeforeTheContainer(t *testing.T) {
	fd := funcDecl(t, "keeper.go", "run")
	pos := map[string]token.Pos{}
	ast.Inspect(fd, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit, *ast.DeferStmt:
			return false // a deferred release is the unwind's, not the order's
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if name := skelCallee(call); name != "" {
				if _, seen := pos[name]; !seen {
					pos[name] = call.Pos()
				}
			}
		}
		return true
	})
	release, ok := pos["releaseReservedPorts"]
	if !ok {
		t.Fatal("keeper.run never lets the jail's reserved ports go, so its daemons cannot bind them")
	}
	for _, before := range []string{"startPortForwards", "startPlannedLoopholes", "registerClaudeCredentialView"} {
		if p, ok := pos[before]; !ok || p > release {
			t.Errorf("keeper.run lets the reserved ports go before %s, whose listeners could then be handed one", before)
		}
	}
	// startJailMainWithEnv since the jail's --with-credentials values ride the client's environment
	// (jailgrant.go, ES-D32); either spelling starts the container.
	p, ok := pos["startJailMainWithEnv"]
	if !ok {
		p, ok = pos["startJailMain"]
	}
	if !ok || p < release {
		t.Error("keeper.run starts the container before it lets the reserved ports go, so the jail's daemons cannot bind them")
	}
}
