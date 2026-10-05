package render

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// NotchUnbuilt is the ONE sentence yolo says when a verb is asked to act at the `guest`
// notch, with `verb` naming the verb the user typed ("apply", "launch").
//
// IT LIVES HERE BECAUSE TWO PACKAGES SAY IT AND NEITHER CAN IMPORT THE OTHER.
// `cli.applyMain` has printed it since Phase 2 (`yolo apply --at guest` → rc 1); the launch
// gate in `run.Run` says the same thing for the same reason, and internal/cli imports
// internal/cli/run, so the string cannot live in either. internal/render is where it
// belongs on its own merits: this package already owns the notch vocabulary (Kind,
// KindGuest, ProfileFor) and the other notch-keyed user-facing reason text in this file,
// and render.KindGuest's own doc comment carries the same Phase 7 story.
//
// A SECOND COPY IS THE DEFECT, not an inconvenience. docs/design/declaration-parity.md
// exists to name declarations a surface accepts and does not honor, and the two surfaces
// disagreeing about one notch by one word is that defect wearing a smaller hat — which is
// why OQ-DP3 ruled the launch gate must reuse this sentence VERBATIM rather than write its
// own. TestGuestNotchRefusalsShareOneSentence pins the two call sites to this function.
func NotchUnbuilt(verb string) string {
	return fmt.Sprintf("%s at the guest notch is not built yet (env-manager plan Phase 7 "+
		"— the LSM-confined backend).", verb)
}

// FieldSet declares which contribution kinds a target can honor, so an inapplicable
// kind produces a refusal that NAMES the kind rather than a silent skip (BACKLOG G5,
// host-render-target.md §2.1/§6.2). The silent skip is the failure mode G3 shipped —
// a backend rendering zero surfaces every launch with nothing in the output to say so.
//
// The census (host-render-target.md §2.1, restated for the twelve contributes[] kinds):
// only the composed-config kinds are target-independent; the provisioning kinds mean
// nothing without a container. Since OQ-DP5's second half it also classifies every
// top-level CONFIG KEY (configkeys.go, ConfigKey), because a key is a declaration too and
// the kinds-only census could not see one.
type FieldSet struct {
	// applies is the set of kinds this target honors. A kind absent from the map is
	// refused by name (Refuse below).
	applies map[packdecl.Kind]bool
	// keys is the config-key half of the census (configkeys.go): every top-level config key
	// with what this target does with it and why. Nil states no key census (ConfigKey).
	keys map[string]keyCensusEntry
}

// Honors reports whether the target renders this kind. A kind the FieldSet does not
// list is refused — callers use Refuse to produce the message.
func (f FieldSet) Honors(k packdecl.Kind) bool { return f.applies[k] }

// Refuse returns a one-line reason a kind is not honored on this target, or "" if it
// is honored (the caller should not have asked). The reasons are the census's, so the
// message tells the user why, not just that.
func (f FieldSet) Refuse(k packdecl.Kind) string {
	if f.applies[k] {
		return ""
	}
	if r, ok := refusalReasons[k]; ok {
		return r
	}
	return string(k) + " is not applicable at this confinement level"
}

// refusalReasons is the census reason per kind, used when a non-jail target refuses one.
//
// `service` and `loophole` have a second shape, the one the host notch DOES deliver (a service's
// host half, a loophole's credential doorway), and that shape's reason is hostAtLaunch's below:
// their entries here describe only the shape no host verb delivers, because a contribution of
// either kind is told apart per contribution (cli's hostNotchOutcomeOf), not per kind.
var refusalReasons = map[packdecl.Kind]string{
	packdecl.KindProgram: "install is refused below jail (a pack must not mutate a real toolchain unprompted)",
	// A GRANT THROUGH A JAIL'S WALL, not a missing mechanism (docs/design/yolo-as-environment-manager.md
	// §4: the grants are "negotiating a boundary rather than creating one, so at lower notches
	// they are inert"). The old reason, "needs a mount namespace", was false of macos-user, which
	// delivers a context mount with no mount namespace at all (DP-B34's reason text).
	packdecl.KindMount: "mount grants a jail a host folder through the jail's wall — at the host " +
		"there is no wall, and the folder is already where you are",
	packdecl.KindReadsHost: "reads-host carries a host file INTO a jail — meaningless when there is no jail",
	packdecl.KindState:     "state names a jail-writable home subtree — off-container the home simply is writable",
	// `loophole` needs its own reason, and the reason is the INVERSE of the generic line.
	// A loophole's effect IS on the host — it spawns a daemon there — so "not applicable at
	// this confinement level" would be the single most confusing sentence in the command:
	// it reads as obviously wrong to anyone who knows what a loophole does.
	//
	// The honest reason is that its COUNTERPARTY is missing, not its mechanism. With no jail
	// there is no client for the daemon, no `--add-host` to write, no YOLO_JAIL_DAEMONS to
	// populate, and nothing for the endpoint file to be mounted into.
	//
	// ONE SHAPE ONLY since the doorway rule (docs/design/host-notch-services.md HS-D15, built at
	// `yolo host --` by HS-D21): a credential loophole whose `jail_daemon` declares a `host_cmd`
	// has a client off-container, the agent `yolo host --` runs, and its doorway opens there
	// (hostAtLaunch). What is left here is every other loophole, whose only client is a
	// container. The sentence used to end "Launch a jail to run it", a remedy for a problem the
	// reader of a notch fact does not have (report-tiers.md P2).
	//
	// And the decline is a FEATURE of the trust story rather than a limitation to fix later.
	// `yolo host apply` is the one command that mutates the real machine, and it deliberately
	// runs no pack `hook` for the same reason. Even a doorway opens only for the life of the one
	// agent a launch runs, which keeps "selecting this pack runs a daemon" a statement about
	// LAUNCHING, not about applying a config.
	packdecl.KindLoophole: "a loophole with no doorway for a notch without a jail (`jail_daemon.host_cmd`) " +
		"is a host daemon whose only client is a container: with no jail there is no client, no " +
		"--add-host, no YOLO_JAIL_DAEMONS, and nothing for its endpoint file to be mounted into",
	// `service` had NO entry here until 2026-09-13, so it fell to Refuse's generic fallback —
	// "<kind> is not applicable at this confinement level", which names the kind and says nothing
	// about it (docs/design/declaration-parity.md DP-B27 / DP-L6). Its reason was not missing,
	// only misplaced, in internal/cli/config_ref.txt's host-notch list, where
	// TestEveryHostNotchInapplicableKindHasItsReasonDocumented keeps it READABLE without making it
	// the thing the code decides by.
	//
	// Since 2026-09-28 a service's HOST HALF runs at `yolo host --` beside the one agent paired
	// through it (docs/design/host-notch-services.md OQ-NC1, OQ-HS4), which is hostAtLaunch's
	// entry; this one names the shape the host runs nothing for, a service with no host half or
	// one only an official pack's gate refuses.
	//
	// `blocked-tool` sat here too, saying "off-container yolo owns no PATH entry to put one in".
	// That stopped being true of `yolo host --` on 2026-09-29, when HE-D1 had it compose the
	// child's PATH, and the kind is honored at the host since 2026-10-04 (HE-D11): HostFields
	// lists it, and `yolo host --` delivers it (hostAtLaunch).
	//
	// ⚠ Nothing PRINTS any of these strings: FieldSet.Refuse has no production caller (DP-B34 —
	// Target.Fields() has none either), and the host apply's tier-1 line names kinds and points at
	// `yolo config-ref` rather than quoting a reason. What the entries keep right is the census's
	// own data, and the manual the drift gate compares it with.
	//
	// `intercept` stays refused at both host verbs, for a reason of its own rather than
	// blocked-tool's old one (boundary-broker.md BB-D17): an intercept layers a permission over a
	// CLI, and at the host the agent runs as the user, so it can run the real program by its path
	// and the layer would govern nothing.
	packdecl.KindIntercept: "an intercept layers a permission over a CLI by putting a forwarder " +
		"first on a JAIL's PATH; at the host the agent runs as you and can run the real program " +
		"by its path, so the layer would govern nothing (boundary-broker.md BB-D17)",
	packdecl.KindService: "a service with no host half (`host_daemon`), or one a pack yolo does not " +
		"ship declares, is a daemon pair plus an endpoint file under the jail's /run. With no " +
		"jail there is nothing to supervise the jail half and nothing to read the endpoint",
}

// hostUnimplemented names the kinds a host target's FieldSet HONORS but whose renderer is
// not built yet, with the reason to print. It is the fix for the failure mode G1 shipped:
// HostFields() promised `skills` and `briefing` applied, while RenderHostPack iterated
// config surfaces only — so a kind that was applicable but had no surface was neither
// rendered NOR refused, and vanished with no output line at all. `refusalReasons` could not
// cover them precisely because the census says they DO apply.
//
// Kept as DATA rather than two `if`s at the call site so each phase that implements one
// deletes an entry here instead of untangling a conditional. An empty map is the end state.
// Four of these were found by the no-silent-skip TEST, not by the gap report that
// prompted this map — G1 named only skills and briefing. `launch`, `env`,
// `config-overlay` and (visibly) `autonomy` were skipped just as silently. That is the
// argument for asserting the invariant over the whole kind set rather than patching the
// two kinds someone happened to notice.
//
// `config-overlay` was here and is GONE, which is what an entry's removal looks like: it
// is applied at both targets now (packoverlay.Collect feeds Inputs.Overlays), and like
// `autonomy` it renders INVISIBLY — an overlay folds into a surface another pack owns, so
// it shows up as that surface's own line. The caller prints the contributing packs there
// (HostRenderResult.Overlays) and names an ownerless overlay in its own line, so the kind
// still produces output on every path; it just is not this map's kind of output.
//
// `env`, `adapter` and `blocked-tool` were here too, each saying "`yolo host apply` never
// starts a process, and `yolo host -- <program>` delivers it". That is not a kind the HOST
// NOTCH leaves undone, it is one this COMMAND writes no file for, and the apply's notch line
// printing it under "does not apply at the host" contradicted the launch that delivers it. They
// moved to hostAtLaunch on 2026-10-04, whose outcome is a different clause of the line. (`launch`
// sat here before them for the same missing verb; that kind is retired, its flags declared inside
// an `autonomy` posture, which the host notch selects rather than leaves unbuilt.)
var hostUnimplemented = map[packdecl.Kind]string{
	// Every shipped hook is jail plumbing: the shared_* pair symlinks a credentials file
	// or a package-store directory into a machine-global dir, and per_jail_history
	// isolates a history file PER JAIL. Off-container each is either meaningless or a
	// mutation of real user state that no pack should perform unprompted — the directory
	// one most of all, since applying it at the host notch would replace a real directory
	// in the user's own home with a symlink. Refused deliberately, not merely unbuilt.
	// (A third, claude_plugins, used to be the sharpest example here — it ran the claude
	// CLI against the user's real plugin cache — and it is RETIRED, so the refusal no
	// longer names it: packdecl.retiredHooks.)
	packdecl.KindHook: "hooks are jail provisioning steps (a credential or package-store " +
		"symlink into the machine tier, per-jail history) — `yolo host apply` does not run " +
		"them against your real home",
	// `provider` WAS HERE, and is built (OQ-HC1, docs/reference/host-agent-environment.md): `yolo
	// host apply` composes the providers table at user scope and runs the derives over it, so
	// a shipped provider's facts reach pi/models, pi/codex-models, codex/config,
	// opencode/config and oh-omp/models at the host as in a jail. Like config-overlay it
	// renders INVISIBLY — into the files of the surfaces that carry it.
	//
	// `profile` WAS HERE, and is built (OQ-HC3): `yolo host apply` applies the selection your
	// user-scope `profile` names, by the jail's edge-triggered rule, and gates each
	// profile-gated config-overlay on the same table. A one-launch `-p` still has no meaning
	// here, since this command launches nothing.
}

// hostAtLaunch names the kinds the HOST NOTCH delivers to the process `yolo host -- <program>`
// starts and `yolo host apply` writes no file for — what docs/reference/report-tiers.md's report
// vocabulary calls AT LAUNCH ONLY (a term coined there on 2026-10-04): what each declares reaches
// an agent through its PROCESS (an environment, a PATH, the providers table a launch composes, a
// child daemon, a loopback listener) and never through a file this command could write.
//
// THE UNIT IS THE NOTCH, as the config-key table's is (declaration-parity.md DP-I1): a kind
// some host verb delivers is not one the host "does not apply", and the apply saying it was is
// the defect this map closes — `yolo host apply` printed env, adapter, service and loophole as
// not applying at the host while `yolo host -- env` printed the pack env vars.
//
// FOUR OF THE FIVE HAVE A SHAPE THE HOST DOES NOT DELIVER, so the apply decides PER
// CONTRIBUTION (cli's hostNotchOutcomeOf) and a kind may land in both of its groups:
//
//   - env: a variable `served_by` a daemon the host notch does not serve (hostWithheldAtLaunch).
//   - adapter: one whose address its own pack's service answers, when that service has no host
//     half the gate admits (hostWithheldAtLaunch).
//   - service: one with no host half, or a host half the official-pack gate refuses
//     (launchservice.Admit); refusalReasons states it, the kind not being honored.
//   - loophole: one with no doorway for a notch without a jail (refusalReasons, likewise).
//   - blocked-tool: none — every contribution is delivered.
//
// service and loophole stay out of HostFields: the FieldSet census says what a host TARGET
// renders, and nothing renders either kind into a home.
var hostAtLaunch = map[packdecl.Kind]string{
	packdecl.KindEnv: "env vars apply to a process yolo starts: `yolo host -- <program>` " +
		"delivers them to that process only, and `yolo host env` prints them. `yolo host apply` " +
		"starts none, and setting them for your whole session would mean editing your shell rc",
	// docs/design/host-launch-environment.md HE-D11: `yolo host -- <program>` applies blocked
	// tools and `yolo host apply` blocks nothing, since it starts no process, which is env's case.
	packdecl.KindBlockedTool: "a blocker is a shim first on the PATH of a process yolo starts: " +
		"`yolo host -- <program>` puts them first on that program's PATH, for that process only. " +
		"`yolo host apply` starts none, and putting one at the head of your whole session's PATH " +
		"would mean editing your shell rc",
	// adapter rides provider's channel: the address it declares is composed INTO the providers
	// table a launch carries, so it reaches an agent the moment one is launched and never
	// through a config file.
	packdecl.KindAdapter: "an adapter's address is composed into the providers table a launch " +
		"carries, so `yolo host -- <program>` points its agent there, starting the pack service " +
		"that answers it when one does; `yolo host apply` writes it into no file",
	// docs/design/host-notch-services.md OQ-NC1 (A) and OQ-HS4.
	packdecl.KindService: "a service's host half (`host_daemon`, in a pack yolo ships) runs at " +
		"`yolo host -- <program>` beside the one agent paired through it, and stops when that " +
		"agent exits; `yolo host apply` runs no process for it to live beside",
	// docs/design/host-notch-services.md HS-D15 (the doorway rule) and HS-D21.
	packdecl.KindLoophole: "a credential loophole's doorway (`jail_daemon.host_cmd`, in a pack " +
		"yolo ships) opens at `yolo host -- <program>` for the agent whose selection asks for it, " +
		"once the loophole is enabled, and closes when that agent exits; `yolo host apply` runs " +
		"no process for it to live beside",
}

// hostWithheldAtLaunch is the shape of an at-launch kind the host notch does NOT deliver, for
// a kind the host FieldSet honors (so refusalReasons cannot state it): `env` and `adapter`.
// service's and loophole's undelivered shapes are their refusalReasons entries.
var hostWithheldAtLaunch = map[packdecl.Kind]string{
	// docs/plans/notch-convergence.md NC-D16's one "served at this notch" predicate
	// (packload/served.go): the host serves no jail daemon but the doorways and the pack
	// services a launch opens, so a pointer at any other is withheld and named at launch. The
	// shipped case is audio's (LP-D1, docs/design/loophole-packaging.md): a bound loophole's
	// socket path exists only in a jail that binds it, and a client at the host reaches its
	// own server at the default path instead.
	packdecl.KindEnv: "a variable `served_by` a daemon the host does not serve — a bound " +
		"loophole's socket path, which only a jail binds (audio's), or a loophole with no doorway " +
		"— is withheld at `yolo host --` and named there: a client at the host reaches its own " +
		"server instead",
	// packload.Adaptation.Service: an adapter's address is answered by its own pack's service
	// when the pack declares one, and the host runs that service only through a host half the
	// launch's gate admits (launchservice.Admit, OQ-HS4). Without one, the composition leaves the
	// address out and `yolo host --` refuses a pairing through it, naming the service. No shipped
	// pack has this shape: wire-bridge's service has an admitted host half.
	packdecl.KindAdapter: "an adapter whose address its own pack's service answers, when that " +
		"service has no host half (`host_daemon`) or is in a pack yolo does not ship: nothing at " +
		"the host serves the address, so `yolo host --` refuses a pairing through it and names " +
		"the service",
}

// HostAtLaunch returns the reason `yolo host -- <program>` delivers a kind at the host notch
// that `yolo host apply` writes no file for (report-tiers.md's AT LAUNCH ONLY), and ok=false for
// any other kind. A kind it names may still have a shape the host does not deliver
// (HostWithheldAtLaunch, and FieldSet.Refuse for a kind the FieldSet does not honor): the apply
// decides per contribution.
func HostAtLaunch(k packdecl.Kind) (string, bool) {
	r, ok := hostAtLaunch[k]
	return r, ok
}

// HostWithheldAtLaunch returns the reason a contribution of an at-launch kind the host FieldSet
// honors is not delivered at the host, ok=false for a kind with no such shape.
func HostWithheldAtLaunch(k packdecl.Kind) (string, bool) {
	r, ok := hostWithheldAtLaunch[k]
	return r, ok
}

// HostUnimplemented returns the reason a kind is honored-but-unbuilt at a host target, and
// ok=false once it is implemented (or was never in the set). Callers report it the same way
// they report a refusal — the point is that NOTHING a pack declares is silently absent.
func HostUnimplemented(k packdecl.Kind) (string, bool) {
	r, ok := hostUnimplemented[k]
	return r, ok
}

// HostLeavesUndone reports whether a host target's FieldSet does nothing in a home with kind k:
// it does not honor the kind (refusalReasons), or honors it with no renderer behind it
// (hostUnimplemented). It is the apply's "does not apply" predicate for a kind with no at-launch
// shape (cli's notchInapplicable), and with HostAtLaunch the whole of HostDelivers.
func HostLeavesUndone(fields FieldSet, k packdecl.Kind) bool {
	if !fields.Honors(k) {
		return true
	}
	_, unbuilt := HostUnimplemented(k)
	return unbuilt
}

// HostDelivers reports whether some host verb does something with kind k: `yolo host apply`
// renders it into a home (HostLeavesUndone is false), or `yolo host -- <program>` delivers it to
// the program it starts (HostAtLaunch). It is the PER-KIND answer: an at-launch kind with a shape
// the host does not deliver (an env pointer at a daemon only a jail serves, a loophole with no
// doorway) still counts, because telling the shapes apart needs the launch's own checks, which
// the apply asks per contribution (cli's hostNotchOutcomeOf).
//
// The host briefing composer asks it of every kind a briefing `describes`
// (packdecl.Contribution.Describes, boundary-broker.md BB-D69), and withholds the prose when one
// answers false: `intercept` is the shipped case, a forwarder no host verb puts anywhere
// (refusalReasons says why), so prose about it would be false in a real home.
func HostDelivers(fields FieldSet, k packdecl.Kind) bool {
	if _, ok := HostAtLaunch(k); ok {
		return true
	}
	return !HostLeavesUndone(fields, k)
}

// JailFields is every kind a jail RENDERS, which is every kind except the ones rendered
// by something other than the render path.
//
// Derived from packdecl.KnownKinds() minus jailRenderedElsewhere, so a new kind is honored
// by default (a jail is the maximal target) and an exclusion has to be written down.
func JailFields() FieldSet {
	all := map[packdecl.Kind]bool{}
	for _, k := range packdecl.KnownKinds() {
		if jailRenderedElsewhere[k] {
			continue
		}
		all[k] = true
	}
	return FieldSet{applies: all}
}

// jailRenderedElsewhere names the kinds whose jail-side effect exists but is NOT produced
// by the render path this FieldSet describes. Excluded EXPLICITLY rather than by
// derivation, because the census is supposed to be executable data and an entry it derives
// from nothing is an assertion no code reads.
//
// `loophole` is the case. Its jail-side effects are real — `--add-host`, bind mounts,
// devices, YOLO_JAIL_DAEMONS, an endpoint file — and every one of them is produced by
// `startLoopholes` in the HOST CLI, before the container exists, not by
// entrypoint.ConfigurePackSurfaces. Measured while designing the kind: `Target.Fields()`
// has no production caller at all (the only consumer of a FieldSet is
// `render.HostFields()`, at apply.go), so deriving `loophole: true` from KnownKinds()
// would have made the census claim "the jail render honors this" — true of nothing.
//
// The honest census answer at `jail` is therefore "rendered elsewhere; its actor is the
// run pipeline". A caller asking Honors() gets false and Refuse() gives the counterparty
// reason (refusalReasons), which is the right answer for the jail target too: whatever
// renders a loophole, it is not this.
var jailRenderedElsewhere = map[packdecl.Kind]bool{
	packdecl.KindLoophole: true,
}

// HostFields is the reduced set a host/guest target honors: the composed-config and
// prose kinds port; env is delivered at launch (hostAtLaunch); program is confirm-gated (honored, but the CALLER
// gates it — the FieldSet says it applies); the provisioning kinds are refused. This
// is §2.1's census as executable data.
//
// config-overlay tracks config (it lands in a composed surface). hook is notch-dependent in
// degree, not applicability, so it is honored here and the caller narrows it (only 1 of 3
// hooks on host); keeping it in the set means "this target can express it," which is true.
func HostFields() FieldSet {
	honored := map[packdecl.Kind]bool{
		packdecl.KindConfig:        true,
		packdecl.KindConfigOverlay: true,
		packdecl.KindSkills:        true,
		packdecl.KindBriefing:      true,
		packdecl.KindEnv:           true,
		// blocked-tool is honored for env's reason: `yolo host --` starts the process and owns
		// its PATH (HE-D11), so it is delivered at launch (hostAtLaunch).
		packdecl.KindBlockedTool: true,
		packdecl.KindHook:        true,
		packdecl.KindProgram:     true, // honored but confirm-gated by the caller (OQ-6/7)
		// config-list tracks config for config-overlay's reason — its entries land in a
		// composed surface — and it is honored in the final sense, with no hostUnimplemented
		// entry: a surface whose mode cannot capture a list path per entry yet refuses the
		// CONTRIBUTION by name at render time (the engine's ListCaptureRefusal), which is a
		// fact about one surface's mechanism and not about the notch.
		packdecl.KindConfigList: true,
		// requires is honored, and REPORTED with its hints — that is the kind's entire
		// host-side purpose. It asserts a binary must exist, which is exactly the question a
		// host target answers (below jail, yolo bakes no image, so every dep is the host's);
		// and it generates nothing, so there is no install to gate. Refusing it would leave a
		// content-only pack unable to carry a remedy for the tool it needs — the gap that
		// motivated the kind, since `program` was the only way to get install_hints and it
		// implies an install nobody wanted.
		packdecl.KindRequires: true,
		packdecl.KindAutonomy: true, // honored: host renders the GUARDED posture (§4.2)
		// provider is honored at this notch and BUILT: `yolo host apply` composes the
		// providers table at user scope and runs each surface's derive over it (OQ-HC1), and
		// `yolo host -- <program>` composes the same table into the launched process.
		packdecl.KindProvider: true,
		// models is honored and built for provider's reason: it shapes the providers table
		// packload.ComposeProviders composes, which `yolo host apply` and `yolo host --`
		// compose too.
		packdecl.KindModels: true,
		// adapter is provider's constant companion and gets provider's answer, for
		// provider's reason: it declares an ADDRESS, and an address reaches an agent through
		// the providers table a LAUNCH composes, never through a file this command writes.
		// hostAtLaunch states where it is delivered, and hostWithheldAtLaunch the shape that is
		// not (an address only a service with no admitted host half answers).
		packdecl.KindAdapter: true,
		// profile is honored in the sense the census means — since OQ-PT8 it IS a
		// selection (`name` + `provider`), not a patch of its own, so there is nothing to
		// apply at this notch and nothing to gate either: it carries no key a config write
		// could render. Its config half lives in `config-overlay` contributions, which
		// reach the host render through the same collector the boot uses — with no table
		// passed, a gated overlay is a clean skip rather than a written key.
		packdecl.KindProfile: true,
		// files is honored by WRITING the tree, not binding it. The old refusal ("nothing
		// to bind into off-container") was true of the mechanism and false of the intent: a
		// pack that owns ~/.claude/file-suggestion.sh means "this file is mine to
		// maintain", and off-container the way to honor that is a real copy. Ownership does
		// NOT carry over though — see internal/entrypoint/hostfilestree.go.
		packdecl.KindFiles: true,
	}
	// The config-key census rides the same FieldSet (configkeys.go, OQ-DP5's second half), so
	// the one value the apply's survey reads answers for kinds and keys alike.
	return FieldSet{applies: honored, keys: hostConfigKeys}
}

// Fields returns the FieldSet for this target's kind.
//
// A SWITCH naming every kind rather than "jail, else host" (Q1). The `if` was correct while a
// Kind could only be one of three INFERRED values, and becomes a silent over-permission the
// moment a fourth exists — so the point of writing it out is that the default is now a
// decision on the record. That default is the REDUCED set, which is the fail-closed direction:
// a kind wrongly refused is a message, a kind wrongly honored is a write nobody asked for. In
// particular `guest` must not fall into the jail set, which would honor
// `mount`/`reads-host`/`state` at a notch with no mount namespace to honor them with; its real
// census is Phase 7's to state.
//
// KindPreview is in the default branch because that is where the shape inference put it, and
// this change is meant to be behavior-preserving — not because it is obviously right. A
// preview exists to show what the JAIL render produces, so the jail set is the likelier
// answer; nothing depends on it either way today (render.Preview has no production caller), so
// the wrong-looking half is at least now VISIBLE as a listed case rather than a fallthrough.
func (t Target) Fields() FieldSet {
	switch t.KindOf() {
	case KindJail:
		return JailFields()
	default: // KindHost, KindGuest, KindPreview, KindUnset
		return HostFields()
	}
}
