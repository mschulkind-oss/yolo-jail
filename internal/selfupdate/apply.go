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
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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

// BrewKeepOldKegEnv keeps `brew upgrade` from deleting the version it replaces.
// By default Homebrew removes the old Cellar keg right after an upgrade, and a
// running jail bind-mounts its binaries and flake bundle from paths that
// resolve into that keg (internal/cli/run/jailprefix.go), so
// the upgrade would delete pid1 out from under every running jail. With it set,
// the old keg stays until a `brew cleanup` removes it.
const BrewKeepOldKegEnv = "HOMEBREW_NO_INSTALL_CLEANUP=1"

// SnapshotSummary and SnapshotSteps are what every surface says about a
// snapshot install (KindSnapshot): `yolo update`, `yolo update --check` and
// `yolo check` share them word for word. A snapshot of an arbitrary commit has
// no release to compare against and no checkout it may follow, so the steps are
// the two ways its owner deploys a checkout: by running the install there, or
// by naming it (docs/design/installed-launcher-provenance.md, OQ-IP1).
const (
	SnapshotSummary = "this yolo is a snapshot of a checkout, installed by `just install`, so there is no release to compare it against and `yolo update` changes nothing"
	SnapshotSteps   = "To update it, run `just install` in your yolo-jail checkout, or `yolo update --from <checkout>`."
)

// CheckoutRunningJailsNote says what `yolo update --from` does to jails that
// are already running.
const CheckoutRunningJailsNote = "Running jails keep their binaries until you restart them: `just deploy` installs a new bundle beside the one they use."

// RunningJailsNote says what an update through ch does to jails that are
// already running, for the channels `yolo update` installs. It is "" for the
// rest.
func RunningJailsNote(ch Channel) string {
	if ch.Kind == KindHomebrew {
		return "The previous version stays installed, so running jails keep their binaries until you restart them; `brew cleanup yolo-jail` removes it after that."
	}
	return ""
}

// Plan returns the commands that update ch, in order.
func Plan(ch Channel) ([]Step, error) {
	switch ch.Kind {
	case KindHomebrew:
		return []Step{{Argv: []string{"brew", "upgrade", "yolo-jail"}, Env: []string{BrewKeepOldKegEnv}}}, nil
	case KindSnapshot:
		return nil, fmt.Errorf("%s. %s", SnapshotSummary, SnapshotSteps)
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
	return fmt.Errorf("%s installs only the host binary, without the build files each jail is made from, so `yolo update` does not run `%s`: it would update the binary alone. %s", kind, command, reporoot.ReinstallSteps)
}

// Apply updates ch by running Plan's steps, stopping at the first failure.
func Apply(ctx context.Context, ch Channel, run Runner, stdout, stderr io.Writer) error {
	steps, err := Plan(ch)
	if err != nil {
		return err
	}
	for _, s := range steps {
		if err := runStep(ctx, run, s, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}

// CheckoutOptions are the choices `yolo update --from` passes through.
type CheckoutOptions struct {
	// Autostash lets the deploy proceed over uncommitted changes: they are
	// stashed first, `just deploy` builds the checkout's committed HEAD, and
	// they are restored afterwards — also when the deploy fails.
	Autostash bool
}

// AutostashMessage names the stash an autostash deploy makes, so a person can
// find it in `git stash list` if restoring it fails.
const AutostashMessage = "yolo update autostash"

// PlanCheckout returns the commands `yolo update --from dir` runs: `just
// deploy` in the named checkout, exactly as it stands — no fetch, no pull, no
// branch check, because naming the checkout is naming what to deploy.
//
// The deploy is pinned to the directory the running binary is in. Without that
// it follows whatever GOBIN the toolchain manager exports — a per-Go-version
// directory under mise, which is not on PATH — and the update "succeeds" into a
// binary nothing runs. It is pinned to the running binary's NAME too: a launcher
// deployed as `just deploy yolo-stable` redeploys itself, not the `yolo` beside it.
func PlanCheckout(exe, dir string) []Step {
	argv := []string{"just", "deploy"}
	if name := filepath.Base(exe); name != "yolo" && name != "." && name != string(filepath.Separator) {
		argv = append(argv, name)
	}
	return []Step{{Argv: argv, Dir: dir, Env: []string{"GOBIN=" + filepath.Dir(exe)}}}
}

// DeployCheckout runs PlanCheckout's steps for `yolo update --from dir`. A
// checkout with uncommitted changes is refused unless opts.Autostash: the
// install snapshots the working tree, so a deploy over half-finished edits
// would install them, and naming a checkout is not the same as asking for that.
func DeployCheckout(ctx context.Context, exe, dir string, opts CheckoutOptions, git GitRunner, run Runner, stdout, stderr io.Writer) (retErr error) {
	dirty, err := git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status in %s: %w", dir, err)
	}
	if dirty != "" {
		if !opts.Autostash {
			return fmt.Errorf("the checkout at %s has uncommitted changes; commit or stash them, run `yolo update --from %s --autostash` to set them aside for the deploy and restore them after, or run `just install` there to install them as they are", dir, shquote.Quote(dir))
		}
		stash := Step{Argv: []string{"git", "stash", "push", "--include-untracked", "-m", AutostashMessage}, Dir: dir}
		if err := runStep(ctx, run, stash, stdout, stderr); err != nil {
			return err
		}
		defer func() {
			pop := Step{Argv: []string{"git", "stash", "pop"}, Dir: dir}
			if perr := runStep(ctx, run, pop, stdout, stderr); perr != nil {
				perr = fmt.Errorf("%w — your changes are safe in `git stash list` as %q", perr, AutostashMessage)
				retErr = errors.Join(retErr, perr)
			}
		}()
	}
	for _, s := range PlanCheckout(exe, dir) {
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
	case KindArchive:
		return fmt.Sprintf("⬆ yolo-jail %s is available (this is %s) — download it from %s", s.Latest, s.Current, ReleasesPage)
	case KindGoInstall, KindPipx, KindUV:
		return fmt.Sprintf("⬆ yolo-jail %s is available (this is %s) — this install cannot update itself; reinstall from Homebrew or a release archive: %s", s.Latest, s.Current, ReleasesPage)
	}
	return fmt.Sprintf("⬆ yolo-jail %s is available (this is %s) — run `yolo update`", s.Latest, s.Current)
}
