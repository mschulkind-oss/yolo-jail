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
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
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

  program          install a tool onto PATH
  requires         a tool that must already exist
  skills           merge a skills tree
  briefing         prose for the briefing
  files            own a file tree, bind-mounted :ro in the jail
  config           a composed config surface
  config-overlay   keys on a config surface another pack owns
  state            a persistent home dir
  reads-host       read one host-home file :ro
  mount            mount a host-home dir :ro
  env              set static env vars in the jail
  launch           inject launch flags after a binary
  hook             a named capability (shared_credentials, …)
  autonomy         the agent's autonomous/guarded permission postures (notch-selected)
  loophole         ship a host-capability loophole: a module dir with a manifest.jsonc

loophole is the sharpest kind: its module may declare a daemon that runs ON YOUR MACHINE,
TLS intercepts (a CA every client in the jail trusts), host bind mounts and host devices.
Every one of those is a separate claim you approve, and the daemon claim carries its raw
argv — so a pack whose daemon changes re-prompts.

program vs requires is install-vs-presence: program means yolo installs the tool (a lazy
launcher, last on PATH), requires means it must already be there and yolo installs
nothing — which is what a pack needing a baked or user-provided tool wants, and the only
way for a content-only pack to carry install_hints for the host notch.

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
                              --from-plugin <dir>  wrap an EXISTING agent plugin (a tree with
                              a .claude-plugin/plugin.json) as a pack: its tree is delivered
                              verbatim, so its skills invoke as /<plugin>:<skill> and cannot
                              collide with yours. Components that RUN (hooks, MCP/LSP servers)
                              are named at init and approved at install.
  yolo pack lint [dir]        validate the tree AND the pack.json manifest; print its footprint
                              --allow-exec  lint as a consumer who set "allow_exec"
  yolo pack ls                list configured packs and what each stages
  yolo pack explain <name>    show which files a pack stages, and what it dropped
  yolo pack footprint [ref]   what packs claim on the environment + collisions;
                              [ref] = an embedded name OR a local path / file:// pack
                              --allow-exec  same as lint's: inspect a pack shipping an executable
  yolo pack install           fetch configured packs, write the lockfile, approve host access.
                              It NEVER asks a registry what the latest version is
  yolo pack update            install, PLUS the only act that resolves a new version for a
                              pack's npm-declared program. Run it inside the jail — that is
                              where an agent CLI is installed
  yolo pack status            show locked commits, and flag config/lock drift
  yolo pack --help, -h        this text (also 'yolo pack help')

Packs are configured in ~/.config/yolo-jail/config.jsonc under "packs" (USER scope
only — a workspace config cannot name one), as a bare name for a pack yolo ships or an
address for one from elsewhere:

  "packs": ["claude",
            "file:///home/me/code/my-pack",
            "git+ssh://git@github.com/org/repo//subdir?ref=main"]

Then run ` + "`yolo pack install`" + ` (fetching only ever happens there, never at launch).

A pack's ` + "`program via npm`" + ` no longer updates itself. Its launcher installs the tool on
first use and afterwards only REPORTS, at most hourly, that a newer version exists —
nothing changes a binary between two invocations with nobody present. ` + "`yolo pack update`" + `,
run inside the jail, is the act that resolves the new version.

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
// host-access approval prompt uses; anything that is not a real terminal — nil
// (tests), a pipe, a redirect — means "no approval given", so a fetched pack's host
// access stays refused rather than being granted without a human — fail-closed on
// the credential boundary (see approvalStdinFrom).
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
	// install and update are two ACTS, not two spellings of one. Only update may
	// resolve a new version for a pack's npm-declared program — docs/design/
	// trust-paths.md §1 row 1, and see packupdate.go for the whole of the difference.
	case "install":
		return packInstall(out, errw, color, stdin)
	case "update":
		return packUpdate(out, errw, color, stdin)
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
//
// --from-plugin <dir> wraps an EXISTING agent plugin instead, which is what turns "you can
// pull in a plugin" from documented into trivial.
func packInit(args []string, out, errw io.Writer) int {
	dir := "."
	fromPlugin := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--from-plugin":
			if i+1 >= len(args) {
				fmt.Fprintf(errw, "yolo pack init: --from-plugin needs a plugin directory\n")
				return 2
			}
			i++
			fromPlugin = args[i]
		case strings.HasPrefix(a, "--from-plugin="):
			fromPlugin = strings.TrimPrefix(a, "--from-plugin=")
		default:
			dir = a
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack init: %v\n", err)
		return 1
	}
	name := filepath.Base(abs)
	if fromPlugin != "" {
		return packInitFromPlugin(fromPlugin, abs, name, out, errw)
	}

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
		if rc := writeScaffoldFile(abs, f.rel, f.content, out, errw); rc != 0 {
			return rc
		}
	}
	fmt.Fprintf(out, "\nPack scaffolded at %s\nNext: yolo pack lint %s\n", abs, dir)
	return 0
}

// writeScaffoldFile writes one scaffolded file, never clobbering: `init` on an existing pack
// must be safe to re-run. Shared with the --from-plugin path so both report identically.
func writeScaffoldFile(root, rel, content string, out, errw io.Writer) int {
	p := filepath.Join(root, rel)
	if _, err := os.Stat(p); err == nil {
		fmt.Fprintf(out, "  skip %s (exists)\n", rel)
		return 0
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
	return 0
}

// packLint validates a pack DIRECTORY by staging it into a throwaway dir with the
// real executor, so the linter cannot disagree with the stager.
//
// --allow-exec lints the way a CONSENTING CONSUMER would stage. `allow_exec` lives in the
// user's config, not the manifest, so an author linting their own pack otherwise has no way
// to see past the exec-bit refusal to the rest of the report — the flag supplies the
// consumer's half of the decision without pretending a pack can grant it.
func packLint(args []string, out, errw io.Writer, color bool) int {
	dir := "."
	allowExec := false
	for _, a := range args {
		if a == "--allow-exec" {
			allowExec = true
			continue
		}
		dir = a
	}
	pr := richtext.Printer{W: out, Color: color}

	tmp, err := os.MkdirTemp("", "yolo-pack-lint-")
	if err != nil {
		fmt.Fprintf(errw, "yolo pack lint: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	var problems []string

	// A staging failure is collected as a PROBLEM, not returned on. Returning here (as
	// this used to) meant the exec-bit refusal MASKED the manifest validation below —
	// so an author who followed the old message's advice and put `allow_exec` in
	// pack.json saw only the refusal, never the "unknown field allow_exec" line that
	// explains why their fix did nothing. The two messages together are
	// self-explanatory; either alone is not.
	res, err := packstage.Stage(packstage.Spec{Root: dir, Dest: tmp, AllowExec: allowExec})
	if err != nil {
		// The staging rules ARE the lint rules: exec bit, escaping symlink, missing
		// root. Reporting the executor's own message keeps the two from drifting.
		problems = append(problems, err.Error())
		// Nothing staged means the content checks below would all fire spuriously, but
		// the MANIFEST is still worth validating: pack.json is read from the source dir
		// when staging produced nothing, so an author gets both halves of the story.
		res = &packstage.Result{}
		// Validate the manifest from the SOURCE dir, since nothing reached the staging
		// dir. This is the line that turns "your fix did nothing" into "allow_exec is not
		// a manifest key", printed right beside the refusal that prompted the attempt.
		_, sourceManifestProblems := packload.LoadDir(dir, filepath.Base(dir), true)
		problems = append(problems, sourceManifestProblems...)
		for _, p := range problems {
			pr.Printf("[red]✗[/red] %s", p)
		}
		return 1
	}
	if len(res.Staged) == 0 {
		problems = append(problems, "pack contains no stageable files")
	}

	// Manifest validation, BEFORE the content checks below, because those checks depend on
	// it: a `skills` contribution's `from` decides WHICH staged dir holds the skills, so the
	// "no skills dir", "skill without SKILL.md" and plugin-wrapper rules all have to read the
	// declared source rather than assume the conventional one. Linting `skills/` while the
	// pack delivers from `my-skills/` is the same silent-ignore bug in the linter.
	//
	// LoadDir runs packdecl.Decode over the STAGED tree (tmp), so an unknown kind, a missing
	// required field, or an unknown top-level key is caught HERE rather than at jail boot
	// (where only the first problem surfaces, one per launch). Staged, not source, so a
	// manifest filtered out by only/exclude is not linted as if it shipped. A manifest is
	// optional, so an absent one is not a problem — LoadDir returns no problems in that case.
	//
	// mayAccessHost=true so a reads-host / installer / host-prepending-briefing
	// contribution is validated for SHAPE regardless of origin; lint checks the
	// declaration, and the origin gate (a fetched pack getting it refused) is a
	// separate, install-time concern.
	pack, manifestProblems := packload.LoadDir(tmp, filepath.Base(dir), true)
	problems = append(problems, manifestProblems...)

	// A `loophole` contribution points at a module dir, so validating the pack.json entry is
	// only half of it: the manifest INSIDE that dir is the half that declares what runs on
	// the host, and it is read by a decoder pack.json's does not reach. Read STRICTLY here
	// (LoopholeDeclProblems), which is the whole reason lint exists for this kind — a
	// misspelled `host_deamon` otherwise reads as a loophole with no daemon, and the symptom
	// surfaces much later as a missing endpoint.
	//
	// Against the STAGED tree, like the manifest check above, so a module filtered out by
	// only/exclude is reported as absent rather than linted as if it shipped.
	problems = append(problems, pack.LoopholeDeclProblems()...)

	// The skills SOURCE dirs this pack actually delivers from, pack-relative. Every
	// `skills` contribution's `from`, or the conventional dir when the manifest names none.
	skillRoots := pack.Decl.SkillsSources()
	if len(skillRoots) == 0 {
		skillRoots = []string{packdecl.DefaultSkillsDir}
	}
	// A NON-CONVENTIONAL `from` that stages nothing delivers nothing, at either notch.
	// Reported as a lint failure because this is the authoring boundary: `from` used to be
	// validated for SHAPE and then ignored, so a typo'd source was invisible everywhere — the
	// linter said the manifest was fine and the jail read skills/ instead.
	//
	// The conventional dir is deliberately exempt (the same line packload.SkillsSourceDir
	// draws): a pack whose contribution exists only to NAME a destination other packs merge
	// into carries no skills of its own, and that is what all six shipped packs do. That
	// exemption is STILL load-bearing after the rewrite below — the two checks that replaced
	// "nothing reads this pack" ask about the pack as a whole, and a shipped pack passes both
	// (it has contributions, and it stages no unclaimed content), so neither one would silence
	// a per-contribution complaint about `skills/` being absent. Resolved through
	// SkillsSource() rather than `from` directly, so an OMITTED `from` (which resolves to the
	// convention) is exempt for the same reason an explicit `"skills"` is.
	missingSkillsSource := false
	for _, c := range pack.Decl.Contributions() {
		if c.Kind != packdecl.KindSkills {
			continue
		}
		src := c.SkillsSource()
		if path.Clean(src) == packdecl.DefaultSkillsDir {
			continue
		}
		if !stagedUnder(res.Staged, src) {
			missingSkillsSource = true
			problems = append(problems, fmt.Sprintf(
				"skills `from` is %q, but nothing stages under %s/ — that contribution would "+
					"deliver no skills (check the path, and any only/exclude filters)",
				src, strings.TrimSuffix(src, "/")))
		}
	}

	// The two checks below replaced ONE bad one: "pack has neither a skills/ dir nor an
	// AGENTS.md — it would stage files nothing reads". That rule asked "did this pack stage
	// skills/ or AGENTS.md?" as a proxy for "does anything read this pack?", which was true
	// when a pack could only ship content and is false now that a pack contributes any of 14
	// kinds. It rejected all six packs yolo ships, and a real user's `files`+`config-overlay`
	// pack — and, decisively, it gave a pack that does ABSOLUTELY NOTHING the identical
	// message, so it was useless in the one case it existed for.
	//
	// The two honest questions, separated (docs/plans/roadmap.md §7):
	claimed, unclaimed := stagedContent(res.Staged, pack, skillRoots)

	// The two are MUTUALLY EXCLUSIVE by construction, not by accident: a pack that declares
	// nothing gets question 1's answer and a pack that declares something gets question 2's,
	// so one mistake never draws two lines. The old rule's failure was the opposite — one line
	// for two unrelated mistakes.
	//
	// And both yield to the per-contribution complaint above. A typo'd `from` leaves the pack's
	// real skills tree unclaimed, so question 2 fires too — with advice ("move them under
	// skills/") that the author has ALREADY followed. The specific diagnosis is the correct
	// one, and printing a second, contradictory line beside it is how a fixed rule becomes new
	// noise.
	switch {
	case missingSkillsSource:
		// Already diagnosed, precisely.
	// 1. DOES THIS PACK DO ANYTHING? Zero declared contributions AND nothing a reader picks
	//    up by convention. Both halves are required: the pack `pack init` scaffolds has no
	//    pack.json at all, and the jail's zero-ceremony merge still delivers its skills/ tree
	//    and its AGENTS.md (packload.SkillsSourceDirs' and packload.BriefingProse's undeclared
	//    fallbacks) — so "declares nothing" alone would fail-lint the scaffold.
	case len(pack.Decl.Contributions()) == 0 && len(claimed) == 0:
		msg := "pack declares ZERO contributions and ships nothing read by convention — it " +
			"would do nothing in a jail. Add a contributes[] entry to pack.json " +
			"(`yolo pack --help` lists the kinds), or ship a skills/ tree or an AGENTS.md, " +
			"which a jail reads with no manifest at all"
		if len(unclaimed) > 0 {
			// Name what it DID stage. "Does nothing" is hard to believe while looking at a
			// directory full of files, and the files are the evidence for the claim.
			msg += " (staged, and read by nothing: " + strings.Join(sampleOf(unclaimed, 3), ", ") + ")"
		}
		problems = append(problems, msg)

	// 2. DOES IT STAGE CONTENT NOTHING READS? The old rule NARROWED to the case it was
	//    actually about: files that really would be read by nothing. It fires only when NOT
	//    ONE staged content file is claimed — by a contribution's source or by a
	//    conventionally-read location — because a pack whose content mostly lands correctly
	//    does not need a linter nitpicking a stray file, and a per-file version would flag
	//    every stray note in a working tree.
	case len(claimed) == 0 && len(unclaimed) > 0:
		problems = append(problems, fmt.Sprintf(
			"pack stages %d file(s) nothing reads (%s) — no contribution names those paths, "+
				"and none is in a conventionally-read location (skills/, AGENTS.md). Name them "+
				"with a `skills`/`files`/`briefing` contribution, or move them under skills/",
			len(unclaimed), strings.Join(sampleOf(unclaimed, 3), ", ")))
	}
	// A skill dir without SKILL.md is invisible to every agent, which is the single
	// most likely authoring mistake and produces no error anywhere else.
	//
	// A WRAPPED PLUGIN is the one exception, and it has to be: a plugin dir is not a skill
	// dir — its skills live one level down and its manifest is what makes them loadable — so
	// the SKILL.md rule would reject every plugin wrapper, including the one
	// `pack init --from-plugin` just wrote. Recognized from the staged tree rather than
	// assumed, so a dir merely NAMED like a plugin still gets the rule. Through pack.Plugins()
	// so discovery scans the declared source dirs, matching what delivery will find.
	pluginDirs := map[string]bool{}
	for _, pl := range pack.Plugins() {
		pluginDirs[filepath.Base(pl.Dir)] = true
	}
	for _, root := range skillRoots {
		for _, d := range skillDirsMissingManifest(res.Staged, root) {
			if pluginDirs[d] {
				continue
			}
			problems = append(problems, root+"/"+d+" has no SKILL.md — agents will not see it")
		}
	}

	if len(problems) > 0 {
		for _, p := range problems {
			pr.Printf("[red]✗[/red] %s", p)
		}
		return 1
	}

	pr.Printf("[green]✓[/green] pack ok — %d file(s) stage", len(res.Staged))

	// Advice: if a custom pack explicitly declares `briefing` or `skills` into a standard agent path,
	// remind the author that root-level AGENTS.md or skills/ is automatically routed to all agents.
	for i, c := range pack.Decl.Contributions() {
		if c.Kind == packdecl.KindBriefing || c.Kind == packdecl.KindSkills {
			for _, std := range []string{
				".claude/CLAUDE.md", ".claude/AGENTS.md", ".claude/skills",
				".pi/agent/AGENTS.md", ".pi/agent/skills",
				".gemini/config/AGENTS.md", ".gemini/antigravity-cli/skills",
				".codex/AGENTS.md", ".codex/skills",
				".opencode/AGENTS.md", ".opencode/skills",
			} {
				if c.Into == std {
					kindSrc := "AGENTS.md"
					if c.Kind == packdecl.KindSkills {
						kindSrc = "skills/"
					}
					pr.Printf("[yellow]ℹ[/yellow] contributes[%d]: %s into %q targets an agent's standard path — root-level %s in your pack is automatically routed to all active agents with zero ceremony; declaring it explicitly is unnecessary",
						i, c.Kind, c.Into, kindSrc)
					break
				}
			}
		}
	}

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
	printClaimLines(pr, fp.Claims)
	reportShippedSurfaceClash(pr, p)
}

// printClaimLines renders one claim per line, with the review marker.
//
// ONE function, called by both `pack lint` (printPackFootprint) and `pack footprint`
// (reportFootprint), because until now the two INLINED the same loop under a comment
// claiming they were shared "so their output does not drift" — which made the claim false
// in the one place it was written down, and meant a new marker had to be added twice or the
// two commands would diverge. It is now shared in fact.
func printClaimLines(pr richtext.Printer, claims []packload.Claim) {
	for _, c := range claims {
		// TWO markers, not one, because ReviewWorthy is a single boolean carrying two very
		// different propositions since the `loophole` kind: "yolo will read a file of yours"
		// and "yolo will execute this argv on your machine". A reader scanning a dozen
		// identically-flagged lines should not have to notice which is which.
		flag := ""
		switch {
		case c.RunsHostCode:
			flag = " [bold red]⚠ RUNS CODE ON YOUR MACHINE[/bold red]"
		case c.ReviewWorthy:
			flag = " [yellow]⚠ review[/yellow]"
		}
		detail := ""
		if c.Detail != "" {
			detail = "  [dim]" + c.Detail + "[/dim]"
		}
		pr.Printf("  [cyan]%-14s[/cyan] %s%s%s", string(c.Kind), c.Target, detail, flag)
	}
}

// reportShippedSurfaceClash warns when the pack under inspection declares a `config`
// surface one of the packs yolo SHIPS already owns.
//
// It exists because the single-pack views (`pack lint`, `pack footprint <dir>`) cannot see
// a cross-pack collision by construction — they hold one pack — and this particular clash
// is REFUSED at launch and at `apply --host`. Without this line an author's pack lints
// clean and then fails to boot, which is the worst possible place to learn it: the check
// that exists to be run before configuring a pack would be the one check that misses.
//
// Reads the not-selection-gated embedded set on purpose (the same argument
// packoverlay.shippedOwnerOf makes): the point is to see a pack the author has not
// selected, since that is exactly who they are about to collide with.
//
// A WARNING, not a lint failure, and the distinction is real: whether the two packs are
// ever selected TOGETHER is a config question this command cannot answer. Refusal belongs
// where the pack set is known.
func reportShippedSurfaceClash(pr richtext.Printer, p *packload.Pack) {
	mine, _ := p.Surfaces()
	if len(mine) == 0 {
		return
	}
	for _, shipped := range packload.Embedded() {
		if shipped.Name == p.Name {
			continue // linting a copy of a shipped pack is not a clash with itself
		}
		theirs, _ := shipped.Surfaces()
		for _, s := range theirs {
			for _, m := range mine {
				if m.Key() != s.Key() {
					continue
				}
				id := m.Key().String()
				pr.Printf("[yellow]⚠ %s is already owned by the `%s` pack yolo ships[/yellow] "+
					"[dim]— selecting both is REFUSED at launch and by `apply --host` "+
					"(a surface has one owner). Use `config-overlay` to contribute keys "+
					"instead:[/dim]", id, shipped.Name)
				pr.Printf("[dim]    { \"kind\": \"config-overlay\", \"surface\": \"%s\", "+
					"\"config\": { \"managed\": { …your keys… } } }[/dim]", id)
			}
		}
	}
}

// packNonContentFiles are the staged ROOT-level names that are not pack CONTENT: yolo reads
// the first two itself (pack.json is the manifest, derive.lua is the surface producer
// entrypoint/packsurfaces.go runs), and a human reads the rest — they are the repo-hygiene
// files `pack init` scaffolds and any real pack carries.
//
// They are excluded from "stages files nothing reads" because a config-only pack with a
// README is a legitimate shape, and flagging its README would make the replacement check
// noise for exactly the packs the rule it replaced wrongly rejected. Root-level only: a
// README INSIDE a skills dir is content, and is claimed by that dir anyway.
var packNonContentFiles = map[string]bool{
	"pack.json": true, "pack.jsonc": true, "derive.lua": true,
	"README.md": true, "LICENSE": true, "LICENSE.md": true, "CHANGELOG.md": true,
	".gitignore": true, ".gitattributes": true,
}

// stagedContent splits a pack's staged files into the ones some reader would pick up
// (CLAIMED — named by a contribution's source, or sitting in a conventionally-read
// location) and the ones nothing reads (UNCLAIMED). Non-content files (the manifest, the
// derive script, repo hygiene) are in neither.
//
// It answers the question `pack lint`'s old "neither a skills/ dir nor an AGENTS.md" rule
// was reaching for and got wrong. The difference is that this asks about the CLAIMS a pack
// makes rather than about two hardcoded paths, so a `files` tree, a non-conventional
// `skills` source and a declared `briefing` all count as read — which they are.
//
// skillRoots is the resolved skills sources (SkillsSources(), or the conventional dir when
// the manifest declares none), passed in because the caller already computed it and the two
// must agree: a pack whose skills the linter reads from one dir and counts as claimed in
// another is the same silent-ignore bug in a new place.
func stagedContent(staged []string, pack *packload.Pack, skillRoots []string) (claimed, unclaimed []string) {
	// Every pack-relative path a reader looks at. Dirs and single files both, since
	// `briefing.from` names a file and `skills.from` names a dir.
	sources := append([]string{}, skillRoots...)
	// The conventional briefing file is read whether or not a `briefing` contribution names
	// it: a pack with no manifest at all still contributes it (packload.BriefingProse's
	// undeclared fallback), and a declared `from` falls back to it (BriefingCandidates).
	//
	// READ FROM packdecl, never re-listed here. This was a hardcoded {"AGENTS.md",
	// "CLAUDE.md"} and it went stale the day CLAUDE.md left DefaultBriefingFiles
	// (2026-08-17, pack-code-separation.md §3.3): lint went on counting a root CLAUDE.md as
	// CLAIMED — "some reader picks this up" — after every reader had stopped, so the check
	// whose whole job is to say when nothing reads a file said nothing about the one file
	// nothing read. A second copy of a convention is a copy that drifts silently, which is
	// the same failure mode the `from` resolvers were unified to end (briefingsource.go).
	sources = append(sources, packdecl.DefaultBriefingFiles()...)
	for _, c := range pack.Decl.Contributions() {
		switch c.Kind {
		// KindLoophole is here for the same reason as the other two: its `from` names a
		// DIRECTORY of content a reader picks up — internal/loopholes loads
		// <from>/manifest.jsonc, and everything the manifest references ({loophole_dir}/x)
		// resolves inside that dir. Omitted, `pack lint` rejected every pack whose only
		// contribution is a loophole ("stages N file(s) nothing reads", naming the manifest
		// the whole pack exists to deliver), which is the accepted-and-ignored shape this
		// check was rewritten to stop producing.
		case packdecl.KindFiles, packdecl.KindBriefing, packdecl.KindLoophole:
			if c.From != "" {
				sources = append(sources, c.From)
			}
		}
	}

	for _, s := range staged {
		if !strings.Contains(s, "/") && packNonContentFiles[s] {
			continue
		}
		if stagedPathClaimed(s, sources) {
			claimed = append(claimed, s)
			continue
		}
		unclaimed = append(unclaimed, s)
	}
	return claimed, unclaimed
}

// stagedPathClaimed reports whether one staged path is the source itself or lives under it.
func stagedPathClaimed(staged string, sources []string) bool {
	for _, src := range sources {
		src = strings.TrimSuffix(filepath.ToSlash(path.Clean(src)), "/")
		if staged == src || strings.HasPrefix(staged, src+"/") {
			return true
		}
	}
	return false
}

// sampleOf returns at most n items, with a "+N more" tail when it truncated — so a lint
// message naming the offending files stays one line for a pack that staged fifty of them.
func sampleOf(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(append([]string{}, items[:n]...),
		fmt.Sprintf("+%d more", len(items)-n))
}

// stagedUnder reports whether any staged path lives under the pack-relative dir `root`.
// Used by lint to ask "did this contribution's declared source stage anything", which is a
// question about the DECLARED dir rather than the conventional one.
func stagedUnder(staged []string, root string) bool {
	prefix := strings.TrimSuffix(filepath.ToSlash(root), "/") + "/"
	for _, s := range staged {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// skillDirsMissingManifest returns the <root>/<dir> names that staged files but no
// SKILL.md, for one skills source dir. Sorted, deduped.
//
// root is a parameter rather than the constant "skills/" it used to be, because a `skills`
// contribution's `from` decides where the skills live: linting the conventional dir for a
// pack that delivers from `my-skills/` would check a tree nothing reads and pass every
// missing SKILL.md in the tree that IS read.
func skillDirsMissingManifest(staged []string, root string) []string {
	prefix := strings.TrimSuffix(filepath.ToSlash(root), "/") + "/"
	hasManifest := map[string]bool{}
	seen := map[string]bool{}
	var order []string
	for _, s := range staged {
		rest, ok := strings.CutPrefix(s, prefix)
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
//
// --allow-exec is accepted for the same reason `pack lint` takes it, and it had to be added
// because the two disagreeing was worse than either answer: a pack shipping an executable
// LINTED with the flag and then could not be FOOTPRINTED at all (exit 1 on packstage's
// exec-bit refusal), while this command's own help advertises it as the way to inspect a pack
// you are authoring. Only the local-path branch consumes it; an embedded pack is already
// materialized and never re-staged.
func packFootprint(args []string, out, errw io.Writer, color bool) int {
	pr := richtext.Printer{W: out, Color: color}

	arg := ""
	allowExec := false
	for _, a := range args {
		if a == "--allow-exec" {
			allowExec = true
			continue
		}
		arg = a
	}

	if arg != "" {
		// A local path or file:// source → stage + load it, so footprint works on the
		// pack you are AUTHORING, not only the six yolo ships. This is the one command
		// that surfaces a same-into skills collision before boot does, so it must accept
		// a pack that is not yet configured.
		if isLocalPackArg(arg) {
			return packFootprintLocal(arg, allowExec, pr, errw)
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
//
// allowExec is the consumer's half of the exec-bit decision, exactly as in `pack lint`: it
// lets an author inspect a pack whose executable a consenting consumer would stage, rather
// than having the refusal mask the entire report.
func packFootprintLocal(arg string, allowExec bool, pr richtext.Printer, errw io.Writer) int {
	root := strings.TrimPrefix(arg, "file://")
	tmp, err := os.MkdirTemp("", "yolo-pack-footprint-")
	if err != nil {
		fmt.Fprintf(errw, "yolo pack footprint: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	if _, err := packstage.Stage(packstage.Spec{Root: root, Dest: tmp, AllowExec: allowExec}); err != nil {
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
		printClaimLines(pr, fp.Claims)
	}
	// A ONE-PACK report cannot see a cross-pack collision, and the most likely one — a
	// surface a shipped pack already owns — is refused at launch. So the single-pack case
	// checks against the packs yolo ships explicitly. The multi-pack case needs no such
	// help: Collisions below sees the whole set.
	if len(packs) == 1 {
		reportShippedSurfaceClash(pr, packs[0])
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
//
// A count-by-kind is the wrong unit for a HOST-EXECUTION claim — "1 loophole" is the least
// interesting true thing that can be said about a pack that runs a daemon as you — so those
// are counted separately and named for what they do, not for their kind. Everything else
// keeps the compact by-kind shape, which is right for reads.
func reviewSummary(claims []packload.Claim) string {
	byKind := map[string]int{}
	var order []string
	execs := 0
	for _, c := range claims {
		if c.RunsHostCode {
			execs++
			continue
		}
		k := string(c.Kind)
		if byKind[k] == 0 {
			order = append(order, k)
		}
		byKind[k]++
	}
	parts := make([]string, 0, len(order)+1)
	if execs > 0 {
		// FIRST, because it outranks every read in the list.
		parts = append(parts, fmt.Sprintf("%d RUNNING CODE ON YOUR MACHINE", execs))
	}
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
		approved, denied := resolveHostApproval(e.Name, treeRoot, prev, hadPrev, pr,
			approvalStdinFrom(stdin), out)
		if denied {
			// The user declined (or a non-interactive run cannot ask). The pack is still
			// installed and its non-host contributions work; its host claims will be
			// refused at launch until approved. Not an install failure.
			rc = 1
		}
		lock.Set(packsrc.LockEntry{
			Name: e.Name, Source: e.Source, Commit: commit, Ref: addr.Ref,
			ApprovedHostAccess: approved,
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

// approvalStdin is the interactive channel the install-time host-access approval
// prompt reads. reader answers the prompt; isTerminal reports whether that reader is
// a real terminal, and is a seam so tests can drive both branches. A nil isTerminal
// (or one answering false) fails closed: the approval this prompt grants means "this
// pack may read my host or run code on it", and that consent must come from a human
// at a keyboard — `yes | yolo pack install` is not consent (design §4.4 item 3).
type approvalStdin struct {
	reader     io.Reader
	isTerminal func() bool
}

// terminal reports whether the reader is a real terminal; a nil seam fails closed.
func (in approvalStdin) terminal() bool { return in.isTerminal != nil && in.isTerminal() }

// approvalStdinFrom wraps pack install's stdin for the approval gate: isTerminal is
// true only for an *os.File the tty ioctl confirms is a terminal. A pipe or redirect
// (`yes |`, a heredoc, CI) has bytes to offer but no human behind them, and any
// non-File reader cannot be a terminal at all.
func approvalStdinFrom(stdin io.Reader) approvalStdin {
	f, ok := stdin.(*os.File)
	return approvalStdin{
		reader:     stdin,
		isTerminal: func() bool { return ok && tty.IsTerminalFile(f) },
	}
}

// resolveHostApproval decides which host-access claims are approved for a freshly
// materialized fetched pack, prompting the user only when the pack declares a claim
// they have not already approved.
//
// treeRoot is the materialized pack tree. prev/hadPrev are the pack's previous
// lockfile entry. Returns the approved claim set to record, and denied=true when the
// pack WANTS host access the user did not grant (declined, or no terminal to ask at).
//
//   - No host-access claims → approve nothing, denied=false (a pack that reads
//     nothing from the host needs no consent).
//   - Every claim already approved (prev.HostAccessApproved) → carry the prior
//     approval forward silently. This is the "unchanged or narrowed pin" case.
//   - A claim not previously approved with stdin NOT a terminal → refuse without
//     reading a byte (denied=true). Piped input must not be able to answer the one
//     prompt that grants a pack the host.
//   - A claim not previously approved → show the full claim set and prompt y/N. On
//     yes, approve the current set (so a later narrowing is remembered too); on no,
//     keep the prior approvals but do NOT add the new ones (denied=true).
func resolveHostApproval(name, treeRoot string, prev packsrc.LockEntry, hadPrev bool,
	pr richtext.Printer, stdin approvalStdin, out io.Writer) (approved []string, denied bool) {
	p, _ := packload.LoadDir(treeRoot, name, true)
	if p == nil {
		return prevApproved(prev, hadPrev), false
	}
	// EVERY producer's claims, through the ONE merged helper (packload.Pack.HostAccessClaims).
	// pack.json's contributions are only one of three: a WRAPPED PLUGIN's code-running
	// components and a SHIPPED LOOPHOLE's daemon/intercepts/binds/devices are declared in
	// files outside pack.json, so reading only the contributions would let a fetched tree
	// arrive with code to run and nothing to approve.
	//
	// Not appended by hand here, and this is the one gate where that matters most: the union
	// this prompt records into the lockfile must be the SAME union run.packMayAccessHost
	// checks at launch, or approving is either insufficient (a claim the launch demands and
	// this never showed) or vacuous (a claim this recorded and the launch never asks about).
	// Two hand-built unions had already drifted once; hostaccessgates_test.go now fails if
	// either site reaches for a producer directly.
	want := p.HostAccessClaims()
	if len(want) == 0 {
		return nil, false // reads nothing from the host, runs nothing on it
	}
	if hadPrev && prev.HostAccessApproved(want) {
		// Nothing new since the last approval — carry it forward, no prompt. Re-record
		// the CURRENT set so a claim the pack dropped stops being carried.
		return want, false
	}

	// New host access: show the full claim set (not just the delta — the user is
	// approving the whole current footprint) and ask.
	pr.Printf("  [bold yellow]⚠ pack %s reads your host or runs code on it:[/bold yellow]", name)
	for _, c := range want {
		pr.Printf("      [yellow]%s[/yellow]", c)
	}
	if !stdin.terminal() {
		// Refuse BEFORE reading a byte: showing the y/N prompt to a pipe invites the
		// pipe to answer it, and `yes(1)` would. The claims above still print, so a CI
		// log shows exactly what is waiting on a human.
		pr.Printf("  [red]host access NOT approved — approval requires an interactive "+
			"terminal, and stdin is not one. %s will REFUSE TO LAUNCH until its claims "+
			"are approved; rerun `yolo pack install` from a terminal[/red]", name)
		return prevApproved(prev, hadPrev), true
	}
	if !promptYesNo(out, stdin.reader, "  Approve host access for "+name+"? [y/N] ") {
		// Say what actually happens, not what used to. Before OQ-TP6 an unapproved
		// claim was withheld and the jail started with the pack half-loaded, so
		// "will be refused at launch" was accurate. It is not any more: a refused
		// contribution REFUSES THE LAUNCH (docs/design/trust-paths.md §3.1). Leaving
		// the old wording would tell a user their jail still starts, which is the
		// single most expensive thing this message could get wrong — they would find
		// out at the next launch instead of here, where the fix is one command away.
		pr.Printf("  [red]host access NOT approved — %s will REFUSE TO LAUNCH until its "+
			"claims are approved. Run `yolo pack install` and approve, edit the pack so "+
			"it stops asking, or remove it from `packs`[/red]", name)
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

// promptYesNo writes a prompt and reads a single line from stdin, returning true
// only for an explicit yes. A nil stdin (non-interactive, or a test) is a NO — every
// caller's confirmation fails closed. It does NOT check that stdin is a terminal:
// the apply-side confirmations accept a scripted answer by contract, while the
// host-access approval gate requires a real terminal and enforces that BEFORE
// calling here (resolveHostApproval / approvalStdin).
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
		if e.Implicit {
			// Same rule as the builtin above, for the opposite reason: the conventional
			// local pack has no lock entry because nothing FETCHED it — it is a directory
			// the user created, found by convention rather than by a config line. Falling
			// through would print "not installed (run `yolo pack install`)" for a pack the
			// user never installed and cannot install, which is exactly the dead end the
			// builtin case above exists to avoid.
			pr.Printf("%-20s [dim]local (by convention, no install step)[/dim]", e.Name)
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
