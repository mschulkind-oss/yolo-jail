// init.go implements `yolo init` and `yolo init-user-config` — the config
// scaffolders. The workspace template writes yolo-jail.jsonc (with a
// --mount-driven mounts block), appends .yolo/ to .gitignore, and prints an
// agent briefing; init-user-config writes the user-level defaults.
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
)

//go:embed template_head.txt
var templateHead string

//go:embed template_tail.txt
var templateTail string

//go:embed userconfig.jsonc
var userConfigContent string

//go:embed briefing.txt
var briefingContent string

// mountsBlock builds the mounts section of the workspace template. Mirrors
// init()'s branch: with --mount entries, a real `mounts` array (each entry via
// json.dumps == jsonx compact string, trailing comma trimmed on the last);
// without, the commented-out placeholder.
func mountsBlock(mounts []string) string {
	if len(mounts) == 0 {
		return "  // Extra host paths to mount read-only into the jail for context.\n" +
			"  // Each entry is a host path (mounted at /ctx/<basename>) or \"host:container\".\n" +
			"  // Pass --mount/-m on `yolo init` to populate this automatically, e.g.\n" +
			"  //   yolo init -m ~/code/shared-lib -m ~/notes\n" +
			"  // \"mounts\": [\n" +
			"  //   \"~/code/other-repo\",\n" +
			"  //   \"~/code/shared-lib:/ctx/shared-lib\"\n" +
			"  // ]\n"
	}
	var b strings.Builder
	b.WriteString("  // Extra host paths to mount read-only into the jail at /ctx/.\n")
	b.WriteString("  // Each entry is a host path (mounted at /ctx/<basename>) or \"host:container\".\n")
	b.WriteString("  \"mounts\": [\n")
	for _, m := range mounts {
		// json.dumps(m) — a plain string, so jsonx compact of the string.
		enc, _ := jsonx.DumpsCompact(m)
		b.WriteString("    " + enc + ",\n")
	}
	b.WriteString("  ],\n")
	// Trim the trailing comma on the last list entry.
	return strings.Replace(b.String(), ",\n  ],", "\n  ],", 1)
}

// Init runs `yolo init` in cwd. Writes yolo-jail.jsonc (unless it exists),
// appends .yolo/ to .gitignore, and prints the briefing to out (color per the
// caller). Returns the exit code (0).
func Init(cwd string, mounts []string, out io.Writer, color bool) int {
	configPath := filepath.Join(cwd, "yolo-jail.jsonc")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintln(out, "yolo-jail.jsonc already exists.")
		printBriefing(out, configPath, color)
		return 0
	}

	content := templateHead + mountsBlock(mounts) + templateTail
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		fmt.Fprintf(out, "Error writing config: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "Created yolo-jail.jsonc")

	// Append .yolo/ to .gitignore (create if absent).
	gitignore := filepath.Join(cwd, ".gitignore")
	if data, err := os.ReadFile(gitignore); err == nil {
		if !strings.Contains(string(data), ".yolo/") {
			appendFile(gitignore, "\n# YOLO Jail workspace state\n.yolo/\n")
		}
	} else {
		_ = os.WriteFile(gitignore, []byte("# YOLO Jail workspace state\n.yolo/\n"), 0o644)
	}

	printBriefing(out, configPath, color)
	return 0
}

// InitUserConfig runs `yolo init-user-config`. Writes the user-level defaults at
// USER_CONFIG_PATH unless it exists.
func InitUserConfig(out io.Writer) int {
	p := paths.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fmt.Fprintf(out, "Error creating config dir: %v\n", err)
		return 1
	}
	if _, err := os.Stat(p); err == nil {
		fmt.Fprintf(out, "%s already exists.\n", p)
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
		fmt.Fprintf(out, "Error writing config: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Created %s\n", p)
	// The template selects no pack, so this is always the case for a file just written.
	printUserConfigNextSteps(out)
	return 0
}

// printUserConfigNextSteps ends `yolo init-user-config` with what to do next, for a user
// config that selects no pack: as written, the next jail has no coding agent, and the
// command used to end at `Created <path>` without saying so
// (docs/reference/happy-path-principle.md, rule 2). Choosing the agent is the user's call,
// so the step is the edit (rung 3), not a write made for them. The example agent is the one
// the empty-packs notice names (config.NoPacksGuidance), so a new user hears one answer.
//
// It follows the line naming the file, so "that file" is the path the user just read.
func printUserConfigNextSteps(out io.Writer) {
	io.WriteString(out, "It selects no packs yet, so a jail has no coding agent. Next:\n"+
		"  1. Choose your agent: add this line to that file, right after the opening {\n"+
		"       \"packs\": [\"claude\"],\n"+
		"     (`yolo pack --help` says what other packs deliver)\n"+
		"  2. Check the edit:  yolo check\n"+
		"  3. From your project's directory, launch it:  yolo -- claude\n")
}

// printBriefing renders the post-init agent briefing with {config_path}
// interpolated. Rich markup → ANSI when color, stripped otherwise (info-parity).
func printBriefing(out io.Writer, configPath string, color bool) {
	text := strings.ReplaceAll(briefingContent, "{config_path}", configPath)
	io.WriteString(out, renderMarkup(text, color)+"\n")
}

func appendFile(path, s string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(s)
}
