package mcpauthdaemon

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

// StatusHandlerForShare constructs only a reader. The front still owns endpoint
// authentication; this handler checks the frozen jail before executing any read.
// Source projection is deferred until execution, never handler construction.
func StatusHandlerForShare(stateDir string, share mcpauth.Share, session ShareSessionBinding, sources func() []mcpauth.OnceSource) func(*hostservice.Session) {
	return func(s *hostservice.Session) {
		op, _ := s.Get("op")
		if op != "status" || !session.accepts(share, s) {
			s.Stderr("mcp-auth: unsupported request or jail; inspect the selected pack and retry\n")
			s.Exit(2)
			return
		}
		agents := make([]any, 0, len(share.Replicas))
		for _, replica := range share.Replicas {
			store := &mcpauth.CanonicalStore{Dir: stateDir, Filename: replica.Pack + ".json", LockName: replica.Pack + ".lock", Wait: mcpauth.HeaderReadWait}
			file, readErr := store.ReadStatus()
			servers := make([]any, 0, len(replica.Shared))
			for _, shared := range replica.Shared {
				row := map[string]any{"name": shared.Name, "scope": "user", "state": "not-signed-in", "next_step": strings.ReplaceAll(replica.Decl.LoginHint, "{name}", shared.Name)}
				if readErr != nil {
					row["availability"] = "unavailable"
					row["next_step"] = "canonical state is unavailable; run yolo mcp-auth status on the host, then restore the private regular state and retry"
				}
				for _, record := range file.Records {
					if record.Server == shared.Name && record.Identity == shared.Identity {
						row["state"], row["generation"], row["expires_at_ms"] = record.State, int64(record.Generation), record.ExpiresAtMS
						if record.State == "live" {
							row["next_step"] = ""
						}
						break
					}
				}
				servers = append(servers, row)
			}
			agent := map[string]any{"pack": replica.Pack, "sharing": "launch-boundary", "servers": servers}
			if readErr != nil {
				agent["availability"] = "unavailable"
				agent["next_step"] = "canonical state is unavailable; run yolo mcp-auth status on the host, then restore private regular state and retry"
			}
			agents = append(agents, agent)
		}
		var snapshots []mcpauth.OnceSource
		if sources != nil {
			snapshots = sources()
		}
		requested, _ := s.Get("server")
		name, _ := requested.(string)
		once := ReadOnceStatus(stateDir, share, snapshots, "this launch's broker", name)
		_ = s.JSON(map[string]any{"schema": "yolo.mcp-auth.status/1", "agents": agents, "once": anyRows(once)})
		s.Exit(0)
	}
}
