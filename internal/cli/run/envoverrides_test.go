package run

// envoverrides_test.go pins the ninth pre-flight at BOTH ends: what it says about a
// composed channel, and that every arm of the launch still asks it.
//
// The second half is the point. packload.EnvOverrideRefusal has its own unit tests, and
// none of them would notice if this package stopped calling it — the
// callee-pinned/call-site-unpinned shape AGENTS.md names, which this repo has shipped
// repeatedly. There are THREE call sites, one per way a launch delivers an environment,
// and each is pinned by a test named below:
//
//	the macos-user arm (run.go, Run)     TestEnvOverrideRefusesTheMacosUserLaunch
//	runContainer (run.go)                TestFreshLaunchChecksEnvOverridesFirst
//	deliverChannelOnAttach (run.go)      TestAttachRefusesAnOverriddenContribution
//
// Two are behavioral (the refusal is observable through Run and through attachExisting);
// the container one is a source pin, for the reason its sibling
// TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv gives — runContainer starts a
// real container, so a unit test has no other witness.
//
// The fixtures use the SHIPPED packs/aws-auth declaration, because that is the
// configuration OQ-SSO8 is about. That core names none of these variables is pinned in
// internal/packload (a made-up variable in a made-up pack) and by the absence of any AWS
// name in envoverrides.go.
//
// ⚠ AN OVERRIDE IN THESE FIXTURES ARRIVES THROUGH env_sources, NEVER THROUGH THE SHELL.
// The first cut of this file refused a bearer and a key pair exported only in the shell
// yolo was launched from, and pinned that as intended: no backend forwards that
// environment, so the jail would have held the pointer alone and the refusal was a false
// positive (jailOriginLookup). TestEnvOverrideIgnoresTheShellYoloWasLaunchedFrom is the
// regression test.
//
// ⚠ THE ~/.aws GRANT WARNS, IT DOES NOT REFUSE (2026-09-25): the shipped entry is
// `certain: false`, so checkEnvOverrides prints it and returns no refusal. The warning is
// printed INSIDE checkEnvOverrides, so the three call-site pins above cover it at every arm;
// TestEnvOverrideWarnsAboutARenderedHostFilesGrant is its behavioral pin. On macos-user the
// pointer is withheld, since nothing there serves it, so a ~/.aws grant beside it overrides
// nothing and warns about nothing (TestMacosUserWithholdsTheBedrockPointerSoNothingOverridesIt).

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const (
	bearerVar  = "AWS_BEARER_TOKEN_BEDROCK"
	pointerVar = "AWS_CONTAINER_CREDENTIALS_FULL_URI"
)

// shellWith is an invoking environment carrying exactly the given variables.
func shellWith(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// bearerInShell is an invoking environment carrying the bearer — which the jail never
// receives, because nothing forwards that environment (jailOriginLookup).
var bearerInShell = shellWith(map[string]string{bearerVar: "sk-bedrock-frozen"})

// awsAuthUserConfig is a user config selecting the shipped claude and aws-auth packs, with
// extra appended as further top-level members (each beginning with a comma). It gives the
// shipped `bedrock` provider a region (bedrockRegionMember): every launch built on it selects
// bedrock to pin something about the pointer, and without a region the region pre-flight would
// refuse it first (OQ-BR6, pinned on its own in regionpreflight_test.go).
func awsAuthUserConfig(extra string) string {
	return `{"packs": ["claude", "aws-auth"]` + bedrockRegionMember + extra + `}`
}

// testBedrockRegion is the region fixtures give the shipped `bedrock` provider when a launch
// selects it to pin something other than the region pre-flight.
const testBedrockRegion = "us-test-1"

// bedrockRegionMember is the user-config member giving the shipped bedrock provider
// testBedrockRegion, beginning with a comma like every extra member here.
const bedrockRegionMember = `, "providers": {"bedrock": {"region": "` + testBedrockRegion + `"}}`

// withBedrockRegion is bedrockRegionMember on an in-memory config.
func withBedrockRegion(cfg *jsonx.OrderedMap) {
	cfg.Set("providers", newConfig("bedrock", newConfig("region", testBedrockRegion)))
}

// inEnvSources is the extra member that delivers vars through one inline env_sources
// entry — the channel an override actually reaches a jail by. json.Marshal sorts the keys,
// so the config is the same bytes every run.
func inEnvSources(vars map[string]string) string {
	b, err := json.Marshal(vars)
	if err != nil {
		panic(err)
	}
	return `, "env_sources": [` + string(b) + `]`
}

// nativeLaunch is what overrideNativeLaunch saw of the dispatch: whether the handler ran,
// and what it was handed.
type nativeLaunch struct {
	home    string
	reached bool
	env     *jsonx.OrderedMap
	hostCtx macosuser.HostContext
	// jailDaemons is what the arm handed the guest's supervisor (OQ-DP8/DP9).
	jailDaemons macosuser.JailDaemons
}

// overrideNativeLaunch drives Run() to the macos-user arm over the given user config with
// `-p bedrock` — so aws-auth's pointer IS delivered — and the given invoking environment.
// The user config is written under a fresh HOME before Run reads it; a test that needs
// files under that HOME creates them through the returned home before calling Run.
func overrideNativeLaunch(t *testing.T, userConfig string, env func(string) string) (*Options, *bytes.Buffer, *nativeLaunch) {
	t.Helper()
	home := packHome(t)
	writeUserConfig(t, home, userConfig)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"claude"}
	o.ProfileName = "bedrock"
	o.Getenv = func(name string) string {
		if name == "YOLO_RUNTIME" {
			return "macos-user"
		}
		return env(name)
	}
	seen := &nativeLaunch{home: home}
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay, hostCtx macosuser.HostContext, _ bool,
		launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool, jd macosuser.JailDaemons) int {
		seen.reached = true
		seen.env = launchEnv
		seen.jailDaemons = jd
		seen.hostCtx = hostCtx
		return 0
	}
	return o, &stderr, seen
}

// writeWidgetLocalPack writes the conventional local pack (~/.config/yolo-jail/local, which
// config.LoadPacks appends to every selection) with an env contribution gated on `bedrock` and
// an `overridden_by` entry of its own — a pointer that points at no yolo daemon, so it is
// delivered on EVERY backend, macos-user included. It pins call site 1 with a pointer whose
// delivery does not depend on any jail daemon being served, so the test measures the
// pre-flight and nothing else.
func writeWidgetLocalPack(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "local", "contributes": [{"kind": "env", "profile": "bedrock",
	  "vars": {"WIDGET_POINTER": "https://widget.example/creds"},
	  "overridden_by": [{"vars": ["WIDGET_TOKEN"], "because": "the widget client reads WIDGET_TOKEN first"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEnvOverrideRefusesTheMacosUserLaunch is CALL SITE 1, asserted at the dispatch: the
// handler must never be reached. This backend is where the credential pre-flight was
// missing for a whole release (it lived only in runContainer, below the return), so a rule
// added to one arm and not the other is this file's own history. Pinned with a local pack's
// own override, which macos-user delivers (writeWidgetLocalPack).
func TestEnvOverrideRefusesTheMacosUserLaunch(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t,
		awsAuthUserConfig(inEnvSources(map[string]string{"WIDGET_TOKEN": "frozen"})),
		shellWith(nil))
	writeWidgetLocalPack(t, seen.home)
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1: a native launch carrying a pack's pointer and the "+
			"variable that overrides it must refuse\nstderr:\n%s", rc, stderr.String())
	}
	if seen.reached {
		t.Error("the refused launch still reached the macos-user handler — the pre-flight " +
			"must run BEFORE the backend is dispatched, not after the sandbox started")
	}
	got := stderr.String()
	for _, want := range []string{"WIDGET_TOKEN is delivered by " + packload.FromEnvSources,
		"WIDGET_POINTER", "pack local", "Drop one."} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q, or the reader cannot tell which to drop:\n%s",
				want, got)
		}
	}
}

// TestMacosUserWithholdsTheBedrockPointerSoNothingOverridesIt is aws-auth on macos-user with
// its loophole left at its default, OFF (docs/plans/notch-convergence.md §4 item 2): the adapter
// behind AWS_CONTAINER_CREDENTIALS_FULL_URI is then in no payload, so the pointer is a port
// nothing serves, although a macos-user launch opens the adapter as its own doorway, outside the
// sandbox, when the loophole is on (macosuserguestdaemons_test.go, HS-D15). The launch withholds
// the pointer and says so. With no pointer, a bearer, a static pair or a ~/.aws grant beside it
// overrides nothing, so none of them refuses or warns — before this, a macos-user launch refused
// a working bearer over a pointer that could never answer.
func TestMacosUserWithholdsTheBedrockPointerSoNothingOverridesIt(t *testing.T) {
	for _, tc := range []struct{ name, extra string }{
		{"a bearer", inEnvSources(map[string]string{bearerVar: "sk-bedrock-frozen"})},
		{"the static pair", inEnvSources(map[string]string{
			"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"})},
		{"a ~/.aws/config grant", `, "host_files": [{"path": "~/.aws/config", "content": "[default]\nregion = us-east-1\n"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, stderr, seen := overrideNativeLaunch(t, awsAuthUserConfig(tc.extra), shellWith(nil))
			if rc := Run(*o); rc != 0 || !seen.reached {
				t.Fatalf("Run() = %d (reached=%v), want the launch to proceed: nothing serves the "+
					"pointer here, so nothing can override it\nstderr:\n%s", rc, seen.reached, stderr.String())
			}
			if _, ok := seen.env.Get(pointerVar); ok {
				t.Errorf("macos-user delivered %s, a port no daemon of its binds", pointerVar)
			}
			got := stderr.String()
			for _, unwanted := range []string{"Refusing to launch", "MAY override it"} {
				if strings.Contains(got, unwanted) {
					t.Errorf("the launch printed %q over a pointer it withholds:\n%s", unwanted, got)
				}
			}
			for _, want := range []string{"Not set at this notch", pointerVar, `"aws-auth"`} {
				if !strings.Contains(got, want) {
					t.Errorf("the launch does not name the withheld pointer (%q):\n%s", want, got)
				}
			}
		})
	}
}

// TestEnvOverrideLetsTheNonOverridingShapesThrough is the control for the refusals, and it is
// not decoration: OQ-SSO8 forbids a false positive, and each case here is a jail the pointer
// still serves. A lone AWS_ACCESS_KEY_ID answers nothing in any SDK; the pair plus
// AWS_PROFILE, all three delivered, makes the JavaScript SDKs skip their environment provider.
func TestEnvOverrideLetsTheNonOverridingShapesThrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"nothing beside the pointer", nil},
		{"a lone access key id", map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE"}},
		{"the pair plus AWS_PROFILE", map[string]string{
			"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret",
			"AWS_PROFILE": "work"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := overrideOptions(t)
			o.Getenv = shellWith(nil)
			o.ProfileName = "bedrock"
			selected := awsAuthSelected(t)
			cfg := awsAuthServedConfig(t, selected)
			channel := channelFor(t, o, cfg, selected, userEnvWith(tc.env))
			if _, ok := channel.scope.DeliveredPackEnv(pointerVar); !ok {
				t.Fatal("the pointer was not delivered, so this test is not testing anything")
			}
			if lines := o.checkEnvOverrides(cfg, "podman", selected, channel, nil); len(lines) != 0 {
				t.Errorf("nothing here overrides the pointer, and the launch refused:\n%s",
					strings.Join(lines, "\n"))
			}
		})
	}
}

// TestEnvOverrideIgnoresTheShellYoloWasLaunchedFrom is the regression test for the first
// cut's false positive. A bearer or a static pair exported in the invoking shell — common
// for anyone who uses AWS — never reaches the jail: no backend forwards that environment, and
// the channel carries no such variable. So the jail holds the pointer alone, the SDK asks
// the service, and a refusal would have stopped a launch that works.
//
// The composed channel is asserted too, because it is the premise: if a shell variable ever
// DID start crossing, this test must fail rather than keep blessing a launch that no longer
// holds the pointer alone.
func TestEnvOverrideIgnoresTheShellYoloWasLaunchedFrom(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shell map[string]string
	}{
		{"a bearer", map[string]string{bearerVar: "sk-bedrock-frozen"}},
		{"the static pair", map[string]string{
			"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := overrideOptions(t)
			o.Getenv = shellWith(tc.shell)
			o.ProfileName = "bedrock"
			selected := awsAuthSelected(t)
			cfg := awsAuthServedConfig(t, selected)
			channel := channelFor(t, o, cfg, selected, emptyEnv())
			if lines := o.checkEnvOverrides(cfg, "podman", selected, channel, nil); len(lines) != 0 {
				t.Errorf("a variable only in the invoking shell is not delivered, so it overrides "+
					"nothing, and the launch refused:\n%s", strings.Join(lines, "\n"))
			}
			for name := range tc.shell {
				if _, ok := channel.scope.DeliveredPackEnv(name); ok {
					t.Errorf("%s from the invoking shell reached the channel — the premise of this "+
						"test is gone, and the refusal it removed is owed again", name)
				}
			}
			if _, ok := channel.scope.DeliveredPackEnv(pointerVar); !ok {
				t.Error("the pointer was not delivered, so this test is not testing anything")
			}
		})
	}
}

// TestEnvOverrideUnlessMustBeDeliveredToo is the other direction of the same narrowing, and
// OQ-SSO8's own stakes case: the maintainer's static pair through env_sources, and an
// AWS_PROFILE exported in the host shell for `aws sso login`. The jail gets the pair and no
// AWS_PROFILE, so the environment provider answers ahead of the pointer — a shell-only
// AWS_PROFILE must not make the pair entry step aside.
func TestEnvOverrideUnlessMustBeDeliveredToo(t *testing.T) {
	o := overrideOptions(t)
	o.Getenv = shellWith(map[string]string{"AWS_PROFILE": "work"})
	o.ProfileName = "bedrock"
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	lines := o.checkEnvOverrides(cfg, "podman", selected, channelFor(t, o, cfg, selected,
		userEnvWith(map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"})), nil)
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "Remove any one of") {
		t.Errorf("AWS_PROFILE in the shell is not delivered, so the delivered pair still beats "+
			"the pointer, and the pair refusal must offer either half:\n%s", got)
	}
}

// TestEnvOverrideRefusesTheStaticPair: the pair without AWS_PROFILE is the maintainer's own
// configuration the day he turns SSO on (OQ-SSO8's stakes), so it is pinned beside the
// evaluator's own tests on a launch that serves the pointer.
func TestEnvOverrideRefusesTheStaticPair(t *testing.T) {
	o := overrideOptions(t)
	o.Getenv = shellWith(nil)
	o.ProfileName = "bedrock"
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	got := strings.Join(o.checkEnvOverrides(cfg, "podman", selected, channelFor(t, o, cfg, selected,
		userEnvWith(map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"})), nil), "\n")
	for _, want := range []string{"AWS_ACCESS_KEY_ID is delivered by " + packload.FromEnvSources,
		"AWS_SECRET_ACCESS_KEY is delivered by " + packload.FromEnvSources, "Remove any one of"} {
		if !strings.Contains(got, want) {
			t.Errorf("the pair refusal must say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "AKIAEXAMPLE") || strings.Contains(got, "secret\n") {
		t.Errorf("the refusal printed a credential VALUE:\n%s", got)
	}
}

// TestFreshLaunchChecksEnvOverridesFirst is CALL SITE 2. Its sibling
// TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv states why this shape is a
// source pin rather than a behavioral one, and the orderings asserted here are the
// design's, not this test's:
//
//   - AFTER assembleRunCmd, because the `-e K=V` argv is half of what "delivered" means
//     on this backend (a pack-shipped loophole's jail_env reaches the jail only there);
//   - BEFORE checkProviderCredentials, because an overridden channel is a WRONG answer and
//     a missing credential is a MISSING one, and the wrong answer has no hatch to weigh;
//   - BEFORE startLoopholesDisclosed, so a refusal never has to unwind a host daemon it
//     started.
//
// A call's POSITION is not enough, which the first cut of this test proved by passing two
// mutations: deleting `lock.Close(); return 1` from the refusing branch (the launch prints
// the refusal and starts the jail anyway — "fatal, no hatch" gone), and passing nil for
// the argv (the jail_env channel unchecked). So the call's arguments and its branch are
// pinned too — see refusingBranch.
func TestFreshLaunchChecksEnvOverridesFirst(t *testing.T) {
	const (
		assemble  = "assembleRunCmd"
		overrides = "checkEnvOverrides"
		creds     = "checkProviderCredentials"
		daemons   = "startLoopholesDisclosed"
	)
	fn := methodDecl(t, "run.go", "runContainer")

	pos := map[string]token.Pos{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, seen := pos[sel.Sel.Name]; !seen {
			pos[sel.Sel.Name] = call.Pos()
		}
		return true
	})

	if _, ok := pos[overrides]; !ok {
		t.Fatalf("runContainer no longer calls %s. packload's unit tests still pass and so do "+
			"this file's other two call-site pins, so a container launch carrying a pack's "+
			"variable beside what the pack declares overrides it would start a jail that "+
			"silently uses the other one — the wrong answer OQ-SSO8 made fatal. If the seam "+
			"moved, move this assertion with it rather than deleting it.", overrides)
	}
	if pos[overrides] < pos[assemble] {
		t.Errorf("runContainer calls %s BEFORE %s: the check answers against the assembled -e "+
			"argv, so it cannot run before that argv exists", overrides, assemble)
	}
	if _, ok := pos[creds]; ok && pos[overrides] > pos[creds] {
		t.Errorf("runContainer calls %s AFTER %s: an overridden channel is a wrong answer and "+
			"a missing credential is a missing one, so the wrong answer is refused first",
			overrides, creds)
	}
	if _, ok := pos[daemons]; ok && pos[overrides] > pos[daemons] {
		t.Errorf("runContainer calls %s AFTER %s: a refusal must land before any host-side "+
			"daemon a refusal would have to clean up", overrides, daemons)
	}

	call, body := refusingBranch(t, fn, overrides)
	// The argument list is (cfg, rt, packs, channel, argvPairs).
	if len(call.Args) != 5 {
		t.Fatalf("%s takes %d arguments here, want 5 (cfg, rt, packs, channel, argvPairs) — "+
			"if the signature moved, move these assertions with it", overrides, len(call.Args))
	}
	if id, ok := call.Args[1].(*ast.Ident); !ok || id.Name != "rt" {
		t.Errorf("runContainer passes %s something other than its own `rt` as the backend: a "+
			"fixed runtime would count a directory host_files grant the launch never binds",
			overrides)
	}
	argv, ok := call.Args[4].(*ast.CallExpr)
	if !ok || !isIdentNamed(argv.Fun, "envPairs") || len(argv.Args) != 1 ||
		!isIdentNamed(argv.Args[0], "runCmd") {
		t.Errorf("runContainer's %s is not handed envPairs(runCmd): the assembled argv is the "+
			"only channel a pack-shipped loophole's jail_env reaches the jail by, so without it "+
			"that override goes unchecked on this backend", overrides)
	}
	if !endsInReturnOne(body) {
		t.Errorf("runContainer's %s branch does not end in `return 1`: the refusal would print "+
			"and the jail start anyway, which is the hatch OQ-SSO8 ruled out", overrides)
	}
	if !callsRecvMethod(body, "lock", "Close") {
		t.Errorf("runContainer's %s branch returns without lock.Close(): the workspace lock is "+
			"held at this point, and a refused launch must release it", overrides)
	}
}

// TestEveryOverrideCallSitePassesItsOwnRuntime: the backend decides whether a directory
// host_files grant renders at all (hostFileDirsDeliver), so each of the three call sites must
// hand over the runtime it is actually launching on. A literal there would count a `~/.aws/`
// grant on a backend that never binds it — the false positive this argument was added to
// remove. The macos-user arm is also pinned behaviorally
// (TestEnvOverrideLetsAMacosUserDirectoryGrantThrough); the attach arm has no other witness
// for an Apple Container jail.
func TestEveryOverrideCallSitePassesItsOwnRuntime(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "checkEnvOverrides" {
			return true
		}
		calls++
		if len(c.Args) < 2 || !isIdentNamed(c.Args[1], "rt") {
			t.Errorf("a checkEnvOverrides call in run.go does not pass its own `rt` as the backend")
		}
		return true
	})
	if calls != 3 {
		t.Errorf("run.go calls checkEnvOverrides %d times, want 3 (the macos-user arm, "+
			"runContainer, deliverChannelOnAttach) — if an arm moved, move this count with it", calls)
	}
}

// refusingBranch finds, in fn, the `if lines := o.<method>(…); len(lines) > 0 { … }` that
// calls method, and returns the call and the branch body.
func refusingBranch(t *testing.T, fn *ast.FuncDecl, method string) (*ast.CallExpr, *ast.BlockStmt) {
	t.Helper()
	var call *ast.CallExpr
	var body *ast.BlockStmt
	ast.Inspect(fn, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || call != nil {
			return call == nil
		}
		assign, ok := ifs.Init.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		c, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
			call, body = c, ifs.Body
		}
		return true
	})
	if call == nil {
		t.Fatalf("no `if lines := o.%s(…); … { … }` branch found — the refusal's shape changed; "+
			"move these assertions with it rather than deleting them", method)
	}
	return call, body
}

func isIdentNamed(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// endsInReturnOne reports whether body's last statement is `return 1`.
func endsInReturnOne(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	ret, ok := body.List[len(body.List)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	lit, ok := ret.Results[0].(*ast.BasicLit)
	return ok && lit.Value == "1"
}

// callsRecvMethod reports whether body calls recv.method() anywhere.
func callsRecvMethod(body *ast.BlockStmt, recv, method string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method && isIdentNamed(sel.X, recv) {
			found = true
		}
		return true
	})
	return found
}

// userEnvWith is a hydrated env_sources map carrying exactly vars, in sorted key order.
func userEnvWith(vars map[string]string) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m.Set(k, vars[k])
	}
	return m
}

// TestAttachRefusesAnOverriddenContribution is CALL SITE 3, driven through attachExisting
// — the arm that delivers an environment into a jail that is ALREADY RUNNING. It refuses
// there for the same reason the fresh path does: the channel file this arm writes is what
// the next agent process reads, so an attach that delivered both would hand a live jail the
// silent wrong answer without any launch banner in sight.
//
// Which is also why the refusal must come BEFORE that write, and the test asserts the file
// is byte-for-byte what the previous entry left: the first cut refused after writing, so a
// refused attach had already put the bearer beside the pointer in the live jail.
func TestAttachRefusesAnOverriddenContribution(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")}
	o, cfg, channel, stderr := attachFixture(t, awsAdapterJailEnv,
		packs, userEnvWith(map[string]string{bearerVar: "sk-bedrock-frozen"}),
		func(o *Options, cfg *jsonx.OrderedMap) {
			o.Getenv = shellWith(nil)
			o.ProfileName = "bedrock"
			// A jail that runs aws-auth's adapter, so its pointer is delivered at all.
			served := awsAuthServedConfig(t, []*packload.Pack{officialPack(t, "claude"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")})
			v, _ := served.Get("loopholes")
			cfg.Set("loopholes", v)
		})
	envFile, before := seedLiveChannelFile(t, o)

	rc, _ := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false, nil)
	if rc != 1 {
		t.Fatalf("an attach delivering aws-auth's pointer beside a bearer must refuse, rc=%d\n"+
			"stderr:\n%s", rc, stderr.String())
	}
	got := stderr.String()
	for _, want := range []string{bearerVar + " is delivered by " + packload.FromEnvSources, pointerVar} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}
	assertLiveChannelFileUnchanged(t, envFile, before)
}

// TestAttachCredentialRefusalLeavesTheLiveFileAlone: the credential pre-flight moved ahead of
// the write with the override check, for the same reason — refusing after the write handed
// the running jail a provider with no token while telling this entry "no".
func TestAttachCredentialRefusalLeavesTheLiveFileAlone(t *testing.T) {
	packs := zaiSelected(t)
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv,
		packs, emptyEnv(), func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })
	envFile, before := seedLiveChannelFile(t, o)

	rc, _ := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false, nil)
	if rc != 1 {
		t.Fatalf("an attach that cannot hydrate the selected provider's key must refuse, rc=%d\n%s",
			rc, stderr.String())
	}
	assertLiveChannelFileUnchanged(t, envFile, before)
}

// seedLiveChannelFile writes a previous entry's yolo-user-env.sh, as a running jail has one,
// and returns its path and bytes.
func seedLiveChannelFile(t *testing.T, o *Options) (string, []byte) {
	t.Helper()
	envFile := filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte("# the previous entry's channel\nexport PREVIOUS_ENTRY='1'\n")
	if err := os.WriteFile(envFile, before, 0o600); err != nil {
		t.Fatal(err)
	}
	// And the previous entry's per-agent file, live-bound beside it: a refused attach must
	// leave it alone too, or it could revoke an agent's credential, or switch claude's shape
	// vars to a provider with no token, in the running jail it refused.
	agentFile := filepath.Join(filepath.Dir(envFile), agentEnvStateDir, "claude.sh")
	if err := os.MkdirAll(filepath.Dir(agentFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentFile, previousAgentFile, 0o600); err != nil {
		t.Fatal(err)
	}
	return envFile, before
}

// previousAgentFile is the per-agent file seedLiveChannelFile leaves as the previous entry's.
var previousAgentFile = []byte("# the previous entry's claude file\nexport PREVIOUS_AGENT_ENTRY='1'\n")

func assertLiveChannelFileUnchanged(t *testing.T, envFile string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("reading the live channel file: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("a refused attach rewrote the RUNNING jail's channel file — every new process "+
			"in it now sources the refused channel:\n%s", after)
	}
	dir := filepath.Join(filepath.Dir(envFile), agentEnvStateDir)
	entries, _ := os.ReadDir(dir)
	agentAfter, err := os.ReadFile(filepath.Join(dir, "claude.sh"))
	if err != nil || !bytes.Equal(agentAfter, previousAgentFile) || len(entries) != 1 {
		t.Errorf("a refused attach rewrote the RUNNING jail's per-agent env files (%d now, "+
			"err %v) — claude's launcher now sources the refused channel:\n%s", len(entries), err, agentAfter)
	}
}

// --- what the check says about a composed channel (the input assembly) ---

// overrideOptions is retireOptions with HOME isolated first. The pre-flight reads the USER
// config (config.RenderedHostFilePaths, through LoadHostFiles), and in this jail the
// inherited HOME is the live session's — a test must not depend on, or read, that file.
func overrideOptions(t *testing.T) *Options {
	t.Helper()
	packHome(t)
	return retireOptions(t, discardBuf())
}

// awsAuthSelected is the realistic selected set: an agent pack plus aws-auth, which
// installs no CLI and whose pointer is gated on claude's `bedrock` profile.
func awsAuthSelected(t *testing.T) []*packload.Pack {
	return []*packload.Pack{officialPack(t, "claude"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")}
}

// awsAuthServedConfig is the config of a container launch that RUNS aws-auth's adapter: the
// loophole enabled, as a Bedrock user enables it (packs/aws-auth/README.md), and the staged
// packs' loophole modules recorded, as staging records them. Without both the adapter is not
// in the launch's payload, nothing serves the pointer, and the credential gate withholds it
// (docs/plans/notch-convergence.md §4 item 2) — so there would be nothing to override.
func awsAuthServedConfig(t *testing.T, packs []*packload.Pack) *jsonx.OrderedMap {
	t.Helper()
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules(packs))
	return newConfig("loopholes", newConfig("aws-auth", newConfig("enabled", true)))
}

// TestEnvOverrideNeedsTheGatingProfile: selecting aws-auth WITHOUT the profile that gates
// its pointer delivers no pointer, so a bearer beside it overrides nothing and the launch
// proceeds — the negative that makes selecting the pack harmless (design §12 step 4).
func TestEnvOverrideNeedsTheGatingProfile(t *testing.T) {
	o := overrideOptions(t)
	o.Getenv = shellWith(nil)
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	bearer := userEnvWith(map[string]string{bearerVar: "sk-bedrock-frozen"})
	if lines := o.checkEnvOverrides(cfg, "podman", selected, channelFor(t, o, cfg, selected, bearer), nil); len(lines) != 0 {
		t.Errorf("aws-auth selected without `bedrock` refused a launch that delivers no "+
			"pointer:\n%s", strings.Join(lines, "\n"))
	}
}

// TestEnvOverrideNamesThePackContributionAndTheSecretChannel: the realistic conflict — an
// existing Bedrock jail whose owner adds aws-auth — with the bearer from env_sources. The
// refusal names the pack by name (the declaration is per pack, so for the first time the
// refusal CAN), the secret channel as the bearer's origin, and the gating profile.
func TestEnvOverrideNamesThePackContributionAndTheSecretChannel(t *testing.T) {
	o := overrideOptions(t)
	o.Getenv = shellWith(nil)
	o.ProfileName = "bedrock"
	userEnv := jsonx.NewOrderedMap()
	userEnv.Set(bearerVar, "sk-bedrock-frozen")
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	lines := o.checkEnvOverrides(cfg, "podman", selected, channelFor(t, o, cfg, selected, userEnv), nil)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"pack aws-auth",
		pointerVar,
		bearerVar + " is delivered by " + packload.FromEnvSources,
		// The gate the pointer rides, in the refusal's remedy: the provider's platform since
		// OQ-BR8, not a profile name.
		"selected provider's platform is `aws-bedrock`",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "sk-bedrock-frozen") {
		t.Errorf("the refusal printed the bearer's VALUE:\n%s", joined)
	}
}

// TestEnvOverrideReadsTheAssembledArgv: the container arm's fifth source. A pack-shipped
// loophole's jail_env reaches the jail on the `-e K=V` argv and nowhere else, so a check
// that read only the composed channel would be blind to an override delivered that way.
func TestEnvOverrideReadsTheAssembledArgv(t *testing.T) {
	o := overrideOptions(t)
	o.Getenv = shellWith(nil)
	o.ProfileName = "bedrock"
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	channel := channelFor(t, o, cfg, selected, emptyEnv())

	if lines := o.checkEnvOverrides(cfg, "podman", selected, channel, nil); len(lines) != 0 {
		t.Fatalf("the pointer alone must not refuse:\n%s", strings.Join(lines, "\n"))
	}
	argv := map[string]string{bearerVar: "sk-bedrock-frozen"}
	lines := o.checkEnvOverrides(cfg, "podman", selected, channel, argv)
	if !strings.Contains(strings.Join(lines, "\n"), packload.FromContainerArgv) {
		t.Errorf("a bearer that reaches the jail only through the assembled argv was not seen "+
			"(or not attributed to it):\n%s", strings.Join(lines, "\n"))
	}
}

// TestEnvOverrideWarnsAboutARenderedHostFilesGrant is the `~/.aws` half, moved here from
// config.ValidateConfig: a source-less host_files entry under ~/.aws renders a file the SDK's
// shared-config provider reads ahead of the pointer. Source-less on purpose: what decides
// whether the SDK answers is what it finds under $HOME in the jail, and an inline seed is
// exactly as overriding as a mount.
//
// ⚠ IT WARNS AND DOES NOT REFUSE, since the 2026-09-25 ruling under OQ-SSO8. Whether such a
// grant overrides depends on what the file holds for the profile the SDK resolves — a region-
// only config does not, and the JavaScript SDKs' fromIni then throws with tryNextLink and the
// chain reaches the container provider — and the declaration cannot see which. So packs/aws-auth
// declares the entry `certain: false`, and both fixtures below launch with the warning: the
// one holding a key pair (which does override) and the region-only one (which does not). The
// returned refusal is empty for both, and the WARNING is on stderr.
func TestEnvOverrideWarnsAboutARenderedHostFilesGrant(t *testing.T) {
	for _, tc := range []struct{ name, path, content string }{
		{"a credentials file with a key pair", "~/.aws/credentials",
			"[default]\naws_access_key_id = AKIAEXAMPLE\naws_secret_access_key = example\n"},
		{"a region-only config", "~/.aws/config", "[default]\nregion = us-east-1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := overrideOptions(t)
			var stderr bytes.Buffer
			o.Stderr = &stderr
			o.Getenv = shellWith(nil)
			o.ProfileName = "bedrock"
			selected := awsAuthSelected(t)
			cfg := awsAuthServedConfig(t, selected)
			cfg.Set("host_files", []any{newConfig("path", tc.path, "content", tc.content)})
			lines := o.checkEnvOverrides(cfg, "podman", selected,
				channelFor(t, o, cfg, selected, emptyEnv()), nil)
			if len(lines) != 0 {
				t.Errorf("a ~/.aws grant REFUSED the launch — it may be harmless, so it must "+
					"only warn:\n%s", strings.Join(lines, "\n"))
			}
			dest := strings.TrimPrefix(tc.path, "~/")
			got := stderr.String()
			for _, want := range []string{
				"Warning: pack aws-auth's",
				"MAY override it",
				"The launch continues.",
				"A host_files entry renders ~/" + dest + " into the jail.",
				"If it does, drop one. Remove the host_files entry for ~/" + dest,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the grant warning does not say %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "Refusing to launch") {
				t.Errorf("the warning is worded as a refusal:\n%s", got)
			}
			if strings.Contains(got, "AKIAEXAMPLE") {
				t.Errorf("the warning printed the grant's content:\n%s", got)
			}
		})
	}
}

// TestEnvOverrideWarningDoesNotHideARefusal: a jail carrying a certain override AND an
// uncertain one is refused for the first and warned about the second — the warning must not
// replace the refusal, and the refusal must not swallow the warning.
func TestEnvOverrideWarningDoesNotHideARefusal(t *testing.T) {
	o := overrideOptions(t)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.Getenv = shellWith(nil)
	o.ProfileName = "bedrock"
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	cfg.Set("host_files", []any{newConfig("path", "~/.aws/config", "content", "[default]\n")})
	lines := o.checkEnvOverrides(cfg, "podman", selected, channelFor(t, o, cfg, selected,
		userEnvWith(map[string]string{bearerVar: "sk-bedrock-frozen"})), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, bearerVar+" is delivered by "+packload.FromEnvSources) {
		t.Errorf("the bearer's refusal is missing:\n%s", joined)
	}
	if strings.Contains(joined, "~/.aws") {
		t.Errorf("the uncertain grant leaked into the refusal:\n%s", joined)
	}
	if !strings.Contains(stderr.String(), "A host_files entry renders ~/.aws/config into the jail.") {
		t.Errorf("the grant warning was not printed beside the refusal:\n%s", stderr.String())
	}
}

// TestEnvOverrideCountsADirectoryGrantOnlyWhereItIsBound: the canonical `~/.aws/` grant is a
// DIRECTORY entry, and one backend never delivers one — Apple Container below the
// read-only-bind floor declines the bind. There a finding over it is a false positive: the jail
// gets no ~/.aws and the pointer serves. macos-user used to be the second, and copies the tree
// now (buildMacosCtxTree), so a ~/.aws/ grant reaches that sandbox and is counted there. The
// shipped entry is uncertain, so the finding is a WARNING on stderr and never a refusal; the
// backend rule decides whether it is printed at all.
func TestEnvOverrideCountsADirectoryGrantOnlyWhereItIsBound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rt      string
		ac      *acVersionProbe
		counted bool // the warning is printed
	}{
		{"podman binds it", "podman", nil, true},
		{"macos-user copies the tree", "macos-user", nil, true},
		{"Apple Container below the floor declines it", "container",
			&acVersionProbe{v: "1.0.0", ok: true}, false},
		{"Apple Container of unknown version declines it", "container",
			&acVersionProbe{}, false},
		{"Apple Container at the floor binds it", "container",
			&acVersionProbe{v: acROBindsFloor, ok: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserConfig(t, home, `{"host_files": ["~/.aws/"]}`)
			if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o755); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			o := retireOptions(t, &stderr)
			o.Getenv = shellWith(nil)
			o.ProfileName = "bedrock"
			o.acVersion = tc.ac
			selected := awsAuthSelected(t)
			cfg := awsAuthServedConfig(t, selected)
			lines := o.checkEnvOverrides(cfg, tc.rt, selected,
				channelFor(t, o, cfg, selected, emptyEnv()), nil)
			if len(lines) != 0 {
				t.Errorf("rt=%s: a directory grant refused the launch — the shipped entry is "+
					"uncertain and may only warn:\n%s", tc.rt, strings.Join(lines, "\n"))
			}
			if got := strings.Contains(stderr.String(), "renders ~/.aws into the jail"); got != tc.counted {
				t.Errorf("rt=%s: warned=%v, want %v:\n%s", tc.rt, got, tc.counted, stderr.String())
			}
		})
	}
}

// TestEnvOverrideLetsAMacosUserDirectoryGrantThrough is the same fact through Run, on the
// backend of the internal rollout: `~/.aws/` CROSSES there now, by copy, so with `-p bedrock`
// and aws-auth's loophole on (its pointer is served then, through a launch-owned doorway) the
// launch says the grant may override that pointer — the shipped entry is uncertain, so it WARNS
// — and still launches rather than being refused over it. It used to assert the opposite (no
// warning, the grant absent from the sandbox's host files), when the backend dropped a directory
// entry; the premise that test guarded is gone, and this is the warning it said would be owed.
func TestEnvOverrideLetsAMacosUserDirectoryGrantThrough(t *testing.T) {
	installFakeAWSCLI(t)
	o, stderr, seen := overrideNativeLaunch(t, awsAuthUserConfig(`, "host_files": ["~/.aws/"], `+
		`"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "yolo-unit", "unnarrowed": true}}}`), shellWith(nil))
	observeDoorways(t)
	if err := os.MkdirAll(filepath.Join(seen.home, ".aws"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rc := Run(*o); rc != 0 || !seen.reached {
		t.Fatalf("Run() = %d (reached=%v), want the launch to proceed: an uncertain override "+
			"warns and never refuses\nstderr:\n%s", rc, seen.reached, stderr.String())
	}
	if !strings.Contains(stderr.String(), "renders ~/.aws into the jail") {
		t.Errorf("the launch did not warn that the ~/.aws grant it delivers may override the "+
			"pointer:\n%s", stderr.String())
	}
	crossed := false
	for _, e := range seen.hostCtx.HostFiles {
		crossed = crossed || (e.Path == ".aws" && e.IsDir)
	}
	if !crossed {
		t.Errorf("the directory grant did not reach the sandbox's host files (%+v), so the "+
			"warning above names a delivery that did not happen", seen.hostCtx.HostFiles)
	}
}

// TestEnvOverrideIsSilentWithoutAComposedChannel: every arm calls this with whatever it
// has, and the macos-user arm can reach the call with a nil channel on a launch that
// composed nothing. Silence, not a panic.
func TestEnvOverrideIsSilentWithoutAComposedChannel(t *testing.T) {
	o := overrideOptions(t)
	o.Getenv = bearerInShell
	if lines := o.checkEnvOverrides(newConfig(), "podman", awsAuthSelected(t), nil, nil); lines != nil {
		t.Errorf("a nil channel produced a refusal: %v", lines)
	}
}

// TestEnvOverrideTreatsAnEmptyValueAsUndelivered mirrors the credential pre-flight's own
// rule: the launch drops an empty value rather than composing an empty token, so an empty
// bearer is not an override — there would be nothing for a client to send.
func TestEnvOverrideTreatsAnEmptyValueAsUndelivered(t *testing.T) {
	o := overrideOptions(t)
	o.Getenv = shellWith(nil)
	o.ProfileName = "bedrock"
	selected := awsAuthSelected(t)
	cfg := awsAuthServedConfig(t, selected)
	channel := channelFor(t, o, cfg, selected, emptyEnv())
	if lines := o.checkEnvOverrides(cfg, "podman", selected, channel, map[string]string{bearerVar: ""}); len(lines) != 0 {
		t.Errorf("an empty bearer counted as delivered:\n%s", strings.Join(lines, "\n"))
	}
}
