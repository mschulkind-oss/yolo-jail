package entrypoint

// launchflagsdelivery_test.go pins the THIRD launch spelling (DP-B44, OQ-DP7
// "unify all paths"): a pack's declared launch flags now reach a NON-INTERACTIVE in-jail
// shell — an agent's own `bash -c claude`, a build script, a Makefile — which expands no
// alias and passes through no host argv.
//
// THE SHAPE THE RULING DEMANDS, and it is what most of this file is about. A partial
// injector is worse than none: injecting from the lazy installer alone would deliver the
// bypass for most packs and silently drop it for the names the image bakes, because
// launcherShadows writes no installer for those. So the cells below are written to fail if
// ANY flagged binary ends up without a carrier — not to show that the common case works.
//
// Every behavioural cell runs the GENERATED SCRIPT against a fake program that logs its
// argv, because the question is what the exec line does, and a string assertion on the
// template cannot answer it.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// flaggedPackRoot stages one pack that declares `program <installed>` and launch flags for
// every name in flagged. The two lists are deliberately separable: a pack may declare flags
// for a name it does not install, and the host argv rewrite has always honoured that.
func flaggedPackRoot(t *testing.T, installed []string, flagged map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "flagpack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var entries []string
	for _, b := range installed {
		entries = append(entries,
			`{"kind":"program","bin":"`+b+`","via":"npm","package":"`+b+`-pkg"}`)
	}
	var launch []string
	for _, bin := range sortedBins(flagged) {
		var quoted []string
		for _, f := range flagged[bin] {
			quoted = append(quoted, `"`+f+`"`)
		}
		launch = append(launch, `{"bin":"`+bin+`","flags":[`+strings.Join(quoted, ",")+`]}`)
	}
	entries = append(entries,
		`{"kind":"autonomy","autonomous":{"launch":[`+strings.Join(launch, ",")+`]}}`)
	manifest := `{"name":"flagpack","contributes":[` + strings.Join(entries, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func sortedBins(m map[string][]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	// Small and fixture-only; insertion order does not matter, determinism does.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// runLaunchDirPasses runs the three launch-dir generators in the order both boot paths run
// them. Driving the PRODUCTION functions is the point: a cell that called DeliverLaunchFlags
// alone would pass against a tree where the installer pass claims every name first.
func runLaunchDirPasses(t *testing.T, e *Env) {
	t.Helper()
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers: %v", err)
	}
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatalf("GeneratePackageManagerLaunchers: %v", err)
	}
	if err := DeliverLaunchFlags(e); err != nil {
		t.Fatalf("DeliverLaunchFlags: %v", err)
	}
}

// TestEveryFlaggedBinGetsACarrier is the no-partial-injector cell, and the reason this
// change could not ship as an edit to the launcher template alone.
//
// The four names are the four ways a flagged binary can arrive, and the middle two are the
// ones a naive injector drops: `sh` is provided by the image (launcherShadows declines to
// write an installer for it), `node` is a declared mise tool (same), `git-absent` is a name
// no pack installs at all, and `flagged-agent` is the ordinary case. All four are declared
// with flags by one pack, so all four must have a script in the launch dir that injects
// them.
func TestEveryFlaggedBinGetsACarrier(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":       home,
		"MISE_DATA_DIR":   filepath.Join(home, "mise"),
		"YOLO_MISE_TOOLS": `{"node":"24"}`,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, []string{"flagged-agent"}, map[string][]string{
			"flagged-agent": {"--dangerous"},
			"sh":            {"--dangerous"},
			"node":          {"--dangerous"},
			"git-absent":    {"--dangerous"},
		}),
	})
	runLaunchDirPasses(t, e)

	for _, bin := range []string{"flagged-agent", "sh", "node", "git-absent"} {
		body, err := os.ReadFile(filepath.Join(e.LaunchDir(), bin))
		if err != nil {
			t.Errorf("%s: no carrier in the launch dir (%v). A pack declared launch flags "+
				"for it, so `yolo -- %s` and the interactive prompt inject them and a "+
				"script does not — the partial injector OQ-DP7 forbids", bin, err, bin)
			continue
		}
		if !strings.Contains(string(body), "--dangerous") {
			t.Errorf("%s: its carrier does not carry the declared flag:\n%s", bin, body)
		}
	}
}

// TestAShadowedNameGetsAWrapperAndNOTAnInstaller is the SPLIT itself.
//
// The collision check is untouched by this change and must stay untouched: a lazy installer
// standing in front of /bin/sh is defect 11.1, and "write the installer anyway when it
// carries flags" would also install a second copy of a binary the image ships. So the
// artefact written for a shadowed name must install NOTHING — that is what makes it safe to
// write one at all.
func TestAShadowedNameGetsAWrapperAndNOTAnInstaller(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, nil, map[string][]string{
			"sh": {"--dangerous"},
		}),
	})
	runLaunchDirPasses(t, e)

	body := readFileString(t, filepath.Join(e.LaunchDir(), "sh"))
	for _, forbidden := range []string{"npm install", "_do_install", "curl", "Installing"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the carrier for a name the image provides must install nothing, and "+
				"it contains %q:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, "Launch-flag WRAPPER") {
		t.Errorf("expected the wrapper, got:\n%s", body)
	}
}

// TestTheCollisionCheckStillRefusesTheInstaller is the guard on the guard: adding the
// wrapper must not have relaxed launcherShadows, whose whole job is that a lazy INSTALLER
// never shadows a baked binary. A flagless shadowed name still gets nothing at all — unless
// the credential gate wrote it an env file this entry, which only an agent a profile selects
// has (TestAShadowedAgentWithoutLaunchFlagsStillSourcesItsOwnFile).
func TestTheCollisionCheckStillRefusesTheInstaller(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_PACK_ROOT": writePackWithProgram(t, "shadowy", "sh"),
	})
	runLaunchDirPasses(t, e)
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "sh")); !os.IsNotExist(err) {
		t.Errorf("a name the image provides and NO pack gives flags to must get no script "+
			"at all (err=%v) — the wrapper exists to deliver a declaration, never to stand "+
			"in front of /bin for its own sake", err)
	}
}

// --- behaviour: what the generated scripts actually exec ------------------------------

// fakeTarget writes a program at dir/name that logs one line per argument to logPath.
func fakeTarget(t *testing.T, dir, name, logPath string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argvLogger(t, dir, name, logPath, "")
}

// runScript runs a generated script with argv under a CONTROLLED env, returning its
// combined output and exit code.
//
// The env is passed whole rather than appended to the test process's: PATH is the input
// under test in half these cells, and inheriting the runner's would make "the next entry"
// mean something different on every machine.
func runScript(t *testing.T, path string, argv []string, env []string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	cmd := exec.Command(path, argv...)
	cmd.Dir = filepath.Dir(path)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("could not run %s: %v\n%s", path, err, out)
	}
	return string(out), rc
}

// TestTheGeneratedCarrierInjectsWhatTheInjectorWould is the DIFFERENTIAL cell, and it is
// the one that makes the bash half of the rule safe to have.
//
// The flags are folded once in Go and baked; what the shell re-implements is the skip rule
// (a flag the argv already carries is not added again), because generation time cannot know
// the user's argv. Two implementations of one rule is a drift risk, so neither is trusted:
// each argv below is run through the generated script AND through packload.InjectLaunchFlags,
// and the two answers must be identical.
//
// The second row is the case that makes the skip mandatory rather than nice: `yolo -- acme
// --dangerous` arrives in the jail with the HOST's rewrite already applied, so a carrier
// that prepended unconditionally would double every flag on the most common path there is.
func TestTheGeneratedCarrierInjectsWhatTheInjectorWould(t *testing.T) {
	home := t.TempDir()
	// The third flag carries a GLOB character on purpose. The shell half compares the
	// declared flag against each argument with `case`, the one construct in these templates
	// where a pattern is intended — so the halves that are DATA have to be quoted while the
	// `*` that is syntax is not. Unquoted, `--glob*` matches the `--globby` argv below and
	// the flag is silently dropped, where the Go injector compares strings and adds it.
	root := flaggedPackRoot(t, nil, map[string][]string{
		"target": {"--dangerous", "--also", "--glob*"},
	})
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": root})
	runLaunchDirPasses(t, e)

	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(home, "real")
	logPath := filepath.Join(home, "argv.log")
	fakeTarget(t, realDir, "target", logPath)

	for _, argv := range [][]string{
		{},
		{"sub", "cmd"},
		{"--dangerous"},                // the host already injected it
		{"--dangerous", "--also"},      // …both of them
		{"sub", "--also=yes"},          // the `--flag=value` form packload.hasFlag accepts
		{"--unrelated", "--dangerous"}, // present, but not first
		{"--", "--dangerous"},          // after a separator: still "present", as in Go
		{"arg with space", "'quoted'"}, // quoting survives the round trip
		{"--globby"},                   // a glob-matching NEIGHBOUR of a flag, not the flag
		{"--glob*"},                    // the glob-carrying flag itself: not added twice
	} {
		if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out, rc := runScript(t, filepath.Join(e.LaunchDir(), "target"), argv,
			[]string{"HOME=" + home, "PATH=" + realDir + ":/usr/bin:/bin"})
		if rc != 0 {
			t.Fatalf("argv %v: the carrier exited %d:\n%s", argv, rc, out)
		}
		got := logLines(t, logPath)

		wantFull, _ := packload.InjectLaunchFlags(packs, append([]string{"target"}, argv...))
		want := wantFull[1:]
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("argv %v: the generated carrier and packload.InjectLaunchFlags disagree.\n"+
				" carrier ran: %q\n injector says: %q\n"+
				"The shell half of the skip rule has drifted from the Go half.", argv, got, want)
		}
	}
}

// TestTheInstallerCarrierInjectsToo covers the OTHER carrier with the same instrument: the
// lazy npm launcher, which is what an ordinary pack (claude, copilot) actually gets. The
// wrapper cell above cannot stand in for it — they are two templates, and a flag spliced
// into one is not spliced into the other.
func TestTheInstallerCarrierInjectsToo(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		// Updates OFF, so the launcher's refresh branch cannot reach the network for a
		// question this cell is not asking.
		AgentUpdatesEnv: "false",
		"YOLO_PACK_ROOT": flaggedPackRoot(t, []string{"agenty"}, map[string][]string{
			"agenty": {"--dangerous"},
		}),
	})
	runLaunchDirPasses(t, e)

	// The npm launcher execs $NPM_CONFIG_PREFIX/bin/$BIN, which defaults to
	// $HOME/.npm-global — so the fake goes where the script will look, and the install
	// branch is skipped because the binary is already there.
	logPath := filepath.Join(home, "argv.log")
	fakeTarget(t, filepath.Join(home, ".npm-global", "bin"), "agenty", logPath)

	out, rc := runScript(t, filepath.Join(e.LaunchDir(), "agenty"), []string{"chat"},
		[]string{"HOME=" + home, "PATH=/usr/bin:/bin"})
	if rc != 0 {
		t.Fatalf("the npm launcher exited %d:\n%s", rc, out)
	}
	if got := logLines(t, logPath); strings.Join(got, " ") != "--dangerous chat" {
		t.Errorf("the lazy installer must inject the declared flags at its exec; ran %q\n%s",
			got, out)
	}
}

// TestTheWrapperRunsWhatPathWouldHaveRun is the wrapper's transparency contract. It adds
// flags; it does not change WHICH program runs. The program is resolved at run time from
// PATH, skipping the wrapper's own dir — which is also what keeps an exec of itself (a fork
// bomb) from being reachable.
func TestTheWrapperRunsWhatPathWouldHaveRun(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, nil, map[string][]string{
			"wrapped": {"--dangerous"},
		}),
	})
	runLaunchDirPasses(t, e)

	// TWO copies on PATH, the first one winning, because "the next entry" is the claim —
	// not "some entry".
	first := filepath.Join(home, "first")
	second := filepath.Join(home, "second")
	firstLog := filepath.Join(home, "first.log")
	secondLog := filepath.Join(home, "second.log")
	fakeTarget(t, first, "wrapped", firstLog)
	fakeTarget(t, second, "wrapped", secondLog)

	// The launch dir comes FIRST on the PATH, exactly as BootPath orders it: the wrapper
	// has to skip its own dir or it execs itself.
	path := strings.Join([]string{e.LaunchDir(), first, second, "/usr/bin", "/bin"}, ":")
	out, rc := runScript(t, filepath.Join(e.LaunchDir(), "wrapped"), []string{"go"},
		[]string{"HOME=" + home, "PATH=" + path})
	if rc != 0 {
		t.Fatalf("the wrapper exited %d:\n%s", rc, out)
	}
	if got := logLines(t, firstLog); strings.Join(got, " ") != "--dangerous go" {
		t.Errorf("the wrapper must run the next PATH entry with the flags added; first copy "+
			"logged %q\n%s", got, out)
	}
	if got := logLines(t, secondLog); len(got) != 0 {
		t.Errorf("the wrapper ran the WRONG copy: %q", got)
	}
}

// TestTheWrapperInstallsNothingForUpdateOrCapture is the trap the split creates and the
// reason the wrapper knows these two variables at all.
//
// `yolo pack update` runs <launch dir>/<bin> with YOLO_PACK_UPDATE=1 for every program a
// pack declares, and `yolo capture` runs `env YOLO_INSTALL_ONLY=1 <bin>` — both BY PATH, so
// both land on a wrapper when one is what sits there. Falling through to the exec would RUN
// the agent in both cases, which for capture also writes the tool's first-run state into the
// very directories the capture is about to record.
func TestTheWrapperInstallsNothingForUpdateOrCapture(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, nil, map[string][]string{
			"wrapped": {"--dangerous"},
		}),
	})
	runLaunchDirPasses(t, e)

	realDir := filepath.Join(home, "real")
	logPath := filepath.Join(home, "argv.log")
	fakeTarget(t, realDir, "wrapped", logPath)

	for _, env := range []string{"YOLO_PACK_UPDATE=1", InstallOnlyEnv + "=1"} {
		if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out, rc := runScript(t, filepath.Join(e.LaunchDir(), "wrapped"), nil,
			[]string{"HOME=" + home, "PATH=" + realDir + ":/usr/bin:/bin", env})
		if rc != 0 {
			t.Errorf("%s: the wrapper must exit 0 — there is genuinely nothing to install "+
				"or refresh for a name yolo does not install; got %d:\n%s", env, rc, out)
		}
		if got := logLines(t, logPath); len(got) != 0 {
			t.Errorf("%s: the wrapper RAN the program (%q). `yolo pack update` and `yolo "+
				"capture` would launch the agent instead of refreshing or installing it",
				env, got)
		}
	}
}

// TestNoLaunchFlagsEnvRunsTheCommandBare pins the escape this change owes the user. While
// the alias was the only in-jail carrier, `\name` ran the program without the flags; a
// launcher is on PATH, so that escape is gone and this one replaces it.
func TestNoLaunchFlagsEnvRunsTheCommandBare(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, nil, map[string][]string{
			"wrapped": {"--dangerous"},
		}),
	})
	runLaunchDirPasses(t, e)

	realDir := filepath.Join(home, "real")
	logPath := filepath.Join(home, "argv.log")
	fakeTarget(t, realDir, "wrapped", logPath)

	out, rc := runScript(t, filepath.Join(e.LaunchDir(), "wrapped"), []string{"go"},
		[]string{"HOME=" + home, "PATH=" + realDir + ":/usr/bin:/bin", NoLaunchFlagsEnv + "=1"})
	if rc != 0 {
		t.Fatalf("exited %d:\n%s", rc, out)
	}
	if got := logLines(t, logPath); strings.Join(got, " ") != "go" {
		t.Errorf("%s=1 must run the program with the user's own argv and nothing else; "+
			"ran %q", NoLaunchFlagsEnv, got)
	}
}

// TestAWrapperForANameNothingProvidesSaysSo: a pack may declare flags for a name it does
// not install and nothing else supplies. The wrapper is still written — the rule is total,
// which is what keeps "every flagged bin has a carrier" checkable — and at run time it says
// what happened instead of exec'ing a guess. 127 is the shell's own "command not found",
// which is what this is.
func TestAWrapperForANameNothingProvidesSaysSo(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, nil, map[string][]string{
			"nowhere-at-all": {"--dangerous"},
		}),
	})
	runLaunchDirPasses(t, e)

	out, rc := runScript(t, filepath.Join(e.LaunchDir(), "nowhere-at-all"), nil,
		[]string{"HOME=" + home, "PATH=/usr/bin:/bin"})
	if rc != 127 {
		t.Errorf("a name nothing provides must exit 127 (command not found), got %d:\n%s", rc, out)
	}
	if !strings.Contains(out, "flagpack") {
		t.Errorf("the message must name the pack whose declaration put this file here:\n%s", out)
	}
}

// --- the disclosure --------------------------------------------------------------------

// TestLaunchFlagDeliveryIsDisclosed. The alias disclosure (DP-B42) says what the prompt
// does; the host's block says what `yolo --` did. Neither can say the thing this pass makes
// true — that a SCRIPT gets the flags too — and a reader of either would have concluded the
// opposite, correctly, until now.
func TestLaunchFlagDeliveryIsDisclosed(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, []string{"agenty"}, map[string][]string{
			"agenty": {"--dangerous"},
		}),
	})
	var buf strings.Builder
	e.Stderr = &buf
	runLaunchDirPasses(t, e)

	got := buf.String()
	for _, want := range []string{"agenty", "EVERY invocation", NoLaunchFlagsEnv, e.LaunchDir()} {
		if !strings.Contains(got, want) {
			t.Errorf("the boot must disclose the reach of the launch-flag delivery and "+
				"name %q; got:\n%s", want, got)
		}
	}
}

// Silence is EXACT, for the reason the other two disclosures keep it: a jail whose packs
// declare no launch flag has nothing to say, and a line printed on every launch is how a
// disclosure surface becomes wallpaper (OQ-BP-3).
func TestNoFlagsMeansNoDeliveryDisclosure(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_PACK_ROOT": writePackWithProgram(t, "plain", "plain-bin"),
	})
	var buf strings.Builder
	e.Stderr = &buf
	if err := DeliverLaunchFlags(e); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("no pack declared a launch flag, so nothing may be disclosed; got:\n%s", got)
	}
}

// TestTheWrapperIsAnnouncedAsInstallingNothing: the installer pass warns "no launcher for
// sh — the image provides /bin/sh", which read as a dropped declaration. It is not one any
// more, and the boot has to say which half actually happened.
func TestTheWrapperIsAnnouncedAsInstallingNothing(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, nil, map[string][]string{
			"sh": {"--dangerous"},
		}),
	})
	var buf strings.Builder
	e.Stderr = &buf
	runLaunchDirPasses(t, e)
	if got := buf.String(); !strings.Contains(got, "installs nothing") {
		t.Errorf("a wrapped name must be reported as one yolo installs nothing for; got:\n%s", got)
	}
}

// --- the boot wiring ---------------------------------------------------------------

// TestBothBootPathsDeliverLaunchFlags is the cell that fails when the CALL SITE is deleted
// (AGENTS.md: "does it fail if I delete the call site?").
//
// Everything above drives the generators directly, which is the right instrument for what
// they DO and the wrong one for whether anything runs them. Both boot paths have to: the
// container's entrypoint.Main and RunDarwinBootstrap are separate genStep lists that have
// drifted before — DP-B43 exists because one of them ran a generator whose output the other
// backend cannot read. A jail whose boot skipped this step would have a launch dir with no
// wrapper in it and no test anywhere the wiser.
//
// It reads the SOURCE rather than running a boot, the way run.TestLaunchFlagInjectionHasOne
// DisclosedCallSite does, because a real boot needs a container and this question is about
// two lines of Go.
func TestBothBootPathsDeliverLaunchFlags(t *testing.T) {
	for _, file := range []string{"boot.go", "darwin.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		body := string(src)
		if !strings.Contains(body, "DeliverLaunchFlags(e)") {
			t.Errorf("%s does not run DeliverLaunchFlags. Every pack-declared launch flag "+
				"whose binary the image provides — or which no pack installs — reaches "+
				"nothing on this boot path, silently", file)
			continue
		}
		// ORDER: it fills the gap the other two leave, and a gap is only a gap once they
		// have run. Before GeneratePackageManagerLaunchers it would put a wrapper where
		// pnpm's lazy installer belongs, since that generator yields to any file already
		// at the path.
		if strings.Index(body, "DeliverLaunchFlags(e)") <
			strings.Index(body, "GeneratePackageManagerLaunchers(e)") {
			t.Errorf("%s runs DeliverLaunchFlags BEFORE the package-manager launchers; it "+
				"must run last of the three launch-dir steps", file)
		}
	}
}

// TestTheInjectorHasOneCallSiteInThisPackage is the "one injector" half of OQ-DP7's ruling,
// made structural. Three mechanisms inject a pack's launch flags in this jail — the alias,
// the lazy launcher, the wrapper — and the ruling is that they share the INJECTOR and the
// RECORD, not that they share a delivery point (they cannot: the entry points are disjoint).
//
// So this package may call packload.InjectLaunchFlags exactly once, from launchFlagsFor.
// A second call site is not wrong today and would be the thing that drifts tomorrow, which
// is the same reason run.TestLaunchFlagInjectionHasOneDisclosedCallSite exists on the host
// side. Source-level for that test's reason: the question is about call sites.
func TestTheInjectorHasOneCallSiteInThisPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, "packload.InjectLaunchFlags(") {
				sites = append(sites, fmt.Sprintf("%s:%d", name, i+1))
			}
		}
	}
	if len(sites) != 1 || !strings.HasPrefix(sites[0], "launchflags.go:") {
		t.Errorf("packload.InjectLaunchFlags must be called exactly once in this package, "+
			"from launchFlagsFor; found %v. Every in-jail spelling of a launch folds the "+
			"table through that one call — a second one is the drift DP-B42 deleted from "+
			"the alias path, arriving one file over", sites)
	}
}
