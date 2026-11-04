package packsrc

// gitbin.go is which git binary the store runs when Store.Git names none: "git" from PATH, except
// that ON DARWIN, WHERE THAT IS APPLE'S /usr/bin/git, it is the developer tools' git that shim
// forwards every run to. /usr/bin/git is an xcrun trampoline: each run looks up the active
// developer directory and starts the real git there, which doubles what one git start costs —
// MEASURED on the maintainer's Mac 2026-10-23, 20.0–21.1 ms a `/usr/bin/git --version` against
// 10.1–11.2 ms for `$(xcrun --find git) --version` (docs/plans/test-suite-speed.md) — and a
// patched fork's check and replay start git dozens of times a launch.
//
// xcrun is asked once per process for each DEVELOPER_DIR, and its answer is the binary the shim
// would run at that moment. The answer is kept for the process, so a later `xcode-select -s`, or a
// TOOLCHAINS setting (which the key does not include), is not seen until the next process. An
// xcrun that fails, or does not answer within xcrunBound, leaves "git" as it was and is not kept:
// the next store asks again.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// appleGitShim is the xcrun trampoline macOS installs as git.
const appleGitShim = "/usr/bin/git"

// The seams storeGit reads, so a test on any host can stand in for darwin's.
var (
	gitGOOS      = runtime.GOOS
	gitLookPath  = exec.LookPath
	xcrunFindGit = askXcrunForGit
	// xcrunBound bounds the one xcrun question. It runs at a store's first git, outside every
	// replay's and launch's own bound, so an xcrun that hangs must not hold the launch.
	xcrunBound = 5 * time.Second
)

// askXcrunForGit is `xcrun --find git`, bounded by xcrunBound: past it, the run is killed and its
// answer is an error.
func askXcrunForGit() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), xcrunBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xcrun", "--find", "git")
	cmd.WaitDelay = xcrunBound // a child holding stdout open does not outlast the bound either
	out, err := cmd.Output()
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return strings.TrimSpace(string(out)), err
}

// shimTargets is xcrun's answer per developer directory (DEVELOPER_DIR, which selects it as
// xcode-select does), "" for an answer that names no usable git. A failed question is not kept.
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
	p, err := xcrunFindGit()
	if err != nil {
		return "git" // no answer: not kept, so the next store asks again
	}
	target := ""
	if filepath.IsAbs(p) && p != appleGitShim {
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
