package check

// awschannels.go predicts the launch's AWS CREDENTIAL-CHANNEL EXCLUSIVITY refusal: a jail
// that would carry both the frozen `AWS_BEARER_TOKEN_BEDROCK` and the pointer at the
// refreshing credential service is refused before it starts
// (docs/design/sso-backed-bedrock.md §8, Forbidden; internal/cli/run/awschannels.go).
//
// It shipped there and only there, so without this file a config `yolo check` called
// clean was still refused at launch — the one thing `check` exists to prevent, and the
// same defect capabilities.go and protocols.go were each written to close.
//
// ⚠ IT FOLLOWS protocols.go, NOT capabilities.go, AND THE DIFFERENCE IS THE POINT.
// capabilities.go is a deliberate SECOND COPY of the launch's census, because reaching
// the real gate would mean exporting a method on `run.Options` and dragging its printer
// in; its ⚠ records what that copy costs. Nothing of the kind applies here.
// `internal/awschain` is already a free package over inputs a caller assembles — that is
// the whole reason it exists — so this file calls `awschain.ExclusivityRefusal`, the same
// function the launch calls, through the same origin phrases (`awschain.From*`). Two
// copies of the rule could disagree; there is only one.
//
// WHAT IS DUPLICATED IS THE INPUT ASSEMBLY, and that is the honest residue — the same
// residue protocols.go names. Composing the delivered environment the way the launch does
// is this file's own code, and getting one source wrong makes the prediction wrong in a
// way no shared function can catch.
//
// ⚠ THREE OF THE LAUNCH'S FIVE DELIVERY CHANNELS ARE VISIBLE FROM HERE, and the prediction
// is deliberately NARROWER than the launch rather than wider. A prediction that is wrong
// in places the launch is not would refuse a config that launches fine, which is worse
// than missing one — so the two it cannot see are stated rather than guessed at:
//
//   - `awschain.FromContainerArgv`. The assembled `-e` argv carries every pack-shipped
//     loophole's `jail_env`, which reaches the jail nowhere else. `check` assembles no
//     argv, and inventing one would mean predicting a mount plan rather than reading a
//     config.
//   - `awschain.FromProfileEnv`. Composing the provider environment runs the env-derive
//     runner (`packload.AgentEnv`), which executes each agent pack's derive. `check` reads
//     configuration; running a pack's code to answer a read-only question is a different
//     act, and protocols.go declines it for its own gate too.
//
// ⚠ AND IT CANNOT SEE `-p`, exactly as protocols.go cannot: a `-p <name>` is an argument
// to a launch that has not happened, and the launch folds it in ABOVE this. So a clean
// prediction means "the `use_profiles` selection delivers at most one channel", never
// "any launch from this config does". `packs/aws-auth`'s pointer is gated on the `bedrock`
// profile, so a `-p bedrock` is precisely the flag that turns a clean config into the
// refused one. Narrowing that needs the flag, not a wider census.
//
// THE OTHER HALF OF §8's Forbidden — the `~/.aws` grant conflict — is deliberately NOT
// here. It is a `config.ValidateConfig` error (internal/config/validate_loopholes.go), so
// `yolo check` already reports it in Merged Configuration and the launch already refuses
// on it, from one rule. Predicting it a second time here would be the two-channel defect
// docs/design/reference-mismatch-diagnostics.md §3 names.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/awschain"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// awsCredentialChannelGap reports what the Packs block should say about the exclusivity
// gate, as (errors, warnings) in this section's own vocabulary.
//
// Two outcomes, mirroring capabilityGap and protocolPairingGap so a reader does not have
// to learn a third report vocabulary:
//
//   - At most one channel delivered → NOTHING, not a PASS line. It matches the launch,
//     which never announces a gate it did not trip, and it keeps the golden that pins
//     section ordering and the pass/warn/fail counts from needing a bump for a line saying
//     nothing happened.
//   - Both delivered → a FAIL carrying the launch's own refusal text verbatim. The launch
//     refusal is fatal and — unlike the credential pre-flight beside it — has NO ESCAPE
//     HATCH, so there is no third "will continue anyway" outcome to report and no warning
//     arm. A prediction of a fatal refusal that exited 0 would be this file's own defect.
//
// configWarn receives the env_sources loader's own findings, GRADED and COUNTED rather
// than printed beside the verdict — the sink rule protocols.go states, and what
// TestEveryConfigWarnSinkIsGraded enforces.
func awsCredentialChannelGap(packs []*packload.Pack, merged *jsonx.OrderedMap,
	workspace string, getenv func(string) string, configWarn func(string)) (errs []string, warns []string) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	// The hydrated secret channel. A dotenv file that cannot be read degrades to
	// "delivered nothing" with a warning on configWarn, which is the loader's own
	// contract; it never changes the verdict, because an unreadable source delivers no
	// variable at launch either.
	userEnv := config.ResolveEnvSources(workspace, merged, configWarn)
	// The pack env fold over the CONFIG's profile table — the `use_profiles` selection,
	// which is the only one a launch-less command has. See the `-p` note above.
	packEnv := packload.EnvVarsFor(packs, packload.ProfileTable(subMap(merged, "use_profiles")))

	lines := awschain.ExclusivityRefusal(func(name string) (string, bool) {
		// The order is the launch's own (run/profilechannel.go's deliverySource), minus
		// the two channels named above. An EMPTY value is unset at every step, exactly as
		// there: the launch drops an empty value rather than composing an empty token.
		if str(userEnv, name) != "" {
			return awschain.FromEnvSources, true
		}
		if packEnv[name] != "" {
			return awschain.FromPackEnv, true
		}
		if getenv(name) != "" {
			return awschain.FromLaunchEnv, true
		}
		return "", false
	})
	return lines, nil
}
