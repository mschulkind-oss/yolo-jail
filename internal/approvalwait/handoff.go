package approvalwait

import "strings"

// handoff.go is the ONE renderer of the menu a refusal prints when a request needs the human:
// the git-broker's push refusal, the github-broker's pending write and the jail briefing's
// example all print Menu.Render, so the three cannot drift apart. It lists every path, ordered
// by how long it lasts (once, for a while, for good on this host, for good in the project, then
// doing it on the host), and tags each with the side that runs it, `(host)` or `(here)`. Each
// command stands alone on its own indented line, never inside a sentence, because the human
// copies it rather than reads it.

// Answer is one lasting answer the human may give: what it does, then its flags after
// `yolo approve <id>`.
type Answer struct{ What, Flags string }

// ProjectStep is the project request that makes a path last for good in the project: the
// command run here, and whether the project already holds it, so only `yolo config apply` on
// the host is left.
type ProjectStep struct {
	Command   string
	Requested bool
}

// Menu is one refusal's paths.
type Menu struct {
	// ID is the pending request, "" when none was filed: then the menu has no `yolo approve`
	// answers and no waiter, only the lasting paths and the host fallback.
	ID string
	// Retry is what the agent runs again once the request is allowed, as a verb phrase:
	// "run the same push again".
	Retry string
	// Lasting is the answers that last on this host, in the order shown.
	Lasting []Answer
	// Ceiling is the host commands the human runs before a lasting path can take effect,
	// when the user config does not yet allow the kind; nil when it does.
	Ceiling []string
	// Project is the requests that make the path last for good in the project.
	Project []ProjectStep
	// Fallback is the command that does it on the host instead, "" for none.
	Fallback string
}

// WaitCommand is the in-jail command that waits on request id.
func WaitCommand(id string) string { return "yolo wait " + id }

// ApproveCommand is the host command that answers request id, with flags when given.
func ApproveCommand(id, flags string) string {
	if flags == "" {
		return "yolo approve " + id
	}
	return "yolo approve " + id + " " + flags
}

// ConfigApplyCommand is the host command that approves a project's requests.
const ConfigApplyCommand = "yolo config apply"

// TimedFlags is the "for a while" answer every request takes.
const TimedFlags = "--for 15m"

// Render is the menu, ending in a newline.
func (m Menu) Render() string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	command := func(s string) { line(""); line("    " + s); line("") }
	if m.ID != "" {
		line("Request " + m.ID + " needs the human's approval. Relay every path below. A (host) command")
		line("is the human's, run in any host terminal, directly, not through `yolo host-run`; a (here)")
		line("command is yours, run in this jail.")
		line("")
		line("Once, now (host):")
		command(ApproveCommand(m.ID, ""))
		line("For 15 minutes (host):")
		command(ApproveCommand(m.ID, TimedFlags))
	} else if len(m.Ceiling) > 0 || len(m.Project) > 0 || m.Fallback != "" {
		line("The paths that cover it: a (host) command is the human's, run in any host terminal,")
		line("directly, not through `yolo host-run`; a (here) command is yours, run in this jail.")
		line("")
	}
	if len(m.Ceiling) > 0 && (len(m.Lasting) > 0 || len(m.Project) > 0) {
		line("Before a lasting path takes effect, the host's user config must allow it (host):")
		for _, c := range m.Ceiling {
			command(c)
		}
	}
	if m.ID != "" {
		for _, a := range m.Lasting {
			line(a.What + " (host):")
			command(ApproveCommand(m.ID, a.Flags))
		}
	}
	var asks, requested []string
	for _, p := range m.Project {
		if p.Requested {
			requested = append(requested, p.Command)
		} else {
			asks = append(asks, p.Command)
		}
	}
	if len(asks) > 0 {
		line("For good, in the project: request it (here):")
		for _, c := range asks {
			command(c)
		}
		line("then the human approves the project's change (host):")
		command(ConfigApplyCommand)
	} else if len(requested) > 0 {
		line("For good, in the project: already requested; this finishes it (host):")
		command(ConfigApplyCommand)
	}
	if m.Fallback != "" {
		line("Or do it on the host instead, in this workspace (host):")
		command(m.Fallback)
	}
	if m.ID != "" {
		line("After relaying them, in the same turn, start this (here) in the background, so that its")
		line("exit wakes you:")
		command(WaitCommand(m.ID))
		line("Exit 0: allowed; " + m.Retry + ", once. Exit 75: still unanswered at the timeout; give")
		line("the human the commands again and stop. Any other exit (denied, expired, closed, already")
		line("used): report what it printed, and do not retry.")
	}
	return b.String()
}
