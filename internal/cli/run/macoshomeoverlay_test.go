package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// The overlay must lay staged content out at the HOME-RELATIVE destinations, because
// that layout IS the manifest: the bootstrap copies the tree and interprets nothing.
// If this drifts, the sandbox gets files in the wrong place with no error anywhere.
func TestHomeOverlayLaysContentOutByDestination(t *testing.T) {
	staging := t.TempDir()

	// A staged skills dir, as PrepareSkills would leave it.
	skillSrc := filepath.Join(staging, "skills-acme")
	if err := os.MkdirAll(filepath.Join(skillSrc, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillSrc, "demo", "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A staged briefing, as the write loop would leave it.
	if err := os.WriteFile(filepath.Join(staging, briefingStagingName(".claude/CLAUDE.md")),
		[]byte("briefing body"), 0o644); err != nil {
		t.Fatal(err)
	}

	overlay, err := buildMacosHomeOverlayFor(staging,
		[]jailcontent.SkillTarget{{Staging: "skills-acme", Dest: ".claude/skills"}},
		[]briefingDest{{Into: ".claude/CLAUDE.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if overlay == "" {
		t.Fatal("no overlay built despite staged skills and a briefing")
	}

	if got, err := os.ReadFile(filepath.Join(overlay, ".claude", "skills", "demo", "SKILL.md")); err != nil {
		t.Errorf("skills did not land at their destination: %v", err)
	} else if string(got) != "body" {
		t.Errorf("skill content = %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(overlay, ".claude", "CLAUDE.md")); err != nil {
		t.Errorf("briefing did not land at its destination: %v", err)
	} else if string(got) != "briefing body" {
		t.Errorf("briefing content = %q", got)
	}
	// The staging-side names must not survive into the overlay, or the bootstrap would
	// copy `skills-acme` and `briefing-…` into the home as literal paths.
	var leaked []string
	_ = filepath.Walk(overlay, func(p string, _ os.FileInfo, _ error) error {
		if strings.Contains(filepath.Base(p), "skills-acme") ||
			strings.HasPrefix(filepath.Base(p), "briefing-") {
			leaked = append(leaked, p)
		}
		return nil
	})
	if len(leaked) > 0 {
		t.Errorf("staging-side names leaked into the overlay: %v", leaked)
	}
}

// A destination that LEAVES the config must stop being delivered. The overlay is
// rebuilt from scratch every launch precisely so a removed pack's skills disappear
// from the home rather than being served forever.
func TestHomeOverlayIsRebuiltFromScratch(t *testing.T) {
	staging := t.TempDir()
	stale := filepath.Join(staging, "home-overlay", ".claude", "skills", "gone")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	overlay, err := buildMacosHomeOverlayFor(staging, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overlay != "" {
		t.Errorf("overlay = %q, want \"\" — nothing was declared, so nothing should be delivered", overlay)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a previous launch's content survived into a launch that declares none")
	}
}

// Nothing declared → "" rather than an empty dir, so a bare `yolo -- bash` pays for no
// staging step and no bootstrap step at all.
func TestHomeOverlayEmptyWhenNothingDeclared(t *testing.T) {
	overlay, err := buildMacosHomeOverlayFor(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overlay != "" {
		t.Errorf("overlay = %q, want empty", overlay)
	}
}
