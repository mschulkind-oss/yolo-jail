package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func TestScopedPureReadsUseCachedPinUnderTransportRefusingGit(t *testing.T) {
	home := readablePackTestHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	repo := t.TempDir()
	configPackGit(t, repo, "init", "-q", "--initial-branch=main")
	write(t, filepath.Join(repo, "corpus/skills/chosen/SKILL.md"), "chosen\n")
	configPackGit(t, repo, "add", "--all")
	configPackGit(t, repo, "commit", "-qm", "fixture")
	body := "packs=[{pack='demo/corpus',skills={only=['chosen']}}]\n[pack_sources.demo]\ngit='file://" + filepath.ToSlash(repo) + "'\nref='main'\n"
	physical := filepath.Join(home, ".config/yolo-jail/config.toml")
	write(t, physical, body)
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) > 0 || len(entries) != 1 {
		t.Fatalf("producer: %v %v", err, problems)
	}
	entry := entries[0]
	store := &packsrc.Store{Dir: paths.PacksDir(), Getenv: func(string) string { return "" }}
	lockPath := packsrc.LockPath(paths.UserConfigPath())
	out, err := store.Refresh([]packsrc.RefreshPack{entry.RefreshSpec(false)}, packsrc.RefreshOptions{LockPath: lockPath, Force: true})
	if err != nil || len(out) != 1 || out[0].Err != nil {
		t.Fatalf("fixture refresh: %+v %v", out, err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	trace := filepath.Join(t.TempDir(), "git-reads")
	script := "#!/bin/sh\ncommand_seen=\nfor arg do\n case \"$arg\" in\n  cat-file|rev-parse|ls-tree|symbolic-ref|show-ref) command_seen=$arg;;\n  fetch|clone|init|checkout|archive|reset|update-ref|worktree|remote|gc|maintenance) printf 'DENIED %s\\n' \"$arg\" >> " + shquote.Quote(trace) + "; exit 91;;\n esac\ndone\n[ -n \"$command_seen\" ] || exit 92\nprintf '%s\\n' \"$command_seen\" >> " + shquote.Quote(trace) + "\nexec " + shquote.Quote(realGit) + " \"$@\"\n"
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	before := explicitEditTreeSnapshot(t, filepath.Dir(physical), paths.PacksDir())
	for _, tc := range []struct {
		name      string
		selection *packsrc.SkillSelection
		want      string
	}{{"typed cached", &packsrc.SkillSelection{Only: []string{"chosen"}}, ""}, {"raw cached", &packsrc.SkillSelection{Dir: "skills", Only: []string{"chosen"}}, ""}, {"stale cached", &packsrc.SkillSelection{Only: []string{"missing"}}, "available names"}} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := entry
			candidate.Skills = tc.selection
			res, err := ResolvePack(candidate, ResolvePackSpec{ReadOnlyStore: true, Git: git, Getenv: func(string) string { return "" }})
			if tc.want == "" && (err != nil || res.Pack == nil || res.Staged != nil) || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("pure projection: %+v %v", res, err)
			}
		})
	}
	workspace := t.TempDir()
	target, err := NewExplicitEditTarget(workspace, physical, ExplicitEditUser, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewConfigReadInput(physical, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	result, err := ValidateExplicitEdit(workspace, target, input)
	if err != nil || len(result.UnverifiedPacks()) != 0 {
		t.Fatalf("cached edit validation: %+v %v", result, err)
	}
	if after := explicitEditTreeSnapshot(t, filepath.Dir(physical), paths.PacksDir()); after != before {
		t.Fatal("pure cached reads changed source/config/store")
	}
	reads, err := os.ReadFile(trace)
	if err != nil || len(reads) == 0 || strings.Contains(string(reads), "DENIED") {
		t.Fatalf("read-only Git evidence: %s %v", reads, err)
	}
	// A cached branch without the handle's matching lock entry is authored-only,
	// not permission to float to the mirror's current branch tip.
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	before = explicitEditTreeSnapshot(t, filepath.Dir(physical), paths.PacksDir())
	if _, err := ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: true, Git: git}); !errors.Is(err, packsrc.ErrDefaultRefUnavailable) {
		t.Fatalf("unrecorded cached branch accepted: %v", err)
	}
	result, err = ValidateExplicitEdit(workspace, target, input)
	if err != nil || len(result.UnverifiedPacks()) != 1 {
		t.Fatalf("missing pin not unverified: %+v %v", result, err)
	}
	if after := explicitEditTreeSnapshot(t, filepath.Dir(physical), paths.PacksDir()); after != before {
		t.Fatal("unverified read wrote store/config")
	}
}
