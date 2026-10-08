package wirebridged

// viaconverse.go is the via route's CONVERSE PASS-THROUGH (docs/design/wire-bridge-gateway.md
// WG-I36, WG-I48): Bedrock's own Converse API, signed and otherwise forwarded as sent, for an agent
// whose own Bedrock client is pointed at its via route. That agent is pi on a Bedrock via profile
// (`-p bedrock-bridge`): its models.json row says `api: "bedrock-converse-stream"` with the route as
// its baseUrl and this launch's caller token as its key, so pi's AWS SDK sends an UNSIGNED request
// carrying the token as a bearer, and the bridge signs it.
//
// WHAT PI SENDS THERE (READ in pi 1.0.4 and the AWS SDK 3.1127 it bundles, never measured):
// `POST <baseUrl>/model/{modelId}/converse-stream` (or `/converse`), the model id percent-encoded as
// one path segment (`:` as %3A, an inference-profile ARN's `/` as %2F), the Converse JSON body, which
// names no model, and `Authorization: Bearer <key>`. It reads the answer as AWS's binary
// `application/vnd.amazon.eventstream`. The SDK's default handler speaks HTTP/2 to an http:// address
// with prior knowledge, which is why the via listener also speaks unencrypted HTTP/2 (boot.go).
//
// So the pass-through relays the body, the status and the stream byte for byte and adds the
// credential: the route's SigV4 signer, or a Bedrock API key as `Authorization: Bearer`. The
// destination is composed from the route's own Bedrock upstream, and the model id from the path is
// re-encoded as the SDK encodes it, so nothing in a request can name a new host or a second path.
// ONE REFUSAL before any upstream: a model off the provider's narrowed list (Part 5's allowlist),
// read off the PATH, since the body names none. Every error the bridge itself answers is in the
// shape the AWS SDK reads (writeAWSError).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// converseOps are Converse's two routes, by their last path segment.
var converseOps = map[string]bool{"converse": true, "converse-stream": true}

// conversePassthrough is one via route's Converse upstream: runtime's base (scheme, host and any
// path prefix the provider's address carries), the route's signer, and the agent's allowlist.
type conversePassthrough struct {
	agent, provider      string
	scheme, host, prefix string
	signer               *bedrockSigner
	client               *http.Client
	// allow is the agent's model allowlist (allowlist.go, Part 5), nil when none is in force.
	allow *modelAllowlist
}

// newConversePassthrough is the Converse pass-through beside a Bedrock upstream's OpenAI base URL,
// nil for a base with no https host. The prefix rule is newInvokePassthrough's: runtime serves
// Converse from its root, and a proxy mirroring runtime under a path prefix keeps the prefix.
func newConversePassthrough(agent, provider, openaiBase string, signer *bedrockSigner) *conversePassthrough {
	scheme, host, prefix, ok := converseBase(openaiBase)
	if !ok || scheme != "https" || signer == nil {
		return nil
	}
	return &conversePassthrough{agent: agent, provider: provider, scheme: scheme, host: host,
		prefix: prefix, signer: signer, client: &http.Client{Transport: upstreamTransport}}
}

// converseBase splits a Bedrock upstream's OpenAI base URL into where its Converse routes live:
// its scheme and host, and the path prefix before runtime's /openai/v1 ("" for a base that does
// not end in it, whose Converse is at the host's root). ok is false for a base with no host.
func converseBase(openaiBase string) (scheme, host, prefix string, ok bool) {
	u, err := url.Parse(openaiBase)
	if err != nil || u.Host == "" {
		return "", "", "", false
	}
	if p := strings.TrimSuffix(u.Path, "/"); strings.HasSuffix(p, runtimeOpenAIPath) {
		prefix = strings.TrimSuffix(p, runtimeOpenAIPath)
	}
	return u.Scheme, u.Host, prefix, true
}

// base is the pass-through's upstream, for the serve line and the logs.
func (p *conversePassthrough) base() string {
	return p.scheme + "://" + p.host + p.prefix
}

// parseConversePath reads `/model/{id}/{op}` off the DECODED path the via mux hands its route: the
// model id and the op, which is the LAST segment, so an inference-profile ARN's '/' stays in the
// id whether the agent encoded it or not. ok is false for any other shape, or an id holding a byte
// no model or inference profile id has (validModelID).
func parseConversePath(decoded string) (id, op string, ok bool) {
	rest, found := strings.CutPrefix(decoded, "/model/")
	if !found {
		return "", "", false
	}
	i := strings.LastIndexByte(rest, '/')
	if i < 0 || !converseOps[rest[i+1:]] {
		return "", "", false
	}
	id, op = rest[:i], rest[i+1:]
	if !validModelID(id) {
		return "", "", false
	}
	return id, op, true
}

// upstreamURL is runtime's Converse route for id and op, the id encoded as the AWS SDKs encode it
// (awsEscape), so the path the upstream receives is the one the signature covers.
func (p *conversePassthrough) upstreamURL(id, op string) *url.URL {
	return &url.URL{Scheme: p.scheme, Host: p.host,
		Path:    p.prefix + "/model/" + id + "/" + op,
		RawPath: p.prefix + "/model/" + awsEscape(id) + "/" + op}
}

func (p *conversePassthrough) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, op, ok := parseConversePath(r.URL.Path)
	if !ok {
		writeAWSError(w, http.StatusNotFound, "UnknownOperationException",
			fmt.Sprintf("wire-bridge: %q is not a Converse route the via route for %s passes through — it "+
				"serves POST /model/{model id}/converse and /converse-stream", r.URL.Path, p.agent))
		return
	}
	if r.Method != http.MethodPost {
		writeAWSError(w, http.StatusMethodNotAllowed, "ValidationException",
			"wire-bridge: Bedrock's "+op+" route takes POST")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxViaBody+1))
	if err != nil {
		writeAWSError(w, http.StatusBadRequest, "ValidationException", "wire-bridge: reading the request body: "+err.Error())
		return
	}
	if len(body) > maxViaBody {
		writeAWSError(w, http.StatusRequestEntityTooLarge, "ValidationException",
			fmt.Sprintf("wire-bridge: the request body exceeds %d bytes", maxViaBody))
		return
	}
	// THE ALLOWLIST (Part 5): Converse names its model in the path alone.
	if ok, msg := p.allow.checksModel("via route for "+p.agent, id, false); !ok {
		writeAWSError(w, http.StatusBadRequest, "ValidationException", msg)
		return
	}
	target := p.upstreamURL(id, op)
	resp, err := p.do(r, target, body)
	if err == nil && expiredRejection(resp) {
		// An expired signature or session: the request did not run, so refresh and retry once.
		logf("via route for %s: upstream rejected the credential as expired; refreshing it and retrying once (%s)",
			p.agent, target.Redacted())
		_ = resp.Body.Close()
		p.signer.chain.Invalidate()
		resp, err = p.do(r, target, body)
	}
	if err != nil {
		var ce *credentialError
		switch {
		case errors.As(err, &ce):
			writeAWSError(w, ce.status, "UnrecognizedClientException", ce.message)
		case errors.Is(err, errViaHeaderTimeout):
			logf("via route for %s: upstream %s %s", p.agent, target.Redacted(), err)
			writeAWSError(w, http.StatusGatewayTimeout, "ServiceUnavailableException",
				"wire-bridge: the upstream "+p.base()+" "+err.Error())
		default:
			logf("via route for %s: upstream %s: %v", p.agent, target.Redacted(), err)
			writeAWSError(w, http.StatusBadGateway, "ServiceUnavailableException",
				"wire-bridge: the upstream "+p.base()+" could not be reached: "+err.Error())
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		if invokeResponseHeader(k) {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	copied, cut := relayFlushing(w, flusher, r.Context(), resp.Body)
	if cut == nil {
		return
	}
	// The upstream failed after the status line went out, possibly inside an event-stream
	// message: aborting the connection makes the agent's read fail rather than end (WG-I24).
	logf("via route for %s: upstream %s: the Converse stream for model %s ended early after %d bytes: %v — "+
		"aborting the agent's connection so it sees a failure, not a finished reply",
		p.agent, p.base(), id, copied, cut)
	if r.Context().Value(http.ServerContextKey) != nil {
		panic(http.ErrAbortHandler)
	}
}

// do builds and sends one signed upstream request: the agent's body, the headers
// invokeRequestHeaders keeps, no query, and the credential added last. The inbound Authorization
// (this launch's caller token) is never forwarded.
func (p *conversePassthrough) do(in *http.Request, target *url.URL, body []byte) (*http.Response, error) {
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

// idleAWSHandler answers every Converse request on a via route that cannot be served, naming what
// is missing, in the shape the agent's AWS SDK reads.
type idleAWSHandler struct{ reason string }

func (h idleAWSHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeAWSError(w, http.StatusServiceUnavailable, "ServiceUnavailableException", "wire-bridge: "+h.reason)
}

// converseHandlerFor builds a route's Converse pass-through over its Bedrock upstream, completed by
// bedrockViaUpstream exactly as the route's OpenAI-shaped pass-through is, or the AWS-shaped idle
// handler naming what is missing; and the serve-log description of either.
func converseHandlerFor(rt viaRoute, lookup func(name string) (string, string), where string) (http.Handler, string) {
	b := bedrockViaUpstream(rt, rt.Converse, lookup, where)
	if b.idle != "" {
		return idleAWSHandler{reason: b.idle}, "idle: " + b.idle
	}
	conv := newConversePassthrough(rt.Agent, rt.ProviderName, b.base, b.signer)
	if b.noCredential {
		at := b.base
		if scheme, host, prefix, ok := converseBase(b.base); ok {
			at = scheme + "://" + host + prefix
		}
		reason := noBedrockCredential(rt.Agent, at)
		return idleAWSHandler{reason: reason}, "idle: " + reason
	}
	if conv == nil {
		reason := "the via route for " + rt.Agent + " goes to Bedrock at " + b.base + ", which is not an https " +
			"address, so the bridge composes no Converse route on it. Give provider " + rt.ProviderName +
			" an https address, or drop it and let the bridge compose runtime's from the region"
		return idleAWSHandler{reason: reason}, "idle: " + reason
	}
	return conv, "→ " + conv.base() + " (" + b.desc + ")"
}
