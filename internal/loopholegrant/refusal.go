package loopholegrant

import "strings"

// Refusal is what a mediated client prints when its daemon refuses for want of a grant
// (docs/reference/loophole-grants.md §5). The daemon fills it from its grant file and
// the client prints Text; every client renders through here, so the wording is §5's for all.
type Refusal struct {
	Client    string // the in-jail command, "yolo-journalctl"
	Pack      string // the pack whose loophole refused, "journal"
	Access    string // the manifest's `access`, "read the host's journal"
	Kind      string // the grant kind the call needed, "read"
	ValueNoun string // a valued kind's noun, "device globs"; the placeholder when Values is empty
	Dir       string // the host path of the workspace, for the host-run line
	Values    []string
	Standing  Standing
	// Inert is the inert note's reason; when set, the loophole does nothing on this setup and
	// that is all the refusal says.
	Inert string
}

// Text is the refusal, newline-terminated. A standing that is absent or unknown fails closed
// with the not_allowed text (plan PD-12).
func (r Refusal) Text() string {
	if r.Inert != "" {
		return r.Client + ": the " + r.Pack + " loophole does nothing on this setup (" + r.Inert +
			"); nothing is missing to install.\n"
	}
	request := "yolo request " + r.Pack + " " + r.Kind + r.valueArgs()
	apply := "  The human runs, on the host, in this workspace:  yolo config apply   and answers y.\n"
	switch r.Standing {
	case Requestable:
		return r.Client + ": this project holds no grant to " + r.Access + ", so nothing ran.\n" +
			"  The " + r.Pack + " pack is selected and its service is running; it is waiting on a grant.\n" +
			"  Ask for this project:   " + request + "\n" +
			"  then the human runs, on the host, in this workspace:  yolo config apply   and answers y.\n"
	case Pending:
		return r.Client + ": this project's request for " + r.Kind + r.valueList() +
			" is not approved yet, so nothing ran.\n" + apply
	case Granted:
		// Not a §5 shape: the kind is granted, but not for the value this call named (a process
		// or a device outside the approved list). The step is a request for the value.
		cover := r.valueList()
		if cover == "" {
			cover = " this call"
		}
		return r.Client + ": this project's grant to " + r.Access + " does not cover" + cover +
			", so nothing ran.\n" +
			"  Ask for this project:   " + request + "\n" +
			"  then the human runs, on the host, in this workspace:  yolo config apply   and answers y.\n"
	}
	allow := "yolo allow " + r.Pack + " on_request " + r.Kind
	var b strings.Builder
	b.WriteString(r.Client + ": this project holds no grant to " + r.Access + ", so nothing ran.\n")
	b.WriteString("  Your user config does not allow " + r.Pack + " " + r.Kind + " on request, so a request would be skipped.\n")
	b.WriteString("  The human allows it, on the host:   " + allow + "\n")
	if r.Dir != "" {
		b.WriteString("    (or:  yolo host-run -C " + r.Dir + " --name allow-" + r.Pack + " -- " + allow + ")\n")
	}
	b.WriteString("  then this project asks:             " + request + "\n")
	b.WriteString("  then the human runs `yolo config apply` on the host, in this workspace, and answers y.\n")
	return b.String()
}

// valueArgs is the request line's value words: the values, or the noun as a placeholder for a
// valued kind with none in hand.
func (r Refusal) valueArgs() string {
	if len(r.Values) > 0 {
		return " " + strings.Join(r.Values, " ")
	}
	if r.ValueNoun != "" {
		return " <" + r.ValueNoun + ">"
	}
	return ""
}

// valueList is the values as prose, "postgres, redis-server", the gate lines' spelling.
func (r Refusal) valueList() string {
	if len(r.Values) == 0 {
		return ""
	}
	return " " + strings.Join(r.Values, ", ")
}

// RefusedExit is the exit status of every grant refusal and of the inert line, in a daemon and
// in its client alike (docs/reference/loophole-grants.md, PD-7): a caller tells "no
// grant here" from a usage error (2) or a failed read (1) by status alone.
const RefusedExit = 64
