package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// All real clean commands in these tests are confined to disposable repositories.
// The production shim is found through PATH, exactly as a shell consumer finds it.
type gitGuardFixture struct {
	t                    *testing.T
	root, ws, git, block string
}

func newGitGuardFixture(t *testing.T, override string) *gitGuardFixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &gitGuardFixture{t: t, root: root, ws: filepath.Join(root, "repo"), git: git}
	f.raw("init", "-q", f.ws)
	for _, p := range []string{".yolo/.gitignore", ".yolo/home/session", ".yolo/durable/work/marker", "junk/file", "artifact.ignored", "sub/file", "-rf"} {
		f.put(p, "marker\n")
	}
	f.put(".yolo/.gitignore", "*\n")
	f.put(".git/info/exclude", "*.ignored\n")
	var selected []*packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "guardrails" {
			selected = append(selected, p)
		}
	}
	if len(selected) != 1 {
		t.Fatal("no embedded guardrails pack")
	}
	var security *jsonx.OrderedMap
	if override != "" {
		decoded, err := jsonx.Decode([]byte(`{"blocked_tools":` + override + `}`))
		if err != nil {
			t.Fatal(err)
		}
		security = decoded.(*jsonx.OrderedMap)
	}
	cfg := jsonx.MustDumpsCompact(config.NormalizeBlockedToolsWith(security, packload.BlockedTools(selected)))
	e := NewEnv(map[string]string{"JAIL_HOME": filepath.Join(root, "home"), "YOLO_BLOCK_CONFIG": cfg})
	e.Workspace = f.ws
	e.ShimBinDir = filepath.Dir(git)
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	f.block = e.BlockDir()
	return f
}
func (f *gitGuardFixture) put(p, body string) {
	f.t.Helper()
	p = filepath.Join(f.ws, p)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		f.t.Fatal(err)
	}
}
func (f *gitGuardFixture) raw(args ...string) {
	f.t.Helper()
	cmd := exec.Command(f.git, args...)
	cmd.Env = []string{"PATH=/bin:/usr/bin", "HOME=" + f.root, "GIT_CONFIG_NOSYSTEM=1"}
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("fixture git %v: %v: %s", args, err, out)
	}
}
func (f *gitGuardFixture) run(dir string, env []string, args ...string) (int, string) {
	f.t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", `exec git "$@"`, "git"}, args...)...)
	cmd.Stdin = strings.NewReader("1\n")
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + f.block + ":" + filepath.Dir(f.git) + ":/bin:/usr/bin", "HOME=" + f.root, "GIT_CONFIG_NOSYSTEM=1"}, env...)
	out, err := cmd.CombinedOutput()
	rc := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else {
			f.t.Fatal(err)
		}
	}
	return rc, string(out)
}
func (f *gitGuardFixture) exists(p string, want bool) {
	f.t.Helper()
	_, err := os.Stat(filepath.Join(f.ws, p))
	if (err == nil) != want {
		f.t.Errorf("%s exists=%v, want %v", p, err == nil, want)
	}
}
func (f *gitGuardFixture) refuse(dir string, args ...string) {
	f.t.Helper()
	rc, out := f.run(dir, nil, args...)
	if rc != 1 || !strings.Contains(out, "git: refusing") || !strings.Contains(out, "git clean -n") {
		f.t.Errorf("git %v: rc=%d output=%s, want refusal with dry-run next step", args, rc, out)
	}
	f.exists(".yolo/home/session", true)
	f.exists(".yolo/durable/work/marker", true)
}
func TestGuardGitCleanRefusesStateWipes(t *testing.T) {
	cases := [][]string{
		{"clean", "-fdx"}, {"clean", "-fdX"}, {"clean", "-ffdx"},
		{"--no-literal-pathspecs", "clean", "-fdx"},
		{"--attr-source=HEAD", "clean", "-fdx"},
		{"--attr-source", "HEAD", "clean", "-fdx"},
		{"clean", "-fdx", "--", ".yolo"}, {"clean", "-fdx", ".yolo/home"},
		{"clean", "-fdx", ".yolo/durable"}, {"clean", "-fdx", "."},
		{"clean", "-fdx", "junk", ".yolo"}, {"clean", "-fdx", "*"},
		{"clean", "-fdx", ":(top)*"}, {"clean", "-fdx", ".y*"},
		{"clean", "-fdx", `.\yolo`},
		{"clean", "-fdx", "missing/../.yolo"},
		{"clean", "-fx", ".yolo/home/session"},
		{"clean", "-n", "--no-dry-run", "-ffdx"},
		{"clean", "-n", "--no-dry", "-ffdx"},
		{"clean", "-fdx", "-n", "--no-dry-run"}, {"clean", "-i", "-dx"},
		{"clean", "-fdx", "-e", ".yolo"}, {"clean", "-fdX", "-e", ".yolo"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) { f := newGitGuardFixture(t, ""); f.refuse(f.ws, args...) })
	}
}
func TestGuardGitCleanSafeScopesAndReadOnlyCommands(t *testing.T) {
	cases := []struct {
		args         []string
		dir, removed string
	}{
		{[]string{"status", "--short"}, "", ""},
		{[]string{"clean", "-ndx"}, "", ""},
		{[]string{"clean", "-nidx"}, "", ""},
		{[]string{"clean", "-fdx", "--no-dry-run", "-n"}, "", ""},
		{[]string{"clean", "-fdx", "--", "junk"}, "", "junk/file"},
		{[]string{"clean", "-fdx", "--", "-rf"}, "", "-rf"},
		{[]string{"clean", "-fdx"}, ".yolo/durable/work", ".yolo/durable/work/marker"},
		{[]string{"clean", "-fdX", "--", "artifact.ignored"}, "", "artifact.ignored"},
		{[]string{"clean", "-fdx", "--", ".yolo/durable/work"}, "", ".yolo/durable/work/marker"},
		{[]string{"clean", "-fdx"}, "sub", "sub/file"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " ")+tc.dir, func(t *testing.T) {
			f := newGitGuardFixture(t, "")
			rc, out := f.run(filepath.Join(f.ws, tc.dir), nil, tc.args...)
			if rc != 0 {
				t.Fatalf("rc=%d: %s", rc, out)
			}
			f.exists(".yolo/home/session", true)
			if tc.removed != "" {
				f.exists(tc.removed, false)
			}
		})
	}
}
func TestGuardGitGlobalOptionsAndNestedPaths(t *testing.T) {
	f := newGitGuardFixture(t, "")
	f.refuse(f.root, "-C", f.root, "-C", "repo", "-c", "clean.requireForce=false", "clean", "-dx")
	f.refuse(f.root, "--git-dir", filepath.Join(f.ws, ".git"), "--work-tree="+f.ws, "clean", "-fdx")
	f.refuse(filepath.Join(f.ws, "sub"), "clean", "-fdx", "../.yolo")
	f.refuse(filepath.Join(f.ws, ".yolo", "home"), "clean", "-fx")
	f.refuse(filepath.Join(f.ws, ".yolo", "durable"), "clean", "-fdx")
	f.refuse(f.ws, "--icase-pathspecs", "clean", "-fdx", ".YOLO")
	f.refuse(f.root, "--git-dir="+filepath.Join(f.ws, ".git"), "--work-tree", f.ws, "clean", "-fdX")
	f.refuse(f.root, "-C", f.ws, "-c", "core.worktree="+f.ws, "clean", "-ffdx")
	f.refuse(filepath.Join(f.ws, "sub"), "clean", "-fdx", "..")
	outside := filepath.Join(f.root, "outside", "dir")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.ws, "link")); err != nil {
		t.Fatal(err)
	}
	f.refuse(f.ws, "clean", "-fdx", "link/../.yolo")
	rc, out := f.run(f.root, nil, "-C", f.ws, "-C", "", "clean", "-ndx")
	if rc != 0 {
		t.Fatalf("dry run: %d %s", rc, out)
	}
	other := filepath.Join(f.root, "other")
	f.raw("init", "-q", other)
	if err := os.WriteFile(filepath.Join(other, "junk"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	rc, out = f.run(f.ws, nil, "-C", other, "clean", "-fx")
	if rc != 0 {
		t.Fatalf("outside clean: %d %s", rc, out)
	}
	if _, err := os.Stat(filepath.Join(other, "junk")); !os.IsNotExist(err) {
		t.Fatal("outside clean did not run")
	}
	f.exists(".yolo/home/session", true)
}
func TestGuardGitBypassAndUserOverrideKeepTheirSemantics(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "bypass", true: "override"}[override], func(t *testing.T) {
			cfg := ""
			env := []string{"YOLO_BYPASS_SHIMS=1"}
			if override {
				cfg = `[{"name":"git","message":"user override"}]`
				env = nil
			}
			f := newGitGuardFixture(t, cfg)
			rc, out := f.run(f.ws, env, "clean", "-fdx")
			if override {
				if rc != 127 || !strings.Contains(out, "user override") || strings.Contains(out, "git: refusing") {
					t.Fatalf("override: %d %s", rc, out)
				}
				f.exists(".yolo/home/session", true)
				return
			}
			if rc != 0 {
				t.Fatalf("opt-in clean: %d %s", rc, out)
			}
			f.exists(".yolo/home/session", false)
			f.exists(".yolo/durable/work/marker", false)
		})
	}
}
