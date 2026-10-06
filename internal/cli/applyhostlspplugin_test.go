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

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
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

// lspClashFixture is lspHostFixture plus a configured pack, `name`, contributing skills to
// ~/.claude/skills: one skill dir per entry of skills, and `extra` spliced into its contribution
// (`,"reserved":["yolo-lsp"]` for a fence). It returns the home and the pack's root.
func lspClashFixture(t *testing.T, name, extra string, skills ...string) (home, packDir string) {
	t.Helper()
	home = lspHostFixture(t, `"claude"`, lspHostServers)
	packDir = filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"`+name+`","description":"d","contributes":[`+
			`{"kind":"skills","from":"skills","into":".claude/skills"`+extra+`}]}`)
	for _, s := range skills {
		writeFile(t, filepath.Join(packDir, "skills", s, "SKILL.md"),
			"---\nname: "+s+"\ndescription: x\n---\nbody\n")
	}
	writeLSPHostConfig(t, home, `"claude",{"source":"file://`+packDir+`","name":"`+name+`"}`, lspHostServers)
	return home, packDir
}

// A SKILL NAMED yolo-lsp THAT A PACK COMPOSES is refused by name, naming the pack and the rename —
// and the dry run says so too, on a home where nothing has been applied yet. The pack's claim is
// this composition's, not the ownership record's: a dry run records nothing, so read from the
// record the first dry run promised to render the plugin that the --assert then refused.
func TestHostApplyRefusesAYoloLSPAPackComposesNamingThePack(t *testing.T) {
	home, _ := lspClashFixture(t, "lspclash", "", "yolo-lsp")
	verboseReport(t)
	const refusal = "pack lspclash composes a skill named yolo-lsp"

	rc, dry := applyWith(t, false, nil)
	if rc != 0 || countLines(dry, refusal, "rename that skill in pack lspclash") != 1 ||
		countLines(dry, "Claude's language servers", "would render") != 0 {
		t.Errorf("the dry run must name the refusal the --assert makes, and exit 0; rc=%d\n%s", rc, dry)
	}
	rc, wet := applyWith(t, true, strings.NewReader(""))
	if rc != 1 || countLines(wet, refusal, "rename that skill in pack lspclash") != 1 {
		t.Errorf("want rc 1 and one refusal naming the pack, got rc=%d\n%s", rc, wet)
	}
	if jailcontent.IsLSPPlugin(filepath.Join(home, ".claude", "skills", jailcontent.LSPPluginDir)) {
		t.Error("yolo's plugin was written over the pack's skill")
	}
}

// ONCE THE USER FOLLOWS THE REFUSAL'S REMEDY, the dry run agrees with the --assert. The renamed
// pack's old `yolo-lsp` is still on disk and still in the record until an --assert archives it, so
// a dry run reading either repeated "rename that skill" about a skill already renamed, while the
// --assert archived the old entry and wrote the plugin.
func TestHostApplyDryRunAfterTheRenameAgreesWithTheAssert(t *testing.T) {
	home, packDir := lspClashFixture(t, "lspclash", "", "yolo-lsp")
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 1 {
		t.Fatalf("fixture: want the refusal first, rc=%d\n%s", rc, report)
	}
	if err := os.Rename(filepath.Join(packDir, "skills", "yolo-lsp"),
		filepath.Join(packDir, "skills", "renamed")); err != nil {
		t.Fatal(err)
	}
	verboseReport(t)

	rc, dry := applyWith(t, false, nil)
	if rc != 0 || countLines(dry, "pack lspclash composes a skill named yolo-lsp") != 0 ||
		countLines(dry, jailcontent.LSPPluginDir, "would render", "Claude's language servers") != 1 {
		t.Errorf("the dry run must say the plugin would render, and repeat no refusal; rc=%d\n%s", rc, dry)
	}
	if jailcontent.IsLSPPlugin(filepath.Join(home, ".claude", "skills", jailcontent.LSPPluginDir)) {
		t.Error("the dry run wrote the plugin")
	}
	rc, wet := applyWith(t, true, strings.NewReader(""))
	if rc != 0 || countLines(wet, jailcontent.LSPPluginDir, "rendered", "Claude's language servers") != 1 {
		t.Errorf("the --assert must write what the dry run promised; rc=%d\n%s", rc, wet)
	}
	mustExist(t, lspPluginManifest(home, ".claude/skills"), "the rename freed the name")
}

// A DESTINATION THAT RESERVES yolo-lsp gets no plugin: a pack contributing there fenced the name as
// another tool's tree, and a fence is never composed over. The skip is named under --verbose.
func TestHostApplyWritesNoLSPPluginWhereAPackReservesTheName(t *testing.T) {
	home, _ := lspClashFixture(t, "fence", `,"reserved":["yolo-lsp"]`, "x")
	verboseReport(t)

	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("assert rc=%d\n%s", rc, report)
	}
	mustNotExist(t, filepath.Join(home, ".claude", "skills", jailcontent.LSPPluginDir),
		"a pack contributing to ~/.claude/skills reserves yolo-lsp")
	if n := countLines(report, jailcontent.LSPPluginDir, "reserves this name"); n != 1 {
		t.Errorf("want the skip named once under --verbose, got %d:\n%s", n, report)
	}
}

// A DESTINATION IS DEAD ONLY OVER A COMPLETE PACK SET. With a configured pack that does not
// resolve, ~/.claude/skills — which no resolved pack composes any more — may still be that pack's,
// so neither the dry run nor anything after it may offer to archive the plugin there. (An --assert
// refuses an incomplete pack set before this, so the dry run is the posture that reaches the guard.)
func TestHostApplyKeepsTheLSPPluginWhileAConfiguredPackDoesNotResolve(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first assert rc=%d\n%s", rc, report)
	}
	writeLSPHostConfig(t, home,
		`"codex",{"source":"file://`+filepath.Join(home, "no-such-pack")+`","name":"ghost"}`, lspHostServers)
	verboseReport(t)

	_, dry := applyWith(t, false, nil)
	if n := countLines(dry, jailcontent.LSPPluginDir, "would archive"); n != 0 {
		t.Errorf("a dry run over an incomplete pack set offered to archive the plugin:\n%s", dry)
	}
	mustExist(t, lspPluginManifest(home, ".claude/skills"), "a configured pack did not resolve")
}

// AN UNSELECTED PACK'S DESTINATION THAT IS A LINK TO A SELECTED ONE is not dead. Sharing one skills
// tree between tools by a symlink (`~/.codex/skills -> ~/.claude/skills`) is common, and a path
// comparison read the link as a destination no selected pack composes: every --assert wrote
// Claude's plugin and then archived the same directory through the link, so Claude never kept its
// servers, the archive grew a `yolo-lsp.N` per apply, and the dry run always reported a change.
// The jail stages into fresh directories, so only a real home can meet this.
func TestHostApplyKeepsTheLSPPluginWhereADroppedDestinationLinksToALiveOne(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claudeSkills, filepath.Join(home, ".codex", "skills")); err != nil {
		t.Fatal(err)
	}
	verboseReport(t)

	for i := 1; i <= 2; i++ {
		rc, report := applyWith(t, true, strings.NewReader(""))
		if rc != 0 {
			t.Fatalf("apply %d: rc=%d\n%s", i, rc, report)
		}
		mustExist(t, lspPluginManifest(home, ".claude/skills"), "~/.codex/skills is ~/.claude/skills")
		if n := countLines(report, jailcontent.LSPPluginDir, "archived"); n != 0 {
			t.Errorf("apply %d archived the plugin it had just written, through the link:\n%s", i, report)
		}
	}
	if archivedPlugin(t, home) {
		t.Errorf("the archive holds the plugin: %v", archivedAll(t, home))
	}
	if survey, report := surveyApply(t); survey.Changes() {
		t.Errorf("a dry run after two asserts would change %v\n%s", survey.Changed, report)
	}
}

// THE DRY RUN'S RETIRE MOVES NOTHING, and says what the --assert would: the launch gate runs this
// observe pass on every `yolo host -- <bin>`, so a dry run that archived would delete the plugin
// from the home on each wrapped start. The dry-run twin of
// TestHostApplyArchivesTheLSPPluginWhenTheTableEmpties.
func TestHostApplyDryRunOnlyReportsTheLSPPluginRetire(t *testing.T) {
	home := lspHostFixture(t, `"claude"`, lspHostServers)
	if rc, report := applyWith(t, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("first assert rc=%d\n%s", rc, report)
	}
	writeLSPHostConfig(t, home, `"claude"`, "")
	verboseReport(t)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("dry run rc=%d\n%s", rc, report)
	}
	mustExist(t, lspPluginManifest(home, ".claude/skills"), "a dry run moves nothing")
	if archivedPlugin(t, home) {
		t.Errorf("the dry run archived the plugin: %v", archivedAll(t, home))
	}
	if n := countLines(report, jailcontent.LSPPluginDir, "would archive"); n != 1 {
		t.Errorf("want one `would archive` line for the plugin, got %d:\n%s", n, report)
	}
	if n := countLines(report, jailcontent.LSPPluginDir, " archived "); n != 0 {
		t.Errorf("the dry run labelled the retire as done:\n%s", report)
	}
}

// A `yolo-lsp` YOLO DID NOT WRITE IS NEVER RETIRED, on either retire path: the table emptying, and
// a destination no selected pack composes any more. Both archive unconfirmed, so the plugin's own
// manifest is the only thing between them and the user's directory.
func TestHostApplyNeverRetiresAYoloLSPItDidNotWrite(t *testing.T) {
	const theirs = `{"name":"yolo-lsp","lspServers":{"mine":{"command":"mine"}}}`
	for _, tc := range []struct {
		name, packs, lsp, skillsDir string
	}{
		{"the table is empty", `"claude"`, "", ".claude/skills"},
		{"no selected pack composes the destination", `"claude"`, lspHostServers, ".codex/skills"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := lspHostFixture(t, tc.packs, tc.lsp)
			mine := lspPluginManifest(home, tc.skillsDir)
			writeFile(t, mine, theirs)
			verboseReport(t)

			if _, report := applyWith(t, true, strings.NewReader("")); countLines(report,
				jailcontent.LSPPluginDir, "archive") != 0 {
				t.Errorf("the user's own yolo-lsp was offered for retirement:\n%s", report)
			}
			if got, err := os.ReadFile(mine); err != nil || string(got) != theirs {
				t.Errorf("the user's own yolo-lsp was moved or rewritten (err=%v):\n%s", err, got)
			}
			if archivedPlugin(t, home) {
				t.Errorf("the archive holds the user's yolo-lsp: %v", archivedAll(t, home))
			}
		})
	}
}

// EVERY I/O REFUSAL NAMES ITS NEXT STEP: the directory to check, then the rerun. An --assert that
// meets one exits 1 with the skills stage failed, so a line that only reported the error left the
// user with no way forward (the happy-path rule). The failures are made with a regular file where a
// directory belongs, since permission bits do not stop root, which this suite may run as.
func TestHostLSPPluginIORefusalsNameTheNextStep(t *testing.T) {
	const rerun = "then run `yolo host apply --assert` again"
	manifest := []byte(renderedLSPPlugin(t, lspHostServers))
	home := t.TempDir()
	file := func(rel string) string {
		p := filepath.Join(home, filepath.FromSlash(rel))
		writeFile(t, p, "not a directory\n")
		return p
	}

	t.Run("cannot inspect", func(t *testing.T) {
		parent := file("inspect")
		r := writeHostLSPPlugin(filepath.Join(parent, jailcontent.LSPPluginDir), manifest, "", false, home, true)
		wantRefusal(t, r, "cannot inspect ~/inspect/yolo-lsp", "check that ~/inspect is a directory you can write, "+rerun)
	})
	t.Run("could not create", func(t *testing.T) {
		path := file("create/yolo-lsp")
		r := writeHostLSPPlugin(path, manifest, "", true, home, true)
		wantRefusal(t, r, "could not create ~/create/yolo-lsp", "check that ~/create is a directory you can write, "+rerun)
	})
	t.Run("could not write", func(t *testing.T) {
		path := filepath.Join(home, "write", jailcontent.LSPPluginDir)
		if err := os.MkdirAll(filepath.Join(path, filepath.FromSlash(jailcontent.LSPPluginManifestRel)), 0o755); err != nil {
			t.Fatal(err)
		}
		r := writeHostLSPPlugin(path, manifest, "", true, home, true)
		wantRefusal(t, r, "could not write ~/write/yolo-lsp/.claude-plugin/plugin.json",
			"check that ~/write/yolo-lsp/.claude-plugin is a directory you can write, "+rerun)
	})
	t.Run("could not archive", func(t *testing.T) {
		path := filepath.Join(home, "retire", jailcontent.LSPPluginDir)
		writeFile(t, filepath.Join(path, filepath.FromSlash(jailcontent.LSPPluginManifestRel)), string(manifest))
		blocked := file("blocked")
		req := hostskills.ComposeRequest{ArchiveRoot: hostskills.ArchiveRoot(filepath.Join(blocked, "archive")),
			Stamp: "s"}
		r, ok := retireHostLSPPlugin(path, req, home, true, "your lsp_servers declares no server with a command")
		if !ok {
			t.Fatal("the plugin yolo wrote was not offered for retirement")
		}
		wantRefusal(t, r, "could not move ~/retire/yolo-lsp into the archive at ~/blocked/archive",
			"check that you can write both, "+rerun)
		mustExist(t, path, "an archive that failed moves nothing")
	})
}

// wantRefusal asserts r is a refusal whose line carries every one of want.
func wantRefusal(t *testing.T, r hostskills.Result, want ...string) {
	t.Helper()
	if r.Action != hostskills.ActionRefused || r.WouldChange {
		t.Errorf("want a refusal that changes nothing, got %s (would change: %v): %s", r.Action, r.WouldChange, r.Detail)
	}
	for _, w := range want {
		if !strings.Contains(r.Detail, w) {
			t.Errorf("the refusal does not say %q:\n%s", w, r.Detail)
		}
	}
}
