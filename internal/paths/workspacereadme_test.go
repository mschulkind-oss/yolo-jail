package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ignoreTextBeforeSD10 is the .yolo/.gitignore text yolo shipped from 7f52f2406 (2026-09-12)
// until SD-D10, recovered from that commit. Every workspace launched in that window holds it.
const ignoreTextBeforeSD10 = `# yolo-jail's per-workspace state. None of it belongs in version control:
# home/ is the jail's home overlay, archive/ holds verbatim copies of your own
# agent config files, and the logs record a launch's full argv.
#
# The bare '*' below ignores this file too, so .yolo/ stays invisible to git
# however the repo's own .gitignore is written. yolo writes this file once and
# never overwrites it - edit it, or delete it, if you mean to commit any of this.
*
`

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The hash list is the whole mechanism of SD-D10, so it is pinned against the texts: every text
// yolo has shipped, the current one included, must be recognised, or a workspace holding it
// keeps a stale comment forever.
func TestEveryShippedIgnoreTextIsRecognised(t *testing.T) {
	for name, text := range map[string]string{"before SD-D10": ignoreTextBeforeSD10, "current": WorkspaceStateIgnore} {
		if !slices.Contains(shippedWorkspaceStateIgnoreSHA256, sha256Hex(text)) {
			t.Errorf("the %s .gitignore text (sha256 %s) is not in shippedWorkspaceStateIgnoreSHA256", name, sha256Hex(text))
		}
	}
	if !strings.Contains(WorkspaceStateIgnore, "\n*\n") {
		t.Error("the current .gitignore lost its bare '*', the line that keeps .yolo out of git")
	}
	if strings.Contains(WorkspaceStateIgnore, "home/") || !strings.Contains(WorkspaceStateIgnore, "README.md") {
		t.Errorf("the current .gitignore still names the retired home/ layout or does not point at README.md:\n%s", WorkspaceStateIgnore)
	}
}

// A workspace holding the old shipped text gets the current one at the next ensure.
func TestAShippedIgnoreTextIsRefreshed(t *testing.T) {
	ws := t.TempDir()
	dir := WorkspaceStateDir(ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, WorkspaceStateIgnoreName)
	if err := os.WriteFile(p, []byte(ignoreTextBeforeSD10), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != WorkspaceStateIgnore {
		t.Errorf("the old shipped .gitignore was not refreshed:\n%s", got)
	}
}

// One changed byte makes the file the user's: it is never touched.
func TestANearlyShippedIgnoreTextIsTheUsers(t *testing.T) {
	ws := t.TempDir()
	dir := WorkspaceStateDir(ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, WorkspaceStateIgnoreName)
	mine := ignoreTextBeforeSD10 + "!handover.md\n"
	if err := os.WriteFile(p, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != mine {
		t.Errorf("yolo rewrote a .gitignore the user edited:\n%s", got)
	}
}

// A link at the ignore name is never followed, and never replaced either: it is not a regular
// file holding a shipped text, so it is the user's, even when it points at one.
func TestALinkedIgnoreFileIsLeftAlone(t *testing.T) {
	ws := t.TempDir()
	dir := WorkspaceStateDir(ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "shipped")
	if err := os.WriteFile(target, []byte(ignoreTextBeforeSD10), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, WorkspaceStateIgnoreName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link at the ignore name was replaced: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != ignoreTextBeforeSD10 {
		t.Errorf("the write followed the link onto %s:\n%s", target, got)
	}
}

func readReadme(t *testing.T, ws string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(WorkspaceStateDir(ws), WorkspaceStateReadmeName))
	if err != nil {
		t.Fatalf("no README in .yolo: %v", err)
	}
	return string(b)
}

// THE PROPERTY: whoever opens .yolo first reads that the agents' sessions are in it, not to move
// it, and how to start without it.
func TestTheStateDirSaysWhatItHolds(t *testing.T) {
	ws := t.TempDir()
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	got := readReadme(t, ws)
	for _, want := range []string{
		"The agents' homes are in here",
		"Never move, rename, delete or `git clean` this directory",
		"`yolo --fresh-home`",
		"- `setups/<setup>/home/` IRREPLACEABLE",
		"- `durable/` IRREPLACEABLE",
		"yolo rewrites this file at every launch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the README does not say %q:\n%s", want, got)
		}
	}
	// The logs and locks move under logs/ and run/ in a later slice, which updates this text.
	for _, gone := range []string{"`logs/`", "`run/`"} {
		if strings.Contains(got, gone) {
			t.Errorf("the README names %s, which no launch makes yet:\n%s", gone, got)
		}
	}
}

// The README names the homes this workspace really holds, by the layout a launch binds, with the
// install dirs from HomeSurfaces and the agents' own dirs from the disk. It is rewritten at every
// ensure, so a home made after the first one is listed at the next.
func TestTheReadmeNamesTheLayoutTheLaunchUses(t *testing.T) {
	ws := t.TempDir()
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	setupHome := SetupHomeState(ws, "work")
	for _, d := range []string{"claude", "pi", "local"} {
		if err := os.MkdirAll(filepath.Join(setupHome, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(setupHome, "yolo-perf.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(LegacyHomeState(ws), "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	got := readReadme(t, ws)

	if strings.Contains(got, "setups/<setup>/home/") {
		t.Errorf("the README still shows the placeholder home with real homes on disk:\n%s", got)
	}
	setupRow := rowFor(t, got, "- `setups/work/home/` IRREPLACEABLE")
	for _, s := range HomeSurfaces() {
		if want := "`" + s.Subtree + "/` is `~/" + s.HomeRel + "`"; !strings.Contains(setupRow, want) {
			t.Errorf("the home row does not name the install dir %s:\n%s", want, setupRow)
		}
	}
	if !strings.Contains(setupRow, "Other dirs: `claude/`, `pi/`.") {
		t.Errorf("the home row does not list the agents' own dirs from the disk:\n%s", setupRow)
	}
	if strings.Contains(setupRow, "yolo-perf.log") {
		t.Errorf("the home row lists a file as a directory:\n%s", setupRow)
	}
	legacyRow := rowFor(t, got, "- `home/` IRREPLACEABLE")
	if !strings.Contains(legacyRow, "`codex/`") {
		t.Errorf("the legacy home row does not list its dirs:\n%s", legacyRow)
	}
}

func rowFor(t *testing.T, readme, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(readme, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("the README has no row starting %q:\n%s", prefix, readme)
	return ""
}

// The README is yolo's own text: an edit to it, or a link the last jail left at its name, is
// replaced at the next ensure, and the link's target is never written.
func TestTheReadmeIsRewrittenAndALinkIsNotFollowed(t *testing.T) {
	ws := t.TempDir()
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(WorkspaceStateDir(ws), WorkspaceStateReadmeName)
	if err := os.WriteFile(p, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	if got := readReadme(t, ws); got != WorkspaceStateReadme(ws) {
		t.Errorf("a stale README was not rewritten:\n%s", got)
	}

	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := EnsureWorkspaceStateDir(ws); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "mine\n" {
		t.Errorf("the README write followed a link onto %s:\n%s", target, got)
	}
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("the README is not a regular file after the rewrite: %v", err)
	}
	// No temporary file is left behind.
	entries, err := os.ReadDir(WorkspaceStateDir(ws))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("a temporary file was left in .yolo: %s", e.Name())
		}
	}
}
