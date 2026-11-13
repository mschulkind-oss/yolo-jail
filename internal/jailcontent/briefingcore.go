package jailcontent

// briefingcore.go renders the CORE of a jail's briefing (a term coined in
// docs/reference/agent-briefings.md#what-a-composed-briefing-contains): the part of yolo's body every agent loads at session start.
// It builds a model and does not instruct a procedure (P1): a line stays here only if an agent
// that never read it would do damage or waste a turn before it had a reason to look anything up.
// The rest is in the topic files (briefingtopics.go), which the closing pointer names.
//
// EACH LINE IS CONDITIONAL ON THE FACT IT STATES, as the body was: no forwarded port, no port
// clause; no block, no blocked line. Its size is a test (TestTheCoreFitsItsBudget, BD-D4), whose
// failure names moving text to a topic file, never raising the number.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// corePortListMax is how many port entries the core's network line lists before it gives the
// count instead (docs/reference/agent-briefings.md#budgets-and-the-size-line: past four, a jail's ports must not grow the
// core by one entry each). The network topic lists every one.
const corePortListMax = 4

// coreLines are the core's bullets and its pointer, after the header and any alert.
func coreLines(in BriefingInput, v briefingView, topics []BriefingTopic) []string {
	lines := []string{coreWorkspaceLine(in, v), coreSurvivesLine(in, v), coreNetworkLine(in, v), coreNotInHereLine(in)}
	// The three rules the host-requests topic opens with: approvals are the human's, commands go
	// on their own lines, and a background waiter follows. A refusal prints the menu; it cannot
	// make the agent relay it.
	lines = append(lines, "- **Asking the human**: relay a refusal's whole menu, each command alone in a "+
		"code block; run `yolo wait <id>` in the background and retry once on exit 0. "+
		"`yolo approve`, `yolo config apply` and `yolo allow` are the human's to run; anything "+
		"else only the host can do: `yolo host-run`.")
	if blocked := coreBlockedLine(in.BlockedTools); blocked != "" {
		lines = append(lines, blocked)
	}
	return append(lines, "", corePointer(in, topics), "")
}

func coreWorkspaceLine(in BriefingInput, v briefingView) string {
	if v.native {
		return "- **Workspace**: `" + in.Workspace + "`, the host directory; no `/workspace` exists, " +
			"and a doc naming it means this path."
	}
	return "- **Workspace**: `/workspace` is the host directory `" + in.Workspace + "`, bind-mounted live: the same files, no sync step."
}

// coreSurvivesLine answers where work that must outlive a restart goes, from the same durable dir
// and persistence map as the storage topic: the two cannot disagree about a path.
func coreSurvivesLine(in BriefingInput, v briefingView) string {
	var parts []string
	d := in.Durable
	// On a native backend the durable dir is spelled from the workspace the line above names, and
	// the home persists too: the macos-user header's home sentences, in one clause
	// (macosUserHomeLines open the storage topic).
	home, dir := "", ""
	if d != nil {
		dir = d.Path
	}
	if v.native {
		home = " your home (this workspace's own; its machine-wide logins and caches are shared),"
		if rel, ok := strings.CutPrefix(dir, strings.TrimRight(in.Workspace, "/")+"/"); ok && in.Workspace != "" {
			dir = rel
		}
	}
	// Without a durable dir the home, when it persists, is the second of two things kept.
	kept := "the workspace"
	if home != "" {
		kept += " and" + strings.TrimSuffix(home, ",")
	}
	switch {
	case d != nil && d.Path != "":
		s := "the workspace," + home + " and `$" + durable.EnvVar + "` (`" + dir + "`) for scratch, clones and worktrees."
		if d.Caveat != "" {
			s += " ⚠ " + d.Caveat
		}
		parts = append(parts, s)
	case d != nil:
		reason := d.Unavailable
		if reason == "" {
			reason = "yolo could not make it"
		}
		parts = append(parts, kept+"; there is no durable directory this launch ("+reason+"), so tell the user.")
	default:
		parts = append(parts, kept+".")
	}
	if m := in.Persistence; m != nil && !v.native {
		if slices.Contains(m.Of(PathPerLaunch), "/tmp") {
			parts = append(parts, "`/tmp` does not.")
		}
		if m.HomeIsDurable(v.home) {
			parts = append(parts, "Your home is writable and kept for this workspace.")
		} else {
			parts = append(parts, "Most of home is read-only.")
		}
	}
	yolo := "`" + v.workspace + "/.yolo`"
	if v.native {
		yolo = "`.yolo`"
	}
	parts = append(parts, "Never move or delete "+yolo+" or `git clean -x` the workspace.")
	return "- **What survives a restart**: " + strings.Join(parts, " ")
}

func coreNetworkLine(in BriefingInput, v briefingView) string {
	switch {
	case v.netMode == "host":
		return "- **Network**: host networking: `localhost` is the host's own."
	case in.Sealed:
		return "- **Network**: the runtime's own bridge, with nothing of the host's forwarded in; outbound works, for dependencies."
	}
	s := "- **Network**: `localhost` is the jail's own. The host is `host.containers.internal`."
	var fwd []string
	for _, entry := range in.ForwardHostPorts {
		lp, hp, kind := portEntry(entry)
		switch kind {
		case portInt, portPlain:
			fwd = append(fwd, lp)
		case portMapped:
			fwd = append(fwd, lp+"→"+hp)
		}
	}
	switch {
	case len(fwd) > corePortListMax:
		s += " " + strconv.Itoa(len(fwd)) + " host ports are forwarded to `localhost` here; the network topic lists them."
	case len(fwd) > 0:
		s += " Host ports forwarded to `localhost` here: " + strings.Join(fwd, ", ") + "."
	}
	var pub []string
	for _, entry := range in.PublishPorts {
		if hp, jp, ok := publishEntry(entry); ok {
			pub = append(pub, jp+"→"+hp)
		}
	}
	switch {
	case len(pub) > corePortListMax:
		s += " " + strconv.Itoa(len(pub)) + " of your ports are published to the host; the network topic lists them."
	case len(pub) > 0:
		s += " Your ports published to the host (jail→host, bind `0.0.0.0`): " + strings.Join(pub, ", ") + "."
	}
	return s
}

func coreNotInHereLine(in BriefingInput) string {
	s := "- **Not in here**: the host's credentials, sudo, and the host's home."
	if len(in.ContextMounts) > 0 {
		// The variable where the context dir is not `/ctx`: on a native backend it is a long path
		// under the home, and the mounts topic spells it out.
		where := "`" + paths.ContainerContextDir + "`"
		if in.ContextDir != "" && in.ContextDir != paths.ContainerContextDir {
			where = "`$" + paths.ContextDirEnv + "`"
		}
		s += " Host directories shared with you are under " + where + "."
	}
	return s
}

// coreBlockedLine names each block's replacement and puts every guard of one kind in one clause,
// so the line grows by a name per tool, never by a message. The `rg -r` trap is said only when a
// block names `rg` (BD-D7).
func coreBlockedLine(tools []BlockedTool) string {
	if len(tools) == 0 {
		return ""
	}
	var blocks []string
	guards := map[string][]string{}
	var guardKinds []string
	for _, t := range tools {
		if t.Guard != "" {
			if _, ok := guards[t.Guard]; !ok {
				guardKinds = append(guardKinds, t.Guard)
			}
			guards[t.Guard] = append(guards[t.Guard], "`"+t.Name+"`")
			continue
		}
		b := "`" + t.Name + "`"
		switch {
		case t.Replacement == "rg":
			b += " (use `rg`, where `-r` means replace)"
		case t.Replacement != "":
			b += " (use `" + t.Replacement + "`)"
		}
		blocks = append(blocks, b)
	}
	var clauses []string
	if len(blocks) > 0 {
		clauses = append(clauses, joinAnd(blocks)+" are blocked or narrowed")
		if len(blocks) == 1 {
			clauses[0] = blocks[0] + " is blocked or narrowed"
		}
	}
	for _, kind := range guardKinds {
		if kind == "workspace-state" {
			clauses = append(clauses, joinAnd(guards[kind])+" refuse to touch `.yolo`")
		} else {
			clauses = append(clauses, joinAnd(guards[kind])+" are guarded ("+kind+")")
		}
	}
	return "- **Blocked here**: " + strings.Join(clauses, "; ") + "."
}

// joinAnd renders names as a sentence list: "a", "a and b", "a, b and c".
func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// corePointer is the core's last line: the one place it names the skill that holds the rest
// (BD-D6). It names the destination agent's own SKILL.md when there is one, and lists the topics
// this jail has, so an agent can match a question to a file before opening anything. With the
// topics inlined it points down the file instead.
func corePointer(in BriefingInput, topics []BriefingTopic) string {
	s := "Read the **" + BuiltinSkillName(in.ShippedSkillPrefix, mechanicsSkill) + "** skill"
	if in.MechanicsSkillPath != "" && !in.InlineTopics {
		s += " (`" + in.MechanicsSkillPath + "`)"
	}
	s += " before you edit config, ask for a restart, or diagnose a refusal."
	if in.InlineTopics {
		return s + " The sections below hold the rest of this jail's facts."
	}
	names := make([]string, 0, len(topics))
	for _, t := range topics {
		names = append(names, t.Name)
	}
	return s + " Its topic files: " + strings.Join(names, ", ") + "."
}
