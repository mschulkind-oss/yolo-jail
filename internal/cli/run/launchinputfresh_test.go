package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// freshHomeMCPOptions returns Options wired to the REAL mcp-auth-share destination
// (loopholes.StateDirFor("mcp-auth-broker")) under a HOME that has never run a launch, with a
// fixture body standing in for the MCP composition, and the assembled jail mount argv.
func freshHomeMCPOptions(t *testing.T, cname string) (*Options, *strings.Builder, string) {
	t.Helper()
	workspace := t.TempDir()
	mounts := assembledPrivateFixtureMounts(t, workspace, cname) // sets HOME to a fresh dir
	stateRoot := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "state")
	if _, err := os.Lstat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("fixture HOME is not fresh: %s exists (%v)", stateRoot, err)
	}
	previous := loopholes.StateDirFor
	loopholes.StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	t.Cleanup(func() { loopholes.StateDirFor = previous })
	real := launchInputProducers()["mcp-auth-share"]
	var output strings.Builder
	o := &Options{Workspace: workspace, launchInputProducers: map[string]LaunchInputProducer{
		"mcp-auth-share": {StateDir: real.StateDir, Produce: func(LaunchInputContext) ([]byte, error) { return []byte(`{"fixture":true}`), nil }},
	}}
	fillDefaults(o)
	o.Stdout, o.Stderr = &output, &output
	o.launchInputMountArgv = mounts
	return o, &output, filepath.Join(stateRoot, "mcp-auth-broker")
}

func mcpLaunchInputLoophole() *loopholes.Loophole {
	return &loopholes.Loophole{Name: "mcp-auth-broker", HostDaemon: &loopholes.HostDaemon{LaunchInput: "mcp-auth-share"}}
}

// TestLaunchInputOnAFreshHomeCreatesThePrivateStateDir is the first launch on a machine: the
// mcp-auth-broker state directory does not exist until its daemon runs, and the daemon runs only
// after its launch input is proven outside every jail mount. Before the fix, the proof could not
// resolve the missing directory and every fresh-HOME launch refused the service.
func TestLaunchInputOnAFreshHomeCreatesThePrivateStateDir(t *testing.T) {
	cname := "yolo-fresh-home-launch-input"
	o, output, stateDir := freshHomeMCPOptions(t, cname)
	if !o.writeLaunchInput(mcpLaunchInputLoophole(), cname) {
		t.Fatalf("fresh-HOME launch input refused: %s", output.String())
	}
	info, err := os.Lstat(stateDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir %s = %v, %v; want a 0700 directory", stateDir, info, err)
	}
	path := o.launchInputFiles["mcp-auth-broker"]
	if path != filepath.Join(stateDir, "replicas", cname+".json") {
		t.Fatalf("launch input published at %q", path)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != `{"fixture":true}` {
		t.Fatalf("launch input = %q, %v", body, err)
	}
}

// TestLaunchInputOnAFreshHomeStillRefusesAMountedStateRoot keeps the safety check: creating
// the directory must not let a private path inside a jail mount through, and the refusal names
// the path and the mount.
func TestLaunchInputOnAFreshHomeStillRefusesAMountedStateRoot(t *testing.T) {
	cname := "yolo-fresh-home-mounted-state"
	o, output, stateDir := freshHomeMCPOptions(t, cname)
	mounted := filepath.Dir(filepath.Dir(stateDir)) // ~/.local/share/yolo-jail
	if err := os.MkdirAll(mounted, 0o700); err != nil {
		t.Fatal(err)
	}
	o.launchInputMountArgv = append(o.launchInputMountArgv, "-v", mounted+":/leak:ro")
	if o.writeLaunchInput(mcpLaunchInputLoophole(), cname) {
		t.Fatalf("launch input inside a jail mount was published: %v", o.launchInputFiles)
	}
	if !strings.Contains(output.String(), filepath.Join(stateDir, "replicas", cname+".json")) || !strings.Contains(output.String(), mounted) {
		t.Fatalf("refusal does not name the path and the mount: %s", output.String())
	}
}

// TestLaunchInputOnAFreshHomeRefusesALinkedStateDir: the leaf is never followed, so a link
// planted where the state directory belongs refuses rather than redirecting the secret.
func TestLaunchInputOnAFreshHomeRefusesALinkedStateDir(t *testing.T) {
	cname := "yolo-fresh-home-linked-state"
	o, output, stateDir := freshHomeMCPOptions(t, cname)
	if err := os.MkdirAll(filepath.Dir(stateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, stateDir); err != nil {
		t.Fatal(err)
	}
	if o.writeLaunchInput(mcpLaunchInputLoophole(), cname) {
		t.Fatal("launch input followed a planted state-dir link")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("link target received %d entries", len(entries))
	}
	if !strings.Contains(output.String(), stateDir) {
		t.Fatalf("refusal does not name the state dir: %s", output.String())
	}
}

// TestLaunchInputOnAFreshHomeFollowsALinkedStateRoot: only the loophole's own directory is the
// no-follow point (paths.OpenStateDirRoot follows the components above it), so a state ROOT that
// is a link to a real directory, as a relocated ~/.local/share is, still gets its first input.
func TestLaunchInputOnAFreshHomeFollowsALinkedStateRoot(t *testing.T) {
	cname := "yolo-fresh-home-linked-root"
	o, output, stateDir := freshHomeMCPOptions(t, cname)
	stateRoot := filepath.Dir(stateDir)
	if err := os.MkdirAll(filepath.Dir(stateRoot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), stateRoot); err != nil {
		t.Fatal(err)
	}
	if !o.writeLaunchInput(mcpLaunchInputLoophole(), cname) {
		t.Fatalf("a linked state root refused the first launch input: %s", output.String())
	}
	if info, err := os.Lstat(stateDir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir %s = %v, %v; want a real 0700 directory", stateDir, info, err)
	}
}
