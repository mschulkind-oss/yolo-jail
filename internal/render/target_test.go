package render

import (
	"path/filepath"
	"strings"
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
