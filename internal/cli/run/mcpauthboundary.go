package run

// mcpauthboundary.go is the one-sign-in tier (MA-D17, docs/design/mcp-auth-sharing.md §9.3) on a
// backend where no mcp-auth broker runs for the launch: Apple Container and podman on macOS,
// whose host daemon is not admitted (§10, *Daemon admitted*), and macos-user, which starts no
// such daemon. The launcher is the tier's refresher there itself, with the daemon's own code
// (mcpauthdaemon.BoundaryPass, ServeOnce), its canonical store and its audit log:
//
//   - LAUNCH-BOUNDARY (ID-19), on the two container backends: one pass when a fresh jail starts,
//     before the container does, and one in the keeper once the container is gone (§8.5). A
//     lock over the VM file share is unmeasured, so nothing runs while the jail does.
//   - SESSION-LONG (ID-20), on macos-user: one kernel, so Claude's lock directory holds between
//     the host and the agent (§10, *Live sync*); the launcher lives exactly as long as the
//     session, and runs the daemon's poll for that long, then one last pass.
//
// The composition is the launch's frozen one (frozenLaunchMCP), with the same provenance rule a
// broker's share has: only a server the user's own config declares, not one a workspace
// redefined, is ever refreshed, so a workspace cannot steer the refresh token to a URL of its
// own (onceShare).

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

const (
	onceModeBoundary = "boundary"
	onceModeSession  = "session"

	onceAtStart = "start"
	onceAtExit  = "exit"
)

// onceTierGOOS is the host OS the mode reads, a variable so a test can stand in for a Mac.
var onceTierGOOS = runtime.GOOS

// onceTierMode is how the launcher itself keeps a sign_in = "once" server on backend rt, or ""
// where a broker does (Linux podman) or nothing can (`yolo host`, OQ-MA10).
func onceTierMode(rt string) string {
	switch {
	case rt == "container", rt == "podman" && onceTierGOOS != "linux": // parity: HonoredBy — no broker is admitted, so the launcher keeps the tier at the launch boundary (ID-19)
		return onceModeBoundary
	case rt == "macos-user": // parity: HonoredBy — no broker runs, so the launcher keeps the tier for the session (ID-20)
		return onceModeSession
	}
	return ""
}

// onceTierWhen is the mode in the launch line's words.
func onceTierWhen(rt string) string {
	if onceTierMode(rt) == onceModeSession {
		return "while each session runs, and once more when it ends"
	}
	return "when a jail starts and again when it ends"
}

// The launcher's halves of the tier, seams so a launch test can observe the call without an
// authorization server; production runs the daemon's own code.
var (
	onceBoundaryPass = mcpauthdaemon.BoundaryPass
	onceServe        = mcpauthdaemon.ServeOnce
)

// onceBoundaryBudget bounds one launcher pass: a failure guard, so an authorization server
// that never answers cannot hold a launch, not a deadline a healthy pass has to beat.
var onceBoundaryBudget = 30 * time.Second

// hasOnceServer reports whether cfg declares any sign_in = "once" server. Only the user's own
// config can (config.mcpSignInProjectRefusal), so this reads no provenance; onceShare does.
func hasOnceServer(cfg *jsonx.OrderedMap) bool {
	return len(OnceServerNames(cfg)) > 0
}

// OnceServerNames is the sign_in = "once" servers cfg declares, in config order: what `yolo host`
// names when it says it does not keep them (OQ-MA10).
func OnceServerNames(cfg *jsonx.OrderedMap) []string {
	servers := cfgMap(cfg, "mcp_servers")
	if servers == nil {
		return nil
	}
	var names []string
	for _, name := range servers.Keys() {
		raw, _ := servers.Get(name)
		if server, ok := jsonx.Plain(raw).(map[string]any); ok && server["sign_in"] == "once" {
			names = append(names, name)
		}
	}
	return names
}

// onceLaunchShare is the tier's share for this launch from its frozen composition: the user's
// own one-sign-in servers and the machine-wide store their sign-ins are adopted from. false
// when there is nothing for the launcher to do.
func (o *Options) onceLaunchShare(cname string, packs []*packload.Pack) (mcpauth.Share, bool) {
	frozen := o.launchInputMCP
	if !frozen.Proven || frozen.SharedCredentialDir == "" {
		return mcpauth.Share{}, false
	}
	effective, effectiveErr := jsonx.Decode([]byte(frozen.EffectiveJSON))
	userOnly, userErr := jsonx.Decode([]byte(frozen.UserOnlyJSON))
	effectiveMap, _ := effective.(*jsonx.OrderedMap)
	userMap, _ := userOnly.(*jsonx.OrderedMap)
	if effectiveErr != nil || userErr != nil || effectiveMap == nil || userMap == nil {
		o.pr(o.Stderr).print("MCP one sign-in: this launch's composition could not be read back, so yolo did not refresh its sign-ins. Relaunch; if it repeats, run yolo check.")
		return mcpauth.Share{}, false
	}
	in := LaunchInputContext{Workspace: o.Workspace, Container: cname, EffectiveMCPServers: effectiveMap,
		UserOnlyMCPServers: userMap, MCPScopes: packload.MCPServerScopes(effectiveMap, userMap), MCPProven: true,
		SharedCredentialDir: frozen.SharedCredentialDir, SharedCredentialHome: frozen.SharedCredentialHome}
	once, sources, err := onceShare(in, packs)
	if err != nil {
		o.pr(o.Stderr).print("MCP one sign-in: " + richtext.Escape(err.Error()))
		return mcpauth.Share{}, false
	}
	if len(once) == 0 {
		return mcpauth.Share{}, false
	}
	share := mcpauth.Share{Version: 1, Jail: cname, Workspace: o.Workspace, WrittenAt: time.Now().UTC(),
		Replicas: []mcpauth.Replica{}, Once: once, Sources: sources}
	if err := share.Validate(); err != nil {
		o.pr(o.Stderr).print("MCP one sign-in: " + richtext.Escape(err.Error()))
		return mcpauth.Share{}, false
	}
	return share, true
}

func onceNames(share mcpauth.Share) string {
	names := make([]string, 0, len(share.Once))
	for _, server := range share.Once {
		names = append(names, server.Name)
	}
	return strings.Join(names, ", ")
}

// runOnceBoundary is one launch-boundary pass (ID-19) at phase: before a fresh jail's container
// starts, or in its keeper once the container is gone. A no-op on any other backend.
func (o *Options) runOnceBoundary(rt, cname, phase string, packs []*packload.Pack) {
	if o.DryRun || onceTierMode(rt) != onceModeBoundary {
		return
	}
	share, ok := o.onceLaunchShare(cname, packs)
	if !ok {
		return
	}
	out := o.pr(o.Stderr)
	if phase == onceAtStart {
		out.print("MCP one sign-in: " + onceNames(share) + " signs in once for every jail on this machine. No broker runs on " +
			backendLabel(rt) + ", so yolo takes its sign-in over and refreshes it " + onceTierWhen(rt) +
			"; a sign-in made while a jail runs is taken over at the next start or end. Follow it with yolo audit --service mcp-auth.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), onceBoundaryBudget)
	defer cancel()
	err := onceBoundaryPass(ctx, loopholes.StateDirFor(selectedMCPHostService), share, func(line string) {
		out.print("mcp-auth: " + richtext.Escape(line))
	})
	if err != nil {
		out.print("MCP one sign-in: the pass at this jail's " + phase + " did not finish (" + richtext.Escape(err.Error()) +
			"); the agent keeps the sign-in it has, and the next start or end tries again. yolo audit --service mcp-auth shows what was done.")
	}
}

// startOnceSession runs the tier for one macos-user session (ID-20): the daemon's poll from now
// until the returned stop, which ends the poll, runs one last pass, and returns. Its lines go to
// the launch log while the session runs, never to the terminal the agent holds.
func (o *Options) startOnceSession(rt, cname string, packs []*packload.Pack) (stop func()) {
	if o.DryRun || onceTierMode(rt) != onceModeSession {
		return func() {}
	}
	share, ok := o.onceLaunchShare(cname, packs)
	if !ok {
		return func() {}
	}
	o.pr(o.Stderr).print("MCP one sign-in: " + onceNames(share) + " signs in once for every session on this machine. yolo refreshes its sign-in itself " +
		onceTierWhen(rt) + ". Follow it with yolo audit --service mcp-auth.")
	var mu sync.Mutex
	logLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		o.logRecord("mcp-auth: %s", line)
	}
	state := loopholes.StateDirFor(selectedMCPHostService)
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		if err := onceServe(state, share, logLine, quit); err != nil {
			logLine("the session's one-sign-in poll did not start: " + err.Error())
		}
	}()
	return func() {
		close(quit)
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), onceBoundaryBudget)
		defer cancel()
		if err := onceBoundaryPass(ctx, state, share, logLine); err != nil {
			o.pr(o.Stderr).print("MCP one sign-in: the pass at this session's end did not finish (" + richtext.Escape(err.Error()) +
				"); the next session tries again. yolo audit --service mcp-auth shows what was done.")
		}
	}
}

// backendLabel is a backend's name in a launch line.
func backendLabel(rt string) string {
	switch rt {
	case "container":
		return "Apple Container"
	case "podman":
		if onceTierGOOS != "linux" {
			return "podman on macOS"
		}
	}
	return rt
}
