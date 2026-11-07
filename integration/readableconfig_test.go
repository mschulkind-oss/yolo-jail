package integration

// readableconfig_test.go is the container tier of the readable config (docs/design/readable-config.md):
// a jail launched on TOML files, the named verbs that edit them, and what `yolo init` leaves alone.
// The unit tiers pin each reader and verb against fixtures; only a launch shows the pieces joined:
// what the loader read is what the container got, and what a verb wrote is what the next launch
// reads. Every test asserts what the JAIL or the FILES show, not what a message says.
//
// Fixtures write the user config as config.toml with writeUserTOML, and never leave a
// config.jsonc beside it (both files in one place is a refusal by design, RC-D19).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// writeUserTOML replaces the isolated home's user config with body, written as config.toml. The
// isolation seeds config.jsonc; it is removed so the home holds the new format alone.
func writeUserTOML(t *testing.T, body string) string {
	t.Helper()
	packHome(t, "{}")
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.Remove(filepath.Join(dir, paths.UserConfigLegacyJSONCName)); err != nil {
		t.Fatalf("removing the seeded config.jsonc: %v", err)
	}
	path := filepath.Join(dir, paths.UserConfigTOMLName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeWorkspaceTOML makes a workspace holding yolo-jail.toml with body and registers the
// container cleanup writeProject does.
func writeWorkspaceTOML(t *testing.T, body string) string {
	t.Helper()
	dir := writeProject(t, `{}`)
	if err := os.Remove(filepath.Join(dir, "yolo-jail.jsonc")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.WorkspaceConfigTOMLName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A jail launched on yolo-jail.toml and config.toml alone, with no old-format file anywhere:
// the TOML-declared package is on the jail's PATH, a bare mount path lands read-only at
// /ctx/<name>, and a writable request the user's [allow.mounts] permits is approved by
// --accept-config-changes (jailRunArgs carries it) and is writable from inside, the file
// appearing on the host. The launch must not offer a conversion, as nothing is in the old format.
func TestAJailLaunchesOnTOMLFiles(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "[allow.mounts]\non_request = [\"write\"]\n")
	roSrc, rwSrc := t.TempDir(), t.TempDir()
	dir := writeWorkspaceTOML(t,
		"mounts = [\n  '"+roSrc+"',\n  { host = '"+rwSrc+"', at = '/ctx/scratch', write = true },\n]\n\n"+
			"[install]\nnix = [\"hello\"]\n")

	script := `command -v hello && echo HELLO-ON-PATH
touch /ctx/scratch/from-jail && echo WRITABLE-OK
touch /ctx/` + filepath.Base(roSrc) + `/nope 2>/dev/null || echo READONLY-OK`
	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("a launch on TOML files must succeed: rc %d\n%s", r.rc, r.combined())
	}
	for _, want := range []string{"HELLO-ON-PATH", "WRITABLE-OK", "READONLY-OK"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the jail did not show %s:\n%s", want, r.combined())
		}
	}
	if _, err := os.Stat(filepath.Join(rwSrc, "from-jail")); err != nil {
		t.Errorf("the approved writable mount did not carry the jail's file to the host: %v", err)
	}
	if _, err := os.Stat(filepath.Join(roSrc, "nope")); err == nil {
		t.Errorf("the read-only mount let the jail write to the host")
	}
	if strings.Contains(r.combined(), "Deprecated config format") {
		t.Errorf("a launch on TOML files offered a conversion:\n%s", r.combined())
	}
}

// A writable mount request the user config does NOT allow is refused at launch, and the jail
// does not start: the ceiling is the user's, not the project's (RC-D24).
func TestAWritableMountRequestWithoutAllowRefusesTheLaunch(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "packs = []\n")
	rwSrc := t.TempDir()
	dir := writeWorkspaceTOML(t,
		"mounts = [{ host = '"+rwSrc+"', at = '/ctx/scratch', write = true }]\n")

	r := runYolo(t, dir, `touch /ctx/scratch/from-jail`)
	if r.rc == 0 {
		t.Fatalf("a writable request over a user config that does not allow it must refuse:\n%s", r.combined())
	}
	if _, err := os.Stat(filepath.Join(rwSrc, "from-jail")); err == nil {
		t.Errorf("the refused request still produced a writable mount")
	}
	if !strings.Contains(r.combined(), "allow.mounts") {
		t.Errorf("the refusal should name the user-config lines that would allow it:\n%s", r.combined())
	}
}

// `yolo config why` run INSIDE the jail reads the workspace's TOML and names the file that sets
// the key. It is also the proof that the jail holds the project's readable config at all.
func TestConfigWhyInsideAJailNamesTheTOMLFile(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "packs = []\n")
	dir := writeWorkspaceTOML(t, "[machine]\nresources = { memory = \"3g\" }\n")

	r := runYolo(t, dir, `yolo config why machine.resources.memory`)
	if r.rc != 0 {
		t.Fatalf("yolo config why in the jail: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, `"3g"`) || !strings.Contains(r.stdout, paths.WorkspaceConfigTOMLName) {
		t.Errorf("config why should print the value and the yolo-jail.toml that sets it:\n%s", r.stdout)
	}
}

// `yolo pack add` on the host edits the user config's list, and the next launch reads it: the
// launch's pack line carries the added pack and its blocked-tool shim is on the jail's PATH
// (guardrails refuses `grep -r`). The comment beside the existing entry stays where it was, as
// the verb changes the one item and nothing around it.
func TestPackAddOnTheHostIsSeenByTheNextLaunch(t *testing.T) {
	requireJail(t)

	cfg := writeUserTOML(t, "# my packs\npacks = [\"claude\"]  # the agent\n")
	dir := writeWorkspaceTOML(t, "")

	// Before: the pack is not selected, so grep -r is not blocked.
	before := runYolo(t, dir, `grep -r nothing-will-match /etc/hostname; echo GREP-RC=$?`)
	if before.rc != 0 || strings.Contains(before.combined(), "recursive mode is blocked") {
		t.Fatalf("grep -r should run before guardrails is added: rc %d\n%s", before.rc, before.combined())
	}

	r := runCommand(t, dir, []string{"pack", "add", "guardrails"}, withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("yolo pack add guardrails on the host: rc %d\n%s", r.rc, r.combined())
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"guardrails"`) || !strings.Contains(got, "# my packs") ||
		!strings.Contains(got, "# the agent") || !strings.Contains(got, `"claude"`) {
		t.Errorf("pack add should add the one item and keep the file's comments and other entries:\n%s", got)
	}

	after := runYolo(t, dir, `grep -r nothing-will-match /etc/hostname; echo GREP-RC=$?`)
	if !strings.Contains(after.combined(), "recursive mode is blocked") {
		t.Errorf("the launch after `pack add guardrails` did not select it (grep -r still runs):\n%s", after.combined())
	}
}

// `yolo init` in a git checkout writes the pointer file and leaves the project's tracked
// .gitignore and the clone's .git/info/exclude byte-for-byte as they were (config-project.md §2,
// C5; the owner, 2026-11-07). It writes no old-format file.
func TestInitLeavesTheGitignoreAlone(t *testing.T) {
	requireJail(t)

	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("no git to make a checkout: %v\n%s", err, out)
	}
	const ignore = "node_modules/\n*.log\n"
	gitignore := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignore, []byte(ignore), 0o644); err != nil {
		t.Fatal(err)
	}

	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	excludeBefore, _ := os.ReadFile(excludePath)
	r := runCommand(t, dir, []string{"init"}, withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("yolo init: rc %d\n%s", r.rc, r.combined())
	}
	after, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != ignore {
		t.Errorf("yolo init changed the project's .gitignore:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(dir, paths.WorkspaceConfigTOMLName)); err != nil {
		t.Errorf("yolo init wrote no yolo-jail.toml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "yolo-jail.jsonc")); err == nil {
		t.Errorf("yolo init wrote an old-format yolo-jail.jsonc")
	}
	if exclude, _ := os.ReadFile(excludePath); string(exclude) != string(excludeBefore) {
		t.Errorf("yolo init wrote .git/info/exclude:\nbefore %q\nafter  %q", excludeBefore, exclude)
	}
}

// A TOML [env] set variable reaches the jail's environment.
func TestATOMLEnvSetReachesTheJail(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "packs = []\n")
	dir := writeWorkspaceTOML(t, "[env]\nset = { READABLE_CONFIG_PROBE = \"from-toml\" }\n")
	r := runYolo(t, dir, `echo PROBE=$READABLE_CONFIG_PROBE`)
	if r.rc != 0 || !strings.Contains(r.stdout, "PROBE=from-toml") {
		t.Errorf("env.set did not reach the jail: rc %d\n%s", r.rc, r.combined())
	}
}

// active_provider in the user config.toml, including the reserved "native", is accepted by a
// launch, and the launch log records the agent as left to its own login.
func TestActiveProviderNativeInTOMLLaunches(t *testing.T) {
	requireJail(t)

	writeUserTOML(t, "packs = [\"claude\"]\n[active_provider]\nclaude = \"native\"\n")
	dir := writeWorkspaceTOML(t, "")
	r := runYolo(t, dir, `true`)
	if r.rc != 0 {
		t.Fatalf("a launch on active_provider = native must succeed: rc %d\n%s", r.rc, r.combined())
	}
	// The profile record is the launch log's (run.ProfileRecord), not the terminal's.
	logBody, err := os.ReadFile(filepath.Join(dir, ".yolo", "launch.log"))
	if err != nil {
		t.Fatalf("read the launch log: %v", err)
	}
	if !strings.Contains(string(logBody), "claude=none (config active_provider.claude)") {
		t.Errorf("native should leave claude with no profile, selected by active_provider.claude:\n%s", logBody)
	}
}
