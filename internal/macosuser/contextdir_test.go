package macosuser

// contextdir_test.go pins $YOLO_CONTEXT_DIR on this backend (docs/design/context-mounts.md
// §4 step 2, CX-D4) through the PLAN BUILDER — the value a launch really carries — and the
// PlanInvariants guard that fails a plan which loses any half of it.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Every launch names the context dir: with a composed tree and without one, the bootstrap
// env and the agent's session env file both carry YOLO_CONTEXT_DIR=<StagedCtxRoot>, and a
// stage command puts a directory there — empty when nothing was composed, because the
// variable is set either way and must never name a directory that does not exist.
func TestEveryPlanNamesAndStagesTheContextDir(t *testing.T) {
	for name, hostCtx := range map[string]HostContext{
		"a composed tree":  deliveredCtx(),
		"nothing composed": {},
	} {
		t.Run(name, func(t *testing.T) {
			plan := planWithCtx(t, hostCtx)
			want := StagedCtxRoot(cnameFor("/Users/Shared/yolo/proj"), "")

			if plan.ContextDir != want {
				t.Fatalf("plan.ContextDir = %q, want %q", plan.ContextDir, want)
			}
			if !slices.Contains(plan.BootstrapArgv, paths.ContextDirEnv+"="+want) {
				t.Errorf("the bootstrap env does not carry %s=%s:\n%v", paths.ContextDirEnv, want,
					plan.BootstrapArgv)
			}
			if !SandboxEnvFileSets(plan.EnvFileContent, paths.ContextDirEnv, want) {
				t.Errorf("the agent's session env file does not export %s:\n%s",
					paths.ContextDirEnv, plan.EnvFileContent)
			}
			if !stagesTreeAt(plan.StageCommands, want) {
				t.Errorf("nothing stages a directory at %s: %v", want, plan.StageCommands)
			}
			if probs := PlanInvariants(plan); len(probs) != 0 {
				t.Errorf("a correct plan reports invariant violations: %v", probs)
			}
		})
	}
}

// YOLO_CTX_ROOT keeps its own rule (CX-D4): set only when a tree was composed. The context
// dir being staged empty must not make the entrypoint think host bytes crossed.
func TestAnEmptyContextDirDoesNotClaimAComposedTree(t *testing.T) {
	plan := planWithCtx(t, HostContext{})
	if plan.CtxRoot != "" {
		t.Errorf("plan.CtxRoot = %q with nothing composed", plan.CtxRoot)
	}
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, "YOLO_CTX_ROOT=") {
			t.Errorf("the bootstrap is told host bytes crossed when none did: %q", a)
		}
	}
}

// THE GUARD ON THE GUARD: each half removed from an otherwise correct plan is caught.
func TestPlanInvariantsCatchALostContextDir(t *testing.T) {
	good := planWithCtx(t, HostContext{})
	for name, break_ := range map[string]func(p *RunPlan){
		"not staged": func(p *RunPlan) {
			var kept [][]string
			for _, c := range p.StageCommands {
				if !(len(c) > 0 && c[0] == mvBin && c[len(c)-1] == p.ContextDir) {
					kept = append(kept, c)
				}
			}
			p.StageCommands = kept
		},
		"not in the bootstrap env": func(p *RunPlan) {
			var kept []string
			for _, a := range p.BootstrapArgv {
				if !strings.HasPrefix(a, paths.ContextDirEnv+"=") {
					kept = append(kept, a)
				}
			}
			p.BootstrapArgv = kept
		},
		"not in the env file": func(p *RunPlan) { p.EnvFileContent = "" },
		"agent-writable":      func(p *RunPlan) { p.ContextDir = p.Workspace + "/.yolo/ctx" },
	} {
		t.Run(name, func(t *testing.T) {
			plan := good
			plan.StageCommands = append([][]string(nil), good.StageCommands...)
			plan.BootstrapArgv = append([]string(nil), good.BootstrapArgv...)
			break_(&plan)
			if probs := strings.Join(PlanInvariants(plan), " "); !strings.Contains(probs, paths.ContextDirEnv) &&
				!strings.Contains(probs, "context dir") {
				t.Errorf("PlanInvariants passed a plan whose context dir is %s: %v", name, probs)
			}
		})
	}
}
