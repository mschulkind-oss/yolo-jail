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
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const allowUsage = `usage: yolo allow <source> [always|on_request] <capability> [--dry-run]

Allow a broker capability, or a grant kind a loophole pack declares, in your user config, in the
file that already holds [allow], then offer this workspace's approval of it through the gate
` + "`yolo config apply`" + ` runs.

  always       every project's jail gets it (the default); never a kind that carries a value
  on_request   a project may request it in its own config (yolo request)

Examples:
  yolo allow git fetch
  yolo allow git on_request push
  yolo allow github always read
  yolo allow journal read
  yolo allow host-processes on_request view

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
	// A former spelling (`dispatch_workflows`) allows the capability that replaced it.
	capability = config.CanonicalCapability(source, capability)

	// The user config is the host's: a jail cannot write it. An allow is the human's own
	// answer, so the command is handed over to run directly in a host terminal, never as a
	// `yolo host-run` line (the jail briefing's "Approvals are not host-run").
	if config.InJail() {
		argv := append([]string{"yolo", "allow"}, positionals...)
		fmt.Fprint(errw, "yolo allow writes your user config, which only the host can edit, and it is the human's own answer.\n"+
			hostApprovalStep(strings.Join(argv, " ")))
		return 1
	}

	brokers, packSettings, err := allowSelectedBrokers()
	if err != nil {
		fmt.Fprintf(errw, "yolo allow: cannot read what the selected packs declare: %v\n"+
			"  Run `yolo check` to see which pack is broken.\n", err)
		return 1
	}
	// seed is what a first on_request list starts with besides the kind asked for: a written
	// list replaces the requestable-by-default ceiling (config.ResolveLoopholeGrants), so a list
	// created without those kinds would withdraw them from a user who asked to add one.
	var seed []string
	if grants, ok := findGrantDeclaringPack(source, packSettings); ok {
		if rc := allowLoopholeKindRefusal(grants, list, capability, errw); rc != 0 {
			return rc
		}
		if list == "on_request" {
			seed = requestableByDefaultKinds(grants, capability)
		}
	} else {
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
	}

	workspace := configEditWorkspace()
	key := "allow." + configKeyComponent(source) + "." + list
	seeded := false
	if rc := namedVerbEditSeeded(allowConvertFirst, workspace, key, configedit.Append, capability, seed, &seeded,
		config.EditTargetChoice{Kind: config.EditTargetUser}, dryRun, false, out, errw); rc != 0 {
		return rc
	}
	if seeded {
		verb := "now lists"
		if dryRun {
			verb = "would list"
		}
		fmt.Fprintf(out, "%s %s %s (requestable by default) and %s: a written on_request list replaces that default, so it keeps them.\n",
			key, verb, strings.Join(seed, ", "), capability)
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
	for _, p := range packSettings {
		if p.Pack == source && len(p.Settings) > 0 {
			fmt.Fprintf(errw, "yolo %s: %s is a pack with settings, not a broker with capabilities.\n"+
				"  Set one with:  yolo config set allow.%s.<setting> <value>\n", verb, source, source)
			return config.ReadableBrokerDeclaration{}, 1
		}
	}
	for _, p := range packSettings {
		if len(p.Grants) > 0 && !slices.Contains(sources, p.Pack) {
			sources = append(sources, p.Pack)
		}
	}
	sort.Strings(sources)
	listed := "none"
	if len(sources) > 0 {
		listed = strings.Join(sources, ", ")
	}
	fmt.Fprintf(errw, "yolo %s: no selected pack declares a broker source or grant kinds named %q. Declared sources: %s.\n"+
		"  If a pack provides it, select that pack first:  yolo pack add <pack>\n", verb, source, listed)
	return config.ReadableBrokerDeclaration{}, 1
}

// findGrantDeclaringPack is the selected pack named source whose loophole declares [[grants]]
// (docs/reference/loophole-grants.md §4.2): its kinds are what allow.<pack>.always and
// .on_request and requests.<pack> take, as config.ResolveLoopholeGrants reads them back.
func findGrantDeclaringPack(source string, packSettings []config.ReadablePackDeclaration) (config.LoopholeGrantDeclaration, bool) {
	for _, p := range packSettings {
		if p.Pack == source && len(p.Grants) > 0 {
			return config.LoopholeGrantDeclaration{Pack: p.Pack, Loophole: p.Loophole, Access: p.Access,
				Grants: append([]loopholedecl.Grant(nil), p.Grants...)}, true
		}
	}
	return config.LoopholeGrantDeclaration{}, false
}

// requestableByDefaultKinds is decl's kinds declared requestable_by_default, in declaration
// order, except except: what a first allow.<pack>.on_request list must keep.
func requestableByDefaultKinds(decl config.LoopholeGrantDeclaration, except string) []string {
	var out []string
	for _, g := range decl.Grants {
		if g.RequestableByDefault && g.Kind != except {
			out = append(out, g.Kind)
		}
	}
	return out
}

// loopholeGrantKind is the declared kind named kind.
func loopholeGrantKind(decl config.LoopholeGrantDeclaration, kind string) (loopholedecl.Grant, bool) {
	for _, g := range decl.Grants {
		if g.Kind == kind {
			return g, true
		}
	}
	return loopholedecl.Grant{}, false
}

func loopholeGrantKindNames(decl config.LoopholeGrantDeclaration) []string {
	out := make([]string, 0, len(decl.Grants))
	for _, g := range decl.Grants {
		out = append(out, g.Kind)
	}
	return out
}

// allowLoopholeKindRefusal refuses, before anything is written, a kind the pack does not declare
// and a valued kind under always, whose value only a project can give: the ceiling the grant
// reader refuses (config.ValuedKindUnderAlwaysError), turned into the command that works. 0 when
// the kind may be written to list.
func allowLoopholeKindRefusal(decl config.LoopholeGrantDeclaration, list, kind string, errw io.Writer) int {
	g, ok := loopholeGrantKind(decl, kind)
	if !ok {
		example, exampleList := decl.Grants[0], list
		if example.Valued {
			exampleList = "on_request"
		}
		fmt.Fprintf(errw, "yolo allow: %s declares no grant kind %q. Declared: %s.\n  For example:  yolo allow %s %s %s\n",
			decl.Pack, kind, strings.Join(loopholeGrantKindNames(decl), ", "), decl.Pack, exampleList, example.Kind)
		return 1
	}
	if g.Valued && list == "always" {
		fmt.Fprintf(errw, "yolo allow: %s %s carries a value only a project can give (%s), so always has none to carry.\n"+
			"  Allow it on request:  yolo allow %s on_request %s\n",
			decl.Pack, kind, loopholeValueNoun(g), decl.Pack, kind)
		return 1
	}
	return 0
}

// loopholeValueNoun names a valued kind's values, as refusals print them.
func loopholeValueNoun(g loopholedecl.Grant) string {
	if g.ValueNoun != "" {
		return g.ValueNoun
	}
	return "values"
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

// hostApprovalStep is the next step for an approval command (`yolo allow`, `yolo approve`,
// `yolo config apply`) refused in a jail: the command alone on its own line, for the human to
// run directly in a host terminal. Never a `yolo host-run` line: an approval is the human's own
// answer, which the jail briefing's "Approvals are not host-run" rule keeps out of the mailbox.
func hostApprovalStep(command string) string {
	return "  Ask the human to run this directly in a host terminal, not through `yolo host-run`:\n\n" +
		"    " + command + "\n\n"
}

// hostRunLineFor is the paste line that runs argv on the host for this jail's workspace. It is
// for a command that is not an approval; hostApprovalStep is for those.
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
	return namedVerbEditSeeded(refuse, workspace, key, action, value, nil, nil, choice, dryRun, postWriteCheck, out, errw)
}

// namedVerbEditSeeded is namedVerbEditUnless where an Append that creates the list starts it
// with seed before value; seeded reports that it did. An existing list takes value alone.
func namedVerbEditSeeded(refuse func(config.ResolvedEditTarget) string, workspace, key string, action configedit.Action,
	value any, seed []string, seeded *bool, choice config.EditTargetChoice, dryRun, postWriteCheck bool, out, errw io.Writer) int {
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
	if action == configedit.Append && len(seed) > 0 &&
		!configEditPathPresent(target.ExpectedBytes(), target.Format, target.AuthoredPath()) {
		first := make([]any, 0, len(seed)+1)
		for _, s := range seed {
			first = append(first, s)
		}
		first = append(first, value)
		mutation, value = configedit.Mutation{Action: configedit.Set, Path: path, Value: first}, first
		if seeded != nil {
			*seeded = true
		}
	}
	mutation, target, ok := addMissingKey(workspace, target, mutation, value, choice, errw)
	if !ok {
		return 1
	}
	return applyConfigEdit(workspace, target, mutation, dryRun, out, errw, postWriteCheck)
}
