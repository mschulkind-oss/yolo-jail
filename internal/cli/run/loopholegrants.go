package run

import (
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// loopholegrants.go is the LAUNCH half of docs/reference/loophole-grants.md (§4.1, §4.4,
// §4.6, §5): what each selected grant-declaring loophole holds in this workspace, and the four
// places the launch says so — the grant file its daemon is handed (publishLoopholeSettings), the
// container argv that carries an unmediated crossing only under a grant (loopholesRuntimeArgs,
// jailDaemonsFor), the pack disclosure (noteLoopholeGrants) and the briefing (briefingLoopholes).
//
// A "grant-declaring" loophole is one whose manifest has `[[grants]]` (loopholedecl.Grant). One
// that declares none is ungated, and nothing here touches it.

// grantStandings is each grant-declaring loophole's standing per kind, by loophole name.
type grantStandings = map[string]map[string]config.LoopholeGrantStanding

// loopholeGrantStandings is what each selected grant-declaring loophole holds for this launch:
// the config-change gate's own result once it has run (Options.approvedLoopholeGrants, WW-P2),
// else the approval record as it stands, read through the collector the gate and `yolo check`
// share (CollectScopeCheck). The second source serves the readers that run before the gate (the
// served set, jailDaemonsFor) and a launch with no gate (an attach); it is never what a grant file
// is written from (loopholeGrantBlock). nil when no grant-declaring loophole is selected, or when
// the config cannot be read, which grants nothing.
func (o *Options) loopholeGrantStandings(rt string, cfg *jsonx.OrderedMap) grantStandings {
	if o.approvedLoopholeGrants != nil {
		return o.approvedLoopholeGrants
	}
	if len(grantDeclaring(loopholes.NewHostSet(loopholes.LaunchBlock(cfg)))) == 0 {
		return nil
	}
	read, err := config.ReadWorkspaceForGate(o.Workspace)
	if err != nil {
		return nil
	}
	check := CollectScopeCheck(ScopeInput{Workspace: o.Workspace, Merged: cfg, Runtime: rt,
		Allow: o.loopholeAllow(rt, cfg), Read: read})
	if check == nil || check.Fatal != nil {
		return nil
	}
	byPack := check.LoopholeGrantStandings(o.Workspace)
	out := grantStandings{}
	for _, src := range check.LoopholeGrants {
		out[src.Loophole] = byPack[src.Pack]
	}
	return out
}

// grantDeclaring is every selected pack loophole in set that declares grants, in the set's order.
func grantDeclaring(set loopholes.Set) []*loopholes.Loophole {
	var out []*loopholes.Loophole
	for _, lp := range set.All() {
		if lp.Source == loopholes.SourcePack && len(lp.Grants) > 0 {
			out = append(out, lp)
		}
	}
	return out
}

// grantedKinds is the kinds standings hold granted, by loophole name, as loopholes.Set.WithGrants
// takes them.
func grantedKinds(standings grantStandings) map[string][]string {
	out := map[string][]string{}
	for name, kinds := range standings {
		for kind, st := range kinds {
			if st.Standing == config.LoopholeGrantGranted {
				out[name] = append(out[name], kind)
			}
		}
		sort.Strings(out[name])
	}
	return out
}

// recomposeForApprovedGrants is this launch's jail-daemon payload and channel as the
// config-change gate that just passed left them: unchanged when the gate approved the grant kinds
// the composition read from the approval record (composedGrants), else composed again over the
// gate's result. Run composes both above the backend dispatch, before either arm's gate, and the
// argv reads the gate's result (loopholesRuntimeArgs through loopholeGrantStandings), so without
// this the launch whose gate first records a grant bound a loophole and withheld every pointer
// `served_by` it, or the reverse for a grant the gate dropped. The pack env a launch serves must
// agree with the binds its own argv carries, and the disclosures read the served set off the
// channel, so they say what this launch holds too.
//
// The second composition takes the first one's hydrated env_sources (channel.userEnv), so no
// env_sources command runs twice, and this process's caller tokens and served addresses, which
// are settled once per process (launchCallerTokens, settleServedAddresses). A sealed launch holds
// no grant and composes nothing.
func (o *Options) recomposeForApprovedGrants(cfg *jsonx.OrderedMap, rt string, packs []*packload.Pack,
	channel *packChannel, jailDaemons []loopholes.JailDaemonSpec) (*packChannel, []loopholes.JailDaemonSpec, error) {
	if o.Sealed || channel == nil ||
		reflect.DeepEqual(grantedKinds(o.loopholeGrantStandings(rt, cfg)), o.composedGrants) {
		return channel, jailDaemons, nil
	}
	recomposed, err := o.composePackChannel(cfg, packs, channel.userEnv)
	if err != nil {
		return nil, nil, err
	}
	if err := o.resolveJailGrant(recomposed); err != nil {
		return nil, nil, err
	}
	return recomposed, o.jailDaemonsFor(cfg, rt, packs), nil
}

// withLaunchGrants is set narrowed by what this launch holds: an unmediated grant-declaring
// loophole's crossing, and any grant-declaring loophole's binds and devices, reach the argv only
// under a grant (loopholes.Set.WithGrants).
func (o *Options) withLaunchGrants(set loopholes.Set, rt string, cfg *jsonx.OrderedMap) loopholes.Set {
	return set.WithGrants(grantedKinds(o.loopholeGrantStandings(rt, cfg)))
}

// ungrantedBoundWhy is the clause, by loophole name, for every active grant-declaring BOUND
// LOOPHOLE in set (no jail daemon; host binds or devices) that holds no grant in standings: its
// binds stay out of the jail (loopholes.Set.WithGrants), so a pack env pointer `served_by` it is
// withheld, and the line naming the pointer says what grants it (packload.WithUngrantedBinds)
// instead of the `enabled` switch, which grants nothing to such a loophole. nil when none.
func ungrantedBoundWhy(set loopholes.Set, standings grantStandings) map[string]string {
	granted := grantedKinds(standings)
	var out map[string]string
	for _, lp := range grantDeclaring(set) {
		if lp.JailDaemon != nil || (len(lp.HostBindMount) == 0 && len(lp.HostDevices) == 0) ||
			len(granted[lp.Name]) > 0 || !lp.Active() {
			continue
		}
		clause := summarizeGrants(lp, standings[lp.Name]).unboundClause(lp)
		if clause == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[lp.Name] = clause
	}
	return out
}

// unboundClause is the clause for a pointer at what lp would bind, holding no grant: the standing
// and the step that grants one, worded as the disclosure's (disclosureLines).
func (s grantSummary) unboundClause(lp *loopholes.Loophole) string {
	if len(s.granted) > 0 || s.standing == "" {
		return ""
	}
	pack := packOf(lp)
	switch s.standing {
	case config.LoopholeGrantPending:
		return "it holds no grant in this project yet (its request for " + s.kind.Kind +
			" waits on `yolo config apply` on the host, answered y)"
	case config.LoopholeGrantRequestable:
		return "it holds no grant in this project (`" + requestCommand(pack, s.kind) + "` asks for one)"
	case config.LoopholeGrantNotAllowed:
		if s.withheld {
			return "it holds no grant in this project, which turns it off (to take it: " + withheldStep(pack) + ")"
		}
		fallthrough
	default:
		return "it holds no grant in this project (`yolo allow " + pack + " on_request " + s.kind.Kind +
			"`, then `" + requestCommand(pack, s.kind) + "`)"
	}
}

// loopholeGrantBlock is lp's grant block for this launch's grant file (PD-2): this launch's
// identity, the manifest's `access` line, and every declared kind with its standing, line and
// value noun.
//
// FROM THE GATE'S RESULT ONLY. A launch whose gate recorded nothing for lp (a spawn path that
// skipped the gate) FAILS CLOSED, as writeScopeFiles does: the record's standings are read, and a
// kind it holds granted is written pending, so the daemon refuses and names the step.
func (o *Options) loopholeGrantBlock(lp *loopholes.Loophole, cname string) loopholegrant.Block {
	standings, fromGate := o.approvedLoopholeGrants[lp.Name]
	if !fromGate {
		standings = o.loopholeGrantStandings(o.runtime, nil)[lp.Name]
	}
	// A launch id that could not be drawn leaves the field empty; Merge refuses nothing for it,
	// and the daemon keys its live-file check on the pid and container too.
	launchID, _ := brokerscope.NewLaunchID()
	b := loopholegrant.Block{Pack: lp.Pack, Workspace: o.Workspace, Container: cname,
		LaunchID: launchID, PID: os.Getpid(), Access: lp.Access,
		Kinds: grantFileKinds(lp, standings, fromGate)}
	if b.Pack == "" {
		b.Pack = lp.Name
	}
	return b
}

// grantFileKinds is the grant block's kinds for lp from standings: every declared kind with its
// standing, line and value noun. A kind granted where fromGate is false is written pending. The
// launch (loopholeGrantBlock) and `yolo config apply` (liveapplygrants.go) both write through it,
// so a live rewrite and a launch's file compare equal for the same approval.
func grantFileKinds(lp *loopholes.Loophole, standings map[string]config.LoopholeGrantStanding, fromGate bool) map[string]loopholegrant.Kind {
	kinds := make(map[string]loopholegrant.Kind, len(lp.Grants))
	for _, g := range lp.Grants {
		st, ok := standings[g.Kind]
		standing := loopholegrant.Standing(st.Standing)
		if !ok || !standing.Valid() {
			standing = loopholegrant.NotAllowed
		}
		if standing == loopholegrant.Granted && !fromGate {
			standing = loopholegrant.Pending
		}
		k := loopholegrant.Kind{Standing: standing, Text: g.Text}
		if g.Valued {
			k.Values = append([]string(nil), st.Values...)
			k.ValueNoun = g.ValueNoun
		}
		kinds[g.Kind] = k
	}
	return kinds
}

// loopholeInertEnv is the jail-env variable (paths.LoopholeInertEnv) of every selected
// grant-declaring loophole this setup runs not at all, set to the inert report's reason, so its
// client says that instead of a refusal it cannot get (plan PD-8). Two axes are this launch's to
// name: the backend (one Apple Container does not admit) and the platform. A loophole a nested
// launch inherits is served by the launching jail and is not inert. Empty when none is.
func (o *Options) loopholeInertEnv(rt string, cfg *jsonx.OrderedMap) map[string]string {
	allow := o.loopholeAllow(rt, cfg)
	out := map[string]string{}
	for _, lp := range grantDeclaring(loopholes.NewHostSet(loopholes.LaunchBlock(cfg))) {
		name := paths.LoopholeInertEnv(lp.Name)
		if name == "" {
			continue
		}
		reason := ""
		if !allow(lp.Name) {
			reason = backendInertReason(rt)
		} else if why, unsupported := lp.UnsupportedHereReason(); unsupported {
			reason = why
		}
		if reason != "" {
			out[name] = reason
		}
	}
	return out
}

// loopholeInertEnvArgs is loopholeInertEnv as container `-e` pairs, sorted by name.
func (o *Options) loopholeInertEnvArgs(rt string, cfg *jsonx.OrderedMap) []string {
	env := o.loopholeInertEnv(rt, cfg)
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	var args []string
	for _, name := range names {
		args = append(args, "-e", name+"="+env[name])
	}
	return args
}

// grantSummary is what one grant-declaring loophole holds, as the disclosure and the briefing
// say it: the kinds granted (with any values), or else the one kind whose step is shown, chosen
// pending over requestable over not_allowed, in declaration order.
type grantSummary struct {
	granted  []string
	kind     loopholedecl.Grant
	standing string
	// withheld: the chosen kind is not_allowed because this project turns the pack's grants off
	// (config.LoopholeGrantStanding.Withheld), so the step is the project's, not the user's.
	withheld bool
}

// withheldStep is the step that takes a withheld pack's grants in this project: the project's
// own off, which it removes.
func withheldStep(pack string) string {
	return "remove this project's `requests." + pack + " = \"off\"` (or, in a legacy yolo-jail.jsonc, its `\"enabled\": false` under loopholes)"
}

func summarizeGrants(lp *loopholes.Loophole, kinds map[string]config.LoopholeGrantStanding) grantSummary {
	var s grantSummary
	rank := map[string]int{config.LoopholeGrantPending: 3, config.LoopholeGrantRequestable: 2,
		config.LoopholeGrantNotAllowed: 1}
	best := 0
	for _, g := range lp.Grants {
		st := kinds[g.Kind]
		standing := st.Standing
		if standing == config.LoopholeGrantGranted {
			label := g.Kind
			if len(st.Values) > 0 {
				label += " (" + strings.Join(st.Values, ", ") + ")"
			}
			s.granted = append(s.granted, label)
			continue
		}
		if rank[standing] == 0 {
			standing = config.LoopholeGrantNotAllowed
		}
		if rank[standing] > best {
			best, s.kind, s.standing, s.withheld = rank[standing], g, standing, st.Withheld
		}
	}
	return s
}

// requestCommand is `yolo request <pack> <kind>`, with a placeholder for a valued kind's values.
func requestCommand(pack string, g loopholedecl.Grant) string {
	cmd := "yolo request " + pack + " " + g.Kind
	if g.Valued {
		noun := g.ValueNoun
		if noun == "" {
			noun = "values"
		}
		cmd += " <" + noun + ">"
	}
	return cmd
}

// packOf is the pack a loophole's grants are spelled under (`allow.<pack>`, `requests.<pack>`).
func packOf(lp *loopholes.Loophole) string {
	if lp.Pack != "" {
		return lp.Pack
	}
	return lp.Name
}

// disclosureLines is the pack disclosure's lines for lp (§5): one dim line naming the grants in
// force, or, holding none, the standing and the step that grants it. Nothing for no grants
// declared.
func (s grantSummary) disclosureLines(lp *loopholes.Loophole) []string {
	if len(s.granted) > 0 {
		return []string{"[dim]  " + richtext.Escape(lp.Name+": grants for this workspace: "+strings.Join(s.granted, ", ")) + "[/dim]"}
	}
	if s.standing == "" {
		return nil
	}
	verb := "selected"
	if lp.Mediated() {
		verb = "running"
	}
	pack := packOf(lp)
	var why, step string
	switch s.standing {
	case config.LoopholeGrantPending:
		why = "this project's request for " + s.kind.Kind + " is not approved yet"
		step = "The human runs, on the host, in this workspace:  yolo config apply   and answers y"
	case config.LoopholeGrantRequestable:
		why = "not requested by this project"
		step = "To ask for this project:  " + requestCommand(pack, s.kind)
	case config.LoopholeGrantNotAllowed:
		if s.withheld {
			why = "this project turns it off"
			step = "To take it here:  " + withheldStep(pack)
			break
		}
		fallthrough
	default:
		why = "not allowed on request by your config"
		step = "To let this project ask:  yolo allow " + pack + " on_request " + s.kind.Kind +
			"   then  " + requestCommand(pack, s.kind)
	}
	return []string{
		"  " + richtext.Escape(lp.Name+": "+verb+", grants nothing here ("+why+")."),
		"    " + richtext.Escape(step),
	}
}

// briefingLine is the briefing's one line for lp (§5), "" when it holds a grant: the agent reads
// that it holds nothing in this project and the step that would grant it, and so does not debug a
// socket that was never bound.
func (s grantSummary) briefingLine(lp *loopholes.Loophole) string {
	if len(s.granted) > 0 || s.standing == "" {
		return ""
	}
	pack := packOf(lp)
	switch s.standing {
	case config.LoopholeGrantPending:
		return "It grants nothing in this project yet: this project's request for `" + s.kind.Kind +
			"` waits on the human, who runs `yolo config apply` on the host, in this workspace, and answers y."
	case config.LoopholeGrantRequestable:
		return "It grants nothing in this project, which has not asked. To ask: `" + requestCommand(pack, s.kind) +
			"`, then the human runs `yolo config apply` on the host, in this workspace, and answers y."
	case config.LoopholeGrantNotAllowed:
		if s.withheld {
			return "It grants nothing in this project, which turns it off. To take it here: " + withheldStep(pack) +
				", then the human runs `yolo config apply` on the host, in this workspace, and answers y."
		}
		fallthrough
	default:
		return "It grants nothing in this project, and the user's config does not allow asking. The human " +
			"allows it on the host: `yolo allow " + pack + " on_request " + s.kind.Kind + "`; then `" +
			requestCommand(pack, s.kind) + "`, and the human runs `yolo config apply` and answers y."
	}
}

// noteLoopholeGrants prints, in the pack disclosure block, what each selected grant-declaring
// loophole holds in this workspace (§5): the grants in force, or the standing and the next step.
// An inert one is left to the inert report, which already says it does nothing here.
func (o *Options) noteLoopholeGrants(rt string, cfg *jsonx.OrderedMap) {
	lps := grantDeclaring(loopholes.NewHostSet(loopholes.LaunchBlock(cfg)))
	if len(lps) == 0 {
		return
	}
	standings := o.loopholeGrantStandings(rt, cfg)
	inert := o.loopholeInertEnv(rt, cfg)
	var lines []string
	for _, lp := range lps {
		if _, isInert := inert[paths.LoopholeInertEnv(lp.Name)]; isInert {
			continue
		}
		lines = append(lines, summarizeGrants(lp, standings[lp.Name]).disclosureLines(lp)...)
	}
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[dim]Loophole grants this launch:[/dim]")
	for _, l := range lines {
		out.print(l)
	}
}
