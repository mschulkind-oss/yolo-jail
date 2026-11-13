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
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// BlockedTool is one entry of the "Blocked Tools" section (name + optional
// message + optional suggestion).
type BlockedTool struct {
	Name       string
	Message    string
	Suggestion string
	// Replacement is the tool a block points to instead (`rg` for `grep`), "" for none. The core
	// names it, and the `rg -r` warning is rendered only when it is `rg` (BD-D7).
	Replacement string `json:",omitempty"`
	// Guard is the guard kind of an entry that refuses only some arguments rather than blocking
	// the tool (`workspace-state`), "" for a block.
	Guard string `json:",omitempty"`
}

// Loophole is a (name, description) pair for the loopholes section.
type Loophole struct {
	Name            string
	Desc            string
	SwitchAvailable bool
	// Standing is one line saying what this loophole holds in this project, for a grant-declaring
	// loophole that holds nothing here, and the step that would grant it
	// (docs/reference/loophole-grants.md §5): an agent reading the section then does not
	// debug a socket that never crossed. "" for an ungated loophole and for one holding a grant.
	Standing string
}

// ContextMount is one entry of the "Additional Context Mounts" section: a context mount
// (docs/reference/context-mounts.md#defined-terms) this launch actually BOUND.
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
	// Copied is true for a pack's single-file `mount` that macos-user COPIES into the context
	// dir at launch instead of linking (docs/reference/context-mounts.md#CX-D25): the agent reads a
	// snapshot, and a host edit arrives at the next launch, which the entry says.
	Copied bool
	// Optional is true for a config `mounts` element marked "optional"
	// (docs/reference/context-mounts.md#optional-mounts): the entry says it may be absent.
	Optional bool
}

// BriefingInput carries everything the jail-managed briefing content depends
// on. Workspace is the host workspace path (rendered verbatim);
// ProvisioningFailed is true when the last boot's .yolo/logs/startup.log contained
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
	// Sealed is true for a fork's or a patched extension's BUILD jail: THE SEAL, a term coined
	// by docs/design/forked-programs-as-packs.md (FP-D9), for a launch handed no credential and
	// no host file. Its bridge is the runtime's own, with no forwarding of the host's loopback
	// asked for (FP-D13), so the bridge paragraph says that instead of telling the build that
	// host services are forwarded in. The caller leaves out the rest the seal withholds itself:
	// the port lists, the loopholes, the context mounts and HostNix.
	Sealed bool
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
	// Setup is the selected setup and its fields. SetupDescription is the user-facing spelling
	// supplied by the launch pipeline; empty uses setup.Describe.
	Setup            setup.Setup
	SetupDescription string

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
	// carries: a `guest` notch whose caller resolved no mechanism. A macOS guest launch
	// passes Mechanism "macos-user", which decides it (env-manager plan Phase 7.1); a
	// Linux guest has no backend (Phase 7.2), so the platform picks the Landlock spelling
	// of that preset. Every other combination is decided by Mechanism and ignores this.
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
	// as stale (docs/reference/in-jail-nix-roots.md "Why a jail-spelled root is lost"). macos-user shares the host's
	// filesystem, so its roots are real and it never sets this.
	HostNix bool

	// Persistence is the launch's persistence map (persistence.go): which paths survive a
	// restart, which are shared by every workspace, and which are gone once the jail
	// exits. The run pipeline builds it from the definitions the mount argv reads, and it
	// renders the storage-classes section (docs/reference/jail-home.md#storage-classes-and-the-durable-dir
	// §4.1). Nil renders no section: macos-user, the host notch and a hand-built input.
	Persistence *PersistenceMap

	// Durable is the launch's durable dir (persistencesection.go): the path the section LEADS
	// with, or why there is none. Nil renders no durable line, which is every caller that
	// has not made one — the host notch (OQ-DS3: a static sentence in the host header
	// instead) and a hand-built input. Non-nil with no map (macos-user) renders the section
	// with the durable answer alone.
	Durable *DurableDir

	// ConfigName is the workspace config file the briefing tells the agent to edit: the name
	// the loader reads it under (config.ResolveWorkspaceConfigPath), so `yolo-jail.json` in a
	// workspace that keeps that file. Empty means config.WorkspaceConfigName. The caller
	// resolves it, as it reads the provisioning log: this function reads no file. Naming the
	// `.jsonc` file beside a `yolo-jail.json` told the agent to create a file the next launch
	// reads in its place, dropping every key the user's own file sets.
	ConfigName string

	// ConfigLocked is true when this launch made that file read-only to the agent: any
	// `workspace_readonly` entry locks it (run.workspaceConfigLocked), so the agent cannot edit its
	// own protection out. The packages section then tells the agent to propose the edit to the human
	// instead of making it, which is the only edit open to it.
	ConfigLocked bool

	// ShippedSkillPrefix is the user's `shipped_skill_prefix` (config.ShippedSkillPrefix): the
	// skills section names each built-in skill as it is delivered, `<prefix>-<skill>`
	// (docs/reference/pack-system.md PN-D13). Empty names them bare, as before. The caller reads
	// it, as it reads the provisioning log: this function reads no file.
	ShippedSkillPrefix string

	// InlineTopics is true when this entry did NOT write the jail-mechanics skill's `topics/`
	// from this input: no fact sheet (a legacy attach keeps the prior skill byte for byte), or a
	// render that failed. The topics are then appended to the body in full, so the core never
	// points at a topic file that is not there (docs/design/briefing-diet.md §5, the trap in
	// briefing-diet-plan.md). The run pipeline decides it per entry; it is never carried over from
	// an applied snapshot.
	InlineTopics bool `json:"-"`

	// MechanicsSkillPath is the jail-mechanics SKILL.md of the agent this destination belongs to,
	// home-relative with a leading `~/` (BD-D6), or "" for a destination with no skills directory
	// of the same identity: the pointer then names the skill without a path. The run pipeline sets
	// it per destination.
	MechanicsSkillPath string `json:"-"`
}

// configName is the workspace config file name the briefing names (BriefingInput.ConfigName).
func (in BriefingInput) configName() string {
	if in.ConfigName != "" {
		return in.ConfigName
	}
	return config.WorkspaceConfigName
}

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
// the notch is called. isMacOS decides only the guest variant no mechanism names: a macOS
// guest launch names macos-user (env-manager plan Phase 7.1), so this is a reader that
// resolved no mechanism, or a Linux guest, which has no backend (Phase 7.2) — and the
// platform's spelling is the best available answer for both.
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
	s := briefingSetup(in.Setup)
	return LaunchProfile(render.KindForSetup(s.Kind()), in.Mechanism, in.IsMacOS, netMode == "host")
}

func briefingSetup(s setup.Setup) setup.Setup {
	if s.Name == "" {
		return setup.MustBuiltIn("podman-self")
	}
	return s
}

func briefingSetupDescription(in BriefingInput, s setup.Setup) string {
	if in.SetupDescription != "" {
		return in.SetupDescription
	}
	return setup.Describe(s)
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

// macosUserHomeLines are the macos-user home sentences. Since slice 12a HOME is THIS workspace's
// own sidecar (its setup's home, `<ws>/.yolo/setups/<setup>/home`, named-setups.md §9) in both
// modes, run as the second account or as you (macos-user-model-plan.md MMP-D5, MMP-D25), so a
// second workspace has a home of its own; what every workspace shares is the machine-wide state its
// links reach. Until the Mac run of 2026-11-03 this said the account home was shared by every
// workspace, which stopped being true with 12a. The last sentence is SD-D2's stake: a `mv` of
// `.yolo` moves this home, and the session reading this with it.
//
// They were the header's until the briefing diet (docs/design/briefing-diet.md §4): the core now
// says the same in its "What survives a restart" line, and these, verbatim, open the storage topic.
var macosUserHomeLines = []string{
	"Your home is THIS workspace's own (`.yolo/setups/<setup>/home`) and it PERSISTS between",
	"launches; another workspace has its own. Only its links to machine-wide state, the",
	"logins and caches your packs declare at `scope: machine`, reach directories that",
	"are shared by every workspace on this machine. Your own session is in that home: never",
	"move, rename or delete `.yolo` or anything in it but your own files in `.yolo/durable`.",
}

// toolingLine names the CLI's own help. The host notch's header keeps it; a jail's moved to the
// jail-mechanics index with the briefing diet (docs/design/briefing-diet.md §4), so the core spends
// no bytes on what the index carries.
const toolingLine = "Jail tooling: `yolo --help`; config reference: `yolo config-ref`."

// setupHeader names the selected setup and spells its fields, while deriving the enforcement
// vector from the setup kind and applied backend. It ends with a blank line.
func setupHeader(s setup.Setup, description, mechanism string, isMacOS bool) []string {
	kind := render.KindForSetup(s.Kind())
	prof := ConfinementProfile(kind, mechanism, isMacOS)
	title := "# YOLO Environment — setup " + s.Name
	intro := "You are running under setup **" + s.Name + "**: " + description + "."

	switch s.Kind() {
	case setup.KindHost:
		return append(append([]string{
			title, "", intro,
			"This is the human's REAL machine, with no container around you. Changes are NOT disposable.",
			"You have: their real credentials, their real dotfiles, no snapshot to fall back on.",
			"Absent: nothing is mounted read-only; there is no jail to restart; `sudo` is real.",
			"`/tmp` is this machine's own: it survives an agent restart but may not survive a reboot; put worktrees you need later inside the repository or beside it.",
		}, enforcementLines(prof, false)...), toolingLine, "")
	case setup.KindGuest:
		return append([]string{title, "", intro, "This setup runs on the real machine, not in a disposable container."}, enforcementLines(prof, true)...)
	case setup.KindJail:
		if MechanismHasNoContainer(mechanism) {
			return append([]string{title, "", intro, "This setup runs on the real machine, not in a container."}, enforcementLines(prof, true)...)
		}
		return []string{title, "", intro, "You are running inside a container.", ""}
	default:
		return append([]string{title, "", intro,
			"This setup's boundary is not recognized. Do not assume a container; the enforcement details below are the only ones established."},
			enforcementLines(ConfinementProfile(render.KindUnset, mechanism, isMacOS), true)...)
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
// copy. It ends with a blank line.
//
// compact says the autonomy sentence in one short line, for a jail briefing whose header counts
// against the core's budget (docs/design/briefing-diet.md §8); the host notch's body is unchanged
// by the diet (§10), so it keeps the long form.
func enforcementLines(prof render.Profile, compact bool) []string {
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
	switch {
	case compact && prof.AgentAutonomy:
		lines = append(lines, "", "Agent autonomy is **ON**: no permission prompts, safe only because the boundary above contains you.")
	case compact:
		lines = append(lines, "", "Agent autonomy is **OFF**: permission prompts stay on, because nothing above contains you. Do not try to disable them.")
	case prof.AgentAutonomy:
		lines = append(lines,
			"",
			"Agent autonomy is **ON**: your tools run without permission prompts, which is safe",
			"only because the boundary above contains you.")
	default:
		lines = append(lines,
			"",
			"Agent autonomy is **OFF**: permission prompts stay on, because nothing above",
			"contains you. Do not try to disable them.")
	}
	return append(lines, "")
}

// BriefingContent renders the jail-managed briefing body, which opens yolo's block: the user block
// (the host file, agents_md_extra, non-shipped packs' prose) precedes it and the shipped packs'
// pointers follow it (ComposeBriefingFile). The lines joined with "\n" plus a trailing newline.
//
// SINCE THE BRIEFING DIET (docs/design/briefing-diet.md) a jail's body is three parts: the setup
// header, any alert (provisioning failed, the attach-skew notice, the handoff), and the CORE, a
// budgeted page of the facts whose breach is silent (briefingcore.go). Everything else the body
// used to say is a TOPIC (briefingtopics.go), rendered from this same input into the generated
// jail-mechanics skill's `topics/` — or, when this entry did not write that skill
// (BriefingInput.InlineTopics), appended here in full, so the core never points at a file that
// is not there. The core's size is held by TestTheCoreFitsItsBudget.
//
// The network mode is AppliedNetMode when set, else NetMode, else "bridge".
func BriefingContent(in BriefingInput) string {
	// THE BASE IS NOTCH-AWARE (docs/plans/notch-convergence.md item 26, row D7). Everything below
	// the header describes a LAUNCH, and at the host notch there is no launch: the agent is the
	// human's own process on the real machine, and every one of those sections would be a false
	// sentence there. So the host's base is its confinement header and nothing else. It is what
	// `yolo host apply` composes ahead of the packs' prose (HostBriefingBase).
	s := briefingSetup(in.Setup)
	if s.Kind() == setup.KindHost {
		return strings.Join(setupHeader(s, briefingSetupDescription(in, s), in.Mechanism, in.IsMacOS), "\n") + "\n"
	}
	v := newBriefingView(in)
	topics := BriefingTopics(in)

	lines := append([]string{}, setupHeader(s, briefingSetupDescription(in, s), in.Mechanism, in.IsMacOS)...)
	lines = append(lines, briefingAlerts(in, v)...)
	lines = append(lines, coreLines(in, v, topics)...)
	if in.InlineTopics {
		for _, t := range topics {
			lines = append(lines, "## "+t.Title, "")
			lines = append(lines, t.Body...)
		}
		lines = append(lines, inlineSkillsSection(in)...)
	}
	return strings.Join(lines, "\n") + "\n"
}

// briefingAlerts are the conditional sections that report an event rather than describe the jail
// (an "alert", docs/design/briefing-diet.md §1). They stay always-loaded, after the header and
// outside the core's budget (BD-D5): each appears only after its event, and each is something the
// agent must see without asking.
func briefingAlerts(in BriefingInput, v briefingView) []string {
	var lines []string
	if in.ProvisioningFailed {
		lines = append(lines,
			"## ⚠ Provisioning failed",
			"",
			"The last boot's provisioning failed — project tools may be missing.",
			"Read `"+v.workspace+"/.yolo/logs/startup.log` and self-serve (e.g. run",
			"`mise install` in "+v.workspace+", then re-run the step that failed).",
			"",
		)
	}
	// An acknowledged attach's account of the jail it entered, beside provisioning's: both say the
	// environment is not what it should be, and both are absent in the common case.
	lines = append(lines, attachSkewSection(in.AttachSkew)...)
	// The handoff, if one was handed over for this launch: a one-time transition task, surfaced
	// once (the run pipeline consumes the pointer once this briefing is written, so it never
	// returns as a stale task). "Handed over" rather than "the host agent handed over": a jail
	// agent filing a pointer for its successor uses the same carrier (agent-briefings.md).
	//
	// When there is no handoff (the common case) no section appears and NO standing line is
	// added: the one-time-ness is stated INSIDE the conditional section, where it costs the
	// header's pinned bytes nothing (TestBriefingJailHeaderIsUnchanged).
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
	return lines
}

// briefingMenuExample is the approval menu the briefing shows as its example: the same
// renderer (approvalwait.Menu.Render) prints the git-broker's and the github-broker's, so the
// example cannot drift from what a refusal really says. A placeholder id, since the example
// is read, not run.
var briefingMenuExample = approvalwait.Menu{
	ID:       "r-<id>",
	Retry:    "run the refused command again",
	Project:  []approvalwait.ProjectStep{{Command: "yolo request git push <branch>"}},
	Fallback: "git push",
}

// hostRunSection teaches the agent what to do when the human must act on the host: the three
// rules (approvals are not host-run; each human command alone in a fenced block; the waiter
// started in the background in the same turn), the approval menu a refusal prints, and
// `yolo host-run` (docs/reference/host-run-mailbox.md, HX-D41), one line a human runs on the
// host whose output comes back here. It is the body of the host-requests topic
// (briefingtopics.go), whose title is its old heading; the core keeps the three rules in one line.
// It names no harness: a pack that installs one adds how its background task is started.
func hostRunSection() []string {
	lines := []string{
		"Three rules whenever the human must do something on the host:",
		"",
		"- **Approvals are not host-run.** `yolo approve`, `yolo config apply` and `yolo allow` are the",
		"  human's own answers: they run them directly in a host terminal, never through `yolo host-run`.",
		"- **Each command for the human goes alone on its own line in a fenced code block**, one per",
		"  line, never inline in prose: it is copied, not read.",
		"- **Start the waiter in the background in the same turn** as you hand the human something, so",
		"  its exit re-invokes you: `yolo wait r-<id>` for an approval request, `yolo wait <label>` for a",
		"  host-run line. Exit 0: retry the refused command exactly once. Exit 75: give the human the",
		"  commands again and stop. Any other exit: report what it printed, and do not retry.",
		"",
		"A git or gh write that needs approval is refused with a menu of every path that answers it.",
		"Relay the whole menu, and recommend one path: the project request and then `yolo config apply`",
		"for a branch you will push again; `yolo approve r-<id>` alone for a one-off.",
		"Never report a host capability as impossible: the refusal names a path. A refusal's menu",
		"reads like this:",
		"",
		"```text",
	}
	lines = append(lines, strings.Split(strings.TrimRight(briefingMenuExample.Render(), "\n"), "\n")...)
	return append(lines,
		"```",
		"",
		"If you have no background task whose exit wakes you, run `yolo wait --timeout <less than your",
		"tool's timeout> r-<id>` once in the foreground. On exit 75, give the human the command and stop;",
		"when they say they are done, run `yolo wait r-<id>` again. Never wait in a loop. A bare",
		"`yolo wait` lists the requests still waiting on you, after a restart too.",
		"",
		"When you need something only the host can do, write ONE line for the human to run in a host",
		"terminal, and the output comes back to you; nothing needs to run here first:",
		"",
		"```",
		"yolo host-run -C <value of $YOLO_HOST_DIR> --name <fresh-label> -- <command>",
		"```",
		"",
		"Then start `yolo wait <fresh-label>` in the background (run it again if it exits 75), or ask",
		"the human to say \"done\" and run `yolo host-run show --name <fresh-label>`. Start the line",
		"with bare `yolo host-run`; no `;`, `&&`, `|`, `>` or `$(`; single-quote words with shell",
		"characters; keep it under 300 characters (long text goes in a workspace file the command",
		"reads). Typing the line here prints it ready to paste. If the host's yolo has no `host-run`,",
		"ask the human to run the command and paste the output.",
		"",
	)
}

// HostBriefingBase is the host notch's base body for every briefing destination `yolo host apply`
// composes: BriefingContent at the host notch (the confinement header — the real machine, nothing
// disposable, autonomy off). It opens yolo's block, as BriefingContent does a jail's; the
// user-scope agents_md_extra is user prose and goes in the user block (BriefingParts.Extra,
// docs/design/briefing-diet.md §7). isMacOS picks the platform's spelling of the (empty)
// enforcement vector, as the jail's header does.
func HostBriefingBase(isMacOS bool) string {
	return BriefingContent(BriefingInput{Setup: setup.MustBuiltIn("host-self"), IsMacOS: isMacOS})
}

// BriefingParts is one destination's material, by whose it is (docs/design/briefing-diet.md §7).
type BriefingParts struct {
	// HostFile is the user's own file the destination's `after` names, as read: "" for none, for
	// one the caller would not read, or for one that is yolo's own output.
	HostFile string
	// Extra is agents_md_extra: the workspace's at a jail, the user scope's at the host notch.
	Extra string
	// Base is yolo's base body: the header, any alert and the core (BriefingContent), or the host
	// notch's header (HostBriefingBase).
	Base string
	// Sections are every pack's section for the destination, in config order. A Shipped one is
	// yolo's pointer; any other is the user's prose.
	Sections []BriefingSection
	// Provenance labels each pack's section (BriefingProvenanceLabel).
	Provenance bool
}

// ComposeBriefingFile is THE TWO-BLOCK ORDER, at every notch (docs/design/briefing-diet.md §7, the
// owner's ruling BD-D11): the USER block first — the host file, then agents_md_extra, then each
// non-shipped pack's section in config order — and yolo's block last — the base, then each
// shipped pack's section in config order. One `---` rule separates them when both are non-empty;
// either alone is the whole file, with no rule. So an agent that cuts a long file from the end
// loses yolo's text before any of the user's.
//
// The alerts stay inside the base, at the top of yolo's block (BD-D5), not ahead of the user's
// prose: the ruling puts the user's rules first, and an alert is yolo's text.
func ComposeBriefingFile(p BriefingParts) string {
	var user string
	for _, text := range []string{p.HostFile, p.Extra} {
		text = strings.TrimRight(text, " \t\r\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		if user != "" {
			user += "\n"
		}
		user += text + "\n"
	}
	var mine, yolos []BriefingSection
	for _, sec := range p.Sections {
		if sec.Shipped {
			yolos = append(yolos, sec)
		} else {
			mine = append(mine, sec)
		}
	}
	user = ComposeBriefingSections(user, mine, p.Provenance)
	yolo := ComposeBriefingSections(p.Base, yolos, p.Provenance)
	switch {
	case user == "":
		return yolo
	case yolo == "":
		return user
	}
	return strings.TrimRight(user, "\n") + "\n\n---\n\n" + yolo
}

// ReadHostBriefing reads the user's host briefing file for the user block: "" when it is absent.
//
// The error is a read that failed for any reason but absence: the briefing still composes without
// it, and the caller says what was left out. It used to be swallowed with absence, so a host
// briefing that existed and could not be read vanished from the jail without a word. A dangling
// link reads as absent here (ENOENT); the caller names that one before it calls.
func ReadHostBriefing(hostBriefingPath string) (string, error) {
	data, err := os.ReadFile(hostBriefingPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
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
	// Shipped is the pack's origin: one yolo ships (packload.Pack.Official). Its prose is yolo's
	// pointer and composes in yolo's block; any other pack's, a forked shipped pack's included, is
	// the user's (docs/design/briefing-diet.md §7).
	Shipped bool
}

// ComposePackBriefings composes each pack's prose to ONE DESTINATION's briefing, in config
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
//
// base is yolo's base body, so a shipped pack's section follows it and any other pack's precedes
// it in the user block (ComposeBriefingFile). The launch composes through ComposeBriefingFile
// itself, with the host file and agents_md_extra beside these sections.
func ComposePackBriefings(base string, packs []PackBriefing, agent string, provenance bool) string {
	return ComposeBriefingFile(BriefingParts{Base: base, Sections: PackBriefingSections(packs, agent),
		Provenance: provenance})
}

// PackBriefingSections is the sections of packs that reach the destination whose declared identity
// is agent, in order: one per contiguous run of one pack's entries, its files joined by one blank
// line. Empty text is skipped.
func PackBriefingSections(packs []PackBriefing, agent string) []BriefingSection {
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
		if n := len(sections); n > 0 && sections[n-1].Pack == p.Name && sections[n-1].Shipped == p.Shipped {
			sections[n-1].Text += "\n\n" + text
			continue
		}
		sections = append(sections, BriefingSection{Pack: p.Name, Text: text, Shipped: p.Shipped})
	}
	return sections
}

// BriefingSection is one pack's section of one destination's briefing: the pack, and all of its
// prose for that destination already joined.
type BriefingSection struct {
	Pack string
	Text string
	// Shipped marks a section of yolo's block: the prose of a pack yolo ships
	// (PackBriefing.Shipped).
	Shipped bool
}

// ComposeBriefingSections is THE PER-DESTINATION BRIEFING COMPOSER, at every notch
// (docs/plans/notch-convergence.md item 26, row D7): the notch's base body, then each pack's
// section in order, one blank line between them, each headed by `<!-- from pack: NAME -->` only
// when `provenance` is on. ComposeBriefingFile calls it once per block, at both notches: for the
// user block over the host file and agents_md_extra, for yolo's over BriefingContent (a jail) or
// HostBriefingBase (`yolo host apply`). Which packs' prose reaches a destination is each notch's
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
