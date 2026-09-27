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
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
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

Usage:
  yolo host [flags] -- <command> [args...]   run a command with the composed environment
  yolo host apply [flags]                    render config surfaces into your real home
  yolo host env [flags]                      print the composed environment
  yolo host wrappers [status]                report the PATH launch wrappers

Exec flags (yolo host -- ...):
  --profile <name>, -p <name>   Select a declared profile for the wrapped COMMAND, this
                                launch only. Any command, not only an agent: an ad-hoc
                                one (bash, curl, terraform) then receives that profile's
                                claimed env_sources credentials, which are otherwise
                                withheld from it. use_profiles cannot do this for a
                                command no pack installs; only the typed flag can.
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
                                Nothing else implies it: not -p, not use_profiles, not
                                any YOLO_ALLOW_* variable, and no config key. HOST ONLY:
                                a jail launch refuses it.
  --help, -h                    Show this help.

With ` + "`host_apply_on_launch`" + ` enabled (defaulting to on when ` + "`host_wrappers: true`" + `),
` + "`yolo host -- <agent>`" + ` checks whether ` + "`yolo host apply`" + ` would change anything,
and automatically synchronizes host configuration before launch — silently exec'ing when fresh.
When first-time adoption would overwrite unmanaged host keys, it prompts for confirmation or
reads approval from ` + "`YOLO_ACCEPT_CONFIG_CHANGES`" + ` (any non-empty value, this launch only).
An apply that would ask anything else, or a configured pack that cannot be resolved, renders
NOTHING: the launch says what needs deciding or fixing, and the agent starts on the render
already in place. See ` + "`yolo config-ref`" + `.

apply flags:
  --assert        Write. Without it apply is a DRY RUN and writes nothing.
  --dry-run       Force the dry run, even alongside --assert.
  --verbose, -v   List every destination the report otherwise counts: each settled
                  surface, every dependency probe, every skill by destination.
  --shell-init    Append the PATH line for the wrapper dir to your shell rc.
                  yolo otherwise only PRINTS that line — the rc is your file.
  --revert        Take yolo back OUT of this home: remove the keys it asserted, on
                  the authority of the provenance record it wrote, and delete that
                  record. A key you set yourself (recorded "host") is never touched.
                  A DRY RUN like every other posture here — it lists each key with
                  the attribution the removal rests on, and --assert performs it.
                  It REMOVES what yolo wrote; it does not restore what a key held
                  before yolo wrote it, because nothing snapshots that. Needs
                  host_management "assert" — refused at "none" (nothing was
                  written) and at "own" (the file is derived; delete it instead).
  --format json   Emit the dry run as data instead of a report: destinations, losses,
                  blockers, the counts and the outcome. --json is the same flag.
                  Refused with --assert (exit 2): that posture acts, and an acting
                  verb does not grow a second output mode.

The report ends in one sentence saying how the run went, with the counts beneath it.
Packs resolve the way a launch resolves them: a git pack from the pack store, which this
command first fetches when it never was and refreshes when it follows a branch (hourly; a
tag or commit pin is never re-fetched), a local one from its path. If ANY configured pack
cannot be resolved, an --assert is REFUSED with nothing written — an incomplete pack set
is never applied — and the dry run names each pack, why, and the fix.
A missing declared dependency STOPS an --assert: yolo shows the install command each
pack declares and offers to run it, and a NO refuses the run with nothing written.
` + "`yolo pack --help`" + ` says what each contribution kind is, and ` + "`yolo config-ref`" + ` says why
some of them do not apply at this notch.

env flags:
  --format <fmt>  export (default) or json.
  --profile <name>, -p <name>   As above, for the --agent it composes.
  --with-credentials <provider[,provider...]|all>
                  As above, for the script: it exports the granted keys. With no
                  --agent the script is an ad-hoc command's slice (as --agent bash),
                  so no agent's provider shape reaches the shell alongside the keys.
  --agent <name>  Compose as if launching this agent (default: claude, or bash under
                  --with-credentials). The agent name selects which use_profiles entry
                  applies, and the output is that agent's slice: a provider credential
                  another agent's profile claims is withheld from it, and stderr says
                  which, by name.

Examples:
  yolo host -- claude                 # bare claude, with the composed environment
  yolo host -p bedrock -- claude      # ... on the bedrock profile, this launch only
  yolo host -p zai -- curl ...        # any command, handed zai's claimed key
  yolo host --with-credentials all -- usage-bar   # every provider's key, keys only
  yolo host -p bedrock --with-credentials zai -- claude   # bedrock, plus zai's key
  eval "$(yolo host env)"             # the same environment, in this shell
  eval "$(yolo host env --agent bash -p zai)"   # zai's key, in this shell
  eval "$(yolo host env --with-credentials all)"   # every provider's key, in this shell
  yolo host apply --assert            # write the config surfaces
  yolo host apply --revert            # what would withdrawing yolo remove?

` + "`yolo apply --at host`" + ` is the systematic spelling of ` + "`yolo host apply`" + `; both remain.`

// runHost is the `yolo host` entry point.
func runHost(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:] // drop the "host" token
	}
	return hostMain(rest, os.Stdout, os.Stderr, colorForWriter(os.Stdout), os.Stdin)
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

// hostExecFlags is what the exec half accepts before `--`.
type hostExecFlags struct {
	profile string
	// grant is the --with-credentials request, nil when the flag was not given: the only
	// spelling that makes one (credential-sources-separation.md OQ-ES5, ruled for the host).
	grant *hostGrantRequest
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
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--profile" || a == "-p":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo host: %s needs a value\n", a)
				return f, false
			}
			i++
			f.profile = args[i]
		case strings.HasPrefix(a, "--profile="):
			f.profile = a[len("--profile="):]
		case a == withCredentialsFlag:
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo host: %s needs a value\n", a)
				return f, false
			}
			i++
			if !grant(args[i]) {
				return f, false
			}
		case strings.HasPrefix(a, withCredentialsFlag+"="):
			if !grant(a[len(withCredentialsFlag+"="):]) {
				return f, false
			}
		default:
			fmt.Fprintf(errw, "yolo host: unexpected argument %q before `--`\n\n%s\n", a, hostUsage)
			return f, false
		}
	}
	return f, true
}

// hostExec composes the environment and launches the target.
//
// Ordinary launches still use syscall.Exec. Managed Codex stays resident because its
// dynamic loopback credential adapter must be closed when the agent exits.
func hostExec(flagArgs, cmd []string, out, errw io.Writer, stdin io.Reader) int {
	flags, ok := parseHostExecFlags(flagArgs, errw)
	if !ok {
		return 2
	}
	if len(cmd) == 0 {
		fmt.Fprintf(errw, "yolo host: nothing to run after `--`\n\n%s\n", hostUsage)
		return 2
	}
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

	launch := composeHostLaunch(cmd[0], flags.profile, flags.grant, func(msg string) {
		fmt.Fprintf(errw, "Warning: %s\n", msg)
	})

	// THE PROVIDER COMPOSITION's own refusal, before anything else: a provider table this
	// notch cannot compose is one no launch may exec from, and the credential pre-flight
	// below would be answering a question about a table that was never built. Same exit
	// the pre-flight takes, same renderer's shape — verdict first, then the remedy.
	if launch.err != nil {
		fmt.Fprintf(errw, "yolo host: refusing to launch: %v\n", launch.err)
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
	if lines := launch.credentialGaps(os.Getenv); len(lines) > 0 {
		held := os.Getenv(paths.AllowMissingProvidersEnv) != ""
		if held {
			// The override says what it is suppressing rather than going quiet — and does
			// not re-offer the hatch it just honoured.
			lines = append([]string{"Warning: " + paths.AllowMissingProvidersEnv +
				" is set — CONTINUING, with a selected pack's provider credential still " +
				"missing. Nothing was repaired: the agent's first request against that " +
				"provider will still fail."}, lines...)
		} else {
			lines = append(lines, "  Put the variable in one of the consulted channels, or "+
				"launch anyway with "+paths.AllowMissingProvidersEnv+"=1.")
		}
		for i, line := range lines {
			if i == 0 {
				fmt.Fprintf(errw, "yolo host: %s\n", line)
				continue
			}
			fmt.Fprintln(errw, line)
		}
		if !held {
			return 1
		}
	}

	// THE CREDENTIAL GATE'S DISCLOSURE (docs/design/provider-credential-scope.md §4, "no
	// silent narrowing"): a launch that withholds a credential the user configured says so,
	// on stderr like every other line here, names only. The same wording the jail notch
	// prints, because it is the same gate's answer.
	// THE GRANT'S DISCLOSURE (OQ-ES5): on every run the flag is given, whatever it delivered,
	// names only. Never suppressible (OQ-RO3), so it is printed unconditionally here rather
	// than folded into a line a quieter path could skip.
	for _, block := range [][]string{launch.credentialScopeLines(), launch.grantLines()} {
		for i, line := range block {
			if i == 0 {
				fmt.Fprintf(errw, "yolo host: %s\n", line)
				continue
			}
			fmt.Fprintln(errw, line)
		}
	}

	target, err := resolveHostTarget(os.Getenv("PATH"), cmd[0])
	if err != nil {
		fmt.Fprintf(errw, "yolo host: %v\n", err)
		return 127
	}
	// argv[0] stays the name the user typed, not the resolved path: agents branch on it
	// (usage text, `$0`), and handing them an absolute path changes what they print.
	argv := append([]string{cmd[0]}, cmd[1:]...)
	managed, err := prepareOpenAIAuthHost(cmd[0], errw)
	if err != nil {
		fmt.Fprintf(errw, "yolo host: prepare shared OpenAI authentication: %v\n", err)
		return 1
	}
	environ := launch.environ()
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
	if err := hostSyscallExec(target, argv, environ); err != nil {
		fmt.Fprintf(errw, "yolo host: exec %s: %v\n", target, err)
		return 126
	}
	return 0 // unreachable: a successful Exec never returns
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
// day they exist without this list needing to be revisited.
func yoloManagedDirs() []string {
	return []string{paths.GeneratedBinDir()}
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
	// shape, and the removals last.
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
	// command is the command as the user typed it after `--`, for the remedy to spell back;
	// empty for `yolo host env`, which launches nothing.
	command string
	// scopeInput is the credential gate's input for this launch, for the remedy to ask the gate
	// whether a -p it would name composes (runsOn).
	scopeInput packload.ScopeInput
	// profile is the profile this launch selected for its command — a typed -p, else the
	// command's use_profiles entry — "" when none. The remedy says the -p it names replaces
	// it (remedyAction).
	profile string
	// grant is the --with-credentials request this launch was given, resolved; nil without
	// the flag. Its disclosure is grantLines.
	grant *hostGrant
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

// selectedProviders is the provider this launch's agent selected, as the narrowed
// pre-flight reads it (OQ-CN3) — none when the composition refused before the gate.
func (c *hostComposition) selectedProviders() []string {
	if c.scope == nil {
		return nil
	}
	return c.scope.SelectedProviders()
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

// credentialRemedy is ES-D2's remedy for a group of withheld names: the one existing way to
// receive them at this notch, a typed `-p` naming a declared profile that resolves to a
// claiming provider — `yolo host -p <profile> -- <cmd>`, which keys the launched command
// whatever it is (ES-D1). With no such profile it says to declare one, because `-p` takes a
// profile name, never a provider's. Either way the profile is one the named command can run
// on (runsOn, ES-D10), so the line never names a launch that refuses.
//
// The front door decides the spelling: `yolo host --` names the command it was given, and
// `yolo host env`, which launches nothing, names the ad-hoc slice for the shell beside the exec
// spelling for its agent. The shell spelling is `--agent bash` and never the verb's own
// `--agent`: an agent's slice on that profile carries its whole provider shape (claude's
// ANTHROPIC_BASE_URL, and the key again under ANTHROPIC_AUTH_TOKEN), which an eval'ing shell
// would then hand, undisclosed, to every process it starts (CN-D13). Any name no selected pack
// installs composes the same slice, so `bash` stands for all of them, as §3.1 and the help's
// example spell it.
func (c *hostComposition) credentialRemedy(claimants []string) string {
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
			c.remedyAction(example, runs))
	}
	profile, runs := candidates[0], false
	for _, name := range candidates {
		if c.runsOn(name, nil) {
			profile, runs = name, true
			break
		}
	}
	action := c.remedyAction(profile, runs)
	return strings.ToUpper(action[:1]) + action[1:]
}

// runsOn reports whether `yolo host -p <profile> -- <this command>` would compose rather than
// refuse, asked the way that launch asks: the credential gate over this launch's own inputs
// with the candidate selected for the command, whose AgentEnv holds the protocol pairing gate
// and the pack's env derive. A remedy is a command the user will run, so it may never name one
// that refuses — `yolo host -p cerebras -- claude` does, cerebras speaking only openai and no
// `needs` joining wire-bridge at this notch. A command no selected pack installs runs on any
// declared profile, since no pack code runs for it, so it is never asked.
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

// remedyAction is the remedy's instruction for one profile, as a clause starting "to …";
// runs is runsOn's answer for it.
//
// A -p NAMES ONE PROFILE, so the one it names REPLACES the command's own: a withheld name
// belongs to a provider this launch's profile did not select, and on an agent a selected pack
// installs, the named -p re-points the agent's backend rather than adding a key to it. So an
// agent's remedy is worded as the switch it is ("run claude on the zai profile"), an ad-hoc
// command's as the grant it is ("hand it to bash"), and either says which profile it replaces
// when the launch selected one. An agent that cannot run on the profile is named only to say
// so, and the key goes to the ad-hoc spelling instead: `bash` at the exec, and the shell
// spelling alone at `yolo host env`.
func (c *hostComposition) remedyAction(profile string, runs bool) string {
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
	shell := fmt.Sprintf("to receive it in this shell: `eval \"$(yolo host env --agent bash -p %s)\"`", p)
	var launch string
	switch {
	case !runs:
		cannot := fmt.Sprintf("%s cannot run on the %s profile at this notch", cmd, profile)
		if c.command == "" {
			return fmt.Sprintf("%s (%s)", shell, cannot)
		}
		return fmt.Sprintf("to hand it to an ad-hoc command for one launch instead, since %s: "+
			"`yolo host -p %s -- bash`", cannot, p)
	case selectedPackInstalls(c.packs, c.agent):
		launch = fmt.Sprintf("to run %s on the %s profile for one launch%s: `yolo host -p %s -- %s`",
			cmd, profile, replacing, p, cmd)
	default:
		launch = fmt.Sprintf("to hand it to %s for one launch%s: `yolo host -p %s -- %s`", cmd, replacing, p, cmd)
	}
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
	return packload.ProviderCredentialGaps(c.packs, c.providers, c.selectedProviders(), func(name string) (string, bool) {
		if v := idx[name]; v != "" {
			return v, true
		}
		if v := getenv(name); v != "" {
			return v, true
		}
		return "", false
	}, consulted)
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
//     the same runner the jail's podman argv is built from).
//  4. removals — a null in env_sources, i.e. `unset AWS_PROFILE`. Last, so a removal
//     beats an assignment from any earlier step.
func composeHostLaunch(bin, profile string, grant *hostGrantRequest, warn func(string)) *hostComposition {
	agent := filepath.Base(bin)
	cfg := config.UserScopeConfigOrEmpty()
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}

	c := composeHostVarsGranting(cfg, workspace, agent, profile, grant, warn)
	c.command = bin
	return c
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
//     by packload.AgentEnv, the same runner the jail's podman argv is built from.
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
	var vars []agentenv.Var
	c := &hostComposition{agent: agent}

	// The selected packs, read once for both the env they declare and the provider they
	// ship. The config here is USER SCOPE ONLY (the boundary this function's doc records),
	// so the composed provider table below is user entries over pack facts and never a
	// workspace's. A pack set that cannot be resolved right now contributes nothing — an
	// empty slice makes every fold below a no-op — which is loadedHostPacks' own contract,
	// so the error needs no second handling here. A single pack that does not resolve is
	// WARNED about by name: it is dropped from this launch's env, and silence would make
	// its missing provider vars look like a credential problem.
	packs, unresolved, _ := loadedHostPacks()
	if warn != nil {
		for _, u := range unresolved {
			warn(fmt.Sprintf("pack %s could not be resolved, so it contributes nothing to "+
				"this launch's environment: %s", u.Name, u.Reason))
		}
	}
	c.packs = packs
	// The profile this launch selects, resolved once: it gates (1) and feeds (3), and
	// both must read the same selection or the env a host launch carries and the one its
	// launch line describes would disagree.
	effective := effectiveHostProfiles(cfg, agent, profile)
	profileName := ""
	if v, ok := effective.Get(agent); ok {
		if s, isStr := v.(string); isStr {
			profileName = s
		}
	}
	// Scoped to the ONE agent this process is. A jail carries the whole CLI-keyed table
	// because one container holds every agent; a host launch composes a single process, so
	// only the profile selected at THIS agent's own CLI name may contribute env to it.
	agentTable := map[string]string{}
	if profileName != "" {
		agentTable[agent] = profileName
	}
	c.profile = profileName
	// ONLY A TYPED -p KEYS A COMMAND NO PACK INSTALLS
	// (docs/design/credential-sources-separation.md ES-D5). The one-agent table above keys
	// whatever basename was launched, which is what makes `yolo host -p zai -- bash` the host's
	// grant (ES-D1) — and what made a `use_profiles` entry for `bash` deliver here too, only
	// because this notch never runs ValidateConfig while `yolo check` and every jail launch
	// refuse that entry in the same user file. So a use_profiles key doing the selecting is
	// asked the validator's own question, and refused with its message plus the spelling that
	// IS legal.
	if profile == "" && profileName != "" && !selectedPackInstalls(packs, agent) {
		if msg, unknown := config.UnknownUseProfileKey(agent); unknown {
			p, a := shquote.Quote(profileName), shquote.Quote(agent)
			c.err = fmt.Errorf("%s. Only a typed -p selects a profile for a command no pack "+
				"installs: remove the entry and run `yolo host -p %s -- %s` (or "+
				"`yolo host env --agent %s -p %s` for a shell)", msg, p, a, a, p)
			return c
		}
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
	// reversal was ruled to end. The declared set is the staged packs' kind:profile names
	// plus the user's own entries — and this notch's known gap applies to it as it does
	// to the provider table above: a pack that could not be resolved this launch
	// contributes no declaration, so a profile only THAT pack declared refuses here
	// rather than composing nothing.
	userProfiles, err := config.LoadProfiles(warn)
	if err != nil {
		c.err = err
		return c
	}
	// Composed HERE rather than at (3) below, because the profile resolution reads the
	// declared options off it (packload.providerOptions) and must measure the surface
	// this launch carries. The same object is reused at (3), so the pre-flight and the
	// env derive read the table the resolution was measured against.
	providers, err := composedHostProviders(cfg, packs)
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
	// a ctx.via_url nothing serves. packload.ViaInert clears the address, and ViaURLFor, the
	// predicate both notches' derive paths ask, answers "" for every agent.
	resolvedProfiles = packload.ViaInert(resolvedProfiles)
	c.resolved = resolvedProfiles
	if profileName != "" {
		declared := packload.DeclaredProfileNames(packs, userProfiles)
		if i := sort.SearchStrings(declared, profileName); i >= len(declared) || declared[i] != profileName {
			c.err = fmt.Errorf("packs: profile %q selected for %s: %s", profileName, agent,
				packload.UndeclaredProfileMessage(profileName, declared))
			return c
		}
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
	// looked and not only that the key never arrived.
	c.consulted = append(config.DescribeEnvSources(workspace, scoped), "the invoking shell's environment")

	// THE GRANT (docs/design/credential-sources-separation.md OQ-ES5, ruled for the host
	// 2026-09-27): the named providers' claimed env_sources values, for this one process, keys
	// only. Resolved here, after the table and env_sources it reads and before the gate, which
	// delivers it — a grant is one more recipient of a claimed value, so the gate's own claim
	// model decides which names it carries. Nothing but the typed flag reaches this: no config
	// key, no use_profiles entry, no -p and no environment variable builds a request.
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
	// does. One the store does not have is dropped and warned about above.
	c.scopeInput = packload.ScopeInput{
		Packs:      packs,
		Providers:  providers,
		Profiles:   agentTable,
		Resolved:   resolvedProfiles,
		EnvSources: userEnv,
		Fallback:   os.LookupEnv,
		Grants:     grants,
		// What composedHostProviders left out, so a pairing only one of them would resolve
		// refuses naming why (ES-D18) rather than as one nothing declares an adapter for.
		UnservedAdaptations: packload.ServiceAdaptations(packs, hostAdapterAddresses()),
	}
	scope, err := packload.ScopeCredentials(c.scopeInput)
	if err != nil {
		var unserved *packload.UnservedAdapterError
		if errors.As(err, &unserved) && unserved.Agent == agent {
			err = unservedAdapterRefusal(unserved, profileName)
		}
		c.err = err
		return c
	}
	c.scope = scope
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
	fold := packload.EnvFold(packs, agentTable, agent)
	if delivery != nil {
		fold = delivery.Fold
	}
	for _, e := range fold {
		vars = append(vars, agentenv.Var{Key: e.Key, Value: e.Value})
	}

	// (2) the secret channel, as the gate delivers it to this agent: every unclaimed entry
	// and its own provider's claimed ones, in hydration order.
	sources := scope.EnvSourcesFor(agent)
	for _, k := range sources.Keys() {
		v, _ := sources.Get(k)
		if s, ok := v.(string); ok {
			vars = append(vars, agentenv.Var{Key: k, Value: s})
		}
	}

	// (3) the profile's provider vars, the env derive's output the gate composed.
	if delivery != nil {
		vars = append(vars, delivery.Shape...)
	}

	// (4) removals last, so an unset beats every assignment above no matter which source
	// made it — the env_sources nulls from the same pass as (2) (the same scoped config,
	// so an inline null's cancellation by a later dotenv cannot disagree with the
	// assignments). The pack fold no longer contributes any: its only removal spelling
	// died with the profile body. Sorted, because a set of removals has no order to
	// preserve and the `export` script must not reshuffle between runs.
	for _, k := range removals {
		vars = append(vars, agentenv.Var{Key: k, Unset: true})
	}
	c.vars = vars
	return c
}

// selectedPackInstalls reports whether a selected pack installs a CLI named bin — the case in
// which a use_profiles key for it is certainly one the validator accepts, so ES-D5's refusal
// need not enumerate the whole namespace to know it.
func selectedPackInstalls(packs []*packload.Pack, bin string) bool {
	for _, p := range packs {
		for _, b := range p.InstallBins() {
			if b == bin {
				return true
			}
		}
	}
	return false
}

// composedHostProviders is the host notch's ONE provider composition — the host spelling
// of the jail notch's composedProviders (internal/cli/run/assemble.go): the user's
// `providers` config entries with every selected pack's shipped `kind: "provider"` facts
// composed under them, per field. Composed ONCE and its result handed to BOTH of this
// launch's consumers — the provider env derive in composeHostVars and the providers.md#the-credential-preflight pre-flight's
// c.providers — because packload/providers.go states the composition happens exactly once
// per launch, and two compositions would be two chances for the check and the exec to
// disagree about what the launch carries.
func composedHostProviders(cfg *jsonx.OrderedMap, packs []*packload.Pack) (*jsonx.OrderedMap, error) {
	var user *jsonx.OrderedMap
	if v, ok := cfg.Get("providers"); ok {
		user, _ = v.(*jsonx.OrderedMap)
	}
	// NO ADDRESS A PACK'S OWN SERVICE SERVES (docs/design/credential-sources-separation.md
	// ES-D18): `yolo host` starts no pack service, so the wire bridge's adapter address —
	// declared by packs/wire-bridge beside the service whose in-jail daemon listens on it — is
	// one no host process serves (wire-bridge.md, "No host-side bridge"). Composed in, an agent
	// the bridge would front is pointed at a dead address: claude on cerebras at
	// http://127.0.0.1:8214. Left out, an agent that speaks the provider's own wire resolves to
	// it directly, and one that cannot refuses at the gate, which composeHostVars words.
	return packload.ComposeProviders(user, packs, packload.WithAdapterAddresses(hostAdapterAddresses()),
		packload.WithoutServiceAdaptations())
}

// hostAdapterAddresses is the user's adapter address overrides, read the same way the jail
// notch reads them (run.composedProviders): from the user file directly, so the two notches
// cannot disagree about where an adapted provider answers.
func hostAdapterAddresses() map[string]string {
	addresses, _ := config.LoadAdapterAddresses(nil)
	return addresses
}

// unservedAdapterRefusal words the gate's *packload.UnservedAdapterError for this notch: the
// profile, the address the agent would have been pointed at and what serves it, that nothing
// at the host does, and the launch where the profile works.
func unservedAdapterRefusal(e *packload.UnservedAdapterError, profile string) error {
	a := e.Adaptation
	agent, p := shquote.Quote(e.Agent), shquote.Quote(profile)
	return fmt.Errorf("profile %q points %s at %s, where pack %q adapts %q → %q for provider %q — "+
		"and that address is served by the pack's own %q service, a daemon yolo runs only inside a "+
		"jail. No host process serves it, so `yolo host` will not run %s pointed at it.\n"+
		"  The profile works inside a jail: `yolo -p %s=%s -- %s`.\n"+
		"  At the host, choose a profile whose provider %s speaks to directly",
		profile, agent, a.Address, a.Pack, a.From, a.To, e.Provider, a.Service, agent,
		agent, p, agent, agent)
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

// loadedHostPacks resolves the selected packs for a host launch, plus every one it could not
// resolve. A pack that cannot be resolved (a git pack not in the store) contributes nothing
// rather than failing the launch — the user asked to run an agent, not to reconcile their pack
// set — but it is RETURNED, so the caller names it rather than dropping it in silence.
func loadedHostPacks() ([]*packload.Pack, []unresolvedPack, error) {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil, nil, err
	}
	var packs []*packload.Pack
	var unresolved []unresolvedPack
	for _, e := range entries {
		p, rerr := resolveConfiguredPack(e)
		if rerr != nil {
			unresolved = append(unresolved, newUnresolvedPack(e.Name, rerr))
			continue
		}
		packs = append(packs, p)
	}
	return packs, unresolved, nil
}

// effectiveHostProfiles returns the use_profiles map with a `-p` override applied to
// the agent being launched, mirroring what `yolo run -p` does for a jail so the two
// notches agree about what a profile selects.
func effectiveHostProfiles(cfg *jsonx.OrderedMap, agent, profile string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if v, ok := cfg.Get("use_profiles"); ok {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			for _, k := range m.Keys() {
				val, _ := m.Get(k)
				out.Set(k, val)
			}
		}
	}
	if profile != "" && agent != "" {
		out.Set(agent, profile)
	}
	return out
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
// The HOST half reads the USER-SCOPE config's use_profiles and nothing else, and that is
// the boundary every host composition draws (UserScopeConfig's whole argument): a gated
// overlay's payload lands in the user's REAL config files, and the one this design ships
// first rewrites ANTHROPIC_BASE_URL — where an agent sends the credentials the user
// already has. Letting a workspace yolo-jail.jsonc (agent-editable, /workspace is
// bind-mounted rw) switch that on would hand a cloned repository the redirection
// host_wrappers refuses it, so workspace scope stays inexpressible here exactly as it is
// there. No `-p` is honored because neither caller takes one — the flag exists on `yolo
// host --` and `yolo --`, which compose per-process and read this same table through their
// own channels.
func overlayGateProfiles(notch render.Kind) map[string]string {
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
	return packload.ProfileTable(effectiveHostProfiles(config.UserScopeConfigOrEmpty(), "", ""))
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
		switch a := args[i]; {
		case isHelpToken(a):
			fmt.Fprintln(out, hostUsage)
			return 0
		case a == withCredentialsFlag:
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo host env: %s needs a value\n", a)
				return 2
			}
			i++
			if !addGrant(args[i]) {
				return 2
			}
		case strings.HasPrefix(a, withCredentialsFlag+"="):
			if !addGrant(a[len(withCredentialsFlag+"="):]) {
				return 2
			}
		case a == "--format":
			if i+1 >= len(args) {
				fmt.Fprintln(errw, "yolo host env: --format needs a value (export|json)")
				return 2
			}
			i++
			format = args[i]
		case strings.HasPrefix(a, "--format="):
			format = a[len("--format="):]
		case a == "--profile" || a == "-p":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo host env: %s needs a value\n", a)
				return 2
			}
			i++
			profile = args[i]
		case strings.HasPrefix(a, "--profile="):
			profile = a[len("--profile="):]
		case a == "--agent":
			if i+1 >= len(args) {
				fmt.Fprintln(errw, "yolo host env: --agent needs a value")
				return 2
			}
			i++
			agent = args[i]
		case strings.HasPrefix(a, "--agent="):
			agent = a[len("--agent="):]
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
		// construction (use_profiles maps ONE profile per agent), so there is no single
		// environment that is right for all of them — two agents on different providers
		// would produce contradictory values for the same variable. `claude` is the
		// default because it is the pack this repo's own workflows assume; --agent names
		// any other. The help says exactly this, and used to say "every configured one",
		// which was never what the code did.
		//
		// UNDER A GRANT THE DEFAULT IS THE AD-HOC SLICE, `bash` (ES-D7's stand-in for any
		// name no selected pack installs): `eval "$(yolo host env --with-credentials all)"`
		// asks for keys, and claude's slice would export its profile's whole provider shape
		// beside them — ANTHROPIC_BASE_URL among it — re-pointing every claude that shell
		// starts, which is the one thing a grant never does. --agent still names an agent
		// whose profile the caller does want.
		agent = "claude"
		if grant != nil {
			agent = "bash"
		}
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
	return c.vars, append(c.credentialScopeLines(), c.grantLines()...), nil
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
