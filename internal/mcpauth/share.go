package mcpauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Share is an immutable, host-produced instruction for one launch's selected replicas.
// It carries keys and declarations, never OAuth credentials or agent-written paths.
type Share struct {
	Version   int    `json:"version"`
	Jail      string `json:"jail"`
	Workspace string `json:"workspace"`
	// Home is the launch's workspace home relative to <workspace>/.yolo (paths.HomeStateRel):
	// "setups/<setup>/home", or "" for the legacy "home" (docs/design/named-setups.md §9). Each
	// replica's Root[0] "home" names this directory.
	Home      string    `json:"home,omitempty"`
	WrittenAt time.Time `json:"written_at"`
	Replicas  []Replica `json:"replicas"`
	// Once lists the user's `sign_in = "once"` servers, and Sources the machine-wide agent
	// stores their sign-ins are adopted from and written back to (MA-D17).
	Once    []OnceServer      `json:"once,omitempty"`
	Sources []Source          `json:"sources,omitempty"`
	SignIn  *mcpsignin.Launch `json:"sign_in,omitempty"`
}

// Source is a selected pack's sign-in store in an explicit host-owned directory.
// Logical pack provenance, declared Lua adapter and store paths are independent.
type Source struct {
	Pack string `json:"pack"`
	// Adapter is the selected program's exact Lua mcp_auth registration. Pack
	// remains logical provenance/a map key, never a filesystem component.
	// Its absence retains the old leaf-only descriptor behavior.
	Adapter    string           `json:"adapter,omitempty"`
	DeriveFile string           `json:"derive_file"`
	Dir        string           `json:"dir"`
	Store      string           `json:"store"`
	LockDir    string           `json:"lock_dir"`
	Decl       packdecl.MCPAuth `json:"decl"`
}

func (s Source) AdapterName() string {
	if s.Adapter != "" {
		return s.Adapter
	}
	if validLeaf(s.Pack) {
		return s.Pack
	}
	return ""
}

func (s Source) Validate() error {
	logical := s.Pack != "" && !filepath.IsAbs(s.Pack) && !strings.ContainsAny(s.Pack, "\x00\r\n")
	for _, part := range strings.Split(s.Pack, "/") {
		logical = logical && validLeaf(part)
	}
	if !logical || !validLeaf(s.AdapterName()) ||
		!filepath.IsAbs(s.DeriveFile) || filepath.Base(s.DeriveFile) != "derive.lua" ||
		!filepath.IsAbs(s.Dir) || filepath.Clean(s.Dir) != s.Dir || !validLeaf(s.Store) || !validLeaf(s.LockDir) ||
		s.Decl.Format != "json" || s.Decl.Lock == nil || s.Decl.Lock.Protocol != "proper-lockfile" || s.Decl.Lock.StaleSeconds <= 0 ||
		s.Decl.Fields.AccessToken == "" || s.Decl.Fields.RefreshToken == "" {
		return errors.New("mcp-auth: unsupported one-sign-in source; retry with a supported pack")
	}
	return nil
}

// SourceStore is the source's store with the lock wait the broker uses.
func (s Source) SourceStore(wait time.Duration) SourceStore {
	return SourceStore{Dir: s.Dir, File: s.Store, LockDir: s.LockDir, Stale: time.Duration(s.Decl.Lock.StaleSeconds) * time.Second,
		Wait: wait, EntriesAt: append([]string(nil), s.Decl.EntriesAt...)}
}

// HomeRel is the share's workspace home relative to <workspace>/.yolo: Home, or the legacy
// "home" when the share names none.
func (s Share) HomeRel() string {
	if s.Home == "" {
		return "home"
	}
	return s.Home
}

type Replica struct {
	Pack       string               `json:"pack"`
	DeriveFile string               `json:"derive_file"`
	Root       []string             `json:"workspace_state_subdirs"`
	Store      string               `json:"store"`
	Lock       packdecl.MCPAuthLock `json:"lock"`
	Decl       packdecl.MCPAuth     `json:"decl"`
	Shared     []SharedEntry        `json:"shared"`
}

type SharedEntry struct {
	Name     string `json:"name"`
	Identity string `json:"identity"`
	URL      string `json:"url"`
	Key      string `json:"key"`
	ClientID string `json:"client_id,omitempty"`
}

func (s Share) Validate() error {
	if s.Version != 1 || s.Jail == "" || filepath.Base(s.Jail) != s.Jail || !filepath.IsAbs(s.Workspace) || s.WrittenAt.IsZero() {
		return errors.New("mcp-auth: invalid launch share; retry the launch")
	}
	if s.Home != "" {
		if rel, ok := paths.HomeStateRel(s.Workspace, filepath.Join(paths.WorkspaceStateDir(s.Workspace), s.Home)); !ok || rel != s.Home {
			return errors.New("mcp-auth: the launch share names no home of its workspace; retry the launch")
		}
	}
	for _, r := range s.Replicas {
		if r.Pack == "" || filepath.Base(r.Pack) != r.Pack || r.Pack == "." || r.Pack == ".." || !filepath.IsAbs(r.DeriveFile) || filepath.Base(r.DeriveFile) != "derive.lua" || len(r.Root) != 2 || r.Root[0] != "home" || r.Decl.Format != "json" || r.Lock.Protocol != "flock" || r.Store == "" || r.Decl.Lock == nil || *r.Decl.Lock != r.Lock {
			return errors.New("mcp-auth: unsupported replica declaration; retry with a supported pack")
		}
		for _, part := range append(append([]string(nil), r.Root...), r.Store, r.Lock.Path) {
			if part == "" || filepath.IsAbs(part) || filepath.Clean(part) != part || strings.HasPrefix(part, "../") || part == ".." {
				return fmt.Errorf("mcp-auth: unsafe replica component; retry with a valid pack declaration")
			}
		}
		if filepath.Base(r.Root[1]) != r.Root[1] {
			return errors.New("mcp-auth: unsafe replica root; retry with a valid pack declaration")
		}
		for _, entry := range r.Shared {
			if entry.Name == "" || entry.URL == "" || entry.Key == "" || !validDigest(entry.Identity) {
				return errors.New("mcp-auth: invalid shared server; retry the launch")
			}
		}
	}
	if s.SignIn != nil && s.SignIn.Version != mcpsignin.LaunchVersion {
		return errors.New("mcp-auth: unsupported managed sign-in snapshot; relaunch with a current yolo build")
	}
	for _, server := range s.Once {
		if server.Transport != "" && server.Transport != "http" && server.Transport != "sse" || strings.ContainsAny(server.ExpectedClientID, "\x00\r\n") {
			return errors.New("mcp-auth: invalid once identity facts; correct the user MCP configuration and relaunch")
		}
		if server.Name == "" || server.URL == "" || !validDigest(server.Identity) {
			return errors.New("mcp-auth: invalid one-sign-in server; retry the launch")
		}
	}
	for _, source := range s.Sources {
		if err := source.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (r Replica) Servers() []SharedServer {
	servers := make([]SharedServer, 0, len(r.Shared))
	for _, entry := range r.Shared {
		servers = append(servers, SharedServer{Server: entry.Name, Identity: entry.Identity, URL: entry.URL, Key: entry.Key,
			AccessTokenPath: r.Decl.Fields.AccessToken, RefreshTokenPath: r.Decl.Fields.RefreshToken,
			ExpiresAtPath: r.Decl.Fields.ExpiresAt.Path, ExpiresAtUnit: r.Decl.Fields.ExpiresAt.Unit, ClientIDPath: r.Decl.Fields.ClientID, ClientID: entry.ClientID})
	}
	return servers
}

func DecodeShare(data []byte) (Share, error) {
	var s Share
	if err := json.Unmarshal(data, &s); err != nil {
		return Share{}, fmt.Errorf("mcp-auth: parse private launch share: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Share{}, err
	}
	return s, nil
}
