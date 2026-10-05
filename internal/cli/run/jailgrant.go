package run

// jailgrant.go is --with-credentials AT A JAIL LAUNCH (docs/design/credential-sources-separation.md
// OQ-ES5's jail half, ruled 2026-10-05; ES-D31 to ES-D36). The maintainer: "The jail shouldn't be
// able to discover credentials from outside that it wasn't launched with. But you should be able
// to run a jail with whatever set of credentials you want. Like that should be the same."
//
// So the flag is the host's (§5.1: the named providers' CLAIMED env_sources values, keys only, no
// profile routing, disclosed by name on every entry, an unknown provider refused naming the known
// ones, a named provider with no value reported, combining with -p, implied by nothing), resolved
// by the resolver the host reads it with (packload.ResolveGrant), and the set is fixed when the
// jail is launched: the jail holds exactly the credentials it was launched with and can fetch no
// others later.
//
// WHO HOLDS IT. Not one process, as at the host, but the jail: every process in it, every session
// attached to it later and everything each starts (ES-D31). That is what "the granted set is the
// jail's" reads as, and it is why the grant is NOT a recipient of the gate here (the host's
// ScopeInput.Grants): a gate delivery is written into the per-agent env files every entry
// rewrites, under <workspace>/.yolo/home, which is both a file the next attach replaces and a
// copy of the credential on disk in the workspace.
//
// THE VEHICLES (ES-D32), one per notch, none of them a file the workspace holds:
//
//   - podman, and Apple Container, which shares the argv: each granted NAME as `-e NAME` on the
//     container's argv, and the value in the environment of the runtime client that starts the
//     container (the keeper's startJailMainWithEnv), which takes a bare `-e NAME`'s value from its
//     own environment. So no value is on an argv, the container's frozen environment holds the
//     set for the jail's life, and every `exec` session inherits it. The values cross from the
//     launch to its keeper in the keeper's plan (keeperPlan.GrantEnv: 0600, in a 0700 directory
//     of its own, read once and removed), as the merged config's inline env_sources already do.
//   - macos-user: the launch env, which the backend writes into the root-owned per-session env
//     file (macosuser.SandboxEnvFile), never into the per-agent env files the arm writes under
//     <workspace>/.yolo/home (writeMacosUserAgentEnvFiles, which reads the gate's channel and
//     never this grant).
//
// THE ATTACH (ES-D33). The jail's grant is recorded, names only, in its keeper's start record
// (keeperRecord.Grant), which an attach reads: a later session holds the jail's set and is told
// so, and an attach asking for a provider or a name the running jail was not launched with is
// refused, naming the fresh launch. macos-user has no attach: every invocation is a session of its
// own, launched with its own flags (sessionfiles.go), so each one's grant is its own.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// jailGrant is a --with-credentials request resolved over one launch's composition. Its exported
// fields are NAMES ONLY, and they are exactly what a keeper's start record carries
// (keeperRecord.Grant), so a record can never hold a value: the values are env, which is
// unexported, so no encoding reaches it.
type jailGrant struct {
	// Spelled is the request as typed, comma-joined, for the disclosure to quote back.
	Spelled string `json:"spelled"`
	// Providers is every provider the grant names, `all` expanded, sorted.
	Providers []string `json:"providers,omitempty"`
	// Granted is what each provider delivered, names only (packload.GrantedProvider).
	Granted []grantedProvider `json:"granted,omitempty"`
	// env is the granted values, in hydration order; nil on a grant read back from a record.
	env *jsonx.OrderedMap
}

// grantedProvider is packload.GrantedProvider with the record's field names.
type grantedProvider struct {
	Provider  string   `json:"provider"`
	Delivered []string `json:"delivered,omitempty"`
	Claims    []string `json:"claims,omitempty"`
}

// resolveJailGrant resolves this launch's --with-credentials request over its composed channel,
// into o.jailGrant (nil without the flag). An unknown provider is the error, in the host's words
// (packload.UnknownGrantError), which the launch refuses with before anything starts. A sealed
// build carries none: no typed flag reaches one.
func (o *Options) resolveJailGrant(channel *packChannel) error {
	o.jailGrant = nil
	if len(o.WithCredentials) == 0 || o.Sealed || channel == nil {
		return nil
	}
	envSources, _ := config.SplitHydratedEnvSources(channel.userEnv)
	providers, err := packload.ResolveGrant(o.WithCredentials, channel.providers, envSources)
	if err != nil {
		return err
	}
	delivered, values := channel.scope.GrantFor(providers)
	g := &jailGrant{Spelled: strings.Join(o.WithCredentials, ","), Providers: providers,
		env: jsonx.NewOrderedMap()}
	for _, d := range delivered {
		g.Granted = append(g.Granted, grantedProvider(d))
	}
	for _, k := range values.Keys() {
		if v, _ := values.Get(k); v != nil {
			if s, ok := v.(string); ok {
				g.env.Set(k, s)
				continue
			}
			g.env.Set(k, fmt.Sprint(v))
		}
	}
	o.jailGrant = g
	return nil
}

// names is every name the grant holds, in its providers' order, each once.
func (g *jailGrant) names() []string {
	if g == nil {
		return nil
	}
	if g.env != nil {
		return g.env.Keys()
	}
	var out []string
	for _, p := range g.Granted {
		for _, n := range p.Delivered {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// holds reports whether the grant holds name.
func (g *jailGrant) holds(name string) bool { return slices.Contains(g.names(), name) }

// grants reports whether the grant names provider.
func (g *jailGrant) grants(provider string) bool {
	return g != nil && slices.Contains(g.Providers, provider)
}

// envArgs is the container argv's half of the grant: `-e NAME` for each granted name, NEVER its
// value (ES-D32). The runtime client takes the value from its own environment (envPairs).
func (g *jailGrant) envArgs() []string {
	var out []string
	for _, n := range g.names() {
		out = append(out, "-e", n)
	}
	return out
}

// envPairs is the runtime client's half: NAME=VALUE for each granted name, for the environment of
// the one process that starts the container, and no other.
func (g *jailGrant) envPairs() []string {
	if g == nil || g.env == nil {
		return nil
	}
	var out []string
	for _, k := range g.env.Keys() {
		v, _ := g.env.Get(k)
		s, _ := v.(string)
		out = append(out, k+"="+s)
	}
	return out
}

// applyTo sets each granted value on a launch env: the macos-user arm's vehicle, whose launch env
// the backend writes into the root-owned per-session env file.
func (g *jailGrant) applyTo(env *jsonx.OrderedMap) {
	if g == nil || g.env == nil || env == nil {
		return
	}
	for _, k := range g.env.Keys() {
		v, _ := g.env.Get(k)
		env.Set(k, v)
	}
}

// grantHolder is who holds a jail grant on runtime rt, for the gate's lines.
func grantHolder(rt string) string {
	if rt == "macos-user" { // parity: HonoredBy — a container jail's grant is held by every process of the jail; macos-user's by every process of its one session, which is its own launch
		return "every process of this session"
	}
	return "every process in this jail"
}

// grantDisclosureNotes are the gate disclosure's notes for the grant this entry's processes hold
// (o.heldGrant): a name it holds is named as every process's, never as withheld. The zero notes
// without one.
func (o *Options) grantDisclosureNotes() packload.DisclosureNotes {
	g := o.heldGrant
	if g == nil {
		return packload.DisclosureNotes{}
	}
	return packload.DisclosureNotes{Granted: g.holds, GrantHolder: grantHolder(o.runtime)}
}

// grantEntry is which entry a grant's disclosure describes.
type grantEntry int

const (
	// grantFreshJail: a container launch that starts the jail holding the grant.
	grantFreshJail grantEntry = iota
	// grantAttach: a session attached to a running jail, which holds the grant its launch named.
	grantAttach
	// grantMacosUserSession: a macos-user session, which is its own launch.
	grantMacosUserSession
)

// noteHeldGrant prints the disclosure of the grant this entry's processes hold (o.heldGrant), on
// every entry that holds one, a grant that delivered nothing included: a header saying what the
// grant is, who holds it, and that everything the command starts inherits it (CN-D8), then one line
// per granted provider (packload.GrantProviderLines). Names only, never a value. A disclosure, so
// it has no quiet switch (OQ-RO3). launched is the macos-user session's program, and profiled
// whether some agent of this entry keeps a profile beside the grant.
func (o *Options) noteHeldGrant(entry grantEntry, launched string, profiled bool) {
	g := o.heldGrant
	if g == nil {
		return
	}
	var header string
	switch entry {
	case grantAttach:
		header = fmt.Sprintf("Credential grant (this jail was launched with --with-credentials %s): it "+
			"holds the granted providers' claimed env_sources values as they were at its launch, keys "+
			"only — the grant selects no profile and re-points nothing — and this session and "+
			"everything it starts inherit them", g.Spelled)
		if o.jailGrant != nil {
			header += fmt.Sprintf("; this entry's --with-credentials %s asks for nothing more",
				o.jailGrant.Spelled)
		}
	case grantMacosUserSession:
		header = fmt.Sprintf("Credential grant (--with-credentials %s): this session receives the "+
			"granted providers' claimed env_sources values, keys only — the grant selects no profile "+
			"and re-points nothing — and %s and everything it starts inherit them", g.Spelled, launched)
	default:
		header = fmt.Sprintf("Credential grant (--with-credentials %s): this jail holds the granted "+
			"providers' claimed env_sources values for its whole life, keys only — the grant selects "+
			"no profile and re-points nothing — and every process in it inherits them: this session, "+
			"every session attached to it later, and everything each one starts", g.Spelled)
	}
	if profiled {
		header += "; each agent keeps its profile, and the grant only adds keys beside it"
	}
	out := o.pr(o.Stderr)
	out.print(richtext.Escape(header))
	var delivered []packload.GrantedProvider
	for _, p := range g.Granted {
		delivered = append(delivered, packload.GrantedProvider(p))
	}
	for _, l := range packload.GrantProviderLines(delivered) {
		out.print(richtext.Escape(l))
	}
}

// channelProfiled reports whether some agent of the channel keeps a profile, for the grant's
// "each agent keeps its profile" clause.
func channelProfiled(channel *packChannel) bool {
	return channel != nil && channel.profiles != nil && channel.profiles.Len() > 0
}

// runningJailGrant is the grant the running jail named cname was launched with, read from its
// keeper's start record: nil when the record names none, and known false when there is no record
// to read.
func runningJailGrant(cname string) (g *jailGrant, known bool) {
	rec, ok := readKeeperRecord(cname)
	if !ok {
		return nil, false
	}
	return rec.Grant, true
}

// refuseGrantTheJailLacks is the attach's half of the ruling (ES-D33): an attach that asks for a
// provider, or a name, the running jail was not launched with is refused, naming the fresh launch,
// because a running jail takes no credentials later. A subset of the jail's set, or the same set,
// passes, and so does an attach that asks for nothing. Compared by provider and by name, never by
// value: the record holds no values. known is whether the jail's record could be read; a jail whose
// record cannot be read holds nothing this attach can prove, so any request is refused.
func (o *Options) refuseGrantTheJailLacks(cname string, running *jailGrant, known bool) bool {
	req := o.jailGrant
	if req == nil {
		return false
	}
	var lacks []string
	for _, p := range req.Granted {
		if !running.grants(p.Provider) {
			if len(p.Delivered) > 0 {
				lacks = append(lacks, p.Provider+" ("+strings.Join(p.Delivered, ", ")+")")
			} else {
				lacks = append(lacks, p.Provider)
			}
			continue
		}
		for _, n := range p.Delivered {
			if !running.holds(n) {
				lacks = append(lacks, n+" (provider "+p.Provider+")")
			}
		}
	}
	if len(lacks) == 0 && known {
		return false
	}
	launchedWith := "no --with-credentials grant"
	if running != nil {
		launchedWith = "--with-credentials " + running.Spelled
	}
	if !known {
		launchedWith = "a grant this attach cannot read (its keeper's start record is missing)"
	}
	why := "it does not hold " + strings.Join(lacks, ", ")
	if len(lacks) == 0 {
		why = "nothing shows it holds them"
	}
	relaunch, together := req.Spelled, ""
	if running != nil {
		relaunch, together = unionSpelling(running.Spelled, req.Spelled), " (the jail's grant and this entry's together)"
	}
	cmd := "yolo --with-credentials " + shquote.Quote(relaunch)
	if len(o.Args) > 0 {
		cmd += " -- " + shquoteJoin(o.Args)
	}
	out := o.pr(o.Stderr)
	out.printf("[bold red]%s[/bold red]", richtext.Escape(fmt.Sprintf("Refusing to attach: this entry "+
		"asks for --with-credentials %s, and the running jail (%s) was launched with %s, so %s. A jail "+
		"holds exactly the credentials it was launched with and takes no others later.",
		req.Spelled, cname, launchedWith, why)))
	out.print(richtext.Escape(fmt.Sprintf("  To run with them, launch the jail fresh: %s, then `%s`%s. "+
		"An attach naming the jail's own set, or part of it, or no grant at all, enters the running "+
		"jail.", stopRemedy("", cname), cmd, together)))
	return true
}

// unionSpelling is a's comma list followed by each of b's entries a lacks.
func unionSpelling(a, b string) string {
	parts := strings.Split(a, ",")
	for _, p := range strings.Split(b, ",") {
		if !slices.Contains(parts, p) {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ",")
}
