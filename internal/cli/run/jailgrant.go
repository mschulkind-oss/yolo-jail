package run

// jailgrant.go is --with-credentials AT A JAIL LAUNCH (docs/design/credential-sources-separation.md
// OQ-ES5's jail half, ruled 2026-10-05; ES-D31 to ES-D39). The maintainer: "The jail shouldn't be
// able to discover credentials from outside that it wasn't launched with. But you should be able
// to run a jail with whatever set of credentials you want. Like that should be the same."
//
// So the flag is the host's (§5.1: the named providers' CLAIMED env_sources values, keys only, no
// profile routing, disclosed by name on every entry, an unknown provider refused naming the known
// ones, a named provider with no value reported, combining with -p, implied by nothing), resolved
// by the resolver the host reads it with (packload.ResolveGrant), and THE GRANT is fixed when the
// jail is launched: no later entry adds to it. What this file does NOT govern is a profile: an
// attach's `-p <profile>` still delivers that profile's provider key into the agent's own env file
// (deliverChannel's per-entry delivery, which predates the ruling), so a running jail can hold a
// key it was not launched with that way, pending the maintainer's ruling on it (ES-D39).
//
// WHO HOLDS IT. Not one process, as at the host, but the jail: every process the boot and each
// session's entrypoint start, so every session attached later and everything each starts
// (ES-D31). That is what "the granted set is the jail's" reads as, and it is why the grant is NOT
// a recipient of the gate here (the host's ScopeInput.Grants): a gate delivery is written into
// the per-agent env files every entry rewrites, which is a file the next attach replaces.
//
// THE VEHICLES (ES-D32 as revised by ES-D37), one per notch:
//
//   - podman: a per-launch grant file, plain `export K='v'` lines, 0600 in a 0700 directory of the
//     launcher's own state outside the workspace (paths.AgentsDir()/<cname>/grant), written once by
//     the fresh launch (stageJailGrant) and bound `:ro` at ~/.config/yolo-grant-env.sh, which every
//     boot and session reads into its environment (entrypoint's hydrateEnvFromGrantFile). NEVER a
//     name or a value as `-e`: podman resolves an `-e` into the container's configuration, its
//     inspect output, its exec files and its database, which outlive the container (MEASURED by the
//     security review). No granted name or value reaches a host process's environment either.
//   - Apple Container: the same file, copied into the jail home it binds whole (acMaterialize's
//     choice for a file read at boot: a bind below its `:ro` floor would be writable), with the host
//     copy kept for an attach to read.
//   - macos-user: the launch env, which the backend writes into the root-owned per-session env
//     file (macosuser.SandboxEnvFile), never into the per-agent env files the arm writes under
//     <workspace>/.yolo/home (writeMacosUserAgentEnvFiles, which reads the gate's channel and
//     never this grant).
//
// The files go with the jail: when its container is known gone (forgetGone's
// forgetGoneCredentials, ES-D38), or at once for a launch whose container never started
// (discardUnheldJailGrant).
//
// THE ATTACH (ES-D33). The jail's grant is recorded, names only, in its keeper's start record
// (keeperRecord.Grant), which an attach reads: a later session holds the jail's set and is told
// so, and an attach asking for a provider or a name the running jail was not launched with in its
// grant is refused, naming the fresh launch. macos-user has no attach: every invocation is a
// session of its own, launched with its own flags (sessionfiles.go), so each one's grant is its own.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

// fileContent renders the grant's per-launch file: one plain `export K='v'` line per granted
// name, in hydration order, in the grammar every env file of this backend uses (exportPlain), so
// the in-jail reader (entrypoint's hydrateEnvFromGrantFile) parses it as it parses the rest.
func (g *jailGrant) fileContent() string {
	if g == nil || g.env == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Auto-generated by yolo: this jail's --with-credentials grant, written once at its launch\n")
	b.WriteString("# and removed with the jail (credential-sources-separation.md ES-D37). Keys only.\n")
	for _, k := range g.env.Keys() {
		v, _ := g.env.Get(k)
		s, _ := v.(string)
		b.WriteString(exportPlain(k, s))
	}
	return b.String()
}

// grantFileValues reads a grant file's values back, for an attach's view of what yolo put in a
// session's environment (attachExisting: channel.bootEnv). Nil when there is no file.
func grantFileValues(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, val, _, ok := entrypoint.ParseExportLine(line); ok {
			out[key] = val
		}
	}
	return out
}

// THE GRANT FILE'S PLACES (ES-D37). The host copy is the launcher's own: a 0600 file in a 0700
// directory under the jail's per-name state (paths.AgentsDir()/<cname>/grant), outside the
// workspace and outside every mount but its own bind. The in-jail path is the jail home's
// entrypoint.JailGrantFileRel on both container backends.
const (
	jailGrantDirLeaf  = "grant"
	jailGrantFileLeaf = "grant.env"
)

// jailGrantHostFile is cname's host copy of its grant file.
func jailGrantHostFile(cname string) string {
	return filepath.Join(paths.AgentsDir(), cname, jailGrantDirLeaf, jailGrantFileLeaf)
}

// jailGrantHomeRel is where the grant file sits in a jail's home as the host sees it, beneath
// wsState: on podman the mountpoint its bind leaves in the per-workspace `config` dir that
// /home/agent/.config binds (empty: the bind covers it); on Apple Container, which binds wsState
// at /home/agent and ignores `:ro` below its floor, the copy itself (acMaterialize's choice).
func jailGrantHomeRel(rt string) string {
	if rt == "container" { // parity: HonoredBy — Apple Container reads a copy in the jail home it binds whole; podman binds the host copy :ro over the mountpoint its .config bind holds
		return entrypoint.JailGrantFileRel
	}
	return filepath.Join("config", filepath.Base(entrypoint.JailGrantFileRel))
}

// stageJailGrant writes this fresh launch's grant file before its container starts, and only on a
// fresh launch: an attach never touches it, so no later entry can change what the jail holds. A
// launch with no grant removes a file a jail of this name left behind without its teardown. The
// host copy always; on Apple Container also the copy in the jail home, both 0600. The values never
// reach an argv, a process environment of the host, or the runtime's container configuration,
// inspect output or database (ES-D37). A write that fails is the error, which refuses the launch:
// the user asked for credentials the jail would not hold.
func (o *Options) stageJailGrant(cname, rt, wsState string) error {
	removeJailGrantFiles(cname, rt, wsState)
	o.jailGrantFile = ""
	if o.jailGrant == nil {
		return nil
	}
	host := jailGrantHostFile(cname)
	dir := filepath.Dir(host)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not make the directory for this jail's --with-credentials grant (%s): %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("could not narrow %s to its owner: %w", dir, err)
	}
	content := []byte(o.jailGrant.fileContent())
	if err := writeFileBeneathMode(dir, jailGrantFileLeaf, content, 0o600); err != nil {
		return fmt.Errorf("could not write this jail's --with-credentials grant (%s): %w", host, err)
	}
	o.jailGrantFile = host
	if rt == "container" { // parity: HonoredBy — Apple Container gets a copy in the jail home it binds whole (acMaterialize's choice for a file read at boot); podman binds the host copy :ro
		if err := writeFileBeneathMode(wsState, jailGrantHomeRel(rt), content, 0o600); err != nil {
			return fmt.Errorf("could not copy this jail's --with-credentials grant into its home (%s): %w",
				filepath.Join(wsState, jailGrantHomeRel(rt)), err)
		}
	}
	return nil
}

// jailGrantBindArgs is the container argv's half of the grant: on podman one `:ro` bind of the host
// copy at the jail home's grant path, and nothing else; on Apple Container nothing, since the copy is
// already in the home it binds. Never a name or a value as `-e` (ES-D37).
func (o *Options) jailGrantBindArgs(rt string) []string {
	if o.jailGrantFile == "" || rt == "container" { // parity: HonoredBy — Apple Container reads the copy stageJailGrant put in the jail home
		return nil
	}
	return []string{"-v", o.jailGrantFile + ":/home/agent/" + entrypoint.JailGrantFileRel + ":ro"}
}

// removeJailGrantFiles removes cname's grant file wherever it is: the host copy and its
// directory, and the copy or mountpoint in the jail home beneath wsState (a removal beneath the
// root, never through a link the jail left). Best-effort: a file it cannot remove is the next
// launch's to replace.
func removeJailGrantFiles(cname, rt, wsState string) {
	if cname != "" {
		_ = os.RemoveAll(filepath.Dir(jailGrantHostFile(cname)))
	}
	if wsState == "" {
		return
	}
	if r, err := openStateRoot(wsState); err == nil {
		_ = r.Remove(jailGrantHomeRel(rt))
		_ = r.Close()
	}
}

// discardUnheldJailGrant is Run's deferred discard of a grant file this launch staged for a
// container that never started (o.packTreeHeld is the fresh path's "a container holds what this
// launch staged"): once one did, the file goes with the jail, when its container is known gone
// (forgetGone).
func (o *Options) discardUnheldJailGrant(cname, rt string) {
	if o.jailGrantFile == "" || o.packTreeHeld {
		return
	}
	removeJailGrantFiles(cname, rt, paths.WorkspaceHomeState(o.Workspace))
	o.jailGrantFile = ""
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
		"asks for --with-credentials %s, and the running jail (%s) was launched with %s, so %s. A jail's "+
		"--with-credentials grant is fixed when the jail is launched, and an attach cannot add to it.",
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
