package run

import (
	"strings"
	"testing"
	"time"
)

// stalecollision_test.go covers the window between `created` and `running`.
//
// THE FAILURE IT REPRODUCES, measured twice on the macOS nightly (shard 9 of runs
// 34995936829 and 35033142943): the second launch in one workspace waited for the lock
// correctly, then created into a name collision and died with the runtime's own error —
//
//	Error: creating container storage: the container name "yolo-002-…" is already in use
//	by e1e5de83…
//
// rc 125, and it never attached.
//
// WHY IT NEEDS A UNIT TEST AND NOT AN INTEGRATION ONE. The integration test that catches it
// (TestConcurrentLaunchesInOneWorkspace) passes on an unloaded machine — verified on a real
// podman here, twice, at 70s and 124s — because the first container reaches `running` long
// before the second launch looks. Only a loaded runner widens the window enough. Driving the
// finders directly is what makes the case reproducible at all.

// stalePhase drives findRunningContainer/findExistingContainer/rm through Options.Exec, in the
// exact sequence the race produces. The argvs are podman's real ones (lifecycle.go):
//
//	running only : podman ps -q      --filter name=^/<cname>$
//	running OR not: podman ps -a -q  --filter name=^/<cname>$
//	remove       : podman rm <cname>
//
// `-a` is what separates the two, so it is what this switches on — matching on "ps" alone
// would make the narrow check answer like the wide one and the race would be unrepresentable.
type stalePhase struct {
	runningAfter int // how many `rm` attempts before the container reports RUNNING
	rmCalls      int
}

func staleOptions(t *testing.T, p *stalePhase) *Options {
	t.Helper()
	o := goldenOptions("/ws", t.TempDir())
	o.Now = func() time.Time { return time.Now() }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		joined := ""
		for _, a := range argv {
			joined += a + " "
		}
		switch {
		case strings.Contains(joined, " rm "):
			p.rmCalls++
			// `rm` REFUSES a live container. That refusal is the evidence.
			return ExecResult{Ran: true, RC: 2, Stderr: "container is running"}
		case strings.Contains(joined, " -a "):
			// The container exists from the other launch's create.
			return ExecResult{Ran: true, RC: 0, Stdout: "abcd1234deadbeef\n"}
		default:
			// `ps -q` without -a: RUNNING only once the other launch has started it.
			if p.rmCalls >= p.runningAfter {
				return ExecResult{Ran: true, RC: 0, Stdout: "abcd1234deadbeef\n"}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: ""}
		}
	}
	return o
}

// TestWaitForRunningContainerAttachesInsteadOfColliding is the fix, asserted on the seam the
// launch actually consults.
//
// The container becomes RUNNING one poll after the failed `rm` — which is the real sequence,
// since `rm` refusing it means it is already alive. A launch that waits finds it; one that does
// not goes straight to a create that cannot succeed.
func TestWaitForRunningContainerAttachesInsteadOfColliding(t *testing.T) {
	p := &stalePhase{runningAfter: 1}
	o := staleOptions(t, p)

	// Before any rm, the narrow check misses it — this is the state the post-lock re-check
	// sees, and why it falls through to the stale-removal block at all.
	if cid := o.findRunningContainer("yolo-002-abcd1234", "podman"); cid != "" {
		t.Fatalf("the container reported RUNNING before it was started: %q", cid)
	}
	// The wider check sees it, and `rm` refuses it. That pair IS the race.
	if cid := o.findExistingContainer("yolo-002-abcd1234", "podman"); cid == "" {
		t.Fatal("findExistingContainer missed a container the other launch had created")
	}
	if o.removeStaleContainer("yolo-002-abcd1234", "podman") {
		t.Fatal("removeStaleContainer claimed to remove a LIVE container — the whole fix " +
			"rests on that refusal being reported")
	}

	// The fix: having been refused, wait rather than create.
	if cid := o.waitForRunningContainer("yolo-002-abcd1234", "podman"); cid == "" {
		t.Error("waitForRunningContainer gave up on a container that was about to run.\n\n" +
			"That is the nightly failure: the launch then creates and the runtime answers " +
			"`the container name … is already in use`, rc 125, and the second launch never " +
			"attaches.")
	}
}

// TestWaitForRunningContainerGivesUpBounded pins the other direction, because an unbounded
// wait here would turn a genuinely wedged container into a hung launch.
//
// Giving up returns "" and the caller proceeds to the create it would have attempted anyway —
// so this can only convert a certain failure into a possible success, never the reverse.
func TestWaitForRunningContainerGivesUpBounded(t *testing.T) {
	// Never reports running, whatever happens.
	p := &stalePhase{runningAfter: 1 << 30}
	o := staleOptions(t, p)

	start := time.Now()
	if cid := o.waitForRunningContainer("yolo-002-abcd1234", "podman"); cid != "" {
		t.Fatalf("a container that never runs was reported as running: %q", cid)
	}
	elapsed := time.Since(start)
	if elapsed > 30*time.Second {
		t.Errorf("the wait took %s — it must be bounded, or a wedged container hangs the "+
			"launch instead of failing it", elapsed)
	}
}
