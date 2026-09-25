package run

// envoverrides.go is the NINTH bespoke launch pre-flight: a jail that would carry a
// pack's env contribution AND something that pack declares overrides it is refused before
// it starts (packdecl.EnvOverride; docs/design/sso-backed-bedrock.md, OQ-SSO8).
//
// The rule and its wording are packload.EnvOverrideRefusal's, not this file's, because
// `yolo check` predicts the same refusal (internal/cli/check/envoverrides.go) and the two
// must not word one problem two ways. What lives here is the INPUT ASSEMBLY: what
// "delivered" means to a launch — the composed channel the credential pre-flight beside it
// already consults, plus the assembled argv on the container arm, minus the environment
// yolo was launched from (jailOriginLookup says why) — and which host_files destinations
// the launch would render on this backend.
//
// ⚠ NO VARIABLE, PACK OR LOOPHOLE IS NAMED HERE, and that is the ruling rather than a
// style preference. This pre-flight began as the AWS exclusivity check, keyed on the
// Bedrock bearer and the container-credentials pointer by name in core
// (internal/awschain, deleted); OQ-SSO8 moved every fact about which variable beats which
// into the pack that ships the channel. packs/aws-auth declares its three overrides; this
// file evaluates whatever any selected pack declares.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// checkEnvOverrides returns the refusal lines, or nil. A caller that gets lines MUST stop:
// unlike checkProviderCredentials this pre-flight has no escape hatch, so there is no
// verdict to carry back beside the lines (packload.EnvOverrideRefusal says why a hatch
// would be wrong here).
//
// rt is the backend the launch runs on, which decides whether a DIRECTORY host_files grant
// renders anything at all (hostFileDirsDeliver). argvPairs is the `-e K=V` map of the
// assembled container argv, or nil off the container — the same argument, for the same
// reason, as the pre-flight beside it: a pack-shipped loophole's jail_env can put a
// variable on the argv that the composed channel alone does not know about.
func (o *Options) checkEnvOverrides(cfg *jsonx.OrderedMap, rt string, packs []*packload.Pack,
	channel *packChannel, argvPairs map[string]string) []string {
	if channel == nil {
		return nil
	}
	return packload.EnvOverrideRefusal(packs, packload.ProfileTable(channel.profiles),
		channel.jailOriginLookup(o, argvPairs),
		config.RenderedHostFilePaths(cfg, o.hostFileDirsDeliver(rt)))
}

// jailOriginLookup is deliverySource narrowed to what REACHES THE JAIL under the asked
// name, which is the only thing an override can be: a variable the agent never sees
// overrides nothing, and refusing over it is the false positive OQ-SSO8 condition 3
// forbids.
//
// So it drops deliverySource's last source, the environment yolo itself was launched
// from. No backend forwards that environment: the container gets explicit `-e K=V` pairs
// and yolo-user-env.sh (env_sources plus the channel section, writeUserEnvFile), and the
// macos-user sandbox starts under `env -i` with a closed list (macosuser.LaunchArgv). The
// one way a launch-environment value does cross is a derive's relay, and a relayed value is
// in shapeVars, where deliverySource answers FromProfileEnv before it ever reaches the
// launch-environment arm.
//
// The narrowing applies to `unless` as much as to `vars`, and that half matters in the
// other direction: an AWS_PROFILE exported only in the host shell would otherwise make a
// static pair delivered through env_sources step aside, while the jail gets the pair and
// no AWS_PROFILE — the silent wrong answer the rule exists to catch.
//
// The credential pre-flight keeps the full lookup (deliveryLookup), because there the
// launch environment IS an input: the relay can draw a credential from it.
func (c *packChannel) jailOriginLookup(o *Options, argvPairs map[string]string) packload.OriginLookup {
	return func(name string) (string, bool) {
		_, origin, ok := c.deliverySource(o, argvPairs, name)
		if !ok || origin == packload.FromLaunchEnv {
			return "", false
		}
		return origin, true
	}
}

// hostFileDirsDeliver reports whether a source-bearing DIRECTORY host_files entry renders
// anything on backend rt. It restates the two mount-side decisions rather than calling
// them, because both are made inside the loops that build the delivery:
//
//   - macos-user never delivers one: buildMacosCtxTree returns it in undeliveredDirs and
//     the launch names it as not crossing (DP-D15 — a copy does not scale to a tree);
//   - Apple Container below acROBindsFloor declines one: hostUserFileArgs skips the bind
//     when roBindsUnsupported says `:ro` would be ignored, and the entrypoint then finds
//     nothing at /ctx/host-user/<slug> and writes nothing.
//
// podman binds it whenever the source exists, which config.RenderedHostFilePaths checks.
// Counting a directory grant on a backend that drops it would refuse a launch over a
// ~/.aws the jail never gets.
func (o *Options) hostFileDirsDeliver(rt string) bool {
	if rt == "macos-user" { // parity: Warned — this mirrors macos-user dropping a directory host_files entry, which noteMacosUserHostByteGaps names at launch
		return false
	}
	return o.roBindsUnsupported(rt) == ""
}
