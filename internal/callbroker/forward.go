package callbroker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// ForwardCLI is the confined side of a selected pack's intercept. The name only
// selects the launch-published endpoint; it cannot assert a host identity or grant.
func ForwardCLI(name, op string, argv []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	refuse := func() int {
		fmt.Fprintln(stderr, "call broker unavailable; select the pack, run `yolo check` on the host, and relaunch")
		return 69
	}
	if !loopholedecl.ValidBrokerSourceName(name) || len(argv) == 0 || (op != "act" && op != "evaluate" && op != "ask") {
		fmt.Fprintln(stderr, "invalid call broker arguments; use the selected pack's CLI forwarder")
		return 64
	}
	endpoint := getenv(paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug(name) + paths.ServiceEnvVarSuffix)
	if endpoint == "" {
		return refuse()
	}
	// The handler caps base64 stdin at 4 MiB. Never silently truncate canonical bytes.
	body := map[string]any{"op": op, "argv": argv}
	if stdin != nil {
		data, err := io.ReadAll(io.LimitReader(stdin, 3<<20+1))
		if err != nil || len(data) > 3<<20 {
			fmt.Fprintln(stderr, "call broker stdin exceeds 3 MiB; narrow the call and retry")
			return 64
		}
		body["stdin"] = base64.StdEncoding.EncodeToString(data)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return refuse()
	}
	conn, err := svcendpoint.Dial(endpoint, 5*time.Second)
	if err != nil {
		return refuse()
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(40 * time.Second))
	if err := frameproto.WriteRequest(conn, payload); err != nil {
		return refuse()
	}
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			return refuse()
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			_, _ = stdout.Write(f.Payload)
		case frameproto.StreamStderr:
			_, _ = stderr.Write(f.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(f.Payload)
			if err != nil {
				return refuse()
			}
			return rc
		}
	}
}
