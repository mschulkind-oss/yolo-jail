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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const mcpAuthUsage = `Usage: yolo mcp-auth header SERVER [--format bare|json]
       yolo mcp-auth status [SERVER] [--json]

Status is a token-free read of configuration, native placement, source snapshots
and existing canonical state. Missing or unhealthy state returns status, not a
health-gate error. It never stages packs, starts a poll or calls a provider.

Print the Authorization header value for SERVER, one of your own MCP servers
configured with sign_in = "once". The default is Bearer <access token>; --format json
prints one Authorization object. An unexpired token is issued without refresh. An agent that takes a server's header from a
command runs this (pi's "!yolo mcp-auth header SERVER"), so it uses the one
sign-in every jail shares and never signs in itself. Each token handed over is
recorded: yolo audit --service mcp-auth.

In a jail it asks this launch's mcp-auth broker; on the host it reads the
broker's store itself and never redeems a token. An expired live record in a jail
asks its existing poll to renew, waiting at most five seconds. The whole command
has an eight-second budget, including dial and framing. It serves only a server your own config makes a
one-sign-in server; to make one:

  yolo config set mcp_servers.SERVER.sign_in once --user

Exit status: 0 with the header value on stdout; 1 when there is no token yet
(stderr names the sign-in); 2 for a server this launch does not serve, or a
usage error.

Managed native content: yolo pack add PACK; yolo config check; relaunch.
Status names the resolved native entry. Paste the full callback URL there when
callback publication is unavailable. Placement is not native plugin-loading proof.
Own-client option: yolo config set mcp_servers.SERVER.oauth.client_id ID --user,
then yolo config check and relaunch. No other agent borrows that client identity.

Examples:
  yolo mcp-auth status slack --json
  yolo mcp-auth header slack --format json
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

var mcpAuthCommandTimeout = 8 * time.Second
var newMCPAuthContext = func() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), mcpAuthCommandTimeout)
}
var mcpAuthDial = func(endpoint string, wait time.Duration) (net.Conn, error) { return svcendpoint.Dial(endpoint, wait) }

func runMCPAuthVerb(args []string) int { return mcpAuthMain(args[1:], os.Getenv, os.Stdout, os.Stderr) }

// mcpAuthMain is `yolo mcp-auth` after the verb.
func mcpAuthMain(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if answerHelp("mcp-auth", args, stdout) {
		return 0
	}
	if len(args) > 0 && args[0] == "status" {
		return mcpAuthStatusMain(args[1:], getenv, stdout, stderr)
	}
	ctx, cancel := newMCPAuthContext()
	defer cancel()
	form := "bare"
	if len(args) == 4 && args[2] == "--format" && (args[3] == "bare" || args[3] == "json") {
		form = args[3]
		args = args[:2]
	}
	if len(args) != 2 || args[0] != "header" || args[1] == "" {
		fmt.Fprintln(stderr, "yolo mcp-auth: expected `header SERVER [--format bare|json]`; see `yolo mcp-auth --help`")
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
		deadline, _ := ctx.Deadline()
		conn, err := mcpAuthDial(endpoint, time.Until(deadline))
		if err != nil {
			fmt.Fprintf(stderr, "yolo mcp-auth: cannot reach this jail's mcp-auth broker (%v); `yolo check` on the host names why\n", err)
			return 1
		}
		defer conn.Close()
		_ = conn.SetDeadline(deadline)
		return mcpAuthExchangeForm(conn, server, form, stdout, stderr)
	}
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
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(stderr, "yolo mcp-auth: header budget exhausted; retry or run yolo mcp-auth status")
		return 1
	}
	writeMCPHeader(stdout, token, form)
	return 0
}

// mcpAuthExchange is the framed header request on a dialled connection: the broker's stdout and
// stderr are passed through as they arrive, and its exit status is returned.
func writeMCPHeader(stdout io.Writer, token, form string) {
	if form == "json" {
		body, _ := json.Marshal(map[string]string{"Authorization": "Bearer " + token})
		fmt.Fprintln(stdout, string(body))
		return
	}
	fmt.Fprintln(stdout, "Bearer "+token)
}

func mcpAuthExchange(conn io.ReadWriter, server string, stdout, stderr io.Writer) int {
	return mcpAuthExchangeForm(conn, server, "bare", stdout, stderr)
}

func mcpAuthExchangeForm(conn io.ReadWriter, server, form string, stdout, stderr io.Writer) int {
	body, _ := json.Marshal(map[string]string{"op": "header", "server": server, "format": form})
	var output bytes.Buffer
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
			_, _ = output.Write(frame.Payload)
		case frameproto.StreamStderr:
			_, _ = stderr.Write(frame.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(frame.Payload)
			if err != nil {
				fmt.Fprintf(stderr, "yolo mcp-auth: the broker's exit status is unreadable (%v); retry\n", err)
				return 1
			}
			if rc == 0 {
				_, _ = stdout.Write(output.Bytes())
			}
			return rc
		}
	}
}
