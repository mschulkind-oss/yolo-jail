package entrypoint

// gitconfiglink_test.go pins WHERE configureGit's `git config --global` writes land on
// macos-user, against the real layout and real git.
//
// The bootstrap runs outside Seatbelt as the sandbox account, which can write every workspace
// under the shared root. ~/.gitconfig is the layout's redirect to .config/git/config, and
// ~/.config its link into the workspace sidecar, which the agent can write — so a link the
// agent leaves in the sidecar, below the layout's own, would carry git's write to wherever it
// points. git follows it: its lockfile resolves a symlinked config file before locking.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitLayoutLaunch runs the real macos-user bootstrap with the home layout (a sidecar named),
// the claude pack staged and git reachable only on the agent's PATH, as on a Mac. It returns
// what the bootstrap printed.
func gitLayoutLaunch(t *testing.T, home, ws, packRoot, email string) string {
	t.Helper()
	t.Setenv("PATH", "")
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME":             home,
		"YOLO_HOST_DIR":         ws,
		"YOLO_BLOCK_CONFIG":     `[]`,
		"YOLO_MISE_TOOLS":       `{}`,
		"YOLO_PACK_ROOT":        packRoot,
		"YOLO_DARWIN_WORKSPACE": ws,
		"YOLO_GIT_EMAIL":        email,
		DarwinHomeSidecarEnv:    filepath.Join(ws, ".yolo", "home"),
		"MISE_DATA_DIR":         filepath.Join(home, ".yolo", "mise"),
		DarwinLoginPathEnv:      filepath.Dir(gitBin),
	}, home)
	var out strings.Builder
	e.Stderr = &out
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})
	return out.String()
}

// swapForLink replaces p, whatever it is, with a symbolic link to target — what an agent can do
// to any path in its own workspace's sidecar between two launches.
func swapForLink(t *testing.T, p, target string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

// THE LAYOUT'S OWN PATH WORKS, AND NO OTHER. A clean first launch writes the identity and the
// safe.directory entry into the sidecar's git config, through the layout's links, and the
// agent's git (which reads ~/.gitconfig) accepts the workspace. Then the agent swaps a link in
// at each point of the sidecar path below the layout's own — the directory, or the file itself —
// aimed at ANOTHER workspace's .git, and the next launch must leave that repository's config
// byte-for-byte alone and say which link it refused and how to remove it.
//
// It fails if configureGit hands git ~/.gitconfig instead of the checked physical path (the
// other repository's config gained `[user] email` and `[safe] directory`), and its first half
// fails if the check stops following the layout's own links (nothing would be written at all).
func TestConfigureGitWritesOnlyThroughTheLayoutsOwnLinks(t *testing.T) {
	cases := map[string]func(sidecar, other string) (link, target string){
		"a link at the git config directory": func(sidecar, other string) (string, string) {
			return filepath.Join(sidecar, "config", "git"), filepath.Join(other, ".git")
		},
		"a link at the git config file": func(sidecar, other string) (string, string) {
			return filepath.Join(sidecar, "config", "git", "config"), filepath.Join(other, ".git", "config")
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			hermeticGit(t)
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(base, "home")
			ws := filepath.Join(base, "ws")
			other := filepath.Join(base, "other") // another workspace under the shared root
			for _, d := range []string{home, ws, other} {
				if out, err := exec.Command(gitBin, "init", "-q", d).CombinedOutput(); err != nil {
					t.Fatalf("git init %s: %v\n%s", d, err, out)
				}
			}
			sidecar := filepath.Join(ws, ".yolo", "home")
			packRoot := stagePackForBootstrap(t, "claude")

			said := gitLayoutLaunch(t, home, ws, packRoot, "first@example.com")
			written, err := os.ReadFile(filepath.Join(sidecar, "config", "git", "config"))
			if err != nil || !bytes.Contains(written, []byte(ws)) {
				t.Fatalf("the clean launch did not write safe.directory into the sidecar's git "+
					"config (%v):\n%s\nbootstrap said:\n%s", err, written, said)
			}
			if out, rc := gitAsAnotherOwner(home, ws, "status"); rc != 0 {
				t.Fatalf("after the clean launch the agent's git refuses the workspace (rc %d):\n%s", rc, out)
			}

			link, target := plant(sidecar, other)
			swapForLink(t, link, target)
			before, err := os.ReadFile(filepath.Join(other, ".git", "config"))
			if err != nil {
				t.Fatal(err)
			}

			said = gitLayoutLaunch(t, home, ws, packRoot, "second@example.com")

			after, err := os.ReadFile(filepath.Join(other, ".git", "config"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("the bootstrap wrote another workspace's .git/config through a link the "+
					"agent planted at %s:\n%s", link, after)
			}
			for _, want := range []string{link, "no safe.directory entry"} {
				if !strings.Contains(said, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, said)
				}
			}
			if !offers(t, said, "sudo", "rm", link) {
				t.Errorf("the refusal does not offer `sudo rm %s` as a shell reads it:\n%s", link, said)
			}
		})
	}
}

// A LAYOUT PATH THAT IS NOT YET THIS LAUNCH'S LINK IS NOT WRITTEN THROUGH. ~/.config left
// pointing at another workspace's sidecar (a launch whose layout step failed before it
// repointed the link) would otherwise have this workspace's identity and safe.directory entry
// written into that workspace's git config; and a real ~/.gitconfig where the redirect belongs
// would be one the next layout refuses forever (OQ-HT2), so it is not written either.
func TestTheGitConfigPathRefusesALayoutPathThatIsNotItsLink(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	otherSidecar := filepath.Join(base, "other", ".yolo", "home")
	for _, d := range []string{home, filepath.Join(sidecar, "config", "git"), filepath.Join(otherSidecar, "config", "git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	l := DeriveDarwinHomeLayout(home, sidecar, nil, nil)

	if err := os.Symlink(filepath.Join(".config", "git", "config"), filepath.Join(home, ".gitconfig")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(otherSidecar, "config"), filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	if got, err := l.homeFileThroughLayout(".gitconfig"); err == nil {
		t.Errorf("~/.config points at another workspace's sidecar and the path was accepted: %s", got)
	} else if !strings.Contains(err.Error(), filepath.Join(home, ".config")) {
		t.Errorf("the refusal does not name ~/.config: %v", err)
	}

	// Repointed at this launch's sidecar, the same walk arrives at the physical file.
	if err := os.Remove(filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sidecar, "config"), filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(sidecar, "config", "git", "config")
	if got, err := l.homeFileThroughLayout(".gitconfig"); err != nil || got != want {
		t.Errorf("through the layout's own links the path is %q (%v), want %q", got, err, want)
	}

	// A real file where the redirect belongs.
	if err := os.Remove(filepath.Join(home, ".gitconfig")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := l.homeFileThroughLayout(".gitconfig"); err == nil {
		t.Errorf("a real ~/.gitconfig where the layout's redirect belongs was accepted: %s", got)
	}
}

// Without a layout (an install capture's staging home, or a launch that named no sidecar) the
// file is ~/.gitconfig itself, and a link there is refused all the same: nothing lays one.
func TestTheGitConfigPathWithoutALayoutIsTheHomesOwnFile(t *testing.T) {
	home := t.TempDir()
	l := DarwinHomeLayout{Home: home}
	want := filepath.Join(home, ".gitconfig")
	if got, err := l.homeFileThroughLayout(".gitconfig"); err != nil || got != want {
		t.Fatalf("path = %q (%v), want %q", got, err, want)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), want); err != nil {
		t.Fatal(err)
	}
	if got, err := l.homeFileThroughLayout(".gitconfig"); err == nil {
		t.Errorf("a link at ~/.gitconfig was accepted with no layout to have laid it: %s", got)
	}
}
