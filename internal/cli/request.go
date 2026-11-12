package cli

// request.go is `yolo request`, the named verb for a project's broker request
// (docs/design/config-commands.md §4; the `requests` section is config-user.md §6, RC-D12 and
// RC-D13). It writes the project file, which is the agent's to edit, so it runs in a jail too;
// then it runs `yolo check` and names `yolo config apply`, the host step that approves it.
// A request outside the user's ceiling is reported and skipped, never refused: the project
// file is shared, and the ceiling is each person's own.

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const requestUsage = `usage: yolo request <source> <capability> [resource] [--dry-run]
       yolo request <pack> <kind> [value...] [--dry-run]

Request a broker capability, or a grant kind a loophole pack declares, in this project's config,
run ` + "`yolo check`" + `, and name the host step that approves it (` + "`yolo config apply`" + `). A
request your user config does not allow on request is reported and skipped, never an error.

Examples:
  yolo request git push main            a branch the jail may push
  yolo request git new_branches         the jail may create branches
  yolo request github create_pr
  yolo request github runs nightly.yml  a workflow the jail may run and re-run on the default branch,
                                       and cancel any run
  yolo request github approve_runs 'github-actions[bot]'  an actor whose held runs the jail may approve
  yolo request repos infra github.com/acme/infra   name a repository for this project (--local: this
                                       workspace's local file, not the project file)
  yolo request repos.infra.github runs deploy.yml  the same request, on the repository named infra
  yolo request github repos acme/other  a repository beyond this one's own remotes
  yolo request journal read             the jail may read your user journal
  yolo request host-processes view postgres redis-server  the host processes the jail may see`

func runRequest(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return requestRunW(rest, os.Stdout, os.Stderr)
}

func requestRunW(args []string, out, errw io.Writer) int {
	return requestRunIn("", args, out, errw)
}

// requestRunIn is the verb against a named workspace; "" is the one the verb resolves from where
// it runs.
func requestRunIn(workspace string, args []string, out, errw io.Writer) int {
	dryRun, local := false, false
	var positionals []string
	for _, a := range args {
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, requestUsage)
			return 0
		case a == "--dry-run":
			dryRun = true
		case a == "--local":
			local = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo request: unknown flag %q\n\n%s\n", a, requestUsage)
			return 2
		default:
			positionals = append(positionals, a)
		}
	}
	if len(positionals) < 2 {
		fmt.Fprintln(errw, requestUsage)
		return 2
	}
	// `repos` is the repository form, read ahead of every source and pack (GW-D77).
	if isReposWord(positionals[0]) {
		return requestReposRun(workspace, positionals, local, dryRun, out, errw)
	}
	if local {
		fmt.Fprintf(errw, "yolo request: --local applies to `yolo request repos` only.\n\n%s\n", requestUsage)
		return 2
	}
	source, capability := positionals[0], positionals[1]
	// A former spelling (`dispatch_workflows`) requests the capability that replaced it.
	capability = config.CanonicalCapability(source, capability)

	brokers, packSettings, err := allowSelectedBrokers()
	if err != nil {
		fmt.Fprintf(errw, "yolo request: cannot read what the selected packs declare: %v\n"+
			"  Run `yolo check` to see which pack is broken.\n", err)
		return 1
	}
	if grants, ok := findGrantDeclaringPack(source, packSettings); ok {
		return requestLoopholeKind(workspace, grants, capability, positionals[2:], dryRun, out, errw)
	}
	if len(positionals) > 3 {
		fmt.Fprintln(errw, requestUsage)
		return 2
	}
	resource := ""
	if len(positionals) == 3 {
		resource = positionals[2]
	}
	decl, rc := findDeclaredBroker("request", source, brokers, packSettings, errw)
	if rc != 0 {
		return rc
	}
	key := "requests." + configKeyComponent(source) + "." + configKeyComponent(capability)

	var verb, value string
	if capability == "repos" {
		if decl.Brokered.RemoteHost == brokerscope.AnyRemoteHost {
			fmt.Fprintf(errw, "yolo request: %s reaches this workspace's own remotes on any host, so it takes no repository list.\n"+
				"  Add the remote to the workspace instead (git remote add <name> <url>), then run `yolo check`.\n", source)
			return 1
		}
		if !brokerscope.ValidRepo(resource) {
			fmt.Fprintf(errw, "yolo request: repos takes one OWNER/REPO, such as acme/other; got %q.\n"+
				"  For example:  yolo request %s repos acme/other\n", resource, source)
			return 1
		}
		verb, value = "add", resource
	} else {
		var refusal string
		verb, value, refusal = requestCapabilityEdit(decl, source, capability, resource)
		if refusal != "" {
			fmt.Fprint(errw, refusal)
			return 1
		}
	}

	if workspace == "" {
		workspace = configEditWorkspace()
	}
	project := config.EditTargetChoice{Kind: config.EditTargetProject}
	rc = requestWriteEdit(workspace, key, verb, value, project, dryRun, out, errw)
	if dryRun {
		return rc
	}
	if rc != 0 && !requestWritten(workspace, []string{"requests", source, capability}, value) {
		return rc
	}

	skipped := capability != "repos" && !requestWithinCeiling(workspace, source, capability)
	if skipped {
		reportRequestSkipped(out, source, capability, requestDescription(capability, value))
	}
	fmt.Fprintln(out, requestApprovalLine(skipped))
	return rc
}

// requestCapabilityEdit is the edit a request for capability and resource makes on the broker decl:
// verb "add" with the value appended, or "set" with "true" for a yes/no request. Otherwise refusal
// is what to print, naming the request by subject (`github`, or `repos.infra.github`).
func requestCapabilityEdit(decl config.ReadableBrokerDeclaration, subject, capability, resource string) (verb, value, refusal string) {
	source := decl.Brokered.Source
	cmd := "yolo request " + subject
	declared := declaredCapabilities(decl)
	switch {
	case !slices.Contains(declared, capability):
		hint := ""
		if subject == source {
			hint = ", and repos for a repository"
		}
		return "", "", fmt.Sprintf("yolo request: %s declares no capability %q. Declared: %s%s.\n"+
			"  For example:  %s %s\n", subject, capability, strings.Join(declared, ", "), hint, cmd, declared[len(declared)-1])
	case capability == decl.ReadCapability:
		// The readable schema has no requests.<source>.<read capability> key: the read is the
		// user's to allow for every project, so the step is the user's verb, on the host.
		return "", "", fmt.Sprintf("yolo request: %s %s is not requested by a project; your user config allows it for every project.\n"+
			"  On the host:  yolo allow %s %s\n", subject, capability, source, capability)
	case requestGrantKind(decl, capability) == brokerscope.KindRuns:
		if resource == "" || !brokerscope.ValidGrantWorkflow(resource) {
			return "", "", fmt.Sprintf("yolo request: %s %s takes one workflow file name, such as nightly.yml, or * for every workflow; got %q.\n"+
				"  For example:  %s %s nightly.yml\n", subject, capability, resource, cmd, capability)
		}
		return "add", resource, ""
	case requestGrantKind(decl, capability) == brokerscope.KindApproveRun:
		if resource == "" || !brokerscope.ValidGrantActor(resource) {
			return "", "", fmt.Sprintf("yolo request: %s %s takes one GitHub login, such as 'github-actions[bot]', or * for every actor; got %q.\n"+
				"  For example:  %s %s 'github-actions[bot]'\n", subject, capability, resource, cmd, capability)
		}
		return "add", resource, ""
	case requestTakesRef(decl, capability):
		if resource == "" || !brokerscope.ValidGrantRefPattern(resource) {
			return "", "", fmt.Sprintf("yolo request: %s %s takes one ref name git accepts, such as main or patch/*; got %q.\n"+
				"  For example:  %s %s main\n", subject, capability, resource, cmd, capability)
		}
		return "add", resource, ""
	default:
		if resource != "" {
			return "", "", fmt.Sprintf("yolo request: %s %s is a yes/no request and takes no %q.\n"+
				"  Run:  %s %s\n", subject, capability, resource, cmd, capability)
		}
		return "set", "true", ""
	}
}

// requestWriteEdit writes one request into the file choice selects: an append to the list at key,
// or the yes/no set to true.
func requestWriteEdit(workspace, key, verb, value string, choice config.EditTargetChoice, dryRun bool, out, errw io.Writer) int {
	if verb == "add" {
		return namedVerbEditUnless(requestConvertFirst, workspace, key, configedit.Append, value, choice, dryRun, !dryRun, out, errw)
	}
	return namedVerbEditUnless(requestConvertFirst, workspace, key, configedit.Set, true, choice, dryRun, !dryRun, out, errw)
}

// requestApprovalLine is the host step after a written request. Approving the project's change
// alone does not turn on a capability the user config does not allow, so a skipped request names
// the allow command, which offers the approval itself when the human runs it in this workspace.
func requestApprovalLine(skipped bool) string {
	if skipped {
		return "Approving this project's change with `yolo config apply` does not turn it on until it is allowed. " +
			"Run in this workspace on the host, the `yolo allow` command above allows it and then offers the change for approval."
	}
	return "It is approved on the host: the human runs `yolo config apply` there, in this workspace, and answers y."
}

// reportRequestSkipped says a written request is outside the user's ceiling, so the project runs
// without it, and names the command that allows it.
func reportRequestSkipped(out io.Writer, source, capability, description string) {
	allowArgv := []string{"yolo", "allow", source, "on_request", capability}
	fmt.Fprintf(out, "This project requests %s %s, but your user config does not allow %s on request, "+
		"so this project runs without it.\n  To allow it for every project you work on (in your config, not this file):\n    %s\n",
		source, description, capability, strings.Join(allowArgv, " "))
	if config.InJail() {
		// An allow is the human's own answer: never a `yolo host-run` line.
		fmt.Fprintln(out, "  (From this jail: the human runs it directly in a host terminal, not through `yolo host-run`.)")
	}
}

// requestLoopholeKind writes a grant-declaring pack's request (docs/reference/loophole-grants.md
// §4.2) in the shape config.ResolveLoopholeGrants reads: `<kind> = true` for a plain kind, the
// values appended to `<kind> = [...]` for a valued one. Then, as for a broker, it reports a request
// outside the ceiling and names the host step.
func requestLoopholeKind(workspace string, decl config.LoopholeGrantDeclaration, kind string, values []string,
	dryRun bool, out, errw io.Writer) int {
	g, ok := loopholeGrantKind(decl, kind)
	if !ok {
		example := decl.Grants[0]
		exampleArgs := example.Kind
		if example.Valued {
			exampleArgs += " <" + loopholeValueNoun(example) + ">"
		}
		fmt.Fprintf(errw, "yolo request: %s declares no grant kind %q. Declared: %s.\n  For example:  yolo request %s %s\n",
			decl.Pack, kind, strings.Join(loopholeGrantKindNames(decl), ", "), decl.Pack, exampleArgs)
		return 1
	}
	if !g.Valued && len(values) > 0 {
		fmt.Fprintf(errw, "yolo request: %s %s is a yes/no request and takes no %q.\n  Run:  yolo request %s %s\n",
			decl.Pack, kind, strings.Join(values, " "), decl.Pack, kind)
		return 1
	}
	if g.Valued && (len(values) == 0 || slices.Contains(values, "")) {
		fmt.Fprintf(errw, "yolo request: %s %s carries %s; name at least one, none empty.\n"+
			"  For example:  yolo request %s %s <%s>\n",
			decl.Pack, kind, loopholeValueNoun(g), decl.Pack, kind, loopholeValueNoun(g))
		return 1
	}

	if workspace == "" {
		workspace = configEditWorkspace()
	}
	key := "requests." + configKeyComponent(decl.Pack) + "." + configKeyComponent(kind)
	var rc int
	if g.Valued {
		rc = requestAppendValues(workspace, key, values, dryRun, out, errw)
	} else {
		rc = namedVerbEditUnless(requestConvertFirst, workspace, key, configedit.Set, true,
			config.EditTargetChoice{Kind: config.EditTargetProject}, dryRun, !dryRun, out, errw)
	}
	if dryRun {
		return rc
	}
	if rc != 0 && !loopholeRequestWritten(workspace, decl.Pack, kind, g.Valued, values) {
		return rc
	}
	// The gate's own reading (config.LoopholeRequestAdmitted: the legacy projection and a legacy
	// user config's admission, LC-D14, included), so the verb and the gate cannot disagree.
	if !config.LoopholeRequestAdmitted(workspace, decl, kind) {
		description := kind
		if g.Valued {
			description += " " + strings.Join(values, ", ")
		}
		reportRequestSkipped(out, decl.Pack, kind, description)
		fmt.Fprintln(out, requestApprovalLine(true))
		return rc
	}
	fmt.Fprintln(out, requestApprovalLine(false))
	return rc
}

// requestAppendValues adds values to the project's list at key: the whole list in one edit when the
// file has none yet, each value appended (a value already listed is no change) when it has, then
// one post-write check.
func requestAppendValues(workspace, key string, values []string, dryRun bool, out, errw io.Writer) int {
	path, err := parseConfigEditKey(key)
	if err != nil {
		fmt.Fprintf(errw, "invalid config key: %v\n", err)
		return 2
	}
	project := config.EditTargetChoice{Kind: config.EditTargetProject}
	first := configedit.Mutation{Action: configedit.Append, Path: path, Value: values[0]}
	target, err := configedit.ResolveTarget(workspace, first, project)
	if err != nil {
		fmt.Fprint(errw, configEditRefusal(err))
		return 1
	}
	if msg := requestConvertFirst(target); msg != "" {
		fmt.Fprintln(errw, msg)
		return 1
	}
	if !configEditPathPresent(target.ExpectedBytes(), target.Format, target.AuthoredPath()) {
		var list []any
		for _, v := range values {
			if !slices.Contains(list, any(v)) {
				list = append(list, v)
			}
		}
		added := configedit.Mutation{Action: configedit.Add, Path: path, Value: list}
		resolved, err := configedit.ResolveTarget(workspace, added, project)
		if err != nil {
			fmt.Fprint(errw, configEditRefusal(err))
			return 1
		}
		return applyConfigEdit(workspace, resolved, added, dryRun, out, errw, !dryRun)
	}
	for _, v := range values {
		m := configedit.Mutation{Action: configedit.Append, Path: path, Value: v}
		resolved, err := configedit.ResolveTarget(workspace, m, project)
		if err != nil {
			fmt.Fprint(errw, configEditRefusal(err))
			return 1
		}
		if rc := applyConfigEdit(workspace, resolved, m, dryRun, out, errw, false); rc != 0 {
			return rc
		}
	}
	if dryRun {
		return 0
	}
	if rc := runConfigPostWriteCheck(workspace, out, errw); rc != 0 {
		fmt.Fprintln(errw, "The config file was written, but the post-write check did not pass; follow its findings before continuing.")
		return rc
	}
	return 0
}

// loopholeRequestWritten reports whether the request is in the effective config after a post-write
// check that did not pass: the file was written, so the verb still says what follows.
func loopholeRequestWritten(workspace, pack, kind string, valued bool, values []string) bool {
	cfg, err := config.LoadReadableConfig(workspace, false, func(string) {})
	if err != nil {
		return false
	}
	got, ok := configValueAt(cfg, "requests", pack, kind)
	if !ok {
		return false
	}
	if !valued {
		return got == true
	}
	items, isList := got.([]any)
	if !isList {
		return false
	}
	for _, v := range values {
		if !slices.Contains(items, any(v)) {
			return false
		}
	}
	return true
}

// requestConvertFirst refuses a `requests` write into a legacy JSONC project file that still
// carries `brokered`: one file would then hold both vocabularies for the same grants. A JSONC
// project file without `brokered` is written, since the gate reads `requests` from it.
func requestConvertFirst(target config.ResolvedEditTarget) string {
	if target.Format != configdoc.JSONC ||
		!configEditPathPresent(target.ExpectedBytes(), target.Format, []configdoc.Step{{Key: "brokered"}}) {
		return ""
	}
	return "yolo request: " + config.ConvertFirstRefusal(target.PhysicalPath, "a `requests` entry beside its legacy `brokered` grants")
}

func requestDescription(capability, value string) string {
	if value == "true" {
		return capability
	}
	return capability + " " + value
}

// requestTakesRef reports whether the capability is a grant kind that names something (a ref or
// a workflow), which makes its request a list of names rather than a yes/no.
func requestTakesRef(decl config.ReadableBrokerDeclaration, capability string) bool {
	return brokerscope.GrantTakesName(requestGrantKind(decl, capability))
}

// requestGrantKind is the grant kind the capability maps to, "" for the read capability or one
// the source does not declare.
func requestGrantKind(decl config.ReadableBrokerDeclaration, capability string) string {
	if capability == decl.ReadCapability {
		return ""
	}
	for kind, mapped := range decl.KindCapabilities {
		if mapped == capability {
			return kind
		}
	}
	return ""
}

// requestWritten reports whether the request at keys (its path in the composed config) holds value
// after a post-write check that did not pass: the file was written, so the verb still says what follows.
func requestWritten(workspace string, keys []string, value string) bool {
	cfg, err := config.LoadReadableConfig(workspace, false, func(string) {})
	if err != nil {
		return false
	}
	got, ok := configValueAt(cfg, keys...)
	if !ok {
		return false
	}
	if items, isList := got.([]any); isList {
		return slices.Contains(items, any(value))
	}
	if text, isText := got.(string); isText {
		return text == value
	}
	return value == "true" && got == true
}

// requestWithinCeiling reports whether the user config allows the capability always or on
// request. A user config still on the legacy `brokered` grants has no `allow` table, and its
// ceiling is not this check's to judge, so it counts as within.
func requestWithinCeiling(workspace, source, capability string) bool {
	cfg, err := config.LoadReadableConfig(workspace, false, func(string) {})
	if err != nil {
		return true
	}
	if _, legacy := cfg.Get("brokered"); legacy {
		if _, readable := cfg.Get("allow"); !readable {
			return true
		}
	}
	for _, list := range []string{"always", "on_request"} {
		if got, ok := configValueAt(cfg, "allow", source, list); ok {
			if items, isList := got.([]any); isList && slices.Contains(items, any(capability)) {
				return true
			}
		}
	}
	return false
}

// configValueAt walks object keys through a composed config.
func configValueAt(cfg *jsonx.OrderedMap, keys ...string) (any, bool) {
	var value any = cfg
	for _, key := range keys {
		object, ok := value.(*jsonx.OrderedMap)
		if !ok || object == nil {
			return nil, false
		}
		value, ok = object.Get(key)
		if !ok {
			return nil, false
		}
	}
	return value, true
}
