package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// A pack list that fails to LOAD must not produce this notice: stagePacks turns a
// broken `packs` into a fatal launch error naming the real problem, and "you have no
// packs" would be a misdiagnosis of "your packs are malformed".
func TestWarnIfNoPacksSilentOnLoadError(t *testing.T) {
	userPacksConfig(t, `{"packs": `)
	if got := noPacksOutput(t); got != "" {
		t.Errorf("a load error must not be reported as an empty pack list, got:\n%s", got)
	}
}

// The notice keys off the PACK list, not the agent list. Guards the requirement
// directly: config.SelectedAgents is a transitional shim on its way out, and a notice
// written against it would either die with it or resurrect the deleted `agents` key.
func TestWarnIfNoPacksKeysOffPacksNotAgents(t *testing.T) {
	pack := t.TempDir()
	// A config that names a pack AND (legacy, ignored) agents: the pack decides.
	userPacksConfig(t, `{"packs": ["file://`+pack+`"], "agents": []}`)
	if got := noPacksOutput(t); got != "" {
		t.Errorf("a configured pack must silence the notice regardless of `agents`, got:\n%s", got)
	}
}
