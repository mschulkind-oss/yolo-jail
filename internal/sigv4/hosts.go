package sigv4

import (
	"regexp"
	"strings"
)

// The host patterns decide WHETHER a request is signed for a provider that says nothing about
// what it is (OQ-WG1, ruled 2026-09-25: key on the upstream host, with the provider's Bedrock
// marker as the follow-up once OQ-BR2 gave providers one). Since the follow-up (the bridge's
// bedrockSigning, docs/design/wire-bridge-gateway.md WG-I37), a provider whose `platform` is
// "aws-bedrock" is signed at whatever address it names, and these patterns decide for every
// other provider. A pattern that matched too much would hand an AWS signature — and the
// scope naming the access key — to a host that is not AWS, so both are exact, anchored and
// lower-case only:
//
//   - the argument is a HOSTNAME (url.URL.Hostname()), so "host:port" never matches;
//   - an upper-case spelling never matches, though DNS would resolve it: yolo's own
//     writers emit lower case, and the cost of a miss is AWS's own 403, never a
//     signature sent somewhere unexpected;
//   - an IP literal, a suffix like ".amazonaws.com.evil.example", an extra or missing
//     label, and a FIPS or VPC endpoint (bedrock-runtime-fips…, vpce-…) never match.
//     The last is the known cost of keying on the host, and why the marker re-key was
//     owed: a provider that declares its platform is signed at such an address now.
var (
	bedrockRuntimeHost = regexp.MustCompile(`^bedrock-runtime\.(` + regionPattern + `)\.amazonaws\.com$`)
	agentCoreGateway   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.gateway\.bedrock-agentcore\.(` + regionPattern + `)\.amazonaws\.com$`)
)

// regionPattern is an AWS region's shape: us-east-1, eu-central-1, ap-southeast-2,
// us-gov-west-1, cn-north-1.
const regionPattern = `[a-z]{2}(?:-[a-z]+)+-[0-9]+`

var regionShape = regexp.MustCompile(`^` + regionPattern + `$`)

// RegionVars are the variables an AWS SDK reads its region from, in the order it reads them:
// AWS_REGION, then AWS_DEFAULT_REGION. The bridge's Bedrock arm is an AWS client like codex's
// and Claude Code's own, so it reads its region where they do, beside the credential variables
// EnvVars names (docs/design/wire-bridge-gateway.md WG-I38). The shared-config file's region is
// not read here: the credential gate's region fill delivers it in AWS_REGION
// (docs/design/bedrock-plumbing.md BR-D20).
var RegionVars = []string{"AWS_REGION", "AWS_DEFAULT_REGION"}

// ValidRegion reports whether s has an AWS region's shape. The bridge composes a host from a
// region only when it does, so a variable holding a hostname, a path or a port can never name
// a destination (docs/design/wire-bridge-gateway.md §7).
func ValidRegion(s string) bool {
	return regionShape.MatchString(s)
}

// BedrockRuntimeHost is bedrock-runtime's hostname in region, the one host
// BedrockRuntimeRegion matches for it, and "" for a string that is not a region.
func BedrockRuntimeHost(region string) string {
	if !ValidRegion(region) {
		return ""
	}
	return "bedrock-runtime." + region + ".amazonaws.com"
}

// BedrockService is the SigV4 service name bedrock-runtime signs with.
const BedrockService = "bedrock"

// AgentCoreService is the SigV4 service name an AgentCore gateway signs with.
const AgentCoreService = "bedrock-agentcore"

// BedrockRuntimeRegion reports whether hostname is bedrock-runtime.<region>.amazonaws.com
// and, if so, the region to sign for.
func BedrockRuntimeRegion(hostname string) (string, bool) {
	return matchRegion(bedrockRuntimeHost, hostname)
}

// AgentCoreGatewayRegion reports whether hostname is an AgentCore gateway,
// <gateway-id>.gateway.bedrock-agentcore.<region>.amazonaws.com, and the region. It
// is exported for the web-search signing proxy (docs/design/bedrock-web-search.md,
// OQ-BR21), which signs with this package and is not built yet.
func AgentCoreGatewayRegion(hostname string) (string, bool) {
	return matchRegion(agentCoreGateway, hostname)
}

func matchRegion(re *regexp.Regexp, hostname string) (string, bool) {
	if strings.ContainsAny(hostname, ":[]") {
		return "", false
	}
	m := re.FindStringSubmatch(hostname)
	if m == nil {
		return "", false
	}
	return m[1], true
}
