package cli

// applyhostlocalpack_test.go is the HOST-NOTCH gate for the conventional local pack
// (`~/.config/yolo-jail/local`, outstanding-work.md §6a-2): the user's own skills and briefing
// prose, included with no `packs` entry.
//
// It must work at BOTH notches or the convention reintroduces the asymmetry finding F1 just
// closed — a pack the jail delivers and the host silently does not. The jail half is
// internal/cli/run/localpack_test.go; this half drives `applyHost` end-to-end, which is the
// only level the delivery exists at.
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME is never
// read or written — and this feature makes that especially load-bearing, since the real
// ~/.config/yolo-jail holds the developing jail's own live config.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localPackFixture writes a conventional local pack (skills/ + AGENTS.md, no pack.json) into a
// temp home's config dir and selects `alongside` as the configured packs (a raw `packs` list
// fragment, so a test can select agent packs or none).
func localPackFixture(t *testing.T, alongside, skill, body string) string {
	t.Helper()
	home := t.TempDir()
	local := filepath.Join(home, ".config", "yolo-jail", "local")
	writeFile(t, filepath.Join(local, "skills", skill, "SKILL.md"),
		"---\nname: "+skill+"\ndescription: d\n---\n"+body+"\n")
	writeFile(t, filepath.Join(local, "AGENTS.md"), "My own briefing prose.\n")
	selectPacks(t, home, alongside)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// THE CONVENTION. A skills tree and an AGENTS.md in ~/.config/yolo-jail/local reach the
// destinations the configured packs name, with nothing in `packs` mentioning them. The pack is
// zero-ceremony, so it also exercises packload.ResolveDestinations at the host — which is what
// makes this a convention rather than a second delivery mechanism.
func TestApplyHostDeliversTheLocalPack(t *testing.T) {
	home := localPackFixture(t, `"claude"`, "mine", "Personal body.")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	// UNNAMESPACED, at a destination whose AGENT PACK is namespaced. That inversion is S1/S2: the
	// tier used to be inherited from the destination's declaring pack, so this same skill landed at
	// `.claude/skills/local/skills/mine` here and at `.codex/skills/mine` elsewhere — one skill,
	// two invocation names, neither of them chosen by the pack that owns the content. A tier is now
	// the PACK's own opt-in, and the local pack (which has no manifest at all) never opts in.
	skill := filepath.Join(home, ".claude", "skills", "mine", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("the local pack's skill did not reach %s: %v\nreport:\n%s", skill, err, report)
	}
	// And NOT under a namespace it did not ask for.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "local")); err == nil {
		t.Errorf("the local pack got a namespaced subtree it never declared — `skills_tier` is a "+
			"POSITIVE choice, and a pack with no pack.json cannot have made it:\n%s", report)
	}
	brief, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("the local pack's prose did not reach ~/.claude/CLAUDE.md: %v\n%s", err, report)
	}
	if !strings.Contains(string(brief), "My own briefing prose.") {
		t.Errorf("the briefing does not carry the local pack's prose:\n%s", brief)
	}
}

// ONE COPY, EVERY AGENT, UNDER ONE NAME — the win the ruling names as the real risk it addresses,
// plus the half S1/S2 added.
//
// Before S2 the paths below were NOT parallel: claude's copy sat at `.claude/skills/local/skills/`
// because the tier was inherited per destination, while pi's and codex's were bare. So the user
// invoked `/local:mine` in one agent and `/mine` in the other two — a split-brain in the naming of
// a single skill, which the pack that owns the content never asked for and could not express. The
// symmetry of this list IS the assertion.
func TestApplyHostLocalPackReachesEveryAgentPackUnderOneName(t *testing.T) {
	home := localPackFixture(t, `"claude","pi","codex"`, "mine", "Personal body.")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	for _, rel := range []string{
		filepath.Join(".claude", "skills", "mine", "SKILL.md"),
		filepath.Join(".pi", "agent", "skills", "mine", "SKILL.md"),
		filepath.Join(".codex", "skills", "mine", "SKILL.md"),
	} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("the local pack's skill did not reach ~/%s: %v\n%s", rel, err, report)
		}
	}
	// The invocation name is the same everywhere, which is the fact the paths above encode. Asserted
	// on the REPORT too, because the detail line is what tells the user what to type: three
	// `invoke as /mine` lines and no `invoke as /local:mine` anywhere.
	if n := countLines(report, "invoke as /mine"); n != 3 {
		t.Errorf("want one `invoke as /mine` line per destination (3), got %d:\n%s", n, report)
	}
	if n := countLines(report, "invoke as /local:mine"); n != 0 {
		t.Errorf("the local pack's skill is namespaced at some destination — one skill must have "+
			"ONE name at every notch, got %d such lines:\n%s", n, report)
	}
}

// ABSENT IS NORMAL, and must be indistinguishable from today: no warning, no error, no line.
// Most users will never create the directory, so its absence cannot cost them a message about
// a feature they have not used.
func TestApplyHostWithoutALocalPackSaysNothing(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"claude"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	// `local` as a WORD, not as a substring: the `program` line resolves claude on the
	// invoking machine's PATH and prints e.g. /home/agent/.local/bin/claude, so a plain
	// strings.Contains fails on every report ever produced. namesLocalPack is what
	// distinguishes "yolo mentioned the local pack" from "a path happened to contain .local".
	if line, named := namesLocalPack(report); named {
		t.Errorf("an absent local pack was mentioned — its absence must be silent:\n%s\n"+
			"(full report:\n%s)", line, report)
	}
}

// namesLocalPack reports whether any report line names the local PACK, returning that line.
//
// Word-bounded on both sides, which is what excludes a path component (`.local/bin`, preceded
// by `.` and followed by `/`) while catching every line the delivery would actually print
// (`skills     local declares no destination …`, `local/briefing  rendered …`).
func namesLocalPack(report string) (string, bool) {
	for _, line := range strings.Split(report, "\n") {
		for _, f := range strings.Fields(line) {
			if f == "local" || strings.HasPrefix(f, "local/") {
				return line, true
			}
		}
	}
	return "", false
}

// ORDER IS LOAD-BEARING AND IT IS LAST: the local pack is appended after every configured
// entry, so it renders last. Asserted on the REPORT order rather than on a same-named file,
// because at the host notch hostskills' tier-B rule refuses to overwrite another pack's
// recorded entry regardless of order (that is a pre-existing property of flat delivery, not
// something the local pack changes) — so the position in the render is the observable that
// actually corresponds to "composes last".
func TestApplyHostLocalPackRendersLast(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "shared")
	writeFile(t, filepath.Join(shared, "skills", "shrd", "SKILL.md"),
		"---\nname: shrd\ndescription: d\n---\nShared body.\n")
	home := t.TempDir()
	local := filepath.Join(home, ".config", "yolo-jail", "local")
	writeFile(t, filepath.Join(local, "skills", "mine", "SKILL.md"),
		"---\nname: mine\ndescription: d\n---\nPersonal body.\n")
	selectPacks(t, home, `"claude",{"source":"file://`+shared+`","name":"shared"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	mine, shrd := strings.Index(report, "mine"), strings.Index(report, "shrd")
	if mine < 0 || shrd < 0 {
		t.Fatalf("both skills must be delivered (mine=%d shrd=%d):\n%s", mine, shrd, report)
	}
	if mine < shrd {
		t.Errorf("the local pack rendered BEFORE the configured pack — it must compose last so a "+
			"personal skill outranks a shared pack's:\n%s", report)
	}
}

// A CONFIGURED pack named `local` wins the slot and the convention stays inert: two packs with
// one name share a staging dir and one silently overwrites the other, and a config line the
// user wrote outranks a convention yolo applied for them.
func TestApplyHostConfiguredPackNamedLocalWins(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit")
	writeFile(t, filepath.Join(explicit, "skills", "fromconfig", "SKILL.md"),
		"---\nname: fromconfig\ndescription: d\n---\nExplicit body.\n")
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "skills",
		"fromconvention", "SKILL.md"), "---\nname: fromconvention\ndescription: d\n---\nConv.\n")
	selectPacks(t, home, `"claude",{"source":"file://`+explicit+`","name":"local"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	// Unnamespaced: the explicit `local` entry declares no `skills_tier` either (S1/S2).
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills",
		"fromconfig", "SKILL.md")); err != nil {
		t.Errorf("the CONFIGURED pack named `local` did not deliver: %v\n%s", err, report)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills",
		"fromconvention")); err == nil {
		t.Error("the conventional dir delivered too — two packs named `local` would share one " +
			"staging dir, and the explicit entry must win the slot outright")
	}
}
