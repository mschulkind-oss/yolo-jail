package entrypoint

// launcherretry_test.go pins the last line a generated launcher prints when a first-use install
// leaves nothing to run. It used to end at "⚠ <name> not available", though running the command
// again retries the install, which is the next step (docs/reference/happy-path-principle.md,
// rule 6). Each test runs the launcher a second time, as its line says to, and checks that the
// second run installs: a line naming a retry that does not happen would be a dead end that looks
// like a next step.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// The npm agent launcher: a cold install that fails says that running it again retries, and the
// next run does.
func TestAFailedNpmFirstInstallSaysRunningItAgainRetries(t *testing.T) {
	p := newNpmProbe(t, "tool")
	_, out, rc := p.runStatus(t, "tool", "tool", "FAKE_INSTALL_FAIL=1")
	if rc == 0 {
		t.Fatalf("a cold home whose install failed reported success:\n%s", out)
	}
	const want = "  ⚠ tool not available: its install failed, above. Run tool again to retry the install.\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("the launcher does not end with %q:\n%s", want, out)
	}
	p.truncateLog(t)
	log, out, rc := p.runStatus(t, "tool", "tool")
	if rc != 0 || !strings.Contains(out, "RAN") || !hasArgv(log, "install -g --prefer-online tool") {
		t.Errorf("running it again did not retry the install and run it (rc=%d):\n%s\nnpm calls:\n%s",
			rc, out, strings.Join(log, "\n"))
	}
}

// The native agent launcher: an installer download that fails says the same, and the next run
// downloads the installer again.
func TestAFailedNativeFirstInstallSaysRunningItAgainRetries(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("#!/bin/bash\nset -eu\nmkdir -p \"$HOME/.local/bin\"\n" +
			"printf '#!/bin/bash\\necho PROBETOOL_RAN\\n' > \"$HOME/.local/bin/probetool\"\n" +
			"chmod +x \"$HOME/.local/bin/probetool\"\n"))
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	body := nativeAgentLauncher("probe",
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: srv.URL + "/install.sh"},
		filepath.Join(home, "stamps"), filepath.Join(home, "ws", ".yolo", "receipts.jsonl"),
		"", true, launcherServers{}, nil)
	script := filepath.Join(home, "probetool")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() (string, int) {
		cmd := exec.Command(script)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		} else if err != nil {
			t.Fatalf("running launcher: %v\n%s", err, out)
		}
		return string(out), 0
	}

	out, rc := run()
	if rc == 0 {
		t.Fatalf("a failed installer download reported success:\n%s", out)
	}
	const want = "  ⚠ probetool not available: its install failed, above. Run probetool again to retry the install.\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("the launcher does not end with %q:\n%s", want, out)
	}
	out, rc = run()
	if rc != 0 || !strings.Contains(out, "PROBETOOL_RAN") || requests.Load() != 2 {
		t.Errorf("running it again did not retry the install and run it (rc=%d, %d installer "+
			"request(s)):\n%s", rc, requests.Load(), out)
	}
}

// The pnpm launcher THROTTLES a failed install (RETRY_INTERVAL), so running it again within the
// interval does not retry, and its line may not say it does. It says when a run retries, and
// gives the command that retries now; following that command installs pnpm and runs it.
func TestAFailedPackageManagerInstallSaysHowToRetry(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	logPath := filepath.Join(home, "npm.log")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	npm := `#!/bin/bash
printf '%s\n' "$*" >> "` + logPath + `"
if [ "${1:-}" = install ]; then
    if [ -n "${FAKE_INSTALL_FAIL:-}" ]; then echo "npm ERR! network unreachable" >&2; exit 1; fi
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/sh\necho PNPM_RAN\n' > "$NPM_CONFIG_PREFIX/bin/pnpm"
    chmod +x "$NPM_CONFIG_PREFIX/bin/pnpm"
fi
`
	if err := os.WriteFile(filepath.Join(fakeBin, "npm"), []byte(npm), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": filepath.Join(home, "ws")})
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	pathEnv := "PATH=" + e.LaunchDir() + ":" + fakeBin + ":" + os.Getenv("PATH")
	run := func(argv []string, env ...string) (string, int) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = append([]string{"HOME=" + home, pathEnv}, env...)
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		} else if err != nil {
			t.Fatalf("running %v: %v\n%s", argv, err, out)
		}
		return string(out), 0
	}
	launcher := []string{filepath.Join(e.LaunchDir(), "pnpm")}
	stamp := filepath.Join(home, ".cache", "yolo-package-manager-stamps", "pnpm.stamp")
	// The launcher spells the path with bash's printf %q, so a path with a space pastes as one
	// word; build the expectation the same way rather than assuming the path needs no quoting.
	quoted, err := exec.Command("bash", "-c", `printf '%q' "$1"`, "_", stamp).Output()
	if err != nil {
		t.Fatal(err)
	}
	retryNow := "    To retry the install now: rm -f " + string(quoted) + " && pnpm\n"

	out, rc := run(launcher, "FAKE_INSTALL_FAIL=1")
	if rc == 0 {
		t.Fatalf("a failed install reported success:\n%s", out)
	}
	if want := "  ⚠ pnpm not available: its install failed, above. A run of pnpm 60 minutes " +
		"from now retries it.\n" + retryNow; !strings.HasSuffix(out, want) {
		t.Errorf("the first run does not end with %q:\n%s", want, out)
	}

	// Within the interval the launcher does not try again, and says so rather than claiming it did.
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, rc = run(launcher, "FAKE_INSTALL_FAIL=1")
	if rc == 0 {
		t.Fatalf("a throttled run with nothing installed reported success:\n%s", out)
	}
	if want := "  ⚠ pnpm not available: its install failed less than 60 minutes ago, so this run " +
		"did not try again.\n" + retryNow; !strings.HasSuffix(out, want) {
		t.Errorf("the throttled run does not end with %q:\n%s", want, out)
	}
	if log, _ := os.ReadFile(logPath); len(log) != 0 {
		t.Fatalf("the throttled run called npm, so the throttle this test relies on is gone:\n%s", log)
	}

	// The printed command, run as printed, retries now.
	cmdline := strings.TrimSuffix(strings.TrimPrefix(retryNow, "    To retry the install now: "), "\n")
	out, rc = run([]string{"bash", "-c", cmdline})
	if rc != 0 || !strings.Contains(out, "PNPM_RAN") {
		t.Errorf("`%s` did not install pnpm and run it (rc=%d):\n%s", cmdline, rc, out)
	}
}
