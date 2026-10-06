package wirebridged

// messages.go is the adapter route's MESSAGES PASS-THROUGH for a Bedrock upstream
// (docs/design/wire-bridge-gateway.md Part 2, the mechanism of bedrock-plumbing.md OQ-BR11;
// released as WG-I25, built as WG-I30 to WG-I35). An agent speaking Anthropic Messages to the
// bridge (claude, and copilot, whose derive prefers an anthropic endpoint) asks for a model
// per request. When the provider's own model list declares that model's vendor "anthropic",
// the request is forwarded UNTRANSLATED to bedrock-runtime's own Anthropic Messages route, so
// cache_control, thinking and every other Messages field reach a Claude model as the agent
// wrote them. Every other model is translated to chat-completions exactly as before
// (handler.go): one route, two upstreams, chosen per request by the model id.
//
// THE MEASUREMENT §3 LEFT TO THE BUILDER (SOURCED 2026-09-29; MEASURED 2026-10-01, once): the
// route is `POST https://bedrock-runtime.<region>.amazonaws.com/anthropic/v1/messages`, taking
// the first-party body (`model` and `stream` in it, `anthropic-version: 2023-06-01` as a
// header), and it streams Anthropic server-sent events, not AWS's binary event stream. AWS's
// Messages API page drives it with the plain Anthropic SDK (`Anthropic(base_url=".../anthropic")`,
// `client.messages.stream`), whose stream decoder is the SSE one; in that SDK only the
// InvokeModel client (`/model/{id}/invoke-with-response-stream`) swaps in the AWS event-stream
// decoder. The first live request through this pass-through, a streamed Claude Opus 5.5 turn
// in us-east-1 signed by the bridge, was answered 200 as text/event-stream carrying Anthropic's
// events (docs/design/wire-bridge-gateway.md §2.4, request 7). So the stream is relayed byte for
// byte, and an answer framed as application/vnd.amazon.eventstream is refused by name rather
// than relayed as bytes claude cannot parse (WG-I30).
//
// What the route adds is the credential and nothing else: the same SigV4 signer and chain as
// the route's chat-completions upstream (one credential cache for both), or, when the chain's
// only source is AWS_BEARER_TOKEN_BEDROCK, that key as `x-api-key`, the header AWS documents
// for this route (WG-I31). The destination is composed from the provider row's own
// bedrock-runtime host, so nothing in a request can name a new one (§7).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const (
	// bedrockMessagesPath is bedrock-runtime's Anthropic Messages route (AWS's "Inference
	// using Anthropic Messages API" page: base URL `https://bedrock-runtime.{region}.amazonaws.com/anthropic`).
	bedrockMessagesPath = "/anthropic/v1/messages"
	// anthropicVendor is the model-list vendor whose ids the route passes through. vendor is
	// an open vocabulary core does not interpret (model-lists-and-pickers.md §1); the bridge
	// asks about exactly this one value, compared by equality.
	anthropicVendor = "anthropic"
	// defaultAnthropicVersion is the version header AWS's Messages page requires on this
	// route, sent when the agent sent none (claude always sends one).
	defaultAnthropicVersion = "2023-06-01"
	// awsEventStreamType is AWS's binary event-stream media type, the framing of
	// InvokeModelWithResponseStream, which this route's documentation does not use.
	awsEventStreamType = "application/vnd.amazon.eventstream"
	// oneMillionSuffix is Claude Code's client spelling of the 1M-context variant of a model id
	// (docs/reference/providers.md): never a wire id, so never on a provider's list.
	oneMillionSuffix = "[1m]"
)

// bedrockMessagesURL is bedrock-runtime's Messages route beside a Bedrock upstream base URL, ""
// for a URL with no https host. It is asked only of a route that signs (newMessagesPassthrough
// needs its signer), so the pass-through exists exactly where the route is already a Bedrock
// one: at runtime's own host, or, for a provider whose platform says it is Bedrock
// (bedrockSigning, WG-I37), at the address it names.
//
// WHERE THE BASE'S OWN /openai/v1 WAS: runtime serves both routes from its root, and a proxy
// that mirrors runtime under a path prefix (a corporate gateway's `/aws/bedrock/openai/v1`)
// serves Messages under the same prefix, so the prefix is kept. A base that does not end in
// runtime's OpenAI path says nothing about where Messages lives, and gets the host's root.
func bedrockMessagesURL(upstreamBaseURL string) string {
	u, err := url.Parse(upstreamBaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	prefix := ""
	if p := strings.TrimSuffix(u.Path, "/"); strings.HasSuffix(p, runtimeOpenAIPath) {
		prefix = strings.TrimSuffix(p, runtimeOpenAIPath)
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: prefix + bedrockMessagesPath}).String()
}

// anthropicModelIDs reads, off one composed provider entry, every model id its list declares
// vendor "anthropic" for (declaredVendors). The id is never parsed for a maker
// (wire-bridge-gateway.md §3). conflicts are the ids two of the list's aliases declare different
// vendors for, for the serve log (WG-I34).
func anthropicModelIDs(entry *jsonx.OrderedMap) (ids map[string]bool, conflicts []string) {
	for id, set := range declaredVendorSets(entry) {
		if !set[anthropicVendor] {
			continue
		}
		if len(set) > 1 {
			conflicts = append(conflicts, id)
			continue
		}
		if ids == nil {
			ids = map[string]bool{}
		}
		ids[id] = true
	}
	sort.Strings(conflicts)
	return ids, conflicts
}

// declaredVendors reads, off one composed provider entry, the maker its list declares for each
// model id: `models.<alias>` is the wire id and `model_options.<alias>.vendor` its maker (the flat
// shape a pack's model_options and a user's object-form entry both compose into;
// packload.dropRepointedVendors drops a pack's vendor from an alias the user points at another
// id), and, where no pack or config gives the provider a list, each entry of the FETCHED LIST
// (packload.FetchedModelsKey), whose maker is AWS's own providerName
// (docs/design/model-lists-and-pickers.md OQ-MM6). A list id is keyed with Claude's [1m] client
// suffix trimmed, as a request's is (claims): the suffix is never part of the wire id, and a list
// written for claude may carry it.
//
// Only a DECLARED vendor counts. An alias declaring none (a user's string-form alias for the
// id, or a pack alias with no facts) says nothing about the maker, so it leaves another alias's
// declaration standing. An id its aliases declare two different vendors for has none: it keeps
// today's translation, and anthropicModelIDs' conflicts say which when one was Anthropic (WG-I34).
func declaredVendors(entry *jsonx.OrderedMap) map[string]string {
	var vendors map[string]string
	for id, set := range declaredVendorSets(entry) {
		if len(set) != 1 {
			continue
		}
		for vendor := range set {
			if vendors == nil {
				vendors = map[string]string{}
			}
			vendors[id] = vendor
		}
	}
	return vendors
}

// declaredVendorSets is declaredVendors' read before conflicts are settled: each wire id, and every
// vendor an entry declares for it.
func declaredVendorSets(entry *jsonx.OrderedMap) map[string]map[string]bool {
	if entry == nil {
		return nil
	}
	declared := map[string]map[string]bool{} // wire id -> the vendors its entries declare
	note := func(spelled, vendor string) {
		id := strings.TrimSuffix(spelled, oneMillionSuffix)
		if id == "" || vendor == "" {
			return
		}
		if declared[id] == nil {
			declared[id] = map[string]bool{}
		}
		declared[id][vendor] = true
	}
	mv, _ := entry.Get("models")
	if models, _ := mv.(*jsonx.OrderedMap); models != nil {
		ov, _ := entry.Get("model_options")
		options, _ := ov.(*jsonx.OrderedMap)
		for _, alias := range models.Keys() {
			raw, _ := models.Get(alias)
			spelled, _ := raw.(string)
			note(spelled, aliasVendor(options, alias))
		}
	}
	for _, m := range packload.FetchedModelsOf(entry) {
		note(m.ID, m.Vendor)
	}
	return declared
}

// aliasVendor is anthropicModelIDs' read of one alias's vendor, "" for none. The composed
// facts are flat strings, and a non-string (which validation refuses) is no vendor.
func aliasVendor(options *jsonx.OrderedMap, alias string) string {
	if options == nil {
		return ""
	}
	fv, _ := options.Get(alias)
	facts, _ := fv.(*jsonx.OrderedMap)
	if facts == nil {
		return ""
	}
	v, _ := facts.Get("vendor")
	s, _ := v.(string)
	return s
}

// messagesPassthrough is one Bedrock route's Messages upstream: the route's own signer, the
// ids it carries, and a client with no whole-exchange timeout (WG-I33).
type messagesPassthrough struct {
	url    string
	models map[string]bool
	signer *bedrockSigner
	client *http.Client
}

// newMessagesPassthrough is the pass-through for a Bedrock upstream whose list declares at
// least one Anthropic id, or nil: no Anthropic id, or a route with no signer (one that is not
// Bedrock's), leaves the route translating every request, as it did before Part 2.
func newMessagesPassthrough(upstreamBaseURL string, models map[string]bool, signer *bedrockSigner) *messagesPassthrough {
	u := bedrockMessagesURL(upstreamBaseURL)
	if u == "" || len(models) == 0 || signer == nil {
		return nil
	}
	return &messagesPassthrough{url: u, models: models, signer: signer,
		client: &http.Client{Transport: upstreamTransport}}
}

// claims reports whether this pass-through carries a request for model, and the id it sends:
// the model with Claude's [1m] client suffix trimmed, which is how the list spells it.
func (m *messagesPassthrough) claims(model string) (string, bool) {
	id := strings.TrimSuffix(model, oneMillionSuffix)
	return id, id != "" && m.models[id]
}

// ids is the carried ids in order, for the serve log.
func (m *messagesPassthrough) ids() []string {
	out := make([]string, 0, len(m.models))
	for id := range m.models {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// messagesServeNote is the adapter route's serve-line suffix naming its Messages pass-through,
// "" when the route has none, so the log says which models skip translation before any
// request arrives.
func messagesServeNote(h http.Handler) string {
	bh, _ := h.(*bridgeHandler)
	if bh == nil || bh.messages == nil {
		return ""
	}
	return fmt.Sprintf("; Anthropic models on its list pass untranslated to %s: %s",
		bh.messages.url, strings.Join(bh.messages.ids(), ", "))
}

// serve forwards one request and relays the answer. note is the request line's suffix
// (ServeHTTP logs it after serve returns, and after an abort's panic too, since the line is
// a deferred call).
func (m *messagesPassthrough) serve(rec *statusRecorder, in *http.Request, body []byte,
	model, id string, note *string) {
	*note = fmt.Sprintf(" (model %s is Anthropic's on the provider's list: untranslated to %s)", id, m.url)
	if id != model {
		// The one body edit the pass-through makes, and the translator makes it too
		// (wirebridge.normalizeModel): [1m] is Claude Code's client spelling, not a wire id.
		rewritten, err := withModel(body, id)
		if err != nil {
			writeAnthropicError(rec, http.StatusBadRequest, "invalid_request_error", "wire-bridge: "+err.Error())
			return
		}
		body = rewritten
	}
	resp, err := m.do(in, body)
	if err == nil && expiredRejection(resp) {
		// The request did not run, so a refresh and one retry is safe (§2.1), as on the
		// route's chat-completions upstream.
		logf("upstream rejected the credential as expired; refreshing it and retrying once (%s)", m.url)
		_ = resp.Body.Close()
		m.signer.chain.Invalidate()
		resp, err = m.do(in, body)
	}
	if err != nil {
		var ce *credentialError
		switch {
		case errors.As(err, &ce):
			writeAnthropicError(rec, ce.status, ce.typ, ce.message)
		case errors.Is(err, errViaHeaderTimeout):
			logf("upstream %s %s", m.url, err)
			writeAnthropicError(rec, http.StatusGatewayTimeout, "api_error", "wire-bridge: the upstream "+m.url+" "+err.Error())
		default:
			logf("upstream %s: %v", m.url, err)
			writeAnthropicError(rec, http.StatusBadGateway, "api_error", "wire-bridge: upstream unavailable: "+err.Error())
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		relayMessagesError(rec, resp)
		return
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == awsEventStreamType {
		// One live answer was SSE (§2.4), and the framing is otherwise SOURCED (WG-I30). If the
		// route ever frames its stream the other way, the agent gets a named failure, never
		// bytes it cannot parse.
		logf("upstream %s answered with AWS's binary event stream (%s), where the route's "+
			"documentation promises Anthropic server-sent events; refusing it with a 502 (WG-I30)",
			m.url, awsEventStreamType)
		writeAnthropicError(rec, http.StatusBadGateway, "api_error", "wire-bridge: Bedrock's Messages route answered "+
			"with AWS's binary event stream ("+awsEventStreamType+") instead of the Anthropic server-sent events its "+
			"documentation describes, and the bridge relays those only (wire-bridge-gateway.md WG-I30)")
		return
	}
	copyMessagesHeaders(rec.Header(), resp.Header)
	rec.WriteHeader(resp.StatusCode)
	// The end-of-stream watch follows the ANSWER's framing, not the request's `stream` flag:
	// the grammar it checks is the one the bytes are in, so an SSE answer cut short is
	// truncated whatever the agent asked for, and a JSON answer has no closing event to watch.
	if m.relay(rec, in, resp, mediaType == "text/event-stream", id) {
		*note += "; the stream was cut short and the agent's connection aborted"
		if in.Context().Value(http.ServerContextKey) != nil {
			panic(http.ErrAbortHandler)
		}
	}
}

// do builds and sends one upstream request: the agent's body, the Messages headers it sent,
// and the credential. The inbound Authorization and x-api-key are never copied: they carry
// this launch's caller token (WB-D18), checked before the handler ran.
func (m *messagesPassthrough) do(in *http.Request, body []byte) (*http.Response, error) {
	return sendHeaderBounded(in.Context(), m.client, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build upstream request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if v := in.Header.Get("Accept"); v != "" {
			req.Header.Set("Accept", v)
		}
		version := in.Header.Get("Anthropic-Version")
		if version == "" {
			version = defaultAnthropicVersion
		}
		req.Header.Set("Anthropic-Version", version)
		for _, v := range in.Header.Values("Anthropic-Beta") {
			req.Header.Add("Anthropic-Beta", v)
		}
		// LAST, because SigV4 signs the headers present (sigv4.Sign).
		if err := m.signer.authorizeAs(req, body, "X-Api-Key", ""); err != nil {
			return nil, err
		}
		return req, nil
	})
}

// relay copies the upstream's body to the agent chunk by chunk, flushing each, and reports
// whether the agent must be told the response failed (WG-I33). A body read that fails, or an
// SSE stream that ends without the event closing Anthropic's grammar (message_stop, or an
// error event), is a reply the agent would otherwise take as finished: the translation path
// answers the same case with an error event, and here, where the bytes are the upstream's
// own and the cut may fall inside an event, the connection is aborted instead (WG-I24's
// answer). The agent closing its own request is no fault and is reported as none.
func (m *messagesPassthrough) relay(rec *statusRecorder, in *http.Request, resp *http.Response, sse bool, id string) bool {
	flusher, _ := rec.ResponseWriter.(http.Flusher)
	var closed sseClose
	buf := make([]byte, 32<<10)
	var copied int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			copied += int64(n)
			if sse {
				closed.feed(buf[:n])
			}
			if _, werr := rec.Write(buf[:n]); werr != nil {
				// The agent hung up: the recorder kept the error for the request line, and
				// there is nobody left to tell.
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr == nil {
			continue
		}
		if in.Context().Err() != nil {
			return false
		}
		var cause string
		switch {
		case !errors.Is(rerr, io.EOF):
			cause = rerr.Error()
		case !sse || closed.done:
			return false
		default:
			cause = "it ended without a message_stop or error event"
		}
		logf("upstream %s: the Messages stream for model %s ended early after %d bytes: %s — "+
			"aborting the agent's connection so it sees a failure, not a finished reply", m.url, id, copied, cause)
		return true
	}
}

// sseClose watches a relayed Anthropic SSE stream for the event that closes it: an `event:`
// line naming message_stop or error, or a data line whose JSON's type is one of them (so a
// stream sending data lines alone is read too). It keeps only the head of the current line,
// because both markers sit at a line's start: a text delta can carry either word, but only
// inside a JSON string on a data line whose own type is a delta, never at the head of one.
type sseClose struct {
	head []byte
	done bool
}

const sseHeadLen = 64

func (s *sseClose) feed(p []byte) {
	for len(p) > 0 && !s.done {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		if room := sseHeadLen - len(s.head); room > 0 {
			if len(part) > room {
				part = part[:room]
			}
			s.head = append(s.head, part...)
		}
		if i < 0 {
			return
		}
		s.done = closesStream(strings.TrimRight(string(s.head), "\r"))
		s.head = s.head[:0]
		p = p[i+1:]
	}
}

func closesStream(line string) bool {
	if v, ok := strings.CutPrefix(line, "event:"); ok {
		v = strings.TrimSpace(v)
		return v == "message_stop" || v == "error"
	}
	v, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return false
	}
	compact := strings.Join(strings.Fields(v), "")
	return strings.HasPrefix(compact, `{"type":"message_stop"`) || strings.HasPrefix(compact, `{"type":"error"`)
}

// messagesResponseHeaders are the upstream response headers an Anthropic client reads and the
// pass-through copies back: the body's type, the retry hints, and the request ids a support
// case names. No credential or AWS signing header is among them.
var messagesResponseHeaders = []string{"Content-Type", "Cache-Control", "Retry-After", "X-Should-Retry",
	"Request-Id", "X-Amzn-Requestid"}

func copyMessagesHeaders(dst, src http.Header) {
	for _, k := range messagesResponseHeaders {
		if v := src.Get(k); v != "" {
			dst.Set(k, v)
		}
	}
}

// relayMessagesError relays an upstream refusal at its own status, since the route speaks the
// agent's protocol and its statuses already mean what an Anthropic client reads them as (the
// translating route's 5xx-to-502 mapping exists because an OpenAI status does not): an
// Anthropic-shaped error body verbatim, any other (AWS's {"message": ...} envelope) put into
// the Anthropic shape with its message (WG-I32).
func relayMessagesError(rec *statusRecorder, resp *http.Response) {
	// A truncated and an unparseable body take the same path below, so the read error cannot
	// change the outcome; the status reaches the log through the request line either way.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamErrorBody))
	for _, k := range []string{"Retry-After", "X-Should-Retry", "Request-Id", "X-Amzn-Requestid"} {
		if v := resp.Header.Get(k); v != "" {
			rec.Header().Set(k, v)
		}
	}
	if anthropicErrorBody(body) {
		rec.Header().Set("Content-Type", "application/json")
		rec.WriteHeader(resp.StatusCode)
		_, _ = rec.Write(body)
		return
	}
	writeAnthropicError(rec, resp.StatusCode, anthropicErrorType(resp.StatusCode),
		upstreamErrorMessage(body, resp.StatusCode))
}

// anthropicErrorBody reports whether body is Anthropic's error shape,
// {"type":"error","error":{"type":…,"message":…}}.
func anthropicErrorBody(body []byte) bool {
	var shape struct {
		Type  string `json:"type"`
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &shape) == nil && shape.Type == "error" && shape.Error != nil && shape.Error.Type != ""
}

// anthropicErrorType is the Anthropic error type for a status, as Anthropic's API assigns them.
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	}
	return "api_error"
}

// withModel is body with its top-level "model" replaced and every other member kept as
// sent. Members are re-encoded without HTML escaping, so no string changes; only the
// members' order and insignificant whitespace may, which no Messages field depends on.
func withModel(body []byte, model string) ([]byte, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		return nil, fmt.Errorf("decoding the request to trim the %s suffix from its model: %w", oneMillionSuffix, err)
	}
	id, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	members["model"] = id
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(members); err != nil {
		return nil, fmt.Errorf("encoding the request: %w", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}
