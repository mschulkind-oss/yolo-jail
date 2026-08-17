package jailcontent

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// C2. The per-notch header's two most consequential facts — what enforces the boundary, and
// whether agent autonomy is on — are DERIVED from render.ProfileFor rather than written per
// notch. These tests pin that derivation, not the prose: the framing sentence still differs
// by notch on purpose (a human reads it), and asserting its exact words would just make the
// wording unrefactorable.

// briefingHeader is the confinement header block — everything before "## Environment".
func briefingHeader(t *testing.T, confinement string) string {
	t.Helper()
	out := BriefingContent(BriefingInput{Workspace: "/w", Confinement: confinement})
	i := strings.Index(out, "## Environment")
	if i < 0 {
		t.Fatalf("briefing has no ## Environment section:\n%s", out)
	}
	return out[:i]
}

// The header names exactly the primitives the notch's Profile composes, in the canonical
// order, using the canonical phrasing — so a notch cannot describe a boundary it does not
// have, nor omit one it does. This is what makes the header correct for a notch nobody has
// enumerated: the vector is read, not written.
//
// The JAIL is deliberately excluded and gets its own test below.
func TestBriefingHeaderStatesTheProfilesPrimitives(t *testing.T) {
	for _, notch := range []string{"guest", "host"} {
		kind, ok := render.KindForNotch(notch)
		if !ok {
			t.Fatalf("fixture: %q is not a selectable notch", notch)
		}
		prof := render.ProfileFor(kind)
		header := briefingHeader(t, notch)

		for _, prim := range render.PrimitiveOrder() {
			phrase := render.PrimitiveDoes(prim)
			present := strings.Contains(header, phrase)
			switch {
			case prof.Has(prim) && !present:
				t.Errorf("%s: header omits the primitive that enforces it (%q):\n%s",
					notch, phrase, header)
			case !prof.Has(prim) && present:
				t.Errorf("%s: header claims a primitive the notch does not compose (%q):\n%s",
					notch, phrase, header)
			}
		}
		// A preset composing NOTHING must say so rather than leaving the section blank — the
		// absence IS the fact at the host notch.
		if !prof.Has(render.PrimNamespaces) && !prof.Has(render.PrimVM) &&
			!prof.Has(render.PrimSeatbelt) && !prof.Has(render.PrimLandlock) &&
			!prof.Has(render.PrimSeparateUser) && !prof.Has(render.PrimBakedImage) {
			if !strings.Contains(header, "nothing") {
				t.Errorf("%s: a notch with no primitive must say so explicitly:\n%s", notch, header)
			}
		}
	}
}

// The autonomy bit is stated, and in the direction the Profile says. This is the fact that is
// invisible everywhere else — it decides the posture inside a pack's config surfaces, never as
// a line of its own — and getting it backwards would tell a host agent its prompts are off.
func TestBriefingHeaderStatesTheAutonomyBit(t *testing.T) {
	for _, notch := range []string{"guest", "host"} {
		kind, _ := render.KindForNotch(notch)
		header := briefingHeader(t, notch)
		on := strings.Contains(header, "autonomy is **ON**")
		off := strings.Contains(header, "autonomy is **OFF**")
		if on == off {
			t.Fatalf("%s: header must state autonomy exactly once, one way:\n%s", notch, header)
		}
		if want := render.ProfileFor(kind).AgentAutonomy; on != want {
			t.Errorf("%s: header says autonomy ON=%v, but the notch's profile says %v — "+
				"an inverted autonomy line tells an agent its prompts are off when they are not:\n%s",
				notch, on, want, header)
		}
	}
}

// THE JAIL'S BYTES DO NOT MOVE. Every jail that boots renders this header, so the C2 refactor
// must be byte-identical there — the primitive detail is added only at the notches whose prose
// was thin. Pinned as a literal rather than derived, because "unchanged" is the claim.
func TestBriefingJailHeaderIsUnchanged(t *testing.T) {
	want := "# YOLO Jail Environment\n" +
		"\n" +
		"You are running inside a YOLO Jail — a sandboxed container.\n" +
		"Jail tooling: `yolo --help`; config reference: `yolo config-ref`.\n" +
		"\n"
	for _, notch := range []string{"jail", ""} { // empty means the default, which is jail
		if got := briefingHeader(t, notch); got != want {
			t.Errorf("confinement=%q header moved:\ngot:\n%s\nwant:\n%s", notch, got, want)
		}
	}
}

// An UNRECOGNIZED notch must not be told it is in a container. This is the case the Profile
// read exists for: the previous name-switch fell through to the jail branch, so any notch
// nobody enumerated was handed "you are running inside a YOLO Jail — a sandboxed container",
// which for anything below jail is exactly the dangerous falsehood the notch line was added to
// prevent. ProfileFor is total and fails closed, so the fallback describes the MOST restricted
// reading (no primitives, autonomy off).
func TestBriefingUnknownNotchDoesNotClaimAContainer(t *testing.T) {
	header := briefingHeader(t, "bwrap-guest")
	if strings.Contains(header, "sandboxed container") {
		t.Errorf("an unrecognized notch must not be told it is in a sandboxed container:\n%s", header)
	}
	// The name the CONFIG wrote is echoed, not the Kind it failed to resolve to: "unset" would
	// hide the one clue a human debugging it needs.
	if !strings.Contains(header, "bwrap-guest") {
		t.Errorf("the unrecognized notch name must appear, as evidence of what produced it:\n%s", header)
	}
	if !strings.Contains(header, "autonomy is **OFF**") {
		t.Errorf("an unrecognized notch must fail closed on autonomy:\n%s", header)
	}
}

// THE netMode TRAP. "host" is BOTH a confinement notch and podman's network mode, and they are
// unrelated meanings of the word in this very file. A sweep that conflated them would either
// give a bridge-networked host-notch jail the host-networking paragraph or, worse, tell a
// host-NETWORKED jail it is running on the human's real machine. Pinned in both directions so
// the conflation cannot be introduced silently.
func TestBriefingNetModeHostIsNotTheHostNotch(t *testing.T) {
	// A jail with host NETWORKING is still a jail: container framing, network paragraph.
	jailHostNet := BriefingContent(BriefingInput{Workspace: "/w", NetMode: "host"})
	if !strings.Contains(jailHostNet, "sandboxed container") {
		t.Errorf("netMode=host must not change the confinement framing:\n%s", jailHostNet)
	}
	if !strings.Contains(jailHostNet, "Host networking") {
		t.Errorf("netMode=host must still produce the host-networking line:\n%s", jailHostNet)
	}
	if strings.Contains(jailHostNet, "NOT disposable") {
		t.Errorf("netMode=host must NOT be read as the host confinement notch:\n%s", jailHostNet)
	}

	// A host-NOTCH environment with default networking gets the host framing and the BRIDGE
	// network line — the mirror-image confusion.
	hostNotch := BriefingContent(BriefingInput{Workspace: "/w", Confinement: "host"})
	if !strings.Contains(hostNotch, "NOT disposable") {
		t.Errorf("confinement=host must produce the host framing:\n%s", hostNotch)
	}
	if !strings.Contains(hostNotch, "Bridge mode") {
		t.Errorf("confinement=host must not change the NETWORK mode (default is bridge):\n%s", hostNotch)
	}
}
