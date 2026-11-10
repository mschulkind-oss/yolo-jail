package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A host daemon a launch starts runs from the launch's pinned yolo bundle and says so on its
// start line (docs/design/pinned-host-helpers.md): exe= inside the bundle the harness launched
// with, build= the jail's YOLO_VERSION, and a daemon the keeper restarts logs a second start
// line with the same build. The keeper itself logs one too. The logs are read under the run's
// own state dir (paths.GlobalStorage), never the outer jail's live home.
func TestAHostDaemonRunsAndLogsThePinnedBuild(t *testing.T) {
	requireJail(t)
	// git-broker is a real `yolo internal daemon`, the only kind that writes a start line; its
	// fixture needs no fetch, so no scope is recorded and no repository is touched.
	fx := newGitBrokerFixture(t, true)
	const service = "git-broker"
	dir := fx.dir
	cname := jailName(dir)

	const release = "release-pin"
	first := startYoloBackground(t, "first", dir,
		`echo VERSION-IS-$YOLO_VERSION; for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.2; done`,
		"YOLO_NO_AUTO_IMAGE_REAP=1")
	t.Cleanup(func() { writeRelease(t, dir, release) })
	awaitOutput(t, first, regexp.MustCompile(`VERSION-IS-(\d\S*)`))
	jailBuild := ""
	if sub := regexp.MustCompile(`VERSION-IS-(\d\S*)`).FindStringSubmatch(first.combined()); sub != nil {
		jailBuild = sub[1]
	}
	if jailBuild == "" {
		t.Fatalf("the jail reported no YOLO_VERSION:\n%s", first.combined())
	}
	awaitLaunchLockReleased(t, dir, first)

	logs := filepath.Join(paths.GlobalStorage(), "logs")
	startLines := func(file, name string) []string {
		raw, _ := os.ReadFile(filepath.Join(logs, file))
		var out []string
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.Contains(l, "helper start: name="+name+" ") {
				out = append(out, l)
			}
		}
		return out
	}
	field := func(line, key string) string {
		sub := regexp.MustCompile(` ` + key + `=("(?:[^"\\]|\\.)*"|\S+)`).FindStringSubmatch(line)
		if sub == nil {
			return ""
		}
		if v, err := strconv.Unquote(sub[1]); err == nil {
			return v
		}
		return sub[1]
	}

	bundle := filepath.Dir(filepath.Dir(yoloBin))
	hostLog := "host-service-" + service + ".log"
	lines := startLines(hostLog, service)
	if len(lines) != 1 {
		raw, _ := os.ReadFile(filepath.Join(logs, hostLog))
		t.Fatalf("want one helper start line for %s in %s, got %d:\n%s", service, hostLog, len(lines), raw)
	}
	if exe := field(lines[0], "exe"); !strings.HasPrefix(exe, bundle+string(filepath.Separator)) {
		t.Errorf("the daemon's exe= is %q, want a path inside the launch's bundle %s:\n%s", exe, bundle, lines[0])
	}
	if build := field(lines[0], "build"); build != jailBuild {
		t.Errorf("the daemon's build= is %q, want the jail's YOLO_VERSION %q:\n%s", build, jailBuild, lines[0])
	}

	keeperLog := "jail-keeper-" + cname + ".log"
	if len(startLines(keeperLog, "jail-keeper")) == 0 {
		raw, _ := os.ReadFile(filepath.Join(logs, keeperLog))
		t.Errorf("the keeper log has no `helper start: name=jail-keeper` line:\n%s", raw)
	}

	keeper := keeperPID(t, dir)
	before := childrenMatching(keeper, "internal", service)
	if len(before) == 0 {
		t.Fatalf("no %s daemon is running under the keeper; the launch:\n%s", service, first.combined())
	}
	for _, pid := range before {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	// Wait on the second start line itself; the timeout only guards a hang.
	deadline := time.Now().Add(jailTimeout())
	for {
		if lines = startLines(hostLog, service); len(lines) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			raw, _ := os.ReadFile(filepath.Join(logs, hostLog))
			t.Fatalf("the killed daemon never logged a second start line; %s:\n%s", hostLog, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if build := field(lines[1], "build"); build != jailBuild {
		t.Errorf("the restarted daemon's build= is %q, want %q:\n%s", build, jailBuild, lines[1])
	}
	if exe := field(lines[1], "exe"); exe != field(lines[0], "exe") {
		t.Errorf("the restarted daemon runs %q, the first ran %q", exe, field(lines[0], "exe"))
	}

	writeRelease(t, dir, release)
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the session ended rc %d:\n%s", rc, first.combined())
	}
}
