package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
)

// THE JAIL MUST NOT READ YOLO'S OWN HOST COMPOSITION BACK IN. This is the briefing half of
// S3 — the defect packSkillTargets' comment records for skills ("the jail was reading yolo's
// generated output back in as the user's tree").
//
// The loop: `yolo host apply` composes ~/.claude/CLAUDE.md wholesale from every pack's prose
// (entrypoint.RenderHostBriefings, the 2026-08-04 ruling); the claude pack's briefing then
// declares `after: "host:.claude/CLAUDE.md"`, so the jail PREPENDS that composed file to a
// briefing into which it is about to compose the very same packs again. Measured in a real
// jail 2026-08-31: every pack section appeared twice in /home/agent/.claude/CLAUDE.md.
//
// Asserted on the briefing that gets WRITTEN, not on the ownership helper, so it fails if the
// gate is bypassed anywhere between the two.
func TestJailBriefingDoesNotPrependYolosOwnHostComposition(t *testing.T) {
	body := briefingWithHostFile(t, "<!-- from pack: matt-core -->\nHOSTCOMPOSED prefer rg\n", true)
	if strings.Contains(body, "HOSTCOMPOSED") {
		t.Errorf("the jail prepended a host briefing yolo composed itself — every pack's prose "+
			"is delivered twice:\n%s", body)
	}
}

// And the case the mechanism exists for is untouched: a host briefing yolo did NOT write is
// the user's own, and still outranks anything a pack ships. Without this the fix would delete
// the feature instead of correcting it — and a user who never runs `yolo host apply` would
// silently lose their global instructions.
func TestJailBriefingStillPrependsTheUsersOwnHostFile(t *testing.T) {
	body := briefingWithHostFile(t, "MY OWN RULES\n", false)
	if !strings.Contains(body, "MY OWN RULES") {
		t.Errorf("the user's own hand-written host briefing did not reach the jail:\n%s", body)
	}
}

// An UNREADABLE record proves nothing, and here that must fail OPEN (prepend): losing the
// user's instructions is worse than repeating a pack's. Same posture applyHostBriefings takes
// when LoadManifest errors ("treating every existing briefing as yours").
func TestJailBriefingPrependsWhenTheOwnershipRecordIsCorrupt(t *testing.T) {
	body := briefingWithHostFile(t, "MY OWN RULES\n", false, func(home string) {
		p := entrypoint.HostBriefingManifestPath(home)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(body, "MY OWN RULES") {
		t.Errorf("a corrupt ownership record suppressed the user's own briefing:\n%s", body)
	}
}

// briefingWithHostFile composes the claude pack's briefing against a host
// ~/.claude/CLAUDE.md holding hostContent, recording it as yolo's own composition when
// generated is true, and returns the text actually staged for the jail.
func briefingWithHostFile(t *testing.T, hostContent string, generated bool,
	tweak ...func(home string)) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	emptyLoopholeDirs(t)

	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(hostContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if generated {
		man, err := hostskills.LoadManifest(entrypoint.HostBriefingManifestPath(home))
		if err != nil {
			t.Fatal(err)
		}
		man.Record(dest, entrypoint.HostBriefingOwner)
		if err := man.Save(entrypoint.HostBriefingManifestPath(home)); err != nil {
			t.Fatal(err)
		}
	}
	for _, fn := range tweak {
		fn(home)
	}

	o := goldenOptions(ws, home)
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", newConfig(), "podman",
		stagedPacks{packs: claudePackFixture(t)})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
	if err != nil {
		t.Fatalf("no briefing was written for the claude pack: %v", err)
	}
	return string(body)
}
