package run

import "testing"

// TestCaptureConfigOnTerminateIsInvokedWithTheJailsIdentity: the teardown hook gets
// the workspace and the resolved runtime, which is everything the host-side capture
// needs to find the surfaces (the jail home is laid out differently per backend).
func TestCaptureConfigOnTerminateIsInvokedWithTheJailsIdentity(t *testing.T) {
	var gotWS, gotRT string
	calls := 0
	o := &Options{
		Workspace: "/some/workspace",
		CaptureOnTerminate: func(ws, rt string) {
			calls++
			gotWS, gotRT = ws, rt
		},
	}
	o.captureConfigOnTerminate("podman")
	if calls != 1 {
		t.Fatalf("hook called %d times, want 1", calls)
	}
	if gotWS != "/some/workspace" || gotRT != "podman" {
		t.Errorf("hook got (%q, %q), want (/some/workspace, podman)", gotWS, gotRT)
	}
}

// TestCaptureConfigOnTerminateNilIsANoOp: an unwired hook is a legitimate state
// (every test-constructed Options, and any build that does not inject it), not a
// nil-deref during teardown.
func TestCaptureConfigOnTerminateNilIsANoOp(t *testing.T) {
	o := &Options{Workspace: "/ws"}
	o.captureConfigOnTerminate("podman") // must not panic
}

// TestCaptureConfigOnTerminateSwallowsAPanic is R7 at the run seam: a jail that
// will not exit cleanly because an observability fold blew up is a worse bug than
// the stale `yolo config diff` the fold exists to fix. The teardown continues.
func TestCaptureConfigOnTerminateSwallowsAPanic(t *testing.T) {
	o := &Options{
		Workspace:          "/ws",
		CaptureOnTerminate: func(string, string) { panic("capture exploded") },
	}
	o.captureConfigOnTerminate("podman")
	// Reaching here IS the assertion: the teardown statements after this call in
	// runContainer (clearOwnerPID, the OOM notice, the profile report) still run.
}
