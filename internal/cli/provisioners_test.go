package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// provisioners_test.go drives the user's provisioner order (`provisioners`,
// docs/design/provisioner-sets.md PS-D11) through the two host reports that resolve a remedy —
// `yolo check-deps` and `yolo host apply --assert`'s dependency gate — the way each command runs,
// so deleting the order's one read (packDepRequirements) fails both; and through the host floor's
// production wiring (PS-D12), so deleting newHostFloor's Outranked fails the last two.

// floorBrewProgram is the floorpack fixture's program with a brew recipe beside its npm package.
const floorBrewProgram = `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg",` +
	`"install_hints":{"brew":"floorcli-brew"}}`

// TestHostLaunchRunsTheCopyTheUsersOrderRanksFirst: with brew ranked first and on the launch PATH,
// the floor has no entry for the program, so `yolo host -- floorcli` runs brew's copy on PATH,
// says why, and installs nothing into the floor. With brew absent the order skips it and the floor
// installs and runs its own copy, as it does with no order.
func TestHostLaunchRunsTheCopyTheUsersOrderRanksFirst(t *testing.T) {
	dist, _ := floorHostFixtureWith(t, floorBrewProgram, `,"provisioners":{"host":["brew"]}`)
	brewed := filepath.Join(stubBins(t, "brew", "floorcli"), "floorcli")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || got.target != brewed {
		t.Fatalf("rc=%d target=%s, want brew's copy on PATH %s\n%s", rc, got.target, brewed, errw.String())
	}
	if !strings.Contains(errw.String(), "`provisioners` order ranks brew first for floorcli") {
		t.Errorf("the launch did not say the user's order chose the PATH copy:\n%s", errw.String())
	}
	if n := len(dist.NpmCalls("install")); n != 0 {
		t.Errorf("the floor installed a program the user's order gives to brew (%d npm installs)", n)
	}
}

// TestCheckDepsAsksTheManagerTheUsersOrderRanksAboveTheFloor: the same program, missing, is
// reported by check-deps with brew's command instead of as the floor's to install.
func TestCheckDepsAsksTheManagerTheUsersOrderRanksAboveTheFloor(t *testing.T) {
	floorHostFixtureWith(t, floorBrewProgram, `,"provisioners":{"host":["brew"]}`)
	fakeBinDir(t, "brew")
	rc, report := runCheckDepsWritingTheBundle(t)
	if rc != 1 || !strings.Contains(report, "floorcli         MISSING → brew install floorcli-brew\n") {
		t.Fatalf("rc=%d, want floorcli missing with brew's command:\n%s", rc, report)
	}
	if strings.Contains(report, "yolo's floor installs it") {
		t.Errorf("the floor still claims a program the user's order gives to brew:\n%s", report)
	}

	// Brew gone from the PATH: the order skips it, and the floor answers again.
	fakeBinDir(t)
	if _, report := runCheckDepsWritingTheBundle(t); !strings.Contains(report, "yolo's floor installs it") {
		t.Errorf("with brew absent the floor must answer for floorcli:\n%s", report)
	}
}

// TestCheckDepsFollowsTheUsersProvisionerOrder: on a host with apt and nix, guardrails' rg and fd
// resolve to apt by default; with nix ranked first both resolve to nix, the bundle is nix's, and
// each line says the order chose it.
func TestCheckDepsFollowsTheUsersProvisionerOrder(t *testing.T) {
	home := checkDepsHome(t, `{"packs":["guardrails"]}`, "apt", "nix")
	rc, report := runCheckDepsWritingTheBundle(t)
	if rc != 1 || !strings.Contains(report, "MISSING → sudo apt install -y ripgrep\n") {
		t.Fatalf("with no order, rg's remedy must be apt's (rc %d):\n%s", rc, report)
	}
	if strings.Contains(report, "`provisioners`") {
		t.Errorf("with no order, no line may credit one:\n%s", report)
	}

	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["guardrails"],"provisioners":{"host":["nix"]}}`)
	rc, report = runCheckDepsWritingTheBundle(t)
	if rc != 1 {
		t.Fatalf("rc = %d with rg and fd missing, want 1:\n%s", rc, report)
	}
	for _, want := range []string{
		"MISSING → nix profile install nixpkgs#ripgrep\n",
		"MISSING → nix profile install nixpkgs#fd\n",
		"from nix, which your `provisioners` order ranks first for rg",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	bundle := filepath.Join(home, ".config", "yolo", "nix-packages.txt")
	if got := readFileT(t, bundle); got != "nixpkgs#fd\nnixpkgs#ripgrep\n" {
		t.Errorf("bundle = %q, want nix's installables for both", got)
	}
}

// TestApplyHostAssertOffersTheRemedyTheUsersOrderRanksFirst: a program whose pack ships its own
// npm package and an apt hint. Ranked apt-first, the gate prints and runs apt's command, with the
// terminal (a manager's sudo asks there), the pack's own installer named as the alternative, and
// the order credited under the command.
func TestApplyHostAssertOffersTheRemedyTheUsersOrderRanksFirst(t *testing.T) {
	home, _, binDir := depGateFixture(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin","install_hints":{"apt":"gatebin-deb"}}`)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	body := readFileT(t, cfg)
	writeFile(t, cfg, strings.Replace(body, `{"host_management"`,
		`{"provisioners":{"host":["apt"]},"host_management"`, 1))
	ran := watchInstalls(t, func(string) error {
		return os.WriteFile(filepath.Join(binDir, "gatebin"), []byte("#!/bin/sh\n"), 0o755)
	})

	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, false, true, strings.NewReader("y\n"))
	report := out.String() + errw.String()
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, report)
	}
	if len(*ran) != 1 || (*ran)[0] != "sudo apt install -y gatebin-deb" {
		t.Fatalf("the gate must run the command the user's order ranks first; ran %v\n%s", *ran, report)
	}
	for _, want := range []string{
		"→ sudo apt install -y gatebin-deb",
		"or the pack's own installer: npm install -g gatebin",
		"from apt, which your `provisioners` order ranks first for gatebin",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "runs with no terminal") {
		t.Errorf("an apt remedy keeps the terminal, so the prompt must not say otherwise:\n%s", report)
	}
}

func TestApplyHostDryRunPreservesTheProvisionerRankingDisclosure(t *testing.T) {
	home, _, _ := depGateFixture(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin","install_hints":{"apt":"gatebin-deb"}}`)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeFile(t, cfg, strings.Replace(readFileT(t, cfg), `{"host_management"`,
		`{"provisioners":{"host":["apt"]},"host_management"`, 1))
	ran := watchInstalls(t, func(string) error { t.Fatal("dry run attempted an install"); return nil })
	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("dry run rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{
		"from apt, which your `provisioners` order ranks first for gatebin",
		"a dry run installs nothing",
		"`--assert` offers to run the command above",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("dry run lost %q:\n%s", want, report)
		}
	}
	if len(*ran) != 0 {
		t.Fatalf("dry run installed %v", *ran)
	}
}

// TestHostLaunchNamesTheManagersInstallWhenTheOrderedCopyIsMissing: the usual state right after
// the user writes the order. The floor installed and ran its own copy before; with brew ranked
// first and on the PATH but brew's copy not installed yet, the launch stops (brew's copy is the
// one the order runs) and names the one command that installs it, brew's own, with the route back
// to yolo's copy — never the `host_path` hint or the deselected-entry removal, which say the
// program is not installed anywhere and that no selected pack delivers it, both false here.
func TestHostLaunchNamesTheManagersInstallWhenTheOrderedCopyIsMissing(t *testing.T) {
	floorHostFixtureWith(t, floorBrewProgram, "")
	stubBins(t, "brew")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("with no order the floor must install and run its copy: rc=%d\n%s", rc, errw.String())
	}

	cfg := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	writeFile(t, cfg, strings.Replace(readFileT(t, cfg), `"packs"`, `"provisioners":{"host":["brew"]},"packs"`, 1))
	*got = execCapture{}
	errw.Reset()
	rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil)
	report := errw.String()
	if rc != 127 || got.execed {
		t.Fatalf("brew's copy is missing, so the launch must stop: rc=%d target=%q\n%s", rc, got.target, report)
	}
	for _, want := range []string{
		"`brew install floorcli-brew`",
		"`provisioners` order ranks brew first for floorcli",
		`put "pack" ahead of brew`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the stop lacks %q:\n%s", want, report)
		}
	}
	for _, wrong := range []string{"no selected pack delivers it", "rm -rf", "host_path", "yolo has no copy"} {
		if strings.Contains(report, wrong) {
			t.Errorf("the stop says %q, which is false or not the step here:\n%s", wrong, report)
		}
	}
}
