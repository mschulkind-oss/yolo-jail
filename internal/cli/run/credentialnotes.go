package run

// credentialnotes.go is the credential gate's DISCLOSURE on the jail notch
// (docs/design/provider-credential-scope.md §4, "no silent narrowing"): a launch that
// withholds a credential the user configured says so, and one that scopes it says to whom.
// Names only, never a value. A disclosure rather than a debug line, so it has no quiet
// switch (docs/reference/report-tiers.md, OQ-RO3).

import (
	"sort"
	"strings"
)

// noteCredentialScope prints the gate's disclosure to stderr — every arm that delivers a
// channel calls it beside the delivery: the fresh container launch, the attach, and the
// macos-user arm. Silent when env_sources hydrated no credential a provider claims.
func (o *Options) noteCredentialScope(channel *packChannel) {
	if channel == nil || channel.scope == nil {
		return
	}
	lines := channel.scope.Disclosure()
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[dim]" + lines[0] + "[/dim]")
	for _, l := range lines[1:] {
		out.print(l)
	}
}

// noteMacosUserCredentialScope says what this backend's per-LAUNCH delivery costs, when it
// costs anything: another profiled agent has values of its own that this invocation, which
// starts `launched`, cannot carry to it (launchEnv's doc — OQ-CN6's "a vehicle that cannot
// express per-agent delivery stays per launch and says so as a disclosure").
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
	out.print("[yellow]Credential scope on macos-user is per launch:[/yellow] this invocation " +
		"starts " + launched + ", so only its own scoped values ride the sandbox session, and " +
		strings.Join(others, ", ") + " started inside it receive none of theirs. Launch each " +
		"agent as its own `yolo -- <agent>` to give it its credentials.")
}
