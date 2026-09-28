package wirebridged

// auth.go is the bridge's INBOUND authentication (docs/reference/wire-bridge.md, WB-D18): every
// request on every listener must carry this launch's CALLER TOKEN, and one that does not is
// refused 401 before any route sees it.
//
// WHY A JAIL-LOCAL DAEMON NEEDS IT. WB-D4 ruled inbound auth out because "the jail is the
// boundary". The maintainer's ruling of 2026-09-27 answers that premise: "calling the jail the
// boundary here seems also just as bad for security because jails don't need to be bridge type,
// they can be house type and then um it's identical." A jail on `network.mode: host`, a
// macos-user sandbox and a nested podman forced onto `--net=host` all share the host's loopback.
// There the bridge's port is one every host process can reach, so an unauthenticated bridge
// spends the user's provider keys and ChatGPT subscription for anyone; and a port the bridge does
// not hold yet is one a host process can take first, so a client that sends its real credential
// there hands it over. Claude sends its saved login's OAuth bearer to whatever
// ANTHROPIC_BASE_URL names when no ANTHROPIC_AUTH_TOKEN is set (agent-auth-modes.md §8.1).
//
// THE TOKEN. The launcher mints it per launch (crypto/rand, 256 bits), an attach reuses the
// running jail's, and it reaches this daemon through the same 0600 per-entry channel the
// provider tables cross (CallerTokenEnv), so the daemon reads it where it reads everything else
// it serves by. Every client a derive points here sends it in the header that client already
// sends a key in: claude's ANTHROPIC_AUTH_TOKEN and the OpenAI clients' api key as
// `Authorization: Bearer`, and `x-api-key` for a client that speaks Anthropic's SDK form. So the
// bridge accepts either header, compares in constant time, and never forwards either upstream:
// every upstream request is built fresh (doUpstream, passthroughHandler.do), copying no inbound
// credential.
//
// A REFUSAL IS A CLEAR 401, in the shape the listener's clients read: Anthropic's on the adapter
// routes, OpenAI's on the via address. It says whether the token was missing or wrong, which
// tells a caller nothing it could use, and the log line names the request and the reason and
// never a presented value.

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// CallerTokenEnv is the variable carrying the bridge's per-launch caller token, in this
// daemon's environment (hydrated from the per-entry channel) and in every bridged agent's. It
// is composed, never spelled: the launcher that mints it and the pack composition that names
// it as the bridged addresses' credential spell it through the same function.
var CallerTokenEnv = paths.ServiceCallerTokenEnv(ServiceName)

// callerToken reads and checks the token this daemon demands. The format is the launcher's
// (svcendpoint.NewToken: 64 lowercase hex, 256 bits), so anything else is a launcher and a
// daemon that disagree, which the caller refuses to serve over rather than guess past.
func callerToken(getenv func(string) string) (string, string) {
	tok := getenv(CallerTokenEnv)
	if tok == "" {
		return "", "this launch handed the bridge no caller token ($" + CallerTokenEnv + " is " +
			"unset), and the bridge serves no caller it cannot authenticate (wire-bridge.md WB-D18) — " +
			"a launcher older than the jail's binaries is the usual cause"
	}
	if !svcendpoint.IsToken(tok) {
		return "", "$" + CallerTokenEnv + " is not a caller token this launcher mints (64 lowercase " +
			"hex characters), so the bridge will not serve behind it (wire-bridge.md WB-D18)"
	}
	return tok, ""
}

// callerAuth refuses every request that does not carry token, and hands the rest to next.
type callerAuth struct {
	token  string
	next   http.Handler
	refuse func(w http.ResponseWriter, message string)
	// what names the listener for the log line.
	what string
}

// requireAnthropicCaller guards an adapter route, whose clients read Anthropic's error shape.
func requireAnthropicCaller(token, what string, next http.Handler) http.Handler {
	return callerAuth{token: token, next: next, what: what, refuse: func(w http.ResponseWriter, message string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(anthropicErrorJSON("authentication_error", message)))
	}}
}

// requireOpenAICaller guards the via address, whose clients read OpenAI's error shape.
func requireOpenAICaller(token, what string, next http.Handler) http.Handler {
	return callerAuth{token: token, next: next, what: what, refuse: func(w http.ResponseWriter, message string) {
		writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", message)
	}}
}

// The two refusal reasons, which differ only in what a caller that got one should check.
const (
	callerTokenMissing = "missing"
	callerTokenWrong   = "wrong"
)

func (a callerAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reason := a.check(r)
	if reason == "" {
		a.next.ServeHTTP(w, r)
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="wire-bridge"`)
	a.refuse(w, callerRefusalMessage(reason))
	// The request line the handlers write for a served request, with the reason beside it.
	// Never a presented value: a wrong token may be somebody's real credential sent to the
	// wrong port.
	logf("%s %s %d — refused on %s: caller token %s (the request reached no route and nothing "+
		"was sent upstream)", r.Method, r.URL.Path, http.StatusUnauthorized, a.what, reason)
}

// check returns "" when some credential the request presents is the token, else why not.
func (a callerAuth) check(r *http.Request) string {
	presented := presentedCredentials(r)
	if len(presented) == 0 {
		return callerTokenMissing
	}
	for _, p := range presented {
		if subtle.ConstantTimeCompare([]byte(p), []byte(a.token)) == 1 {
			return ""
		}
	}
	return callerTokenWrong
}

// presentedCredentials is every credential the request carries in a header a bridged client
// sends one in: `Authorization: Bearer <t>` (the scheme case-insensitively, as RFC 9110 has it)
// and `x-api-key: <t>`. Each header may appear more than once; every value is offered.
func presentedCredentials(r *http.Request) []string {
	var out []string
	for _, v := range r.Header.Values("Authorization") {
		scheme, cred, ok := strings.Cut(strings.TrimSpace(v), " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			if cred = strings.TrimSpace(cred); cred != "" {
				out = append(out, cred)
			}
		}
	}
	for _, v := range r.Header.Values("X-Api-Key") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// callerRefusalMessage is the 401's body text.
func callerRefusalMessage(reason string) string {
	what := "carried no caller token"
	if reason == callerTokenWrong {
		what = "carried a caller token that is not this launch's"
	}
	return fmt.Sprintf("wire-bridge: refused — this request %s. The bridge serves only its own "+
		"launch's agents, which send the launch's token as `Authorization: Bearer <token>` or "+
		"`x-api-key: <token>` from $%s; a jail sharing the host's loopback makes this port "+
		"reachable from outside the jail (wire-bridge.md WB-D18). An agent that gets this was "+
		"started without the launch's environment, or by an older yolo: restart it from a fresh "+
		"`yolo` entry", what, CallerTokenEnv)
}
