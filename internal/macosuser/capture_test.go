package macosuser

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// capture_test.go pins the macos-user install-capture plan and its executor.
//
// EVERY TEST HERE ASKS AGENTS.md's QUESTION: does it fail if I delete the call site? The two
// that matter most are TestCapturePlanRefusesTheSessionProfile and
// TestCapturePlanRefusesTheSharedHome — they fail when BuildCapturePlan is pointed at
// SeatbeltProfile or SandboxHome() respectively, which are the two mistakes that would make a
// capture silently run a vendor installer against the machine's one shared agent home.
//
// None of it measures macOS. See seatbeltcapture_test.go's header for what a green run means and
// what it does not.

func testCaptureOptions() CaptureOptions {
	env := jsonx.NewOrderedMap()
	env.Set("TERM", "xterm-256color")
	env.Set("YOLO_GIT_NAME", "Someone")
	return CaptureOptions{
		Bin:          "probetool",
		Config:       jsonx.NewOrderedMap(),
		SelfExe:      "/opt/yolo-jail/bin/yolo",
		HostPackRoot: "/Users/admin/.local/share/yolo-jail/packs-staged",
		SandboxEnv:   env,
		HostUser:     "admin",
	}
}

// The plan is viable as built. Every later test mutates one thing and expects a refusal, so this
// is the baseline that makes those meaningful rather than vacuous.
func TestBuildCapturePlanIsViable(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	if problems := CapturePlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a freshly built capture plan is not viable: %v", problems)
	}
	if plan.StagingRoot != "/Users/Shared/yolo-captures/probetool" {
		t.Errorf("staging root = %q", plan.StagingRoot)
	}
	if plan.StagingHome != plan.StagingRoot+"/home" || plan.OutDir != plan.StagingRoot+"/out" {
		t.Errorf("staging layout = %q / %q", plan.StagingHome, plan.OutDir)
	}
	if plan.OffendingHomeSet {
		t.Errorf("the default capture root reads as inside a user home: %q", plan.OffendingHome)
	}
}

// THE PROFILE CALL SITE. Swap SeatbeltCaptureProfile for the session profile in
// BuildCapturePlan and this fires: a session profile makes the shared home writable, which is
// the one thing a capture must not be able to do.
func TestCapturePlanRefusesTheSessionProfile(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	if plan.Seatbelt != SeatbeltCaptureProfile(plan.StagingRoot) {
		t.Fatalf("BuildCapturePlan did not use SeatbeltCaptureProfile")
	}
	plan.Seatbelt = SeatbeltProfile(plan.StagingRoot, "", nil, HomeReadonly{})
	problems := CapturePlanInvariants(plan)
	if !anyContains(problems, "ALLOWS writes to the shared sandbox home") {
		t.Errorf("a session profile in a capture plan was not refused: %v", problems)
	}
}

// A deny that precedes the allow is no deny at all under last-match-wins, and a hand-written
// profile is exactly where that mistake gets made. The invariant must catch the ordering, not
// just the presence.
func TestCapturePlanRefusesADenyBeforeTheAllow(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	deny := `(deny file-write* (subpath "` + SandboxHome() + `"))`
	// Move the deny above the allow, leaving both present.
	stripped := strings.Replace(plan.Seatbelt, deny+"\n", "", 1)
	plan.Seatbelt = strings.Replace(stripped, "(allow file-write*", deny+"\n(allow file-write*", 1)
	if !anyContains(CapturePlanInvariants(plan), "last-match-wins") {
		t.Errorf("a deny placed before the allow was not refused:\n%s", plan.Seatbelt)
	}
}

// THE BOOTSTRAP-HOME CALL SITE. buildBootstrapEnv takes the home as a parameter precisely so a
// capture can point it at the staging tree; pass SandboxHome() and the capture provisions the
// machine's real agent home and then captures a delta of it.
func TestCapturePlanRefusesTheSharedHome(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	for _, argv := range [][]string{plan.BootstrapArgv, plan.DriverArgv} {
		if !inSlice(argv, "HOME="+plan.StagingHome) {
			t.Errorf("argv does not set HOME=%s: %v", plan.StagingHome, argv)
		}
		if inSlice(argv, "HOME="+SandboxHome()) {
			t.Errorf("argv sets HOME=%s — a capture must not run against the shared home",
				SandboxHome())
		}
	}
	// The login-rc PATH the bootstrap bakes must name the staging home too, or the generated
	// launcher would be reachable only from a PATH pointing at the shared one.
	if !anyHasPrefix(plan.BootstrapArgv, "YOLO_DARWIN_LOGIN_PATH="+plan.StagingHome+"/.yolo/bin/block") {
		t.Errorf("the bootstrap login PATH is not rooted at the staging home: %v", plan.BootstrapArgv)
	}
	broken := plan
	broken.BootstrapArgv = DarwinBootstrapArgv(plan.StagedYolo, SandboxHome(), jsonx.NewOrderedMap(), "")
	if !anyContains(CapturePlanInvariants(broken), "must never run against the shared sandbox home") {
		t.Error("a bootstrap pointed at the shared home was not refused")
	}
}

// THE BLOCKED-TOOL CALL SITE. Core blocks nothing on its own since the `grep -r`/`find`
// rules became a pack contribution, so CaptureOptions.BlockedTools is the ONLY thing that
// can put a shim in the staging home. Drop the argument from the buildBootstrapEnv call —
// the easy omission when adapting to that wider signature — and the capture bootstraps a
// home with no blockers while the launch it claims to reproduce has the guardrails pack's,
// which is exactly the capture-vs-launch difference this file's header forbids.
//
// Both halves are asserted because either alone is vacuous: the positive would pass against
// a config that hard-coded the name, and the negative alone would pass against a call site
// that never threads anything.
func TestCapturePlanCarriesThePacksBlockedTools(t *testing.T) {
	opts := testCaptureOptions()
	opts.BlockedTools = []packload.BlockedTool{{Name: "probeblocker", Suggestion: "use rg"}}
	plan := BuildCapturePlan(opts)
	if !anyContains(plan.BootstrapArgv, "probeblocker") {
		t.Errorf("the packs' blocked tools did not reach YOLO_BLOCK_CONFIG; the staging "+
			"home would carry no shims: %v", plan.BootstrapArgv)
	}
	bare := BuildCapturePlan(testCaptureOptions())
	if anyContains(bare.BootstrapArgv, "probeblocker") {
		t.Errorf("a plan declaring no blocked tools produced one anyway: %v", bare.BootstrapArgv)
	}
}

// A CAPTURE STAGES NO HOME OVERLAY, and must therefore NAME none. The overlay is the
// composed content tree — skills and briefing prose — which a throwaway installer home has
// no use for; StageCommands below copy the binary and the packs and nothing else. Setting
// the variable anyway would point the bootstrap's recursive copy at a directory no command
// created, so the two facts have to move together and this is what fails if only one does.
func TestCapturePlanNamesNoHomeOverlay(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	if anyHasPrefix(plan.BootstrapArgv, "YOLO_DARWIN_HOME_OVERLAY=") {
		t.Errorf("the capture bootstrap names a home overlay it never stages: %v",
			plan.BootstrapArgv)
	}
	for _, cmd := range plan.StageCommands {
		if anyContains(cmd, homeOverlayLeaf) {
			t.Errorf("a capture stage command touches the home-overlay tree: %v", cmd)
		}
	}
}

// The driver argv is where four separate facts have to agree, and none is checkable at run time.
func TestCaptureDriverArgvRunsTheDriverInsideTheProfile(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	argv := plan.DriverArgv
	if argv[0] != "sudo" || !inSlice(argv, "--user=_yolojail") {
		t.Errorf("the driver must run as the sandbox user: %v", argv)
	}
	if !inSlice(argv, "-i") {
		t.Errorf("the driver must run under `env -i`: %v", argv)
	}
	// sandbox-exec, its flag and the profile path CONSECUTIVELY: three loose membership
	// tests would pass for an argv that mentioned all three in unrelated places.
	if !containsArgPair(argv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		t.Errorf("the driver does not run under `sandbox-exec -f %s`: %v", plan.ProfilePath, argv)
	}
	for _, want := range []string{
		plan.StagedYolo, "internal", "capture-run",
		"--home=" + plan.StagingHome,
		"--out=" + plan.OutDir,
		"--scan-content-refs",
		"YOLO_INSTALL_ONLY=1",
		"probetool",
	} {
		if !inSlice(argv, want) {
			t.Errorf("driver argv missing %q: %v", want, argv)
		}
	}
	// The installer runs AFTER the driver's `--`, or the driver would parse it as its own
	// flags rather than executing it.
	sep := idxSlice(argv, "--")
	if sep < 0 || idxSlice(argv, "probetool") < sep {
		t.Errorf("the installer argv is not after the driver's `--`: %v", argv)
	}
	// PATH must reach the staging home's launcher dir, LAST, or `probetool` resolves to
	// whatever else answers to that name.
	if !anyContains(argv, "PATH=") || !anyContains(argv, plan.StagingHome+"/.yolo/bin/launch") {
		t.Errorf("driver PATH does not include the staging home's launcher dir: %v", argv)
	}
}

// Each of the driver's four load-bearing arguments is individually refused when absent, so a
// later edit that drops one is a failure and not a silent behaviour change.
func TestCapturePlanRefusesADriverMissingItsLoadBearingArgs(t *testing.T) {
	cases := []struct{ drop, want string }{
		{"--scan-content-refs", "relocatable:false"},
		{"YOLO_INSTALL_ONLY=1", "first-run state"},
	}
	for _, tc := range cases {
		plan := BuildCapturePlan(testCaptureOptions())
		plan.DriverArgv = without(plan.DriverArgv, tc.drop)
		if !anyContains(CapturePlanInvariants(plan), tc.want) {
			t.Errorf("dropping %q from the driver argv was not refused", tc.drop)
		}
	}
	plan := BuildCapturePlan(testCaptureOptions())
	plan.DriverArgv = without(plan.DriverArgv, "/usr/bin/sandbox-exec")
	if !anyContains(CapturePlanInvariants(plan), "would run unconfined") {
		t.Error("a driver argv with no sandbox-exec was not refused")
	}
}

// Neutral ground, the same rule a workspace obeys. A staging tree inside a user's home is a
// writable foothold for the sandbox uid in the home this backend exists to isolate it from.
func TestCapturePlanRefusesAStagingTreeInsideAHome(t *testing.T) {
	opts := testCaptureOptions()
	opts.CaptureRoot = "/Users/admin/.local/share/yolo-jail/captures/staging"
	plan := BuildCapturePlan(opts)
	if !plan.OffendingHomeSet {
		t.Fatalf("a staging tree under /Users/admin did not read as inside a home")
	}
	if !anyContains(CapturePlanInvariants(plan), "stages on neutral ground") {
		t.Errorf("a staging tree inside a user home was not refused: %v", CapturePlanInvariants(plan))
	}
}

// The pack tree is what makes the launcher exist. Staged-but-unnamed and named-but-unstaged are
// both silently empty renders, so both are refused.
func TestCapturePlanRefusesAPackTreeThatWouldNotArrive(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	if plan.PackRoot == "" {
		t.Fatal("a capture with a host pack root produced no staged pack root")
	}
	unstaged := plan
	unstaged.StageCommands = StageBinaryCommands("/opt/yolo-jail/bin/yolo", "")
	if !anyContains(CapturePlanInvariants(unstaged), "no launcher for probetool would exist") {
		t.Error("an unstaged pack tree was not refused")
	}
	unnamed := plan
	unnamed.BootstrapArgv = without(plan.BootstrapArgv, "YOLO_PACK_ROOT="+plan.PackRoot)
	if !anyContains(CapturePlanInvariants(unnamed), "LoadJailPacks would find no packs") {
		t.Error("a pack tree the bootstrap is never told about was not refused")
	}
}

// The staging commands must START by deleting: a tree left by a killed capture would merge into
// the next one's baseline and be filed as this installer's output.
func TestCaptureStagingCommandsClearBeforeProvisioning(t *testing.T) {
	cmds := CaptureStagingCommands("/Users/Shared/yolo-captures/probetool", "admin")
	if len(cmds) == 0 || cmds[0][0] != rmBin || cmds[0][2] != "/Users/Shared/yolo-captures/probetool" {
		t.Fatalf("the first staging command is not an rm of the tree: %v", cmds)
	}
	joined := joinCmds(cmds)
	for _, want := range []string{
		"mkdir -p /Users/Shared/yolo-captures/probetool/home",
		"mkdir -p /Users/Shared/yolo-captures/probetool/out",
		"chown admin:_yolojail /Users/Shared/yolo-captures/probetool",
		"chmod 2770 /Users/Shared/yolo-captures/probetool",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("staging commands missing %q:\n%s", want, joined)
		}
	}
	// The inheriting ACLs, so the host user can move and chmod files the sandbox uid made.
	aces := WorkspaceACLAces(SandboxGroup)
	for _, ace := range []string{aces["dir"], aces["file_inherit"]} {
		if !strings.Contains(joined, ace) {
			t.Errorf("staging commands do not apply the ACE %q:\n%s", ace, joined)
		}
	}
}

// entrypoint.InstallOnlyEnv is spelled a second time in this package because macosuser must not
// import entrypoint. This is the drift guard that makes the duplication safe.
func TestInstallOnlyEnvMatchesTheMacosUserCaptureArgv(t *testing.T) {
	if captureInstallOnlyVar != entrypoint.InstallOnlyEnv {
		t.Fatalf("macosuser spells the install-only variable %q and entrypoint spells it %q; "+
			"the capture driver would run the launcher's full path and record the tool's "+
			"first-run state", captureInstallOnlyVar, entrypoint.InstallOnlyEnv)
	}
}

// --- the executor ----------------------------------------------------------

// captureDeps is a Deps whose every seam is recorded and scripted.
type captureDeps struct {
	ran      []string
	files    []string
	failOn   string // the first argv containing this substring returns 1
	failFile bool
	out      bytes.Buffer
}

func (c *captureDeps) deps() Deps {
	return Deps{
		IsMacOS:           func() bool { return true },
		Geteuid:           func() int { return 501 },
		Which:             func(string) bool { return true },
		SandboxUserExists: func() bool { return true },
		SelfExe:           func() string { return "/opt/yolo-jail/bin/yolo" },
		HostUser:          func() string { return "admin" },
		Run: func(argv []string) int {
			joined := strings.Join(argv, " ")
			c.ran = append(c.ran, joined)
			if c.failOn != "" && strings.Contains(joined, c.failOn) {
				return 3
			}
			return 0
		},
		InstallRootFile: func(path, _, _ string) bool {
			c.files = append(c.files, path)
			return !c.failFile
		},
		Out:   &c.out,
		Color: false,
	}
}

// The whole order, in one assertion, because the order IS the contract: a profile installed
// after the bootstrap would leave the bootstrap unconfined, and a driver run before the pack
// stage would find no launcher.
func TestRunCapturePlanRunsTheStepsInOrder(t *testing.T) {
	c := &captureDeps{}
	plan := BuildCapturePlan(testCaptureOptions())
	if rc := RunCapturePlan(c.deps(), plan); rc != 0 {
		t.Fatalf("RunCapturePlan = %d, want 0\n%s", rc, c.out.String())
	}
	// Two root-owned files, in this order: the Seatbelt profile the driver is confined by,
	// then the session env file it reads its composed environment out of (envfile.go). The
	// order is the contract — the env file's directory is prepared by a sudo step, and a
	// write before that step would land in a world-readable directory.
	if len(c.files) != 2 || c.files[0] != plan.ProfilePath || c.files[1] != plan.EnvFile {
		t.Errorf("want the profile at %s then the env file at %s, got %v",
			plan.ProfilePath, plan.EnvFile, c.files)
	}
	joined := strings.Join(c.ran, "\n")
	prepare := strings.Index(joined, "sudo "+rmBin+" -rf "+plan.StagingRoot)
	stage := strings.Index(joined, "sudo "+cpBin+" -f /opt/yolo-jail/bin/yolo")
	boot := strings.Index(joined, "darwin-bootstrap")
	drive := strings.Index(joined, "capture-run")
	for _, step := range []struct {
		name string
		at   int
	}{{"prepare", prepare}, {"stage", stage}, {"bootstrap", boot}, {"drive", drive}} {
		if step.at < 0 {
			t.Fatalf("the %s step never ran:\n%s", step.name, joined)
		}
	}
	if !(prepare < stage && stage < boot && boot < drive) {
		t.Errorf("steps out of order (prepare %d, stage %d, bootstrap %d, drive %d):\n%s",
			prepare, stage, boot, drive, joined)
	}
	// Nothing swept: cleanup is the ACT's, after the proto-entry has been moved out.
	if strings.Count(joined, "-rf "+plan.StagingRoot) != 1 {
		t.Errorf("RunCapturePlan cleaned up its own staging tree; the proto-entry is still "+
			"in it at that point:\n%s", joined)
	}
}

// A failing step must stop the ones after it. A capture that bootstrapped nothing and then ran
// the driver would record an empty delta and call it a package.
func TestRunCapturePlanAbortsOnAFailedStep(t *testing.T) {
	for _, tc := range []struct{ failOn, notAfter string }{
		{failOn: "rm -rf", notAfter: "capture-run"},
		{failOn: "darwin-bootstrap", notAfter: "capture-run"},
	} {
		c := &captureDeps{failOn: tc.failOn}
		plan := BuildCapturePlan(testCaptureOptions())
		if rc := RunCapturePlan(c.deps(), plan); rc == 0 {
			t.Errorf("a failure at %q returned 0", tc.failOn)
		}
		if strings.Contains(strings.Join(c.ran, "\n"), tc.notAfter) {
			t.Errorf("a failure at %q did not stop %q from running", tc.failOn, tc.notAfter)
		}
	}
}

// THE INVARIANT CALL SITE. An unviable plan must not reach a single sudo — the gate is worth
// nothing if it runs after the machine has been changed.
func TestRunCapturePlanRefusesAnUnviablePlanBeforeAnySudo(t *testing.T) {
	c := &captureDeps{}
	plan := BuildCapturePlan(testCaptureOptions())
	plan.Seatbelt = SeatbeltProfile(plan.StagingRoot, "", nil, HomeReadonly{})
	if rc := RunCapturePlan(c.deps(), plan); rc != 1 {
		t.Errorf("RunCapturePlan on an unviable plan = %d, want 1", rc)
	}
	if len(c.ran) != 0 || len(c.files) != 0 {
		t.Errorf("an unviable plan still touched the machine: ran %v, wrote %v", c.ran, c.files)
	}
	if !strings.Contains(c.out.String(), "capture plan is not viable") {
		t.Errorf("the refusal did not say why:\n%s", c.out.String())
	}
}

// The gates, each one on its own, because "fail closed before any subprocess" is the property.
func TestRunCapturePlanGatesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*Deps)
		want   string
	}{
		{"not macOS", func(d *Deps) { d.IsMacOS = func() bool { return false } }, "requires macOS"},
		{"under sudo", func(d *Deps) { d.Geteuid = func() int { return 0 } }, "under sudo"},
		{"no sandbox-exec", func(d *Deps) { d.Which = func(string) bool { return false } }, "sandbox-exec not found"},
		{"no sandbox user", func(d *Deps) { d.SandboxUserExists = func() bool { return false } }, "does not exist"},
	} {
		c := &captureDeps{}
		d := c.deps()
		tc.break_(&d)
		if rc := RunCapturePlan(d, BuildCapturePlan(testCaptureOptions())); rc != 1 {
			t.Errorf("%s: RunCapturePlan = %d, want 1", tc.name, rc)
		}
		if len(c.ran) != 0 {
			t.Errorf("%s: a closed gate still ran %v", tc.name, c.ran)
		}
		if !strings.Contains(c.out.String(), tc.want) {
			t.Errorf("%s: refusal did not mention %q:\n%s", tc.name, tc.want, c.out.String())
		}
	}
}

// The act's own responsibility: move the finished proto-entry where the host act will look for
// it, then sweep. A capture that produced an entry nobody can admit produced nothing.
func TestRunCaptureActMovesTheProtoEntryAndSweeps(t *testing.T) {
	tmp := t.TempDir()
	opts := testCaptureOptions()
	opts.CaptureRoot = filepath.Join(tmp, "captures")
	dest := filepath.Join(tmp, "store", "staging", "probetool", "out")

	c := &captureDeps{}
	d := c.deps()
	// The driver's job, simulated: fill the out dir the way capture.Run would.
	realRun := d.Run
	d.Run = func(argv []string) int {
		rc := realRun(argv)
		if strings.Contains(strings.Join(argv, " "), "capture-run") {
			tree := filepath.Join(opts.CaptureRoot, "probetool", "out", "tree", ".local")
			if err := os.MkdirAll(tree, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return rc
	}
	if rc := RunCaptureAct(d, opts, dest, false); rc != 0 {
		t.Fatalf("RunCaptureAct = %d, want 0\n%s", rc, c.out.String())
	}
	if _, err := os.Stat(filepath.Join(dest, "tree", ".local")); err != nil {
		t.Errorf("the proto-entry did not arrive at %s: %v", dest, err)
	}
	// The sweep runs, and only after the move — the tree is gone from the source side and
	// the destination still has the entry.
	if strings.Count(strings.Join(c.ran, "\n"), "-rf "+filepath.Join(opts.CaptureRoot, "probetool")) != 2 {
		t.Errorf("expected one clearing rm and one sweeping rm:\n%s", strings.Join(c.ran, "\n"))
	}
}

// A dry run prints the plan and touches nothing — the same contract `yolo run --dry-run` has,
// and the only way to inspect this backend's capture from a machine that cannot run it.
func TestRunCaptureActDryRunExecutesNothing(t *testing.T) {
	c := &captureDeps{}
	if rc := RunCaptureAct(c.deps(), testCaptureOptions(), "/nonexistent/dest", true); rc != 0 {
		t.Errorf("dry run = %d, want 0\n%s", rc, c.out.String())
	}
	if len(c.ran) != 0 || len(c.files) != 0 {
		t.Errorf("a dry run touched the machine: ran %v, wrote %v", c.ran, c.files)
	}
	for _, want := range []string{
		"macos-user install-capture plan",
		"/Users/Shared/yolo-captures/probetool",
		"(deny file-write* (subpath \"/Users/_yolojail\"))",
		"capture-run",
		"all capture plan invariants hold",
	} {
		if !strings.Contains(c.out.String(), want) {
			t.Errorf("the dry-run plan does not show %q:\n%s", want, c.out.String())
		}
	}
}

// --- helpers ---------------------------------------------------------------

func anyContains(items []string, sub string) bool {
	for _, s := range items {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func anyHasPrefix(items []string, prefix string) bool {
	for _, s := range items {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func without(items []string, drop string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s == drop {
			continue
		}
		out = append(out, s)
	}
	return out
}

func joinCmds(cmds [][]string) string {
	parts := make([]string, 0, len(cmds))
	for _, c := range cmds {
		parts = append(parts, strings.Join(c, " "))
	}
	return strings.Join(parts, "\n")
}

// --- a fork's build (FP-D24) -------------------------------------------------

const testBuildID = "0123456789abcdef"

func testForkBuildOptions() ForkBuildOptions {
	darwinEnv := jsonx.NewOrderedMap()
	darwinEnv.Set("PKG_CONFIG_PATH", "/nix/store/abc-profile/lib/pkgconfig")
	opts := testCaptureOptions()
	opts.Darwin = &Darwin{PathPrefix: []string{"/nix/store/abc-profile/bin"}, Env: darwinEnv,
		ProfilePath: "/nix/store/abc-profile", System: "aarch64-darwin"}
	return ForkBuildOptions{
		CaptureOptions: opts,
		BuildID:        testBuildID,
		Build:          `npm ci && npm install -g "$(npm pack --silent)"`,
		Source:         "/Users/admin/.local/share/yolo-jail/captures/staging/fork-" + testBuildID + "/src",
		RepoRoot:       "/Users/admin/code/yolo-jail",
		Toolchain:      "yolo 1.2.3",
	}
}

// The plan is viable as built, and its shape is the build's: a staging tree keyed by the build, the
// checkout and the record siblings of home/ and out/, the sealed profile, and the driver running the
// build line in the checkout under the full reference scan with the blocked tools bypassed, on the
// darwin floor's PATH and environment.
func TestBuildForkBuildPlanIsViable(t *testing.T) {
	plan := BuildForkBuildPlan(testForkBuildOptions())
	if problems := ForkBuildPlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("a freshly built fork build plan is not viable: %v", problems)
	}
	root := "/Users/Shared/yolo-captures/fork-" + testBuildID
	if plan.StagingRoot != root || plan.SrcDir != root+"/src" || plan.ToolchainFile != root+"/toolchain" ||
		plan.StagingHome != root+"/home" || plan.OutDir != root+"/out" {
		t.Errorf("layout = %q %q %q %q %q", plan.StagingRoot, plan.SrcDir, plan.ToolchainFile, plan.StagingHome, plan.OutDir)
	}
	if want := [][]string{{cpBin, "-R", testForkBuildOptions().Source + "/.", root + "/src"}}; joinCmds(plan.SourceCommands) != joinCmds(want) {
		t.Errorf("source commands = %v, want %v", plan.SourceCommands, want)
	}
	if !strings.Contains(plan.Seatbelt, `(deny network-outbound (remote ip "localhost:*"))`) {
		t.Errorf("the build's profile is not the sealed one:\n%s", plan.Seatbelt)
	}
	argv := strings.Join(plan.DriverArgv, " ")
	for _, want := range []string{"/usr/bin/sandbox-exec -f " + plan.ProfilePath, "internal capture-run",
		"--home=" + plan.StagingHome, "--out=" + plan.OutDir, "--scan-content-refs",
		"/usr/bin/env YOLO_BYPASS_SHIMS=1 /bin/bash -c", "cd '" + root + "/src' && npm ci && npm install -g",
		"/nix/store/abc-profile/bin", "printf '%s' 'yolo 1.2.3, darwin floor /nix/store/abc-profile'",
		"> '" + root + "/toolchain'", `NPM_CONFIG_PREFIX="$HOME/.npm-global"`} {
		if !strings.Contains(argv, want) {
			t.Errorf("the driver argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "YOLO_INSTALL_ONLY") {
		t.Errorf("the build's driver runs the installer's launcher protocol:\n%s", argv)
	}
	// The darwin floor's build variables reach the build through the session env file, never the argv.
	if !strings.Contains(plan.EnvFileContent, "PKG_CONFIG_PATH") || strings.Contains(argv, "PKG_CONFIG_PATH") {
		t.Errorf("PKG_CONFIG_PATH is not in the env file (or is on the argv):\n%s\n%s", plan.EnvFileContent, argv)
	}
	// The prepare commands make the checkout as they make home/ and out/.
	prepare := joinCmds(plan.PrepareCommands)
	for _, want := range []string{"mkdir -p " + root + "/src", "chown admin:" + SandboxGroup + " " + root + "/src",
		"chmod 2770 " + root + "/src"} {
		if !strings.Contains(prepare, want) {
			t.Errorf("the prepare commands lack %q:\n%s", want, prepare)
		}
	}
}

// THE STAGING TREE IS THE BUILD'S, never the bin's: an installer capture of the same program clears
// <root>/<bin> before it starts, which a build staged there would lose mid-run.
func TestAForkBuildIsNotStagedWhereAnInstallerCaptureOfItsBinClears(t *testing.T) {
	build := BuildForkBuildPlan(testForkBuildOptions())
	installer := BuildCapturePlan(testCaptureOptions())
	clears := installer.PrepareCommands[0]
	if strings.Join(clears, " ") != rmBin+" -rf "+installer.StagingRoot {
		t.Fatalf("an installer capture's first command is %v, not the clear this test is about", clears)
	}
	if build.StagingRoot == installer.StagingRoot || strings.HasPrefix(build.StagingRoot, installer.StagingRoot+"/") {
		t.Errorf("the build stages at %s, which an installer capture of %s clears (%s)", build.StagingRoot,
			installer.Bin, installer.StagingRoot)
	}
}

// Each invariant fails when the call site it guards is deleted or swapped.
func TestForkBuildPlanInvariantsRefuseEachMissingPiece(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(p *ForkBuildPlan)
		want string
	}{
		{"the unsealed capture profile", func(p *ForkBuildPlan) { p.Seatbelt = SeatbeltCaptureProfile(p.StagingRoot) },
			"it is not sealed"},
		{"the seal before allow default", func(p *ForkBuildPlan) {
			p.Seatbelt = strings.Replace(SeatbeltCaptureProfile(p.StagingRoot), "(allow default)\n",
				sealedBuildLoopbackDeny+"\n"+sealedBuildNixSocketDeny+"\n"+sealedBuildNixPathDeny+"\n(allow default)\n", 1)
		}, "BEFORE `(allow default)`"},
		{"no reference scan", func(p *ForkBuildPlan) { p.DriverArgv = without(p.DriverArgv, captureScanFlag) },
			captureScanFlag},
		{"no shim bypass", func(p *ForkBuildPlan) { p.DriverArgv = without(p.DriverArgv, forkBypassShimsVar+"=1") },
			forkBypassShimsVar + "=1"},
		{"another build line", func(p *ForkBuildPlan) { p.Build = "make install" }, "does not run the fork's build line"},
		{"a checkout copied under sudo", func(p *ForkBuildPlan) {
			p.SourceCommands = [][]string{append([]string{"sudo"}, p.SourceCommands[0]...)}
		}, "copied under sudo"},
		{"no checkout copy", func(p *ForkBuildPlan) { p.SourceCommands = nil }, "nothing copies the checkout"},
		{"no checkout at all", func(p *ForkBuildPlan) { p.Source, p.SourceCommands = "", nil }, "no checkout to copy"},
		{"the checkout inside the home", func(p *ForkBuildPlan) { p.SrcDir = p.StagingHome + "/src" },
			"is not a sibling of the staging home"},
		{"a tree keyed by the bin", func(p *ForkBuildPlan) {
			p.StagingRoot = CaptureStagingRoot("", p.Bin)
		}, "is not keyed by the build"},
		{"no checkout leaf prepared", func(p *ForkBuildPlan) {
			p.PrepareCommands = CaptureStagingCommands(p.StagingRoot, "admin")
		}, "never make " + "/Users/Shared/yolo-captures/fork-" + testBuildID + "/src"},
	} {
		plan := BuildForkBuildPlan(testForkBuildOptions())
		tc.mut(&plan)
		if problems := ForkBuildPlanInvariants(plan); !anyContains(problems, tc.want) {
			t.Errorf("%s: no problem names %q: %v", tc.name, tc.want, problems)
		}
	}
}

// THE RUN ORDER: the tree prepared and the binary staged under sudo, then the checkout copied in as the
// invoking user — never sudo — then the sealed profile installed, the bootstrap, and the driver.
func TestRunForkBuildPlanCopiesTheCheckoutAsTheUserBeforeTheProfile(t *testing.T) {
	c := &captureDeps{}
	plan := BuildForkBuildPlan(testForkBuildOptions())
	if rc := RunForkBuildPlan(c.deps(), plan); rc != 0 {
		t.Fatalf("RunForkBuildPlan = %d\n%s", rc, c.out.String())
	}
	joined := strings.Join(c.ran, "\n")
	stage := strings.Index(joined, "sudo "+cpBin+" -f /opt/yolo-jail/bin/yolo")
	copySrc := strings.Index(joined, "\n"+cpBin+" -R "+plan.Source+"/. "+plan.SrcDir)
	boot := strings.Index(joined, "darwin-bootstrap")
	drive := strings.Index(joined, "capture-run")
	if stage < 0 || copySrc < 0 || boot < 0 || drive < 0 || !(stage < copySrc && copySrc < boot && boot < drive) {
		t.Errorf("steps out of order or missing (stage %d, copy %d, bootstrap %d, drive %d):\n%s", stage, copySrc,
			boot, drive, joined)
	}
	if strings.Contains(joined, "sudo "+cpBin+" -R "+plan.Source) {
		t.Errorf("the checkout was copied under sudo:\n%s", joined)
	}
	if len(c.files) == 0 || c.files[0] != plan.ProfilePath {
		t.Fatalf("no profile installed: %v", c.files)
	}
	// The profile went in after the copy: every command before it ran first.
	if plan.Seatbelt != SeatbeltSealedCaptureProfile(plan.StagingRoot) {
		t.Error("the plan's profile is not the sealed capture profile")
	}
}

// THE ACT: the gates, the darwin floor built from the flake with the config's darwin packages, the
// plan run on its PATH, and the proto-entry and the toolchain record moved where the host act reads
// them.
func TestRunForkBuildActBuildsTheToolchainAndMovesTheResultAndTheRecord(t *testing.T) {
	tmp := t.TempDir()
	opts := testForkBuildOptions()
	opts.Darwin = nil
	opts.CaptureRoot = filepath.Join(tmp, "captures")
	dest := filepath.Join(tmp, "store", "staging", "fork-"+testBuildID, "out")
	toolchain := filepath.Join(tmp, "store", "staging", "fork-"+testBuildID, "toolchain")
	root := ForkBuildStagingRoot(opts.CaptureRoot, testBuildID)

	c := &captureDeps{}
	d := c.deps()
	var built []string
	d.MaterializeDarwin = func(repoRoot string, _ []any) (*Darwin, bool, error) {
		built = append(built, repoRoot)
		return &Darwin{PathPrefix: []string{"/nix/store/floor/bin"}, ProfilePath: "/nix/store/floor"}, true, nil
	}
	realRun := d.Run
	d.Run = func(argv []string) int {
		rc := realRun(argv)
		if strings.Contains(strings.Join(argv, " "), "capture-run") {
			if !strings.Contains(strings.Join(argv, " "), "/nix/store/floor/bin") {
				t.Errorf("the build's driver runs off the darwin floor's PATH:\n%s", strings.Join(argv, " "))
			}
			if err := os.MkdirAll(filepath.Join(root, "out", "tree", ".local"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "toolchain"), []byte("yolo 1.2.3 macOS 26.0"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return rc
	}
	if rc := RunForkBuildAct(d, opts, dest, toolchain, false); rc != 0 {
		t.Fatalf("RunForkBuildAct = %d\n%s", rc, c.out.String())
	}
	if len(built) != 1 || built[0] != opts.RepoRoot {
		t.Errorf("the darwin floor was built %v, want once from %s", built, opts.RepoRoot)
	}
	if _, err := os.Stat(filepath.Join(dest, "tree", ".local")); err != nil {
		t.Errorf("the proto-entry did not arrive at %s: %v", dest, err)
	}
	if b, err := os.ReadFile(toolchain); err != nil || string(b) != "yolo 1.2.3 macOS 26.0" {
		t.Errorf("the toolchain record at %s is %q (%v)", toolchain, b, err)
	}
	if strings.Count(strings.Join(c.ran, "\n"), "-rf "+root) != 2 {
		t.Errorf("expected one clearing rm and one sweeping rm of %s:\n%s", root, strings.Join(c.ran, "\n"))
	}
}

// A MACHINE THE ACT WOULD REFUSE PAYS FOR NO TOOLCHAIN: the gates come before the darwin floor's
// build, and a declared package with no darwin build refuses before the build line runs.
func TestRunForkBuildActRefusesBeforeItsToolchainAndOverAMissingPackage(t *testing.T) {
	c := &captureDeps{}
	d := c.deps()
	d.SandboxUserExists = func() bool { return false }
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		t.Error("the darwin floor was built for a machine with no sandbox account")
		return nil, false, nil
	}
	if rc := RunForkBuildAct(d, testForkBuildOptions(), "/nonexistent/out", "/nonexistent/toolchain", false); rc != 1 ||
		!strings.Contains(c.out.String(), "yolo macos-setup") {
		t.Errorf("rc = %d, want a refusal naming the setup:\n%s", rc, c.out.String())
	}

	c = &captureDeps{}
	d = c.deps()
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return &Darwin{PathPrefix: []string{"/nix/store/floor/bin"}, Skipped: []string{"ripgrpe"}, System: "aarch64-darwin"}, true, nil
	}
	if rc := RunForkBuildAct(d, testForkBuildOptions(), "/nonexistent/out", "/nonexistent/toolchain", false); rc != 1 ||
		!strings.Contains(c.out.String(), "no aarch64-darwin build") || !strings.Contains(c.out.String(), "ripgrpe") {
		t.Errorf("rc = %d, want a refusal naming the package:\n%s", rc, c.out.String())
	}
	if strings.Contains(strings.Join(c.ran, "\n"), "capture-run") {
		t.Error("the build line ran with a declared package missing")
	}
}

// A dry run prints the build's plan and touches nothing.
func TestRunForkBuildActDryRunExecutesNothing(t *testing.T) {
	c := &captureDeps{}
	if rc := RunForkBuildAct(c.deps(), testForkBuildOptions(), "/nonexistent/out", "/nonexistent/toolchain", true); rc != 0 {
		t.Errorf("dry run = %d, want 0\n%s", rc, c.out.String())
	}
	if len(c.ran) != 0 || len(c.files) != 0 {
		t.Errorf("a dry run touched the machine: ran %v, wrote %v", c.ran, c.files)
	}
	for _, want := range []string{"macos-user fork build", "fork-" + testBuildID, sealedBuildLoopbackDeny,
		"all capture plan invariants hold"} {
		if !strings.Contains(c.out.String(), want) {
			t.Errorf("the dry-run plan does not show %q:\n%s", want, c.out.String())
		}
	}
}
