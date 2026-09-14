package integration

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
)

// THE MISSING TEST. `internal/containerbuilder` was fully unit-tested and the offload
// was broken for as long as it had shipped, because every one of those tests pinned the
// string we EMIT and none of them pinned the only thing that matters about it: whether
// nix accepts that string and builds through the container it names. The one manual
// check that ever ran (the mac-ac-container-builder runbook, "PROVEN on real hardware
// 2026-07-17") exercised `nix build --store ssh-ng://…`, which is a DIFFERENT mechanism
// from the `--builders` line the code ships — so the green it produced was about
// something nobody shipped.
//
// This closes that: a real builder container, the real constructors, a real nix remote
// build, on every push, on both CI arches.
//
// ⚠ WHAT IT DOES NOT COVER, stated so the next reader does not over-trust it. The macOS
// failure had two halves and this reaches one of them:
//
//   - COVERED — the host key. `--builders` is spelled for a daemon nix that never sees
//     the caller's NIX_SSHOPTS, so the line has to carry the builder's host key in its
//     eighth field or ssh dies at `Host key verification failed.` This test runs with
//     NIX_SSHOPTS deliberately REMOVED from the environment, which is exactly the
//     condition the daemon is in, so a line without the key fails here.
//   - NOT COVERED — WHICH PROCESS forks ssh. `--store <dir>` below makes the CLIENT own
//     the store and therefore run the build, so this never exercises the nix-daemon leg.
//     That is deliberate: the daemon leg only engages for a trusted user on a multi-user
//     install, so testing it would make this test's meaning depend on how the host's nix
//     was installed — green on a laptop, skipped on CI, and no way to tell which. The
//     daemon leg is what the macOS nightly is for.
//
// That split is also how to READ a macOS nightly: this test green while the three
// `packages:` tests are red says the container, the published port and the pinned key
// all work and the remaining fault is in the daemon leg alone. This test red says the
// builder never came up at all, and says it in two minutes instead of thirty.
//
// Runs anywhere there is a container runtime and a nix: this is a Linux-side test of a
// macOS-only feature, which is the point — the offload's Linux half is identical on both
// and Linux CI runs on every push, where the macOS nightly runs once a night.
func TestTheLinuxBuilderOffloadLineNixActuallyBuildsThrough(t *testing.T) {
	requireJail(t)
	rt := detectRuntime()
	if rt == "" {
		t.Skip("no container runtime: nothing to start the builder on")
	}
	if rt != "podman" {
		// Apple Container reaches the builder on a VM IP rather than a published
		// port. Reachable in principle; unmeasured, and a test that has never run
		// green is not coverage.
		t.Skipf("%s: the offload's address discovery differs and is unverified here", rt)
	}
	for _, bin := range []string{"nix", "ssh-keygen", "ssh-keyscan"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: the offload needs it", bin)
		}
	}
	if goruntime.GOOS != "linux" && goruntime.GOOS != "darwin" {
		t.Skip("unsupported host")
	}

	// The keypair goes where the product puts it, not somewhere convenient: the
	// builders line names BuilderKey() when the caller passes no path, so a test that
	// generated its key elsewhere would authorize one key in the container and tell
	// nix to offer another. (That is not hypothetical — it is what this test did on its
	// first run, and nix answered `Permission denied (publickey)`.) requireJail has
	// already redirected HOME, so BuilderKeyDir() is inside this test's own tree.
	// resolvedTempDir, NOT t.TempDir(): this becomes `nix --store <dir>/store` below, and
	// nix walks the parents of a store path and REFUSES a symlink among them. On macOS
	// t.TempDir() is under /var/folders and /var is a symlink to /private/var, so the raw
	// path fails with `error: the path "/var" is a symlink; this is not allowed for the Nix
	// store and its parent directories` — after the ten-minute timeout below, which is what
	// made it look like a hung builder rather than a rejected path.
	dir := resolvedTempDir(t)
	key := containerbuilder.BuilderKey()
	if err := os.MkdirAll(containerbuilder.BuilderKeyDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	// Generate only when absent, exactly as image.ensureBuilderKey does — the isolated
	// HOME shares the yolo store between runs, so this key outlives one test.
	if _, err := os.Stat(key + ".pub"); err != nil {
		if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key, "-q").CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v\n%s", err, out)
		}
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("builder key: %s", key)

	// A leftover container from an interrupted run owns the frozen name and port.
	_ = exec.Command(rt, "rm", "-f", containerbuilder.BuilderContainer).Run()

	run := func(argv []string) int {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			return 1
		}
		return 0
	}
	sess := &containerbuilder.Session{
		Runtime: rt,
		Pubkey:  strings.TrimSpace(string(pub)),
		Deps: containerbuilder.Deps{
			Run: run,
			Output: func(argv []string) (string, int) {
				out, err := exec.Command(argv[0], argv[1:]...).Output()
				if err != nil {
					if ee, ok := err.(*exec.ExitError); ok {
						return string(out), ee.ExitCode()
					}
					return "", 1
				}
				return string(out), 0
			},
			Reachable: func(host string, port int) bool {
				c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), time.Second)
				if err != nil {
					return false
				}
				_ = c.Close()
				return true
			},
			Sleep: func(s float64) { time.Sleep(time.Duration(s * float64(time.Second))) },
			Now:   func() float64 { return float64(time.Now().UnixNano()) / 1e9 },
			Out:   testWriter{t},
		},
	}
	host, port, ok := sess.Start()
	if !ok {
		t.Fatal("the builder container never came up; the offload cannot be tested " +
			"and, on a real macOS launch, would have fallen back to a failed build")
	}
	t.Cleanup(sess.Stop)

	line := sess.BuildersLine(host, port, 1)
	if got := len(strings.Fields(line)); got != 8 {
		t.Fatalf("Start produced an UNPINNED builders line (%d fields, want 8): %q\n"+
			"Without the host key in field 8 a daemon nix cannot authenticate to this "+
			"container at all — that is the macOS nightly's failure.", got, line)
	}

	// --max-jobs 0 forbids a local build, so a pass cannot come from nix quietly
	// building this itself; --store makes the client own the store (see the header).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix",
		"--extra-experimental-features", "nix-command flakes",
		"build", "--no-link", "--print-out-paths",
		"--store", filepath.Join(dir, "store"),
		"--max-jobs", "0",
		"--builders", line,
		"--expr", `derivation { name = "offload-probe"; system = "`+
			containerbuilder.BuilderSystem()+
			`"; builder = "/bin/sh"; args = ["-c" "echo OFFLOAD-WORKS > $out"]; }`)
	// THE POINT OF THE TEST: no NIX_SSHOPTS. It is what the shipped code exports and
	// what a daemon nix can never receive, and ssh takes the FIRST occurrence of an
	// option — so leaving it set here would let its StrictHostKeyChecking=no answer
	// the question instead of the pinned key, and the test would pass with the fix
	// removed.
	cmd.Env = withoutVars(os.Environ(), "NIX_SSHOPTS", "NIX_REMOTE")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nix refused to build through the builder container (%v)\n"+
			"builders line: %q\n%s", err, line, out)
	}
	if !strings.Contains(string(out), "offload-probe") {
		t.Errorf("no output path from the offloaded build:\n%s", out)
	}
}

// withoutVars returns env with the named variables removed (not emptied: an empty
// NIX_SSHOPTS and an absent one differ to nix's shell-splitter only in that one of them
// is what a daemon actually has).
func withoutVars(env []string, names ...string) []string {
	out := env[:0:0]
	for _, kv := range env {
		drop := false
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				drop = true
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// testWriter routes the Session's progress lines into the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("builder: %s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
