// Package wirebridged is the `yolo-jaild wire-bridge` subcommand: the transport
// half of the wire bridge (docs/reference/wire-bridge.md §3-§5). internal/wirebridge
// is the translation library and is deliberately I/O-free; everything with a
// socket, a file, a clock or a key in it lives here — the loopback listener, the
// upstream dial, the SSE line framing, the status codes, the outbound
// Authorization header (WB-D4), and the boot-time read of the composed provider
// table that decides whether there is anything to serve at all.
//
// The daemon is SELECTION-LAZY, not config-lazy (§3.4): at boot it reads
// YOLO_PROVIDERS / YOLO_PROFILES / YOLO_USE_PROFILES (the same loaders the
// derives read) and serves only when some agent's active profile names a
// provider whose `anthropic` endpoint is jail-local — i.e. routed AT this
// bridge — and whose `openai` endpoint supplies the upstream. Any other boot is
// a HEALTHY IDLE: bind nothing, publish nothing, sleep forever, one stderr line
// saying why. That laziness is what licenses the coarse when_bins inclusion
// (§3.2): the bridge may be staged in every launch that selects a consumer of
// the bridged URL, and precise behavior is recovered here by the same
// selection table every agent honors.
//
// "Some agent", not "claude": §3.4 wrote claude because claude was the only
// consumer the design knew, and the shipped tree outgrew it — copilot's derive
// prefers the anthropic endpoint of any provider declaring one (D-3), so
// cerebras's bridged URL reaches copilot the same as claude. The consumers of
// an anthropic endpoint decide the need's when_bins (claude and copilot today,
// packs/cerebras/pack.json); the selection table decides the serving. One
// table, so the two can never disagree about who the bridge is for.
//
// Frozen contracts: the listen port lives ONLY in the provider's
// `endpoints.anthropic.base_url` (WB-D2/D13 — one writer, no second knob), the
// bind happens BEFORE the endpoint file is published (§5 — the file appearing
// means the listener exists), count_tokens refuses 404 (WB-D14), inbound
// Authorization is ignored (WB-D4), and nothing but the boot-selected upstream
// is ever dialed, no body and no key ever logged (§5's forbidden list).
package wirebridged

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ServiceName is this daemon's service name — the supervisor entry's name and
// the endpoint file's stem. The packs/wire-bridge manifest's `endpoint` field
// must carry the same name: the manifest is the host-side declaration, this
// constant is the thing that actually publishes, and the daemon-side file is
// the authority (there is no host half to disagree with in this build).
const ServiceName = "wire-bridge"

// CodexResponsesListenAddr is the jail-local endpoint this daemon binds for the
// Claude=codex profile, and it is THIS SIDE of a contract whose other side moved.
// packs/wire-bridge declares the same address on its `openai-responses → anthropic`
// adaptation, and the resolver composes it into openai-codex's entry, so the Claude
// derive reads an endpoint like any other instead of hand-copying this constant
// (docs/design/protocol-resolution.md §3). openai-codex remains CREDENTIAL-free in
// YOLO_PROVIDERS — its row names no key variable, and this daemon's Codex route takes
// its access-token view from openai-auth, never from a generated file; what the row
// carries is the public Responses ADDRESS, which is the upstream below.
//
// The route selection still reads this constant rather than the table, because the
// Codex route is chosen before any endpoint is consulted (routeFor's first branch).
const CodexResponsesListenAddr = "127.0.0.1:8215"

// CodexResponsesBaseURL is the subscription Responses API base. The handler
// appends /responses, just as the existing route appends /chat/completions.
const CodexResponsesBaseURL = "https://chatgpt.com/backend-api/codex"

const entryChannelPollInterval = 200 * time.Millisecond

// EndpointFile is the endpoint file this daemon publishes once its listener is
// up — /run/yolo-services/wire-bridge.endpoint. Composed from internal/paths
// rather than spelled out, for the same reason oauthterminator's
// BrokerEndpointEnv is: producer and consumer drifting apart is what once
// silently disabled a whole service class.
var EndpointFile = paths.JailHostServicesDir + "/" + ServiceName + paths.ServiceEndpointExt

// Main is the subcommand body. rest is accepted and ignored — the daemon's
// whole input is the environment, like `yolo-jaild supervise`'s. Return codes:
// 1 on a boot failure (a bind or publish error; `restart: on-failure` makes
// supervise retry with backoff); healthy idle and serving states block until
// the jail retires them.
func Main(rest []string) int {
	return run(context.Background(), entrypoint.EnvFromOS(), entryChannelPollInterval)
}

// run resolves the boot route, then watches the live per-entry channel while
// idle. An attach runs a new agent entry but does not restart the supervisor,
// so this is the one path by which a selection-lazy daemon can observe a later
// selection. Once serving, its upstream remains fixed for the daemon lifetime:
// a jail may have concurrent entries, and letting the latest attach replace a
// listener under an earlier entry would redirect that entry's traffic.
func run(ctx context.Context, initial *entrypoint.Env, pollInterval time.Duration) int {
	route, e, ok := waitForActiveRoute(ctx, initial, pollInterval)
	if !ok {
		return 0
	}
	return serve(ctx, route, e)
}

func waitForActiveRoute(ctx context.Context, initial *entrypoint.Env, pollInterval time.Duration) (route, *entrypoint.Env, bool) {
	if route, idleReason := resolveRoute(initial); idleReason == "" {
		return route, initial, true
	} else {
		fmt.Fprintf(os.Stderr, "wire-bridge: idling: %s\n", idleReason)
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return route{}, nil, false
		case <-ticker.C:
			candidate := cloneEnv(initial)
			if !entrypoint.HydrateEntryChannel(candidate) {
				continue
			}
			route, idleReason := resolveRoute(candidate)
			if idleReason == "" {
				return route, candidate, true
			}
		}
	}
}

func cloneEnv(e *entrypoint.Env) *entrypoint.Env {
	vars := make(map[string]string, len(e.Vars))
	for key, value := range e.Vars {
		vars[key] = value
	}
	return entrypoint.NewEnv(vars)
}

func serve(ctx context.Context, route route, e *entrypoint.Env) int {

	var (
		key, keySource string
		handler        http.Handler
	)
	if route.CodexAccessToken {
		endpoint := e.Getenv(openauthclient.EndpointEnv)
		if endpoint == "" {
			fmt.Fprintf(os.Stderr, "wire-bridge: idling: Codex route needs %s; no unauthenticated upstream is served\n", openauthclient.EndpointEnv)
			return idleUntilStopped(ctx)
		}
		handler = NewCodexResponsesHandler(route.UpstreamBaseURL, endpoint)
		keySource = "OpenAI credential service access-token views"
	} else {
		key, keySource = resolveKey(route.KeyEnvName, e.Home)
		if key == "" && route.KeyEnvName != "" {
			fmt.Fprintf(os.Stderr, "wire-bridge: idling: provider %q names credential variable "+
				"%s, and it is set neither in %s nor in this process's environment — the bridge "+
				"never serves unauthenticated upstream traffic (wire-bridge.md §5)\n",
				route.ProviderName, route.KeyEnvName, userEnvFilePath(e.Home))
			return idleUntilStopped(ctx)
		}
		handler = NewHandler(route.UpstreamBaseURL, key)
	}

	// BIND BEFORE PUBLISH (§5): the endpoint file's appearance is the promise
	// that a listener exists at the address it names.
	ln, err := net.Listen("tcp", route.ListenAddr)
	if err != nil {
		// A port the manifest URL names that something else holds is a real
		// fault (WB-D13: the URL is the single source of the port), not an
		// idle: exit non-zero so `restart: on-failure` retries with backoff.
		fmt.Fprintf(os.Stderr, "wire-bridge: cannot bind %s (from provider %q's anthropic base_url): %v\n",
			route.ListenAddr, route.ProviderName, err)
		return 1
	}
	if err := publishEndpoint(EndpointFile, ln.Addr().String()); err != nil {
		_ = ln.Close()
		fmt.Fprintf(os.Stderr, "wire-bridge: cannot publish %s: %v\n", EndpointFile, err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "wire-bridge: serving provider %q: anthropic on %s → openai %s "+
		"(endpoint %s, credential %s)\n",
		route.ProviderName, ln.Addr().String(), route.UpstreamBaseURL, EndpointFile,
		credentialDescription(route, keySource))

	srv := &http.Server{Handler: handler}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		_ = srv.Close()
		<-errCh
		_ = os.Remove(EndpointFile)
		return 0
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "wire-bridge: server stopped: %v\n", err)
			return 1
		}
		return 0
	}
}

func idleUntilStopped(ctx context.Context) int {
	<-ctx.Done()
	return 0
}

// route is the boot resolution's output: everything the daemon needs to run,
// read ONCE from the composed table and never again (§5: the upstream is never
// taken from request content).
type route struct {
	ProviderName     string
	ListenAddr       string
	UpstreamBaseURL  string
	KeyEnvName       string
	CodexAccessToken bool
}

func credentialDescription(route route, source string) string {
	if route.CodexAccessToken {
		return source
	}
	if route.KeyEnvName == "" {
		return "none"
	}
	return "$" + route.KeyEnvName + " from " + source
}

// resolveRoute is the boot read of the decision: the same inputs Main loaded
// before the extraction, handed to the pure core. It is WillServe's
// (route, reason)-shaped twin — the daemon wants the details and the WHY, the
// launcher wants only the yes/no, and both answers come out of routeFor so the
// two call sites cannot decide differently (wire-bridge.md §5's WARNING).
func resolveRoute(e *entrypoint.Env) (route, string) {
	return routeFor(e.LoadProviders(), useProfilesTable(e.LoadUseProfiles()), e.LoadProfiles())
}

// useProfilesTable lowers YOLO_USE_PROFILES' decoded map to the plain
// agent→profile table the decision reads. A non-string value decodes to "" —
// the same "no profile active here" answer LoadUseProfiles' malformed-input
// path gives, so a corrupt entry idles the bridge instead of guessing.
func useProfilesTable(m *jsonx.OrderedMap) map[string]string {
	out := map[string]string{}
	if m == nil {
		return out
	}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		s, _ := v.(string)
		out[k] = s
	}
	return out
}

// WillServe is THE serve-or-idle decision (wire-bridge.md §5's WARNING): one
// exported pure function over the composed tables, with TWO call sites — the
// daemon's boot (through resolveRoute, for the route and the idle reason) and
// the host launcher's witness registration (internal/cli/run's service
// composition, which emits YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT exactly when this
// returns true). The env var, the endpoint file and the witness probe can never
// disagree because there is nothing here for one call site to decide alone:
// drift is unrepresentable, not prevented.
//
// The inputs are the parsed shapes of exactly what the launcher delivers per-entry
// through the yolo-user-env.sh channel section — YOLO_PROVIDERS, YOLO_USE_PROFILES,
// YOLO_PROFILES, hydrated into the entrypoint's env before this daemon is spawned —
// so a launch whose channel and whose emission were built from different tables
// could not answer differently even in principle.
func WillServe(providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile) bool {
	_, idle := routeFor(providers, useProfiles, resolved)
	return idle == ""
}

// routeFor is the pure core of the decision, and the only place it is made:
// from the composed provider table and the resolved selection, whether this
// boot serves and with what — or why it idles, in a string that names the exact
// absent fact (§3.4). Every idle branch is a HEALTHY outcome, not an error: the
// bridge is staged coarsely whenever a consumer is selected, and "claude rides
// zai today" is the common no-op.
//
// WHO IS SERVED. §3.4 keyed the read on claude alone because claude was the
// only consumer the design knew; the shipped tree outgrew that the day
// cerebras's manifest declared the bridge's loopback URL as its anthropic
// endpoint, because copilot's derive PREFERS the anthropic endpoint of any
// provider that declares one (cerebras-pack-and-copilot-delivery.md D-3 — a
// standing ruling this build will not fork). From that moment the URL in the
// composed table reaches every derive that reads it, so the serve decision
// reads the same table the derives do: EVERY active profile is a candidate,
// and the first one whose provider routes at this jail's loopback with a
// usable upstream is the route. The agent's name is gone from the logic and
// lives only in the idle diagnostics — "the same selection table every agent
// honors" (§3.4's own sentence), taken literally.
//
// Candidates are walked in the use-profiles table's sorted agent order, so the
// route is deterministic; with the shipped packs exactly one provider is ever
// routed at the bridge (cerebras), so every candidate that can serve serves
// the same one. A profile that resolves to no provider, or to one that is not
// routed at the bridge, is a SKIPPED candidate — another agent's may still
// serve — and its reason is what the idle line names if nothing serves.
//
// The listen port is parsed out of the anthropic base_url and nothing else
// (WB-D2/D13): a URL without an explicit port gets its scheme's default,
// because that IS the port the URL names, and the manifest URL stays the one
// writer. The openai endpoint must exist and must speak the chat-completions
// wire when it declares a wire_api at all (WB-D1 — the bridge translates
// exactly one protocol pair; an `openai-responses` endpoint is not this
// bridge's upstream).
//
// The provider entry is read through the `endpoints` map only — the `base_url`
// shorthand is deliberately not consulted. The shorthand is the
// single-protocol spelling whose ambiguity is why composition refuses the pair
// (packdecl.ProviderAddressConflictMessage): a shorthand names no protocol, so
// reading it here would guess which wire it points at.
//
// What is deliberately NOT decided here: the credential. The key is the daemon's
// own boot read (resolveKey, after this returns serve), because the launcher's
// copy of that question — the hydrated env_sources — is a different channel
// from the 0600 file the daemon actually reads. A bridged route with a missing
// key therefore emits the witness env var and then idles unpublished, which the
// witness refuses loudly; the credential preflight upstream already refuses
// that launch in the ordinary case, so the pair only meets through the escape
// hatch, where loud is the point.
func routeFor(providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile) (route, string) {
	agents := make([]string, 0, len(useProfiles))
	for agent := range useProfiles {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	if len(agents) == 0 {
		return route{}, "no profile is active in YOLO_USE_PROFILES — nothing routes at the bridge"
	}

	skip := ""
	for _, agent := range agents {
		profileName := useProfiles[agent]
		providerName := packload.ProviderFor(resolved, profileName)
		if providerName == "" {
			skip = agent + "'s active profile " + profileName +
				" resolves to no provider in YOLO_PROFILES"
			continue
		}
		// `openai-codex` is Pi/Codex's built-in subscription provider, not a
		// composed provider fact. The Claude profile is the only consumer that
		// asks this bridge to translate it; a Pi codex profile must not make an
		// otherwise unused listener appear.
		if agent == "claude" && providerName == "openai-codex" {
			return route{ProviderName: providerName, ListenAddr: CodexResponsesListenAddr,
				UpstreamBaseURL: CodexResponsesBaseURL, CodexAccessToken: true}, ""
		}

		v, ok := providers.Get(providerName)
		if !ok {
			skip = "provider " + providerName + " (active for " + agent + ")" +
				" is not in the composed table (YOLO_PROVIDERS)"
			continue
		}
		entry, isMap := v.(*jsonx.OrderedMap)
		if !isMap {
			skip = "provider " + providerName + "'s table entry is malformed (YOLO_PROVIDERS)"
			continue
		}

		anthropicURL := endpointBaseURL(entry, "anthropic")
		if anthropicURL == "" {
			skip = "provider " + providerName +
				" declares no anthropic endpoint — nothing routes at the bridge"
			continue
		}
		listenAddr, ok := loopbackListenAddr(anthropicURL)
		if !ok {
			skip = "provider " + providerName + "'s anthropic endpoint (" + anthropicURL +
				") is not jail-local — nothing asks this jail's loopback for it"
			continue
		}

		openaiURL := endpointBaseURL(entry, "openai")
		if openaiURL == "" {
			// A provider ROUTED AT the bridge with no upstream is a broken route,
			// not a skipped candidate: report it the moment it is seen.
			return route{}, "provider " + providerName +
				" declares no openai endpoint — the bridge has no upstream to serve (wire-bridge.md §3.4)"
		}
		if wireAPI := endpointField(entry, "openai", "wire_api"); wireAPI != "" && wireAPI != "openai-chat-completions" {
			return route{}, "provider " + providerName + "'s openai endpoint speaks wire_api " + wireAPI +
				" — the bridge translates exactly anthropic ↔ openai-chat-completions (wire-bridge.md WB-D1)"
		}

		return route{
			ProviderName:    providerName,
			ListenAddr:      listenAddr,
			UpstreamBaseURL: openaiURL,
			KeyEnvName:      entryString(entry, "", "api_key_env_name"),
		}, ""
	}
	return route{}, skip
}

// endpointBaseURL reads endpoints.<protocol>.base_url off a composed provider
// entry, "" when any link is absent or malformed. The composed table's shape is
// packload.ComposeProviders'/shippedProviderEntry's output, read verbatim.
func endpointBaseURL(entry *jsonx.OrderedMap, protocol string) string {
	return endpointField(entry, protocol, "base_url")
}

// endpointField reads endpoints.<protocol>.<field> off a composed provider
// entry, "" when any link is absent or malformed.
func endpointField(entry *jsonx.OrderedMap, protocol, field string) string {
	if entry == nil {
		return ""
	}
	v, ok := entry.Get("endpoints")
	if !ok {
		return ""
	}
	endpoints, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return ""
	}
	v, ok = endpoints.Get(protocol)
	if !ok {
		return ""
	}
	protocolEntry, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return ""
	}
	return entryString(protocolEntry, "", field)
}

// entryString walks entry → <via> → key and returns the string at the end of
// the path, "" when any link is absent or not a string. With an empty via it
// reads the entry's own key (base_url and wire_api live one hop down; this
// helper reads the leaf once the caller has walked to the right object).
func entryString(entry *jsonx.OrderedMap, via, key string) string {
	if entry == nil {
		return ""
	}
	m := entry
	if via != "" {
		v, ok := entry.Get(via)
		if !ok {
			return ""
		}
		m, ok = v.(*jsonx.OrderedMap)
		if !ok {
			return ""
		}
	}
	v, ok := m.Get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// loopbackListenAddr turns an anthropic base_url into the tcp address to bind:
// the URL's own host and port, refusing anything that is not this jail's
// loopback (§3.4 — a non-loopback anthropic endpoint is somebody else's route,
// and the bridge binds only what the manifest aims at it). "localhost" binds
// the IPv4 loopback address — the spelling the rest of this repo binds and
// probes (portInUse, svcendpoint's listener) — because the resolver's
// localhost may answer ::1 first, and the jail's claude follows whatever the
// URL's authority resolves to from a 127.0.0.1 listener just the same.
func loopbackListenAddr(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", false
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", false
		}
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), true
}

// userEnvFilePath is where the launcher wrote the hydrated env_sources — the
// 0600 file the key channel reads once at boot (wire-bridge.md §5). Derived
// from the jail home, never spelled absolute: the same file the entrypoint and
// .bashrc read back.
func userEnvFilePath(home string) string {
	return filepath.Join(home, ".config", "yolo-user-env.sh")
}
