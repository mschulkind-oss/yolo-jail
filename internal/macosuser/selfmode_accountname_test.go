package macosuser

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// selfmode_accountname_test.go pins the two run-as-you defects of
// the 2026-10-24 Mac session's sections 1b and 1c (docs/design/macos-user-model-plan.md, MMP-D22 and MMP-D23), on Linux:
// a run-as-you capture's bootstrap is told your real home and may stat it (MMP-D22), and a
// diagnostic that mentions the second account never makes a plan unviable (MMP-D23).

// forkBuildFailureWire is a YOLO_FORK_BUILDS value as a failed run-as-you build leaves it: its
// reason quotes the bootstrap's stderr, which names the account's home.
const forkBuildFailureWire = `{"mupacktree-x":{"reason":"mupacktree-x was not started: its build failed",` +
	`"cause":{"lines":["The end of its stderr:","yolo internal darwin-bootstrap: cannot read the sandbox ` +
	`account's home /Users/_yolojail (stat /Users/_yolojail: operation not permitted)"]}}}`

// patchedTreeFailureWire is a YOLO_PATCHED_TREES value for an extension whose build failed the
// same way: its cause quotes the same stderr, and its other fields name nothing of the account's.
func patchedTreeFailureWire() string {
	return entrypoint.PatchedTreesWire(map[string]entrypoint.TreeDelivery{
		"pi-ext": {Into: ".pi/agent/extensions/pi-ext", Owner: "pi",
			Reason: "pi-ext was not mounted: its build failed on /Users/_yolojail",
			Cause: &entrypoint.BuildCause{Lines: []string{"yolo internal darwin-bootstrap: cannot read the " +
				"sandbox account's home /Users/_yolojail"}}},
	})
}

// selfPlanWithEnv is a run-as-you launch plan of ws whose composed env is env.
func selfPlanWithEnv(ws string, env *jsonx.OrderedMap) RunPlan {
	return BuildRunPlanWithDaemons(ws, jsonx.NewOrderedMap(), nil, []string{"claude"},
		"/usr/local/bin/yolo", "", HomeOverlay{}, HostContext{}, env, nil, nil,
		JailDaemons{}, FloorStage{}, PlanSession{Mode: ModeSelf, HostHome: "/Users/you", HostUser: "you",
			DarwinUserTempDir: "/private/var/folders/ab/xyz/T/"})
}

// A BUILD FAILURE'S TEXT LEAVES THE LAUNCH VIABLE (MMP-D23): each wire rides the bootstrap's env -i
// list through the real relay (buildBootstrapEnv), its reason and cause name /Users/_yolojail, and
// the plan still passes, so the missing-programs hatch can start the sandbox. Red with the wires'
// diagnostic fields no longer exempted.
func TestSelfModeABuildFailureNamingTheAccountKeepsThePlanViable(t *testing.T) {
	for name, wire := range map[string][2]string{
		"a fork's build":         {entrypoint.ForkBuildsEnv, forkBuildFailureWire},
		"a patched tree's build": {entrypoint.PatchedTreesEnv, patchedTreeFailureWire()},
	} {
		env := jsonx.NewOrderedMap()
		env.Set(wire[0], wire[1])
		plan := selfPlanWithEnv("/Users/you/code/alpha", env)
		if !containsArg(plan.BootstrapArgv, wire[0]+"="+wire[1]) {
			t.Fatalf("%s: the fixture's decision does not reach the bootstrap argv, so this test proves "+
				"nothing: %v", name, plan.BootstrapArgv)
		}
		if problems := PlanInvariants(plan); len(problems) != 0 {
			t.Errorf("%s: a build failure's text made the run-as-you plan unviable:\n  %s", name,
				strings.Join(problems, "\n  "))
		}
	}
}

// EVERY OTHER FORM THAT NAMES THE ACCOUNT IS STILL REFUSED (MMP-D23), on a viable plan with one word
// added: the five forms the review found admitted by the first narrowing, a non-diagnostic field
// of each wire, a wire that does not decode, and the plain location forms. Red with the check
// narrowed to anything short of the whole argv.
func TestSelfModeEveryOtherWordNamingTheAccountIsRefused(t *testing.T) {
	plan := selfPlanWithEnv("/Users/you/code/alpha", jsonx.NewOrderedMap())
	if problems := PlanInvariants(plan); len(problems) != 0 {
		t.Fatalf("the base plan is not viable:\n  %s", strings.Join(problems, "\n  "))
	}
	for name, word := range map[string]string{
		"USER as the account":    "USER=_yolojail",
		"a launch body":          "cd '/Users/_yolojail' && exec claude",
		"an MCP command path":    `YOLO_MCP_SERVERS={"x":{"command":"/Users/_yolojail/bin/srv"}}`,
		"a cache alias":          `YOLO_DARWIN_CACHE_ALIASES={"/Users/you/code/alpha/.yolo/home/.cache/pip":"/Users/_yolojail/pip"}`,
		"a tilde home":           "HOME=~_yolojail",
		"a fork's key":           entrypoint.ForkBuildsEnv + `={"x":{"key":"/Users/_yolojail/store/x"}}`,
		"a tree's into":          entrypoint.PatchedTreesEnv + `={"x":{"into":"../../_yolojail/x"}}`,
		"an undecodable wire":    entrypoint.ForkBuildsEnv + `=not json: /Users/_yolojail`,
		"a path value":           "HOME=/Users/_yolojail",
		"a path list":            "PATH=/usr/bin:/Users/_yolojail/.local/bin",
		"a flag":                 "--home=/Users/_yolojail",
		"a program":              "/Users/_yolojail/bin/yolo",
		"the bare name":          SandboxUser,
		"the account as a value": "--user=_yolojail",
	} {
		p := plan
		p.BootstrapArgv = append(append([]string(nil), plan.BootstrapArgv[:2]...), append([]string{word},
			plan.BootstrapArgv[2:]...)...)
		if !hasProblem(PlanInvariants(p), "names the second account") {
			t.Errorf("%s naming the account (%s) passed the run-as-you check", name, word)
		}
	}
}

// A NAME THAT ONLY CONTAINS THE ACCOUNT'S IS NOT THE ACCOUNT: a workspace called my_yolojail_fork
// is viable, in every argv that names it. Red with the match back on a raw substring.
func TestSelfModeAWorkspaceNameContainingTheAccountsIsViable(t *testing.T) {
	for _, ws := range []string{"/Users/you/code/my_yolojail_fork", "/Users/you/code/_yolojail-fork",
		"/Users/you/code/x_yolojail"} {
		plan := selfPlanWithEnv(ws, jsonx.NewOrderedMap())
		if !strings.Contains(strings.Join(plan.BootstrapArgv, " "), ws) {
			t.Fatalf("the plan does not name the workspace %s, so this test proves nothing", ws)
		}
		if problems := PlanInvariants(plan); len(problems) != 0 {
			t.Errorf("workspace %s: refused for a name containing the account's:\n  %s", ws,
				strings.Join(problems, "\n  "))
		}
	}
	for _, s := range []string{"/Users/_yolojail", "USER=_yolojail", "'/Users/_yolojail/x'", "~_yolojail"} {
		if !namesSandboxAccount(s) {
			t.Errorf("%q names the account and was not matched", s)
		}
	}
}

// THE EXEMPT FIELDS ARE THE ENTRYPOINT TYPES' OWN SPELLINGS: renaming ForkDelivery's or
// TreeDelivery's reason or cause tag without this list would put the diagnostic back under the check
// (and the launch back under the refusal); a list naming a field the type does not have exempts
// nothing.
func TestDiagnosticWireFieldsMatchTheEntrypointTypes(t *testing.T) {
	for wire, typ := range map[string]reflect.Type{
		entrypoint.ForkBuildsEnv:   reflect.TypeOf(entrypoint.ForkDelivery{}),
		entrypoint.PatchedTreesEnv: reflect.TypeOf(entrypoint.TreeDelivery{}),
	} {
		tags := map[string]string{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tags[f.Name], _, _ = strings.Cut(f.Tag.Get("json"), ",")
		}
		want := []string{tags["Reason"], tags["Cause"]}
		if got := diagnosticWireFields[wire]; !slices.Equal(got, want) {
			t.Errorf("%s exempts %v; %s's diagnostic fields are tagged %v", wire, got, typ.Name(), want)
		}
	}
	if len(diagnosticWireFields) != 2 {
		t.Errorf("diagnosticWireFields names %d wires; each one added needs its type pinned here", len(diagnosticWireFields))
	}
}

// A RUN-AS-YOU CAPTURE'S BOOTSTRAP IS TOLD YOUR REAL HOME, AND ITS PROFILE LETS IT STAT IT
// (MMP-D22). Red with capture.go's self branch no longer setting YOLO_DARWIN_REAL_HOME, or
// SeatbeltSelfCaptureProfile no longer setting HostHomeStat: the bootstrap then takes the account
// mode's identity check and stats /Users/_yolojail, which the lab report's witness shows.
func TestSelfModeCaptureBootstrapIsToldYourRealHome(t *testing.T) {
	capture := BuildCapturePlan(testSelfCaptureOptions())
	build := BuildForkBuildPlan(testSelfForkBuildOptions())
	if problems := CapturePlanInvariants(capture); len(problems) != 0 {
		t.Errorf("capture: not viable:\n  %s", strings.Join(problems, "\n  "))
	}
	if problems := ForkBuildPlanInvariants(build); len(problems) != 0 {
		t.Errorf("fork build: not viable:\n  %s", strings.Join(problems, "\n  "))
	}
	for name, plan := range map[string]CapturePlan{"capture": capture, "fork build": build.CapturePlan} {
		if !containsArg(plan.BootstrapArgv, paths.DarwinRealHomeEnv+"=/Users/you") {
			t.Errorf("%s: the bootstrap is not told your real home: %v", name, plan.BootstrapArgv)
		}
		if !strings.Contains(plan.Seatbelt, HostHomeStatAllow("/Users/you")) {
			t.Errorf("%s: the profile does not let the bootstrap stat your real home", name)
		}
		// Neither the machine root nor the shared store rides with it: the profile denies both.
		for _, k := range []string{entrypoint.DarwinMachineHomeEnv, entrypoint.DarwinSharedHomeEnv} {
			for _, w := range plan.BootstrapArgv {
				if strings.HasPrefix(w, k+"=") {
					t.Errorf("%s: the capture bootstrap names %s", name, w)
				}
			}
		}
	}
	// The account mode's capture names no real home.
	for _, w := range BuildCapturePlan(testCaptureOptions()).BootstrapArgv {
		if strings.HasPrefix(w, paths.DarwinRealHomeEnv+"=") {
			t.Errorf("the account capture's bootstrap names a real home: %s", w)
		}
	}
}

// The capture plan check catches each half missing, on Linux, before a Mac sees it.
func TestSelfModeCaptureInvariantsRequireTheRealHome(t *testing.T) {
	base := BuildCapturePlan(testSelfCaptureOptions())
	noVar := base
	noVar.BootstrapArgv = nil
	for _, w := range base.BootstrapArgv {
		if !strings.HasPrefix(w, paths.DarwinRealHomeEnv+"=") {
			noVar.BootstrapArgv = append(noVar.BootstrapArgv, w)
		}
	}
	if !hasProblem(CapturePlanInvariants(noVar), "not told your real home") {
		t.Errorf("a capture bootstrap with no real home passed: %v", CapturePlanInvariants(noVar))
	}
	noStat := base
	noStat.Seatbelt = strings.Replace(base.Seatbelt, HostHomeStatAllow("/Users/you"), "", 1)
	if noStat.Seatbelt == base.Seatbelt {
		t.Fatal("the fixture's profile has no stat block to remove")
	}
	if !hasProblem(CapturePlanInvariants(noStat), "stat your real home") {
		t.Errorf("a capture profile with no stat of the real home passed: %v", CapturePlanInvariants(noStat))
	}
}

// The stat block is metadata only, on the home and its ancestors, and comes before every closing
// deny, so SBPL's last match keeps ~/.ssh and the store closed.
func TestHostHomeStatAllowIsMetadataOnly(t *testing.T) {
	got := HostHomeStatAllow("/Users/you/")
	want := "(allow file-read-metadata\n    (literal \"/Users/you\")\n    (literal \"/Users\"))\n"
	if !strings.Contains(got, want) {
		t.Errorf("the stat block is not metadata on the home and its parents:\n%s", got)
	}
	if strings.Contains(got, "subpath") || strings.Contains(got, "file-read*") {
		t.Errorf("the stat block grants more than metadata:\n%s", got)
	}
	if HostHomeStatAllow("") != "" || HostHomeStatAllow("relative") != "" {
		t.Error("a home that is not absolute rendered a rule")
	}
	p := SeatbeltSelfCaptureProfile(testStagingRoot, testSelfHomes(), "you", "", false)
	at := strings.Index(p, "#seatbelt-test-id:"+HostHomeStatID+"#")
	if at < 0 || at > strings.Index(p, "#seatbelt-test-id:host-secrets-deny#") {
		t.Errorf("the stat block is missing or follows the closing denies")
	}
}
