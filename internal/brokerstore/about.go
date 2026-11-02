package brokerstore

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// About is what a request does, for a person, as the broker that filed it read its command:
// what a notifier shows without parsing the argv. It is shown, never part of Digest.
//
// The fields are of two kinds. Action, Target and Headline are SAFE: yolo builds them from its
// own vocabulary and validated parts (a repository name, a number, a count), so they hold only
// the characters SafeLabel admits and need no escaping in any renderer; File drops one that
// does not. Title and Refs are FREE TEXT the agent chose (a PR title, a branch name, a
// workflow file, an API path), shown as written, made terminal-safe by the readers below.
type About struct {
	// Action is one of the Action* names below.
	Action string `json:",omitempty"`
	// Target is the resource written, such as "PR o/r#70" or "issue creation in o/r".
	Target string `json:",omitempty"`
	// Headline is one short, complete sentence fragment, such as "merge PR o/r#70".
	Headline string `json:",omitempty"`
	// Title is a creation's or merge's --title/--subject, a dispatch's workflow file, or an
	// API call's path.
	Title string `json:",omitempty"`
	// Refs is, for a push, the refs nothing else covered (NeedsApproval); for a workflow
	// dispatch, the ref it names.
	Refs []string `json:",omitempty"`
}

// The actions a request can name: yolo's own words, each only a-z and '-'.
const (
	ActionPush             = "push"
	ActionPRCreate         = "pr-create"
	ActionPRMerge          = "pr-merge"
	ActionPRWrite          = "pr-write"
	ActionIssueCreate      = "issue-create"
	ActionIssueWrite       = "issue-write"
	ActionWorkflowDispatch = "workflow-dispatch"
	ActionRepoWrite        = "repo-write"
	ActionAPI              = "api"
)

// Actions lists every action, for a test that checks each is safe.
var Actions = []string{ActionPush, ActionPRCreate, ActionPRMerge, ActionPRWrite, ActionIssueCreate,
	ActionIssueWrite, ActionWorkflowDispatch, ActionRepoWrite, ActionAPI}

// SafeLabel reports whether s can be shown in any renderer unescaped: printable ASCII
// letters, digits, space and `.,_/:#@+=()-` only. No markup, entity, quote, shell or
// control character, and nothing invisible.
func SafeLabel(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(" .,_/:#@+=()-", r):
		default:
			return false
		}
	}
	return true
}

// safe returns a copy of a whose safe fields that are not SafeLabel are dropped, so no
// broker's mistake puts free text where a notifier expects none.
func (a About) safe() About {
	if !SafeLabel(a.Action) || (a.Action != "" && !isAction(a.Action)) {
		a.Action = ""
	}
	if !SafeLabel(a.Target) {
		a.Target = ""
	}
	if !SafeLabel(a.Headline) {
		a.Headline = ""
	}
	return a
}

func isAction(s string) bool {
	for _, a := range Actions {
		if s == a {
			return true
		}
	}
	return false
}

// Headline is the request's short headline, safe in any renderer: the filing broker's, or,
// for a request filed without one, "a <source> request".
func (s *Store) Headline(q Request) string {
	if h := q.About.safe().Headline; h != "" {
		return h
	}
	if src := s.SourceName(); SafeLabel(src) && src != "" {
		return "a " + src + " request"
	}
	return "a request"
}

// Description is the headline, then the free text a person needs to judge it, each part on
// its own line and made terminal-safe, so an agent's text cannot add a line: `title: ...`,
// then one `ref: ...` per ref. It needs no assembly, but its free text is the agent's words.
func (s *Store) Description(q Request) string {
	lines := []string{s.Headline(q)}
	if t := q.About.Title; t != "" {
		lines = append(lines, "title: "+termsafe.Visible(t))
	}
	for _, r := range q.About.Refs {
		lines = append(lines, "ref: "+termsafe.Visible(r))
	}
	return strings.Join(lines, "\n")
}

// Refs is the request's refs, each terminal-safe, one per line.
func Refs(q Request) string {
	out := make([]string, len(q.About.Refs))
	for i, r := range q.About.Refs {
		out[i] = termsafe.Visible(r)
	}
	return strings.Join(out, "\n")
}

// MarkupEscape replaces the XML metacharacters &, <, >, " and ' with entities, for a
// renderer of the freedesktop notification (Pango) markup, and for that dialect only. A
// Replacer scans once, so the & of an entity it writes is never escaped again.
func MarkupEscape(s string) string { return markupEscaper.Replace(s) }

var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
