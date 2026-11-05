package packload_test

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

func TestCodexPackLoadPreservesMCPAuthDeclaration(t *testing.T) {
	loaded, problems := packload.MaterializeEmbedded(packs.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("strict PackLoad rejected a shipped manifest: %v", problems)
	}
	var codex *packload.Pack
	for _, p := range loaded {
		if p.Name == "codex" {
			codex = p
			break
		}
	}
	if codex == nil {
		t.Fatal("PackLoad did not load the shipped codex pack")
	}

	got := codex.Decl.MCPAuth
	if got == nil {
		t.Fatal("PackLoad did not preserve Codex's mcp_auth declaration")
	}
	if got.Store != ".codex/.credentials.json" || got.Format != "json" || len(got.EntriesAt) != 0 ||
		len(got.Companions) != 0 || got.EmptyStore != "delete" {
		t.Errorf("Codex store metadata = %+v", got)
	}
	if got.Lock == nil || got.Lock.Protocol != "flock" || got.Lock.Path != ".codex/mcp-oauth-locks/file-store.lock" ||
		got.Lock.StaleSeconds != 0 {
		t.Errorf("Codex lock metadata = %+v", got.Lock)
	}
	wantFields := packdecl.MCPAuthFields{
		AccessToken:  "access_token",
		RefreshToken: "refresh_token",
		ExpiresAt:    packdecl.MCPAuthExpiryField{Path: "expires_at", Unit: "ms"},
		ClientID:     "client_id",
	}
	if got.Fields != wantFields {
		t.Errorf("Codex native token-field metadata = %+v, want %+v", got.Fields, wantFields)
	}
	if !got.RereadsBeforeRefresh || got.LoginHint != "codex mcp login {name}" || got.UnsupportedReason != "" {
		t.Errorf("Codex refresh and sign-in metadata = %+v", got)
	}
}
