package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestResolvePackWholeRelocatedGitRootsUseOnlyNamedSubtree(t *testing.T) {
	for _, mode := range []string{"direct", "named"} {
		t.Run(mode, func(t *testing.T) {
			home := readablePackTestHome(t)
			repo, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			configPackGit(t, repo, "init", "-q", "--initial-branch=main")
			write(t, filepath.Join(repo, "corpus/nested/pack.toml"), "[[env]]\nvars.FIXTURE_WHOLE_ROOT = \"whole\"\n")
			write(t, filepath.Join(repo, "corpus/nested/skills/chosen/SKILL.md"), "nested\n")
			write(t, filepath.Join(repo, "corpus/sibling/SKILL.md"), "unrelated\n")
			write(t, filepath.Join(repo, "pack.toml"), "malformed outside relocated root [\n")
			configPackGit(t, repo, "add", "--all")
			configPackGit(t, repo, "commit", "-qm", "synthetic relocated whole source")
			root := filepath.Join(home, ".config/yolo-jail")
			url := "file://" + filepath.ToSlash(repo)
			git := "git"
			if mode == "direct" {
				realGit, err := exec.LookPath("git")
				if err != nil {
					t.Fatal(err)
				}
				git = filepath.Join(t.TempDir(), "fixture-git")
				url = "https://example.invalid/team/corpus"
				wrapper := fmt.Sprintf("#!/bin/bash\nargs=()\nfor arg in \"$@\"; do\n  if [ \"$arg\" = %q ]; then arg=%q; fi\n  args+=(\"$arg\")\ndone\nexec %q \"${args[@]}\"\n", url, "file://"+filepath.ToSlash(repo), realGit)
				if err := os.WriteFile(git, []byte(wrapper), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			body := fmt.Sprintf("packs = [{git=%q, ref=\"main\", subdir=\"corpus/nested\"}]\n", url)
			if mode == "named" {
				body = fmt.Sprintf("packs = [{pack=\"demo/corpus\", subdir=\"nested\"}]\n[pack_sources.demo]\ngit=%q\nref=\"main\"\n", url)
			}
			write(t, filepath.Join(root, "config.toml"), body)
			entries, problems, err := LoadPackEntries()
			entries = withoutDefaultSelected(entries)
			if err != nil || len(problems) != 0 || len(entries) != 1 {
				t.Fatalf("whole relocated producer: %+v %v %v", entries, problems, err)
			}
			store := &packsrc.Store{Dir: paths.PacksDir(), Git: git, Getenv: func(string) string { return "" }}
			results, err := store.Refresh([]packsrc.RefreshPack{entries[0].RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: packsrc.LockPath(paths.UserConfigPath())})
			if err != nil || len(results) != 1 || results[0].Err != nil {
				t.Fatalf("local Git pin: %+v %v", results, err)
			}
			dest := filepath.Join(t.TempDir(), "staged")
			resolved, err := ResolvePack(entries[0], ResolvePackSpec{Dest: dest, ReadOnlyStore: true, Git: git, Getenv: func(string) string { return "" }})
			if err != nil || resolved.Pack == nil || len(resolved.Problems) != 0 || resolved.Pack.Projection != nil {
				t.Fatalf("whole semantics changed: %+v %v", resolved, err)
			}
			if len(resolved.Pack.Decl.Contributes) != 1 || resolved.Pack.Decl.EnvContributions()["FIXTURE_WHOLE_ROOT"] != "whole" {
				t.Fatalf("relocation dropped ordinary effect: %+v", resolved.Pack.Decl)
			}
			if data, err := os.ReadFile(filepath.Join(dest, "skills/chosen/SKILL.md")); err != nil || string(data) != "nested\n" {
				t.Fatalf("relocated content: %q %v", data, err)
			}
			if _, err := os.Lstat(filepath.Join(dest, "sibling")); !os.IsNotExist(err) {
				t.Fatalf("unrelated sibling staged: %v", err)
			}
		})
	}
}
