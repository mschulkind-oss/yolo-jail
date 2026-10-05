package cli

// applyhostlspplugin_test.go pins Claude's yolo-lsp plugin at the host notch at the COMMAND level:
// `yolo host apply` writes the plugin a launch stages, keeps it in sync, retires it when the table
// or the destination goes, never mistakes it for a dropped pack's output, and leaves alone a
// `yolo-lsp` it did not write.
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it, as applyhostmcp_test.go's
// fixture does. The real $HOME is never read or written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// lspHostServers is the lsp_servers table every fixture here configures.
const lspHostServers = `{"gopls":{"command":"gopls","fileExtensions":{".go":"go"}}}`

// lspHostFixture points a throwaway $HOME at a user config selecting packs, with lspServers as its
// lsp_servers ("" for none), and returns the home.
func lspHostFixture(t *testing.T, packs, lspServers string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeLSPHostConfig(t, home, packs, lspServers)
	// The shipped packs declare their agent CLIs, and a missing declared binary refuses a writing
	// apply; whether this machine has them is not the subject here (hostdepstub_test.go).
	stubDeclaredBins(t)
	return home
}

// writeLSPHostConfig rewrites the user config, for a test that changes it between applies.
func writeLSPHostConfig(t *testing.T, home, packs, lspServers string) {
	t.Helper()
	cfg := `{"packs":[` + packs + `]`
	if lspServers != "" {
		cfg += `,"lsp_servers":` + lspServers
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg+`}`)
}

// lspPluginManifest is where the plugin's manifest lands under one skills destination.
func lspPluginManifest(home, skillsDir string) string {
	return filepath.Join(home, filepath.FromSlash(skillsDir), jailcontent.LSPPluginDir,
		filepath.FromSlash(jailcontent.LSPPluginManifestRel))
}

// renderedLSPPlugin is the renderer's own bytes for a table, the file the host must hold.
func renderedLSPPlugin(t *testing.T, table string) string {
	t.Helper()
	v, err := jsonx.Decode([]byte(table))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := v.(*jsonx.OrderedMap)
	data, ok := jailcontent.RenderLSPPlugin(m)
	if !ok {
		t.Fatalf("the fixture table renders nothing: %s", table)
	}
	return string(data)
}

// THE GAP, end to end: `--assert` writes Claude's plugin into ~/.claude/skills, the very bytes a
// launch stages. Fails with the applyHostLSPPlugin call deleted from applyHostSkills.
func TestHostApplyWritesClaudesLSPPlugin(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(lspPluginManifest(home, ".claude/skills"))
	if err != nil {
		t.Fatalf("no yolo-lsp plugin at ~/.claude/skills — Claude at the host gets no language "+
			"server: %v\n%s", err, report)
	}
	if want := renderedLSPPlugin(t, lspHostServers); string(got) != want {
		t.Errorf("the host's plugin is not the renderer's bytes:\n--- host\n%s\n--- rendered\n%s", got, want)
	}
	if !strings.Contains(string(got), `"x-yolo-managed-by"`) {
		t.Errorf("the plugin carries no ownership marker, so the adoption walk would offer to "+
			"migrate it:\n%s", got)
	}
}

// NO RETIRE PROMPT, on the apply that writes the plugin or the one after. The plugin carries the
// marker a namespaced pack subtree does, so read by the dropped-pack scan it was a pack named
// `yolo-lsp` that the config no longer selects: every apply asked to retire it, and the launch
// gate saw a decision pending. Fails with the applyhostprune.go skip deleted.
func TestHostApplyNeverOffersToRetireTheLSPPlugin(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)

	for i := 1; i <= 2; i++ {
		rc, report := applyWith(t, true, strings.NewReader(""))
		if rc != 0 {
			t.Fatalf("apply %d: rc=%d\n%s", i, rc, report)
		}
		// Not vacuous: the plugin the scan must not mistake is there.
		mustExist(t, lspPluginManifest(home, ".claude/skills"), "the apply writes it")
		for _, bad := range []string{"[y/N]", "no longer in your config", "not retired"} {
			if strings.Contains(report, bad) {
				t.Errorf("apply %d: %q in the report — the plugin was read as a dropped pack's "+
					"output:\n%s", i, bad, report)
			}
		}
	}
	survey, report := surveyApply(t)
	if pending := survey.PendingDecisions(); len(pending) > 0 {
		t.Errorf("the launch gate would see a decision pending: %v\n%s", pending, report)
	}
}

// IN SYNC after an --assert: the second one changes nothing and a dry run after it finds nothing
// to do, so a wrapped launch does not re-apply on every start.
func TestHostApplyKeepsTheLSPPluginInSync(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	verboseReport(t)

	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first assert rc=%d\n%s", rc, report)
	}
	path := lspPluginManifest(home, ".claude/skills")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("second assert rc=%d\n%s", rc, report)
	}
	if n := countLines(report, jailcontent.LSPPluginDir, "unchanged"); n == 0 {
		t.Errorf("the second assert does not report the plugin unchanged:\n%s", report)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an in-sync plugin was rewritten")
	}
	if survey, report := surveyApply(t); survey.Changes() {
		t.Errorf("a dry run after two asserts would change %v\n%s", survey.Changed, report)
	}
}

// THE DRY RUN writes nothing and says what the --assert would.
func TestHostApplyDryRunOnlyReportsTheLSPPlugin(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	verboseReport(t)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("dry run rc=%d\n%s", rc, report)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", jailcontent.LSPPluginDir)); err == nil {
		t.Error("the dry run wrote the plugin")
	}
	if n := countLines(report, jailcontent.LSPPluginDir, "would render"); n != 1 {
		t.Errorf("want one `would render` line for the plugin, got %d:\n%s", n, report)
	}
}

// EMPTYING lsp_servers retires the plugin, by archive and without a prompt: dropping the last
// server must stop it reaching Claude, and every byte moved is one yolo wrote.
func TestHostApplyArchivesTheLSPPluginWhenTheTableEmpties(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first assert rc=%d\n%s", rc, report)
	}
	writeLSPHostConfig(t, home, `"claude"`, "")

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("assert after emptying lsp_servers rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("retiring yolo's own plugin asked a question:\n%s", report)
	}
	mustNotExist(t, filepath.Join(home, ".claude", "skills", jailcontent.LSPPluginDir),
		"lsp_servers no longer declares a server")
	if !archivedPlugin(t, home) {
		t.Errorf("the plugin was not archived (it must move, never be deleted): %v",
			archivedAll(t, home))
	}
}

// DROPPING the pack whose destination held it retires it there, while the destination still
// selected gets its own copy.
func TestHostApplyArchivesTheLSPPluginWhereNoPackComposesAnyMore(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first assert rc=%d\n%s", rc, report)
	}
	writeLSPHostConfig(t, home, `"codex"`, lspHostServers)

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("assert after dropping claude rc=%d\n%s", rc, report)
	}
	mustNotExist(t, filepath.Join(home, ".claude", "skills", jailcontent.LSPPluginDir),
		"no selected pack composes ~/.claude/skills")
	if !archivedPlugin(t, home) {
		t.Errorf("the plugin was not archived: %v", archivedAll(t, home))
	}
	if _, err := os.Stat(lspPluginManifest(home, ".codex/skills")); err != nil {
		t.Errorf("the destination still selected has no plugin: %v\n%s", err, report)
	}
}

// AN EMPTY `packs` is the most complete drop there is, and it retires the plugin too.
func TestHostApplyWithNoPacksArchivesTheLSPPlugin(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first assert rc=%d\n%s", rc, report)
	}
	mustExist(t, lspPluginManifest(home, ".claude/skills"), "the first apply writes it")
	writeLSPHostConfig(t, home, "", lspHostServers)

	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("assert with no packs rc=%d\n%s", rc, report)
	}
	mustNotExist(t, filepath.Join(home, ".claude", "skills", jailcontent.LSPPluginDir),
		"no pack is configured")
}

// AN ENTRY NAMING A JAIL-ONLY PATH is left out of the plugin and named, as it is for Copilot's file:
// the table is the composition's.
func TestHostApplyLeavesAJailPathServerOutOfTheLSPPlugin(t *testing.T) {
	home := lspHostFixture(t, `"claude"`,
		`{"ws":{"command":"/workspace/bin/ws-lsp"},"gopls":{"command":"gopls"}}`)

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("assert rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(lspPluginManifest(home, ".claude/skills"))
	if err != nil {
		t.Fatalf("no plugin: %v\n%s", err, report)
	}
	if strings.Contains(string(got), "/workspace") || !strings.Contains(string(got), "gopls") {
		t.Errorf("want gopls and not the jail-only server:\n%s", got)
	}
	if n := countLines(report, "lsp_servers ws is not written at the host"); n != 1 {
		t.Errorf("the left-out server must be named once, got %d:\n%s", n, report)
	}
}

// A `yolo-lsp` YOLO DID NOT WRITE is left byte-for-byte alone and named with its remedy. Here it is
// a plugin the user authored, which the adoption walk also leaves alone.
func TestHostApplyLeavesTheUsersOwnYoloLSPAlone(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	mine := lspPluginManifest(home, ".claude/skills")
	const theirs = `{"name":"yolo-lsp","lspServers":{"mine":{"command":"mine"}}}`
	writeFile(t, mine, theirs)

	// The dry run names it and exits 0: its output is the finding (OQ-RO5).
	if rc, report := applyWith(t, false, nil); rc != 0 ||
		countLines(report, jailcontent.LSPPluginDir, "refused", "rename or remove it") != 1 {
		t.Errorf("the dry run must report the refusal and exit 0; rc=%d\n%s", rc, report)
	}
	rc, report := applyWith(t, true, strings.NewReader(""))
	if got, _ := os.ReadFile(mine); string(got) != theirs {
		t.Errorf("the user's own yolo-lsp was rewritten:\n%s", got)
	}
	if n := countLines(report, jailcontent.LSPPluginDir, "refused", "rename or remove it"); n != 1 {
		t.Errorf("want one refusal line naming the remedy, got %d:\n%s", n, report)
	}
	if rc == 0 {
		t.Errorf("an apply that could not deliver the configured servers exited 0\n%s", report)
	}
}

// archivedPlugin reports whether the archive holds a retired plugin manifest.
func archivedPlugin(t *testing.T, home string) bool {
	t.Helper()
	for _, rel := range archivedAll(t, home) {
		if strings.Contains(filepath.ToSlash(rel), lspPluginArchiveAttribution+"/"+
			jailcontent.LSPPluginDir+"/"+jailcontent.LSPPluginManifestRel) {
			return true
		}
	}
	return false
}
