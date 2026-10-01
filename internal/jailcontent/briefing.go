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
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

// ContextMount is one entry of the "Additional Context Mounts" section: a context mount
// (docs/design/context-mounts.md, Defined terms) this launch actually BOUND.
//
// A struct, where it used to be a "host:container" string: a string cannot carry a mode,
// and its reader split on the FIRST colon, which is the wrong half of the split the string
// was built with (§3.8).
type ContextMount struct {
	// Path is the jail path the agent opens, expanded for this backend.
	Path string
	// Host is the resolved host source.
	Host string
	// ReadWrite is true for a read-write config `mounts` element.
	ReadWrite bool
	// Pack names the pack whose `mount` grant this is; "" for a config `mounts` element.
	Pack string
}

// BriefingInput carries everything the jail-managed briefing content depends
// on. Workspace is the host workspace path (rendered verbatim);
// ProvisioningFailed is true when the last boot's .yolo/startup.log contained
// "PROVISIONING FAILED" (the caller reads the log — see ReadProvisioningFailed).
type BriefingInput struct {
	Workspace    string
	BlockedTools []BlockedTool
	// ContextMounts are the context mounts this launch delivered, config elements and pack
	// grants alike — bound on a container backend, linked on macos-user — and only those
	// (run.briefedCtxMounts).
	ContextMounts []ContextMount
	// ContextDir is what $YOLO_CONTEXT_DIR names on this backend (paths.ContextDirEnv);
	// empty means the container answer, /ctx.
	ContextDir string
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
	ProvisioningFailed bool
	// IOPriority is the disk I/O priority the launch PASSED to the entrypoint ("low" or
	// "idle"), empty where it passed none. It is rendered on a line of its own, never inside
	// the kernel-enforced limits line: any process can raise its own priority, and the disk
	// under the workspace may ignore it (docs/design/io-priority.md §5.2).
	IOPriority string
	// Confinement is the notch this environment runs at ("jail"|"guest"|"host"),
	// env-manager plan Phase 8. Empty is treated as "jail" (the default and today's
	// behavior). The briefing states the notch so an agent at guest/host knows it is
	// NOT disposable — a briefing that always said "sandboxed container" would tell a
	// host agent something dangerously false.
	Confinement string

	// BackendLimits are this backend's standing constraints, in the agent's voice —
	// the facts the launch prints to the human on stderr, which the agent never sees.
	// Empty for every container backend, because they impose none beyond what the rest
	// of the briefing already describes.
	BackendLimits []string

	// Mechanism is the RUNTIME this launch uses — "podman", "container" (Apple
	// Container) or "macos-user" — and it is the second of the two axes the header
	// reads. The notch dial says how much the environment is restricted; the mechanism
	// says by WHAT, and the two are independent: macos-user runs at `confinement: jail`
	// with no container in it, and the header that pairing produced claimed a container
	// that does not exist.
	//
	// IT REPLACED A BOOLEAN, and that is the whole of OQ-DP2
	// (docs/design/declaration-parity.md §2.3). The field used to be `NoContainer bool`,
	// which is the mechanism smuggled in as one of its own consequences: it could say
	// "there is no container" but not "a Seatbelt profile around a separate account is
	// what there is instead", so every notch still printed the LINUX primitive vector
	// off render.ProfileFor — "namespaces", "a baked image" — three lines under a
	// paragraph saying there was no container. ConfinementProfile below takes the
	// mechanism and answers both questions from it, and `yolo describe` calls the same
	// function, so the human's vector and the agent's cannot drift.
	//
	// Empty means "the caller has not resolved a backend", which reads as a container
	// runtime — the historical behaviour, and what every jail renders today.
	Mechanism string

	// IsMacOS is the platform the launch runs ON, used for the one answer no mechanism
	// carries: a `guest` notch has no backend of its own yet (env-manager Phase 7), so
	// the platform picks between the Seatbelt and the Landlock spelling of that preset.
	// Every other combination is decided by Mechanism and ignores this.
	IsMacOS bool

	// Home is the agent's home directory. Empty means `/home/agent`, the container
	// answer and what every jail rendered before macos-user had a briefing worth
	// reading — so no caller that has not resolved a backend changes behaviour.
	//
	// A FIELD rather than a macosuser import: this package cannot reach
	// internal/macosuser without routing through internal/entrypoint, whose tests
	// import this package back. The caller that already knows the mechanism is the
	// one that knows the home.
	Home string

	// Handoff is the content of a fresh .yolo/handover.md pointer, read by the run
	// pipeline at launch. Empty in the common case, where the task comes from the user.
	// When non-empty it is rendered as a prominent Handoff section near the top — the
	// one-time transition task handed over for this launch. The run pipeline consumes the
	// pointer once this briefing has been WRITTEN, so it appears on exactly one launch and
	// a launch that carries it nowhere leaves it fresh.
	Handoff string

	// AttachSkew is set by an attach that proceeded under YOLO_ALLOW_ATTACH_SKEW into a jail
	// that could not receive what it delivers (attachskewsection.go, SK-D15). Nil, the case for
	// every fresh launch and every other attach, renders no section.
	AttachSkew *AttachSkew

	// HostNix is true when the launch mounted the host's nix daemon socket and store, so
	// `nix` here runs with NIX_REMOTE=daemon. It is the SAME predicate that emits those
	// mounts (run.hostNixMounted), so the line it gates appears exactly where the hazard
	// it states exists: every root the jail asks for is recorded under the jail's spelling
	// of the out-link, which the host daemon resolves on the HOST filesystem and deletes
	// as stale (docs/design/in-jail-nix-roots.md §2). macos-user shares the host's
	// filesystem, so its roots are real and it never sets this.
	HostNix bool

	// Persistence is the launch's persistence map (persistence.go): which paths survive a
	// restart, which are shared by every workspace, and which are gone once the jail
	// exits. The run pipeline builds it from the definitions the mount argv reads, and it
	// renders the storage-classes section (docs/design/durable-scratch-space.md
	// §4.1). Nil renders no section: macos-user, the host notch and a hand-built input.
	Persistence *PersistenceMap

	// Durable is the launch's durable dir (persistencesection.go): the path the section LEADS
	// with, or why there is none. Nil renders no durable line, which is every caller that
	// has not made one — the host notch (OQ-DS3: a static sentence in the host header
	// instead) and a hand-built input. Non-nil with no map (macos-user) renders the section
	// with the durable answer alone.
	Durable *DurableDir
}

// BriefingContent renders the jail-managed briefing body (before any host-level
// user content is prepended and before agents_md_extra is appended). The body
// is assembled from BriefingInput — the briefing lines joined with "\n" plus a
// trailing newline. NOTE: this is NOT golden-pinned; no test asserts the full
// output (briefing_test.go covers only the helpers), so sections can be added
// or removed without regenerating a golden. The network mode is AppliedNetMode
// when set, else NetMode, else "bridge".

// ConfinementProfile is the (notch, mechanism, platform) → primitive vector lookup that
// BOTH printing surfaces read: this package's briefing header, for the agent, and
// cli.describe's `enforced by` block, for the human. OQ-DP2 ruled them one function
// (docs/design/declaration-parity.md §2.3); before that they were two, and the agent's
// copy was the wrong one.
//
// IT IS NOT render.ProfileFor, which is deliberately platform-blind — a render Target
// carries no platform, so that table returns the LINUX spelling of every preset and its own
// doc comment says a printed vector must come from the caller that knows the backend. This
// is that caller. It lives HERE rather than in internal/render for exactly that reason: the
// platform and the mechanism are inputs render does not have.
//
// MECHANISM FIRST, platform only as the fallback, because `runtime` is what a launch
// actually uses and a primitive is a property of the backend, not of the machine reading
// the config. So `container` prints the VM, and a NATIVE runtime (macos-user) prints the
// macOS guest vector — a separate user plus Seatbelt is what that backend composes by
// definition, and it is the guest notch by another name (no container, no image) whatever
// the notch is called. isMacOS decides only the guest variant no mechanism names: a `guest`
// notch has no backend of its own yet (env-manager Phase 7), so the platform's spelling is
// the best available answer.
//
// KindUnset FAILS CLOSED, to the host preset — no primitives, autonomy OFF. The briefing
// reaches this with an unresolvable notch name (config validation rejects one, so getting
// here means something bypassed it) where `describe` errors out first, and the asymmetry is
// the reason: guessing toward `jail` tells an agent on a real machine that its permission
// prompts are off, and guessing toward `host` only shows prompts a contained agent did not
// need. render.ProfileFor takes the same direction for the same reason.
func ConfinementProfile(notch render.Kind, mechanism string, isMacOS bool) render.Profile {
	switch {
	case notch == render.KindHost || notch == render.KindUnset:
		return render.HostProfile()
	case MechanismHasNoContainer(mechanism):
		return render.GuestProfileMacOS()
	case notch == render.KindGuest:
		if isMacOS {
			return render.GuestProfileMacOS()
		}
		return render.GuestProfileLinux()
	default: // jail — Apple Container gives each container its own VM; podman gives namespaces.
		return render.JailProfile(mechanism == "container")
	}
}

// LaunchProfile is the profile one launch runs under: ConfinementProfile's vector and policy,
// with the network primitive set from the launch's own answer to "does this jail share the
// launcher's network namespace?" (OQ-NC3). The launch line and the briefing both read it, so the
// fact each prints is one value.
//
// ONLY RAISES the primitive: a mechanism that shares the network by construction (macos-user,
// GuestProfileMacOS) stays shared whatever the caller passes, because no launch can give a
// Seatbelt sandbox a namespace of its own.
func LaunchProfile(notch render.Kind, mechanism string, isMacOS, sharedNetwork bool) render.Profile {
	prof := ConfinementProfile(notch, mechanism, isMacOS)
	return prof.WithSharedNetwork(prof.SharesHostNetwork() || sharedNetwork)
}

// briefingProfile is LaunchProfile for a briefing: the notch as the config names it (empty is
// the jail), and the network as the launch APPLIED it. An applied "host" mode is exactly the
// launcher's shared-namespace predicate read as a mode (run.appliedNetMode), so the network
// line above and this fact rest on one answer.
func briefingProfile(in BriefingInput, netMode string) render.Profile {
	notch, _ := render.KindForNotch(in.Confinement)
	if in.Confinement == "" {
		notch = render.KindJail // confinementHeader's reading of the default
	}
	return LaunchProfile(notch, in.Mechanism, in.IsMacOS, netMode == "host")
}

// MechanismHasNoContainer reports whether a runtime puts no container around the jail —
// paths.NativeRuntimes, which is `macos-user` today.
//
// It is a function over the mechanism rather than a BriefingInput field because the two
// answers a caller used to supply separately ("there is no container" and "what is there
// instead") are one fact, and supplying them apart is how a header came to deny a container
// and then list the container's own primitives (DP-B19).
func MechanismHasNoContainer(mechanism string) bool {
	return slices.Contains(paths.NativeRuntimes, mechanism)
}

// confinementHeader is the briefing's opening block for the notch this environment runs
// at (env-manager plan Phase 8, C2).
//
// It reads the notch's PROFILE and the MECHANISM, not just the notch's name. The name still
// picks the title and the framing sentence — that prose genuinely differs per notch, a human reads it, and no
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
// enforcementLines is appended on the guest/host/unknown paths and on the jail-WITHOUT-a-
// container path, whose prose was rewritten for macos-user and never had historical bytes to
// protect; the jail-with-a-container branch returns its historical literal and no vector.
func confinementHeader(confinement, mechanism string, isMacOS bool) []string {
	notch, known := render.KindForNotch(confinement)
	if confinement == "" {
		// Empty means the default, which is jail — the historical behavior, preserved so a
		// caller that has not resolved the notch renders exactly what it always did.
		notch, known = render.KindJail, true
	}
	// ConfinementProfile, not render.ProfileFor: the second reads the notch alone and
	// returns the LINUX spelling of every preset, which is the table's own documented
	// limitation and was printing "namespaces" and "a baked image" to an agent inside a
	// Seatbelt sandbox with no image at all (OQ-DP2). `yolo describe` calls this same
	// function for the human's copy of the vector.
	prof := ConfinementProfile(notch, mechanism, isMacOS)
	noContainer := MechanismHasNoContainer(mechanism)

	switch {
	case known && notch == render.KindHost:
		return append([]string{
			"# YOLO Environment — host",
			"",
			"You are running at the **host** confinement level: this is the human's REAL",
			"machine, with no container around you. Changes are NOT disposable.",
			"You have: their real credentials, their real dotfiles, no snapshot to fall back on.",
			"Absent: nothing is mounted read-only; there is no jail to restart; `sudo` is real.",
			// OQ-DS3 (docs/design/durable-scratch-space.md §5.7): one static sentence and no
			// variable, because host /tmp is the machine's own and a per-launch variable
			// cannot reach the files `yolo host apply` writes.
			"`/tmp` is this machine's own: it survives an agent restart but may not survive a reboot; put worktrees you need later inside the repository or beside it.",
		}, enforcementLines(prof)...)
	case known && notch == render.KindGuest:
		return append([]string{
			"# YOLO Environment — guest",
			"",
			"You are running at the **guest** confinement level: a restricted account on the",
			"real machine, NOT a disposable container.",
			"Your home is real and persists; there is no image and no jail to restart.",
		}, enforcementLines(prof)...)
	case known && notch == render.KindJail && noContainer:
		// THE JAIL NOTCH WITHOUT A CONTAINER. macos-user runs at `confinement: jail`
		// — the notch dial is a separate axis from the runtime, and `guest` is not
		// wired yet — but there is no container anywhere in it: the boundary is a
		// Seatbelt profile around a real account whose home persists and is shared by
		// every workspace on the machine.
		//
		// The header below said "a sandboxed container" there until 2026-09-04, which
		// is the same dangerous falsehood the default branch was rewritten to avoid,
		// arriving through the one branch that was allowed to keep asserting it. An
		// agent told it is in a disposable container reasons about its home as
		// throwaway; here it is neither disposable nor its own.
		//
		// TWO LINES OF IT WERE ALSO WRONG, and both were wrong because the mechanism
		// stopped at this branch instead of reaching enforcementLines (DP-B19, DP-B11):
		// the vector under it read "namespaces … a baked image" one line after the
		// paragraph said there was no container, and the "Jail tooling" line printed
		// twice because this literal carried its own copy of the line enforcementLines
		// appends. The tooling line is gone from here; the vector is now this
		// mechanism's.
		//
		// The home sentence is narrowed to what is still true of it. Every workspace on
		// the machine does share the ACCOUNT, but a pack's `scope: workspace` state dirs
		// are symlinked into <workspace>/.yolo/home by entrypoint.DeriveDarwinHomeLayout,
		// so "state you write there is not yours alone" stopped being true of the
		// directories an agent actually writes. What remains machine-wide is the
		// `scope: machine` set, which backendLimits names one by one.
		return append([]string{
			"# YOLO Environment — jail (native, no container)",
			"",
			"You are confined by a Seatbelt sandbox on the human's REAL machine, not by a",
			"container. There is no image and no jail to restart.",
			"Your home is a real account's home and it PERSISTS between launches. The account",
			"is shared by every workspace on this machine; the state directories your packs",
			"declare at `scope: workspace` are linked into THIS workspace's own sidecar, and",
			"anything else you write in the home is not yours alone.",
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
	// THE BASE IS NOTCH-AWARE (docs/plans/notch-convergence.md item 26, row D7). Everything below
	// the header describes a LAUNCH — the /workspace bind, the network stack it applied, the
	// mounts, the shims, the loopholes, the jail's own limitations and how to ask for a package —
	// and at the host notch there is no launch: the agent is the human's own process on the real
	// machine, and every one of those sections would be a false sentence there. So the host's
	// base is its confinement header and nothing else: that it is on the REAL machine, that
	// nothing is disposable, what enforces nothing, and that autonomy is off. It is what
	// `yolo host apply` composes ahead of the packs' prose (HostBriefingBase), which is how a host
	// agent is told where it is (env-manager Phase 8's done-when).
	if notch, known := render.KindForNotch(in.Confinement); known && notch == render.KindHost {
		return strings.Join(confinementHeader(in.Confinement, in.Mechanism, in.IsMacOS), "\n") + "\n"
	}
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
		// "this environment", not "the container": host networking is also what the
		// macos-user backend applies, and there is no container anywhere in it
		// (DP-B3 / DP-L2). The sentence has to be true of both, so it names neither.
		networkLine = "- **Network**: Host networking — this environment shares the host's network stack. `localhost` / `127.0.0.1` resolves directly to the host. No port mapping needed."
	} else {
		// NO NUMERIC ADDRESS HERE, and that is a correction rather than a style choice.
		// This line used to say "(169.254.1.2)", which is the address yolo asks pasta for
		// — one of three answers, and wrong on the other two. Under slirp4netns podman
		// aims the name at the host's GLOBAL address (hostloopback.go's own table says
		// so), and on a macOS podman machine it is gvproxy's 192.168.127.254 (measured
		// 2026-09-16 on podman 6.0.2/applehv). The NAME is the contract; the number is a
		// property of a host stack the briefing cannot see, so it tells the agent how to
		// look instead of asserting one.
		//
		// The $YOLO_HOST_LOOPBACK sentence gained its `unknown` case for the same
		// measurement: on macOS podman the launcher excludes itself from the decision by
		// name and always says `unknown`, while the Mac's loopback IS forwarded (by
		// gvproxy, with no flag asked for). "requested/shared = forwarding is in place"
		// alone therefore reads as "unknown = it is not", which is false exactly where
		// the value is always unknown — and an agent that believes it skips a route that
		// works. See docs/plans/setup-support-gaps.md G8.
		networkLine = "- **Network**: Bridge mode. `localhost` in here is the JAIL's loopback. Reach the host at " +
			"`host.containers.internal` — including host services bound to the host's own `127.0.0.1`, which yolo " +
			"has the network stack forward in. Which ADDRESS that name resolves to depends on the host's network " +
			"stack, so read it (`getent hosts host.containers.internal`) rather than assuming one. " +
			"`$YOLO_HOST_LOOPBACK` says what the launcher decided: `requested`/`shared` mean forwarding is in " +
			"place, and `unknown` means the launcher did not ask — on a macOS podman machine that is the normal " +
			"value and the hop usually works anyway, so probe it before concluding it does not."
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

	// Report what was emitted (backend-parity.md §6): the class the launch passed, in words
	// that claim neither enforcement nor effect. The disk may ignore it, and the agent can
	// raise it, so "kernel-enforced" or "in effect" would be two different false sentences.
	// The schedulers named as ignoring it are the grading's own (ioprio.IgnoredBy), so the
	// briefing and the launch line cannot disagree about mq-deadline and "low". And it is
	// scoped to the jail: wherever the host nix daemon is mounted the jail runs with
	// NIX_REMOTE=daemon, so a `nix build` runs in the daemon's builders on the host, which
	// no process here started (docs/design/io-priority.md §2, Non-Goal 6).
	var ioPriorityLine []string
	if p := ioprio.Priority(in.IOPriority); p.Declared() {
		ioPriorityLine = []string{
			"- **Disk I/O priority**: `" + in.IOPriority + "` (" + p.ClassName() + "), set on every " +
				"process in this jail at boot so builds here yield the disk under contention. " +
				"Advisory, not a limit: a process can raise its own, it does not reach buffered " +
				"writeback, disks whose scheduler is " + joinOr(ioprio.IgnoredBy(p)) + " ignore it, " +
				"and work a host process does for the jail is outside it: a `nix build` through the " +
				"host nix daemon keeps the host's priority.",
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

	lines := append([]string{}, confinementHeader(in.Confinement, in.Mechanism, in.IsMacOS)...)
	lines = append(lines, provisioningFailed...)
	// An acknowledged attach's account of the jail it entered, beside provisioning's: both say the
	// environment is not what it should be, and both are absent in the common case.
	lines = append(lines, attachSkewSection(in.AttachSkew)...)
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
	home := in.Home
	if home == "" {
		home = "/home/agent"
	}
	// THE WORKSPACE BULLET EXISTS TO EXPLAIN AN ALIAS, AND ON A NATIVE BACKEND THERE IS
	// NONE. A container binds the host directory at `/workspace`, so the paragraph's job
	// is to say that the two are one thing and no sync step stands between them. A
	// macos-user launch mounts nothing: the agent is in the host directory, at its real
	// path, and `/workspace` does not exist at all.
	//
	// RULED 2026-09-13: name the absence and keep `/workspace` canonical. The three
	// built-in skills carry 25 references to `/workspace` and are static markdown with no
	// templating, so substituting the real path everywhere was the larger change and
	// making `/workspace` real (an /etc/synthetic.conf entry, the mechanism nix uses for
	// /nix) was not taken. The consequence is that this bullet is the ONE place a
	// macos-user agent is told those references mean its own path — so it says so
	// explicitly rather than leaving the reader to infer it from a path that looks
	// different.
	envLines := []string{"## Environment", ""}
	if slices.Contains(paths.NativeRuntimes, in.Mechanism) {
		envLines = append(envLines,
			"- **Workspace**: `"+in.Workspace+"` — the host directory itself, not a",
			"  copy and not a mount. Edits here ARE the host's; there is never a git",
			"  pull/push, fetch, or any sync step for this directory.",
			"  ⚠ There is no `/workspace` on this backend. Skills and docs that name",
			"  `/workspace` — including the built-in ones — mean the path above.",
			// Not "persistent across sessions": true of this account home, but it hid the
			// fact that matters, that one home serves every workspace on the machine
			// (docs/design/durable-scratch-space.md §2.2, §4.1).
			"- **Home**: `"+home+"` (one account home, shared by every workspace on this",
			"  machine; its per-workspace directories are links into this workspace's `.yolo/home`)",
			"- **OS**: macOS, Seatbelt-confined (no container, no systemd, no sudo)",
		)
	} else {
		envLines = append(envLines,
			"- **Workspace**: `/workspace` is the host directory `"+in.Workspace+"`,",
			"  bind-mounted LIVE — the same files, not a copy. Host-side edits are",
			"  instantly visible here and vice versa; there is never a git",
			"  pull/push, fetch, or any sync step between the jail and the host",
			"  for this directory.",
			// NOT "(persistent across sessions)", which this line said until the durable-paths
			// slice: on podman the home is a read-only bind, so the claim invited writes that
			// fail, and the agent fell back to /tmp, which is deleted after the jail exits
			// (docs/design/durable-scratch-space.md §2.5).
			"- **Home**: `"+home+"`"+homeLineNote(in.Persistence, home),
			"- **OS**: NixOS-based minimal container (no systemd, no sudo)",
		)
	}
	lines = append(lines, append(envLines, networkLine)...)
	// THE NETWORK PRIMITIVE'S FACT (OQ-NC3, docs/plans/notch-convergence.md, ruled 2026-09-28,
	// A): a jail on a shared namespace keeps its autonomy, and is told so beside the network
	// line it qualifies. The sentence is render's (SharedNetworkFact), the one the launch line
	// prints, so the agent's copy and the human's cannot disagree.
	if fact := render.SharedNetworkFact(briefingProfile(in, netMode)); fact != "" {
		lines = append(lines, "- **Shared network**: "+fact)
	}
	lines = append(lines, publishedPorts...)
	lines = append(lines, forwardedPorts...)
	lines = append(lines, resourceLine...)
	lines = append(lines, ioPriorityLine...)
	if in.HostNix {
		lines = append(lines,
			"- **Nix** uses the host's daemon and store (`NIX_REMOTE=daemon`). A `result`",
			"  link, `--out-link`, `nix profile` or `.direnv` root made here is NOT a GC root",
			"  the host honors, so a host garbage collection can delete its target between",
			"  commands (a running process keeps what it uses). If a link dangles, rebuild it.",
		)
	}
	lines = append(lines,
		"",
		"⚠ rg is recursive by default — never pass grep-style `-r`/`-rn` flags",
		"(in rg, `-r` means `--replace` and silently corrupts match output).",
		"Use `rg -n <pattern> [path]`.",
		"",
	)
	// Right after the Environment block whose Home line points at it, and before every
	// capability section: where work survives decides where an agent puts it, so it has to
	// be read before the agent plans anything (docs/design/durable-scratch-space.md §4.1).
	wsShown := "/workspace"
	if slices.Contains(paths.NativeRuntimes, in.Mechanism) {
		wsShown = in.Workspace
	}
	lines = append(lines, persistenceSection(in.Persistence, in.Durable, wsShown, in.Workspace, home)...)

	// BEFORE the capability sections, deliberately: these are constraints that change
	// how everything below them should be read, and a constraint discovered after the
	// capability it qualifies has already been read too late.
	if len(in.BackendLimits) > 0 {
		lines = append(lines, "## What this environment does NOT do for you", "")
		for _, l := range in.BackendLimits {
			lines = append(lines, "- "+l)
		}
		lines = append(lines, "")
	}
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

	// THE MODE IS PER ENTRY, where the heading used to say "(read-only)" for all of them:
	// a read-write config element is the host's own directory, and an agent told it was a
	// read-only view would treat a write there as scratch (docs/design/context-mounts.md
	// §3.8). The path printed is the one the agent opens on THIS backend, and the context
	// dir's variable is named so pack prose spelling `$YOLO_CONTEXT_DIR/<rel>` resolves.
	if len(in.ContextMounts) > 0 {
		ctxDir := in.ContextDir
		if ctxDir == "" {
			ctxDir = paths.ContainerContextDir
		}
		lines = append(lines, "## Additional Context Mounts", "",
			"Host directories mounted into this jail. `$"+paths.ContextDirEnv+"` is `"+ctxDir+"` here.",
			"")
		anyRW := false
		for _, m := range in.ContextMounts {
			mode := "read-only"
			if m.ReadWrite {
				mode, anyRW = "read-write", true
			}
			entry := "- `" + m.Path + "` (" + mode + "; host `" + m.Host + "`"
			if m.Pack != "" {
				entry += "; from pack `" + m.Pack + "`"
			}
			lines = append(lines, entry+")")
		}
		if anyRW {
			lines = append(lines, "",
				"A read-write mount is the host's own directory, not a copy: what you write there",
				"is what the host reads.")
		}
		// THE LINK DELIVERY'S TWO DELTAS, said where an agent would trip on them
		// (docs/design/context-mounts.md §3.7, §3.8). With no container there is no mount
		// namespace: each entry is a link to the host folder itself, so a tool that resolves
		// paths reports the host's, and the sandbox account's file permissions apply below the
		// folder, which a container's root would have read past.
		if MechanismHasNoContainer(in.Mechanism) {
			lines = append(lines, "",
				"Each is a link to the host folder itself, not a mount: `pwd -P`, `realpath` and",
				"git print the host path, and a subfolder this account may not read stays",
				"unreadable here (`Permission denied`).")
		}
		lines = append(lines, "")
	}

	// DP-B1's SECOND briefing site. The `/ctx` clause was unconditional, so a jail with no
	// `mounts` at all — and every macos-user jail, which bound none before it delivered them by
	// link — was told a read-only filesystem existed that nothing had mounted. The "Additional Context Mounts" section above lists what was BOUND; this line
	// describes the same thing, so the two have to appear and disappear together. Fixing one
	// and not the other is how the first fix was found to be half a fix.
	noSudoLine := "- No sudo/root."
	if len(in.ContextMounts) > 0 {
		noSudoLine = "- No sudo/root; context mounts under `$" + paths.ContextDirEnv +
			"` are read-only unless marked read-write."
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
		noSudoLine,
		"",
	)
	lines = append(lines, packagesSection(in.Mechanism)...)
	lines = append(lines,
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

	return strings.Join(lines, "\n") + "\n"
}

// packagesSection is how an agent asks for a tool — and, on a container, for a bigger cap.
//
// IT BRANCHES BECAUSE THE SECOND HALF IS FALSE OFF-CONTAINER, which the `## Environment` fix
// above did not reach: it told every agent to request a "container-limit change" by editing
// `resources`, on a backend with no container where `resources` is read and IGNORED by ruling
// (DP-D1 — RLIMIT_AS is address space rather than RSS, RLIMIT_NPROC is per-USER and collides
// across concurrent sessions on the shared account, both rejected by name). Instructing an
// agent to ask its human for a limit nothing can deliver is worse than a wrong path: the human
// grants it, the config carries it, and the cap does not exist. DP-D1's own words are the rule
// applied here — "a cap a user believes in but that does not hold is worse than a documented
// absence" — so the absence is NAMED rather than left as silence, the same disposition every
// other unreadable declaration on this backend gets.
//
// ⚠ `/workspace` IS KEPT ON BOTH ARMS, deliberately. The 2026-09-13 ruling made it canonical
// and spends the Environment bullet explaining that it means the real path on a native backend;
// a second spelling here would fork the convention that bullet exists to establish, and this
// section is not the one place an agent learns its paths.
func packagesSection(mechanism string) []string {
	if MechanismHasNoContainer(mechanism) {
		return []string{
			"## Packages",
			"",
			"To request a tool: edit `/workspace/yolo-jail.jsonc` (`packages`), ALWAYS run",
			"`yolo check` after every config edit (`yolo check --no-build` is fine inside a",
			"running jail), then ask the human to restart the jail. Reference: `yolo config-ref`.",
			"⚠ `resources` is not enforced here — there is no container to cap, so a memory or",
			"CPU limit in the config is read and ignored. Do not plan around one, and do not ask",
			"for one: it cannot be delivered on this backend.",
			"",
		}
	}
	return []string{
		"## Packages & Resource Limits",
		"",
		"To request a tool or a container-limit change: edit `/workspace/yolo-jail.jsonc`",
		"(`packages` / `resources`), ALWAYS run `yolo check` after every config edit",
		"(`yolo check --no-build` is fine inside a running jail), then ask the human to",
		"restart the jail. Reference: `yolo config-ref`.",
		"",
	}
}

// HostBriefingBase is the host notch's base body for every briefing destination `yolo host apply`
// composes: BriefingContent at the host notch (the confinement header — the real machine, nothing
// disposable, autonomy off) with agents_md_extra appended exactly as a jail appends it
// (ComposeBriefing). extra is the USER-SCOPE value, which is the caller's to read: a workspace
// yolo-jail.jsonc is agent-editable, and what it says must not reach a file in the real home.
// isMacOS picks the platform's spelling of the (empty) enforcement vector, as the jail's header
// does.
func HostBriefingBase(extra string, isMacOS bool) string {
	return ComposeBriefing(BriefingContent(BriefingInput{Confinement: render.KindHost.String(), IsMacOS: isMacOS}), extra)
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
//
// The error is a read that failed for any reason but absence, with jailContent
// returned beside it: the briefing still composes, and the caller says what was
// left out. It used to be swallowed with absence, so a host briefing that existed
// and could not be read vanished from the jail without a word. A dangling link
// reads as absent here (ENOENT); the caller names that one before it calls.
func PrependHostBriefing(hostBriefingPath, jailContent string) (string, error) {
	data, err := os.ReadFile(hostBriefingPath)
	if err != nil {
		if os.IsNotExist(err) {
			return jailContent, nil
		}
		return jailContent, err
	}
	return string(data) + "\n---\n\n" + jailContent, nil
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

// PackBriefing is one pack's contribution to an agent briefing (C3).
//
// ONE PER SOURCE FILE since the briefing/ convention (docs/reference/pack-system.md#briefing-governance; one per contribution before it, one per
// PACK before docs/reference/agent-briefings.md#where-each-notch-narrows). A pack's entries arrive contiguous and in filename order
// (run.packBriefingProses, over packload.GovernedSources), each with its own audience, so a
// destination receives exactly the files addressed to it and still reads them as one section.
type PackBriefing struct {
	// Name is the pack's name, used for the provenance header.
	Name string
	// Text is the prose of ONE of the pack's governed briefing sources — a file under its
	// briefing/ directory, or the one a contribution's `from` names — already read. Never a root
	// AGENTS.md: that is the repository's own file, and yolo does not ship it (P1).
	Text string
	// Agents is the AUDIENCE this prose names — the launcher commands it is FOR. EMPTY MEANS
	// BROADCAST, which is the pre-field behavior and the only behavior a pack with no
	// pack.json can ask for (docs/reference/agent-briefings.md#ba-p2).
	//
	// It holds the audience rather than a resolved destination because a content pack names
	// WHO and never WHERE: where an agent reads is that agent pack's business and changes
	// when the agent changes (P4). ComposePackBriefings matches it against the identity the
	// DESTINATION declared.
	Agents []string
}

// ComposePackBriefings appends each pack's prose to ONE DESTINATION's briefing, in config
// order.
//
// `agent` is the identity that destination declared for itself, or "" for a destination that
// declared none. It is what makes this per-DESTINATION rather than per-jail, and moving that
// call inside the write loop is the jail half of docs/reference/agent-briefings.md#where-each-notch-narrows: before, one body was
// composed once and written to every destination, so a pack whose rules applied to one agent
// had to broadcast them to all of them.
//
// THE MATCH IS AGAINST A DECLARED STRING, never anything derived (OQ-BA2). So a destination
// that declares no identity is simply never named by any selector (R4) — an addressed
// contribution skips it, and a broadcast one still reaches it.
//
// `provenance` labels each pack's SECTION with `<!-- from pack: NAME -->`, and it is OFF unless
// the user's `briefing_provenance` asks for it (config.BriefingProvenance says why: the label
// made agents treat the user's own every-repository rules as someone else's, and Claude strips
// HTML comments before it reads the file anyway). Off, entries are separated by one blank
// line and nothing else — ComposeBriefingSections assembles the file, the host notch's too.
//
// ONE LABEL PER CONTIGUOUS RUN of one pack's delivered entries, not one per entry: a pack's
// several briefing/ files are ONE section headed by the pack's one label
// (docs/reference/pack-system.md#briefing-directory), and a file the pack routed elsewhere is
// simply absent — it does not split the section in two. That is the unit the host composes (one section per pack per destination).
//
// Empty text is skipped rather than emitting an empty section: a pack with no
// briefing should leave no trace.
func ComposePackBriefings(base string, packs []PackBriefing, agent string, provenance bool) string {
	var sections []BriefingSection
	for _, p := range packs {
		if !addressesAgent(p.Agents, agent) {
			continue
		}
		text := strings.TrimRight(p.Text, " \t\r\n")
		if text == "" {
			continue
		}
		// A contiguous run of one pack's entries is ONE section: its files joined by one blank
		// line, under one label.
		if n := len(sections); n > 0 && sections[n-1].Pack == p.Name {
			sections[n-1].Text += "\n\n" + text
			continue
		}
		sections = append(sections, BriefingSection{Pack: p.Name, Text: text})
	}
	return ComposeBriefingSections(base, sections, provenance)
}

// BriefingSection is one pack's section of one destination's briefing: the pack, and all of its
// prose for that destination already joined.
type BriefingSection struct {
	Pack string
	Text string
}

// ComposeBriefingSections is THE PER-DESTINATION BRIEFING COMPOSER, at every notch
// (docs/plans/notch-convergence.md item 26, row D7): the notch's base body, then each pack's
// section in order, one blank line between them, each headed by `<!-- from pack: NAME -->` only
// when `provenance` is on. The jail calls it with BriefingContent plus agents_md_extra as the
// base (ComposePackBriefings); `yolo host apply` with HostBriefingBase
// (entrypoint.ComposeHostBriefings). Which packs' prose reaches a destination is each notch's
// own question — the jail matches an audience against the destination's declared identity, the
// host follows the destinations the packs name (agent-briefings.md#where-each-notch-narrows) —
// and how a destination's file is ASSEMBLED from it is this function's alone, so one pack set
// reads the same at both.
//
// An empty base composes the sections alone, with nothing ahead of the first; an empty section
// is skipped rather than emitting an empty one, so a pack with no prose leaves no trace.
func ComposeBriefingSections(base string, sections []BriefingSection, provenance bool) string {
	out := base
	for _, sec := range sections {
		text := strings.TrimRight(sec.Text, " \t\r\n")
		if text == "" {
			continue
		}
		section := text + "\n"
		if provenance {
			section = BriefingProvenanceLabel(sec.Pack) + "\n" + section
		}
		if out == "" {
			out = section
			continue
		}
		out = strings.TrimRight(out, "\n") + "\n\n" + section
	}
	return out
}

// BriefingProvenanceLabel is the per-section label, emitted only when `briefing_provenance` is
// on. It is a debugging aid for a human reading a merged file, and off by default: labelled, the
// user's every-repository rules read to an agent as someone else's (config.BriefingProvenance).
func BriefingProvenanceLabel(pack string) string { return "<!-- from pack: " + pack + " -->" }

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

// joinOr renders names as a sentence alternative: "a", "a or b", "a, b or c".
func joinOr(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
