package run

// workspaceskills.go is the launcher's half of the workspace skills layer
// (docs/reference/agent-briefings.md): it decides WHICH workspace directories are skills sources
// and how the jail sees the workspace, hands both to jailcontent.PrepareSkillsWith, and SAYS
// what came of it.
//
// It is reached from refreshJailBriefings and nowhere else, which is the point: that function is
// the one every invocation runs — a fresh container launch, an attach to a running jail, and the
// macos-user arm — so the layer is re-staged from the workspace as it stands on EVERY entry
// (OQ-WS1's "refresh on every launch and attach"). The host notch never calls it: `yolo host`
// renders no workspace's skills into a real home, by ruling (OQ-WS5), and writes nothing into the
// workspace for skills either: the host has no workspace skills layer, by the owner's ruling of
// 2026-11-07 (docs/reference/agent-briefings.md WS-D24).

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/skillimports"
)

// workspaceSkillsFor is this launch's workspace layer input, or nil when no pack declares a
// project-scope skills directory at all.
func (o *Options) workspaceSkillsFor(cfg *jsonx.OrderedMap, rt string, selected []*packload.Pack) *jailcontent.WorkspaceSkills {
	dirs := workspaceSkillDirs(selected)
	if o.Workspace == "" || len(dirs) == 0 && (!config.WorkspaceSkillImportsEnabled(cfg) || len(config.SkillImports(cfg)) == 0) {
		return nil
	}
	ws := &jailcontent.WorkspaceSkills{Root: o.Workspace, Dirs: dirs}
	if rt != "macos-user" { // parity: NotApplicable — macos-user's sandbox reads the workspace at its own real path (no second mount path to alias) and applies no per-side shadow, which its orchestrator warns about by name
		// The jail mounts the workspace at jailWorkspace, so an absolute link the agent wrote
		// in there names this root by that spelling; and the per-side shadows are what the
		// jail does NOT see of the host's tree.
		ws.Aliases = []string{jailWorkspace}
		ws.PerSide = perSideShadowRels(cfg, o.Workspace)
	}
	return ws
}

func cloneWorkspaceSkills(ws *jailcontent.WorkspaceSkills) *jailcontent.WorkspaceSkills {
	if ws == nil {
		return nil
	}
	copy := *ws
	copy.Aliases = append([]string(nil), ws.Aliases...)
	copy.Dirs = append([]string(nil), ws.Dirs...)
	copy.PerSide = append([]string(nil), ws.PerSide...)
	copy.Imported = append([]jailcontent.ImportedWorkspaceSkill(nil), ws.Imported...)
	copy.ImportRefused = append([]jailcontent.WorkspaceSkillRefusal(nil), ws.ImportRefused...)
	return &copy
}

func (o *Options) skillImportAuthority(cfg *jsonx.OrderedMap) skillimports.Authority {
	if o.skillImportGatePassed {
		label := gitBrokerScopeLabel(cfg)
		var remotes []string
		for _, scope := range o.approvedScopes[label] {
			remotes = append(remotes, scope.Repo)
		}
		return skillimports.AuthorityFromWorkspace(o.Workspace, remotes, o.approvedGrants[label])
	}
	return skillimports.AuthorityFromWorkspace(o.Workspace, config.ApprovedScope(o.Workspace, "git"),
		config.ApprovedGrants(o.Workspace, "git"))
}

func gitBrokerScopeLabel(cfg *jsonx.OrderedMap) string {
	label := ""
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(cfg)).All() {
		if lp.Brokered == nil || lp.Brokered.Source != "git" {
			continue
		}
		if label != "" {
			return ""
		}
		label = lp.Name
	}
	return label
}

// resolveFreshSkillImports is called only after a fresh-launch config gate succeeds. A failed
// import is disclosed and withheld, never a reason to stop the jail launch.
var resolveSkillImportForLaunch = func(ctx context.Context, store *skillimports.Store, entry config.SkillImport,
	authority skillimports.Authority, now time.Time) (skillimports.Resolution, string, error) {
	return store.ResolveLaunch(ctx, entry, authority, now)
}

func (o *Options) resolveFreshSkillImports(cfg *jsonx.OrderedMap) {
	entries := config.SkillImports(cfg)
	if !config.WorkspaceSkillImportsEnabled(cfg) || !o.skillImportGatePassed || len(entries) == 0 || o.Workspace == "" {
		return
	}
	store, err := skillimports.Open(o.Workspace)
	if err != nil {
		o.pr(o.Stderr).print("[yellow]Workspace skill imports: host state is unavailable; no imported skills were refreshed. Fix the host storage problem, then run `yolo` again.[/yellow]")
		return
	}
	authority := o.skillImportAuthority(cfg)
	for _, entry := range entries {
		_, warning, err := resolveSkillImportForLaunch(context.Background(), store, entry, authority, time.Now())
		if warning != "" {
			o.pr(o.Stderr).print("[yellow]Workspace skills: " + quoteSafe(entry.Handle) + ": " +
				displaySafe(warning) + "; run `yolo skills update " + quoteSafe(entry.Handle) + "` to retry.[/yellow]")
		}
		if err != nil {
			o.pr(o.Stderr).print("[yellow]Workspace skills: import " + quoteSafe(entry.Handle) +
				" was withheld because no broker-authorized commit is available; review this workspace's approved Git scope, " +
				"then run `yolo skills update " + quoteSafe(entry.Handle) + "`.[/yellow]")
		}
	}
}

// projectSkillImports adds only current-record projections; it has no fetch or record mutation
// path. Its cleanup must run after jailcontent has copied the per-skill roots into its confined
// scratch layer.
func (o *Options) projectSkillImports(cfg *jsonx.OrderedMap, ws **jailcontent.WorkspaceSkills) func() {
	entries := config.SkillImports(cfg)
	if !config.WorkspaceSkillImportsEnabled(cfg) || len(entries) == 0 || o.Workspace == "" {
		return func() {}
	}
	if *ws == nil {
		*ws = &jailcontent.WorkspaceSkills{Root: o.Workspace}
	}
	store, err := skillimports.Open(o.Workspace)
	if err != nil {
		for _, entry := range entries {
			(*ws).ImportRefused = append((*ws).ImportRefused, jailcontent.WorkspaceSkillRefusal{
				Path: "skill_imports." + entry.Handle, Reason: "host import state is unavailable",
			})
		}
		return func() {}
	}
	var projections []*skillimports.Projection
	var budget skillimports.ProjectionBudget
	authority := o.skillImportAuthority(cfg)
	for _, entry := range entries {
		projection, err := store.ProjectWithBudget(entry, authority, &budget)
		if err != nil {
			(*ws).ImportRefused = append((*ws).ImportRefused, jailcontent.WorkspaceSkillRefusal{
				Path: "skill_imports." + entry.Handle, Reason: "the stored import could not be projected safely",
			})
			continue
		}
		projections = append(projections, projection)
		for _, skill := range projection.Skills {
			(*ws).Imported = append((*ws).Imported, jailcontent.ImportedWorkspaceSkill{
				Handle: skill.Handle, Name: skill.Name, Root: skill.Root,
			})
		}
		for _, refusal := range projection.Refused {
			(*ws).ImportRefused = append((*ws).ImportRefused, jailcontent.WorkspaceSkillRefusal{
				Path: refusal.Path, Reason: refusal.Reason,
			})
		}
		for _, missing := range projection.Missing {
			(*ws).ImportRefused = append((*ws).ImportRefused, jailcontent.WorkspaceSkillRefusal{
				Path:   "skill_imports." + entry.Handle + "/" + missing,
				Reason: "the configured skill is absent from the selected commit",
			})
		}
	}
	return func() {
		for _, projection := range projections {
			projection.Close()
		}
	}
}

// workspaceSkillDirs is the SOURCE SET's declaration: every project-scope skills directory any
// pack declares on a skills destination (packdecl's `project_dirs`), in a fixed order.
//
// EVERY SHIPPED PACK, SELECTED OR NOT — the OQ-WS3 ruling: "if we get a repository with files in
// it that are of a path that are related to skills, I want them in my agent". A repo that ships
// `.agents/skills/` reaches a codex-only jail because copilot, opencode, agy and pi declare that
// path, and it would be a strange rule that made selecting a pack a precondition for honoring a
// convention its agent defines. Core still names no path (P2): the packs declare, and this reads.
//
// The SELECTED packs are read too, first: a configured agent pack yolo does not ship knows where
// its own agent reads, and its destination's skip rule already names that path — leaving the
// path out of the source set would make the skip rule the only place it counted.
//
// THE ORDER IS THE COLLISION RULE (a same-named skill in two source dirs goes to the first):
// the selected packs in config order, then the rest of the shipped packs by name, each pack's own
// paths in its agent's precedence order, each path at its first appearance.
func workspaceSkillDirs(selected []*packload.Pack) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p *packload.Pack) {
		if p == nil || p.Decl == nil {
			return
		}
		for _, d := range p.Decl.ProjectSkillDirs() {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	for _, p := range selected {
		add(p)
	}
	shipped := append([]*packload.Pack(nil), packload.Embedded()...)
	sort.SliceStable(shipped, func(i, j int) bool { return shipped[i].Name < shipped[j].Name })
	for _, p := range shipped {
		add(p)
	}
	return out
}

// perSideShadowRels is the part of the per-side shadow set venvShadowMountArgs actually MOUNTS:
// the workspace paths whose host copy the jail never sees. Computed by the same rules and without
// its warnings, which that function prints once per launch already.
func perSideShadowRels(cfg *jsonx.OrderedMap, workspace string) []string {
	var out []string
	for _, rel := range perSideShadowCandidates(cfg, workspace) {
		if !ValidPerSideRel(rel) {
			continue
		}
		hostPath := filepath.Join(workspace, rel)
		if isSymlink(hostPath) || (fileExists(hostPath) && !isDir(hostPath)) {
			continue // not shadowed: the jail sees the host's entry there
		}
		out = append(out, rel)
	}
	return out
}

// noteWorkspaceSkills prints what the workspace layer did, to stderr.
//
// NOT SUPPRESSIBLE — a launch has no quiet mode (OQ-RO3) — and SILENT WHEN THERE IS NOTHING TO
// SAY, which is every workspace with no skills directory: an empty report prints nothing, so the
// lines that do print are read. Stderr for the reason noteLaunchFlagInjection gives and one more:
// this runs on an attach too, whose stdout belongs to its "Attaching" line and the command after.
//
// Six kinds of line, each one fact:
//
//   - a REFUSAL per entry not read or not staged (P5's escaping symlink above all), naming the
//     entry and never its target;
//   - a COLLISION per skill name two source dirs both carry, naming which copy is delivered and
//     which agents read a losing copy natively instead;
//   - a HELD-BACK line per skill some agents were not sent because a directory they read natively
//     carries that name, when no collision line says so already;
//   - a SHADOW per workspace skill a built-in, pack or local-pack skill took the name of — one
//     line per name, however many destinations (OQ-WS2);
//   - a COMPETING line per workspace skill an agent reads natively under a name a higher layer
//     also gives it, which the mirror cannot prevent;
//   - a MIRROR line per source dir that delivered anything, naming the destinations and the
//     skills — the line R6 of the design leans on, since an attach re-stages the workspace as it
//     stands into a live session.
//
// Every workspace-supplied string is passed through displaySafe, the refusal's REASON included:
// these lines are a disclosure, and a skill directory a clone names `"\n[dim]nothing refused"`
// must not be able to forge one.
func (o *Options) noteWorkspaceSkills(r *jailcontent.WorkspaceSkillsReport) {
	if r.Empty() {
		return
	}
	out := o.pr(o.Stderr)
	// The PACK layers' lines first: what the host's writer would not deliver, and each reserved
	// child it withheld (OQ-NC11). A pack's own directory names the entry, so it is rendered as
	// safely as a workspace's.
	for _, n := range r.PackNotices {
		out.print(fmt.Sprintf("[yellow]Skills: %s in %s: %s[/yellow]", quoteSafe(n.Name),
			strings.Join(n.In, ", "), displaySafe(n.Detail)))
	}
	for _, f := range r.Refused {
		out.print("[yellow]Workspace skills: refused " + displaySafe(f.Path) + " — " + displaySafe(f.Reason) + "[/yellow]")
	}
	for _, c := range r.Collisions {
		losers := make([]string, len(c.Losers))
		for i, l := range c.Losers {
			losers[i] = displaySafe(l)
		}
		line := fmt.Sprintf("Workspace skills: %s is in both %s and %s — the copy in %s is the one "+
			"delivered", quoteSafe(c.Name), displaySafe(c.Winner), strings.Join(losers, " and "),
			displaySafe(c.Winner))
		for _, n := range c.ReadNatively {
			line += fmt.Sprintf("; %s %s the copy in %s natively and %s sent no other",
				strings.Join(n.By, ", "), plural(len(n.By), "reads", "read"), displaySafe(n.Source),
				plural(len(n.By), "is", "are"))
		}
		out.print("[yellow]" + line + "[/yellow]")
	}
	for _, h := range r.HeldBack {
		out.print(fmt.Sprintf("[yellow]Workspace skills: %s from %s was not sent to %s, which %s a "+
			"skill of that name in %s natively[/yellow]", quoteSafe(h.Name), displaySafe(h.From),
			strings.Join(h.In, ", "), plural(len(h.In), "reads", "read"), displaySafe(h.Native)))
	}
	for _, s := range r.Shadowed {
		out.print(fmt.Sprintf("[yellow]Workspace skills: %s from %s is shadowed by %s, so it was not "+
			"delivered to %s[/yellow]", quoteSafe(s.Name), displaySafe(s.Source),
			strings.Join(s.By, " and "), strings.Join(s.In, ", ")))
	}
	for _, c := range r.Competing {
		out.print(fmt.Sprintf("[yellow]Workspace skills: %s %s %s natively, so yolo cannot keep it "+
			"from competing with %s of that name[/yellow]", strings.Join(c.Readers, ", "),
			plural(len(c.Readers), "reads", "read"), displaySafe(c.Source+"/"+c.Name),
			strings.Join(c.With, " and ")))
	}
	for _, m := range r.Mirrored {
		names := make([]string, len(m.Skills))
		for i, n := range m.Skills {
			names[i] = displaySafe(n)
		}
		out.print(fmt.Sprintf("[dim]Workspace skills from %s mirrored into %s: %s[/dim]",
			displaySafe(m.Source), strings.Join(m.To, ", "), strings.Join(names, ", ")))
	}
}

// plural picks one or many by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// displaySafe renders a workspace-supplied path or name for a disclosure line: verbatim when it
// is plain printable text, and Go-quoted otherwise — with `[` escaped too, since a bracketed
// word is console markup here and a name like `[/yellow]` would restyle the line it sits in.
func displaySafe(s string) string {
	plain := s != ""
	for _, r := range s {
		if r == '[' || r == ']' || !unicode.IsPrint(r) || r == '"' {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return quoteSafe(s)
}

// quoteSafe is strconv.Quote with `[` escaped, for the reason displaySafe gives.
func quoteSafe(s string) string {
	return strings.ReplaceAll(strconv.Quote(s), "[", `\x5b`)
}
