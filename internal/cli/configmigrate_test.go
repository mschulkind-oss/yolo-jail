package cli

// configmigrate_test.go drives `yolo config migrate` and `yolo pack migrate` through their
// dispatchers (configRunW, packMain), so each fails if the verb's entry is removed, and pins
// RC-D21's "no command writes convert_later" through the config edit verbs.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

type migrateEnv struct {
	home, userDir, ws string
}

func newMigrateEnv(t *testing.T) migrateEnv {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Chdir(ws)
	e := migrateEnv{home: home, userDir: filepath.Join(home, ".config", "yolo-jail"), ws: ws}
	if err := os.MkdirAll(e.userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

func writeMigrateFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func present(path string) bool { _, err := os.Lstat(path); return err == nil }

func runConfigMigrateVerb(t *testing.T, args ...string) (rc int, stdout, stderr string) {
	t.Helper()
	interactive := configMigrateInteractive
	configMigrateInteractive = func() bool { return false }
	t.Cleanup(func() { configMigrateInteractive = interactive })
	var out, errw bytes.Buffer
	rc = configRunW(append([]string{"migrate"}, args...), &out, &errw)
	return rc, out.String(), errw.String()
}

func TestConfigMigrateConvertsUserAndProjectOnTheHost(t *testing.T) {
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), "{\n  // mine\n  \"packs\": []\n}\n")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), "{\n  \"network\": {\"mode\": \"bridge\"}\n}\n")

	rc, out, errw := runConfigMigrateVerb(t, "--dry-run")
	if rc != 0 || !strings.Contains(out, "Found 2 files in the old format") || !strings.Contains(out, "Dry run; nothing was written.") {
		t.Fatalf("--dry-run: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	if present(filepath.Join(e.userDir, "config.toml")) {
		t.Fatal("--dry-run wrote a file")
	}

	rc, _, errw = runConfigMigrateVerb(t)
	if rc != 1 || !strings.Contains(errw, "`yolo config migrate --yes`") || present(filepath.Join(e.userDir, "config.toml")) {
		t.Fatalf("no terminal and no --yes must write nothing and name --yes: rc=%d\nstderr:\n%s", rc, errw)
	}

	rc, out, errw = runConfigMigrateVerb(t, "--yes")
	if rc != 0 {
		t.Fatalf("--yes: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	for _, p := range []string{filepath.Join(e.userDir, "config.toml"), filepath.Join(e.userDir, "config.jsonc.pre-migrate"),
		filepath.Join(e.ws, "yolo-jail.toml"), filepath.Join(e.ws, "yolo-jail.jsonc.pre-migrate")} {
		if !present(p) {
			t.Errorf("%s was not written\nstdout:\n%s", p, out)
		}
	}
	if !strings.Contains(out, "Next: `yolo check`") {
		t.Errorf("no next step:\n%s", out)
	}
	// The converted-config warning lasts only as long as the user config's backup, and the
	// migration says so (pack-naming second review, minor 1).
	if !strings.Contains(out, "While the user config's .pre-migrate copy is kept") ||
		!strings.Contains(out, "once it is deleted, they no longer do") {
		t.Errorf("the migration does not say the rename report lasts only while the backup is kept:\n%s", out)
	}
}

// The verb says, on --dry-run and on --yes, the line that would keep a renamed local file out of
// commits, and writes no ignore file either way (RC-D71).
func TestConfigMigrateNamesTheIgnoreLineAndWritesNoIgnoreFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	e := newMigrateEnv(t)
	t.Setenv("XDG_CONFIG_HOME", e.home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(e.ws))
	if out, err := exec.Command("git", "-C", e.ws, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeMigrateFile(t, filepath.Join(e.ws, ".gitignore"), "yolo-jail.local.jsonc\n")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.local.jsonc"), "{\n  \"network\": {\"mode\": \"bridge\"}\n}\n")
	exclude := filepath.Join(e.ws, ".git", "info", "exclude")
	before, _ := os.ReadFile(exclude)

	line := "yolo-jail.local.toml is not ignored by git as yolo-jail.local.jsonc was; nothing was added to any ignore file. " +
		"To keep it out of commits, add the line `/yolo-jail.local.toml` to .gitignore or .git/info/exclude."
	for _, flag := range []string{"--dry-run", "--yes"} {
		rc, out, errw := runConfigMigrateVerb(t, flag)
		if rc != 0 || !strings.Contains(out, line) {
			t.Fatalf("%s: rc=%d\nstdout:\n%s\nstderr:\n%s", flag, rc, out, errw)
		}
		if after, _ := os.ReadFile(exclude); !bytes.Equal(after, before) {
			t.Fatalf("%s wrote info/exclude: %q", flag, after)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(e.ws, ".gitignore")); string(data) != "yolo-jail.local.jsonc\n" {
		t.Fatalf(".gitignore changed: %q", data)
	}
}

// After `yolo config migrate --yes` and a `yolo config set` in a git work tree, `git status`
// shows only what the migration is for: the tracked old file gone, the new files and the backups.
// No lock file is left beside any config file (RC-D34), and no ignore file is written (RC-D71):
// the closing lines name every change, the ignore line to add, and say the backups can go.
func TestConfigMigrateAndSetLeaveOnlyTheirChangesInGitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	e := newMigrateEnv(t)
	t.Setenv("XDG_CONFIG_HOME", e.home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(e.ws))
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", e.ws}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	writeMigrateFile(t, filepath.Join(e.ws, ".gitignore"), "yolo-jail.local.jsonc\n")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), "{\n  \"network\": {\"mode\": \"bridge\"}\n}\n")
	git("add", ".gitignore", "yolo-jail.jsonc")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.local.jsonc"), "{\n  \"mise_tools\": {\"lua\": \"5.4.9\"}\n}\n")

	rc, out, errw := runConfigMigrateVerb(t, "--yes")
	if rc != 0 {
		t.Fatalf("--yes: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	// The private file's new name and backup show as untracked: the migration writes no ignore
	// file (RC-D71), and names the line to add instead.
	want := " D yolo-jail.jsonc\n?? yolo-jail.jsonc.pre-migrate\n?? yolo-jail.local.jsonc.pre-migrate\n?? yolo-jail.local.toml\n?? yolo-jail.toml\n"
	if got := git("status", "--porcelain", "--untracked-files=all"); got != want {
		t.Fatalf("git status after migrate:\n%s\nwant:\n%s\nstdout:\n%s", got, want, out)
	}
	if strings.Contains(out, "no tracked file was touched") || strings.Contains(out, "Nothing else on this machine changed") {
		t.Errorf("the closing line claims nothing else changed:\n%s", out)
	}
	for _, line := range []string{
		"Kept the originals as yolo-jail.jsonc.pre-migrate, yolo-jail.local.jsonc.pre-migrate; delete them once the new files work for you.",
		"To keep it out of commits, add the line `/yolo-jail.local.jsonc.pre-migrate` to .gitignore or .git/info/exclude.",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("stdout lacks %q:\n%s", line, out)
		}
	}

	var setOut, setErr bytes.Buffer
	// The edit is written before the post-write check runs; that check's verdict depends on the
	// machine, so only its agreement with rc is asserted, as every public edit test does.
	rc = configRunW([]string{"set", "machine.resources.cpus", "4", "--project"}, &setOut, &setErr)
	assertPublicPostWriteCheck(t, rc, setOut.String(), setErr.String())
	if got, _ := os.ReadFile(filepath.Join(e.ws, "yolo-jail.toml")); !strings.Contains(string(got), "cpus = 4") {
		t.Fatalf("config set did not write yolo-jail.toml: %q\nstderr:\n%s", got, setErr.String())
	}
	if got := git("status", "--porcelain", "--untracked-files=all"); got != want {
		t.Fatalf("git status after config set:\n%s\nwant:\n%s", got, want)
	}
}

func TestConfigMigrateInJailTouchesOnlyProjectFiles(t *testing.T) {
	e := newMigrateEnv(t)
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	t.Setenv("YOLO_WORKSPACE", e.ws)
	userOld := "{\n  \"packs\": []\n}\n"
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), userOld)
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), "{\n  \"network\": {\"mode\": \"bridge\"}\n}\n")

	rc, out, errw := runConfigMigrateVerb(t, "--yes")
	if rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	if !present(filepath.Join(e.ws, "yolo-jail.toml")) {
		t.Fatalf("the project file was not converted\nstdout:\n%s", out)
	}
	if present(filepath.Join(e.userDir, "config.toml")) || present(filepath.Join(e.userDir, "config.jsonc.pre-migrate")) {
		t.Fatal("a migration in a jail touched the user config")
	}
	if data, _ := os.ReadFile(filepath.Join(e.userDir, "config.jsonc")); string(data) != userOld {
		t.Fatalf("the user config changed: %q", data)
	}
	if !strings.Contains(errw, "Run `yolo config migrate` on the host") {
		t.Errorf("in-jail run does not say where the user config migrates:\n%s", errw)
	}
}

// A user-scope `brokered` block has no TOML spelling in this release (W1-L1's refusal): the verb
// shows the exact difference, writes nothing, and exits non-zero.
func TestConfigMigrateShowsTheLibrarysRefusal(t *testing.T) {
	e := newMigrateEnv(t)
	old := "{\n  \"brokered\": {\"git\": {\"grants\": [{\"kind\": \"read\", \"repo\": \"*\"}]}}\n}\n"
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), old)
	rc, out, errw := runConfigMigrateVerb(t, "--yes")
	if rc != 1 {
		t.Fatalf("rc=%d, want 1\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	if !strings.Contains(out, "brokered") || !strings.Contains(out, "nothing was written") {
		t.Errorf("the refusal does not show the difference:\n%s", out)
	}
	if present(filepath.Join(e.userDir, "config.toml")) {
		t.Fatal("an unequal plan wrote a file")
	}
}

// Both formats in one place are refused by the reader, naming both files; the migration does
// not pick one either.
func TestBothFormatsInOnePlaceRefused(t *testing.T) {
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), "{\n  \"packages\": [\"just\"]\n}\n")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.toml"), "")
	if _, err := config.LoadConfig(e.ws, true, func(string) {}); err == nil ||
		!strings.Contains(err.Error(), "yolo-jail.jsonc") || !strings.Contains(err.Error(), "yolo-jail.toml") {
		t.Fatalf("the reader did not refuse both formats naming both: %v", err)
	}
	rc, out, errw := runConfigMigrateVerb(t, "--yes")
	if rc == 0 {
		t.Fatalf("migrate accepted both formats in one place\nstdout:\n%s\nstderr:\n%s", out, errw)
	}
	if data, _ := os.ReadFile(filepath.Join(e.ws, "yolo-jail.toml")); len(data) != 0 {
		t.Fatalf("migrate wrote over the TOML file: %q", data)
	}
}

func TestConfigSetRefusesConvertLater(t *testing.T) {
	e := newMigrateEnv(t)
	userPath := filepath.Join(e.userDir, "config.jsonc")
	old := "{\n  \"packs\": []\n}\n"
	writeMigrateFile(t, userPath, old)
	for _, verb := range []string{"set", "add"} {
		var out, errw bytes.Buffer
		rc := configRunW([]string{verb, "convert_later", "true", "--user"}, &out, &errw)
		if rc != 1 {
			t.Fatalf("%s: rc=%d, want 1\nstderr:\n%s", verb, rc, errw.String())
		}
		for _, want := range []string{"written by hand", "nothing was written", `"convert_later": true to ` + userPath, "`yolo config migrate`"} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("%s refusal lacks %q:\n%s", verb, want, errw.String())
			}
		}
	}
	if data, _ := os.ReadFile(userPath); string(data) != old {
		t.Fatalf("the user config changed: %q", data)
	}
}

const packMigrateFixture = `{
  // the pack's name
  "name": "fixture-pack",
  "contributes": [
    { "kind": "files", "from": "bin/finder.sh", "into": ".claude/bin/finder.sh" }
  ]
}
`

func writeMigratePack(t *testing.T, manifest string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "fixture-pack")
	writeMigrateFile(t, filepath.Join(dir, "pack.json"), manifest)
	writeMigrateFile(t, filepath.Join(dir, "bin", "finder.sh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "bin", "finder.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runPackMigrateVerb(t *testing.T, args ...string) (rc int, stdout, stderr string) {
	t.Helper()
	interactive := packMigrateInteractive
	packMigrateInteractive = func() bool { return false }
	t.Cleanup(func() { packMigrateInteractive = interactive })
	var out, errw bytes.Buffer
	rc = packMain(append([]string{"migrate"}, args...), &out, &errw, false)
	return rc, out.String(), errw.String()
}

func TestPackMigrateRewritesTheManifestAndMovesFiles(t *testing.T) {
	newMigrateEnv(t)
	dir := writeMigratePack(t, packMigrateFixture)

	rc, out, errw := runPackMigrateVerb(t, dir, "--dry-run")
	if rc != 0 || !strings.Contains(out, "bin/finder.sh → home/.claude/bin/finder.sh") || present(filepath.Join(dir, "pack.toml")) {
		t.Fatalf("--dry-run: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	rc, _, errw = runPackMigrateVerb(t, dir)
	if rc != 1 || !strings.Contains(errw, "--yes") || present(filepath.Join(dir, "pack.toml")) {
		t.Fatalf("no terminal and no --yes must change nothing: rc=%d\nstderr:\n%s", rc, errw)
	}
	rc, out, errw = runPackMigrateVerb(t, dir, "--yes")
	if rc != 0 {
		t.Fatalf("--yes: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	if !present(filepath.Join(dir, "pack.toml")) || present(filepath.Join(dir, "pack.json")) ||
		!present(filepath.Join(dir, "home", ".claude", "bin", "finder.sh")) {
		t.Fatalf("the pack was not migrated\nstdout:\n%s", out)
	}
	if !strings.Contains(out, "`yolo pack lint "+dir+"`") {
		t.Errorf("no next step:\n%s", out)
	}
}

// A JSON null that still has no grouped TOML spelling (W1-L3's refusal; a config-overlay's
// managed null, unlike a provider option's or a surface row's, has none): the verb says so,
// names keeping the legacy manifest, and changes nothing.
func TestPackMigrateRefusesAnUnrepresentableNull(t *testing.T) {
	newMigrateEnv(t)
	dir := writeMigratePack(t, `{"contributes":[{"kind":"config-overlay","surface":"x/settings","config":{"managed":{"theme":null}}}]}`)
	rc, out, errw := runPackMigrateVerb(t, dir, "--yes")
	if rc != 1 || !strings.Contains(errw, "theme") || !strings.Contains(errw, "keep this pack on its legacy manifest") {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
	}
	if present(filepath.Join(dir, "pack.toml")) || !present(filepath.Join(dir, "pack.json")) {
		t.Fatal("a refused migration changed the pack")
	}
}

// TestConfigMigrateDropsWrittenDefaultsAndKeepsTheProof pins OQ-RC18's dropping of written
// defaults through the verb: a default written once is left out, --dry-run says so, the comment
// above it stays, and a non-default value is kept. It fails if the migration stops dropping, or
// stops proving the result equal.
func TestConfigMigrateDropsWrittenDefaultsAndKeepsTheProof(t *testing.T) {
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"),
		"{\n  // timing stays off\n  \"perf_logging\": false,\n  \"update_check\": false,\n  \"network\": {\"mode\": \"bridge\"}\n}\n")

	interactive := configMigrateInteractive
	configMigrateInteractive = func() bool { return false }
	t.Cleanup(func() { configMigrateInteractive = interactive })
	if rc := registry["config"]([]string{"config", "migrate", "--dry-run"}); rc != 0 {
		t.Fatalf("registry config migrate --dry-run rc=%d", rc)
	}
	rc, dry, errs := runConfigMigrateVerb(t, "--dry-run")
	if rc != 0 {
		t.Fatalf("--dry-run: rc=%d\n%s\n%s", rc, dry, errs)
	}
	for _, want := range []string{`"network.mode": "bridge" dropped: it is the default`,
		`"perf_logging": false dropped: it is the default`, "identical"} {
		if !strings.Contains(dry, want) {
			t.Errorf("--dry-run lacks %q:\n%s", want, dry)
		}
	}
	if strings.Contains(dry, "update_check") {
		t.Errorf("update_check=false is not the default and must not be dropped:\n%s", dry)
	}
	if present(filepath.Join(e.userDir, "config.toml")) {
		t.Fatal("--dry-run wrote a file")
	}

	if rc, out, errs := runConfigMigrateVerb(t, "--yes"); rc != 0 {
		t.Fatalf("--yes: rc=%d\n%s\n%s", rc, out, errs)
	}
	data, err := os.ReadFile(filepath.Join(e.userDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "network") || strings.Contains(got, "perf_logging") {
		t.Errorf("a written default survived:\n%s", got)
	}
	if !strings.Contains(got, "update_check = false") || !strings.Contains(got, "# timing stays off") {
		t.Errorf("the non-default value or the comment above a dropped key was lost:\n%s", got)
	}
}

// TestConfigMigrateKeepsADefaultTwoFilesWrite pins the single-writer rule: a project's bridge over
// a user's host is a decision, not a default, so it is kept and the proof still holds.
func TestConfigMigrateKeepsADefaultTwoFilesWrite(t *testing.T) {
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.userDir, "config.jsonc"), "{\n  \"network\": {\"mode\": \"host\"}\n}\n")
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), "{\n  \"network\": {\"mode\": \"bridge\"}\n}\n")
	rc, out, errs := runConfigMigrateVerb(t, "--yes")
	if rc != 0 || strings.Contains(out, "dropped") {
		t.Fatalf("rc=%d\n%s\n%s", rc, out, errs)
	}
	data, err := os.ReadFile(filepath.Join(e.ws, "yolo-jail.toml"))
	if err != nil || !strings.Contains(string(data), `mode = "bridge"`) {
		t.Fatalf("the project's bridge was dropped: %v\n%s", err, data)
	}
}

// TestConfigMigrateKeepsTheShapesTheLegacyReaderAccepts pins RC-D31 through the verb: a key the
// legacy reader takes in a shape the readable schema types differently (an MCP server's command
// as one string, a pids limit as a boolean) converts and reads back equal, instead of the
// reader refusing the file the migration just wrote. It fails if either spelling is refused
// again, or if the converted value changes type to get past the reader.
func TestConfigMigrateKeepsTheShapesTheLegacyReaderAccepts(t *testing.T) {
	for _, tc := range []struct{ name, old, keeps string }{
		{"mcp command as a string", `{"mcp_servers":{"tavily":{"command":"npx","args":["-y","tavily-mcp"],"env":{"K":"v"}}}}`, `command = "npx"`},
		{"pids limit as a boolean", `{"resources":{"pids_limit":true}}`, `pids_limit = true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newMigrateEnv(t)
			writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.jsonc"), tc.old)
			if rc, out, errw := runConfigMigrateVerb(t, "--dry-run"); rc != 0 {
				t.Fatalf("--dry-run: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
			}
			if rc, out, errw := runConfigMigrateVerb(t, "--yes"); rc != 0 {
				t.Fatalf("--yes: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out, errw)
			}
			data, err := os.ReadFile(filepath.Join(e.ws, "yolo-jail.toml"))
			if err != nil || !strings.Contains(string(data), tc.keeps) {
				t.Fatalf("the converted file lost %q: %v\n%s", tc.keeps, err, data)
			}
		})
	}
}
