package loopholedecl

import (
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// parentjail.go is the `inherit_from_parent_jail` manifest block: a loophole's statement that a
// NESTED launch may take the credential pointer this loophole serves from the launching jail's own
// environment, instead of starting the loophole's daemons
// (docs/design/sso-backed-bedrock.md SSO-D2 to SSO-D5).
//
// # What core does with it, and what core does not know
//
// Core knows environment variable names and loopback, and nothing about the credential behind
// them. When yolo launches a podman jail from INSIDE a jail, the nested jail is forced onto the
// launching jail's network namespace (`--net=host`), so an address on the launching jail's
// loopback is the nested jail's too. For an enabled loophole declaring this block, when every
// variable in `vars` is set and non-empty in the launching environment, that launch:
//
//  1. starts neither of the loophole's daemons: no host daemon, no front, no endpoint variable,
//     and no jail daemon in the nested jail's payload;
//  2. delivers each variable's value, verbatim, in place of the pack `env` pointer `served_by`
//     the loophole, to exactly the agents that pointer's gate reaches;
//  3. prints one launch line: `<loophole>: <disclose>`.
//
// Anywhere else (a launch from the host, another backend, a variable missing) the block does
// nothing and the loophole runs as it always has.
//
// The block is a PERMISSION the loophole's author grants, never a widening: what the nested
// jail receives is the launching jail's own pointer, so it can reach exactly what the launching
// jail could, and nothing the nested launch's own config asks for.

// ParentJailInheritance is a loophole's `inherit_from_parent_jail` block.
type ParentJailInheritance struct {
	// Vars are the variables that carry the pointer, read from the launching environment. Every
	// one must be set and non-empty there, or the launch runs the loophole as usual. Each is the
	// name of a variable some pack `env` contribution `served_by` this loophole declares; a
	// pointer variable not listed here is withheld from the nested jail.
	Vars []string
	// Disclose is the launch line's text after `<loophole>: `, printed by every launch that
	// inherits.
	Disclose string
}

const (
	keyInheritFromParentJail = "inherit_from_parent_jail"
	keyInheritVars           = "vars"
)

// parentJailKeys is the block's census. Refused in both decoders, as `brokered`'s is: a
// misspelled `disclose` would otherwise be a launch that inherits without saying so.
var parentJailKeys = []string{keyInheritVars, keyDisclose}

var envVarNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseParentJailInheritance(manifestPath string, raw any, jail *JailDaemon) (*ParentJailInheritance, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: '%s' must be a mapping", manifestPath, keyInheritFromParentJail)
	}
	for _, k := range m.Keys() {
		if !inList(k, parentJailKeys) {
			return nil, Errorf("%s: unknown key %q in '%s' (known: %s)", manifestPath,
				keyInheritFromParentJail+"."+k, keyInheritFromParentJail,
				strings.Join(sortedCopy(parentJailKeys), ", "))
		}
	}
	if jail == nil {
		return nil, Errorf("%s: '%s' needs a 'jail_daemon': the inherited pointer stands in for the "+
			"address that daemon serves", manifestPath, keyInheritFromParentJail)
	}
	list, isList := getOrNil(m, keyInheritVars).([]any)
	if !isList || len(list) == 0 || !AllStrings(list) {
		return nil, Errorf("%s: '%s.vars' must be a non-empty list of variable names", manifestPath,
			keyInheritFromParentJail)
	}
	out := &ParentJailInheritance{}
	seen := map[string]bool{}
	for _, v := range StringSlice(list) {
		if !envVarNameRE.MatchString(v) {
			return nil, Errorf("%s: '%s.vars' entry %q is not an environment variable name",
				manifestPath, keyInheritFromParentJail, v)
		}
		if seen[v] {
			return nil, Errorf("%s: '%s.vars' names %q twice", manifestPath, keyInheritFromParentJail, v)
		}
		seen[v] = true
		out.Vars = append(out.Vars, v)
	}
	disclose, _ := getOrNil(m, keyDisclose).(string)
	if strings.TrimSpace(disclose) == "" {
		return nil, Errorf("%s: '%s.disclose' must be a non-empty string: every launch that inherits "+
			"says so in that line", manifestPath, keyInheritFromParentJail)
	}
	if err := refuseControlChars(manifestPath, "'"+keyInheritFromParentJail+".disclose'", disclose); err != nil {
		return nil, err
	}
	out.Disclose = disclose
	return out, nil
}
