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

// selfInstallPack is a pack whose program has its own installer (`via: npm`) and an apt hint
// beside it, plus a plain requirement only apt installs. The user config it is used with sets
// `host_floor: false`, so the host floor does not answer for the program and check-deps probes it
// as a dependency.
func selfInstallPack(t *testing.T) string {
	t.Helper()
	pack := filepath.Join(floortest.ResolvedTemp(t), "selfpack")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"selfpack","contributes":[`+
		`{"kind":"program","bin":"yolo-cd-self","via":"npm","package":"yolo-cd-self-pkg",`+
		`"install_hints":{"apt":"yolo-cd-self-apt"}},`+
		`{"kind":"requires","bin":"yolo-cd-plain","install_hints":{"apt":"yolo-cd-plain-pkg"}}]}`)
	return pack
}

// TestCheckDepsLeavesAFirstPartyInstallerOutOfTheBundle: a dep whose tool has its own installer
// gets that installer as its remedy, because the tool's own updater keeps it current and a distro
// package pins whatever that repo has (depcheck's selfInstallFlavor). The bundle used to list the
// distro package anyway, through the remedy's apt fallback, so the closing command installed the
// copy the per-line advice had just steered the user away from. The bundle now leaves such a dep
// out, and the closing lines print its own installer beside the bundle's command.
func TestCheckDepsLeavesAFirstPartyInstallerOutOfTheBundle(t *testing.T) {
	pack := selfInstallPack(t)
	home := checkDepsHome(t, `{"host_floor": false, "packs":[{"source":"file://`+pack+`","name":"selfpack"}]}`, "apt")

	rc, report := runCheckDepsWritingTheBundle(t)
	if rc != 1 {
		t.Fatalf("rc = %d with two deps missing, want 1:\n%s", rc, report)
	}
	if !strings.Contains(report, "MISSING → npm install -g yolo-cd-self-pkg\n") {
		t.Fatalf("the program's line does not lead with its own installer:\n%s", report)
	}
	bundle := filepath.Join(home, ".config", "yolo", "apt-packages.txt")
	if got := readFileT(t, bundle); got != "yolo-cd-plain-pkg\n" {
		t.Errorf("bundle = %q, want the plain requirement alone: the program has its own installer", got)
	}
	_, closing, _ := strings.Cut(report, "To install what is missing, run:")
	if !strings.Contains(closing, "\n  npm install -g yolo-cd-self-pkg  # yolo-cd-self (not in the file)\n") {
		t.Errorf("the closing lines do not name the program's own installer beside the bundle:\n%s", report)
	}
	if strings.Contains(closing, "yolo-cd-self-apt") {
		t.Errorf("the closing lines still install the distro package for a tool with its own installer:\n%s", report)
	}
}

// TestCheckDepsEndsWithTheRecheckWithoutABundle: every run that finds something missing ends
// with the re-check the bundle path ends with, `yolo check-deps`. A `--no-manifest` run, and a
// run whose missing deps fit no bundle, used to end at the last MISSING line.
func TestCheckDepsEndsWithTheRecheckWithoutABundle(t *testing.T) {
	const recheck = "\n  yolo check-deps  # check again\n"
	pack := func(t *testing.T, name, contributes string) string {
		dir := filepath.Join(floortest.ResolvedTemp(t), name)
		writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"`+name+`","contributes":[`+contributes+`]}`)
		return `{"host_floor": false, "packs":[{"source":"file://` + dir + `","name":"` + name + `"}]}`
	}
	for _, tc := range []struct {
		name, packName, contributes string
		args                        []string
		want                        string
	}{
		{"no manifest", "needpack",
			`{"kind":"requires","bin":"yolo-cd-one","install_hints":{"apt":"yolo-cd-one-pkg"}},` +
				`{"kind":"requires","bin":"yolo-cd-two"}`,
			[]string{"--no-manifest"},
			"\nTo install what is missing, run:\n  sudo apt install -y yolo-cd-one-pkg  # yolo-cd-one\n"},
		{"nothing fits a bundle", "solo",
			`{"kind":"program","bin":"yolo-cd-solo","via":"npm","package":"yolo-cd-solo-pkg"}`,
			nil,
			"\nTo install what is missing, run:\n  npm install -g yolo-cd-solo-pkg  # yolo-cd-solo\n"},
		{"no remedy at all", "bare", `{"kind":"requires","bin":"yolo-cd-bare"}`, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := checkDepsHome(t, pack(t, tc.packName, tc.contributes), "apt")
			var out, errw bytes.Buffer
			rc := checkDepsMain(tc.args, &out, &errw, false)
			report := out.String() + errw.String()
			if rc != 1 {
				t.Fatalf("rc = %d with a dep missing, want 1:\n%s", rc, report)
			}
			if !strings.HasSuffix(report, recheck) {
				t.Errorf("the report does not end with the re-check %q:\n%s", recheck, report)
			}
			if tc.want != "" && !strings.Contains(report, tc.want) {
				t.Errorf("the report is missing %q:\n%s", tc.want, report)
			}
			if entries, _ := os.ReadDir(filepath.Join(home, ".config", "yolo")); len(entries) != 0 {
				t.Errorf("a run with no bundle wrote %d file(s) under ~/.config/yolo", len(entries))
			}
		})
	}
}
