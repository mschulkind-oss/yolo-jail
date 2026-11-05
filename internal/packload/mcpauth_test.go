package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestLoadDirRefusesGroupedMCPAuthBinBinding(t *testing.T) {
	root := t.TempDir()
	body := `[[mcp_auth]]
bin = "codex"
store = ".codex/.credentials.json"
format = "json"
empty_store = "delete"
lock = { protocol = "none" }
`
	if err := os.WriteFile(filepath.Join(root, packdecl.ManifestTOMLName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, problems := LoadDir(root, "codex-auth")
	if !strings.Contains(strings.Join(problems, "\n"), `"bin"`) {
		t.Fatalf("LoadDir did not refuse a per-bin binding on pack-wide mcp-auth: problems=%v", problems)
	}
}
