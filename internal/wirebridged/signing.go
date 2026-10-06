package wirebridged

// signing.go is the bridge's Bedrock arm (docs/design/wire-bridge-gateway.md, Part 1):
// an upstream the provider row says is Bedrock gets every request signed with SigV4
// (internal/sigv4) instead of carrying the boot-read bearer key. WHICH upstream that is,
// and for which region, is bedrockroute.go's decision (bedrockSigning, WG-I37 and WG-I38):
// the provider's `platform` marker first, the runtime host for a provider that declares
// no Bedrock platform.
//
// The credential is resolved LAZILY, on the first request, because aws-auth's adapter
// may come up after the bridge; the chain (static key pair, the aws-auth pointer,
// AWS_BEARER_TOKEN_BEDROCK) is read once at boot from the same key channel as every
// other bridge credential, and the container fetch is single-flight and cached until
// five minutes before its Expiration (sigv4.Chain).

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
)

// bedrockSigner is one route's signing state.
type bedrockSigner struct {
	region string
	chain  *sigv4.Chain
	now    func() time.Time
}

// bedrockSignRegion is the region to sign for when the upstream base URL's host is
// bedrock-runtime.<region>.amazonaws.com, "" otherwise (and for any URL that does not
// parse, which is then never signed): the host rule, which decides alone for a provider
// that declares no Bedrock platform (bedrockSigning).
func bedrockSignRegion(upstreamBaseURL string) string {
	u, err := url.Parse(upstreamBaseURL)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	region, ok := sigv4.BedrockRuntimeRegion(u.Hostname())
	if !ok {
		return ""
	}
	return region
}

// hasAWSCredentialSource reports whether any of the chain's three sources is set.
func hasAWSCredentialSource(env sigv4.Env) bool {
	return (env.AccessKeyID != "" && env.SecretAccessKey != "") || env.ContainerURI != "" || env.Bearer != ""
}

// newSignedChatHandler is newChatHandler for a Bedrock upstream: no bearer key, every
// request authorized by signer. anthropicModels are the ids the provider's list declares
// vendor "anthropic" for (route.AnthropicModels): a request for one of them goes untranslated
// to runtime's Messages route under the same signer (messages.go), and every other request is
// translated as before. None leaves the handler translating everything.
func newSignedChatHandler(upstreamBaseURL string, opts wirebridge.ChatOptions, signer *bedrockSigner,
	anthropicModels map[string]bool) *bridgeHandler {
	h := newChatHandler(upstreamBaseURL, "", opts).(*bridgeHandler)
	h.signer = signer
	h.messages = newMessagesPassthrough(upstreamBaseURL, anthropicModels, signer)
	// Bedrock's own invoke routes, for an agent in its own Bedrock mode (invoke.go). The makers
	// the list declares are the boot's to add (route.ModelVendors).
	h.invoke = newInvokePassthrough(upstreamBaseURL, signer)
	return h
}

// credentialError is a failure to obtain the upstream credential, rendered to the agent
// in the Anthropic error shape at a status that says whose problem it is.
type credentialError struct {
	status  int
	typ     string
	message string
}

func (e *credentialError) Error() string { return e.message }

// authorize puts the request's credential on req: a SigV4 signature over body, or a
// bearer when the chain's only source is AWS_BEARER_TOKEN_BEDROCK. Never both.
func (s *bedrockSigner) authorize(req *http.Request, body []byte) error {
	return s.authorizeAs(req, body, "Authorization", "Bearer ")
}

// authorizeAs is authorize with the bearer's header chosen by the route: a Bedrock API key
// travels as `Authorization: Bearer <key>` on runtime's OpenAI-compatible routes and as
// `x-api-key: <key>` on its Anthropic Messages route, each the form AWS documents for that
// route (messages.go, WG-I31). The signature is the same on every route.
func (s *bedrockSigner) authorizeAs(req *http.Request, body []byte, bearerHeader, bearerPrefix string) error {
	resolved, err := s.chain.Resolve(req.Context())
	if err != nil {
		return signingFailure(err)
	}
	if resolved.Bearer != "" {
		req.Header.Set(bearerHeader, bearerPrefix+resolved.Bearer)
		return nil
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	if err := sigv4.Sign(req, body, resolved.Creds, sigv4.Options{
		Region: s.region, Service: sigv4.BedrockService, Time: now()}); err != nil {
		return &credentialError{status: http.StatusInternalServerError, typ: "api_error",
			message: "wire-bridge: could not sign the Bedrock request: " + err.Error()}
	}
	return nil
}

// signingFailure maps a credential-chain error onto what the agent sees. Every message
// names the fix; none carries a credential (sigv4's errors never do).
func signingFailure(err error) error {
	var ee *sigv4.EndpointError
	var ue *sigv4.UnreachableError
	switch {
	case errors.Is(err, sigv4.ErrNoCredential):
		return &credentialError{status: http.StatusUnauthorized, typ: "authentication_error",
			message: "wire-bridge: " + err.Error()}
	case errors.As(err, &ue):
		return &credentialError{status: http.StatusServiceUnavailable, typ: "api_error",
			message: "wire-bridge: the aws-auth credential service did not answer (" + ue.Err.Error() +
				"). Is the aws-auth loophole enabled for this jail? If your SSO session lapsed, run " +
				"`aws sso login --profile <your profile>` on the host; the next request picks it up."}
	case errors.As(err, &ee):
		// aws-auth's own refusal (a lapsed session names `aws sso login --profile …`)
		// is the sentence the human needs, so it is forwarded as is.
		return &credentialError{status: http.StatusUnauthorized, typ: "authentication_error",
			message: "wire-bridge: " + ee.Error()}
	}
	return &credentialError{status: http.StatusServiceUnavailable, typ: "api_error",
		message: "wire-bridge: resolving the Bedrock credential: " + err.Error()}
}

// expiredRejection reports whether resp is AWS refusing the request because the
// signature or the session token it carried has expired: a 403 whose error type or
// message says so. Such a request did not run, so the handler refreshes the credential
// and retries once (docs/design/wire-bridge-gateway.md §2.1). The body is buffered and
// put back, so a response that is NOT an expiry is relayed untouched.
func expiredRejection(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	evidence := resp.Header.Get("X-Amzn-ErrorType") + " " + string(body)
	for _, marker := range []string{"ExpiredToken", "Signature expired",
		"security token included in the request is expired"} {
		if strings.Contains(evidence, marker) {
			return true
		}
	}
	return false
}
