package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestMCPAuthFinalHarvestLockFailureFailsSelectedLaunch(t *testing.T) {
	requireJail(t)
	if detectRuntime() != "podman" || goruntime.GOOS != "linux" {
		t.Skip("Linux podman only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("packs = [\"codex\"]\n[mcp_servers.linear]\nurl = \"https://mcp.example.test/mcp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := writeProject(t, `{}`)
	state := loopholes.StateDirFor("mcp-auth-broker")
	canonical, err := mcpauth.NewCanonicalStore(state)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://mcp.example.test/mcp"
	identitySum := sha256.Sum256([]byte(`{"client_id":"","headers":{},"transport":"http","url":"https://mcp.example.test/mcp"}`))
	entry, _ := json.Marshal(map[string]any{"server_url": url, "access_token": "fake-access", "refresh_token": "fake-refresh", "expires_at": time.Now().Add(24 * time.Hour).UnixMilli(), "client_id": "fake-client"})
	record := mcpauth.Record{Server: "linear", Identity: hex.EncodeToString(identitySum[:]), URL: url, Generation: 1, State: "live", Entry: entry, AccessToken: "fake-access", RefreshToken: "fake-refresh"}
	if err := canonical.WithLock(func(file *mcpauth.CanonicalFile) error { file.Records = []mcpauth.Record{record}; return nil }); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "lock-held")
	finished := make(chan error, 1)
	go func() {
		lockPath := filepath.Join(paths.WorkspaceStateDir(workspace), "home", "codex", "mcp-oauth-locks", "file-store.lock")
		storePath := filepath.Join(paths.WorkspaceStateDir(workspace), "home", "codex", ".credentials.json")
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(storePath); err == nil {
				f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
				if err == nil {
					if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
						f.Close()
						finished <- err
						return
					}
					if err := os.WriteFile(marker, []byte("ready"), 0o600); err != nil {
						syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
						f.Close()
						finished <- err
						return
					}
					// Keep the host lock until the daemon itself reports the
					// bounded native-lock failure. A fixed sleep measured from
					// seed can expire before the jail begins its teardown.
					logPath := filepath.Join(paths.GlobalStorageUnder(home), "logs", "host-service-mcp-auth-broker.log")
					observed := false
					for until := time.Now().Add(30 * time.Second); time.Now().Before(until); {
						data, _ := os.ReadFile(logPath)
						if strings.Contains(string(data), "final harvest failed") {
							observed = true
							break
						}
						time.Sleep(20 * time.Millisecond)
					}
					_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
					closeErr := f.Close()
					if !observed {
						finished <- fmt.Errorf("daemon did not report native-lock harvest failure before the test's bound")
						return
					}
					finished <- closeErr
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		finished <- fmt.Errorf("seed store was not published before lock interleaving")
	}()
	script := `python3 - <<'PY'
import os,time
p='/workspace/lock-held'
for _ in range(600):
 if os.path.exists(p): break
 time.sleep(.02)
else: raise RuntimeError('synthetic lock was not held after seed')
PY`
	result := runYolo(t, workspace, script, withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if result.rc == 0 || !strings.Contains(result.stderr, "FINAL_HOST_SERVICE_HARVEST_FAILED") || !strings.Contains(result.stderr, "final harvest was not confirmed") {
		t.Fatalf("launch ignored selected daemon final-harvest failure: rc=%d stderr=%s", result.rc, result.stderr)
	}
	current, err := canonical.Read()
	if err != nil || len(current.Records) != 1 || current.Records[0].Generation != 1 {
		t.Fatalf("failure rewrote canonical: %v %+v", err, current)
	}
	files, err := os.ReadDir(filepath.Join(state, "replicas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			t.Fatal("private input persisted after observed daemon exit")
		}
	}
}
