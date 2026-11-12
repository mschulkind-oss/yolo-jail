package cli

// requestrepos.go is the repository form of `yolo request` (GW-D77): `yolo request repos <alias>
// <host>/<path> [--local]` declares a named repository for the project, and
// `yolo request repos.<alias>.<broker> <capability> [resource]` requests a capability of a broker
// on that repository (docs/design/github-repository-binding-plan.md, Task 5). `repos` is a word of
// its own, read ahead of every source and pack, so it never reaches the broker lookup.

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// isReposWord reports whether a verb's first word is the repository form: `repos`, or
// `repos.<alias>` with an optional `.<broker>`.
func isReposWord(word string) bool {
	return word == "repos" || strings.HasPrefix(word, "repos.")
}

// splitReposWord splits `repos.<alias>` or `repos.<alias>.<broker>` into its alias and broker; the
// broker is "" for the alias alone.
func splitReposWord(word string) (alias, broker string) {
	alias, broker, _ = strings.Cut(strings.TrimPrefix(word, "repos."), ".")
	return alias, broker
}

// repoAliasRefusal is the refusal for a name a `[repos.<name>]` table cannot take, or "" for a
// name it takes. example is the command that shows the right form.
func repoAliasRefusal(alias, example string) string {
	switch {
	case alias == "all":
		return fmt.Sprintf("yolo: all is not a name: it is the every-broker word of the ceiling; choose another name.\n"+
			"  For example:  %s\n", example)
	case !bareConfigKey.MatchString(alias):
		return fmt.Sprintf("yolo: %q is not a name: use letters, digits, - or _.\n"+
			"  For example:  %s\n", alias, example)
	}
	return ""
}

// namedRepoLine checks a repository written as <host>/<owner>/<repo>, or <host>/<owner>/* with a
// whole owner or repository segment as a wildcard, and returns it. The host is never a wildcard.
// The refusal is "" when value is good; otherwise it names the fix and the example.
func namedRepoLine(value, example string) (line, refusal string) {
	host, rest, ok := strings.Cut(value, "/")
	fail := func(problem string) (string, string) {
		return "", problem + "\n  For example:  " + example + "\n"
	}
	switch {
	case !ok:
		return fail(fmt.Sprintf("%q names no repository: write the host, then OWNER/REPO, such as github.com/acme/infra.", value))
	case !strings.Contains(rest, "/"):
		return fail(fmt.Sprintf("%q has no host: write the host before OWNER/REPO, such as github.com/%s.", value, value))
	case host == "" || strings.Contains(host, "*"):
		return fail(fmt.Sprintf("%q: the host is never a wildcard or empty: write the host itself, such as github.com/acme/infra.", value))
	case brokerscope.ValidRepo(rest):
		return value, ""
	case brokerscope.ValidPattern(rest) && strings.Contains(rest, "*"):
		return value, ""
	case strings.Contains(rest, "*"):
		return fail(fmt.Sprintf("%q: a * stands for a whole owner or repository segment, such as github.com/acme/*.", value))
	}
	return fail(fmt.Sprintf("%q: write OWNER/REPO after the host, such as github.com/acme/infra.", value))
}

// requestReposRun dispatches the repository form: the declaration `repos <alias> <line>`, or a
// request under `repos.<alias>.<broker>`.
func requestReposRun(workspace string, positionals []string, local, dryRun bool, out, errw io.Writer) int {
	if positionals[0] == "repos" {
		return requestReposDeclare(workspace, positionals[1:], local, dryRun, out, errw)
	}
	if local {
		fmt.Fprintf(errw, "yolo request: --local applies to `yolo request repos <alias> <host>/<path>` only.\n\n%s\n", requestUsage)
		return 2
	}
	return requestReposNamed(workspace, positionals, dryRun, out, errw)
}

// requestReposDeclare writes `[repos.<alias>] repo = "<host>/<path>"` into the project file, or
// the workspace's local file with --local. Nothing else is written: a request is its own verb.
func requestReposDeclare(workspace string, args []string, local, dryRun bool, out, errw io.Writer) int {
	if len(args) != 2 {
		fmt.Fprint(errw, "yolo request repos: takes a name and a repository: yolo request repos <alias> <host>/<path> [--local]\n"+
			"  For example:  yolo request repos infra github.com/acme/infra\n")
		return 2
	}
	alias, value := args[0], args[1]
	example := "yolo request repos " + alias + " github.com/acme/infra"
	if msg := repoAliasRefusal(alias, example); msg != "" {
		fmt.Fprint(errw, msg)
		return 1
	}
	line, refusal := namedRepoLine(value, example)
	if refusal != "" {
		fmt.Fprint(errw, "yolo request repos: "+refusal)
		return 1
	}
	if workspace == "" {
		workspace = configEditWorkspace()
	}
	choice := config.EditTargetChoice{Kind: config.EditTargetProject}
	if local {
		choice.Kind = config.EditTargetLocal
	}
	rc := namedVerbEditUnless(requestConvertFirst, workspace, "repos."+configKeyComponent(alias)+".repo",
		configedit.Set, line, choice, dryRun, !dryRun, out, errw)
	if dryRun {
		return rc
	}
	if rc != 0 && !requestWritten(workspace, []string{"repos", alias, "repo"}, line) {
		return rc
	}
	fmt.Fprintln(out, requestApprovalLine(false))
	return rc
}

// requestReposNamed writes one request under `repos.<alias>.requests.<broker>`, after checking it
// the way the unqualified request is checked. Its capability above the user's ceiling is written
// and reported skipped, naming the scoped allow first.
func requestReposNamed(workspace string, positionals []string, dryRun bool, out, errw io.Writer) int {
	word := positionals[0]
	alias, source := splitReposWord(word)
	if len(positionals) > 3 {
		fmt.Fprintln(errw, requestUsage)
		return 2
	}
	capability := positionals[1]
	resource := ""
	if len(positionals) == 3 {
		resource = positionals[2]
	}
	example := "yolo request repos <alias> <host>/<path>"

	brokers, packSettings, err := allowSelectedBrokers()
	if err != nil {
		fmt.Fprintf(errw, "yolo request: cannot read what the selected packs declare: %v\n"+
			"  Run `yolo check` to see which pack is broken.\n", err)
		return 1
	}
	if source == "" {
		return refuseReposAliasForm(alias, capability, positionals[2:], brokers, errw)
	}
	decl, rc := findDeclaredBroker("request", source, brokers, packSettings, errw)
	if rc != 0 {
		return rc
	}
	capability = config.CanonicalCapability(source, capability)
	subject := "repos." + alias + "." + source
	if capability == "repos" {
		fmt.Fprintf(errw, "yolo request: %s takes no repository list: a repository is named by its own line.\n"+
			"  For example:  %s\n", subject, example)
		return 1
	}
	if alias == "origin" && decl.Brokered.UnqualifiedRequests == loopholedecl.UnqualifiedRequestsOrigin {
		fmt.Fprintf(errw, "yolo request: repos.origin.%s: origin's %s requests have one home, the unqualified request:\n"+
			"  yolo request %s %s\n", source, source, source, capability)
		return 1
	}
	if decl.Brokered.RemoteHost == brokerscope.AnyRemoteHost {
		fmt.Fprintf(errw, "yolo request: %s reaches this workspace's remotes on any host, so a request under a name waits for a later slice.\n"+
			"  Request it unqualified:  yolo request %s %s\n", source, source, capability)
		return 1
	}
	if !workspaceNamesRepo(workspace, alias, decl.Brokered.RemoteHost) {
		fmt.Fprintf(errw, "yolo request: repos.%s names no repository in this workspace. Declare it first:\n"+
			"  yolo request repos %s <host>/<path>\n", alias, alias)
		return 1
	}

	verb, value, refusal := requestCapabilityEdit(decl, subject, capability, resource)
	if refusal != "" {
		fmt.Fprint(errw, refusal)
		return 1
	}
	if workspace == "" {
		workspace = configEditWorkspace()
	}
	key := "repos." + configKeyComponent(alias) + ".requests." + configKeyComponent(source) + "." + configKeyComponent(capability)
	project := config.EditTargetChoice{Kind: config.EditTargetProject}
	rc = requestWriteEdit(workspace, key, verb, value, project, dryRun, out, errw)
	if dryRun {
		return rc
	}
	keys := []string{"repos", alias, "requests", source, capability}
	if rc != 0 && !requestWritten(workspace, keys, value) {
		return rc
	}
	skipped := !requestNamedWithinCeiling(workspace, alias, source, decl.Brokered.RemoteHost, capability)
	if skipped {
		reportNamedRequestSkipped(out, workspaceRepoLine(workspace, alias), alias, source, capability,
			requestDescription(capability, value))
	}
	fmt.Fprintln(out, requestApprovalLine(skipped))
	return rc
}

// refuseReposAliasForm refuses `repos.<alias>` with no broker: a capability with no repository
// under it is the unqualified request, which names the broker that declares it.
func refuseReposAliasForm(alias, capability string, rest []string, brokers []config.ReadableBrokerDeclaration, errw io.Writer) int {
	request := strings.Join(append([]string{capability}, rest...), " ")
	for _, b := range brokers {
		if slices.Contains(declaredCapabilities(b), capability) {
			fmt.Fprintf(errw, "yolo request: repos.%s %s is not built yet: a Git line under a name waits for a later slice.\n"+
				"  Request it unqualified:  yolo request %s %s\n", alias, request, b.Brokered.Source, request)
			return 1
		}
	}
	fmt.Fprintf(errw, "yolo request: repos.%s names a repository, not a broker. Request a broker's capability on it:\n"+
		"  For example:  yolo request repos.%s.<broker> %s\n", alias, alias, capability)
	return 1
}

// workspaceNamedRepos is the `repos` table of the workspace's own files, project and local, nil
// when it has none or the files do not read. The user config's `[repos.<name>]` tables are never
// in it: a user's name is a ceiling, not an identity a workspace line resolves (GW-D73).
func workspaceNamedRepos(workspace string) *jsonx.OrderedMap {
	read, err := config.ReadWorkspaceForGate(workspace)
	if err != nil {
		return nil
	}
	repos, _ := configValueAt(read.Config, "repos")
	table, _ := repos.(*jsonx.OrderedMap)
	return table
}

// workspaceNamesRepo reports whether alias names a repository this workspace declares in its own
// files, or a remote of its clone on host (a name with no repo line takes that remote).
func workspaceNamesRepo(workspace, alias, host string) bool {
	if _, ok := configValueAt(workspaceNamedRepos(workspace), alias); ok {
		return true
	}
	for _, r := range brokerscope.ReadRemotes(workspace, host).Remotes {
		if r.Name == alias {
			return true
		}
	}
	return false
}

// workspaceRepoLine is the repo line the workspace's own [repos.<alias>] names, "" with none.
func workspaceRepoLine(workspace, alias string) string {
	line, _ := configValueAt(workspaceNamedRepos(workspace), alias, "repo")
	text, _ := line.(string)
	return text
}

// workspaceRepoIdentity is the repository alias names in this workspace, as a repo line: its own
// table's line, or `<host>/<owner>/<repo>` of the clone's remote of that name; "" with neither.
func workspaceRepoIdentity(workspace, alias, host string) string {
	if line := workspaceRepoLine(workspace, alias); line != "" {
		return line
	}
	for _, r := range brokerscope.ReadRemotes(workspace, host).Remotes {
		if r.Name == alias {
			return host + "/" + r.Repo
		}
	}
	return ""
}

// requestNamedWithinCeiling reports whether the user config allows the capability on the repository
// alias names: the broad allow, which covers every repository, or a user
// `[repos.<name>.allow.<broker>]` table whose repo line admits that repository, whatever its name
// (GW-D64, config.ScopedCeilingAllowsRequest).
func requestNamedWithinCeiling(workspace, alias, source, host, capability string) bool {
	if requestWithinCeiling(workspace, source, capability) {
		return true
	}
	return config.ScopedCeilingAllowsRequest(source, workspaceRepoIdentity(workspace, alias, host), capability)
}

// reportNamedRequestSkipped says a request under a name is outside the user's ceiling, so the project
// runs without it, and names the scoped allow first: the broad one would allow it on every repository.
func reportNamedRequestSkipped(out io.Writer, line, alias, source, capability, description string) {
	if line == "" {
		line = "<host>/<path>"
	}
	fmt.Fprintf(out, "This project requests %s %s for repos.%s, but your user config does not allow %s on request for that repository, "+
		"so this project runs without it.\n"+
		"  To allow it for that repository (in your config, not this file), name the repository, then allow the capability:\n"+
		"    yolo allow repos <alias> %s\n"+
		"    yolo allow repos.<alias>.%s on_request %s\n"+
		"  To allow it for every repository instead:\n"+
		"    yolo allow %s on_request %s\n",
		source, description, alias, capability, line, source, capability, source, capability)
	if config.InJail() {
		fmt.Fprintln(out, "  (From this jail: the human runs it directly in a host terminal, not through `yolo host-run`.)")
	}
}
