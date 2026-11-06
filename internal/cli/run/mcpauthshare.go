package run

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func produceMCPAuthShare(in LaunchInputContext) ([]byte, error) {
	// This slice has one kernel and Codex's flock. A different backend must never
	// receive an access-token replica as if its lock or credential store were known.
	if in.Runtime != "podman" || runtime.GOOS != "linux" || !in.MCPProven {
		return json.Marshal(mcpauth.Share{Version: 1, Jail: in.Container, Workspace: in.Workspace, Home: shareHomeRel(in), WrittenAt: time.Now().UTC(), Replicas: []mcpauth.Replica{}})
	}
	if in.PackTree == "" {
		return nil, fmt.Errorf("mcp-auth: selected staged pack tree is absent; retry the launch")
	}
	packs, err := loadPackTree(in.PackTree)
	if err != nil {
		return nil, fmt.Errorf("mcp-auth: cannot read selected staged packs; retry with a readable pack tree: %w", err)
	}
	return mcpShareFromPacks(in, packs)
}

func mcpShareFromPacks(in LaunchInputContext, packs []*packload.Pack) ([]byte, error) {
	share := mcpauth.Share{Version: 1, Jail: in.Container, Workspace: in.Workspace, Home: shareHomeRel(in), WrittenAt: time.Now().UTC(), Replicas: []mcpauth.Replica{}}
	names := in.EffectiveMCPServers.Keys()
	sort.Strings(names)
	for _, pack := range packs {
		if pack == nil || pack.Decl == nil || pack.Decl.MCPAuth == nil {
			continue
		}
		decl := pack.Decl.MCPAuth
		if decl.Format != "json" || decl.Lock == nil || decl.Lock.Protocol != "flock" {
			continue
		}
		rootPart, store, lock, err := replicaPaths(decl.Store, decl.Lock.Path)
		if err != nil {
			return nil, fmt.Errorf("mcp-auth: %s: %w", pack.Name, err)
		}
		replica := mcpauth.Replica{Pack: pack.Name, DeriveFile: filepath.Join(pack.Root, "derive.lua"), Root: []string{"home", rootPart}, Store: store, Lock: *decl.Lock, Decl: *decl, Shared: []mcpauth.SharedEntry{}}
		replica.Lock.Path = lock
		replica.Decl.Lock = &replica.Lock
		refs := make([]luahook.MCPServerRef, 0, len(names))
		identities := map[string]string{}
		clientIDs := map[string]string{}
		for _, name := range names {
			if in.MCPScopes[name].Level != "user" {
				continue
			}
			raw, _ := in.EffectiveMCPServers.Get(name)
			server, ok := jsonx.Plain(raw).(map[string]any)
			if !ok {
				continue
			}
			// Boot evaluates requires_env per agent and derives provider capability
			// eligibility. Neither per-agent environment nor provider facts are in
			// this frozen input; never infer eligibility from the host's environment.
			if _, conditional := server["requires_env"]; conditional {
				continue
			}
			if _, conditional := server["provides"]; conditional {
				continue
			}
			url, remote := server["url"].(string)
			if !remote || url == "" {
				continue
			}
			transport, _ := server["transport"].(string)
			if transport == "" {
				transport = "http"
			}
			// The pack's derive only renders declared remote transports.
			supported := false
			for _, form := range pack.Decl.MCPRemoteTransports() {
				if form == transport {
					supported = true
				}
			}
			if !supported {
				continue
			}
			headers := map[string]string{}
			if rawHeaders, ok := server["headers"].(map[string]any); ok {
				for key, value := range rawHeaders {
					v, ok := value.(string)
					if !ok {
						return nil, fmt.Errorf("mcp-auth: %s: invalid headers in approved MCP server", name)
					}
					headers[key] = v
				}
			}
			clientID := ""
			if oauth, ok := server["oauth"].(map[string]any); ok {
				clientID, _ = oauth["client_id"].(string)
			}
			digest, err := mcpIdentity(url, transport, headers, clientID)
			if err != nil {
				return nil, err
			}
			refs = append(refs, luahook.MCPServerRef{Name: name, URL: url, Transport: transport, Headers: headers, ClientID: clientID})
			identities[name] = digest
			clientIDs[name] = clientID
		}
		if len(refs) == 0 {
			continue
		}
		script := packload.DeriveScript(pack)
		if script == "" {
			return nil, fmt.Errorf("mcp-auth: selected pack %s has no store key function; repair its derive and retry", pack.Name)
		}
		keys, err := luahook.MCPAuthKeys(script, pack.Name, refs)
		if err != nil {
			return nil, fmt.Errorf("mcp-auth: selected pack %s cannot derive a store key: %w", pack.Name, err)
		}
		for _, server := range refs {
			key := keys[server.Name]
			if key == "" {
				return nil, fmt.Errorf("mcp-auth: %s: missing derived store key; repair the selected pack", server.Name)
			}
			replica.Shared = append(replica.Shared, mcpauth.SharedEntry{Name: server.Name, Identity: identities[server.Name], URL: server.URL, Key: key, ClientID: clientIDs[server.Name]})
		}
		share.Replicas = append(share.Replicas, replica)
	}
	if err := share.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(share)
}

func replicaPaths(store, lock string) (string, string, string, error) {
	store = wsStateHomeRel("podman", store)
	lock = wsStateHomeRel("podman", lock)
	parts := strings.Split(filepath.ToSlash(store), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || !strings.HasPrefix(filepath.ToSlash(lock), parts[0]+"/") {
		return "", "", "", fmt.Errorf("store and lock must share a single declared home directory; correct the pack's mcp_auth paths")
	}
	return parts[0], parts[1], strings.TrimPrefix(filepath.ToSlash(lock), parts[0]+"/"), nil
}

func mcpIdentity(url, transport string, headers map[string]string, clientID string) (string, error) {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"url": url, "transport": transport, "headers": headers, "client_id": clientID}); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(body.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}

// shareHomeRel is the share's Home: the launch's workspace home relative to <workspace>/.yolo, ""
// for the legacy home, so the broker reconciles the replicas of the home this launch binds
// (docs/design/named-setups.md §9; TestMCPAuthShareNamesTheLaunchsHome).
func shareHomeRel(in LaunchInputContext) string {
	rel, ok := paths.HomeStateRel(in.Workspace, in.Home)
	if !ok || rel == "home" {
		return ""
	}
	return rel
}
