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

// localBriefingPack writes a pack declaring `from` for its briefing and carrying its prose at
// `file` (which may differ from `from` — that is the whole point), and configures it as the only
// pack. `file` empty ships no prose at all.
func localBriefingPack(t *testing.T, from, file, prose string) *Options {
	t.Helper()
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "bf")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, packDir, `{"contributes":[{"kind":"briefing","from":"`+from+
		`","into":".claude/CLAUDE.md"}]}`)
	if file != "" {
		if err := os.WriteFile(filepath.Join(packDir, file), []byte(prose), 0o644); err != nil {
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

// The CONVENTION still works: `from: "AGENTS.md"` reads AGENTS.md, which every shipped pack
// declares. A fix that only honored a custom `from` would break all six.
func TestJailBriefingDefaultFromStillWorks(t *testing.T) {
	o := localBriefingPack(t, "AGENTS.md", "AGENTS.md", "Conventional prose.\n")
	briefings, warnings := stagedBriefings(t, o)
	if len(briefings) != 1 || !strings.Contains(briefings[0].Text, "Conventional prose.") {
		t.Fatalf("briefings = %+v, want the conventional AGENTS.md\nwarnings:\n%s",
			briefings, warnings)
	}
	if strings.Contains(warnings, "Warning") {
		t.Errorf("unexpected warning:\n%s", warnings)
	}
}

// CLAUDE.md is the other half of the convention, and it is reached by FALLBACK from an omitted
// `from` — both names are in the wild and a pack author should not have to know which one yolo
// happens to read.
func TestJailBriefingFallsBackToClaudeMd(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "bf")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, packDir, `{"contributes":[{"kind":"briefing","into":".claude/CLAUDE.md"}]}`)
	if err := os.WriteFile(filepath.Join(packDir, "CLAUDE.md"),
		[]byte("Claude-md prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"bf"}]`)

	briefings, warnings := stagedBriefings(t, &Options{Workspace: t.TempDir()})
	if len(briefings) != 1 || !strings.Contains(briefings[0].Text, "Claude-md prose.") {
		t.Fatalf("briefings = %+v, want CLAUDE.md via the convention\nwarnings:\n%s",
			briefings, warnings)
	}
}

// A pack with NO manifest still contributes its AGENTS.md — the zero-ceremony case both notches
// depend on, and the one a naive "iterate the contributions" fix would drop.
func TestJailBriefingZeroCeremonyPackStillContributes(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "bare")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "AGENTS.md"),
		[]byte("Bare pack prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"bare"}]`)

	briefings, warnings := stagedBriefings(t, &Options{Workspace: t.TempDir()})
	if len(briefings) != 1 || !strings.Contains(briefings[0].Text, "Bare pack prose.") {
		t.Fatalf("briefings = %+v, want the manifest-less pack's prose\nwarnings:\n%s",
			briefings, warnings)
	}
}

// A declared source yolo could not read still FALLS BACK to the convention — that is
// BriefingCandidates' documented contract and what the host notch has always done — but the
// fallback is no longer SILENT.
//
// The distinction matters more here than for `skills`, which refuses outright: this pack briefs
// successfully with somebody else's content, so without the warning the author's only symptom is
// prose they did not write appearing in their agent's instructions.
func TestJailBriefingWarnsWhenADeclaredFromFallsBack(t *testing.T) {
	// The pack ships AGENTS.md but declares house-rules.md.
	o := localBriefingPack(t, "house-rules.md", "AGENTS.md", "Conventional prose.\n")
	briefings, warnings := stagedBriefings(t, o)
	if len(briefings) != 1 || !strings.Contains(briefings[0].Text, "Conventional prose.") {
		t.Fatalf("briefings = %+v, want the conventional fallback (BriefingCandidates' "+
			"contract)\nwarnings:\n%s", briefings, warnings)
	}
	if !strings.Contains(warnings, "house-rules.md") {
		t.Errorf("the ignored declaration was silent — the author gets prose they did not name, "+
			"with nothing saying why:\n%s", warnings)
	}
}

// A declared source missing with NO conventional file either delivers nothing, and says so with
// the sharper message: this pack briefs NOTHING, which is a different problem than briefing with
// the wrong file.
func TestJailBriefingWarnsWhenADeclaredFromDeliversNothing(t *testing.T) {
	o := localBriefingPack(t, "house-rules.md", "", "")
	briefings, warnings := stagedBriefings(t, o)
	if len(briefings) != 0 {
		t.Errorf("briefings = %+v, want none — nothing in the pack holds prose", briefings)
	}
	if !strings.Contains(warnings, "house-rules.md") || !strings.Contains(warnings, "no prose") {
		t.Errorf("the warning must say the pack briefs nothing at all:\n%s", warnings)
	}
}
