package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// darwinbootlog_test.go pins the macos-user bootstrap's boot log: the container's
// <workspace>/.yolo/boot.log, kept by RunDarwinBootstrap. Every test drives the production
// translation (DarwinEnvFrom) and the production entry (RunDarwinBootstrap).

// resolvedDir is t.TempDir with its symbolic links resolved where the path is MINTED, so a
// comparison against code that resolves them holds on darwin too (AGENTS.md, the darwin
// PATH-RESOLUTION class).
func resolvedDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// darwinBootEnv is the bootstrap Env a launch builds for workspace ws, plus extra variables.
func darwinBootEnv(t *testing.T, home, ws string, extra map[string]string) *Env {
	t.Helper()
	vars := map[string]string{"JAIL_HOME": home, "YOLO_DARWIN_WORKSPACE": ws}
	for k, v := range extra {
		vars[k] = v
	}
	return DarwinEnvFrom(vars, home)
}

// The bootstrap keeps the container's log: its header says what booted, a terminal warning
// lands in both sinks, a log-only note in the log alone, and the last line says how it ended.
// And the writers it installed are taken off the Env again once the log is closed.
//
// MUTATION: delete the attachDarwinBootLog call in RunDarwinBootstrap and this goes red at the
// read of boot.log.
func TestTheDarwinBootstrapKeepsABootLog(t *testing.T) {
	home, ws := resolvedDir(t), resolvedDir(t)
	e := darwinBootEnv(t, home, ws, map[string]string{"YOLO_MCP_PRESETS": `["chrome-devtools"]`})
	var term strings.Builder
	e.Stderr = &term

	if err := RunDarwinBootstrap(e, DarwinBootstrapOptions{Version: "0.12.0+3.gabc"}); err != nil {
		t.Fatalf("bootstrap failed: %v\n%s", err, term.String())
	}
	raw, err := os.ReadFile(BootLogPath(ws))
	if err != nil {
		t.Fatalf("the macos-user bootstrap kept no boot log: %v\n%s", err, term.String())
	}
	log := string(raw)
	if !strings.HasPrefix(log, "=== yolo entrypoint ") ||
		!strings.Contains(log, "\n  macos-user bootstrap, yolo 0.12.0+3.gabc\n") {
		t.Errorf("boot.log's header does not say what booted:\n%s", log)
	}
	const warning = "mcp_presets are not delivered on macos-user"
	if !strings.Contains(log, warning) || !strings.Contains(term.String(), warning) {
		t.Errorf("a terminal warning must reach both sinks\nterminal:\n%s\nlog:\n%s", term.String(), log)
	}
	note := "durable dir: none this launch ($" + durable.EnvVar + " unset)"
	if !strings.Contains(log, note) || strings.Contains(term.String(), note) {
		t.Errorf("a log-only note must reach the log alone\nterminal:\n%s\nlog:\n%s", term.String(), log)
	}
	if !strings.HasSuffix(log, "=== boot complete, handing over ===\n") {
		t.Errorf("boot.log does not record how the bootstrap ended:\n%s", log)
	}
	if e.Stderr != &term || e.LogOnly != nil {
		t.Errorf("RunDarwinBootstrap left its closed log installed on the Env")
	}
}

// An unstamped binary says so, rather than leaving the line out.
func TestTheDarwinBootLogNamesAnUnstampedBinary(t *testing.T) {
	home, ws := resolvedDir(t), resolvedDir(t)
	e := darwinBootEnv(t, home, ws, nil)
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{})
	raw, err := os.ReadFile(BootLogPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\n  macos-user bootstrap, yolo unstamped\n") {
		t.Errorf("boot.log's header does not say the binary is unstamped:\n%s", raw)
	}
}

// A second bootstrap of the same workspace rotates the first log to boot.log.prev, so a
// relaunch after a failure keeps the failure's evidence.
func TestTheDarwinBootstrapRotatesItsBootLog(t *testing.T) {
	home, ws := resolvedDir(t), resolvedDir(t)
	for _, v := range []string{"first", "second"} {
		e := darwinBootEnv(t, home, ws, nil)
		e.Stderr = &strings.Builder{}
		_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{Version: v})
	}
	cur, err := os.ReadFile(BootLogPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	prev, err := os.ReadFile(filepath.Join(paths.WorkspaceStateDir(ws), bootLogPrevName))
	if err != nil {
		t.Fatalf("the first boot's log was not kept as %s: %v", bootLogPrevName, err)
	}
	if !strings.Contains(string(cur), "yolo second\n") || !strings.Contains(string(prev), "yolo first\n") {
		t.Errorf("rotation kept the wrong logs\n%s:\n%s\n%s:\n%s", bootLogName, cur, bootLogPrevName, prev)
	}
}

// A REFUSED bootstrap is the case the log exists for: the refusal is its last line.
func TestTheDarwinBootstrapLogsItsRefusal(t *testing.T) {
	home, ws := resolvedDir(t), resolvedDir(t)
	// A FILE where the generated-bin anchor goes: the shim and launcher generators fail,
	// which is a refusal (A12).
	if err := os.WriteFile(filepath.Join(home, ".yolo"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e := darwinBootEnv(t, home, ws, nil)
	e.Stderr = &strings.Builder{}
	if err := RunDarwinBootstrap(e, DarwinBootstrapOptions{}); err == nil {
		t.Fatal("the fixture did not make the bootstrap refuse")
	}
	raw, err := os.ReadFile(BootLogPath(ws))
	if err != nil {
		t.Fatalf("a refused bootstrap kept no boot log: %v", err)
	}
	if !strings.Contains(string(raw), "\n=== BOOT REFUSED: ") {
		t.Errorf("boot.log does not record the refusal:\n%s", raw)
	}
}

// An Env the launch did not name a workspace for writes no boot log. Its WorkspaceDir is the
// container's literal /workspace — in a jail, the LIVE jail's workspace, whose boot.log a test
// would otherwise rotate away.
func TestTheDarwinBootstrapWritesNoBootLogWithoutAWorkspace(t *testing.T) {
	home := resolvedDir(t)
	e := DarwinEnvFrom(map[string]string{"JAIL_HOME": home}, home)
	var term strings.Builder
	e.Stderr = &term
	if bl := attachDarwinBootLog(e, ""); bl != nil {
		bl.finish(nil)
		t.Fatalf("attached a boot log under %s for an Env naming no workspace", e.WorkspaceDir())
	}
	if e.LogOnly != nil || e.Stderr != &term {
		t.Errorf("rewired the Env's writers with no log to write")
	}
}

// The bootstrap writes as the sandbox account OUTSIDE Seatbelt, so a link the agent left at
// either log name must not carry the write anywhere else. Both are replaced by regular files.
func TestTheDarwinBootLogFollowsNoLinkTheAgentLeft(t *testing.T) {
	home, ws, elsewhere := resolvedDir(t), resolvedDir(t), resolvedDir(t)
	target := filepath.Join(elsewhere, "protected")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := paths.WorkspaceStateDir(ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{bootLogName, bootLogPrevName} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	e := darwinBootEnv(t, home, ws, nil)
	e.Stderr = &strings.Builder{}
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{})
	if got, _ := os.ReadFile(target); string(got) != "keep\n" {
		t.Errorf("the boot log was written through a link onto %s:\n%s", target, got)
	}
	if fi, err := os.Lstat(BootLogPath(ws)); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("boot.log is not a fresh regular file (err=%v)", err)
	}
}

// A `.yolo` that is itself a link is refused outright: nothing is written beneath it, and the
// bootstrap still runs on plain stderr.
//
// RED ON A PLAIN-PATH OPEN: os.OpenFile(<ws>/.yolo/boot.log) follows the linked directory
// and creates the log in it.
func TestTheDarwinBootLogRefusesALinkedStateDir(t *testing.T) {
	home, ws, elsewhere := resolvedDir(t), resolvedDir(t), resolvedDir(t)
	if err := os.Symlink(elsewhere, paths.WorkspaceStateDir(ws)); err != nil {
		t.Fatal(err)
	}
	e := darwinBootEnv(t, home, ws, map[string]string{"YOLO_MCP_PRESETS": `["chrome-devtools"]`})
	var term strings.Builder
	e.Stderr = &term
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{})
	for _, name := range []string{bootLogName, bootLogPrevName} {
		if _, err := os.Lstat(filepath.Join(elsewhere, name)); err == nil {
			t.Errorf("%s was written through a linked .yolo into %s", name, elsewhere)
		}
	}
	if !strings.Contains(term.String(), "mcp_presets are not delivered on macos-user") {
		t.Errorf("with no log to write, the terminal lost the bootstrap's lines:\n%s", term.String())
	}
}

// The container boot's log moved to the same beneath-a-root writer, so its own linked-`.yolo`
// case holds too: attachBootLog itself refuses, and leaves stderr as handed in.
func TestTheContainerBootLogRefusesALinkedStateDir(t *testing.T) {
	ws, elsewhere := resolvedDir(t), resolvedDir(t)
	if err := os.Symlink(elsewhere, paths.WorkspaceStateDir(ws)); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": resolvedDir(t), "YOLO_WORKSPACE": ws})
	var term strings.Builder
	if bl := attachBootLog(e, &term); bl != nil {
		bl.finish(nil)
		t.Errorf("attached a boot log beneath a linked .yolo")
	}
	if e.Stderr != &term || e.LogOnly != nil {
		t.Errorf("a refused attach rewired the Env's writers")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote %d entries through the linked .yolo into %s", len(entries), elsewhere)
	}
}

// THE LEAF-LINK PROTECTION ON ITS OWN, where the rotation cannot help. With a non-empty
// directory at boot.log.prev the rename aside fails, so the link the agent left at boot.log is
// still there when the log is opened, and only the beneath-a-root open
// (paths.OpenWorkspaceStateFile) keeps the write off its target: the link is replaced by a
// fresh regular file and what it pointed at is untouched.
//
// MUTATION M9 (the reviewer's): replace that open with a plain
// os.OpenFile(filepath.Join(dir, name), O_CREATE|O_WRONLY|O_TRUNC, 0o644) and the outside file
// is overwritten with the boot log. TestTheDarwinBootLogFollowsNoLinkTheAgentLeft cannot catch
// it, because there the rename moves the link aside first.
func TestTheDarwinBootLogWritesNoLinkWhenItsRotationFails(t *testing.T) {
	home, ws, elsewhere := resolvedDir(t), resolvedDir(t), resolvedDir(t)
	target := filepath.Join(elsewhere, "protected")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := paths.WorkspaceStateDir(ws)
	prev := filepath.Join(dir, bootLogPrevName)
	if err := os.MkdirAll(filepath.Join(prev, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, bootLogName)); err != nil {
		t.Fatal(err)
	}
	e := darwinBootEnv(t, home, ws, nil)
	e.Stderr = &strings.Builder{}
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{})
	if got, _ := os.ReadFile(target); string(got) != "keep\n" {
		t.Errorf("the boot log was written through the link at %s onto %s:\n%s", bootLogName, target, got)
	}
	fi, err := os.Lstat(BootLogPath(ws))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("boot.log is not a fresh regular file (err=%v)", err)
	}
	if raw, _ := os.ReadFile(BootLogPath(ws)); !strings.Contains(string(raw), "macos-user bootstrap") {
		t.Errorf("boot.log is not this bootstrap's log:\n%s", raw)
	}
	if fi, err := os.Stat(filepath.Join(prev, "occupied")); err != nil || !fi.IsDir() {
		t.Errorf("the directory at %s was disturbed (err=%v)", bootLogPrevName, err)
	}
}

// The beneath-a-root open is rooted at `.yolo` and FOLLOWS the components above it, which are
// the workspace's own path: a workspace reached through a symbolic link above it (on a Mac,
// /var -> /private/var holds every t.TempDir) keeps its log. The macos-user launcher resolves
// the workspace before it hands it over (macosuser's resolvePathAbs), so this is the open's
// own property rather than a case a launch produces.
func TestTheDarwinBootLogFollowsALinkAboveTheStateDir(t *testing.T) {
	real, links := resolvedDir(t), resolvedDir(t)
	if err := os.MkdirAll(filepath.Join(real, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	via := filepath.Join(links, "via")
	if err := os.Symlink(real, via); err != nil {
		t.Fatal(err)
	}
	e := darwinBootEnv(t, resolvedDir(t), filepath.Join(via, "proj"), nil)
	e.Stderr = &strings.Builder{}
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{Version: "via-link"})
	raw, err := os.ReadFile(BootLogPath(filepath.Join(real, "proj")))
	if err != nil {
		t.Fatalf("no boot log for a workspace reached through a link above .yolo: %v", err)
	}
	if !strings.Contains(string(raw), "macos-user bootstrap, yolo via-link\n") {
		t.Errorf("boot.log is not this bootstrap's log:\n%s", raw)
	}
}
