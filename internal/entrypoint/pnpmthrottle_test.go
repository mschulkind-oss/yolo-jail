package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pnpmthrottle_test.go pins what the pnpm launcher's retry marker (pnpm.failed.<id>, one per jail
// start; docs/design/launcher-update-per-jail-start.md LU-D7) records: a failed install, and
// nothing else. It touched its stamp after a successful install too, so a pnpm removed after its
// install was not installed again, and the run said its install had failed, which was false. A
// failed install is retried at the next jail start, never on a clock.

// pnpmProbe is the pnpm launcher over a fake npm whose `install` puts a pnpm in the prefix, or
// fails when FAKE_INSTALL_FAIL is set.
type pnpmProbe struct {
	t         *testing.T
	home      string
	installed string // the installed pnpm's path
	logPath   string // the npm call log
	launcher  string
	pathEnv   string
	starts    int
	id        string // the jail-start record's id; "" once removed
}

func newPnpmProbe(t *testing.T) *pnpmProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	logPath := filepath.Join(home, "npm.log")
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
	p := &pnpmProbe{t: t, home: home, logPath: logPath, launcher: filepath.Join(e.LaunchDir(), "pnpm"),
		installed: filepath.Join(home, ".npm-global", "bin", "pnpm"),
		pathEnv:   "PATH=" + e.LaunchDir() + ":" + fakeBin + ":" + os.Getenv("PATH")}
	p.newStart()
	return p
}

// newStart writes a fresh jail-start record, as the boot does.
func (p *pnpmProbe) newStart() {
	p.t.Helper()
	p.starts++
	p.id = "start-" + strconv.Itoa(p.starts)
	path := filepath.Join(p.home, filepath.FromSlash(JailStartRel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(p.id+"\n"), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// marker is where a failed install of THIS start is recorded. The name is spelled here, so the
// production name is pinned.
func (p *pnpmProbe) marker() string {
	return filepath.Join(p.home, ".cache", "yolo-package-manager-stamps", "pnpm.failed."+p.id)
}

func (p *pnpmProbe) run(env ...string) (string, int) {
	p.t.Helper()
	cmd := exec.Command(p.launcher)
	cmd.Env = append([]string{"HOME=" + p.home, p.pathEnv}, env...)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	} else if err != nil {
		p.t.Fatalf("running the pnpm launcher: %v\n%s", err, out)
	}
	return string(out), 0
}

func (p *pnpmProbe) npmCalls() string {
	log, _ := os.ReadFile(p.logPath)
	return string(log)
}

// Each order ends with a pnpm removed after a successful install, and the next run installs it
// again rather than calling a success a failure.
func TestAPnpmRemovedAfterASuccessfulInstallIsInstalledAgain(t *testing.T) {
	for _, order := range []struct {
		name  string
		setup func(t *testing.T, p *pnpmProbe)
	}{
		{"installed, then removed", func(*testing.T, *pnpmProbe) {}},
		// A failure first, then the NEXT START installs: that success clears the failure's marker.
		{"failed, installed at the next start, then removed", func(t *testing.T, p *pnpmProbe) {
			if out, rc := p.run("FAKE_INSTALL_FAIL=1"); rc == 0 {
				t.Fatalf("a failed install reported success:\n%s", out)
			}
			if _, err := os.Stat(p.marker()); err != nil {
				t.Fatalf("the failed install left no marker: %v", err)
			}
			p.newStart()
		}},
	} {
		t.Run(order.name, func(t *testing.T) {
			p := newPnpmProbe(t)
			stale := p.marker()
			order.setup(t, p)
			if out, rc := p.run(); rc != 0 || !strings.Contains(out, "PNPM_RAN") {
				t.Fatalf("the install did not run pnpm (rc=%d):\n%s", rc, out)
			}
			for _, m := range []string{stale, p.marker()} {
				if _, err := os.Stat(m); !os.IsNotExist(err) {
					t.Errorf("a successful install left the failure marker %s (err=%v)", m, err)
				}
			}
			if err := os.Remove(p.installed); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p.logPath, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			out, rc := p.run()
			if rc != 0 || !strings.Contains(out, "PNPM_RAN") {
				t.Errorf("a pnpm removed after a successful install was not installed again (rc=%d):\n%s",
					rc, out)
			}
			if strings.Contains(out, "failed earlier") {
				t.Errorf("the run says an install failed, and none did:\n%s", out)
			}
			if !strings.Contains(p.npmCalls(), "install -g --prefer-online pnpm@latest") {
				t.Errorf("the run did not call npm install:\n%s", p.npmCalls())
			}
		})
	}
}

// A failure, then another run of the SAME start: it does not retry and says so truly, the marker
// being the failure's. The next start retries.
func TestAPnpmInstallThatFailedInThisStartIsNotRetriedUntilTheNextStart(t *testing.T) {
	p := newPnpmProbe(t)
	if out, rc := p.run("FAKE_INSTALL_FAIL=1"); rc == 0 {
		t.Fatalf("a failed install reported success:\n%s", out)
	}
	if err := os.WriteFile(p.logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, rc := p.run()
	if rc == 0 || !strings.Contains(out, "its install failed earlier in this jail start, so this run did not try again") {
		t.Errorf("a run of the same start does not say it was throttled (rc=%d):\n%s", rc, out)
	}
	if p.npmCalls() != "" {
		t.Errorf("the throttled run called npm:\n%s", p.npmCalls())
	}
	p.newStart()
	if out, rc := p.run(); rc != 0 || !strings.Contains(out, "PNPM_RAN") {
		t.Errorf("the next jail start did not retry the install (rc=%d):\n%s", rc, out)
	}
}

// With no jail-start record (an older boot, a failed write) nothing can be recorded, so a cold
// install is simply attempted again, and the launcher never writes the record itself.
func TestAPnpmLauncherWithNoStartRecordRetriesAndWritesNoRecord(t *testing.T) {
	p := newPnpmProbe(t)
	record := filepath.Join(p.home, filepath.FromSlash(JailStartRel))
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if out, rc := p.run("FAKE_INSTALL_FAIL=1"); rc == 0 {
			t.Fatalf("a failed install reported success:\n%s", out)
		}
	}
	if n := strings.Count(p.npmCalls(), "install -g"); n != 2 {
		t.Errorf("with no record the install was attempted %d times, want 2:\n%s", n, p.npmCalls())
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("the launcher wrote the jail-start record; the boot is its only writer")
	}
}
