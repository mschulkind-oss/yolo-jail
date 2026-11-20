package check

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

func TestCheckPreservesRetiredRepoRootRemedy(t *testing.T) {
	for _, format := range []string{"", "json"} {
		t.Run("format="+format, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			getenv := func(key string) string {
				if key == "YOLO_REPO_ROOT" {
					return "/obsolete/source"
				}
				return ""
			}
			resolution, ok := resolveRepoRoot(getenv)
			if ok || resolution.Error == nil {
				t.Fatal("retired source selector did not produce a resolver error")
			}
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			opts.Format = format
			opts.RepoRoot = func() (reporoot.Resolution, bool) {
				return resolveRepoRoot(getenv)
			}
			if exit := Check(opts); exit != 1 {
				t.Fatalf("Check exit = %d, want 1", exit)
			}
			want := resolution.Error.Error()
			if !strings.Contains(want, "unset YOLO_REPO_ROOT") {
				t.Fatalf("resolver remedy does not name the obsolete export: %q", want)
			}
			if format == "json" {
				var report Report
				if err := json.Unmarshal(out.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				for _, finding := range report.Findings {
					if finding.Message == "Could not resolve the yolo-jail repo root" {
						if finding.Note != want {
							t.Fatalf("repo-root note = %q, want resolver remedy %q", finding.Note, want)
						}
						return
					}
				}
				t.Fatal("JSON report omitted the repo-root finding")
			}
			if !strings.Contains(out.String(), want) {
				t.Fatalf("check lost resolver remedy %q:\n%s", want, out.String())
			}
			if strings.Contains(out.String(), "Reinstall yolo from Homebrew") {
				t.Fatalf("check substituted reinstall advice for an obsolete export:\n%s", out.String())
			}
		})
	}
}

// TestResolveRepoRootIgnoresUserConfigRepoPath is the retirement guard: the
// user-config repo_path key was dropped (2026-07-23), so check's resolver — like
// run's, since both delegate to the single internal/reporoot.Resolve — must NOT
// resolve a stray repo_path. HOME is isolated so the staged bundle misses and the
// exe-relative bundle misses beside the test binary; nothing left could return
// repoDir, so a pass proves the key is gone and check/run still agree.
func TestResolveRepoRootIgnoresUserConfigRepoPath(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{ "repo_path": "` + repoDir + `" }`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("YOLO_REPO_ROOT", "")
	os.Unsetenv("YOLO_REPO_ROOT")

	res, ok := resolveRepoRoot(os.Getenv)
	got := res.Root
	wantAbs, _ := filepath.EvalSymlinks(repoDir)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if ok && gotAbs == wantAbs {
		t.Fatalf("resolveRepoRoot honored the retired repo_path key: %q", got)
	}
}
