package render

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// The three constructors set the shape KindOf reads back — the discriminator callers
// use without threading a Kind field through every construction.
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

// A jail honors every kind; a host/guest target honors the reduced census set and
// refuses the provisioning kinds BY NAME.
func TestFieldSetCensus(t *testing.T) {
	jail := Jail("/h", "/workspace", nil).Fields()
	for _, k := range packdecl.KnownKinds() {
		if !jail.Honors(k) {
			t.Errorf("jail must honor every kind; does not honor %q", k)
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
	// The provisioning kinds are refused, and the refusal names why. These three are
	// genuinely container-shaped: two are mounts of host content INTO a jail, and the third
	// names a subtree that needs making writable only because a jail home is not.
	for _, k := range []packdecl.Kind{
		packdecl.KindMount, packdecl.KindReadsHost, packdecl.KindState,
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
