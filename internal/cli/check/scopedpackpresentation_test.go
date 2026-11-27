package check

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// scopedPresentationCheck runs the real check section over one typed local
// import whose pack declares an audience-limited tree and an audience-limited
// manifest-sourced row, and returns what it printed.
func scopedPresentationCheck(t *testing.T) string {
	t.Helper()
	user := testconfig.User(t, "packs=[{pack='local/corpus',skills={only=['marked','remote']}}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	source := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for path, text := range map[string]string{
		"pack.toml":              "[[skills]]\nfrom='skills'\nagents=['claude']\n[[skills]]\nsource='git+https://example.invalid/other//skills/remote?ref=main'\nas='remote'\nagents=['pi']\n",
		"skills/marked/SKILL.md": "marked\n",
		"skills/folder/notes.md": "folder\n",
	} {
		full := filepath.Join(source, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 {
		t.Fatalf("check failed: %s", out.String())
	}
	return out.String()
}

func TestSectionPacksScopedInspectionPrintsRetainedAudience(t *testing.T) {
	out := scopedPresentationCheck(t)
	for _, want := range []string{"row marked", "row remote"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "row marked") && !strings.Contains(line, "audience=claude") {
			t.Fatalf("marked row lacks audience: %s", line)
		}
		if strings.Contains(line, "row remote") && !strings.Contains(line, "audience=pi") {
			t.Fatalf("remote row lacks audience: %s", line)
		}
	}
}

func TestSectionPacksUnpublishedSourcedRowNeverShowsTheEnclosingCommit(t *testing.T) {
	repo := t.TempDir()
	gitRun := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitRun("init", "-q", "--initial-branch=main")
	for path, text := range map[string]string{
		"corpus/pack.toml":              "[[skills]]\nfrom='skills'\n[[skills]]\nsource='git+https://example.invalid/other//skills/remote?ref=main'\nas='remote'\n",
		"corpus/skills/chosen/SKILL.md": "chosen\n",
	} {
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun("add", "--all")
	gitRun("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	commit := gitRun("rev-parse", "HEAD")
	user := testconfig.User(t, "packs=[{pack='demo/corpus',skills={only=['chosen','remote']}}]\n[pack_sources.demo]\ngit='file://"+filepath.ToSlash(repo)+"'\nref='main'\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	var entry config.PackEntry
	for _, candidate := range entries {
		if candidate.Name == "demo/corpus" {
			entry = candidate
		}
	}
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	refreshed, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath()), Force: true})
	if err != nil || len(refreshed) != 1 || refreshed[0].Err != nil {
		t.Fatalf("fixture refresh: %+v %v", refreshed, err)
	}
	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 {
		t.Fatalf("check failed: %s", out.String())
	}
	sawChosen, sawRemote := false, false
	for _, line := range strings.Split(out.String(), "\n") {
		switch {
		case strings.Contains(line, "row chosen"):
			sawChosen = true
			if !strings.Contains(line, "commit="+commit) {
				t.Fatalf("enclosing row lost its own commit: %s", line)
			}
		case strings.Contains(line, "row remote"):
			sawRemote = true
			if strings.Contains(line, commit) {
				t.Fatalf("unpublished sourced row shows the enclosing pack's commit: %s", line)
			}
			if !strings.Contains(line, "other") {
				t.Fatalf("sourced row lost its own repository: %s", line)
			}
		}
	}
	if !sawChosen || !sawRemote {
		t.Fatalf("rows missing: %s", out.String())
	}
}
