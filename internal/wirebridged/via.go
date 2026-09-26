package wirebridged

// via.go is the bridge's VIA side (docs/design/wire-bridge-gateway.md OQ-WG6/WG7, Part 3):
// an agent whose active profile says `via: "wire-bridge"` points its OpenAI base URL at
// its own path on one declared address, <via_address>/agent/<agent>, and this daemon
// forwards what it sends there, UNCHANGED, to that profile's provider's own `openai`
// endpoint. It adds the upstream credential and nothing else: a SigV4 signature for an
// exact bedrock-runtime host (the signer, OQ-WG1), the provider's key otherwise. No
// translation, no body rewrite, no model remapping — the sign-only route.
//
// One daemon, several routes (WG7): the adapter routes keep the root of their own ports
// (claude's ANTHROPIC_BASE_URL is unchanged), and every via route shares the via
// address, told apart by the /agent/<name>/ prefix. A request with no known prefix is
// refused, never routed to a default (WG4), and a route with no credential answers with
// an error naming what is missing while every other route serves (WG7 (e)).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// viaRoute is one agent's pass-through route.
type viaRoute struct {
	Agent           string
	ProviderName    string
	UpstreamBaseURL string
	KeyEnvName      string
	SignRegion      string
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
// the service's address (ViaBase); its upstream is the provider's own `openai` endpoint,
// which must speak chat-completions when it declares a wire_api at all.
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
		upstream := endpointBaseURL(entry, "openai")
		if upstream == "" {
			plan.Skipped = append(plan.Skipped, "provider "+r.Provider+" (via for "+agent+
				") declares no openai endpoint — the via route passes chat-completions through "+
				"to it, so it has no upstream")
			continue
		}
		if w := endpointField(entry, "openai", "wire_api"); w != "" && w != "openai-chat-completions" {
			plan.Skipped = append(plan.Skipped, "provider "+r.Provider+"'s openai endpoint speaks "+
				w+" — the via route passes chat-completions through")
			continue
		}
		plan.Routes = append(plan.Routes, viaRoute{
			Agent:           agent,
			ProviderName:    r.Provider,
			UpstreamBaseURL: upstream,
			KeyEnvName:      entryString(entry, "", "api_key_env_name"),
			SignRegion:      bedrockSignRegion(upstream),
		})
	}
	if len(plan.Routes) == 0 {
		plan.ListenAddr = ""
	}
	return plan
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
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + tail
	r2.URL.RawPath = ""
	h.ServeHTTP(w, r2)
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

func newPassthroughHandler(route viaRoute, key string, signer *bedrockSigner) *passthroughHandler {
	return &passthroughHandler{agent: route.Agent, upstream: strings.TrimRight(route.UpstreamBaseURL, "/"),
		key: key, signer: signer, client: &http.Client{Transport: upstreamTransport, Timeout: upstreamTimeout}}
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
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

// do builds and sends one upstream request: the agent's method, path remainder, query
// and body, with only the credential added. Inbound Authorization is never forwarded
// (WB-D4): the agent's key, if it sent one, is not the upstream's.
func (h *passthroughHandler) do(r *http.Request, body []byte) (*http.Response, error) {
	target := h.upstream + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
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
	return h.client.Do(req)
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

// viaHandlerFor builds the via listener's handler: one pass-through per route, each with
// its own credential from the key channel (WG7 (e)), and an idle handler for a route
// whose credential is missing. It returns the handler and one description per route for
// the serve log.
func viaHandlerFor(plan viaPlan, home string) (http.Handler, []string) {
	mux := &viaMux{routes: map[string]http.Handler{}}
	var lines []string
	for _, rt := range plan.Routes {
		switch {
		case rt.SignRegion != "":
			env := sigv4.EnvFrom(func(name string) string {
				v, _ := resolveKey(name, home)
				return v
			})
			if !hasAWSCredentialSource(env) {
				reason := "the via route for " + rt.Agent + " goes to Bedrock (" + rt.UpstreamBaseURL +
					"), and none of its credential sources is set — AWS_ACCESS_KEY_ID + " +
					"AWS_SECRET_ACCESS_KEY, the aws-auth pointer AWS_CONTAINER_CREDENTIALS_FULL_URI, " +
					"or AWS_BEARER_TOKEN_BEDROCK"
				mux.routes[rt.Agent] = idleViaHandler{reason: reason}
				lines = append(lines, "/agent/"+rt.Agent+"/ idle: "+reason)
				continue
			}
			mux.routes[rt.Agent] = newPassthroughHandler(rt, "",
				&bedrockSigner{region: rt.SignRegion, chain: &sigv4.Chain{Env: env}})
			lines = append(lines, "/agent/"+rt.Agent+"/ → "+rt.UpstreamBaseURL+" (provider "+
				rt.ProviderName+", SigV4 for bedrock in "+rt.SignRegion+", from "+env.String()+")")
		default:
			key, source := resolveKey(rt.KeyEnvName, home)
			if key == "" && rt.KeyEnvName != "" {
				reason := "the via route for " + rt.Agent + " needs $" + rt.KeyEnvName +
					" (provider " + rt.ProviderName + "), and it is set neither in " +
					userEnvFilePath(home) + " nor in the daemon's environment"
				mux.routes[rt.Agent] = idleViaHandler{reason: reason}
				lines = append(lines, "/agent/"+rt.Agent+"/ idle: "+reason)
				continue
			}
			mux.routes[rt.Agent] = newPassthroughHandler(rt, key, nil)
			cred := "none"
			if rt.KeyEnvName != "" {
				cred = "$" + rt.KeyEnvName + " from " + source
			}
			lines = append(lines, "/agent/"+rt.Agent+"/ → "+rt.UpstreamBaseURL+" (provider "+
				rt.ProviderName+", credential "+cred+")")
		}
	}
	return mux, lines
}
