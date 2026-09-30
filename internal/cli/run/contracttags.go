package run

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// contracttags.go is the attach's CONTRACT GATE: before an attach hands a running jail
// anything, it asks whether that jail's binaries can receive it, and when they cannot the
// attach never proceeds on its own (docs/design/attach-skew-and-contract-guardrails.md,
// OQ-SK1 to OQ-SK3, ruled 2026-09-26).
//
// WHY AN ATTACH NEEDS ONE. A running jail keeps the binaries it launched with: its
// /opt/yolo-jail/bin is a bind of one immutable flake-bundle generation, and nothing may change
// it under a live pid1. The host yolo that attaches may be newer, and an attach is an ENTRY
// rather than a reconnect: it writes the per-entry channel (deliverChannelOnAttach) and re-runs
// the jail's boot. So a newer host can hand an older jail something that jail's launchers or
// boot cannot read. Before this gate the result was one of two things. Either a silent
// ride-along: a line said the jail would run without what this entry selected, and the session
// started anyway. Or a failure later, somewhere that named neither half.
//
// CONTRACT TAGS (the doc's "named capability tags", OQ-SK2). Every container launch freezes the
// tags its binaries implement into entrypoint.ContractTagsEnv; an attach computes the tags IT
// needs from what it would deliver (attachContractFor), and a tag the jail lacks is a missing
// contract. Named tags and no version matrix: a tag is added by the change that adds the
// contract and read by the attach that depends on it, with no central counter for two branches
// to collide on.
//
// THE DISPOSITION (OQ-SK1, OQ-SK3). A missing tag never proceeds on its own, and neither does
// the other skew an attach can find, a running jail whose packs cannot serve this entry: a tree
// this build cannot read, or a selection that fails over the packs the jail has
// (packtree.go's attachPackSkew). Both are known differences, worded by contractSkew and
// packSkew, and both take the same three arms:
//
//   - AllowAttachSkewEnv set: the one acknowledgment. The attach proceeds, says loudly on
//     stderr what differs and what is withheld, and degrades on the host: this entry's channel
//     is not written, so the jail keeps what its last entry gave it.
//   - a terminal on stdin and stdout: `Restart jail now? [Y/n]`, naming the sessions a stop
//     ends. Yes stops the jail and this launch continues as a fresh one; no refuses.
//   - anything else: refuse, naming `yolo stop` (`container stop` on Apple Container, where
//     `yolo stop` cannot see the jail) and then a launch.
//
// WHAT THIS OVERRULES. The config-only arms this replaced warned and proceeded because
// "refusing here would hold a workspace's day-to-day re-entry hostage to a one-time upgrade".
// The ruling weighed exactly that and chose the restart ("not crazy to restart jails after an app
// upgrade"), so the typed/config split is gone: both ask, and both refuse without a terminal.
// The remedy is always the restart series, `yolo stop` then a launch, and never the removed
// `--new`, which force-removed a RUNNING container and its live sessions without asking.
//
// NO OTHER OVERRIDE IMPLIES THE ACKNOWLEDGMENT. The ruling's words: "if you pass an override
// flag acknowledging it, that's fine, but it shouldn't just silently ride along, even if there's
// another similar override flag". So this file reads AllowAttachSkewEnv and nothing else, and
// TestNoOtherHatchAcknowledgesAttachSkew sets every other YOLO_* variable the tree spells, and
// every override-style Options field.

// AllowAttachSkewEnv is the acknowledgment that lets an attach proceed into a jail missing a
// contract this entry needs. Named in the refusal, in the house style of every YOLO_ALLOW_*
// hatch: a refusal says how to overrule it, and what overruling it costs.
const AllowAttachSkewEnv = "YOLO_ALLOW_ATTACH_SKEW"

// The contract tags. Each names one thing a later attach may need a running jail to do, spelled
// once here and frozen by every launch into the container (launchContractTags).
const (
	// contractEntryChannel: the jail's boot applies the per-entry channel file over its
	// launch-time environment, so a provider/profile selection made at an attach takes effect.
	// A jail launched before per-entry delivery froze its provider tables into the container
	// instead, and its hydrate lets that frozen environment beat the file (OQ-CS6's pre-change
	// jail). LEGACY inference, for a jail with no tags: present unless the jail's environment
	// holds a frozen YOLO_PROVIDERS, the signature every such launch left.
	contractEntryChannel = "entry-channel"
	// contractAgentEnvFiles: the jail's launchers source ~/.config/yolo-agent-env/<agent>.sh, so
	// a value the credential gate scopes to one agent reaches that agent
	// (provider-credential-scope.md, CN-D7 and CN-D10). LEGACY inference, for a jail with no
	// tags: present when entrypoint.AgentEnvFilesEnv is set, the marker the gate froze before
	// this list existed.
	contractAgentEnvFiles = "agent-env-files"
	// contractProfileSets: the jail's boot reads a YOLO_USE_PROFILES value that is a LIST, an
	// agent's active set (docs/design/active-provider-sets.md AP-D8), and hands the whole set to
	// its derives as ctx.active_set. An older boot lowers a list to no selection at all, so an
	// agent on a set would start with none of it. No legacy inference: a jail that predates the
	// tag list predates sets too.
	contractProfileSets = "profile-sets"
	// contractSessionHangup: the jail's entrypoint records each session an exec names in
	// entrypoint.SessionIDEnv, and its entrypoint.HangupSessionArg form ends that session's
	// processes (sessionhangup.go). An attach's signal arm asks it when the attach's terminal
	// closes or its launcher is signalled. No attach NEEDS it: a jail without it is attached to as
	// before, and its arm says it cannot hang the session up. No legacy inference: a jail that
	// predates the tag list predates the hangup too.
	contractSessionHangup = "session-hangup"
)

// launchContractTags is every contract THIS build's jail implements, frozen into every
// container it launches (assemble.go's env block). The host writes its own set because the jail
// it launches runs binaries built from the same source: the mounted prefix comes from the flake
// this launch resolved, and the source-skew gate refuses a launch whose two halves differ.
//
// ADDING A CONTRACT is up to three edits, all in this file: the tag's constant, its entry
// here, and, only when some attach depends on it, a need in attachContractFor. A contract no
// attach asks about still belongs here, since the tag is what a LATER build's attach will read
// to learn this jail has it. The per-launch pack trees of pack-system.md's OQ-PK2 needed none: an
// attach no longer writes any pack tree, so it asks nothing of the jail's binaries, and it finds
// the tree an older jail binds on the host side (packtree.go's runningJailPackTree).
var launchContractTags = []string{contractEntryChannel, contractAgentEnvFiles, contractProfileSets,
	contractSessionHangup}

// launchContractTagsValue is ContractTagsEnv's value for a launch: the tags, comma-joined.
func launchContractTagsValue() string { return strings.Join(launchContractTags, ",") }

// jailContractTags reads a running jail's contract tags from its container-inspect env
// listing. known is false when the listing proves nothing: an inspect that failed, or a
// runtime whose inspect does not answer. Such a jail is treated as current, the "cannot prove,
// do not refuse" rule the credential gate's attach probe followed before this gate replaced it.
//
// A listing that carries ContractTagsEnv is authoritative, even when empty. One without it
// comes from a jail launched before the tags, and each tag is inferred from the marker its
// contract left then (the legacy notes on each constant).
func jailContractTags(envLines []string) (tags map[string]bool, known bool) {
	if !inspectedEnv(envLines) {
		return nil, false
	}
	tags = map[string]bool{}
	if raw, ok := envLineLookup(envLines, entrypoint.ContractTagsEnv); ok {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags[t] = true
			}
		}
		return tags, true
	}
	if envLineValue(envLines, "YOLO_PROVIDERS") == "" {
		tags[contractEntryChannel] = true
	}
	if envLineValue(envLines, entrypoint.AgentEnvFilesEnv) != "" {
		tags[contractAgentEnvFiles] = true
	}
	return tags, true
}

// inspectedEnv reports whether a container-inspect env listing holds anything at all.
func inspectedEnv(envLines []string) bool {
	for _, l := range envLines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

// envLineLookup returns the value of KEY= in a container-inspect env listing, and whether the
// key is there at all, so a present-but-empty value is told apart from an absent one.
func envLineLookup(envLines []string, key string) (string, bool) {
	for _, l := range envLines {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// contractNeed is one contract tag an attach needs, with what it withholds without it.
type contractNeed struct {
	tag string
	// lacks says, in the disclosure, what a jail without the tag cannot do.
	lacks string
	// withheld names what this entry would have delivered through the contract. Names only,
	// never a value: the disclosure is printed and teed to the launch log.
	withheld []string
}

// attachContract is what one attach asks of the running jail.
type attachContract struct {
	// needs are the tags this attach depends on; the jail's tags are compared against them.
	needs []contractNeed
	// standIn: the jail's launch-time delivery already is this entry's, so this entry writes
	// nothing and needs nothing. It is the plain re-entry into a jail launched before per-entry
	// delivery, with the selection it was launched with or with none. The first cut of that
	// jail's check refused here, which broke every plain attach of a config carrying a
	// persistent selection, `use_profiles` then and the `profile` key now (measured on a live
	// jail, 2026-09-05).
	standIn bool
}

// attachContractFor computes the contract tags THIS attach needs, from the channel it would
// deliver and, for a jail that froze its selection at launch, that frozen selection.
//
//   - contractEntryChannel whenever this entry delivers a profile selection: the selection only
//     takes effect in a jail whose boot applies the file over its launch-time environment.
//   - contractAgentEnvFiles whenever the credential gate scoped anything to an agent: only a
//     jail whose launchers source the per-agent file delivers it to that agent. Delivering the
//     shared half alone would strip every scoped credential and shape variable from the agents
//     that selected them while the disclosure named them as its recipients (CN-D18).
func attachContractFor(channel *packChannel, envLines []string) attachContract {
	var profiles *jsonx.OrderedMap
	if channel != nil {
		profiles = channel.profiles
	}
	selects := profiles != nil && profiles.Len() > 0
	frozen := frozenUseProfiles(envLines)
	if envLineValue(envLines, "YOLO_PROVIDERS") != "" && (!selects || profileTablesEqual(frozen, profiles)) {
		return attachContract{standIn: true}
	}
	var c attachContract
	if selects {
		line := "the profile selection " + selectionPhrase(profiles)
		if frozen != nil && frozen.Len() > 0 {
			line += " (the jail keeps " + selectionPhrase(frozen) + ", frozen at its launch)"
		}
		c.needs = append(c.needs, contractNeed{
			tag: contractEntryChannel,
			lacks: "it froze its providers into the container at launch, and its boot lets that " +
				"frozen environment beat a later entry's selection",
			withheld: []string{line},
		})
	}
	// A SET OF MORE THAN ONE ENTRY needs a boot that reads a list (AP-D8); a table whose every
	// set has one entry crosses as the strings it always did and needs nothing.
	if lists := listedSets(profiles); len(lists) > 0 {
		c.needs = append(c.needs, contractNeed{
			tag: contractProfileSets,
			lacks: "its boot reads one profile per agent, so an agent's list of profiles would " +
				"reach it as no selection at all",
			withheld: lists,
		})
	}
	if scoped := channel.agentsWithOwnValues(); len(scoped) > 0 {
		var withheld []string
		for _, agent := range scoped {
			withheld = append(withheld, scopedValuesPhrase(channel, agent))
		}
		c.needs = append(c.needs, contractNeed{
			tag:      contractAgentEnvFiles,
			lacks:    "its launchers read no per-agent env file, so a value scoped to one agent cannot reach that agent",
			withheld: withheld,
		})
	}
	return c
}

// missingFrom returns the needs whose tag the jail lacks, in need order. Nil when the jail has
// every tag, or when its env listing proves nothing (jailContractTags).
func (c attachContract) missingFrom(envLines []string) []contractNeed {
	tags, known := jailContractTags(envLines)
	if !known {
		return nil
	}
	var missing []contractNeed
	for _, n := range c.needs {
		if !tags[n.tag] {
			missing = append(missing, n)
		}
	}
	return missing
}

// selectionPhrase renders a selection table as "cli=profile" pairs, sorted, for a disclosure. A
// set of more than one is spelled as `-p` spells it, "pi=zai,openrouter".
func selectionPhrase(m *jsonx.OrderedMap) string {
	if m == nil || m.Len() == 0 {
		return "(none)"
	}
	pairs := make([]string, 0, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		s, _ := v.(string)
		if set, ok := packload.ProfileSetValue(v); ok {
			s = strings.Join(set, ",")
		}
		pairs = append(pairs, k+"="+s)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ", ")
}

// listedSets names each agent in a selection table whose active set has more than one entry,
// "the profile list pi=zai,openrouter", sorted: what an attach withholds from a jail that cannot
// read a list. Nil when every set has one entry.
func listedSets(m *jsonx.OrderedMap) []string {
	var out []string
	for agent, set := range packload.ProfileSets(m) {
		if len(set) > 1 {
			out = append(out, "the profile list "+agent+"="+strings.Join(set, ","))
		}
	}
	sort.Strings(out)
	return out
}

// scopedValuesPhrase names what the credential gate scoped to one agent: "claude (profile
// zai): ANTHROPIC_AUTH_TOKEN, ZAI_API_KEY". Variable NAMES only; a shape variable the derive
// unsets is not a value this entry delivers and is left out.
func scopedValuesPhrase(channel *packChannel, agent string) string {
	d := channel.scope.Agent(agent)
	seen := map[string]bool{}
	var names []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			names = append(names, k)
		}
	}
	if d != nil {
		if d.EnvSources != nil {
			for _, k := range d.EnvSources.Keys() {
				add(k)
			}
		}
		for k := range d.PackEnv {
			add(k)
		}
		for _, v := range d.Shape {
			if !v.Unset && packdecl.ValidEnvName(v.Key) {
				add(v.Key)
			}
		}
	}
	sort.Strings(names)
	label := agent
	if d != nil && d.Profile != "" {
		label += " (profile " + d.Profile + ")"
	}
	return label + ": " + strings.Join(names, ", ")
}

// skewDisposition is how settleAttachSkew resolved a missing contract.
type skewDisposition int

const (
	// skewAcknowledged: AllowAttachSkewEnv was set; the attach proceeds and delivers nothing.
	skewAcknowledged skewDisposition = iota + 1
	// skewRestarted: the user chose the restart and the jail is stopped and gone; the caller
	// continues into the fresh launch, still holding the launch lock.
	skewRestarted
	// skewRefused: the attach stops here with rc 1; the refusal is already printed.
	skewRefused
)

// attachSkew is one reason an attach cannot deliver what this entry would into the running jail,
// worded for the disposition. Two kinds reach it: a missing contract tag (contractSkew) and a pack
// set the jail cannot serve this entry from (packSkew, packtree.go).
type attachSkew struct {
	// jail completes the sentence "this jail …": what is wrong with it for this entry.
	jail string
	// lines say what differs, one indented line each.
	lines []string
	// keeps, when set, extends the acknowledgment's "the channel is withheld whole" with what
	// else the jail keeps as its last entry left it.
	keeps string
}

// contractSkew words missing contract tags for the disposition.
func (o *Options) contractSkew(baked string, missing []contractNeed) attachSkew {
	return attachSkew{
		jail:  "was launched by an older yolo and cannot receive what this entry delivers",
		lines: o.attachSkewLines(baked, missing),
	}
}

// settleAttachSkew applies the disposition to an attach that cannot deliver what this entry
// would: a jail missing a contract tag it needs, or one whose packs cannot serve it. It never
// returns "proceed as normal": every outcome is the acknowledgment, a restart, or a refusal.
func (o *Options) settleAttachSkew(cname, rt string, skew attachSkew) skewDisposition {
	if o.Getenv(AllowAttachSkewEnv) != "" {
		o.discloseAcknowledgedAttachSkew(cname, rt, skew)
		return skewAcknowledged
	}
	if o.IsTTYStdin() && o.IsTTYStdout() {
		if !o.askToRestartJail(cname, rt, skew) {
			err := o.pr(o.Stderr)
			err.print("[bold red]Refusing to attach: the jail was not restarted.[/bold red]")
			err.printf("[dim]%s once its sessions are done, then launch again; or attach without "+
				"what it cannot receive: %s=1.[/dim]", stopRemedy(rt, cname), AllowAttachSkewEnv)
			return skewRefused
		}
		if !o.restartJailForAttach(cname, rt) {
			return skewRefused
		}
		return skewRestarted
	}
	o.refuseAttachSkew(cname, rt, skew)
	return skewRefused
}

// versionSkewLines is the two versions, when both are known and differ, or the jail's alone.
func (o *Options) versionSkewLines(baked string) []string {
	host := o.yoloVersion("")
	switch {
	case baked != "" && host != "" && host != "unknown" && host != baked:
		return []string{fmt.Sprintf("  This jail runs yolo %s; this launcher is %s.", baked, host)}
	case baked != "":
		return []string{fmt.Sprintf("  This jail runs yolo %s.", baked)}
	}
	return nil
}

// attachSkewLines words what differs: the two versions when both are known, then each missing
// contract and what it withholds.
func (o *Options) attachSkewLines(baked string, missing []contractNeed) []string {
	lines := o.versionSkewLines(baked)
	for _, n := range missing {
		lines = append(lines, fmt.Sprintf("  • It lacks %s: %s.", n.tag, n.lacks))
		for _, w := range n.withheld {
			lines = append(lines, "      withheld: "+w)
		}
	}
	return lines
}

// refuseAttachSkew is the non-interactive refusal, in the credential gate's attach style: the
// headline, what differs, then the restart and the acknowledgment, each with its cost.
func (o *Options) refuseAttachSkew(cname, rt string, skew attachSkew) {
	out := o.pr(o.Stderr)
	out.printf("[bold red]Refusing to attach: this jail %s.[/bold red]", skew.jail)
	for _, l := range skew.lines {
		out.print(l)
	}
	out.printf("[dim]Restart the jail to pick it up: %s, then launch again — the next launch is "+
		"fresh. The stop ends %s.[/dim]", stopRemedy(rt, cname), o.jailSessionsPhrase(rt, cname))
	out.printf("[dim]Or attach without it, acknowledging the difference: %s=1. This entry then "+
		"delivers nothing, and the jail keeps the environment its last entry gave it.[/dim]", AllowAttachSkewEnv)
}

// askToRestartJail is the terminal arm: what differs, what a restart ends, and the question.
// Enter means yes (the ruled `[Y/n]`); end of input means no, since nobody answered.
func (o *Options) askToRestartJail(cname, rt string, skew attachSkew) bool {
	out := o.pr(o.Stdout)
	out.printf("[bold yellow]⚠  This jail %s.[/bold yellow]", skew.jail)
	for _, l := range skew.lines {
		out.print(l)
	}
	out.printf("Restarting stops the jail, ending %s, and this launch then starts it fresh.",
		o.jailSessionsPhrase(rt, cname))
	return tty.Confirm(o.Stdout, o.Stdin, "Restart jail now? [Y/n] ", true)
}

// discloseAcknowledgedAttachSkew is the acknowledgment's loud line: what differs, what is
// withheld, and that the jail keeps what its last entry gave it.
func (o *Options) discloseAcknowledgedAttachSkew(cname, rt string, skew attachSkew) {
	out := o.pr(o.Stderr)
	out.printf("[bold yellow]⚠  %s is set: attaching to this jail, which %s.[/bold yellow]",
		AllowAttachSkewEnv, skew.jail)
	for _, l := range skew.lines {
		out.print(l)
	}
	out.printf("[bold yellow]This entry delivers nothing: its provider/profile channel is withheld "+
		"whole%s, and the jail keeps the environment its last entry gave it. %s, then a launch, "+
		"ends the difference.[/bold yellow]", skew.keeps, stopRemedy(rt, cname))
}

// jailSessionsPhrase words what stopping the jail ends: every session in it, with the count
// when the runtime can tell.
func (o *Options) jailSessionsPhrase(rt, cname string) string {
	n, ok := o.jailSessionCount(rt, cname)
	if !ok {
		return "every session in it"
	}
	return fmt.Sprintf("every session in it (%d running now)", n)
}

// jailSessionCount counts the sessions in a running jail, for DISPLAY ONLY: it decides nothing
// (the session lock does, sessionlock.go), because it also counts an exec whose terminal is gone.
// Each live exec is a session, which is how every session of a jail whose main process is a
// hold enters, the first included (entrypoint.JailMainEnv). A jail launched before that ran
// its first session AS its main process, which is one more. podman drops an exec session when
// it exits, so ExecIDs lists the live ones (measured on podman 5.8.6: a finished exec is gone
// from it, a running one stays). No runtime is special-cased: an inspect that fails or answers
// anything but a count — Apple Container's has not been measured — reads as "cannot tell", and
// the phrase then names every session without a number.
func (o *Options) jailSessionCount(rt, cname string) (int, bool) {
	if o.Exec == nil {
		return 0, false
	}
	res := o.Exec([]string{rt, "inspect", "--format", "{{len .ExecIDs}}", cname}, "", nil, 3*time.Second)
	if !res.Ran || res.RC != 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil || n < 0 {
		return 0, false
	}
	if envLineValue(o.inspectContainerEnv(rt, cname), entrypoint.JailMainEnv) != entrypoint.JailMainHold {
		n++ // the main process is the first session
	}
	return n, true
}

// restartPollAttempts and restartPollInterval bound how long a restart waits for the stopped
// container to be removed: its launcher ran it with --rm, so it goes once that launcher's
// runtime notices the exit. Counted in attempts rather than against o.Now, which tests pin, and
// variables only so a test of the did-not-stop arm need not wait fifteen real seconds.
var (
	restartPollAttempts = 75
	restartPollInterval = 200 * time.Millisecond
)

// restartJailForAttach stops the running jail so this launch can continue as a fresh one. It
// returns true only once the container is gone, because the fresh launch that follows creates
// a container of the same name. The caller holds the workspace launch lock throughout, which is
// what keeps the stopped jail's own teardown from removing the host-services dir this launch is
// about to publish into (stopLoopholes leaves the dir to a launch that holds the lock).
func (o *Options) restartJailForAttach(cname, rt string) bool {
	o.pr(o.Stdout).printf("[bold cyan]Stopping %s; this launch then starts it fresh...[/bold cyan]", cname)
	o.stopJail(cname, rt, attachRestartReason(o.Getpid()))
	for i := 0; i < restartPollAttempts; i++ {
		if id, answered := o.probeExistingContainer(cname, rt, 5*time.Second); answered && id == "" {
			return true
		}
		time.Sleep(restartPollInterval)
	}
	// Still there. A stopped leftover is removed the way the fresh path removes any stale one;
	// a container that is still RUNNING did not stop, and launching beside it cannot work.
	if o.findRunningContainer(cname, rt) == "" && o.removeStaleContainer(cname, rt) {
		return true
	}
	o.pr(o.Stderr).printf("[bold red]Refusing to launch: %s did not stop.[/bold red] "+
		"[dim]Try %s, then launch again.[/dim]", cname, stopRemedy(rt, cname))
	return false
}
