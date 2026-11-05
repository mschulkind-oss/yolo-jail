package run

import (
	"runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func mcpUnavailableLines(packs []*packload.Pack, backend string) []string {
	var lines []string
	for _, pack := range packs {
		if pack == nil || pack.Decl == nil || pack.Decl.MCPAuth == nil {
			continue
		}
		decl := pack.Decl.MCPAuth
		lines = append(lines, "MCP sign-ins for "+pack.Name+" are not shared on "+backend+" in this yolo yet; each workspace signs in once ("+decl.LoginHint+"). To share them, use a Linux podman jail.")
	}
	return lines
}

func (o *Options) noteMCPAuthShare(in LaunchInputContext, data []byte) {
	share, err := mcpauth.DecodeShare(data)
	if err != nil {
		return
	} // writeLaunchInput already rejects this producer's invalid data.
	for _, replica := range share.Replicas {
		shared := make([]string, 0, len(replica.Shared))
		for _, s := range replica.Shared {
			shared = append(shared, s.Name)
		}
		if len(shared) > 0 {
			o.pr(o.Stderr).print("MCP sign-ins (" + replica.Pack + "): " + strings.Join(shared, ", ") + " eligible for a verified canonical copy at launch. A new native sign-in whose original headers cannot be proved stays in this workspace and is not harvested into user scope; use " + strings.ReplaceAll(replica.Decl.LoginHint, "{name}", "<name>") + " in each workspace until origin can be verified.")
		}
	}
	if in.Runtime != "podman" || runtime.GOOS != "linux" || !in.MCPProven {
		if in.Runtime != "podman" || runtime.GOOS != "linux" {
			packs, err := loadPackTree(in.PackTree)
			if err != nil {
				o.pr(o.Stderr).print("MCP sign-in status is unavailable because the selected pack tree cannot be read; retry the launch with a readable pack tree.")
				return
			}
			backend := in.Runtime
			if in.Runtime == "podman" {
				backend = "podman on macOS"
			}
			for _, line := range mcpUnavailableLines(packs, backend) {
				o.pr(o.Stderr).print(line)
			}
			return
		}
		o.pr(o.Stderr).print("MCP sign-ins: user-level provenance could not be proved, so no sign-in was shared. Retry the launch after checking the selected user packs with yolo pack ls.")
		return
	}
	names := make([]string, 0, len(in.MCPScopes))
	for name := range in.MCPScopes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		scope := in.MCPScopes[name]
		if scope.Level != "workspace" {
			continue
		}
		raw, exists := in.EffectiveMCPServers.Get(name)
		if !exists {
			continue
		}
		server, ok := jsonx.Plain(raw).(map[string]any)
		if !ok || server["url"] == nil {
			continue
		}
		o.pr(o.Stderr).print("MCP sign-in " + name + " kept to this workspace (" + scope.Reason + "). Inspect it with yolo config why mcp_servers." + name + " before moving its declaration into user config.")
	}
}
