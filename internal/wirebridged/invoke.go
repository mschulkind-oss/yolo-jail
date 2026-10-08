package wirebridged

// invoke.go is the adapter route's INVOKE PASS-THROUGH for a Bedrock upstream: bedrock-runtime's
// own InvokeModel routes, signed and otherwise forwarded as sent, for an agent running in its own
// Bedrock mode pointed at the bridge. That agent is claude on `-p bedrock-bridge`
// (docs/design/model-lists-and-pickers.md OQ-MM6, ruled 2026-10-05, amending wire-bridge-gateway.md
// Part 2's "never CLAUDE_CODE_USE_BEDROCK"): Claude Code's documented gateway settings
// (CLAUDE_CODE_USE_BEDROCK=1, ANTHROPIC_BEDROCK_BASE_URL at this route, CLAUDE_CODE_SKIP_BEDROCK_AUTH=1,
// its gateway token in ANTHROPIC_AUTH_TOKEN) make it send Bedrock's own requests unsigned, so its
// own Bedrock defaults name the model and the bridge signs them, translating another maker's
// model (invoketranslate.go).
//
// WHAT CLAUDE CODE SENDS THERE (SOURCED 2026-10-05 from Claude Code's gateway compatibility guide,
// https://code.claude.com/docs/en/llm-gateway-protocol, and read in the 2.1.290 binary, never run):
// `POST /model/{model}/invoke`, `/model/{model}/invoke-with-response-stream` and, optionally,
// `/model/{model}/count-tokens`, the Anthropic body with `anthropic_version` and `anthropic_beta`
// in it, and a streamed answer it reads as AWS's binary `application/vnd.amazon.eventstream`,
// unmodified. It also sends best-effort control-plane reads, `GET /inference-profiles?…` and
// `GET /inference-profiles/{profile}`, which a gateway may refuse: Claude Code then falls back on
// its built-in Bedrock model ids. The bridge refuses them, since a jail's credential is
// invoke-only and the list the launch fetched is not that API's shape.
//
// So the pass-through relays the body, the status and the stream byte for byte, and adds the
// credential: the route's SigV4 signer (signing.go), or a Bedrock API key as
// `Authorization: Bearer`, the form AWS documents for runtime. The destination is composed from the
// provider row's own bedrock-runtime host, and the model id from the path is re-encoded segment by
// segment, so nothing in a request can name a new host or a second path (§7).
//
// ONE REFUSAL, before any upstream: a model off the provider's narrowed list (Part 5's allowlist,
// as on the Messages route). And ONE TRANSLATION: a model the list declares another maker's. This
// route's pass-through forwards Anthropic's request format unchanged, which only an Anthropic model
// takes, so a GPT or Llama id is translated instead, exactly as /v1/messages translates it
// (invoketranslate.go): that keeps claude's everything profile, every model on the list in one
// session (docs/design/bedrock-plumbing.md OQ-BR11), on its own Bedrock mode. A model the list does
// not name is forwarded: the bridge only signs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// invokeOps are the InvokeModel routes the pass-through serves, by their last path segment.
var invokeOps = map[string]bool{"invoke": true, "invoke-with-response-stream": true, "count-tokens": true}

// invokePassthrough is one Bedrock route's InvokeModel upstream: runtime's base (scheme, host and
// any path prefix the provider's address carries), the route's signer, and what the list says
// about each model's maker.
type invokePassthrough struct {
	scheme, host, prefix string
	vendors              map[string]string
	signer               *bedrockSigner
	client               *http.Client
	// translate serves a model the list declares another maker's (invoketranslate.go), through
	// the route's own Messages translation: bridgeHandler.serveInvokeTranslated.
	translate func(rec *statusRecorder, in *http.Request, id, op, vendor string, note *string)
}

// newInvokePassthrough is the pass-through beside a Bedrock upstream base URL, nil for a route
// with no signer or a base with no https host. The prefix rule is bedrockMessagesURL's: runtime
// serves its routes from its root, and a proxy that mirrors it under a path prefix keeps the
// prefix for all of them.
func newInvokePassthrough(upstreamBaseURL string, signer *bedrockSigner) *invokePassthrough {
	u, err := url.Parse(upstreamBaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || signer == nil {
		return nil
	}
	prefix := ""
	if p := strings.TrimSuffix(u.Path, "/"); strings.HasSuffix(p, runtimeOpenAIPath) {
		prefix = strings.TrimSuffix(p, runtimeOpenAIPath)
	}
	return &invokePassthrough{scheme: u.Scheme, host: u.Host, prefix: prefix, signer: signer,
		client: &http.Client{Transport: upstreamTransport}}
}

// parseInvokePath reads `/model/{id}/{op}` off a request's escaped path: the model id, decoded,
// and the op. ok is false for any other shape, an op the pass-through does not serve, or an id
// holding a byte no model or inference profile id has.
func parseInvokePath(escaped string) (id, op string, ok bool) {
	parts := strings.Split(escaped, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "model" || !invokeOps[parts[3]] {
		return "", "", false
	}
	id, err := url.PathUnescape(parts[2])
	if err != nil || !validModelID(id) {
		return "", "", false
	}
	return id, parts[3], true
}

// validModelID reports whether a decoded model id from a Bedrock route's path holds only bytes a
// model or inference profile id has (an ARN's `:` and `/` among them), and is neither empty nor
// longer than any id AWS mints. Both of Bedrock's own pass-throughs ask it before the id is
// re-encoded into the upstream path: InvokeModel's here and the via route's Converse
// (viaconverse.go).
func validModelID(id string) bool {
	if id == "" || len(id) > 2048 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune(".-_:/[]", r)) {
			return false
		}
	}
	return true
}

// awsEscape percent-encodes every byte of s outside RFC 3986's unreserved set, as the AWS SDKs
// encode a model id in its path segment (`:` becomes %3A, an ARN's `/` %2F), so the path the
// upstream receives is the one the signature covers.
func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// upstreamURL is runtime's route for id and op.
func (p *invokePassthrough) upstreamURL(id, op string) *url.URL {
	return &url.URL{Scheme: p.scheme, Host: p.host,
		Path:    p.prefix + "/model/" + id + "/" + op,
		RawPath: p.prefix + "/model/" + awsEscape(id) + "/" + op}
}

// invokeRequestHeaders are the inbound headers the pass-through forwards: the body's type, what
// the agent accepts, and Bedrock's own request options (`X-Amzn-Bedrock-*`: a service tier, a
// guardrail, a trace). Never an inbound credential: those carry this launch's caller token.
func invokeRequestHeaders(dst, src http.Header) {
	for _, k := range []string{"Content-Type", "Accept"} {
		if v := src.Get(k); v != "" {
			dst.Set(k, v)
		}
	}
	for k, vs := range src {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "X-Amzn-Bedrock-") {
			for _, v := range vs {
				dst.Add(k, v)
			}
		}
	}
}

// invokeResponseHeader reports whether an upstream response header is relayed: the body's type,
// the retry hints, the request id and Bedrock's own response facts (token counts, latency).
func invokeResponseHeader(k string) bool {
	k = http.CanonicalHeaderKey(k)
	switch k {
	case "Content-Type", "Retry-After", "X-Amzn-Requestid", "X-Amzn-Errortype", "X-Should-Retry":
		return true
	}
	return strings.HasPrefix(k, "X-Amzn-Bedrock-")
}

// writeAWSError renders one of the bridge's own refusals on a Bedrock route in the shape an AWS
// SDK reads: a JSON `message` and the error's type in X-Amzn-Errortype. The adapter route's invoke
// pass-through writes it, and so does the via route's Converse (viaconverse.go).
func writeAWSError(rec http.ResponseWriter, status int, typ, msg string) {
	rec.Header().Set("Content-Type", "application/json")
	rec.Header().Set("X-Amzn-Errortype", typ)
	rec.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"message": msg})
	_, _ = rec.Write(body)
}

// serve forwards one InvokeModel request and relays the answer. note is the request line's
// suffix.
func (p *invokePassthrough) serve(rec *statusRecorder, in *http.Request, id, op string,
	allow *modelAllowlist, note *string) {
	*note = fmt.Sprintf(" (model %s: signed and passed through to Bedrock's %s)", id, op)
	if in.Method != http.MethodPost {
		writeAWSError(rec, http.StatusMethodNotAllowed, "ValidationException",
			"wire-bridge: Bedrock's "+op+" route takes POST")
		return
	}
	if ok, msg := allow.checksModel("the adapter route", id, false); !ok {
		*note = " (model refused: off the provider's list)"
		writeAWSError(rec, http.StatusBadRequest, "ValidationException", msg)
		return
	}
	if vendor := p.vendors[strings.TrimSuffix(id, oneMillionSuffix)]; vendor != "" && vendor != anthropicVendor {
		p.translate(rec, in, id, op, vendor, note)
		return
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		writeAWSError(rec, http.StatusBadRequest, "ValidationException", "wire-bridge: reading request body: "+err.Error())
		return
	}
	target := p.upstreamURL(id, op)
	resp, err := p.do(in, target, body)
	if err == nil && expiredRejection(resp) {
		logf("upstream rejected the credential as expired; refreshing it and retrying once (%s)", target.Redacted())
		_ = resp.Body.Close()
		p.signer.chain.Invalidate()
		resp, err = p.do(in, target, body)
	}
	if err != nil {
		var ce *credentialError
		switch {
		case errors.As(err, &ce):
			writeAWSError(rec, ce.status, "UnrecognizedClientException", ce.message)
		case errors.Is(err, errViaHeaderTimeout):
			logf("upstream %s %s", target.Redacted(), err)
			writeAWSError(rec, http.StatusGatewayTimeout, "ServiceUnavailableException", "wire-bridge: the upstream "+err.Error())
		default:
			logf("upstream %s: %v", target.Redacted(), err)
			writeAWSError(rec, http.StatusBadGateway, "ServiceUnavailableException", "wire-bridge: upstream unavailable: "+err.Error())
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		if invokeResponseHeader(k) {
			for _, v := range vs {
				rec.Header().Add(k, v)
			}
		}
	}
	rec.WriteHeader(resp.StatusCode)
	if p.relay(rec, in, resp, id) {
		*note += "; the stream was cut short and the agent's connection aborted"
		if in.Context().Value(http.ServerContextKey) != nil {
			panic(http.ErrAbortHandler)
		}
	}
}

// do builds and sends one signed upstream request.
func (p *invokePassthrough) do(in *http.Request, target *url.URL, body []byte) (*http.Response, error) {
	return sendHeaderBounded(in.Context(), p.client, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build upstream request: %w", err)
		}
		invokeRequestHeaders(req.Header, in.Header)
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		// LAST, because SigV4 signs the headers present (sigv4.Sign).
		if err := p.signer.authorize(req, body); err != nil {
			return nil, err
		}
		return req, nil
	})
}

// relay copies the upstream's body to the agent chunk by chunk, flushing each, and reports
// whether the agent must be told the response failed: a body read that fails before the end is
// a reply the agent would otherwise take as finished, and the cut may fall inside an event-stream
// message, so the connection is aborted (WG-I33's answer). The agent closing its own request is
// no fault.
func (p *invokePassthrough) relay(rec *statusRecorder, in *http.Request, resp *http.Response, id string) bool {
	flusher, _ := rec.ResponseWriter.(http.Flusher)
	copied, cut := relayFlushing(rec, flusher, in.Context(), resp.Body)
	if cut == nil {
		return false
	}
	logf("upstream: the Bedrock answer for model %s ended early after %d bytes: %v — aborting the "+
		"agent's connection so it sees a failure, not a finished reply", id, copied, cut)
	return true
}

// relayFlushing copies body to w chunk by chunk, flushing each, and returns how many bytes it
// copied and, when the body's read failed before its end while the agent was still listening, that
// failure: the cut the caller answers by aborting the agent's connection. A write failure (the
// agent went away) and the agent's own close are no cut. Both Bedrock pass-throughs relay through
// it: InvokeModel's and the via route's Converse (viaconverse.go).
func relayFlushing(w io.Writer, flusher http.Flusher, ctx context.Context, body io.Reader) (int64, error) {
	buf := make([]byte, 32<<10)
	var copied int64
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			copied += int64(n)
			if _, werr := w.Write(buf[:n]); werr != nil {
				return copied, nil
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr == nil {
			continue
		}
		if errors.Is(rerr, io.EOF) || ctx.Err() != nil {
			return copied, nil
		}
		return copied, rerr
	}
}

// invokeServeNote is the adapter route's serve-line suffix naming its invoke pass-through, "" when
// the route has none.
func invokeServeNote(h http.Handler) string {
	bh, _ := h.(*bridgeHandler)
	if bh == nil || bh.invoke == nil {
		return ""
	}
	return fmt.Sprintf("; Bedrock's own POST /model/{id}/invoke routes are signed and passed through to %s://%s%s, "+
		"a model the list declares another maker's translated", bh.invoke.scheme, bh.invoke.host, bh.invoke.prefix)
}
