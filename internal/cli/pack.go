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
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
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
// It used to state plainly that an agent-installing pack DID NOT EXIST YET, because
// promising it would have sent a user to configure a pack, watch it silence the very
// warning that told them they had no agent, and still have no agent.
//
// It exists now. A pack declares its own install, mounts, writable dirs, composed
// surfaces and host-file grants (internal/packdecl), and the boot path renders every one
// of them with no switch on any tool name. The packs yolo ships are selected by BARE NAME
// — `packs: ["claude"]` — and nothing is on by default, so the empty-packs warning stays
// true rather than being contradicted by six silently-active packs.
const packUsage = `yolo pack — author and inspect agent config packs

A pack is a directory of jail CONFIG — skills, briefing prose (AGENTS.md), composed
config files, and optionally a tool to install — delivered into every jail you launch.
Everything a jail has beyond a bare shell arrives as a pack: with no packs configured, a
jail gets nothing but the built-ins, and no coding agent.

What a pack delivers:
  • a coding agent: its CLI, its config files, its skills and briefing
  • a SHARED pack of your own skills and house rules, applied in every project
  • per-project narrowing of that corpus, via only/exclude

A zero-ceremony pack needs no manifest: a skills/ dir and an AGENTS.md at the pack
root are staged as-is. A pack.json adds a "contributes" list, one typed entry per
effect, with a "kind" from a closed set:

  program        install a tool onto PATH        skills    merge a skills tree
  briefing       prose appended to the briefing  files     own a file tree
  config         a composed config surface       state     a persistent home dir
  launch         inject launch flags             hook      a named capability
  reads-host     read one host-home file :ro     mount     mount a host-home dir :ro
  env            set static env vars in the jail
  autonomy       the agent's autonomous/guarded permission postures (notch-selected)
  config-overlay keys on a config surface another pack owns

See ` + "`yolo config-ref`" + ` (the "packs" section) for the full per-kind field reference.

The packs yolo ships are selected by NAME, and none is on by default:

  "packs": ["claude"]        # or copilot, codex, opencode, pi, agy

An EMBEDDED or LOCAL (file://) pack may read the host home unconditionally — reads-host,
mount, an installer program, or a host-prepending briefing. A FETCHED (git) pack may too,
but only for the claims you APPROVE at install: yolo pack install shows what the pack
reads and asks y/N once, recording the approval (per commit) in the lockfile. A pin that
later gains a new host-access claim re-prompts; an unapproved claim is refused at launch,
with a notice. Static "env" values are never gated (they read nothing from the host), and
every loaded pack's host access is listed in the startup banner each launch.

  yolo pack init [dir]        scaffold a pack skeleton (default: current dir)
  yolo pack lint [dir]        validate the tree AND the pack.json manifest; print its footprint
  yolo pack ls                list configured packs and what each stages
  yolo pack explain <name>    show which files a pack stages, and what it dropped
  yolo pack footprint [ref]   what packs claim on the environment + collisions;
                              [ref] = an embedded name OR a local path / file:// pack
  yolo pack install           fetch configured packs, write the lockfile, approve host access
  yolo pack update            same as install (re-fetch; reports moved pins, re-approves new access)
  yolo pack status            show locked commits, and flag config/lock drift

Packs are configured in ~/.config/yolo-jail/config.jsonc under "packs" (USER scope
only — a workspace config cannot name one), as a bare name for a pack yolo ships or an
address for one from elsewhere:

  "packs": ["claude",
            "file:///home/me/code/my-pack",
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
	return packMain(rest, os.Stdout, os.Stderr, isTTYStdout(), os.Stdin)
}

// packMain dispatches a pack subcommand. stdin is the reader the install-time
// host-access approval prompt uses; a nil stdin (tests, or a non-interactive run)
// means "no approval given", so a fetched pack's host access stays refused rather
// than being granted without a human — fail-closed on the credential boundary.
func packMain(args []string, out, errw io.Writer, color bool, stdin io.Reader) int {
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
	case "footprint":
		return packFootprint(args[1:], out, errw, color)
	case "install", "update":
		return packInstall(out, errw, color, stdin)
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

	// Manifest validation. LoadDir runs packdecl.Decode over the STAGED tree (tmp),
	// so an unknown kind, a missing required field, or an unknown top-level key is
	// caught HERE rather than at jail boot (where only the first problem surfaces,
	// one per launch). Staged, not source, so a manifest filtered out by only/exclude
	// is not linted as if it shipped. A manifest is optional, so an absent one is not
	// a problem — LoadDir returns no problems in that case.
	//
	// mayAccessHost=true so a reads-host / installer / host-prepending-briefing
	// contribution is validated for SHAPE regardless of origin; lint checks the
	// declaration, and the origin gate (a fetched pack getting it refused) is a
	// separate, install-time concern.
	pack, manifestProblems := packload.LoadDir(tmp, filepath.Base(dir), true)
	problems = append(problems, manifestProblems...)

	if len(problems) > 0 {
		for _, p := range problems {
			pr.Printf("[red]✗[/red] %s", p)
		}
		return 1
	}

	pr.Printf("[green]✓[/green] pack ok — %d file(s) stage", len(res.Staged))

	// The footprint: what this pack CLAIMS on the environment. An author who never
	// launches a jail still sees, at lint time, exactly what the manifest declares —
	// the same view `yolo pack footprint` gives, computed from this pack alone.
	printPackFootprint(pr, pack)
	return 0
}

// printPackFootprint prints one pack's declared claims, flagging the ones a human
// should review (machine-scope state, a host read, an installer URL). Shared by
// `pack lint` and `pack footprint` so their output does not drift.
func printPackFootprint(pr richtext.Printer, p *packload.Pack) {
	fp := packload.FootprintOf(p)
	if len(fp.Claims) == 0 {
		return
	}
	pr.Printf("[dim]declares %d claim(s):[/dim]", len(fp.Claims))
	for _, c := range fp.Claims {
		flag := ""
		if c.ReviewWorthy {
			flag = " [yellow]⚠ review[/yellow]"
		}
		detail := ""
		if c.Detail != "" {
			detail = "  [dim]" + c.Detail + "[/dim]"
		}
		pr.Printf("  [cyan]%-14s[/cyan] %s%s%s", string(c.Kind), c.Target, detail, flag)
	}
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
		// KIND is the pack's ORIGIN, which is the one thing that decides whether its
		// host-access declarations are honored — so it is the column worth showing.
		kind := "git"
		switch {
		case e.Embedded():
			kind = "builtin"
		case e.IsLocal():
			kind = "local"
		}
		source := e.Source
		if e.Embedded() {
			// "embedded:claude" is a synthetic marker, not an address; printing it in a
			// SOURCE column would invite someone to copy it as one.
			source = "(ships with yolo)"
		}
		pr.Printf("%-20s %-8s %s", e.Name, kind, source)
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
	if entry.Embedded() {
		// A builtin's tree lives in the binary, so there is no directory to stage from
		// here and its content is fixed — `explain` answers "why isn't MY skill showing
		// up", which is never about a shipped pack. Say so rather than staging from
		// "embedded:<name>" as if it were a path (which would report an empty pack).
		fmt.Fprintf(errw, "yolo pack explain: %s ships with yolo — its content is fixed, "+
			"so there are no only/exclude filters to explain. See `yolo config ls` for the "+
			"config files it renders.\n", name)
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

// packFootprint prints what each pack CLAIMS on the environment, and the cross-pack
// collisions the one-writer rule forbids. With no argument it reports the embedded
// packs yolo ships. With an argument it reports ONE pack: an embedded name, or a
// local path / file:// source — which is staged, loaded, and reported so an author
// can see their own pack's claims (and any self-collision) before configuring it.
// A git source is not fetched here (the same limit `pack explain` has).
//
// The footprint is computed from each pack's contributes[] via
// packload.FootprintOf, dispatching on contribution kind.
func packFootprint(args []string, out, errw io.Writer, color bool) int {
	pr := richtext.Printer{W: out, Color: color}

	if len(args) > 0 {
		arg := args[0]
		// A local path or file:// source → stage + load it, so footprint works on the
		// pack you are AUTHORING, not only the six yolo ships. This is the one command
		// that surfaces a same-into skills collision before boot does, so it must accept
		// a pack that is not yet configured.
		if isLocalPackArg(arg) {
			return packFootprintLocal(arg, pr, errw)
		}
		// Otherwise treat it as an embedded pack name.
		var one []*packload.Pack
		for _, p := range packload.Embedded() {
			if p.Name == arg {
				one = append(one, p)
			}
		}
		if len(one) == 0 {
			fmt.Fprintf(errw, "yolo pack footprint: %q is neither an embedded pack nor a "+
				"local path/file:// source — see `yolo pack ls`, or pass a directory to "+
				"footprint a pack you are authoring\n", arg)
			return 1
		}
		return reportFootprint(one, pr)
	}

	packs := packload.Embedded()
	if len(packs) == 0 {
		fmt.Fprintln(errw, "yolo pack footprint: no embedded packs available")
		return 1
	}
	return reportFootprint(packs, pr)
}

// isDir reports whether p exists and is a directory.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// isLocalPackArg reports whether a footprint/explain argument names a local pack
// (a file:// source or an existing directory) rather than an embedded pack name.
func isLocalPackArg(arg string) bool {
	if strings.HasPrefix(arg, "file://") {
		return true
	}
	// A bare name like "claude" is not a path; only treat it as local if it resolves
	// to an existing directory (covers "./my-pack", "../x", and absolute paths).
	if strings.ContainsAny(arg, "/.") {
		return isDir(strings.TrimPrefix(arg, "file://"))
	}
	return false
}

// packFootprintLocal stages a local pack directory and prints its footprint. Uses
// the same packstage.Stage → packload.LoadDir path as boot, so the claims match
// what a jail would render. mayAccessHost=true so host-gated claims show up in the
// footprint (the origin gate is what decides whether they are HONORED, and footprint
// exists precisely to show what a pack WANTS before you trust it).
func packFootprintLocal(arg string, pr richtext.Printer, errw io.Writer) int {
	root := strings.TrimPrefix(arg, "file://")
	tmp, err := os.MkdirTemp("", "yolo-pack-footprint-")
	if err != nil {
		fmt.Fprintf(errw, "yolo pack footprint: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	if _, err := packstage.Stage(packstage.Spec{Root: root, Dest: tmp}); err != nil {
		fmt.Fprintf(errw, "yolo pack footprint: %v\n", err)
		return 1
	}
	pack, problems := packload.LoadDir(tmp, filepath.Base(root), true)
	if len(problems) > 0 {
		for _, p := range problems {
			pr.Printf("[red]✗[/red] %s", p)
		}
		return 1
	}
	return reportFootprint([]*packload.Pack{pack}, pr)
}

// reportFootprint prints the per-pack claims for a set of packs, then their
// cross-pack collisions and the review summary.
func reportFootprint(packs []*packload.Pack, pr richtext.Printer) int {
	for _, p := range packs {
		fp := packload.FootprintOf(p)
		pr.Printf("[bold]%s[/bold]", p.Name)
		if len(fp.Claims) == 0 {
			pr.Printf("  [dim](no declared claims)[/dim]")
			continue
		}
		for _, c := range fp.Claims {
			flag := ""
			if c.ReviewWorthy {
				flag = " [yellow]⚠ review[/yellow]"
			}
			detail := ""
			if c.Detail != "" {
				detail = "  [dim]" + c.Detail + "[/dim]"
			}
			pr.Printf("  [cyan]%-14s[/cyan] %s%s%s", string(c.Kind), c.Target, detail, flag)
		}
	}

	// Cross-pack collisions across the reported set (the good-citizen check).
	cols := packload.Collisions(packs)
	if len(cols) > 0 {
		pr.Printf("")
		pr.Printf("[bold red]%d collision(s):[/bold red]", len(cols))
		for _, c := range cols {
			pr.Printf("  [red]%s %s[/red] — packs %s: %s",
				string(c.Kind), c.Target, strings.Join(c.Packs, ", "), c.Reason)
		}
		return 1
	}

	// Review summary — the claims a human should look at before trusting the set.
	rw := packload.ReviewWorthy(packs)
	if len(rw) > 0 {
		pr.Printf("")
		pr.Printf("[dim]%d claim(s) worth review: %s[/dim]", len(rw), reviewSummary(rw))
	}
	return 0
}

// reviewSummary renders the one-line "N claims worth review" tail: a compact
// count-by-kind so the reader sees the shape (1 machine-wide state, 1 host read,
// …) without re-listing every claim.
func reviewSummary(claims []packload.Claim) string {
	byKind := map[string]int{}
	var order []string
	for _, c := range claims {
		k := string(c.Kind)
		if byKind[k] == 0 {
			order = append(order, k)
		}
		byKind[k]++
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%d %s", byKind[k], k))
	}
	return strings.Join(parts, ", ")
}

// packInstall fetches every configured pack and records what it resolved to (C5).
//
// THIS IS THE ONLY PLACE NETWORK ACCESS HAPPENS. Launch resolves offline from the
// store, so fetching is an explicit, user-initiated act — never something that fires
// mid-boot. `update` is the same operation with a different name and intent, so they
// share this body: the distinction users care about is "did my pins move", and the
// output reports exactly that.
func packInstall(out, errw io.Writer, color bool, stdin io.Reader) int {
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
		// An EMBEDDED pack ships inside the binary: nothing to fetch, no commit to pin.
		// It is still NAMED here so the lockfile prune below does not treat it as
		// removed-from-config and drop a real entry alongside it.
		if e.Embedded() {
			continue
		}
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
		resolved, err := store.Materialize(addr, commit)
		if err != nil {
			fmt.Fprintf(errw, "yolo pack install: %s: %v\n", e.Name, err)
			rc = 1
			continue
		}
		treeRoot := resolved.Root
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

		// HOST-ACCESS APPROVAL. A fetched pack may read the host (mount, reads-host,
		// installer, host-briefing) only with explicit consent, recorded per-commit in
		// the lockfile. Carry a prior approval forward when the pack asks for nothing
		// new; prompt when it declares a host-access claim the user has not approved.
		approved, denied := resolveHostApproval(e.Name, treeRoot, prev, hadPrev, pr, stdin, out)
		if denied {
			// The user declined (or a non-interactive run cannot ask). The pack is still
			// installed and its non-host contributions work; its host claims will be
			// refused at launch until approved. Not an install failure.
			rc = 1
		}
		lock.Set(packsrc.LockEntry{
			Name: e.Name, Source: e.Source, Commit: commit, Ref: addr.Ref,
			ApprovedHostAccess: approved,
			ApprovedAt:         approvedAtFor(approved, commit),
		})
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

// resolveHostApproval decides which host-access claims are approved for a freshly
// materialized fetched pack, prompting the user only when the pack declares a claim
// they have not already approved.
//
// treeRoot is the materialized pack tree. prev/hadPrev are the pack's previous
// lockfile entry. Returns the approved claim set to record, and denied=true when the
// pack WANTS host access the user did not grant (declined, or no stdin to ask).
//
//   - No host-access claims → approve nothing, denied=false (a pack that reads
//     nothing from the host needs no consent).
//   - Every claim already approved (prev.HostAccessApproved) → carry the prior
//     approval forward silently. This is the "unchanged or narrowed pin" case.
//   - A claim not previously approved → show the full claim set and prompt y/N. On
//     yes, approve the current set (so a later narrowing is remembered too); on no or
//     no-tty, keep the prior approvals but do NOT add the new ones (denied=true).
func resolveHostApproval(name, treeRoot string, prev packsrc.LockEntry, hadPrev bool,
	pr richtext.Printer, stdin io.Reader, out io.Writer) (approved []string, denied bool) {
	p, _ := packload.LoadDir(treeRoot, name, true)
	if p == nil {
		return prevApproved(prev, hadPrev), false
	}
	want := p.Decl.HostAccessClaims()
	if len(want) == 0 {
		return nil, false // reads nothing from the host
	}
	if hadPrev && prev.HostAccessApproved(want) {
		// Nothing new since the last approval — carry it forward, no prompt. Re-record
		// the CURRENT set so a claim the pack dropped stops being carried.
		return want, false
	}

	// New host access: show the full claim set (not just the delta — the user is
	// approving the whole current footprint) and ask.
	pr.Printf("  [bold yellow]⚠ pack %s reads your host:[/bold yellow]", name)
	for _, c := range want {
		pr.Printf("      [yellow]%s[/yellow]", c)
	}
	if !promptYesNo(out, stdin, "  Approve host access for "+name+"? [y/N] ") {
		pr.Printf("  [red]host access NOT approved — %s's host claims will be refused at "+
			"launch until you run `yolo pack install` and approve[/red]", name)
		return prevApproved(prev, hadPrev), true
	}
	return want, false
}

// prevApproved returns the previously-approved claim set, or nil when there was no
// prior entry.
func prevApproved(prev packsrc.LockEntry, hadPrev bool) []string {
	if !hadPrev {
		return nil
	}
	return prev.ApprovedHostAccess
}

// approvedAtFor records the commit an approval was granted against, but only when
// something was actually approved — an empty set (a pack that reads nothing, or a
// declined prompt that carried nothing forward) records no anchor.
func approvedAtFor(approved []string, commit string) string {
	if len(approved) == 0 {
		return ""
	}
	return commit
}

// promptYesNo writes a prompt and reads a single line from stdin, returning true
// only for an explicit yes. A nil stdin (non-interactive, or a test) is a NO — the
// credential boundary fails closed: host access is never granted without a human.
func promptYesNo(out io.Writer, stdin io.Reader, prompt string) bool {
	if stdin == nil {
		return false
	}
	_, _ = out.Write([]byte(prompt))
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
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
		// EMBEDDED packs are excluded from the drift map deliberately: they have no lock
		// entry to drift from, and including them would make DriftFrom report every
		// builtin as "config changed since install" forever.
		if e.Embedded() {
			continue
		}
		configured[e.Name] = e.Source
	}
	for _, e := range entries {
		if e.Embedded() {
			// Never "not installed": it ships in the binary, so telling the user to run
			// `yolo pack install` would send them to a command that cannot help.
			pr.Printf("%-20s [dim]builtin[/dim]", e.Name)
			continue
		}
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
