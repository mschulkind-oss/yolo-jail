package cli

// hostmanagementgate.go is the `host_management` REFUSAL: the one place the declared
// ownership contract (docs/design/config-ownership-and-promotion.md §4) stops a host apply.
//
// # What the key decides here, and what it deliberately does not
//
// It selects the MODE the host notch renders in — `unrendered` at `none`, whole-file
// `stateful` (plus a pack's own `rmw`) at `own` — and this file is the half of that which
// needs no render engine: at `none` there is no rendered host surface at all, so the command
// that would write one refuses and names the key that decided it (§4.1). `none` is also the
// UNSET state since the `assert` retirement (OQ-CO14), so this refusal is what a user who never
// wrote the key now meets — with no prompt or notice at upgrade, only here, at the act.
//
// # The retired `"assert"` refuses with a message of its own
//
// It resolves to `none` like any unusable value, and printing `none`'s sentences to a user
// whose file says "assert" would describe a file they did not write. So a config still
// spelling it gets config.HostManagementRetired's targeted refusal instead (OQ-CO14 face 1),
// above every stage, exactly where `none`'s goes.
//
// > THE KEY ENABLES THE MECHANISM. IT DOES NOT GRANT THE APPROVAL (§4.4).
//
// That is host_apply_on_launch's ruling, and it holds identically: `own` selects the mode, it
// does not pre-authorize the writes that mode performs. Every confirmation an apply runs today
// — confirmHostLosses, the skills and briefing adoption gates, the dropped-pack retire prompt —
// is untouched by this file.
//
// # `own` no longer refuses, and the refusal it replaced said why
//
// Until §10's last build step landed, `own` refused here: whole-file composition at the host
// notch did not exist, and silently rendering `rmw` instead would have told a user who
// declared their file DERIVED that it is derived while it still held bytes existing nowhere
// else — the exact inference P1 exists to end. It is built now (render.HostOwnedModes, and
// the `stateful` arm in entrypoint.RenderHostPack), so this file's job for `own` is the
// TRANSLATION below rather than a refusal.
//
// # The translation is the other half, and it must not be a second reading of the key
//
// `hostOwnership` turns the declared contract into the primitive the render notch is keyed on
// (render.HostOwnership). Every host entry takes it as a PARAMETER from here — none of them
// reads the user config itself — so one invocation cannot render under two contracts, and a
// test renders the contract it names rather than the invoking user's.

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostOwnership resolves the declared `host_management` contract into the render notch's
// ownership primitive.
//
// It FAILS CLOSED on a value render cannot name, and that branch is unreachable by
// construction rather than by hope: render.TestHostOwnershipNamesMatchTheConfigVocabulary
// pins config.KnownHostManagements against render.DeclarableOwnerships in both directions, so
// a value added to one table alone fails a test instead of quietly landing every host target
// on OwnershipUnstated — which renders nothing. Keeping the branch anyway is what makes that
// outcome a refusal-shaped no-op rather than an inherited contract.
func hostOwnership() render.HostOwnership {
	o, ok := render.HostOwnershipFor(string(config.HostManagementMode()))
	if !ok {
		return render.OwnershipUnstated
	}
	return o
}

// hostManagementRefusal is the message for a mode that cannot render, or "" when the apply
// may proceed. Split from the writer below so the tests read the same sentences the refusal
// prints, rather than a second copy of them. declared says whether the user WROTE the key: an
// unset key is `none` since OQ-CO14, and the sentence says which of the two it is reading
// rather than claiming the file says something it does not.
//
// The remedy names `own` (the one value that renders), then `yolo config promote` for a key the
// user keeps by hand — IN THAT ORDER: promote lifts a captured key into the local pack, and only
// an owned apply captures one (its adoption takes in what the file already holds), so under
// `none` there is no capture store at the host and promote answers "Nothing to promote" — and
// `--revert`, the way back to a file purely the user's on a home an earlier yolo wrote into. It
// used to name `"assert"`, which no longer exists.
func hostManagementRefusal(mode config.HostManagement, declared bool) string {
	switch mode {
	case config.HostManagementNone:
		state := "is \"none\" in " + paths.UserConfigPath()
		if !declared {
			state = "is unset in " + paths.UserConfigPath() + ", which means \"none\""
		}
		return "`host_management` " + state + ": your agents' config files are yours " +
			"entirely, so there is nothing for yolo to render into your home and nothing was " +
			"written.\n" +
			"  Set it to \"own\" to have yolo compose those files from your packs; an owned " +
			"apply captures each key you keep by hand, and `yolo config promote` then declares " +
			"a captured key into your local pack. `yolo config-ref` says what each value means.\n" +
			"  If an earlier yolo wrote keys into those files, `yolo host apply --revert` " +
			"lists them and takes them out."
	}
	return ""
}

// retiredHostManagementRefusal prints the retired-`"assert"` refusal under the verb's own
// prefix and returns its exit code, or (0, false) when the user config does not say it. One
// function for every host verb that would have written under `assert`, so the wording is
// config's (HostManagementRetired) and the exit code is the same everywhere. EXIT 1: the argv
// is fine, and the user's configuration is what declined it.
func retiredHostManagementRefusal(errw io.Writer, verb string) (int, bool) {
	msg := config.HostManagementRetired()
	if msg == "" {
		return 0, false
	}
	fmt.Fprintf(errw, "%s: host_management: %s\n", verb, msg)
	return 1, true
}

// refuseHostManagement stops a host apply the declared contract does not permit, printing the
// refusal and returning its exit code.
//
// IT MUST BE CALLED ABOVE EVERY STAGE, not just above the render — the same rule
// jsonRefusedForPosture earned the hard way. When `yolo host apply` still had a stage after
// the render (the since-removed --shell-init, which appended a PATH line to the user's shell
// rc), a refusal that stopped only the render left that stage writing inside a command that
// wrote nothing, which is the write P3 forbids. Hence the two call sites (hostApply, and
// applyMain's host notch) rather than one inside applyHostSurveyed.
//
// EXIT 1, not 2. Nothing about the argv is wrong: the command is well-formed and the user's
// own configuration is what declined it.
//
// THE DRY RUN'S DOCUMENT GOES OUT BESIDE IT. With format JSON the refusal still prints its prose
// on errw, and out gets the dry run's document saying so (emitRefusedHostApplyDoc: outcome
// `refused`, failed_stages ["host_management"]) — the unset key is "none" since OQ-CO14, so a
// fresh home is a home this refuses, and an empty stdout there is the silence a machine consumer
// cannot diagnose. Only the dry run asks: both callers refuse JSON for an acting apply
// (jsonRefusedForPosture) before they reach this.
func refuseHostManagement(out, errw io.Writer, format string) (int, bool) {
	rc, refused := retiredHostManagementRefusal(errw, "yolo host apply")
	var verdict string
	if refused {
		verdict = hostManagementVerdict(config.HostManagementNone, false, true)
	} else {
		mode, declared := config.HostManagementDeclared()
		msg := hostManagementRefusal(mode, declared)
		if msg == "" {
			return 0, false
		}
		fmt.Fprintf(errw, "yolo host apply: %s\n", msg)
		rc, verdict = 1, hostManagementVerdict(mode, declared, false)
	}
	if outfmt.IsJSON(format) {
		rc = emitRefusedHostApplyDoc(out, errw, verdict, rc)
	}
	return rc, true
}

// hostManagementVerdict is the dry-run document's verdict for an apply this gate refuses, in the
// shape of the other refused verdicts (hostApplyVerdict's outcomeRefused): what an --assert would
// do, why, the value to set, and that nothing would be written. retired is the retired
// `"assert"`; otherwise mode and declared are hostManagementRefusal's.
func hostManagementVerdict(mode config.HostManagement, declared, retired bool) string {
	const own = `set it to "own" to have yolo compose your agents' config files from your packs`
	path := paths.UserConfigPath()
	switch {
	case retired:
		return `An --assert would REFUSE: host_management is the retired "assert" in ` + path +
			` (` + own + `, or to "none", or delete the key), so nothing would be written.`
	case mode == config.HostManagementNone && declared:
		return `An --assert would REFUSE: host_management is "none" in ` + path + `, under which ` +
			`yolo renders nothing into your home (` + own + `), so nothing would be written.`
	default:
		return `An --assert would REFUSE: host_management is unset in ` + path + `, which means ` +
			`"none", under which yolo renders nothing into your home (` + own + `), so nothing ` +
			`would be written.`
	}
}
