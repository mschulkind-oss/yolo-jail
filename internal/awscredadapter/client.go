package awscredadapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// client.go is the jail→host hop: one framed request across the authenticated
// endpoint file yolo published for this jail, and the JSON the `aws-auth` daemon
// answered with.
//
// # Why this is ~60 lines copied from internal/openauthclient rather than an import
//
// Two reasons, and the second is the load-bearing one.
//
//  1. Its error strings say "OpenAI". A jail reading "connect to OpenAI credential
//     service" while its AWS credential fetch failed is a wrong report, not a
//     cosmetic one.
//  2. ITS Request DISCARDS THE BODY ON A NONZERO EXIT. openauthclient returns
//     *RemoteExitError and throws stdout away, which is correct for a refresh that
//     either worked or did not — and fatal here. The `aws-auth` daemon answers a
//     lapsed session with the container-credentials 4xx body on STDOUT and exits 1
//     (internal/awsauthdaemon's replyError), and that body is the entire mechanism
//     OQ-SSO6 rests on: it carries `aws sso login --profile X` verbatim to the
//     agent's error text. A client that drops it turns the design's one message
//     into a bare transport failure.
//
// So the exit code here is DATA on the Answer, never an error. An error from this
// file means the hop itself failed — nothing was said by anyone.

// EndpointEnv is the variable the run pipeline sets for this loophole's jail-facing
// front. It is a function of the loophole NAME and the transport alone
// (hostServiceEnvVar, internal/cli/run/helpers.go), so it is spelled here only
// because the adapter has to read it — nothing chooses it.
const EndpointEnv = "YOLO_SERVICE_AWS_AUTH_ENDPOINT"

// dialTimeout bounds the connect to the per-jail front. Generous next to the SDK's
// own 1000 ms ceiling on purpose: a dial that is going to succeed succeeds in
// microseconds over loopback, and a dial that is going to fail should fail with a
// message rather than race the caller's deadline into an ambiguous one.
const dialTimeout = 5 * time.Second

// Answer is one reply from the host credential service: the JSON body it returned
// and whether it returned it as a success.
//
// BOTH SHAPES ARE BODIES. On OK the body is the four-key container-credentials
// response; otherwise it is `{"Code": …, "Message": …}`. The adapter forwards
// whichever it got and chooses only the status code, which is what keeps the
// SessionToken → Token rename in one place in the tree
// (awsauth.Credential.ContainerCredentials).
type Answer struct {
	Body json.RawMessage
	OK   bool
}

// Request sends one JSON request through the authenticated endpoint file and
// returns what the service said. Stderr frames are forwarded as they arrive.
func Request(endpointPath string, request any, stderr io.Writer) (Answer, error) {
	if endpointPath == "" {
		return Answer{}, fmt.Errorf("%s is not set: this jail has no aws-auth front to ask "+
			"(is the `aws-auth` loophole enabled for this launch?)", EndpointEnv)
	}
	conn, err := svcendpoint.Dial(endpointPath, dialTimeout)
	if err != nil {
		return Answer{}, fmt.Errorf("connect to the aws-auth credential service: %w", err)
	}
	defer conn.Close()
	return exchange(conn, request, stderr)
}

// exchange is the framed conversation on an already-dialled connection, split out
// so it is testable over a net.Pipe with no endpoint file, no TLS and no daemon.
func exchange(conn net.Conn, request any, stderr io.Writer) (Answer, error) {
	if stderr == nil {
		stderr = io.Discard
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Answer{}, fmt.Errorf("encode aws-auth request: %w", err)
	}
	if err := frameproto.WriteRequest(conn, body); err != nil {
		return Answer{}, fmt.Errorf("send aws-auth request: %w", err)
	}

	var stdout bytes.Buffer
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			return Answer{}, fmt.Errorf("read aws-auth response: %w", err)
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			stdout.Write(frame.Payload)
		case frameproto.StreamStderr:
			if _, err := stderr.Write(frame.Payload); err != nil {
				return Answer{}, fmt.Errorf("write aws-auth diagnostic: %w", err)
			}
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(frame.Payload)
			if err != nil {
				return Answer{}, err
			}
			response := bytes.TrimSpace(stdout.Bytes())
			// A NONZERO EXIT IS NOT AN ERROR HERE — see the file comment. What IS
			// an error is a reply with no parseable body, in either direction:
			// there is then nothing to forward and nothing to say, so the adapter
			// must write its own message rather than an empty 4xx.
			if len(response) == 0 || !json.Valid(response) {
				return Answer{}, fmt.Errorf(
					"the aws-auth credential service returned malformed JSON (exit %d)", rc)
			}
			return Answer{Body: append(json.RawMessage(nil), response...), OK: rc == 0}, nil
		default:
			return Answer{}, fmt.Errorf(
				"the aws-auth credential service returned unknown stream %d", frame.StreamID)
		}
	}
}
