package integration

// sessioninputs_test.go is the nested-jail regression for the session inputs
// (docs/design/live-config-apply.md LA-S4a, LA-D18): a blocked-tools change approved by
// `yolo config apply` reaches the next session of the running jail, through the per-entry
// channel, and the container's frozen environment carries none of the inputs.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestASessionInputChangeReachesTheNextSessionOfARunningJail(t *testing.T) {
	requireJail(t)

	workspace := writeProject(t, `{"security":{"blocked_tools":[{"name":"yolosessiona","message":"SESSION-BLOCK-A"}]}}`)
	cname := naming.FromWorkspace(workspace)
	const release = "release-session-inputs"
	holder := startYoloBackground(t, "session-inputs-holder", workspace,
		`yolosessiona; printf '\nHOLDER-READY\n'; `+
			`for _ in $(seq 1 1200); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(workspace, release), []byte("release\n"), 0o644) })
	awaitOutput(t, holder, regexp.MustCompile("HOLDER-READY"))
	awaitLaunchLockReleased(t, workspace, holder)
	if !strings.Contains(holder.combined(), "SESSION-BLOCK-A") {
		t.Fatalf("the first session did not run the launch's blocked-tool shim:\n%s", holder.combined())
	}

	// No session input is frozen into the container (LA-D18).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	inspect, err := exec.CommandContext(ctx, detectRuntime(), "inspect", "--format",
		"{{range .Config.Env}}{{println .}}{{end}}", cname).Output()
	if err != nil {
		t.Fatalf("inspect the running jail's env: %v", err)
	}
	for _, name := range []string{"YOLO_BLOCK_CONFIG", "YOLO_MISE_TOOLS", "MISE_DISABLE_TOOLS", "YOLO_LSP_SERVERS", "YOLO_MCP_SERVERS"} {
		if strings.Contains(string(inspect), "\n"+name+"=") || strings.HasPrefix(string(inspect), name+"=") {
			t.Errorf("the container's frozen env carries the session input %s:\n%s", name, inspect)
		}
	}

	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"),
		[]byte(`{"security":{"blocked_tools":[{"name":"yolosessionb","message":"SESSION-BLOCK-B"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	applied := runYoloCLI(t, workspace, "config", "apply", "--accept-config-changes")
	if applied.rc != 0 || !strings.Contains(applied.combined(), "from your next yolo session") {
		t.Fatalf("`yolo config apply` did not report the blocked-tools change for the next session: rc=%d\n%s",
			applied.rc, applied.combined())
	}

	attached := runCommand(t, workspace, []string{"run", "--", "bash", "-lc",
		`yolosessionb; printf '\nB-DONE\n'; if [ -e "$HOME/.yolo/bin/block/yolosessiona" ]; then echo A-STILL-BLOCKED; fi`})
	if attached.rc != 0 || !strings.Contains(attached.stderr, "Attaching to existing jail") {
		t.Fatalf("the attach after the apply did not enter the running jail: rc=%d\n%s", attached.rc, attached.combined())
	}
	if !strings.Contains(attached.combined(), "SESSION-BLOCK-B") {
		t.Errorf("the next session did not render the applied blocked tool:\n%s", attached.combined())
	}
	if strings.Contains(attached.stdout, "A-STILL-BLOCKED") {
		t.Errorf("the next session kept the removed blocked tool's shim:\n%s", attached.combined())
	}
}
