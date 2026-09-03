package jailcontent

// Per-workspace briefing generation: the jail-managed body (BriefingContent), the
// config's agents_md_extra and each pack's prose composed onto it, and the user's own
// host briefing prepended in front of the lot.
//
// WHERE it lands is deliberately not this file's business. Every briefing path is some
// pack's `briefing` contribution `into`, so this renders ONE text and the CLI writes it
// to each declared destination — which is why nothing here names a briefing FILE. It used
// to say "AGENTS.md / CLAUDE.md briefing generation", from when the destinations were a
// per-agent constant in the Go registry.
//
// The briefing content is a byte-exact string contract; WriteBriefing's (write.go)
// hardlink-breaking truncation is an inode-preservation contract a running jail's bind
// mount depends on.

import (
	"bytes"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// BlockedTool is one entry of the "Blocked Tools" section (name + optional
// message + optional suggestion).
type BlockedTool struct {
	Name       string
	Message    string
	Suggestion string
}

// Loophole is a (name, description) pair for the loopholes section.
type Loophole struct {
	Name string
	Desc string
}

// BriefingInput carries everything the jail-managed briefing content depends
// on. Workspace is the host workspace path (rendered verbatim);
// ProvisioningFailed is true when the last boot's .yolo/startup.log contained
// "PROVISIONING FAILED" (the caller reads the log — see ReadProvisioningFailed).
type BriefingInput struct {
	Workspace         string
	BlockedTools      []BlockedTool
	MountDescriptions []string
	// NetMode is the CONFIGURED network mode (`network.mode`, or the --network flag).
	// AppliedNetMode is the mode the launch actually ran under, which is not always the
	// same one: podman-in-podman is forced to host networking whatever the config says,
	// and Apple Container emits no network selector at all. When it is set it decides the
	// network paragraph and the port sections; NetMode is the fallback for a caller that
	// has not resolved the backend (docs/design/backend-parity.md §6).
	//
	// A SECOND FIELD rather than a retype of NetMode, deliberately. "host" is both a
	// network mode and a confinement notch here, and the two are pinned apart by
	// TestBriefingNetModeHostIsNotTheHostNotch; several callers construct NetMode
	// directly, and collapsing them would have made that conflation cheap to reintroduce.
	NetMode        string
	AppliedNetMode string
	// PublishPorts and ForwardHostPorts are the two DIRECTIONS, and they are
	// rendered as separate sections on purpose: a jail that showed only the
	// second one let an agent see which host ports had been imported while
	// leaving it blind to which of its own ports were published outward. Their
	// entry orders are opposite — PublishPorts (network.ports) is "HOST:JAIL",
	// ForwardHostPorts is "JAIL:HOST" — so neither may be rendered as a bare
	// pair; every line names which side each port belongs to.
	PublishPorts       []any
	ForwardHostPorts   []any
	Loopholes          []Loophole
	Resources          map[string]any
	IsYoloSourceTree   bool
	ProvisioningFailed bool
	// Confinement is the notch this environment runs at ("jail"|"guest"|"host"),
	// env-manager plan Phase 8. Empty is treated as "jail" (the default and today's
	// behavior). The briefing states the notch so an agent at guest/host knows it is
	// NOT disposable — a briefing that always said "sandboxed container" would tell a
	// host agent something dangerously false.
	Confinement string

	// Handoff is the content of a fresh .yolo/handover.md pointer, read by the run
	// pipeline at launch. Empty in the common case, where the task comes from the user.
	// When non-empty it is rendered as a prominent Handoff section near the top — the
	// one-time transition task handed over for this launch. The run pipeline consumes the
	// pointer once this briefing has been WRITTEN, so it appears on exactly one launch and
	// a launch that carries it nowhere leaves it fresh.
	Handoff string
}

// BriefingContent renders the jail-managed briefing body (before any host-level
// user content is prepended and before agents_md_extra is appended). The body
// is assembled from BriefingInput — the briefing lines joined with "\n" plus a
// trailing newline. NOTE: this is NOT golden-pinned; no test asserts the full
// output (briefing_test.go covers only the helpers), so sections can be added
// or removed without regenerating a golden. The network mode is AppliedNetMode
// when set, else NetMode, else "bridge".

// confinementHeader is the briefing's opening block for the notch this environment runs
// at (env-manager plan Phase 8, C2).
//
// It reads the notch's PROFILE, not just its name. The name still picks the title and the
// framing sentence — that prose genuinely differs per notch, a human reads it, and no
// generated sentence would say "this is the human's REAL machine" as usefully — but the
// two facts an agent most needs are DERIVED: which primitives actually enforce the
// boundary, and whether agent autonomy is on. That is what makes the header correct for a
// notch nobody has enumerated yet (a Linux `guest`, whatever Phase 7 lands): an unrecognized
// name falls to the default branch and still describes its real enforcement vector instead
// of asserting a container that may not be there. Same argument that motivated
// render.KindGuest — a new notch should be a question the code asks, not a branch it
// silently inherits.
//
// The vocabulary comes from render.PrimitiveDoes, the same table `yolo describe` prints, so
// the two human-facing descriptions of one primitive cannot drift.
//
// THE JAIL'S BYTES ARE UNCHANGED, deliberately. Every jail that boots today renders this
// header, so adding detail there would move a rendered surface for every existing user to
// tell them something the next two lines of the briefing already say ("a sandboxed
// container", "no systemd, no sudo"). The notches that gain the primitive vector are the
// ones whose prose was thin and whose enforcement is genuinely ambiguous — so
// enforcementLines is appended on the guest/host/unknown paths only, and the jail branch
// returns its historical literal.
func confinementHeader(confinement string) []string {
	notch, known := render.KindForNotch(confinement)
	if confinement == "" {
		// Empty means the default, which is jail — the historical behavior, preserved so a
		// caller that has not resolved the notch renders exactly what it always did.
		notch, known = render.KindJail, true
	}
	prof := render.ProfileFor(notch)

	switch {
	case known && notch == render.KindHost:
		return append([]string{
			"# YOLO Environment — host",
			"",
			"You are running at the **host** confinement level: this is the human's REAL",
			"machine, with no container around you. Changes are NOT disposable.",
			"You have: their real credentials, their real dotfiles, no snapshot to fall back on.",
			"Absent: nothing is mounted read-only; there is no jail to restart; `sudo` is real.",
		}, enforcementLines(prof)...)
	case known && notch == render.KindGuest:
		return append([]string{
			"# YOLO Environment — guest",
			"",
			"You are running at the **guest** confinement level: a restricted account on the",
			"real machine, NOT a disposable container.",
			"Your home is real and persists; there is no image and no jail to restart.",
		}, enforcementLines(prof)...)
	case known && notch == render.KindJail:
		// Byte-identical to the historical briefing — see the doc comment.
		return []string{
			"# YOLO Jail Environment",
			"",
			"You are running inside a YOLO Jail — a sandboxed container.",
			"Jail tooling: `yolo --help`; config reference: `yolo config-ref`.",
			"",
		}
	default:
		// A notch this function does not recognize. It gets a header describing its actual
		// primitive vector rather than one that CLAIMS a container, which is the whole point
		// of reading the Profile: the previous version's default branch told an agent at an
		// unknown notch it was in a sandboxed container, which for anything below jail is
		// exactly the dangerous falsehood Phase 8 exists to prevent. ProfileFor is total and
		// fails closed (an unrecognized name resolves to KindUnset and thus the host preset —
		// no primitives, autonomy off), so this describes the most restricted reading rather
		// than guessing a stronger one.
		//
		// The name is echoed as the CONFIG WROTE IT, not as notch.String(): an unresolvable
		// name lands on KindUnset, so printing the Kind would title the section "unset" and
		// lose the one clue a human debugging it needs — which value produced this. Config
		// validation rejects an unknown `confinement` (validateConfinement), so reaching here
		// means something bypassed that, and the actual string is the evidence.
		return append([]string{
			"# YOLO Environment — " + confinement,
			"",
			"You are running at confinement level `" + confinement + "`, which this briefing does",
			"not recognize. Do not assume a container: what actually constrains you is listed",
			"below, and nothing beyond it is implied.",
		}, enforcementLines(prof)...)
	}
}

// enforcementLines is the derived tail of a confinement header: what enforces the boundary,
// and whether agent autonomy is on. Both are read off the Profile rather than written per
// notch, which is what keeps them true for a notch nobody enumerated.
//
// The autonomy line is here because it is the most consequential thing an agent can know
// about its own notch and is invisible everywhere else — it decides the posture INSIDE a
// pack's config surfaces, never as a statement of its own. `yolo describe` prints the same
// two facts to the human from the same table (printConfinementVector); this is the agent's
// copy.
func enforcementLines(prof render.Profile) []string {
	lines := []string{"", "Enforced by:"}
	var any bool
	for _, prim := range render.PrimitiveOrder() {
		if prof.Has(prim) {
			lines = append(lines, "- "+render.PrimitiveDoes(prim))
			any = true
		}
	}
	if !any {
		// A preset that composes NOTHING must say so plainly. Omitting the section would read
		// as "not stated" when the fact IS the point.
		lines = append(lines, "- nothing — no enforcement primitive at all; this is a real machine.")
	}
	if prof.AgentAutonomy {
		lines = append(lines,
			"",
			"Agent autonomy is **ON**: your tools run without permission prompts, which is safe",
			"only because the boundary above contains you.")
	} else {
		lines = append(lines,
			"",
			"Agent autonomy is **OFF**: permission prompts stay on, because nothing above",
			"contains you. Do not try to disable them.")
	}
	return append(lines, "", "Jail tooling: `yolo --help`; config reference: `yolo config-ref`.", "")
}

func BriefingContent(in BriefingInput) string {
	// What the launch APPLIED wins over what the config asked for; NetMode is the
	// fallback for a caller that never resolved a backend. Everything downstream — the
	// network paragraph and both port sections, which describe forwarding that only
	// happens under bridge — keys off this one value, so a nested jail forced onto host
	// networking is told so instead of being told about a bridge it does not have.
	netMode := in.AppliedNetMode
	if netMode == "" {
		netMode = in.NetMode
	}
	if netMode == "" {
		netMode = "bridge"
	}

	var networkLine string
	if netMode == "host" {
		networkLine = "- **Network**: Host networking — the container shares the host network stack. `localhost` / `127.0.0.1` resolves directly to the host. No port mapping needed."
	} else {
		networkLine = "- **Network**: Bridge mode. `localhost` in here is the JAIL's loopback. Reach the host at " +
			"`host.containers.internal` (169.254.1.2) — including host services bound to the host's own " +
			"`127.0.0.1`, which yolo has the network stack forward in. `$YOLO_HOST_LOOPBACK` says what it decided " +
			"(`requested`/`shared` = forwarding is in place)."
	}

	// Both port sections are suppressed under host networking, where the stacks are
	// shared and neither key is honored at launch — rendering them would describe
	// forwarding that is not happening.
	var publishedPorts []string
	if len(in.PublishPorts) > 0 && netMode != "host" {
		publishedPorts = append(publishedPorts,
			"- **Published Ports** (the HOST connects IN to a server you run in here). Only works if "+
				"the server binds `0.0.0.0`; a `127.0.0.1` listener in here is not publishable:")
		for _, entry := range in.PublishPorts {
			hp, jp, ok := publishEntry(entry)
			if !ok {
				continue
			}
			publishedPorts = append(publishedPorts,
				"  - jail port "+jp+" → `localhost:"+hp+"` on the host")
		}
	}

	var forwardedPorts []string
	if len(in.ForwardHostPorts) > 0 && netMode != "host" {
		forwardedPorts = append(forwardedPorts,
			"- **Forwarded Host Ports** (YOU connect OUT to a service on the host). These answer on "+
				"the JAIL's own `localhost`, so a client in here can use `localhost:<port>` directly:")
		for _, entry := range in.ForwardHostPorts {
			lp, hp, kind := portEntry(entry)
			switch kind {
			case portInt, portPlain:
				forwardedPorts = append(forwardedPorts, "  - `localhost:"+lp+"` in here → host port "+lp)
			case portMapped:
				forwardedPorts = append(forwardedPorts, "  - `localhost:"+lp+"` in here → host port "+hp)
			}
		}
	}

	var resourceLine []string
	if len(in.Resources) > 0 {
		keys := make([]string, 0, len(in.Resources))
		for k := range in.Resources {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+pyValue(in.Resources[k]))
		}
		resourceLine = []string{
			"- **Resource limits** (kernel-enforced): " + strings.Join(parts, ", ") +
				".  Sub-limit your own processes with `yolo-cglimit` (`--help` for usage).",
		}
	}

	var provisioningFailed []string
	if in.ProvisioningFailed {
		provisioningFailed = []string{
			"## ⚠ Provisioning failed",
			"",
			"The last boot's provisioning failed — project tools may be missing.",
			"Read `/workspace/.yolo/startup.log` and self-serve (e.g. run",
			"`mise install` in /workspace, then re-run the step that failed).",
			"",
		}
	}

	lines := append([]string{}, confinementHeader(in.Confinement)...)
	lines = append(lines, provisioningFailed...)
	// The handoff, if one was handed over for this launch: a one-time transition task,
	// surfaced once (the run pipeline consumes the pointer once this briefing is written,
	// so it never returns as a stale task). Prominent — it is what the agent is here to do.
	//
	// "Handed over" rather than "the host agent handed over": the host→jail transition is
	// the motivating case, but a jail agent filing a pointer for its successor uses the
	// same carrier (agent-briefings.md), and the briefing should not misattribute it.
	//
	// When there is no handoff (the common case) no section appears, and the agent's
	// default — wait for the user — is the whole story, so NO standing line is added: the
	// jail's config-independent header bytes are pinned unchanged
	// (TestBriefingJailHeaderIsUnchanged), and an always-present line would move that
	// surface for every existing user. The one-time-ness therefore has to be stated INSIDE
	// the conditional section, where it costs those pinned bytes nothing.
	if in.Handoff != "" {
		lines = append(lines,
			"## Handoff",
			"",
			"Handed over for this launch — it is **the task**. Work it. This appears once:",
			"the pointer that carried it has been consumed, so it will not be here next session.",
			"",
			strings.TrimSpace(in.Handoff),
			"",
		)
	}
	lines = append(lines,
		"## Environment",
		"",
		"- **Workspace**: `/workspace` is the host directory `"+in.Workspace+"`,",
		"  bind-mounted LIVE — the same files, not a copy. Host-side edits are",
		"  instantly visible here and vice versa; there is never a git",
		"  pull/push, fetch, or any sync step between the jail and the host",
		"  for this directory.",
		"- **Home**: `/home/agent` (persistent across sessions)",
		"- **OS**: NixOS-based minimal container (no systemd, no sudo)",
		networkLine,
	)
	lines = append(lines, publishedPorts...)
	lines = append(lines, forwardedPorts...)
	lines = append(lines, resourceLine...)
	lines = append(lines,
		"",
		"⚠ rg is recursive by default — never pass grep-style `-r`/`-rn` flags",
		"(in rg, `-r` means `--replace` and silently corrupts match output).",
		"Use `rg -n <pattern> [path]`.",
		"",
	)

	if len(in.Loopholes) > 0 {
		lines = append(lines, "## Loopholes — host capabilities wired into this jail", "")
		for _, lh := range in.Loopholes {
			first := loopholeFirst(lh.Desc)
			if first != "" {
				lines = append(lines, "- **"+lh.Name+"**: "+first)
			} else {
				lines = append(lines, "- **"+lh.Name+"**")
			}
		}
		lines = append(lines, "", "Details: `yolo loopholes list`.", "")
	}

	if len(in.BlockedTools) > 0 {
		lines = append(lines,
			"## Blocked Tools",
			"",
			"The following tools are blocked or shimmed in this project:",
			"",
		)
		for _, tool := range in.BlockedTools {
			entry := "- `" + tool.Name + "`"
			if tool.Message != "" {
				entry += ": " + tool.Message
			}
			if tool.Suggestion != "" {
				entry += " Use `" + tool.Suggestion + "` instead."
			}
			lines = append(lines, entry)
		}
		lines = append(lines, "")
	}

	if len(in.MountDescriptions) > 0 {
		lines = append(lines, "## Additional Context Mounts (read-only)", "")
		for _, m := range in.MountDescriptions {
			hostPath, containerPath := m, m
			if i := strings.Index(m, ":"); i >= 0 {
				hostPath, containerPath = m[:i], m[i+1:]
			}
			lines = append(lines, "- `"+containerPath+"` (from host `"+hostPath+"`)")
		}
		lines = append(lines, "")
	}

	lines = append(lines,
		"## Limitations",
		"",
		"- Host credentials are not propagated into the jail: the host's `~/.ssh`,",
		"  `~/.gitconfig`, and cloud/gh tokens are invisible here. This is a credential",
		"  boundary, not a network block — outbound SSH and HTTPS work normally, so git",
		"  push/pull and API calls succeed whenever the jail has its own credentials",
		"  (e.g. a workspace-specific deploy key or a token in `.env`). Only without",
		"  such jail-local credentials do authenticated operations fail.",
		"- No sudo/root; context mounts under `/ctx/` are read-only.",
		"",
		"## Packages & Resource Limits",
		"",
		"To request a tool or a container-limit change: edit `/workspace/yolo-jail.jsonc`",
		"(`packages` / `resources`), ALWAYS run `yolo check` after every config edit",
		"(`yolo check --no-build` is fine inside a running jail), then ask the human to",
		"restart the jail. Reference: `yolo config-ref`.",
		"",
		"## Skills",
		"",
		"User-level skills dirs (`~/.<agent>/skills/`) are **read-only** in-jail",
		"(kernel-enforced); workspace-level ones (`/workspace/.<agent>/skills/`) are",
		"writable — develop there, then ask the human to promote to the host.",
		"",
		"On-demand skills are staged for you: read **configuring-the-jail** before",
		"editing `yolo-jail.jsonc`, and **diagnosing-the-jail** when a command",
		"misbehaves. Their bodies load only when invoked — they cost nothing until then.",
		"",
	)

	if in.IsYoloSourceTree {
		lines = append(lines,
			"When editing this repo's own Go code (`cmd/`/`internal/`) or `flake.nix`,",
			"read the **developing-yolo-jail** skill for the build/deploy/verify traps.",
			"",
		)
	}

	return strings.Join(lines, "\n") + "\n"
}

// ComposeBriefing appends agents_md_extra to the jail content:
// jailContent + "\n" + rstrip(extra) + "\n" when extra is non-empty.
func ComposeBriefing(jailContent, extra string) string {
	if extra == "" {
		return jailContent
	}
	return jailContent + "\n" + strings.TrimRight(extra, " \t\r\n") + "\n"
}

// PrependHostBriefing produces one agent's final briefing: the host briefing
// file's content + "\n---\n\n" + jailContent when the host file exists, else
// jailContent alone.
func PrependHostBriefing(hostBriefingPath, jailContent string) string {
	data, err := os.ReadFile(hostBriefingPath)
	if err != nil {
		return jailContent
	}
	return string(data) + "\n---\n\n" + jailContent
}

type portKind int

const (
	portNone portKind = iota
	portInt
	portMapped
	portPlain
)

// portEntry classifies a forward_host_ports entry, returning the rendered
// local/host port strings. An int → (n, n, portInt); a string "a:b" →
// (a, b, portMapped) [split once]; a plain string → (s, s, portPlain);
// anything else → portNone.
func portEntry(entry any) (local, host string, kind portKind) {
	// jsonx decodes ints as jsonInt; accept both that and native ints.
	if s, ok := intString(entry); ok {
		return s, s, portInt
	}
	if str, ok := entry.(string); ok {
		if i := strings.Index(str, ":"); i >= 0 {
			return str[:i], str[i+1:], portMapped
		}
		return str, str, portPlain
	}
	return "", "", portNone
}

// publishEntry classifies a network.ports entry, returning the rendered HOST and
// JAIL port strings. The order is podman's `-p`: host side FIRST, the reverse of
// forwardHostPorts (see portEntry). Accepted shapes, all with an optional
// "/tcp"|"/udp" suffix that belongs to neither port:
//
//	an int or a bare string → the same port on both sides
//	"host:jail"             → two fields
//	"ip:host:jail"          → three fields; the MIDDLE one is the host port
//
// Anything else returns ok=false and is skipped: `yolo check` rejects those
// shapes, so a briefing is not the place to complain about them a second time.
func publishEntry(entry any) (host, jail string, ok bool) {
	if s, isInt := intString(entry); isInt {
		return s, s, true
	}
	str, isStr := entry.(string)
	if !isStr {
		return "", "", false
	}
	if i := strings.LastIndex(str, "/"); i >= 0 {
		str = str[:i]
	}
	parts := strings.Split(str, ":")
	switch len(parts) {
	case 1:
		return parts[0], parts[0], true
	case 2:
		return parts[0], parts[1], true
	case 3:
		return parts[1], parts[2], true
	}
	return "", "", false
}

// loopholeFirst extracts the first-sentence summary of a loophole description:
// the text up to the first ". " or newline, trimmed and with a trailing "."
// stripped.
func loopholeFirst(desc string) string {
	s := desc
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	return strings.TrimRight(s, ".")
}

// WorkspaceIsYoloSourceTree reports whether workspace is a yolo-jail source
// checkout: go.mod with the yolo-jail module path AND cmd/yolo/main.go present.
func WorkspaceIsYoloSourceTree(workspace string) bool {
	data, err := os.ReadFile(workspace + "/go.mod")
	if err != nil {
		return false
	}
	if !bytes.Contains(data, []byte("yolo-jail")) {
		return false
	}
	_, err = os.Stat(workspace + "/cmd/yolo/main.go")
	return err == nil
}

// PackBriefing is one pack's contribution to an agent briefing (C3).
//
// ONE PER CONTRIBUTION since briefing-audiences.md, where it used to be one per PACK. That is
// the limit §5 lifts: with a single (pack, text) pair, a pack declaring two briefing
// contributions with two different `from` files could deliver only the first into a jail,
// while the host render honored both. The caller (run.packBriefingProses) is what enumerates
// them; this type only had to stop being the bottleneck.
type PackBriefing struct {
	// Name is the pack's name, used for the provenance header.
	Name string
	// Text is the pack's AGENTS.md prose, already read.
	Text string
	// Agents is the AUDIENCE this prose names — the launcher commands it is FOR. EMPTY MEANS
	// BROADCAST, which is the pre-field behavior and the only behavior a pack with no
	// pack.json can ask for (briefing-audiences.md P2).
	//
	// It holds the audience rather than a resolved destination because a content pack names
	// WHO and never WHERE: where an agent reads is that agent pack's business and changes
	// when the agent changes (P4). ComposePackBriefings matches it against the identity the
	// DESTINATION declared.
	Agents []string
}

// ComposePackBriefings appends each pack's prose to ONE DESTINATION's briefing, in config
// order, under a provenance header naming the pack.
//
// `agent` is the identity that destination declared for itself, or "" for a destination that
// declared none. It is what makes this per-DESTINATION rather than per-jail, and moving that
// call inside the write loop is the jail half of briefing-audiences.md: before, one body was
// composed once and written to every destination, so a pack whose rules applied to one agent
// had to broadcast them to all of them.
//
// THE MATCH IS AGAINST A DECLARED STRING, never anything derived (OQ-BA2). So a destination
// that declares no identity is simply never named by any selector (R4) — an addressed
// contribution skips it, and a broadcast one still reaches it.
//
// The header is not decoration. Pack prose is INSTRUCTIONS an agent will follow, and
// a jail may carry several packs plus yolo's own briefing plus the user's — so
// without attribution an agent reading a surprising rule has no way to find out
// where it came from, and neither does the human debugging it. Naming the source is
// the same legibility argument as `yolo config diff` reporting which layer set a key.
//
// Empty text is skipped rather than emitting an empty section: a pack with no
// briefing should leave no trace.
func ComposePackBriefings(base string, packs []PackBriefing, agent string) string {
	out := base
	for _, p := range packs {
		if !addressesAgent(p.Agents, agent) {
			continue
		}
		text := strings.TrimRight(p.Text, " \t\r\n")
		if text == "" {
			continue
		}
		out = strings.TrimRight(out, "\n") + "\n\n" +
			"<!-- from pack: " + p.Name + " -->\n" + text + "\n"
	}
	return out
}

// addressesAgent reports whether prose naming `agents` belongs at a destination whose declared
// identity is `agent`.
//
// NIL/EMPTY IS BROADCAST, and the whole safety of landing the field ahead of any pack adopting
// it rests on this line: a jail full of packs that name no audience composes exactly what it
// did before. An empty `agent` (a destination declaring no identity) therefore still receives
// every broadcast and no addressed prose — the two halves of R4 in one predicate.
func addressesAgent(agents []string, agent string) bool {
	if len(agents) == 0 {
		return true
	}
	for _, a := range agents {
		if a == agent {
			return true
		}
	}
	return false
}
