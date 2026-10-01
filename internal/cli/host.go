package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostpath"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const hostUsage = `yolo host — configure and launch coding agents on the HOST

The host is one notch of the confinement dial, and this is where its ergonomics
live. Two things reach a host agent, split by what they carry:

  configuration  endpoints, model aliases, permissions, MCP wiring, and the NAME
                 of a credential variable  ->  the agent's own config file, via
                 ` + "`yolo host apply`" + `. Works from any invocation: an IDE, cron, an
                 absolute path.
  environment    the credential itself, feature flags, and unsets  ->  the
                 process environment, via ` + "`yolo host -- <agent>`" + `. Works wherever
                 yolo is in the launch path.

Both always apply. A config file cannot deliver a secret — api_key_env_name carries a
variable's NAME, not its value — so the environment channel is not a fallback.

A profile that runs through a pack's service (claude on codex or cerebras, through the
wire bridge) starts that service for the one command: ` + "`yolo host -- <agent>`" + ` runs it
beside the agent on a port it picked, answering only that agent, and stops it when the
agent exits. ` + "`yolo host env`" + ` refuses such a profile and ` + "`yolo host apply`" + ` writes
no address for it, so an agent started without yolo runs without it.

Usage:
  yolo host [flags] -- <command> [args...]   run a command with the composed environment
  yolo host apply [flags]                    render config surfaces into your real home
  yolo host env [flags]                      print the composed environment
  yolo host wrappers [status]                report the PATH launch wrappers

Exec flags (yolo host -- ...):
  --profile <name>, -p <name>   Select a declared profile for the wrapped agent CLI, this
                                launch only. A profile reaches agent CLIs only (a CLI a
                                selected pack installs), as it does in a jail, and never
                                an arbitrary command: -p for any other command (bash,
                                curl, terraform) is refused, naming the grant that hands
                                it a provider's key, --with-credentials below. The jail's
                                pair spelling works too: -p claude=zai -- claude is
                                -p zai. A pair naming any other command is refused, since
                                this runs one command. Also -p=<name> and
                                --profile=<name>; a value flag with no value is refused
                                (exit 2), as in a jail.
  --with-credentials <provider[,provider...]|all>
                                GRANT the wrapped command the named providers' claimed
                                env_sources credentials, this launch only. KEYS ONLY: no
                                profile is selected and nothing is re-pointed (no base
                                URL, no model). ` + "`all`" + ` is every composed provider that claims
                                a value in env_sources. Repeatable; also
                                --with-credentials=<list>. It combines with -p: an agent
                                keeps its profile and also receives the granted keys.
                                Every run names what it granted, by name, never by value,
                                and everything the command starts inherits it. An
                                unknown provider refuses, naming the known ones; a named
                                provider env_sources holds no value for is reported.
                                Nothing else implies it: not -p, not the profile key, not
                                any YOLO_ALLOW_* variable, and no config key. HOST ONLY:
                                a jail launch refuses it.
  --at host                     Accepted and changes nothing: this verb is the host notch.
                                ` + "`yolo --at host -- <cmd>`" + ` and ` + "`yolo run --at host -- <cmd>`" + `
                                are this verb, wherever --at sits. Another notch is
                                refused, as is a jail-launch flag with no meaning here
                                (--timing, --dry-run, --network, --accept-config-changes).
  --help, -h                    Show this help.

With ` + "`host_apply_on_launch`" + ` enabled (defaulting to on when ` + "`host_wrappers: true`" + `),
` + "`yolo host -- <agent>`" + ` checks whether ` + "`yolo host apply`" + ` would change anything,
and automatically synchronizes host configuration before launch — silently exec'ing when fresh.
When a first apply would drop MCP servers yolo does not declare, it asks at a terminal; off a
terminal the launch is refused, naming ` + "`yolo host apply --assert`" + `, which asks the same
question where you can answer it. No flag or variable approves it for a launch.
An apply that would ask anything else renders NOTHING: the launch says what needs deciding, and
the agent starts on the render already in place. A configured pack that cannot be resolved or
whose manifest has problems, or a malformed ` + "`packs`" + ` entry, refuses the launch, as it refuses a
jail launch. See ` + "`yolo config-ref`" + `.

apply flags:
  --assert        Write. Without it apply is a DRY RUN and writes nothing.
  --dry-run       Force the dry run, even alongside --assert.
  --verbose, -v   List every destination the report otherwise counts: each settled
                  surface, every dependency probe, every skill by destination.
  --revert        Take yolo back OUT of this home: remove the keys it asserted, on
                  the authority of the provenance record it wrote, and delete that
                  record. A key you set yourself (recorded "host") is never touched.
                  A DRY RUN like every other posture here — it lists each key with
                  the attribution the removal rests on, and --assert performs it.
                  It REMOVES what yolo wrote; it does not restore what a key held
                  before yolo wrote it, because nothing snapshots that. It keeps an
                  empty default, such as pi's "providers": {}, which the agent's
                  file needs, and names each one. Needs host_management "assert" —
                  refused at "none" (nothing was written) and at "own" (the file is
                  derived; delete it instead).
  --format json   Emit the dry run as data instead of a report: destinations, losses,
                  blockers, the counts and the outcome. --json is the same flag.
                  Refused with --assert (exit 2): that posture acts, and an acting
                  verb does not grow a second output mode.

The report ends in one sentence saying how the run went, with the counts beneath it.
Packs resolve the way a launch resolves them: a git pack from the pack store, which this
command first fetches when it never was and refreshes when it follows a branch (hourly; a
tag or commit pin is never re-fetched), a local one from its path. If ANY configured pack
cannot be resolved, or its manifest has problems (the ones ` + "`yolo pack lint`" + ` and every launch
refuse), an --assert is REFUSED with nothing written — an incomplete pack set is never
applied — and the dry run names each pack, why, and the fix.
A missing declared dependency STOPS an --assert: yolo shows the install command each
pack declares and offers to run it, and a NO refuses the run with nothing written.
` + "`yolo pack --help`" + ` says what each contribution kind is, and ` + "`yolo config-ref`" + ` says why
some of them do not apply at this notch.

env flags:
  --format <fmt>  export (default) or json.
  --profile <name>, -p <name>   As above, for the --agent it composes.
  --with-credentials <provider[,provider...]|all>
                  As above, for the script: it exports the granted keys. With neither
                  --agent nor -p the script is an ad-hoc command's slice (as --agent
                  bash), so no agent's provider shape reaches the shell alongside the
                  keys. With -p it is the slice -p composes, plus the keys.
  --agent <name>  Compose as if launching this agent (default: claude, or bash under
                  --with-credentials without -p). The agent name selects which
                  entry of the profile key applies ("*" when none names it), and the
                  output is that agent's slice: a provider credential another agent's
                  profile claims is withheld from it, and stderr says which, by name.

Examples:
  yolo host -- claude                 # bare claude, with the composed environment
  yolo host -p bedrock -- claude      # ... on the bedrock profile, this launch only
  yolo host -p codex -- claude        # claude on your ChatGPT subscription, bridged
  yolo -p bedrock host -- claude      # the same launch, with -p before host
  yolo host --with-credentials zai -- curl ...   # any command, handed zai's key
  yolo host --with-credentials all -- usage-bar   # every provider's key, keys only
  yolo host -p bedrock --with-credentials zai -- claude   # bedrock, plus zai's key
  eval "$(yolo host env)"             # the same environment, in this shell
  eval "$(yolo host env --with-credentials zai)"   # zai's key, in this shell
  eval "$(yolo host env --with-credentials all)"   # every provider's key, in this shell
  yolo host apply --assert            # write the config surfaces
  yolo host apply --revert            # what would withdrawing yolo remove?

` + "`yolo apply --at host`" + ` is the systematic spelling of ` + "`yolo host apply`" + `; both remain.`

// runHost is the `yolo host` entry point.
func runHost(args []string) int {
	return hostMain(hostArguments(args), os.Stdout, os.Stderr, colorForWriter(os.Stdout), os.Stdin)
}

// hostArguments removes the host verb wherever dispatch found it, leaving launch flags
// in their original order. Flag values (including a profile named "host") are not verbs.
func hostArguments(args []string) []string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if valueTakingFlags[args[i]] {
			i++
			continue
		}
		if args[i] == "host" {
			return append(append([]string{}, args[:i]...), args[i+1:]...)
		}
	}
	return args
}

// hostMain dispatches `yolo host`.
//
// `--` IS CHECKED FIRST, before any verb, and that ordering is the whole grammar: the
// exec half takes flags before the separator (`yolo host -p bedrock -- claude`), so
// args[0] is routinely a flag rather than a verb, and a verb switch that ran first would
// have to re-implement flag parsing to find out whether a verb was even present.
func hostMain(args []string, out, errw io.Writer, color bool, stdin io.Reader) int {
	if i := indexOf(args, "--"); i >= 0 {
		return hostExec(args[:i], args[i+1:], out, errw, stdin)
	}
	if len(args) == 0 {
		fmt.Fprintln(out, hostUsage)
		return 0
	}
	// A LEADING FLAG WITH NO `--` IS AN EXEC FLAG, never a verb: no verb is spelled with a dash,
	// and `yolo --at host -p zai` reaches here as [-p zai] exactly as `yolo host -p zai` does. So
	// the exec half's parser judges it, and a missing value, a jail-only flag or a typo gets the
	// refusal it gets before a `--` (exit 2) rather than `unknown verb "-p"` (exit 1). Help keeps
	// the switch below.
	if a := args[0]; len(a) > 1 && a[0] == '-' && a != "-h" && a != "--help" {
		return hostExecWithoutCommand(args, out, errw)
	}
	switch args[0] {
	case "apply":
		return hostApply(args[1:], out, errw, color, stdin)
	case "env":
		return hostEnv(args[1:], out, errw)
	case "wrappers":
		return hostWrappers(args[1:], out, errw, color)
	case "-h", "--help", "help":
		fmt.Fprintln(out, hostUsage)
		return 0
	default:
		fmt.Fprintf(errw, "yolo host: unknown verb %q\n\n%s\n", args[0], hostUsage)
		return 1
	}
}

// hostExecWithoutCommand is exec flags typed with no `--` and so no command: `yolo host -p zai`,
// `yolo --at host --timing`. The flags are parsed as a `--` line's are, so each is refused as it
// would be there; flags that parse are refused for naming nothing to run, since the host verb has
// no default command (a jail's is its shell, and the host's shell is the one the user typed in).
func hostExecWithoutCommand(flagArgs []string, out, errw io.Writer) int {
	flags, ok := parseHostExecFlags(flagArgs, errw)
	if flags.help {
		fmt.Fprintln(out, hostUsage)
		return 0
	}
	if !ok {
		return 2
	}
	typed := strings.Join(flagArgs, " ")
	fmt.Fprintf(errw, "yolo host: %s names no command to run: the command goes after `--`, as in "+
		"`yolo host %s -- <command>`.\n", typed, typed)
	return 2
}

// hostExecFlags is what the exec half accepts before `--`.
type hostExecFlags struct {
	profile string
	// grant is the --with-credentials request, nil when the flag was not given: the only
	// spelling that makes one (credential-sources-separation.md OQ-ES5, ruled for the host).
	grant *hostGrantRequest
	// help is a --help/-h among the exec flags: parseHostExecFlags stops there, and hostExec
	// prints the usage and exits 0.
	help bool
}

// jailOnlyRunFlags are the launch flags `yolo run` takes and `yolo host --` has no meaning for:
// runFlags less the two the host shares (the profile, and `--at`, a no-op here). Derived, so a
// run flag added later is named here as a jail-launch flag rather than called unknown.
//
// `--accept-config-changes` is among them, by the maintainer's ruling (notch-convergence.md
// OQ-NC10, 2026-09-28): since host-apply-staleness.md's zero-prompt auto-apply the host launch
// asks nothing the flag could answer, so accepting it would be a flag that does nothing, and the
// one question the launch gate keeps, the first-apply MCP loss, stays `yolo host apply
// --assert`'s at a terminal. Its refusal says so (acceptConfigChangesAtHost).
func jailOnlyRunFlags() []string {
	var out []string
	for _, f := range runFlags {
		if f != "--profile" && f != "--at" {
			out = append(out, f)
		}
	}
	return out
}

// acceptConfigChangesAtHost is the line the refusal of `--accept-config-changes` adds at the host
// (OQ-NC10): what the flag would approve there, which is nothing, and where the one question the
// host launch keeps is asked.
const acceptConfigChangesAtHost = "  A host launch has nothing to approve: it re-renders a stale " +
	"home without asking. The one question it keeps, a first apply dropping MCP servers yolo " +
	"does not declare, is asked at a terminal; `yolo host apply --assert` asks it.\n"

// refuseHostNotchContradiction is `yolo host --at <notch>` for a notch other than the host:
// the verb and the flag name two notches, and neither silently wins.
func refuseHostNotchContradiction(notch string, errw io.Writer) {
	known := false
	for _, k := range config.KnownConfinements {
		if config.Confinement(notch) == k {
			known = true
		}
	}
	if !known {
		// The jail launcher's words for the same typo (run.refuseUnbuiltNotch).
		fmt.Fprintf(errw, "yolo host: --at %q is not a confinement level (jail|guest|host)\n", notch)
		return
	}
	fmt.Fprintf(errw, "yolo host: --at %s names the %s notch, and `yolo host` runs at the host "+
		"notch.\n  Drop one of them: `yolo --at %s -- <command>` for that notch, or "+
		"`yolo host -- <command>` for this one.\n", notch, notch, notch)
}

// hostGrantRequest is a --with-credentials request as typed: provider names and `all`, from
// every occurrence of the flag, comma lists split, in the order given.
type hostGrantRequest struct {
	names []string
}

// withCredentialsFlag is the grant's one spelling. A constant because the jail's refusal
// (refuseHostOnlyFlags) names it too, and the two must not drift apart.
const withCredentialsFlag = "--with-credentials"

// addGrantValue folds one --with-credentials value into the request: comma-separated, every
// element a name. An empty element is refused rather than dropped — `--with-credentials ""`
// asking for nothing is a mistake, and a grant that silently grants nothing hides it.
func addGrantValue(req *hostGrantRequest, v string) error {
	for _, n := range strings.Split(v, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			return fmt.Errorf("%s needs a provider name, a comma-separated list of them, or all "+
				"(got %q)", withCredentialsFlag, v)
		}
		req.names = append(req.names, n)
	}
	return nil
}

func parseHostExecFlags(args []string, errw io.Writer) (hostExecFlags, bool) {
	var f hostExecFlags
	grant := func(v string) bool {
		if f.grant == nil {
			f.grant = &hostGrantRequest{}
		}
		if err := addGrantValue(f.grant, v); err != nil {
			fmt.Fprintf(errw, "yolo host: %v\n", err)
			return false
		}
		return true
	}
	// Every value flag through the one reader the jail's parser uses (readValueFlag), so a
	// spelling means the same at both notches: `-p=zai` works here as it does in a jail, and
	// `--profile=` refuses here as it does there, instead of selecting nothing in silence.
	value := func(fl valueFlag) (string, bool) {
		if fl.err != nil {
			fmt.Fprintf(errw, "yolo host: %v\n", fl.err)
			return "", false
		}
		return fl.value, true
	}
	for i := 0; i < len(args); i++ {
		if fl, ok := readValueFlag(args, i, "--profile", "-p"); ok {
			i = fl.last
			v, ok := value(fl)
			if !ok {
				return f, false
			}
			f.profile = v
			continue
		}
		if fl, ok := readValueFlag(args, i, withCredentialsFlag); ok {
			i = fl.last
			v, ok := value(fl)
			if !ok || !grant(v) {
				return f, false
			}
			continue
		}
		// THE NOTCH, which is this verb's own. `--at host` is a no-op, so the front door's
		// spellings and this one agree (routeArgv hands `yolo --at host host -- c` here with the
		// pair intact); any other notch contradicts the verb and is refused by name rather than
		// silently overruled, in either direction.
		if fl, ok := readValueFlag(args, i, "--at"); ok {
			i = fl.last
			v, ok := value(fl)
			if !ok {
				return f, false
			}
			if v != string(config.ConfinementHost) {
				refuseHostNotchContradiction(v, errw)
				return f, false
			}
			continue
		}
		a := args[i]
		// `yolo host --help -- c`, and the front door's `yolo run --at host --help`: help, as
		// `yolo run --help -- c` answers run's.
		if a == "--help" || a == "-h" {
			f.help = true
			return f, false
		}
		// A LAUNCH FLAG WITH NO HOST MEANING is named as one. It reaches here from
		// `yolo --at host --timing -- c` as readily as from `yolo host --timing -- c`, and an
		// "unknown flag" would hide that the flag exists and where it does mean something.
		if name, _, _ := strings.Cut(a, "="); slices.Contains(jailOnlyRunFlags(), name) {
			fmt.Fprintf(errw, "yolo host: %s is a jail-launch flag, and the host notch has no "+
				"meaning for it, so it is refused rather than ignored.\n", name)
			if name == config.AcceptConfigChangesFlag {
				fmt.Fprint(errw, acceptConfigChangesAtHost)
			}
			fmt.Fprint(errw, "  Drop it, or launch in this workspace's jail instead (`yolo run "+
				"--help` lists it).\n")
			return f, false
		}
		// A mistyped flag is refused in refuseUnknownFlags' words, the jail's, so one typo reads
		// the same at both notches; a stray positional keeps its own sentence, since it is not a
		// flag at all.
		if len(a) > 1 && a[0] == '-' {
			refuseUnknownFlags("host", []string{a}, nil, errw)
			return f, false
		}
		fmt.Fprintf(errw, "yolo host: unexpected argument %q before `--`\n\n%s\n", a, hostUsage)
		return f, false
	}
	return f, true
}

// hostProfileFor reads a host -p value in the run path's grammar (parseProfileValue, ES-D27)
// for the one agent this notch composes. A bare name is that name. A cli=name pair naming the
// agent means that bare name, so the jail's `-p claude=codex -- claude` spelling says the same
// thing with `host` added. A pair naming any other CLI is refused by name: the host composes
// one command's environment, and there is no second process for the pair to select for. A
// grant of another provider's key to this process stays --with-credentials' alone, never a
// pair's. envVerb says the value came from `yolo host env`, whose agent is --agent's, so the
// refusal spells that verb's launch.
//
// A LIST (docs/design/active-provider-sets.md OQ-AP1) comes back comma-joined, the spelling
// run.Options carries it in: a bare `-p zai,openrouter` or a pair `-p pi=zai,openrouter`. Which
// lists this one agent may hold is the composition's to decide (packload.ProfileSetProblems),
// and a typed bare list is narrowed, for an agent whose pack declares no provider_sets, by
// narrowHostBareList first (OQ-AP3); the config key's bare list is narrowed by the fold
// (hostProfileFold).
func hostProfileFor(v, agent string, envVerb bool) (string, error) {
	bare, pairs, err := parseProfileValue(v)
	if err != nil {
		return "", err
	}
	if pairs == nil {
		return strings.Join(bare, ","), nil
	}
	clis := make([]string, 0, len(pairs))
	for cli := range pairs {
		clis = append(clis, cli)
	}
	sort.Strings(clis)
	for _, cli := range clis {
		if cli == agent {
			continue
		}
		list := strings.Join(pairs[cli], ",")
		chose, other := "the command after `--`", fmt.Sprintf("`yolo host -p %s -- %s`",
			shquote.Quote(list), shquote.Quote(cli))
		if envVerb {
			chose, other = "--agent, claude by default", fmt.Sprintf("`yolo host env --agent %s -p %s`",
				shquote.Quote(cli), shquote.Quote(list))
		}
		return "", fmt.Errorf("-p %s selects a profile for %q, but this composes the environment "+
			"of %q alone (%s), so a cli=name pair may name only %q: `-p %s=<name>`, or the bare "+
			"`-p <name>`. For %q on that profile, compose it instead: %s. To hand this process "+
			"another provider's key, `%s <provider>` is the grant",
			shquote.Quote(v), cli, agent, chose, agent, shquote.Quote(agent), cli, other, withCredentialsFlag)
	}
	if len(pairs[agent]) == 0 || pairs[agent][0] == "" {
		return "", fmt.Errorf("-p %s names no profile for %q", shquote.Quote(v), agent)
	}
	return strings.Join(pairs[agent], ","), nil
}

// narrowHostBareList is OQ-AP3 (ruled 2026-09-29, option C) at the host: a BARE list, a typed -p
// naming no agent, goes whole to a set-capable agent and its first entry to one that runs one
// provider per session, with the one line naming what it ignores. typed is the -p as typed and
// list what hostProfileFor made of it; a pair is not bare, so its list is left for the
// composition to refuse at a single-provider agent (OQ-AP2). Set capability is read over every
// resolvable pack (config.SetCapableCLINames), since a CLI is installed by one pack wherever it
// is selected; when that universe cannot be enumerated nothing is narrowed, and the composition's
// own check refuses the list rather than dropping part of it in silence.
//
// EVERY ENTRY IS STILL DECLARED (AP-D3): the entries the agent ignores never reach the
// composition, whose declaration check reads only the set it is handed, so they are asked here,
// over the same packs and user profiles that check reads (hostBareTailUndeclared). A typo in the
// tail refuses as it would at pi, and as it does in a jail.
func narrowHostBareList(typed, list, agent string) (string, string, error) {
	entries := packload.SplitProfileList(list)
	if strings.Contains(typed, "=") || len(entries) <= 1 {
		return list, "", nil
	}
	capable, known := config.SetCapableCLINames()
	if !known || capable[agent] {
		return list, "", nil
	}
	if err := hostBareListUndeclared(agent, entries); err != nil {
		return "", "", err
	}
	return entries[0], packload.BareListNote(entries, nil, []string{agent}, false), nil
}

// hostBareListUndeclared refuses the first entry of a bare list that nothing declares, in the
// jail's words (checkProfileDeclarations), every entry in order, so an undeclared first entry is
// named before the tail. The declared names are the ones the composition will read: the selected
// packs for agent on the list's first entry and the user's profile declarations. A selection or a
// profiles file the composition refuses is left for it to refuse, in its own words, so this adds
// no second message for one problem.
func hostBareListUndeclared(agent string, entries []string) error {
	sel := loadedHostPacks(config.UserScopeConfigOrEmpty(), agent, entries[0])
	if sel.launchRefusal() != nil {
		return nil
	}
	userProfiles, err := config.LoadProfiles(nil)
	if err != nil {
		return nil
	}
	declared := packload.DeclaredProfileNames(sel.packs, userProfiles)
	for i, name := range entries {
		if !slices.Contains(declared, name) {
			return fmt.Errorf("packs: profile %q (entry %d of the bare -p list %s): %s", name, i+1,
				strings.Join(entries, ","), packload.UndeclaredProfileMessage(name, declared))
		}
	}
	return nil
}

// hostExec composes the environment and launches the target.
//
// Ordinary launches still use syscall.Exec. Managed Codex stays resident because its
// dynamic loopback credential adapter must be closed when the agent exits, and so does a
// launch that starts a pack service's host half or opens a credential doorway, which stay
// the agent's parent (launchservice.RunAgent) and close both when it exits.
func hostExec(flagArgs, cmd []string, out, errw io.Writer, stdin io.Reader) int {
	flags, ok := parseHostExecFlags(flagArgs, errw)
	if flags.help {
		fmt.Fprintln(out, hostUsage)
		return 0
	}
	if !ok {
		return 2
	}
	if len(cmd) == 0 {
		fmt.Fprintf(errw, "yolo host: nothing to run after `--`\n\n%s\n", hostUsage)
		return 2
	}
	profile, err := hostProfileFor(flags.profile, filepath.Base(cmd[0]), false)
	if err != nil {
		fmt.Fprintf(errw, "yolo host: %v\n", err)
		return 2
	}
	profile, bareNote, err := narrowHostBareList(flags.profile, profile, filepath.Base(cmd[0]))
	if err != nil {
		fmt.Fprintf(errw, "yolo host: refusing to launch: %v\n", err)
		return 1
	}
	if bareNote != "" {
		fmt.Fprintf(errw, "yolo host: %s\n", bareNote)
	}
	flags.profile = profile
	// THE HOST-RENDER GATE, before anything else this function does (hostapplygate.go, and
	// docs/reference/host-apply-staleness.md §4.1). It is the host notch's answer to the jail's
	// launch-time config approval, and it sits FIRST for the reason the credential pre-flight
	// below gives for its own placement: a launch that is going to be stopped should be stopped
	// while the only thing it has done is read some files. It is silent unless the user opted
	// in, and it is a no-op in a jail.
	// THE PACK REFRESH, above the gate: the gate's observe pass and the composition below
	// both resolve the selected packs, and a never-fetched git pack must be fetched (and a
	// branch-following one refreshed hourly) before either reads the store, exactly as a
	// jail launch does (hostpackrefresh.go). stderr, like the gate: an agent's stdout is
	// routinely parsed.
	refreshHostPacks(errw)
	if !hostApplyGate(errw, stdin, cmd[0]) {
		return 1
	}

	// hostServicesStart: this is the one front door that owns its command's lifetime, so a
	// profile paired through a pack service runs that service's host half for the command
	// (docs/design/host-notch-services.md; OQ-NC1 A, OQ-HS3 per launch).
	launch := composeHostLaunchWith(cmd[0], flags.profile, flags.grant, func(msg string) {
		fmt.Fprintf(errw, "Warning: %s\n", msg)
	}, hostServicesStart)

	// THE PROVIDER COMPOSITION's own refusal, before anything else: a provider table this
	// notch cannot compose is one no launch may exec from, and the credential pre-flight
	// below would be answering a question about a table that was never built. Same exit
	// the pre-flight takes, same renderer's shape — verdict first, then the remedy.
	if launch.err != nil {
		fmt.Fprintf(errw, "yolo host: refusing to launch: %v\n", launch.err)
		return 1
	}
	// THE PACKS THE SELECTION CLOSURE JOINED, before any line that may name one of them, as a
	// jail launch prints them before its pre-flights (WB-D12).
	for _, line := range launch.selectionLines() {
		fmt.Fprintf(errw, "yolo host: %s\n", line)
	}
	// WHERE THE PROFILE SELECTION LANDED, the line a jail launch prints (noteUseProfiles),
	// from the same function (notch-convergence item 13, row A7).
	for _, line := range launch.profileLines() {
		fmt.Fprintf(errw, "yolo host: %s\n", line)
	}
	// THE OQ-SSO8 CHECK, before the credential pre-flight as a jail runs it: a pack's env
	// contribution this launch delivers beside something the pack declares overrides it. A
	// certain override refuses, with no hatch (packload's envoverride.go says why); an
	// uncertain one warns and the launch goes on. It never ran at this notch, so a bearer
	// beside the pointer it silently beats reached the agent here while every jail refused it.
	refusal, warnings := launch.envOverrideLines(os.Getenv)
	for _, w := range warnings {
		printHostLines(errw, w)
	}
	if len(refusal) > 0 {
		printHostLines(errw, refusal)
		return 1
	}

	// THE CREDENTIAL PRE-FLIGHT at the host notch (docs/reference/providers.md#the-credential-preflight,
	// #pv-oq-13) — the same check the jail's launcher runs, on the environment THIS notch
	// would exec with. Before resolveHostTarget, deliberately: a launch that would fail
	// at the agent's first API call should be refused while the only thing it has done is
	// compose an environment.
	//
	// It lives here and not inside the composition because `yolo host env` shares that
	// composition and is an OBSERVE verb — a debugging front door that has to answer even
	// when the answer is "this launch is missing a key".
	//
	// ONE REFUSAL AT BOTH NOTCHES (packload.ProviderCredentialRefusal, notch-convergence.md
	// item 14): the verdict, the facts, the remedy, or the override notice that says what it is
	// suppressing without re-offering the hatch it just honored. Only the "yolo host: " prefix
	// is this notch's.
	// PP-D1 (docs/design/providers-and-profiles-redesign.md, ruled 2026-09-29), before the
	// pre-flights as the jail's arms print it: the launched agent's own config switching it onto
	// a platform its selection does not serve — CLAUDE_CODE_USE_BEDROCK in the real
	// ~/.claude/settings.json while claude's provider is not Bedrock — is named in one line with
	// both fixes. yolo deletes nothing it did not write; the agent may still reach the platform
	// on credentials of the user's own that this shell passes through, which is why the line says
	// what yolo delivers rather than that the launch will fail.
	for _, c := range launch.platformSwitchConflicts() {
		printHostLines(errw, []string{c.Line()})
	}
	held := os.Getenv(paths.AllowMissingProvidersEnv) != ""
	lines, refuse := packload.ProviderCredentialRefusal(launch.credentialGaps(os.Getenv), held)
	printHostLines(errw, lines)
	// THE REGION HALF (OQ-BR6, docs/design/bedrock-plumbing.md §8), beside the credential half
	// as the jail's checkProviderCredentials runs it: the same facts (packload.ProviderRegionGaps),
	// the same renderer, the same hatch, answered against the environment this notch execs.
	regionLines, regionRefuse := packload.ProviderRegionRefusal(launch.regionGaps(), held)
	printHostLines(errw, regionLines)
	if refuse || regionRefuse {
		return 1
	}

	// THE CREDENTIAL GATE'S DISCLOSURE (docs/design/provider-credential-scope.md §4, "no
	// silent narrowing"): a launch that withholds a credential the user configured says so,
	// on stderr like every other line here, names only. The same wording the jail notch
	// prints, because it is the same gate's answer.
	//
	// THE GRANT'S DISCLOSURE (OQ-ES5) follows it: on every run the flag is given, whatever it
	// delivered, names only. Never suppressible (OQ-RO3), so it is printed unconditionally
	// here rather than folded into a line a quieter path could skip.
	//
	// THE REGION FILL'S DISCLOSURE (BR-DIR1) leads them: a region the gate read from the region
	// file for this agent, with the file and the profile, in the jail's words.
	for _, block := range [][]string{launch.regionLines(), launch.credentialScopeLines(), launch.grantLines()} {
		printHostLines(errw, block)
	}

	// WHICH BINARY RUNS (HP-DIR4, host-launch-environment.md §3): a bare name of a program a
	// selected pack delivers is the FLOOR's copy, installed first when missing; a path is exec'd
	// as given; anything else is looked up on the child's PATH — the LAUNCH PATH (the caller's
	// PATH, then `host_path`'s folders not already on it: HE-DIR1, HE-D3), then the floor's bin/
	// (OQ-HE10 (c), HE-D1), which is also the PATH the child is handed below. The launch PATH is
	// resolved once, here, for both.
	lp := hostLaunchPath()
	childPath := hostChildPath(lp, hostFloorBinDir())
	resolved, rc := resolveHostLaunchTarget(launch.packs, cmd[0], lp, errw)
	if rc != 0 {
		return rc
	}
	target := resolved.Path
	// argv[0] stays the name the user typed, not the resolved path: agents branch on it
	// (usage text, `$0`), and handing them an absolute path changes what they print.
	argv := injectHostLaunchFlags(launch.packs, append([]string{cmd[0]}, cmd[1:]...), errw)
	// THE PROGRAM'S MODEL MENU (docs/design/model-lists-and-pickers.md §14.7, MM-D24 to MM-D28):
	// the jail launcher's step, run here against the resolved target with the list this launch
	// composed, and only where the launch's provider is the configured profile's. After the
	// binary resolves and the pack's flags are added, so the catalog is the program that runs;
	// its flag goes right after argv[0], ahead of those flags, and is disclosed in their words.
	// The menu's lock is held for the program's life: by this process where it stays resident
	// (the deferred Close), and by the program itself across the exec below.
	menu := launch.modelMenu(target, launch.childEnviron(childPath), errw)
	defer menu.Close()
	argv, menuLines := menu.rewrite(argv)
	printHostLines(errw, menuLines)
	// THE DECLARATIVE OPENAI PRELAUNCH (notch-convergence item 15): what the launched command's
	// pack declares, from the composition, logging in only where a human can answer the browser
	// login. It used to switch on the command's name and log in regardless of profile or terminal.
	managed, err := prepareOpenAIAuthHost(launch.prelaunch(hostGateCanPrompt()), errw)
	if err != nil {
		fmt.Fprintf(errw, "yolo host: prepare shared OpenAI authentication: %v\n", err)
		return 1
	}
	// THE MANAGED LAUNCH'S OWN ARGV REWRITE (OQ-CDX1): a managed Codex launch runs with
	// --no-daemon, so it never attaches to a background server an earlier launch left running
	// with that launch's refresh address. Before both exec paths below, and disclosed like the
	// pack flags above: a launch has no quiet mode.
	if managed != nil {
		var disclosure []string
		argv, disclosure = managed.Argv(argv)
		printHostLines(errw, disclosure)
	}
	// WHAT THIS NOTCH WITHHOLDS BECAUSE NOTHING HERE SERVES IT (notch convergence item 2),
	// after the managed launch is prepared, because that launch serves one of them itself:
	// `yolo host -- codex` runs its own refresh adapter and sets the URL the codex pack's
	// pointer names, so that one is not missing and is not named.
	printHostLines(errw, launch.unservedLines(managedHostVars(managed)))
	environ := launch.childEnviron(childPath)
	// THE LAUNCH-OWNED SERVICES (docs/design/host-notch-services.md §4.4): started after the
	// agent resolved on PATH and after the prelaunch, so a missing agent starts nothing and the
	// OpenAI login exists before the bridge asks for a view; the agent starts only once each
	// service is listening, and every one stops when the agent exits. Said on stderr, every
	// time: this is host code yolo runs on the user's machine, and a launch has no quiet mode.
	//
	// THE DOORWAYS FIRST (HS-D15, HS-D21; run.HostDoorways.Start): the host service each one
	// forwards to, fronted for this launch, then the doorway, as the macos-user arm orders them
	// (HS-D19). The fronts and their session dir close after the agent's parent has stopped the
	// doorways, when this function returns.
	if len(launch.services) > 0 || len(launch.doorways.Plans()) > 0 {
		if managed != nil {
			environ = managed.Environ(environ)
		}
		running, stopHostServices, lines, err := launch.doorways.Start(launch.cfg, launch.workspace,
			launch.agent, errw, startLaunchService)
		for _, line := range lines {
			fmt.Fprintf(errw, "yolo host: %s\n", line)
		}
		if err != nil {
			fmt.Fprintf(errw, "yolo host: refusing to launch: %v\n", err)
			return 1
		}
		defer stopHostServices()
		for _, plan := range launch.services {
			r, err := startLaunchService(plan, launch.serviceInput())
			if err != nil {
				for _, started := range running {
					started.Stop()
				}
				fmt.Fprintf(errw, "yolo host: refusing to launch: %v\n", err)
				return 1
			}
			running = append(running, r)
			// The addresses the agent's provider environment points it at, the only routes the
			// service opens (HS-D24): never read out of environ, whose wire tables (FT-D2) name
			// every address the plan moved.
			fmt.Fprintf(errw, "yolo host: started the %q service (pack %q, pid %d) for %s on %s; "+
				"it answers only this launch's caller token and stops when %s exits. Its log: %s\n",
				plan.Service, plan.Pack, r.PID(), launch.agent,
				strings.Join(plan.PointedAt(launch.scope.Agent(launch.agent)), ", "), launch.agent, r.Log)
		}
		// WHAT STARTS, AND FROM WHERE, the last line before the hand-over: a slow agent startup
		// is then visibly the agent's, not yolo's.
		fmt.Fprintln(errw, hostStartingLine(cmd[0], resolved))
		return launchservice.RunAgent(target, argv, environ, stdin, out, errw, running,
			hostServiceSignals, "yolo host: ")
	}
	// The same line on the exec path — a managed launch that stays resident included, since it
	// runs the same target.
	fmt.Fprintln(errw, hostStartingLine(cmd[0], resolved))
	if managed != nil {
		environ = managed.Environ(environ)
		if rc, handled := managed.Run(target, argv, environ, stdin, out, errw); handled {
			return rc
		}
	}
	// Given back BEFORE the exec, because cli.Main's deferred release never runs once this
	// process has been replaced — and every host wrapper launch comes through here. With the
	// shared cache tree that closes a lease the exec would drop anyway (close-on-exec); with a
	// per-process FALLBACK tree it is the only thing that deletes it. Nothing after the exec
	// reads a Pack.Root: the host-apply sync above rendered copies out of it.
	packload.ReleaseEmbedded()
	// The menu's lock crosses the exec, so the program holds it for its own life (MM-D27): the
	// deferred Close above never runs once this process is replaced.
	menu.KeepAcrossExec(errw)
	if err := hostSyscallExec(target, argv, environ); err != nil {
		fmt.Fprintf(errw, "yolo host: exec %s: %v\n", target, err)
		return 126
	}
	return 0 // unreachable: a successful Exec never returns
}

// startLaunchService starts one launch-owned service; a var so a test can observe what started.
var startLaunchService = launchservice.Start

// hostServiceSignals is the channel a launch with a service reads its signals from, nil for this
// process's own; a var so a test can deliver one without signalling itself.
var hostServiceSignals chan os.Signal

// serviceInput is what every launch-owned service of this composition is handed
// (launchservice.Input): the three wire tables for the one agent, the host broker's private
// socket, and the env_sources the credential gate delivers to that agent for its provider
// (AgentDelivery.EnvSources), so a service reaches exactly the credential of the provider it
// serves and no other. The caller token is added by launchservice.Start.
func (c *hostComposition) serviceInput() map[string]string {
	env := c.wireTables()
	env[openauthclient.HostSocketEnv] = openaiauthhost.HostSocketPath()
	if d := c.scope.Agent(c.agent); d != nil && d.EnvSources != nil {
		for _, k := range d.EnvSources.Keys() {
			if v, _ := d.EnvSources.Get(k); v != nil {
				if str, ok := v.(string); ok {
					env[k] = str
				}
			}
		}
	}
	return env
}

// wireTables is this launch's three wire tables, serialized and keyed by the names in
// entrypoint.WireTables: the composed provider table, the resolved profile table, and a
// selection holding this one agent's entry alone ("{}" when it selected no profile). ONE
// composition for both of its readers, the agent's own environment (step 3b of
// composeHostVarsWith; docs/design/agent-footer.md FT-D2) and every launch-owned service
// (serviceInput), so the footer, `yolo host env` and a launch's bridge are never handed
// different tables for one launch. A jail's channel writes the same three names
// (run's packChannel.wireTableValues). A fresh map each call.
func (c *hostComposition) wireTables() map[string]string {
	use := jsonx.NewOrderedMap()
	if c.profile != "" {
		set := c.set
		if len(set) == 0 {
			set = []string{c.profile}
		}
		use.Set(c.agent, packload.ProfileSetWire(set))
	}
	return map[string]string{
		entrypoint.ProvidersWireEnv:   wireJSON(c.providers),
		entrypoint.ProfilesWireEnv:    wireJSON(packload.ProfilesWireTable(c.resolved)),
		entrypoint.UseProfilesWireEnv: wireJSON(use),
	}
}

// printHostLines writes one pre-flight or disclosure block the way this notch names itself:
// the first line after "yolo host: ", the rest as they are. The block's wording is packload's,
// shared with the jail notch, so this prefix is the only part of it that is the host's.
func printHostLines(errw io.Writer, lines []string) {
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(errw, "yolo host: %s\n", line)
			continue
		}
		fmt.Fprintln(errw, line)
	}
}

// injectHostLaunchFlags is the host notch's argv rewrite: the SAME injector the jail launcher
// and every in-jail carrier call (packload.InjectLaunchFlags), handed the host notch's posture
// bit, so a pack's `guarded.launch` entry reaches `yolo host -- <bin>` and its `autonomous` one
// never does (docs/plans/notch-convergence.md item 20, row D9). Before this the host exec'd the
// argv as typed, and a guarded launch flag reached no notch at all.
//
// The rewrite is DISCLOSED in the jail's words (LaunchInjection.DisclosureLines), on stderr like
// every other line this launch prints, and is never suppressible (OQ-RO3). Silent when nothing
// was rewritten, which is every shipped pack today: none declares a guarded launch flag.
func injectHostLaunchFlags(packs []*packload.Pack, argv []string, errw io.Writer) []string {
	out, inj := packload.InjectLaunchFlags(packs, render.ProfileFor(render.KindHost).AgentAutonomy, argv)
	printHostLines(errw, inj.DisclosureLines())
	return out
}

// hostSyscallExec is the exec `yolo host` replaces itself with; a var so a test can pin
// what has already happened by the time it runs (host_test.go) without replacing the test
// process.
var hostSyscallExec = syscall.Exec

// resolveHostTarget finds the real binary for a host launch, skipping yolo's OWN
// generated directories.
//
// THIS IS THE RECURSION GUARD, and it is load-bearing for the entire wrapper design:
// <wrap dir>/claude is `exec yolo host -- claude`, so an ordinary PATH lookup would find
// the wrapper again and exec it, forever. It is a separate function rather than an inline
// call so a test can pin the CALL SITE — passing an empty skip list here compiles, passes
// every callee test in internal/hostwrap, and fork-bombs in production.
func resolveHostTarget(pathEnv, bin string) (string, error) {
	return hostwrap.LookPathSkipping(pathEnv, bin, yoloManagedDirs())
}

// yoloManagedDirs are the directories a host PATH lookup must skip. The whole generated
// tree is named rather than just bin/wrap, so bin/block and bin/launch are covered the
// day they exist without this list needing to be revisited. It is the launch PATH's own skip
// list (hostpath.ManagedDirs), so the checks and the exec skip the same folders (HE-D5).
func yoloManagedDirs() []string {
	return hostpath.ManagedDirs()
}

// hostComposition is one host agent launch's composed environment, with the facts the
// credential pre-flight (providers.md#the-credential-preflight) reads beside it. It exists because the pre-flight has to
// answer against the SAME packs, the SAME composed provider table and the SAME env_sources
// walk the vars were composed from — loading them a second time would not just double the
// work, it would let the check and the exec disagree about what the launch carries.
type hostComposition struct {
	// agent is the CLI name the profile table is keyed by (the target's basename).
	agent string
	// vars is the composition proper, in application order: the pack env fold (per pack,
	// static then that pack's profile-gated entries), env_sources, the provider's env
	// shape, the three wire tables (wireTables), and the removals last.
	vars []agentenv.Var
	// packs and providers are the selected pack set and the composed provider table the
	// vars were composed from.
	packs     []*packload.Pack
	providers *jsonx.OrderedMap
	// err is the refusal composing the provider table produced, if any. A field and not
	// a second return because every consumer of this composition already owns an exit:
	// the exec path prints and refuses, `yolo host env` returns an error to its caller.
	// A caller that ignores it would exec an environment the launch refused to compose.
	err error
	// consulted is everything this launch asked for credentials: the env_sources entries
	// it walked (relative ones already dropped, with their own warning) and the invoking
	// shell's environment.
	consulted []string
	// envSources is consulted's env_sources half alone, for the region pre-flight, which
	// names the channels it looked in under its own headings (packload.RegionConsulted).
	envSources []string
	// scope is the CREDENTIAL GATE's answer for this one-agent launch
	// (packload.ScopeCredentials, docs/design/provider-credential-scope.md OQ-CN5): the
	// same function the jail notch's composePackChannel calls, over this notch's user-scope
	// inputs. vars were composed from it; the pre-flight narrows by it and the exec path
	// discloses it. Nil only when the composition refused before reaching the gate.
	scope *packload.CredentialScope
	// resolved is the launch's resolved profile table — every declared profile and the
	// provider it selects — which the disclosure's remedy reads to name a profile that would
	// deliver a withheld credential (credentialRemedy).
	resolved map[string]packload.ResolvedProfile
	// unservedVias are the profiles whose via this notch cleared (packload.ViaServedAt),
	// sorted, for the disclosure to name.
	unservedVias []string
	// command is the command as the user typed it after `--`, for the remedy to spell back;
	// empty for `yolo host env`, which launches nothing.
	command string
	// scopeInput is the credential gate's input for this launch, for the remedy to ask the gate
	// whether a -p it would name composes (runsOn).
	scopeInput packload.ScopeInput
	// profile is the profile this launch selected for its command — a typed -p, else the
	// command's entry in the config `profile` key — "" when none. The remedy says the -p it names replaces
	// it (remedyAction). For an active set it is the set's primary.
	profile string
	// set is the command's whole active set (docs/design/active-provider-sets.md), profile
	// first; nil when profile is "".
	set []string
	// bareList is the config key's BARE list when it reached this command narrowed to its first
	// entry (OQ-AP3: the command's pack declares no provider_sets), nil otherwise; bareNote is
	// the one line saying so, which the launch prints with its profile lines. The list's ignored
	// entries must still be declared (AP-D3).
	bareList []string
	bareNote string
	// grant is the --with-credentials request this launch was given, resolved; nil without
	// the flag. Its disclosure is grantLines.
	grant *hostGrant
	// typedProfile is the -p as typed, "" when none: what a remedy that re-runs this launch
	// with its grant widened must spell again (grantRemedy). profile can instead come from
	// the config `profile` key, which the re-run picks up by itself.
	typedProfile string
	// configuredProfile is the primary profile the user-scope `profile` key selects for this
	// agent, "" when it selects none: profile's value had no -p been typed, which is the one
	// `yolo host apply` writes into the agent's own config. The model menu builds only where
	// the two select one provider (docs/design/model-lists-and-pickers.md MM-D25).
	configuredProfile string
	// services are the launch-owned services this composition planned (hostServicesStart), or
	// the one a pairing needs (hostServicesDetect, with no ports or token): zero or one, since
	// one agent resolves one pairing (docs/design/host-notch-services.md §4.2).
	services []*launchservice.Plan
	// doorways are the credential doorways this launch opens for its agent
	// (run.PlanHostDoorways; docs/design/host-notch-services.md HS-D15, HS-D21), planned only
	// by the front door that runs the agent, and the reason each one it leaves closed is closed.
	doorways *run.HostDoorways
	// served is the served set the credential gate composed this launch against: the doorways
	// and services above, at their settled addresses, marked as the host notch's. The OQ-SSO8
	// check reads it too, so a pointer this launch delivers is checked for its overrides.
	served packload.ServedDaemons
	// cfg and workspace are the user-scope config and the directory this launch composed from,
	// for the doorways' host services, which read the loophole settings from that config.
	cfg       *jsonx.OrderedMap
	workspace string
	// selection is the one selection function's answer this launch composed from: packs is its
	// complete set, and its causes are the packs the closure joined (selectionLines).
	selection hostPackSet
	// origins is, index for index with vars, the delivery channel each var came from (the
	// packload.From* phrases, fromRemoval for an unset), for the env-override check's lookup
	// (envOverrideFindings), which names where a delivered variable came from. originPacks is,
	// index for index, the pack each var is attributed to: the declaring pack for a pack `env`
	// var, the agent's own installing pack for a shape var (its env derive's output), and ""
	// for an env_sources value or a removal.
	origins, originPacks []string
}

// prelaunch is the declarative OpenAI prelaunch this launch's composition carries for its
// command (docs/plans/notch-convergence.md item 15, row C6): the YOLO_AUTH_PRELAUNCH_<BIN>_*
// values a jail's launcher reads, read here from the same composed variables the agent is
// handed, so the prelaunch fires exactly when the agent's composition delivers it. pi's env
// derive composes the view only when pi's selected provider is openai-codex, whatever the profile
// is named (OQ-BR8), so `yolo host -p zai -- pi` declares none and starts no login.
// Pack is the pack whose contribution set the view flag (or the login), which keys the managed
// home. interactive is whether a human can answer a login.
func (c *hostComposition) prelaunch(interactive bool) openaiauthhost.Prelaunch {
	p := openaiauthhost.Prelaunch{Bin: c.agent, Interactive: interactive}
	flagVar := openaiauthhost.PrelaunchVar(c.agent, "FLAG")
	loginVar := openaiauthhost.PrelaunchVar(c.agent, "LOGIN")
	for i, v := range c.vars {
		value := v.Value
		if v.Unset {
			value = ""
		}
		switch v.Key {
		case flagVar:
			p.Flag, p.Pack = value, c.originPacks[i]
		case loginVar:
			p.Login = value != ""
			if p.Flag == "" {
				p.Pack = c.originPacks[i]
			}
		}
	}
	return p
}

// fromRemoval marks a var in hostComposition.origins that UNSETS its name: an env_sources null.
// It is never printed; envOverrideFindings' lookup reads it as "not delivered".
const fromRemoval = "a removal"

// profileLines are the launch's profile disclosure (packload.ProfileDisclosures, the lines a jail
// launch prints) over this launch's one-agent table, its selected packs and the composition the
// gate composed it against: the line, then any warning that the selection reaches nothing for
// the agent or delivers it no credential here. "Reaches the agent" is environ(), the environment
// the exec hands it, which at this notch includes the invoking shell.
func (c *hostComposition) profileLines() []string {
	table := map[string]string{}
	var sets map[string][]string
	if len(c.set) > 0 {
		table[c.agent] = c.set[0]
		sets = map[string][]string{c.agent: c.set}
	}
	env := map[string]string{}
	for _, kv := range c.environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	var out []string
	// Over the agent's whole ACTIVE SET (docs/design/active-provider-sets.md): every entry says
	// where it landed, beside the set's own line and the key's bare-list note.
	for _, d := range packload.ProfileDisclosures(packload.ProfileDisclosureInput{
		Table: table, Sets: sets, Packs: c.packs, Resolved: c.resolved, Providers: c.providers,
		Scope:   c.scope,
		Reaches: func(agent, name string) bool { return agent == c.agent && env[name] != "" },
	}) {
		out = append(append(out, d.Line()), d.Warnings()...)
	}
	out = append(out, packload.ActiveSetLines(sets)...)
	if c.bareNote != "" {
		out = append(out, c.bareNote)
	}
	return out
}

// envOverrideFindings is the OQ-SSO8 check (packload.EnvOverrideFindings, the one a jail launch
// runs as its ninth pre-flight) over this launch: its selected packs, its one-agent profile
// table, the notch's served set, and a lookup answering where each variable the agent would
// receive comes from. getenv is the process lookup, passed as credentialGaps takes it.
//
// THE INHERITED SHELL IS A DELIVERY HERE, and that is the one input that differs from a jail's
// (P2: a named input, not a second check). No jail backend forwards the environment yolo was
// launched from, so the jail's lookup answers "not delivered" for it (jailOriginLookup); an
// agent `yolo host` execs inherits that environment whole, so a bearer exported in the user's
// shell does reach it, and does override a pointer the launch delivers.
//
// No host_files destination is rendered at this notch (OQ-NC8 owns whether one should be), so
// a `host_file` override is never evaluated here: nil, which costs a false negative and never a
// false refusal. The host's own files are the agent's own at this notch, which is a question for
// that ruling, not for this check.
func (c *hostComposition) envOverrideFindings(getenv func(string) string) []packload.EnvOverrideFinding {
	final := map[string]string{}
	for i, v := range c.vars {
		if v.Unset || v.Value == "" { // an empty value reads as unset (OriginLookup's contract)
			final[v.Key] = fromRemoval
			continue
		}
		final[v.Key] = c.origins[i]
	}
	lookup := func(name string) (string, bool) {
		if origin, composed := final[name]; composed {
			return origin, origin != fromRemoval
		}
		if getenv(name) != "" {
			return packload.FromLaunchEnv, true
		}
		return "", false
	}
	sets := map[string][]string{}
	if c.profile != "" {
		sets[c.agent] = c.set
		if len(c.set) == 0 {
			sets[c.agent] = []string{c.profile}
		}
	}
	// THE SERVED SET THE GATE COMPOSED THIS LAUNCH AGAINST, doorways included: a pointer this
	// launch delivers, aws-auth's at a doorway `yolo host --` opens, is checked for its overrides
	// exactly as a jail checks it (notch-convergence item 13's done-when, NC-D34), and one it
	// withholds has nothing to override.
	served := c.served
	// The gate's view of this one-agent selection, platform included (OQ-BR8), built from the
	// table and resolution the credential gate composed this launch against, over the agent's
	// whole active set (AP-P1).
	sel := packload.SelectionOfSets(sets, c.resolved, c.providers)
	// NO HOST FILES, ON PURPOSE. A pack's `host_file` override (aws-auth's `.aws`) asks whether
	// the launch renders that file for the agent, and at the host the agent reads the user's own
	// home: every aws-auth user has a ~/.aws, since the service mints from its SSO login, so
	// passing it would print the pack's MAY-override warning on every host launch, whatever the
	// profile the agent's SDK resolves holds, against OQ-SSO8's "never a false positive"
	// condition. Whether that profile holds credentials is AWS knowledge core may not carry
	// (OQ-SSO8 condition 1). So the host cannot tell when ~/.aws wins over the doorway's pointer,
	// and host-notch-services.md §4.8 says so instead.
	return packload.EnvOverrideFindings(c.packs, sel, lookup, nil, &served)
}

// doorwayLaunchSpelling is the `yolo host` launch that opens this composition's doorways, for
// the reason a front door that runs no process gives for leaving them closed: the typed -p when
// there was one, since the config's `profile` key alone would not select the same profile.
func (c *hostComposition) doorwayLaunchSpelling() string {
	if c.typedProfile != "" {
		return "`yolo host -p " + shquote.Quote(c.typedProfile) + " -- " + shquote.Quote(c.agent) + "`"
	}
	return "`yolo host -- " + shquote.Quote(c.agent) + "`"
}

// envOverrideLines splits envOverrideFindings into what refuses, as the lines of one refusal
// (packload.EnvOverrideRefusal's shape), and what only warns, one block per finding.
func (c *hostComposition) envOverrideLines(getenv func(string) string) (refusal []string, warnings [][]string) {
	for _, f := range c.envOverrideFindings(getenv) {
		if f.Certain {
			refusal = append(refusal, f.Lines...)
		} else {
			warnings = append(warnings, f.Lines)
		}
	}
	return refusal, warnings
}

// selectionLines are the cause lines of every pack the selection closure joined to this launch,
// one each, as a jail launch prints them (WB-D12: a pack no config line named never joins a
// launch in silence).
func (c *hostComposition) selectionLines() []string {
	return append([]string(nil), c.selection.causes...)
}

// hostGrant is a --with-credentials request resolved against this launch's composed provider
// table: the providers it names, `all` expanded.
type hostGrant struct {
	// spelled is the request as typed, comma-joined, for the disclosure to quote back.
	spelled string
	// providers is every provider the grant hands the command, sorted: the named ones, plus
	// every one claiming an env_sources value when `all` was among the names.
	providers []string
}

// resolveHostGrant resolves a --with-credentials request over the launch's composed provider
// table and its hydrated env_sources. A name the table does not hold REFUSES, naming the known
// ones, because a typo that silently granted nothing would read as "the provider has no key".
// `all` is every provider that claims a name env_sources holds (packload.ClaimingProviders,
// the gate's own claim model), and it may stand beside names.
func resolveHostGrant(req *hostGrantRequest, providers, envSources *jsonx.OrderedMap) (*hostGrant, error) {
	var known []string
	if providers != nil {
		known = append(known, providers.Keys()...)
	}
	sort.Strings(known)
	var named, unknown []string
	all := false
	for _, n := range req.names {
		switch {
		case n == "all":
			all = true
		case slices.Contains(known, n):
			named = append(named, n)
		default:
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		quoted := make([]string, len(unknown))
		for i, u := range unknown {
			quoted[i] = fmt.Sprintf("%q", u)
		}
		if len(known) == 0 {
			return nil, fmt.Errorf("%s names %s, and no provider is composed at this notch: no "+
				"selected pack ships one and %s declares none under `providers`",
				withCredentialsFlag, strings.Join(quoted, ", "), paths.UserConfigPath())
		}
		return nil, fmt.Errorf("%s names %s, which no composed provider is: the known providers "+
			"are %s (or `all`, every one that claims a value in env_sources)",
			withCredentialsFlag, strings.Join(quoted, ", "), strings.Join(known, ", "))
	}
	if all {
		named = append(named, packload.ClaimingProviders(providers, envSources)...)
	}
	sort.Strings(named)
	return &hostGrant{spelled: strings.Join(req.names, ","), providers: slices.Compact(named)}, nil
}

// grantLines is the grant's disclosure, nil without one: a header saying what a grant is and
// who holds it, then one line per granted provider naming what it delivered — or that it
// delivered nothing, which is reported rather than skipped. Names only, never a value.
func (c *hostComposition) grantLines() []string {
	if c.grant == nil {
		return nil
	}
	subject, heirs, owner := "the script exports", "every process the eval'ing shell starts",
		shquote.Quote(c.agent)+"'s slice"
	if c.command != "" {
		cmd := shquote.Quote(c.command)
		subject, heirs, owner = cmd+" receives", "every process "+cmd+" starts", cmd
	}
	header := fmt.Sprintf("Credential grant (%s %s): %s the granted providers' claimed "+
		"env_sources values, keys only — the grant selects no profile and re-points nothing, "+
		"and %s inherits them", withCredentialsFlag, c.grant.spelled, subject, heirs)
	if c.profile != "" {
		header += fmt.Sprintf("; %s keeps its %s profile, and the grant only adds keys beside it",
			owner, c.profile)
	}
	lines := []string{header}
	if len(c.grant.providers) == 0 {
		return append(lines, "  nothing granted: no composed provider claims a value env_sources holds")
	}
	for _, g := range c.scope.GrantedTo(c.agent) {
		switch {
		case len(g.Delivered) > 0:
			lines = append(lines, fmt.Sprintf("  %s: %s", g.Provider, strings.Join(g.Delivered, ", ")))
		case len(g.Claims) == 0:
			lines = append(lines, fmt.Sprintf("  %s: nothing granted — it claims no credential "+
				"name (no api_key_env_name), so there is no value to hand over", g.Provider))
		default:
			lines = append(lines, fmt.Sprintf("  %s: nothing granted — env_sources holds no value "+
				"for the names it claims (%s)", g.Provider, strings.Join(g.Claims, ", ")))
		}
	}
	return lines
}

// credentialScopeLines is the gate's disclosure for this launch, nil when nothing the user
// configured was scoped. It is packload's wording, the jail notch's lines, plus what only this
// notch can say (docs/design/credential-sources-separation.md): a withheld line names the typed
// `-p` that would deliver it (credentialRemedy, ES-D2), and a withheld name the process holds
// anyway — the invoking shell's own value (ES-D4), or one yolo composed from another source —
// is said to be held, never withheld (processHolds).
func (c *hostComposition) credentialScopeLines() []string {
	if c.scope == nil {
		return nil
	}
	inherited, composed := c.processHolds()
	return c.scope.DisclosureWith(packload.DisclosureNotes{
		Remedy:    c.credentialRemedy,
		Inherited: inherited,
		Composed:  composed,
	})
}

// regionLines is the region fill's disclosure for this launch (packload's RegionLines,
// docs/design/bedrock-plumbing.md BR-DIR1): the region the gate read from the region file for
// this agent, the file and the profile, in the words the jail prints. Nil when it read none.
func (c *hostComposition) regionLines() []string {
	return c.scope.RegionLines()
}

// unservedLines names what this notch withheld because nothing here serves it — a pack env
// variable pointing at a jail daemon, a profile's via (P4, notch convergence item 2) — in the
// words every notch prints (packload.UnservedLines). Header first, like the disclosure.
//
// servedByLaunch is what the launch serves itself (managedHostVars), nil for none.
func (c *hostComposition) unservedLines(servedByLaunch func(string) bool) []string {
	return packload.UnservedLines(c.scope, c.unservedVias, servedByLaunch)
}

// managedHostVars reports the variables a managed host launch sets from a server of its own
// (openaiauthhost's Codex adapter), nil when there is no managed launch.
func managedHostVars(managed managedOpenAIHostLaunch) func(string) bool {
	if managed == nil {
		return nil
	}
	set := map[string]bool{}
	for _, kv := range managed.Environ(nil) {
		if k, _, ok := strings.Cut(kv, "="); ok {
			set[k] = true
		}
	}
	return func(name string) bool { return set[name] }
}

// processHolds answers, for this composition, whether and whence the process it composes holds
// a name, which the disclosure asks of every withheld one. Both are asked of environ(), the
// environment the exec hands over and the one an eval'ing shell ends up with, so a removal (an
// env_sources null) answers "neither" and the name stays "withheld".
//
// inherited is ES-D4's question: the name holds the invoking shell's own value, intact. The
// shell passes through untouched (CN-D13), so a name the gate withheld from env_sources still
// reaches the process from there, and a disclosure calling it "withheld" would be false.
//
// composed is the same question's other half: the name holds a value that is not the shell's,
// so yolo composed it from another source than env_sources — a pack's `env` of the same name.
// The process holds the name then too, so "withheld from every process" would be as false.
func (c *hostComposition) processHolds() (inherited, composed func(string) bool) {
	env := map[string]string{}
	for _, kv := range c.environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	inherited = func(name string) bool {
		v, ok := os.LookupEnv(name)
		return ok && v != "" && env[name] == v
	}
	composed = func(name string) bool {
		v := env[name]
		if v == "" {
			return false
		}
		shell, ok := os.LookupEnv(name)
		return !ok || shell != v
	}
	return inherited, composed
}

// credentialRemedy is ES-D2's remedy for a group of withheld names. For an AD-HOC command (one no
// selected pack installs) it is the grant, `yolo host --with-credentials <claimant> -- <cmd>`:
// a profile reaches agent CLIs only (OQ-NC5), and the grant needs no declared profile, so there
// is nothing to declare. For an AGENT it is a typed `-p` naming a declared profile that resolves
// to a claiming provider — `yolo host -p <profile> -- <agent>`. With no such profile it says to
// declare one, because `-p` takes a profile name, never a provider's. Either way the profile is
// one the named command can run on (runsOn, ES-D10), so the line never names a launch that
// refuses.
//
// ON A RUN GIVEN --with-credentials the remedy is the grant widened instead (grantRemedy,
// ES-D23). The run already chose the grant, and a named -p would replace the typed one and drop
// it.
//
// The front door decides the spelling: `yolo host --` names the command it was given, and
// `yolo host env`, which launches nothing, names the grant for the shell beside the exec
// spelling for its agent. The shell spelling is `--with-credentials` and never the verb's own
// `--agent` with a -p: an agent's slice on that profile carries its whole provider shape
// (claude's ANTHROPIC_BASE_URL, and the key again under ANTHROPIC_AUTH_TOKEN), which an
// eval'ing shell would then hand, undisclosed, to every process it starts (CN-D13). The grant
// is keys only, and with no -p its script is the ad-hoc slice (hostEnvDefaultAgent). Any one
// claimant delivers every name of the group, so the first is named.
func (c *hostComposition) credentialRemedy(claimants []string) string {
	if c.grant != nil {
		return c.grantRemedy(claimants)
	}
	claimant := "<provider>"
	if len(claimants) > 0 {
		claimant = claimants[0]
	}
	if !selectedPackInstalls(c.packs, c.agent) {
		action := c.grantAction(claimant)
		return strings.ToUpper(action[:1]) + action[1:]
	}
	candidates := remedyProfiles(c.resolved, claimants)
	if len(candidates) == 0 {
		example := "<name>"
		if len(claimants) > 0 {
			example = claimants[0]
		}
		// Asked of the profile the line tells the user to declare, resolved as that
		// declaration would resolve: to the provider alone.
		runs := c.runsOn(example, &packload.ResolvedProfile{Provider: example})
		return fmt.Sprintf("No declared profile selects %s: declare one under `profiles` in %s "+
			"(for example `%q: {\"provider\": %q}`), then %s",
			strings.Join(claimants, " or "), paths.UserConfigPath(), example, example,
			c.remedyAction(example, runs, claimant))
	}
	profile, runs := candidates[0], false
	for _, name := range candidates {
		if c.runsOn(name, nil) {
			profile, runs = name, true
			break
		}
	}
	action := c.remedyAction(profile, runs, claimant)
	return strings.ToUpper(action[:1]) + action[1:]
}

// grantAction is the remedy's instruction for an ad-hoc command, as a clause starting "to …":
// the grant of the claimant's keys, spelled for the command as typed at `yolo host --` and for
// the shell at `yolo host env`.
func (c *hostComposition) grantAction(claimant string) string {
	if c.command == "" {
		return c.shellGrantAction(claimant)
	}
	cmd := shquote.Quote(c.command)
	return fmt.Sprintf("to hand it to %s for one launch: `yolo host %s %s -- %s`", cmd,
		withCredentialsFlag, shquote.Quote(claimant), cmd)
}

// shellGrantAction is the grant's shell spelling, the one `yolo host env` names for every
// withheld group: keys only, into the ad-hoc slice.
func (c *hostComposition) shellGrantAction(claimant string) string {
	return fmt.Sprintf("to receive it in this shell: `eval \"$(yolo host env %s %s)\"`",
		withCredentialsFlag, shquote.Quote(claimant))
}

// grantRemedy is the remedy on a run given --with-credentials (ES-D23): this same run with a
// claimant added to its grant, `yolo host [-p <typed>] --with-credentials <as typed>,<claimant>
// -- <cmd>`. A grant is keys only and runs no derive, so the widened run composes whenever this
// one did, and it keeps what the grant already delivered. That is why it is named over a -p,
// which would replace the typed profile and silently drop the grant. Any one claimant delivers
// every name of the group, so the first is named.
//
// At `yolo host env` it keeps the flags that chose this slice: the typed -p, and --agent only
// when this agent is not the one the verb would default to for that spelling
// (hostEnvDefaultAgent). So the widened script is this one plus the key.
func (c *hostComposition) grantRemedy(claimants []string) string {
	if len(claimants) == 0 {
		return ""
	}
	var flags []string
	if c.command == "" && c.agent != hostEnvDefaultAgent(true, c.typedProfile) {
		flags = append(flags, "--agent", shquote.Quote(c.agent))
	}
	if c.typedProfile != "" {
		flags = append(flags, "-p", shquote.Quote(c.typedProfile))
	}
	flags = append(flags, withCredentialsFlag, shquote.Quote(c.grant.spelled+","+claimants[0]))
	if c.command == "" {
		return fmt.Sprintf("To add it to this shell's grant: `eval \"$(yolo host env %s)\"`",
			strings.Join(flags, " "))
	}
	return fmt.Sprintf("To add it to this launch's grant: `yolo host %s -- %s`",
		strings.Join(flags, " "), shquote.Quote(c.command))
}

// runsOn reports whether `yolo host -p <profile> -- <this command>` would compose rather than
// refuse, asked the way that launch asks: the credential gate over this launch's own inputs
// with the candidate selected for the command, whose AgentEnv holds the protocol pairing gate
// and the pack's env derive. A remedy is a command the user will run, so it may never name one
// that refuses — `yolo host -p cerebras -- claude` does, cerebras speaking only openai and no
// `needs` joining wire-bridge at this notch. It is asked only of an agent: an ad-hoc command's
// remedy is the grant, which names no profile (OQ-NC5), so it answers true for one.
//
// extra, when set, is resolved as profile first: the declaration the line tells the user to
// write. The credential pre-flight is not re-asked, because the name the line is about is the
// credential it would look for, and env_sources holds it.
func (c *hostComposition) runsOn(profile string, extra *packload.ResolvedProfile) bool {
	if !selectedPackInstalls(c.packs, c.agent) {
		return true
	}
	in := c.scopeInput
	in.Profiles = map[string]string{c.agent: profile}
	if extra != nil {
		resolved := make(map[string]packload.ResolvedProfile, len(in.Resolved)+1)
		for k, v := range in.Resolved {
			resolved[k] = v
		}
		resolved[profile] = *extra
		in.Resolved = resolved
	}
	_, err := packload.ScopeCredentials(in)
	return err == nil
}

// remedyAction is an AGENT's remedy instruction for one profile, as a clause starting "to …";
// runs is runsOn's answer for it, and claimant the provider the grant spelling names.
//
// A -p NAMES ONE PROFILE, so the one it names REPLACES the agent's own: a withheld name belongs
// to a provider this launch's profile did not select, and on an agent a selected pack installs,
// the named -p re-points the agent's backend rather than adding a key to it. So the remedy is
// worded as the switch it is ("run claude on the zai profile"), and says which profile it
// replaces when the launch selected one. An agent that cannot run on the profile is named only
// to say so, and the key goes to the grant instead (OQ-NC5): `--with-credentials` for an ad-hoc
// `bash` at the exec, and the shell spelling alone at `yolo host env`.
func (c *hostComposition) remedyAction(profile string, runs bool, claimant string) string {
	p := shquote.Quote(profile)
	cmd := c.command
	if cmd == "" {
		cmd = c.agent
	}
	cmd = shquote.Quote(cmd)
	replacing := ""
	if c.profile != "" {
		replacing = fmt.Sprintf(", replacing its %s profile", c.profile)
	}
	shell := c.shellGrantAction(claimant)
	if !runs {
		cannot := fmt.Sprintf("%s cannot run on the %s profile at this notch", cmd, profile)
		if c.command == "" {
			return fmt.Sprintf("%s (%s)", shell, cannot)
		}
		return fmt.Sprintf("to hand it to an ad-hoc command for one launch instead, since %s: "+
			"`yolo host %s %s -- bash`", cannot, withCredentialsFlag, shquote.Quote(claimant))
	}
	launch := fmt.Sprintf("to run %s on the %s profile for one launch%s: `yolo host -p %s -- %s`",
		cmd, profile, replacing, p, cmd)
	if c.command == "" {
		return shell + "; " + launch
	}
	return launch
}

// remedyProfiles are the declared profiles the remedy may name for a group claimed by
// claimants, in the order it prefers them: each one named after a claiming provider that
// resolves to that provider (every shipped provider ships one), then every other that resolves
// to any claimant, by name. Empty when no declared profile selects any of them — a provider
// the user declared under `providers` with no `profiles` entry beside it.
func remedyProfiles(resolved map[string]packload.ResolvedProfile, claimants []string) []string {
	var own []string
	for _, p := range claimants {
		if packload.ProviderFor(resolved, p) == p {
			own = append(own, p)
		}
	}
	var others []string
	for name, r := range resolved {
		if slices.Contains(own, name) {
			continue
		}
		if slices.Contains(claimants, r.Provider) {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	return append(own, others...)
}

// environ applies the composition over the environment this process inherited — the env
// the exec hands the agent, and therefore the thing "is the key set in this launch" is
// asked of.
func (c *hostComposition) environ() []string {
	return agentenv.Apply(os.Environ(), c.vars)
}

// credentialGaps is the providers.md#the-credential-preflight pre-flight for this launch, answered against environ().
// getenv is the process lookup, passed rather than closed over so a test can stand in for
// the shell this process inherited.
func (c *hostComposition) credentialGaps(getenv func(string) string) []string {
	idx := make(map[string]string, len(c.vars))
	for _, kv := range c.environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			idx[kv[:i]] = kv[i+1:]
		}
	}
	consulted := append([]string(nil), c.consulted...)
	return packload.ProviderCredentialGapsIn(c.packs, c.providers, c.scope, func(name string) (string, bool) {
		if v := idx[name]; v != "" {
			return v, true
		}
		if v := getenv(name); v != "" {
			return v, true
		}
		return "", false
	}, consulted)
}

// platformSwitchConflicts is PP-D1 for this launch's one agent, read from the real home, the
// file the agent `yolo host` execs reads itself (packload.PlatformSwitchConflicts), with the
// host's computed-leaf record saying which switch `yolo host apply` wrote there.
func (c *hostComposition) platformSwitchConflicts() []packload.PlatformSwitchConflict {
	if c.scope == nil || c.agent == "" {
		return nil
	}
	home := paths.Home()
	return packload.PlatformSwitchConflicts(c.packs, c.scope.Selection(), c.resolved, c.providers,
		home, c.agent, render.HostLeafWrote(home))
}

// regionGaps is the region pre-flight (packload.ProviderRegionGaps, OQ-BR6) for this launch,
// answered against environ() — the environment the exec hands the agent. Unlike a jail's, that
// environment INCLUDES the invoking shell, so a region exported there counts here and nothing is
// ever stranded; and an env_sources null removing AWS_REGION removes it here too, which is why
// this reads environ() alone rather than falling back to the process lookup.
func (c *hostComposition) regionGaps() []string {
	if c.scope == nil {
		return nil
	}
	idx := map[string]string{}
	for _, kv := range c.environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			idx[kv[:i]] = kv[i+1:]
		}
	}
	// ONE AGENT: this notch composes one process, so the one ask is its agent's, on the provider
	// its profile resolves to.
	d := c.scope.Agent(c.agent)
	if d == nil || d.Provider == "" {
		return nil
	}
	// One ask per provider of the agent's active set (AP-P1): a regional provider anywhere in
	// the set needs its region, and carries what the region fill read for it.
	var asks []packload.RegionAsk
	for _, provider := range packload.SetProvidersOf(d) {
		asks = append(asks, packload.RegionAsk{Agent: c.agent, Provider: provider,
			File: d.RegionFileFor(provider),
			Lookup: func(name string) (string, bool) {
				v, ok := idx[name]
				return v, ok && v != ""
			}})
	}
	return packload.ProviderRegionGaps(c.packs, c.providers, asks, nil,
		packload.RegionConsulted(c.envSources, packload.FromPackEnv, packload.FromProfileEnv,
			packload.FromLaunchEnv))
}

// hostSkewRepoRoot is the source tree the supersession refusal's skew clause compares this
// yolo against (RM-D2): the one reporoot.Resolve answers, as `yolo check`'s default resolver
// does, or "" when none resolves, which version.SourceSkew answers with no skew. Asked only
// once a claim has failed to match.
func hostSkewRepoRoot() string {
	res, ok := reporoot.Resolve(os.Getenv)
	if !ok {
		return ""
	}
	return res.Root
}

// composeHostEnv builds the environment for one agent launch, and returns it alongside
// the agent name it resolved.
func composeHostEnv(bin, profile string, warn func(string)) ([]string, string, error) {
	c := composeHostLaunch(bin, profile, nil, warn)
	return c.environ(), c.agent, c.err
}

// composeHostLaunch composes the whole launch `yolo host -- <bin>` would exec: the
// environment, the agent name it resolved, and the facts the credential pre-flight reads
// beside them.
//
// The order is the one docs/reference/host-agent-environment.md §6.1 step 3 specifies, and
// each step is there for a reason the previous one cannot cover:
//
//  1. os.Environ() — the user's own shell, which the agent should otherwise inherit whole.
//  2. env_sources — the SECRET channel. This is the step that gives "env_sources
//     hydrates your credentials" something to hydrate INTO on a host.
//  3. the resolved profile's vars — the profile-gated env entries its pack declares,
//     plus the provider environment the agent pack's derive composes (packload.AgentEnv,
//     the same runner the jail's podman argv is built from), then YOLO_PROVIDERS,
//     YOLO_PROFILES and YOLO_USE_PROFILES as this launch composed them (FT-D2).
//  4. removals — a null in env_sources, i.e. `unset AWS_PROFILE`. Last, so a removal
//     beats an assignment from any earlier step.
func composeHostLaunch(bin, profile string, grant *hostGrantRequest, warn func(string)) *hostComposition {
	return composeHostLaunchWith(bin, profile, grant, warn, hostServicesRefuse)
}

// composeHostLaunchWith is composeHostLaunch with the launch's answer to a profile that needs a
// pack service (hostServicesMode): `yolo host --` starts it, every other caller refuses.
func composeHostLaunchWith(bin, profile string, grant *hostGrantRequest, warn func(string),
	services hostServicesMode) *hostComposition {
	agent := filepath.Base(bin)
	cfg := config.UserScopeConfigOrEmpty()
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}

	return composeHostVarsWith(cfg, workspace, agent, bin, profile, grant, warn, services)
}

// hostEnvVars is the composition itself, without the inherited environment — the
// vars-only projection `yolo host env` reads, so the observe verb and the exec half
// cannot disagree about what a launch would carry.
//
// The sources are docs/reference/host-agent-environment.md §5.4's, in order:
//
//  1. the pack env fold, per pack — each pack's static `kind: "env"` contributions, then
//     the ones the same pack gated on the launch's active profile, so a gated entry wins
//     over its own pack's static (OQ-8). packload.EnvVarsFor's sequence, the same one the
//     jail's env block reduces, so a cross-pack key has one winner;
//  2. env_sources — the SECRET channel, and the step that gives "env_sources hydrates
//     your credentials" something to hydrate INTO on a host;
//  3. the resolved profile's provider vars — the env derive of the agent's own pack, run
//     by packload.AgentEnv, the same runner the jail's podman argv is built from, then the
//     three wire tables the launch composed for this agent (FT-D2), under a jail's names.
//
// Removals come last so an `unset` beats an assignment from any earlier source, including
// one inherited from the invoking shell.
// The config it reads is USER SCOPE ONLY (config.UserScopeConfig) — never the merged
// config. This process runs on the host, outside every sandbox, and a workspace
// yolo-jail.jsonc is agent-editable; composing a host process's environment from it would
// hand a cloned repo LD_PRELOAD on the user's machine. See UserScopeConfig for the whole
// argument.
func hostEnvVars(cfg *jsonx.OrderedMap, workspace, agent, profile string, warn func(string)) ([]agentenv.Var, error) {
	c := composeHostVars(cfg, workspace, agent, profile, warn)
	return c.vars, c.err
}

// composeHostVars is hostEnvVars' body, returning the whole composition rather than just
// the vars: the credential pre-flight reads the same packs, the same composed provider
// table and the same env_sources walk the vars were composed from, and re-reading them
// for the check would let the check and the exec disagree about what the launch carries.
func composeHostVars(cfg *jsonx.OrderedMap, workspace, agent, profile string, warn func(string)) *hostComposition {
	return composeHostVarsGranting(cfg, workspace, agent, profile, nil, warn)
}

// composeHostVarsGranting is composeHostVars with a --with-credentials request, nil for none:
// the body both front doors reach, `yolo host --` through composeHostLaunch and `yolo host env`
// through hostEnvDelta. The grant is resolved over the same composed table and the same
// hydrated env_sources the gate reads, and handed to the gate itself (ScopeInput.Grants), so
// the disclosure's recipients, the pre-flight and the vars cannot disagree about it.
func composeHostVarsGranting(cfg *jsonx.OrderedMap, workspace, agent, profile string,
	grant *hostGrantRequest, warn func(string)) *hostComposition {
	return composeHostVarsFor(cfg, workspace, agent, "", profile, grant, warn)
}

// composeHostVarsFor is composeHostVarsGranting for a launch of command, the command as typed
// after `--` ("" for `yolo host env`, which launches nothing): known from the start, so a
// refusal the composition makes can spell the launch it refuses (adHocGrantSpelling).
func composeHostVarsFor(cfg *jsonx.OrderedMap, workspace, agent, command, profile string,
	grant *hostGrantRequest, warn func(string)) *hostComposition {
	return composeHostVarsWith(cfg, workspace, agent, command, profile, grant, warn, hostServicesRefuse)
}

// hostServicesMode is what a host composition does when the one agent it composes for is paired
// through a pack service's adaptation (docs/design/host-notch-services.md §4.2, the trigger).
type hostServicesMode int

const (
	// hostServicesRefuse refuses, naming the launch that would start the service: the answer of
	// `yolo host env` and every other caller that owns no process lifetime (OQ-HS3, HS-D5).
	hostServicesRefuse hostServicesMode = iota
	// hostServicesStart plans the service for this launch (launchservice.NewPlan: its ports and
	// caller token) and composes the agent against it. `yolo host --` alone asks for it.
	hostServicesStart
	// hostServicesDetect records which service the pairing needs and composes nothing further:
	// `yolo host apply`'s question, which renders no address for it and says so.
	hostServicesDetect
)

func composeHostVarsWith(cfg *jsonx.OrderedMap, workspace, agent, command, profile string,
	grant *hostGrantRequest, warn func(string), services hostServicesMode) *hostComposition {
	var vars []agentenv.Var
	c := &hostComposition{agent: agent, command: command, cfg: cfg, workspace: workspace,
		served: packload.NothingServed().AtHost()}

	// The selected packs, read once for both the env they declare and the provider they
	// ship, through the one selection function every notch calls (notch-convergence item 6):
	// the configured packs and every pack their `needs`, or this agent's profile's `via`,
	// joins. The config here is USER SCOPE ONLY (the boundary this function's doc records),
	// so the composed provider table below is user entries over pack facts and never a
	// workspace's.
	//
	// A SELECTION THAT IS NOT THE ONE THE CONFIG ASKS FOR REFUSES (NC-D5): a malformed
	// `packs` entry, a pack that does not resolve, or a closure the selection refuses. This
	// launch used to compose without the pack and warn, so a typo refused every jail launch
	// while `yolo host` ran the agent without the pack's env and providers.
	sel := loadedHostPacks(cfg, agent, profile)
	if err := sel.launchRefusal(); err != nil {
		c.err = err
		return c
	}
	packs := sel.packs
	// A `supersedes` claim that matches no capability any loophole of this selection serves
	// REFUSES, through the gate a jail launch refuses on and in its words
	// (run.UnmatchedSupersessionRefusal; docs/design/reference-mismatch-diagnostics.md §7
	// step 4, one path per concern by NC-D1). At this notch a claim retires a loophole's
	// doorway, so a mistyped one leaves the doorway open as it leaves a jail's loophole
	// running. Here, as the first question asked of the complete selection and before
	// run.PlanHostDoorways, whose discovery warns the same sentence to stderr, so the finding
	// prints once, as the refusal. In the composition, so `yolo host env` refuses it too, as
	// it refuses the selection above.
	if lines := run.UnmatchedSupersessionRefusal(packs, hostSkewRepoRoot); lines != nil {
		c.err = errors.New(strings.Join(lines, "\n"))
		return c
	}
	c.packs = packs
	c.selection = sel
	// The profile this launch selects, resolved once over the selected packs (the closure
	// above read the same fold): it gates (1) and feeds (3), and both must read the same
	// selection or the env a host launch carries and the one its launch line describes would
	// disagree. Over the packs because the config key's "*" (or its string form) reaches the
	// agent only when a selected pack installs it, as a bare -p reaches a jail's CLIs.
	//
	// The agent's ACTIVE SET (docs/design/active-provider-sets.md §4.9): a typed -p list, or its
	// `profile` value, a name or a list. profileName is its primary, the one every fold written
	// before sets reads. A BARE list in the key (its string or list form, or "*") reaches an agent
	// whose pack declares no provider_sets as its first entry alone (OQ-AP3), said on the launch.
	fold := hostProfileFold(cfg, packs, agent, profile)
	set := packload.ProfileSets(fold.Table)[agent]
	profileName := ""
	if len(set) > 0 {
		profileName = set[0]
	}
	if slices.Contains(fold.Narrowed, agent) {
		c.bareList = fold.BareList
		c.bareNote = packload.BareListNote(fold.BareList, nil, []string{agent}, true)
	}
	// Scoped to the ONE agent this process is. A jail carries the whole CLI-keyed table
	// because one container holds every agent; a host launch composes a single process, so
	// only the profile selected at THIS agent's own CLI name may contribute env to it.
	agentTable := map[string]string{}
	var setTable map[string][]string
	if profileName != "" {
		agentTable[agent] = profileName
		setTable = map[string][]string{agent: set}
	}
	c.profile = profileName
	c.set = set
	c.typedProfile = profile
	c.configuredProfile = profileName
	if profile != "" {
		// The fold with no -p: what the config alone selects for this agent (MM-D25).
		if cfgSet := packload.ProfileSets(hostProfileFold(cfg, packs, agent, "").Table)[agent]; len(cfgSet) > 0 {
			c.configuredProfile = cfgSet[0]
		} else {
			c.configuredProfile = ""
		}
	}
	// NO PROFILE KEYS A COMMAND NO PACK INSTALLS (docs/design/credential-sources-separation.md
	// ES-D5, and OQ-NC5 for the typed -p below). A `profile` entry for `bash` delivered
	// here only because this notch ran no validation, while `yolo check` and every jail launch
	// refuse that entry in the same user file. So a profile key doing the selecting is asked
	// the validator's own question, and refused with its message plus the spelling that IS
	// legal: the grant. The provider and profile section of validation below refuses the same
	// entry whatever selects this launch's profile; this refusal runs first for the remedy it
	// adds.
	if profile == "" && profileName != "" && !selectedPackInstalls(packs, agent) {
		if msg, unknown := config.UnknownProfileKey(agent); unknown {
			// Located in the user scope, as hostProviderSectionRefusal's problems are.
			msg = config.UserScopeSources().AnnotateOne(msg)
			c.err = fmt.Errorf("%s. A profile reaches agent CLIs only, so no profile entry "+
				"or -p selects one for a command no pack installs: remove the entry. %s",
				msg, c.adHocGrantSpelling(hostProfileProvider(packs, profileName), grant))
			return c
		}
	}
	// THE PROVIDER AND PROFILE SECTION OF VALIDATION (notch-convergence item 13, row A8), over
	// the user scope this launch composes from: the same checks, in the same words, that every
	// jail launch and `yolo check` run over this file. This notch ran none of them, so a
	// provider written with removed keys composed into a launch that sent claude to its own
	// first-party endpoint with a model named `m1`. After ES-D5's refusal above, which says
	// the same thing about the launched command's own key and adds the spelling that is legal.
	if err := hostProviderSectionRefusal(cfg, warn); err != nil {
		c.err = err
		return c
	}

	// The user's profile declarations, resolved ONCE for this launch — the host notch's
	// half of the resolution the jail notch composes in its channel, read from the same
	// user scope (this cfg IS user scope, but LoadProfiles is the `profiles` key's one
	// reader, and its direct read of the user file is what keeps a workspace spelling
	// inexpressible for this key too). Both the OQ-CS6 refusal and the AgentEnv call
	// below consume the one result, so a host launch cannot describe a profile its env
	// did not compose.
	//
	// DECLARATION IS MANDATORY (OQ-CS6), so a selected name nothing declares refuses
	// here exactly as the jail notch's channel refuses: a host launch that silently ran
	// without the profile its operator named would be the same undetectable no-op the
	// reversal was ruled to end. The declared set is the selected packs' kind:profile names
	// plus the user's own entries. A pack that could not be resolved never reaches here: the
	// selection above refused it (NC-D5), so an undeclared profile is undeclared by the whole
	// pack set the config asks for.
	userProfiles, err := config.LoadProfiles(warn)
	if err != nil {
		c.err = err
		return c
	}
	// Composed HERE rather than at (3) below, because the profile resolution reads the
	// declared options off it (packload.providerOptions) and must measure the surface
	// this launch carries. The same object is reused at (3), so the pre-flight and the
	// env derive read the table the resolution was measured against.
	providers, unservedAdaptations, err := composedHostProviders(cfg, packs, nil)
	if err != nil {
		c.err = err
		return c
	}
	c.providers = providers
	resolvedProfiles, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		c.err = err
		return c
	}
	// VIA IS INERT AT THE HOST NOTCH (WG-I8, WG-I12): no jail daemon runs here, so no via
	// route is served whatever the pack set holds. ResolveProfiles gives a via profile a
	// via_address whenever its service pack is selected, and a user who lists wire-bridge in
	// `packs` explicitly selects it at this notch too, so the env derive below would be handed
	// a ctx.via_url nothing serves. packload.ViaServedAt clears the address of every via this
	// notch does not serve, and ViaURLFor, the predicate both notches' derive paths ask,
	// answers "" for those agents; the cleared profiles are named (credentialScopeLines).
	resolvedProfiles, c.unservedVias = packload.ViaServedAt(resolvedProfiles, packs, packload.NothingServed())
	c.resolved = resolvedProfiles
	if profileName != "" {
		// EVERY ENTRY of the set must be declared (AP-D3): one undeclared entry refuses the launch,
		// and the declared rest never runs alone.
		declared := packload.DeclaredProfileNames(packs, userProfiles)
		for _, name := range set {
			if i := sort.SearchStrings(declared, name); i >= len(declared) || declared[i] != name {
				c.err = fmt.Errorf("packs: profile %q selected for %s: %s", name, agent,
					packload.UndeclaredProfileMessage(name, declared))
				return c
			}
		}
		// And every entry of the key's BARE list this agent took the first of (OQ-AP3 read with
		// AP-D3): the ignored entries are in no set above, and a typo there would pass here while
		// every launch whose agent holds the list refuses it.
		for i, name := range c.bareList {
			if j := sort.SearchStrings(declared, name); j >= len(declared) || declared[j] != name {
				c.err = fmt.Errorf("packs: profile %q (entry %d of the profile key's list %s): %s",
					name, i+1, strings.Join(c.bareList, ","), packload.UndeclaredProfileMessage(name, declared))
				return c
			}
		}
		// The set's own rules (AP-D3, OQ-AP2, AP-D9), in the words every notch uses.
		if problems := packload.ProfileSetProblems(packs, providers, setTable, resolvedProfiles); len(problems) > 0 {
			c.err = fmt.Errorf("packs: %s", strings.Join(problems, "\npacks: "))
			return c
		}
	}
	// A BARE -p REACHES AGENT CLIs ONLY, AT EVERY NOTCH (docs/plans/notch-convergence.md OQ-NC5,
	// ruled 2026-09-28; NC-D62). In a jail a bare `-p <name>` keys every CLI a selected pack
	// installs and never the `--` command (effectiveUseProfiles). Here it used to key whatever
	// basename was launched, so `yolo host -p zai -- curl` handed curl zai's key (ES-D1, retired
	// by that ruling). An arbitrary command gets a provider's key through the one grant ruled for
	// exactly that, --with-credentials, so a typed -p for a command no selected pack installs is
	// refused, naming the grant spelled for this launch. Refused rather than ignored: at this
	// notch the command is the whole launch, so a -p that reaches nothing would be a flag that
	// silently does nothing. After the declaration check, so an undeclared name keeps its own
	// message, and the provider the refusal names is the profile's resolved one.
	if profile != "" && !selectedPackInstalls(packs, agent) {
		c.err = fmt.Errorf("-p %s selects a profile for agent CLIs only, at every notch, and no "+
			"selected pack installs %q, so it would reach nothing here. An arbitrary command "+
			"receives a provider's key only through the grant: %s", shquote.Quote(profile), agent,
			c.adHocGrantSpelling(packload.ProviderFor(resolvedProfiles, profileName), grant))
		return c
	}

	// The secret channel, hydrated BEFORE the fold because the credential gate below reads
	// it. The loader anchors relative entries beside the file that declared them
	// (config.AnchorEnvSources), so a user-config relative entry arrives here absolute and
	// legal. What is still refused is an UNANCHORED relative entry — one from a hand-built
	// config or a pre-ruling artifact — because the only resolution left for it is the
	// CURRENT DIRECTORY, which a workspace controls: cd into a cloned repo,
	// `yolo host -- claude`, and the repo's .env feeds a host process. That would re-open,
	// through the filesystem, the exact boundary the user-scope-only cfg closes;
	// hostScopedEnvSources is the backstop.
	scoped := hostScopedEnvSources(cfg, warn)
	// ONE pass for (2) and (4): the assignments and the removals are the same ordered
	// walk, and asking for them separately would read every dotenv file twice and warn
	// twice — noise a missing host-only file used to produce on every `yolo host env`.
	userEnv, removals := config.ResolveEnvSourcesFull(workspace, scoped, warn)
	// What this launch consulted for credentials, recorded as it is consulted: the
	// env_sources entries that survived the scope filter, plus the shell this process
	// inherited. The providers.md#the-credential-preflight pre-flight quotes the list verbatim, so a refusal says where it
	// looked and not only that the key never arrived. The inherited environment is named in
	// the jail's words (packload.FromLaunchEnv), so one refusal reads the same at both notches
	// (notch-convergence.md item 14).
	c.envSources = config.DescribeEnvSources(workspace, scoped)
	c.consulted = append(append([]string(nil), c.envSources...), packload.FromLaunchEnv)

	// THE GRANT (docs/design/credential-sources-separation.md OQ-ES5, ruled for the host
	// 2026-09-27): the named providers' claimed env_sources values, for this one process, keys
	// only. Resolved here, after the table and env_sources it reads and before the gate, which
	// delivers it — a grant is one more recipient of a claimed value, so the gate's own claim
	// model decides which names it carries. Nothing but the typed flag reaches this: no config
	// key, no profile entry, no -p and no environment variable builds a request.
	var grants map[string][]string
	if grant != nil {
		g, err := resolveHostGrant(grant, providers, userEnv)
		if err != nil {
			c.err = err
			return c
		}
		c.grant = g
		if len(g.providers) > 0 {
			grants = map[string][]string{agent: g.providers}
		}
	}

	// THE CREDENTIAL GATE (docs/design/provider-credential-scope.md; OQ-CN5 ruled that the
	// host notch ships with the jail's, since it composes for a process outside every
	// sandbox). The same packload.ScopeCredentials the jail notch's composePackChannel
	// calls, over this notch's inputs: a provider's claimed credential reaches this agent
	// only when its profile selects that provider, a gated env only when its own selection
	// satisfies it, and its env derive hydrates only its own provider's key. The shell this
	// process inherited is the user's, not a yolo channel, so it passes through untouched
	// (environ); what the gate governs is what yolo ADDS to it. It also runs the agent's
	// own pack's env derive — packload.AgentEnv, the ONE runner the jail notch reduces
	// through too (OQ-CS8), so the two notches cannot disagree about what a resolved
	// profile delivers. A GIT PACK CONTRIBUTES HERE TOO: loadedHostPacks resolves through
	// resolveConfiguredPack, which reads a git pack from the pack store the way a launch
	// does. One the store does not have refuses the launch above (NC-D5).
	//
	// THE CREDENTIAL DOORWAYS (docs/design/host-notch-services.md HS-D15, the doorway rule;
	// HS-D21): a loophole's doorway that this agent's selection asks for, aws-auth's for an
	// agent on a Bedrock provider, is served at this notch by `yolo host --`, which opens it as
	// a launch-owned listener on a port it picked, behind a caller token it minted
	// (run.PlanHostDoorways). So the gate composes the pointer at that address and scopes the
	// token to this agent, as a jail's gate does for its jail daemon. A front door that runs no
	// process plans none, and its reason rides the served set into the "Not set" line.
	// Over the agent's whole ACTIVE SET (docs/design/active-provider-sets.md AP-P1): a Bedrock
	// entry anywhere in pi's set asks for aws-auth's doorway as a Bedrock primary does.
	doorways, err := run.PlanHostDoorways(cfg, packs,
		packload.SelectionOfSets(setTable, resolvedProfiles, providers),
		services == hostServicesStart, c.doorwayLaunchSpelling())
	if err != nil {
		c.err = err
		return c
	}
	c.doorways = doorways
	hostServed := doorways.Served()
	c.scopeInput = packload.ScopeInput{
		Packs:     packs,
		Providers: providers,
		Profiles:  agentTable,
		// The agent's whole active set (§4.5): each entry's claimed key reaches it.
		Sets:       setTable,
		Resolved:   resolvedProfiles,
		EnvSources: userEnv,
		Fallback:   os.LookupEnv,
		Grants:     grants,
		// What composedHostProviders left out, and the unselected shipped packs' adaptations
		// of the same kind, so a pairing only one of them would resolve refuses once, naming
		// why (ES-D18, ES-D19). It never refuses as a pairing nothing declares an adapter for,
		// and never as outcome 3 telling the user to list a pack that resolves nothing here.
		UnservedAdaptations: unservedAdaptations,
		// Nothing but this launch's own doorways is served at this notch, so a pack env variable
		// pointing at any other jail daemon (`served_by`) is withheld and named rather than
		// exported as a dead address, and on the host's own loopback a credential for whoever
		// binds the port (notch convergence item 2). The jail's vehicles apply the same rule
		// through the same gate.
		Served:       &hostServed,
		CallerTokens: doorways.CallerTokens(),
		// THE REGION FILE, the jail's rule (docs/design/bedrock-plumbing.md BR-DIR1): this
		// agent could read ~/.aws/config itself, and is given its credential profile's region
		// anyway, so the refusal and the disclosure are one at every notch. The invoking shell is
		// Inherited here, since the exec'd agent receives it: a region or profile exported there
		// counts, as it does for the agent — except a name an env_sources null removes, which
		// (4) below takes out of the exec'd environment, so the agent never sees it.
		RegionFiles: &packload.RegionFileSource{Getenv: os.Getenv, Inherited: inheritedExcept(removals),
			Setting: packload.LoopholeSettingIn(cfg)},
	}
	scope, err := packload.ScopeCredentials(c.scopeInput)
	// A PAIRING THROUGH A PACK SERVICE'S ADAPTATION (docs/design/host-notch-services.md §4.2). The
	// composition above served nothing, so the gate refused it; a launch that owns its process
	// runs the service's host half as a launch-owned child and composes the agent against it
	// (NC-D65: OQ-NC1's option A at the host). One agent resolves one pairing, so this adds at
	// most one service; the loop is bounded by the services the selection declares.
	for tries := 0; err != nil && tries <= len(packs); tries++ {
		var unserved *packload.UnservedAdapterError
		if !errors.As(err, &unserved) || unserved.Agent != agent {
			break
		}
		plan, refusal, detected := c.planHostService(unserved, packs, profileName, sel, services)
		if detected || refusal != nil {
			c.err = refusal
			return c
		}
		c.services = append(c.services, plan)
		providers, unservedAdaptations, err = composedHostProviders(cfg, packs, c.services)
		if err != nil {
			c.err = err
			return c
		}
		c.providers = providers
		if resolvedProfiles, err = packload.ResolveProfiles(packs, userProfiles, providers); err != nil {
			c.err = err
			return c
		}
		// Via stays inert at the host whatever this launch serves (WG-I12).
		resolvedProfiles, c.unservedVias = packload.ViaServedAt(resolvedProfiles, packs, packload.NothingServed())
		c.resolved = resolvedProfiles
		// The services beside the doorways: the host notch serves both kinds of launch-owned
		// listener, and each has its own caller token.
		hostServed = launchservice.Served(c.services).Plus(doorways.Served())
		c.scopeInput.Providers = providers
		c.scopeInput.Resolved = resolvedProfiles
		c.scopeInput.UnservedAdaptations = unservedAdaptations
		c.scopeInput.Served = &hostServed
		tokens := map[string]string{}
		for k, v := range launchservice.CallerTokens(c.services) {
			tokens[k] = v
		}
		for k, v := range doorways.CallerTokens() {
			tokens[k] = v
		}
		c.scopeInput.CallerTokens = tokens
		scope, err = packload.ScopeCredentials(c.scopeInput)
	}
	if err != nil {
		var unserved *packload.UnservedAdapterError
		if errors.As(err, &unserved) && unserved.Agent == agent {
			err = unservedAdapterRefusal(unserved, profileName, sel, "")
		}
		c.err = err
		return c
	}
	c.scope = scope
	c.served = hostServed
	delivery := scope.Agent(agent)

	// (1) the pack env fold, PER PACK — each pack's static `kind: "env"` keys, then the
	// keys of its `profile`-gated env contributions whose gate fires for THIS agent
	// (providers.md#pv-oq-8). The sequence is packload.EnvFold's, the ONE fold the jail
	// notch reduces through packload.EnvVarsFor: folding it here as
	// all-static-then-all-gated instead gave a key that pack A's gated env and pack B's
	// static both write two answers (the jail said the later pack's static wins, the host
	// the earlier pack's gated value). hostFoldParity_test.go pins the two notches to the
	// same winner.
	//
	// Keys are sorted within each pack, because a map has no order and an `export` script
	// that reshuffles between runs is a diff nobody can read.
	//
	// Assignments only, and that is the OQ-PT8 shrink rather than a shortcut: the only
	// env map here that could spell a removal was the profile body's, whose
	// null-means-unset decoder died with the body. What a removal still has is (2)'s
	// env_sources nulls, held for (4) below.
	//
	// The gate's fold (FoldFor), for an agent with or without a delivery: it withholds what
	// this notch does not serve, which a fold of our own would not.
	for _, e := range scope.FoldFor(agent) {
		vars = append(vars, agentenv.Var{Key: e.Key, Value: e.Value})
		c.origins = append(c.origins, packload.FromPackEnv)
		c.originPacks = append(c.originPacks, e.Pack)
	}

	// (2) the secret channel, as the gate delivers it to this agent: every unclaimed entry
	// and its own provider's claimed ones, in hydration order.
	sources := scope.EnvSourcesFor(agent)
	for _, k := range sources.Keys() {
		v, _ := sources.Get(k)
		if s, ok := v.(string); ok {
			vars = append(vars, agentenv.Var{Key: k, Value: s})
			c.origins = append(c.origins, packload.FromEnvSources)
			c.originPacks = append(c.originPacks, "")
		}
	}

	// (3) the profile's provider vars, the env derive's output the gate composed. Each is its
	// agent's own pack's, the pack whose derive produced it, which is what a reader keyed on the
	// declaring pack needs: pi's OpenAI prelaunch comes from its derive since OQ-BR8, and the
	// prelaunch keys its managed home on the pack that declared it (prelaunch).
	if delivery != nil {
		vars = append(vars, delivery.Shape...)
		owner := installingPack(packs, agent)
		for range delivery.Shape {
			c.origins = append(c.origins, packload.FromProfileEnv)
			c.originPacks = append(c.originPacks, owner)
		}
	}

	// (3b) THE WIRE TABLES the launch composed for this agent (docs/design/agent-footer.md
	// FT-D2, OQ-FT15), under the names a jail's channel uses: YOLO_PROVIDERS, YOLO_PROFILES and a
	// YOLO_USE_PROFILES holding this agent's entry alone. The footer renderer reads its own env
	// first (OQ-FT6), so a one-launch `yolo host -p zai -- claude` names zai rather than the
	// config's selection. ALL THREE ON EVERY LAUNCH, empty tables included, so a launch started
	// inside another agent's launch replaces what it inherited instead of keeping that launch's
	// selection. After the pack fold, env_sources and the shape, so none of those can write a
	// table this launch did not compose (FT-D3); before the removals, whose "an unset beats every
	// assignment" holds for these too. Ranged over entrypoint.WireTables, as every jail writer
	// ranges (NC-D22), and attributed to the launch's own composition, since no pack declares them.
	wire := c.wireTables()
	for _, k := range entrypoint.WireTables() {
		vars = append(vars, agentenv.Var{Key: k, Value: wire[k]})
		c.origins = append(c.origins, packload.FromProfileEnv)
		c.originPacks = append(c.originPacks, "")
	}

	// (4) removals last, so an unset beats every assignment above no matter which source
	// made it — the env_sources nulls from the same pass as (2) (the same scoped config,
	// so an inline null's cancellation by a later dotenv cannot disagree with the
	// assignments). The pack fold no longer contributes any: its only removal spelling
	// died with the profile body. Sorted, because a set of removals has no order to
	// preserve and the `export` script must not reshuffle between runs.
	for _, k := range removals {
		vars = append(vars, agentenv.Var{Key: k, Unset: true})
		c.origins = append(c.origins, fromRemoval)
		c.originPacks = append(c.originPacks, "")
	}
	c.vars = vars
	return c
}

// inheritedExcept is the invoking shell as the exec'd agent inherits it: os.LookupEnv, with every
// name in removed answering nothing, because the composition's removals (an env_sources null)
// are applied last and take that name out of the environment the agent receives.
func inheritedExcept(removed []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if slices.Contains(removed, name) {
			return "", false
		}
		return os.LookupEnv(name)
	}
}

// adHocGrantSpelling is the pasteable grant that hands this composition's ad-hoc command the
// named provider's claimed env_sources values, for the refusals that tell a user a profile does
// not (OQ-NC5): `yolo host --with-credentials <provider> -- <cmd>` for the exec, spelled for the
// command as typed, and `eval "$(yolo host env --with-credentials <provider>)"` for the shell,
// whose default slice under a grant is the ad-hoc one (hostEnvDefaultAgent). A grant already
// typed is kept and widened, so the named run delivers everything this one asked for. provider
// is "" when the profile does not resolve, and the spelling then shows the placeholder.
func (c *hostComposition) adHocGrantSpelling(provider string, grant *hostGrantRequest) string {
	if provider == "" {
		provider = "<provider>"
	}
	names := []string{}
	if grant != nil {
		names = append(names, grant.names...)
	}
	if !slices.Contains(names, provider) && !slices.Contains(names, "all") {
		names = append(names, provider)
	}
	value := shquote.Quote(strings.Join(names, ","))
	if c.command == "" {
		return fmt.Sprintf("`eval \"$(yolo host env %s %s)\"` exports that provider's key into "+
			"this shell", withCredentialsFlag, value)
	}
	return fmt.Sprintf("`yolo host %s %s -- %s` hands %s that provider's key for one launch",
		withCredentialsFlag, value, shquote.Quote(c.command), shquote.Quote(c.command))
}

// hostProfileProvider is the provider a declared profile selects, resolved over the selected
// packs' shipped profiles and the user's own, "" when it resolves to none. For ES-D5's refusal,
// which runs before the launch's own resolution and needs only the provider's name.
func hostProfileProvider(packs []*packload.Pack, profile string) string {
	user, err := config.LoadProfiles(nil)
	if err != nil {
		return ""
	}
	resolved, err := packload.ResolveProfiles(packs, user, nil)
	if err != nil {
		return ""
	}
	return packload.ProviderFor(resolved, profile)
}

// selectedPackInstalls reports whether a selected pack installs a CLI named bin — the case in
// which a profile key for it is certainly one the validator accepts, so ES-D5's refusal
// need not enumerate the whole namespace to know it.
func selectedPackInstalls(packs []*packload.Pack, bin string) bool {
	return installingPack(packs, bin) != ""
}

// installingPack is the name of the selected pack that installs bin, "" when none does. A
// program is sole-owned by bin, so there is at most one in a launch that loaded.
func installingPack(packs []*packload.Pack, bin string) string {
	for _, p := range packs {
		for _, b := range p.InstallBins() {
			if b == bin {
				return p.Name
			}
		}
	}
	return ""
}

// composedHostProviders is the host notch's ONE provider composition — the host spelling
// of the jail notch's composedProviders (internal/cli/run/assemble.go): the user's
// `providers` config entries with every selected pack's shipped `kind: "provider"` facts
// composed under them, per field. Composed ONCE and its result handed to BOTH of this
// launch's consumers — the provider env derive in composeHostVars and the providers.md#the-credential-preflight pre-flight's
// c.providers — because packload/providers.go states the composition happens exactly once
// per launch, and two compositions would be two chances for the check and the exec to
// disagree about what the launch carries.
func composedHostProviders(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	services []*launchservice.Plan) (*jsonx.OrderedMap, []packload.Adaptation, error) {
	var user *jsonx.OrderedMap
	if v, ok := cfg.Get("providers"); ok {
		user, _ = v.(*jsonx.OrderedMap)
	}
	// A LAUNCH THAT STARTS A SERVICE composes its adaptations at the ports it picked, and a
	// user's `adapters` override of those conversions does not apply here: the launch-chosen
	// address overrides the manifest's and the user's (OQ-HS4).
	if len(services) > 0 {
		addresses := launchservice.WithoutOverrides(hostAdapterAddresses(), packs,
			launchservice.Names(services))
		return packload.ComposeProvidersAt(user, packs, addresses, launchservice.Served(services))
	}
	// NO ADDRESS A PACK'S OWN SERVICE SERVES (docs/design/credential-sources-separation.md
	// ES-D18): `yolo host` starts no jail daemon, so nothing is served at this notch
	// (packload.NothingServed) and the wire bridge's adapter address — declared by
	// packs/wire-bridge beside the service whose in-jail daemon listens on it — is left out.
	// Composed in, an agent the bridge would front is pointed at a dead address: claude on
	// cerebras at http://127.0.0.1:8214. Left out, an agent that speaks the provider's own
	// wire resolves to it directly, and one that cannot refuses at the gate, which
	// composeHostVars words. The same composition every notch calls (ComposeProvidersAt),
	// with this notch's served set as its input (notch convergence item 2).
	return packload.ComposeProvidersAt(user, packs, hostAdapterAddresses(), packload.NothingServed())
}

// hostAdapterAddresses is the user's adapter address overrides, read the same way the jail
// notch reads them (run.composedProviders): from the user file directly, so the two notches
// cannot disagree about where an adapted provider answers.
func hostAdapterAddresses() map[string]string {
	addresses, _ := config.LoadAdapterAddresses(nil)
	return addresses
}

// planHostService is a host composition's answer to a pairing through a pack service's
// adaptation (e): the service's plan when this launch will start it, or the refusal, or, for
// hostServicesDetect, only the record of which service the pairing needs (detected true).
//
// THE FOUR REFUSALS HS-D5 NARROWS ES-D18 TO. A service with no host half and a service whose
// pack yolo does not ship (launchservice.Admit) refuse with the reason; `yolo host env`, which
// owns no process lifetime, refuses naming the launch that would start it (OQ-HS3); and a host
// half that fails to start is hostExec's (launchservice.Start). A pairing through a pack nothing
// selects, or a provider no selected pack ships, refuses as before.
func (c *hostComposition) planHostService(e *packload.UnservedAdapterError, packs []*packload.Pack,
	profile string, sel hostPackSet, mode hostServicesMode) (*launchservice.Plan, error, bool) {
	if !e.Selected || e.ProviderPack != "" {
		return nil, unservedAdapterRefusal(e, profile, sel, ""), false
	}
	// A service this composition already planned that still leaves the pairing unserved is a
	// contradiction between the gate and the plan, never a reason to plan it twice.
	for _, p := range c.services {
		if p.Service == e.Adaptation.Service {
			return nil, unservedAdapterRefusal(e, profile, sel, "this launch planned it and the "+
				"pairing still does not resolve through it (a yolo bug)"), false
		}
	}
	d, err := launchservice.Admit(packs, e.Adaptation.Service)
	if err != nil {
		var adm *launchservice.AdmissionError
		why := err.Error()
		if errors.As(err, &adm) {
			why = adm.Why
		}
		return nil, unservedAdapterRefusal(e, profile, sel, why), false
	}
	switch mode {
	case hostServicesDetect:
		c.services = append(c.services, &launchservice.Plan{Declared: d})
		return nil, nil, true
	case hostServicesRefuse:
		spelling := "yolo host -- " + shquote.Quote(e.Agent)
		if c.typedProfile != "" {
			spelling = "yolo host -p " + shquote.Quote(c.typedProfile) + " -- " + shquote.Quote(e.Agent)
		}
		return nil, fmt.Errorf("profile %q pairs %s with provider %q through pack %q's %q service, and "+
			"this command runs no process, so there is no command whose lifetime that service could "+
			"follow: a host service lives only for the command that starts it "+
			"(docs/design/host-notch-services.md OQ-HS3). Launch the agent as `%s`, or through its "+
			"host wrapper, which starts the service for that command and stops it when %s exits",
			profile, e.Agent, e.Provider, d.Pack, d.Service, spelling, e.Agent), false
	}
	plan, err := launchservice.NewPlan(packs, d)
	if err != nil {
		return nil, err, false
	}
	return plan, nil, false
}

// unservedAdapterRefusal words the gate's *packload.UnservedAdapterError for this notch when the
// pairing's service cannot run here: the profile, the address the agent would have been pointed
// at and what serves it, why this launch cannot start it (why, from launchservice.Admit; "" for a
// pack nothing selects or a provider no selected pack ships), and the launch where the profile
// works.
//
// THAT LAUNCH IS A CONTAINER JAIL'S (ES-D20): it runs the service's jail daemon, which needs no
// host half and no trust ruling. A macos-user launch runs a pack service only through its host
// half, as the host does, so it refuses the same profile, and the refusal names the dial that
// picks a container backend for one launch. It never says the profile works "in a jail"
// unqualified.
//
// A PACK THE SELECTION CLOSURE JOINED IS WORDED AS JOINED (HS-D1). With `"packs": ["claude"]`
// the wire bridge joins through claude's `needs`, so "though "wire-bridge" is in `packs`" would
// send the user to a config line that does not exist; the refusal quotes the cause line instead.
//
// A PACK NOTHING SELECTS (!e.Selected) is named with the remedy that is true of it: listing it
// makes this launch start its service when yolo ships the pack and the service has a host half,
// and changes nothing otherwise (ES-D19, and HS-D1's converse).
//
// THE PROVIDER'S OWN PACK MAY BE MISSING TOO (ES-D25): only a pack yolo ships declares the
// provider, and nothing selects it; e.ProviderPack names the pack, and the refusal says that
// listing it changes nothing here either.
//
// THE JAIL SPELLING IS SAID TO BE A JAIL LAUNCH (ES-D26): `yolo -p claude=codex -- claude`
// starts a container, not a `yolo host` one.
func unservedAdapterRefusal(e *packload.UnservedAdapterError, profile string, sel hostPackSet, why string) error {
	a := e.Adaptation
	agent, p := shquote.Quote(e.Agent), shquote.Quote(profile)
	listing := fmt.Sprintf("though %q is in `packs`", a.Pack)
	if cause, joined := sel.joined(a.Pack); joined {
		listing = fmt.Sprintf("though %q joined this launch (%s)", a.Pack, cause)
	}
	if !e.Selected {
		listing = fmt.Sprintf("and adding %q to `packs` does not change that here", a.Pack)
		if _, err := launchservice.Admit(packload.Embedded(), a.Service); err == nil {
			listing = fmt.Sprintf("because nothing selects pack %q: add it to `packs` and `yolo host` "+
				"starts its %q service for the command", a.Pack, a.Service)
		}
	}
	cannot := fmt.Sprintf("which this launch cannot start: %s", why)
	if why == "" {
		cannot = "which does not run for this launch"
	}
	providerPack := ""
	if e.ProviderPack != "" {
		needs := ""
		if e.NeededBy != "" {
			needs = fmt.Sprintf(" (pack %q's `needs` names it, under a `when_bins` this launch "+
				"does not meet)", e.NeededBy)
		}
		providerPack = fmt.Sprintf(" Provider %q is not in this launch's provider table either: "+
			"pack %q ships it and nothing selects it%s, so adding %q to `packs` does not change "+
			"the answer either.", e.Provider, e.ProviderPack, needs, e.ProviderPack)
	}
	return fmt.Errorf("profile %q would point %s at %s, where pack %q adapts %q → %q for provider %q — "+
		"and that address is served by the pack's own %q service, %s. No host process serves it, so "+
		"`yolo host` will not run %s pointed at it, %s.%s\n"+
		"  The profile works in a container jail (podman or Apple Container), where that service's "+
		"jail daemon runs: `yolo -p %s=%s -- %s`, which is a jail launch, not a `yolo host` one. A "+
		"macos-user launch runs a pack service only through its host half, as `yolo host` does, so "+
		"it refuses this profile too; `YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container` picks a "+
		"container backend for one launch.\n"+
		"  At the host, choose a profile whose provider %s speaks to directly",
		profile, agent, a.Address, a.Pack, a.From, a.To, e.Provider, a.Service, cannot, agent, listing,
		providerPack, agent, p, agent, agent)
}

// hostScopedEnvSources returns cfg with any still-RELATIVE env_sources file entry
// dropped — one warning per dropped entry, naming the remedy — for the host notch's env
// composition. Under the 2026-08-30 ruling (envsource-relative-paths.md OQ-E1) a
// relative entry in a real config is legal and arrives already ANCHORED beside its
// declaring file (config.AnchorEnvSources runs in the loader), so what reaches this
// filter unanchored is a hand-built config or a pre-ruling artifact — sources whose
// only remaining resolution is the cwd, which a workspace controls. It is a backstop,
// not the rule. Absolute and ~-relative entries pass untouched (they name where they
// name, independent of the cwd); inline dict entries pass (they are not paths at all).
//
// Returns cfg itself when nothing is dropped, and a shallow copy otherwise — the caller
// shares this map with composition steps that must keep seeing the original, and the
// copy is throwaway.
func hostScopedEnvSources(cfg *jsonx.OrderedMap, warn func(string)) *jsonx.OrderedMap {
	entries, present := cfg.Get("env_sources")
	if !present {
		return cfg
	}
	list, ok := entries.([]any)
	if !ok || len(list) == 0 {
		return cfg
	}
	var kept []any
	dropped := false
	for _, e := range list {
		if s, isStr := e.(string); isStr && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "~") {
			dropped = true
			if warn != nil {
				warn("env_sources: \"" + s + "\" is relative and ignored by `yolo host` — it would " +
					"resolve against the current directory, which a workspace controls. " +
					"Use an absolute path or ~/…")
			}
			continue
		}
		kept = append(kept, e)
	}
	if !dropped {
		return cfg
	}
	out := jsonx.NewOrderedMap()
	for _, k := range cfg.Keys() {
		v, _ := cfg.Get(k)
		out.Set(k, v)
	}
	if len(kept) > 0 {
		out.Set("env_sources", kept)
	}
	return out
}

// loadedHostPacks is the selection for one host launch of agent: the one selection function
// (selectHostPacks) with the launch's resolver and, as the closure's table, the one profile this
// launch selects for the one agent it runs (HS-D1). A host launch composes a single process, so
// only that agent's profile can join a `via` service, and the host serves no via route anyway
// (ViaServedAt, WG-I12).
//
// IT APPLIES `needs`, which ES-D24 once measured it must not: with `"packs": ["claude"]`, claude
// needs aws-auth, and aws-auth's Bedrock-gated env points AWS_CONTAINER_CREDENTIALS_FULL_URI at
// http://127.0.0.1:1461/credentials, an address only a jail's side of the aws-auth loophole
// serves. That pointer declares the daemon that serves it (`served_by`), and the credential gate
// withholds it and names it where that daemon does not run (NC-D16), so `yolo host -p bedrock --
// claude` exports no address nothing serves (NC-D4).
//
// cfg is the user-scope config the launch composes from and typed the -p it was given for
// agent (hostProfileFor's answer, "" for none): the closure selects agent's profile from them
// over each set it is handed (hostAgentProfile), so a "*" reaches agent only when that set
// installs it.
func loadedHostPacks(cfg *jsonx.OrderedMap, agent, typed string) hostPackSet {
	return selectHostPacks(resolveConfiguredPack, hostLaunchSelection(cfg, agent, typed))
}

// hostLaunchSelection is the closure's profile input for a host launch: agent's profile alone,
// and the user's profile declarations from user scope.
func hostLaunchSelection(cfg *jsonx.OrderedMap, agent, typed string) packload.Selection {
	return packload.Selection{
		UseProfiles: func(set []*packload.Pack) map[string]string {
			if profile := hostAgentProfile(cfg, set, agent, typed); profile != "" {
				return map[string]string{agent: profile}
			}
			return nil
		},
		UserProfiles: func() (map[string]packload.UserProfile, error) {
			return config.LoadProfiles(nil)
		},
	}
}

// hostAgentProfile is the profile a host launch of agent selects over packs: its entry in
// effectiveHostProfiles, the first of its active set, "" when none.
func hostAgentProfile(cfg *jsonx.OrderedMap, packs []*packload.Pack, agent, typed string) string {
	if set := packload.ProfileSets(effectiveHostProfiles(cfg, packs, agent, typed))[agent]; len(set) > 0 {
		return set[0]
	}
	return ""
}

// effectiveHostProfiles is the host notch's profile table over packs: the config `profile`
// key with a typed -p for agent above it, through the one fold every notch reads
// (config.ProfileTableFor, PP-D10), so a host launch and a jail's agree about what a profile
// selects. The key's "*" (or its string form) reaches every CLI those packs install that the
// key does not name, as in a jail. The typed -p names agent whatever it is, because at this
// notch it is the launch's only command; a -p for a command no selected pack installs is
// refused by the launch that composes it, never dropped here. With agent "" (a verb that
// launches nothing: `yolo host apply`, the footer, the overlay gate) it is the key alone.
//
// A value is the agent's ACTIVE SET (docs/design/active-provider-sets.md): a typed list (the
// comma-joined entries hostProfileFor checked) replaces the agent's whole set (AP-D4), and a
// set of one crosses as the plain string it always did.
func effectiveHostProfiles(cfg *jsonx.OrderedMap, packs []*packload.Pack, agent, typed string) *jsonx.OrderedMap {
	return hostProfileFold(cfg, packs, agent, typed).Table
}

// hostProfileFold is the fold effectiveHostProfiles returns the table of, kept whole for what the
// key's BARE list did in it (OQ-AP3): the config key over packs' receivers, then the typed -p
// for agent as its named entry.
func hostProfileFold(cfg *jsonx.OrderedMap, packs []*packload.Pack, agent, typed string) config.ProfileFold {
	var flag config.ProfileSelection
	if typed != "" && agent != "" {
		flag.Named = map[string][]string{agent: packload.SplitProfileList(typed)}
	}
	return config.FoldProfiles(config.ReceiversOf(packs), config.ConfigProfileSelection(cfg), flag)
}

// overlayGateProfiles is the ACTIVE profile table the config-overlay `profile` modifier
// gates on, for the notch being rendered or described — the shared source for `yolo host
// apply`'s render and `yolo config diff`'s report, so the two cannot describe a different
// selection than the render either of them is reasoning about.
//
// The JAIL half reads YOLO_USE_PROFILES: the effective workspace < user < CLI table the
// launcher emitted, which is the very table the boot render gated on, so an in-jail
// inspection answers with the render that actually happened rather than a re-derivation
// that could disagree with it.
//
// The HOST half reads the USER-SCOPE config's `profile` and nothing else, folded over packs
// (the set the caller collects overlays from, so a "*" reaches the agents that set installs),
// and that is the boundary every host composition draws (UserScopeConfig's whole argument): a
// gated overlay's payload lands in the user's REAL config files, and the one this design ships
// first rewrites ANTHROPIC_BASE_URL — where an agent sends the credentials the user
// already has. Letting a workspace yolo-jail.jsonc (agent-editable, /workspace is
// bind-mounted rw) switch that on would hand a cloned repository the redirection
// host_wrappers refuses it, so workspace scope stays inexpressible here exactly as it is
// there. No `-p` is honored because neither caller takes one — the flag exists on `yolo
// host --` and `yolo --`, which compose per-process and read this same table through their
// own channels.
func overlayGateProfiles(notch render.Kind, packs []*packload.Pack) map[string]string {
	if notch == render.KindJail {
		raw := os.Getenv("YOLO_USE_PROFILES")
		if raw == "" {
			return nil
		}
		decoded, err := jsonx.Decode([]byte(raw))
		if err != nil {
			return nil
		}
		if m, ok := decoded.(*jsonx.OrderedMap); ok {
			return packload.ProfileTable(m)
		}
		return nil
	}
	return packload.ProfileTable(effectiveHostProfiles(config.UserScopeConfigOrEmpty(), packs, "", ""))
}

// hostEnv prints the composed environment instead of exec'ing into it — the third front
// door onto the same composition, for direnv/mise users and for debugging.
func hostEnv(args []string, out, errw io.Writer) int {
	format := "export"
	profile := ""
	agent := ""
	var grant *hostGrantRequest
	addGrant := func(v string) bool {
		if grant == nil {
			grant = &hostGrantRequest{}
		}
		if err := addGrantValue(grant, v); err != nil {
			fmt.Fprintf(errw, "yolo host env: %v\n", err)
			return false
		}
		return true
	}
	for i := 0; i < len(args); i++ {
		// The launch value flags through the one reader both notches' launches use
		// (readValueFlag), so `-p=zai` and `--profile=` read here as they do at `yolo host --`
		// and in a jail.
		if fl, ok := readValueFlag(args, i, "--profile", "-p"); ok {
			i = fl.last
			if fl.err != nil {
				fmt.Fprintf(errw, "yolo host env: %v\n", fl.err)
				return 2
			}
			profile = fl.value
			continue
		}
		if fl, ok := readValueFlag(args, i, withCredentialsFlag); ok {
			i = fl.last
			if fl.err != nil {
				fmt.Fprintf(errw, "yolo host env: %v\n", fl.err)
				return 2
			}
			if !addGrant(fl.value) {
				return 2
			}
			continue
		}
		switch a := args[i]; {
		case isHelpToken(a):
			fmt.Fprintln(out, hostUsage)
			return 0
		case a == "--format":
			if i+1 >= len(args) {
				fmt.Fprintln(errw, "yolo host env: --format needs a value (export|json)")
				return 2
			}
			i++
			format = args[i]
		case strings.HasPrefix(a, "--format="):
			format = a[len("--format="):]
		case a == "--agent":
			if i+1 >= len(args) {
				fmt.Fprintln(errw, "yolo host env: --agent needs a value")
				return 2
			}
			i++
			agent = args[i]
		case strings.HasPrefix(a, "--agent="):
			agent = a[len("--agent="):]
		case len(a) > 1 && a[0] == '-':
			// refuseUnknownFlags' words, the jail's, for a mistyped flag at every notch.
			refuseUnknownFlags("host env", []string{a}, nil, errw)
			return 2
		default:
			fmt.Fprintf(errw, "yolo host env: unexpected argument %q\n", a)
			return 2
		}
	}
	if format != "export" && format != "json" {
		fmt.Fprintf(errw, "yolo host env: unknown --format %q (want export or json)\n", format)
		return 2
	}
	if agent == "" {
		// A default rather than "every configured agent": the composition is per-agent by
		// construction (`profile` selects ONE profile per agent), so there is no single
		// environment that is right for all of them — two agents on different providers
		// would produce contradictory values for the same variable. `claude` is the
		// default because it is the pack this repo's own workflows assume; --agent names
		// any other. The help says exactly this, and used to say "every configured one",
		// which was never what the code did.
		//
		// UNDER A GRANT WITH NO -p THE DEFAULT IS THE AD-HOC SLICE, `bash` (ES-D7's stand-in
		// for any name no selected pack installs): `eval "$(yolo host env --with-credentials
		// all)"` asks for keys, and claude's slice would export its profile's whole provider
		// shape beside them — ANTHROPIC_BASE_URL among it — re-pointing every claude that shell
		// starts, which is the one thing a grant never does. --agent still names an agent whose
		// profile the caller does want.
		//
		// A TYPED -p KEEPS THE VERB'S DEFAULT (ES-D21): `yolo host env -p zai` is claude's zai
		// slice, and the grant only adds keys beside it. Flipping to bash there would drop the
		// shape the -p asked for, which is not additive.
		agent = hostEnvDefaultAgent(grant != nil, profile)
	}
	typedProfile := profile
	profile, err := hostProfileFor(profile, agent, true)
	if err != nil {
		fmt.Fprintf(errw, "yolo host env: %v\n", err)
		return 2
	}
	profile, bareNote, err := narrowHostBareList(typedProfile, profile, agent)
	if err != nil {
		fmt.Fprintf(errw, "yolo host env: %v\n", err)
		return 1
	}
	if bareNote != "" {
		fmt.Fprintf(errw, "yolo host env: %s\n", bareNote)
	}

	// Only what yolo ADDS is printed, never the whole inherited environment: `yolo host
	// env` is meant to be eval'd, and echoing os.Environ() back into the shell would be
	// both enormous and a way to leak an unrelated secret into a log.
	added, disclosure, err := hostEnvDelta(agent, profile, grant, func(msg string) {
		fmt.Fprintf(errw, "Warning: %s\n", msg)
	})
	if err != nil {
		fmt.Fprintf(errw, "yolo host env: %v\n", err)
		return 1
	}
	// THE CREDENTIAL GATE'S DISCLOSURE, as `yolo host --` prints it and on stderr for the
	// same reason: an eval'ing shell reads only stdout, and "no silent narrowing"
	// (provider-credential-scope.md §4) holds for this front door too. The script below is
	// ONE agent's slice, so an env_sources credential another agent's profile claims is not
	// in it — which a shell that used to receive every value must be told.
	// Each block's head line is unindented (the gate's rule line, the grant's header) and
	// carries the verb's prefix; the indented lines under it are its detail.
	for _, line := range disclosure {
		if !strings.HasPrefix(line, "  ") {
			fmt.Fprintf(errw, "yolo host env: %s\n", line)
			continue
		}
		fmt.Fprintln(errw, line)
	}
	if format == "json" {
		m := jsonx.NewOrderedMap()
		for _, v := range added {
			if v.Unset {
				// A removal is a null, matching how env_sources spells one — so a tool
				// consuming this can tell "unset" from "set to empty".
				m.Set(v.Key, nil)
				continue
			}
			m.Set(v.Key, v.Value)
		}
		text, err := jsonx.DumpsIndent(m, 2)
		if err != nil {
			fmt.Fprintf(errw, "yolo host env: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, text)
		return 0
	}
	for _, v := range added {
		if v.Unset {
			fmt.Fprintf(out, "unset %s\n", v.Key)
			continue
		}
		fmt.Fprintf(out, "export %s=%s\n", v.Key, shellQuote(v.Value))
	}
	return 0
}

// hostEnvDelta returns just the variables yolo would add or remove, in composition order,
// and the credential gate's disclosure for them. The composition's own refusal travels with
// it — `yolo host env` is an observe verb and has to say why it has no environment to show,
// but it says it as an error rather than printing a refusal an eval'ing shell would swallow.
//
// The disclosure is the gate's lines, then the grant's when a --with-credentials request was
// given, which are printed on every run that carries one (OQ-ES5).
func hostEnvDelta(agent, profile string, grant *hostGrantRequest, warn func(string)) ([]agentenv.Var, []string, error) {
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}
	c := composeHostVarsGranting(config.UserScopeConfigOrEmpty(), workspace, agent, profile, grant, warn)
	if c.err != nil {
		return nil, nil, c.err
	}
	// The override check's findings are DISCLOSED here and refuse nothing, as the credential
	// pre-flight is: this is the observe verb, which has to answer even when the answer is "the
	// launch would refuse this" (hostExec's note on the pre-flight).
	refusal, warnings := c.envOverrideLines(os.Getenv)
	blocks := [][]string{c.selectionLines(), c.profileLines(), refusal}
	blocks = append(blocks, warnings...)
	var disclosure []string
	for _, block := range append(blocks, c.regionLines(), c.credentialScopeLines(), c.unservedLines(nil), c.grantLines()) {
		disclosure = append(disclosure, block...)
	}
	return c.vars, disclosure, nil
}

// hostEnvDefaultAgent is the agent `yolo host env` composes for when no --agent is given:
// claude, or the ad-hoc slice `bash` under a grant with no typed -p (ES-D16, ES-D21). One
// function because grantRemedy has to know it too, to spell back a script's slice.
func hostEnvDefaultAgent(granted bool, typedProfile string) string {
	if granted && typedProfile == "" {
		return "bash"
	}
	return "claude"
}

// shellQuote wraps a value in single quotes for `export K=V`, escaping embedded quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hostWrappers reports and toggles the PATH launch wrappers.
func hostWrappers(args []string, out, errw io.Writer, color bool) int {
	verb := "status"
	if len(args) > 0 {
		verb = args[0]
	}
	pr := richtext.Printer{W: out, Color: color}
	switch verb {
	case "-h", "--help", "help":
		fmt.Fprintln(out, hostUsage)
		return 0
	case "status":
		return hostWrappersStatus(pr, errw)
	case "enable", "disable":
		// DELETED 2026-09-22. These wrote `host_wrappers` into the user's config file — a verb
		// whose whole effect was a one-key edit anyone can make by hand, and which therefore
		// existed to make yolo the author of a line the user owns. Two reasons it went:
		//
		//  - `host_wrappers` is now DERIVED: unset at `host_management: "own"` means ON, so the
		//    common case needs no key at all and the verb had nothing left to enable.
		//  - A command that edits a config file is a second writer of that file. `yolo config`
		//    is where config edits live; a per-key mutation verb beside it is drift.
		fmt.Fprintf(errw, "yolo host wrappers: `%s` was removed — wrappers are ON by default "+
			"when `host_management` is \"own\", so there is usually nothing to set.\n"+
			"To force them off, put `\"host_wrappers\": false` in %s yourself; an explicit value "+
			"always wins over the default.\nRun `yolo host wrappers status` to see what is in "+
			"effect.\n", verb, paths.UserConfigPath())
		return 2
	default:
		fmt.Fprintf(errw, "yolo host wrappers: unknown verb %q (want status)\n", verb)
		return 1
	}
}

func hostWrappersStatus(pr richtext.Printer, errw io.Writer) int {
	dir := paths.WrapDir()
	enabled := config.HostWrappersEnabled()
	pr.Printf("[bold]host_wrappers[/bold]  %v  [dim]%s[/dim]", enabled, paths.UserConfigPath())
	pr.Printf("[bold]wrapper dir[/bold]    %s", dir)

	entries, err := os.ReadDir(dir)
	switch {
	case err != nil && os.IsNotExist(err):
		pr.Printf("[dim]not generated yet[/dim]")
	case err != nil:
		fmt.Fprintf(errw, "yolo host wrappers: reading %s: %v\n", dir, err)
		return 1
	default:
		var names []string
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			pr.Printf("[dim]generated, empty[/dim]")
		} else {
			pr.Printf("[bold]wrappers[/bold]       %s", strings.Join(names, " "))
		}
	}

	if hostwrap.OnPath(os.Getenv("PATH"), dir) {
		pr.Printf("[green]on PATH[/green]")
		return 0
	}
	pr.Printf("[yellow]NOT on this shell's PATH[/yellow] — add this line to your shell rc:")
	pr.Printf("  [bold]%s[/bold]", hostwrap.PathLine(dir))
	return 0
}

// hostProviderSectionRefusal is the provider and profile section of validation
// (config.ValidateProviderSection, notch-convergence NC-D35) over the user scope a host notch
// composes from: nil when it is clean, else one error naming every problem in the words each
// host reader prints. Its warnings (a retired key read off an in-jail snapshot) go to warn, or
// nowhere when it is nil.
//
// ONE FUNCTION FOR EVERY HOST READER OF THOSE KEYS: the launch composition (composeHostVarsWith),
// the render composition (composeHostInputs: `yolo host apply` in both postures, the automatic
// apply a wrapped launch runs, `yolo config render --at host`) and the launch gate, which asks
// before its observe pass. Every one of them reads the selection off the `profile` key alone, so
// a reader that skipped this refusal did not refuse the retired `use_profiles`: it ignored it,
// and an --assert then deselected the profile an earlier apply had written into the real home
// (PP-D11: "use_profiles is an error on the host").
//
// Each problem names the file and line its key was written at, as the launch's and `yolo
// check`'s do (config's sources.go). Every caller hands this the user scope
// (config.UserScopeConfigOrEmpty), so the record is that scope's, read again only when there is
// something to locate.
func hostProviderSectionRefusal(cfg *jsonx.OrderedMap, warn func(string)) error {
	errs, warns := config.ValidateProviderSection(cfg)
	if len(errs) > 0 || len(warns) > 0 {
		src := config.UserScopeSources()
		errs, warns = src.Annotate(errs), src.Annotate(warns)
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %s that every launch refuses (`yolo check` reports the "+
			"same):\n  ✗ %s", plural(len(errs), "a problem", fmt.Sprintf("%d problems", len(errs))),
			strings.Join(errs, "\n  ✗ "))
	}
	if warn != nil {
		for _, w := range warns {
			warn(w)
		}
	}
	return nil
}
