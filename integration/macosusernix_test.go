package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `nix` INSIDE THE macos-user SANDBOX — docs/plans/setup-support-gaps.md G21, the macos-user
// arm.
//
// WHAT IT SETTLES. The one backend that needs a host nix for EVERY launch (the floor is a
// native nix build) was the one whose sandbox could not run it: `nix: command not found`. A
// 2026-09-16 hardware probe (§5.1 rows 10-12 of that doc) showed confinement was never the
// obstacle — connect(2) to the daemon socket survives the profile's write-deny and
// `nix build nixpkgs#hello` returns 0 under it — so the fix is DELIVERY:
// internal/macosuser/hostnix.go puts the host's own nix client (the resolved store bin dir of
// the `nix` the launcher found on PATH) on the sandbox PATH, with NIX_REMOTE=daemon and a
// NIX_CONFIG enabling nix-command + flakes. The Linux half is
// internal/macosuser/hostnix_test.go; nothing there can say the sandbox really runs it.
//
// "A DAEMON CLIENT" IS ASSERTED BY A STORE OPERATION. `nix eval --expr 1+1` opens no store
// connection (measured on Linux: it prints 2 with NIX_REMOTE pointed at a socket that does
// not exist), so it proves the CLI runs and nothing about the daemon. `nix store info` does
// the daemon handshake AS THE SANDBOX ACCOUNT — the half the 2026-09-16 probe never
// attributed to `_yolojail` — so `Store URL: daemon` with RC=0 is what says that account can
// use this host's daemon (socket mode, allowed-users).
//
// "NO FLAGS" IS ASSERTED TWO WAYS, because the CI host's /etc/nix/nix.conf already enables
// the features (.github/workflows/macos-user.yml, the Install Nix step), so a green `nix eval`
// alone cannot tell the launch's NIX_CONFIG from the host's file. The env check is the half
// that is about yolo.
//
// ONE LAUNCH, FENCED, for macosusertools_test.go's reason: every launch pays for a native
// floor build.
func TestMacosUserNixIsTheHostDaemonClientWithNoFlags(t *testing.T) {
	requireMacosUser(t)

	// A SINGLE-USER host (or a client outside the store) gets no nix by design (hostnix.go:
	// the sandbox account cannot use a store it does not own), so a developer's own Mac of
	// that shape has nothing to assert. ON THE DECLARED JOB IT IS A FAILURE, not a skip: the
	// workflow's Install Nix step is a daemon install, and the vacuity guard is per GATE, so a
	// runner whose nix layout changed would otherwise go green here by skipping while every
	// sibling launch test passed.
	hostShape := t.Skipf
	if os.Getenv(macosUserDeclareEnv) != "" {
		hostShape = t.Fatalf
	}
	const sock = "/nix/var/nix/daemon-socket/socket"
	if info, err := os.Stat(sock); err != nil || info.Mode()&os.ModeSocket == 0 {
		hostShape("macos-user nix: no nix daemon socket at %s on this host (single-user nix?); "+
			"the launch delivers no nix there by design", sock)
	}
	// The client the LAUNCHER will resolve — this process's PATH is the one the yolo
	// subprocess inherits, so the same lookup gives the expected answer.
	hostNix, err := exec.LookPath("nix")
	if err != nil {
		hostShape("macos-user nix: no `nix` on this process's PATH (%v)", err)
	}
	resolved, err := filepath.EvalSymlinks(hostNix)
	if err != nil || !strings.HasPrefix(resolved, "/nix/store/") {
		hostShape("macos-user nix: host `nix` (%s) does not resolve into /nix/store (%q, %v); "+
			"the launch delivers no nix for such a client by design", hostNix, resolved, err)
	}
	wantDir := filepath.Dir(resolved)
	hostVersion, _ := exec.Command(resolved, "--version").Output()

	ws := macosUserWorkspace(t, "{}")
	probe := strings.Join([]string{
		`echo "=== WHICH ==="`,
		`command -v nix || echo "NIX-NOT-FOUND"`,
		`echo "=== VERSION ==="`,
		`nix --version 2>&1 || echo "VERSION-FAILED"`,
		`echo "=== ENV ==="`,
		`printf 'NIX_REMOTE=%s\n' "${NIX_REMOTE-UNSET}"`,
		`printf 'NIX_CONFIG=%s\n' "${NIX_CONFIG-UNSET}"`,
		`echo "=== EVAL ==="`,
		`nix eval --expr 1+1 2>&1 || echo "EVAL-FAILED"`,
		`echo "=== STORE ==="`,
		`nix store info 2>&1; echo "RC=$?"`,
		`echo "=== END ==="`,
	}, "\n")

	r := runMacosUser(t, ws, probe)
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before the probe could answer.\n"+
			"stdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	which := strings.TrimSpace(section(r.stdout, "=== WHICH ===", "=== VERSION ==="))
	version := strings.TrimSpace(section(r.stdout, "=== VERSION ===", "=== ENV ==="))
	env := section(r.stdout, "=== ENV ===", "=== EVAL ===")
	eval := strings.TrimSpace(section(r.stdout, "=== EVAL ===", "=== STORE ==="))
	store := section(r.stdout, "=== STORE ===", "=== END ===")
	if which == "" && version == "" && eval == "" {
		t.Fatalf("the sandbox produced no probe output.\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}

	t.Run("on_path", func(t *testing.T) {
		if which != filepath.Join(wantDir, "nix") {
			t.Errorf("`command -v nix` in the sandbox = %q, want the host client %s/nix. "+
				"NIX-NOT-FOUND is the G21 break this test exists for; another path means a "+
				"different nix outranks the host's.\nlaunch output:\n%s%s",
				which, wantDir, r.stdout, r.stderr)
		}
	})
	t.Run("version_matches_host", func(t *testing.T) {
		if strings.Contains(version, "VERSION-FAILED") || version == "" {
			t.Fatalf("`nix --version` failed in the sandbox:\n%s", version)
		}
		if hv := strings.TrimSpace(string(hostVersion)); hv != "" && version != hv {
			t.Errorf("sandbox `nix --version` = %q, host = %q; they are meant to be one binary", version, hv)
		}
	})
	t.Run("daemon_client_env", func(t *testing.T) {
		if !strings.Contains(env, "NIX_REMOTE=daemon") {
			t.Errorf("NIX_REMOTE is not `daemon` in the sandbox:\n%s", env)
		}
		if !strings.Contains(env, "nix-command") || !strings.Contains(env, "flakes") {
			t.Errorf("NIX_CONFIG does not enable nix-command + flakes, so a host whose nix.conf "+
				"does not would refuse the new CLI:\n%s", env)
		}
	})
	t.Run("eval_with_no_flags", func(t *testing.T) {
		if eval != "2" {
			t.Errorf("`nix eval --expr 1+1` in the sandbox = %q, want 2", eval)
		}
	})
	// The one subtest that talks to the daemon (see the header): eval never connects.
	t.Run("store_op_reaches_the_daemon", func(t *testing.T) {
		if !strings.Contains(store, "RC=0") || !strings.Contains(store, "Store URL: daemon") {
			t.Errorf("`nix store info` in the sandbox did not complete a daemon handshake as the "+
				"sandbox account; want RC=0 and `Store URL: daemon`:\n%s", store)
		}
	})
}
