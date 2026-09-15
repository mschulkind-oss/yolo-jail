package openauthclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
)

// Run parses one client command. getenv and writers are explicit so tests use
// the complete command path without replacing process-global stdout or stderr.
func Run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: yolo internal openai-auth-client <token|login|status>")
		return 2
	}
	action := args[0]
	if action != "token" && action != "login" && action != "status" {
		fmt.Fprintf(stderr, "openai-auth-client: unknown command %q\n", action)
		return 2
	}
	fs := flag.NewFlagSet("openai-auth-client "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var codexAuth string
	if action == "token" || action == "login" {
		fs.StringVar(&codexAuth, "codex-auth", "", "atomically write a Codex auth.json view")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintf(stderr, "openai-auth-client %s: unexpected arguments: %v\n", action, fs.Args())
		return 2
	}
	request := map[string]any{"action": action}
	if codexAuth != "" {
		request["view"] = "codex"
	}
	var response json.RawMessage
	var err error
	if endpoint := getenv(EndpointEnv); endpoint != "" {
		response, err = Request(endpoint, request, stderr)
	} else if socket := getenv(HostSocketEnv); socket != "" {
		response, err = RequestUnix(socket, request, stderr)
	} else {
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
