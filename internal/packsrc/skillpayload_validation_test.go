package packsrc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillPayloadSelectedRefusalsAndSupportingOmissions(t *testing.T) {
	for _, tc := range []struct {
		name, selected, license, want string
		setup                         func(*testing.T, string)
	}{
		{name: "ancestor-case", selected: "skills/Foo/chosen", want: "case-collision", setup: func(t *testing.T, r string) {
			writeSkillFixture(t, r, "skills/Foo/chosen/SKILL.md", "chosen", 0644)
			writeSkillFixture(t, r, "skills/foo/other/SKILL.md", "other", 0644)
		}},
		{name: "selected-escape", want: "escapes selected directory", setup: func(t *testing.T, r string) {
			if err := os.Symlink("../../VERSION", filepath.Join(r, "skills/chosen/out")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "selected-cycle", want: "cyclic", setup: func(t *testing.T, r string) {
			if err := os.Symlink("loop", filepath.Join(r, "skills/chosen/loop")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "selected-dangling", want: "dangling", setup: func(t *testing.T, r string) {
			if err := os.Symlink("absent", filepath.Join(r, "skills/chosen/link")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "selected-case", want: "case-collision", setup: func(t *testing.T, r string) {
			writeSkillFixture(t, r, "skills/chosen/A", "a", 0644)
			writeSkillFixture(t, r, "skills/chosen/a", "b", 0644)
		}},
		{name: "missing-marker", want: "SKILL.md", setup: func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, "skills/chosen/SKILL.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing-directory", selected: "skills/absent", want: "absent"},
		{name: "marker-bound", want: "bound 1048576", setup: func(t *testing.T, r string) {
			writeSkillFixture(t, r, "skills/chosen/SKILL.md", strings.Repeat("a", skillMarkerLimit+1), 0644)
		}},
		{name: "explicit-missing-license", license: "legal/absent", want: "license"},
		{name: "explicit-license-collision", license: "legal/terms", want: "conflicts", setup: func(t *testing.T, r string) {
			writeSkillFixture(t, r, "legal/terms", "license", 0644)
			writeSkillFixture(t, r, "skills/chosen/TERMS", "own", 0644)
		}},
		{name: "safe-support", setup: func(t *testing.T, r string) {
			writeSkillFixture(t, r, "docs/A", "a", 0644)
			writeSkillFixture(t, r, "docs/a", "a", 0644)
			if err := os.Symlink("cycle", filepath.Join(r, "cycle")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("absent", filepath.Join(r, "dangling")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "marker-symlink", setup: func(t *testing.T, r string) {
			if err := os.Rename(filepath.Join(r, "skills/chosen/SKILL.md"), filepath.Join(r, "skills/chosen/marker")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("marker", filepath.Join(r, "skills/chosen/SKILL.md")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitRepo(t, map[string]string{"VERSION": "root", "skills/chosen/SKILL.md": "raw marker", "skills/chosen/data": "asset", "LICENSE": "root license"})
			if tc.setup != nil {
				tc.setup(t, repo)
			}
			gitIn(t, repo, "add", "--all")
			gitIn(t, repo, "commit", "--allow-empty", "-m", "case")
			chosen := tc.selected
			if chosen == "" {
				chosen = "skills/chosen"
			}
			row := SkillRequest{Label: "pack/skills[0]", Source: "git+file://" + repo + "//" + chosen + "?ref=main", License: tc.license}
			s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
			t.Cleanup(func() { unsealSkillFixture(t, s.Dir) })
			lock := filepath.Join(t.TempDir(), SkillsLockName)
			res := s.ResolveSkillPayloads([]SkillRequest{row}, SkillResolveOptions{LockPath: lock})[0]
			if tc.want != "" {
				if res.State != SkillRefused || !strings.Contains(res.Reason, tc.want) || !strings.Contains(res.Reason, row.Label) {
					t.Fatalf("want %q: %+v", tc.want, res)
				}
				if _, err := os.Stat(lock); !os.IsNotExist(err) {
					t.Fatalf("invalid record published %v", err)
				}
				if _, err := os.Stat(res.Payload); res.Payload != "" && !os.IsNotExist(err) {
					t.Fatalf("invalid payload published %v", err)
				}
				return
			}
			if res.State != SkillReady {
				t.Fatalf("ready: %+v", res)
			}
			if tc.name == "safe-support" {
				if len(res.Plan.Omissions) != 3 {
					t.Fatalf("omissions: %+v", res.Plan.Omissions)
				}
				if target, err := os.Readlink(filepath.Join(res.Payload, "cycle")); err != nil || target != "cycle" {
					t.Fatalf("cycle not kept: %s %v", target, err)
				}
			}
			if !bytes.Equal(res.Plan.Rows[0].Marker, []byte("raw marker")) {
				t.Fatalf("marker: %q", res.Plan.Rows[0].Marker)
			}
			if res.Plan.Rows[0].LicenseName != "LICENSE" {
				t.Fatalf("license: %+v", res.Plan.Rows[0])
			}
		})
	}
}

func TestSkillPayloadReaderAndNotDueWriterNeverInvokeTransport(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/chosen/SKILL.md": "chosen"})
	s := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	t.Cleanup(func() { unsealSkillFixture(t, s.Dir) })
	row := SkillRequest{Label: "fixture", Source: "git+file://" + repo + "//skills/chosen?ref=main"}
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	res := s.ResolveSkillPayloads([]SkillRequest{row}, SkillResolveOptions{LockPath: lock})[0]
	if res.State != SkillReady {
		t.Fatalf("initial: %+v", res)
	}
	log := filepath.Join(t.TempDir(), "transport")
	wrapper := filepath.Join(t.TempDir(), "git")
	writeSkillFixture(t, filepath.Dir(wrapper), "git", "#!/bin/sh\ncase \" $* \" in *' clone '*|*' fetch '*|*' checkout '*) printf '%s\\n' \"$*\" >> '"+log+"'; exit 91;; esac\nexec git \"$@\"\n", 0755)
	s.Git = wrapper
	for _, r := range s.ReadSkillPayloads([]SkillRequest{row}, lock) {
		if r.State != SkillReady {
			t.Fatalf("reader: %+v", r)
		}
	}
	res = s.ResolveSkillPayloads([]SkillRequest{row}, SkillResolveOptions{LockPath: lock})[0]
	if res.State != SkillReady || res.Fetched {
		t.Fatalf("not due writer: %+v", res)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("transport invoked: %v", err)
	}
}

func TestSkillPayloadGitlinksAndUserSmudgeConfig(t *testing.T) {
	repo := gitRepo(t, map[string]string{"skills/chosen/SKILL.md": "raw marker", ".gitattributes": "* filter=fixture\n"})
	head := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",outside-submodule")
	gitIn(t, repo, "commit", "-m", "supporting gitlink")
	sentinel := filepath.Join(t.TempDir(), "smudge-ran")
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(cfg, []byte("[filter \"fixture\"]\n smudge = touch "+sentinel+"; cat\n required = true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Store{Dir: t.TempDir(), Env: append(gitTestEnv(), "GIT_CONFIG_GLOBAL="+cfg)}
	t.Cleanup(func() { unsealSkillFixture(t, s.Dir) })
	row := SkillRequest{Label: "fixture", Source: "git+file://" + repo + "//skills/chosen?ref=main"}
	lock := filepath.Join(t.TempDir(), SkillsLockName)
	res := s.ResolveSkillPayloads([]SkillRequest{row}, SkillResolveOptions{LockPath: lock})[0]
	if res.State != SkillReady || len(res.Plan.Omissions) != 1 || res.Plan.Omissions[0].Path != "outside-submodule" {
		t.Fatalf("support gitlink: %+v", res)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("smudge executed: %v", err)
	}
	gitIn(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",skills/chosen/submodule")
	gitIn(t, repo, "commit", "-m", "selected gitlink")
	res = s.ResolveSkillPayloads([]SkillRequest{row}, SkillResolveOptions{LockPath: lock, Force: true})[0]
	if res.State != SkillReady || !strings.Contains(res.Warning, "unsupported mode 160000") {
		t.Fatalf("selected gitlink fallback: %+v", res)
	}
	// A first install of that invalid tip cannot publish either half.
	fresh := &Store{Dir: t.TempDir(), Env: gitTestEnv()}
	res = fresh.ResolveSkillPayloads([]SkillRequest{row}, SkillResolveOptions{LockPath: filepath.Join(t.TempDir(), SkillsLockName)})[0]
	if res.State != SkillRefused || !strings.Contains(res.Reason, "unsupported mode 160000") {
		t.Fatalf("selected gitlink first install: %+v", res)
	}
}
