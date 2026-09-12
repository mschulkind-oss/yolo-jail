package packload_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// copilot's `--yolo` IS AN AUTONOMY CONTRIBUTION, NOT A PLAIN LAUNCH FLAG — the jail notch
// injects it, the host notch does not, and the declaration is what makes that true.
//
// `--yolo` is copilot's permission bypass: `--allow-all-tools --allow-all-paths
// --allow-all-urls`, in its own help text. Declared as a plain `launch` contribution it was
// outside the §4.2 notch policy (jail/guest autonomous, host guarded) by construction — the
// one thing that policy exists to prevent, after `yolo host apply` leaked shipped packs'
// jail-bypass keys onto a real machine. It was harmless only because `cli.hostExec` injects
// no flags at all today, which is a fact about that function and not about the declaration.
//
// THIS FAILS IF THE FLAG MOVES BACK. The `plain` assertion is the one that does the work: the
// notch pair below would still pass with `--yolo` back in `launch.flags`, because the guarded
// posture declares no copilot entry to overwrite it with — see TestCopilotHasNoGuardedPosture
// for why that emptiness is the right declaration. Together they say the flag is gated at the
// notch AND gated by being declared where the notch can see it.
func TestCopilotYoloIsDeclaredUnderAutonomyNotAsAPlainLaunchFlag(t *testing.T) {
	packs := loadAll(t)
	copilot := packNamed(t, packs, "copilot")

	// 1. The plain launch contribution carries the ALIAS and no flags.
	plain := copilot.Decl.LaunchFlagContributions()["copilot"]
	if len(plain) != 0 {
		t.Errorf("copilot's plain launch flags = %v, want none — a permission-bypass flag "+
			"declared here escapes the autonomy notch policy entirely; it belongs in the "+
			"autonomy contribution's autonomous posture", plain)
	}
	if aliases := copilot.Decl.FlagAliasContributions()["--yolo"]; len(aliases) == 0 {
		t.Error("the copilot pack stopped declaring the --yolo alias — a user who types `-y` " +
			"now gets `--yolo` injected beside it (InjectLaunchFlags reads FlagAliases)")
	}

	// 2. The notch pair, read off render's ONE notch→preset table rather than a literal
	//    true/false, so flipping HostProfile's policy bit fails here too.
	jail := packload.LaunchFlagsFor(packs, render.ProfileFor(render.KindJail).AgentAutonomy)["copilot"]
	if strings.Join(jail, " ") != "--yolo" {
		t.Errorf("at the JAIL notch copilot's flags = %v, want [--yolo]", jail)
	}
	host := packload.LaunchFlagsFor(packs, render.ProfileFor(render.KindHost).AgentAutonomy)["copilot"]
	if len(host) != 0 {
		t.Errorf("at the HOST notch copilot's flags = %v, want none: %q is a permission "+
			"bypass and nothing contains the agent on a real machine", host, strings.Join(host, " "))
	}
}

// copilot's GUARDED POSTURE IS ABSENT, and that is the declaration rather than an omission.
//
// A guarded posture exists to TIGHTEN — to assert the safe value where an unsafe one would
// otherwise persist. claude needs one because its autonomous posture writes `managed` keys
// into `~/.claude/settings.json`, a file that survives the render and carries a host layer, so
// a stale `skipDangerousModePermissionPrompt: true` would outlive the notch change. copilot's
// autonomous posture is a LAUNCH FLAG ONLY, and a flag has no persistence: it is composed per
// launch from this table, so NOT selecting it is the whole of the tightening. There is nothing
// for a guarded posture to undo.
//
// Nor may it be spelled as an empty `guarded.launch` entry for copilot. A posture entry
// REPLACES the binary's plain flags (LaunchFlagsFor), so `{"bin":"copilot"}` under `guarded`
// would strip every ordinary launch flag at the host notch too — a different and wrong claim,
// since a plain launch flag is one no notch gates. It would also hide exactly the regression
// the sibling test above catches.
//
// packdecl requires only "at least one of autonomous or guarded", so this is valid; `yolo pack
// footprint` reports it as "autonomous posture only", which is the honest disclosure.
func TestCopilotHasNoGuardedPosture(t *testing.T) {
	copilot := packNamed(t, loadAll(t), "copilot")

	ac := copilot.Decl.AutonomyContributions()
	if ac == nil {
		t.Fatal("the copilot pack declares no autonomy contribution — `--yolo` is then outside " +
			"the notch policy again")
	}
	if ac.Autonomous == nil {
		t.Error("copilot's autonomous posture is gone; the jail notch would launch copilot with " +
			"permission prompts on")
	}
	if ac.Guarded != nil {
		t.Error("copilot grew a guarded posture. That may well be right — but it is a decision " +
			"about what `yolo host apply` and `yolo host -- copilot` assert on a real machine, " +
			"so state it in the manifest's commit and rewrite this test's reasoning rather than " +
			"deleting the check")
	}

	// The DECLARED validity of the shape, not just the shipped instance: a posture pair with
	// only `autonomous` is what packdecl accepts, and a pack with neither is what it refuses.
	if _, probs := packdecl.Decode([]byte(
		`{"name":"x","contributes":[{"kind":"autonomy","autonomous":{"launch":[{"bin":"x","flags":["--f"]}]}}]}`,
	)); len(probs) != 0 {
		t.Errorf("an autonomy contribution with only an autonomous posture must be valid: %v", probs)
	}
	if _, probs := packdecl.Decode([]byte(
		`{"name":"x","contributes":[{"kind":"autonomy"}]}`,
	)); len(probs) == 0 {
		t.Error("an autonomy contribution with NEITHER posture should still be a validation error")
	}
}

func packNamed(t *testing.T, packs []*packload.Pack, name string) *packload.Pack {
	t.Helper()
	for _, p := range packs {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no %q pack among the embedded set", name)
	return nil
}
