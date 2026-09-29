package awscredadapter

// doorway.go is `yolo internal daemon aws-credential-adapter --listen <addr>`: this adapter
// opened OUTSIDE a sandbox whose agent shares the host's loopback, as the loophole's
// `jail_daemon.host_cmd` declares (docs/design/host-notch-services.md HS-D15, the doorway rule;
// "doorway" is that ruling's word for the thin adapter an agent's client talks to, which checks
// the launch's caller token and forwards to the service's host daemon). A macos-user launch
// starts it as a launch-owned listener (internal/launchservice) on the port it picked for the
// loophole's `listen`, which AWS_CONTAINER_CREDENTIALS_FULL_URI names for the agents whose
// profile selects `bedrock`, and stops it when the sandboxed command exits.
//
// IT IS THE SAME HANDLER as the jail's (Handler, Serve), forwarding the same way: through the
// authenticated front the launch published for this session's aws-auth service, named by
// EndpointEnv. What differs is only where its inputs come from: the launch's input file, which
// carries the caller token the launch minted and the endpoint file, instead of a jail's
// environment and the shared channel's scoped record.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
)

// DoorwayMain is the doorway's subcommand body.
func DoorwayMain(args []string) int {
	fs := flag.NewFlagSet("aws-credential-adapter", flag.ContinueOnError)
	listen := fs.String("listen", "", "the loopback address the launch picked")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintf(os.Stderr, "aws-credential-adapter: unexpected arguments: %v\n", fs.Args())
		return 2
	}
	return launchservice.ServeListener(LoopholeName, *listen, doorwayPrepare(Request))
}

// doorwayPrepare reads the doorway's two inputs and returns its serve: request is how it asks
// the host service, a parameter so a test can stand a fake one in.
func doorwayPrepare(request func(string, any, io.Writer) (Answer, error)) launchservice.Prepare {
	return func(getenv func(string) string) (func(net.Listener) error, error) {
		token, why := callerTokenFrom(getenv)
		if why != "" {
			return nil, errors.New(why)
		}
		// An ABSENT endpoint is served, not refused: the host service did not start (it refuses
		// loudly at spawn without a configured profile, and the launch goes on, as it does with
		// every other service), so each request is answered with the 4xx naming the host log,
		// exactly as the jail's copy answers when its endpoint is missing (Handler).
		endpoint := getenv(EndpointEnv)
		if endpoint == "" {
			fmt.Fprintf(os.Stderr, "aws-credential-adapter: the launch handed this doorway no "+
				"endpoint ($%s is unset), so the host aws-auth service did not start; every "+
				"request is answered ServiceUnreachable\n", EndpointEnv)
		}
		fetch := func() (Answer, error) {
			return request(endpoint, map[string]any{"action": "credentials"}, os.Stderr)
		}
		return func(l net.Listener) error { return Serve(l, token, fetch) }, nil
	}
}
