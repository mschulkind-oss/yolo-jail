package containerbuilder

import (
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
