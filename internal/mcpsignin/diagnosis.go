package mcpsignin

import (
	"fmt"
	"strings"
)

// DiagnosisInput contains observations only, never credentials or provider errors.
type DiagnosisInput struct {
	Server, URL                           string
	Once                                  bool
	Busy, BrokerUnreachable               bool
	Record, Outcome, Source               string
	Point                                 *Point
	Agent, Mode, Delivery, DeliveryReason string
	Capturable                            bool
	Callback                              string
	HeaderSource                          bool
}

type Diagnosis struct {
	Verdict  string `json:"verdict"`
	NextStep string `json:"next_step"`
}

// Diagnose is shared by status, launch disclosure and header refusal. Callback
// ownership chooses recovery wording, never the verdict.
func Diagnose(in DiagnosisInput) Diagnosis {
	verdict := "sign-in needed"
	switch {
	case !in.Once:
		verdict = "not one-sign-in here"
	case in.Busy:
		verdict = "busy"
	case in.BrokerUnreachable:
		verdict = "broker unreachable"
	case in.Outcome == "rotated-not-saved":
		verdict = "sign-in lost"
	case in.Record == "dead":
		verdict = "record dead"
	case in.Record == "expired":
		verdict = "record expired"
	case in.Record == "live" && in.Delivery == "delivered plain":
		verdict = "delivered plain"
	case in.Record == "live":
		verdict = "ready"
	case strings.HasPrefix(in.Outcome, "refused-") || in.Outcome == "unsupported-token-type":
		verdict = "adoption refused"
	case in.Source == "malformed" || in.Source == "unsafe" || in.Source == "unavailable":
		verdict = "source unreadable"
	case in.Point != nil && !in.Capturable:
		verdict = "not capturable here"
	case in.Source == "entry awaiting adoption":
		verdict = "awaiting adoption"
	case in.Point == nil:
		verdict = "no sign-in point"
	}
	own := fmt.Sprintf("yolo config set mcp_servers.%s.oauth.client_id <id> --user", in.Server)
	action := own
	if in.Point != nil && in.Point.NativeName != "" {
		action = fmt.Sprintf("In %s, run /mcp and authenticate %s", in.Agent, in.Point.NativeName)
		if in.Callback != "owner" && in.Callback != "not needed" {
			action += "; if the browser cannot reach the agent, paste the full callback URL into its prompt"
		}
	}
	switch verdict {
	case "ready":
		return Diagnosis{verdict, ""}
	case "busy":
		action = "canonical state is busy; retry shortly, or run yolo mcp-auth status"
	case "broker unreachable":
		action = "run yolo mcp-auth status on the host, then relaunch"
	case "not one-sign-in here":
		action = fmt.Sprintf("make it one in your own config: yolo config set mcp_servers.%s.sign_in once --user, then relaunch", in.Server)
	case "no sign-in point":
		action = fmt.Sprintf("no agent here can sign in. Select a pack that delivers a sign-in bundle for %s, or set your own client: %s", in.URL, own)
	case "not capturable here":
		action = fmt.Sprintf("a sign-in made in this launch is not shared (%s). Sign in from a bridged managed %s", in.Mode, in.Agent)
	case "delivered plain":
		action = fmt.Sprintf("signed in; %s lists a second entry because %s; it needs no sign-in", in.Agent, in.DeliveryReason)
	case "awaiting adoption":
		action = "the source entry awaits this launch's broker; retry shortly or run yolo mcp-auth status"
	case "source unreadable":
		action = "source state is unreadable; restore the agent's regular private store, then " + action
	case "sign-in lost":
		action = "renewal succeeded but was not saved; a new sign-in is required. " + action
	case "adoption refused":
		action = "adoption refused (" + in.Outcome + "); " + action
	case "record dead":
		if in.HeaderSource {
			action = "restart the agent to restore its plain native sign-in entry, then " + action
		}
	case "record expired":
		action = "record expired; retry after the session's broker refreshes, or " + action
	}
	return Diagnosis{verdict, in.Server + ": " + action}
}
