package openauthclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
)

// hostOnlyAction reports whether a verb mutates the MACHINE-WIDE grant, and may therefore
// only be sent on the private host socket.
//
// The client enforces this as well as the daemon, and the redundancy is deliberate: the daemon
// serves these on one socket and refuses them on the other (openaiauthdaemon's two handlers),
// which is the boundary that actually holds — but a client that would happily put `logout` on
// a jail's endpoint invites the next reader to believe the endpoint serves it. Refusing here
// means the request is never composed, so there is nothing on the wire to reason about.
func hostOnlyAction(action string) bool { return action == "import" || action == "logout" }

// Run parses one client command. getenv and writers are explicit so tests use
// the complete command path without replacing process-global stdout or stderr.
func Run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: yolo internal openai-auth-client <token|login|status|import|logout>")
		return 2
	}
	action := args[0]
	switch action {
	case "token", "login", "status", "import", "logout":
	default:
		fmt.Fprintf(stderr, "openai-auth-client: unknown command %q\n", action)
		return 2
	}
	fs := flag.NewFlagSet("openai-auth-client "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var codexAuth, piAuth, importFrom, hostSocket string
	if action == "token" || action == "login" {
		fs.StringVar(&codexAuth, "codex-auth", "", "atomically write a Codex auth.json view")
		fs.StringVar(&piAuth, "pi-auth", "", "atomically merge a Pi auth.json view")
	}
	if hostOnlyAction(action) {
		// --host-socket, because a machine-wide mutation must NAME its door. The env var is
		// how `yolo host -- <agent>` hands it to a child; a human running this directly has
		// no such variable, and deriving the path here would put a second copy of the
		// singleton's socket layout in the client (openaiauthhost owns the one that also
		// ensures the daemon is running).
		fs.StringVar(&hostSocket, "host-socket", "",
			"private host socket of the machine-wide credential daemon (default: $"+HostSocketEnv+")")
	}
	if action == "import" {
		fs.StringVar(&importFrom, "from", "",
			"absolute path of a Codex auth.json to install as the canonical grant")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintf(stderr, "openai-auth-client %s: unexpected arguments: %v\n", action, fs.Args())
		return 2
	}
	if codexAuth != "" && piAuth != "" {
		fmt.Fprintln(stderr, "openai-auth-client: --codex-auth and --pi-auth are mutually exclusive")
		return 2
	}
	request := map[string]any{"action": action}
	if codexAuth != "" {
		request["view"] = "codex"
	}
	if action == "import" {
		if importFrom == "" {
			fmt.Fprintln(stderr, "openai-auth-client import: --from <path to a Codex auth.json> is required")
			return 2
		}
		request["path"] = importFrom
	}
	var response json.RawMessage
	var err error
	switch {
	case hostOnlyAction(action):
		// THE PRIVATE SOCKET OR NOTHING. No endpoint fallback, for the reason hostOnlyAction
		// gives: the endpoint is a jail's door, the daemon refuses these there, and sending
		// them anyway would turn a boundary into a round trip whose only outcome is a
		// refusal — while reading as though the door served them.
		if hostSocket == "" {
			hostSocket = getenv(HostSocketEnv)
		}
		if hostSocket == "" {
			fmt.Fprintf(stderr, "openai-auth-client %s: this is a MACHINE-WIDE operation and "+
				"runs only on the host's private credential socket. Set $%s or pass "+
				"--host-socket <path>.\n", action, HostSocketEnv)
			return 2
		}
		response, err = RequestUnix(hostSocket, request, stderr)
	case getenv(EndpointEnv) != "":
		response, err = Request(getenv(EndpointEnv), request, stderr)
	case getenv(HostSocketEnv) != "":
		response, err = RequestUnix(getenv(HostSocketEnv), request, stderr)
	default:
		response, err = Request("", request, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "openai-auth-client:", err)
		var remote *RemoteExitError
		if errors.As(err, &remote) && remote.Code > 0 && remote.Code <= 125 {
			return remote.Code
		}
		return 1
	}
	if codexAuth != "" {
		if err := WriteCodexAuth(codexAuth, response); err != nil {
			fmt.Fprintln(stderr, "openai-auth-client:", err)
			return 1
		}
		response, _ = json.Marshal(map[string]any{"auth_path": codexAuth})
	} else if piAuth != "" {
		if err := WritePiAuth(piAuth, response); err != nil {
			fmt.Fprintln(stderr, "openai-auth-client:", err)
			return 1
		}
		response, _ = json.Marshal(map[string]any{"auth_path": piAuth})
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, response); err != nil {
		fmt.Fprintln(stderr, "openai-auth-client: compact response:", err)
		return 1
	}
	compact.WriteByte('\n')
	if _, err := stdout.Write(compact.Bytes()); err != nil {
		fmt.Fprintln(stderr, "openai-auth-client: write response:", err)
		return 1
	}
	return 0
}
