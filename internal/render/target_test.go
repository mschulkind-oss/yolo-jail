package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Each constructor STATES its notch, and KindOf reads that field back — the discriminator
// callers use, unchanged by Q1's move from inference to declaration.
func TestTargetKinds(t *testing.T) {
	if got := Jail("/home/agent", "/workspace", nil).KindOf(); got != KindJail {
		t.Errorf("Jail().KindOf() = %d, want KindJail", got)
	}
	if got := Preview("/tmp/x").KindOf(); got != KindPreview {
		t.Errorf("Preview().KindOf() = %d, want KindPreview", got)
	}
	if got := Host("/home/me", nil).KindOf(); got != KindHost {
		t.Errorf("Host().KindOf() = %d, want KindHost", got)
	}
	// A host target has no workspace referent — that is what refuses ${workspace}.
	if Host("/home/me", nil).Workspace != "" {
		t.Error("Host target must have empty Workspace (no ${workspace} referent)")
	}
}

// The explicit Kind field agrees with the SHAPE INFERENCE it replaced, for all three
// constructors that existed before Q1. This is what proves the refactor behavior-preserving
// rather than assuming it: every caller in the tree reads KindOf, so if the stated field and
// the old derivation ever disagreed on a constructed target, the change moved behavior.
func TestExplicitKindMatchesTheOldShapeInference(t *testing.T) {
	for _, c := range []struct {
		name   string
		target Target
	}{
		{"jail", Jail("/home/agent", "/workspace", nil)},
		{"preview", Preview("/tmp/scratch")},
		{"host", Host("/home/me", nil)},
	} {
		if got, want := c.target.KindOf(), inferKindFromShape(c.target); got != want {
			t.Errorf("%s: stated Kind = %d but the pre-Q1 shape inference said %d — the "+
				"refactor changed behavior for a constructor that already worked", c.name, got, want)
		}
	}
}

// GUEST IS WHY THE FIELD IS EXPLICIT (plan §6b D2). A guest home is a real home WITH a
// workspace and Home != Workspace, so the old shape inference called it a jail — and it would
// have inherited the jail's every kind, the jail's sidecars, and (via the profile) the jail's
// autonomy, with nothing recording that as a choice. This test is the inverse of the one
// above: it pins that the two DISAGREE on a guest-shaped target, so the disagreement is the
// documented reason the field exists rather than an accident someone might "fix" by restoring
// the inference.
func TestGuestShapeWouldHaveInferredJail(t *testing.T) {
	guestShaped := Target{Home: "/Users/agent", Workspace: "/Users/matt/code/proj", kind: KindGuest}
	if got := guestShaped.KindOf(); got != KindGuest {
		t.Fatalf("KindOf() = %d, want KindGuest — the field must win over the shape", got)
	}
	if inferKindFromShape(guestShaped) != KindJail {
		t.Fatal("a guest-shaped target no longer infers as a jail — if the shapes have " +
			"genuinely diverged, this test's premise (and D2's) needs restating")
	}
	// And the consequences a silent KindJail would have handed it: the full jail census, jail
	// autonomy, and a sidecar tree under someone's real workspace. All three must be the
	// conservative answer until Phase 7 states guest's own.
	if guestShaped.Fields().Honors(packdecl.KindMount) {
		t.Error("guest must not honor `mount` by default — there is no mount namespace below jail")
	}
	if guestShaped.SidecarDir() != "" {
		t.Errorf("guest must not inherit the jail's sidecar tree by default; got %q",
			guestShaped.SidecarDir())
	}
}

// An unconstructed Target claims NOTHING. The zero value is the one Kind a caller outside this
// package can produce (the field is unexported, so a struct literal cannot set it), which is
// exactly why KindUnset must not be a notch: with KindJail at iota 0, `render.Target{}` would
// have claimed the strongest one.
func TestUnsetTargetIsNotANotch(t *testing.T) {
	var zero Target
	if zero.KindOf() != KindUnset {
		t.Fatalf("the zero Target must read KindUnset, got %d", zero.KindOf())
	}
	if zero.Fields().Honors(packdecl.KindMount) {
		t.Error("an unset target must get the REDUCED census, not the jail's")
	}
	for label, got := range map[string]string{
		"SidecarDir":    zero.SidecarDir(),
		"ProvenanceDir": zero.ProvenanceDir(),
	} {
		if got != "" {
			t.Errorf("an unset target has nowhere to keep records; %s = %q", label, got)
		}
	}
}

// NO TARGET EVER YIELDS A RELATIVE SIDECAR OR PROVENANCE PATH. This is the trap the host
// notch walked into: Workspace is empty at the host BY DEFINITION (KindOf uses that as the
// discriminator), so any path built by joining it is relative — and resolves against
// whatever directory `yolo apply --host` happened to be invoked from.
func TestTargetPathsAreNeverRelative(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target Target
	}{
		{"jail", Jail("/home/agent", "/workspace", nil)},
		{"preview", Preview("/tmp/scratch")},
		{"host", Host("/home/me", nil)},
	} {
		for label, got := range map[string]string{
			"SidecarDir":     tc.target.SidecarDir(),
			"ProvenanceDir":  tc.target.ProvenanceDir(),
			"ProvenancePath": tc.target.ProvenancePath("claude", "settings"),
		} {
			// "" is the honest answer for a target that keeps no such record; anything else
			// must be absolute.
			if got != "" && !filepath.IsAbs(got) {
				t.Errorf("%s target %s = %q — a relative path scatters records into the CWD",
					tc.name, label, got)
			}
		}
	}
}

// The host target keeps a PROVENANCE record but no capture sidecars, and the split is the
// resolved model rather than an omission: a host render is pure RMW (OQ-4), so there is no
// last_render baseline and no captured edit to replay — but it still knows which layer won
// each key, and without that record `config diff` has nothing to measure.
func TestHostTargetKeepsProvenanceButNoCaptureSidecars(t *testing.T) {
	host := Host("/home/me", nil)
	if got := host.SidecarDir(); got != "" {
		t.Errorf("a host target has no capture sidecars; SidecarDir = %q", got)
	}
	got := host.ProvenancePath("claude", "settings")
	if got == "" {
		t.Fatal("a host target must keep a provenance record — without one `config diff` at " +
			"the host infers a winner and can state the opposite of what landed")
	}
	// Under the rendered home's STATE dir: not the user's config dir (yolo bookkeeping in
	// ~/.claude is indistinguishable from config) and not any workspace (there is none).
	want := filepath.Join("/home/me", ".local", "share", "yolo-jail")
	if !strings.HasPrefix(got, want+string(filepath.Separator)) {
		t.Errorf("host provenance at %q, want it under the state dir %q", got, want)
	}
}

// Keyed on the TARGET's home, never the process $HOME. Two host targets with different homes
// must not share a record — otherwise a render into a temp home (every test, and any
// non-default home) writes into the invoking user's real state dir.
func TestHostProvenanceIsKeyedOnTheTargetHome(t *testing.T) {
	a := Host("/home/alice", nil).ProvenancePath("claude", "settings")
	b := Host("/home/bob", nil).ProvenancePath("claude", "settings")
	if a == b {
		t.Fatalf("two host targets share one provenance path (%q) — the record must follow the "+
			"home being rendered into, not the process environment", a)
	}
	if !strings.HasPrefix(a, "/home/alice/") || !strings.HasPrefix(b, "/home/bob/") {
		t.Errorf("provenance paths do not follow their targets' homes: %q / %q", a, b)
	}
}

// A jail's provenance record stays where it always was — beside the other two sidecars under
// <workspace>/.yolo/prism/. Host-side provenance is purely additive; if this moves, the jail
// path changed and the render fingerprint gate is the next thing to break.
func TestJailProvenanceStaysInTheSidecarTree(t *testing.T) {
	jail := Jail("/home/agent", "/workspace", nil)
	if got, want := jail.SidecarDir(), "/workspace/.yolo/prism"; got != want {
		t.Errorf("jail SidecarDir = %q, want %q", got, want)
	}
	if got, want := jail.ProvenancePath("claude", "settings"),
		"/workspace/.yolo/prism/claude-settings.provenance"; got != want {
		t.Errorf("jail ProvenancePath = %q, want %q", got, want)
	}
}

// A jail honors every kind IT RENDERS; a host/guest target honors the reduced census set
// and refuses the provisioning kinds BY NAME.
func TestFieldSetCensus(t *testing.T) {
	jail := Jail("/h", "/workspace", nil).Fields()
	for _, k := range packdecl.KnownKinds() {
		if jailRenderedElsewhere[k] {
			// Explicitly excluded, not honored-and-unbuilt: the kind's jail-side effect is
			// produced by something other than the render path (for `loophole`, by
			// startLoopholes in the host CLI before the container exists). The census is
			// meant to be executable data, so it must not assert a render nothing performs.
			// The exclusion still gets a REASON, checked below.
			if jail.Honors(k) {
				t.Errorf("kind %q is listed as rendered elsewhere but the jail FieldSet "+
					"honors it — one of the two is wrong", k)
			}
			if jail.Refuse(k) == "" {
				t.Errorf("jail.Refuse(%q) is empty — a kind excluded from the jail set must "+
					"still say why, or it is a silent skip", k)
			}
			continue
		}
		if !jail.Honors(k) {
			t.Errorf("jail must honor every kind it renders; does not honor %q", k)
		}
	}

	host := Host("/home/me", nil).Fields()
	// The portable, target-independent kinds. autonomy is honored on host because that
	// is how the GUARDED posture reaches the real home (§4.2) — refusing it would leave
	// the host with no way to render prompts-on.
	for _, k := range []packdecl.Kind{
		packdecl.KindConfig, packdecl.KindSkills, packdecl.KindBriefing, packdecl.KindEnv,
		packdecl.KindAutonomy,
		// files MOVED into the honored set (plan Phase 7). The old refusal — "files binds a
		// pack tree into a jail, nothing to bind into off-container" — was true of the
		// MECHANISM and false of the intent: a pack owning ~/.claude/bin/file-suggestion.sh
		// means "this file is mine to maintain", and off-container the way to honor that is
		// to write the tree. The bind mount was never the point; it is how a JAIL gets an
		// immutable copy. What does not carry over is the ownership posture — see
		// internal/entrypoint/hostfilestree.go, which refuses any path it cannot prove yolo
		// wrote.
		packdecl.KindFiles,
	} {
		if !host.Honors(k) {
			t.Errorf("host must honor %q (target-independent)", k)
		}
	}
	// The provisioning kinds are refused, and the refusal names why. Three are genuinely
	// container-shaped: two are mounts of host content INTO a jail, and the third names a
	// subtree that needs making writable only because a jail home is not. `loophole` is
	// refused for the INVERSE reason (see TestLoopholeRefusalNamesTheMissingCounterparty).
	for _, k := range []packdecl.Kind{
		packdecl.KindMount, packdecl.KindReadsHost, packdecl.KindState, packdecl.KindLoophole,
	} {
		if host.Honors(k) {
			t.Errorf("host must NOT honor provisioning kind %q", k)
		}
		if host.Refuse(k) == "" {
			t.Errorf("host.Refuse(%q) must give a reason, not empty", k)
		}
	}
	// program is honored by the FieldSet (confirm-gated by the caller, not refused here).
	if !host.Honors(packdecl.KindProgram) {
		t.Error("host FieldSet should honor program (the caller confirm-gates it, OQ-6/7)")
	}
}

// `loophole`'s refusal must name the missing COUNTERPARTY, not the generic
// "not applicable at this confinement level".
//
// This is the one kind for which the generic line is actively misleading rather than merely
// vague: a loophole's effect IS on the host — it spawns a daemon there — so "not applicable
// off-container" reads as obviously wrong to anyone who knows what a loophole does, and
// would be the single most confusing sentence in the command. The honest reason is the
// inverse: with no jail there is no CLIENT for the daemon.
func TestLoopholeRefusalNamesTheMissingCounterparty(t *testing.T) {
	host := Host("/home/me", nil).Fields()
	reason := host.Refuse(packdecl.KindLoophole)
	if reason == "" {
		t.Fatal("host.Refuse(loophole) is empty — the kind must be refused BY NAME")
	}
	if strings.Contains(reason, "not applicable at this confinement level") {
		t.Fatalf("loophole fell through to Refuse's generic line (%q). A loophole's effect IS "+
			"on the host, so that sentence reads as obviously wrong; the reason must be that "+
			"its counterparty — a container to serve — is missing", reason)
	}
	// The counterparty, named. Each token is a concrete thing that has no meaning with no
	// jail, which is what makes the sentence checkable rather than merely different.
	for _, want := range []string{"client", "container"} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal %q does not mention %q — the reason is the missing counterparty, "+
				"so it has to say what is missing", reason, want)
		}
	}
	// And it must NOT claim the mechanism is unavailable, which is the backwards reading.
	if strings.Contains(reason, "needs a mount namespace") {
		t.Errorf("refusal %q blames the mechanism; a loophole's mechanism (spawning a host "+
			"process) works fine off-container — its client does not exist", reason)
	}
}

// `loophole` is excluded from the JAIL set EXPLICITLY, via jailRenderedElsewhere, rather
// than by derivation from KnownKinds().
//
// The census is supposed to be executable data, and `loophole` is the case where derivation
// would make it assert something no code reads: `Target.Fields()` has no production caller,
// and a loophole's jail-side effects (--add-host, binds, devices, YOLO_JAIL_DAEMONS, the
// endpoint file) are all produced by startLoopholes in the HOST CLI before the container
// exists — not by the render path this FieldSet describes.
func TestLoopholeIsExcludedFromTheJailSetExplicitly(t *testing.T) {
	if !jailRenderedElsewhere[packdecl.KindLoophole] {
		t.Fatal("loophole is not in jailRenderedElsewhere — the exclusion has to be written " +
			"down, not derived, or the census claims the jail render honors a kind it never sees")
	}
	if JailFields().Honors(packdecl.KindLoophole) {
		t.Error("JailFields() honors loophole — its jail-side effect is produced by the run " +
			"pipeline, so the render census must not claim it")
	}
	// Every OTHER kind is still honored: the jail is the maximal target, so a new kind is
	// honored by default and an exclusion is a deliberate entry.
	for _, k := range packdecl.KnownKinds() {
		if k == packdecl.KindLoophole {
			continue
		}
		if !JailFields().Honors(k) {
			t.Errorf("JailFields() stopped honoring %q — only kinds listed in "+
				"jailRenderedElsewhere may be excluded", k)
		}
	}
}

// `env` and `launch` are unbuilt because `apply --host` NEVER LAUNCHES A PROCESS — a limit of
// the command, not of the notch (plan §6b D3). The distinction is not pedantry: at `guest`
// yolo already execs the agent, and `yolo --at host -- <cmd>` would give the host notch the
// same verb, so a reason phrased as "off-container" or "below jail" would refuse two kinds
// that are in fact honorable and send a reader looking for a confinement fix to a problem that
// is a missing command.
//
// Asserted on the SHAPE of the reason, not its exact prose, so rewording stays free while the
// two claims that must not come back are pinned: the reason has to name the command, and must
// not blame the notch.
func TestEnvAndLaunchRefusalsBlameTheCommandNotTheNotch(t *testing.T) {
	for _, k := range []packdecl.Kind{packdecl.KindEnv, packdecl.KindLaunch} {
		why, unbuilt := HostUnimplemented(k)
		if !unbuilt {
			// Implemented is a fine outcome — delete this kind's row rather than the test.
			continue
		}
		if !strings.Contains(why, "apply --host") {
			t.Errorf("%s: the reason must name the COMMAND that cannot honor it; got %q", k, why)
		}
		// The old wording's mistake, in the two forms it took: presenting the limit as a
		// property of being off a container rather than of this command.
		for _, blames := range []string{"off-container", "below jail", "off the container"} {
			if strings.Contains(why, blames) {
				t.Errorf("%s: reason blames the NOTCH (%q) — a guest target inheriting this "+
					"text would refuse a kind it can honor; got %q", k, blames, why)
			}
		}
	}
}
