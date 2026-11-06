package integration

// livectxmount_test.go is the live context-mount injector against a real podman jail
// (docs/design/live-config-apply.md §5, M1). It is THE instrument for the rootless branch: a
// nested jail runs podman with --userns host and reports rootful, so there it proves only the
// direct branch (AGENTS.md's carve-out). CI's rootless jobs are where `podman unshare` is shown.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ctxinject"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// liveCtxPodmanRootless is `podman info --format {{.Host.Security.Rootless}}`, logged with the
// result as §5.4 asks.
func liveCtxPodmanRootless(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("podman", "info", "--format", "{{.Host.Security.Rootless}}").Output()
	if err != nil {
		t.Fatalf("podman info: %v", err)
	}
	v := strings.TrimSpace(string(out))
	t.Logf("podman info --format '{{.Host.Security.Rootless}}' = %s", v)
	return v == "true"
}

// liveCtxExec runs script in the jail and returns its combined output and whether it exited 0.
func liveCtxExec(t *testing.T, cname, script string) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := jailExec(ctx, "podman", cname, script).CombinedOutput()
	return string(out), err == nil
}

// liveCtxRemountProbeScript runs the probe against both disposable binds and preserves enough
// information to distinguish a syscall result from a helper that could not execute.
func liveCtxRemountProbeScript(probePath, launchTarget, injectedTarget string) string {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	return fmt.Sprintf(`for pair in launch:%s injected:%s; do
  name=${pair%%:*}; target=${pair#*:}
  probe_output=$(%s "$target" 2>&1)
  probe_rc=$?
  case "$probe_output" in
    remount-succeeded)
      if [ "$probe_rc" -eq 0 ]; then echo "$name-REMOUNT=remount-succeeded"; else echo "$name-REMOUNT-HELPER-ERROR=exit-$probe_rc"; fi ;;
    remount-failed:*)
      if [ "$probe_rc" -eq 1 ]; then printf '%%s-REMOUNT=%%s\n' "$name" "$probe_output"; else echo "$name-REMOUNT-HELPER-ERROR=exit-$probe_rc"; fi ;;
    *) printf '%%s-REMOUNT-HELPER-ERROR=exit-%%s:%%s\n' "$name" "$probe_rc" "$probe_output" ;;
  esac
  if printf attack >> "$target/sentinel" 2>/dev/null; then echo "$name-AFTER-REMOUNT-WRITE=OK"; else echo "$name-AFTER-REMOUNT-WRITE=DENIED"; fi
done`, quote(launchTarget), quote(injectedTarget), quote(probePath))
}

// validateLiveCtxRemountResults requires a syscall result attributable to each disposable bind.
func validateLiveCtxRemountResults(output string) error {
	results := make(map[string]bool, 2)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.Contains(line, "-REMOUNT-HELPER-ERROR") {
			return fmt.Errorf("remount helper failed: %s", line)
		}
		name, result, ok := strings.Cut(line, "-REMOUNT=")
		if !ok {
			if strings.Contains(line, "-REMOUNT") && !strings.Contains(line, "-AFTER-REMOUNT") {
				return fmt.Errorf("malformed remount result %q", line)
			}
			continue
		}
		if name != "launch" && name != "injected" {
			return fmt.Errorf("remount result has unknown target %q", name)
		}
		if _, exists := results[name]; exists {
			return fmt.Errorf("duplicate remount result for %s", name)
		}
		if result != "remount-succeeded" && !(strings.HasPrefix(result, "remount-failed:") && strings.TrimSpace(strings.TrimPrefix(result, "remount-failed:")) != "") {
			return fmt.Errorf("malformed remount result for %s: %q", name, result)
		}
		results[name] = true
	}
	for _, target := range []string{"launch", "injected"} {
		if !results[target] {
			return fmt.Errorf("missing remount result for %s bind", target)
		}
	}
	return nil
}

func TestLiveCtxRemountProbeRejectsMissingHelper(t *testing.T) {
	out, err := exec.Command("sh", "-c", liveCtxRemountProbeScript("/proc/self/yolo-no-such-remount-probe", "/proc/self/yolo-no-such-launch-target", "/proc/self/yolo-no-such-injected-target")).CombinedOutput()
	if err != nil {
		t.Fatalf("shell should report helper failures per target: %v\n%s", err, out)
	}
	if err := validateLiveCtxRemountResults(string(out)); err == nil {
		t.Fatalf("missing helper was accepted as syscall results:\n%s", out)
	}
	if !strings.Contains(string(out), "launch-REMOUNT-HELPER-ERROR") || !strings.Contains(string(out), "injected-REMOUNT-HELPER-ERROR") {
		t.Fatalf("the nonexistent procfs helpers did not fail for both targets:\n%s", out)
	}
}

// TestLiveCtxMountInjectAndRemove: a podman launch puts /ctx on a read-only tmpfs and records the
// config mounts it bound under the token it froze into the jail; then the injector adds a
// read-only directory the jail can read but not write, `mkdir /ctx/x` stays refused, removal
// takes it away, and a read-write injection writes through to the host.
func TestLiveCtxMountInjectAndRemove(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" || detectRuntime() != "podman" {
		t.Skip("live context mounts are injected on podman on Linux only")
	}
	rootless := liveCtxPodmanRootless(t)
	_, statErr := os.Stat("/run/.containerenv")
	nested := statErr == nil
	if !rootless && !nested && os.Geteuid() != 0 {
		t.Skip("a rootful podman driven by a non-root user is the restart-only arm (design §5.3)")
	}

	atLaunch := t.TempDir()
	if err := os.WriteFile(filepath.Join(atLaunch, "launch.txt"), []byte("at launch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{"mounts": ["`+atLaunch+`:/ctx/atlaunch"]}`)
	cname := jailName(dir)

	const release = "release-livectx"
	first := startYoloBackground(t, "hold", dir,
		`echo LIVECTX-READY-$((40+2)); for _ in $(seq 1 1200); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, release), []byte("go\n"), 0o644) })
	ready := regexp.MustCompile(`LIVECTX-READY-42`)
	deadline := time.Now().Add(jailTimeout())
	for !ready.MatchString(first.combined()) {
		select {
		case err := <-first.done:
			t.Fatalf("the launch exited (%v) before its script ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the jail never ran its script within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	awaitLaunchLockReleased(t, dir, first)

	// THE LAUNCH HALF: the tmpfs, read-only to the jail, the launch-time bind still under it, and
	// the record carrying the jail's token and the mount it bound.
	if out, ok := liveCtxExec(t, cname, `stat -f -c %T /ctx; cat /ctx/atlaunch/launch.txt; mkdir /ctx/x 2>&1; true`); !ok ||
		!strings.Contains(out, "tmpfs") || !strings.Contains(out, "at launch") || !strings.Contains(out, "Read-only file system") {
		t.Fatalf("/ctx is not a read-only tmpfs holding the launch-time mount:\n%s", out)
	}
	envOut, err := exec.Command("podman", "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", cname).Output()
	if err != nil {
		t.Fatalf("podman inspect env: %v", err)
	}
	var token string
	for _, l := range strings.Split(string(envOut), "\n") {
		if v, ok := strings.CutPrefix(l, "YOLO_CTX_MOUNTS_TOKEN="); ok {
			token = v
		}
		if v, ok := strings.CutPrefix(l, "YOLO_CONTRACT_TAGS="); ok && !strings.Contains(v, "live-ctx-mounts") {
			t.Errorf("the jail's contract tags %q lack live-ctx-mounts", v)
		}
	}
	recBytes, err := os.ReadFile(filepath.Join(paths.AgentsDir(), cname, "ctx-mounts", "record.json"))
	if err != nil {
		t.Fatalf("the launch wrote no live-mounts record: %v", err)
	}
	var rec struct {
		Token   string `json:"token"`
		Entries []struct {
			Source, Dest string
			RW           bool
		} `json:"entries"`
	}
	if err := json.Unmarshal(recBytes, &rec); err != nil {
		t.Fatalf("record: %v\n%s", err, recBytes)
	}
	if token == "" || rec.Token != token || len(rec.Entries) != 1 || rec.Entries[0].Dest != "/ctx/atlaunch" {
		t.Errorf("record %s does not answer to the jail's token %q with its one mount", recBytes, token)
	}

	pidOut, err := exec.Command("podman", "inspect", "--format", "{{.State.Pid}}", cname).Output()
	if err != nil {
		t.Fatalf("podman inspect pid: %v", err)
	}
	pid := strings.TrimSpace(string(pidOut))
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		t.Fatalf("podman reports pid %q for %s", pid, cname)
	}
	identity, err := ctxinject.ReadProcessIdentity(n)
	if err != nil {
		t.Fatalf("read jail process identity for pid %d: %v", n, err)
	}
	identityArgs := []string{"--start-time", strconv.FormatUint(identity.StartTime, 10),
		"--mount-ns-device", strconv.FormatUint(identity.MountNSDevice, 10),
		"--mount-ns-inode", strconv.FormatUint(identity.MountNSInode, 10)}
	// The branch the host driver takes (ctxinjectdrive.go): `podman unshare` on rootless, the verb
	// directly when nested (--userns host) or rootful as root.
	ctxAttach := func(args ...string) (string, error) {
		argv := append([]string{yoloBin, "internal", "ctx-attach", "--pid", pid}, identityArgs...)
		argv = append(argv, args...)
		if rootless && !nested {
			argv = append([]string{"podman", "unshare", "--"}, argv...)
		}
		t.Logf("injector: %s", strings.Join(argv, " "))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}

	// READ-ONLY INJECTION.
	ro := t.TempDir()
	if err := os.WriteFile(filepath.Join(ro, "file.txt"), []byte("live bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := ctxAttach("--source", ro, "--dest", "/ctx/live"); err != nil {
		t.Fatalf("read-only injection failed (%v): %s", err, out)
	}
	out, _ := liveCtxExec(t, cname, `cat /ctx/live/file.txt; echo w > /ctx/live/file.txt 2>&1; echo WRITE-RC=$?; `+
		`mkdir /ctx/x 2>&1; echo MKDIR-RC=$?`)
	if !strings.Contains(out, "live bytes") {
		t.Errorf("the jail cannot read the injected file:\n%s", out)
	}
	if !strings.Contains(out, "Read-only file system") || strings.Contains(out, "WRITE-RC=0") {
		t.Errorf("a write into the read-only injection was not refused with EROFS:\n%s", out)
	}
	if strings.Contains(out, "MKDIR-RC=0") {
		t.Errorf("mkdir /ctx/x succeeded after the injection; the private clone exposed /ctx:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(ro, "file.txt")); string(b) != "live bytes\n" {
		t.Errorf("the host file changed through a read-only injection: %q", b)
	}

	// AN ADD IS IDEMPOTENT: the same source in the same mode again is success (a step cut off
	// after its mount landed is recorded by the next apply); another source there is refused.
	if out, err := ctxAttach("--source", ro, "--dest", "/ctx/live"); err != nil {
		t.Errorf("the same injection again failed (%v): %s", err, out)
	}
	if out, err := ctxAttach("--source", t.TempDir(), "--dest", "/ctx/live"); err == nil ||
		!strings.Contains(out, "a mount already") {
		t.Errorf("another source over /ctx/live was not refused (%v): %s", err, out)
	}
	if out, err := ctxAttach("--source", ro, "--dest", "/ctx/live", "--rw"); err == nil {
		t.Errorf("the same source read-write over its read-only mount was not refused: %s", out)
	}

	// REMOVAL.
	if out, err := ctxAttach("--dest", "/ctx/live", "--remove"); err != nil {
		t.Fatalf("removal failed (%v): %s", err, out)
	}
	if out, _ := liveCtxExec(t, cname, `test -e /ctx/live && echo STILL-THERE || echo GONE`); !strings.Contains(out, "GONE") {
		t.Errorf("/ctx/live is still in the jail after removal:\n%s", out)
	}

	// READ-WRITE INJECTION.
	rw := t.TempDir()
	if out, err := ctxAttach("--source", rw, "--dest", "/ctx/liverw", "--rw"); err != nil {
		t.Fatalf("read-write injection failed (%v): %s", err, out)
	}
	if out, ok := liveCtxExec(t, cname, `echo from-the-jail > /ctx/liverw/new.txt`); !ok {
		t.Errorf("a write into the read-write injection failed:\n%s", out)
	}
	if b, err := os.ReadFile(filepath.Join(rw, "new.txt")); err != nil || string(b) != "from-the-jail\n" {
		t.Errorf("the jail's write did not reach the host file: %q, %v", b, err)
	}
	if out, err := ctxAttach("--dest", "/ctx/liverw", "--remove"); err != nil {
		t.Errorf("removing the read-write injection failed (%v): %s", err, out)
	}
}

func TestLiveCtxMountReadOnlyRemountParity(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" || detectRuntime() != "podman" {
		t.Skip("CAP_SYS_ADMIN remount parity is measured on podman on Linux only")
	}
	rootless := liveCtxPodmanRootless(t)
	t.Logf("architecture: GOARCH=%s", runtime.GOARCH)
	_, statErr := os.Stat("/run/.containerenv")
	nested := statErr == nil
	if !rootless && !nested && os.Geteuid() != 0 {
		t.Skip("a rootful podman driven by a non-root user is the restart-only arm (design §5.3)")
	}

	// Both source trees and every byte a remount can change are disposable test data.
	launchSource := resolvedTempDir(t)
	injectedSource := resolvedTempDir(t)
	for _, dir := range []string{launchSource, injectedSource} {
		if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("sentinel\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	probeDir := t.TempDir()
	probeSource := filepath.Join(probeDir, "remount.go")
	probeBinary := filepath.Join(probeDir, "remount-probe")
	const remountProbeSource = `package main
import (
	"fmt"
	"os"
	"syscall"
)
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: remount-probe <mountpoint>")
		os.Exit(2)
	}
	if err := syscall.Mount("", os.Args[1], "", syscall.MS_REMOUNT|syscall.MS_BIND, ""); err != nil {
		fmt.Printf("remount-failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("remount-succeeded")
}`
	if err := os.WriteFile(probeSource, []byte(remountProbeSource), 0o600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", probeBinary, probeSource)
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the syscall-only remount probe: %v\n%s", err, output)
	}
	probe, err := os.ReadFile(probeBinary)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{launchSource, injectedSource} {
		if err := os.WriteFile(filepath.Join(dir, "remount-probe"), probe, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	packHome(t, `{"packs": []}`)
	dir := writeProject(t, `{"mcp_servers": {}, "mounts": ["`+
		launchSource+`:/ctx/launch-ro"]}`)
	cname := jailName(dir)

	const release = "release-livectx-remount"
	first := startYoloBackground(t, "hold-remount-parity", dir,
		`echo LIVECTX-REMOUNT-READY; for _ in $(seq 1 1200); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, release), []byte("go\n"), 0o644) })
	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(first.combined(), "LIVECTX-REMOUNT-READY") {
		select {
		case err := <-first.done:
			t.Fatalf("the launch exited (%v) before its script ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the jail never ran its script within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	awaitLaunchLockReleased(t, dir, first)

	if out, ok := liveCtxExec(t, cname, `cat /ctx/launch-ro/sentinel; printf attack >> /ctx/launch-ro/sentinel 2>/dev/null; echo LAUNCH-INITIAL-WRITE-RC=$?`); !ok ||
		!strings.Contains(out, "sentinel") || !strings.Contains(out, "LAUNCH-INITIAL-WRITE-RC=1") {
		t.Fatalf("launch-time read-only bind was not observed refusing an ordinary write:\n%s", out)
	}

	envOut, err := exec.Command("podman", "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", cname).Output()
	if err != nil {
		t.Fatalf("podman inspect env: %v", err)
	}
	var token string
	for _, line := range strings.Split(string(envOut), "\n") {
		if value, ok := strings.CutPrefix(line, "YOLO_CTX_MOUNTS_TOKEN="); ok {
			token = value
		}
	}
	pidOut, err := exec.Command("podman", "inspect", "--format", "{{.State.Pid}}", cname).Output()
	if err != nil {
		t.Fatalf("podman inspect pid: %v", err)
	}
	pid := strings.TrimSpace(string(pidOut))
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		t.Fatalf("podman reports pid %q for %s", pid, cname)
	}
	identity, err := ctxinject.ReadProcessIdentity(n)
	if err != nil {
		t.Fatalf("read jail process identity for pid %d: %v", n, err)
	}
	identityArgs := []string{"--start-time", strconv.FormatUint(identity.StartTime, 10),
		"--mount-ns-device", strconv.FormatUint(identity.MountNSDevice, 10),
		"--mount-ns-inode", strconv.FormatUint(identity.MountNSInode, 10)}
	ctxAttach := func(args ...string) (string, error) {
		argv := append([]string{yoloBin, "internal", "ctx-attach", "--pid", pid}, identityArgs...)
		argv = append(argv, args...)
		if rootless && !nested {
			argv = append([]string{"podman", "unshare", "--"}, argv...)
		}
		t.Logf("injector: %s", strings.Join(argv, " "))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}
	if token == "" {
		t.Fatal("the jail has no live context-mount token")
	}
	if out, err := ctxAttach("--source", injectedSource, "--dest", "/ctx/injected-ro"); err != nil {
		t.Fatalf("read-only injection failed (%v): %s", err, out)
	}
	if out, ok := liveCtxExec(t, cname, `cat /ctx/injected-ro/sentinel; printf attack >> /ctx/injected-ro/sentinel 2>/dev/null; echo INJECTED-INITIAL-WRITE-RC=$?`); !ok ||
		!strings.Contains(out, "sentinel") || !strings.Contains(out, "INJECTED-INITIAL-WRITE-RC=1") {
		t.Fatalf("injected read-only bind was not observed refusing an ordinary write:\n%s", out)
	}

	capOut, ok := liveCtxExec(t, cname, `awk '$1 == "CapEff:" {print $2}' /proc/self/status`)
	if !ok {
		t.Fatalf("could not read the jail's effective capability mask:\n%s", capOut)
	}
	capHex := strings.TrimSpace(capOut)
	capEff, err := strconv.ParseUint(capHex, 16, 64)
	if err != nil {
		t.Fatalf("parse jail CapEff %q: %v", capHex, err)
	}
	const capSysAdmin = uint64(1 << 21)
	if capEff&capSysAdmin == 0 {
		t.Skipf("remount attempt not measured: jail CapEff=0x%s does not include CAP_SYS_ADMIN; initial writes were refused for launch-time and injected mounts, but that does not prove remount resistance", capHex)
	}

	// Try the same kernel remount syscall and follow-up write against each bind. The helper
	// is a static test binary staged only in the two disposable source directories; sentinels
	// are the witness for any jail write.
	out, ok := liveCtxExec(t, cname, liveCtxRemountProbeScript("/ctx/launch-ro/remount-probe", "/ctx/launch-ro", "/ctx/injected-ro"))
	if !ok {
		t.Fatalf("jail remount probe did not complete:\n%s", out)
	}
	if err := validateLiveCtxRemountResults(out); err != nil {
		t.Fatalf("jail remount probe did not return attributable syscall results: %v\n%s", err, out)
	}
	t.Logf("CAP_SYS_ADMIN was effective in the jail; remount/write witness:\n%s", out)
	launchBytes, launchErr := os.ReadFile(filepath.Join(launchSource, "sentinel"))
	injectedBytes, injectedErr := os.ReadFile(filepath.Join(injectedSource, "sentinel"))
	if launchErr != nil || injectedErr != nil {
		t.Fatalf("read disposable sentinels: launch=%v injected=%v", launchErr, injectedErr)
	}
	launchWritable := strings.Contains(string(launchBytes), "attack")
	injectedWritable := strings.Contains(string(injectedBytes), "attack")
	t.Logf("sentinel witness: launch-time writable=%t (%q); injected writable=%t (%q)",
		launchWritable, string(launchBytes), injectedWritable, string(injectedBytes))
	if injectedWritable && !launchWritable {
		t.Fatalf("injected read-only bind was weaker than launch-time bind: remount/write reached only the injected disposable source; jail output:\n%s", out)
	}
	if launchWritable && injectedWritable {
		t.Log("BASELINE TRUST ISSUE: CAP_SYS_ADMIN remounted both read-only binds writable; parity holds, but read-only binds are not resistant to this jail capability")
	}
}
