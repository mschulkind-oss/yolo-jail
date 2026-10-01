package entrypoint

// launchermisplaced_test.go pins the line a generated launcher prints when its install REPORTED
// SUCCESS and left nothing where the launcher runs the program from: npm exited 0, or the vendor
// installer did, and the program is not at the launcher's path. Those launchers used to print
// the failed-install line, "its install failed, above. Run <name> again to retry the install",
// which was false (nothing above failed) and sent the user round a loop, since the next run
// installs the same way. The line now says what happened and who can act on it: the pack's
// author, or yolo's issue tracker for a pack yolo ships (docs/reference/happy-path-principle.md,
// rung 4). The agent launchers' update mode, which `yolo pack update` runs, ends with the same
// line and exits non-zero: on the npm path it exited 0. Every launcher here is the one the boot's
// generator writes, so the pack name in the line is the generator's.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// misplacedFixture is a jail home whose pack root holds pack acme declaring contribution, and a
// fake npm on PATH whose `install` exits 0 and writes nothing. It returns the Env, the PATH a
// launcher runs with, and the npm call log.
func misplacedFixture(t *testing.T, contribution string) (*Env, string, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	packRoot := filepath.Join(t.TempDir(), "packs")
	writeTestFile(t, filepath.Join(packRoot, "acme", "pack.json"),
		`{"name":"acme","contributes":[`+contribution+`]}`)
	fakeBin := filepath.Join(home, "fakebin")
	logPath := filepath.Join(home, "npm.log")
	writeTestFile(t, filepath.Join(fakeBin, "npm"), "#!/bin/bash\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"+
		"echo \"added 1 package in 0s\"\nexit 0\n")
	if err := os.Chmod(filepath.Join(fakeBin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot,
		"YOLO_WORKSPACE": filepath.Join(home, "ws")})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	return e, fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"), logPath
}

// runGeneratedLauncher runs the launcher for bin from e's launch dir, with HOME and PATH set, and
// extraEnv after them.
func runGeneratedLauncher(t *testing.T, e *Env, pathEnv, bin string, extraEnv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(e.LaunchDir(), bin))
	cmd.Env = append([]string{"HOME=" + e.Home, "PATH=" + pathEnv}, extraEnv...)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the %s launcher: %v\n%s", bin, err, out)
	}
	return string(out), 0
}

// assertNoRetryLoop fails when out tells the user to run the program again, or says an install
// failed, neither of which is what happened.
func assertNoRetryLoop(t *testing.T, out, bin string) {
	t.Helper()
	for _, wrong := range []string{"its install failed", "again to retry", "retries it"} {
		if strings.Contains(out, wrong) {
			t.Errorf("the launcher says %q, though the install reported success:\n%s", wrong, out)
		}
	}
}

// The npm agent launcher: npm exits 0 and the package provides no misnpm.
func TestAnNpmInstallThatLandsNothingSaysSoAndNamesThePacksAuthor(t *testing.T) {
	e, pathEnv, _ := misplacedFixture(t,
		`{"kind":"program","bin":"misnpm","via":"npm","package":"misnpm-pkg"}`)
	out, rc := runGeneratedLauncher(t, e, pathEnv, "misnpm")
	if rc == 0 {
		t.Fatalf("a launcher with nothing to run reported success:\n%s", out)
	}
	realBin := filepath.Join(e.Home, ".npm-global", "bin", "misnpm")
	want := "  ⚠ misnpm not available: npm reported installing misnpm-pkg@latest, but there is no misnpm " +
		"at " + realBin + ", where this launcher runs it from.\n" +
		"    Pack acme's install for misnpm does not put it there: tell that pack's author, or, if yolo " +
		"ships pack acme, report it at " + IssuesURL + ".\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("the launcher does not end with\n%s\ngot:\n%s", want, out)
	}
	assertNoRetryLoop(t, out, "misnpm")
}

// The native agent launcher: the vendor installer exits 0 and puts nothing at ~/.local/bin.
func TestAnInstallerThatLandsNothingSaysSoAndNamesThePacksAuthor(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#!/bin/bash\nmkdir -p \"$HOME/elsewhere\"\n" +
			"printf '#!/bin/sh\\n' > \"$HOME/elsewhere/misnative\"\necho installed\nexit 0\n"))
	}))
	t.Cleanup(srv.Close)
	url := srv.URL + "/install.sh"
	e, pathEnv, _ := misplacedFixture(t,
		`{"kind":"program","bin":"misnative","via":"installer","url":"`+url+`"}`)
	out, rc := runGeneratedLauncher(t, e, pathEnv, "misnative")
	if rc == 0 {
		t.Fatalf("a launcher with nothing to run reported success:\n%s", out)
	}
	realBin := filepath.Join(e.Home, ".local", "bin", "misnative")
	want := "  ⚠ misnative not available: its installer, " + url + ", reported success, but there is no " +
		"misnative at " + realBin + ", where this launcher runs it from.\n" +
		"    Pack acme's install for misnative does not put it there: tell that pack's author, or, if " +
		"yolo ships pack acme, report it at " + IssuesURL + ".\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("the launcher does not end with\n%s\ngot:\n%s", want, out)
	}
	assertNoRetryLoop(t, out, "misnative")
}

// The pnpm launcher: npm exits 0 and leaves no pnpm. pnpm is yolo's own install, not a pack's, so
// the line names yolo's issue tracker, and no retry: the next run would install the same way.
func TestAPnpmInstallThatLandsNothingSaysSoAndNamesYolosTracker(t *testing.T) {
	e, pathEnv, _ := misplacedFixture(t,
		`{"kind":"program","bin":"misnpm","via":"npm","package":"misnpm-pkg"}`)
	out, rc := runGeneratedLauncher(t, e, pathEnv, "pnpm")
	if rc == 0 {
		t.Fatalf("a launcher with nothing to run reported success:\n%s", out)
	}
	realBin := filepath.Join(e.Home, ".npm-global", "bin", "pnpm")
	want := "  ⚠ pnpm not available: npm reported installing pnpm@latest, but there is no pnpm at " +
		realBin + ", where this launcher runs it from.\n" +
		"    yolo's install for pnpm does not put it there: report it at " + IssuesURL + ".\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("the launcher does not end with\n%s\ngot:\n%s", want, out)
	}
	assertNoRetryLoop(t, out, "pnpm")
}

// packUpdateEnv is what `yolo pack update` hands every launcher it runs (cli.execLauncherUpdate):
// the update mode, which installs or refreshes the program and exits instead of running it.
const packUpdateEnv = "YOLO_PACK_UPDATE=1"

// `yolo pack update` on the npm path: npm exits 0 and the package provides no misnpm. The update
// mode exited 0 for it, so `yolo pack update` reported a refresh that left nothing to run. It now
// ends with the first-use launcher's line and exits non-zero, pinned and unpinned alike, since both
// reach the install.
func TestAPackUpdateWhoseNpmInstallLandsNothingSaysSoAndFails(t *testing.T) {
	for _, tc := range []struct{ name, pkg, spec string }{
		{"unpinned", "misnpm-pkg", "misnpm-pkg@latest"},
		{"pinned", "misnpm-pkg@1.2.3", "misnpm-pkg@1.2.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, pathEnv, logPath := misplacedFixture(t,
				`{"kind":"program","bin":"misnpm","via":"npm","package":"`+tc.pkg+`"}`)
			out, rc := runGeneratedLauncher(t, e, pathEnv, "misnpm", packUpdateEnv)
			if rc == 0 {
				t.Fatalf("`yolo pack update` reported success for an install that left nothing to run:\n%s", out)
			}
			if log, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(log), "install") {
				t.Fatalf("the update mode never ran npm install (%v):\n%s", err, log)
			}
			realBin := filepath.Join(e.Home, ".npm-global", "bin", "misnpm")
			want := "  ⚠ misnpm not available: npm reported installing " + tc.spec + ", but there is no " +
				"misnpm at " + realBin + ", where this launcher runs it from.\n" +
				"    Pack acme's install for misnpm does not put it there: tell that pack's author, or, " +
				"if yolo ships pack acme, report it at " + IssuesURL + ".\n"
			if !strings.HasSuffix(out, want) {
				t.Errorf("the update does not end with\n%s\ngot:\n%s", want, out)
			}
			assertNoRetryLoop(t, out, "misnpm")
		})
	}
}

// `yolo pack update` on the installer path: the vendor installer exits 0 and puts nothing at
// ~/.local/bin. The update mode already exited non-zero, and said nothing about why.
func TestAPackUpdateWhoseInstallerLandsNothingSaysSoAndFails(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#!/bin/bash\nmkdir -p \"$HOME/elsewhere\"\n" +
			"printf '#!/bin/sh\\n' > \"$HOME/elsewhere/misnative\"\necho installed\nexit 0\n"))
	}))
	t.Cleanup(srv.Close)
	url := srv.URL + "/install.sh"
	e, pathEnv, _ := misplacedFixture(t,
		`{"kind":"program","bin":"misnative","via":"installer","url":"`+url+`"}`)
	out, rc := runGeneratedLauncher(t, e, pathEnv, "misnative", packUpdateEnv)
	if rc == 0 {
		t.Fatalf("`yolo pack update` reported success for an install that left nothing to run:\n%s", out)
	}
	realBin := filepath.Join(e.Home, ".local", "bin", "misnative")
	want := "  ⚠ misnative not available: its installer, " + url + ", reported success, but there is no " +
		"misnative at " + realBin + ", where this launcher runs it from.\n" +
		"    Pack acme's install for misnative does not put it there: tell that pack's author, or, if " +
		"yolo ships pack acme, report it at " + IssuesURL + ".\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("the update does not end with\n%s\ngot:\n%s", want, out)
	}
	assertNoRetryLoop(t, out, "misnative")
}

// The installer re-run that updates an installed program: it exits 0 and takes the program away.
// That is the install that left nothing to run, not a failed update, so the update says what the
// launch path's last line says, and only that. It also printed "update failed (status 1) —
// running the installed version.", which contradicted the line under it ("reported success") and
// named a version that was gone, in `yolo pack update` and at a launch alike.
func TestAnInstallerUpdateThatLeavesNothingToRunSaysSoOnce(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#!/bin/bash\nrm -f \"$HOME/.local/bin/misnative\"\necho installed\nexit 0\n"))
	}))
	t.Cleanup(srv.Close)
	url := srv.URL + "/install.sh"
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"yolo pack update", []string{packUpdateEnv}},
		{"a launch due an update", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, pathEnv, _ := misplacedFixture(t,
				`{"kind":"program","bin":"misnative","via":"installer","url":"`+url+`"}`)
			realBin := filepath.Join(e.Home, ".local", "bin", "misnative")
			writeTestFile(t, realBin, "#!/bin/sh\necho old\n")
			if err := os.Chmod(realBin, 0o755); err != nil {
				t.Fatal(err)
			}
			out, rc := runGeneratedLauncher(t, e, pathEnv, "misnative", tc.env...)
			if rc == 0 {
				t.Fatalf("an update that left nothing to run reported success:\n%s", out)
			}
			if !strings.Contains(out, "re-running its installer") {
				t.Fatalf("the launcher did not run the update:\n%s", out)
			}
			want := "  ⚠ misnative not available: its installer, " + url + ", reported success, but there is no " +
				"misnative at " + realBin + ", where this launcher runs it from.\n" +
				"    Pack acme's install for misnative does not put it there: tell that pack's author, or, if " +
				"yolo ships pack acme, report it at " + IssuesURL + ".\n"
			if !strings.HasSuffix(out, want) {
				t.Errorf("the update does not end with\n%s\ngot:\n%s", want, out)
			}
			for _, wrong := range []string{"update failed", "running the installed version"} {
				if strings.Contains(out, wrong) {
					t.Errorf("the update says %q over an installer that reported success and left nothing:\n%s",
						wrong, out)
				}
			}
			assertNoRetryLoop(t, out, "misnative")
		})
	}
}
