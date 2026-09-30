package jailcontent

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// C2. The per-notch header's two most consequential facts — what enforces the boundary, and
// whether agent autonomy is on — are DERIVED rather than written per notch. These tests pin
// that derivation, not the prose: the framing sentence still differs by notch on purpose (a
// human reads it), and asserting its exact words would just make the wording unrefactorable.
//
// The source of the derivation moved on 2026-09-13 (OQ-DP2): it was render.ProfileFor, which
// takes the notch alone, and is now ConfinementProfile, which takes the notch, the MECHANISM
// and the platform. The tests below that pass no mechanism still compare against ProfileFor,
// and that is not laziness — with an empty mechanism and isMacOS=false the two agree by
// construction, which is what "the jail's bytes do not move" means here. The mechanism rows
// are in the section at the bottom.

// briefingHeader is the confinement header block — everything before "## Environment", or the
// whole body at the host notch, whose base IS the header (there is no launch to describe).
func briefingHeader(t *testing.T, confinement string) string {
	t.Helper()
	return headerOf(t, confinement, BriefingContent(BriefingInput{Workspace: "/w", Confinement: confinement}))
}

func headerOf(t *testing.T, confinement, out string) string {
	t.Helper()
	i := strings.Index(out, "## Environment")
	if confinement == "host" {
		if i >= 0 {
			t.Fatalf("the host notch's base describes a launch (## Environment):\n%s", out)
		}
		return out
	}
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

	// A host-NOTCH environment gets the host framing, and — the mirror-image confusion — is
	// never read as a network mode either way. Since notch convergence item 26 its base is the
	// header alone, so it carries no network paragraph at all: there is no launch whose stack
	// the paragraph could describe.
	hostNotch := BriefingContent(BriefingInput{Workspace: "/w", Confinement: "host"})
	if !strings.Contains(hostNotch, "NOT disposable") {
		t.Errorf("confinement=host must produce the host framing:\n%s", hostNotch)
	}
	if strings.Contains(hostNotch, "Bridge mode") || strings.Contains(hostNotch, "Host networking") {
		t.Errorf("confinement=host must describe no launch's network:\n%s", hostNotch)
	}
}

// THE HOST NOTCH'S BASE IS ITS HEADER, and none of the launch's sections: each describes a jail
// (the /workspace bind, the jail's limitations, the shims, how to ask for a package), and at the
// host every one would be a false sentence to an agent on the real machine.
func TestTheHostNotchBaseIsTheHeaderAlone(t *testing.T) {
	out := BriefingContent(BriefingInput{Workspace: "/w", Confinement: "host",
		BlockedTools: []BlockedTool{{Name: "grep"}}, Loopholes: []Loophole{{Name: "l", Desc: "d"}},
		ContextMounts: []ContextMount{{Path: "/ctx/a", Host: "/a"}}})
	if want := strings.Join(confinementHeader("host", "", false), "\n") + "\n"; out != want {
		t.Errorf("host base:\n got %q\nwant %q", out, want)
	}
	for _, jailOnly := range []string{"/workspace", "## Limitations", "## Blocked Tools",
		"## Loopholes", "## Skills", "## Packages"} {
		if strings.Contains(out, jailOnly) {
			t.Errorf("the host base carries the jail's %q:\n%s", jailOnly, out)
		}
	}
	// HostBriefingBase is that base with agents_md_extra appended the jail's way.
	if got := HostBriefingBase("  EXTRA  \n", false); got != ComposeBriefing(out, "  EXTRA  \n") ||
		!strings.Contains(got, "real machine") && !strings.Contains(got, "REAL") {
		t.Errorf("HostBriefingBase:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// OQ-DP2: the mechanism is the second axis, and both printing surfaces read one
// function (docs/design/declaration-parity.md §2.3).
// ---------------------------------------------------------------------------

// The header's vector is ConfinementProfile's, for every (notch, mechanism, platform)
// combination — not render.ProfileFor's, which is platform-blind by construction and
// returns the LINUX spelling of every preset.
//
// The macos-user rows are the ones that were wrong: an agent inside a Seatbelt sandbox was
// told it was enforced by "namespaces" and "a baked image", three lines under a paragraph
// saying there was no container and no image (DP-B18, DP-B19). The Apple Container row is
// the same defect at a backend where it is currently invisible — the jail-with-a-container
// branch prints no vector at all — and is asserted anyway, so the vector is right on the
// day that branch gains one.
func TestBriefingHeaderVectorFollowsTheMechanism(t *testing.T) {
	cases := []struct {
		notch     string
		mechanism string
		isMacOS   bool
		want      []render.Primitive
	}{
		{"jail", "macos-user", true, []render.Primitive{render.PrimSeparateUser, render.PrimSeatbelt}},
		{"guest", "macos-user", true, []render.Primitive{render.PrimSeparateUser, render.PrimSeatbelt}},
		{"guest", "podman", false, []render.Primitive{render.PrimNamespaces, render.PrimLandlock}},
		{"guest", "podman", true, []render.Primitive{render.PrimSeparateUser, render.PrimSeatbelt}},
		// The notch wins over every mechanism at `host`: nothing contains the agent there,
		// whatever backend the config names.
		{"host", "macos-user", true, nil},
		{"host", "podman", false, nil},
	}
	for _, tc := range cases {
		name := tc.notch + "/" + tc.mechanism
		t.Run(name, func(t *testing.T) {
			out := BriefingContent(BriefingInput{
				Workspace: "/w", Confinement: tc.notch,
				Mechanism: tc.mechanism, IsMacOS: tc.isMacOS,
			})
			header := headerOf(t, tc.notch, out)
			for _, prim := range render.PrimitiveOrder() {
				phrase := render.PrimitiveDoes(prim)
				want := false
				for _, p := range tc.want {
					if p == prim {
						want = true
					}
				}
				if got := strings.Contains(header, phrase); got != want {
					t.Errorf("%s: header states %q = %v, want %v — the agent's vector must be "+
						"this backend's, not the platform-blind preset:\n%s",
						name, phrase, got, want, header)
				}
			}
		})
	}
}

// DP-B17: the mechanism must CHANGE the answer, not merely be accepted as a parameter.
// It was acted on in exactly one of five arms — confinementHeader("guest", true) and
// ("guest", false) were byte-identical, as were the two `host` calls — so the field's own
// doc comment claimed two separate axes while only one of them moved anything.
//
// The `host` pair is asserted IDENTICAL on purpose and is not an exception: at that notch
// nothing contains the agent whatever the mechanism is, so an answer that moved with the
// backend would be the overclaim.
func TestBriefingHeaderMechanismChangesTheGuestAnswer(t *testing.T) {
	guest := func(mechanism string) string {
		return strings.Join(confinementHeader("guest", mechanism, false), "\n")
	}
	if guest("macos-user") == guest("podman") {
		t.Error("the guest header is byte-identical across mechanisms — a Seatbelt sandbox " +
			"and a bwrap namespace are different boundaries and the header names neither")
	}
	host := func(mechanism string) string {
		return strings.Join(confinementHeader("host", mechanism, false), "\n")
	}
	if host("macos-user") != host("podman") {
		t.Error("the host header moved with the mechanism — at that notch there is no " +
			"boundary for a backend to change")
	}
}

// DP-B19's second half: the jail-without-a-container branch carried its own copy of the
// "Jail tooling" line AND appended enforcementLines, which ends with the same line, so the
// one backend whose header was rewritten to be honest printed it twice.
func TestBriefingHeaderStatesTheToolingLineOnce(t *testing.T) {
	for _, tc := range []struct{ notch, mechanism string }{
		{"jail", "macos-user"}, {"jail", "podman"}, {"guest", "podman"}, {"host", "podman"},
	} {
		header := strings.Join(confinementHeader(tc.notch, tc.mechanism, false), "\n")
		if n := strings.Count(header, "Jail tooling:"); n != 1 {
			t.Errorf("%s/%s: `Jail tooling` appears %d times, want 1:\n%s",
				tc.notch, tc.mechanism, n, header)
		}
	}
}

// An unresolvable notch still FAILS CLOSED once the mechanism is threaded. This is the one
// input ConfinementProfile has to treat differently from cli.describe's use of it: describe
// errors out on a name render.KindForNotch cannot resolve, and the briefing renders one —
// so a default arm that assumed `jail` would hand an agent on a real machine the jail's
// autonomy bit.
func TestBriefingUnknownNotchFailsClosedWhateverTheMechanism(t *testing.T) {
	for _, mechanism := range []string{"", "podman", "container", "macos-user"} {
		header := strings.Join(confinementHeader("bwrap-guest", mechanism, false), "\n")
		if !strings.Contains(header, "autonomy is **OFF**") {
			t.Errorf("mechanism=%q: an unrecognized notch must fail closed on autonomy:\n%s",
				mechanism, header)
		}
		if strings.Contains(header, "a baked image") {
			t.Errorf("mechanism=%q: an unrecognized notch was given the jail's vector:\n%s",
				mechanism, header)
		}
	}
}

// DP-B1's second briefing site, at the renderer. The `## Limitations` bullet described a
// read-only `/ctx/` tree unconditionally — so a jail with no `mounts` at all, and every
// macos-user jail (which binds none whatever the config says), was told a filesystem
// existed that nothing had mounted. The section listing what was BOUND and the line
// describing it now appear and disappear together.
func TestBriefingDescribesCtxOnlyWhenSomethingIsMounted(t *testing.T) {
	with := BriefingContent(BriefingInput{
		Workspace: "/w", ContextMounts: []ContextMount{{Path: "/ctx/logs", Host: "/host/logs"}},
	})
	if !strings.Contains(with, "context mounts under `$YOLO_CONTEXT_DIR` are read-only unless marked read-write") {
		t.Errorf("a jail WITH context mounts must still be told they are read-only unless marked:\n%s", with)
	}
	without := BriefingContent(BriefingInput{Workspace: "/w"})
	if strings.Contains(without, "/ctx/") {
		t.Errorf("a jail with no context mounts was told about a /ctx tree:\n%s", without)
	}
	if !strings.Contains(without, "- No sudo/root.") {
		t.Errorf("the rest of the bullet must survive:\n%s", without)
	}
}
