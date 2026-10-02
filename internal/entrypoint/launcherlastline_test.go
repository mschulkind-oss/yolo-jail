package entrypoint

// launcherlastline_test.go pins the last line of the native launcher's two modes that run no
// program: install-only, which `yolo capture` drives in its capture jail (InstallOnlyEnv), and
// update mode, which `yolo pack update` drives (YOLO_PACK_UPDATE=1). Install-only ended an install
// that left nothing to run with a bare "⚠ <bin> not available", where a launch says what happened
// and who can act; update mode said "running the installed version" over an update that failed,
// though it runs nothing.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installerURL serves script as a vendor installer and returns its URL.
func installerURL(t *testing.T, script string) string {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(script))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/install.sh"
}

const installOnlyEnv = InstallOnlyEnv + "=1"

// An installer that exits 0 and puts nothing at ~/.local/bin gets the line a launch gives it: what
// reported success, where the launcher looked, and who can fix the install.
func TestInstallOnlyModeSaysAnInstallThatLandsNothingSoAndNamesThePacksAuthor(t *testing.T) {
	url := installerURL(t, "#!/bin/bash\nmkdir -p \"$HOME/elsewhere\"\n"+
		"printf '#!/bin/sh\\n' > \"$HOME/elsewhere/misnative\"\necho installed\nexit 0\n")
	e, pathEnv, _ := misplacedFixture(t,
		`{"kind":"program","bin":"misnative","via":"installer","url":"`+url+`"}`)
	out, rc := runGeneratedLauncher(t, e, pathEnv, "misnative", installOnlyEnv)
	if rc == 0 {
		t.Fatalf("an install-only run with nothing installed reported success:\n%s", out)
	}
	realBin := filepath.Join(e.Home, ".local", "bin", "misnative")
	want := "  ⚠ misnative not available: its installer, " + url + ", reported success, but there is no " +
		"misnative at " + realBin + ", where this launcher runs it from.\n" +
		"    Pack acme's install for misnative does not put it there: tell that pack's author, or, if " +
		"yolo ships pack acme, report it at " + IssuesURL + ".\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("the install-only run does not end with\n%s\ngot:\n%s", want, out)
	}
	assertNoRetryLoop(t, out, "misnative")
}

// An installer that fails gets a line saying so, and no retry advice: what retries a capture is the
// capture's caller (`yolo capture` again, or the next launch), not a run of the program.
func TestInstallOnlyModeSaysAFailedInstallFailed(t *testing.T) {
	url := installerURL(t, "#!/bin/bash\necho 'no such release' >&2\nexit 1\n")
	e, pathEnv, _ := misplacedFixture(t,
		`{"kind":"program","bin":"misnative","via":"installer","url":"`+url+`"}`)
	out, rc := runGeneratedLauncher(t, e, pathEnv, "misnative", installOnlyEnv)
	if rc == 0 {
		t.Fatalf("an install-only run whose installer failed reported success:\n%s", out)
	}
	if want := "  ⚠ misnative not available: its install failed, above.\n"; !strings.HasSuffix(out, want) {
		t.Errorf("the install-only run does not end with\n%s\ngot:\n%s", want, out)
	}
	if strings.Contains(out, "again to retry") {
		t.Errorf("the install-only run tells the user to run the program again:\n%s", out)
	}
}

// Update mode runs nothing, so an update that failed leaves the installed version where it is, and
// the line says that and how to retry; a launch due the same update still runs the installed one.
func TestAFailedUpdateSaysWhatEachModeDoesNext(t *testing.T) {
	url := installerURL(t, "#!/bin/bash\nexit 0\n")
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"yolo pack update", []string{packUpdateEnv},
			"  ⚠ misnative: update failed (status 3) — the installed version stays. Run 'yolo pack update' again to retry.\n"},
		{"a launch due an update", nil,
			"  ⚠ misnative: update failed (status 3) — running the installed version.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, pathEnv, _ := misplacedFixture(t, `{"kind":"program","bin":"misnative","via":"installer",`+
				`"url":"`+url+`","update":["selfupdate"]}`)
			realBin := filepath.Join(e.Home, ".local", "bin", "misnative")
			writeTestFile(t, realBin, "#!/bin/sh\nif [ \"$1\" = selfupdate ]; then exit 3; fi\necho RAN_INSTALLED\n")
			if err := os.Chmod(realBin, 0o755); err != nil {
				t.Fatal(err)
			}
			out, _ := runGeneratedLauncher(t, e, pathEnv, "misnative", tc.env...)
			if !strings.Contains(out, tc.want) {
				t.Errorf("the failed update does not say\n%s\ngot:\n%s", tc.want, out)
			}
			ran := strings.Contains(out, "RAN_INSTALLED")
			if ran != (tc.env == nil) {
				t.Errorf("ran the installed version: %v, want %v:\n%s", ran, tc.env == nil, out)
			}
		})
	}
}
