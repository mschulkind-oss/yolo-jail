package integration

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// capSysPtrace is CAP_SYS_PTRACE's bit in a /proc/<pid>/status capability mask.
const capSysPtrace = uint64(1) << 19

// ownCapBound is this test process's CapBnd: the bounding set the launcher, and so every jail it
// starts, is limited to.
func ownCapBound(t *testing.T) uint64 {
	t.Helper()
	f, err := os.Open("/proc/self/status")
	if err != nil {
		t.Fatalf("read this process's capability masks: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "CapBnd:"); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			if err != nil {
				t.Fatalf("parse CapBnd %q: %v", v, err)
			}
			return n
		}
	}
	t.Fatal("/proc/self/status has no CapBnd line")
	return 0
}

// TestNestedPodmanExecIntoANonRootContainer: a jail is granted CAP_SYS_PTRACE
// (docs/reference/security-shim.md#sys-ptrace), so a `podman exec` and a `podman healthcheck run`
// into a nested container running as uid 1000 work. Without the capability crun, running as the
// jail's root, cannot open /proc/<pid>/ns/mnt of a process with another uid, and both fail;
// a container running as root never showed it.
//
// THE WITNESS is the exec itself and the healthcheck, on a `--rootfs` of an empty directory
// with the store mounted in, so no image is pulled (TestNestedPodmanRunsOverAUserContainersConf
// uses the same shape). The jail's CapEff is printed beside it.
//
// It skips when this process's own bounding set lacks CAP_SYS_PTRACE: podman drops a cap-add
// outside it rather than refusing (measured 2026-11-13), so a jail started from a jail that an
// older yolo launched cannot hold the capability whatever this launcher asks for.
func TestNestedPodmanExecIntoANonRootContainer(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" || detectRuntime() != "podman" {
		t.Skip("CAP_SYS_PTRACE is granted on podman on Linux only; no other backend has a Linux capability set yolo adjusts")
	}
	if bnd := ownCapBound(t); bnd&capSysPtrace == 0 {
		t.Skipf("this launcher's bounding set (CapBnd=%016x) lacks CAP_SYS_PTRACE, so no jail it starts "+
			"can hold it: it is itself in a jail an older yolo launched. Run this on CI, or in a "+
			"jail the current yolo launched", bnd)
	}
	dir := writeProject(t, `{}`)
	script := `
test -f /etc/containers/yolo-required.conf || exit 3
awk '$1 == "CapEff:" {print "capeff=" $2}' /proc/self/status
bash_bin=$(readlink -f "$(command -v bash)")
sleep_bin=$(readlink -f "$(command -v sleep)")
r=$(mktemp -d); chmod 755 "$r"
podman run -d --name ptrace-probe --user 1000:1000 --network=host -v /nix/store:/nix/store:ro \
  --health-cmd "[\"$bash_bin\",\"-c\",\"true\"]" --health-interval disable \
  --rootfs "$r" "$sleep_bin" --coreutils-prog=sleep infinity >/dev/null || exit 5
podman exec ptrace-probe "$bash_bin" -c 'echo "EXEC_RAN uid=$UID"'; echo "exec_rc=$?"
podman healthcheck run ptrace-probe; echo "healthcheck_rc=$?"
podman rm -f -t 0 ptrace-probe >/dev/null 2>&1
`
	r := runYolo(t, dir, script)
	if r.rc == noRequiredPodmanConfRC {
		t.Skip("this jail image bakes no /etc/containers/yolo-required.conf (the minimal variant, " +
			"or an image built before it existed), so nested podman is not configured; rebuild the " +
			"full image (YOLO_TEST_REBUILD_IMAGE=1) to run this test")
	}
	out := r.combined()
	if r.rc == 5 {
		t.Fatalf("the uid-1000 nested container did not start, so the exec was never tried:\n%s", out)
	}
	capEff := uint64(0)
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "capeff="); ok {
			capEff, _ = strconv.ParseUint(v, 16, 64)
		}
	}
	if capEff&capSysPtrace == 0 {
		t.Errorf("the jail's CapEff (%016x) lacks CAP_SYS_PTRACE although its launcher's bounding set "+
			"has it: the launch did not add `--cap-add SYS_PTRACE`:\n%s", capEff, out)
	}
	if !strings.Contains(out, "EXEC_RAN uid=1000") || !strings.Contains(out, "exec_rc=0") {
		t.Errorf("a nested `podman exec` into a container running as uid 1000 failed:\n%s", out)
	}
	if !strings.Contains(out, "healthcheck_rc=0") {
		t.Errorf("a nested `podman healthcheck run` on a container running as uid 1000 failed:\n%s", out)
	}
}
