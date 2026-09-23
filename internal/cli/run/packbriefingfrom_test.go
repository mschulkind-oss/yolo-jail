package run

// packbriefingfrom_test.go is the JAIL-PATH gate for the `briefing` kind's `from` field — the
// sibling of packskillsfrom_test.go, for the sibling defect.
//
// The bug (roadmap.md §6a-4, verified 2026-08-04): readPackBriefing took a DIRECTORY and
// scanned AGENTS.md/CLAUDE.md unconditionally, so a pack declaring
// `{"kind":"briefing","from":"house-rules.md","into":"…"}` had it honored at the HOST notch
// (hostBriefingProse built [from, AGENTS.md, CLAUDE.md]) and silently ignored in a jail. Both
// readers now go through packload, which is the same convergence `skills` needed.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// localBriefingPack writes a pack declaring `from` for its briefing (empty omits it) and carrying
// its prose at the pack-relative `file` (which may differ from `from` — that is the whole point),
// and configures it as the only pack. `file` empty ships no prose at all.
func localBriefingPack(t *testing.T, from, file, prose string) *Options {
	t.Helper()
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "bf")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fromField := ""
	if from != "" {
		fromField = `"from":"` + from + `",`
	}
	writePack(t, packDir, `{"contributes":[{"kind":"briefing",`+fromField+`"into":".claude/CLAUDE.md"}]}`)
	if file != "" {
		full := filepath.Join(packDir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(prose), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"bf"}]`)
	return &Options{Workspace: t.TempDir()}
}

// stagedBriefings runs stagePacks and returns the pack prose it collected, plus the warnings.
func stagedBriefings(t *testing.T, o *Options) ([]jailcontent.PackBriefing, string) {
	t.Helper()
	var out bytes.Buffer
	o.Stdout = &out
	jailcontent.SetPackSkillDirs(nil)
	_, _, briefings, err := o.stagePacks("yolo-test-briefingfrom")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	return briefings, out.String()
}

// THE §6a-4 ASSERTION. A custom `from` is read in a jail, which it was not before.
func TestJailBriefingHonorsCustomFrom(t *testing.T) {
	o := localBriefingPack(t, "house-rules.md", "house-rules.md", "House rules prose.\n")
	briefings, warnings := stagedBriefings(t, o)
	if len(briefings) != 1 {
		t.Fatalf("briefings = %+v, want exactly one\nwarnings:\n%s", briefings, warnings)
	}
	if !strings.Contains(briefings[0].Text, "House rules prose.") {
		t.Errorf("the jail read the wrong file — a declared `from` must be honored here as it "+
			"already is at the host notch; got %q", briefings[0].Text)
	}
	if strings.Contains(warnings, "Warning") {
		t.Errorf("unexpected warning for a source that IS there:\n%s", warnings)
	}
}

// The CONVENTION is briefing/ (pack-briefing-defaults.md §3.1): an omitted `from` reads every
// briefing/*.md, and a `from` naming one of them reads that one. A fix that only honored a custom
// `from` would break every manifest-less pack.
func TestJailBriefingDefaultFromStillWorks(t *testing.T) {
	for _, from := range []string{"", "briefing/prose.md"} {
		o := localBriefingPack(t, from, "briefing/prose.md", "Conventional prose.\n")
		briefings, warnings := stagedBriefings(t, o)
		if len(briefings) != 1 || !strings.Contains(briefings[0].Text, "Conventional prose.") {
			t.Fatalf("from=%q: briefings = %+v, want briefing/prose.md\nwarnings:\n%s",
				from, briefings, warnings)
		}
		if strings.Contains(warnings, "Warning") {
			t.Errorf("from=%q: unexpected warning:\n%s", from, warnings)
		}
	}
}

// A ROOT AGENTS.md / CLAUDE.md IS NEVER READ, at the jail notch as at the host (P1): it is the
// pack REPOSITORY'S own instructions. The pack briefs nothing from it, silently (OQ-PB3 — no
// notice), and naming it in `from` is REFUSED at launch with the edit spelled out, which is the
// shape the maintainer's own local packs have (§4's second exception).
func TestJailBriefingNeverReadsARootInstructionFile(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "bf")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, packDir, `{"contributes":[{"kind":"briefing","into":".claude/CLAUDE.md"}]}`)
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if err := os.WriteFile(filepath.Join(packDir, name),
			[]byte("Repository guide.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"bf"}]`)

	briefings, warnings := stagedBriefings(t, &Options{Workspace: t.TempDir()})
	if len(briefings) != 0 {
		t.Fatalf("briefings = %+v, want none: a root instruction file is never a SOURCE\n"+
			"warnings:\n%s", briefings, warnings)
	}
	if strings.Contains(warnings, "Warning") {
		t.Errorf("a root AGENTS.md that is not shipped is announced nowhere (OQ-PB3):\n%s", warnings)
	}

	for _, from := range []string{"AGENTS.md", "CLAUDE.md"} {
		writePack(t, packDir, `{"contributes":[{"kind":"briefing","from":"`+from+
			`","into":".claude/CLAUDE.md"}]}`)
		o := &Options{Workspace: t.TempDir(), Stdout: &bytes.Buffer{}}
		jailcontent.SetPackSkillDirs(nil)
		_, _, _, err := o.stagePacks("yolo-test-briefingfrom")
		if err == nil || !strings.Contains(err.Error(), from) ||
			!strings.Contains(err.Error(), "git mv "+from+" briefing/") {
			t.Errorf("from=%q: stagePacks err = %v, want a launch refusal spelling the move", from, err)
		}
	}
}

// A pack with NO manifest still contributes its briefing/ prose — the zero-ceremony case both
// notches depend on, and the one a naive "iterate the contributions" fix would drop. Its root
// AGENTS.md does not come along.
func TestJailBriefingZeroCeremonyPackStillContributes(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "bare")
	if err := os.MkdirAll(filepath.Join(packDir, "briefing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "briefing", "bare.md"),
		[]byte("Bare pack prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "AGENTS.md"),
		[]byte("Repository guide.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"bare"}]`)

	briefings, warnings := stagedBriefings(t, &Options{Workspace: t.TempDir()})
	if len(briefings) != 1 || briefings[0].Text != "Bare pack prose." || len(briefings[0].Agents) != 0 {
		t.Fatalf("briefings = %+v, want the manifest-less pack's briefing/ prose, broadcast\n"+
			"warnings:\n%s", briefings, warnings)
	}
}

// A DECLARED SOURCE IS THE ONLY SOURCE (§3.4, P4). A `from` yolo could not read delivers NOTHING
// and is warned about; it no longer falls back to the pack's AGENTS.md, the substitution the
// deleted from-then-convention fallback chain used to make.
func TestJailBriefingDeclaredFromIsTheOnlySource(t *testing.T) {
	// The pack ships a root AGENTS.md but declares house-rules.md.
	o := localBriefingPack(t, "house-rules.md", "AGENTS.md", "Repository guide.\n")
	briefings, warnings := stagedBriefings(t, o)
	if len(briefings) != 0 {
		t.Fatalf("briefings = %+v, want none — no other file may arrive in the declared "+
			"source's place\nwarnings:\n%s", briefings, warnings)
	}
	if !strings.Contains(warnings, "house-rules.md") || !strings.Contains(warnings, "no prose") {
		t.Errorf("the unmet declaration was silent:\n%s", warnings)
	}
}

// A declared source missing BESIDE a briefing/ directory: the missing one is reported and
// delivers nothing, and the briefing/ file still broadcasts — as ITSELF, governed by nobody, not as
// a stand-in for the declared file (P3).
func TestJailBriefingWarnsWhenADeclaredFromDeliversNothing(t *testing.T) {
	o := localBriefingPack(t, "house-rules.md", "briefing/other.md", "Other prose.\n")
	briefings, warnings := stagedBriefings(t, o)
	if len(briefings) != 1 || briefings[0].Text != "Other prose." {
		t.Errorf("briefings = %+v, want only briefing/other.md", briefings)
	}
	if !strings.Contains(warnings, "house-rules.md") || !strings.Contains(warnings, "no prose") {
		t.Errorf("the warning must say the declared source delivered nothing:\n%s", warnings)
	}
}

// A repository instruction file INSIDE briefing/ is a LAUNCH REFUSAL (OQ-PB2), through the LoadDir
// problem stagePacks already treats as fatal — the one site that covers every notch — naming the
// rename.
func TestJailBriefingRefusesAReservedNameInsideTheBriefingDir(t *testing.T) {
	o := localBriefingPack(t, "", "briefing/AGENTS.md", "Dual use.\n")
	o.Stdout = &bytes.Buffer{}
	jailcontent.SetPackSkillDirs(nil)
	_, _, _, err := o.stagePacks("yolo-test-briefingfrom")
	if err == nil || !strings.Contains(err.Error(), "briefing/AGENTS.md") ||
		!strings.Contains(err.Error(), "git mv briefing/AGENTS.md briefing/") {
		t.Errorf("stagePacks err = %v, want a launch refusal naming the file and the rename", err)
	}
}
