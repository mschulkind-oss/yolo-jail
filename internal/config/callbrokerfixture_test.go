package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func selectedCallBrokerFixturePack(t *testing.T, root, credentialPath string, tables []string) *packload.Pack {
	t.Helper()
	packDir := filepath.Join(root, "local-tasks")
	moduleDir := filepath.Join(packDir, "loopholes", "tasks")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	packManifest := "name = \"local-tasks\"\n\n[[loophole]]\nfrom = \"loopholes/tasks\"\n"
	if err := os.WriteFile(filepath.Join(packDir, "pack.toml"), []byte(packManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	credential := ""
	if credentialPath != "" {
		credential = "credential_paths = [" + strconv.Quote(credentialPath) + "]\n"
	}
	cellTables := make([]string, len(tables))
	for i, table := range tables {
		cellTables[i] = strconv.Quote(table)
	}
	manifest := "name = \"tasks\"\ndescription = \"fixture call broker\"\nversion = 1\ntransport = \"loopback-tls\"\n\n" +
		"[call_broker]\nsource = \"local-tasks\"\nfront = \"cli\"\nprogram = \"tasks\"\n" +
		"classifier = [\"yolo\", \"internal\", \"call-broker-classify\", \"tasks\"]\n" + credential +
		"\n[call_broker.scope]\nroots = \"declared\"\n\n[[call_broker.resource]]\ntype = \"project\"\n\n" +
		"[[call_broker.cell]]\nname = \"rerun_workflows\"\ntables = [" + strings.Join(cellTables, ", ") + "]\n" +
		"[call_broker.cell.default]\nworking_set = true\n"
	if err := os.WriteFile(filepath.Join(moduleDir, "loophole.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(packDir, "local-tasks")
	if pack == nil || len(problems) != 0 {
		t.Fatalf("load selected call-broker fixture pack: pack=%v problems=%v", pack, problems)
	}
	return pack
}
