package jailcontent

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoopholeFirst(t *testing.T) {
	cases := map[string]string{
		"audio. Second sentence here":    "audio",
		"single line no period":          "single line no period",
		"first line\nsecond line":        "first line",
		"trailing dots...":               "trailing dots",
		"":                               "",
		"PipeWire pass-through. More.\n": "PipeWire pass-through",
	}
	for in, want := range cases {
		if got := loopholeFirst(in); got != want {
			t.Errorf("loopholeFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeBriefing(t *testing.T) {
	if got := ComposeBriefing("body\n", ""); got != "body\n" {
		t.Errorf("no extra = %q", got)
	}
	if got := ComposeBriefing("body\n", "  extra  \n\n"); got != "body\n\n  extra\n" {
		t.Errorf("with extra = %q", got)
	}
}

func TestWriteBriefingBreaksHardlink(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	must(t, os.WriteFile(a, []byte("shared"), 0o644))
	must(t, os.Link(a, b)) // a and b now share an inode (nlink=2)

	// Writing b must break the link (fresh inode), leaving a untouched.
	must(t, WriteBriefing(b, "new b content"))
	if data, _ := os.ReadFile(a); string(data) != "shared" {
		t.Errorf("a clobbered through hardlink: %q", data)
	}
	if data, _ := os.ReadFile(b); string(data) != "new b content" {
		t.Errorf("b content = %q", data)
	}
	// Single-linked file: in-place write preserves the inode.
	ino1 := inodeOf(t, b)
	must(t, WriteBriefing(b, "again"))
	if inodeOf(t, b) != ino1 {
		t.Error("single-linked write should preserve inode")
	}
}

func TestWorkspaceIsYoloSourceTree(t *testing.T) {
	// A non-yolo dir.
	dir := t.TempDir()
	if WorkspaceIsYoloSourceTree(dir) {
		t.Error("empty dir is not a yolo source tree")
	}
	// The real repo root IS one.
	root := repoRoot(t)
	if !WorkspaceIsYoloSourceTree(root) {
		t.Error("repo root should be recognized as a yolo source tree")
	}
	// go.mod present but foreign module path -> false.
	must(t, os.MkdirAll(filepath.Join(dir, "cmd", "yolo"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "cmd", "yolo", "main.go"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n"), 0o644))
	if WorkspaceIsYoloSourceTree(dir) {
		t.Error("foreign module path should not match")
	}
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	must(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no syscall.Stat_t")
	}
	return st.Ino
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// C3: pack prose is appended with a provenance header naming the pack. The header
// matters because pack prose is INSTRUCTIONS an agent will follow: with several
// packs plus yolo's own briefing in one file, an agent (or a human debugging it) that
// hits a surprising rule needs to know which pack it came from.
func TestComposePackBriefingsAttributesEachPack(t *testing.T) {
	got := ComposePackBriefings("BASE BRIEFING\n", []PackBriefing{
		{Name: "acme", Text: "Always use rg.\n"},
		{Name: "team-rust", Text: "Prefer thiserror.\n"},
	})
	for _, want := range []string{
		"BASE BRIEFING",
		"<!-- from pack: acme -->",
		"Always use rg.",
		"<!-- from pack: team-rust -->",
		"Prefer thiserror.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("briefing missing %q:\n%s", want, got)
		}
	}
	// Config order is preserved: later packs win on conflicting advice, so the
	// order an agent reads them in is load-bearing.
	if strings.Index(got, "acme") > strings.Index(got, "team-rust") {
		t.Errorf("pack order not preserved:\n%s", got)
	}
}

// A pack with no briefing must leave no trace — no empty attributed section.
func TestComposePackBriefingsSkipsEmpty(t *testing.T) {
	got := ComposePackBriefings("BASE\n", []PackBriefing{{Name: "quiet", Text: "  \n"}})
	if strings.Contains(got, "quiet") {
		t.Errorf("an empty pack briefing must emit nothing:\n%s", got)
	}
	if got != "BASE\n" {
		t.Errorf("base briefing altered: %q", got)
	}
}

// Phase 8 (env-manager): the briefing states the confinement notch. jail (and empty)
// is unchanged; guest/host tell the agent it is NOT disposable.
func TestBriefingConfinementHeader(t *testing.T) {
	base := BriefingInput{Workspace: "/home/me/proj"}

	jail := BriefingContent(base) // empty Confinement == jail
	if !contains(jail, "sandboxed container") {
		t.Errorf("default/jail briefing must keep the historical 'sandboxed container' line:\n%s", jail)
	}

	base.Confinement = "jail"
	if BriefingContent(base) != jail {
		t.Error("explicit confinement=jail must be byte-identical to the empty default")
	}

	// The HEADER (the top paragraph, §8.1) states the notch and non-disposability.
	// The deeper "## Environment" body stays jail-shaped until guest/host actually
	// boot (Phases 4/7) — no jail boots at those notches today, so the body is not yet
	// specialized; this test pins the header, which is what Phase 8 delivers.
	base.Confinement = "host"
	host := firstParagraph(BriefingContent(base))
	for _, want := range []string{"host", "real", "NOT disposable"} {
		if !contains(host, want) {
			t.Errorf("host briefing header must warn it is not disposable (missing %q):\n%s", want, host)
		}
	}
	if contains(host, "sandboxed container") {
		t.Errorf("host briefing header must NOT claim it is a sandboxed container:\n%s", host)
	}

	base.Confinement = "guest"
	guest := firstParagraph(BriefingContent(base))
	if !contains(guest, "guest") || contains(guest, "sandboxed container") {
		t.Errorf("guest briefing header wrong:\n%s", guest)
	}
}

// firstParagraph returns the briefing up to the first blank line after the title —
// the confinement header block Phase 8 owns.
func firstParagraph(s string) string {
	// Header runs from the "# " title through the blank line before "## Environment".
	if i := index(s, "## Environment"); i >= 0 {
		return s[:i]
	}
	return s
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && index(s, sub) >= 0) }
func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
