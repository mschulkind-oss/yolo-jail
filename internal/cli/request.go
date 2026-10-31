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

Request a broker capability in this project's config, run ` + "`yolo check`" + `, and name the host
step that approves it (` + "`yolo config apply`" + `). A request your user config does not allow on
request is reported and skipped, never an error.

Examples:
  yolo request git push main            a branch the jail may push
  yolo request git new_branches         the jail may create branches
  yolo request github create_pr
  yolo request github dispatch_workflows nightly.yml  a workflow the jail may run on the default branch
  yolo request github repos acme/other  a repository beyond this one's own remotes`

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
// it runs. `yolo approve --remember` names the request's own workspace.
func requestRunIn(workspace string, args []string, out, errw io.Writer) int {
	dryRun := false
	var positionals []string
	for _, a := range args {
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, requestUsage)
			return 0
		case a == "--dry-run":
			dryRun = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo request: unknown flag %q\n\n%s\n", a, requestUsage)
			return 2
		default:
			positionals = append(positionals, a)
		}
	}
	if len(positionals) < 2 || len(positionals) > 3 {
		fmt.Fprintln(errw, requestUsage)
		return 2
	}
	source, capability := positionals[0], positionals[1]
	resource := ""
	if len(positionals) == 3 {
		resource = positionals[2]
	}

	brokers, packSettings, err := allowSelectedBrokers()
	if err != nil {
		fmt.Fprintf(errw, "yolo request: cannot read what the selected packs declare: %v\n"+
			"  Run `yolo check` to see which pack is broken.\n", err)
		return 1
	}
	decl, rc := findDeclaredBroker("request", source, brokers, packSettings, errw)
	if rc != 0 {
		return rc
	}
	declared := declaredCapabilities(decl)
	key := "requests." + configKeyComponent(source) + "." + configKeyComponent(capability)

	verb, value := "", ""
	switch {
	case capability == "repos":
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
	case !slices.Contains(declared, capability):
		fmt.Fprintf(errw, "yolo request: %s declares no capability %q. Declared: %s, and repos for a repository.\n"+
			"  For example:  yolo request %s %s\n", source, capability, strings.Join(declared, ", "), source, declared[len(declared)-1])
		return 1
	case capability == decl.ReadCapability:
		// The readable schema has no requests.<source>.<read capability> key: the read is the
		// user's to allow for every project, so the step is the user's verb, on the host.
		fmt.Fprintf(errw, "yolo request: %s %s is not requested by a project; your user config allows it for every project.\n"+
			"  On the host:  yolo allow %s %s\n", source, capability, source, capability)
		return 1
	case requestGrantKind(decl, capability) == brokerscope.KindDispatchWorkflow:
		if resource == "" || !brokerscope.ValidGrantWorkflow(resource) {
			fmt.Fprintf(errw, "yolo request: %s %s takes one workflow file name, such as nightly.yml, or * for every workflow; got %q.\n"+
				"  For example:  yolo request %s %s nightly.yml\n", source, capability, resource, source, capability)
			return 1
		}
		verb, value = "add", resource
	case requestTakesRef(decl, capability):
		if resource == "" || !brokerscope.ValidGrantRefPattern(resource) {
			fmt.Fprintf(errw, "yolo request: %s %s takes one ref name git accepts, such as main or patch/*; got %q.\n"+
				"  For example:  yolo request %s %s main\n", source, capability, resource, source, capability)
			return 1
		}
		verb, value = "add", resource
	default:
		if resource != "" {
			fmt.Fprintf(errw, "yolo request: %s %s is a yes/no request and takes no %q.\n"+
				"  Run:  yolo request %s %s\n", source, capability, resource, source, capability)
			return 1
		}
		verb, value = "set", "true"
	}

	if workspace == "" {
		workspace = configEditWorkspace()
	}
	project := config.EditTargetChoice{Kind: config.EditTargetProject}
	if verb == "add" {
		rc = namedVerbEditUnless(requestConvertFirst, workspace, key, configedit.Append, value, project, dryRun, !dryRun, out, errw)
	} else {
		rc = namedVerbEditUnless(requestConvertFirst, workspace, key, configedit.Set, true, project, dryRun, !dryRun, out, errw)
	}
	if dryRun {
		return rc
	}
	if rc != 0 && !requestWritten(workspace, source, capability, value) {
		return rc
	}

	if capability != "repos" && !requestWithinCeiling(workspace, source, capability) {
		allowArgv := []string{"yolo", "allow", source, "on_request", capability}
		fmt.Fprintf(out, "This project requests %s %s, but your user config does not allow %s on request, "+
			"so this project runs without it.\n  To allow it for every project you work on (in your config, not this file):\n    %s\n",
			source, requestDescription(capability, value), capability, strings.Join(allowArgv, " "))
		if config.InJail() {
			fmt.Fprintf(out, "  (from this jail:  %s)\n", hostRunLineFor(allowArgv))
		}
	}
	fmt.Fprintln(out, "It is approved on the host: the human runs `yolo config apply` there, in this workspace, and answers y.")
	return rc
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

// requestWritten reports whether the request is in the effective config after a post-write
// check that did not pass: the file was written, so the verb still says what follows.
func requestWritten(workspace, source, capability, value string) bool {
	cfg, err := config.LoadReadableConfig(workspace, false, func(string) {})
	if err != nil {
		return false
	}
	got, ok := configValueAt(cfg, "requests", source, capability)
	if !ok {
		return false
	}
	if items, isList := got.([]any); isList {
		return slices.Contains(items, any(value))
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
