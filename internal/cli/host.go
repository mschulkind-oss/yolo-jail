package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
  yolo host wrappers [status|enable|disable] manage the PATH launch wrappers

Exec flags (yolo host -- ...):
  --profile <name>, -p <name>   Profile/provider preset for the wrapped agent.
  --help, -h                    Show this help.

apply flags:
  --assert        Write. Without it apply OBSERVES and writes nothing.
  --dry-run       Force observe, even alongside --assert.
  --shell-init    Append the PATH line for the wrapper dir to your shell rc.
                  yolo otherwise only PRINTS that line — the rc is your file.

env flags:
  --format <fmt>  export (default) or json.
  --profile <name>, -p <name>   As above.
  --agent <name>  Compose as if launching this agent (default: claude). The agent name
                  selects which use_profiles entry applies.

Examples:
  yolo host -- claude                 # bare claude, with the composed environment
  yolo host -p bedrock -- claude      # ... on the bedrock profile, this launch only
  eval "$(yolo host env)"             # the same environment, in this shell
  yolo host apply --assert            # write the config surfaces

` + "`yolo apply --at host`" + ` is the systematic spelling of ` + "`yolo host apply`" + `; both remain.`

// runHost is the `yolo host` entry point.
func runHost(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:] // drop the "host" token
	}
	return hostMain(rest, os.Stdout, os.Stderr, isTTYStdout(), os.Stdin)
}

// hostMain dispatches `yolo host`.
//
// `--` IS CHECKED FIRST, before any verb, and that ordering is the whole grammar: the
// exec half takes flags before the separator (`yolo host -p bedrock -- claude`), so
// args[0] is routinely a flag rather than a verb, and a verb switch that ran first would
// have to re-implement flag parsing to find out whether a verb was even present.
func hostMain(args []string, out, errw io.Writer, color bool, stdin io.Reader) int {
	if i := indexOf(args, "--"); i >= 0 {
		return hostExec(args[:i], args[i+1:], out, errw)
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
}

func parseHostExecFlags(args []string, errw io.Writer) (hostExecFlags, bool) {
	var f hostExecFlags
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
		default:
			fmt.Fprintf(errw, "yolo host: unexpected argument %q before `--`\n\n%s\n", a, hostUsage)
			return f, false
		}
	}
	return f, true
}

// hostExec composes the environment and REPLACES this process with the target.
//
// syscall.Exec rather than fork+wait is deliberate: yolo has nothing to do after the
// agent starts — no teardown hook, no lock, no capture fold, none of what makes
// `yolo run` supervise a child — so staying resident would only add a process to every
// launch and put yolo between the agent and its terminal signals.
func hostExec(flagArgs, cmd []string, out, errw io.Writer) int {
	flags, ok := parseHostExecFlags(flagArgs, errw)
	if !ok {
		return 2
	}
	if len(cmd) == 0 {
		fmt.Fprintf(errw, "yolo host: nothing to run after `--`\n\n%s\n", hostUsage)
		return 2
	}
	_ = out

	launch := composeHostLaunch(cmd[0], flags.profile, func(msg string) {
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

	// THE CREDENTIAL PRE-FLIGHT at the host notch (profiles-as-pack-variants.md §6.2,
	// OQ-13) — the same check the jail's launcher runs, on the environment THIS notch
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

	target, err := resolveHostTarget(os.Getenv("PATH"), cmd[0])
	if err != nil {
		fmt.Fprintf(errw, "yolo host: %v\n", err)
		return 127
	}
	// argv[0] stays the name the user typed, not the resolved path: agents branch on it
	// (usage text, `$0`), and handing them an absolute path changes what they print.
	argv := append([]string{cmd[0]}, cmd[1:]...)
	if err := syscall.Exec(target, argv, launch.environ()); err != nil {
		fmt.Fprintf(errw, "yolo host: exec %s: %v\n", target, err)
		return 126
	}
	return 0 // unreachable: a successful Exec never returns
}

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
// §6.2 credential pre-flight reads beside it. It exists because the pre-flight has to
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
}

// environ applies the composition over the environment this process inherited — the env
// the exec hands the agent, and therefore the thing "is the key set in this launch" is
// asked of.
func (c *hostComposition) environ() []string {
	return agentenv.Apply(os.Environ(), c.vars)
}

// credentialGaps is the §6.2 pre-flight for this launch, answered against environ().
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
	return packload.ProviderCredentialGaps(c.packs, c.providers, func(name string) (string, bool) {
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
	c := composeHostLaunch(bin, profile, warn)
	return c.environ(), c.agent, c.err
}

// composeHostLaunch composes the whole launch `yolo host -- <bin>` would exec: the
// environment, the agent name it resolved, and the facts the credential pre-flight reads
// beside them.
//
// The order is the one docs/design/host-agent-environment.md §6.1 step 3 specifies, and
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
func composeHostLaunch(bin, profile string, warn func(string)) *hostComposition {
	agent := filepath.Base(bin)
	cfg := config.UserScopeConfigOrEmpty()
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}

	return composeHostVars(cfg, workspace, agent, profile, warn)
}

// hostEnvVars is the composition itself, without the inherited environment — the
// vars-only projection `yolo host env` reads, so the observe verb and the exec half
// cannot disagree about what a launch would carry.
//
// The sources are docs/design/host-agent-environment.md §5.4's, in order:
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
	var vars []agentenv.Var
	c := &hostComposition{agent: agent}

	// The selected packs, read once for both the env they declare and the provider they
	// ship. The config here is USER SCOPE ONLY (the boundary this function's doc records),
	// so the composed provider table below is user entries over pack facts and never a
	// workspace's. A pack set that cannot be resolved right now contributes nothing — an
	// empty slice makes every fold below a no-op — which is loadedHostPacks' own contract,
	// so the error needs no second handling here.
	packs, _ := loadedHostPacks()
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
	resolvedProfiles, err := packload.ResolveProfiles(packs, userProfiles)
	if err != nil {
		c.err = err
		return c
	}
	if profileName != "" {
		declared := packload.DeclaredProfileNames(packs, userProfiles)
		if i := sort.SearchStrings(declared, profileName); i >= len(declared) || declared[i] != profileName {
			c.err = fmt.Errorf("packs: profile %q selected for %s: %s", profileName, agent,
				packload.UndeclaredProfileMessage(profileName, declared))
			return c
		}
	}

	// (1) the pack env fold, PER PACK — each pack's static `kind: "env"` keys, then the
	// keys of its `profile`-gated env contributions whose gate is satisfied (OQ-8). The
	// sequence is packload.EnvFold's, the ONE fold the jail notch reduces through
	// packload.EnvVarsFor: folding it here as all-static-then-all-gated instead gave a
	// key that pack A's gated env and pack B's static both write two answers (the jail
	// said the later pack's static wins, the host the earlier pack's gated value).
	// hostFoldParity_test.go pins the two notches to the same winner.
	//
	// Keys are sorted within each pack, because a map has no order and an `export` script
	// that reshuffles between runs is a diff nobody can read.
	//
	// Assignments only, and that is the OQ-PT8 shrink rather than a shortcut: the only
	// env map here that could spell a removal was the profile body's, whose
	// null-means-unset decoder died with the body. What a removal still has is (2)'s
	// env_sources nulls, held for (4) below.
	for _, e := range packload.EnvFold(packs, agentTable) {
		vars = append(vars, agentenv.Var{Key: e.Key, Value: e.Value})
	}

	// (2) the secret channel. The loader anchors relative entries beside the file that
	// declared them (config.AnchorEnvSources), so a user-config relative entry arrives
	// here absolute and legal. What is still refused is an UNANCHORED relative entry —
	// one from a hand-built config or a pre-ruling artifact — because the only
	// resolution left for it is the CURRENT DIRECTORY, which a workspace controls: cd
	// into a cloned repo, `yolo host -- claude`, and the repo's .env feeds a host
	// process. That would re-open, through the filesystem, the exact boundary the
	// user-scope-only cfg closes; hostScopedEnvSources is the backstop.
	scoped := hostScopedEnvSources(cfg, warn)
	// ONE pass for (2) and (4): the assignments and the removals are the same ordered
	// walk, and asking for them separately would read every dotenv file twice and warn
	// twice — noise a missing host-only file used to produce on every `yolo host env`.
	userEnv, removals := config.ResolveEnvSourcesFull(workspace, scoped, warn)
	// What this launch consulted for credentials, recorded as it is consulted: the
	// env_sources entries that survived the scope filter, plus the shell this process
	// inherited. The §6.2 pre-flight quotes the list verbatim, so a refusal says where it
	// looked and not only that the key never arrived.
	c.consulted = append(config.DescribeEnvSources(workspace, scoped), "the invoking shell's environment")
	for _, k := range userEnv.Keys() {
		v, _ := userEnv.Get(k)
		if s, ok := v.(string); ok {
			vars = append(vars, agentenv.Var{Key: k, Value: s})
		}
	}

	// (3) the profile's provider vars, composed by the agent's OWN pack: the env-derive
	// producer its derive.lua registers, run by packload.AgentEnv — the ONE runner the
	// jail notch's channel reduces through too (OQ-CS8) — so the two notches cannot
	// disagree about what a resolved profile delivers. A credential resolves through
	// what this launch actually carries: the hydrated env_sources above, then the
	// environment this process inherited.
	//
	// KNOWN GAP, left standing deliberately: loadedHostPacks drops a pack fetched from a
	// git remote this launch (packForCheckDeps cannot resolve it offline), so an agent
	// pack that lives in git contributes its env derive to the JAIL notch and nothing to
	// this one. The gap predates the runner — the old agentenv.Resolve read the
	// composed table, which the same dropped pack's provider facts were equally absent
	// from — and closing it means teaching the host notch to resolve git packs, which is
	// its own decision, not a side effect of this flip.
	lookup := func(name string) (string, bool) {
		if v, ok := userEnv.Get(name); ok {
			if s, isStr := v.(string); isStr && s != "" {
				return s, true
			}
		}
		return os.LookupEnv(name)
	}
	providers, err := composedHostProviders(cfg, packs)
	if err != nil {
		c.err = err
		return c
	}
	providerVars, err := packload.AgentEnv(packs, providers, agentTable,
		agent, profileName, lookup, packload.WithResolvedProfiles(resolvedProfiles))
	if err != nil {
		c.err = err
		return c
	}
	vars = append(vars, providerVars...)
	c.providers = providers

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

// composedHostProviders is the host notch's ONE provider composition — the host spelling
// of the jail notch's composedProviders (internal/cli/run/assemble.go): the user's
// `providers` config entries with every selected pack's shipped `kind: "provider"` facts
// composed under them, per field. Composed ONCE and its result handed to BOTH of this
// launch's consumers — the provider env derive in composeHostVars and the §6.2 pre-flight's
// c.providers — because packload/providers.go states the composition happens exactly once
// per launch, and two compositions would be two chances for the check and the exec to
// disagree about what the launch carries.
func composedHostProviders(cfg *jsonx.OrderedMap, packs []*packload.Pack) (*jsonx.OrderedMap, error) {
	var user *jsonx.OrderedMap
	if v, ok := cfg.Get("providers"); ok {
		user, _ = v.(*jsonx.OrderedMap)
	}
	return packload.ComposeProviders(user, packs)
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

// loadedHostPacks resolves the selected packs for a host launch. A pack that cannot be
// resolved right now (an offline git remote) contributes nothing rather than failing the
// launch: the user asked to run an agent, not to reconcile their pack set.
func loadedHostPacks() ([]*packload.Pack, error) {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil, err
	}
	var packs []*packload.Pack
	for _, e := range entries {
		if p := packForCheckDeps(e); p != nil {
			packs = append(packs, p)
		}
	}
	return packs, nil
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
	for i := 0; i < len(args); i++ {
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
		agent = "claude"
	}

	// Only what yolo ADDS is printed, never the whole inherited environment: `yolo host
	// env` is meant to be eval'd, and echoing os.Environ() back into the shell would be
	// both enormous and a way to leak an unrelated secret into a log.
	added, err := hostEnvDelta(agent, profile, func(msg string) {
		fmt.Fprintf(errw, "Warning: %s\n", msg)
	})
	if err != nil {
		fmt.Fprintf(errw, "yolo host env: %v\n", err)
		return 1
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

// hostEnvDelta returns just the variables yolo would add or remove, in composition order.
// The composition's own refusal travels with it — `yolo host env` is an observe verb and
// has to say why it has no environment to show, but it says it as an error rather than
// printing a refusal an eval'ing shell would swallow.
func hostEnvDelta(agent, profile string, warn func(string)) ([]agentenv.Var, error) {
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}
	return hostEnvVars(config.UserScopeConfigOrEmpty(), workspace, agent, profile, warn)
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
		want := verb == "enable"
		if err := setHostWrappers(want); err != nil {
			fmt.Fprintf(errw, "yolo host wrappers %s: %v\n", verb, err)
			return 1
		}
		pr.Printf("[green]host_wrappers = %v[/green] in %s", want, paths.UserConfigPath())
		if want {
			pr.Printf("Run [bold]yolo host apply --assert[/bold] to generate the wrappers.")
		} else {
			pr.Printf("Run [bold]yolo host apply --assert[/bold] to remove them.")
		}
		return 0
	default:
		fmt.Fprintf(errw, "yolo host wrappers: unknown verb %q (want status, enable or disable)\n", verb)
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
