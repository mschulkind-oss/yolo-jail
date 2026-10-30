package cli

// allow.go is `yolo allow`, the named verb for a broker ceiling in the user config
// (docs/design/config-commands.md §4, RC-D17; the `allow` section is config-user.md §6). It
// is sugar over `yolo config add allow.<source>.<list> <capability>`, plus the two things a
// text edit cannot do: it validates the capability against what the selected packs declare,
// and it chains the write into the workspace approval that makes it live, through the same
// gate `yolo config apply` runs. It never approves anything itself.

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/hostrun"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const allowUsage = `usage: yolo allow <source> [always|on_request] <capability> [--dry-run]

Allow a broker capability in your user config, in the file that already holds [allow], then
offer this workspace's approval of it through the gate ` + "`yolo config apply`" + ` runs.

  always       every project's jail gets it (the default)
  on_request   a project may request it in its own config (yolo request)

Examples:
  yolo allow git fetch
  yolo allow git on_request push
  yolo allow github always read

A pack's own settings under allow.<pack> have no named verb; use
` + "`yolo config set allow.<pack>.<setting> <value>`" + `.`

// allowApprovalGate runs the existing approval gate for the workspace that is the cwd: the
// same sequence as `yolo config apply`, which shows the change and asks y/N itself. A variable
// so a test records what reached the gate instead of driving a terminal.
var allowApprovalGate = func(workspace string, out, errw io.Writer) int {
	_ = workspace // ConfigApply acts on the cwd's workspace, which is the one named here.
	return run.ConfigApply(nil, out, errw, colorForWriter(out))
}

// allowInteractive reports whether a human can answer the gate's prompt. Without one the
// verb names the command instead of running a gate nobody can answer.
var allowInteractive = func() bool {
	return tty.IsTerminalFile(os.Stdin) && tty.IsTerminalFile(os.Stderr)
}

// allowSelectedBrokers is the broker declarations of the packs the user config selects, the
// set a capability name is validated against: the same selection validation resolves
// (config.UserScopeSelectedPacks), projected by the reader the gate's declarations use.
func allowSelectedBrokers() ([]config.ReadableBrokerDeclaration, []config.ReadablePackDeclaration, error) {
	packs, _ := config.UserScopeSelectedPacks()
	return config.ReadableDeclarationsForPacks(nonNilPacks(packs))
}

func nonNilPacks(packs []*packload.Pack) []*packload.Pack {
	out := make([]*packload.Pack, 0, len(packs))
	for _, p := range packs {
		if p != nil && p.Decl != nil && p.Name != "" {
			out = append(out, p)
		}
	}
	return out
}

func runAllow(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return allowRunW(rest, os.Stdout, os.Stderr)
}

func allowRunW(args []string, out, errw io.Writer) int {
	dryRun := false
	var positionals []string
	for _, a := range args {
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, allowUsage)
			return 0
		case a == "--dry-run":
			dryRun = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo allow: unknown flag %q\n\n%s\n", a, allowUsage)
			return 2
		default:
			positionals = append(positionals, a)
		}
	}
	list := "always"
	var source, capability string
	switch len(positionals) {
	case 2:
		source, capability = positionals[0], positionals[1]
	case 3:
		source, list, capability = positionals[0], positionals[1], positionals[2]
		if list != "always" && list != "on_request" {
			fmt.Fprintf(errw, "yolo allow: %q is neither always nor on_request\n\n%s\n", list, allowUsage)
			return 2
		}
	default:
		fmt.Fprintln(errw, allowUsage)
		return 2
	}

	// The user config is the host's: a jail cannot write it, and asking from inside one is
	// exactly what the host-run line is for.
	if config.InJail() {
		argv := append([]string{"yolo", "allow"}, positionals...)
		fmt.Fprintf(errw, "yolo allow writes your user config, which only the host can edit.\n"+
			"  Ask the human to run, on the host:  %s\n  (or:  %s)\n",
			strings.Join(argv, " "), hostRunLineFor(argv))
		return 1
	}

	brokers, packSettings, err := allowSelectedBrokers()
	if err != nil {
		fmt.Fprintf(errw, "yolo allow: cannot read what the selected packs declare: %v\n"+
			"  Run `yolo check` to see which pack is broken.\n", err)
		return 1
	}
	decl, rc := findDeclaredBroker("allow", source, brokers, packSettings, errw)
	if rc != 0 {
		return rc
	}
	declared := declaredCapabilities(decl)
	if !slices.Contains(declared, capability) {
		fmt.Fprintf(errw, "yolo allow: %s declares no capability %q. Declared: %s.\n  For example:  yolo allow %s %s %s\n",
			source, capability, strings.Join(declared, ", "), source, list, declared[0])
		return 1
	}

	workspace := configEditWorkspace()
	key := "allow." + configKeyComponent(source) + "." + list
	if rc := namedVerbEditUnless(allowConvertFirst, workspace, key, configedit.Append, capability,
		config.EditTargetChoice{Kind: config.EditTargetUser}, dryRun, false, out, errw); rc != 0 {
		return rc
	}
	if dryRun {
		return 0
	}

	// THE STEP AFTER THE EDIT (RC-D17): a user grant acts in a workspace only once that
	// workspace approves it, so the verb offers the approval here, through the one gate.
	approveWorkspace, inWorkspace := resolveWorkspaceRoot()
	if !inWorkspace {
		fmt.Fprintln(out, "This takes effect in a workspace when it approves it: run `yolo config apply` in that workspace.")
		return 0
	}
	if !allowInteractive() {
		fmt.Fprintf(out, "This takes effect when a workspace approves it. To approve it for this one, run `yolo config apply` in %s.\n", approveWorkspace)
		return 0
	}
	fmt.Fprintf(out, "This takes effect when a workspace approves it. Opening this workspace's approval (%s), as `yolo config apply` does:\n", approveWorkspace)
	return allowApprovalGate(approveWorkspace, out, errw)
}

// allowConvertFirst refuses an `allow` write into a user config still on the legacy JSONC
// `brokered` grants: the readers take a canonical `allow` table as the whole ceiling and drop
// every legacy `brokered` user grant once one exists (config.UserScopeHoldsBrokered), so the
// write would silently revoke what the file already allows. A JSONC user file without
// `brokered` is written: the readers honor `allow` there.
func allowConvertFirst(target config.ResolvedEditTarget) string {
	if target.Format != configdoc.JSONC || !config.UserScopeHoldsBrokered() {
		return ""
	}
	return "yolo allow: " + config.ConvertFirstRefusal(target.PhysicalPath, "an `allow` entry beside the legacy `brokered` grants in your user config")
}

// findDeclaredBroker is the broker declaration a verb names, or the refusal that lists what is
// declared. A selected pack with settings but no broker gets the generic verb, since a pack
// cannot ship a named one (config-commands.md §4).
func findDeclaredBroker(verb, source string, brokers []config.ReadableBrokerDeclaration,
	packSettings []config.ReadablePackDeclaration, errw io.Writer) (config.ReadableBrokerDeclaration, int) {
	var sources []string
	for _, b := range brokers {
		if b.Brokered.Source == source {
			return b, 0
		}
		sources = append(sources, b.Brokered.Source)
	}
	sort.Strings(sources)
	for _, p := range packSettings {
		if p.Pack == source && len(p.Settings) > 0 {
			fmt.Fprintf(errw, "yolo %s: %s is a pack with settings, not a broker with capabilities.\n"+
				"  Set one with:  yolo config set allow.%s.<setting> <value>\n", verb, source, source)
			return config.ReadableBrokerDeclaration{}, 1
		}
	}
	listed := "none"
	if len(sources) > 0 {
		listed = strings.Join(sources, ", ")
	}
	fmt.Fprintf(errw, "yolo %s: no selected pack declares a broker source %q. Declared sources: %s.\n"+
		"  If a pack provides it, select that pack first:  yolo pack add <pack>\n", verb, source, listed)
	return config.ReadableBrokerDeclaration{}, 1
}

// declaredCapabilities is the source's capability names, the read capability first: the
// names allow.<source>.always/on_request and requests.<source> take.
func declaredCapabilities(decl config.ReadableBrokerDeclaration) []string {
	out := []string{decl.ReadCapability}
	var rest []string
	for _, capability := range decl.KindCapabilities {
		if capability != decl.ReadCapability && !slices.Contains(rest, capability) {
			rest = append(rest, capability)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

var bareConfigKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// configKeyComponent spells one key component for parseConfigEditKey: bare when it can be,
// quoted otherwise, so a source name with a dot stays one component.
func configKeyComponent(name string) string {
	if bareConfigKey.MatchString(name) {
		return name
	}
	return fmt.Sprintf("%q", name)
}

var hostRunLabelBad = regexp.MustCompile(`[^a-z0-9._-]+`)

// hostRunLineFor is the paste line that runs argv on the host for this jail's workspace.
func hostRunLineFor(argv []string) string {
	dir := os.Getenv("YOLO_HOST_DIR")
	if dir == "" {
		dir = config.JailWorkspace()
	}
	label := strings.Trim(hostRunLabelBad.ReplaceAllString(strings.ToLower(strings.Join(argv[1:], "-")), "-"), "-._")
	if len(label) > 40 {
		label = strings.TrimRight(label[:40], "-._")
	}
	if hostrun.CheckLabel(label) != nil {
		label = "yolo-config"
	}
	return hostrun.FormatLine(hostrun.Line{Dir: dir, Label: label, Argv: argv})
}

// namedVerbSet sets key to value, adding the key (and its table) when the selected file does
// not hold it yet, which the generic set refuses.
func namedVerbSet(workspace, key string, value any, choice config.EditTargetChoice, dryRun, postWriteCheck bool, out, errw io.Writer) int {
	return namedVerbEdit(workspace, key, configedit.Set, value, choice, dryRun, postWriteCheck, out, errw)
}

// namedVerbEdit runs action with value at key when the selected file holds the key, and adds
// the key when it does not (addMissingKey, the path the generic verbs share), through the same
// target rules and transaction as the generic verbs.
func namedVerbEdit(workspace, key string, action configedit.Action, value any, choice config.EditTargetChoice,
	dryRun, postWriteCheck bool, out, errw io.Writer) int {
	return namedVerbEditUnless(nil, workspace, key, action, value, choice, dryRun, postWriteCheck, out, errw)
}

// namedVerbEditUnless is namedVerbEdit with a refusal checked against the resolved target before
// anything is written, dry run included: refuse returns the message to print, or "" to go on.
// An Append on a missing list creates the list (and its table): the generic append refuses a
// missing list, and a named verb's first use is exactly that case.
func namedVerbEditUnless(refuse func(config.ResolvedEditTarget) string, workspace, key string, action configedit.Action,
	value any, choice config.EditTargetChoice, dryRun, postWriteCheck bool, out, errw io.Writer) int {
	path, err := parseConfigEditKey(key)
	if err != nil {
		fmt.Fprintf(errw, "invalid config key: %v\n", err)
		return 2
	}
	mutation := configedit.Mutation{Action: action, Path: path, Value: value}
	target, err := configedit.ResolveTarget(workspace, mutation, choice)
	if err != nil {
		fmt.Fprint(errw, configEditRefusal(err))
		return 1
	}
	if refuse != nil {
		if msg := refuse(target); msg != "" {
			fmt.Fprintln(errw, msg)
			return 1
		}
	}
	mutation, target, ok := addMissingKey(workspace, target, mutation, value, choice, errw)
	if !ok {
		return 1
	}
	return applyConfigEdit(workspace, target, mutation, dryRun, out, errw, postWriteCheck)
}
