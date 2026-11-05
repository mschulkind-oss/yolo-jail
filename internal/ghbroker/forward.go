package ghbroker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// forward.go is the jail side (docs/design/boundary-broker.md §4.3): `yolo gh [--] <args>`,
// which packs/github's `intercept` contribution routes a bare `gh` to. It sends the argv,
// the repository the workspace's `origin` names, and stdin when the argv reads `-`, then
// prints what comes back and exits with the broker's code. It holds no credential and never
// falls back to a gh of its own (§3.5): the real gh is still at its own path, holding no
// login, for anyone who asks for it by name.

// LoopholeName is the loophole packs/github ships; the jail finds its endpoint through
// the variable the launch derives from this name.
const LoopholeName = "github-broker"

// EndpointEnv is the variable naming the broker's endpoint file in the jail.
var EndpointEnv = paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug(LoopholeName) + paths.ServiceEnvVarSuffix

// dialTimeout bounds the dial and the accept ack only; a call streams for as long as the
// broker's own 300-second timeout lets it.
const dialTimeout = 30 * time.Second

// ForwardEnv is what the forwarder reads from its process.
type ForwardEnv struct {
	Getenv func(string) string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// OriginRepo returns the `owner/repo` of the workspace's origin remote on github.com,
	// or "". Nil means read it with the jail's own git, which is safe here: the jail is
	// the confined side.
	OriginRepo func() string
}

func Forward(args []string, env ForwardEnv) int {
	args = normalizedArgs(args)
	endpoint := env.Getenv(EndpointEnv)
	say := func(format string, a ...any) { fmt.Fprintf(env.Stderr, "gh (github-broker): "+format+"\n", a...) }
	if endpoint == "" {
		where := "in this project"
		if hostDir := env.Getenv("YOLO_HOST_DIR"); hostDir != "" {
			where = "in " + shquote.QuoteDisplay(hostDir)
		}
		say("this jail has no github-broker endpoint, so `gh` cannot reach the host's GitHub login: the broker\n"+
			"  did not start for this launch. It runs whenever the `github` pack is selected; run `yolo check` on\n"+
			"  the host, %s, to see why, then start a fresh jail. Apple Container runs no host\n"+
			"  services, so there it never starts. The image's own gh is still reachable by path, or with\n"+
			"  YOLO_BYPASS_SHIMS=1, and holds no GitHub credential in the jail.", where)
		return ExitUnavailable
	}
	req := map[string]any{"argv": args}
	if len(args) > 0 && (args[0] == "status" || args[0] == "wait") {
		if len(args) != 2 || !strings.HasPrefix(args[1], "r-") {
			say("use `yolo gh %s <request-id>`", args[0])
			return ExitUsage
		}
		req = map[string]any{"operation": args[0], "request": args[1]}
	}
	if err := addCallContext(req, args, env); err != nil {
		fmt.Fprintf(env.Stderr, "gh (github-broker): %s\n", err)
		return ExitUsage
	}
	body, _ := json.Marshal(req)
	conn, err := svcendpoint.Dial(endpoint, dialTimeout)
	if err != nil {
		switch {
		case errors.Is(err, svcendpoint.ErrEndpointMissing):
			say("no endpoint is published at %s: the broker never started on the host, or stopped; "+
				"relaunch the jail and see `yolo check`", endpoint)
		case errors.Is(err, svcendpoint.ErrAuthRejected):
			say("the broker rejected this jail's token (the endpoint file %s is stale); relaunch the jail", endpoint)
		default:
			say("cannot reach the broker named by %s: %v", endpoint, err)
		}
		return ExitUnavailable
	}
	defer conn.Close()
	if err := frameproto.WriteRequest(conn, body); err != nil {
		say("the broker stopped answering: %v", err)
		return ExitUnavailable
	}
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			say("the broker on the host stopped answering before the command finished; its log is " +
				"~/.local/share/yolo-jail/logs/host-service-github-broker.log on the host")
			return ExitUnavailable
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			_, _ = env.Stdout.Write(f.Payload)
		case frameproto.StreamStderr:
			_, _ = env.Stderr.Write(f.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(f.Payload)
			if err != nil {
				return ExitUnavailable
			}
			return rc
		}
	}
}

// ForwardAccess asks the broker for a versioned status, evaluate or ask answer. Each access message is
// nested under `access`, so an older decoder that ignores unknown keys cannot mistake proposed
// argv for a legacy act call even if endpoint publication changes between connections.
func ForwardAccess(op string, args []string, env ForwardEnv) ([]byte, int) {
	args = normalizedArgs(args)
	if len(args) > 0 && args[0] == "gh" {
		args = args[1:]
	}
	if op != "status" && op != "evaluate" && op != "ask" {
		fmt.Fprintf(env.Stderr, "yolo access: unsupported operation\n")
		return nil, ExitUsage
	}
	if op != "status" {
		if len(args) == 0 {
			fmt.Fprintf(env.Stderr, "yolo access: provide a GitHub command after --\n")
			return nil, ExitUsage
		}
		// Safe compatibility probe. A legacy daemon ignores the nested envelope, receives no
		// top-level argv and cannot run the proposed command; its non-schema response is rejected
		// before the proposed argv is sent to a supported broker.
		probe, code := forwardFrame(map[string]any{"access": map[string]any{"op": "status", "version": 1}}, env, true)
		if code != 0 || !hasAccessSchema(probe, "yolo.access/1") {
			fmt.Fprintf(env.Stderr, "yolo access: the github-broker does not support versioned access requests; relaunch the jail\n")
			return nil, ExitUnavailable
		}
	}
	req := map[string]any{"op": op, "version": 1}
	if op != "status" {
		req["argv"] = args
		if err := addCallContext(req, args, env); err != nil {
			fmt.Fprintf(env.Stderr, "yolo access: %s\n", err)
			return nil, ExitUsage
		}
	}
	body, code := forwardFrame(map[string]any{"access": req}, env, true)
	if code != 0 {
		return nil, code
	}
	schema := "yolo.access/1"
	if op != "status" {
		schema = "yolo.access.prediction/1"
	}
	if !hasAccessSchema(body, schema) {
		fmt.Fprintf(env.Stderr, "yolo access: the github-broker returned an unsupported response; relaunch the jail\n")
		return nil, ExitUnavailable
	}
	return body, 0
}

func normalizedArgs(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if args == nil {
		return []string{}
	}
	return args
}

func addCallContext(req map[string]any, args []string, env ForwardEnv) error {
	origin := env.OriginRepo
	if origin == nil {
		origin = gitOriginRepo
	}
	if repo := origin(); repo != "" {
		req["repo"] = repo
	}
	if readsDash(args) {
		data, err := io.ReadAll(io.LimitReader(env.Stdin, StdinCap+1))
		if err != nil {
			return fmt.Errorf("cannot read standard input: %w", err)
		}
		if len(data) > StdinCap {
			return fmt.Errorf("standard input is over the %d-byte cap the broker takes", StdinCap)
		}
		req["stdin"] = base64.StdEncoding.EncodeToString(data)
	}
	return nil
}

func hasAccessSchema(body []byte, want string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return false
	}
	var got string
	if json.Unmarshal(object["schema"], &got) != nil || got != want {
		return false
	}
	if want == "yolo.access/1" {
		for _, key := range []string{"source", "state", "repositories", "read", "grants", "working_set", "requests", "limits"} {
			if _, ok := object[key]; !ok {
				return false
			}
		}
		return true
	}
	var verdict string
	if json.Unmarshal(object["verdict"], &verdict) != nil || (verdict != "runs" && verdict != "asks" && verdict != "refused") {
		return false
	}
	for _, key := range []string{"source", "call", "command_digest", "covered_by", "next", "relies_on_until", "rule"} {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func forwardFrame(req map[string]any, env ForwardEnv, capture bool) ([]byte, int) {
	body, _ := json.Marshal(req)
	endpoint := env.Getenv(EndpointEnv)
	if endpoint == "" {
		fmt.Fprintf(env.Stderr, "github-broker: no endpoint is published; run `yolo check` on the host, then relaunch the jail\n")
		return nil, ExitUnavailable
	}
	conn, err := svcendpoint.Dial(endpoint, dialTimeout)
	if err != nil {
		fmt.Fprintf(env.Stderr, "github-broker: cannot reach the broker named by %s: %v\n", endpoint, err)
		return nil, ExitUnavailable
	}
	defer conn.Close()
	if err := frameproto.WriteRequest(conn, body); err != nil {
		fmt.Fprintf(env.Stderr, "github-broker: the broker stopped answering: %v\n", err)
		return nil, ExitUnavailable
	}
	var output []byte
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			fmt.Fprintln(env.Stderr, "github-broker: the broker stopped answering; see its host log")
			return nil, ExitUnavailable
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			if capture {
				output = append(output, f.Payload...)
			} else {
				_, _ = env.Stdout.Write(f.Payload)
			}
		case frameproto.StreamStderr:
			_, _ = env.Stderr.Write(f.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(f.Payload)
			if err != nil {
				return nil, ExitUnavailable
			}
			return output, rc
		}
	}
}

// readsDash reports whether the argv names standard input: a lone `-`, or a flag value
// glued on as `=-`. The broker decides whether the command reads it; the forwarder only
// sends it when the argv could.
func readsDash(args []string) bool {
	for _, a := range args {
		if a == "-" || strings.HasSuffix(a, "=-") {
			return true
		}
	}
	return false
}

// gitOriginRepo reads the workspace's origin remote with the jail's own git.
func gitOriginRepo() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return brokerscope.RepoFromRemoteURL(strings.TrimSpace(string(out)), "github.com")
}
