package hostservice

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// launchcheck.go is the daemon side of the LAUNCH CHECK, a term coined here: the one framed
// request a launch sends a host daemon whose manifest declares `host_daemon.launch_check`
// (internal/loopholedecl), right after starting or ensuring it, asking what this launch should
// warn the human about. An attach to a running jail sends the same request through the front
// that jail's launch published, and starts nothing. The request is
//
//	{"action": "launch-check", "budget_ms": <n>}
//
// and the answer, on stdout with exit 0, is one JSON object:
//
//	{"warnings": ["…"], "notes": ["…"]}
//
// either list absent or empty when there is nothing to say. The launch prints each warning,
// prefixed `loophole <name>: `, as a yellow line, and each note as a dim one, and proceeds: no
// answer refuses a launch. It is a DISCLOSURE, so no flag hides what it prints
// (docs/reference/report-tiers.md, OQ-RO3).
//
// The launch sends it through the endpoint it just published for the daemon, dialled as a
// host-side client (svcendpoint.DialLocal), so the request crosses the same front and carries
// the same connection preamble as a jail's.
//
// # The budget is the launch's, and the daemon keeps it
//
// `budget_ms` is how long the launch will wait for the answer. The daemon must answer within
// it, from what it already knows when that is enough, and with what it knew before when it is
// not: a launch check is not allowed to hold a launch for a slow upstream. The launch reads for
// a short margin past the budget and then gives up, printing a note. The daemon clamps the
// value to LaunchCheckBudgetCap, so a caller cannot park a goroutine for long.
//
// # A daemon that does not know the action
//
// The action-protocol handlers of the credential daemons (aws-auth's, openai-auth's and the
// Claude broker's) answer an action they do not know with a non-zero exit and
// `unknown action: <action>` on stderr, and a host-wide daemon outlives the yolo that started
// it: nothing restarts one on an upgrade. So a launch reads that answer to its launch check
// (IsUnknownLaunchCheck) as a daemon that lacks the check rather than as a check that merely
// failed, and a fresh launch REFUSES, naming the restart command, when the daemon is host-wide
// and an older yolo started it (internal/cli/run/launchcheck.go; OQ-HD11, HD-D5 and HD-D6 in
// docs/design/host-daemon-ownership.md).
//
// First consumer: packs/aws-auth, whose daemon answers with the mint failure that would fail
// the jail's first Bedrock request (docs/design/sso-backed-bedrock.md SSO-D1).

// LaunchCheckAction is the launch check's `action`.
const LaunchCheckAction = "launch-check"

// LaunchCheckBudgetKey is the request field carrying the launch's budget, in milliseconds.
const LaunchCheckBudgetKey = "budget_ms"

// LaunchCheckBudget is the budget a launch gives each daemon it checks. Two seconds covers one
// `aws` invocation that fails locally or on one round trip, which is what a lapsed SSO session
// or a missing profile costs, and it is paid only when the daemon cannot answer from what it
// already knows.
const LaunchCheckBudget = 2 * time.Second

// LaunchCheckBudgetCap is the most a daemon waits whatever budget it is sent.
const LaunchCheckBudgetCap = 5 * time.Second

// LaunchCheckReport is the launch check's answer.
type LaunchCheckReport struct {
	// Warnings are printed as yellow lines: each names something that will fail for this
	// launch, and what fixes it.
	Warnings []string `json:"warnings,omitempty"`
	// Notes are printed dim: something the check could not settle.
	Notes []string `json:"notes,omitempty"`
}

// AnswerLaunchCheck writes r as the launch check's answer. Through here rather than s.JSON(r):
// the session's encoder (jsonx) takes maps and lists, not structs, and would write nothing.
func (s *Session) AnswerLaunchCheck(r LaunchCheckReport) error {
	body := map[string]any{}
	if len(r.Warnings) > 0 {
		body["warnings"] = r.Warnings
	}
	if len(r.Notes) > 0 {
		body["notes"] = r.Notes
	}
	return s.JSON(body)
}

// LaunchCheckBudgetOf is the budget a launch-check request asked for, clamped to
// (0, LaunchCheckBudgetCap]. A missing or unusable value is LaunchCheckBudget.
func LaunchCheckBudgetOf(s *Session) time.Duration {
	raw, _ := s.Get(LaunchCheckBudgetKey)
	// A decoded integer literal is jsonx's own type, a fraction a float64.
	var ms float64
	if n, ok := jsonx.AsInt(raw); ok {
		ms = float64(n)
	} else if f, ok := raw.(float64); ok {
		ms = f
	}
	if ms <= 0 {
		return LaunchCheckBudget
	}
	budget := time.Duration(ms * float64(time.Millisecond))
	if budget > LaunchCheckBudgetCap {
		return LaunchCheckBudgetCap
	}
	return budget
}

// IsUnknownLaunchCheck reports whether a daemon's first stderr line is its handler's answer to
// an action it does not know, for the launch-check action. The Claude broker quotes the action
// (`unknown action: 'launch-check'`), the other daemons do not, so the test is the prefix and
// the action's name rather than one spelling.
func IsUnknownLaunchCheck(stderrLine string) bool {
	line := strings.TrimSpace(stderrLine)
	return strings.HasPrefix(line, "unknown action:") && strings.Contains(line, LaunchCheckAction)
}
