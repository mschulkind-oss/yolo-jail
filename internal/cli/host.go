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

Both always apply. A config file cannot deliver a secret — api_key_env carries a
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
                  selects which pack_profiles entry applies.

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

	env, agent, err := composeHostEnv(cmd[0], flags.profile, func(msg string) {
		fmt.Fprintf(errw, "Warning: %s\n", msg)
	})
	if err != nil {
		fmt.Fprintf(errw, "yolo host: %v\n", err)
		return 1
	}

	target, err := resolveHostTarget(os.Getenv("PATH"), cmd[0])
	if err != nil {
		fmt.Fprintf(errw, "yolo host: %v\n", err)
		return 127
	}
	_ = agent

	// argv[0] stays the name the user typed, not the resolved path: agents branch on it
	// (usage text, `$0`), and handing them an absolute path changes what they print.
	argv := append([]string{cmd[0]}, cmd[1:]...)
	if err := syscall.Exec(target, argv, env); err != nil {
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

// composeHostEnv builds the environment for one agent launch, and returns it alongside
// the agent name it resolved.
//
// The order is the one docs/design/host-agent-environment.md §6.1 step 3 specifies, and
// each step is there for a reason the previous one cannot cover:
//
//  1. os.Environ() — the user's own shell, which the agent should otherwise inherit whole.
//  2. env_sources — the SECRET channel. This is the step that gives "env_sources
//     hydrates your credentials" something to hydrate INTO on a host.
//  3. the resolved profile's vars — the variant's own env plus the flags a provider
//     implies, composed by internal/agentenv, which is the same function the jail's
//     podman argv is built from.
//  4. removals — a null in env_sources or in a variant's env, i.e. `unset AWS_PROFILE`.
//     Last, so a removal beats an assignment from any earlier step.
func composeHostEnv(bin, profile string, warn func(string)) ([]string, string, error) {
	agent := filepath.Base(bin)
	cfg := config.UserScopeConfigOrEmpty()
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}

	vars := hostEnvVars(cfg, workspace, agent, profile, warn)
	return agentenv.Apply(os.Environ(), vars), agent, nil
}

// hostEnvVars is the composition itself, without the inherited environment — shared by
// the exec half and by `yolo host env`, so the two can never disagree about what a launch
// would carry.
//
// The sources are docs/design/host-agent-environment.md §5.4's, in order:
//
//  1. a pack's static `kind: "env"` contributions, then (1b) the env of the variant each
//     pack has selected — the same literals, gated on the launch's profile table, folded
//     after the pack's own so the variant wins (OQ-8);
//  2. env_sources — the SECRET channel, and the step that gives "env_sources hydrates
//     your credentials" something to hydrate INTO on a host;
//  3. the resolved profile's provider vars — the env shape of the provider the variant
//     names, composed by internal/agentenv, the same function the jail's podman argv is
//     built from.
//
// Removals come last so an `unset` beats an assignment from any earlier source, including
// one inherited from the invoking shell.
// The config it reads is USER SCOPE ONLY (config.UserScopeConfig) — never the merged
// config. This process runs on the host, outside every sandbox, and a workspace
// yolo-jail.jsonc is agent-editable; composing a host process's environment from it would
// hand a cloned repo LD_PRELOAD on the user's machine. See UserScopeConfig for the whole
// argument.
func hostEnvVars(cfg *jsonx.OrderedMap, workspace, agent, profile string, warn func(string)) []agentenv.Var {
	var vars []agentenv.Var

	// The selected packs, read once for both the env they declare and the provider they
	// ship. The config here is USER SCOPE ONLY (the boundary this function's doc records),
	// so the composed provider table below is user entries over pack facts and never a
	// workspace's.
	packs, packErr := loadedHostPacks()
	// The variant this launch selects, resolved once: it gates (1b) and feeds (3), and
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
	// only the variant selected at THIS agent's own CLI name may contribute env to it.
	agentTable := map[string]string{}
	if profileName != "" {
		agentTable[agent] = profileName
	}

	// (1) pack-declared env. Sorted, because a map has no order and an argv (or an
	// `export` script) that reshuffles between runs is a diff nobody can read.
	if packErr == nil {
		packEnv := packload.EnvVars(packs)
		keys := make([]string, 0, len(packEnv))
		for k := range packEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			vars = append(vars, agentenv.Var{Key: k, Value: packEnv[k]})
		}
	}

	// (1b) the SELECTED VARIANTS' own env (OQ-7/OQ-8), folded after the pack's static map
	// because a variant is the more specific intent. Assignments land here; a null's UNSET
	// is held for (4), because on the host a removal is only a removal if it comes after
	// every assignment — including one an env_sources file below would make.
	var profileUnsets []agentenv.Var
	if packErr == nil {
		for _, p := range packs {
			for _, prof := range p.ActiveProfiles(agentTable) {
				keys := make([]string, 0, len(prof.Env))
				for k := range prof.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					v := prof.Env[k]
					if v.Unset() {
						profileUnsets = append(profileUnsets, agentenv.Var{Key: k, Unset: true})
						continue
					}
					vars = append(vars, agentenv.Var{Key: k, Value: v.Value})
				}
			}
		}
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
	for _, k := range userEnv.Keys() {
		v, _ := userEnv.Get(k)
		if s, ok := v.(string); ok {
			vars = append(vars, agentenv.Var{Key: k, Value: s})
		}
	}

	// (3) the profile's provider vars: the env shape of the provider the variant names,
	// for the protocol this agent speaks (OQ-14). internal/agentenv is the ONE
	// composition — the jail's podman argv is built from the same call — so the two
	// notches cannot disagree about what a resolved profile delivers. A {key}
	// placeholder resolves through what this launch actually carries: the hydrated
	// env_sources above, then the environment this process inherited.
	var userProviders *jsonx.OrderedMap
	if v, ok := cfg.Get("providers"); ok {
		userProviders, _ = v.(*jsonx.OrderedMap)
	}
	lookup := func(name string) (string, bool) {
		if v, ok := userEnv.Get(name); ok {
			if s, isStr := v.(string); isStr && s != "" {
				return s, true
			}
		}
		return os.LookupEnv(name)
	}
	vars = append(vars, agentenv.Resolve(
		packload.ComposeProviders(userProviders, packs),
		agent, profileName, packload.ProviderFor(packs, agent, profileName), lookup)...)

	// (4) removals last, so an unset beats every assignment above no matter which source
	// made it — the env_sources nulls from the same pass as (2) (the same scoped config,
	// so an inline null's cancellation by a later dotenv cannot disagree with the
	// assignments), then a variant's own nulls from (1b).
	for _, k := range removals {
		vars = append(vars, agentenv.Var{Key: k, Unset: true})
	}
	vars = append(vars, profileUnsets...)
	return vars
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

// effectiveHostProfiles returns the pack_profiles map with a `-p` override applied to
// the agent being launched, mirroring what `yolo run -p` does for a jail so the two
// notches agree about what a profile selects.
func effectiveHostProfiles(cfg *jsonx.OrderedMap, agent, profile string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if v, ok := cfg.Get("pack_profiles"); ok {
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
		// construction (pack_profiles maps ONE profile per agent), so there is no single
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
func hostEnvDelta(agent, profile string, warn func(string)) ([]agentenv.Var, error) {
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}
	return hostEnvVars(config.UserScopeConfigOrEmpty(), workspace, agent, profile, warn), nil
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
