package integration

import (
	"strings"
	"testing"
)

// noRequiredPodmanConfRC is the script's exit status for an image that bakes no
// /etc/containers/yolo-required.conf: the minimal variant (CI's), which carries no nested-podman
// config at all, or an image built before the file existed (a stale image the suite was told
// to accept with YOLO_TEST_IMAGE_SKEW=warn or off).
const noRequiredPodmanConfRC = 3

// TestNestedPodmanRunsOverAUserContainersConf: a user ~/.config/containers/containers.conf,
// even one holding only an [engine] key, stops podman applying the image's
// /etc/containers/containers.conf `cgroups = "disabled"`, and every `podman run` in the jail then
// fails on the read-only /sys/fs/cgroup (crun: cgroup.subtree_control, exit 126). The entrypoint
// keeps the required keys in force through CONTAINERS_CONF_OVERRIDE (podmanconf.go).
//
// THE WITNESS is a real `podman run`, on a `--rootfs` of an empty directory with the store
// mounted in, so no image is pulled: the cgroup decision is made before the command runs, and
// without the override the run fails exactly as the bug does.
func TestNestedPodmanRunsOverAUserContainersConf(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	script := `
test -f /etc/containers/yolo-required.conf || exit 3
echo "override=${CONTAINERS_CONF_OVERRIDE:-unset}"
mkdir -p ~/.config/containers
printf '[engine]\ntmp_dir = "/tmp/podman-engine-tmp"\n' > ~/.config/containers/containers.conf
r=$(mktemp -d)
podman run --rm --network=host -v /nix/store:/nix/store:ro --rootfs "$r" \
  "$(readlink -f "$(command -v bash)")" -c 'echo NESTED_RAN'
`
	r := runYolo(t, dir, script)
	if r.rc == noRequiredPodmanConfRC {
		t.Skip("this jail image bakes no /etc/containers/yolo-required.conf (the minimal variant, " +
			"or an image built before this change); rebuild the full image " +
			"(YOLO_TEST_REBUILD_IMAGE=1) to run this test")
	}
	out := r.combined()
	if !strings.Contains(out, "override=/etc/containers/yolo-required.conf") {
		t.Errorf("the session's CONTAINERS_CONF_OVERRIDE is not the image's required file:\n%s", out)
	}
	if r.rc != 0 || !strings.Contains(out, "NESTED_RAN") {
		t.Fatalf("a nested `podman run` failed with a user containers.conf present (rc %d); "+
			"the image's cgroups = \"disabled\" was not kept in force:\n%s", r.rc, out)
	}
}
