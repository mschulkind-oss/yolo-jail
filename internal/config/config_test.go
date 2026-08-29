package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Unit tests for filesystem-backed config loading, includes/cycles, and the
// interactive config-change control flow.

func decode(t *testing.T, s string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decode %q: not a map (%T)", s, v)
	}
	return m
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- loading / includes (TestIncludeIfFound + TestWorkspaceLocalConfig) ----

func TestLoadJSONCFileNonexistentReturnsEmpty(t *testing.T) {
	m, err := LoadJSONCFile(filepath.Join(t.TempDir(), "nope.jsonc"), "test", false, discard)
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 0 {
		t.Errorf("expected empty, got %d keys", m.Len())
	}
}

func TestLoadJSONCFileNonObjectStrictRaises(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonc")
	write(t, p, "[1, 2, 3]")
	if _, err := LoadJSONCFile(p, "test", true, discard); err == nil {
		t.Errorf("expected ConfigError for non-object in strict mode")
	}
}

func TestLoadJSONCFileInvalidStrictRaises(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonc")
	write(t, p, "{broken json")
	if _, err := LoadJSONCFile(p, "test", true, discard); err == nil {
		t.Errorf("expected ConfigError for invalid JSON in strict mode")
	}
}

func TestIncludeChainAndCycle(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.jsonc"), `{"packages": ["a"], "include_if_found": ["b.jsonc"]}`)
	write(t, filepath.Join(dir, "b.jsonc"), `{"packages": ["b"], "include_if_found": ["c.jsonc"]}`)
	write(t, filepath.Join(dir, "c.jsonc"), `{"packages": ["c"]}`)
	m, err := LoadJSONCWithIncludes(filepath.Join(dir, "a.jsonc"), "a", false, discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m, "a", "b", "c")
	if _, ok := m.Get("include_if_found"); ok {
		t.Errorf("include_if_found should be consumed")
	}

	// Cycle a->b->a must terminate with both present.
	write(t, filepath.Join(dir, "a.jsonc"), `{"packages": ["a"], "include_if_found": ["b.jsonc"]}`)
	write(t, filepath.Join(dir, "b.jsonc"), `{"packages": ["b"], "include_if_found": ["a.jsonc"]}`)
	m2, err := LoadJSONCWithIncludes(filepath.Join(dir, "a.jsonc"), "a", false, discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m2, "a", "b")
}

func TestIncludeAbsoluteRejectedStrict(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "base.jsonc"), `{"include_if_found": ["/etc/passwd"]}`)
	if _, err := LoadJSONCWithIncludes(filepath.Join(dir, "base.jsonc"), "base", true, discard, nil); err == nil {
		t.Errorf("expected ConfigError for absolute include in strict mode")
	}
}

func TestWorkspaceLocalWinsAndMerges(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, WorkspaceConfigName), `{"packages": ["just"], "network": {"mode": "bridge"}}`)
	write(t, filepath.Join(dir, WorkspaceLocalConfigName), `{"packages": ["htop"], "network": {"mode": "host"}}`)
	m, err := LoadWorkspaceConfig(dir, false, discard)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m, "just", "htop")
	net, _ := m.Get("network")
	mode, _ := net.(*jsonx.OrderedMap).Get("mode")
	if mode != "host" {
		t.Errorf("network.mode = %v, want host", mode)
	}
}

func TestWorkspaceExplicitIncludeOfLocalNotMergedTwice(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, WorkspaceConfigName),
		`{"packages": ["just"], "include_if_found": ["yolo-jail.local.jsonc"]}`)
	write(t, filepath.Join(dir, WorkspaceLocalConfigName), `{"packages": ["htop"]}`)
	m, err := LoadWorkspaceConfig(dir, false, discard)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m, "just", "htop")
}

func TestLoadWorkspaceConfigSupportsJSONExtension(t *testing.T) {
	dir := t.TempDir()
	// Write yolo-jail.json (without 'c') with JSONC comments & trailing commas
	write(t, filepath.Join(dir, "yolo-jail.json"), `{
		// Comments in .json file
		"packages": ["just"],
		"network": {"mode": "bridge",},
	}`)
	write(t, filepath.Join(dir, "yolo-jail.local.json"), `{
		// Local override in .json
		"packages": ["htop"],
	}`)
	m, err := LoadWorkspaceConfig(dir, false, discard)
	if err != nil {
		t.Fatal(err)
	}
	assertPackages(t, m, "just", "htop")
}

// ---- CheckConfigChanges control flow (TestConfigSnapshot) ----

// approvalWorkspace sets up an isolated HOST for one approval test: a real
// workspace directory plus a private $HOME, so ApprovalSnapshotPath resolves under
// a temp state dir instead of the developer's own. The workspace is CREATED rather
// than merely named because the approval key is runtime.FromWorkspace, which
// resolves symlinks when the path exists and falls back to the lexical form when
// it does not — an existing directory keeps the key stable across a test that both
// writes and reads it (on darwin /tmp is a symlink to /private/tmp, which is
// exactly where the two forms diverge).
func approvalWorkspace(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return ws
}

// approve is the accept-and-record call every test uses to establish a baseline:
// non-interactive with the flag granted, which is the one path that both proceeds
// and rewrites the snapshot without a prompter.
func approve(t *testing.T, ws string, cfg *jsonx.OrderedMap) {
	t.Helper()
	ok, err := CheckConfigChanges(ws, cfg, false, true, nil)
	if err != nil || !ok {
		t.Fatalf("establishing the approved baseline: ok=%v err=%v", ok, err)
	}
}

func TestCheckConfigChangesEmptyWorkspacePassesSilently(t *testing.T) {
	ws := approvalWorkspace(t)
	config := decode(t, `{}`)
	ok, err := CheckConfigChanges(ws, config, false, false, nil)
	if err != nil || !ok {
		t.Fatalf("empty config first run: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(ApprovalSnapshotPath(ws)); err != nil {
		t.Errorf("snapshot not written: %v", err)
	}
}

func TestCheckConfigChangesFreshWorkspaceNonEmptyPrompts(t *testing.T) {
	ws := approvalWorkspace(t)
	config := decode(t, `{"packages": ["strace"]}`)

	// Without flag or TTY prompter -> refused
	ok, err := CheckConfigChanges(ws, config, false, false, nil)
	if ok || err == nil {
		t.Fatalf("fresh non-empty workspace without TTY or flag must refuse, got ok=%v err=%v", ok, err)
	}

	// With TTY prompter -> prompts with "none (initial launch)" and saves
	prompter := &recordingPrompter{accept: true}
	ok, err = CheckConfigChanges(ws, config, true, false, prompter)
	if err != nil || !ok {
		t.Fatalf("fresh workspace TTY accept failed: ok=%v err=%v", ok, err)
	}
	if !prompter.called {
		t.Fatal("fresh non-empty workspace must prompt")
	}
	joined := strings.Join(prompter.diff, "\n")
	if !strings.Contains(joined, "none (initial launch)") || !strings.Contains(joined, "strace") {
		t.Errorf("fresh workspace prompt should show initial launch diff:\n%s", joined)
	}
	if _, err := os.Stat(ApprovalSnapshotPath(ws)); err != nil {
		t.Errorf("snapshot not written: %v", err)
	}
}

// THE APPROVAL RECORD MUST LAND OUTSIDE THE WORKSPACE (OQ-D1). /workspace is
// bind-mounted read-write, so a record kept in there can be rewritten by the very
// agent whose edits it exists to gate — the next launch then shows nothing to
// approve. This pins both halves: the new path is under the host state dir, and
// the old workspace path is not written at all.
func TestCheckConfigChangesSnapshotLandsOutsideTheWorkspace(t *testing.T) {
	ws := approvalWorkspace(t)
	approve(t, ws, decode(t, `{"packages": ["strace"]}`))

	snap := ApprovalSnapshotPath(ws)
	if strings.HasPrefix(snap, ws+string(filepath.Separator)) {
		t.Errorf("approval snapshot is inside the workspace (%s) — an agent could rewrite it", snap)
	}
	if want := paths.ApprovalsDir(); !strings.HasPrefix(snap, want+string(filepath.Separator)) {
		t.Errorf("approval snapshot %s is not under the host approvals dir %s", snap, want)
	}
	if _, err := os.Stat(LegacyWorkspaceSnapshotPath(ws)); !os.IsNotExist(err) {
		t.Errorf("the old workspace-side location was written (err=%v)", err)
	}
}

type failPrompter struct {
	t      *testing.T
	called bool
}

func (p *failPrompter) Prompt(diffLines []string) bool {
	p.called = true
	p.t.Errorf("unexpected prompt with diff:\n%v", diffLines)
	return false
}

func TestCheckConfigChangesUnchangedPasses(t *testing.T) {
	ws := approvalWorkspace(t)
	config := decode(t, `{"packages": ["strace"]}`)
	approve(t, ws, config)
	ok, err := CheckConfigChanges(ws, config, false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("unchanged: ok=%v err=%v", ok, err)
	}
}

// NON-INTERACTIVE + CHANGED IS FATAL (OQ-D2). This used to auto-accept and rewrite
// the snapshot, which made the human-approval promise conditional on somebody
// happening to have a terminal attached — and the scripted launch is exactly the
// one nobody is watching.
func TestCheckConfigChangesNonTTYRefuses(t *testing.T) {
	ws := approvalWorkspace(t)
	orig := decode(t, `{"packages": ["strace"]}`)
	approve(t, ws, orig)

	newCfg := decode(t, `{"packages": ["strace", "htop"]}`)
	ok, err := CheckConfigChanges(ws, newCfg, false /*non-tty*/, false, &failPrompter{t: t})
	if ok {
		t.Fatal("non-tty + changed config must refuse the launch")
	}
	var refusal *ChangedNonInteractiveError
	if !errors.As(err, &refusal) {
		t.Fatalf("want *ChangedNonInteractiveError, got %T: %v", err, err)
	}

	// The reader of this message is by construction someone who cannot be
	// prompted, so it has to carry everything they need: the flag, the files, and
	// what actually changed.
	msg := refusal.Error()
	for _, want := range []string{
		AcceptConfigChangesFlag,
		filepath.Join(ws, WorkspaceConfigName),
		ApprovalSnapshotPath(ws),
		"+    \"htop\"",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message does not mention %q:\n%s", want, msg)
		}
	}

	// The snapshot is untouched, so an interactive launch still has the same
	// change to show.
	want, _ := SnapshotJSON(orig)
	got, _ := os.ReadFile(ApprovalSnapshotPath(ws))
	if string(got) != want+"\n" {
		t.Errorf("a refused launch must not rewrite the approval record")
	}
}

// The flag is the opt-in Design Goal 5 survives through: non-interactive use still
// works, via an explicit approval rather than an implicit yes. It must record the
// approval exactly as a `y` does, or the next run refuses over the same change.
func TestCheckConfigChangesNonTTYFlagAcceptsAndRecords(t *testing.T) {
	ws := approvalWorkspace(t)
	approve(t, ws, decode(t, `{"packages": ["strace"]}`))

	newCfg := decode(t, `{"packages": ["strace", "htop"]}`)
	ok, err := CheckConfigChanges(ws, newCfg, false /*non-tty*/, true /*flag*/, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("non-tty + flag: ok=%v err=%v", ok, err)
	}
	want, _ := SnapshotJSON(newCfg)
	got, _ := os.ReadFile(ApprovalSnapshotPath(ws))
	if string(got) != want+"\n" {
		t.Fatalf("snapshot not updated by the flag: %q", string(got))
	}
	// And the recorded approval sticks: the same config now passes with no flag.
	ok, err = CheckConfigChanges(ws, newCfg, false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Errorf("re-running the approved config must proceed: ok=%v err=%v", ok, err)
	}
}

func TestCheckConfigChangesTTYYesUpdates(t *testing.T) {
	ws := approvalWorkspace(t)
	approve(t, ws, decode(t, `{"packages": ["strace"]}`))
	newCfg := decode(t, `{"packages": ["strace", "htop"]}`)
	ok, err := CheckConfigChanges(ws, newCfg, true, false, yesPrompter{})
	if err != nil || !ok {
		t.Fatalf("tty yes: ok=%v err=%v", ok, err)
	}
	want, _ := SnapshotJSON(newCfg)
	got, _ := os.ReadFile(ApprovalSnapshotPath(ws))
	if string(got) != want+"\n" {
		t.Errorf("snapshot not updated on tty-yes")
	}
}

func TestCheckConfigChangesTTYNoRejectsAndKeepsSnapshot(t *testing.T) {
	ws := approvalWorkspace(t)
	orig := decode(t, `{"packages": ["strace"]}`)
	approve(t, ws, orig)
	newCfg := decode(t, `{"packages": ["strace", "htop"]}`)
	ok, err := CheckConfigChanges(ws, newCfg, true, false, noPrompter{})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("tty no: expected proceed=false")
	}
	// Snapshot NOT updated — still the original.
	want, _ := SnapshotJSON(orig)
	got, _ := os.ReadFile(ApprovalSnapshotPath(ws))
	if string(got) != want+"\n" {
		t.Errorf("snapshot changed on tty-no rejection")
	}
}

// ---- OQ-S3: Fresh workspace initial configuration confirmation ----

// A fresh workspace with non-empty configuration prompts on first launch.
// When rejected, no approval snapshot is recorded.
func TestCheckConfigChangesFreshWorkspaceRefusedRecordsNoSnapshot(t *testing.T) {
	ws := approvalWorkspace(t)
	cfg := decode(t, `{"packages": ["strace"]}`)

	ok, _ := CheckConfigChanges(ws, cfg, true, false, noPrompter{})
	if ok {
		t.Fatal("a rejected initial config must not proceed")
	}
	if _, err := os.Stat(ApprovalSnapshotPath(ws)); !os.IsNotExist(err) {
		t.Errorf("a rejected initial config must not record an approval (err=%v)", err)
	}
}

// Non-interactive initial launch of a workspace with packages/configuration is refused without the flag.
func TestCheckConfigChangesFreshWorkspaceNonTTYRefuses(t *testing.T) {
	ws := approvalWorkspace(t)
	ok, err := CheckConfigChanges(ws, decode(t, `{"packages": ["strace"]}`), false, false, nil)
	if ok {
		t.Fatal("non-tty fresh launch with packages must refuse")
	}
	var refusal *ChangedNonInteractiveError
	if !errors.As(err, &refusal) {
		t.Fatalf("want *ChangedNonInteractiveError, got %T: %v", err, err)
	}
}

// recordingPrompter captures the diff it was shown and answers with accept.
type recordingPrompter struct {
	accept bool
	called bool
	diff   []string
}

func (p *recordingPrompter) Prompt(diffLines []string) bool {
	p.called = true
	p.diff = diffLines
	return p.accept
}

// ---- helpers ----

func discard(string) {}

type yesPrompter struct{}

func (yesPrompter) Prompt([]string) bool { return true }

type noPrompter struct{}

func (noPrompter) Prompt([]string) bool { return false }

func assertPackages(t *testing.T, m *jsonx.OrderedMap, want ...string) {
	t.Helper()
	v, _ := m.Get("packages")
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("packages not a list: %T", v)
	}
	if len(list) != len(want) {
		t.Fatalf("packages = %v, want %v", list, want)
	}
	for i, w := range want {
		if list[i] != w {
			t.Errorf("packages[%d] = %v, want %s", i, list[i], w)
		}
	}
}
