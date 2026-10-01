package jailcontent

// workspaceskills_cap_test.go pins OQ-WS7's ruling (docs/reference/agent-briefings.md): a per-launch
// byte and entry cap on the workspace layer's scratch copy. A skill that would cross either is
// refused and named, and the skills copied before it are delivered. Every test drives the real
// composition, PrepareSkillsWith, the call a launch stages workspace skills through.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withWorkspaceCaps lowers the caps for one test, so a fan-out can cross them without the test
// writing tens of megabytes.
func withWorkspaceCaps(t *testing.T, bytes, entries int64) {
	t.Helper()
	old := workspaceCaps
	workspaceCaps = workspaceSkillCaps{bytes: bytes, entries: entries}
	t.Cleanup(func() { workspaceCaps = old })
}

// stagedBytes is the size of every regular file under dir.
func stagedBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, ierr := d.Info(); ierr == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// fanOut makes skills names under .agents/skills, each a SKILL.md and a link to the one shared
// directory — R10's shape: the links cost nothing to commit, and each copies shared/ again.
func (f wsFixture) fanOut(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		f.file(t, ".agents/skills/"+n+"/SKILL.md", "# "+n)
		f.link(t, ".agents/skills/"+n+"/shared", "../../../shared")
	}
}

// THE BYTE CAP: skills linking one directory copy it once each, until the next would pass the
// cap. That skill is refused and named with the cap and what it would add; the skills before it
// are delivered whole, and a small skill after it that still fits is delivered too.
func TestWorkspaceLayerRefusesTheSkillThatWouldCrossTheByteCap(t *testing.T) {
	f := newWSFixture(t)
	withWorkspaceCaps(t, 10_000, 1_000_000)
	f.file(t, "shared/blob.md", strings.Repeat("b", 3000))
	f.fanOut(t, "a", "b", "c", "d", "e")
	f.file(t, ".agents/skills/zz-small/SKILL.md", "small")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})
	got := skillsOf(t, staging, "codex")
	for _, n := range []string{"a", "b", "c"} {
		data, err := os.ReadFile(filepath.Join(staging, SkillStagingName("codex"), n, "shared", "blob.md"))
		if err != nil || len(data) != 3000 {
			t.Errorf("skill %s, staged before the cap was reached, should arrive whole: %v", n, err)
		}
	}
	refused := refusedPaths(rep)
	why := refused[".agents/skills/d"]
	if !strings.Contains(why, "cap") || !strings.Contains(why, "adds at least") {
		t.Fatalf("the skill crossing the byte cap should be refused by name, with the cap and what "+
			"it adds: %+v", rep.Refused)
	}
	for _, n := range []string{"d", "e"} {
		if got[n] {
			t.Errorf("skill %s crossed the cap and must not be delivered, even in part", n)
		}
		if refused[".agents/skills/"+n] == "" {
			t.Errorf("skill %s crossed the cap and should be named: %+v", n, rep.Refused)
		}
	}
	if !got["zz-small"] {
		t.Error("a skill that still fits under the cap should be delivered after one that did not")
	}
	var n int64
	for _, s := range []string{"a", "b", "c", "d", "e", "zz-small"} {
		n += stagedBytes(t, filepath.Join(staging, SkillStagingName("codex"), s))
	}
	if n > 10_000 {
		t.Errorf("the destination holds %d bytes of workspace skills, past the 10000-byte cap", n)
	}
}

// THE ENTRY CAP: the same fan-out over a directory of many small files crosses the count of files
// and directories before it crosses any byte figure.
func TestWorkspaceLayerRefusesTheSkillThatWouldCrossTheEntryCap(t *testing.T) {
	f := newWSFixture(t)
	withWorkspaceCaps(t, 1<<40, 40)
	for i := range 10 {
		f.file(t, fmt.Sprintf("shared/f%02d.md", i), "x")
	}
	// Each skill is 13 entries: its directory, SKILL.md, shared/ and shared's ten files.
	f.fanOut(t, "a", "b", "c", "d")
	targets(t, map[string][]string{"codex": nil})

	staging, rep := f.stage(t, []string{".agents/skills"})
	got := skillsOf(t, staging, "codex")
	for _, n := range []string{"a", "b", "c"} {
		entries, err := os.ReadDir(filepath.Join(staging, SkillStagingName("codex"), n, "shared"))
		if err != nil || len(entries) != 10 {
			t.Errorf("skill %s, staged before the cap was reached, should arrive whole: %v", n, err)
		}
	}
	why := refusedPaths(rep)[".agents/skills/d"]
	if !strings.Contains(why, "40 files and directories") || !strings.Contains(why, "adds at least") {
		t.Fatalf("the skill crossing the entry cap should be refused by name, with the cap and "+
			"what it adds: %+v", rep.Refused)
	}
	if got["d"] {
		t.Error("the skill that crossed the entry cap must not be delivered, even in part")
	}
}

// THE SHIPPED NUMBERS ARE THE ONES IN FORCE: with no test override, a file one byte past
// maxWorkspaceSkillBytes refuses its skill — sparse, and refused on its size before a byte of it
// is read — and a fan-out past maxWorkspaceSkillEntries refuses the skill that crosses it.
func TestWorkspaceLayerEnforcesTheShippedCaps(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		f := newWSFixture(t)
		f.file(t, ".agents/skills/a/SKILL.md", "a")
		f.file(t, ".agents/skills/big/SKILL.md", "big")
		p := filepath.Join(f.ws, ".agents/skills/big/vendored.bin")
		fh, err := os.Create(p)
		must(t, err)
		must(t, fh.Truncate(maxWorkspaceSkillBytes+1))
		must(t, fh.Close())
		targets(t, map[string][]string{"codex": nil})

		staging, rep := f.stage(t, []string{".agents/skills"})
		got := skillsOf(t, staging, "codex")
		if !got["a"] {
			t.Error("the skill before the oversized one should be delivered")
		}
		if got["big"] {
			t.Error("a skill past the byte cap must not be delivered")
		}
		if why := refusedPaths(rep)[".agents/skills/big"]; !strings.Contains(why, "32 MiB") {
			t.Errorf("the refusal should name the 32 MiB cap: %+v", rep.Refused)
		}
	})
	t.Run("entries", func(t *testing.T) {
		f := newWSFixture(t)
		for i := range 1100 {
			f.file(t, fmt.Sprintf("shared/f%04d.md", i), "x")
		}
		// 1103 entries a skill: three fit under 4096, the fourth does not.
		f.fanOut(t, "a", "b", "c", "d")
		targets(t, map[string][]string{"codex": nil})

		staging, rep := f.stage(t, []string{".agents/skills"})
		got := skillsOf(t, staging, "codex")
		if !got["a"] || !got["b"] || !got["c"] {
			t.Errorf("the three skills under the entry cap should be delivered, got %v", got)
		}
		if got["d"] {
			t.Error("the skill past the entry cap must not be delivered")
		}
		if why := refusedPaths(rep)[".agents/skills/d"]; !strings.Contains(why, "4096 files and directories") {
			t.Errorf("the refusal should name the 4096-entry cap: %+v", rep.Refused)
		}
	})
}

// A file that grows after it was charged for its size is refused as changed, never copied
// past its charge: the read side of the cap.
func TestBoundedReaderRefusesAFileThatGrewPastItsCharge(t *testing.T) {
	var out strings.Builder
	_, err := io.Copy(&out, &boundedReader{r: strings.NewReader("12345"), left: 3})
	if !errors.Is(err, errChanged) {
		t.Fatalf("reading past the charged size should fail as changed, got %v", err)
	}
	out.Reset()
	if _, err := io.Copy(&out, &boundedReader{r: strings.NewReader("123"), left: 3}); err != nil || out.String() != "123" {
		t.Fatalf("a file of exactly its charged size should copy whole: %q, %v", out.String(), err)
	}
}

func TestByteSizeReadsAsTheConstant(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 bytes", 1023: "1023 bytes", 1024: "1 KiB", 1536: "1.5 KiB",
		maxWorkspaceSkillBytes: "32 MiB", 3 << 30: "3 GiB",
	} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", n, got, want)
		}
	}
}

// A NORMAL SKILL SET MEETS NEITHER CAP: prose, scripts, references, and a shared/ directory two
// skills link — the shape option (c) of OQ-WS7 would have refused — all arrive, nothing is refused.
func TestWorkspaceLayerCopiesANormalSkillSetWhole(t *testing.T) {
	f := newWSFixture(t)
	f.file(t, "shared/style.md", strings.Repeat("style guide\n", 2000))
	for _, n := range []string{"design-doc", "review", "release", "lint", "triage"} {
		f.file(t, ".agents/skills/"+n+"/SKILL.md", strings.Repeat(n+" instructions\n", 500))
		f.file(t, ".agents/skills/"+n+"/scripts/run.sh", "#!/bin/sh\necho "+n+"\n")
		f.file(t, ".agents/skills/"+n+"/references/notes.md", strings.Repeat("note\n", 4000))
	}
	f.link(t, ".agents/skills/design-doc/shared", "../../../shared")
	f.link(t, ".agents/skills/review/shared", "../../../shared")
	f.file(t, ".claude/skills/deploy/SKILL.md", "deploy")
	targets(t, map[string][]string{"codex": nil, "pi": {".agents/skills"}})

	staging, rep := f.stage(t, []string{".claude/skills", ".agents/skills"})
	if len(rep.Refused) != 0 {
		t.Fatalf("a normal skill set should copy with nothing refused: %+v", rep.Refused)
	}
	got := skillsOf(t, staging, "codex")
	for _, n := range []string{"design-doc", "review", "release", "lint", "triage", "deploy"} {
		if !got[n] {
			t.Errorf("skill %s should be delivered", n)
		}
	}
	for _, n := range []string{"design-doc", "review"} {
		if _, err := os.Stat(filepath.Join(staging, SkillStagingName("codex"), n, "shared", "style.md")); err != nil {
			t.Errorf("skill %s's linked shared/ should arrive: %v", n, err)
		}
	}
}
