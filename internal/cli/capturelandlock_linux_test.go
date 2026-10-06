//go:build linux

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

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

// THE HOST CAPTURE, end to end, through the production chain (captureHost, the supervisor, the
// confinement, capture-run, the generated launcher, no-terminal), its fixture installer probing each
// thing the chain is built to withhold:
//
//   - the real home's ~/.ssh, for writing and reading: refused, and nothing lands outside staging;
//   - the user's runtime directory and the account's own home, each somewhere the confinement would
//     otherwise grant (runLandlockCapture's Exclude): refused;
//   - a CA bundle under the home that SSL_CERT_FILE names (its Read grant): readable;
//   - the user's SSH_AUTH_SOCK and GH_TOKEN (hostCaptureEnv at its call site): unset;
//   - the chain's session (notty at its call site): not the caller's, so it has no terminal of theirs;
//   - two processes it leaves running, one orphaned and one moved into a session of its own
//     (capture.Supervise): gone once captureHost returns.
//
// The entry holds the staging delta alone, relocatable, under the host's origin, which no jail's
// lookup selects; and the floor materializes it into its own prefix, where it runs with no
// environment.
func TestAHostCaptureConfinesItsInstallerAndTheFloorRunsWhatItLeft(t *testing.T) {
	requireHostConfinement(t)
	granted := grantedScratch(t)
	home := floortest.ResolvedTemp(t)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "secret\n")
	ca := filepath.Join(home, ".config", "certs", "ca.pem")
	writeFile(t, ca, "-----BEGIN CERTIFICATE-----\n")
	runtimeDir := filepath.Join(granted, "runtime")
	account := filepath.Join(granted, "account")
	writeFile(t, filepath.Join(runtimeDir, "keyring"), "session secret\n")
	writeFile(t, filepath.Join(account, ".netrc"), "machine example password hunter2\n")
	origAccount := accountHomeDir
	accountHomeDir = func() string { return account }
	t.Cleanup(func() { accountHomeDir = origAccount })
	// A unique argument, so a process the installer left running is found by its command line alone.
	marker := fmt.Sprintf("0.%d", os.Getpid())
	t.Cleanup(func() { killMarked(marker) })
	// The installer lives where the confinement lets curl read it: under the real /tmp it could not.
	script := filepath.Join(granted, "install.sh")
	writeFile(t, script, `#!/bin/sh
set -eu
mkdir -p "$HOME/.local/share/hosttool/v1" "$HOME/.local/bin"
printf '#!/bin/sh\necho hosttool 1.0 "$@"\n' > "$HOME/.local/share/hosttool/v1/hosttool"
chmod 755 "$HOME/.local/share/hosttool/v1/hosttool"
ln -s "$HOME/.local/share/hosttool/v1/hosttool" "$HOME/.local/bin/hosttool"
if echo pwned > '`+filepath.Join(home, ".ssh", "authorized_keys")+`' 2>/dev/null; then echo "STRAY WRITE LANDED"; else echo "stray write refused"; fi
if cat '`+filepath.Join(home, ".ssh", "id_ed25519")+`' >/dev/null 2>&1; then echo "STRAY READ LANDED"; else echo "stray read refused"; fi
if cat '`+filepath.Join(runtimeDir, "keyring")+`' >/dev/null 2>&1; then echo "RUNTIME READ LANDED"; else echo "runtime read refused"; fi
if cat '`+filepath.Join(account, ".netrc")+`' >/dev/null 2>&1; then echo "ACCOUNT READ LANDED"; else echo "account read refused"; fi
if cat "$SSL_CERT_FILE" >/dev/null 2>&1; then echo "ca bundle readable"; else echo "CA BUNDLE UNREADABLE"; fi
echo "env SSH_AUTH_SOCK=${SSH_AUTH_SOCK-unset} GH_TOKEN=${GH_TOKEN-unset}"
echo "chain-session $(sed 's/.*) //' /proc/$PPID/stat | cut -d' ' -f4)"
( sleep 61 `+marker+` </dev/null >/dev/null 2>&1 & )
( yolo internal no-terminal -- sleep 61 `+marker+` </dev/null >/dev/null 2>&1 & )
`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("SSH_AUTH_SOCK", "/run/user/1000/ssh-agent.sock")
	t.Setenv("GH_TOKEN", "ghp_the_users_own")
	t.Setenv("SSL_CERT_FILE", ca)
	elsewhere := floortest.ResolvedTemp(t)
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
	if left := markedProcesses(marker); len(left) > 0 {
		t.Errorf("processes the installer left running outlived the capture: %v", left)
	}
	both := out.String() + errw.String()
	for _, want := range []string{"stray write refused", "stray read refused", "runtime read refused",
		"account read refused", "ca bundle readable", "env SSH_AUTH_SOCK=unset GH_TOKEN=unset",
		"confined by Landlock"} {
		if !strings.Contains(both, want) {
			t.Errorf("the capture's output lacks %q:\n%s", want, both)
		}
	}
	if strings.Contains(both, "LANDED") || strings.Contains(both, "UNREADABLE") {
		t.Errorf("the confined installer reached what the chain withholds, or not what it grants:\n%s", both)
	}
	if sid, ok := chainSession(both); !ok {
		t.Errorf("the installer reported no chain session:\n%s", both)
	} else if mine, _ := unix.Getsid(0); sid == mine {
		t.Errorf("the confined chain runs in the caller's session %d, with the caller's terminal", mine)
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

// grantedScratch is a directory of this test's own that the host capture's confinement grants for
// reading unless something excludes it: under /var/tmp, outside both the real /tmp (never granted) and
// the test's home. A stand-in placed here is refused only by the exclusion under test.
func grantedScratch(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks("/var/tmp")
	if err != nil || base == "/tmp" || strings.HasPrefix(base, "/tmp/") {
		t.Fatalf("this test needs a /var/tmp outside /tmp, and this machine's is %q (%v)", base, err)
	}
	d, err := os.MkdirTemp(base, "yolo-host-capture-test-")
	if err != nil {
		t.Fatalf("this test needs a writable /var/tmp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// chainSession reads the session the installer's parent reported ("chain-session <sid>").
func chainSession(out string) (int, bool) {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "chain-session "); ok {
			sid, err := strconv.Atoi(strings.TrimSpace(v))
			return sid, err == nil
		}
	}
	return 0, false
}

// markedProcesses lists the live processes whose command line carries marker as one of its words.
func markedProcesses(marker string) []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || !slices.Contains(strings.Split(string(b), "\x00"), marker) {
			continue
		}
		if st, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat")); err == nil {
			if i := strings.LastIndexByte(string(st), ')'); i >= 0 && i+2 < len(st) && st[i+2] == 'Z' {
				continue
			}
		}
		out = append(out, e.Name()+" "+strings.ReplaceAll(strings.TrimRight(string(b), "\x00"), "\x00", " "))
	}
	return out
}

// killMarked kills what markedProcesses finds, so a failing run leaves nothing behind it either.
func killMarked(marker string) {
	for _, p := range markedProcesses(marker) {
		pid, _, _ := strings.Cut(p, " ")
		if n, err := strconv.Atoi(pid); err == nil {
			_ = unix.Kill(n, unix.SIGKILL)
		}
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
// cannot build ends it before the exec; its supervisor form refuses misuse and a relative command the
// same way.
func TestTheLandlockExecVerbRunsNothingItCannotConfine(t *testing.T) {
	marker := filepath.Join(floortest.ResolvedTemp(t), "ran")
	if rc := runLandlockExec([]string{"--rw=relative", "--", "/bin/sh", "-c", "touch " + marker}); rc != 2 {
		t.Errorf("a relative grant: rc=%d, want 2", rc)
	}
	if rc := runLandlockExec([]string{"--rw=/tmp", "--", "sh", "-c", "touch " + marker}); rc != 1 {
		t.Errorf("a command with no absolute path: rc=%d, want 1", rc)
	}
	if rc := runLandlockExec([]string{"--supervise", "/bin/sh", "-c", "touch " + marker}); rc != 2 {
		t.Errorf("a supervisor with no --: rc=%d, want 2", rc)
	}
	if rc := runLandlockExec([]string{"--supervise", "--", "sh", "-c", "touch " + marker}); rc != 1 {
		t.Errorf("a supervised command with no absolute path: rc=%d, want 1", rc)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Error("the command ran")
	}
}
