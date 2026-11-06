package jailcontent

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
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
	// An empty base composes the sections alone (ComposeBriefingSections), which is the host
	// notch's historical shape; the jail used to lead with a blank line it never shipped, because
	// no jail destination has an empty base.
	got := ComposePackBriefings("", packs, "claude", true)
	want := "<!-- from pack: matt -->\nHouse rules.\n\nLate rule.\n\n<!-- from pack: zc -->\nZero.\n"
	if got != want {
		t.Errorf("claude:\n got %q\nwant %q", got, want)
	}
	got = ComposePackBriefings("", packs, "pi", false)
	if want := "House rules.\n\nPi only.\n\nLate rule.\n\nZero.\n"; got != want {
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

// The selected setup, not a raw confinement marker, names the briefing header.
func TestBriefingSetupHeader(t *testing.T) {
	podman := setup.MustBuiltIn("podman-self")
	jail := BriefingContent(BriefingInput{Workspace: "/home/me/proj", Setup: podman})
	if !contains(jail, "setup **podman-self**") || !contains(jail, "inside a container") {
		t.Errorf("podman setup briefing must name the setup and container:\n%s", jail)
	}

	hostSetup := setup.MustBuiltIn("host-self")
	host := firstParagraph(BriefingContent(BriefingInput{Workspace: "/home/me/proj", Setup: hostSetup}))
	for _, want := range []string{"setup **host-self**", "real machine", "NOT disposable"} {
		if !contains(host, want) {
			t.Errorf("host setup briefing header must name it and warn it is not disposable (missing %q):\n%s", want, host)
		}
	}
	if contains(host, "inside a container") {
		t.Errorf("host setup briefing must NOT claim a container:\n%s", host)
	}

	guestSetup := setup.MustBuiltIn("seatbelt-self")
	guest := firstParagraph(BriefingContent(BriefingInput{Workspace: "/home/me/proj", Setup: guestSetup, Mechanism: "macos-user", IsMacOS: true}))
	if !contains(guest, "setup **seatbelt-self**") || contains(guest, "inside a container") {
		t.Errorf("Seatbelt setup briefing must name it and avoid a container claim:\n%s", guest)
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

// THE SEATBELT SETUP RUNS WITHOUT A CONTAINER. Its selected setup and backend determine the
// briefing; the setup name stays explicit and the profile describes the real boundary.
func TestJailBriefingDoesNotClaimAContainerWhenThereIsNone(t *testing.T) {
	got := BriefingContent(BriefingInput{
		Workspace: "/Users/Shared/yolo/proj",
		Setup:     setup.MustBuiltIn("seatbelt-self"),
		Mechanism: "macos-user",
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

// Container setup briefings identify the selected setup and do not revive the old generic
// confinement-header wording.
func TestJailBriefingStillClaimsAContainerWhenThereIsOne(t *testing.T) {
	got := BriefingContent(BriefingInput{
		Workspace: "/workspace",
		Setup:     setup.MustBuiltIn("podman-self"),
	})
	if !strings.Contains(got, "You are running under setup **podman-self**") || !strings.Contains(got, "inside a container") {
		t.Errorf("the container header changed for container backends:\n%s", got)
	}
}

// An ABSENT host briefing is the normal state and no error; one that is there and cannot be read
// is an error the caller names, with the jail content still returned. It used to be swallowed with
// absence, so the briefing silently lost the user's own instructions.
func TestPrependHostBriefingReportsAReadFailureButNotAbsence(t *testing.T) {
	dir := t.TempDir()
	const jail = "JAIL CONTENT\n"

	got, err := PrependHostBriefing(filepath.Join(dir, "absent.md"), jail)
	if err != nil || got != jail {
		t.Errorf("absent host briefing: got (%q, %v), want (%q, nil)", got, err, jail)
	}

	notAFile := filepath.Join(dir, "AGENTS.md")
	must(t, os.Mkdir(notAFile, 0o755))
	got, err = PrependHostBriefing(notAFile, jail)
	if err == nil || got != jail {
		t.Errorf("unreadable host briefing: got (%q, %v), want (%q, an error)", got, err, jail)
	}

	readable := filepath.Join(dir, "CLAUDE.md")
	must(t, os.WriteFile(readable, []byte("MINE\n"), 0o644))
	got, err = PrependHostBriefing(readable, jail)
	if want := "MINE\n\n---\n\n" + jail; err != nil || got != want {
		t.Errorf("readable host briefing: got (%q, %v), want (%q, nil)", got, err, want)
	}
}

// The jail briefing carries the three rules for handing the human something to do on the
// host, and its example menu is byte for byte what the one renderer the brokers' refusals use
// prints, in a fenced block. It names no harness: a pack that installs one adds that line.
func TestTheJailBriefingCarriesTheHandOffRules(t *testing.T) {
	jail := BriefingContent(BriefingInput{Workspace: "/w"})
	for _, want := range []string{
		"**Approvals are not host-run.**",
		"never through `yolo host-run`",
		"**Each command for the human goes alone on its own line in a fenced code block**",
		"it is copied, not read",
		"**Start the waiter in the background in the same turn**",
		"Exit 0: retry the refused command exactly once",
		"Relay the whole menu, and recommend one path",
		"Never report a host capability as impossible",
		"run `yolo wait --timeout <less than your",
		"Never wait in a loop",
	} {
		if !strings.Contains(jail, want) {
			t.Errorf("the jail briefing lacks %q", want)
		}
	}
	example := "```text\n" + briefingMenuExample.Render() + "```\n"
	if !strings.Contains(jail, example) {
		t.Errorf("the jail briefing's example is not the renderer's output:\n%s", example)
	}
	for _, line := range []string{"    yolo approve r-<id>\n", "    yolo approve r-<id> --for 15m\n",
		"    yolo request git push <branch>\n", "    yolo config apply\n", "    yolo wait r-<id>\n"} {
		if !strings.Contains(example, line) {
			t.Errorf("the example menu does not put %q on its own line", line)
		}
	}
	// `yolo approve --add` and `--remember` are removed (GB-D28, OQ-GW2): the briefing offers
	// neither, in its example menu or in its prose.
	for _, removed := range []string{"--add", "--remember"} {
		if strings.Contains(hostRunSectionText(), removed) {
			t.Errorf("the briefing still offers the removed %s", removed)
		}
	}
	for _, harness := range []string{"run_in_background", "background_task", "Claude Code", "completionPolicy"} {
		if strings.Contains(hostRunSectionText(), harness) {
			t.Errorf("the core section names a harness: %q", harness)
		}
	}
}

func hostRunSectionText() string { return strings.Join(hostRunSection(), "\n") }

// The jail briefing teaches `yolo host-run` (docs/reference/host-run-mailbox.md); the host
// notch's base does not, since there is no jail to deliver to. A locked config's briefing
// carries no host-run line for the check's output, only the mechanics pointer.
func TestTheJailBriefingTeachesHostRun(t *testing.T) {
	jail := BriefingContent(BriefingInput{Workspace: "/w"})
	for _, want := range []string{"## Running a command on the host", "Then start `yolo wait <fresh-label>` in the background",
		"\nyolo host-run -C <value of $YOLO_HOST_DIR> --name <fresh-label> -- <command>\n"} {
		if !strings.Contains(jail, want) {
			t.Errorf("the jail briefing lacks %q", want)
		}
	}
	if host := HostBriefingBase("", false); strings.Contains(host, "## Running a command on the host") {
		t.Error("the host notch's briefing teaches host-run")
	}
	locked := BriefingContent(BriefingInput{Workspace: "/w", ConfigLocked: true})
	if strings.Contains(locked, "yolo host-run -C <value of $YOLO_HOST_DIR> -- yolo check") {
		t.Error("the briefing still carries config-edit instructions instead of the mechanics pointer")
	}
}
