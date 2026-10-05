package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestGuestNotchSentenceHasExactlyOneHome is the drift guard NotchUnbuilt exists for.
//
// Two packages say this sentence — `cli.applyMain` for `yolo apply --at guest` and
// `run.Run` for a guest-notch LAUNCH (docs/design/declaration-parity.md DP-A12, DP-B16) —
// and internal/cli imports internal/cli/run, so neither could own the string. OQ-DP3 ruled
// the launch gate must reuse apply's sentence VERBATIM, and "verbatim" enforced by two
// authors reading each other's files is the drift this catalog is a list of. So the
// property is stronger than equality: the sentence exists ONCE in the tree.
//
// It scans production sources only. A test may spell the words (this file does, on the
// line below), and so may a doc — what must not exist is a second thing a user could be
// shown.
func TestGuestNotchSentenceHasExactlyOneHome(t *testing.T) {
	const distinctive = "is not built yet (env-manager plan Phase 7"
	if !strings.Contains(NotchUnbuilt("apply"), distinctive) {
		t.Fatalf("the guard's needle no longer appears in NotchUnbuilt(%q) = %q — update "+
			"the needle, do not delete the guard", "apply", NotchUnbuilt("apply"))
	}

	root := filepath.Join("..") // internal/
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(b), distinctive) {
			found = append(found, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	want := []string{"../render/fieldset.go"}
	if len(found) != 1 || found[0] != want[0] {
		t.Errorf("the guest-notch sentence must live in exactly one production file "+
			"(%s, as NotchUnbuilt).\ngot: %v\n"+
			"A second copy is how `yolo apply --at guest` and a guest-notch launch come to "+
			"describe one notch differently — call render.NotchUnbuilt instead.",
			want[0], found)
	}
}

// TestNotchUnbuiltNamesTheVerbItWasGiven: the verb is the ONLY thing that varies, which is
// what makes "verbatim" mean something. A call site that got the phase wrong, or dropped
// the plan reference, would be a different sentence wearing the same function.
func TestNotchUnbuiltNamesTheVerbItWasGiven(t *testing.T) {
	for _, verb := range []string{"apply", "launch"} {
		got := NotchUnbuilt(verb)
		if !strings.HasPrefix(got, verb+" at the guest notch is not built yet") {
			t.Errorf("NotchUnbuilt(%q) = %q, want it to open with the verb", verb, got)
		}
		if !strings.Contains(got, "Phase 7") || !strings.Contains(got, "LSM-confined backend") {
			t.Errorf("NotchUnbuilt(%q) = %q, want it to name the phase and what it builds",
				verb, got)
		}
	}
	// The two differ ONLY by the verb.
	if strings.TrimPrefix(NotchUnbuilt("apply"), "apply") !=
		strings.TrimPrefix(NotchUnbuilt("launch"), "launch") {
		t.Error("NotchUnbuilt's two call sites would print sentences that differ by more " +
			"than the verb")
	}
}

// TestServiceAndInterceptCarryTheirOwnRefusalReason closes DP-B27 (DP-L6) for the kinds the host
// still refuses: `service` fell to Refuse's generic fallback — "<kind> is not applicable at this
// confinement level", which names the kind and explains nothing — while the real reason sat,
// hand written, in internal/cli/config_ref.txt's host-notch list. `intercept`'s reason is its own
// since HE-D11 took `blocked-tool` out of this set: an intercept layers a permission over a CLI,
// and the agent at the host runs as the user (boundary-broker.md BB-D17).
//
// Asserted on the HOST FieldSet rather than on the map, because "the host notch refuses
// this kind" and "the reason is specific" are one fact for a reader and the map is an
// implementation detail of it.
func TestServiceAndInterceptCarryTheirOwnRefusalReason(t *testing.T) {
	fields := HostFields()
	for _, k := range []packdecl.Kind{packdecl.KindService, packdecl.KindIntercept} {
		if fields.Honors(k) {
			t.Fatalf("%s is honored at the host notch now — this test is asserting the "+
				"reason for a refusal that no longer happens", k)
		}
		got := fields.Refuse(k)
		if got == "" {
			t.Fatalf("%s: Refuse returned nothing for a kind the host notch does not honor", k)
		}
		if strings.Contains(got, "is not applicable at this confinement level") {
			t.Errorf("%s still falls to the generic fallback, which says nothing a reader "+
				"can act on: %q", k, got)
		}
	}
	// The WORDS are config_ref.txt's, so a reader who meets the refusal and a reader who
	// looks the kind up in the manual get one answer. Distinctive fragments rather than
	// whole strings: the manual hard-wraps, so a byte comparison would fail on the wrap
	// and not on the meaning (the same reason
	// TestEveryHostNotchInapplicableKindHasItsReasonDocumented asserts an entry, not text).
	for kind, fragment := range map[packdecl.Kind]string{
		packdecl.KindIntercept: "the agent runs as you and can run the real program",
		packdecl.KindService:   "a daemon pair plus an endpoint file under the jail's /run",
	} {
		if !strings.Contains(fields.Refuse(kind), fragment) {
			t.Errorf("%s's reason is not the one config_ref.txt gives a reader.\n"+
				"want substring: %q\ngot: %q", kind, fragment, fields.Refuse(kind))
		}
	}
	// And the old reason is gone from the one kind that still carries a shim's name: "yolo owns no
	// PATH entry" stopped being true of `yolo host --` when it composed the child's PATH (HE-D1).
	if strings.Contains(fields.Refuse(packdecl.KindIntercept), "owns no PATH entry") {
		t.Errorf("intercept's reason still says yolo owns no PATH entry off-container, which "+
			"`yolo host --` has since HE-D1: %q", fields.Refuse(packdecl.KindIntercept))
	}
}

// TestBlockedToolIsHonoredAtTheHostAndUnbuiltForApply pins HE-D11 in the census: `yolo host --`
// puts the blockers first on the PATH of the program it starts, so the kind is honored at the
// host, and `yolo host apply`, which starts nothing, carries env's honored-but-unbuilt shape and
// says what does deliver it.
func TestBlockedToolIsHonoredAtTheHostAndUnbuiltForApply(t *testing.T) {
	fields := HostFields()
	if !fields.Honors(packdecl.KindBlockedTool) {
		t.Fatalf("blocked-tool is refused at the host, but `yolo host --` renders it (HE-D11): %q",
			fields.Refuse(packdecl.KindBlockedTool))
	}
	if r := fields.Refuse(packdecl.KindBlockedTool); r != "" {
		t.Errorf("an honored kind has a refusal reason: %q", r)
	}
	reason, unbuilt := HostUnimplemented(packdecl.KindBlockedTool)
	if !unbuilt {
		t.Fatal("blocked-tool has no honored-but-unbuilt entry, so `yolo host apply` would " +
			"render it in silence — and apply starts no process to put a shim in front of")
	}
	for _, want := range []string{"`yolo host apply`", "`yolo host -- <program>`"} {
		if !strings.Contains(reason, want) {
			t.Errorf("blocked-tool's apply reason does not name %s: %q", want, reason)
		}
	}
}
