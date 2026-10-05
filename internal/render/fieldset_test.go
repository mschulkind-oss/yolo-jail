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

// TestBlockedToolIsHonoredAtTheHostAndDeliveredAtLaunch pins HE-D11 in the census: `yolo host --`
// puts the blockers first on the PATH of the program it starts, so the kind is honored at the
// host, and delivered AT LAUNCH ONLY (report-tiers.md): `yolo host apply`, which starts nothing,
// writes no file for it, and that is no longer an honored-but-unbuilt entry, which the apply's
// notch line reads as "does not apply at the host".
func TestBlockedToolIsHonoredAtTheHostAndDeliveredAtLaunch(t *testing.T) {
	fields := HostFields()
	if !fields.Honors(packdecl.KindBlockedTool) {
		t.Fatalf("blocked-tool is refused at the host, but `yolo host --` renders it (HE-D11): %q",
			fields.Refuse(packdecl.KindBlockedTool))
	}
	if r := fields.Refuse(packdecl.KindBlockedTool); r != "" {
		t.Errorf("an honored kind has a refusal reason: %q", r)
	}
	reason, ok := HostAtLaunch(packdecl.KindBlockedTool)
	if !ok {
		t.Fatal("blocked-tool is not delivered at launch, so the apply's notch line would name it " +
			"as not applying at the host while `yolo host --` puts the blockers on the PATH")
	}
	for _, want := range []string{"`yolo host apply`", "`yolo host -- <program>`"} {
		if !strings.Contains(reason, want) {
			t.Errorf("blocked-tool's at-launch reason does not name %s: %q", want, reason)
		}
	}
}

// TestTheAtLaunchKindsLeftTheUnbuiltMap is the regression half of the at-launch outcome: env,
// adapter and blocked-tool sat in hostUnimplemented, which the apply's notch line reads through
// notchInapplicable, so `yolo host apply` named them as not applying at the host while
// `yolo host -- <program>` delivered every one. A kind is in at most one of the two maps.
func TestTheAtLaunchKindsLeftTheUnbuiltMap(t *testing.T) {
	for _, k := range []packdecl.Kind{packdecl.KindEnv, packdecl.KindAdapter, packdecl.KindBlockedTool} {
		if why, unbuilt := HostUnimplemented(k); unbuilt {
			t.Errorf("%s is honored-but-unbuilt at the host (%q), but `yolo host --` delivers it: "+
				"it belongs in hostAtLaunch", k, why)
		}
		if _, ok := HostAtLaunch(k); !ok {
			t.Errorf("%s is not delivered at launch", k)
		}
	}
	for k := range hostAtLaunch {
		if _, unbuilt := hostUnimplemented[k]; unbuilt {
			t.Errorf("%s is in both hostAtLaunch and hostUnimplemented — one kind, two answers", k)
		}
	}
	// hook is what is left, and it is the end state's last entry rather than an at-launch kind:
	// `yolo host --` runs no pack hook either.
	if _, unbuilt := HostUnimplemented(packdecl.KindHook); !unbuilt {
		t.Error("hook left hostUnimplemented, but no host verb runs a pack hook")
	}
	if _, ok := HostAtLaunch(packdecl.KindHook); ok {
		t.Error("hook is named as delivered at launch, and `yolo host --` runs no pack hook")
	}
}

// TestEveryHostAtLaunchKindIsHonoredOrRefusedWithItsOwnReason: an at-launch kind is either one
// the host FieldSet honors (env, adapter, blocked-tool) or one it refuses with a reason of its
// own for the shape the host does not deliver (service, loophole). A kind honored and carrying
// a withheld shape names it in hostWithheldAtLaunch, since Refuse cannot.
func TestEveryHostAtLaunchKindIsHonoredOrRefusedWithItsOwnReason(t *testing.T) {
	fields := HostFields()
	if len(hostAtLaunch) < 2 {
		t.Fatalf("hostAtLaunch has %d entries — the test below checks nothing", len(hostAtLaunch))
	}
	for k := range hostAtLaunch {
		if fields.Honors(k) {
			continue
		}
		got := fields.Refuse(k)
		if got == "" || strings.Contains(got, "is not applicable at this confinement level") {
			t.Errorf("%s is delivered at launch and refused by the FieldSet, and its refusal does "+
				"not say which shape the host leaves undone: %q", k, got)
		}
	}
	for k := range hostWithheldAtLaunch {
		if _, ok := hostAtLaunch[k]; !ok {
			t.Errorf("%s has a withheld shape but no delivered one: hostWithheldAtLaunch is the "+
				"other half of an at-launch kind, never a kind of its own", k)
		}
		if !fields.Honors(k) {
			t.Errorf("%s has a hostWithheldAtLaunch entry and is refused by the FieldSet, whose "+
				"refusal reason already states it", k)
		}
	}
	// The "Launch a jail to run it" remedy was a notch fact wearing a warning's word (P2), and
	// it was false of a credential loophole, whose doorway `yolo host --` opens.
	for _, k := range []packdecl.Kind{packdecl.KindLoophole, packdecl.KindService} {
		if r := fields.Refuse(k); strings.Contains(r, "Launch a jail") {
			t.Errorf("%s's reason still tells the reader to launch a jail: %q", k, r)
		}
	}
}

// TestHostAtLaunchReasonsNameTheLaunchAndTheApply holds each at-launch reason to the shape
// TestEnvAndLaunchRefusalsBlameTheCommandNotTheNotch held env's honored-but-unbuilt one to: it
// names the verb that delivers it and the one that writes no file, and does not blame the notch,
// since the notch does deliver it.
func TestHostAtLaunchReasonsNameTheLaunchAndTheApply(t *testing.T) {
	for k, why := range hostAtLaunch {
		for _, want := range []string{"`yolo host -- <program>`", "`yolo host apply`"} {
			if !strings.Contains(why, want) {
				t.Errorf("%s: the at-launch reason does not name %s: %q", k, want, why)
			}
		}
		for _, blames := range []string{"off-container", "below jail", "without a container"} {
			if strings.Contains(why, blames) {
				t.Errorf("%s: the at-launch reason blames the notch (%q), which delivers it: %q",
					k, blames, why)
			}
		}
	}
}
