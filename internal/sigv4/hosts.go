package sigv4

import (
	"regexp"
	"strings"
)

// The host patterns decide WHETHER a request is signed at all (OQ-WG1, ruled
// 2026-09-25: key on the upstream host, never on a provider's Bedrock marker, with
// the marker as a follow-up once OQ-BR2 gives providers one). A pattern that matched
// too much would hand an AWS signature — and the scope naming the access key — to a
// host that is not AWS, so both are exact, anchored and lower-case only:
//
//   - the argument is a HOSTNAME (url.URL.Hostname()), so "host:port" never matches;
//   - an upper-case spelling never matches, though DNS would resolve it: yolo's own
//     writers emit lower case, and the cost of a miss is AWS's own 403, never a
//     signature sent somewhere unexpected;
//   - an IP literal, a suffix like ".amazonaws.com.evil.example", an extra or missing
//     label, and a FIPS or VPC endpoint (bedrock-runtime-fips…, vpce-…) never match.
//     The last is the known cost of keying on the host, and why the marker re-key is
//     owed.
var (
	bedrockRuntimeHost = regexp.MustCompile(`^bedrock-runtime\.(` + regionPattern + `)\.amazonaws\.com$`)
	agentCoreGateway   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.gateway\.bedrock-agentcore\.(` + regionPattern + `)\.amazonaws\.com$`)
)

// regionPattern is an AWS region's shape: us-east-1, eu-central-1, ap-southeast-2,
// us-gov-west-1, cn-north-1.
const regionPattern = `[a-z]{2}(?:-[a-z]+)+-[0-9]+`

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
