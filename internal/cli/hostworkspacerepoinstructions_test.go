package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// A workspace-layer repo_instructions never reaches the host path (IF-D13), and the launch says so
// (docs/reference/workspace-instruction-files.md): it names what is ignored and the next step.

func workspaceWithRepoInstructions(t *testing.T, body string) string {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, config.WorkspaceConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestHostLaunchNamesAWorkspaceRepoInstructionsItIgnores(t *testing.T) {
	ws := workspaceWithRepoInstructions(t, `{"repo_instructions":{"mode":"ignore"}}`)
	launch := &hostComposition{cfg: hostRepoInstrConfig("keep"), packs: shippedHostPacks(t, "claude"), workspace: ws}
	var errw strings.Builder
	applyHostRepoInstructions(launch, "claude", &errw)
	got := errw.String()
	for _, want := range []string{
		"yolo host: repo instructions: this workspace sets repo_instructions (mode ignore)",
		"the host does not read it",
		"your user scope (mode keep)",
		"yolo config set repo_instructions.mode ignore --user",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestHostLaunchSaysNothingAboutAWorkspaceValueThatMatchesTheUserScope(t *testing.T) {
	ws := workspaceWithRepoInstructions(t, `{"repo_instructions":{"mode":"keep"}}`)
	launch := &hostComposition{cfg: hostRepoInstrConfig("keep"), packs: shippedHostPacks(t, "claude"), workspace: ws}
	var errw strings.Builder
	applyHostRepoInstructions(launch, "claude", &errw)
	if errw.Len() != 0 {
		t.Fatalf("said %q", errw.String())
	}
	// And a workspace with no such key is silent too, as is a program that is no agent.
	var other strings.Builder
	applyHostRepoInstructions(&hostComposition{cfg: hostRepoInstrConfig("keep"), packs: shippedHostPacks(t, "claude"), workspace: t.TempDir()}, "claude", &other)
	ws2 := workspaceWithRepoInstructions(t, `{"repo_instructions":{"mode":"ignore"}}`)
	applyHostRepoInstructions(&hostComposition{cfg: hostRepoInstrConfig("keep"), packs: shippedHostPacks(t, "claude"), workspace: ws2}, "bash", &other)
	if other.Len() != 0 {
		t.Fatalf("said %q", other.String())
	}
}

func TestHostLaunchNamesTheReplaceWithOfAWorkspaceItIgnores(t *testing.T) {
	ws := workspaceWithRepoInstructions(t, `{"repo_instructions":{"mode":"replace","replace_with":"rules.md"}}`)
	launch := &hostComposition{cfg: hostRepoInstrConfig("keep"), packs: shippedHostPacks(t, "claude"), workspace: ws}
	var errw strings.Builder
	applyHostRepoInstructions(launch, "claude", &errw)
	if !strings.Contains(errw.String(), "yolo config set repo_instructions.replace_with rules.md --user") {
		t.Fatalf("no replace_with step in %q", errw.String())
	}
}

// THE CALL SITE for `yolo host apply`: it runs from a workspace and writes the shared real home,
// so it says the workspace's value was not read. Delete the call in applyHostBriefings and this
// fails.
func TestApplyHostNamesAWorkspaceRepoInstructionsItIgnores(t *testing.T) {
	home := t.TempDir()
	selectPacksWith(t, home, `"claude"`, ``)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ws := workspaceWithRepoInstructions(t, `{"repo_instructions":{"mode":"ignore"}}`)
	t.Chdir(ws)
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	if !strings.Contains(report, "this workspace sets repo_instructions (mode ignore)") ||
		!strings.Contains(report, "yolo config set repo_instructions.mode ignore --user") {
		t.Errorf("apply did not name the ignored workspace value:\n%s", report)
	}
}
