package cli

// pack.go implements `yolo pack` — the authoring and inspection loop for agent
// config packs (C4).
//
// Four verbs, chosen because they are what an author and a consumer each need on
// day one and neither can get from `yolo check` alone:
//
//	init     scaffold a valid pack skeleton, so nobody has to reverse-engineer the
//	         layout from docs
//	lint     validate a pack DIRECTORY (not the config), so an author finds the
//	         exec-bit or escaping-symlink refusal before a consumer's jail does
//	ls       list the configured packs and what each would stage
//	explain  show, for one pack, exactly which files stage and which its filters
//	         dropped — the answer to "why isn't my skill showing up?"
//
// `lint` and `explain` deliberately run the REAL executor (internal/packstage)
// rather than reimplementing its rules. A linter that disagrees with the stager is
// worse than no linter.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

const packUsage = `yolo pack — author and inspect agent config packs

  yolo pack init [dir]        scaffold a pack skeleton (default: current dir)
  yolo pack lint [dir]        validate a pack directory the way staging will
  yolo pack ls                list configured packs and what each stages
  yolo pack explain <name>    show which files a pack stages, and what it dropped

Packs are configured in ~/.config/yolo-jail/config.jsonc under "packs" (USER scope
only — a workspace config cannot name one). See ` + "`yolo config-ref`" + `.`

// runPack is the registry entry point. Like every handler, args INCLUDES the
// subcommand name itself ("pack"), so the payload is args[1:] — the same shape
// runConfig uses.
func runPack(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return packMain(rest, os.Stdout, os.Stderr, isTTYStdout())
}

func packMain(args []string, out, errw io.Writer, color bool) int {
	if len(args) == 0 {
		fmt.Fprintln(out, packUsage)
		return 0
	}
	switch args[0] {
	case "init":
		return packInit(args[1:], out, errw)
	case "lint":
		return packLint(args[1:], out, errw, color)
	case "ls":
		return packLs(out, errw, color)
	case "explain":
		return packExplain(args[1:], out, errw, color)
	case "-h", "--help", "help":
		fmt.Fprintln(out, packUsage)
		return 0
	default:
		fmt.Fprintf(errw, "yolo pack: unknown verb %q\n\n%s\n", args[0], packUsage)
		return 1
	}
}

// packInit scaffolds a minimal, VALID pack: a skills dir with one real skill and an
// AGENTS.md. It writes a working example rather than empty placeholders, because the
// first question an author has is "what shape does this need to be", and an empty
// dir answers nothing.
func packInit(args []string, out, errw io.Writer) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack init: %v\n", err)
		return 1
	}
	name := filepath.Base(abs)

	type packFile struct{ rel, content string }
	files := []packFile{
		{"AGENTS.md", "# " + name + "\n\n" +
			"Prose here is appended to every selected agent's briefing, under a\n" +
			"`<!-- from pack: " + name + " -->` header so its origin stays traceable.\n" +
			"Write instructions an agent should follow in every project using this pack.\n"},
		{filepath.Join("skills", "example", "SKILL.md"), "---\n" +
			"name: example\n" +
			"description: Replace this with when the agent should read this skill. This line is what an agent sees when deciding whether to open it, so make it specific.\n" +
			"---\n\n# Example skill\n\n" +
			"Skills land in each agent's skills dir. A pack skill overrides a yolo\n" +
			"built-in of the same name, but never the user's own local skill.\n"},
		{"README.md", "# " + name + "\n\n" +
			"A yolo-jail agent config pack. Consume it by adding to\n" +
			"`~/.config/yolo-jail/config.jsonc`:\n\n" +
			"```jsonc\n\"packs\": [\"file://" + abs + "\"]\n```\n\n" +
			"Validate changes with `yolo pack lint`.\n"},
	}
	// A SLICE, not a map: `init` output must be deterministic, and Go map iteration
	// is not.
	for _, f := range files {
		rel, content := f.rel, f.content
		p := filepath.Join(abs, rel)
		if _, err := os.Stat(p); err == nil {
			// Never clobber: init on an existing pack should be safe to re-run.
			fmt.Fprintf(out, "  skip %s (exists)\n", rel)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintf(errw, "yolo pack init: %v\n", err)
			return 1
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			fmt.Fprintf(errw, "yolo pack init: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "  create %s\n", rel)
	}
	fmt.Fprintf(out, "\nPack scaffolded at %s\nNext: yolo pack lint %s\n", abs, dir)
	return 0
}

// packLint validates a pack DIRECTORY by staging it into a throwaway dir with the
// real executor, so the linter cannot disagree with the stager.
func packLint(args []string, out, errw io.Writer, color bool) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	pr := richtext.Printer{W: out, Color: color}

	tmp, err := os.MkdirTemp("", "yolo-pack-lint-")
	if err != nil {
		fmt.Fprintf(errw, "yolo pack lint: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	res, err := packstage.Stage(packstage.Spec{Root: dir, Dest: tmp})
	if err != nil {
		// The staging rules ARE the lint rules: exec bit, escaping symlink, missing
		// root. Reporting the executor's own message keeps the two from drifting.
		fmt.Fprintf(errw, "yolo pack lint: %v\n", err)
		return 1
	}

	var problems []string
	if len(res.Staged) == 0 {
		problems = append(problems, "pack contains no stageable files")
	}
	hasSkills, hasBriefing := false, false
	for _, s := range res.Staged {
		if strings.HasPrefix(s, "skills/") {
			hasSkills = true
		}
		if s == "AGENTS.md" || s == "CLAUDE.md" {
			hasBriefing = true
		}
	}
	if !hasSkills && !hasBriefing {
		problems = append(problems, "pack has neither a skills/ dir nor an AGENTS.md — "+
			"it would stage files nothing reads")
	}
	// A skill dir without SKILL.md is invisible to every agent, which is the single
	// most likely authoring mistake and produces no error anywhere else.
	for _, d := range skillDirsMissingManifest(res.Staged) {
		problems = append(problems, "skills/"+d+" has no SKILL.md — agents will not see it")
	}

	if len(problems) > 0 {
		for _, p := range problems {
			pr.Printf("[red]✗[/red] %s", p)
		}
		return 1
	}
	pr.Printf("[green]✓[/green] pack ok — %d file(s) stage", len(res.Staged))
	return 0
}

// skillDirsMissingManifest returns the skills/<dir> names that staged files but no
// SKILL.md. Sorted, deduped.
func skillDirsMissingManifest(staged []string) []string {
	hasManifest := map[string]bool{}
	seen := map[string]bool{}
	var order []string
	for _, s := range staged {
		rest, ok := strings.CutPrefix(s, "skills/")
		if !ok {
			continue
		}
		dir, _, nested := strings.Cut(rest, "/")
		if !nested {
			continue // a loose file directly under skills/
		}
		if !seen[dir] {
			seen[dir] = true
			order = append(order, dir)
		}
		if strings.HasSuffix(s, "/SKILL.md") && strings.Count(rest, "/") == 1 {
			hasManifest[dir] = true
		}
	}
	var out []string
	for _, d := range order {
		if !hasManifest[d] {
			out = append(out, d)
		}
	}
	return out
}

// packLs lists the configured packs. Reads USER scope only, like LoadPacks.
func packLs(out, errw io.Writer, color bool) int {
	entries, err := config.LoadPacks(func(msg string) {
		fmt.Fprintf(errw, "Warning: %s\n", msg)
	})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack ls: %v\n", err)
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	if len(entries) == 0 {
		pr.Printf("[dim]No packs configured. Add them under \"packs\" in " +
			"~/.config/yolo-jail/config.jsonc (user scope only).[/dim]")
		return 0
	}
	pr.Printf("[bold]%-20s %-8s %s[/bold]", "NAME", "KIND", "SOURCE")
	for _, e := range entries {
		kind := "git"
		if e.IsLocal() {
			kind = "local"
		}
		pr.Printf("%-20s %-8s %s", e.Name, kind, e.Source)
		if len(e.Agents) > 0 {
			pr.Printf("  [dim]agents: %s[/dim]", strings.Join(e.Agents, ", "))
		}
		if len(e.Only) > 0 || len(e.Exclude) > 0 {
			pr.Printf("  [dim]only: %v  exclude: %v[/dim]", e.Only, e.Exclude)
		}
	}
	return 0
}

// packExplain answers "why isn't my skill showing up?" for ONE pack by staging it
// and reporting both what landed and what the filters dropped.
func packExplain(args []string, out, errw io.Writer, color bool) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, "yolo pack explain: need a pack name (see `yolo pack ls`)")
		return 1
	}
	name := args[0]
	entries, err := config.LoadPacks(nil)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack explain: %v\n", err)
		return 1
	}
	var entry *config.PackEntry
	for i := range entries {
		if entries[i].Name == name {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		fmt.Fprintf(errw, "yolo pack explain: no configured pack named %q "+
			"(see `yolo pack ls`)\n", name)
		return 1
	}
	if !entry.IsLocal() {
		fmt.Fprintf(errw, "yolo pack explain: %s is a git source, which this build "+
			"cannot fetch yet\n", name)
		return 1
	}

	tmp, err := os.MkdirTemp("", "yolo-pack-explain-")
	if err != nil {
		fmt.Fprintf(errw, "yolo pack explain: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	res, err := packstage.Stage(packstage.Spec{
		Root:      strings.TrimPrefix(entry.Source, "file://"),
		Dest:      tmp,
		Only:      entry.Only,
		Exclude:   entry.Exclude,
		AllowExec: entry.AllowExec,
	})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack explain: %v\n", err)
		return 1
	}

	pr := richtext.Printer{W: out, Color: color}
	pr.Printf("[bold]%s[/bold] → %s", entry.Name, entry.Source)
	pr.Printf("[green]stages %d file(s)[/green]", len(res.Staged))
	for _, s := range res.Staged {
		pr.Printf("  [cyan]%s[/cyan]", s)
	}
	if len(res.Excluded) > 0 {
		// Reported, never silent: an unexpected entry here is the answer to
		// "why isn't my skill showing up?".
		pr.Printf("[yellow]filtered out %d file(s)[/yellow] [dim](only/exclude)[/dim]", len(res.Excluded))
		for _, s := range res.Excluded {
			pr.Printf("  [dim]%s[/dim]", s)
		}
	}
	return 0
}
