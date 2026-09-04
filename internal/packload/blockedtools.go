package packload

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// blockedtools.go collects the `blocked-tool` contributions of the selected packs.
//
// WHY THIS IS A PACK CONCERN. Core blocked `grep -r` and `find` by DEFAULT until
// 2026-09-04, and the default carried a hidden assumption: that the image bakes `rg`
// and `fd`. True of the container backends, false of macos-user, which bakes nothing —
// so on the first working Mac launch the shims were generated, `grep -r` exited 127,
// and the suggestion named a binary that did not exist. The agent lost the capability
// AND was sent nowhere.
//
// A pack is the honest home for it: the thing that blocks a tool is the thing that can
// also ship or require its replacement, and selecting the pack is the opt-in. Core now
// blocks nothing on its own.

// BlockedTool is one pack-declared refusal, in the shape the entrypoint's shim writer
// consumes.
type BlockedTool struct {
	// Name is the binary being blocked — the FILE written into the block dir.
	Name string
	// Message is printed on refusal; Suggestion is the alternative to try.
	Message    string
	Suggestion string
	// Replacement is the binary Suggestion names, checked against the agent's PATH
	// before the blocker is generated. Empty means "always block".
	Replacement string
	// Flags are argv patterns that trigger the block; empty means "always".
	Flags []string
	// AllowFlags exempt an invocation even when Flags match. Wired and unused: the
	// next rule's shape ("block find UNLESS it is depth-limited") is already visible
	// and block-on-presence cannot express it.
	AllowFlags []string
	// Pack is the declaring pack, for provenance in a collision message.
	Pack string
}

// BlockedTools returns every `blocked-tool` contribution across packs, in pack order.
//
// Order is config order, and later does NOT win: the kind is CombineExclusive, so two
// packs blocking one binary is a collision the loader reports rather than a silent
// override — the blocker is a single file path, and a second pack quietly replacing
// another's refusal message is the kind of thing nobody would find.
func BlockedTools(packs []*Pack) []BlockedTool {
	var out []BlockedTool
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindBlockedTool || c.Bin == "" {
				continue
			}
			out = append(out, BlockedTool{
				Name:        c.Bin,
				Message:     c.Message,
				Suggestion:  c.Suggestion,
				Replacement: c.Replacement,
				Flags:       append([]string(nil), c.Flags...),
				AllowFlags:  append([]string(nil), c.AllowFlags...),
				Pack:        p.Name,
			})
		}
	}
	return out
}
