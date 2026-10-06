package entrypoint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// npmlauncherstdout_test.go pins where every launcher template's own install writes: to STDERR,
// on every path that runs `npm install` or a vendor installer in front of the exec, so a piped
// launch (`pi -p … | consumer`) hands the consumer the program's output and nothing else
// (docs/design/pi-extension-store-builds.md §9, "Found on the way", XB-D34 and XB-D43). The npm
// template's install, the native template's installer run and the package-manager launcher's
// install all used to run with `2>&1` and no redirect, which put the whole log, its errors
// included, on the launcher's standard output whenever an install or update ran.
//
// The npm stand-in writes one marker line to each of its streams, so the cells can tell
// "npm's stdout leaked" from "npm's stderr was folded into stdout": both are the defect.

const (
	npmStdoutMark = "NPM-INSTALL-STDOUT"
	npmStderrMark = "NPM-INSTALL-STDERR"
)

// chattyNpm puts an npm in front of the probe's fake that prints to both streams when it
// installs and then runs the fake, the way the real npm prints its "added N packages" summary
// on stdout and its warnings on stderr.
func chattyNpm(t *testing.T, p *npmProbe) string {
	t.Helper()
	dir := filepath.Join(p.home, "chattybin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/bash\n" +
		"if [ \"${1:-}\" = install ]; then echo " + npmStdoutMark + "; echo " + npmStderrMark + " >&2; fi\n" +
		"exec " + shellQuoteForTest(filepath.Join(p.fakeBin, "npm")) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runPiped renders the npm launcher for pkg and runs it with its two streams kept apart, as a
// pipe sees them, with env added to its environment, returning the fake npm's argv log, stdout
// and stderr.
func (p *npmProbe) runPiped(t *testing.T, bin, pkg string, env []string, args ...string) (log []string, stdout, stderr string) {
	t.Helper()
	body := npmAgentLauncher("probe", &packdecl.Install{Kind: "npm", Bin: bin, Package: pkg},
		filepath.Join(p.home, "stamps"), p.receiptsPath, p.updates, launcherServers{}, nil)
	script := filepath.Join(p.home, bin)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script, args...)
	cmd.Env = append([]string{"HOME=" + p.home,
		"PATH=" + chattyNpm(t, p) + ":" + p.fakeBin + ":" + os.Getenv("PATH")}, env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("launcher failed: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
	}
	return p.log(t), out.String(), errb.String()
}

// assertOnlyTheProgramOnStdout is the claim: npm's lines are on stderr, and stdout is exactly
// what the program printed.
func assertOnlyTheProgramOnStdout(t *testing.T, stdout, stderr, want string) {
	t.Helper()
	if stdout != want {
		t.Errorf("a piped launch's stdout must hold the program's output alone:\n got %q\nwant %q\nstderr:\n%s",
			stdout, want, stderr)
	}
	for _, mark := range []string{npmStdoutMark, npmStderrMark} {
		if !strings.Contains(stderr, mark) {
			t.Errorf("npm's %q is not on stderr, where the launcher's own lines go:\n%s", mark, stderr)
		}
	}
}

// TestADueUpdateKeepsNpmOffAPipedLaunchsStdout is the defect as found: the program is
// installed, its hourly update is due and the registry has a newer version, so the launch
// reinstalls before it execs, and the consumer of `tool -p … | consumer` must still read only
// the program's own output.
func TestADueUpdateKeepsNpmOffAPipedLaunchsStdout(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool")
	p.agePastInterval(t, "tool")
	p.truncateLog(t)

	log, stdout, stderr := p.runPiped(t, "tool", "tool", []string{"FAKE_LATEST=2.0.0"}, "-p", "hello")
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Fatalf("the due update did not reinstall, so this cell measured nothing:\n%s",
			strings.Join(log, "\n"))
	}
	assertOnlyTheProgramOnStdout(t, stdout, stderr, "RAN -p hello\n")
}

// TestAColdInstallKeepsNpmOffAPipedLaunchsStdout: the first launch in a home installs through
// the same function, and a first `tool -p … | consumer` is as piped as any later one.
func TestAColdInstallKeepsNpmOffAPipedLaunchsStdout(t *testing.T) {
	p := newNpmProbe(t, "tool")
	log, stdout, stderr := p.runPiped(t, "tool", "tool", nil, "-p", "hello")
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Fatalf("the cold home did not install, so this cell measured nothing:\n%s",
			strings.Join(log, "\n"))
	}
	assertOnlyTheProgramOnStdout(t, stdout, stderr, "RAN -p hello\n")
}

// runStreamsApart runs script with args and env, its two streams kept apart as a pipe sees them,
// and fails the test when it exits non-zero.
func runStreamsApart(t *testing.T, script string, env []string, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(script, args...)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("launcher failed: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
	}
	return out.String(), errb.String()
}

// TestANativeColdInstallKeepsTheInstallerOffAPipedLaunchsStdout: the native template's installer
// run is the same class. `~/.local` is per workspace, so a new workspace's first `claude -p … |
// consumer` is a cold install, and the installer's own output, on either of its streams, must
// reach stderr only.
func TestANativeColdInstallKeepsTheInstallerOffAPipedLaunchsStdout(t *testing.T) {
	for _, tool := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not found")
		}
	}
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		"set -eu",
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho "RAN $*"\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
		"echo " + npmStdoutMark,
		"echo " + npmStderrMark + " >&2",
	}, "\n")+"\n")
	home := t.TempDir()
	body := nativeAgentLauncher("probe",
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url, UpdateVerb: []string{"update"}},
		filepath.Join(home, "stamps"), filepath.Join(home, "receipts.jsonl"), "", true, launcherServers{}, nil)
	script := filepath.Join(home, "probetool")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := runStreamsApart(t, script, []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, "-p", "hello")
	assertOnlyTheProgramOnStdout(t, stdout, stderr, "RAN -p hello\n")
}

// TestAPackageManagerInstallKeepsNpmOffAPipedLaunchsStdout: the package-manager launcher (pnpm)
// installs through `npm install -g` too, and a cold `pnpm … --json | jq` is as piped as any
// agent's launch.
func TestAPackageManagerInstallKeepsNpmOffAPipedLaunchsStdout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	writeTestFile(t, filepath.Join(fakeBin, "npm"), `#!/bin/bash
if [ "${1:-}" = install ]; then
    echo `+npmStdoutMark+`
    echo `+npmStderrMark+` >&2
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/bash\necho "RAN $*"\n' > "$NPM_CONFIG_PREFIX/bin/pnpm"
    chmod +x "$NPM_CONFIG_PREFIX/bin/pnpm"
fi
`)
	if err := os.Chmod(filepath.Join(fakeBin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := pkgManagerLauncher("pnpm", "pnpm", filepath.Join(home, "stamps"), filepath.Join(home, "receipts.jsonl"), nil)
	script := filepath.Join(home, "pnpm")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := runStreamsApart(t, script,
		[]string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH")}, "list", "--json")
	assertOnlyTheProgramOnStdout(t, stdout, stderr, "RAN list --json\n")
}
