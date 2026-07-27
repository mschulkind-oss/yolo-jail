package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// userPacksConfig writes a user config with the given body and points HOME at it, so
// config.LoadPacks (which reads paths.UserConfigPath() DIRECTLY, never the merged
// config) sees exactly this file.
func userPacksConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noPacksOutput runs warnIfNoPacks over a stderr buffer and returns what it wrote.
func noPacksOutput(t *testing.T) string {
	t.Helper()
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.warnIfNoPacks()
	return errBuf.String()
}

// The empty-packs notice is the whole discoverability story for a jail with no agent:
// with zero agents no briefing file is written at all (refreshJailBriefings loops over
// the RESOLVED agents), so there is nowhere to leave a note — it must be printed.
func TestWarnIfNoPacksPrintsGuidance(t *testing.T) {
	userPacksConfig(t, `{}`)
	got := noPacksOutput(t)
	if got == "" {
		t.Fatal("no packs configured produced no output")
	}
	// The two load-bearing claims: that there is no agent, and a command to run.
	// A user who sees only "no packs are configured" learns nothing they can act on.
	for _, want := range []string{"no coding agent", "yolo pack --help"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice missing %q:\n%s", want, got)
		}
	}
	// Short: this prints right before the jail takes the terminal, so it must not
	// push the banner off a small window.
	if lines := strings.Count(strings.TrimRight(got, "\n"), "\n") + 1; lines > 3 {
		t.Errorf("notice is %d lines, want <= 3:\n%s", lines, got)
	}
}

// The notice must go to STDERR. A launch is usually `yolo -- cmd`, and the user
// redirects the COMMAND's stdout; a notice on stdout would be swallowed by that
// redirect (or, worse, corrupt a piped payload).
func TestWarnIfNoPacksWritesToStderr(t *testing.T) {
	userPacksConfig(t, `{}`)
	var outBuf, errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = &outBuf
	o.Stderr = &errBuf
	o.warnIfNoPacks()
	if outBuf.Len() != 0 {
		t.Errorf("notice leaked to stdout:\n%s", outBuf.String())
	}
	if errBuf.Len() == 0 {
		t.Error("notice did not reach stderr")
	}
}

// One configured pack silences it. This is the half that keeps the notice from
// becoming background noise every launch — the state it reports is "you have nothing",
// not "you might want more".
func TestWarnIfNoPacksSilentWithAPack(t *testing.T) {
	pack := t.TempDir()
	userPacksConfig(t, `{"packs": ["file://`+pack+`"]}`)
	if got := noPacksOutput(t); got != "" {
		t.Errorf("a configured pack must silence the notice, got:\n%s", got)
	}
}

// A `packs` value that is present but UNUSABLE must not produce this notice: validatePacks
// fails the launch naming the real problem, and "you have no packs" would be a
// misdiagnosis of "your packs are malformed".
//
// All three shapes matter, and only the first is a LoadPacks error. A non-list value and
// a list whose every entry is invalid both return zero entries with a NIL error, because
// checkPacks routes per-entry problems to the warn callback instead — so an `err != nil`
// guard alone reported both as "no packs are configured". Table-driven for exactly that
// reason: the two nil-error shapes are the ones a future reader would not predict.
func TestWarnIfNoPacksSilentWhenPacksAreConfiguredButUnusable(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"unparseable config", `{"packs": `},
		{"not a list", `{"packs": {"a": "b"}}`},
		{"every entry invalid", `{"packs": ["nonsense"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userPacksConfig(t, tc.body)
			if got := noPacksOutput(t); got != "" {
				t.Errorf("configured-but-broken packs must not be reported as an empty "+
					"list, got:\n%s", got)
			}
		})
	}
}

// The notice text is the SHARED config constant, not a local copy — the other half of
// TestSectionPacksUsesTheSharedNoPacksText. The two surfaces used to keep separate
// copies (check cannot import this package) and had already drifted by a trailing
// period; both now read config.NoPacksMessage/NoPacksGuidance so they cannot diverge.
// The period is appended HERE because this is prose and a check badge line is not.
func TestWarnIfNoPacksUsesTheSharedNoPacksText(t *testing.T) {
	userPacksConfig(t, `{}`)
	got := noPacksOutput(t)
	for _, want := range []string{config.NoPacksMessage + ".", config.NoPacksGuidance} {
		if !strings.Contains(got, want) {
			t.Errorf("notice does not render the shared constant %q verbatim:\n%s", want, got)
		}
	}
}

// The notice keys off the PACK list, not the agent list.
//
// The assertion that carries this is the INVERSE one: a config with NO packs that still
// names agents must STILL warn. (Its twin — a pack silencing the notice — is already
// TestWarnIfNoPacksSilentWithAPack, and adding an ignored `agents` key to that fixture
// asserts nothing, since no code path reads it.) A notice written against
// config.SelectedAgents rather than the pack list would either die with that transitional
// shim or resurrect the deleted `agents` key; this fails in both cases.
func TestWarnIfNoPacksKeysOffPacksNotAgents(t *testing.T) {
	userPacksConfig(t, `{"agents": ["claude"]}`)
	got := noPacksOutput(t)
	if got == "" {
		t.Fatal("a config naming `agents` but no packs must still warn — the key is " +
			"deleted, so it cannot be evidence that a jail has an agent")
	}
	if !strings.Contains(got, "no coding agent") {
		t.Errorf("notice missing the no-agent claim:\n%s", got)
	}
}
