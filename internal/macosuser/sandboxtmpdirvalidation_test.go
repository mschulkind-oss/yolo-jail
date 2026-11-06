package macosuser

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestSandboxTmpdirValidationSharesTheDirectRunPlanPolicy(t *testing.T) {
	ctx := SandboxTmpdirValidationContext{
		Mode: ModeSelf, Workspace: "/Users/you/code/alpha", Home: "/Users/you/code/alpha/.yolo/home",
		DarwinUserTempDir: "/var/folders/ab/xyz/T",
	}
	for _, tc := range []struct {
		name     string
		variable string
		value    string
		shared   bool
		wantBad  bool
	}{
		{"workspace temp", "CLAUDE_CODE_TMPDIR", "/Users/you/code/alpha/.claude-tmp", false, false},
		{"home temp", "CLAUDE_CODE_TMPDIR", "/Users/you/code/alpha/.yolo/home/claude-tmp", false, false},
		{"user temp", "TMPDIR", "/var/folders/ab/xyz/T/yolo-1234", false, false},
		{"private tmp closed", "CLAUDE_CODE_TMPDIR", "/private/tmp/claude", false, true},
		{"private tmp with hatch", "CLAUDE_CODE_TMPDIR", "/private/tmp/claude", true, false},
		{"outside every root", "CLAUDE_CODE_TMPDIR", "/Users/you/tmp", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx.SharedTmp = tc.shared
			got := SandboxTmpdirValidationProblem(ctx, tc.variable, tc.value)
			if (got != "") != tc.wantBad {
				t.Fatalf("SandboxTmpdirValidationProblem(%s=%q) = %q, want bad=%v",
					tc.variable, tc.value, got, tc.wantBad)
			}
			want := composedTmpdirEnvProblem(tc.variable, tc.value,
				[]string{ctx.Workspace, ctx.Home, ctx.DarwinUserTempDir}, ctx.SharedTmp)
			if got != want {
				t.Errorf("nested validation drifted from direct run-plan policy: got %q, direct %q", got, want)
			}
		})
	}

	account := ctx
	account.Mode = ModeAccount
	if got := SandboxTmpdirValidationProblem(account, "CLAUDE_CODE_TMPDIR", "/Users/you/tmp"); got != "" {
		t.Errorf("account mode acquired a new temp refusal absent from direct run-plan semantics: %q", got)
	}
	accountTMPDIR := "/account/sandbox/tmp"
	if got := SandboxTmpdirValidationProblem(account, "TMPDIR", accountTMPDIR); got != "" {
		t.Errorf("account mode must preserve its inherited TMPDIR: %q", got)
	}
	if got := SandboxTmpdirValidationProblem(account, "CLAUDE_CODE_TMPDIR", "/outside/claude"); got != "" {
		t.Errorf("account mode must not validate program-specific temp overrides: %q", got)
	}
	if err := ValidateSandboxTmpdirEnv(account, []string{"CLAUDE_CODE_TMPDIR"}, func(k string) string {
		if k == "CLAUDE_CODE_TMPDIR" {
			return "/outside/claude"
		}
		return accountTMPDIR
	}); err != nil {
		t.Errorf("account mode added temp validation absent from direct run-plan semantics: %v", err)
	}
}

func TestSandboxTmpdirValidationFailsClosedForMissingOrMalformedMetadata(t *testing.T) {
	for _, raw := range []string{"", "not-json", `{"mode":99,"workspace":"/w","home":"/h"}`, `{"mode":1,"workspace":"relative","home":"/h"}`} {
		if _, err := ParseSandboxTmpdirValidationContext(raw); err == nil {
			t.Errorf("context %q was accepted", raw)
		}
	}
	ctx := SandboxTmpdirValidationContext{Mode: ModeSelf, Workspace: "/workspace", Home: "/home"}
	if err := ValidateSandboxTmpdirEnv(ctx, []string{"CLAUDE_CODE_TMPDIR"}, func(name string) string {
		if name == "TMPDIR" {
			return "/outside/tmp"
		}
		return ""
	}); err == nil {
		t.Fatal("unsafe final TMPDIR was not refused")
	}
	if err := ValidateSandboxTmpdirEnv(ctx, []string{"CLAUDE_CODE_TMPDIR"}, func(name string) string {
		if name == "CLAUDE_CODE_TMPDIR" {
			return "/outside/claude"
		}
		return "/workspace/tmp"
	}); err == nil {
		t.Fatal("unsafe explicit program temp override was not refused")
	}
}

func TestAccountRunPlanDoesNotInventUserTempValidationRoots(t *testing.T) {
	const workspace = "/Users/Shared/yolo/proj"
	plan := BuildRunPlanWithDaemons(workspace, jsonx.NewOrderedMap(), nil, []string{"bash"},
		"/usr/local/bin/yolo", stagedShippedClaudePack(t), HomeOverlay{}, HostContext{},
		jsonx.NewOrderedMap(), nil, nil, JailDaemons{}, FloorStage{}, PlanSession{
			ID: testSessionID, Mode: ModeAccount, HostHome: "/Users/Shared", HostUser: "you",
			DarwinUserTempDir: "/var/folders/ab/xyz/T", SharedTmp: true,
		})
	if plan.TmpdirProblem != "" {
		t.Fatalf("account-mode plan gained a temp refusal: %s", plan.TmpdirProblem)
	}
	value, ok := argvEnvValue(plan.BootstrapArgv, paths.MacosUserTmpdirContextEnv)
	if !ok {
		t.Fatal("the selected account-mode program launcher has no resolved context")
	}
	ctx, err := ParseSandboxTmpdirValidationContext(value)
	if err != nil {
		t.Fatalf("parsing the account-mode context: %v", err)
	}
	if ctx.Mode != ModeAccount || ctx.DarwinUserTempDir != "" || ctx.SharedTmp {
		t.Errorf("account context invented user-temp authority: %+v", ctx)
	}
}

func TestRunPlanCarriesOnlyResolvedTempValidationContextToNativeLaunchers(t *testing.T) {
	const workspace = "/Users/Shared/yolo/proj"
	root := stagedShippedClaudePack(t)
	spoofed := jsonx.NewOrderedMap()
	spoofed.Set(paths.MacosUserTmpdirContextEnv,
		`{"mode":1,"workspace":"/user-selected/root","home":"/user-selected/home"}`)
	plan := BuildRunPlanWithDaemons(workspace, jsonx.NewOrderedMap(), nil, []string{"bash"},
		"/usr/local/bin/yolo", root, HomeOverlay{}, HostContext{}, spoofed, nil, nil,
		JailDaemons{}, FloorStage{}, PlanSession{
			ID: testSessionID, Mode: ModeSelf, HostHome: "/Users/Shared", HostUser: "you",
			DarwinUserTempDir: "/var/folders/ab/xyz/T", SharedTmp: true,
		})
	if plan.TmpdirProblem != "" {
		t.Fatalf("resolved native plan has a temp problem: %s", plan.TmpdirProblem)
	}
	value, ok := sandboxEnvFileValue(plan.EnvFileContent, paths.MacosUserTmpdirContextEnv)
	if !ok {
		t.Fatal("the native session env file has no temp-validation context")
	}
	ctx, err := ParseSandboxTmpdirValidationContext(value)
	if err != nil {
		t.Fatalf("parsing the native env-file context: %v", err)
	}
	if ctx.Mode != ModeSelf || ctx.Workspace != plan.Workspace || ctx.Home != paths.LegacyHomeState(plan.Workspace) ||
		ctx.DarwinUserTempDir != "/var/folders/ab/xyz/T" || !ctx.SharedTmp {
		t.Errorf("native validation roots were not the resolved producer values: %+v", ctx)
	}
	if got, ok := argvEnvValue(plan.BootstrapArgv, paths.MacosUserTmpdirContextEnv); !ok || got != value {
		t.Errorf("bootstrap context = %q, present %v; session context = %q", got, ok, value)
	}
	if !slices.Contains(LauncherOwnedEnvNames(), paths.MacosUserTmpdirContextEnv) {
		t.Error("the run pipeline does not protect the native temp context from composed user env")
	}
}
