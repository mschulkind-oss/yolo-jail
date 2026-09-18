// Package awscredadapter speaks the AWS container-credentials protocol on the
// jail's loopback and forwards every request to yolo's host `aws-auth` credential
// service. It is the JAIL half of docs/design/sso-backed-bedrock.md; the host half
// is internal/awsauth (the mint) wrapped by internal/awsauthdaemon (the service).
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
// # There is no authorization token, and adding one would buy nothing
//
// The SDK sends `Authorization` only when AWS_CONTAINER_AUTHORIZATION_TOKEN is set.
// An environment variable is inherited by every process the agent spawns, and
// everything that could read it can already reach this port — the boundary is
// positional and the position is the network namespace (design §5's warning). A
// nested jail shares this port for the same reason, which is a property to know
// rather than a defect to fix.
package awscredadapter

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// CredentialsPath is the path AWS_CONTAINER_CREDENTIALS_FULL_URI names. The pack's
// `env` contribution writes the whole URI, so this constant and that value are one
// fact in two files; the pack's README says so where the variable is declared.
const CredentialsPath = "/credentials"

// DefaultListen is this adapter's fixed jail-loopback address. The port is
// adjacent to the OpenAI adapter's 1460 because they are the same kind of thing —
// a credential endpoint an agent's own client library dials — and the other fixed
// jail ports (8214 cerebras, 8215 wire-bridge, 8216 kilo) are a different family.
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
// dials.
func Handler(fetch Fetch) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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

// Main runs the jail-local adapter. It holds no credential state: every request
// crosses the authenticated endpoint file published for this jail and the host
// service decides what to answer.
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
	endpoint := os.Getenv(EndpointEnv)
	fetch := func() (Answer, error) {
		return Request(endpoint, map[string]any{"action": "credentials"}, os.Stderr)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aws-credential-adapter:", err)
		return 1
	}
	if err := Serve(listener, fetch); err != nil {
		fmt.Fprintln(os.Stderr, "aws-credential-adapter:", err)
		return 1
	}
	return 0
}

// Serve exposes Handler on an already-bound listener; closing listener stops it.
func Serve(listener net.Listener, fetch Fetch) error {
	server := &http.Server{Handler: Handler(fetch), ReadHeaderTimeout: 5 * time.Second}
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
