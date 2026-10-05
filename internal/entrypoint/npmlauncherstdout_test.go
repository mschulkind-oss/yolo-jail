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

// npmlauncherstdout_test.go pins where the npm launcher's own install writes: to STDERR, on
// every path that runs `npm install`, so a piped launch (`pi -p … | consumer`) hands the
// consumer the program's output and nothing else (docs/design/pi-extension-store-builds.md §9,
// "Found on the way"). The install used to run with `2>&1` and no redirect, which put npm's
// whole log, its errors included, on the launcher's standard output whenever the hourly update
// was due or the home was cold.
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
