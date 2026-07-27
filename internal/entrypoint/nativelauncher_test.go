package entrypoint

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// A native pack's install is a URL piped into bash, and the sharpest thing about it is
// what happens when the URL is WRONG. A stale or moved installer endpoint typically keeps
// answering 200 with a web page — Google's antigravity.google.com/install.sh did exactly
// that, and the launcher fed that HTML to bash, so a wrong URL surfaced as:
//
//	bash: line 1: syntax error near unexpected token `<'
//	bash: line 1: `<!doctype html><html lang="en-US"...
//	curl: (23) Failure writing output to destination
//	⚠ agy not available
//
// Three errors, none of which names the actual problem, and the middle one is a red
// herring (curl only failed because bash died and closed the pipe). The user's reasonable
// conclusion is that the jail is broken.
//
// These tests pin the diagnosis rather than the plumbing: the launcher must SAY the URL
// served a page, and must never hand non-script bytes to bash.

// serveBody starts a localhost server returning body for any path, and returns its URL.
func serveBody(t *testing.T, status int, contentType, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/install.sh"
}

// runNativeLauncher generates the launcher for a native install spec pointing at url,
// runs it, and returns rc + combined output.
func runNativeLauncher(t *testing.T, url string) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	home := t.TempDir()
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url},
		filepath.Join(home, "stamps"),
	)
	script := filepath.Join(home, "probetool")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	// A clean env with HOME pointed at the temp dir, so a real install would land there
	// and nothing touches the developer's home.
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	rc := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running launcher: %v (output %s)", err, out)
		}
		rc = ee.ExitCode()
	}
	return rc, string(out)
}

// TestNativeLauncherRejectsAWebPage is the observed agy failure, reduced.
func TestNativeLauncherRejectsAWebPage(t *testing.T) {
	url := serveBody(t, 200, "text/html; charset=utf-8",
		`<!doctype html><html lang="en-US"><head><base href="https://example.invalid/">`+
			`<script nonce="x">window['cfg'] = {a: 1};</script></head></html>`)
	rc, out := runNativeLauncher(t, url)

	if rc == 0 {
		t.Errorf("a web page must not count as a successful install (rc=0)\n%s", out)
	}
	// The diagnosis, not the symptom.
	if !strings.Contains(out, "not a shell script") {
		t.Errorf("output must say the URL served a page, got:\n%s", out)
	}
	if !strings.Contains(out, url) {
		t.Errorf("output must name the URL that misbehaved, got:\n%s", out)
	}
	// The three misleading lines must be GONE. A bash syntax error means HTML reached
	// bash, which is the bug; curl error 23 is its downstream artifact.
	if strings.Contains(out, "syntax error") {
		t.Errorf("HTML reached bash — it must never be piped there:\n%s", out)
	}
	if strings.Contains(out, "(23)") {
		t.Errorf("curl error 23 is the broken-pipe artifact of feeding bash HTML:\n%s", out)
	}
}

// TestNativeLauncherReportsAnHTTPError covers the other wrong-URL shape: a 404. curl -f
// makes this a clean failure, but it must still be ATTRIBUTED — silence plus "not
// available" sends the user looking at the jail instead of the URL.
func TestNativeLauncherReportsAnHTTPError(t *testing.T) {
	url := serveBody(t, 404, "text/html", "not found")
	rc, out := runNativeLauncher(t, url)

	if rc == 0 {
		t.Errorf("a 404 installer must not count as success\n%s", out)
	}
	if !strings.Contains(out, url) {
		t.Errorf("output must name the URL, got:\n%s", out)
	}
	if !strings.Contains(out, "download failed") {
		t.Errorf("output must attribute the failure to the download, got:\n%s", out)
	}
}

// TestNativeLauncherRunsARealScript is the positive case, and it is what keeps the
// page-rejection above from being over-eager: a genuine installer must still run, and the
// launcher must then exec the binary it produced.
//
// The fake installer writes a script to ~/.local/bin/probetool, which is exactly the
// contract nativeLauncherTemplate assumes (REAL_BIN="$HOME/.local/bin/<bin>").
func TestNativeLauncherRunsARealScript(t *testing.T) {
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		"set -eu",
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
		`echo INSTALLER_RAN`,
	}, "\n")+"\n")

	rc, out := runNativeLauncher(t, url)
	if rc != 0 {
		t.Errorf("a real installer must succeed, rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "INSTALLER_RAN") {
		t.Errorf("the installer script did not run:\n%s", out)
	}
	// The launcher's whole point: after installing, exec the tool.
	if !strings.Contains(out, "PROBETOOL_RAN") {
		t.Errorf("launcher did not exec the installed binary:\n%s", out)
	}
}

// TestNativeLauncherAcceptsAScriptWithoutAShebang guards the rejection rule's edge. The
// check must key on "this is markup", not "this lacks a shebang" — an installer that opens
// with a comment or bare shell is unusual but valid, and rejecting it would break a working
// pack in the name of a better error message.
func TestNativeLauncherAcceptsAScriptWithoutAShebang(t *testing.T) {
	url := serveBody(t, 200, "text/plain", strings.Join([]string{
		"# no shebang here",
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
	}, "\n")+"\n")

	rc, out := runNativeLauncher(t, url)
	if rc != 0 {
		t.Errorf("a shebang-less script is still a script, rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "PROBETOOL_RAN") {
		t.Errorf("launcher did not exec the installed binary:\n%s", out)
	}
}
