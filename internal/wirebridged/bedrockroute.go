package wirebridged

// bedrockroute.go decides which upstreams are Bedrock's, and composes the one a Bedrock
// provider does not name (docs/design/wire-bridge-gateway.md §2.1, §8 step 1).
//
// WHICH UPSTREAM IS SIGNED (bedrockSigning, WG-I37). OQ-WG1 ruled on 2026-09-25 that the
// signer key on the upstream HOST now and re-key on the provider's Bedrock marker once OQ-BR2
// gave providers one: "A is cheating and I have no idea what other people's configurations
// are". OQ-BR2's marker is the provider's `platform`, built 2026-09-29. So a provider whose
// platform is "aws-bedrock" is signed at whatever address it names (a FIPS or VPC endpoint, or
// a corporate proxy, none of which the host pattern matches), and a provider that declares no
// such platform keeps the host rule: signed exactly when its upstream is
// bedrock-runtime.<region>.amazonaws.com. Only over https, whichever rule decides.
//
// THE REGION (WG-I38) is the runtime host's when the upstream is that host, because that is
// the region the signature's scope must name; else the provider's own `region`; else the
// region the launch delivers to the served agent, AWS_REGION then AWS_DEFAULT_REGION
// (sigv4.RegionVars), read at boot from the key channel the credential comes from. The
// credential gate's region fill puts the host's ~/.aws/config region in AWS_REGION
// (docs/design/bedrock-plumbing.md BR-D20), so that region is read too. A value is used only
// when it has a region's shape (sigv4.ValidRegion), so nothing a variable holds can name a
// host (§7).
//
// THE REGION-COMPOSED UPSTREAM (regionalBedrock, WG-I39). A Bedrock provider that names no
// address — the shipped `bedrock`, whose pack cannot know the region — is reached at
// runtime's own OpenAI-compatible base, https://bedrock-runtime.<region>.amazonaws.com/openai/v1,
// for both wires a via route passes through and for the adapter route's chat-completions
// upstream, beside which Part 2's Messages route sits on the same host (messages.go).

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// bedrockPlatform is the provider `platform` (OQ-BR2's marker) the bridge signs for and, when
// the provider names no address, reaches at runtime's own URL. The vocabulary is open and an
// unknown value is inert: a provider declaring any other platform is decided by the host rule,
// as one declaring none is.
const bedrockPlatform = "aws-bedrock"

// FrontedPlatforms is every provider platform this daemon reaches with no address of the
// provider's own. packs/wire-bridge's chat-completions adapter declares the same list under
// `from_platforms`, which is what composes the adapter's address onto such a provider
// (packload.adaptEndpoints); TestTheAdapterFrontsExactlyThePlatformsTheDaemonReaches keeps the
// declaration and this implementation one list.
var FrontedPlatforms = []string{bedrockPlatform}

// runtimeOpenAIPath is bedrock-runtime's OpenAI-compatible base path: chat-completions at
// <base>/chat/completions and Responses at <base>/responses, the base codex's own
// amazon-bedrock-runtime client uses (packs/codex/derive.lua) and the one a user provider
// points `endpoints.openai.base_url` at.
const runtimeOpenAIPath = "/openai/v1"

// signing is bedrockSigning's answer for one upstream: whether it is signed, and for which
// region when the tables alone say.
type signing struct {
	// Region is the region to sign for, known now: the runtime host's, else the provider's
	// own `region`.
	Region string
	// FromEnv is set when the upstream is signed and its region is the served agent's, read at
	// boot (envRegion).
	FromEnv bool
	// Refusal is why a provider that says it is Bedrock cannot be signed at the address it
	// names (plain http, or a `region` that is not one), "" otherwise. Such a route is not
	// served, since its upstream would reject every request unsigned.
	Refusal string
}

// signs reports whether the upstream is signed at all.
func (s signing) signs() bool { return s.Region != "" || s.FromEnv }

// bedrockSigning is THE decision whether an upstream of provider entry is signed (WG-I37), and
// for which region (WG-I38). It is pure over the composed table, so the launcher's WillServe,
// its via-route gate and the daemon's boot answer it the same.
func bedrockSigning(entry *jsonx.OrderedMap, upstreamBaseURL string) signing {
	if region := bedrockSignRegion(upstreamBaseURL); region != "" {
		return signing{Region: region}
	}
	if entryString(entry, "", "platform") != bedrockPlatform {
		return signing{}
	}
	u, err := url.Parse(upstreamBaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return signing{Refusal: fmt.Sprintf("it is Bedrock (platform %q) at %s, and the bridge signs a "+
			"Bedrock request only over https", bedrockPlatform, upstreamBaseURL)}
	}
	return entryRegionSigning(entry)
}

// entryRegionSigning is a Bedrock provider's region when its address names none: its own
// `region`, else the served agent's at boot.
func entryRegionSigning(entry *jsonx.OrderedMap) signing {
	switch region := entryString(entry, "", "region"); {
	case region == "":
		return signing{FromEnv: true}
	case !sigv4.ValidRegion(region):
		return signing{Refusal: fmt.Sprintf("its region %q is not an AWS region, so the bridge composes "+
			"no Bedrock host from it and signs for none", region)}
	default:
		return signing{Region: region}
	}
}

// regionalBedrock reports whether the bridge reaches provider entry at bedrock-runtime's own
// URL, composed from the region (WG-I39): its platform is Bedrock, it names no OpenAI endpoint
// of either wire, and its only anthropic endpoint, if any, is the one this service's adapter
// composed for a via profile (forViaEndpoint). A provider that names an anthropic address of its
// own is somebody else's route, and is left to it.
func regionalBedrock(entry *jsonx.OrderedMap) bool {
	if entryString(entry, "", "platform") != bedrockPlatform {
		return false
	}
	if endpointBaseURL(entry, "openai") != "" || endpointBaseURL(entry, wireAPIResponses) != "" {
		return false
	}
	return endpointBaseURL(entry, "anthropic") == "" || forViaEndpoint(entry, "anthropic")
}

// forViaEndpoint reports whether entry's endpoint for protocol is the address this service's
// adapter composed onto a provider that names none of its own (packload.ForViaKey, WG-I39):
// one an agent is sent to only under a profile that routes through this service.
func forViaEndpoint(entry *jsonx.OrderedMap, protocol string) bool {
	return endpointField(entry, protocol, packload.ForViaKey) == ServiceName
}

// runtimeBaseURL is bedrock-runtime's OpenAI-compatible base in region, "" for a string that
// is not a region.
func runtimeBaseURL(region string) string {
	host := sigv4.BedrockRuntimeHost(region)
	if host == "" {
		return ""
	}
	return "https://" + host + runtimeOpenAIPath
}

// envRegion is the served agent's region, read at boot through lookup (the key channel, which
// answers a value and where it came from): the first of sigv4.RegionVars that is set, with its
// source for the serve line, or why no region can be used. A set variable that is not a region
// is refused by name rather than skipped: skipping it would sign for a region the agent itself
// would not read first.
func envRegion(lookup func(string) (string, string)) (region, source, why string) {
	for _, name := range sigv4.RegionVars {
		v, from := lookup(name)
		if v == "" {
			continue
		}
		if !sigv4.ValidRegion(v) {
			return "", "", fmt.Sprintf("$%s (from %s) is %q, which is not an AWS region, so the bridge "+
				"composes no Bedrock host from it and signs for none", name, from, v)
		}
		return v, "$" + name + " from " + from, ""
	}
	names := make([]string, len(sigv4.RegionVars))
	for i, n := range sigv4.RegionVars {
		names[i] = "$" + n
	}
	return "", "", "it has no region: the provider declares no `region`, and neither " +
		strings.Join(names, " nor ") + " is set"
}

// signingDescription is the serve line's credential phrase for a Bedrock upstream, naming where
// an environment region came from.
func signingDescription(region, regionSource string) string {
	if regionSource == "" {
		return "SigV4 for bedrock in " + region
	}
	return "SigV4 for bedrock in " + region + " (" + regionSource + ")"
}

// resolveRegion completes a Bedrock route whose region is the served agent's (RegionFromEnv)
// at boot, from lookup (the key channel, which where names for the idle line): the signing
// region, where it came from, and runtime's URL in it for a region-composed upstream. A route
// whose region the tables already gave is returned as it is. why is the idle reason when no
// region can be used: the bridge never signs for a region nobody chose.
func (r route) resolveRegion(lookup func(string) (string, string), where string) (route, string) {
	if !r.RegionFromEnv || r.SignRegion != "" {
		return r, ""
	}
	region, source, why := envRegion(lookup)
	if why != "" {
		return r, fmt.Sprintf("provider %q's upstream is Bedrock, and %s in %s", r.ProviderName, why, where)
	}
	r.SignRegion, r.RegionSource = region, source
	if r.RegionalUpstream {
		r.UpstreamBaseURL = runtimeBaseURL(region)
	}
	return r, ""
}
