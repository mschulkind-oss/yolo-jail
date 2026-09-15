// Package openauthclient is the framed client for yolo's machine-wide OpenAI
// credential service. It contains no refresh logic: the host broker is the one
// refresh-token owner, and this package only requests agent-shaped views.
package openauthclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const (
	EndpointEnv   = "YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT"
	HostSocketEnv = "YOLO_OPENAI_AUTH_HOST_SOCKET"
	dialTimeout   = 5 * time.Second
)

// RemoteExitError reports a nonzero exit frame from the host service. Any
// diagnostic frames have already been copied to the caller's stderr writer.
type RemoteExitError struct{ Code int }

func (e *RemoteExitError) Error() string {
	return fmt.Sprintf("OpenAI credential service exited with status %d", e.Code)
}

// Request sends one JSON request through an authenticated endpoint file and
// returns the service's JSON stdout. Stderr frames are forwarded as they arrive.
func Request(endpointPath string, request any, stderr io.Writer) (json.RawMessage, error) {
	if endpointPath == "" {
		return nil, fmt.Errorf("%s is not set", EndpointEnv)
	}
	conn, err := svcendpoint.Dial(endpointPath, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to OpenAI credential service: %w", err)
	}
	defer conn.Close()
	return requestConn(conn, request, stderr)
}

// RequestUnix talks directly to the host-only broker socket. The filesystem mode is
// the authorization boundary on this path, so it sends no jail connection preamble.
func RequestUnix(socketPath string, request any, stderr io.Writer) (json.RawMessage, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("%s is not set", HostSocketEnv)
	}
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to host OpenAI credential service: %w", err)
	}
	defer conn.Close()
	return requestConn(conn, request, stderr)
}

func requestConn(conn net.Conn, request any, stderr io.Writer) (json.RawMessage, error) {
	if stderr == nil {
		stderr = io.Discard
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI credential request: %w", err)
	}
	if err := frameproto.WriteRequest(conn, body); err != nil {
		return nil, fmt.Errorf("send OpenAI credential request: %w", err)
	}

	var stdout bytes.Buffer
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			return nil, fmt.Errorf("read OpenAI credential response: %w", err)
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			_, _ = stdout.Write(frame.Payload)
		case frameproto.StreamStderr:
			if _, err := stderr.Write(frame.Payload); err != nil {
				return nil, fmt.Errorf("write OpenAI credential diagnostic: %w", err)
			}
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(frame.Payload)
			if err != nil {
				return nil, err
			}
			if rc != 0 {
				return nil, &RemoteExitError{Code: rc}
			}
			response := bytes.TrimSpace(stdout.Bytes())
			if len(response) == 0 || !json.Valid(response) {
				return nil, errors.New("OpenAI credential service returned malformed JSON")
			}
			return append(json.RawMessage(nil), response...), nil
		default:
			return nil, fmt.Errorf("OpenAI credential service returned unknown stream %d", frame.StreamID)
		}
	}
}

// Main is the process entry point used by `yolo internal openai-auth-client`.
func Main(args []string) int { return Run(args, os.Getenv, os.Stdout, os.Stderr) }
