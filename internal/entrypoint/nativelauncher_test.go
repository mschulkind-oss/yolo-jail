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
	rc, out, _ := runNativeLauncherWithReceipts(t, url)
	return rc, out
}

// runNativeLauncherWithReceipts is runNativeLauncher plus the receipt file the run
// produced. The path is BAKED into the launcher at generation time, so it has to be
// pointed at the temp home here or every run of this file would append to the
// developer's real /workspace/.yolo.
func runNativeLauncherWithReceipts(t *testing.T, url string) (int, string, []map[string]any) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	home := t.TempDir()
	// A parent that does not exist yet: the receipt writer must create it, because
	// macos-user stages no <ws>/.yolo.
	receipts := filepath.Join(home, "ws", ".yolo", "receipts.jsonl")
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url},
		filepath.Join(home, "stamps"),
		receipts,
		"", // no capture store: these cells are about the DOWNLOAD path
		true,
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
	return rc, string(out), readReceipts(t, receipts)
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

// TestNativeLauncherReportsAnInstallerThatLandsNothing is the LANDING-PATH regression, and
// it is the cell that decides whether a `via: installer` flip is safe for a given vendor.
//
// nativeLauncherTemplate hardcodes REAL_BIN="$HOME/.local/bin/$BIN", so an installer whose
// DEFAULT prefix is anywhere else leaves the jail in the worst of both states: installed,
// and not found. The launcher then reinstalls on every single invocation and exits 1.
//
// It is not hypothetical. gh.io/copilot-install resolves PREFIX to /usr/local when `id -u`
// is 0 and to $HOME/.local otherwise; a container-backend jail runs as root under an
// unconditional `--read-only` rootfs, so the installer's own `mkdir -p "$INSTALL_DIR"`
// fails and it exits 1 having downloaded nothing (measured 2026-09-04, which is why
// packs/copilot stayed on npm while packs/codex flipped). Both shapes are covered below,
// because a flip can fail either way round — an installer can also succeed loudly while
// putting the binary somewhere the launcher will never look.
//
// The receipt assertion is the half that is easy to lose. `_do_install` writes one only
// under `[ -x "$REAL_BIN" ]`; drop that guard and every failed install starts recording an
// install that did not happen, which is precisely the claim the reconcile in
// program-delivery.md §10 reads back.
func TestNativeLauncherReportsAnInstallerThatLandsNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script []string
	}{
		{
			// The quiet shape: the installer believes it succeeded, and put the binary
			// under a prefix the launcher does not know about.
			name: "exits 0 having installed elsewhere",
			script: []string{
				"#!/bin/bash",
				"set -eu",
				`mkdir -p "$HOME/somewhere-else/bin"`,
				`printf '#!/bin/bash\necho WRONG_PREFIX\n' > "$HOME/somewhere-else/bin/probetool"`,
				`chmod +x "$HOME/somewhere-else/bin/probetool"`,
				`echo INSTALLER_RAN`,
			},
		},
		{
			// The copilot shape: the installer refuses a prefix it cannot create and exits
			// non-zero. `bash "$script" || true` in _do_install swallows the status, so the
			// ONLY thing standing between this and a false success is the REAL_BIN test.
			name: "exits 1 unable to create its prefix",
			script: []string{
				"#!/bin/bash",
				"set -eu",
				`echo "Error: Could not create directory /usr/local/bin." >&2`,
				"exit 1",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := serveBody(t, 200, "application/x-sh", strings.Join(tc.script, "\n")+"\n")
			rc, out, receipts := runNativeLauncherWithReceipts(t, url)

			if rc != 1 {
				t.Errorf("an installer that landed nothing at REAL_BIN must fail the "+
					"invocation, rc=%d\n%s", rc, out)
			}
			if !strings.Contains(out, "probetool not available") {
				t.Errorf("output must say the binary is not available, got:\n%s", out)
			}
			if len(receipts) != 0 {
				t.Errorf("no install happened, so no receipt may be written — a receipt here "+
					"is a claim the reconcile cannot distinguish from a real install: %v",
					receipts)
			}
		})
	}
}
