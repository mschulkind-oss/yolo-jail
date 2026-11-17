package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func TestPackMainSourcedSkillWritersAndPureReaders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(realGit, args...)
		cmd.Dir = repo
		cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	writeFile(t, filepath.Join(repo, "skills/first/SKILL.md"), "first\n")
	writeFile(t, filepath.Join(repo, "skills/second/SKILL.md"), "second\n")
	writeFile(t, filepath.Join(repo, "LICENSE"), "fixture license\n")
	writeFile(t, filepath.Join(repo, "pack.toml"), "[[program]]\nbin = \"never-activated\"\n")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	c1 := git("rev-parse", "HEAD")
	first, second := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(first, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"first\"\n", "git+file://"+repo+"//skills/first?ref=main"))
	writeFile(t, filepath.Join(second, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"second\"\n", "git+file://"+repo+"//skills/second?ref=main"))
	cfg := filepath.Join(home, ".config/yolo-jail/config.jsonc")
	writeFile(t, cfg, fmt.Sprintf(`{"packs":[{"name":"first","source":%q},{"name":"second","source":%q}]}`, "file://"+first, "file://"+second))
	wrapper := t.TempDir()
	events := filepath.Join(t.TempDir(), "transport-events")
	writeFile(t, filepath.Join(wrapper, "git"), fmt.Sprintf("#!/bin/sh\nfor a do case \"$a\" in fetch|clone) printf '%%s\\n' \"$*\" >> %q;; esac; done\nexec %q \"$@\"\n", events, realGit))
	if err := os.Chmod(filepath.Join(wrapper, "git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	call := func(args ...string) (int, string) {
		t.Helper()
		var out, err bytes.Buffer
		rc := packMain(args, &out, &err, false)
		return rc, out.String() + err.String()
	}
	rc, report := call("status")
	if rc != 0 || !strings.Contains(report, "NOT FETCHED") {
		t.Fatalf("unfetched status rc=%d:\n%s", rc, report)
	}
	rc, report = call("lint", first)
	if rc != 0 || !strings.Contains(report, "contents not verified") {
		t.Fatalf("offline sourced lint: %d %s", rc, report)
	}
	if _, err := os.Lstat(events); !os.IsNotExist(err) {
		t.Fatalf("offline lint transported: %v", err)
	}
	originalConfig, _ := os.ReadFile(cfg)
	writeFile(t, cfg, fmt.Sprintf(`{"packs":[{"name":"first","source":%q,"only":["pack.toml"]},{"name":"second","source":%q,"only":["pack.toml"]}]}`, "file://"+first, "file://"+second))
	rc, report = call("install")
	if rc != 0 || !strings.Contains(report, "WITHHELD") {
		t.Fatalf("legacy only: %d %s", rc, report)
	}
	if _, err := os.Lstat(events); !os.IsNotExist(err) {
		t.Fatalf("legacy only transported: %v", err)
	}
	if err := os.WriteFile(cfg, originalConfig, 0644); err != nil {
		t.Fatal(err)
	}
	rc, report = call("lint", "--online", first)
	if rc != 0 || !strings.Contains(report, "payload deployment") {
		t.Fatalf("online sourced lint: %d %s", rc, report)
	}
	if _, err := os.Lstat(packsrc.SkillsLockPath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("scratch lint advanced global record: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(paths.PacksDir(), "mirrors")); !os.IsNotExist(err) {
		t.Fatalf("scratch lint wrote global mirror: %v", err)
	}
	rc, report = call("install")
	if rc != 0 || !strings.Contains(report, "payload deployment") || strings.Count(report, "payload deployment") != 1 {
		t.Fatalf("install rc=%d:\n%s", rc, report)
	}
	if strings.Contains(report, "never-activated") {
		t.Fatalf("upstream program active:\n%s", report)
	}
	beforeEvents, _ := os.ReadFile(events)
	s := &packsrc.Store{Dir: paths.PacksDir()}
	digest := func() string {
		t.Helper()
		var data strings.Builder
		_ = filepath.Walk(s.Dir, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			fmt.Fprintf(&data, "%s %s %d\n", p, fi.Mode(), fi.ModTime().UnixNano())
			if fi.Mode().IsRegular() {
				b, _ := os.ReadFile(p)
				data.Write(b)
			}
			return nil
		})
		b, _ := os.ReadFile(packsrc.SkillsLockPath(cfg))
		data.Write(b)
		return data.String()
	}
	t.Cleanup(func() { _ = packsrc.RemoveSealedSkillTree(filepath.Dir(s.Dir), s.Dir) })
	before := digest()
	for _, args := range [][]string{{"status"}, {"explain", "first"}, {"footprint", first}} {
		rc, report = call(args...)
		if rc != 0 || !strings.Contains(report, "payload deployment") || !strings.Contains(report, "delivers registration") {
			t.Fatalf("reader %v rc=%d:\n%s", args, rc, report)
		}
		if strings.Contains(report, "never-activated") {
			t.Fatalf("upstream activated by %v:\n%s", args, report)
		}
	}
	if digest() != before {
		t.Fatal("readers changed store/record/stamps")
	}
	if b, _ := os.ReadFile(events); !bytes.Equal(b, beforeEvents) {
		t.Fatalf("readers fetched: %s", b)
	}
	if err := packsrc.RemoveSealedSkillTree(s.Dir, s.SkillPayloadPath("file://"+repo, c1)); err != nil {
		t.Fatal(err)
	}
	before = digest()
	for _, args := range [][]string{{"status"}, {"explain", "first"}, {"footprint", first}} {
		rc, report = call(args...)
		if rc != 0 || !strings.Contains(report, "NOT MATERIALIZED") || strings.Contains(report, "delivers registration") {
			t.Fatalf("deleted-payload reader %v rc=%d:\n%s", args, rc, report)
		}
	}
	if digest() != before {
		t.Fatal("deleted-payload readers rematerialized/touched state")
	}
	if b, _ := os.ReadFile(events); !bytes.Equal(b, beforeEvents) {
		t.Fatalf("deleted-payload readers fetched: %s", b)
	}
	// Explicit install forces transport and repairs the payload. A later invalid
	// row in another declaring pack then keeps the recorded pair at c1, nonzero.
	rc, report = call("install")
	if rc != 0 {
		t.Fatalf("repair install: %d %s", rc, report)
	}
	if err := os.Remove(filepath.Join(repo, "skills/second/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "skills/second/data"), "marker absent")
	git("add", "--all")
	git("-c", "commit.gpgsign=false", "commit", "-m", "invalid later row")
	c2 := git("rev-parse", "HEAD")
	rc, report = call("install")
	if rc == 0 || !strings.Contains(report, "second skills[0]") || !strings.Contains(report, c1) || !strings.Contains(report, c2) {
		t.Fatalf("group rollback install: %d %s", rc, report)
	}
	lock, err := os.ReadFile(packsrc.SkillsLockPath(cfg))
	if err != nil || !strings.Contains(string(lock), c1) || strings.Contains(string(lock), c2) {
		t.Fatalf("advanced shared record: %s %v", lock, err)
	}
	// Update has the same force/whole-selection behavior; never run an agent updater.
	old := programRefresh
	programRefresh = func(_ richtext.Printer, _ io.Writer) int { return 0 }
	t.Cleanup(func() { programRefresh = old })
	rc, report = call("update")
	if rc == 0 || !strings.Contains(report, "second skills[0]") {
		t.Fatalf("update rollback: %d %s", rc, report)
	}
}
