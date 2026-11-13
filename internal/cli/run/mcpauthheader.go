package run

// mcpauthheader.go is the header route at `yolo host` (docs/design/mcp-auth-sharing.md §9.6, the
// plan's ID-22): `yolo mcp-auth header <server>` run on the host, where no broker runs for the
// caller, answers from the broker's own canonical store with the daemon's own code
// (mcpauthdaemon.HostHeader), so a sign-in any jail's broker or a `yolo host` session adopted is
// served to an agent at the host too.

import (
	"context"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostHeaderGOOS is the host OS the route reads, a variable so a test can stand in for a Mac.
var hostHeaderGOOS = runtime.GOOS

// HostHeaderShare is the share the host's header route answers with: cfg is the user-scope
// config and packs its selection, as at `yolo host`, where every server composed is the user's
// (mcp-auth-sharing.md §6). On Linux its sources are every selected pack's store in the real
// home, as HostOnceSession's are, so a refresh writes the fresh token back to the agent that
// signed in; on a Mac that store is the Keychain, so there is none, and the refresh writes
// nothing back.
func HostHeaderShare(cfg *jsonx.OrderedMap, packs []*packload.Pack, home, workspace string) (mcpauth.Share, error) {
	servers := packload.ComposeMCPServers(cfgMap(cfg, "mcp_servers"), packs, home)
	in := LaunchInputContext{Workspace: workspace, Container: HostOnceJail, EffectiveMCPServers: servers,
		UserOnlyMCPServers: servers, MCPScopes: packload.MCPServerScopes(servers, servers), MCPProven: true}
	share := mcpauth.Share{Version: 1, Jail: HostOnceJail, Workspace: workspace, WrittenAt: time.Now().UTC(), Replicas: []mcpauth.Replica{}}
	var err error
	if hostHeaderGOOS == "linux" {
		if share.Once, share.Sources, err = onceShareFrom(in, packs, func(_ *packload.Pack, store string) string {
			return filepath.Join(home, filepath.FromSlash(path.Dir(store)))
		}); err != nil {
			return mcpauth.Share{}, err
		}
	}
	if len(share.Once) == 0 {
		if share.Once, err = onceServersOf(in); err != nil {
			return mcpauth.Share{}, err
		}
	}
	return share, nil
}

// HostHeader is the access token for server from the host's canonical store, refreshed first when
// under five minutes remain; a refusal is an *mcpauthdaemon.HeaderRefusal naming the next step.
func HostHeader(ctx context.Context, share mcpauth.Share, server string, warn func(string)) (string, error) {
	return mcpauthdaemon.HostHeader(ctx, loopholes.StateDirFor(selectedMCPHostService), share, server, warn)
}
