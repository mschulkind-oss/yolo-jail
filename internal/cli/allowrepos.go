package cli

// allowrepos.go is the repository form of `yolo allow` (GW-D77): `yolo allow repos <alias>
// <host>/<path>` declares a named repository in the user's config, and
// `yolo allow repos.<alias>.<broker> [always|on_request] <capability>` allows a capability of a
// broker on that repository alone (GW-D64). The user form takes capability names, never a resource.

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// allowReposRun dispatches the repository form. A jail cannot write the user config, so it names
// the command for the human's host terminal, as the unqualified allow does.
func allowReposRun(positionals []string, dryRun bool, out, errw io.Writer) int {
	if config.InJail() {
		argv := append([]string{"yolo", "allow"}, positionals...)
		fmt.Fprint(errw, "yolo allow writes your user config, which only the host can edit, and it is the human's own answer.\n"+
			hostApprovalStep(shquote.Join(argv)))
		return 1
	}
	if positionals[0] == "repos" {
		return allowReposDeclare(positionals[1:], dryRun, out, errw)
	}
	return allowReposNamed(positionals, dryRun, out, errw)
}

// allowReposDeclare writes `[repos.<alias>] repo = "<host>/<path>"` into the user config.
func allowReposDeclare(args []string, dryRun bool, out, errw io.Writer) int {
	if len(args) != 2 {
		fmt.Fprint(errw, "yolo allow repos: takes a name and a repository: yolo allow repos <alias> <host>/<path>\n"+
			"  For example:  yolo allow repos infra github.com/acme/infra\n")
		return 2
	}
	alias, value := args[0], args[1]
	example := "yolo allow repos " + alias + " github.com/acme/infra"
	if msg := repoAliasRefusal(alias, example); msg != "" {
		fmt.Fprint(errw, msg)
		return 1
	}
	line, refusal := namedRepoLine(value, example)
	if refusal != "" {
		fmt.Fprint(errw, "yolo allow repos: "+refusal)
		return 1
	}
	user := config.EditTargetChoice{Kind: config.EditTargetUser}
	rc := namedVerbEditUnless(allowConvertFirst, configEditWorkspace(), "repos."+configKeyComponent(alias)+".repo",
		configedit.Set, line, user, dryRun, false, out, errw)
	if dryRun || rc != 0 {
		return rc
	}
	return allowOfferApproval(out, errw)
}

// allowReposNamed writes one capability into the scoped ceiling `[repos.<alias>.allow.<broker>]`
// of the user config. The alias must already name a repository there.
func allowReposNamed(positionals []string, dryRun bool, out, errw io.Writer) int {
	if len(positionals) < 2 || len(positionals) > 3 {
		fmt.Fprintln(errw, allowUsage)
		return 2
	}
	alias, broker := splitReposWord(positionals[0])
	list := "always"
	capability := positionals[len(positionals)-1]
	if len(positionals) == 3 {
		list = positionals[1]
		if list != "always" && list != "on_request" {
			fmt.Fprintf(errw, "yolo allow: %q is neither always nor on_request\n\n%s\n", list, allowUsage)
			return 2
		}
	}
	if broker == "" {
		fmt.Fprintf(errw, "yolo allow: repos.%s names a repository, not a broker. Allow a broker's capability on it:\n"+
			"  For example:  yolo allow repos.%s.<broker> on_request <capability>\n", alias, alias)
		return 1
	}
	if msg := repoAliasRefusal(alias, "yolo allow repos "+alias+" github.com/acme/infra"); msg != "" {
		fmt.Fprint(errw, msg)
		return 1
	}
	brokers, packSettings, err := allowSelectedBrokers()
	if err != nil {
		fmt.Fprintf(errw, "yolo allow: cannot read what the selected packs declare: %v\n"+
			"  Run `yolo check` to see which pack is broken.\n", err)
		return 1
	}
	decl, rc := findDeclaredBroker("allow", broker, brokers, packSettings, errw)
	if rc != 0 {
		return rc
	}
	if decl.CallBroker != nil || decl.Brokered.RemoteHost == brokerscope.AnyRemoteHost {
		fmt.Fprintf(errw, "yolo allow: %s takes no repository ceiling; a repository's ceiling names a host-served broker.\n", broker)
		return 1
	}
	capability = config.CanonicalCapability(broker, capability)
	declared := declaredCapabilities(decl)
	if !slices.Contains(declared, capability) {
		fmt.Fprintf(errw, "yolo allow: %s declares no capability %q. Declared: %s.\n  For example:  yolo allow repos.%s.%s on_request %s\n",
			broker, capability, strings.Join(declared, ", "), alias, broker, declared[0])
		return 1
	}
	if capability == decl.ReadCapability {
		// The read is allowed for every repository by the broad list alone (GW-D80); a repository's
		// ceiling holds no read, so it is refused here before anything is written.
		fmt.Fprintf(errw, "yolo allow: %s %s is not allowed under a name: the read is allowed for every repository by your broad list.\n"+
			"  On the host:  yolo allow %s always %s\n", broker, capability, broker, capability)
		return 1
	}
	if userRepoLine(alias) == "" {
		fmt.Fprintf(errw, "yolo allow: repos.%s names no repository in your user config. Declare it first:\n"+
			"  yolo allow repos %s <host>/<path>\n", alias, alias)
		return 1
	}

	user := config.EditTargetChoice{Kind: config.EditTargetUser}
	key := "repos." + configKeyComponent(alias) + ".allow." + configKeyComponent(broker) + "." + list
	rc = namedVerbEditUnless(allowConvertFirst, configEditWorkspace(), key, configedit.Append, capability, user, dryRun, false, out, errw)
	if dryRun || rc != 0 {
		return rc
	}
	return allowOfferApproval(out, errw)
}

// userRepoLine is the repo line the user config's [repos.<alias>] names, "" with none.
func userRepoLine(alias string) string {
	line, _ := configValueAt(config.UserScopeConfigOrEmpty(), "repos", alias, "repo")
	text, _ := line.(string)
	return text
}
