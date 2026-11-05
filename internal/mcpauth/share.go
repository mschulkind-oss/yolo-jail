package mcpauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Share is an immutable, host-produced instruction for one launch's selected replicas.
// It carries keys and declarations, never OAuth credentials or agent-written paths.
type Share struct {
	Version   int       `json:"version"`
	Jail      string    `json:"jail"`
	Workspace string    `json:"workspace"`
	WrittenAt time.Time `json:"written_at"`
	Replicas  []Replica `json:"replicas"`
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
