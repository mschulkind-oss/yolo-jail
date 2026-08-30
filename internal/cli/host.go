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
  --agent <name>  Compose for this agent (default: every configured one).

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

	// Resolve the target while ignoring yolo's OWN generated directories. This is the
	// recursion guard: <wrap dir>/claude is `exec yolo host -- claude`, so an ordinary
	// lookup would find the wrapper again and fork-bomb.
	target, err := hostwrap.LookPathSkipping(os.Getenv("PATH"), cmd[0], yoloManagedDirs())
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
//  3. the resolved profile's vars — the non-secret flags a provider implies, composed by
//     internal/agentenv, which is the same function the jail's podman argv is built from.
//  4. removals — a null in env_sources, i.e. `unset AWS_PROFILE`. Last, so a removal
//     beats an assignment from any earlier step.
func composeHostEnv(bin, profile string, warn func(string)) ([]string, string, error) {
	agent := filepath.Base(bin)
	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		return nil, agent, fmt.Errorf("loading config: %w", err)
	}
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}

	env := os.Environ()

	// (2) the secret channel.
	userEnv := config.ResolveEnvSources(workspace, cfg, warn)
	var vars []agentenv.Var
	for _, k := range userEnv.Keys() {
		v, _ := userEnv.Get(k)
		if s, ok := v.(string); ok {
			vars = append(vars, agentenv.Var{Key: k, Value: s})
		}
	}

	// (3) the profile's own vars.
	vars = append(vars, agentenv.Resolve(cfg, agent, effectiveHostProfiles(cfg, agent, profile))...)

	// (4) removals last.
	for _, k := range config.EnvSourceRemovals(cfg) {
		vars = append(vars, agentenv.Var{Key: k, Unset: true})
	}

	return agentenv.Apply(env, vars), agent, nil
}

// effectiveHostProfiles returns the agent_profiles map with a `-p` override applied to
// the agent being launched, mirroring what `yolo run -p` does for a jail so the two
// notches agree about what a profile selects.
func effectiveHostProfiles(cfg *jsonx.OrderedMap, agent, profile string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if v, ok := cfg.Get("agent_profiles"); ok {
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
	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}
	var vars []agentenv.Var
	userEnv := config.ResolveEnvSources(workspace, cfg, warn)
	for _, k := range userEnv.Keys() {
		v, _ := userEnv.Get(k)
		if s, ok := v.(string); ok {
			vars = append(vars, agentenv.Var{Key: k, Value: s})
		}
	}
	vars = append(vars, agentenv.Resolve(cfg, agent, effectiveHostProfiles(cfg, agent, profile))...)
	for _, k := range config.EnvSourceRemovals(cfg) {
		vars = append(vars, agentenv.Var{Key: k, Unset: true})
	}
	return vars, nil
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
