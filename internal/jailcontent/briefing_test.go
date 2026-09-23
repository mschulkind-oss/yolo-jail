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

// Pack prose is appended UNLABELLED by default: the user's packs are their rules for every
// repository, and a `<!-- from pack: NAME -->` label made agents read them as someone else's
// (config.BriefingProvenance). Pinned as exact bytes, because "nothing but a blank line between
// sections" is the whole behaviour — a stray label or separator is the regression.
func TestComposePackBriefingsIsUnlabelledByDefault(t *testing.T) {
	got := ComposePackBriefings("BASE BRIEFING\n", []PackBriefing{
		{Name: "acme", Text: "Always use rg.\n"},
		{Name: "team-rust", Text: "Prefer thiserror.\n"},
	}, "claude", false)
	want := "BASE BRIEFING\n\nAlways use rg.\n\nPrefer thiserror.\n"
	if got != want {
		t.Errorf("default composition:\n got %q\nwant %q", got, want)
	}
}

// `briefing_provenance` turns the per-pack label back on, as a debugging aid. Config order is
// preserved either way: later packs win on conflicting advice, so the order an agent reads them
// in is load-bearing.
func TestComposePackBriefingsLabelsEachPackWhenAsked(t *testing.T) {
	got := ComposePackBriefings("BASE BRIEFING\n", []PackBriefing{
		{Name: "acme", Text: "Always use rg.\n"},
		{Name: "team-rust", Text: "Prefer thiserror.\n"},
	}, "claude", true)
	want := "BASE BRIEFING\n\n<!-- from pack: acme -->\nAlways use rg.\n\n" +
		"<!-- from pack: team-rust -->\nPrefer thiserror.\n"
	if got != want {
		t.Errorf("labelled composition:\n got %q\nwant %q", got, want)
	}
}

// A pack with no briefing must leave no trace — no empty attributed section.
// ONE LABEL PER CONTIGUOUS RUN of one pack's entries, never one per entry: a pack's several
// briefing/ files are one section under the pack's one label (docs/reference/pack-system.md#briefing-directory
// "Joining"), and a file routed to a different agent is simply absent — it does not split the
// section in two. The host composes the same bytes (run's TestJailAndHostComposeTheSameBriefing).
func TestComposePackBriefingsLabelsAPacksFilesOnce(t *testing.T) {
	packs := []PackBriefing{
		{Name: "matt", Text: "House rules."},
		{Name: "matt", Text: "Pi only.", Agents: []string{"pi"}},
		{Name: "matt", Text: "Late rule."},
		{Name: "zc", Text: "Zero."},
	}
	got := ComposePackBriefings("", packs, "claude", true)
	want := "\n\n<!-- from pack: matt -->\nHouse rules.\n\nLate rule.\n\n<!-- from pack: zc -->\nZero.\n"
	if got != want {
		t.Errorf("claude:\n got %q\nwant %q", got, want)
	}
	got = ComposePackBriefings("", packs, "pi", false)
	if want := "\n\nHouse rules.\n\nPi only.\n\nLate rule.\n\nZero.\n"; got != want {
		t.Errorf("pi, unlabelled:\n got %q\nwant %q", got, want)
	}
}

func TestComposePackBriefingsSkipsEmpty(t *testing.T) {
	got := ComposePackBriefings("BASE\n", []PackBriefing{{Name: "quiet", Text: "  \n"}}, "claude", true)
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

// The one-time handoff (docs/reference/host-to-jail-handoff.md): a fresh handoff renders as
// a prominent Handoff section, and NOTHING is emitted without one — there is no standing
// "where your task comes from" line, because an always-present line would move the pinned
// jail header (TestBriefingJailHeaderIsUnchanged). The design called for that line; the
// pinned bytes vetoed it, so the one-time-ness is stated inside the section instead.
//
// This pins the RENDER half only. The wire from .yolo/handover.md to here is pinned in
// internal/cli/run/consumehandoff_test.go, which drives refreshJailBriefings — a test at
// this level cannot fail when the call site is deleted.
func TestBriefingContentHandoff(t *testing.T) {
	withHandoff := BriefingContent(BriefingInput{
		Workspace: "/home/me/proj",
		Handoff:   "Task: wire the OAuth broker. Context: docs/handoff/oauth.md",
	})
	for _, want := range []string{
		"## Handoff",
		"it is **the task**",
		"This appears once",
		"Task: wire the OAuth broker. Context: docs/handoff/oauth.md",
	} {
		if !contains(withHandoff, want) {
			t.Errorf("handoff briefing missing %q:\n%s", want, withHandoff)
		}
	}
	// Without a handoff: no section. No standing line either — the default "wait for the
	// user" is the whole story, and an always-present line would move the pinned jail header.
	noHandoff := BriefingContent(BriefingInput{Workspace: "/home/me/proj"})
	if contains(noHandoff, "## Handoff") {
		t.Errorf("no handoff must not emit a Handoff section:\n%s", noHandoff)
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

// THE JAIL NOTCH WITHOUT A CONTAINER. macos-user runs at `confinement: jail` — the
// notch dial is a separate axis from the runtime — but there is no container in it.
// The header claimed "a sandboxed container" there until 2026-09-04, which is the
// same dangerous falsehood the default branch was rewritten to avoid, arriving
// through the one branch still allowed to assert it.
//
// Caught by running the manual verification checklist on a real Mac, not by a test:
// the briefing was delivered correctly and said something false.
func TestJailBriefingDoesNotClaimAContainerWhenThereIsNone(t *testing.T) {
	got := BriefingContent(BriefingInput{
		Workspace:   "/Users/Shared/yolo/proj",
		Confinement: "jail",
		Mechanism:   "macos-user",
	})

	if strings.Contains(got, "sandboxed container") {
		t.Errorf("claimed a container on a backend that has none:\n%s", got)
	}
	// An agent told it is in a disposable container reasons about its home as
	// throwaway. Here it is neither disposable nor exclusively its own, and both
	// halves have to be said.
	for _, want := range []string{"Seatbelt", "PERSISTS", "shared by every workspace"} {
		if !strings.Contains(got, want) {
			t.Errorf("the header does not say %q — an agent would still assume a "+
				"disposable home:\n%s", want, got)
		}
	}
}

// The container case is UNCHANGED: every other backend still gets the historical
// header, byte for byte, because that is what the goldens and every existing jail
// expect.
func TestJailBriefingStillClaimsAContainerWhenThereIsOne(t *testing.T) {
	got := BriefingContent(BriefingInput{
		Workspace:   "/workspace",
		Confinement: "jail",
	})
	if !strings.Contains(got, "You are running inside a YOLO Jail — a sandboxed container.") {
		t.Errorf("the container header changed for container backends:\n%s", got)
	}
}
