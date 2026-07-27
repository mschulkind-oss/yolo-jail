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
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// packUsage is what `yolo pack --help` prints, and it is also the destination the
// empty-packs notice sends a brand-new user to (internal/cli/run.warnIfNoPacks,
// internal/cli/check.sectionPacks). That second audience is why it opens with WHAT A
// PACK IS, before the verb list: a user who has just been told "this jail has no coding
// agent" needs "here is the thing you add", not a command table for authoring.
// `yolo config-ref` remains the exhaustive key schema.
//
// It states plainly that an agent-installing pack DOES NOT EXIST YET. The temptation is
// to describe the destination as if it had arrived — the notice sends people here asking
// "how do I get my agent back", and "an AGENT pack" is the answer the design intends.
// But a pack today stages CONTENT (skills, prose) and nothing declares an agent: no
// PackEntry field, no knownPackKeys entry, and agentcfg.ManifestWith — the seam a pack
// would contribute a surface through — has no production caller. Promising it here
// would send a user to configure a pack, watch it silence the very warning that told
// them they had no agent, and still have no agent. Say what works and what does not.
const packUsage = `yolo pack — author and inspect agent config packs

A pack is a directory of agent CONFIG — skills and briefing prose (AGENTS.md) —
delivered into every jail you launch. Content a jail has beyond a bare shell arrives as
a pack: with no packs configured, a jail gets nothing but the built-ins.

What a pack delivers TODAY:
  • a SHARED pack of your own skills and house rules, applied in every project
  • per-project narrowing of that corpus, via only/exclude

NOT YET: a pack cannot install a coding agent. That is where this is going — an agent is
meant to arrive as a pack like anything else — but no pack can declare one yet, so a
jail launched today has no agent regardless of what you configure here.

  yolo pack init [dir]        scaffold a pack skeleton (default: current dir)
  yolo pack lint [dir]        validate a pack directory the way staging will
  yolo pack ls                list configured packs and what each stages
  yolo pack explain <name>    show which files a pack stages, and what it dropped
  yolo pack install           fetch configured packs and write the lockfile
  yolo pack update            same as install (re-fetch; reports moved pins)
  yolo pack status            show locked commits, and flag config/lock drift

Packs are configured in ~/.config/yolo-jail/config.jsonc under "packs" (USER scope
only — a workspace config cannot name one), as an address per entry:

  "packs": ["file:///home/me/code/my-pack",
            "git+ssh://git@github.com/org/repo//subdir?ref=main"]

Then run ` + "`yolo pack install`" + ` (fetching only ever happens there, never at launch).
For the full entry schema — name, only/exclude, allow_exec — and the precedence
rules, see ` + "`yolo config-ref`" + `.`

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
	case "install", "update":
		return packInstall(out, errw, color)
	case "status":
		return packStatus(out, errw, color)
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

// packInstall fetches every configured pack and records what it resolved to (C5).
//
// THIS IS THE ONLY PLACE NETWORK ACCESS HAPPENS. Launch resolves offline from the
// store, so fetching is an explicit, user-initiated act — never something that fires
// mid-boot. `update` is the same operation with a different name and intent, so they
// share this body: the distinction users care about is "did my pins move", and the
// output reports exactly that.
func packInstall(out, errw io.Writer, color bool) int {
	entries, err := config.LoadPacks(func(msg string) {
		fmt.Fprintf(errw, "Warning: %s\n", msg)
	})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack install: %v\n", err)
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}

	// NOTE: no early return for zero entries. Removing the last pack from config must
	// still PRUNE its lockfile entry — returning early here left a stale lock behind,
	// so the pack looked installed forever and `status` reported a pin for content
	// that was no longer being delivered.
	lockPath := packsrc.LockPath(paths.UserConfigPath())
	lock, err := packsrc.LoadLock(lockPath)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack install: %v\n", err)
		return 1
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}

	rc := 0
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
		addr, err := packsrc.Parse(e.Source)
		if err != nil {
			fmt.Fprintf(errw, "yolo pack install: %s: %v\n", e.Name, err)
			rc = 1
			continue
		}
		if addr.IsLocal() {
			// A local pack has nothing to fetch and no commit to pin. Recording it
			// anyway keeps `pack ls`/rollback able to see every pack, without
			// inventing a pin it does not have.
			lock.Set(packsrc.LockEntry{Name: e.Name, Source: e.Source})
			pr.Printf("[dim]%s: local, nothing to fetch[/dim]", e.Name)
			continue
		}
		prev, hadPrev := lock.Get(e.Name)
		commit, err := store.Sync(addr)
		if err != nil {
			fmt.Fprintf(errw, "yolo pack install: %s: %v\n", e.Name, err)
			rc = 1
			continue
		}
		if _, err := store.Materialize(addr, commit); err != nil {
			fmt.Fprintf(errw, "yolo pack install: %s: %v\n", e.Name, err)
			rc = 1
			continue
		}
		lock.Set(packsrc.LockEntry{
			Name: e.Name, Source: e.Source, Commit: commit, Ref: addr.Ref,
		})
		// Report whether the pin MOVED, which is the thing a user actually wants to
		// know from an update — not merely that it succeeded.
		switch {
		case !hadPrev:
			pr.Printf("[green]%s[/green] %s → %s", e.Name, addr.Ref, shortSHA(commit))
		case prev.Commit != commit:
			pr.Printf("[yellow]%s[/yellow] %s: %s → %s", e.Name, addr.Ref,
				shortSHA(prev.Commit), shortSHA(commit))
		default:
			pr.Printf("[dim]%s unchanged (%s)[/dim]", e.Name, shortSHA(commit))
		}
	}

	// Prune entries for packs that left the config, and SAY SO: a lock entry
	// vanishing means content is about to stop being delivered.
	for _, gone := range lock.Prune(names) {
		pr.Printf("[dim]%s removed from config — dropped from the lockfile[/dim]", gone)
	}
	if len(entries) == 0 {
		pr.Printf("[dim]No packs configured.[/dim]")
	}
	if err := lock.Save(lockPath); err != nil {
		fmt.Fprintf(errw, "yolo pack install: writing lockfile: %v\n", err)
		return 1
	}
	return rc
}

// packStatus reports configured packs against the lockfile, including DRIFT — a
// config address that no longer matches what is locked.
//
// Drift is the whole reason this verb exists. Launch resolves from the store using
// the CONFIG address, but a user who edits `?ref=v1` to `?ref=v2` without running
// install has a config and a lockfile that disagree, and nothing else would tell them.
func packStatus(out, errw io.Writer, color bool) int {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack status: %v\n", err)
		return 1
	}
	lock, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
	if err != nil {
		fmt.Fprintf(errw, "yolo pack status: %v\n", err)
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	if len(entries) == 0 {
		pr.Printf("[dim]No packs configured.[/dim]")
		return 0
	}

	configured := map[string]string{}
	for _, e := range entries {
		configured[e.Name] = e.Source
	}
	for _, e := range entries {
		locked, ok := lock.Get(e.Name)
		switch {
		case !ok:
			pr.Printf("[yellow]%-20s not installed[/yellow] [dim](run `yolo pack install`)[/dim]", e.Name)
		case locked.Commit == "":
			pr.Printf("%-20s [dim]local[/dim]", e.Name)
		default:
			pr.Printf("%-20s %s [dim]%s[/dim]", e.Name, shortSHA(locked.Commit), locked.Ref)
		}
	}
	drift := lock.DriftFrom(configured)
	for _, d := range drift {
		pr.Printf("[yellow]⚠ %s: config changed since install[/yellow]", d.Name)
		pr.Printf("    locked: [dim]%s[/dim]", d.LockedSource)
		pr.Printf("    config: [cyan]%s[/cyan]", d.WantedSource)
	}
	if len(drift) > 0 {
		pr.Printf("[dim]Run `yolo pack install` to fetch the new address.[/dim]")
		return 1
	}
	return 0
}

// shortSHA abbreviates a commit for display, tolerating a short or empty input rather
// than panicking on a slice bound.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
