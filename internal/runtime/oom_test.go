package runtime

import (
	"encoding/json"
	"testing"
)

func TestOOMKillerWarning(t *testing.T) {
	// Fires: macOS + podman + 137 + under-floor machine.
	msg, ok := OOMKillerWarning(137, "podman", true, "podman-machine-default", 2048, true)
	if !ok {
		t.Fatal("should fire")
	}
	want := "Exit 137 is SIGKILL.  On Podman Machine this often means the VM's OOM-killer fired — " +
		"'podman-machine-default' has only 2048 MB (below the 4096 MB recommended floor for running an agent).  " +
		PodmanMachineResizeHint()
	if msg != want {
		t.Errorf("msg = %q\nwant %q", msg, want)
	}
	// Doesn't fire: not macOS.
	if _, ok := OOMKillerWarning(137, "podman", false, "m", 2048, true); ok {
		t.Error("non-macOS should not fire")
	}
	// Not exit 137.
	if _, ok := OOMKillerWarning(1, "podman", true, "m", 2048, true); ok {
		t.Error("non-137 should not fire")
	}
	// Not podman.
	if _, ok := OOMKillerWarning(137, "container", true, "m", 2048, true); ok {
		t.Error("non-podman should not fire")
	}
	// Machine unavailable.
	if _, ok := OOMKillerWarning(137, "podman", true, "", -1, false); ok {
		t.Error("unavailable machine should not fire")
	}
	// At/above floor.
	if _, ok := OOMKillerWarning(137, "podman", true, "m", 4096, true); ok {
		t.Error("at-floor should not fire")
	}
}

// TestOOMKillerParity cross-checks the message text against a live-Python
// reconstruction of _maybe_warn_about_oom_killer's console string.
func TestOOMKillerParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from cli.runtime import _podman_machine_resize_hint, PODMAN_MACHINE_MEMORY_FLOOR_MB
name, mem = "podman-machine-default", 2048
msg = (f"Exit 137 is SIGKILL.  On Podman Machine this often means "
       f"the VM's OOM-killer fired — '{name}' has only {mem} MB "
       f"(below the {PODMAN_MACHINE_MEMORY_FLOOR_MB} MB recommended floor "
       f"for running an agent).  {_podman_machine_resize_hint()}")
print(json.dumps({"msg": msg}))
`
	out, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python runtime import failed: %v", err)
	}
	var want struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, _ := OOMKillerWarning(137, "podman", true, "podman-machine-default", 2048, true)
	if got != want.Msg {
		t.Errorf("msg mismatch:\n go: %q\n py: %q", got, want.Msg)
	}
}
