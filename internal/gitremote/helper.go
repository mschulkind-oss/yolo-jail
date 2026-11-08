// Package gitremote is the jail side of the git-broker (docs/design/git-broker.md §4.1):
// git's remote helper `git-remote-yolo`, reached as `yolo git-remote -- <remote> <url>`.
//
// The jail's git rewrites every `ssh://`, `https://` and `git@` remote to `yolo::<url>`
// (docs/design/git-broker-plan.md ID-4), so git runs this helper with the original URL. It
// answers `capabilities` with `connect` alone, and on `connect <service>` it sends one
// request frame to the broker's endpoint and then relays git's bytes both ways
// (ID-5). It holds no git credential: the leg to the broker is the loophole transport's
// own token, and the broker decides what reaches the upstream.
//
// This package must not import the daemon's code (internal/gitbroker), which runs on the
// host (ID-1), nor share code with internal/ghbroker across the jail/host boundary.
package gitremote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// LoopholeName is the loophole packs/git ships; the jail finds its endpoint through the
// variable the launch derives from this name.
const LoopholeName = "git-broker"

// EndpointEnv is the variable naming the broker's endpoint file in the jail, derived as
// ghbroker.EndpointEnv is.
var EndpointEnv = paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug(LoopholeName) + paths.ServiceEnvVarSuffix

// dialTimeout bounds the dial and the accept ack only; a transfer streams for as long as
// git and the broker keep it open.
const dialTimeout = 30 * time.Second

// The exit codes the helper itself returns, the same numbers as brokercore's, spelled
// here so the jail's helper does not link the host broker's core.
const (
	exitUsage       = 64 // EX_USAGE: a command or service this helper does not carry
	exitUnavailable = 69 // EX_UNAVAILABLE: no endpoint, or the broker stopped answering
)

// gitGone is said when git stops reading the helper's output mid-transfer.
const gitGone = "git stopped reading this helper's output (%v), so the transfer ended early; " +
	"run the git command again"

// relayChunk is the most of git's bytes one Stdout frame carries.
const relayChunk = 64 * 1024

// Env is what the helper reads from its process.
type Env struct {
	Getenv func(string) string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// accessTimeout shortens the client session in caller-level tests only.
	accessTimeout time.Duration
	// accessContext, when set, is the parent of the client session's deadline, so a
	// caller-level test can end the session on an event (a probe was entered) rather
	// than on a wall-clock budget every earlier probe must beat. Tests only.
	accessContext context.Context
}

// request is the one frame the helper sends before git's bytes (ID-5). Agent is left
// empty by this version: nothing in the jail names the running agent to the helper.
type request struct {
	Service  string `json:"service"`
	URL      string `json:"url"`
	Protocol string `json:"protocol"`
	Agent    string `json:"agent,omitempty"`
}

// Run speaks git's remote-helper protocol (gitremote-helpers(7)) on env's stdin and
// stdout, and returns the exit code. args are `[--] <remote> <url>`; only the URL is
// used.
func Run(args []string, env Env) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	// git passes `<remote> <url>` for a `yolo::<url>` remote, and the remote's name alone
	// when the remote has no URL of its own; the name is never sent as a URL.
	remote, url := "", ""
	if len(args) >= 2 {
		remote, url = args[0], args[1]
	} else if len(args) == 1 {
		remote = args[0]
	}
	say := func(format string, a ...any) {
		fmt.Fprintf(env.Stderr, "git-remote-yolo (git-broker): "+format+"\n", a...)
	}
	in := bufio.NewReader(env.Stdin)
	for {
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return 0 // git closed the command stream
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			return 0
		case line == "capabilities":
			_, _ = io.WriteString(env.Stdout, "connect\n\n")
		case strings.HasPrefix(line, "connect "):
			return connect(strings.TrimPrefix(line, "connect "), remote, url, in, env, say)
		default:
			say("git asked for %q, which this helper does not offer: it offers only `connect`. "+
				"Report this with your git version (`git --version`).", line)
			return exitUsage
		}
	}
}

// connect opens the broker stream for service and relays it until the broker's Exit frame.
func connect(service, remote, url string, in *bufio.Reader, env Env, say func(string, ...any)) int {
	if service != "git-upload-pack" && service != "git-receive-pack" {
		say("%s does not cross the git-broker: only fetch (git-upload-pack) and push "+
			"(git-receive-pack) do, so this command has no remote form in a jail. Run it on the host "+
			"instead, for example with `yolo host-run`.", service)
		return exitUsage
	}
	if url == "" && remote != "" {
		say("the remote %q has no URL, so there is nothing to connect to. Give it the remote's real "+
			"URL with `git remote set-url %s <url>`; the jail's git routes ssh://, https:// and git@ "+
			"URLs through the broker itself.", remote, shquote.QuoteDisplay(remote))
		return exitUsage
	}
	if url == "" {
		say("git gave this helper no URL; it runs as `git-remote-yolo <remote> <url>`, which git does " +
			"for a remote whose URL starts with `yolo::`. Run git itself (`git fetch <remote>`) rather " +
			"than this helper.")
		return exitUsage
	}
	endpoint := env.Getenv(EndpointEnv)
	if endpoint == "" {
		// The broker runs whenever the `git` pack is selected (docs/design/boundary-broker.md
		// BB-D104), so a missing endpoint is a launch that did not start it: the step is `yolo
		// check` on the host, in this jail's workspace as the host names it (YOLO_HOST_DIR).
		where := "in this project"
		if hostDir := env.Getenv("YOLO_HOST_DIR"); hostDir != "" {
			where = "in " + shquote.QuoteDisplay(hostDir)
		}
		say("this jail has no git-broker endpoint, so git cannot reach the host's remotes: the broker did not\n" +
			"  start for this launch. It runs whenever the `git` pack is selected; run `yolo check` on the host,\n" +
			"  " + where + ", to see why, then start a fresh jail. Apple Container runs no host services, so\n" +
			"  there it never starts. The jail holds no git credential.")
		return exitUnavailable
	}

	body, _ := json.Marshal(request{Service: service, URL: url, Protocol: env.Getenv("GIT_PROTOCOL")})
	conn, err := svcendpoint.Dial(endpoint, dialTimeout)
	if err != nil {
		switch {
		case errors.Is(err, svcendpoint.ErrEndpointMissing):
			say("no endpoint is published at %s: the broker never started on the host, or stopped; "+
				"relaunch the jail and see `yolo check`", endpoint)
		case errors.Is(err, svcendpoint.ErrAuthRejected):
			say("the broker rejected this jail's token (the endpoint file %s is stale); relaunch the jail", endpoint)
		default:
			say("cannot reach the broker named by %s: %v; see `yolo check` on the host, then start a "+
				"fresh jail", endpoint, err)
		}
		return exitUnavailable
	}
	defer conn.Close()
	if err := frameproto.WriteRequest(conn, body); err != nil {
		say("the broker stopped answering: %v; its log is "+
			"~/.local/share/yolo-jail/logs/host-service-git-broker.log on the host; see `yolo check` "+
			"there, then start a fresh jail", err)
		return exitUnavailable
	}

	// git's side: every byte after the connect line, including what the line reader
	// already buffered, goes up as Stdout frames; an empty one says git closed its side.
	go func() {
		buf := make([]byte, relayChunk)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				if _, werr := frameproto.WriteFrame(conn, frameproto.StreamStdout, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_, _ = frameproto.WriteFrame(conn, frameproto.StreamStdout, nil)
				return
			}
		}
	}()

	connected := false
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			say("the broker on the host stopped answering before git finished; its log is " +
				"~/.local/share/yolo-jail/logs/host-service-git-broker.log on the host")
			return exitUnavailable
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			if len(f.Payload) == 0 {
				continue
			}
			if !connected {
				// git's "connection established" reply comes only once the broker answers
				// with protocol bytes, so a refusal before any leaves git seeing the helper
				// fail rather than an empty remote.
				if _, err := io.WriteString(env.Stdout, "\n"); err != nil {
					say(gitGone, err)
					return exitUnavailable
				}
				connected = true
			}
			if _, err := env.Stdout.Write(f.Payload); err != nil {
				say(gitGone, err)
				return exitUnavailable
			}
		case frameproto.StreamStderr:
			_, _ = env.Stderr.Write(f.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(f.Payload)
			if err != nil {
				say("the broker sent a malformed exit frame (%v), so whether git's command finished is "+
					"unknown; check with `git status` or `git fetch`, and report it with the host's "+
					"~/.local/share/yolo-jail/logs/host-service-git-broker.log", err)
				return exitUnavailable
			}
			return rc
		}
	}
}
