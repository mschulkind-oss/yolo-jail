package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// Step is one command an update runs.
type Step struct {
	Argv []string
	Dir  string   // "" = the caller's working directory
	Env  []string // added to the inherited environment
}

// String renders the step the way a person would type it.
func (s Step) String() string {
	var b strings.Builder
	for _, e := range s.Env {
		b.WriteString(e + " ")
	}
	b.WriteString(strings.Join(s.Argv, " "))
	if s.Dir != "" {
		b.WriteString("   (in " + s.Dir + ")")
	}
	return b.String()
}

// InstallKeepTreeEnv tells `just install` that it is deploying the tree `yolo update` just
// pulled, which it must leave exactly as it found it: it seeds the official pack programs into
// the cache WITHOUT re-pinning one into the checkout, and reports a build it cannot seed rather
// than failing the deploy (docs/design/broker-as-a-pack.md BP-D31). A re-pin there would ship a
// local edit as if it were upstream's, leave the checkout dirty for the next update, and make an
// autostash's `git stash pop` fail after a deploy that worked. The Justfile reads the name; a
// test in tools/pack-binaries holds the two spellings together.
const InstallKeepTreeEnv = "YOLO_INSTALL_KEEP_TREE"

// BrewKeepOldKegEnv keeps `brew upgrade` from deleting the version it replaces.
// By default Homebrew removes the old Cellar keg right after an upgrade, and a
// running jail bind-mounts its binaries and flake bundle from paths that
// resolve into that keg (internal/cli/run/jailprefix.go), so
// the upgrade would delete pid1 out from under every running jail. With it set,
// the old keg stays until a `brew cleanup` removes it.
const BrewKeepOldKegEnv = "HOMEBREW_NO_INSTALL_CLEANUP=1"

// RunningJailsNote says what an update through ch does to jails that are
// already running, for the channels `yolo update` installs. It is "" for the
// rest.
func RunningJailsNote(ch Channel) string {
	switch ch.Kind {
	case KindHomebrew:
		return "The previous version stays installed, so running jails keep their binaries until you restart them; `brew cleanup yolo-jail` removes it after that."
	case KindSource:
		return "Running jails keep their binaries until you restart them: `just deploy` installs a new bundle beside the one they use."
	}
	return ""
}

// Plan returns the commands that update ch, in order.
//
// A from-source deploy is pinned to the directory the running binary is in.
// Without that it follows whatever GOBIN the toolchain manager exports — a
// per-Go-version directory under mise, which is not on PATH — and the update
// "succeeds" into a binary nothing runs.
func Plan(ch Channel) ([]Step, error) {
	gobin := "GOBIN=" + filepath.Dir(ch.Exe)
	switch ch.Kind {
	case KindSource:
		return []Step{
			{Argv: []string{"git", "pull", "--ff-only"}, Dir: ch.SourceDir},
			{Argv: []string{"just", "deploy"}, Dir: ch.SourceDir, Env: []string{gobin, InstallKeepTreeEnv + "=1"}},
		}, nil
	case KindHomebrew:
		return []Step{{Argv: []string{"brew", "upgrade", "yolo-jail"}, Env: []string{BrewKeepOldKegEnv}}}, nil
	case KindGoInstall:
		return nil, binaryOnlyUpdateError(ch.Kind, "go install "+Module+"/cmd/yolo@latest")
	case KindPipx:
		return nil, binaryOnlyUpdateError(ch.Kind, "pipx upgrade yolo-jail")
	case KindUV:
		return nil, binaryOnlyUpdateError(ch.Kind, "uv tool upgrade yolo-jail")
	case KindArchive:
		return nil, fmt.Errorf("this yolo was unpacked from a release archive, which has no package manager to update it; download the new release from %s", ReleasesPage)
	}
	return nil, fmt.Errorf("cannot tell how this yolo was installed, so it cannot update itself; reinstall it from %s", ReleasesPage)
}

// Runner runs one Step, streaming its output.
type Runner func(ctx context.Context, s Step, stdout, stderr io.Writer) error

// RunStep is the real Runner.
func RunStep(ctx context.Context, s Step, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Dir = s.Dir
	cmd.Env = append(packsrc.CleanGitEnv(os.Environ()), s.Env...)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

func binaryOnlyUpdateError(kind Kind, command string) error {
	return fmt.Errorf("%s installs only the host binary and resolves the jail build separately (normally through YOLO_REPO_ROOT); updating one half would create a version skew. Update the checkout and binary together (%s), or reinstall with Homebrew or from source", kind, command)
}

// ApplyOptions are the choices `yolo update` passes through.
type ApplyOptions struct {
	// Autostash lets a source update proceed over uncommitted changes: they are
	// stashed first, the update pulls and deploys a CLEAN build, and they are
	// restored afterwards — also when the update fails.
	Autostash bool
	// SkipPull deploys the checkout exactly as named. `--from` uses it when the
	// checkout has no usable upstream (or the check is offline), because naming
	// a replacement checkout must still be able to repair the binary's stamp.
	SkipPull bool
}

// AutostashMessage names the stash an autostash update makes, so a person can
// find it in `git stash list` if restoring it fails.
const AutostashMessage = "yolo update autostash"

// Apply updates ch by running Plan's steps, stopping at the first failure.
//
// A source checkout is checked first. One that has left the branch the binary
// was built from is refused, as the explicit update check refuses it. One with
// uncommitted changes is refused unless opts.Autostash: `just install` stamps a
// dirty tree into the binary, so updating over the edits would ship them as if
// they were upstream's.
func Apply(ctx context.Context, ch Channel, opts ApplyOptions, git GitRunner, run Runner, stdout, stderr io.Writer) (retErr error) {
	steps, err := Plan(ch)
	if err != nil {
		return err
	}
	if ch.Kind == KindSource {
		if opts.SkipPull {
			steps = steps[1:]
		}
		if ch.Branch != "" {
			current, err := git(ctx, ch.SourceDir, "symbolic-ref", "--short", "-q", "HEAD")
			if err != nil || current == "" {
				return fmt.Errorf("the checkout at %s has a detached HEAD, so there is no branch to update from", ch.SourceDir)
			}
			if current != ch.Branch {
				return SourceBranchMismatch(ch, current)
			}
		}
		dirty, err := git(ctx, ch.SourceDir, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("git status in %s: %w", ch.SourceDir, err)
		}
		if dirty != "" {
			if !opts.Autostash {
				return fmt.Errorf("the checkout at %s has uncommitted changes; commit or stash them, or run `yolo update --autostash` to set them aside for the update and restore them after", ch.SourceDir)
			}
			stash := Step{Argv: []string{"git", "stash", "push", "--include-untracked", "-m", AutostashMessage}, Dir: ch.SourceDir}
			if err := runStep(ctx, run, stash, stdout, stderr); err != nil {
				return err
			}
			defer func() {
				pop := Step{Argv: []string{"git", "stash", "pop"}, Dir: ch.SourceDir}
				if perr := runStep(ctx, run, pop, stdout, stderr); perr != nil {
					perr = fmt.Errorf("%w — your changes are safe in `git stash list` as %q", perr, AutostashMessage)
					// retErr, not err: the block above shadows err, and a pop
					// failure written there would be silently dropped.
					retErr = errors.Join(retErr, perr)
				}
			}()
		}
	}
	for _, s := range steps {
		if err := runStep(ctx, run, s, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}

func runStep(ctx context.Context, run Runner, s Step, stdout, stderr io.Writer) error {
	fmt.Fprintf(stderr, "→ %s\n", s)
	if err := run(ctx, s, stdout, stderr); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(s.Argv, " "), err)
	}
	return nil
}

// Notice is the one line a command prints when s says an update exists.
func Notice(s State) string {
	switch s.Kind {
	case KindSource:
		plural := "s"
		if s.Behind == 1 {
			plural = ""
		}
		return fmt.Sprintf("⬆ yolo-jail: %d new commit%s on %s since this build — run `yolo update`", s.Behind, plural, s.Upstream)
	case KindArchive:
		return fmt.Sprintf("⬆ yolo-jail %s is available (this is %s) — download it from %s", s.Latest, s.Current, ReleasesPage)
	case KindGoInstall, KindPipx, KindUV:
		return fmt.Sprintf("⬆ yolo-jail %s is available (this is %s) — update the host binary and its separate jail source together", s.Latest, s.Current)
	}
	return fmt.Sprintf("⬆ yolo-jail %s is available (this is %s) — run `yolo update`", s.Latest, s.Current)
}
