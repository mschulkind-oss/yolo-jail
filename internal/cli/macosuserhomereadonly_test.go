package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// THE FRONT DOOR'S HALF of G14: the run pipeline hands the macos-user seam a HomeOverlay, and
// the seam this package installs (macosUserRun) must pass ALL of it to macosuser.Options —
// the destinations included — or the plan write-protects nothing while the tree is still
// delivered. The type makes dropping the field impossible to miss at the seam's signature, but
// not at the struct literal inside it, where an omitted field compiles to its zero value.
//
// It invokes the REAL closure the front door installs, on its dry-run path, which prints the
// Seatbelt profile the launch would install — so what is asserted is the profile, not a field.
func TestMacosUserFrontDoorWriteProtectsTheDeliveredContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	captureBoth(t, func() {
		if rc := Main([]string{"yolo", "--", "true"}); rc != 0 {
			t.Errorf("Main rc = %d with the pipeline stubbed", rc)
		}
	})
	if seen.MacosUserRun == nil {
		t.Fatal("the front door installed no macos-user seam")
	}

	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureBoth(t, func() {
		_ = seen.MacosUserRun(jsonx.NewOrderedMap(), ws, nil, []string{"true"}, "", "",
			macosuser.HomeOverlay{
				Tree:          filepath.Join(home, "overlay"),
				Dests:         []string{".claude/skills", ".claude/CLAUDE.md"},
				WorkspaceDirs: []string{".claude"},
			}, macosuser.HostContext{}, true /*dryRun*/, jsonx.NewOrderedMap(), nil,
			macosuser.JailDaemons{})
	})

	if !strings.Contains(stdout, "#seatbelt-test-id:home-content-write-deny#") {
		t.Fatalf("the dry-run plan's profile carries no content deny — the seam dropped the "+
			"overlay's destinations on the way to macosuser.Options:\n%s", stdout)
	}
	for _, want := range []string{
		filepath.Join(ws, ".yolo", "home", "claude", "skills"),
		filepath.Join(ws, ".yolo", "home", "claude", "CLAUDE.md"),
	} {
		if !strings.Contains(stdout, `(subpath "`+want+`")`) {
			t.Errorf("the profile does not write-protect %s:\n%s", want, stdout)
		}
	}
}
