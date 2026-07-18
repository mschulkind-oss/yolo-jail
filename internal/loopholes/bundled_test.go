package loopholes

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// withRepoBundled points BundledLoopholesDir at the repo's real
// src/bundled_loopholes for the duration of a test.
func withRepoBundled(t *testing.T) string {
	t.Helper()
	root := repoRootDir(t)
	orig := BundledLoopholesDir
	dir := filepath.Join(root, "src", "bundled_loopholes")
	BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { BundledLoopholesDir = orig })
	return dir
}

// TestBundledManifestsParse smoke-tests that every shipped bundled manifest
// loads without error through the Go parser — a direct check the wheel's
// manifests stay Go-parseable.
func TestBundledManifestsParse(t *testing.T) {
	dir := withRepoBundled(t)
	for _, name := range []string{"audio", "claude-oauth-broker", "host-processes"} {
		lp, err := LoadLoophole(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("load %s: %v", name, err)
			continue
		}
		if lp.Name != name {
			t.Errorf("%s: name = %q", name, lp.Name)
		}
	}
}

// TestBundledAudioParity loads the REAL audio manifest through both Go and live
// Python with the same XDG_RUNTIME_DIR (both Pulse + PipeWire sockets present)
// and byte-compares the runtime argv — the highest-fidelity check that the full
// audio bridge (Pulse socket + PipeWire native + {loophole_dir}/asound.conf +
// /dev/snd device + jail_env) renders identically.
func TestBundledAudioParity(t *testing.T) {
	unsetJail(t)
	py := pythonRunner(t)
	if py == nil {
		t.Skip("no python oracle available")
	}
	dir := withRepoBundled(t)

	runtime := t.TempDir()
	mkdir(t, filepath.Join(runtime, "pulse"))
	os.WriteFile(filepath.Join(runtime, "pulse", "native"), nil, 0o644)
	os.WriteFile(filepath.Join(runtime, "pipewire-0"), nil, 0o644)
	t.Setenv("XDG_RUNTIME_DIR", runtime)

	audioDir := filepath.Join(dir, "audio")

	out := runOracle(t, py, map[string]any{"action": "load_and_args", "module_path": audioDir})
	pyArgs := toStringList(out["args"])

	lp, err := LoadLoophole(audioDir)
	if err != nil {
		t.Fatalf("load audio: %v", err)
	}
	if !lp.Active() {
		t.Fatal("audio should be active with both sockets present")
	}
	goArgs := RuntimeArgsFor([]*Loophole{lp}, "")

	if !reflect.DeepEqual(goArgs, pyArgs) {
		t.Errorf("bundled audio argv mismatch:\n go: %#v\n py: %#v", goArgs, pyArgs)
	}
}

// TestBundledAudioPulseOnlyParity mirrors the pulse-only-host fixture: the
// PipeWire socket is absent, so runtime_args_for skips that one bind mount while
// the Pulse mount + asound.conf survive. Compared live.
func TestBundledAudioPulseOnlyParity(t *testing.T) {
	unsetJail(t)
	py := pythonRunner(t)
	if py == nil {
		t.Skip("no python oracle available")
	}
	dir := withRepoBundled(t)

	runtime := t.TempDir()
	mkdir(t, filepath.Join(runtime, "pulse"))
	os.WriteFile(filepath.Join(runtime, "pulse", "native"), nil, 0o644)
	// No pipewire-0 socket.
	t.Setenv("XDG_RUNTIME_DIR", runtime)

	audioDir := filepath.Join(dir, "audio")
	out := runOracle(t, py, map[string]any{"action": "load_and_args", "module_path": audioDir})
	pyArgs := toStringList(out["args"])

	lp, err := LoadLoophole(audioDir)
	if err != nil {
		t.Fatal(err)
	}
	goArgs := RuntimeArgsFor([]*Loophole{lp}, "")
	if !reflect.DeepEqual(goArgs, pyArgs) {
		t.Errorf("pulse-only argv mismatch:\n go: %#v\n py: %#v", goArgs, pyArgs)
	}
}

// TestBundledBrokerParity compares the claude-oauth-broker manifest's argv,
// which exercises the {state} ca_cert template + jail_daemon dir mount. The
// broker's requires.command_on_path is "claude"; both sides see the same PATH,
// so active/inactive agree. Uses discover so both sides resolve state_dir the
// same way (default) — and only compares when the state ca.crt is absent (the
// common case), which is deterministic.
func TestBundledBrokerParity(t *testing.T) {
	unsetJail(t)
	py := pythonRunner(t)
	if py == nil {
		t.Skip("no python oracle available")
	}
	dir := withRepoBundled(t)
	brokerDir := filepath.Join(dir, "claude-oauth-broker")

	out := runOracle(t, py, map[string]any{"action": "load_and_args", "module_path": brokerDir})
	pyArgs := toStringList(out["args"])

	lp, err := LoadLoophole(brokerDir)
	if err != nil {
		t.Fatal(err)
	}
	goArgs := RuntimeArgsFor([]*Loophole{lp}, "")
	if !reflect.DeepEqual(goArgs, pyArgs) {
		t.Errorf("broker argv mismatch:\n go: %#v\n py: %#v", goArgs, pyArgs)
	}
}
