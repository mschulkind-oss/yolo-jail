package cli

// mcpauth.go is `yolo mcp-auth`, the MCP sign-in verb (docs/design/mcp-auth-sharing.md §9.6, the
// plan's phase D). This yolo builds one subcommand, `header`: an agent that takes an MCP
// server's Authorization header from a command runs it, and its stdout is that header's value.
// MAIN ROUTES IT BEFORE THE FRONT DOOR (cli.go), as it does `yolo gh`: its stdout is read by
// another program, and the banner or an update notice would corrupt it.
//
//   - IN A JAIL it asks this launch's mcp-auth broker over its endpoint
//     (YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT), which serves only the one-sign-in servers the
//     launch composed from the user's own config.
//   - ON THE HOST, where no broker runs for the caller, it answers from the broker's canonical
//     store itself (run.HostHeader), over the user-scope composition `yolo host` launches with.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const mcpAuthUsage = `Usage: yolo mcp-auth header SERVER

Print the Authorization header value for SERVER, one of your own MCP servers
configured with sign_in = "once". The value is Bearer <access token>,
refreshed first when under five minutes remain. An agent that takes a server's header from a
command runs this (pi's "!yolo mcp-auth header SERVER"), so it uses the one
sign-in every jail shares and never signs in itself. Each token handed over is
recorded: yolo audit --service mcp-auth.

In a jail it asks this launch's mcp-auth broker; on the host it reads the
broker's store itself. It serves only a server your own config makes a
one-sign-in server; to make one:

  yolo config set mcp_servers.SERVER.sign_in once --user

Exit status: 0 with the header value on stdout; 1 when there is no token yet
(stderr names the sign-in); 2 for a server this launch does not serve, or a
usage error.

Examples:
  yolo mcp-auth header slack`

// mcpAuthEndpointEnv is the broker's jail-facing endpoint variable, a function of the service's
// name and transport alone (run's hostServiceEnvVar), spelled here because this client reads it.
const mcpAuthEndpointEnv = "YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT"

// mcpAuthHostHeader is the host's answer, a seam so a test needs no user config or state dir.
var mcpAuthHostHeader = func(ctx context.Context, server string, warn func(string)) (string, error) {
	cfg := config.UserScopeConfigOrEmpty()
	sel := loadedHostPacks(cfg, "", "")
	if err := sel.launchRefusal(); err != nil {
		return "", &mcpauthdaemon.HeaderRefusal{Code: 1, Message: "mcp-auth: " + err.Error()}
	}
	workspace, err := os.Getwd()
	if err != nil {
		workspace = paths.Home()
	}
	share, err := run.HostHeaderShare(cfg, sel.packs, paths.Home(), workspace)
	if err != nil {
		return "", &mcpauthdaemon.HeaderRefusal{Code: 1, Message: err.Error() + "; fix your user config's mcp_servers and retry"}
	}
	return run.HostHeader(ctx, share, server, warn)
}

func runMCPAuthVerb(args []string) int { return mcpAuthMain(args[1:], os.Getenv, os.Stdout, os.Stderr) }

// mcpAuthMain is `yolo mcp-auth` after the verb.
func mcpAuthMain(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if answerHelp("mcp-auth", args, stdout) {
		return 0
	}
	if len(args) != 2 || args[0] != "header" || args[1] == "" {
		fmt.Fprintln(stderr, "yolo mcp-auth: expected `header SERVER`; see `yolo mcp-auth --help`")
		return 2
	}
	server := args[1]
	if config.InJail() {
		endpoint := getenv(mcpAuthEndpointEnv)
		if endpoint == "" {
			fmt.Fprintf(stderr, "yolo mcp-auth: no mcp-auth broker runs for this jail (%s is unset), so no one-sign-in token "+
				"reaches it; the broker runs in a podman jail on Linux that selects the mcp-auth pack (the claude pack "+
				"selects it). Sign %s in in the agent itself, or relaunch with that pack: yolo pack add mcp-auth\n", mcpAuthEndpointEnv, server)
			return 1
		}
		conn, err := svcendpoint.Dial(endpoint, 5*time.Second)
		if err != nil {
			fmt.Fprintf(stderr, "yolo mcp-auth: cannot reach this jail's mcp-auth broker (%v); `yolo check` on the host names why\n", err)
			return 1
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(40 * time.Second))
		return mcpAuthExchange(conn, server, stdout, stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := mcpAuthHostHeader(ctx, server, func(line string) { fmt.Fprintln(stderr, "yolo mcp-auth:", line) })
	if err != nil {
		code := 1
		var refusal *mcpauthdaemon.HeaderRefusal
		if errors.As(err, &refusal) {
			code = refusal.Code
		}
		fmt.Fprintln(stderr, err.Error())
		return code
	}
	fmt.Fprintln(stdout, "Bearer "+token)
	return 0
}

// mcpAuthExchange is the framed header request on a dialled connection: the broker's stdout and
// stderr are passed through as they arrive, and its exit status is returned.
func mcpAuthExchange(conn io.ReadWriter, server string, stdout, stderr io.Writer) int {
	body, _ := json.Marshal(map[string]string{"op": "header", "server": server})
	if err := frameproto.WriteRequest(conn, body); err != nil {
		fmt.Fprintf(stderr, "yolo mcp-auth: the broker did not take the request (%v); retry\n", err)
		return 1
	}
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			fmt.Fprintf(stderr, "yolo mcp-auth: the broker's answer broke off (%v); retry\n", err)
			return 1
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			_, _ = stdout.Write(frame.Payload)
		case frameproto.StreamStderr:
			_, _ = stderr.Write(frame.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(frame.Payload)
			if err != nil {
				fmt.Fprintf(stderr, "yolo mcp-auth: the broker's exit status is unreadable (%v); retry\n", err)
				return 1
			}
			return rc
		}
	}
}
