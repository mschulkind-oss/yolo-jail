package run

// acforwardrefusal_test.go pins the refusal of `network.forward_host_ports` on Apple Container.
//
// The key used to reach that backend's argv as `--publish-socket <host socket>:…`, after the
// host-side socat had already created the socket, and `container run` 1.1.0 then failed the
// launch naming the socket rather than the key (measured 2026-09-16; the user guide's acfwd
// note). That flag forwards a HOST connection inward, the opposite direction, and the backend
// carries no traffic from a container back to the Mac at all, so there was never a forward to
// deliver: the launch only failed later, in the backend's words. The setup census found the
// cell answered none of its six dispositions honestly (docs/design/backend-parity.md §4.2), and
// this refusal is what makes its `Refused` true: the launch stops before anything starts, and
// names the key, its entries and the two ways on.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
)

func TestAppleContainerRefusesForwardHostPortsNamingTheKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.IsMacOS, o.IsLinux = true, false

	got := o.appleContainerForwardRefusal("container", forwardsConfig(""))
	for _, want := range []string{"Refusing to launch", "`network.forward_host_ports`",
		"Apple Container", "9090:5432", "YOLO_RUNTIME=podman"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must say %q:\n%s", want, got)
		}
	}
	// appliedNetMode answers bridge on this backend whatever the key says, so an unhonored
	// `network.mode: "host"` leaves the forward declared and the refusal standing.
	if got := o.appleContainerForwardRefusal("container", forwardsConfig("host")); got == "" {
		t.Error("an unhonored network.mode \"host\" must not hide the forward from the refusal")
	}

	if got := o.appleContainerForwardRefusal("podman", forwardsConfig("")); got != "" {
		t.Errorf("podman forwards the key, so it must not refuse it:\n%s", got)
	}
	if got := o.appleContainerForwardRefusal("container", newConfig()); got != "" {
		t.Errorf("a launch declaring no forward has nothing to refuse:\n%s", got)
	}
	sealed := *o
	sealed.Sealed = true
	if got := sealed.appleContainerForwardRefusal("container", forwardsConfig("")); got != "" {
		t.Errorf("a sealed build forwards nothing, so it has nothing to refuse:\n%s", got)
	}

	// The census cell this refusal makes true, read back so the two cannot part.
	if e, _ := setupcensus.Find("network.forward_host_ports"); e.AppleContainer.Disposition != setupcensus.Refused {
		t.Errorf("the launch refuses forward_host_ports on Apple Container, and the setup census says %s",
			e.AppleContainer.Disposition)
	}
}

// The call site: runContainer asks after the argv is assembled (where the other "this backend
// cannot run what the argv asks" pre-flight sits) and before the keeper, which starts the
// host-side socat and then the container, is spawned. An expression pin in the shape of
// TestRunChecksEveryBindSourceBeforeTheContainerStarts, since driving Run to that point needs a
// loaded image; TestNoReturnAfterTheSkeletonLeaksIt covers the refusal's return.
func TestRunRefusesAppleContainerForwardsBeforeAnythingStarts(t *testing.T) {
	src := runSource(t)
	call := strings.Index(src, "o.appleContainerForwardRefusal(rt, cfg)")
	assembled := strings.Index(src, "runCmd := o.assembleRunCmd(in)")
	started := strings.Index(src, "o.startKeeper(plan)")
	if call < 0 {
		t.Fatal("runContainer no longer refuses network.forward_host_ports on Apple Container: the " +
			"launch would start socat and then fail inside `container run`, naming a socket")
	}
	if assembled < 0 || started < 0 || !(assembled < call && call < started) {
		t.Errorf("the forward refusal is not between assembly (%d) and the keeper's spawn (%d): at %d",
			assembled, started, call)
	}
}
