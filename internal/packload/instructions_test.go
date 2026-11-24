package packload

import (
	"reflect"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestEveryAgentPackDeclaresItsInstructionFiles is the census of docs/design/
// workspace-instruction-files.md IF-D7: a pack that delivers a briefing to an agent's user-level file
// declares which repository files that agent reads, so a new agent pack cannot ship unclassified and
// the launch can say what it did or could not do. Deleting a pack's declaration fails it.
func TestEveryAgentPackDeclaresItsInstructionFiles(t *testing.T) {
	for _, p := range Embedded() {
		declared := map[string]bool{}
		for _, d := range p.Decl.InstructionsContributions() {
			declared[d.Agent] = true
			if len(d.Reads) == 0 {
				t.Errorf("pack %s: instructions for %s name no file it reads", p.Name, d.Agent)
			}
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindBriefing && c.Into != "" && c.Agent != "" && !declared[c.Agent] {
				t.Errorf("pack %s delivers a briefing to agent %q and declares no `instructions` for it: "+
					"add an [[instructions]] table naming the files the agent reads, with the setting or flag "+
					"that stops it or none (docs/design/workspace-instruction-files.md)", p.Name, c.Agent)
			}
		}
	}
}

// The supported set is a ruling, not an accident: copilot is unsupported because its flag also drops
// the briefing and nothing re-delivers it (Appendix A), and the others have no setting at all.
func TestTheSupportedInstructionAgentsAreTheRuledOnes(t *testing.T) {
	var supported, unsupported []string
	for _, a := range InstructionsAgents(Embedded()) {
		if a.Supported() {
			supported = append(supported, a.Agent)
		} else {
			unsupported = append(unsupported, a.Agent)
		}
	}
	sort.Strings(supported)
	sort.Strings(unsupported)
	if want := []string{"claude", "codex", "pi"}; !reflect.DeepEqual(supported, want) {
		t.Errorf("supported = %v, want %v", supported, want)
	}
	if want := []string{"agy", "copilot", "muse", "oh-omp", "opencode"}; !reflect.DeepEqual(unsupported, want) {
		t.Errorf("unsupported = %v, want %v", unsupported, want)
	}
}

// A declaration whose list or set names a surface its own pack does not declare would render nothing
// and report an orphan at every launch; the shipped ones are pinned to a surface their pack owns.
func TestShippedInstructionsTargetASurfaceTheirPackOwns(t *testing.T) {
	for _, p := range Embedded() {
		owned := map[manifest.SurfaceKey]bool{}
		surfaces, _ := p.SurfacesFor(true)
		for _, s := range surfaces {
			owned[s.Key()] = true
		}
		for _, d := range p.Decl.InstructionsContributions() {
			if d.Ignore == nil {
				continue
			}
			for _, target := range []string{surfaceOf(d.Ignore.List), surfaceOfSet(d.Ignore.Set)} {
				if target == "" {
					continue
				}
				key, err := manifest.ParseSurfaceID(target)
				if err != nil || !owned[key] {
					t.Errorf("pack %s: instructions for %s target %q, which the pack does not own (%v)", p.Name, d.Agent, target, err)
				}
			}
		}
	}
}

func surfaceOf(l *packdecl.InstructionsList) string {
	if l == nil {
		return ""
	}
	return l.Surface
}

func surfaceOfSet(s *packdecl.InstructionsSet) string {
	if s == nil {
		return ""
	}
	return s.Surface
}

// TestLaunchFlagsCarryTheInstructionsFlagsOnlyWhileIgnoring pins the one fold every spelling of the
// launch flags reads: pi's flags and its briefing re-delivery appear for an ignoring selection and
// for no other, and survive a `replace` posture row that clears the claim.
func TestLaunchFlagsCarryTheInstructionsFlagsOnlyWhileIgnoring(t *testing.T) {
	var pi *Pack
	for _, p := range Embedded() {
		if p.Name == "pi" {
			pi = p
		}
	}
	if pi == nil {
		t.Fatal("no shipped pi pack")
	}
	clone := *pi
	packs := []*Pack{&clone}
	if flags := LaunchFlagsFor(packs, true)["pi"]; containsFlag(flags, "--no-context-files") {
		t.Fatalf("a launch with no selection carries %v", flags)
	}
	ApplyInstructions(packs, &packdecl.InstructionsSelection{Mode: packdecl.ModeKeep, Home: "/home/agent"})
	if flags := LaunchFlagsFor(packs, true)["pi"]; containsFlag(flags, "--no-context-files") {
		t.Fatalf("keep carries %v", flags)
	}
	ApplyInstructions(packs, &packdecl.InstructionsSelection{Mode: packdecl.ModeIgnore, Workspace: "/workspace", Home: "/home/agent"})
	flags := LaunchFlagsFor(packs, true)["pi"]
	if !containsFlag(flags, "--no-context-files") || !containsFlag(flags, "/home/agent/.pi/agent/AGENTS.md") {
		t.Errorf("ignore carries %v, want the flag and the re-delivered briefing file", flags)
	}
}

func containsFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
