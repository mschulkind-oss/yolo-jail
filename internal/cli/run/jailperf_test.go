package run

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// jailPerfBlock is one block as the entrypoint's dump spells it (boot.go perfLog.dump).
func jailPerfBlock(stamp string, marks ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== YOLO Jail Entrypoint Perf (%s) ===\n", stamp)
	for i, m := range marks {
		delta := "       "
		if i > 0 {
			delta = "+0.010s"
		}
		fmt.Fprintf(&b, "  %s  %9s  %s\n", strings.Split(m, " ")[0], delta, strings.Split(m, " ")[1])
	}
	b.WriteString("  Total: 0.500s\n\n")
	return b.String()
}

// launchWithJailPerf drives a fresh podman launch through Run with --timing, whose keeper writes
// boot (when non-empty) to the jail half before it relays the ready frame — pid 1's dump — and
// whose first session appends session to it. ready says whether the frame is sent. It returns
// the host perf log and the launch's stderr.
func launchWithJailPerf(t *testing.T, ws, stale, boot, session string, ready bool) (string, string) {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	writeUserPacks(t, home, `[]`)
	half := filepath.Join(runtimeHome(ws, "podman"), "yolo-perf.log")
	// THE FIRST SESSION is the runtime client's exec: its pass and provisioning append to the half.
	bin := t.TempDir()
	sessionFile := filepath.Join(bin, "session")
	if err := os.WriteFile(sessionFile, []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ \"$1\" = exec ] && cat '" + sessionFile + "' >> '" + half + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(yoloruntime.FromWorkspace(ws), false)) })
	if err := os.MkdirAll(filepath.Dir(half), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(half, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	appendHalf := func(s string) {
		f, err := os.OpenFile(half, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = f.WriteString(s)
		_ = f.Close()
	}
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	defaultKeeperSpawner = func(_ *Options, planPath string, progress, lifeline, _ *os.File, _ []*os.File) (func() int, error) {
		removeKeeperPlan(planPath)
		prog, err := dupCloseOnExec(progress)
		if err != nil {
			return nil, err
		}
		life, err := dupCloseOnExec(lifeline)
		if err != nil {
			_ = prog.Close()
			return nil, err
		}
		appendHalf(boot)
		_ = writeFrame(prog, frameStarted, []byte("4242"))
		if ready {
			_ = writeFrame(prog, frameReady, nil)
		}
		_ = prog.Close()
		return func() int {
			if !ready {
				_ = life.Close()
				return 1
			}
			_, _ = io.Copy(io.Discard, life)
			_ = life.Close()
			return 0
		}, nil
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.Timing = true
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.RestoreTerminal = func() {}
	done := make(chan struct{})
	go func() { defer close(done); Run(*o) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("the launch did not end\nstderr:\n%s", stderr.String())
	}
	body, _ := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	return string(body), stderr.String()
}

// TestAFreshLaunchRecordsTheContainerBootsSteps: the boot block pid 1 wrote to the jail half
// before its ready frame lands in the host perf log and the --timing table as container.boot.<step>
// (the "start" zero line excluded), and the provisioning stage's line under the first session's
// block as container.provision; the previous launch's block, already in the file, is not recorded.
// Red if Run stops reading the jail half at ready or at the session's end.
func TestAFreshLaunchRecordsTheContainerBootsSteps(t *testing.T) {
	ws := t.TempDir()
	stale := jailPerfBlock("2026-01-01 00:00:00", "0.000s start", "9.000s stale_step")
	boot := jailPerfBlock("2026-01-02 00:00:00", "0.000s start", "0.100s generate_shims", "0.350s configure_pack_surfaces")
	session := jailPerfBlock("2026-01-02 00:00:01", "0.000s start", "0.050s generate_shims") +
		"  provisioning stage: 1634ms (exit 0)\n"
	hostLog, printed := launchWithJailPerf(t, ws, stale, boot, session, true)
	for _, want := range []string{"container.boot.generate_shims", "container.boot.configure_pack_surfaces", "container.provision"} {
		if !strings.Contains(hostLog, "end    "+want) {
			t.Errorf("host perf log lacks %s:\n%s", want, hostLog)
		}
		if !strings.Contains(printed, want) {
			t.Errorf("--timing table lacks %s:\n%s", want, printed)
		}
	}
	for _, unwanted := range []string{"stale_step", "container.boot.start"} {
		if strings.Contains(hostLog, unwanted) {
			t.Errorf("host perf log records %s:\n%s", unwanted, hostLog)
		}
	}
	if n := strings.Count(hostLog, "end    container.boot.generate_shims"); n != 1 {
		t.Errorf("generate_shims recorded %d times, want once (the boot's, not the session pass's):\n%s", n, hostLog)
	}
	for _, want := range []string{"0.250s  container.boot.configure_pack_surfaces", "1.634s  container.provision"} {
		if !strings.Contains(hostLog, want) && !strings.Contains(printed, want) {
			t.Errorf("no %q in the record:\n%s\n%s", want, hostLog, printed)
		}
	}
}

// TestABootThatWroteNothingRecordsNoContainerSteps: a jail half holding only the previous
// launch's block records nothing, ready or refused.
func TestABootThatWroteNothingRecordsNoContainerSteps(t *testing.T) {
	stale := jailPerfBlock("2026-01-01 00:00:00", "0.000s start", "0.100s generate_shims")
	for _, ready := range []bool{true, false} {
		hostLog, _ := launchWithJailPerf(t, t.TempDir(), stale, "", "", ready)
		if strings.Contains(hostLog, "container.boot.") {
			t.Errorf("ready=%v: the previous launch's block was recorded:\n%s", ready, hostLog)
		}
	}
}

// TestARefusedBootStillRecordsItsSteps: a keeper that ends before ready (a refused boot) still
// has its block recorded before the report.
func TestARefusedBootStillRecordsItsSteps(t *testing.T) {
	boot := jailPerfBlock("2026-01-02 00:00:00", "0.000s start", "0.200s probe_service_reachability")
	hostLog, _ := launchWithJailPerf(t, t.TempDir(), "", boot, "", false)
	if !strings.Contains(hostLog, "end    container.boot.probe_service_reachability") {
		t.Errorf("a refused boot's steps were not recorded:\n%s", hostLog)
	}
}

// TestTheJailHalfIsReadAsUntrusted: a link escaping the workspace's .yolo is never read, and a
// hostile block's names are sanitized and its count bounded.
func TestTheJailHalfIsReadAsUntrusted(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte(jailPerfBlock("x", "0.000s start", "0.100s leaked")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".yolo", "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, ".yolo", "home", "yolo-perf.log")); err != nil {
		t.Fatal(err)
	}
	var events []perf.Event
	log := perf.New(time.Now, func(e perf.Event) { events = append(events, e) })
	r := &jailPerfReader{ws: ws, rel: jailPerfRel("podman", "home")}
	r.recordBoot(log)
	if len(events) != 0 {
		t.Errorf("a linked jail half was read: %v", events)
	}

	if err := os.Remove(filepath.Join(ws, ".yolo", "home", "yolo-perf.log")); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("=== YOLO hostile ===\n")
	for i := range perf.MaxForeignSteps + 20 {
		fmt.Fprintf(&b, "  %d.000s  +1.000s  Evil/../NAME\x1b[31m %d\n", i+1, i)
	}
	if err := os.WriteFile(filepath.Join(ws, ".yolo", "home", "yolo-perf.log"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	r = &jailPerfReader{ws: ws, rel: jailPerfRel("podman", "home")}
	r.recordBoot(log)
	if len(events) != perf.MaxForeignSteps {
		t.Errorf("recorded %d steps, want the cap %d", len(events), perf.MaxForeignSteps)
	}
	for _, e := range events {
		if strings.TrimLeft(strings.TrimPrefix(e.Name, "container.boot."), "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
			t.Errorf("unsanitized span name %q", e.Name)
		}
	}
}

// TestTheJailHalfIsWhereEachBackendBindsIt: podman binds the single file, Apple Container the home.
func TestTheJailHalfIsWhereEachBackendBindsIt(t *testing.T) {
	if got := jailPerfRel("podman", "home"); got != filepath.Join("home", "yolo-perf.log") {
		t.Errorf("podman: %s", got)
	}
	if got := jailPerfRel("container", "home"); got != filepath.Join("home", ".yolo-perf.log") {
		t.Errorf("Apple Container: %s", got)
	}
}
