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
//     session, and runs the daemon's poll for that long, then one last pass. `yolo host` on
//     Linux runs the same session (OnceSession, HostOnceSession; ID-21) on the real home's store.
//
// The composition is the launch's frozen one (frozenLaunchMCP), with the same provenance rule a
// broker's share has: only a server the user's own config declares, not one a workspace
// redefined, is ever refreshed, so a workspace cannot steer the refresh token to a URL of its
// own (onceShare).

import (
	"context"
	"path"
	"path/filepath"
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

// onceTierMode is how a jail launcher itself keeps a sign_in = "once" server on backend rt, or
// "" where a broker does (Linux podman). `yolo host` is no backend here: on Linux it keeps the
// tier through HostOnceSession (ID-21).
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
// on a Mac names when it says it does not keep them (OQ-MA10).
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
	session := &OnceSession{share: share, log: func(line string) { o.logRecord("mcp-auth: %s", line) },
		endFailed: func(err error) {
			o.pr(o.Stderr).print("MCP one sign-in: the pass at this session's end did not finish (" + richtext.Escape(err.Error()) +
				"); the next session tries again. yolo audit --service mcp-auth shows what was done.")
		}}
	return session.Start()
}

// OnceSession is the one-sign-in tier kept for the length of one session by the process that
// lives exactly as long as it: a macos-user launcher (ID-20) or `yolo host` on Linux (ID-21). Both
// run the one poll below, the daemon's own (mcpauthdaemon.ServeOnce), never a second loop.
type OnceSession struct {
	share mcpauth.Share
	// log takes the lines of the poll and of the last pass, which must never reach the terminal
	// the agent holds; endFailed says, on the terminal once the agent is gone, that the last pass
	// did not finish.
	log       func(string)
	endFailed func(error)
}

// Names is the session's one-sign-in servers, comma-separated, for its launch line.
func (s *OnceSession) Names() string { return onceNames(s.share) }

// Stores is the store files the session adopts from and writes back to, for its launch line.
func (s *OnceSession) Stores() []string {
	out := make([]string, 0, len(s.share.Sources))
	for _, src := range s.share.Sources {
		out = append(out, filepath.Join(src.Dir, src.Store))
	}
	return out
}

// Start runs the daemon's poll from now until the returned stop, which ends the poll, runs one
// last pass, and returns.
func (s *OnceSession) Start() (stop func()) {
	var mu sync.Mutex
	logLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		if s.log != nil {
			s.log(line)
		}
	}
	state := loopholes.StateDirFor(selectedMCPHostService)
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		if err := onceServe(state, s.share, logLine, quit); err != nil {
			logLine("the session's one-sign-in poll did not start: " + err.Error())
		}
	}()
	return func() {
		close(quit)
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), onceBoundaryBudget)
		defer cancel()
		if err := onceBoundaryPass(ctx, state, s.share, logLine); err != nil && s.endFailed != nil {
			s.endFailed(err)
		}
	}
}

// HostOnceJail is the jail name a `yolo host` session's share and audit events carry: it runs in
// no jail, and the name says so.
const HostOnceJail = "host"

// HostOnceSession is the tier for one `yolo host` session on Linux (ID-21, OQ-MA10): the user's
// own sign_in = "once" servers, adopted from and written back to the real home's store of the
// launched program's own pack (agent; ~/.claude/.credentials.json for Claude), so the record's
// source_dir names that store (ID-18). cfg and packs are the host launch's user-scope config and
// selection: at `yolo host` every server composed is the user's (mcp-auth-sharing.md §6). nil,
// nil when there is nothing to keep: no such server, or a pack whose store is not a supported
// JSON store under proper-lockfile. log and endFailed are OnceSession's.
func HostOnceSession(cfg *jsonx.OrderedMap, packs []*packload.Pack, agent *packload.Pack, home, workspace string,
	log func(string), endFailed func(error)) (*OnceSession, error) {
	if agent == nil || !hasOnceServer(cfg) {
		return nil, nil
	}
	servers := packload.ComposeMCPServers(cfgMap(cfg, "mcp_servers"), packs, home)
	in := LaunchInputContext{Workspace: workspace, Container: HostOnceJail, EffectiveMCPServers: servers,
		UserOnlyMCPServers: servers, MCPScopes: packload.MCPServerScopes(servers, servers), MCPProven: true}
	once, sources, err := onceShareFrom(in, packs, func(pack *packload.Pack, store string) string {
		if pack != agent {
			return ""
		}
		return filepath.Join(home, filepath.FromSlash(path.Dir(store)))
	})
	if err != nil || len(once) == 0 {
		return nil, err
	}
	share := mcpauth.Share{Version: 1, Jail: HostOnceJail, Workspace: workspace, WrittenAt: time.Now().UTC(),
		Replicas: []mcpauth.Replica{}, Once: once, Sources: sources}
	if err := share.Validate(); err != nil {
		return nil, err
	}
	return &OnceSession{share: share, log: log, endFailed: endFailed}, nil
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
