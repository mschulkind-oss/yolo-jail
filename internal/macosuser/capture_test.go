package macosuser

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
	plan.Seatbelt = SeatbeltProfile(plan.StagingRoot, "", nil)
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
	if len(c.files) != 1 || c.files[0] != plan.ProfilePath {
		t.Errorf("the capture profile was not installed at %s: %v", plan.ProfilePath, c.files)
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
	plan.Seatbelt = SeatbeltProfile(plan.StagingRoot, "", nil)
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
