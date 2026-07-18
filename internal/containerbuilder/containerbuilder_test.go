package containerbuilder

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPullArgv(t *testing.T) {
	if got := PullArgv("podman", ""); !reflect.DeepEqual(got, []string{"podman", "pull", BuilderImage}) {
		t.Errorf("podman pull = %v", got)
	}
	if got := PullArgv("container", ""); !reflect.DeepEqual(got, []string{"container", "image", "pull", BuilderImage}) {
		t.Errorf("container pull = %v", got)
	}
}

func TestRunArgv(t *testing.T) {
	// podman publishes the loopback port.
	got := RunArgv("podman", "ssh-ed25519 AAAA", "", "", 0)
	want := []string{
		"podman", "run", "-d", "--rm", "--name", "yolo-linux-builder",
		"-e", "YOLO_BUILDER_PUBKEY=ssh-ed25519 AAAA",
		"-p", "127.0.0.1:31022:22", BuilderImage,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("podman run:\n got %v\n want %v", got, want)
	}
	// Apple Container omits -p.
	got = RunArgv("container", "PUB", "", "", 0)
	want = []string{
		"container", "run", "-d", "--rm", "--name", "yolo-linux-builder",
		"-e", "YOLO_BUILDER_PUBKEY=PUB", BuilderImage,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("container run:\n got %v\n want %v", got, want)
	}
}

func TestBuilderURIAndLine(t *testing.T) {
	uri := BuilderURI("127.0.0.1", 0, "/keys/id_ed25519")
	if uri != "ssh-ng://root@127.0.0.1:31022?ssh-key=/keys/id_ed25519" {
		t.Errorf("uri = %q", uri)
	}
	line := BuildersLine("192.168.64.2", 22, 4, "/keys/id_ed25519")
	if line != "ssh-ng://root@192.168.64.2:22 aarch64-linux /keys/id_ed25519 4" {
		t.Errorf("builders line = %q", line)
	}
}

func TestNixSSHOpts(t *testing.T) {
	if NixSSHOpts() != "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" {
		t.Errorf("opts = %q", NixSSHOpts())
	}
}

func TestReachableAddressFromContainerLs(t *testing.T) {
	stdout := "ID                  IMAGE  STATE    ADDR\n" +
		"yolo-linux-builder  img    running  192.168.64.2/24\n" +
		"other               img    running  192.168.64.3/24\n"
	host, port, ok := ReachableAddressFromContainerLs(stdout, "yolo-linux-builder")
	if !ok || host != "192.168.64.2" || port != 22 {
		t.Errorf("= %q,%d,%v", host, port, ok)
	}
	// Not present -> not found.
	if _, _, ok := ReachableAddressFromContainerLs(stdout, "missing"); ok {
		t.Error("missing container should not resolve")
	}
	// Header-only -> not found.
	if _, _, ok := ReachableAddressFromContainerLs("ID IMAGE STATE ADDR", "x"); ok {
		t.Error("header-only should not resolve")
	}
}

// TestParityVsLivePython cross-checks argv/URI/line against live
// container_builder.py, injecting a fixed key path so BUILDER_KEY doesn't skew.
func TestParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from cli import container_builder as cb
out = {
  "pull_podman": cb.pull_argv("podman"),
  "pull_ac": cb.pull_argv("container"),
  "run_podman": cb.run_argv("podman", "PUB"),
  "run_ac": cb.run_argv("container", "PUB"),
  "uri": cb.builder_uri("127.0.0.1"),
  "line": cb.builders_line("192.168.64.2", 22, 4),
  "ssh_opts": cb.nix_ssh_opts(),
  "key": str(cb.BUILDER_KEY),
}
print(json.dumps(out))
`
	outBytes, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python container_builder import failed: %v", err)
	}
	var want struct {
		PullPodman []string `json:"pull_podman"`
		PullAC     []string `json:"pull_ac"`
		RunPodman  []string `json:"run_podman"`
		RunAC      []string `json:"run_ac"`
		URI        string   `json:"uri"`
		Line       string   `json:"line"`
		SSHOpts    string   `json:"ssh_opts"`
		Key        string   `json:"key"`
	}
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := PullArgv("podman", ""); !reflect.DeepEqual(got, want.PullPodman) {
		t.Errorf("pull podman go=%v py=%v", got, want.PullPodman)
	}
	if got := PullArgv("container", ""); !reflect.DeepEqual(got, want.PullAC) {
		t.Errorf("pull ac go=%v py=%v", got, want.PullAC)
	}
	if got := RunArgv("podman", "PUB", "", "", 0); !reflect.DeepEqual(got, want.RunPodman) {
		t.Errorf("run podman go=%v py=%v", got, want.RunPodman)
	}
	if got := RunArgv("container", "PUB", "", "", 0); !reflect.DeepEqual(got, want.RunAC) {
		t.Errorf("run ac go=%v py=%v", got, want.RunAC)
	}
	// URI/line embed BUILDER_KEY — feed Python's resolved key to the Go side.
	if got := BuilderURI("127.0.0.1", 0, want.Key); got != want.URI {
		t.Errorf("uri go=%q py=%q", got, want.URI)
	}
	if got := BuildersLine("192.168.64.2", 22, 4, want.Key); got != want.Line {
		t.Errorf("line go=%q py=%q", got, want.Line)
	}
	if NixSSHOpts() != want.SSHOpts {
		t.Errorf("ssh opts go=%q py=%q", NixSSHOpts(), want.SSHOpts)
	}
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
