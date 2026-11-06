package integration

// configmigration_test.go is the container tier of the old-format-to-TOML conversion
// (docs/design/config-migration.md): the launch's offer at a terminal, the one line without one,
// `convert_later`, and `yolo config migrate`. The unit tiers pin the planner and the offer's
// branches; these run the whole launch, so they see what only a launch shows: that the jail
// started on the file the answer chose, that the files on disk are what the answer left, and that
// the approval records were respelled so the NEXT launch is not asked about a change it did not
// make.
//
// The offer is made only at a terminal on both stdin and stderr, so these tests drive the real
// launcher through a pty (openTestPty), the way attachskewrestart_linux_test.go does.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// offerPrompt is the token the offer's question ends in; the wording before it is the product's
// to change.
const offerPrompt = "[y/N]"

// oldFormatUser and oldFormatWorkspace are the old-format graph every test starts from: a user
// config selecting a pack and a project setting an environment variable. Both are plain JSONC
// the conversion folds to TOML under its byte-equality rule.
//
// The project's witness is an env.set entry, not a package. A package made every launch here
// build a non-stock image, which these tests do not test, and on the macOS nightly that cold
// build (a darwin copier from source, then the Linux builder container) failed or ran past the
// 20-minute launch budget: TestOfferAnsweredNoLaunchesOnTheOldFiles on 2026-11-03,
// TestConvertLaterSilencesTheOffer on 2026-11-05. TestAnAttachIsNotOfferedTheConversion dropped
// its package for the same reason (5342558ff). The env entry proves the same thing, that the
// jail started on this file: witnessCheck holds only when the launch read it.
const (
	oldFormatUser      = "{\n  \"packs\": [\"guardrails\"]\n}\n"
	oldFormatWorkspace = "{\n  \"env\": {\"set\": {\"OLD_FORMAT_WITNESS\": \"from-jsonc\"}}\n}\n"

	// witnessCheck succeeds in a jail whose project config set the witness; witnessProbe also
	// prints witnessMark when it does.
	witnessCheck = `[ "${OLD_FORMAT_WITNESS-}" = from-jsonc ]`
	witnessProbe = witnessCheck + ` && echo ` + witnessMark
	witnessMark  = "OLD-FORMAT-WITNESS-SET"
)

// oldGraph is a workspace and an isolated home holding the old-format graph, with the paths the
// assertions read.
type oldGraph struct {
	dir       string // the workspace
	userDir   string // ~/.config/yolo-jail in the isolated home
	userOld   string
	workspOld string
	userNew   string
	workspNew string
}

// newOldGraph writes the old-format graph, with userExtra spliced into the user config object
// (for `"convert_later": true`), and gives the launch an empty yolo state so the machine-wide
// cache-reclaim offer, which is also asked at a terminal, is not asked here.
func newOldGraph(t *testing.T, userExtra string) oldGraph {
	t.Helper()
	return newOldGraphWith(t, userExtra, oldFormatWorkspace)
}

// newOldGraphWith is newOldGraph with workspace as the project's old-format file.
func newOldGraphWith(t *testing.T, userExtra, workspace string) oldGraph {
	t.Helper()
	user := oldFormatUser
	if userExtra != "" {
		user = "{\n  \"packs\": [\"guardrails\"],\n  " + userExtra + "\n}\n"
	}
	packHome(t, user)
	emptyYoloState(t)
	dir := writeProject(t, workspace)
	userDir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	return oldGraph{
		dir:       dir,
		userDir:   userDir,
		userOld:   filepath.Join(userDir, paths.UserConfigLegacyJSONCName),
		workspOld: filepath.Join(dir, "yolo-jail.jsonc"),
		userNew:   filepath.Join(userDir, paths.UserConfigTOMLName),
		workspNew: filepath.Join(dir, paths.WorkspaceConfigTOMLName),
	}
}

// emptyYoloState replaces the isolated home's link to the machine's yolo state with an empty
// directory of this test's own, for the reason privateYoloState gives; it is spelled here too so
// this file builds on every platform the pty helper does.
func emptyYoloState(t *testing.T) {
	t.Helper()
	link := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail")
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing the isolated home's link to the machine's yolo state: %v", err)
	}
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// terminalLaunch is one launch run on a pty.
type terminalLaunch struct {
	out   string
	rc    int
	asked bool // the offer's question appeared
}

// launchAtTerminal runs `yolo <args>` with a pty on stdin, stdout and stderr. When the offer's
// question appears it answers with answer plus a newline; an empty answer means the test says
// no question may be asked, so the launch is ended and asked is reported true. The launch is
// bounded by jailTimeout.
func launchAtTerminal(t *testing.T, dir string, args []string, answer string, opts ...runOption) terminalLaunch {
	t.Helper()
	master, slave := openTestPty(t)
	cmd := exec.Command(yoloBin, args...)
	cmd.Dir = dir
	cfg := runConfig{timeout: jailTimeout()}
	for _, o := range opts {
		o(&cfg)
	}
	cmd.Env = launchEnvironment(cfg)
	awaitDetachedWriters(t, dir, launchHome(cmd.Env))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting yolo: %v", err)
	}
	_ = slave.Close()
	out := &syncBuffer{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				_, _ = out.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		forceRemoveContainer(dir)
	})

	res := terminalLaunch{}
	answered := false
	deadline := time.After(jailTimeout())
	for {
		select {
		case err := <-done:
			var exitErr *exec.ExitError
			switch {
			case err == nil:
			case errors.As(err, &exitErr):
				res.rc = exitErr.ExitCode()
			default:
				t.Fatalf("waiting for yolo: %v", err)
			}
			time.Sleep(200 * time.Millisecond) // the reader may still be draining
			res.out = out.String()
			res.asked = strings.Contains(res.out, offerPrompt)
			return res
		case <-deadline:
			_ = cmd.Process.Kill()
			forceRemoveContainer(dir)
			t.Fatalf("the launch did not finish within %s:\n%s", jailTimeout(), out)
		case <-time.After(50 * time.Millisecond):
		}
		if answered && strings.Count(out.String(), offerPrompt) > 1 {
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("a second question followed the offer's answer, which a launch with matching records must not ask:\n%s", out)
		}
		if !answered && strings.Contains(out.String(), offerPrompt) {
			if answer == "" {
				_ = cmd.Process.Kill()
				<-done
				res.out = out.String()
				res.asked = true
				return res
			}
			answered = true
			if _, err := master.Write([]byte(answer + "\n")); err != nil {
				t.Fatalf("answering the offer: %v", err)
			}
		}
	}
}

// treeBytes is every regular file under dir (its relative path and bytes), skipping the jail's
// own .yolo state, for "nothing was written" comparisons.
func treeBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == ".yolo" || e.IsDir() {
			continue
		}
		out[e.Name()] = readFile(t, filepath.Join(dir, e.Name()))
	}
	return out
}

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}

func names(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Answering `n` at the offer launches the jail on the OLD files, and writes nothing: the project's
// witness variable is set in the jail (read from yolo-jail.jsonc), the user's pack is selected (read
// from config.jsonc), and no TOML or .pre-migrate file appears on disk (RC-D19).
func TestOfferAnsweredNoLaunchesOnTheOldFiles(t *testing.T) {
	requireJail(t)
	g := newOldGraph(t, "")

	// A first launch records the old files' approval, so the only question the second one can
	// put is the offer.
	if first := runYolo(t, g.dir, witnessCheck, withHostSemantics()); first.rc != 0 {
		t.Fatalf("the first launch on the old files: rc %d\n%s", first.rc, first.combined())
	}
	r := launchAtTerminal(t, g.dir, []string{"run", "--", "bash", "-lc",
		witnessProbe + `; grep -r x /etc/hostname; true`}, "n", withHostSemantics())
	if !r.asked {
		t.Fatalf("a launch at a terminal on an old-format config must ask to convert it:\n%s", r.out)
	}
	if r.rc != 0 {
		t.Fatalf("answering n must still launch: rc %d\n%s", r.rc, r.out)
	}
	if !strings.Contains(r.out, witnessMark) {
		t.Errorf("the jail did not start on the old yolo-jail.jsonc (witness variable unset):\n%s", r.out)
	}
	if !strings.Contains(r.out, "recursive mode is blocked") {
		t.Errorf("the jail did not start on the old config.jsonc (guardrails not selected):\n%s", r.out)
	}
	for _, p := range []string{g.userNew, g.workspNew, g.userOld + ".pre-migrate", g.workspOld + ".pre-migrate"} {
		if exists(p) {
			t.Errorf("answering n wrote %s", p)
		}
	}
	if got := readFile(t, g.workspOld); got != oldFormatWorkspace {
		t.Errorf("answering n changed yolo-jail.jsonc:\n%s", got)
	}
}

// Answering `y` converts both files in place, keeps each original as <name>.pre-migrate, and
// launches on the converted files: the same witness variable and pack are in effect, and the launch asks
// nothing more, because the conversion respelled the approval records (the launch carries no
// --accept-config-changes, so a record that no longer matched would put a second question to the
// terminal and the launch would not finish).
func TestOfferAnsweredYesConvertsThenLaunches(t *testing.T) {
	requireJail(t)
	g := newOldGraph(t, "")

	// A first launch on the old files records their approval and boot baseline, the state a
	// real user has when the conversion is first offered.
	first := runYolo(t, g.dir, witnessCheck, withHostSemantics())
	if first.rc != 0 {
		t.Fatalf("the first launch on the old files: rc %d\n%s", first.rc, first.combined())
	}

	r := launchAtTerminal(t, g.dir, []string{"run", "--", "bash", "-lc",
		witnessProbe + `; grep -r x /etc/hostname; true`}, "y", withHostSemantics())
	if !r.asked || r.rc != 0 {
		t.Fatalf("offer asked=%v, rc %d\n%s", r.asked, r.rc, r.out)
	}
	if strings.Count(r.out, offerPrompt) != 1 {
		t.Errorf("exactly one question was expected after the conversion:\n%s", r.out)
	}
	if !strings.Contains(r.out, witnessMark) || !strings.Contains(r.out, "recursive mode is blocked") {
		t.Errorf("the jail launched on the converted files lost the witness variable or the pack:\n%s", r.out)
	}
	if !exists(g.userNew) || !exists(g.workspNew) {
		t.Fatalf("the conversion wrote no TOML (user %v, project %v):\n%s",
			exists(g.userNew), exists(g.workspNew), r.out)
	}
	if got := readFile(t, g.workspOld+".pre-migrate"); got != oldFormatWorkspace {
		t.Errorf("the project's .pre-migrate is not its original:\n%s", got)
	}
	if got := readFile(t, g.userOld+".pre-migrate"); got != oldFormatUser {
		t.Errorf("the user config's .pre-migrate is not its original:\n%s", got)
	}
	if exists(g.workspOld) || exists(g.userOld) {
		t.Errorf("an old-format file is still in place beside its conversion")
	}

	// The next launch, with no terminal and no --accept-config-changes, is neither refused for
	// a changed config nor offered anything.
	next := runCommand(t, g.dir, []string{"run", "--", "bash", "-lc", witnessCheck}, withHostSemantics())
	if next.rc != 0 {
		t.Errorf("the launch after the conversion was refused (records not respelled?): rc %d\n%s",
			next.rc, next.combined())
	}
	if strings.Contains(next.combined(), "Deprecated config format") {
		t.Errorf("the launch after the conversion still sees an old file:\n%s", next.combined())
	}
}

// `"convert_later": true` in the old user file silences the offer, at a terminal and without one,
// and the launch runs on the old files. The control is the same launch without the key, which
// does say it (so the silence is the key's doing and not an absent feature).
func TestConvertLaterSilencesTheOffer(t *testing.T) {
	requireJail(t)

	control := newOldGraph(t, "")
	c := runCommand(t, control.dir, append(jailRunArgs(), "--", "bash", "-lc", `true`), withHostSemantics())
	if c.rc != 0 || !strings.Contains(c.stderr, "Deprecated config format") {
		t.Fatalf("control: a launch without a terminal on an old file should continue and say so: rc %d\n%s",
			c.rc, c.combined())
	}

	g := newOldGraph(t, `"convert_later": true`)
	before := treeBytes(t, g.dir)
	quiet := runCommand(t, g.dir, append(jailRunArgs(), "--", "bash", "-lc", witnessCheck), withHostSemantics())
	if quiet.rc != 0 || strings.Contains(quiet.combined(), "Deprecated config format") {
		t.Errorf("convert_later did not silence the line without a terminal: rc %d\n%s", quiet.rc, quiet.combined())
	}
	r := launchAtTerminal(t, g.dir, append(jailRunArgs(), "--", "bash", "-lc", witnessProbe), "", withHostSemantics())
	if r.asked {
		t.Fatalf("convert_later did not silence the question at a terminal:\n%s", r.out)
	}
	if r.rc != 0 || !strings.Contains(r.out, witnessMark) {
		t.Errorf("the silenced launch should run on the old files: rc %d\n%s", r.rc, r.out)
	}
	if !sameTree(before, treeBytes(t, g.dir)) || exists(g.userNew) || exists(g.workspNew) {
		t.Errorf("a silenced offer wrote files: %v", names(treeBytes(t, g.dir)))
	}
}

// `yolo config migrate --dry-run` shows the plan and writes nothing, in the workspace or in the
// user config directory.
func TestConfigMigrateDryRunWritesNothing(t *testing.T) {
	requireJail(t)
	g := newOldGraph(t, "")
	wsBefore, userBefore := treeBytes(t, g.dir), treeBytes(t, g.userDir)

	r := runCommand(t, g.dir, []string{"config", "migrate", "--dry-run"}, withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("yolo config migrate --dry-run: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "yolo-jail.jsonc") || !strings.Contains(r.combined(), "yolo-jail.toml") {
		t.Errorf("the dry run should show the conversion it would make:\n%s", r.combined())
	}
	if !sameTree(wsBefore, treeBytes(t, g.dir)) || !sameTree(userBefore, treeBytes(t, g.userDir)) {
		t.Errorf("--dry-run wrote files: workspace %v, user %v",
			names(treeBytes(t, g.dir)), names(treeBytes(t, g.userDir)))
	}
}

// `yolo config migrate --yes` converts the user graph and the project, leaving each original as a
// byte-identical <name>.pre-migrate, and respells the approval records: a launch with no
// terminal and no --accept-config-changes right after is not refused for a changed config.
func TestConfigMigrateKeepsPreMigrateCopiesAndRespellsRecords(t *testing.T) {
	requireJail(t)
	g := newOldGraph(t, "")

	first := runYolo(t, g.dir, witnessCheck, withHostSemantics())
	if first.rc != 0 {
		t.Fatalf("the first launch on the old files: rc %d\n%s", first.rc, first.combined())
	}

	r := runCommand(t, g.dir, []string{"config", "migrate", "--yes"}, withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("yolo config migrate --yes: rc %d\n%s", r.rc, r.combined())
	}
	for _, c := range []struct{ old, want string }{
		{g.userOld, oldFormatUser}, {g.workspOld, oldFormatWorkspace},
	} {
		if got := readFile(t, c.old+".pre-migrate"); got != c.want {
			t.Errorf("%s.pre-migrate is not the original:\n%s", c.old, got)
		}
	}
	if !exists(g.userNew) || !exists(g.workspNew) {
		t.Fatalf("migrate wrote no TOML:\n%s", r.combined())
	}

	next := runCommand(t, g.dir, []string{"run", "--", "bash", "-lc", witnessProbe}, withHostSemantics())
	if next.rc != 0 || !strings.Contains(next.stdout, witnessMark) {
		t.Fatalf("the launch on the converted files was refused or lost the witness variable (records not respelled?): rc %d\n%s",
			next.rc, next.combined())
	}

	// The control: a real change to the converted file IS refused with no terminal, so the clean
	// launch above was the records matching and not the gate being off in this harness.
	if err := os.WriteFile(g.workspNew, []byte(readFile(t, g.workspNew)+"\n[machine]\nresources = { memory = \"3g\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := runCommand(t, g.dir, []string{"run", "--", "bash", "-lc", `true`}, withHostSemantics())
	if changed.rc == 0 {
		t.Errorf("control: a changed config with no terminal and no flag should be refused:\n%s", changed.combined())
	}

	// Once is once: a second migrate finds nothing in the old format and writes nothing.
	snapshot := treeBytes(t, g.dir)
	again := runCommand(t, g.dir, []string{"config", "migrate", "--yes"}, withHostSemantics())
	if again.rc != 0 || !sameTree(snapshot, treeBytes(t, g.dir)) {
		t.Errorf("a second migrate changed files or failed: rc %d\n%s", again.rc, again.combined())
	}
}

// An ATTACH to a running jail is not offered the conversion: that jail's config was frozen at its
// boot, so a conversion under it is a change it would not see, and every extra terminal would be
// asked again. The first launch holds the jail open; the second, at a terminal, attaches and is
// asked nothing, and the old files are as the first left them (fed00b301).
func TestAnAttachIsNotOfferedTheConversion(t *testing.T) {
	requireJail(t)
	// A project with no `packages`: what is under test is the attach, and a package makes the
	// first launch build an image of its own, which emptyYoloState leaves no record of. On a
	// macOS runner that cold build alone ran past the first session's whole budget (the
	// 2026-11-03 nightly: "first session never started within 20m0s"). Both files are still
	// old-format, so a fresh launch at a terminal would be offered the conversion.
	g := newOldGraphWith(t, "", "{}\n")
	before := treeBytes(t, g.dir)

	const releaseName = "release-offer-attach"
	first := startYoloBackground(t, "first", g.dir,
		`echo FIRST-SESSION-UP; `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
	release := func() { _ = os.WriteFile(filepath.Join(g.dir, releaseName), []byte("go\n"), 0o644) }
	t.Cleanup(release)
	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(first.combined(), "FIRST-SESSION-UP\n") {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before its session ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first session never started within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	awaitLaunchLockReleased(t, g.dir, first)

	r := launchAtTerminal(t, g.dir, append(jailRunArgs(), "--", "bash", "-lc", `echo ATTACHED-OK`), "")
	if r.asked {
		t.Fatalf("an attach to a running jail was offered the conversion:\n%s", r.out)
	}
	if r.rc != 0 || !strings.Contains(r.out, "Attaching to existing jail") || !strings.Contains(r.out, "ATTACHED-OK") {
		t.Errorf("the second launch should have attached and run (rc %d):\n%s", r.rc, r.out)
	}
	if exists(g.userNew) || exists(g.workspNew) {
		t.Errorf("an attach converted the config under a running jail")
	}
	release()
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("first launch rc = %d:\n%s", rc, first.combined())
	}
	after := treeBytes(t, g.dir)
	delete(after, releaseName)
	if !sameTree(before, after) {
		t.Errorf("the workspace files changed under the running jail: %v", names(after))
	}
}
