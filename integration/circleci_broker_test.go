package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// TestCircleCIFakeHostCLIUsesRealClassifier runs the selected pack's real
// in-jail intercept and production classifier against a test-only host CLI.
func TestCircleCIFakeHostCLIUsesRealClassifier(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		t.Skipf("CircleCI CLI forwarding smoke needs podman, runtime is %q", rt)
	}
	bin := t.TempDir()
	callLog := filepath.Join(bin, "circleci-argv.log")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + callLog + "'\nprintf 'token=%s xdg=%s jail=%s\\n' \"$CIRCLE_TOKEN\" \"$XDG_CONFIG_HOME\" \"$JAIL_ONLY_TOKEN\"\n"
	if err := os.WriteFile(filepath.Join(bin, "circleci"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs":["circleci"],"allow":{"circleci":{"always":["read"]}}}`)
	// XDG_CONFIG_HOME is declared host environment, so it must be set to show it is
	// redacted. It names the isolated HOME's .config, where packHome links
	// .config/containers: podman reads its machine connections from
	// $XDG_CONFIG_HOME/containers, and on macOS a bare temp dir there leaves it
	// unable to reach its VM (TestNoJailTestPointsXDGConfigHomeAtABareTempDir).
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"CIRCLE_TOKEN=synthetic-circleci-host-token",
		"CIRCLE_CLI_TOKEN=",
		"DBUS_SESSION_BUS_ADDRESS=",
		"XDG_CONFIG_HOME=" + filepath.Join(os.Getenv("HOME"), ".config"),
		"YOLO_NO_AUTO_IMAGE_REAP=1",
	}
	workspace := writeProject(t, `{}`)
	hostGit(t, workspace, "init", "-q", "-b", "main")
	hostGit(t, workspace, "remote", "add", "origin", "git@github.com:acme/app.git")

	checked := runCommand(t, workspace, []string{"check", "--no-build", config.AcceptConfigChangesFlag}, withEnv(env...))
	if checked.rc != 0 {
		t.Fatalf("yolo check did not approve CircleCI's repository scope (rc=%d):\n%s", checked.rc, checked.combined())
	}
	result := runCommand(t, workspace, []string{"run", config.AcceptConfigChangesFlag, "--", "circleci", "run", "list", "--project", "gh/acme/app", "--json"}, withEnv(env...))
	if result.rc != 0 {
		t.Fatalf("selected CircleCI run list failed (rc=%d):\n%s", result.rc, result.combined())
	}
	if strings.Contains(result.combined(), "synthetic-circleci-host-token") || !strings.Contains(result.combined(), "token=[redacted] xdg=[redacted] jail=") || strings.Contains(result.combined(), "jail-injected-token") {
		t.Fatalf("host-only declared environment or output redaction failed: %s", result.combined())
	}
	got, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("test-only host CircleCI CLI did not execute: %v", err)
	}
	if strings.TrimSpace(string(got)) != "run list --project gh/acme/app --json" {
		t.Fatalf("host CLI received noncanonical or unexpected arguments %q", got)
	}
	// A variable supplied inside the jail must not become a host CLI variable.
	injected := runCommand(t, workspace, []string{"run", config.AcceptConfigChangesFlag, "--", "bash", "-c", "JAIL_ONLY_TOKEN=jail-injected-token circleci run list --project gh/acme/app --json"}, withEnv(env...))
	if injected.rc != 0 || !strings.Contains(injected.combined(), "token=[redacted] xdg=[redacted] jail=") || strings.Contains(injected.combined(), "jail=jail-injected-token") {
		t.Fatalf("jail environment crossed host boundary (rc=%d): %s", injected.rc, injected.combined())
	}
	got, err = os.ReadFile(callLog)
	if err != nil || strings.TrimSpace(string(got)) != "run list --project gh/acme/app --json\nrun list --project gh/acme/app --json" {
		t.Fatalf("host CLI calls after jail injection: %q %v", got, err)
	}
	refused := runCommand(t, workspace, []string{"run", "--accept-config-changes", "--", "circleci", "api", "/me"}, withEnv(env...))
	if refused.rc != 64 {
		t.Fatalf("raw API passthrough was not refused by the actual selected classifier (rc=%d):\n%s", refused.rc, refused.combined())
	}
	afterRefusal, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRefusal) != string(got) {
		t.Fatalf("refused raw API command reached the test-only host CLI: before=%q after=%q", got, afterRefusal)
	}
}
