package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// An attached session must not start when the running selected keeper's
// outcome requirement cannot be recovered. Its log is only presentation.
func TestMCPAuthAttachRefusesMissingKeeperRecordWithoutLog(t *testing.T) {
	requireJail(t)
	if detectRuntime() != "podman" || goruntime.GOOS != "linux" {
		t.Skip("Linux podman selected MCP broker only")
	}
	home := mcpAuthHome(t, "packs = [\"codex\"]\n[mcp_servers.linear]\nurl = \"https://mcp.example.test/mcp\"\n")
	workspace := writeProject(t, `{}`)
	if _, err := mcpauth.NewCanonicalStore(loopholes.StateDirFor("mcp-auth-broker")); err != nil {
		t.Fatal(err)
	}
	first := holdIOPrioJail(t, workspace, "release-mcp-attach", `true`)
	cname := jailName(workspace)
	owners := filepath.Join(paths.GlobalStorageUnder(home), "owners")
	recordPath := filepath.Join(owners, cname+".keeper.json")
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		PID      int    `json:"pid"`
		Birth    string `json:"birth"`
		Required bool   `json:"mcp_teardown_required"`
		ID       string `json:"mcp_teardown_id"`
	}
	if err := json.Unmarshal(data, &selected); err != nil || !selected.Required || len(selected.ID) != 32 || selected.Birth == "" {
		t.Fatalf("running keeper did not select a teardown outcome with a kernel birth: %v", err)
	}
	// A failed next-generation write can leave an older known-off record at
	// exactly the expected path. Workspace, runtime and even PID are insufficient.
	stale, err := json.Marshal(map[string]any{
		"pid": selected.PID, "birth": "older-process-incarnation", "workspace": workspace,
		"runtime": "podman", "mcp_teardown_known": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "attached-executed")
	result := runYolo(t, workspace, `touch /workspace/attached-executed`, withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
	if result.rc == 0 || !strings.Contains(result.stderr, "selected MCP teardown requirement") || strings.Contains(result.stderr, "Attaching to existing jail") {
		t.Fatalf("stale known-off record admitted or announced attach: rc=%d stderr=%s", result.rc, result.stderr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stale known-off record allowed an attached command: %v", err)
	}
	launches, err := os.ReadFile(filepath.Join(paths.GlobalStorageUnder(home), "logs", "launches.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(launches)), "\n")
	if !strings.Contains(lines[len(lines)-1], "outcome=not-started rc=1 ") {
		t.Fatalf("refused attach wrote a successful outcome: %s", lines[len(lines)-1])
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}
	// The keeper still writes its already-open log, but another session cannot
	// read it by name; no log failure marker may determine attach admission.
	log := filepath.Join(paths.GlobalStorageUnder(home), "logs", "jail-keeper-"+cname+".log")
	if err := os.Remove(log); err != nil {
		t.Fatalf("cannot make the keeper log unreadable by name: %v", err)
	}
	result = runYolo(t, workspace, `touch /workspace/attached-executed`, withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
	if result.rc == 0 || !strings.Contains(result.stderr, "selected MCP teardown requirement") || !strings.Contains(result.stderr, "yolo stop") {
		t.Fatalf("selected attach with no keeper record was not refused with recovery advice: rc=%d stderr=%s", result.rc, result.stderr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the attached command ran without a selected outcome ID: %v", err)
	}
	select {
	case err := <-first.done:
		t.Fatalf("first session ended instead of remaining up for its keeper: %v\n%s", err, first.combined())
	default:
	}
}
