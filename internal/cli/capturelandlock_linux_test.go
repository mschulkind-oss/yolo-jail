//go:build linux

package cli

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// capturelandlock_linux_test.go runs the HOST CAPTURE end to end on this machine's kernel
// (docs/design/host-tool-provisioning.md HP-D18): `yolo capture` with no runtime, its fixture
// installer — a file:// script — run confined by Landlock, admitted, and materialized by the floor.
// This test binary stands in for yolo through the whole confined chain (testAsYoloArg), so the
// chain is the production one: landlock-exec, capture-run, the generated launcher, no-terminal.

// requireHostConfinement makes the real kernel this test's, skipping where it cannot confine a host
// capture, unless YOLO_TEST_REQUIRE_LANDLOCK=1 says this runner must.
func requireHostConfinement(t *testing.T) int {
	t.Helper()
	abi, err := capture.HostConfinementABI()
	if err != nil {
		if os.Getenv("YOLO_TEST_REQUIRE_LANDLOCK") == "1" {
			t.Fatalf("YOLO_TEST_REQUIRE_LANDLOCK=1, and this kernel cannot confine a host capture: %v", err)
		}
		if errors.Is(err, capture.ErrLandlockUnavailable) || abi > 0 {
			t.Skipf("no host confinement on this kernel: %v", err)
		}
		t.Fatal(err)
	}
	withHostConfinement(t, abi, nil)
	return abi
}

// THE HOST CAPTURE, end to end: an installer that writes its program under $HOME and also tries the
// real home's ~/.ssh is captured on the host; the stray write and read are refused and nothing lands
// outside the staging home; the entry holds the staging delta alone, relocatable, under the host's
// origin, which no jail's lookup selects; and the floor materializes it into its own prefix, where it
// runs with no environment.
func TestAHostCaptureConfinesItsInstallerAndTheFloorRunsWhatItLeft(t *testing.T) {
	requireHostConfinement(t)
	elsewhere := floortest.ResolvedTemp(t)
	home := floortest.ResolvedTemp(t)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "secret\n")
	script := filepath.Join(elsewhere, "install.sh")
	writeFile(t, script, `#!/bin/sh
set -eu
mkdir -p "$HOME/.local/share/hosttool/v1" "$HOME/.local/bin"
printf '#!/bin/sh\necho hosttool 1.0 "$@"\n' > "$HOME/.local/share/hosttool/v1/hosttool"
chmod 755 "$HOME/.local/share/hosttool/v1/hosttool"
ln -s "$HOME/.local/share/hosttool/v1/hosttool" "$HOME/.local/bin/hosttool"
if echo pwned > '`+filepath.Join(home, ".ssh", "authorized_keys")+`' 2>/dev/null; then echo "STRAY WRITE LANDED"; else echo "stray write refused"; fi
if cat '`+filepath.Join(home, ".ssh", "id_ed25519")+`' >/dev/null 2>&1; then echo "STRAY READ LANDED"; else echo "stray read refused"; fi
`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("SSH_AUTH_SOCK", "/nonexistent/agent.sock")
	pack := filepath.Join(elsewhere, "hostpack")
	installerURL := (&url.URL{Scheme: "file", Path: script}).String()
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"hostpack","contributes":[`+
		`{"kind":"program","bin":"hosttool","via":"installer","url":"`+installerURL+`"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+pack+`","name":"hostpack"}]}`)
	noRuntimeHere(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	orig := hostCaptureSelf
	hostCaptureSelf = func() ([]string, error) { return []string{exe, testAsYoloArg}, nil }
	t.Cleanup(func() { hostCaptureSelf = orig })

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"hosttool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	both := out.String() + errw.String()
	for _, want := range []string{"stray write refused", "stray read refused", "confined by Landlock"} {
		if !strings.Contains(both, want) {
			t.Errorf("the capture's output lacks %q:\n%s", want, both)
		}
	}
	if strings.Contains(both, "LANDED") {
		t.Errorf("the confined installer reached the real home:\n%s", both)
	}
	if _, err := os.Lstat(filepath.Join(home, ".ssh", "authorized_keys")); err == nil {
		t.Fatal("the installer wrote into the real home's ~/.ssh")
	}

	store := &capture.Store{Dir: paths.CapturesDir()}
	entry, rec, err := resolveFloorCapture(store, "hosttool", capture.Platform())
	if err != nil {
		t.Fatalf("the floor finds no host capture: %v\n%s", err, both)
	}
	if rec.Platform != hostCapturePlatform(capture.Platform()) {
		t.Errorf("the capture's record names %s, want the host's origin", rec.Platform)
	}
	if _, _, err := resolveCaptureFor(store, "hosttool", capture.Platform()); err == nil {
		t.Error("a jail's lookup selects the host capture")
	}
	m, err := capture.ReadManifest(entry.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Relocatable || m.RefScan != capture.RefScanFull {
		t.Errorf("manifest relocatable=%v scan=%q (%v), want the full scan's relocatable entry", m.Relocatable,
			m.RefScan, m.NotRelocatable)
	}
	var got []string
	for _, e := range m.Entries {
		got = append(got, e.Path)
	}
	want := []string{".local", ".local/bin", ".local/bin/hosttool", ".local/share", ".local/share/hosttool",
		".local/share/hosttool/v1", ".local/share/hosttool/v1/hosttool"}
	if !slices.Equal(got, want) {
		t.Errorf("the entry holds %q, want the installer's delta alone %q", got, want)
	}

	// The floor's install of it, through the production wiring.
	p := hostfloor.Program{Pack: "hostpack", Install: packdecl.Install{Kind: "native", Bin: "hosttool",
		InstallerURL: installerURL}}
	f := productionHostFloor(io.Discard, []hostfloor.Program{p})
	f.GOOS = "linux"
	f.Capture = func(string) error { t.Fatal("the floor captured again although the store holds one"); return nil }
	st, outcome, err := f.Ensure(t.Context(), p)
	if err != nil || outcome != hostfloor.Installed {
		t.Fatalf("Ensure = %s %v", outcome, err)
	}
	cmd := exec.Command(st.Launcher, "--version")
	cmd.Env = []string{}
	if b, err := cmd.CombinedOutput(); err != nil || string(b) != "hosttool 1.0 --version\n" {
		t.Errorf("the floor's copy ran %q (%v)", b, err)
	}
	if link, _ := os.Readlink(st.Record.Entry); !strings.HasPrefix(link, f.Dir) {
		t.Errorf("~/.local/bin/hosttool links to %s, not into the floor", link)
	}
	if _, err := os.Stat(store.StagingDir("hosttool")); err == nil {
		t.Error("the capture's staging tree outlived the capture")
	}
}

// THE CONFINED INSTALLER'S ENVIRONMENT is env -i's plus what a download needs: locale, terminal type,
// proxies and the CA bundles, with HOME the staging home, TMPDIR beside it, and the launcher's
// directory first on PATH; no token, agent socket, session bus, runtime directory or XDG directory of
// the real home crosses. The CA files it names are what the confinement lets it read.
func TestTheHostCapturesEnvironmentCarriesNothingOfTheUsersButWhatADownloadNeeds(t *testing.T) {
	environ := []string{"PATH=/home/me/bin:/usr/bin", "HOME=/home/me", "TERM=xterm-256color", "LANG=en_US.UTF-8",
		"LC_ALL=C.UTF-8", "https_proxy=http://proxy:3128", "NO_PROXY=localhost", "SSL_CERT_FILE=/home/me/ca.crt",
		"NODE_EXTRA_CA_CERTS=/etc/extra.pem", "SSL_CERT_DIR=/etc/ssl/certs:relative", "SSH_AUTH_SOCK=/run/user/1000/ssh",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus", "XDG_RUNTIME_DIR=/run/user/1000",
		"XDG_CONFIG_HOME=/home/me/.config", "GH_TOKEN=ghp_secret", "AWS_SECRET_ACCESS_KEY=x", "TMPDIR=/home/me/tmp"}
	env := hostCaptureEnv(environ, "/s/home", "/s/home/.yolo/bin/launch", "/s/bin", "/s/tmp")
	have := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		have[k] = v
	}
	for k, v := range map[string]string{"HOME": "/s/home", "TMPDIR": "/s/tmp", "TERM": "xterm-256color",
		"LANG": "en_US.UTF-8", "LC_ALL": "C.UTF-8", "https_proxy": "http://proxy:3128", "NO_PROXY": "localhost",
		"SSL_CERT_FILE": "/home/me/ca.crt"} {
		if have[k] != v {
			t.Errorf("%s = %q, want %q", k, have[k], v)
		}
	}
	if !strings.HasPrefix(have["PATH"], "/s/home/.yolo/bin/launch:/s/bin:") || strings.Contains(have["PATH"], "/home/me") {
		t.Errorf("PATH = %q, want the launcher's and the stand-in yolo's directories, then the baseline", have["PATH"])
	}
	for _, k := range []string{"SSH_AUTH_SOCK", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME",
		"GH_TOKEN", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := have[k]; ok {
			t.Errorf("%s crossed into the confined installer's environment", k)
		}
	}
	if got := caTrustPaths(env); !slices.Equal(got, []string{"/etc/extra.pem", "/etc/ssl/certs", "/home/me/ca.crt"}) &&
		!slices.Equal(sorted(got), []string{"/etc/extra.pem", "/etc/ssl/certs", "/home/me/ca.crt"}) {
		t.Errorf("the CA paths the confinement grants = %q", got)
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	slices.Sort(out)
	return out
}

// THE CONFINING VERB RUNS NOTHING IT CANNOT CONFINE: a malformed policy is misuse, and a policy it
// cannot build ends it before the exec.
func TestTheLandlockExecVerbRunsNothingItCannotConfine(t *testing.T) {
	marker := filepath.Join(floortest.ResolvedTemp(t), "ran")
	if rc := runLandlockExec([]string{"--rw=relative", "--", "/bin/sh", "-c", "touch " + marker}); rc != 2 {
		t.Errorf("a relative grant: rc=%d, want 2", rc)
	}
	if rc := runLandlockExec([]string{"--rw=/tmp", "--", "sh", "-c", "touch " + marker}); rc != 1 {
		t.Errorf("a command with no absolute path: rc=%d, want 1", rc)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Error("the command ran")
	}
}
