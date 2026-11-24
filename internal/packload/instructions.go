package packload

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// ApplyInstructions hands one launch's repo_instructions selection to every loaded pack, the one
// call every loader of a launch's packs makes (the host launch, the jail's boot, `yolo host --`).
// A nil selection, or a keep one, leaves each pack's `instructions` declaration inert.
//
// The selection is per launch state beside Pack.LaunchName, and this is its only writer: a reader
// that sees a pack without it is a reader of a launch that keeps the repository's files.
func ApplyInstructions(packs []*Pack, sel *packdecl.InstructionsSelection) {
	for _, p := range packs {
		if p != nil {
			p.Instructions = sel
		}
	}
}

// InstructionsAgent is one selected agent's declaration, with the pack that carries it, for the
// readers that report across the whole set (the launch disclosure, the census).
type InstructionsAgent struct {
	Pack string
	packdecl.Instructions
}

// InstructionsAgents lists every `instructions` declaration of the selected packs, in pack and
// declaration order, one entry per agent (the first wins when two packs declare one agent).
func InstructionsAgents(packs []*Pack) []InstructionsAgent {
	var out []InstructionsAgent
	seen := map[string]bool{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, d := range p.Decl.InstructionsContributions() {
			if seen[d.Agent] {
				continue
			}
			seen[d.Agent] = true
			out = append(out, InstructionsAgent{Pack: p.Name, Instructions: d})
		}
	}
	return out
}
