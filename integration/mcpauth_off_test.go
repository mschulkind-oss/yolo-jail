package integration

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestMCPAuthUserOffNeverStartsProducerOrDaemon(t *testing.T) {
	requireJail(t)
	if detectRuntime() != "podman" || goruntime.GOOS != "linux" {
		t.Skip("Linux podman MCP broker slice")
	}
	// The test's yolo state is its own; only the harness's store links are shared.
	home := mcpAuthHome(t, `packs = ["codex"]
[allow.mcp-auth]
off = true
[mcp_servers.linear]
url = "https://mcp.example.test/mcp"
`)
	workspace := writeProject(t, `{"requests":{"mcp-auth":{"off":false}}}`)
	state := loopholes.StateDirFor("mcp-auth-broker")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	// If the off switch were ignored, even a doctor/read would fail on this malformed
	// synthetic canonical state. The disabled pack may not touch it or seed a replica.
	canonical := filepath.Join(state, "codex.json")
	if err := os.WriteFile(canonical, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `test ! -e ~/.codex/.credentials.json && test ! -e /run/yolo-services/mcp-auth-broker.endpoint && test -z "${YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT:-}" && echo OFF_CLEAN=yes`
	result := runYolo(t, workspace, script, withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
	if result.rc != 0 || kvLine(result.stdout, "OFF_CLEAN") != "yes" {
		t.Fatalf("owner-off jail should boot without MCP broker: rc=%d stdout=%s stderr=%s", result.rc, result.stdout, result.stderr)
	}
	body, err := os.ReadFile(canonical)
	if err != nil || string(body) != "{" {
		t.Fatal("disabled broker read or rewrote synthetic canonical state")
	}
	if _, err := os.Stat(filepath.Join(state, "replicas")); !os.IsNotExist(err) {
		t.Fatalf("disabled broker produced a private share: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.GlobalStorageUnder(home), "logs", "host-service-mcp-auth-broker.log")); !os.IsNotExist(err) {
		t.Fatalf("disabled broker started a host daemon: %v", err)
	}
	if !strings.Contains(result.stderr, "mcp-auth-broker") || !strings.Contains(result.stderr, "off") {
		t.Fatal("selected off-switch was not disclosed")
	}
}
