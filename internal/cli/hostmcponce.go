package cli

// hostmcponce.go is the one-sign-in tier at `yolo host` (docs/design/mcp-auth-sharing.md §10,
// OQ-MA10; the plan's ID-21).
//
//   - ON LINUX it is kept by default, as on every jail backend: host Claude keeps its MCP
//     sign-ins in the real home's ~/.claude/.credentials.json, so this launch, which then stays
//     resident for the program's life instead of exec'ing it, runs the daemon's own poll on that
//     file while the program runs and one last pass when it ends (run.HostOnceSession, the session
//     macos-user runs, ID-20). It adopts a sign-in's refresh token into the canonical record and
//     writes fresh access tokens back to the one mcpOAuth entry under Claude's own
//     .storage-write.lock (ID-12): the one write yolo makes into the user's own ~/.claude, which
//     OQ-NC7 otherwise leaves alone, ruled so that a host Claude and the broker are never two
//     refreshers of one grant (MA-D10). The record names ~/.claude as its source store (ID-18).
//   - ON A MAC it is not kept: host Claude keeps its sign-ins in the Keychain, which yolo cannot
//     read or write, and the launch says so and names the jail launch that keeps it.
//   - UNDER THE CLAUDE CREDENTIAL VIEW (hostclaudeview.go, CL-D27) the program's store is the
//     managed view directory, not ~/.claude, so it is not kept there either, as in a jail's
//     credential-view mode (ID-16), and the launch says so.

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostOnceGOOS is the host OS the tier reads, a variable so a test can stand in for a Mac.
var hostOnceGOOS = runtime.GOOS

// hostOnceStart starts the session's poll and returns its stop, the last pass: a seam so a launch
// test can observe the call without an authorization server.
var hostOnceStart = func(s *run.OnceSession) (stop func()) { return s.Start() }

// hostOnceSignIn is the tier for this launch: the session to run while the program does, or nil
// when nothing is kept, having said why on errw where a sign_in = "once" server is configured.
// environ is the program's environment as composed so far (the credential view's variable
// included); log takes the session's lines while the program holds the terminal.
func hostOnceSignIn(launch *hostComposition, environ []string, log *run.HostLaunchLog, errw io.Writer) *run.OnceSession {
	if config.InJail() {
		return nil
	}
	pack := hostInstallingPack(launch.packs, launch.agent)
	if pack == nil || pack.Decl == nil || pack.Decl.MCPAuth == nil {
		return nil
	}
	names := run.OnceServerNames(launch.cfg)
	if len(names) == 0 {
		return nil
	}
	if hostOnceGOOS == "darwin" {
		fmt.Fprintf(errw, "yolo host: MCP one sign-in is not kept at yolo host on macOS: %s signs in on its own here, "+
			"because %s keeps its MCP sign-ins in the macOS Keychain, which yolo cannot read or write. "+
			"To use the one sign-in every jail shares, run %s in a jail with `yolo -- %s`\n",
			strings.Join(names, ", "), launch.agent, launch.agent, launch.agent)
		return nil
	}
	if dir := hostEnvironValue(environ, claudeview.SecureStorageEnv); dir != "" {
		fmt.Fprintf(errw, "yolo host: MCP one sign-in is not kept for this launch: %s keeps its MCP sign-ins in %s "+
			"here (%s), not in your ~/.claude, so %s signs in on its own. Launch without %s=1, and with %s unset, to keep it\n",
			launch.agent, homeTilde(dir), claudeview.SecureStorageEnv, strings.Join(names, ", "), claudeview.SwitchEnv,
			claudeview.SecureStorageEnv)
		return nil
	}
	session, err := run.HostOnceSession(launch.cfg, launch.packs, pack, paths.Home(), launch.workspace,
		func(line string) { log.Record("mcp-auth: " + line) },
		func(err error) {
			fmt.Fprintf(errw, "yolo host: MCP one sign-in: the pass at this session's end did not finish (%v); "+
				"the next session tries again. `yolo audit --service mcp-auth` shows what was done\n", err)
		})
	if err != nil {
		fmt.Fprintf(errw, "yolo host: MCP one sign-in is not kept for this launch (%v), so %s signs in on its own; "+
			"fix what it names and launch again\n", err, strings.Join(names, ", "))
		return nil
	}
	if session == nil {
		return nil
	}
	stores := session.Stores()
	for i, s := range stores {
		stores[i] = homeTilde(s)
	}
	fmt.Fprintf(errw, "yolo host: MCP one sign-in: %s signs in once for every jail and host session on this machine. "+
		"yolo takes %s's sign-in over from %s and refreshes it while %s runs, and once more when it ends, "+
		"writing only that server's entry, under %s's own lock; so this launch stays resident instead of exec'ing %s. "+
		"Follow it with `yolo audit --service mcp-auth`\n",
		session.Names(), launch.agent, strings.Join(stores, ", "), launch.agent, launch.agent, launch.agent)
	return session
}

// hostEnvironValue is name's value in environ, "" when unset.
func hostEnvironValue(environ []string, name string) string {
	value := ""
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			value = v
		}
	}
	return value
}
