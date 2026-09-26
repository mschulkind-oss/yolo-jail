package wirebridged

// via.go is the bridge's VIA side (docs/design/wire-bridge-gateway.md OQ-WG6/WG7, Part 3):
// an agent whose active profile says `via: "wire-bridge"` points its OpenAI base URL at
// its own path on one declared address, <via_address>/agent/<agent>, and this daemon
// forwards what it sends there, UNCHANGED, to that profile's provider's own endpoint. It
// adds the upstream credential and nothing else: a SigV4 signature for an exact
// bedrock-runtime host (the signer, OQ-WG1), the provider's key otherwise. No
// translation, no body rewrite, no model remapping — the sign-only route.
//
// TWO WIRES, ONE ROUTE (WG-I20): a provider may offer OpenAI chat-completions, OpenAI
// Responses, or both, and the agent's own client decides which it speaks — pi, oh-omp
// and opencode send /chat/completions, codex sends /responses. So a route carries up to
// two upstreams read off the provider's declared endpoints, and each request goes to the
// one its path names. Nothing in a request can name a destination the provider row did
// not declare; the path only picks between the two it did.
//
// One daemon, several routes (WG7): the adapter routes keep the root of their own ports
// (claude's ANTHROPIC_BASE_URL is unchanged), and every via route shares the via
// address, told apart by the /agent/<name>/ prefix. A request with no known prefix is
// refused, never routed to a default (WG4), and a route with no credential answers with
// an error naming what is missing while every other route serves (WG7 (e)).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// viaRoute is one agent's pass-through route: its provider, the key that provider
// declares, and the provider's two OpenAI-shaped upstreams. Either upstream may be empty
// (the provider declares no endpoint speaking that wire), never both.
type viaRoute struct {
	Agent        string
	ProviderName string
	KeyEnvName   string
	// Chat is the provider's chat-completions endpoint: its `openai` endpoint when that
	// declares no wire_api or openai-chat-completions.
	Chat viaUpstream
	// Responses is the provider's OpenAI Responses endpoint: its `openai-responses`
	// endpoint when it declares one, else its `openai` endpoint when that declares no
	// wire_api or openai-responses — the same preference packs/codex/derive.lua reads,
	// so codex and the route agree on which address is the provider's Responses one.
	Responses viaUpstream
}

// viaUpstream is one upstream base URL and, for an exact bedrock-runtime host, the
// region its requests are signed for (sigv4.BedrockRuntimeRegion; "" means a bearer).
type viaUpstream struct {
	BaseURL    string
	SignRegion string
}

func newViaUpstream(base string) viaUpstream {
	if base == "" {
		return viaUpstream{}
	}
	return viaUpstream{BaseURL: base, SignRegion: bedrockSignRegion(base)}
}

// viaPlan is every via route this boot serves, on one listen address.
type viaPlan struct {
	ListenAddr string
	Routes     []viaRoute
	// Skipped is one reason per via profile that cannot be served (named in the idle
	// line when nothing serves, and logged when something else does).
	Skipped []string
}

// maxViaBody bounds a buffered request body. The body has to be read whole to be signed
// (SigV4 hashes it) and to be replayed on an expired-signature retry.
const maxViaBody = 64 << 20

// viaRoutesFor is the via half of the serve decision: pure over the same three tables
// routeFor reads, so the launcher's WillServe and the daemon's boot cannot disagree. A
// profile is a via route when its resolved `via` is THIS service and the launch resolved
// the service's address (ViaBase); its upstreams are the provider's own chat-completions
// and Responses endpoints (viaUpstreams), and a provider with neither is skipped.
func viaRoutesFor(providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile) viaPlan {
	agents := make([]string, 0, len(useProfiles))
	for agent := range useProfiles {
		agents = append(agents, agent)
	}
	sort.Strings(agents)

	var plan viaPlan
	for _, agent := range agents {
		name := useProfiles[agent]
		r, ok := resolved[name]
		if !ok || r.Via != ServiceName {
			continue
		}
		if r.ViaBase == "" {
			plan.Skipped = append(plan.Skipped, "profile "+name+" (active for "+agent+") names via "+
				ServiceName+", but this launch resolved no via_address for it")
			continue
		}
		addr, local := loopbackListenAddr(r.ViaBase)
		if !local {
			plan.Skipped = append(plan.Skipped, "profile "+name+"'s via address ("+r.ViaBase+
				") is not jail-local")
			continue
		}
		if plan.ListenAddr == "" {
			plan.ListenAddr = addr
		} else if plan.ListenAddr != addr {
			plan.Skipped = append(plan.Skipped, "profile "+name+"'s via address ("+r.ViaBase+
				") differs from "+plan.ListenAddr+" — one daemon serves one via address")
			continue
		}
		if r.Provider == subscriptionProvider {
			// The ChatGPT subscription's credential is openai-auth's access-token view, which
			// a via route does not carry (it carries a declared key or SigV4, WG7 (e)), and
			// every agent that speaks it implements it natively, so no derive points one at
			// this route (pi-codex-provider-shadowing.md OQ-2). Serving it would forward
			// unauthenticated requests to the subscription backend (WG-I21).
			plan.Skipped = append(plan.Skipped, "provider "+r.Provider+" (via for "+agent+
				") is the ChatGPT subscription, whose credential a via route does not carry — "+
				agent+" reaches it with its own client")
			continue
		}
		var entry *jsonx.OrderedMap
		if providers != nil {
			if v, ok := providers.Get(r.Provider); ok {
				entry, _ = v.(*jsonx.OrderedMap)
			}
		}
		if entry == nil {
			plan.Skipped = append(plan.Skipped, "provider "+r.Provider+" (via for "+agent+
				") is not in the composed table (YOLO_PROVIDERS)")
			continue
		}
		chat, responses := viaUpstreams(entry)
		if chat == "" && responses == "" {
			plan.Skipped = append(plan.Skipped, "provider "+r.Provider+" (via for "+agent+
				") declares no chat-completions or Responses endpoint — the via route passes "+
				"those two wires through, so it has no upstream")
			continue
		}
		plan.Routes = append(plan.Routes, viaRoute{
			Agent:        agent,
			ProviderName: r.Provider,
			KeyEnvName:   packload.KeyEnvName(entry),
			Chat:         newViaUpstream(chat),
			Responses:    newViaUpstream(responses),
		})
	}
	if len(plan.Routes) == 0 {
		plan.ListenAddr = ""
	}
	return plan
}

// subscriptionProvider is the one provider a via route never serves (WG-I21).
const subscriptionProvider = "openai-codex"

// The two wire_api values a via route passes through (packdecl's canonical vocabulary).
const (
	wireAPIChatCompletions = "openai-chat-completions"
	wireAPIResponses       = "openai-responses"
)

// viaUpstreams reads a provider's chat-completions and Responses base URLs off its
// composed entry (WG-I20), "" for a wire it does not offer. An endpoint that declares no
// wire_api speaks whichever the agent sends — the derives read an undeclared wire_api
// the same way — so an `openai` endpoint without one is both. The `base_url` shorthand
// is not consulted, for routeFor's reason: it names no protocol.
func viaUpstreams(entry *jsonx.OrderedMap) (chat, responses string) {
	openai := endpointBaseURL(entry, "openai")
	openaiWire := endpointField(entry, "openai", "wire_api")
	if openai != "" && (openaiWire == "" || openaiWire == wireAPIChatCompletions) {
		chat = openai
	}
	if r := endpointBaseURL(entry, wireAPIResponses); r != "" {
		if w := endpointField(entry, wireAPIResponses, "wire_api"); w == "" || w == wireAPIResponses {
			responses = r
		}
	}
	if responses == "" && openai != "" && (openaiWire == "" || openaiWire == wireAPIResponses) {
		responses = openai
	}
	return chat, responses
}

// viaWire is the wire a request's path names.
type viaWire int

const (
	viaWireOther viaWire = iota
	viaWireChatCompletions
	viaWireResponses
)

// requestWire classifies a path after the prefix: /responses and below is Responses,
// /chat/completions and below is chat-completions, anything else (/models, /embeddings)
// names neither.
func requestWire(path string) viaWire {
	switch {
	case path == "/responses" || strings.HasPrefix(path, "/responses/"):
		return viaWireResponses
	case path == "/chat/completions" || strings.HasPrefix(path, "/chat/completions/"):
		return viaWireChatCompletions
	}
	return viaWireOther
}

// viaWireSplit is one agent's route: each request goes to the upstream its path names.
// A path naming a wire the provider does not offer is refused, never sent to the other
// upstream, because a via route passes a request through and never translates it; a
// path naming neither goes to the chat-completions upstream, or to the Responses one
// when that is all the provider offers.
type viaWireSplit struct {
	agent, provider string
	chat, responses http.Handler
}

func (s viaWireSplit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := s.chat
	switch requestWire(r.URL.Path) {
	case viaWireResponses:
		if s.responses == nil {
			s.refuse(w, r, "a Responses", "chat-completions")
			return
		}
		h = s.responses
	case viaWireChatCompletions:
		if s.chat == nil {
			s.refuse(w, r, "a chat-completions", "Responses")
			return
		}
	default:
		if h == nil {
			h = s.responses
		}
	}
	h.ServeHTTP(w, r)
}

func (s viaWireSplit) refuse(w http.ResponseWriter, r *http.Request, wanted, offered string) {
	writeOpenAIError(w, http.StatusNotFound, "invalid_request_error",
		fmt.Sprintf("wire-bridge: %s is %s request, and provider %s (the via route for %s) "+
			"declares only a %s endpoint — a via route passes a request through to the "+
			"provider's own endpoint and never translates it", r.URL.Path, wanted, s.provider,
			s.agent, offered))
}

// viaMux routes /agent/<name>/… to that agent's handler, with the prefix stripped.
type viaMux struct {
	routes map[string]http.Handler
}

func (m *viaMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/agent/")
	agent, tail, _ := strings.Cut(rest, "/")
	h, known := m.routes[agent]
	if !ok || agent == "" || !known {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("wire-bridge: no via route for %q — this address serves only "+
				"/agent/<name>/ for an agent whose active profile says via: %q (known: %s)",
				r.URL.Path, ServiceName, strings.Join(m.agents(), ", ")))
		return
	}
	if !canonicalViaTail("/" + tail) {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("wire-bridge: %q is not a path a via route forwards — it has a '.', '..' "+
				"or empty segment, or an encoded '?' or '#', so the path the route classifies would "+
				"not be the path the provider receives", r.URL.Path))
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + tail
	r2.URL.RawPath = ""
	h.ServeHTTP(w, r2)
}

// canonicalViaTail reports whether a decoded path after the prefix is one the route may
// classify and forward (WG-I23): no '.', '..' or empty segment (one trailing '/' aside)
// and no '?' or '#', which only an encoded form puts in a decoded path. viaWireSplit
// classifies the path as written, and an upstream normalizes dot and empty segments before
// routing, so `/x/../chat/completions` would be refused as nothing and served as
// chat-completions — reaching the provider's other endpoint, or climbing out of its
// declared base path. Refusing is the one answer that keeps the classified path and the
// served path the same without the route rewriting what the agent sent.
func canonicalViaTail(p string) bool {
	if strings.ContainsAny(p, "?#") {
		return false
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return path.Clean(p) == p
}

func (m *viaMux) agents() []string {
	out := make([]string, 0, len(m.routes))
	for a := range m.routes {
		out = append(out, a)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

// passthroughHandler forwards one agent's requests to its upstream unchanged, adding the
// credential. key is the provider's bearer; signer, when set, replaces it (Bedrock).
type passthroughHandler struct {
	agent    string
	upstream string
	key      string
	signer   *bedrockSigner
	client   *http.Client
}

// The pass-through's client has no Client.Timeout, because net/http counts reading the
// response body toward it and so would cut a long stream mid-reply (WG-I24). do bounds
// the wait for the upstream's response HEADERS instead.
func newPassthroughHandler(agent, upstreamBaseURL, key string, signer *bedrockSigner) *passthroughHandler {
	return &passthroughHandler{agent: agent, upstream: strings.TrimRight(upstreamBaseURL, "/"),
		key: key, signer: signer, client: &http.Client{Transport: upstreamTransport}}
}

// viaHeaderTimeout bounds how long the via pass-through waits for an upstream's response
// headers (WG-I24): upstreamTimeout's ten minutes, the daemon's one timeout, applied to
// the part of an exchange that can hang without making progress. Once headers arrive the
// stream runs as long as the upstream keeps sending. It is a variable for one reason — a
// test must reach both sides of the bound without waiting ten minutes — and it is never
// rebound in production.
var viaHeaderTimeout = upstreamTimeout

// errViaHeaderTimeout is the cause do's header timer cancels a request with.
var errViaHeaderTimeout = errors.New("sent no response headers in time")

// cancelOnClose ends an upstream request's context when its body is closed, so the
// header timer's context lives exactly as long as the response it bounds.
type cancelOnClose struct {
	io.ReadCloser
	cancel func()
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (h *passthroughHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxViaBody+1))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "wire-bridge: reading the request body: "+err.Error())
		return
	}
	if len(body) > maxViaBody {
		writeOpenAIError(w, http.StatusRequestEntityTooLarge, "invalid_request_error",
			fmt.Sprintf("wire-bridge: the request body exceeds %d bytes", maxViaBody))
		return
	}
	resp, err := h.do(r, body)
	if err == nil && h.signer != nil && expiredRejection(resp) {
		// An expired signature or session: the request did not run, so refresh and retry once.
		_ = resp.Body.Close()
		h.signer.chain.Invalidate()
		resp, err = h.do(r, body)
	}
	if err != nil {
		var ce *credentialError
		if errors.As(err, &ce) {
			writeOpenAIError(w, ce.status, ce.typ, ce.message)
			return
		}
		if errors.Is(err, errViaHeaderTimeout) {
			logf("via route for %s: upstream %s %s", h.agent, h.upstream, err)
			writeOpenAIError(w, http.StatusGatewayTimeout, "api_error",
				"wire-bridge: the upstream "+h.upstream+" "+err.Error())
			return
		}
		logf("via route for %s: upstream %s: %v", h.agent, h.upstream, err)
		writeOpenAIError(w, http.StatusBadGateway, "api_error",
			"wire-bridge: the upstream "+h.upstream+" could not be reached: "+err.Error())
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Cache-Control", "X-Request-Id", "Retry-After"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	var copied int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			copied += int64(n)
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr == nil {
			continue
		}
		if errors.Is(rerr, io.EOF) || r.Context().Err() != nil {
			// The upstream finished, or the agent closed the request itself: no fault.
			return
		}
		// The upstream failed after the status line went out (WG-I24). Returning would let
		// net/http end the response cleanly, and a chat-completions client reads an end of
		// stream without [DONE] as a finished reply, so the agent would accept a truncated
		// one. Aborting the connection makes its read fail instead, as
		// httputil.ReverseProxy does.
		logf("via route for %s: upstream %s: the response stream ended early after %d bytes: %v — "+
			"aborting the agent's connection so it sees a failure, not a finished reply",
			h.agent, h.upstream, copied, rerr)
		if r.Context().Value(http.ServerContextKey) != nil {
			panic(http.ErrAbortHandler)
		}
		return
	}
}

// do builds and sends one upstream request: the agent's method, path remainder, query
// and body, with only the credential added. Inbound Authorization is never forwarded
// (WB-D4): the agent's key, if it sent one, is not the upstream's. The target is the
// base URL joined with the path's ESCAPED form (WG-I23), so a byte the route decoded is
// re-encoded rather than read a second time: an encoded '?' can never become a query
// separator, nor an encoded '%' a second escape. The wait for response headers is bounded
// by viaHeaderTimeout, and the body's read is not (WG-I24).
func (h *passthroughHandler) do(r *http.Request, body []byte) (*http.Response, error) {
	base, err := url.Parse(h.upstream)
	if err != nil {
		return nil, err
	}
	target := base.JoinPath(r.URL.EscapedPath())
	target.RawQuery = r.URL.RawQuery
	ctx, cancel := context.WithCancelCause(r.Context())
	timer := time.AfterFunc(viaHeaderTimeout, func() { cancel(errViaHeaderTimeout) })
	handedOff := false
	defer func() {
		if !handedOff {
			timer.Stop()
			cancel(nil)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"Content-Type", "Accept"} {
		if v := r.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	switch {
	case h.signer != nil:
		if err := h.signer.authorize(req, body); err != nil {
			return nil, err
		}
	case h.key != "":
		req.Header.Set("Authorization", "Bearer "+h.key)
	}
	resp, err := h.client.Do(req)
	timer.Stop()
	if context.Cause(ctx) == errViaHeaderTimeout {
		if err == nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("%w (%s)", errViaHeaderTimeout, viaHeaderTimeout)
	}
	if err != nil {
		return nil, err
	}
	handedOff = true
	resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: func() { cancel(nil) }}
	return resp, nil
}

// idleViaHandler answers every request on a via route that has no credential, naming
// what is missing (WG7 (e)): the other routes still serve.
type idleViaHandler struct{ reason string }

func (h idleViaHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "wire-bridge: "+h.reason)
}

// writeOpenAIError writes an OpenAI-shaped error body: the via route's agents speak
// OpenAI, so the Anthropic shape the adapter routes use would be unreadable to them.
func writeOpenAIError(w http.ResponseWriter, status int, typ, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": message, "type": typ, "code": nil},
	})
}

// viaHandlerFor builds the via listener's handler: per route, one pass-through per
// distinct upstream, each with its own credential from the key channel (WG7 (e)), and an
// idle handler for an upstream whose credential is missing. It returns the handler and
// one description per upstream for the serve log.
func viaHandlerFor(plan viaPlan, home string) (http.Handler, []string) {
	mux := &viaMux{routes: map[string]http.Handler{}}
	var lines []string
	for _, rt := range plan.Routes {
		split := viaWireSplit{agent: rt.Agent, provider: rt.ProviderName}
		if rt.Chat.BaseURL != "" && rt.Chat == rt.Responses {
			h, line := viaUpstreamHandler(rt, rt.Chat, home)
			split.chat, split.responses = h, h
			lines = append(lines, "/agent/"+rt.Agent+"/ chat-completions and Responses "+line)
		} else {
			if rt.Chat.BaseURL != "" {
				h, line := viaUpstreamHandler(rt, rt.Chat, home)
				split.chat = h
				lines = append(lines, "/agent/"+rt.Agent+"/ chat-completions "+line)
			}
			if rt.Responses.BaseURL != "" {
				h, line := viaUpstreamHandler(rt, rt.Responses, home)
				split.responses = h
				lines = append(lines, "/agent/"+rt.Agent+"/ Responses "+line)
			}
		}
		mux.routes[rt.Agent] = split
	}
	return mux, lines
}

// viaUpstreamHandler builds one upstream's pass-through with its credential — SigV4 for
// a Bedrock host, the provider's key otherwise — or the idle handler naming what is
// missing, and the serve-log description of either.
func viaUpstreamHandler(rt viaRoute, up viaUpstream, home string) (http.Handler, string) {
	if up.SignRegion != "" {
		env := sigv4.EnvFrom(func(name string) string {
			v, _ := resolveKey(name, home, rt.Agent)
			return v
		})
		if !hasAWSCredentialSource(env) {
			reason := "the via route for " + rt.Agent + " goes to Bedrock (" + up.BaseURL +
				"), and none of its credential sources is set — AWS_ACCESS_KEY_ID + " +
				"AWS_SECRET_ACCESS_KEY, the aws-auth pointer AWS_CONTAINER_CREDENTIALS_FULL_URI, " +
				"or AWS_BEARER_TOKEN_BEDROCK"
			return idleViaHandler{reason: reason}, "idle: " + reason
		}
		return newPassthroughHandler(rt.Agent, up.BaseURL, "",
				&bedrockSigner{region: up.SignRegion, chain: &sigv4.Chain{Env: env}}),
			"→ " + up.BaseURL + " (provider " + rt.ProviderName + ", SigV4 for bedrock in " +
				up.SignRegion + ", from " + env.String() + ")"
	}
	key, source := resolveKey(rt.KeyEnvName, home, rt.Agent)
	if key == "" && rt.KeyEnvName != "" {
		reason := "the via route for " + rt.Agent + " needs $" + rt.KeyEnvName +
			" (provider " + rt.ProviderName + "), and it is set neither in " +
			keyChannelDescription(home, rt.Agent) + " nor in the daemon's environment"
		return idleViaHandler{reason: reason}, "idle: " + reason
	}
	cred := "none"
	if rt.KeyEnvName != "" {
		cred = "$" + rt.KeyEnvName + " from " + source
	}
	return newPassthroughHandler(rt.Agent, up.BaseURL, key, nil),
		"→ " + up.BaseURL + " (provider " + rt.ProviderName + ", credential " + cred + ")"
}
