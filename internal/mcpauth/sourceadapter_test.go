package mcpauth

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestSourceAdapterPreservesAbsentLegacyLeafAndQualifiedLogicalIdentity(t *testing.T) {
	root := t.TempDir()
	source := Source{Pack: "claude", DeriveFile: filepath.Join(root, "derive.lua"), Dir: root, Store: ".credentials.json", LockDir: ".storage-write.lock", Decl: packdecl.MCPAuth{Format: "json", Lock: &packdecl.MCPAuthLock{Protocol: "proper-lockfile", StaleSeconds: 15}, Fields: packdecl.MCPAuthFields{AccessToken: "accessToken", RefreshToken: "refreshToken"}}}
	legacy := Share{Version: 1, Jail: "private-jail", Workspace: root, WrittenAt: time.Now(), Sources: []Source{source}}
	body, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(body), `"adapter"`) {
		t.Fatal("absent legacy field was expanded", err)
	}
	decoded, err := DecodeShare(body)
	if err != nil || decoded.Sources[0].AdapterName() != "claude" {
		t.Fatal("legacy descriptor behavior changed", err)
	}
	for _, tc := range []struct {
		pack, adapter string
		valid         bool
	}{
		{"left/claude", "claude", true},
		{"right/claude", "claude", true},
		{"left/claude", "", false},
		{"left/claude", "left/claude", false},
		{"../claude", "claude", false},
		{"/claude", "claude", false},
		{"left//claude", "claude", false},
		{"left/claude\nspoof", "claude", false},
	} {
		s := source
		s.Pack, s.Adapter = tc.pack, tc.adapter
		if (s.Validate() == nil) != tc.valid {
			t.Fatalf("wrong logical/adapter boundary for %q/%q", tc.pack, tc.adapter)
		}
		if s.Dir != source.Dir || s.Store != source.Store || s.LockDir != source.LockDir {
			t.Fatal("logical identity became a path component")
		}
	}
	qualified := source
	qualified.Pack, qualified.Adapter = "left/claude", "claude"
	legacy.Sources = []Source{qualified}
	body, _ = json.Marshal(legacy)
	decoded, err = DecodeShare(body)
	if err != nil || decoded.Version != 1 || decoded.Sources[0].Pack != "left/claude" || decoded.Sources[0].AdapterName() != "claude" {
		t.Fatal("qualified source round-trip changed identity/version", err)
	}
	// Replica.Pack remains a path leaf: adding source adapters never relaxes
	// replica canonical filenames or managed-copy filenames.
	legacy.Replicas = []Replica{{Pack: "left/claude"}}
	if legacy.Validate() == nil {
		t.Fatal("source support widened replica filesystem keys")
	}
}
