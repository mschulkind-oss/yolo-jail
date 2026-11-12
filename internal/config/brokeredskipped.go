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
// Under a named `[repos.<name>]` table (Table is the name) the request is scoped to that table's
// repository: Repository and UserRepository are the repo lines the workspace's table and the
// user's table of the same name name, "" where a config names none.
type BrokeredSkippedRequest struct {
	Source, Capability string
	Values             []string
	Table              string
	Repository         string
	UserRepository     string
}

// sourceName is the request's source as the project wrote it: `repos.<name>.<source>` under a name.
func (s BrokeredSkippedRequest) sourceName() string {
	if s.Table != "" {
		return "repos." + s.Table + "." + s.Source
	}
	return s.Source
}

// AllowCommand is the command that puts the capability in the user's ceiling: for a named table,
// the scoped ceiling of that name, so the broad one (every repository) is never offered for it.
func (s BrokeredSkippedRequest) AllowCommand() string {
	return "yolo allow " + s.sourceName() + " on_request " + s.Capability
}

// conflictNote says a name the workspace and the user both give names two repositories, so the
// report offers no remedy for it: writing the user's name again would retarget it (GW-D76).
func (s BrokeredSkippedRequest) conflictNote() string {
	if s.Table == "" || s.Repository == "" || s.UserRepository == "" || s.Repository == s.UserRepository || repoLineCovers(s.UserRepository, s.Repository) {
		return ""
	}
	return fmt.Sprintf("repos.%s is your config's name for %s, not %s: name this project's repository another name",
		termsafe.Visible(s.Table), termsafe.Visible(s.UserRepository), termsafe.Visible(s.Repository))
}

// repoLineCovers reports whether the user's repo line admits repository by host and whole segments,
// the rule a scoped ceiling admits a repository by (GW-D80): a pattern covers, a name differing only in case is the same.
func repoLineCovers(userLine, repository string) bool {
	host := namedRepoHost(userLine)
	return scopedCeilingAdmits(ReadableScopedCeiling{Host: host, Repository: strings.TrimPrefix(userLine, host+"/")},
		ReadableRepository{RemoteHost: namedRepoHost(repository), Repository: strings.TrimPrefix(repository, namedRepoHost(repository)+"/")})
}

// remedies are the commands the report offers, the scoped allow first. A named request whose user
// config declares no repository for its name also gets `yolo allow repos <name> <repository>`.
func (s BrokeredSkippedRequest) remedies() []string {
	if s.conflictNote() != "" {
		return nil
	}
	out := []string{s.AllowCommand()}
	if s.Table != "" && s.Repository != "" && s.UserRepository == "" {
		out = append(out, "yolo allow repos "+s.Table+" "+s.Repository)
	}
	return out
}

// locationKeys are the config keys the request is written under, for the line it was written on.
func (s BrokeredSkippedRequest) locationKeys() []string {
	if s.Table != "" {
		return []string{"repos", s.Table, "requests", s.Source, s.Capability}
	}
	return []string{"requests", s.Source, s.Capability}
}

// what is the request as the reports name it: source, capability and values, each escaped for a
// terminal, since the project file is the agent's to write.
func (s BrokeredSkippedRequest) what() string {
	out := termsafe.Visible(s.sourceName()) + " " + termsafe.Visible(s.Capability)
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
		source := r.Source
		if r.Table == "" { // an older producer sets no source: the authored key says it
			var capability string
			var ok bool
			source, capability, ok = strings.Cut(strings.TrimPrefix(r.Path, "requests."), ".")
			if !ok || capability != r.Capability {
				continue
			}
		}
		if r.Reason != outsideCeilingReason(source) {
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
		out = append(out, BrokeredSkippedRequest{Source: source, Capability: r.Capability, Values: values,
			Table: r.Table, Repository: r.Repository, UserRepository: r.UserRepository})
	}
	return out
}

// brokeredSkippedRequestMessage is `yolo check`'s (and a launch's) text for one skipped request,
// the loophole message's shape with the approval step after the allow line.
func brokeredSkippedRequestMessage(s BrokeredSkippedRequest) string {
	head := fmt.Sprintf("this project requests %s.\n  Your config does not allow %s on request, so this project runs without it.\n",
		s.what(), termsafe.Visible(s.Capability))
	if note := s.conflictNote(); note != "" {
		return head + "  " + note
	}
	scope := "for every project you work on (in your config, not this file)"
	if s.Table != "" {
		scope = "on this repository"
	}
	var b strings.Builder
	b.WriteString(head + "  To allow it " + scope + ", run on the host:\n")
	for _, c := range s.remedies() {
		b.WriteString("    " + termsafe.Visible(c) + "\n")
	}
	b.WriteString("  then approve it for this workspace, if that command did not already ask:\n    yolo config apply")
	return b.String()
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
		if note := s.conflictNote(); note != "" {
			out = append(out, "  – "+s.what()+" (not allowed on request by your config, so this project runs without it); "+note)
			continue
		}
		out = append(out, "  – "+s.what()+" (not allowed on request by your config, so this project runs without it); to allow it, run on the host:")
		for _, c := range s.remedies() {
			out = append(out, "      "+termsafe.Visible(c))
		}
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
	_, hasRequests := config.Get("requests")
	_, hasNamed := config.Get("repos")
	if !hasRequests && !hasNamed || inJail() {
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
		add(warns, lineLocated(wsNode, brokeredSkippedRequestMessage(s), s.locationKeys()...))
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
