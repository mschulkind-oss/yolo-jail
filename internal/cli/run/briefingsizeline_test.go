package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
)

// THE LAUNCH'S SIZE LINE (docs/reference/agent-briefings.md BD-D9), driven through the jail's
// production staging and briefing write: a destination whose composed file passes its agent's
// declared warns_above_chars gets one warning line, and the file is written whole. Deleting the
// call in refreshJailBriefingsMode fails the over-limit case.
func launchBriefingWithLocalPack(t *testing.T, packsJSON func(local string) string, manifest, prose string) (stderr, claudeMD string) {
	t.Helper()
	home := packHome(t)
	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "briefing"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, local, manifest)
	if err := os.WriteFile(filepath.Join(local, "briefing", "rules.md"), []byte(prose+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, packsJSON(local))
	pinLauncherInJail(t, false)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
	var errBuf bytes.Buffer
	o.Stdout, o.Stderr = discardBuf(), &errBuf
	_, packs, proses, err := o.stagePacks("yolo-ws-abcd1234")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	cfg, _ := persistenceFixture(t, "")
	o.ensureDurableDir("podman", cfg)
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, "podman",
		stagedPacks{packs: packs, briefings: proses}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(staging, briefingStagingName(".claude/CLAUDE.md")))
	return errBuf.String(), string(raw)
}

func longRules(chars int) string {
	var b strings.Builder
	b.WriteString("## my rules\n\n")
	for b.Len() < chars {
		b.WriteString("- one of the user's own rules, which reaches the agent whole.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func withClaude(local string) string {
	return `["claude", {"name":"mine","source":"file://` + local + `"}]`
}

func TestALaunchWarnsWhenClaudesBriefingPassesItsMeasuredLimit(t *testing.T) {
	prose := longRules(41000)
	stderr, claudeMD := launchBriefingWithLocalPack(t, withClaude, `{"name":"mine"}`, prose)
	for _, want := range []string{
		"Warning: ~/.claude/CLAUDE.md is ",
		" characters, over the 40,000 at which claude warns about a long file.",
		"Its largest parts: pack mine ",
		"Move a section of pack mine's briefing into a skill in that pack",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the launch output lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Count(stderr, "~/.claude/CLAUDE.md is ") != 1 {
		t.Errorf("want exactly one size line, got:\n%s", stderr)
	}
	if !strings.Contains(claudeMD, prose) {
		t.Error("the over-long briefing was trimmed; the size line discloses, it never trims")
	}
}

func TestALaunchUnderTheLimitPrintsNoSizeLine(t *testing.T) {
	stderr, _ := launchBriefingWithLocalPack(t, withClaude, `{"name":"mine"}`, longRules(2000))
	if strings.Contains(stderr, "characters, over the") {
		t.Errorf("a briefing under its limit got a size line:\n%s", stderr)
	}
}

// A destination that declares no limit gets no line, however long its file.
func TestADestinationWithoutADeclaredLimitPrintsNoSizeLine(t *testing.T) {
	only := func(local string) string { return `[{"name":"mine","source":"file://` + local + `"}]` }
	stderr, _ := launchBriefingWithLocalPack(t, only,
		`{"name":"mine","contributes":[{"kind":"briefing","agent":"acme","into":".acme/AGENTS.md"}]}`,
		longRules(90000))
	if strings.Contains(stderr, "characters, over the") {
		t.Errorf("a destination with no warns_above_chars got a size line:\n%s", stderr)
	}
}
