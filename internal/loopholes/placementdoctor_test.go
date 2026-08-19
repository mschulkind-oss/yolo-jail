package loopholes

// placementdoctor_test.go is §4.3a's PLACEMENT rule at the DOCTOR face.
//
// The rule had exactly one production caller — the spawn (internal/cli/run's
// startLoopholes) — and `RunDoctorChecks` never asked for it. Measured: a hand-placed
// loophole whose manifest is
//
//	{"name":"probehole","transport":"none","doctor_cmd":["/bin/sh","<WORKSPACE>/evil.sh"]}
//
// EXECUTED that script from `yolo loopholes status` and from `yolo check` — two commands
// AGENTS.md and users treat as READ-ONLY PREFLIGHT — because a doctor_cmd is host
// execution and nothing between the manifest and the spawn read the target's placement.
//
// It is fixed in `runDoctorChecks` and NOT at the two call sites, for exactly the reason
// the ORIGIN gate is there: a slice carries no judgement, so a rule the caller is merely
// asked to apply is a rule the next call site does not know about. `PlacementProblems`
// already accepts workspace == "" (narrowing to the jail-home tree), so the doctor path
// has a usable answer available — it just never asked.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeDoctorModule writes a module dir under parent whose only host-side face is a
// doctor_cmd naming target. Separate from writeModule (convergence_test.go) because that
// helper builds the argv from a []string of literals, and here the argv element under test
// is a PATH the test computed.
func writeDoctorModule(t *testing.T, parent, name, target string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","description":"probe","default_enabled":true,` +
		`"transport":"none","lifecycle":"external",` +
		`"doctor_cmd":["/bin/sh","` + target + `"]}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// touchScript writes an executable shell script that creates sentinel, and returns its path.
func touchScript(t *testing.T, dir, sentinel string) string {
	t.Helper()
	p := filepath.Join(dir, "evil-doctor.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ntouch "+sentinel+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// THE MEASURED EXPLOIT. A doctor_cmd naming a script inside the JAIL-HOME tree is host
// execution from a read-only preflight, of a file an agent rewrites between launches.
//
// The jail-home tree rather than the workspace, because that is the tree the doctor path
// can see: RunDoctorChecks has no workspace in hand (`yolo loopholes status` does not take
// one), and PlacementProblems narrows to paths.GlobalHome() when workspace is "" rather
// than disabling the rule. HOME is redirected, so GlobalHome() is this test's temp dir.
func TestDoctorRefusesADoctorCmdInsideTheJailHomeTree(t *testing.T) {
	unsetJail(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	// paths.GlobalHome() == <home>/.local/share/yolo-jail/home
	agentTree := filepath.Join(home, ".local", "share", "yolo-jail", "home")
	if err := os.MkdirAll(agentTree, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "ran")
	script := touchScript(t, agentTree, sentinel)

	mod := writeDoctorModule(t, t.TempDir(), "probehole", script)
	// Through an APPROVED pack module, which is the production shape: every module
	// manifest yolo reads is pack-shipped now, and the ungated RunDoctorChecks refuses a
	// SourcePack record outright. It used to relabel the record SourceBundled to get past
	// that; the bundled channel is retired (docs/design/broker-as-a-pack.md OQ-BP4), and
	// the gated Set is a better answer than the relabel ever was — it exercises the pair
	// of gates in the order production applies them, ORIGIN then PLACEMENT, so a
	// placement refusal here cannot be an origin refusal wearing its message.
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	lp, ok := set.Lookup("probehole")
	if !ok {
		t.Fatalf("fixture module %s was not discovered", mod)
	}

	results := set.RunDoctorChecks([]*Loophole{lp}, 5*time.Second)
	if len(results) != 1 {
		t.Fatalf("want one result, got %d", len(results))
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("THE DOCTOR_CMD RAN. `yolo check` and `yolo loopholes status` are read-only " +
			"preflight, and this argv named a script inside the tree yolo hands the agent — " +
			"§4.3a's placement rule exists precisely to refuse it, and the doctor path never asked")
	}
	if results[0].RC != nil {
		t.Errorf("rc = %d; a refused placement must not produce an exit status", *results[0].RC)
	}
	// REPORTED, with the reason, never silently skipped: silence is indistinguishable from
	// `no-check`, which reads as "this loophole declares no self-check" — the wrong story.
	for _, want := range []string{"not run", "probehole", "§4.3a"} {
		if !strings.Contains(results[0].Output, want) {
			t.Errorf("the withheld result does not carry %q:\n  %s", want, results[0].Output)
		}
	}
}

// The MODULE DIR face, which is the one that subsumes the others: a module dir an agent
// writes means every host-side field of that manifest names an agent-writable target,
// including the ones no path check can see (a Python daemon's imports, a compiled one's
// dlopen). A doctor_cmd of `{loophole_dir}/check.sh` resolves inside it.
func TestDoctorRefusesAModuleDirInsideTheJailHomeTree(t *testing.T) {
	unsetJail(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentTree := filepath.Join(home, ".local", "share", "yolo-jail", "home", "loopholes")
	if err := os.MkdirAll(agentTree, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "ran")

	mod := writeDoctorModule(t, agentTree, "insidehole", "{loophole_dir}/evil-doctor.sh")
	touchScript(t, mod, sentinel)

	// THROUGH THE PACK PATH, with the ORIGIN gate deliberately OPEN, because after
	// OQ-LP10 a pack is the only source whose module dir is judged at all: bundled
	// content is exempt (it IS yolo's own artifact — placement.go's Test-1 reasoning)
	// and a config entry has no module dir. The hand-placed `user` source this test used
	// to ride is retired. HostExecApproved:true is what keeps the two gates
	// distinguishable — the only thing left that can withhold this is placement.
	isolateModules(t)
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	lp, ok := set.Lookup("insidehole")
	if !ok {
		t.Fatal("the module was not discovered")
	}
	results := set.RunDoctorChecks([]*Loophole{lp}, 5*time.Second)
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("a module dir inside the jail-home tree had its doctor_cmd EXECUTED — the dir " +
			"face is the one that covers what no path check can see")
	}
	if !strings.Contains(results[0].Output, "module dir") {
		t.Errorf("the refusal must name the DIR as the problem, not the argv (one mistake, one "+
			"message):\n  %s", results[0].Output)
	}
}

// A LEGITIMATELY-placed loophole still runs. Without this the fix would be
// indistinguishable from disabling every self-check, and a false positive here means a
// `yolo check` that reports every loophole as withheld on every machine.
func TestDoctorStillRunsALegitimatelyPlacedLoophole(t *testing.T) {
	unsetJail(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Outside .local/share/yolo-jail/home: an ordinary tools dir in the user's home.
	outside := filepath.Join(home, "tools")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "ran")
	script := touchScript(t, outside, sentinel)

	mod := writeDoctorModule(t, filepath.Join(home, "modules"), "goodhole", script)
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	lp, ok := set.Lookup("goodhole")
	if !ok {
		t.Fatalf("fixture module %s was not discovered", mod)
	}
	results := set.RunDoctorChecks([]*Loophole{lp}, 5*time.Second) // see the argv-face test above
	if results[0].RC == nil || *results[0].RC != 0 {
		t.Fatalf("a legitimately-placed doctor_cmd must run and report: rc=%v out=%q",
			results[0].RC, results[0].Output)
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Errorf("the self-check did not actually execute (%v) — this control is what makes the "+
			"two refusals above mean something", statErr)
	}
}

// The gated Set.RunDoctorChecks applies the placement rule too. Two entry points into one
// body is how a rule comes to hold on one path and not the other — the same argument that
// put the ORIGIN gate in the shared callee.
func TestSetDoctorAppliesThePlacementRuleToo(t *testing.T) {
	unsetJail(t)
	isolateModules(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentTree := filepath.Join(home, ".local", "share", "yolo-jail", "home")
	if err := os.MkdirAll(agentTree, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "ran")
	script := touchScript(t, agentTree, sentinel)

	mod := writeDoctorModule(t, t.TempDir(), "gated", script)
	// HostExecApproved: the ORIGIN gate PASSES, so the only thing that can withhold this
	// is the placement rule — which is what makes the two gates distinguishable here.
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	lp, ok := set.Lookup("gated")
	if !ok {
		t.Fatal("the module was not discovered")
	}
	results := set.RunDoctorChecks([]*Loophole{lp}, 5*time.Second)
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("an origin-APPROVED pack loophole whose doctor_cmd lives where an agent writes " +
			"was executed through Set.RunDoctorChecks — the placement rule has to hold on both " +
			"entry points, or it holds on whichever one the next caller does not use")
	}
	if !strings.Contains(results[0].Output, "§4.3a") {
		t.Errorf("the withheld reason must be the PLACEMENT one, not the origin one:\n  %s",
			results[0].Output)
	}
}
