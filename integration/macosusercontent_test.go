package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// G14 ON A REAL LAUNCH: the staged skills and briefing are write-protected in the sandbox,
// and the agent's own state beside them is not (docs/plans/setup-support-gaps.md, G14).
//
// WHY A LAUNCH AND NOT ONLY THE POLICY SUITE. macosuserseatbelt_test.go loads the generated
// profile against a fixture laid out the way a launch lays it, which proves the SBPL. What
// only a launch can prove is the WIRING that fixture assumes: that the run pipeline hands the
// overlay's destinations to the plan, that the plan resolves them through the home-tier
// layout the bootstrap really lays, and that the profile the agent runs under is that one.
// Every one of those is unit-pinned on Linux; none of them has met the kernel together.
//
// The probe runs as the sandbox user, under the session profile, through ~/.claude — the
// path an agent uses — never through the sidecar spelling, because the whole trap is that
// the two differ.
func TestMacosUserStagedContentIsWriteProtected(t *testing.T) {
	requireMacosUser(t)
	// The claude pack is what delivers content (`.claude/skills`, `.claude/CLAUDE.md`) and
	// what makes ~/.claude a layout link — with no pack there is nothing to protect and
	// every assertion below would pass vacuously.
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)

	r := runMacosUser(t, ws, macosUserContentProbe())
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END CONTENT ===") {
		t.Fatalf("the macos-user launch did not run its content probe (rc %d), so nothing "+
			"below can be read as a statement about G14.\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "CONTENT")
	if got["skill"] == "" || got["skill"] == "NONE" {
		t.Fatalf("no staged skill under ~/.claude/skills, so the refusals below would be "+
			"refusals to write a file that is not there:\n%s", r.stdout)
	}
	for key, want := range macosUserContentWant() {
		if got[key] != want {
			t.Errorf("%s|%s, want %s (G14). %s\nfull output:\n%s",
				key, got[key], want, macosUserContentWhy[key], r.stdout)
		}
	}
}

// macosUserContentProbe is the sandboxed script: one `key|RESULT` line per operation, each
// operation in a subshell so a refusal is a result rather than an exit, and each allowed
// mutation undone at once so a PARTIAL enforcement cannot leave the next launch a broken home.
func macosUserContentProbe() string {
	return strings.Join([]string{
		`echo "=== CONTENT ==="`,
		`skill=$(ls -d "$HOME"/.claude/skills/*/SKILL.md 2>/dev/null | head -n 1)`,
		`echo "skill|${skill:-NONE}"`,
		`if ( printf '' >> "$skill" ) 2>/dev/null; then echo "skill-write|ALLOWED"; else echo "skill-write|DENIED"; fi`,
		`if ( printf '' >> "$HOME/.claude/CLAUDE.md" ) 2>/dev/null; then echo "briefing-write|ALLOWED"; else echo "briefing-write|DENIED"; fi`,
		`if ( touch "$HOME/.claude/skills/yolo-it-planted" ) 2>/dev/null; then rm -f "$HOME/.claude/skills/yolo-it-planted"; echo "plant|ALLOWED"; else echo "plant|DENIED"; fi`,
		`if ( mv "$HOME/.claude/skills" "$HOME/.claude/skills.yolo-it" ) 2>/dev/null; then mv "$HOME/.claude/skills.yolo-it" "$HOME/.claude/skills"; echo "rename|ALLOWED"; else echo "rename|DENIED"; fi`,
		`if ( mv "$HOME/.claude" "$HOME/.claude.yolo-it" ) 2>/dev/null; then mv "$HOME/.claude.yolo-it" "$HOME/.claude"; echo "link-move|ALLOWED"; else echo "link-move|DENIED"; fi`,
		`if ( touch "$HOME/.claude/yolo-it-state-probe" && rm "$HOME/.claude/yolo-it-state-probe" ) 2>/dev/null; then echo "state-write|ALLOWED"; else echo "state-write|DENIED"; fi`,
		`echo "=== END CONTENT ==="`,
	}, "\n")
}

// macosUserContentWant is what the sandbox must answer. Spelled out rather than derived, for
// macosUserHomeTierWantLinks' reason: a table derived from the code under test is satisfied
// by whatever that code says.
func macosUserContentWant() map[string]string {
	return map[string]string{
		"skill-write":    "DENIED",
		"briefing-write": "DENIED",
		"plant":          "DENIED",
		"rename":         "DENIED",
		"link-move":      "DENIED",
		"state-write":    "ALLOWED",
	}
}

var macosUserContentWhy = map[string]string{
	"skill-write": "A staged skill must not be editable: the copy is the agent's own file, so " +
		"only the profile's home-content-write-deny can refuse it, and only if it names the " +
		"sidecar path ~/.claude resolves to.",
	"briefing-write": "The briefing is protected by the same rule as the skills.",
	"plant":          "A new file under the skills dir is a skill the next session would load.",
	"rename":         "Renaming the skills dir away would free its path for one the agent wrote.",
	"link-move": "~/.claude is the layout link the whole resolution rests on; if it can be " +
		"moved aside, ~/.claude can be replaced by a directory the agent wrote " +
		"(home-content-anchor-deny).",
	"state-write": "The agent's own state beside the content must stay writable, or the deny " +
		"froze the agent rather than its instructions.",
}

// TestMacosUserContentProbeReadsARealLayout is the Linux preflight of the probe above: the
// REAL layout deriver on a real filesystem, the staged content where the overlay puts it, the
// probe run UNSANDBOXED — so every operation is allowed and every allowed mutation must have
// been undone. It proves the script and its parser, never the profile. Not behind
// requireMacosUser, for the home-tier preflight's reason: counting it would let the macOS job
// pass its "something ran" check with no sandbox started.
func TestMacosUserContentProbeReadsARealLayout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := entrypoint.DeriveDarwinHomeLayout(home, sidecar, []string{".claude"}, nil).Apply(); err != nil {
		t.Fatalf("applying the real layout: %v", err)
	}
	skill := filepath.Join(home, ".claude", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		skill: "demo skill", filepath.Join(home, ".claude", "CLAUDE.md"): "the briefing",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", "-c", macosUserContentProbe())
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe did not run cleanly: %v\n%s", err, out)
	}
	got := macosUserHomeProbeFields(t, string(out), "CONTENT")
	if got["skill"] != skill {
		t.Errorf("skill|%s, want %s", got["skill"], skill)
	}
	for key := range macosUserContentWant() {
		if got[key] != "ALLOWED" {
			t.Errorf("%s|%s unsandboxed, want ALLOWED — the probe cannot report a refusal "+
				"it would also report with no profile at all.\n%s", key, got[key], out)
		}
	}
	// Every mutation undone: the link, the skills dir and the briefing are where they were.
	if target, err := os.Readlink(filepath.Join(home, ".claude")); err != nil ||
		target != filepath.Join(sidecar, "claude") {
		t.Errorf("~/.claude is no longer the layout link (%q, %v) — the probe's link-move "+
			"did not restore it", target, err)
	}
	for _, p := range []string{skill, filepath.Join(home, ".claude", "CLAUDE.md")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is gone after the probe: %v", p, err)
		}
	}
	for _, leftover := range []string{"skills.yolo-it", "skills/yolo-it-planted", "yolo-it-state-probe"} {
		if _, err := os.Lstat(filepath.Join(home, ".claude", leftover)); err == nil {
			t.Errorf("the probe left ~/.claude/%s behind", leftover)
		}
	}
}

// THE INHERITED USER SCOPE ON A REAL LAUNCH (OQ-LP9): the generated ~/.config/yolo-jail/config.jsonc
// a container mounts as a single `:ro` file arrives in the sandbox through the home overlay, carries
// the user's packs, is what an in-sandbox `yolo pack ls` reads as its user scope, and is
// write-protected — while the directory around it stays the agent's own (R8), so a --user-layer
// file can be written beside it. Unit-pinned on Linux (run.TestMacosUserOverlayCarriesTheInheritedUserScope,
// entrypoint.TestDarwinOverlayInstallsTheInheritedUserScopeBesideTheAgentsOwnFiles); only a Mac
// shows the kernel refusing the write.
func TestMacosUserInheritedUserScopeArrivesReadOnly(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	r := runMacosUser(t, ws, strings.Join([]string{
		`f="$HOME/.config/yolo-jail/config.jsonc"`,
		`echo "=== SCOPE ==="`,
		`if grep -q '"claude"' "$f" 2>/dev/null; then echo "file|PACKS"; else echo "file|MISSING"; fi`,
		`if yolo pack ls 2>&1 | grep -q claude; then echo "pack-ls|CLAUDE"; else echo "pack-ls|NONE"; fi`,
		`if ( printf '' >> "$f" ) 2>/dev/null; then echo "append|ALLOWED"; else echo "append|DENIED"; fi`,
		`if ( mv "$f" "$f.yolo-it" ) 2>/dev/null; then mv "$f.yolo-it" "$f"; echo "rename|ALLOWED"; else echo "rename|DENIED"; fi`,
		`if ( printf '{}\n' > "$HOME/.config/yolo-jail/layer.jsonc" && rm "$HOME/.config/yolo-jail/layer.jsonc" ) 2>/dev/null; then echo "layer|ALLOWED"; else echo "layer|DENIED"; fi`,
		`echo "=== END SCOPE ==="`,
	}, "\n"))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END SCOPE ===") {
		t.Fatalf("the macos-user launch did not run its probe (rc %d).\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "SCOPE")
	for key, want := range map[string]string{
		"file":    "PACKS",
		"pack-ls": "CLAUDE",
		"append":  "DENIED",
		"rename":  "DENIED",
		"layer":   "ALLOWED",
	} {
		if got[key] != want {
			t.Errorf("%s|%s, want %s\nfull output:\n%s", key, got[key], want, r.stdout)
		}
	}
}
