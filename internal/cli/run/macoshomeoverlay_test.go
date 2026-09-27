package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

	overlay, _, err := buildMacosHomeOverlayFor(staging,
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
	// And the destinations are LISTED beside the tree, both of them, because the tree alone
	// cannot say where a destination starts (G36).
	if got, err := os.ReadFile(filepath.Join(overlay, entrypoint.HomeOverlayManifestName)); err != nil {
		t.Errorf("the overlay carries no destination list: %v", err)
	} else if !strings.Contains(string(got), `".claude/skills"`) || !strings.Contains(string(got), `".claude/CLAUDE.md"`) {
		t.Errorf("the destination list does not name both destinations:\n%s", got)
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

// THE HOST HALF OF G36. The sandbox install replaces exactly the destinations the overlay
// LISTS, so the builder has to list them — and the only honest test of that is the real
// install reading the real builder's output. The home is laid out the way the macos-user
// layout lays pi's: ~/.pi a symlink into the workspace sidecar, ~/.pi/agent a real
// directory holding pi's sign-in. Drop the builder's list and the install refuses; guess
// the granularity again and the sign-in goes.
func TestHomeOverlayTheSandboxInstallReplacesOnlyTheListedDestinations(t *testing.T) {
	staging := t.TempDir()
	writeFile := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(filepath.Join(staging, "skills-pi", "demo", "SKILL.md"), "the skill")
	writeFile(filepath.Join(staging, briefingStagingName(".pi/agent/AGENTS.md")), "the briefing")
	overlay, _, err := buildMacosHomeOverlayFor(staging,
		[]jailcontent.SkillTarget{{Staging: "skills-pi", Dest: ".pi/agent/skills"}},
		[]briefingDest{{Into: ".pi/agent/AGENTS.md"}})
	if err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	home := filepath.Join(base, "home")
	sidecar := filepath.Join(base, "workspace", ".yolo", "home")
	writeFile(filepath.Join(sidecar, "pi", "agent", "auth.json"), "pi's sign-in")
	writeFile(filepath.Join(sidecar, "pi", "agent", "skills", "gone", "SKILL.md"), "stale")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sidecar, "pi"), filepath.Join(home, ".pi")); err != nil {
		t.Fatal(err)
	}
	e := entrypoint.DarwinEnvFrom(map[string]string{
		"HOME": home, "JAIL_HOME": home,
		entrypoint.DarwinHomeSidecarEnv: sidecar,
		"YOLO_DARWIN_HOME_OVERLAY":      overlay,
	}, home)
	e.Stderr = &strings.Builder{}
	// The pi pack is selected, so ~/.pi is this launch's own layout link and the install may
	// pass through it (G14 refuses any other).
	if err := entrypoint.InstallHomeOverlay(e, []*packload.Pack{officialPack(t, "pi")}); err != nil {
		t.Fatalf("the sandbox install refused the builder's overlay: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(home, ".pi", "agent", "auth.json"):                  "pi's sign-in",
		filepath.Join(home, ".pi", "agent", "skills", "demo", "SKILL.md"): "the skill",
		filepath.Join(home, ".pi", "agent", "AGENTS.md"):                  "the briefing",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q (err %v), want %q", path, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "skills", "gone")); !os.IsNotExist(err) {
		t.Errorf("a skill the host no longer stages survived (err %v)", err)
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

	overlay, _, err := buildMacosHomeOverlayFor(staging, nil, nil)
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
	overlay, _, err := buildMacosHomeOverlayFor(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if overlay != "" {
		t.Errorf("overlay = %q, want empty", overlay)
	}
}
