package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pnpmthrottle_test.go pins what the pnpm launcher's retry throttle (RETRY_INTERVAL) records: a
// failed install, and nothing else. It touched its stamp after a successful install too, so a
// pnpm removed within the hour of its install was not installed again, and the run said "its
// install failed less than 60 minutes ago, so this run did not try again", which was false.

// pnpmFixture generates the pnpm launcher over a fake npm whose `install` puts a pnpm in the
// prefix, or fails when FAKE_INSTALL_FAIL is set. It returns a runner for the launcher, the
// installed pnpm's path, the throttle stamp's path, and the npm call log.
func pnpmFixture(t *testing.T) (run func(env ...string) (string, int), installed, stamp, logPath string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	logPath = filepath.Join(home, "npm.log")
	writeTestFile(t, filepath.Join(fakeBin, "npm"), `#!/bin/bash
printf '%s\n' "$*" >> "`+logPath+`"
if [ "${1:-}" = install ]; then
    if [ -n "${FAKE_INSTALL_FAIL:-}" ]; then echo "npm ERR! network unreachable" >&2; exit 1; fi
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/sh\necho PNPM_RAN\n' > "$NPM_CONFIG_PREFIX/bin/pnpm"
    chmod +x "$NPM_CONFIG_PREFIX/bin/pnpm"
fi
`)
	if err := os.Chmod(filepath.Join(fakeBin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": filepath.Join(home, "ws")})
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	pathEnv := "PATH=" + e.LaunchDir() + ":" + fakeBin + ":" + os.Getenv("PATH")
	run = func(env ...string) (string, int) {
		cmd := exec.Command(filepath.Join(e.LaunchDir(), "pnpm"))
		cmd.Env = append([]string{"HOME=" + home, pathEnv}, env...)
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		} else if err != nil {
			t.Fatalf("running the pnpm launcher: %v\n%s", err, out)
		}
		return string(out), 0
	}
	return run, filepath.Join(home, ".npm-global", "bin", "pnpm"),
		filepath.Join(home, ".cache", "yolo-package-manager-stamps", "pnpm.stamp"), logPath
}

// Each order ends with a pnpm removed after a successful install, and the next run installs it
// again rather than calling a success a failure.
func TestAPnpmRemovedAfterASuccessfulInstallIsInstalledAgain(t *testing.T) {
	for _, order := range []struct {
		name  string
		setup func(t *testing.T, run func(env ...string) (string, int), stamp string)
	}{
		{"installed, then removed", func(*testing.T, func(env ...string) (string, int), string) {}},
		// A failure first, then a run past the interval that installs: that success clears the
		// failure's stamp.
		{"failed, installed an hour later, then removed", func(t *testing.T,
			run func(env ...string) (string, int), stamp string) {
			if out, rc := run("FAKE_INSTALL_FAIL=1"); rc == 0 {
				t.Fatalf("a failed install reported success:\n%s", out)
			}
			old := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(stamp, old, old); err != nil {
				t.Fatalf("the failed install left no stamp to age: %v", err)
			}
		}},
	} {
		t.Run(order.name, func(t *testing.T) {
			run, installed, stamp, logPath := pnpmFixture(t)
			order.setup(t, run, stamp)
			if out, rc := run(); rc != 0 || !strings.Contains(out, "PNPM_RAN") {
				t.Fatalf("the install did not run pnpm (rc=%d):\n%s", rc, out)
			}
			if _, err := os.Stat(stamp); !os.IsNotExist(err) {
				t.Errorf("a successful install left the failure stamp %s (err=%v)", stamp, err)
			}
			if err := os.Remove(installed); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(logPath, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			out, rc := run()
			if rc != 0 || !strings.Contains(out, "PNPM_RAN") {
				t.Errorf("a pnpm removed after a successful install was not installed again (rc=%d):\n%s",
					rc, out)
			}
			if strings.Contains(out, "failed less than") {
				t.Errorf("the run says an install failed, and none did:\n%s", out)
			}
			if log, _ := os.ReadFile(logPath); !strings.Contains(string(log), "install -g --prefer-online pnpm@latest") {
				t.Errorf("the run did not call npm install:\n%s", log)
			}
		})
	}
}

// The other order: a failure, then a run within the hour, which does not retry and says so
// truly, the stamp being the failure's.
func TestAPnpmInstallThatFailedWithinTheHourIsNotRetried(t *testing.T) {
	run, _, stamp, logPath := pnpmFixture(t)
	if out, rc := run("FAKE_INSTALL_FAIL=1"); rc == 0 {
		t.Fatalf("a failed install reported success:\n%s", out)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatalf("a failed install left no stamp, so nothing throttles the retry: %v", err)
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, rc := run()
	if rc == 0 || !strings.Contains(out, "its install failed less than 60 minutes ago, so this run did not try again") {
		t.Errorf("a run within the hour of a failure does not say it was throttled (rc=%d):\n%s", rc, out)
	}
	if log, _ := os.ReadFile(logPath); len(log) != 0 {
		t.Errorf("the throttled run called npm:\n%s", log)
	}
}
