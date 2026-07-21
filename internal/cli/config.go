// config.go implements `yolo config <subcommand>` — the runnable window into
// the generated-config composition pipeline (docs/plans/agent-settings-composition.md
// §6). Today it provides `yolo config render`, which runs the SAME engine the
// entrypoint boot render calls (internal/agentcfg.Compose) and prints what it
// would write, touching no live config. It runs host-side (the edit-before-launch
// loop) and in-jail (the operating agent's "what is my config, and why?" aid),
// and it is the CLI surface that makes the Lua transform mechanism discoverable
// and operable by interrogation — the self-documenting-CLI gap
// (docs/design/self-documenting-cli.md) this closes for the composed surfaces.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// configUsage is the `yolo config` help, printed on `--help`, `help`, or misuse.
const configUsage = `Usage: yolo config <subcommand>

Inspect the generated-config composition pipeline (the layered regeneration of
agent settings/MCP/LSP/mise config, with optional Lua transforms). See
'yolo config-ref' and docs/plans/agent-settings-composition.md.

Subcommands:
  render <agent> [flags]   Run the composition pipeline and print what it would
                           write, for every surface of <agent> (no writes).

render flags:
  --surface <name>   Render only the named surface (e.g. settings).
  --explain          Print, per config key, which layer/hook set it
                     (defaults<host<workspace<overlay<transform<managed),
                     instead of the rendered file.
  --help, -h         Show this help.

Config transforms live in yolo-jail.config.lua (repo root) and
~/.config/yolo-jail/config.lua (user); both are auto-loaded, user first.`

// runConfig dispatches `yolo config <subcommand>`. Registered in dispatch.go.
// Per the dispatch convention (see runBroker), args[0] is the command name
// ("config") itself; the payload is args[1:].
func runConfig(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return configRunW(rest, os.Stdout, os.Stderr)
}

// configRunW is the testable body: args is everything after `config`.
func configRunW(args []string, out, errw io.Writer) int {
	if len(args) == 0 || isHelpToken(args[0]) {
		// Bare `yolo config` and `yolo config --help` print help to stdout
		// (exit 0); this is a self-documenting request, not an error.
		io.WriteString(out, configUsage+"\n")
		return 0
	}
	switch args[0] {
	case "render":
		return configRender(args[1:], out, errw, colorForWriter(out))
	default:
		fmt.Fprintf(errw, "yolo config: unknown subcommand %q\n\n%s\n", args[0], configUsage)
		return 2
	}
}

// isHelpToken reports whether tok requests help.
func isHelpToken(tok string) bool {
	return tok == "--help" || tok == "-h" || tok == "help"
}

// colorForWriter reports whether to emit ANSI: only when out is os.Stdout AND a
// real terminal. A bytes.Buffer (tests) or a pipe/redirect yields false, so the
// rendered/explain output stays plain and byte-stable off a TTY.
func colorForWriter(out io.Writer) bool {
	f, ok := out.(*os.File)
	return ok && isTTY(f)
}

// configRender implements `yolo config render <agent> [--surface s] [--explain]`.
func configRender(args []string, out, errw io.Writer, color bool) int {
	var agent, surface string
	var explain bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return 0
		case a == "--explain":
			explain = true
		case a == "--surface":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo config render: --surface needs a value\n")
				return 2
			}
			i++
			surface = args[i]
		case strings.HasPrefix(a, "--surface="):
			surface = strings.TrimPrefix(a, "--surface=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo config render: unknown flag %q\n\n%s\n", a, configUsage)
			return 2
		default:
			if agent != "" {
				fmt.Fprintf(errw, "yolo config render: unexpected argument %q (agent already %q)\n", a, agent)
				return 2
			}
			agent = a
		}
	}
	if agent == "" {
		fmt.Fprintf(errw, "yolo config render: needs an agent (e.g. 'yolo config render pi')\n\n%s\n", configUsage)
		return 2
	}

	m := agentcfg.BuiltinManifest()
	surfaces := m.ForAgent(agent)
	if len(surfaces) == 0 {
		known := map[string]bool{}
		var names []string
		for _, s := range m.Surfaces() {
			if !known[s.Agent] {
				known[s.Agent] = true
				names = append(names, s.Agent)
			}
		}
		fmt.Fprintf(errw, "yolo config render: no surfaces for agent %q (known: %s)\n", agent, strings.Join(names, ", "))
		return 1
	}

	// Load the concatenated transform script once (user then workspace, §3.4).
	script := loadTransformScript()
	var vm luahook.LuaVM
	if script != "" {
		vm = &luahook.GopherLuaVM{}
	}

	rc := 0
	for _, s := range surfaces {
		if surface != "" && s.Name != surface {
			continue
		}
		if err := renderSurface(s, script, vm, explain, out, color); err != nil {
			fmt.Fprintf(errw, "yolo config render: %s/%s: %v\n", s.Agent, s.Name, err)
			rc = 1
		}
	}
	return rc
}

// renderSurface composes one surface and writes either the rendered file or the
// --explain provenance to out.
func renderSurface(s manifest.Surface, script string, vm luahook.LuaVM, explain bool, out io.Writer, color bool) error {
	hostBytes, _ := os.ReadFile(expandHome(s.Path)) // absent host file => empty layer

	res, err := agentcfg.Compose(agentcfg.Inputs{
		Surface:   s,
		HostBytes: hostBytes,
		Script:    script,
		VM:        vm,
	})
	if err != nil {
		return err
	}

	pr := richtext.Printer{W: out, Color: color}
	header := fmt.Sprintf("[bold]# %s/%s → %s[/bold]", s.Agent, s.Name, s.Path)
	if explain {
		pr.Printf("%s [dim](layer that set each key)[/dim]", header)
		// ProvenanceLines is sorted "key\tlayer"; color the key cyan and the
		// layer by its distinct hue.
		for _, line := range res.ProvenanceLines() {
			key, layer, _ := strings.Cut(line, "\t")
			pr.Printf("  [cyan]%s[/cyan]\t%s", key, colorLayer(layer))
		}
		if len(res.Excluded) > 0 {
			pr.Printf("  [dim](staged files excluded: %s)[/dim]", strings.Join(res.Excluded, ", "))
		}
		return nil
	}
	pr.Print(header)
	fmt.Fprintf(out, "%s\n", res.Encoded)
	return nil
}

// colorLayer wraps a provenance layer name in its distinct hue so --explain
// reads like syntax-highlighted provenance: one hue per composition layer
// (docs/plans/cli-visual-polish.md). The value may be "transform (dropped)";
// the hue keys on the leading word.
func colorLayer(layer string) string {
	tag := map[string]string{
		"defaults":  "dim",     // lowest precedence — muted
		"host":      "blue",    // the host mirror
		"workspace": "cyan",    // workspace layer
		"overlay":   "magenta", // capture-diff overlay (in-jail edits)
		"transform": "yellow",  // the Lua hook touched it
		"managed":   "green",   // yolo-enforced, wins
	}
	word := layer
	if i := strings.IndexByte(word, ' '); i >= 0 {
		word = word[:i]
	}
	if t, ok := tag[word]; ok {
		return "[" + t + "]" + layer + "[/" + t + "]"
	}
	return layer
}

// loadTransformScript concatenates the user then workspace config.lua (§3.4),
// user first so the workspace transform runs last. A missing file contributes
// nothing; neither present means the identity transform.
func loadTransformScript() string {
	var b strings.Builder
	// User: ~/.config/yolo-jail/config.lua (beside the user config.jsonc).
	userLua := filepath.Join(filepath.Dir(paths.UserConfigPath()), "config.lua")
	if data, err := os.ReadFile(userLua); err == nil {
		b.Write(data)
		b.WriteByte('\n')
	}
	// Workspace: yolo-jail.config.lua at the repo root (cwd for the CLI).
	if data, err := os.ReadFile("yolo-jail.config.lua"); err == nil {
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// expandHome expands a leading "~/" in a manifest path to the resolved home dir.
func expandHome(p string) string {
	if p == "~" {
		return paths.Home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(paths.Home(), p[2:])
	}
	return p
}
