// Package awscredadapter speaks the AWS container-credentials protocol on the
// jail's loopback and forwards every request to yolo's host `aws-auth` credential
// service. It is the JAIL half of the SSO-backed Bedrock channel
// (docs/reference/agent-credentials.md#sso-backed-bedrock-credentials-aws-auth); the host
// half is internal/awsauth (the mint) wrapped by internal/awsauthdaemon (the service).
//
// # It is a pass-through, and that is the design rather than an economy
//
// The host handler's JSON *is* the container-credentials body — four keys on
// success, `Code` and `Message` on failure (internal/awsauthdaemon's BuildHandler
// says so at length). So this package decides exactly two things: the HTTP status,
// and whether the body is well-formed enough to hand an SDK. It never renames a
// field, never reformats a time and never composes a credential, which is what
// keeps the `SessionToken` → `Token` rename to one place in the tree
// (awsauth.Credential.ContainerCredentials) instead of two spellings that can drift.
//
// # EVERY FAILURE IS A 4xx, INCLUDING THE ONES THAT ARE NOT THE CALLER'S FAULT
//
// The protocol carries a message on exactly one class of status: design §5's table
// reads "4xx with {Code, Message}, both surfaced on the error the SDK raises; any
// other status is a bare failure". A 502 for "the host service is unreachable" is
// more honest about whose fault it is and it DELETES the message on the way out —
// the human then sees a bare provider error instead of the sentence naming what to
// fix. OQ-SSO6 is the whole reason this adapter exists in this shape ("a lapsed
// session is a MESSAGE the jail reports"), so the message wins over the status
// semantics, deliberately: every refusal below names a 4xx, and the test that
// enumerates them is what keeps a later "more correct" 5xx from deleting the
// sentence a human needs.
//
// # Every request must carry this launch's caller token
//
// This package used to say "there is no authorization token, and adding one would buy
// nothing", because everything that could read a token could already reach the port.
// That is true only inside a private network namespace, and RETIRED
// (docs/plans/notch-convergence.md §2.3, NC-D2): a jail on `network.mode: host` puts this
// port on the host's real loopback, and a nested podman forced onto `--net=host` shares
// its parent jail's, so host processes and sibling jails can reach it without being able
// to read the jail agent's environment or files. The token is the whole difference there.
//
// So the launcher mints a caller token for this daemon (the loophole declares
// `jail_daemon.caller_token`), and the token is SCOPED to the agents that selected what this
// adapter serves (docs/reference/providers.md OQ-CN7 (c), ruled 2026-09-28): the
// pack's `env` contribution names it as AWS_CONTAINER_AUTHORIZATION_TOKEN = `{caller_token}`
// beside the credentials URI, gated on the provider platform `aws-bedrock` (OQ-BR8), so the
// launcher exports it only in the env file of each agent whose selected provider is Bedrock.
// That is the slot the AWS SDKs' container-credentials provider already has: it sends the
// variable's value verbatim as
// `Authorization` on every fetch (when no AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE is set, which
// the SDKs would prefer). The adapter itself reads the token from YOLO_SERVICE_AWS_AUTH_TOKEN
// when a launch exported it, and otherwise from the unexported record the launcher keeps in
// the shared yolo-user-env.sh's channel section (callerTokenLookup). A request without the
// token is refused 401 in the protocol's own {Code, Message} shape, before the host is asked.
// What it proves: the caller's environment carries the selecting agent's token — no other
// process's environment does, a bare shell's included — or the caller read a same-uid file
// that holds it, which inside the jail any process can; outside it no process can.
package awscredadapter

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// CredentialsPath is the path AWS_CONTAINER_CREDENTIALS_FULL_URI names. The pack's
// `env` contribution writes the whole URI, so this constant and that value are one
// fact in two files; the pack's README says so where the variable is declared.
const CredentialsPath = "/credentials"

// DefaultListen is this adapter's fixed jail-loopback address. The port is
// adjacent to the OpenAI adapter's 1460 because they are the same kind of thing —
// a credential endpoint an agent's own client library dials — and the wire bridge's
// ports are a different family: an ADAPTED WIRE rather than a credential.
//
// ⚠ The bridge's ports are declared by ONE pack now, and this comment used to
// misattribute all three. They are `packs/wire-bridge/pack.json`'s two `adapter`
// contributions — 8214 for `openai → anthropic` and 8215 for
// `openai-responses → anthropic` — not a port per provider. A third, 8216, was
// eliminated rather than relocated when `packs/kilo` stopped hand-writing an
// anthropic endpoint and started sharing the single `openai → anthropic`
// adaptation (docs/reference/protocol-resolution.md).
const DefaultListen = "127.0.0.1:1461"

// Fetch asks the host credential service for one container-credentials body.
//
// IT TAKES NO CONTEXT, which is a decision rather than an omission. The only
// deadline worth honouring here is the SDK's own (1000 ms, three attempts), and
// cancelling the hop on it would throw away the mint the host has already started:
// the daemon mints under context.Background() and caches the result, so a request
// the SDK gave up on still warms the cache the retry hits. A cancellation plumbed
// through here would make the first attempt useless instead of merely late.
type Fetch func() (Answer, error)

// Handler is the loopback endpoint an AWS SDK's container-credentials provider
// dials, serving only a request whose `Authorization` is callerToken (the package
// comment says why).
func Handler(callerToken string, fetch Fetch) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if reason := callerCheck(r, callerToken); reason != "" {
			// Before the path and method checks, so an unauthenticated caller learns nothing
			// about what is served here. Never the presented value, in the log or the body.
			fmt.Fprintf(os.Stderr, "aws-credential-adapter: %s %s 401 — refused: caller token %s "+
				"(the host service was not asked)\n", r.Method, r.URL.Path, reason)
			writeError(w, http.StatusUnauthorized, "CallerUnauthenticated", callerRefusalMessage(reason))
			return
		}
		if r.URL.Path != CredentialsPath {
			writeError(w, http.StatusNotFound, "InvalidRequest",
				"no credentials are served at "+r.URL.Path+" — this adapter serves "+
					CredentialsPath+", which is what AWS_CONTAINER_CREDENTIALS_FULL_URI names")
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "InvalidRequest",
				"the container-credentials protocol is one GET with no body; got "+r.Method)
			return
		}
		answer, err := fetch()
		if err != nil {
			// The HOP failed, so nobody said anything and this adapter has to. The
			// message names the log the reason is in, because the fault is host-side
			// and nothing in the jail can see it.
			writeError(w, http.StatusBadRequest, "ServiceUnreachable",
				"the host aws-auth credential service could not be reached: "+err.Error()+
					" — on the HOST see ~/.local/share/yolo-jail/logs/host-service-aws-auth.log")
			return
		}
		if !answer.OK {
			// THE PASS-THROUGH. The body is the daemon's own {Code, Message} and it
			// is forwarded byte for byte: for a lapsed session that Message holds
			// `aws sso login --profile X` verbatim, and nothing here may summarise,
			// re-wrap or prefix it.
			writeRaw(w, http.StatusBadRequest, answer.Body)
			return
		}
		if missing := missingCredentialFields(answer.Body); len(missing) != 0 {
			// A 200 CARRYING AN INCOMPLETE BODY IS THE ONE FAILURE THE SDK WILL NOT
			// EXPLAIN. It rejects a response with no `Token` WITHOUT naming the
			// field (the trap awsauth's package comment opens with), so forwarding
			// such a body would produce an unreadable client-side error. The host
			// already refuses to cache an incomplete credential
			// (awsauth.Credential.Complete), so reaching this is a bug rather than a
			// configuration — and a named 4xx is how a bug gets reported instead of
			// blamed on AWS.
			writeError(w, http.StatusBadRequest, "IncompleteCredential",
				"the host aws-auth credential service returned a body missing "+
					joinFields(missing)+"; the container-credentials protocol requires all "+
					"four of AccessKeyId, SecretAccessKey, Token and Expiration, and an SDK "+
					"rejects a body missing one without naming it")
			return
		}
		writeRaw(w, http.StatusOK, answer.Body)
	})
}

// credentialFields is the whole of what an SDK reads. Nothing else in the body is
// looked at by anyone, which is why the adapter forwards rather than re-renders.
var credentialFields = []string{"AccessKeyId", "SecretAccessKey", "Token", "Expiration"}

// missingCredentialFields reports which of the four required strings a success body
// does not carry as a non-empty string, in protocol order.
func missingCredentialFields(body json.RawMessage) []string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return credentialFields
	}
	var missing []string
	for _, field := range credentialFields {
		if value, _ := decoded[field].(string); value == "" {
			missing = append(missing, field)
		}
	}
	return missing
}

func joinFields(fields []string) string {
	switch len(fields) {
	case 1:
		return fields[0]
	case 2:
		return fields[0] + " and " + fields[1]
	default:
		out := ""
		for i, f := range fields[:len(fields)-1] {
			if i > 0 {
				out += ", "
			}
			out += f
		}
		return out + " and " + fields[len(fields)-1]
	}
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"Code": code, "Message": message})
}

// CallerTokenEnv is the variable carrying this adapter's per-launch caller token:
// YOLO_SERVICE_AWS_AUTH_TOKEN, composed from the loophole's name.
var CallerTokenEnv = paths.ServiceCallerTokenEnv(LoopholeName)

// LoopholeName is the loophole whose jail daemon this is.
const LoopholeName = "aws-auth"

// The two refusal reasons, which differ only in what a caller that got one should check.
const (
	callerTokenMissing = "missing"
	callerTokenWrong   = "wrong"
)

// callerCheck returns "" when r's Authorization is callerToken, else why not. The SDK
// sends the token file's contents verbatim; a `Bearer ` prefix is accepted too, so a
// human's curl reads the same. Surrounding whitespace is trimmed, since a token file
// written by hand ends in a newline.
func callerCheck(r *http.Request, callerToken string) string {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return callerTokenMissing
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if scheme, rest, ok := strings.Cut(v, " "); ok && strings.EqualFold(scheme, "Bearer") {
			v = strings.TrimSpace(rest)
		}
		if callerToken != "" && subtle.ConstantTimeCompare([]byte(v), []byte(callerToken)) == 1 {
			return ""
		}
	}
	return callerTokenWrong
}

// callerRefusalMessage is the 401's Message, which the SDK surfaces on the error it raises.
func callerRefusalMessage(reason string) string {
	what := "carried no caller token"
	if reason == callerTokenWrong {
		what = "carried a caller token that is not this launch's"
	}
	return "yolo aws-auth adapter: refused — this request " + what + ". The adapter serves " +
		"only the agents whose profile selects it, whose AWS SDK sends the token in " +
		"AWS_CONTAINER_AUTHORIZATION_TOKEN beside AWS_CONTAINER_CREDENTIALS_FULL_URI; a " +
		"loopback shared with other processes makes this port reachable from outside the jail " +
		"(docs/plans/notch-convergence.md §2.3). A client that gets this was started without the " +
		"launch's environment, or by an older yolo: restart it from a fresh `yolo` entry"
}

// callerTokenLookup is getenv with this adapter's caller token answered from where the launcher
// put it: its own environment when a launch exported the token there, and otherwise the scoped
// record in the channel section of the jail home's yolo-user-env.sh
// (entrypoint.ScopedCallerToken). The aws-auth pack SCOPES the token (its pointer names
// `{caller_token}`, docs/reference/providers.md OQ-CN7 (c)): the only exported
// copy is AWS_CONTAINER_AUTHORIZATION_TOKEN in the env file of each agent whose profile selects
// `bedrock`, so no other process's environment carries it, and the adapter reads the record the
// shared file keeps for it instead.
func callerTokenLookup(getenv func(string) string) func(string) string {
	return func(key string) string {
		if v := getenv(key); v != "" || key != CallerTokenEnv {
			return v
		}
		home := getenv("HOME")
		if home == "" {
			return ""
		}
		return entrypoint.ScopedCallerToken(home, CallerTokenEnv)
	}
}

// callerTokenFrom reads and checks the adapter's caller token, returning why it cannot
// serve when it cannot. The format is the launcher's (svcendpoint.NewToken).
func callerTokenFrom(getenv func(string) string) (string, string) {
	tok := getenv(CallerTokenEnv)
	if tok == "" {
		return "", "this launch handed the adapter no caller token ($" + CallerTokenEnv +
			" is unset), and it serves no caller it cannot authenticate — a launcher older " +
			"than the jail's binaries is the usual cause"
	}
	if !svcendpoint.IsToken(tok) {
		return "", "$" + CallerTokenEnv + " is not a caller token this launcher mints " +
			"(64 lowercase hex characters)"
	}
	return tok, ""
}

// idleUntilStopped blocks until the supervisor stops this daemon. A variable so a test can
// observe the idle instead of blocking on it.
var idleUntilStopped = func() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	<-ctx.Done()
}

// Main runs the jail-local adapter. It holds no credential state: every request
// crosses the authenticated endpoint file published for this jail and the host
// service decides what to answer.
//
// It serves only behind this launch's caller token. Handed none, or a malformed one, it
// binds nothing and idles until stopped, logging why: exiting would crash-loop under
// `restart: on-failure`, and serving would answer every process on its loopback.
func Main(args []string) int {
	fs := flag.NewFlagSet("aws-credential-adapter", flag.ContinueOnError)
	listen := fs.String("listen", DefaultListen, "loopback address to listen on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintf(os.Stderr, "aws-credential-adapter: unexpected arguments: %v\n", fs.Args())
		return 2
	}
	callerToken, why := callerTokenFrom(callerTokenLookup(os.Getenv))
	if why != "" {
		fmt.Fprintln(os.Stderr, "aws-credential-adapter: idling, serving nothing:", why)
		idleUntilStopped()
		return 0
	}
	endpoint := os.Getenv(EndpointEnv)
	fetch := func() (Answer, error) {
		return Request(endpoint, map[string]any{"action": "credentials"}, os.Stderr)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aws-credential-adapter:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "aws-credential-adapter: serving on %s; every request must carry this "+
		"launch's caller token ($%s)\n", listener.Addr(), CallerTokenEnv)
	if err := Serve(listener, callerToken, fetch); err != nil {
		fmt.Fprintln(os.Stderr, "aws-credential-adapter:", err)
		return 1
	}
	return 0
}

// Serve exposes Handler on an already-bound listener, behind callerToken; closing
// listener stops it. It refuses to serve behind anything that is not a well-formed token.
func Serve(listener net.Listener, callerToken string, fetch Fetch) error {
	if !svcendpoint.IsToken(callerToken) {
		return errors.New("refusing to serve the AWS credential adapter without a caller token")
	}
	server := &http.Server{Handler: Handler(callerToken, fetch), ReadHeaderTimeout: 5 * time.Second}
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
