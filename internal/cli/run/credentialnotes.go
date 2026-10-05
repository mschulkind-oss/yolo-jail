package run

// credentialnotes.go is the credential gate's DISCLOSURE on the jail notch
// (docs/reference/providers.md, "no silent narrowing"): a launch that
// withholds a credential the user configured says so, and one that scopes it says to whom; and
// one where a source of yolo's own beat another for a name says which (OQ-NC12's disclosure).
// Names only, never a value. A disclosure rather than a debug line, so it has no quiet
// switch (docs/reference/report-tiers.md, OQ-RO3).

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// noteCredentialScope prints the gate's disclosure to stderr — every arm that delivers a
// channel calls it beside the delivery: the fresh container launch, the attach, and the
// macos-user arm. In order: what this notch does not serve, the region fill, what each name's
// winning source shadowed (noteShadowedEnv), then the scope itself, which is silent when
// env_sources hydrated no credential a provider claims.
func (o *Options) noteCredentialScope(channel *packChannel) {
	if channel == nil || channel.scope == nil {
		return
	}
	o.noteUnserved(channel)
	out := o.pr(o.Stderr)
	// THE REGION FILL'S DISCLOSURE (docs/design/bedrock-plumbing.md BR-DIR1): a region the gate
	// read from the host's region file for an agent is named, with the file and the profile, in
	// the words the host notch prints (packload's RegionLines).
	for _, l := range channel.scope.RegionLines() {
		out.print(richtext.Escape(l))
	}
	o.noteShadowedEnv(channel)
	// The one disclosure renderer, the host's too; the jail adds no notes (ES-D2 keeps its
	// wording until OQ-ES5 decides whether a jail shell has a remedy).
	lines := channel.scope.DisclosureWith(packload.DisclosureNotes{})
	if len(lines) == 0 {
		return
	}
	out.print("[dim]" + lines[0] + "[/dim]")
	for _, l := range lines[1:] {
		out.print(l)
	}
}

// noteShadowedEnv is OQ-NC12's disclosure on the jail notch (packload's envshadow.go; ruled
// 2026-10-05): one line per variable for which one of yolo's own sources beat another that set it
// otherwise, naming the winner and every loser, never a value, over every process the launch
// composes for (the shared composition and each profiled agent's). Silent when nothing is
// shadowed. The three wire tables are left out, because every jail vehicle writes them after the
// composition. Called from noteCredentialScope, so every arm that delivers a channel says it: the
// fresh container launch (podman and Apple Container), the attach, and the macos-user arm.
func (o *Options) noteShadowedEnv(channel *packChannel) {
	out := o.pr(o.Stderr)
	for _, l := range channel.scope.ShadowLines(entrypoint.WireTables()) {
		head, rest, _ := strings.Cut(l, ": ")
		out.print("[dim]" + richtext.Escape(head) + ":[/dim] " + richtext.Escape(rest))
	}
}

// noteUnserved names what this launch withheld because nothing at its notch serves it — a pack
// env variable pointing at a jail daemon the launch does not run, a profile's via — in the
// words the host notch prints too (packload.UnservedLines; docs/plans/notch-convergence.md §4
// item 2, P4). On a container launch whose selected loopholes all run, that is nothing; on
// macos-user it names every pointer the declined daemons would have served. A disclosure, so
// no quiet switch (OQ-RO3). Called from noteCredentialScope, so every arm that delivers a
// channel says it.
func (o *Options) noteUnserved(channel *packChannel) {
	lines := packload.UnservedLines(channel.scope, channel.unservedVias, nil)
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[yellow]" + lines[0] + "[/yellow]")
	for _, l := range lines[1:] {
		out.print("[yellow]" + l + "[/yellow]")
	}
}

// noteMacosUserCredentialScope says how this backend delivers when another profiled agent has
// values of its own: the session env carries the launched program's (launchEnv), and every
// other agent reads its own from its env file when it is started inside the session — the
// per-agent files the arm writes (writeMacosUserAgentEnvFiles, OQ-CN9). Before OQ-CN9 such an
// agent received none of its values and this line said so; it now names where they come from.
func (o *Options) noteMacosUserCredentialScope(channel *packChannel, launched string) {
	if channel == nil || channel.scope == nil {
		return
	}
	var others []string
	for _, agent := range channel.scope.Agents() {
		if agent != launched && !channel.scope.Agent(agent).Empty() {
			others = append(others, agent)
		}
	}
	if len(others) == 0 {
		return
	}
	sort.Strings(others)
	out := o.pr(o.Stderr)
	out.print("[dim]Credential scope on macos-user: this invocation starts " + launched +
		", whose own scoped values ride the sandbox session; " + strings.Join(others, ", ") +
		" started inside it read theirs from their own env files " +
		"(provider-credential-scope.md OQ-CN9).[/dim]")
}
