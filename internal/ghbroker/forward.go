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

// Forward runs one forwarded call and returns the exit code.
func Forward(args []string, env ForwardEnv) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	say := func(format string, a ...any) { fmt.Fprintf(env.Stderr, "gh (github-broker): "+format+"\n", a...) }
	endpoint := env.Getenv(EndpointEnv)
	if endpoint == "" {
		// The switch is per project and host-side (docs/design/boundary-broker.md OQ-BB13), so the
		// step names the command to run there, with this jail's workspace as the host names it
		// when the launch said (YOLO_HOST_DIR).
		where := "    yolo loopholes enable github-broker\n  in this project, on the host,"
		if hostDir := env.Getenv("YOLO_HOST_DIR"); hostDir != "" {
			where = "    yolo loopholes enable github-broker --workspace " + shquote.QuoteDisplay(hostDir) + "\n  on the host,"
		}
		say("this jail has no github-broker endpoint, so `gh` cannot reach the host's GitHub login.\n" +
			"  gh here is forwarded by the `github` pack; its loophole is off in a project until turned on there:\n" +
			where + " then start a fresh jail (`yolo check` shows whether it runs). Apple Container runs\n" +
			"  no host services, so there it stays off. The image's own gh is still reachable by path, or\n" +
			"  with YOLO_BYPASS_SHIMS=1, and holds no GitHub credential in the jail.")
		return ExitUnavailable
	}

	req := map[string]any{"argv": args}
	if args == nil {
		req["argv"] = []string{}
	}
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
			say("cannot read standard input: %v", err)
			return ExitUsage
		}
		if len(data) > StdinCap {
			say("standard input is over the %d-byte cap the broker takes", StdinCap)
			return ExitUsage
		}
		req["stdin"] = base64.StdEncoding.EncodeToString(data)
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
