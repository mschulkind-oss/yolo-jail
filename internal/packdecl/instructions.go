package packdecl

// instructions.go is the `instructions` contribution kind: which instruction files an agent reads
// from the repository, and the ONE mechanism that stops it, declared by the pack that installs the
// agent so core names no agent (docs/reference/workspace-instruction-files.md IF-D7).
//
// THERE IS NO MASK, by the owner's ruling (IF-D14): nothing here, and nothing built from it, binds a
// file over a workspace path. An agent whose pack declares no mechanism is UNSUPPORTED, and the
// launch says it still reads the files (IF-D15).
//
// The kind is a DECLARATION; what a launch does with it depends on the repo_instructions mode, which
// the pack never sees. A launch hands the resolved InstructionsSelection to each loaded pack
// (packload.Pack.Instructions), and the readers below answer from the pair:
//
//   - ListFor: entries appended to one array of a config surface, the claude `claudeMdExcludes` case;
//   - SetFor: a body set in a config surface's managed layer, the codex `project_doc_max_bytes` case;
//   - LaunchFor: flags handed to the program at launch, the pi `--no-context-files` case, plus the
//     re-delivery argument that gives back the briefing the flag also dropped (IF-D8).
//
// At the unconfined host path (`yolo host -- <agent>`) there is no per-workspace config file, so the
// list and the set are replaced by the declaration's host_launch flags (IF-D16).

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Modes of the repo_instructions config table (design §3.1). The empty mode means "keep".
const (
	ModeKeep    = "keep"
	ModeIgnore  = "ignore"
	ModeReplace = "replace"
)

// InstructionsSelection is what one launch resolved about repo_instructions, handed to every loaded
// pack. A nil selection, and the mode "keep", mean the launch does nothing about the files.
type InstructionsSelection struct {
	// Mode is keep, ignore or replace.
	Mode string `json:"mode"`
	// Workspace is the workspace's absolute path AS THE AGENT SEES IT: /workspace in a container, the
	// real directory at macos-user and the host.
	Workspace string `json:"workspace,omitempty"`
	// Home is the agent's home directory as the agent sees it.
	Home string `json:"home,omitempty"`
	// Host is true on the unconfined path, where a per-workspace config file does not exist and the
	// declaration's host_launch flags carry the mechanism.
	Host bool `json:"host,omitempty"`
}

// Ignoring reports whether the selection asks an agent to stop reading the repository's files.
func (s *InstructionsSelection) Ignoring() bool {
	return s != nil && (s.Mode == ModeIgnore || s.Mode == ModeReplace)
}

// InstructionsList is the `ignore.list` mechanism: entries appended to an array of a config surface,
// through the config-list fold, so the user's own entries survive (IF-D9).
type InstructionsList struct {
	// Surface is the target surface identity, "agent/name".
	Surface string `json:"surface"`
	// Path is the RFC 6901 pointer to the array.
	Path string `json:"path"`
	// Add are the entries, `${workspace}` standing for the workspace path.
	Add []string `json:"add"`
}

// InstructionsSet is the `ignore.set` mechanism: a body folded into a config surface's managed layer.
type InstructionsSet struct {
	// Surface is the target surface identity, "agent/name".
	Surface string `json:"surface"`
	// Config is the body, in the shape a config-overlay's `set` takes.
	Config map[string]any `json:"config"`
}

// InstructionsLaunch is flags handed to the program at launch.
type InstructionsLaunch struct {
	// Flags are the argument words.
	Flags []string `json:"flags"`
}

// InstructionsIgnore is the mechanism block of an `instructions` contribution: at most one of List,
// Set and Launch, and HostLaunch beside List or Set.
type InstructionsIgnore struct {
	List   *InstructionsList   `json:"list,omitempty"`
	Set    *InstructionsSet    `json:"set,omitempty"`
	Launch *InstructionsLaunch `json:"launch,omitempty"`
	// HostLaunch replaces List and Set at `yolo host --`. It takes `${entries_json}`, the list's
	// entries as a JSON array, and `${workspace}`.
	HostLaunch []string `json:"host_launch,omitempty"`
	// StillRead names the files the mechanism does NOT reach (pi's `.pi/APPEND_SYSTEM.md`), so the
	// disclosure line is truthful about what the mode left.
	StillRead []string `json:"still_read,omitempty"`
}

// InstructionsRedeliver gives back a user-level file the ignore mechanism also dropped (design P1):
// the briefing file the pack already delivers, handed to the program as `Flag <home>/<File>`.
type InstructionsRedeliver struct {
	Flag string `json:"flag"`
	File string `json:"file"`
}

// Instructions is one pack's `instructions` declaration for one agent, as the readers consume it.
type Instructions struct {
	// Agent is the identity the pack's other contributions use for this agent.
	Agent string
	// Bin is the program the launch flags are for.
	Bin string
	// Reads are the workspace-relative name patterns the agent loads, for the disclosure.
	Reads     []string
	Ignore    *InstructionsIgnore
	Redeliver *InstructionsRedeliver
}

// Supported reports whether the declaration carries a mechanism. One without is the UNSUPPORTED
// agent: its launch continues and says so.
func (d Instructions) Supported() bool {
	return d.Ignore != nil && (d.Ignore.List != nil || d.Ignore.Set != nil || d.Ignore.Launch != nil)
}

// InstructionsContributions returns every `instructions` declaration, in declaration order.
func (m *Manifest) InstructionsContributions() []Instructions {
	var out []Instructions
	for _, c := range m.Contributions() {
		if c.Kind != KindInstructions {
			continue
		}
		out = append(out, Instructions{Agent: c.Agent, Bin: c.Bin, Reads: c.Reads, Ignore: c.Ignore, Redeliver: c.Redeliver})
	}
	return out
}

// substituteInstructions expands the tokens a declaration may spell.
func substituteInstructions(s string, sel *InstructionsSelection, entriesJSON string) string {
	r := strings.NewReplacer("${workspace}", sel.Workspace, "${home}", sel.Home, "${entries_json}", entriesJSON)
	return r.Replace(s)
}

// ListFor is the list mechanism's contribution under sel: the surface, the pointer and the entries
// with the workspace path substituted. ok is false for a keep selection, the host path (which uses
// LaunchFor), or a declaration with no list.
func (d Instructions) ListFor(sel *InstructionsSelection) (surface, path string, add []string, ok bool) {
	if !sel.Ignoring() || sel.Host || d.Ignore == nil || d.Ignore.List == nil {
		return "", "", nil, false
	}
	for _, e := range d.Ignore.List.Add {
		add = append(add, substituteInstructions(e, sel, ""))
	}
	return d.Ignore.List.Surface, d.Ignore.List.Path, add, true
}

// SetFor is the set mechanism's body under sel, as the JSON a config-overlay's managed layer takes.
func (d Instructions) SetFor(sel *InstructionsSelection) (surface string, body json.RawMessage, ok bool) {
	if !sel.Ignoring() || sel.Host || d.Ignore == nil || d.Ignore.Set == nil {
		return "", nil, false
	}
	raw, err := json.Marshal(map[string]any{"managed": d.Ignore.Set.Config})
	if err != nil {
		return "", nil, false
	}
	return d.Ignore.Set.Surface, raw, true
}

// LaunchFor is the flags this declaration hands its program under sel, and the program's name. Away
// from the host that is the launch mechanism's flags plus the re-delivery argument; at the host it is
// host_launch when the declaration has a list or a set, and the launch flags as before otherwise.
func (d Instructions) LaunchFor(sel *InstructionsSelection) (bin string, flags []string) {
	if !sel.Ignoring() || d.Ignore == nil || d.Bin == "" {
		return "", nil
	}
	switch {
	case sel.Host && len(d.Ignore.HostLaunch) > 0:
		entries, _ := json.Marshal(d.expandedEntries(sel))
		for _, f := range d.Ignore.HostLaunch {
			flags = append(flags, substituteInstructions(f, sel, string(entries)))
		}
	case d.Ignore.Launch != nil:
		for _, f := range d.Ignore.Launch.Flags {
			flags = append(flags, substituteInstructions(f, sel, ""))
		}
		if d.Redeliver != nil {
			file := strings.TrimRight(sel.Home, "/") + "/" + d.Redeliver.File
			// A jail always has the briefing it delivered. At the host it exists only after
			// `yolo host apply` wrote it, and a program that takes "text or a file" would read a
			// path that names nothing as the text itself, so a missing file is not handed.
			if _, err := os.Stat(file); err == nil || !sel.Host {
				flags = append(flags, d.Redeliver.Flag, file)
			}
		}
	}
	return d.Bin, flags
}

// expandedEntries are the list's entries under sel, [] when the declaration has no list.
func (d Instructions) expandedEntries(sel *InstructionsSelection) []string {
	out := []string{}
	if d.Ignore != nil && d.Ignore.List != nil {
		for _, e := range d.Ignore.List.Add {
			out = append(out, substituteInstructions(e, sel, ""))
		}
	}
	return out
}

// Mechanism names the mechanism for the disclosure line: the setting or flag, or "" when none.
func (d Instructions) Mechanism(sel *InstructionsSelection) string {
	if d.Ignore == nil {
		return ""
	}
	switch {
	case sel != nil && sel.Host && len(d.Ignore.HostLaunch) > 0:
		return strings.Join(d.Ignore.HostLaunch[:1], " ")
	case d.Ignore.List != nil:
		return strings.TrimPrefix(d.Ignore.List.Path, "/")
	case d.Ignore.Set != nil:
		for k := range d.Ignore.Set.Config {
			return k
		}
	case d.Ignore.Launch != nil && len(d.Ignore.Launch.Flags) > 0:
		return d.Ignore.Launch.Flags[0]
	}
	return ""
}

// instructionsProblems checks one `instructions` contribution.
func instructionsProblems(label string, c Contribution) []string {
	var problems []string
	need := func(field, val string) {
		if val == "" {
			problems = append(problems, fmt.Sprintf("%s: kind %q needs %q", label, c.Kind, field))
		}
	}
	need("agent", c.Agent)
	if len(c.Reads) == 0 {
		problems = append(problems, label+": kind \"instructions\" needs a non-empty \"reads\" — the "+
			"workspace-relative names the agent loads, which the launch's disclosure counts")
	}
	for _, r := range c.Reads {
		if r == "" || strings.HasPrefix(r, "/") || strings.Contains(r, "..") {
			problems = append(problems, fmt.Sprintf("%s: reads entry %q must be a non-empty workspace-relative pattern", label, r))
		}
	}
	ig := c.Ignore
	if ig == nil {
		if c.Redeliver != nil {
			problems = append(problems, label+": \"redeliver\" gives back a file an ignore mechanism drops, so it needs \"ignore.launch\"")
		}
		return problems
	}
	mechanisms := 0
	for _, has := range []bool{ig.List != nil, ig.Set != nil, ig.Launch != nil} {
		if has {
			mechanisms++
		}
	}
	if mechanisms == 0 {
		problems = append(problems, label+": \"ignore\" needs one of \"list\", \"set\" or \"launch\" — an agent with no mechanism declares no \"ignore\" at all")
	}
	if mechanisms > 1 {
		problems = append(problems, label+": \"ignore\" takes at most one of \"list\", \"set\" and \"launch\"")
	}
	if ig.List != nil {
		need("ignore.list.surface", ig.List.Surface)
		need("ignore.list.path", ig.List.Path)
		if len(ig.List.Add) == 0 {
			problems = append(problems, label+": ignore.list needs a non-empty \"add\"")
		}
		if ig.List.Path != "" {
			problems = append(problems, configListPathProblems(label+".ignore.list.path", ig.List.Path)...)
		}
	}
	if ig.Set != nil {
		need("ignore.set.surface", ig.Set.Surface)
		if len(ig.Set.Config) == 0 {
			problems = append(problems, label+": ignore.set needs a non-empty \"config\" body")
		}
	}
	if ig.Launch != nil && len(ig.Launch.Flags) == 0 {
		problems = append(problems, label+": ignore.launch needs a non-empty \"flags\"")
	}
	if len(ig.HostLaunch) > 0 && ig.List == nil && ig.Set == nil {
		problems = append(problems, label+": \"host_launch\" replaces a list or a set at `yolo host --`, so it needs one of them beside it")
	}
	if (ig.Launch != nil || len(ig.HostLaunch) > 0) && c.Bin == "" {
		problems = append(problems, label+": launch flags need the \"bin\" they are handed to")
	}
	if c.Redeliver != nil {
		if ig.Launch == nil {
			problems = append(problems, label+": \"redeliver\" needs \"ignore.launch\"")
		}
		if c.Redeliver.Flag == "" || c.Redeliver.File == "" {
			problems = append(problems, label+": \"redeliver\" needs \"flag\" and \"file\"")
		}
		if strings.HasPrefix(c.Redeliver.File, "/") || strings.Contains(c.Redeliver.File, "..") {
			problems = append(problems, fmt.Sprintf("%s: redeliver.file %q must be a home-relative path", label, c.Redeliver.File))
		}
	}
	return problems
}

// validateSingleInstructions refuses two `instructions` declarations for one agent in a pack, so a
// launch never has two mechanisms to choose between.
func (m *Manifest) validateSingleInstructions() []string {
	seen := map[string]bool{}
	var problems []string
	for _, c := range m.Contributions() {
		if c.Kind != KindInstructions || c.Agent == "" {
			continue
		}
		if seen[c.Agent] {
			problems = append(problems, fmt.Sprintf("kind \"instructions\" declared twice for agent %q — one declaration names every file the agent reads and its one mechanism", c.Agent))
		}
		seen[c.Agent] = true
	}
	return problems
}
