package check

// envoverrides.go predicts the launch's ENV-OVERRIDE refusal: a jail that would carry a
// selected pack's env contribution beside something that pack declares OVERRIDES it
// (packdecl.EnvOverride — packs/aws-auth's pointer beside a bearer or a static key pair) is
// refused before it starts, and one beside something the pack declares only MAY override it
// (`certain: false` — aws-auth's ~/.aws grant) is warned about and launches
// (internal/cli/run/envoverrides.go; docs/design/sso-backed-bedrock.md, OQ-SSO8).
//
// It shipped there and only there once already, so without this file a config `yolo check`
// called clean was still refused at launch — the one thing `check` exists to prevent, and
// the same defect capabilities.go and protocols.go were each written to close.
//
// ⚠ IT FOLLOWS protocols.go, NOT capabilities.go, AND THE DIFFERENCE IS THE POINT.
// capabilities.go is a deliberate SECOND COPY of the launch's census, because reaching the
// real gate would mean exporting a method on `run.Options` and dragging its printer in.
// Nothing of the kind applies here: packload.EnvOverrideRefusal is a free function over
// inputs a caller assembles, so this file calls the same function the launch calls, through
// the same origin phrases (packload.From*). Two copies of the rule could disagree; there is
// only one.
//
// WHAT IS DUPLICATED IS THE INPUT ASSEMBLY, and that is the honest residue — the same
// residue protocols.go names. Composing the delivered environment the way the launch does
// is this file's own code, and getting one source wrong makes the prediction wrong in a
// way no shared function can catch. The rendered host_files destinations are NOT
// duplicated: both sides ask config.RenderedHostFilePaths.
//
// ⚠ TWO OF THE FOUR CHANNELS THAT DELIVER INTO A JAIL ARE VISIBLE FROM HERE — env_sources
// and the pack env fold — and the prediction is deliberately NARROWER than the launch rather
// than wider. A prediction that is wrong in places the launch is not would refuse a config
// that launches fine, which is worse than missing one — so the two it cannot see are stated
// rather than guessed at:
//
//   - packload.FromContainerArgv. The assembled `-e` argv carries every pack-shipped
//     loophole's `jail_env`, which reaches the jail nowhere else. `check` assembles no
//     argv, and inventing one would mean predicting a mount plan rather than reading a
//     config.
//   - packload.FromProfileEnv. Composing the provider environment runs the env-derive
//     runner (`packload.AgentEnv`), which executes each agent pack's derive. `check` reads
//     configuration; running a pack's code to answer a read-only question is a different
//     act, and protocols.go declines it for its own gate too.
//
// ⚠ THE ENVIRONMENT `check` RUNS IN IS NOT A CHANNEL AT ALL, and reading it was a false
// positive this file shipped with. No backend forwards the invoking shell into a jail under
// the variable's own name (run.jailOriginLookup has the account), so a key pair exported in
// the shell `yolo` is typed into overrides nothing — and an AWS_PROFILE exported there does
// not make a pair delivered through env_sources step aside either. The launch drops that
// answer; so does this.
//
// ⚠ NOR CAN IT SEE THE BACKEND, for the cost the launch pays to learn it: a directory
// `host_files` grant renders only where directories are delivered (run.hostFileDirsDeliver),
// and on macOS that takes the runtime probe — macos-user never delivers one, and Apple
// Container below its read-only-bind floor declines one. So a directory grant is counted
// only off macOS, where the launch's one backend is podman, which binds it. On macOS that is
// a false negative for podman and a current Apple Container, never a false positive.
//
// ⚠ AND IT CANNOT SEE `-p`, exactly as protocols.go cannot: a `-p <name>` is an argument to
// a launch that has not happened. So a clean prediction means "the `use_profiles` selection
// delivers nothing a pack declares overridden", never "any launch from this config does".
// `packs/aws-auth`'s pointer is gated on the `bedrock` profile, so `-p bedrock` is precisely
// the flag that turns a clean config into the refused one.
//
// ⚠ THE `~/.aws` GRANT IS PREDICTED HERE NOW, and not in Merged Configuration. It was a
// config.ValidateConfig error until the conflict moved into the pack's declaration
// (OQ-SSO8), so it is part of the same refusal as the variables and is predicted with them.
// It is also narrower than it was: it fires only while the contribution it overrides is
// delivered, and only for a grant that renders something. And it is a WARN, not a FAIL,
// since 2026-09-25: packs/aws-auth declares the entry `certain: false`, because a ~/.aws
// with no credentials for the resolved profile leaves the pointer serving, and the launch
// warns and continues on it — so the prediction does the same.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// overrideGapFinding is one tripped override as a report line: the verdict is the line's
// message and the rest of the finding is its note, so one problem reads as ONE graded line
// with its facts beneath it. It was one FAIL per LINE of the refusal until 2026-09-25, so a
// single bearer beside the pointer counted as four failures, three of them fragments.
type overrideGapFinding struct {
	msg, note string
}

// envOverrideGap reports what the Packs block should say about the override gate, as
// (errors, warnings) in this section's own vocabulary.
//
// Three outcomes, mirroring capabilityGap and protocolPairingGap so a reader does not have
// to learn another report vocabulary:
//
//   - Nothing tripped → NOTHING, not a PASS line. It matches the launch, which never
//     announces a gate it did not trip, and it keeps the golden that pins section ordering
//     and the pass/warn/fail counts from needing a bump for a line saying nothing happened.
//   - A CERTAIN override tripped → a FAIL carrying the launch's own refusal text verbatim,
//     the verdict as the message and the facts, the pack's reason and the remedy as the
//     note. The launch refusal is fatal and has NO ESCAPE HATCH.
//   - An UNCERTAIN override tripped (`certain: false`) → a WARN in the same shape, carrying
//     the launch's own warning text: the launch continues and prints it, so `check` exits 0
//     on it too.
//
// configWarn receives the env_sources loader's own findings, GRADED and COUNTED rather than
// printed beside the verdict — the sink rule protocols.go states, and what
// TestEveryConfigWarnSinkIsGraded enforces.
//
// dirsDeliver is whether this platform's launch delivers a directory `host_files` grant; see
// the backend note above.
func envOverrideGap(packs []*packload.Pack, merged *jsonx.OrderedMap,
	workspace string, dirsDeliver bool, configWarn func(string)) (errs, warns []overrideGapFinding) {
	// The hydrated secret channel. A dotenv file that cannot be read degrades to "delivered
	// nothing" with a warning on configWarn, which is the loader's own contract; it never
	// changes the verdict, because an unreadable source delivers no variable at launch either.
	userEnv := config.ResolveEnvSources(workspace, merged, configWarn)
	// The CONFIG's profile table — the `use_profiles` selection, which is the only one a
	// launch-less command has. See the `-p` note above.
	profiles := packload.ProfileTable(subMap(merged, "use_profiles"))
	packEnv := packload.EnvVarsFor(packs, profiles)

	findings := packload.EnvOverrideFindings(packs, profiles, func(name string) (string, bool) {
		// The order is the launch's own (run/profilechannel.go's deliverySource, read through
		// jailOriginLookup), minus the two channels named above. An EMPTY value is unset at
		// every step, exactly as there: the launch drops an empty value rather than
		// composing an empty token.
		if str(userEnv, name) != "" {
			return packload.FromEnvSources, true
		}
		if packEnv[name] != "" {
			return packload.FromPackEnv, true
		}
		return "", false
	}, config.RenderedHostFilePaths(merged, dirsDeliver))
	for _, f := range findings {
		g := overrideGapFinding{msg: f.Lines[0], note: overrideNote(f.Lines[1:])}
		if f.Certain {
			errs = append(errs, g)
		} else {
			warns = append(warns, g)
		}
	}
	return errs, warns
}

// overrideNote joins a finding's detail lines into one note. The launch indents them under
// its verdict; the reporter indents a note itself (NoteLines), so the launch's two leading
// spaces are dropped here rather than doubled.
func overrideNote(lines []string) string {
	trimmed := make([]string, 0, len(lines))
	for _, l := range lines {
		trimmed = append(trimmed, strings.TrimSpace(l))
	}
	return strings.Join(trimmed, "\n")
}
