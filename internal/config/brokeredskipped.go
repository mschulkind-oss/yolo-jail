package config

// brokeredskipped.go reports a project's broker request that the user's ceiling leaves out
// (docs/design/config-user.md §6; docs/design/github-write-scopes.md §5): a
// `requests.<source>.<capability>` a selected broker pack declares, with neither
// `allow.<source>.always` nor `allow.<source>.on_request` naming the capability. Such a request is
// skipped, never refused (RC-D12), and the skip is said where the human looks: `yolo check`, every
// launch and `yolo config apply` (through ValidateConfig's warnings), and the approval gate's
// block. Each names, alone on its line, the command that allows it, then the step that approves it.
// The loophole-grant counterpart is skippedLoopholeRequestMessage.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// BrokeredSkippedRequest is one project request outside the user's ceiling: the source as the
// project wrote it under `requests`, the capability, and the values a valued capability carries.
type BrokeredSkippedRequest struct {
	Source, Capability string
	Values             []string
}

// AllowCommand is the command that puts the capability in the user's ceiling.
func (s BrokeredSkippedRequest) AllowCommand() string {
	return "yolo allow " + s.Source + " on_request " + s.Capability
}

// what is the request as the reports name it: source, capability and values, each escaped for a
// terminal, since the project file is the agent's to write.
func (s BrokeredSkippedRequest) what() string {
	out := termsafe.Visible(s.Source) + " " + termsafe.Visible(s.Capability)
	if len(s.Values) > 0 {
		shown := make([]string, len(s.Values))
		for i, v := range s.Values {
			shown[i] = termsafe.Visible(v)
		}
		out += " " + strings.Join(shown, ", ")
	}
	return out
}

// brokeredSkippedRequests are the requests the plan skipped for the ceiling alone, in the
// plan's order. A request skipped for another reason (an empty list, no read grant, no
// repository) is not one: no `yolo allow … on_request` line would bring it in.
func brokeredSkippedRequests(requests []ReadableRequestResult) []BrokeredSkippedRequest {
	var out []BrokeredSkippedRequest
	for _, r := range requests {
		if r.Status != ReadableRequestSkipped || r.Capability == "repos" {
			continue
		}
		source, capability, ok := strings.Cut(strings.TrimPrefix(r.Path, "requests."), ".")
		if !ok || capability != r.Capability || r.Reason != outsideCeilingReason(source) {
			continue
		}
		var values []string
		if list, isList := r.Value.([]any); isList {
			for _, v := range list {
				if s, isString := v.(string); isString {
					values = append(values, s)
				}
			}
		}
		out = append(out, BrokeredSkippedRequest{Source: source, Capability: capability, Values: values})
	}
	return out
}

// brokeredSkippedRequestMessage is `yolo check`'s (and a launch's) text for one skipped request,
// the loophole message's shape with the approval step after the allow line.
func brokeredSkippedRequestMessage(s BrokeredSkippedRequest) string {
	return fmt.Sprintf("this project requests %s.\n"+
		"  Your config does not allow %s on request, so this project runs without it.\n"+
		"  To allow it for every project you work on (in your config, not this file), run on the host:\n"+
		"    %s\n"+
		"  then approve it for this workspace, if that command did not already ask:\n"+
		"    yolo config apply", s.what(), termsafe.Visible(s.Capability), termsafe.Visible(s.AllowCommand()))
}

// brokeredSkippedHeading is the gate block's heading for the skipped requests.
const brokeredSkippedHeading = "Broker requests this project makes that your config skips:"

// brokeredSkippedGateLines are the gate's lines for the skipped requests of the scope in play:
// each request, the allow command alone on its line, then the approval step once. Nil when none.
func brokeredSkippedGateLines(scope *ScopeCheck) []string {
	if scope == nil || scope.ReadableInput == nil {
		return nil
	}
	skipped := brokeredSkippedRequests(scope.ReadableInput.Requests)
	if len(skipped) == 0 {
		return nil
	}
	out := []string{brokeredSkippedHeading}
	for _, s := range skipped {
		out = append(out, "  – "+s.what()+" (not allowed on request by your config, so this project runs without it); to allow it, run on the host:",
			"      "+termsafe.Visible(s.AllowCommand()))
	}
	return append(out, "  then approve it for this workspace:", "      yolo config apply")
}

// validateBrokeredRequests warns for each project broker request the user's ceiling leaves out,
// located at the line it was written on. Nothing in a jail: `allow` is never inherited into the
// jail's user config (inherit.go), so there every request would read as outside a ceiling the
// jail cannot see; the host's check, launch and `yolo config apply` judge it. A legacy user config (`brokered`, no `allow`) is judged
// by the legacy reader, as `yolo request` judges it; nothing to report then. Every read that
// fails is reported by its own validator, so this one says nothing about it.
func validateBrokeredRequests(config *jsonx.OrderedMap, workspace string, warns *[]string, edit ...*editValidationContext) {
	if _, hasRequests := config.Get("requests"); !hasRequests || inJail() {
		return
	}
	var packs []*packload.Pack
	var user, ws *jsonx.OrderedMap
	var wsNode *srcNode
	if len(edit) != 0 && edit[0] != nil {
		packs = edit[0].packs
		user, ws, wsNode = edit[0].rawUser, edit[0].rawWorkspace, edit[0].workspaceOrigins.node()
	} else {
		packs, _ = resolveSelectedPacks()
		var err error
		user, _, err = LoadReadableUserScopeConfigWithSources(false, func(string) {})
		if err != nil {
			return
		}
		ws, wsNode, err = loadMountsWorkspaceConfig(workspace)
		if err != nil {
			return
		}
	}
	if user == nil || ws == nil {
		return
	}
	if _, hasAllow := user.Get("allow"); !hasAllow {
		if _, legacy := user.Get(brokeredKey); legacy {
			return
		}
	}
	brokers, settings, err := ReadableDeclarationsForPacks(nonNilPacks(packs))
	if err != nil || len(brokers) == 0 {
		return
	}
	selected := make([]string, 0, len(packs))
	for _, p := range nonNilPacks(packs) {
		selected = append(selected, p.Name)
	}
	plan, err := ResolveReadableAllowance(ReadableAllowanceInput{User: user, Workspace: ws,
		SelectedPacks: selected, Declarations: brokers, PackSettings: settings})
	if err != nil {
		return // the readable allowance's own validation reports it
	}
	for _, s := range brokeredSkippedRequests(plan.Requests) {
		add(warns, lineLocated(wsNode, brokeredSkippedRequestMessage(s), "requests", s.Source, s.Capability))
	}
}

// nonNilPacks is the packs with a loaded, named declaration, the set
// ReadableDeclarationsForPacks takes.
func nonNilPacks(packs []*packload.Pack) []*packload.Pack {
	out := make([]*packload.Pack, 0, len(packs))
	for _, p := range packs {
		if p != nil && p.Decl != nil && p.Name != "" {
			out = append(out, p)
		}
	}
	return out
}
