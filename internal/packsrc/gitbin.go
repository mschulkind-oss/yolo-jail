package packsrc

// gitbin.go is which git binary the store runs when Store.Git names none: "git" from PATH, except
// that ON DARWIN, WHERE THAT IS APPLE'S /usr/bin/git, it is the developer tools' git that shim
// forwards every run to. /usr/bin/git is an xcrun trampoline: each run looks up the active
// developer directory and starts the real git there, which doubles what one git start costs —
// MEASURED on the maintainer's Mac 2026-10-23, 20.0–21.1 ms a `/usr/bin/git --version` against
// 10.1–11.2 ms for `$(xcrun --find git) --version` (docs/plans/test-suite-speed.md) — and a
// patched fork's check and replay start git dozens of times a launch. Asking xcrun once per
// process names the same binary the shim would run, for the same developer directory, so every
// answer is that git's; anything xcrun does not answer leaves "git" as it was.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// appleGitShim is the xcrun trampoline macOS installs as git.
const appleGitShim = "/usr/bin/git"

// The seams storeGit reads, so a test on any host can stand in for darwin's.
var (
	gitGOOS      = runtime.GOOS
	gitLookPath  = exec.LookPath
	xcrunFindGit = func() (string, error) {
		out, err := exec.Command("xcrun", "--find", "git").Output()
		return strings.TrimSpace(string(out)), err
	}
)

// shimTargets is xcrun's answer per developer directory (DEVELOPER_DIR, which selects it as
// xcode-select does), "" for no usable answer: asked once per process for each.
var shimTargets sync.Map

// storeGit is the git a Store with no Git set runs: "git", resolved through PATH at each run, unless
// on darwin PATH resolves it to the shim and xcrun names the developer tools' own git, an absolute
// executable path, which is run directly.
func storeGit() string {
	if gitGOOS != "darwin" {
		return "git"
	}
	if p, err := gitLookPath("git"); err != nil || p != appleGitShim {
		return "git"
	}
	key := os.Getenv("DEVELOPER_DIR")
	if v, ok := shimTargets.Load(key); ok {
		if v.(string) != "" {
			return v.(string)
		}
		return "git"
	}
	target := ""
	if p, err := xcrunFindGit(); err == nil && filepath.IsAbs(p) && p != appleGitShim {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			target = p
		}
	}
	shimTargets.Store(key, target)
	if target == "" {
		return "git"
	}
	return target
}
