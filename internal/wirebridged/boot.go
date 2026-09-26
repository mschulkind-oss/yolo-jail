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
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridge"
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
// (docs/reference/protocol-resolution.md#the-four-outcomes). openai-codex remains
// CREDENTIAL-free in YOLO_PROVIDERS — its row names no key variable, and this daemon's
// Codex route takes its access-token view from openai-auth, never from a generated file;
// what the row carries is the public Responses ADDRESS, which is the upstream below.
//
// The route selection reads the TABLE and keeps this constant as its DEFAULT: routeFor's
// Codex branch binds the composed entry's `endpoints.anthropic.base_url` when there is
// one, and this value only when there is not. It used to bind this constant outright,
// ahead of any table read, which meant a user-scope `adapters` override for that pair
// moved claude's ANTHROPIC_BASE_URL and not the bind — the agent dialing one address
// while the daemon listened on another. So this is a declaration and a fallback, never a
// second writer of the port.
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
	ctx, stop := daemonContext()
	defer stop()
	return run(ctx, entrypoint.EnvFromOS(), entryChannelPollInterval)
}

// daemonContext is the context the daemon idles on, and it exists because
// context.Background() CANNOT BE IDLED ON.
//
// Background's Done() returns a NIL channel. A receive on nil blocks forever, and
// when it is the only goroutine the Go runtime kills the process:
//
//	fatal error: all goroutines are asleep - deadlock!
//	goroutine 1 [chan receive (nil chan)]:
//	  wirebridged.idleUntilStopped(...)
//
// So every HEALTHY IDLE — the state this package's own doc comment calls "bind
// nothing, publish nothing, sleep forever, one stderr line saying why" — crashed
// instead. MEASURED 2026-09-19 in a jail whose selected provider named a credential
// variable that was not set: the bridge printed its idle line and panicked, and the
// supervisor was left restarting a daemon that could never stay up.
//
// Cancelling on SIGINT/SIGTERM is the fix AND the behaviour a supervised daemon
// wants anyway: `yolo-jaild supervise` stops a child by signalling it, and until now
// an idling bridge had no path from that signal to a clean exit.
func daemonContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// run resolves the boot route, then watches the live per-entry channel while
// idle. An attach runs a new agent entry but does not restart the supervisor,
// so this is the one path by which a selection-lazy daemon can observe a later
// selection. Once serving, its upstream remains fixed for the daemon lifetime:
// a jail may have concurrent entries, and letting the latest attach replace a
// listener under an earlier entry would redirect that entry's traffic.
func run(ctx context.Context, initial *entrypoint.Env, pollInterval time.Duration) int {
	p, e, ok := waitForActivePlan(ctx, initial, pollInterval)
	if !ok {
		return 0
	}
	return servePlan(ctx, p, e)
}

// plan is everything one boot serves (OQ-WG7): the adapter route on its own port, when
// one is selected, and the via routes on the declared via address. Either may be absent;
// a plan with neither is an idle.
type plan struct {
	adapter    *route
	adapterWhy string // why no adapter route, when adapter is nil
	via        viaPlan
}

func (p plan) serves() bool { return p.adapter != nil || len(p.via.Routes) > 0 }

// idleReason is the whole plan's idle line: the adapter's reason, then each skipped via
// profile's, so an almost-working via selection is named rather than hidden behind the
// adapter's "nothing routes at the bridge".
func (p plan) idleReason() string {
	parts := []string{p.adapterWhy}
	parts = append(parts, p.via.Skipped...)
	return strings.Join(parts, "; ")
}

// resolvePlan is the boot read of the whole decision.
func resolvePlan(e *entrypoint.Env) plan {
	return planFor(e.LoadProviders(), useProfilesTable(e.LoadUseProfiles()), e.LoadProfiles())
}

// planFor is the pure core both call sites share (WillServe, the daemon's boot).
func planFor(providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile) plan {
	var p plan
	if rt, why := routeFor(providers, useProfiles, resolved); why == "" {
		p.adapter = &rt
	} else {
		p.adapterWhy = why
	}
	p.via = viaRoutesFor(providers, useProfiles, resolved)
	return p
}

func waitForActivePlan(ctx context.Context, initial *entrypoint.Env, pollInterval time.Duration) (plan, *entrypoint.Env, bool) {
	if selected := resolvePlan(initial); selected.serves() {
		return selected, initial, true
	} else {
		idleReason := selected.idleReason()
		logIdle(idleReason)
		// A boot that registered this endpoint has already established that a
		// route must exist. Waiting for a later attach in that state would make
		// PID 1 wait forever on a contradiction between the launcher and this
		// daemon. Report the contradiction through the readiness pipe instead;
		// ordinary selection-lazy daemon lifetime still waits for attaches below.
		if readinessRequested() {
			signalNotReady(ServiceName, idleReason)
			return plan{}, nil, false
		}
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return plan{}, nil, false
		case <-ticker.C:
			candidate := cloneEnv(initial)
			if !entrypoint.HydrateEntryChannel(candidate) {
				// The one input this loop has is unreadable, so the wait can
				// never end — an idle that is structurally permanent rather than
				// pending. Once, not per tick: the answer is the same five times
				// a second until the file appears, and it self-corrects (the key
				// is never re-tried, but the success path below is what a reader
				// sees next).
				logOnce("channel-unreadable", "the per-entry channel %s cannot be read, so this "+
					"idle bridge cannot observe a later attach; it will keep polling and will "+
					"pick the file up if it appears", userEnvFilePath(candidate.Home))
				continue
			}
			next := resolvePlan(candidate)
			if next.serves() {
				return next, candidate, true
			}
			idleReason := next.idleReason()
			// An attach rewrote the channel and this bridge STILL idles. That is
			// a legitimate no-op, but a silent one used to make an
			// almost-working selection indistinguishable from an unnoticed
			// attach. Keyed by the reason, so a changed answer is reported and an
			// unchanged one is not repeated.
			logIdle(idleReason)
		}
	}
}

// logIdle reports a serve-or-idle answer of "idle", at most once per distinct
// reason: resolveRoute is re-evaluated every poll tick for the daemon's whole
// lifetime, so the per-reason key is what separates "a new fact" from "the same
// fact, 18,000 times".
func logIdle(reason string) {
	logOnce("idle:"+reason, "idling: %s", reason)
}

func cloneEnv(e *entrypoint.Env) *entrypoint.Env {
	vars := make(map[string]string, len(e.Vars))
	for key, value := range e.Vars {
		vars[key] = value
	}
	return entrypoint.NewEnv(vars)
}

func serve(ctx context.Context, route route, e *entrypoint.Env) int {
	return servePlan(ctx, plan{adapter: &route}, e)
}

// adapterHandler builds the adapter route's handler and describes its credential, or
// returns why it cannot serve (a missing credential): the bridge never serves
// unauthenticated upstream traffic (wire-bridge.md §5).
func adapterHandler(route route, e *entrypoint.Env) (http.Handler, string, string) {
	if route.CodexAccessToken {
		endpoint := e.Getenv(openauthclient.EndpointEnv)
		if endpoint == "" {
			return nil, "", "Codex route needs " + openauthclient.EndpointEnv +
				"; no unauthenticated upstream is served"
		}
		return NewCodexResponsesHandler(route.UpstreamBaseURL, endpoint),
			"OpenAI credential service access-token views", ""
	}
	if route.SignRegion != "" {
		// A Bedrock upstream: the credential chain is snapshotted from the key channel
		// now and resolved lazily per request (signing.go). A jail with no source at
		// all idles, as a missing key does: the bridge never serves unauthenticated.
		env := sigv4.EnvFrom(func(name string) string {
			v, _ := resolveKey(name, e.Home, route.Agent)
			return v
		})
		if !hasAWSCredentialSource(env) {
			return nil, "", fmt.Sprintf("provider %q's upstream is Bedrock (%s), and none of its "+
				"credential sources is set — a static AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY pair, "+
				"the aws-auth pointer AWS_CONTAINER_CREDENTIALS_FULL_URI, or AWS_BEARER_TOKEN_BEDROCK — "+
				"in %s or this process's environment", route.ProviderName, route.UpstreamBaseURL,
				keyChannelDescription(e.Home, route.Agent))
		}
		return newSignedChatHandler(route.UpstreamBaseURL,
				wirebridge.ChatOptions{OmitStreamUsage: route.OmitStreamUsage},
				&bedrockSigner{region: route.SignRegion, chain: &sigv4.Chain{Env: env}}),
			"SigV4 for bedrock in " + route.SignRegion + ", from " + env.String(), ""
	}
	key, keySource := resolveKey(route.KeyEnvName, e.Home, route.Agent)
	if key == "" && route.KeyEnvName != "" {
		return nil, "", fmt.Sprintf("provider %q names credential variable %s, and it is set "+
			"neither in %s nor in this process's environment — the bridge never serves "+
			"unauthenticated upstream traffic (wire-bridge.md §5)",
			route.ProviderName, route.KeyEnvName, keyChannelDescription(e.Home, route.Agent))
	}
	return newChatHandler(route.UpstreamBaseURL, key,
		wirebridge.ChatOptions{OmitStreamUsage: route.OmitStreamUsage}), keySource, ""
}

// listener is one bound address and what it serves.
type listener struct {
	addr    string
	what    string // for the log: "provider \"x\" (from its anthropic base_url)" or "via routes"
	handler http.Handler
	ln      net.Listener
}

// servePlan serves every route in p (OQ-WG7): the adapter route on its own port and the
// via routes on the via address, one listener each. A route that cannot serve (no
// credential) is dropped while another serves; a plan left with nothing to serve idles.
func servePlan(ctx context.Context, p plan, e *entrypoint.Env) int {
	var ls []*listener
	var serving []string
	if p.adapter != nil {
		route := *p.adapter
		handler, keySource, why := adapterHandler(route, e)
		switch {
		case why != "" && len(p.via.Routes) == 0:
			logf("idling: %s", why)
			if route.CodexAccessToken {
				signalNotReady(ServiceName, "Codex upstream credential endpoint is unavailable")
			} else if route.SignRegion != "" {
				signalNotReady(ServiceName, "Bedrock upstream has no credential source")
			} else {
				signalNotReady(ServiceName, "provider credential is unavailable")
			}
			return idleUntilStopped(ctx)
		case why != "":
			logf("the adapter route for provider %q does not serve: %s — the via routes still do",
				route.ProviderName, why)
		default:
			ls = append(ls, &listener{addr: route.ListenAddr, handler: handler,
				what: fmt.Sprintf("provider %q (from its anthropic base_url)", route.ProviderName)})
			serving = append(serving, fmt.Sprintf("provider %q: anthropic on {addr} → openai %s (endpoint {endpoint}, credential %s)",
				route.ProviderName, route.UpstreamBaseURL, credentialDescription(route, keySource)))
		}
	}
	if len(p.via.Routes) > 0 {
		handler, lines := viaHandlerFor(p.via, e.Home)
		ls = append(ls, &listener{addr: p.via.ListenAddr, handler: handler, what: "via routes"})
		serving = append(serving, "via routes on {addr} (endpoint {endpoint}): "+strings.Join(lines, "; "))
		for _, skip := range p.via.Skipped {
			logf("a via profile is not served: %s", skip)
		}
	}

	// BIND BEFORE PUBLISH (§5): the endpoint file's appearance is the promise
	// that a listener exists at the address it names.
	//
	// The attempt is announced BEFORE it is made, and that ordering is the point:
	// the 8214 collision was diagnosed against a log in which this daemon had said
	// nothing at all, so "the bridge got as far as trying to bind X" was itself an
	// unavailable fact. A line here also survives the case an error string cannot
	// reach — a bind that hangs, or a process killed between these two statements.
	for _, l := range ls {
		logf("binding %s for %s", l.addr, l.what)
		ln, err := net.Listen("tcp", l.addr)
		if err != nil {
			// A port the manifest URL names that something else holds is a real
			// fault (WB-D13: the URL is the single source of the port), not an
			// idle. A boot waiting on this daemon must hear that result immediately;
			// otherwise supervisor's intentional retry loop turns an actionable bind
			// conflict into a terminal with no new output.
			//
			// THREE FACTS, ALWAYS, because the first two alone are what made this
			// class cost four wrong hypotheses: the address tried, the syscall error
			// verbatim (net.OpError already carries "listen tcp <addr>: bind: …"),
			// and WHO HOLDS THE PORT (portholder.go, which asks /proc because the
			// host-side instrument cannot see a jail-side listener).
			holder := describePortHolder(l.addr)
			logf("cannot bind %s for %s: %v — %s", l.addr, l.what, err, holder)
			signalNotReady(ServiceName, "cannot bind "+l.addr+": "+err.Error()+" — "+holder)
			for _, done := range ls {
				if done.ln != nil {
					_ = done.ln.Close()
				}
			}
			return 1
		}
		l.ln = ln
	}
	// The endpoint file names the FIRST listener (the adapter route's when there is one):
	// its contract is "a listener of this daemon exists at the address inside", which the
	// reachability witness probes, and any one bound listener proves the daemon is up.
	if err := publishEndpoint(EndpointFile, ls[0].ln.Addr().String()); err != nil {
		// The listeners are abandoned with the process, one statement from now: the
		// only reason to close them explicitly is the in-process test that asserts
		// the port is free again, and a Close error on a listener nobody will
		// accept on has no consumer and no remedy.
		for _, l := range ls {
			_ = l.ln.Close()
		}
		logf("cannot publish %s (the §5 marker for the live listener on %s): %v",
			EndpointFile, ls[0].ln.Addr().String(), err)
		signalNotReady(ServiceName, "cannot publish endpoint "+EndpointFile+": "+err.Error())
		return 1
	}
	signalReady(ServiceName)
	for i, line := range serving {
		line = strings.ReplaceAll(line, "{addr}", ls[i].ln.Addr().String())
		logf("serving %s", strings.ReplaceAll(line, "{endpoint}", EndpointFile))
	}

	servers := make([]*http.Server, len(ls))
	errCh := make(chan error, len(ls))
	for i, l := range ls {
		servers[i] = &http.Server{Handler: l.handler}
		go func(srv *http.Server, ln net.Listener) { errCh <- srv.Serve(ln) }(servers[i], l.ln)
	}
	select {
	case <-ctx.Done():
		logf("stopping: the daemon's context is done (the supervisor signalled, or the jail is retiring)")
		// Close's error is the first failure closing the listener we are
		// discarding anyway, on the way out of a process that is about to exit;
		// it names nothing a reader could act on. The Serve error below IS
		// reported, and it is the one that can say something.
		for _, srv := range servers {
			_ = srv.Close()
		}
		for range servers {
			if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
				logf("a listener stopped with an error while shutting down: %v", err)
			}
		}
		// A STALE ENDPOINT FILE IS A LIE: the file's whole contract is "a
		// listener exists at the address inside" (§5), so one that outlives the
		// listener points the next reader — and the reachability witness — at a
		// closed port. An absent file is the expected case only if something
		// else removed it first.
		if err := os.Remove(EndpointFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			logf("could not remove the endpoint file %s on shutdown: %v — it now names a "+
				"listener that no longer exists", EndpointFile, err)
		}
		return 0
	case err := <-errCh:
		for _, srv := range servers {
			_ = srv.Close()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			logf("server stopped: %v", err)
			return 1
		}
		return 0
	}
}

// signalReady acknowledges the boot dependency only after publishEndpoint has
// made the listener discoverable. The descriptor is absent for ordinary daemon
// lifetime and attach paths; a failed acknowledgement means the entrypoint has
// already gone away, so it must not take down a healthy bridge.
func signalReady(name string) {
	signalReadiness("ready", name)
}

func signalNotReady(name, reason string) {
	signalReadiness("failed", name, reason)
}

// readinessRequested and signalReadiness must agree about what a usable
// descriptor is, and they did not: readinessRequested accepted any integer while
// signalReadiness silently refused anything under 3. With the variable set to
// "0", "1" or "2" the daemon therefore took the "a boot is waiting on me" branch
// and then wrote its answer NOWHERE — and the boot waiting on the pipe hangs
// until its own deadline with no line in any log. One parse, reported once, for
// both.
func readinessFD() (int, bool) {
	raw := os.Getenv(paths.JailDaemonReadyFDEnv)
	if raw == "" {
		return 0, false
	}
	fd, err := strconv.Atoi(raw)
	if err != nil || fd < 3 {
		logOnce("readiness-fd:"+raw, "%s is set to %q, which is not a descriptor this daemon can "+
			"write to (an inherited pipe is 3 or above), so no readiness answer can be sent — a "+
			"boot waiting on this service will wait out its own deadline instead",
			paths.JailDaemonReadyFDEnv, raw)
		return 0, false
	}
	return fd, true
}

func readinessRequested() bool {
	_, ok := readinessFD()
	return ok
}

func signalReadiness(kind, name string, details ...string) {
	fd, ok := readinessFD()
	if !ok {
		return
	}
	signalReadinessOnFD(fd, kind, name, details...)
}

func signalReadyOnFD(fd int, name string) {
	signalReadinessOnFD(fd, "ready", name)
}

// signalReadinessOnFD writes ONE line to the inherited readiness pipe.
//
// The detail is flattened to a single line (oneLine) because the protocol is
// line-framed: the entrypoint's scanner reads each line as a record and rejects
// any whose first field is not ready/failed, so a newline inside a bind
// diagnostic — the port holder's argv is arbitrary process input — would turn a
// precise failure into "jail daemon reported unexpected readiness".
//
// DUP RATHER THAN TAKE. os.NewFile TAKES OWNERSHIP of the descriptor it is handed
// and arms a finalizer that CLOSES it, so wrapping the INHERITED fd hands the
// boot's readiness pipe to the garbage collector; a second call would mint a
// second owner of the same number, and a collection after the kernel recycled it
// closes an unrelated file (a log, a socket) as an EBADF nowhere near here. The
// supervisor fixed exactly this shape on its own side of the same pipe; this was
// the other half of it.
func signalReadinessOnFD(fd int, kind, name string, details ...string) {
	message := kind + " " + name
	if len(details) > 0 {
		message += " " + oneLine(strings.Join(details, " "))
	}
	dup, err := syscall.Dup(fd)
	if err != nil {
		logf("cannot duplicate the readiness descriptor %d to answer %q: %v — the boot waiting "+
			"on this service will not hear it", fd, message, err)
		return
	}
	pipe := os.NewFile(uintptr(dup), "jail-daemon-ready")
	defer pipe.Close()
	if _, err := fmt.Fprintln(pipe, message); err != nil {
		// A failed acknowledgement must not take down a healthy bridge — by the
		// time it fails the entrypoint has usually already stopped waiting — but
		// it must not be invisible either: an unwritten "failed" line is a boot
		// that stalls for a reason recorded nowhere, which is this whole file's
		// defect class.
		logf("could not write readiness %q to fd %d: %v", message, fd, err)
	}
}

// idleUntilStopped is the healthy-idle path: no listener, no endpoint file, and a
// wait that ends only when the supervisor signals.
//
// ⚠ IT IS ONLY SAFE FOR A CONTEXT THAT CAN BE DONE. Handed context.Background() it
// deadlocks the process rather than idling — see daemonContext, which is the one
// place the daemon's context is built and the reason this is now sound.
func idleUntilStopped(ctx context.Context) int {
	if ctx.Done() == nil {
		// Defence in depth for a future caller that reaches here with a
		// non-cancellable context: a daemon that cannot be stopped is still better
		// than one the runtime kills, and the log line says which happened.
		logf("idling on a context that can never be done; blocking without a stop path " +
			"(daemonContext is the supported one)")
		select {}
	}
	<-ctx.Done()
	return 0
}

// route is the boot resolution's output: everything the daemon needs to run,
// read ONCE from the composed table and never again (§5: the upstream is never
// taken from request content).
type route struct {
	// Agent is the agent whose profile selected this route's provider — whose own env
	// file holds the provider's credential since the credential gate (keyfile.go).
	Agent            string
	ProviderName     string
	ListenAddr       string
	UpstreamBaseURL  string
	KeyEnvName       string
	CodexAccessToken bool
	// OmitStreamUsage is the chat-completions route's one request-shape fact:
	// the selected profile's supports_usage_in_streaming option (the provider's
	// declared default, or the user's value over it) is "false", so a streamed
	// request must not carry stream_options. It is the same service fact pi's
	// derive reads as supportsUsageInStreaming (packs/pi/derive.lua), read here
	// by the same rule: only the JSON spelling "false" turns it off, and an
	// absent or unrecognized value keeps the default, which asks for usage.
	OmitStreamUsage bool
	// SignRegion is set when the upstream's host is bedrock-runtime.<region>.amazonaws.com
	// (sigv4.BedrockRuntimeRegion): the route signs every request with SigV4 for that
	// region instead of carrying a bearer key (signing.go; OQ-WG1 keys the decision on
	// the host). Empty for every other upstream.
	SignRegion string
}

// streamUsageOption is the provider option that states whether an upstream
// accepts stream_options.include_usage (packs/llamacpp/README.md, "The compat
// facts").
const streamUsageOption = "supports_usage_in_streaming"

func credentialDescription(route route, source string) string {
	if route.CodexAccessToken || route.SignRegion != "" {
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
//
// AND IT SAYS SO. The dropped value used to leave the daemon idling with
// `<agent>'s active profile  resolves to no provider` — an empty profile name in
// the middle of a sentence, which reads like a bug in the message rather than
// like malformed input in the channel. logOnce, not logf: resolveRoute is
// re-evaluated every poll tick for the daemon's whole idle lifetime, and the fact
// does not change while the channel does not.
func useProfilesTable(m *jsonx.OrderedMap) map[string]string {
	out := map[string]string{}
	if m == nil {
		return out
	}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		s, isString := v.(string)
		if !isString && v != nil {
			logOnce("use-profiles-nonstring:"+k, "YOLO_USE_PROFILES entry %q is a %T, not a "+
				"profile name; reading it as \"no profile active for %s\" rather than guessing — "+
				"the bridge will idle unless another agent's selection routes here", k, v, k)
		}
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
	return planFor(providers, useProfiles, resolved).serves()
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
		// `openai-codex` is the subscription source whose upstream this daemon reaches
		// through openai-auth's token view rather than a key file, and the Claude
		// profile is the only consumer that asks this bridge to translate it: a Pi codex
		// profile speaks Responses natively and must not make an otherwise unused
		// listener appear. That agent test is the whole reason the branch exists.
		//
		// THE LISTEN ADDRESS COMES OFF THE COMPOSED ENTRY, exactly as the general route
		// below takes its own. This branch used to return here without reading the table
		// at all, on the premise that openai-codex was "Pi/Codex's built-in subscription
		// provider, not a composed provider fact" — true when written, false since
		// packs/openai-auth gave the provider the public Responses endpoint. From that
		// moment packs/wire-bridge's `openai-responses → anthropic` adaptation fronts it,
		// so the composed entry carries an `endpoints.anthropic.base_url` like every
		// other bridged provider, and packs/claude's env derive builds ANTHROPIC_BASE_URL
		// out of that entry and nothing else. A user-scope `adapters` override for that
		// pair therefore moved the agent's URL while the constant held the bind where it
		// was: claude dialed an address nobody was listening on and every request was
		// refused. Leaving the expired premise in the comment is how that comes back.
		//
		// THE ADAPTER'S DECLARED ADDRESS AND THE PROVIDER ENTRY'S COINCIDE here, as they
		// do for the general route: packload.adaptEndpoints writes the adapter's address
		// — or the user's override of it, the one field an override may set — into
		// endpoints.anthropic.base_url, so reading the entry IS reading the adaptation,
		// with the override already applied. It is the spelling that cannot drift.
		//
		// CodexResponsesListenAddr survives as the DEFAULT for an entry that supplies
		// nothing, so a jail whose table never composed the adaptation binds exactly what
		// it bound before — a declaration used as a default, not a second writer. A
		// composed endpoint that is NOT jail-local skips the candidate instead, for the
		// same reason the general route skips one: it is somebody else's route, and
		// falling back to this constant there would be this same defect at a different
		// address. The nil check is the empty composed table ComposeProviders encodes as
		// a nil map (orderedOrNil), which WillServe's launcher call site hands over as-is.
		if agent == "claude" && providerName == "openai-codex" {
			listenAddr := CodexResponsesListenAddr
			if providers != nil {
				v, _ := providers.Get(providerName)
				if composed, isMap := v.(*jsonx.OrderedMap); isMap {
					if anthropicURL := endpointBaseURL(composed, "anthropic"); anthropicURL != "" {
						addr, jailLocal := loopbackListenAddr(anthropicURL)
						if !jailLocal {
							skip = "provider " + providerName + "'s anthropic endpoint (" + anthropicURL +
								") is not jail-local — nothing asks this jail's loopback for it"
							continue
						}
						listenAddr = addr
					}
				}
			}
			return route{Agent: agent, ProviderName: providerName, ListenAddr: listenAddr,
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
			Agent:           agent,
			ProviderName:    providerName,
			ListenAddr:      listenAddr,
			UpstreamBaseURL: openaiURL,
			// The ONE variable the provider points at (packload.KeyEnvName): a provider
			// listing several credential variables (OQ-CN1) points at none, and a Bedrock
			// upstream signs from the AWS names instead (SignRegion).
			KeyEnvName:      packload.KeyEnvName(entry),
			OmitStreamUsage: resolved[profileName].Options[streamUsageOption] == "false",
			SignRegion:      bedrockSignRegion(openaiURL),
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
