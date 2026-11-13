// init.go implements `yolo init` and `yolo init-user-config` — the config
// scaffolders. `init` writes a three-line pointer file, yolo-jail.toml, and prints an agent
// briefing; it scaffolds no defaults, because a project
// with no settings needs none. init-user-config writes the user-level defaults.
package cli

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

//go:embed userconfig.toml
var userConfigContent string

//go:embed briefing.txt
var briefingContent string

// workspacePointerHead is the whole of what `yolo init` writes: three comment lines and no key.
// A project with no file runs on defaults, so the file exists only to say where settings go
// (docs/reference/config/project-config.md#no-file-at-all, convention C5). It names the docs and the one command that
// adds a key, which is what a reader opening an empty config wants to know.
const workspacePointerHead = "# yolo-jail: this project needs no settings; with none, yolo runs on its defaults.\n" +
	"# What can go here: https://docs.yolo-jail.mschulkind.dev, or `yolo config-ref` for every key.\n" +
	"# Add a key with:  yolo config set <key> <value> --project\n"

// pointerFile is the pointer text, plus a `mounts` list when the user passed --mount: an
// explicit request is not default scaffolding, so it is written, as TOML.
func pointerFile(mounts []string) string {
	if len(mounts) == 0 {
		return workspacePointerHead
	}
	var b strings.Builder
	b.WriteString(workspacePointerHead)
	b.WriteString("\n# Extra host paths mounted read-only at /ctx/<basename>, or \"host:container\".\nmounts = [\n")
	for _, m := range mounts {
		enc, _ := jsonx.DumpsCompact(m)
		b.WriteString("  " + enc + ",\n")
	}
	b.WriteString("]\n")
	return b.String()
}

// existingWorkspaceConfig names the workspace config the loader would read, in either format:
// yolo-jail.toml first, then the .jsonc and .json it still reads. Empty when there is none.
func existingWorkspaceConfig(cwd string) string {
	for _, name := range []string{paths.WorkspaceConfigTOMLName, config.WorkspaceConfigName,
		strings.TrimSuffix(config.WorkspaceConfigName, "c")} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
			return name
		}
	}
	return ""
}

// Init runs `yolo init` in cwd. It writes the pointer file yolo-jail.toml (unless the workspace
// already has a config of either format, which it leaves byte-for-byte alone) and prints the
// briefing to out (color per the caller). Returns the exit code. It writes no git ignore file:
// .yolo/ keeps itself out of commits with its own .gitignore (paths.EnsureWorkspaceStateDir),
// and the user's .gitignore and .git/info/exclude are theirs (the owner, 2026-11-07).
func Init(cwd string, mounts []string, out io.Writer, color bool) int {
	if name := existingWorkspaceConfig(cwd); name != "" {
		fmt.Fprint(out, name, " ", richtext.Render("[yellow]already exists.[/yellow]\n", color))
		printBriefing(out, filepath.Join(cwd, name), color)
		return 0
	}
	configName := paths.WorkspaceConfigTOMLName
	configPath := filepath.Join(cwd, configName)
	if err := os.WriteFile(configPath, []byte(pointerFile(mounts)), 0o644); err != nil {
		printInitStatus(out, color, "bold red", fmt.Sprintf("Error writing config: %v", err))
		return 1
	}
	printInitStatus(out, color, "green", "Created "+configName)

	printBriefing(out, configPath, color)
	return 0
}

// InitUserConfig runs `yolo init-user-config`. Writes the user-level defaults at
// ~/.config/yolo-jail/config.toml unless a user config of either format exists. It writes
// TOML, the format every launch reads first, so a new user is not onboarded onto the legacy
// format the launch then offers to convert (LC-D12, docs/reference/config/rulings.md).
func InitUserConfig(out io.Writer, color bool) int {
	// With no user config, the display path is the TOML file a first write creates; with
	// one, in either format, it is that file, which is left alone: writing config.toml
	// beside a config.jsonc makes a pair the reader refuses.
	p := paths.UserConfigDisplayPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		printInitStatus(out, color, "bold red", fmt.Sprintf("Error creating config dir: %v", err))
		return 1
	}
	if existing := p; fileExists(existing) {
		fmt.Fprint(out, existing, " ", richtext.Render("[yellow]already exists.[/yellow]\n", color))
		// The likeliest reason to run this again is a jail with no agent, so an existing
		// file that selects no pack gets the same steps a new one does. One that cannot
		// be read says nothing more here: `yolo check` reports that, in its own words.
		problems := 0
		entries, err := config.LoadPacks(func(string) { problems++ })
		if err == nil && problems == 0 && !config.HasConfiguredPack(entries) {
			printUserConfigNextSteps(out)
		}
		return 0
	}
	if err := os.WriteFile(p, []byte(userConfigContent), 0o644); err != nil {
		printInitStatus(out, color, "bold red", fmt.Sprintf("Error writing config: %v", err))
		return 1
	}
	printInitStatus(out, color, "green", "Created "+p)
	// The template selects no pack, so this is always the case for a file just written.
	printUserConfigNextSteps(out)
	return 0
}

// printInitStatus styles only the scaffolder's status line. The message is
// never passed to the markup renderer: paths and filesystem errors stay literal
// without richtext.Escape's zero-width joiner, preserving their stripped bytes.
func printInitStatus(out io.Writer, color bool, style, message string) {
	fmt.Fprint(out, richtext.Render("["+style+"]", color), message, richtext.Render("[/"+style+"]\n", color))
}

// printUserConfigNextSteps ends `yolo init-user-config` with what to do next, for a user
// config that selects no pack: as written, the next jail has no coding agent, and the
// command used to end at `Created <path>` without saying so
// (docs/reference/happy-path-principle.md, rule 2). Choosing the agent is the user's call,
// so the step is the verb that makes the edit, not a write made for them. The example agent
// is the one the empty-packs notice names (config.NoPacksGuidance), so a new user hears one
// answer. It names `yolo pack add`, never a `packs` line to paste: the verb writes the user
// file that exists, in its own format, and appends to a `packs` list already there (LC-D12,
// docs/reference/config/rulings.md).
func printUserConfigNextSteps(out io.Writer) {
	io.WriteString(out, "It selects no packs yet, so a jail has no coding agent. Next:\n"+
		"  1. Choose your agent:  yolo pack add "+nextStepPack+"\n"+
		"     (`yolo pack --help` says what other packs deliver)\n"+
		"  2. Check the edit:  yolo check\n"+
		"  3. From your project's directory, launch it:  yolo -- "+nextStepPack+"\n")
}

// nextStepPack is the agent pack init-user-config's next steps name: the example the
// empty-packs notice gives (config.NoPacksGuidance).
const nextStepPack = "claude"

// printBriefing renders the post-init agent briefing with {config_path}
// interpolated. Rich markup → ANSI when color, stripped otherwise (info-parity).
func printBriefing(out io.Writer, configPath string, color bool) {
	parts := strings.Split(briefingContent, "{config_path}")
	for i, part := range parts {
		io.WriteString(out, renderMarkup(part, color))
		if i < len(parts)-1 {
			// The path is user-controlled text. Keep it outside the markup renderer so
			// brackets stay literal without richtext.Escape's zero-width joiner.
			io.WriteString(out, configPath)
		}
	}
	io.WriteString(out, "\n")
}
