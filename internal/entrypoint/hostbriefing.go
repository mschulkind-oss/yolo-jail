package entrypoint

// hostbriefing.go is the host-target BRIEFING render (pack-host-management-plan.md
// Phase 5): a pack's prose maintained INSIDE the user's own briefing file as a delimited
// managed block.
//
// Why a block and not the jail's concat. In a jail, ComposePackBriefings
// (internal/agents/agentsmd.go) appends pack prose to the composed briefing and writes
// the result to a DIFFERENT path — a staging file the jail bind-mounts :ro. At the host
// notch source and destination are the SAME file: a pack declares
// `after: "host:.claude/CLAUDE.md"` and `into: ".claude/CLAUDE.md"`, and both name the
// user's real file. A plain append would therefore re-append yolo's own previous output
// on every apply and grow without bound. The Markdown analogue of `config`'s key-level
// RMW is a delimited block: yolo owns everything between its markers and nothing else.
//
// The decisions this encodes:
//   - PER-PACK markers, named at BOTH ends. Two packs are two blocks, so dropping one
//     pack removes only its own block; naming the end marker as well means a crossed or
//     truncated block is DETECTED rather than mistaken for a boundary.
//   - FOUND BY MARKER, never by offset. The first write appends at end-of-file; every
//     write after that rewrites in place, wherever the user has since moved the block.
//   - FAIL-CLOSED on malformed state. An unterminated, duplicated, or crossed marker is
//     refused with a message naming the file — the A12 posture ConfigureHostFiles takes
//     (hostfiles.go:50). Guessing where a block ends is exactly how a renderer eats the
//     prose it was supposed to preserve.
//   - The block carries the PACK's prose, not yolo's environment briefing. BriefingContent's
//     body describes a jail (/workspace, no sudo, the shims, the loopholes); at the host
//     there is no jail to describe, and what this render writes must be a function of the
//     pack alone. The `after: "host:…"` half of the declaration is likewise a no-op here:
//     it exists to pull the user's file INTO a jail staging copy, and at the host the
//     user's file is already the destination the block lives in.
//   - Nothing outside the markers is read for meaning or rewritten. That is what makes
//     "hand-written prose survives" a property of the mechanism rather than a test result.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostBriefingMarker is the marker vocabulary. Split into a prefix so a stray marker in
// pack prose can be detected with one substring check, and so the parser and the writer
// cannot drift into two spellings.
const (
	hostBriefingMarkerTag = "yolo:pack-briefing"
	hostBriefingBeginPre  = "<!-- " + hostBriefingMarkerTag + " begin ("
	hostBriefingEndPre    = "<!-- " + hostBriefingMarkerTag + " end ("
	hostBriefingMarkerSuf = ") -->"
)

func hostBriefingBeginMarker(pack string) string {
	return hostBriefingBeginPre + pack + hostBriefingMarkerSuf
}

func hostBriefingEndMarker(pack string) string {
	return hostBriefingEndPre + pack + hostBriefingMarkerSuf
}

// RenderHostBriefing asserts one pack's managed briefing block in homeDir (the real
// $HOME), once per `briefing` contribution the pack declares. When observe is true it
// computes what WOULD change and writes nothing — the same contract RenderHostPack
// honors, so the dry-run preview is not weaker than the write.
//
// A pack that ships no prose gets no block, and any block it left behind on a previous
// apply is REMOVED: "the pack stopped shipping a briefing" and "the pack was dropped"
// should not differ in what is left in the user's file.
//
// Returns one result per destination. On a malformed destination it returns the results
// computed so far AND an error (the RenderHostPack precedent): the caller prints the
// refusal and exits non-zero, and nothing was written.
func RenderHostBriefing(p *packload.Pack, homeDir string, observe bool) ([]HostRenderResult, error) {
	var out []HostRenderResult
	for _, c := range p.Decl.Contributions() {
		if c.Kind != packdecl.KindBriefing || c.Into == "" {
			continue
		}
		path := filepath.Join(homeDir, c.Into)
		id := p.Name + "/briefing"

		prose := hostBriefingProse(p, c)
		if prose == "" {
			// No prose to assert. Still reconcile: a block from a previous apply is stale
			// content the user did not write and cannot be expected to recognize.
			res, err := removeHostBriefingBlockAt(path, p.Name, id, observe)
			if err != nil {
				return append(out, HostRenderResult{Surface: id, Path: path,
					Action: "refused: " + err.Error()}), fmt.Errorf("%s: %w", id, err)
			}
			if res != nil {
				out = append(out, *res)
				continue
			}
			out = append(out, HostRenderResult{Surface: id, Path: path,
				Action: "skipped: pack ships no briefing prose"})
			continue
		}

		existing, err := readHostBriefingFile(path)
		if err != nil {
			return out, fmt.Errorf("%s: %w", id, err)
		}
		updated, err := assertHostBriefingBlock(existing, p.Name, prose)
		if err != nil {
			return append(out, HostRenderResult{Surface: id, Path: path,
				Action: "refused: " + err.Error()}), fmt.Errorf("%s: reading ~/%s: %w", id, c.Into, err)
		}
		// Overwrites is deliberately EMPTY for a briefing block, and that is not an
		// oversight: the config renderer reports an overwrite when it replaces a value the
		// USER owns, and here everything the user owns is outside the markers and never
		// touched. The block's body is yolo's own subtree — the same posture Phase 4 takes
		// inside a pack's skills dir — so re-asserting it is not a clobber to warn about.
		action, err := writeHostBriefingFile(path, existing, updated, observe)
		if err != nil {
			return out, fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, HostRenderResult{Surface: id, Path: path, Action: action})
	}
	return out, nil
}

// PruneHostBriefings removes managed blocks whose pack is no longer active, from every
// briefing destination the candidate packs name.
//
// It is a separate entry from RenderHostBriefing for a structural reason: a pack DROPPED
// from config is never visited by the render loop, so the only way its block leaves the
// user's file is a pass that knows the destinations independently of the active set.
// candidates supplies the paths to look at (the union of every pack yolo can resolve —
// embedded plus configured), active names the packs whose blocks are legitimate.
//
// The candidate set bounds what this can reach, and honestly: a pack removed from config
// whose destination NO remaining pack also names is unreachable, so its block survives.
// That is the conservative direction — leaving one stale attributed block beats scanning
// the user's home for marker-bearing files — and it is why the block carries a provenance
// header a human can act on.
//
// A nil active set is REFUSED rather than treated as "nothing is active": that reading
// would delete every managed block on a caller bug, which is the one outcome this whole
// file exists to prevent. An empty non-nil map is the honest "no packs configured".
func PruneHostBriefings(candidates []*packload.Pack, active map[string]bool, homeDir string, observe bool) ([]HostRenderResult, error) {
	if active == nil {
		return nil, fmt.Errorf("host briefing prune: refusing to prune with an unknown active pack set")
	}
	var out []HostRenderResult
	for _, path := range hostBriefingPaths(candidates, homeDir) {
		content, err := readHostBriefingFile(path)
		if err != nil {
			return out, err
		}
		if content == "" {
			continue
		}
		blocks, err := parseHostBriefingBlocks(strings.Split(content, "\n"))
		if err != nil {
			return out, fmt.Errorf("host briefing %s: %w", path, err)
		}
		// Re-read and re-splice per removal rather than batching: each removal shifts the
		// line numbers of the ones after it, and one splice at a time is the version of
		// this loop that cannot get that arithmetic wrong.
		for _, b := range blocks {
			if active[b.Pack] {
				continue
			}
			id := b.Pack + "/briefing"
			res, rerr := removeHostBriefingBlockAt(path, b.Pack, id, observe)
			if rerr != nil {
				return out, fmt.Errorf("%s: %w", id, rerr)
			}
			if res != nil {
				res.Action += " (pack no longer configured)"
				out = append(out, *res)
			}
		}
	}
	return out, nil
}

// hostBriefingPaths is the deduplicated, order-preserving set of home-absolute briefing
// destinations the given packs declare — the files a prune pass must look at.
func hostBriefingPaths(packs []*packload.Pack, homeDir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindBriefing || c.Into == "" {
				continue
			}
			path := filepath.Join(homeDir, c.Into)
			if seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

// hostBriefingProse reads one pack's briefing prose, right-trimmed. The declared `from`
// is tried first, then the AGENTS.md / CLAUDE.md pair readPackBriefing accepts in a jail
// — a pack must deliver the same prose at both notches, and which of the two names it
// happens to use is not something the author should have to think about.
//
// The containment check is not theater: `from` is manifest data, a caller may hold a pack
// whose Decode problems it ignored, and a "../../.ssh/id_rsa" that slipped through would
// otherwise be copied into a file the user reads as instructions.
func hostBriefingProse(p *packload.Pack, c packdecl.Contribution) string {
	candidates := []string{}
	if c.From != "" {
		candidates = append(candidates, c.From)
	}
	candidates = append(candidates, "AGENTS.md", "CLAUDE.md")
	root := filepath.Clean(p.Root)
	for _, rel := range candidates {
		path := filepath.Clean(filepath.Join(root, rel))
		if root != "" && root != "." && !strings.HasPrefix(path, root+string(filepath.Separator)) {
			continue // escapes the pack tree
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if text := strings.TrimRight(string(data), " \t\r\n"); text != "" {
			return text
		}
	}
	return ""
}

// hostBriefingBlockLines renders one pack's block: the delimiters, the existing
// `<!-- from pack: NAME -->` provenance header from ComposePackBriefings, then the prose.
//
// The provenance header is kept INSIDE the block rather than replaced by the markers. It
// is the line a reader of the composed file sees in a jail, so keeping it means the same
// prose is attributed the same way at both notches; the markers answer a different
// question (who may rewrite this region) and the two are not substitutes.
func hostBriefingBlockLines(pack, prose string) []string {
	lines := []string{
		hostBriefingBeginMarker(pack),
		"<!-- from pack: " + pack + " -->",
	}
	lines = append(lines, strings.Split(prose, "\n")...)
	return append(lines, hostBriefingEndMarker(pack))
}

// hostBriefingBlock is one parsed block: which pack owns it and the inclusive line span
// of its delimiters.
type hostBriefingBlock struct {
	Pack       string
	Begin, End int
}

// parseHostBriefingBlocks finds every managed block in a file's lines, refusing any
// structure it cannot read unambiguously.
//
// Every refusal here is a case where a renderer that "recovered" would have to guess a
// boundary, and a wrong guess deletes the user's prose. So: an unterminated begin, an end
// with no begin, a nested or crossed pair, and two blocks for one pack are all errors. A
// marker line is matched on its TRIMMED text, so an editor that reindented it still
// parses, but it is re-emitted in canonical form.
func parseHostBriefingBlocks(lines []string) ([]hostBriefingBlock, error) {
	var out []hostBriefingBlock
	open := -1
	openPack := ""
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, hostBriefingBeginPre) && strings.HasSuffix(line, hostBriefingMarkerSuf):
			pack := strings.TrimSuffix(strings.TrimPrefix(line, hostBriefingBeginPre), hostBriefingMarkerSuf)
			if open >= 0 {
				return nil, fmt.Errorf("managed block for pack %q at line %d is not closed before pack %q opens at line %d "+
					"— refusing to guess where it ends; close or remove the marker by hand",
					openPack, open+1, pack, i+1)
			}
			open, openPack = i, pack
		case strings.HasPrefix(line, hostBriefingEndPre) && strings.HasSuffix(line, hostBriefingMarkerSuf):
			pack := strings.TrimSuffix(strings.TrimPrefix(line, hostBriefingEndPre), hostBriefingMarkerSuf)
			if open < 0 {
				return nil, fmt.Errorf("managed block end marker for pack %q at line %d has no matching begin "+
					"— refusing to touch the file; remove the stray marker by hand", pack, i+1)
			}
			if pack != openPack {
				return nil, fmt.Errorf("managed block for pack %q (line %d) is closed by pack %q's end marker (line %d) "+
					"— refusing to guess the boundary; fix the markers by hand", openPack, open+1, pack, i+1)
			}
			for _, prev := range out {
				if prev.Pack == pack {
					return nil, fmt.Errorf("two managed blocks for pack %q (lines %d and %d) "+
						"— refusing to guess which one to keep; delete one by hand", pack, prev.Begin+1, open+1)
				}
			}
			out = append(out, hostBriefingBlock{Pack: pack, Begin: open, End: i})
			open, openPack = -1, ""
		}
	}
	if open >= 0 {
		return nil, fmt.Errorf("unterminated managed block for pack %q (begins at line %d, no %q) "+
			"— refusing to guess where it ends; close or remove the marker by hand",
			openPack, open+1, hostBriefingEndMarker(openPack))
	}
	return out, nil
}

// assertHostBriefingBlock returns content with pack's block re-asserted: spliced in place
// when a block for that pack exists, appended at end-of-file when it does not.
//
// The splice is line-for-line over strings.Split/Join, which round-trips a file's bytes
// exactly — that (not a comparison at the end) is what makes a second --assert
// byte-identical to the first.
func assertHostBriefingBlock(content, pack, prose string) (string, error) {
	if strings.Contains(prose, hostBriefingMarkerTag) {
		// A pack whose prose carries a marker could open or close a block the writer did
		// not, so the file's structure would depend on pack content. Refuse it by name.
		return "", fmt.Errorf("pack %q briefing prose contains a %q marker, which would break the managed block", pack, hostBriefingMarkerTag)
	}
	lines := strings.Split(content, "\n")
	blocks, err := parseHostBriefingBlocks(lines)
	if err != nil {
		return "", err
	}
	block := hostBriefingBlockLines(pack, prose)
	for _, b := range blocks {
		if b.Pack != pack {
			continue
		}
		out := append([]string{}, lines[:b.Begin]...)
		out = append(out, block...)
		out = append(out, lines[b.End+1:]...)
		return strings.Join(out, "\n"), nil
	}
	// First write: end-of-file, one blank line after whatever the user wrote. Appending
	// (rather than prepending) keeps the user's own opening prose in the position an agent
	// reads first, and every later write finds the block by marker wherever it has moved.
	rendered := strings.Join(block, "\n") + "\n"
	if strings.TrimSpace(content) == "" {
		return rendered, nil
	}
	return strings.TrimRight(content, "\n") + "\n\n" + rendered, nil
}

// dropHostBriefingBlock returns content with pack's block removed, and whether one was
// there. The blank line assertHostBriefingBlock inserted before an appended block is
// dropped with it, so append-then-remove restores the file's original bytes instead of
// leaving a growing run of blank lines behind.
func dropHostBriefingBlock(content, pack string) (string, bool, error) {
	lines := strings.Split(content, "\n")
	blocks, err := parseHostBriefingBlocks(lines)
	if err != nil {
		return "", false, err
	}
	for _, b := range blocks {
		if b.Pack != pack {
			continue
		}
		out := append([]string{}, lines[:b.Begin]...)
		out = append(out, lines[b.End+1:]...)
		if b.Begin > 0 && lines[b.Begin-1] == "" && (b.Begin >= len(out) || out[b.Begin] == "") {
			out = append(out[:b.Begin-1], out[b.Begin:]...)
		}
		return strings.Join(out, "\n"), true, nil
	}
	return content, false, nil
}

// removeHostBriefingBlockAt drops pack's block from path, returning nil when the file has
// no such block (so the caller can report "nothing to do" rather than a phantom removal).
func removeHostBriefingBlockAt(path, pack, id string, observe bool) (*HostRenderResult, error) {
	existing, err := readHostBriefingFile(path)
	if err != nil {
		return nil, err
	}
	if existing == "" {
		return nil, nil
	}
	updated, found, err := dropHostBriefingBlock(existing, pack)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	action := "removed"
	if observe {
		action = "would remove"
	} else if err := WriteStringInPlace(path, updated, 0o644); err != nil {
		return nil, err
	}
	return &HostRenderResult{Surface: id, Path: path, Action: action}, nil
}

// readHostBriefingFile reads a destination, treating ABSENT as empty. An absent user
// briefing is the normal case, not an error — the render creates the file containing just
// the block.
func readHostBriefingFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// writeHostBriefingFile commits (or, in observe, does not commit) the updated content and
// returns the action to report. Identical content is reported as "unchanged" in both
// postures and is not rewritten, so a no-op apply does not touch the file's mtime.
//
// The write goes through WriteStringInPlace: 0o644 applies only when creating, so a user
// who chmod'd their own briefing keeps their mode.
func writeHostBriefingFile(path, existing, updated string, observe bool) (string, error) {
	if existing == updated {
		return "unchanged", nil
	}
	if observe {
		return "would render", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := WriteStringInPlace(path, updated, 0o644); err != nil {
		return "", err
	}
	return "rendered", nil
}
