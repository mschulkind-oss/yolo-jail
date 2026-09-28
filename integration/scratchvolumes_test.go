package integration

// scratchvolumes_test.go pins, end to end, that quitting a podman jail does not wait for
// its scratch volumes to be deleted, and that they ARE deleted shortly after.
//
// THE BUG. /tmp, /var/tmp and the nested container dirs were ANONYMOUS volumes under
// `podman run --rm`, which makes the attached client delete them itself, file by file,
// before it exits — while the launcher waits on it and the terminal waits on the
// launcher. 32 s on the maintainer's host after a 59 h session (docs/reference/perf-logging.md,
// "The linger was the scratch volumes"). Reproduced in a nested jail 2026-09-28 with bare
// podman: 200,000 empty files in an anonymous /tmp held the client 8.8 s past its
// container's exit; the same files in a named volume, 0.2 s.
//
// These are the CALL-SITE pins: runContainer minting a real per-launch id (the mount
// source must parse as a scratch volume the reaper recognises), the teardown starting
// the remover without waiting, the remover finishing, and the housekeeping slot reaping
// what no remover reached. The unit tier (internal/cli/run/scratchremoval_test.go,
// internal/prune/scratchvolumes_test.go) pins each callee.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// scratchVolumesOf lists the scratch volumes the runtime holds for cname.
func scratchVolumesOf(t *testing.T, cname string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, detectRuntime(), "volume", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		t.Fatalf("volume ls: %v", err)
	}
	var mine []string
	for _, line := range strings.Split(string(out), "\n") {
		if n := strings.TrimSpace(line); strings.HasPrefix(n, cname+".scratch.") {
			mine = append(mine, n)
		}
	}
	return mine
}

func requirePodman(t *testing.T) {
	t.Helper()
	if detectRuntime() != "podman" {
		t.Skip("scratch volumes exist on podman only; Apple Container's scratch is tmpfs")
	}
}

func TestQuitDoesNotWaitForTheScratchDelete(t *testing.T) {
	requireJail(t)
	requirePodman(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	// 200k files in /tmp: 8.8 s of client-side unlinkat on the nested podman this was
	// measured on, when the volume was anonymous. The last line is the moment the jail's
	// command finished, by the jail's own clock (the same kernel's).
	const files = 200000
	script := `set -e
mkdir -p /tmp/fill && cd /tmp/fill
for d in $(seq 1 200); do mkdir $d; (cd $d && seq 1 1000 | xargs touch); done
awk '$5=="/tmp"{print "TMP_SOURCE=" $4}' /proc/self/mountinfo
echo "EXIT_AT=$(date +%s.%N)"`
	res := runYolo(t, dir, script, withTimeout(10*time.Minute))
	returned := time.Now()
	if res.rc != 0 {
		t.Fatalf("rc=%d\n%s", res.rc, res.combined())
	}

	// The mount source names a scratch volume with a real launch id — what the reaper
	// and the remover recognise. An empty id would be a name every launch reused.
	m := regexp.MustCompile(`TMP_SOURCE=\S*/volumes/(` + regexp.QuoteMeta(cname) + `\.scratch\.[0-9a-f]{16}\.tmp)/_data`).
		FindStringSubmatch(res.stdout)
	if m == nil {
		t.Fatalf("/tmp is not a per-launch named scratch volume of %s:\n%s", cname, res.stdout)
	}

	em := regexp.MustCompile(`EXIT_AT=([0-9.]+)`).FindStringSubmatch(res.stdout)
	if em == nil {
		t.Fatalf("no EXIT_AT line:\n%s", res.stdout)
	}
	exitAt, err := strconv.ParseFloat(em[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	lag := returned.Sub(time.Unix(0, int64(exitAt*1e9)))
	t.Logf("jail command done -> yolo returned: %s (with %d files in /tmp)", lag.Round(time.Millisecond), files)
	// The bound is generous against the fixed path (well under 2 s here) and below what
	// the anonymous-volume delete of this many files cost on the same machine.
	if lag > 5*time.Second {
		t.Errorf("yolo returned %s after the jail's command finished; the scratch delete is on the critical path again", lag)
	}

	// And the volumes do go, shortly after, by the detached remover.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		left := scratchVolumesOf(t, cname)
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(dir, ".yolo", "housekeeping.log"))
			t.Fatalf("scratch volumes still present 2 min after the quit: %v\nhousekeeping.log:\n%s", left, log)
		}
		time.Sleep(time.Second)
	}
	log, _ := os.ReadFile(filepath.Join(dir, ".yolo", "housekeeping.log"))
	if !strings.Contains(string(log), "scratch: removed 4 volume(s)") {
		t.Errorf("the remover left no record of its run:\n%s", log)
	}
}

// What no remover reached — a launcher SIGKILLed before its teardown, a host that went
// down mid-delete — is dangling, and the next launch's housekeeping slot removes it once
// it is past the age floor. A young dangling volume (a jail being created right now) and
// a volume that is not a scratch volume are left alone.
func TestTheSlotReapsLeftoverScratchVolumes(t *testing.T) {
	requireJail(t)
	requirePodman(t)
	rt := detectRuntime()
	mkvol := func(name string) {
		t.Helper()
		if out, err := exec.Command(rt, "volume", "create", name).CombinedOutput(); err != nil {
			t.Fatalf("volume create %s: %v\n%s", name, err, out)
		}
		t.Cleanup(func() { _ = exec.Command(rt, "volume", "rm", name).Run() })
	}
	exists := func(name string) bool {
		return exec.Command(rt, "volume", "exists", name).Run() == nil
	}
	leftover := "yolo-scratchreap-1a2b3c4d.scratch.00000000deadbeef.var-lib-containers"
	decoy := "yolo-scratchreap-1a2b3c4d.scratch.notalaunchid.tmp"
	mkvol(leftover)
	mkvol(decoy)
	// Past prune.ScratchVolumeGrace (1 min).
	time.Sleep(65 * time.Second)
	young := "yolo-scratchreap-1a2b3c4d.scratch.00000000cafef00d.tmp"
	mkvol(young)

	dir := writeProject(t, `{}`)
	// Long enough for the slot to run on the proxy goroutine before the jail exits.
	res := runYolo(t, dir, "sleep 8")
	if res.rc != 0 {
		t.Fatalf("rc=%d\n%s", res.rc, res.combined())
	}
	deadline := time.Now().Add(time.Minute)
	for exists(leftover) {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(dir, ".yolo", "housekeeping.log"))
			t.Fatalf("the leftover %s was never reaped\nhousekeeping.log:\n%s", leftover, log)
		}
		time.Sleep(time.Second)
	}
	if !exists(young) {
		t.Errorf("a dangling scratch volume younger than the floor was reaped: %s", young)
	}
	if !exists(decoy) {
		t.Errorf("a volume that is not a scratch volume was reaped: %s", decoy)
	}
}
