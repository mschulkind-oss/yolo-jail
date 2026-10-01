package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// checkdepsnext_test.go pins how `yolo check-deps` ends when something is missing. It wrote
// the bundle file and said "install with the command for your manager", though it had just
// picked the manager (docs/reference/happy-path-principle.md, rule 7). It now prints the
// command, the remedies the file cannot hold, and the re-check, through checkDepsMain, the way
// the command runs.

// checkDepsHome is a temp HOME whose path holds a space, with userConfig as its user config,
// and a PATH holding only the named managers. It returns the home.
func checkDepsHome(t *testing.T, userConfig string, managers ...string) string {
	t.Helper()
	home := filepath.Join(floortest.ResolvedTemp(t), "home dir")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), userConfig)
	fakeBinDir(t, managers...)
	return home
}

func runCheckDepsWritingTheBundle(t *testing.T) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := checkDepsMain(nil, &out, &errw, false)
	return rc, out.String() + errw.String()
}

// TestCheckDepsNamesTheBundleCommand: on an apt host the bundle is a package list, and the
// line after it is the apt command that installs that list, with the path quoted for the
// shell (this home has a space in it), then the re-check.
func TestCheckDepsNamesTheBundleCommand(t *testing.T) {
	pack := filepath.Join(floortest.ResolvedTemp(t), "needpack")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"needpack","contributes":[`+
		`{"kind":"requires","bin":"yolo-cd-one","install_hints":{"apt":"yolo-cd-one-pkg"}},`+
		`{"kind":"requires","bin":"yolo-cd-two","install_hints":{"apt":"yolo-cd-two-pkg"}}]}`)
	home := checkDepsHome(t, `{"packs":[{"source":"file://`+pack+`","name":"needpack"}]}`, "apt")

	rc, report := runCheckDepsWritingTheBundle(t)
	if rc != 1 {
		t.Fatalf("rc = %d with two deps missing, want 1:\n%s", rc, report)
	}
	bundle := filepath.Join(home, ".config", "yolo", "apt-packages.txt")
	if got := readFileT(t, bundle); got != "yolo-cd-one-pkg\nyolo-cd-two-pkg\n" {
		t.Errorf("bundle = %q", got)
	}
	for _, want := range []string{
		"\n  sudo apt install -y $(cat " + shquote.QuoteDisplay(bundle) + ")\n",
		"\n  yolo check-deps",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report is missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "the command for your manager") {
		t.Errorf("the report still hands the user a task instead of the command:\n%s", report)
	}
	if _, ok := registry["check-deps"]; !ok {
		t.Error("the report names `yolo check-deps`, which is not a command")
	}
}

// TestGuardrailsFdHintLeavesFdOnPath is the guardrails pack's apt hint for fd, as a user on
// Debian or Ubuntu meets it. The hint used to be the bare package `fd-find`, which installs
// the binary as /usr/bin/fdfind and /usr/lib/cargo/bin/fd, neither of them `fd` on PATH, so
// following it left fd missing (sources in packs/guardrails/pack.json). Now the remedy links
// Debian's binary into /usr/local/bin, which is on Debian's and Ubuntu's default PATH, and the
// closing lines print it again beside the bundle command, since a package list cannot hold
// the link.
//
// The printed command is RUN, through a shell, against stubs that record what they were asked
// and run nothing, sudo included, so it is checked as a command, not only as text.
func TestGuardrailsFdHintLeavesFdOnPath(t *testing.T) {
	// Before checkDepsHome, which leaves a PATH holding only `apt`.
	sh, shErr := exec.LookPath("sh")
	home := checkDepsHome(t, `{"packs":["guardrails"]}`, "apt")
	rc, report := runCheckDepsWritingTheBundle(t)
	if rc != 1 {
		t.Fatalf("rc = %d with rg and fd missing, want 1:\n%s", rc, report)
	}
	const fdRemedy = "sudo apt install -y fd-find && sudo ln -sf /usr/lib/cargo/bin/fd /usr/local/bin/fd"
	if !strings.Contains(report, "MISSING → "+fdRemedy+"\n") {
		t.Errorf("fd's line does not carry the remedy that leaves fd on PATH:\n%s", report)
	}
	if !strings.Contains(report, "\n  "+fdRemedy+"  # fd (not in the file)\n") {
		t.Errorf("the closing lines do not name fd's command beside the bundle's:\n%s", report)
	}
	bundle := filepath.Join(home, ".config", "yolo", "apt-packages.txt")
	if got := readFileT(t, bundle); got != "ripgrep\n" {
		t.Errorf("bundle = %q, want ripgrep alone: a package list cannot hold fd's link", got)
	}

	if shErr != nil {
		t.Skip("no sh to run the remedy with")
	}
	stubs, log := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	for _, name := range []string{"sudo", "apt", "ln"} {
		writeFile(t, filepath.Join(stubs, name),
			"#!/bin/sh\nprintf '%s %s\\n' \"${0##*/}\" \"$*\" >> "+shquote.Quote(log)+"\n")
		if err := os.Chmod(filepath.Join(stubs, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(sh, "-c", fdRemedy)
	cmd.Env = []string{"PATH=" + stubs}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the fd remedy does not run as a command: %v\n%s", err, out)
	}
	want := "sudo apt install -y fd-find\nsudo ln -sf /usr/lib/cargo/bin/fd /usr/local/bin/fd\n"
	if got := readFileT(t, log); got != want {
		t.Errorf("the fd remedy ran %q, want %q", got, want)
	}
}
