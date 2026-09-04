package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE DISPATCH HALF of content delivery: run.Run must COMPOSE the overlay and hand
// it to the macos-user backend on every launch.
//
// The tree builder had three tests and this had none, so the composition could have
// been deleted from the arm with a green suite — the agent would silently go back to
// starting with no AGENTS.md and no skills, which is the state the feature ended.
// Same family as TestPacksAreStagedBeforeBackendDispatch, and written for the same
// reason: the arm returns above runContainer, so anything the container path does
// implicitly has to be re-pinned here explicitly.
func TestMacosUserLaunchComposesAndPassesTheHomeOverlay(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)

	var gotOverlay string
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, overlay string,
		_ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		gotOverlay = overlay
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}

	if gotOverlay == "" {
		t.Fatalf("the macos-user arm was handed no overlay — the agent would start with "+
			"no AGENTS.md and no skills\nstderr:\n%s", stderr.String())
	}
	// It must be a real composed tree, not just a path: the claude pack declares a
	// skills destination, so the built-in suite has to be in it.
	if _, err := os.Stat(filepath.Join(gotOverlay, ".claude", "skills")); err != nil {
		t.Errorf("the overlay carries no skills tree at .claude/skills: %v", err)
	}
	// Laid out by DESTINATION, never by staging name — the layout IS the manifest the
	// bootstrap copies, so a staging-side name here would land in the home verbatim.
	var leaked []string
	_ = filepath.Walk(gotOverlay, func(p string, _ os.FileInfo, _ error) error {
		if strings.HasPrefix(filepath.Base(p), "briefing-") ||
			strings.HasPrefix(filepath.Base(p), "skills-") {
			leaked = append(leaked, p)
		}
		return nil
	})
	if len(leaked) > 0 {
		t.Errorf("staging-side names reached the overlay: %v", leaked)
	}
}

// --dry-run composes too. The plan's job is to describe the launch, so a dry-run
// that skipped composition would print a plan with no content staging in it — a
// launch nobody runs.
func TestMacosUserDryRunStillComposesTheOverlay(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true

	var gotOverlay string
	var gotDryRun bool
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, overlay string,
		dryRun bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		gotOverlay, gotDryRun = overlay, dryRun
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	if !gotDryRun {
		t.Fatal("dry-run flag did not reach the backend")
	}
	if gotOverlay == "" {
		t.Errorf("--dry-run composed no overlay, so the plan it prints omits the content "+
			"staging a real launch performs\nstderr:\n%s", stderr.String())
	}
}
