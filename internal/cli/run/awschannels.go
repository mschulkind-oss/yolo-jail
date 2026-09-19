package run

// awschannels.go is the NINTH bespoke launch pre-flight: a jail that would carry both
// AWS credential arms at once is refused before it starts.
//
// The rule and its wording are internal/awschain's, not this file's, for the reason
// that package's doc gives — `yolo check` predicts the same refusal
// (internal/cli/check/awschannels.go) and the two must not word one problem two ways.
// What lives here is the INPUT ASSEMBLY: what "delivered" means to a launch, which is
// exactly the composed channel the credential pre-flight beside it already consults.
//
// ⚠ NO SWITCH ON A LOOPHOLE OR PACK NAME, and that is a constraint rather than a
// style preference (docs/design/sso-backed-bedrock-plan.md, Blockers 6). The predicate
// is "does this launch DELIVER the container-credentials pointer", so it is true of
// packs/aws-auth's profile-gated `kind: "env"` contribution, of a user who writes the
// variable into env_sources by hand, and of whatever ships the same channel next —
// and false when the loophole is enabled but the `bedrock` profile is not selected,
// which is right: with no pointer the SDK never reaches the container provider, so
// there is no silent winner to refuse over.

import "github.com/mschulkind-oss/yolo-jail/internal/awschain"

// checkAWSCredentialChannels returns the refusal lines, or nil. A caller that gets
// lines MUST stop: unlike checkProviderCredentials this pre-flight has no escape hatch,
// so there is no verdict to carry back beside the lines (awschain.ExclusivityRefusal
// says why the hatch would be wrong here).
//
// argvPairs is the `-e K=V` map of the assembled container argv, or nil off the
// container — the same argument, for the same reason, as the pre-flight beside it: a
// pack-shipped loophole's jail_env can put a variable on the argv that the composed
// channel alone does not know about.
func (o *Options) checkAWSCredentialChannels(channel *packChannel,
	argvPairs map[string]string) []string {
	if channel == nil {
		return nil
	}
	return awschain.ExclusivityRefusal(func(name string) (string, bool) {
		_, origin, ok := channel.deliverySource(o, argvPairs, name)
		return origin, ok
	})
}
