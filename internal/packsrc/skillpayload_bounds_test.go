package packsrc

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitSkillInput(t *testing.T, repo, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = repo
	cmd.Env = gitTestEnv()
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSkillPayloadExactEntryAndByteBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bytes    int
		files    int
		want     string
		gitlinks bool
	}{
		{"entry-bound", 1, skillEntryLimit - 3, "", false},
		{"entry-over", 1, skillEntryLimit - 2, "entries 32769 exceed bound 32768", false},
		{"omitted-entry-bound", 1, skillEntryLimit - 3, "", true},
		{"omitted-entry-over", 1, skillEntryLimit - 2, "entries 32769 exceed bound 32768", true},
		{"byte-bound", 1 << 20, 255, "", false},
		{"byte-over", 1 << 20, 256, "bytes 269484032 exceed bound 268435456", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitRepo(t, map[string]string{"skills/chosen/SKILL.md": strings.Repeat("x", tc.bytes)})
			skills := gitIn(t, repo, "rev-parse", "HEAD:skills")
			blob := gitIn(t, repo, "rev-parse", "HEAD:skills/chosen/SKILL.md")
			baseCommit := gitIn(t, repo, "rev-parse", "HEAD")
			var tree strings.Builder
			fmt.Fprintf(&tree, "040000 tree %s\tskills\n", skills)
			for i := 0; i < tc.files; i++ {
				if tc.gitlinks {
					fmt.Fprintf(&tree, "160000 commit %s\tsupport-%05d\n", baseCommit, i)
				} else {
					fmt.Fprintf(&tree, "100644 blob %s\tsupport-%05d\n", blob, i)
				}
			}
			oid := gitSkillInput(t, repo, tree.String(), "mktree")
			commit := gitSkillInput(t, repo, "bounds fixture\n", "commit-tree", oid)
			gitIn(t, repo, "update-ref", "refs/heads/main", commit)
			source := "git+file://" + repo + "//skills/chosen?ref=" + commit
			s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
			a, _ := Parse(source)
			// Only transport is prepared here. The real cached reader executes the same
			// pure validator as the writer, without materializing a 256-MiB fixture.
			b, cancel := s.newBudget()
			defer cancel()
			if err := s.fetchMirror(b, a, true, false); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(t.TempDir(), SkillsLockName)
			unlock, err := s.lockMirror(a.Repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = s.saveSkillRecord(lock, SkillRecord{Repo: a.Repo, Ref: a.Ref, Commit: commit, Kind: "commit"}, nil)
			unlock()
			if err != nil {
				t.Fatal(err)
			}
			result := s.ReadSkillPayloads([]SkillRequest{{Label: "bounded/skills[0]", Source: source}}, lock)[0]
			if tc.want != "" {
				if result.State != SkillRefused || !strings.Contains(result.Reason, tc.want) {
					t.Fatalf("want %q: %+v", tc.want, result)
				}
				return
			}
			if result.State != SkillNotMaterialized {
				t.Fatalf("exact boundary: %+v", result)
			}
			wantBytes := int64((tc.files + 1) * tc.bytes)
			if tc.gitlinks {
				wantBytes = int64(tc.bytes)
				if len(result.Plan.Omissions) != tc.files {
					t.Fatalf("omissions %d, want %d", len(result.Plan.Omissions), tc.files)
				}
			}
			if result.Plan.ChargedEntries != tc.files+3 || result.Plan.ChargedBytes != wantBytes {
				t.Fatalf("charges: %d %d", result.Plan.ChargedEntries, result.Plan.ChargedBytes)
			}
		})
	}
}
