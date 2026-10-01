package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

// TestCheckDepsNamesTheFixForAnUnresolvedPack: a configured pack check-deps could not resolve
// made it exit 1 after "✗ pack <name> could not be resolved, so its deps were not probed: <why>",
// with no step anywhere in the report. That line is now followed by its fix, in the words
// `yolo check` gives for the same pack (check.UserPackFix), and the report ends with the
// re-check, once: a re-check printed under the pack, before the rest of the report, came twice in
// a run that also found a dep missing, and a run with no hints to probe ended on "nothing to
// check" under it, with exit 1.
func TestCheckDepsNamesTheFixForAnUnresolvedPack(t *testing.T) {
	const recheck = "  yolo check-deps  # check again\n"
	missing := filepath.Join(floortest.ResolvedTemp(t), "gone")
	gone := `{"source":"file://` + missing + `","name":"gonepack"}`
	need := filepath.Join(floortest.ResolvedTemp(t), "needpack")
	writeFile(t, filepath.Join(need, "pack.json"), `{"name":"needpack","contributes":[`+
		`{"kind":"requires","bin":"yolo-cd-one","install_hints":{"apt":"yolo-cd-one-pkg"}}]}`)
	for _, tc := range []struct {
		name, packs string
		want        []string
	}{
		{"nothing else to probe", gone, nil},
		{"a dep missing too", gone + `,{"source":"file://` + need + `","name":"needpack"}`,
			[]string{"\n  sudo apt install -y yolo-cd-one-pkg  # yolo-cd-one\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := checkDepsHome(t, `{"host_floor": false, "packs":[`+tc.packs+`]}`, "apt")
			var out, errw bytes.Buffer
			rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
			report := out.String() + errw.String()
			if rc != 1 {
				t.Fatalf("rc = %d with a pack unresolved, want 1:\n%s", rc, report)
			}
			_, after, ok := strings.Cut(report, "✗ pack gonepack could not be resolved")
			if !ok {
				t.Fatalf("no line for the unresolved pack:\n%s", report)
			}
			userConfig := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
			fix := "\n  → Fix the pack at file://" + missing + " (`yolo pack --help` documents every field), " +
				"or its `packs` entry in " + userConfig + "\n"
			if !strings.Contains(after, fix) {
				t.Errorf("the report is missing %q after the unresolved pack:\n%s", fix, report)
			}
			for _, want := range tc.want {
				if !strings.Contains(report, want) {
					t.Errorf("the report is missing %q:\n%s", want, report)
				}
			}
			if !strings.HasSuffix(report, "\n"+recheck) {
				t.Errorf("the report does not end with the re-check %q:\n%s", recheck, report)
			}
			if n := strings.Count(report, "yolo check-deps"); n != 1 {
				t.Errorf("the report names the re-check %d times, want once, at its end:\n%s", n, report)
			}
			if strings.Contains(report, "nothing to check") {
				t.Errorf("a run that could not probe a pack says there is nothing to check:\n%s", report)
			}
		})
	}
}

// TestCheckDepsRefusesAnUnreadableUserConfig: a user config that does not parse gave check-deps
// no packs at all, and it said "no host-dep hints declared by the resolved packs — nothing to
// check." and exited 0, a pass over binaries it never looked for (rule 5). It now names the
// problem, says where to fix it, and ends with the re-check, exit 1.
func TestCheckDepsRefusesAnUnreadableUserConfig(t *testing.T) {
	home := checkDepsHome(t, `{"packs": [`, "apt")
	var out, errw bytes.Buffer
	rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
	report := out.String() + errw.String()
	if rc != 1 {
		t.Fatalf("rc = %d with an unreadable user config, want 1:\n%s", rc, report)
	}
	userConfig := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	for _, want := range []string{"✗ ", "  → Change what it names, in " + userConfig} {
		if !strings.Contains(report, want) {
			t.Errorf("the report is missing %q:\n%s", want, report)
		}
	}
	if !strings.HasSuffix(report, "\n  yolo check-deps  # check again\n") {
		t.Errorf("the report does not end with the re-check:\n%s", report)
	}
	if strings.Contains(report, "nothing to check") {
		t.Errorf("an unreadable config still reads as nothing to check:\n%s", report)
	}
}

// TestCheckDepsNamesTheLocalPacksDirectory: the conventional local pack has no `packs` entry, so
// its step is its directory and `yolo pack lint`, through checkDepsMain, the way the command runs.
func TestCheckDepsNamesTheLocalPacksDirectory(t *testing.T) {
	home := checkDepsHome(t, `{"host_floor": false}`, "apt")
	local := filepath.Join(home, ".config", "yolo-jail", "local")
	writeFile(t, filepath.Join(local, "pack.json"), `{"name":"local","contributes":[{"kind":"no-such-kind"}]}`)
	var out, errw bytes.Buffer
	rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
	report := out.String() + errw.String()
	if rc != 1 {
		t.Fatalf("rc = %d with the local pack unresolved, want 1:\n%s", rc, report)
	}
	if want := "  → Fix what it names in " + local + " (`yolo pack lint " + local + "` re-checks it)"; !strings.Contains(report, want) {
		t.Errorf("the report is missing %q:\n%s", want, report)
	}
}

// TestAShippedPackThatWillNotResolveIsTheMaintainers: the record newUnresolvedPack makes for a
// pack that ships with yolo carries that fact, so its step is the issue tracker, never "fix the
// pack at <source>".
func TestAShippedPackThatWillNotResolveIsTheMaintainers(t *testing.T) {
	t.Setenv("HOME", floortest.ResolvedTemp(t))
	t.Setenv("XDG_CONFIG_HOME", "")
	got := checkDepsUnresolvedStep(newUnresolvedPack(config.EmbeddedPackEntry("claude"), errors.New("boom")))
	if !strings.Contains(got, "is a yolo bug") || strings.Contains(got, "Fix the pack at") {
		t.Errorf("a shipped pack's step is not the maintainers':\n%s", got)
	}
}

// TestCheckDepsUnresolvedStepNamesWhoCanAct: the step depends on why the pack did not resolve.
// A git pack not fetched yet is fetched by `yolo pack install`; a pack that ships with yolo is
// the maintainers' to fix, so the step is the issue tracker and, until then, dropping it; the
// conventional local pack has no `packs` entry, so its step is its directory.
func TestCheckDepsUnresolvedStepNamesWhoCanAct(t *testing.T) {
	t.Setenv("HOME", floortest.ResolvedTemp(t))
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, tc := range []struct {
		name string
		u    unresolvedPack
		want []string
	}{
		{"not fetched", unresolvedPack{Name: "remote", NeedsInstall: true},
			[]string{"`yolo pack install`"}},
		{"shipped", unresolvedPack{Name: "claude", Shipped: true},
			[]string{"is a yolo bug", "/issues", "drop claude from `packs` in " + paths.UserConfigPath()}},
		{"local pack", unresolvedPack{Name: "local", Implicit: true},
			[]string{paths.LocalPackDir(), "`yolo pack lint " + paths.LocalPackDir() + "`"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkDepsUnresolvedStep(tc.u)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the step lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

// TestCheckDepsPrintsABracketedCommandAsWritten: the MISSING line and the package-manager
// alternative under it printed the install command through the rich-markup printer unescaped. A
// bracketed word the printer reads as a style (`acme[red]`) vanished from the command, which
// then named another package, and an unclosed `[` ran on into the `[/dim]` after it, which
// printed as text. Escaping it (richtext.Escape) kept the bracket on screen by writing an
// invisible U+2060 after it, which a pasted command carried into the install. Each command is now
// printed byte for byte, on a terminal and piped, and no line carries the U+2060.
func TestCheckDepsPrintsABracketedCommandAsWritten(t *testing.T) {
	pack := filepath.Join(floortest.ResolvedTemp(t), "brackets")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"brackets","contributes":[`+
		`{"kind":"program","bin":"yolo-cd-br","via":"npm","package":"acme[red]",`+
		`"install_hints":{"apt":"acme-tools[dim"}}]}`)
	checkDepsHome(t, `{"host_floor": false, "packs":[{"source":"file://`+pack+`","name":"brackets"}]}`, "apt")
	for _, color := range []bool{false, true} {
		var out, errw bytes.Buffer
		rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, color)
		raw := out.String() + errw.String()
		report := stripANSI(raw)
		if rc != 1 {
			t.Fatalf("rc = %d with a dep missing, want 1:\n%s", rc, report)
		}
		if strings.Contains(raw, "\u2060") {
			t.Errorf("color=%v: the report carries an invisible U+2060, which a pasted command keeps:\n%q",
				color, raw)
		}
		for _, want := range []string{
			"MISSING → npm install -g acme[red]\n",
			"  or via apt: sudo apt install -y acme-tools[dim\n",
			"\n  npm install -g acme[red]  # yolo-cd-br\n",
		} {
			if !strings.Contains(report, want) {
				t.Errorf("color=%v: the report does not print %q as written:\n%s", color, want, report)
			}
		}
		if strings.Contains(report, "[/dim]") {
			t.Errorf("color=%v: a closing tag printed as text, so the command ran on into it:\n%s",
				color, report)
		}
	}
}
