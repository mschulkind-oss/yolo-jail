package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// bootstrapgit_test.go pins which `git` the macos-user bootstrap runs, against the PATH the
// launch really hands it.
//
// THE BOOTSTRAP RUNS OUTSIDE SEATBELT (DarwinBootstrapArgv has no sandbox-exec), as the
// sandbox account, which can read and write every workspace under the shared root. P4 in
// docs/reference/macos-user-provisioning.md tolerates that only because what it runs is not the
// agent's to change. Its PATH is SandboxPath, which opens with directories in the sandbox home,
// and the profile lets the agent write the whole home.

// Every directory of the REAL login path that lies under the sandbox home gets a planted `git`
// that records its argv and exits 0, and the real bootstrap runs with the process's PATH empty,
// as `env -i` leaves it. None of them may run, and the git outside the home must still write
// the safe.directory entry.
//
// Planting in every home entry of SandboxPath, rather than in a list spelled here, is the
// point: an entry SandboxPath gains under the home is covered the day it is added. It fails if
// the bootstrap's git lookup searches the whole login path (it did: ~/.local/bin/git ran with
// `config --global user.email …` and `config --global --replace-all --fixed-value
// safe.directory …`).
func TestTheBootstrapNeverRunsAGitTheAgentCanWrite(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_DIR", "GIT_WORK_TREE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(realGit, "init", "-q", ws).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	marker := filepath.Join(base, "planted-git-ran")
	loginPath := SandboxPath(home, []string{filepath.Dir(realGit)})
	var planted []string
	for _, dir := range strings.Split(loginPath, ":") {
		if !strings.HasPrefix(dir, home+"/") {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\necho \"$0 $*\" >> '" + marker + "'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		planted = append(planted, dir)
	}
	if len(planted) == 0 {
		t.Fatalf("SandboxPath(%q) has no directory under the home, so this test plants nothing "+
			"and proves nothing: %s", home, loginPath)
	}

	t.Setenv("PATH", "")
	e := entrypoint.DarwinEnvFrom(map[string]string{
		"JAIL_HOME":                   home,
		"YOLO_DARWIN_WORKSPACE":       ws,
		"YOLO_GIT_EMAIL":              "someone@example.com",
		"MISE_DATA_DIR":               SandboxMiseData(home),
		entrypoint.DarwinLoginPathEnv: loginPath,
	}, home)
	e.Stderr = &strings.Builder{}
	_ = entrypoint.RunDarwinBootstrap(e, entrypoint.DarwinBootstrapOptions{MacosLog: "off"})

	if ran, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the bootstrap ran a git the agent can write, outside Seatbelt (planted in %s):\n%s",
			strings.Join(planted, ", "), ran)
	}
	get := exec.Command(realGit, "config", "--global", "--get-all", "safe.directory")
	get.Env = append(os.Environ(), "HOME="+home, "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"))
	out, err := get.Output()
	if err != nil || strings.TrimSpace(string(out)) != ws {
		t.Errorf("safe.directory = %q (%v), want %q written by the git outside the home\n"+
			"bootstrap said:\n%s", out, err, ws, e.Stderr)
	}
}

// THE HOST'S GLOBAL GITIGNORE REACHES THE SANDBOX'S GIT: the plan names the staged copy to the
// bootstrap, and the REAL bootstrap points core.excludesFile at it; a name whose file is missing
// sets nothing (git would ignore a dangling value anyway, and a jail that never had the file
// should not carry the setting). The variable and the staged layout are read off a real plan, so
// a rename on either side of the plan/bootstrap seam fails here.
func TestTheBootstrapAppliesTheStagedGlobalGitignore(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_DIR", "GIT_WORK_TREE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	plan := BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "", HomeOverlay{},
		HostContext{Tree: "/tmp/yolo-ctx-tree", GlobalGitignore: paths.ContextGlobalGitignore},
		jsonx.NewOrderedMap(), nil, nil)
	named, ok := argvEnvValue(plan.BootstrapArgv, GlobalGitignoreEnv)
	if !ok || !strings.HasPrefix(named, plan.CtxRoot+"/") {
		t.Fatalf("the plan names no staged gitignore under %s: %v", plan.CtxRoot, plan.BootstrapArgv)
	}

	for _, tc := range []struct {
		name   string
		staged bool
	}{{"staged", true}, {"missing", false}} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home, ws, ctxRoot := filepath.Join(base, "home"), filepath.Join(base, "ws"), filepath.Join(base, "ctx")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(realGit, "init", "-q", ws).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
			ignore := filepath.Join(ctxRoot, strings.TrimPrefix(named, plan.CtxRoot+"/"))
			if tc.staged {
				if err := os.MkdirAll(filepath.Dir(ignore), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(ignore, []byte("*.yolo-probe\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			e := entrypoint.DarwinEnvFrom(map[string]string{
				"JAIL_HOME":             home,
				"YOLO_DARWIN_WORKSPACE": ws,
				"MISE_DATA_DIR":         SandboxMiseData(home),
				GlobalGitignoreEnv:      ignore,
				entrypoint.DarwinLoginPathEnv: SandboxPath(home,
					[]string{filepath.Dir(realGit)}),
			}, home)
			e.Stderr = &strings.Builder{}
			_ = entrypoint.RunDarwinBootstrap(e, entrypoint.DarwinBootstrapOptions{MacosLog: "off"})

			get := exec.Command(realGit, "config", "--global", "--get", "core.excludesFile")
			get.Env = append(os.Environ(), "HOME="+home, "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"))
			out, _ := get.Output()
			got := strings.TrimSpace(string(out))
			if tc.staged && got != ignore {
				t.Errorf("core.excludesFile = %q, want the staged gitignore %q\nbootstrap said:\n%s",
					got, ignore, e.Stderr)
			}
			if !tc.staged && got != "" {
				t.Errorf("core.excludesFile = %q for a gitignore that was never staged", got)
			}
		})
	}
}
