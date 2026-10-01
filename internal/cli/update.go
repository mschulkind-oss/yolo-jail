package cli

// update.go is the CLI half of internal/selfupdate: the `yolo update` command,
// the hidden `yolo internal update-check` a stale cache spawns, and the hook
// dispatchNative runs after the startup banner — a one-line notice on ordinary
// host commands run at a terminal, and on an interactive launch an offer to
// update and relaunch.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
	"github.com/mschulkind-oss/yolo-jail/internal/updatehint"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

const updateUsage = `Usage: yolo update [--check] [--force] [--autostash] [--from <checkout>]

Check for a newer yolo-jail and, where the installation is self-contained,
install it through the same channel:

  from source (just deploy)   git pull --ff-only && just deploy, in the checkout
                              this binary was built from, installing beside it
  Homebrew                    brew upgrade yolo-jail, keeping the old version
                              installed for the jails already running it
  go install / pipx / uv      check only: these install the host binary without
                              the jail bundle, so update the binary and the
                              YOLO_REPO_ROOT checkout together
  release archive             not updatable in place: prints the download link

A from-source install counts new commits on the checkout's upstream branch that
touch the binary, jail image, or deploy/bundle scripts (documentation-only
commits do not count); every other channel counts a newer published release.
A from-source update refuses a checkout that has left the branch the binary
was built from, and one with uncommitted changes unless --autostash. Its
` + "`just deploy`" + ` restarts the Claude OAuth broker, so any jail using that broker
loses it briefly.

` + sourceTrustHelp + `

Options:
  --check             Only check, and report what an update would install.
                      Does not install; a source check may fetch commit objects
                      without moving a ref or changing ` + "`git status`" + `.
  --force             Run the update even when the check finds nothing newer
                      (or fails).
  --autostash         From source: stash uncommitted changes, deploy a clean
                      build, then restore them — also when the update fails.
  --from <checkout>   From source: update from this checkout instead of the one
                      the binary recorded, e.g. after moving or re-cloning it.
                      A non-check invocation redeploys even when already current,
                      so the next build records the new one; when no upstream can
                      be checked, it deploys the named checkout without pulling.

For release-based installs, host commands check in the background normally once
a day, after a terminal has disclosed the network request. Source installs are
checked only when you run ` + "`yolo update --check`" + ` or ` + "`yolo update`" + `, because their
checkout may be writable from a jail. When a cached update exists, one line is
printed under the banner on a terminal; a safe, self-contained interactive
launch (` + "`yolo`" + `, ` + "`yolo -- <cmd>`" + `, ` + "`yolo host -- <cmd>`" + `) offers to update and relaunch.
An explicit YOLO_REPO_ROOT naming another checkout suppresses that offer and
must be updated together with the host binary.

To turn the background check, notice and offer off: "update_check": false in
~/.config/yolo-jail/config.jsonc, or YOLO_NO_UPDATE_CHECK=1 for one shell. They
never run in a jail or in CI. ` + "`yolo check`" + ` reports the last answer.

Examples:
  yolo update --check                 # check now; do not install
  yolo update                         # apply when this install is self-contained
  yolo update --autostash             # from source, with local edits set aside`

// sourceTrustHelp says what a from-source check or update executes. The
// checkout is an ordinary directory, and when a jail uses it as its workspace
// the agent inside can write its git config, hooks and Justfile, all of which
// run on the host as the user here.
const sourceTrustHelp = `On a from-source install, both the check and the update run git in the
checkout with that checkout's own git config and hooks, and the update also
runs its Justfile. If a jail uses that checkout as its workspace, the agent in
it can change what they run; listing .git/config, .git/hooks, .git/info and
Justfile in the checkout's workspace_readonly makes them read-only in the jail.`

// sourcePromptTrust is the launch prompt's shorter form of sourceTrustHelp.
const sourcePromptTrust = "  The update runs git with this checkout's own git config and hooks, and runs its Justfile.\n" +
	"  If a jail uses this checkout as its workspace, protect them with workspace_readonly (see `yolo update --help`)."

// updateDeps are the update surface's side effects, injected for tests.
type updateDeps struct {
	getenv        func(string) string
	unsetenv      func(string) error
	environ       func() []string
	configEnabled func() bool
	current       func() selfupdate.Channel
	statePath     string
	now           func() time.Time
	spawn         func(exe, statePath string, now time.Time) error
	// stderrTTY gates the notice and the disclosure; interactive gates the
	// prompt, which also needs a terminal to read the answer from.
	stderrTTY   func() bool
	interactive func() bool
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	check       func(ctx context.Context, ch selfupdate.Channel, prev selfupdate.State) selfupdate.State
	apply       func(ctx context.Context, ch selfupdate.Channel, opts selfupdate.ApplyOptions, stdout, stderr io.Writer) error
	versionAt   func(path string) (string, error)
	exec        func(path string, argv, env []string) error
	lookPath    func(string) (string, error)
	argv        []string
}

func defaultUpdateDeps() updateDeps {
	return updateDeps{
		getenv:        os.Getenv,
		unsetenv:      os.Unsetenv,
		environ:       os.Environ,
		configEnabled: config.UpdateCheckEnabled,
		current:       selfupdate.Current,
		statePath:     selfupdate.StatePath(),
		now:           time.Now,
		spawn:         selfupdate.SpawnBackgroundCheck,
		stderrTTY:     func() bool { return isTTY(os.Stderr) },
		interactive:   func() bool { return isTTY(os.Stdin) && isTTY(os.Stderr) },
		stdin:         os.Stdin,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
		check: func(ctx context.Context, ch selfupdate.Channel, prev selfupdate.State) selfupdate.State {
			return selfupdate.Check(ctx, ch, prev, selfupdate.DefaultCheckDeps())
		},
		apply: func(ctx context.Context, ch selfupdate.Channel, opts selfupdate.ApplyOptions, stdout, stderr io.Writer) error {
			return selfupdate.Apply(ctx, ch, opts, selfupdate.DefaultCheckDeps().Git, selfupdate.RunStep, stdout, stderr)
		},
		versionAt: installedVersion,
		exec:      syscall.Exec,
		lookPath:  exec.LookPath,
		argv:      os.Args,
	}
}

// updateHook runs in dispatchNative after the banner. A var so dispatch tests
// can observe the call without a real channel.
var updateHook = func(sub string, args []string) (int, bool) {
	return maybeNotifyUpdate(sub, args, defaultUpdateDeps())
}

// updateDisclosure is printed once per machine, the first time a check is
// about to run, so nobody learns from a firewall log that yolo phones home.
const updateDisclosure = "ℹ yolo checks GitHub's releases API for a newer version, normally once a day.\n" +
	"  Turn it off: \"update_check\": false in ~/.config/yolo-jail/config.jsonc."

// maybeNotifyUpdate prints the update notice when the cached check says one
// exists, starts a release-channel background check when the cache is stale,
// and on an interactive launch offers to update and relaunch. It reports (exit
// code, true) only when the process must stop here: the relaunch failed after a
// successful update, so continuing would run the old binary against the new
// install.
//
// Only the CACHE is read here. The network is touched by the detached check
// alone, so a command's startup never waits on it.
//
// # Why the notice is gated on a terminal and the banner is not
//
// The banner goes to every stderr because a pasted bug report must carry the
// version (internal/banner). The notice has no such job, and the callers whose
// stderr is a contract — a wrapper grepping it, a harness diffing it, an editor
// showing it as an error — are exactly the ones without a terminal. The gate
// keeps YOLO_NO_BANNER what its tests pin it to: the version line, nothing else.
func maybeNotifyUpdate(sub string, args []string, d updateDeps) (int, bool) {
	// Read-and-clear, so neither the jail nor anything this process runs
	// inherits the marker.
	reexeced := d.getenv(selfupdate.ReexecEnv) != ""
	if reexeced {
		_ = d.unsetenv(selfupdate.ReexecEnv)
	}
	if sub == "update" || subcommandHelpRequested(sub, args) ||
		!selfupdate.Enabled(d.getenv) || !d.configEnabled() {
		return 0, false
	}
	ch := d.current()
	if ch.Kind == selfupdate.KindUnknown {
		return 0, false
	}
	now := d.now()
	st := selfupdate.LoadState(d.statePath)
	tty := d.stderrTTY()
	if !st.Fresh(ch, now) {
		// A source checkout can be writable from a jail, including its untracked
		// .git/config. Never execute the updater's remote Git check against it
		// merely because another host command ran; source checks are explicit-only.
		if ch.Kind == selfupdate.KindSource {
			return 0, false
		}
		// The first automatic network request waits until its disclosure has a
		// terminal to land on. Once disclosed, later checks may run from scripts.
		if !st.Disclosed {
			if !tty {
				return 0, false
			}
			fmt.Fprintln(d.stderr, updateDisclosure)
			st.Disclosed = true
			// Saved BEFORE the spawn, so the check that reads it keeps it.
			_ = selfupdate.SaveState(d.statePath, st)
		}
		_ = d.spawn(ch.Exe, d.statePath, now)
		return 0, false
	}
	if !st.UpdateFor(ch) || !tty {
		return 0, false
	}
	fmt.Fprintln(d.stderr, selfupdate.Notice(st))

	launch := sub == "run" || (sub == "host" && slices.Contains(args, "--"))
	if !launch || reexeced || !ch.CanApply() || !st.ShouldPrompt() || !d.interactive() {
		return 0, false
	}
	if err := updateRootConflict(ch, d.getenv("YOLO_REPO_ROOT")); err != nil {
		fmt.Fprintf(d.stderr, "  Update not offered: %v\n", err)
		return 0, false
	}
	// Say what "yes" costs before asking, not after.
	if ch.Kind == selfupdate.KindSource {
		fmt.Fprintf(d.stderr, "  Source checkout: %s\n", ch.SourceDir)
		fmt.Fprintln(d.stderr, sourcePromptTrust)
		fmt.Fprintln(d.stderr, "  `just deploy` restarts the Claude OAuth broker; any jail using it loses it briefly.")
	}
	fmt.Fprintln(d.stderr, "  "+selfupdate.RunningJailsNote(ch))
	fmt.Fprintln(d.stderr, "  The relaunch rebuilds the jail image first if the update changed it.")
	fmt.Fprint(d.stderr, "  Update now and relaunch? [y/N] ")
	if answer := readAnswer(d.stdin); answer != "y" && answer != "yes" {
		st.DeclinedFor = st.Latest
		_ = selfupdate.SaveState(d.statePath, st)
		fmt.Fprintln(d.stderr, "  Not now — `yolo update` installs it whenever you're ready.")
		return 0, false
	}
	if err := d.apply(context.Background(), ch, selfupdate.ApplyOptions{}, d.stderr, d.stderr); err != nil {
		fmt.Fprintf(d.stderr, "✗ update failed: %v\n  Continuing with this version.\n", err)
		return 0, false
	}
	path, err := relaunchPath(d.argv[0], d.lookPath)
	if err != nil {
		fmt.Fprintf(d.stderr, "✗ updated, but could not relaunch (%v) — run your command again\n", err)
		return 1, true
	}
	if err := verifyInstalledRelease(ch, st, path, d.versionAt); err != nil {
		// The installed yolo is exactly the one running, so nothing changed
		// under this process and the launch can go on. Recording the decline
		// keeps every later launch from running the same no-op upgrade and
		// asking again, until something newer than this release appears.
		var same installUnchangedError
		if errors.As(err, &same) {
			st.DeclinedFor = st.Latest
			_ = selfupdate.SaveState(d.statePath, st)
			fmt.Fprintf(d.stderr, "⚠ %v\n  Continuing with this version; launches will not offer %s again, and `yolo update` installs it once it is published.\n", err, st.Latest)
			return 0, false
		}
		fmt.Fprintf(d.stderr, "✗ update could not be verified: %v\n  Run your command again after resolving this.\n", err)
		return 1, true
	}
	_ = selfupdate.InvalidateState(d.statePath)
	fmt.Fprintln(d.stderr, "✓ updated — relaunching")
	// Nothing has read a pack yet at this point, so there is normally no
	// lease to give back; exec skips every defer, so give it back anyway.
	packload.ReleaseEmbedded()
	err = d.exec(path, d.argv, append(d.environ(), selfupdate.ReexecEnv+"=1"))
	fmt.Fprintf(d.stderr, "✗ updated, but could not relaunch (%v) — run your command again\n", err)
	return 1, true
}

// relaunchPath resolves the binary to re-exec the way the shell did: argv[0] as
// typed when it holds a slash, else a PATH lookup. Not os.Executable: a
// Homebrew upgrade leaves the OLD version at the versioned Cellar path it ran from.
func relaunchPath(argv0 string, lookPath func(string) (string, error)) (string, error) {
	if strings.Contains(argv0, "/") {
		return argv0, nil
	}
	return lookPath(argv0)
}

// readAnswer reads one line and normalizes it; EOF reads as "no".
func readAnswer(r io.Reader) string {
	line, _ := bufio.NewReader(r).ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line))
}

func installedVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	v, ok := strings.CutPrefix(line, "yolo-jail ")
	if !ok || v == "" || strings.Contains(v, "\n") {
		return "", fmt.Errorf("unexpected `yolo --version` output %q", line)
	}
	return v, nil
}

func verifyInstalledRelease(ch selfupdate.Channel, st selfupdate.State, path string, versionAt func(string) (string, error)) error {
	if ch.Kind != selfupdate.KindHomebrew || !st.Available {
		return nil
	}
	if versionAt == nil {
		return fmt.Errorf("no installed-version probe is available")
	}
	got, err := versionAt(path)
	if err != nil {
		return fmt.Errorf("could not verify the updated yolo at %s: %w", path, err)
	}
	if !selfupdate.AtLeast(got, st.Latest) {
		if got == ch.Version {
			return installUnchangedError{fmt.Sprintf("the update command completed, but %s is still %s (expected at least %s); the Homebrew formula may not be published yet", path, got, st.Latest)}
		}
		return fmt.Errorf("the update command completed, but %s is now %s (this yolo is %s; expected at least %s)", path, got, ch.Version, st.Latest)
	}
	return nil
}

// installUnchangedError is verifyInstalledRelease's answer when the upgrade
// left the installed version exactly where it was: nothing changed, so a
// caller may carry on with the running binary.
type installUnchangedError struct{ msg string }

func (e installUnchangedError) Error() string { return e.msg }

func updateRootConflict(ch selfupdate.Channel, root string) error {
	if root == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, "flake.nix")); err != nil {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
			return nil // reporoot.Resolve ignores an invalid override too
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if ch.Kind == selfupdate.KindSource && abs == ch.SourceDir {
		return nil
	}
	return fmt.Errorf("YOLO_REPO_ROOT=%s overrides this install's bundle; update that checkout together with the host binary, or unset YOLO_REPO_ROOT to use the installation's own bundle", root)
}

func runUpdate(args []string) int {
	if answerHelp("update", args, os.Stdout) {
		return 0
	}
	return updateMain(args, defaultUpdateDeps())
}

// updateMain is `yolo update`'s body: a fresh check (never the cache — the
// person asked NOW), a report, and unless --check the channel's update.
// update_check silences unasked-for checks and notices, not this command.
func updateMain(args []string, d updateDeps) int {
	checkOnly, force := false, false
	var opts selfupdate.ApplyOptions
	var from string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "update":
		case a == "--check":
			checkOnly = true
		case a == "--force":
			force = true
		case a == "--autostash":
			opts.Autostash = true
		case a == "--from":
			if i+1 >= len(args) {
				fmt.Fprintln(d.stderr, "yolo update: --from needs a checkout path")
				return 2
			}
			i++
			from = args[i]
		case strings.HasPrefix(a, "--from="):
			from = strings.TrimPrefix(a, "--from=")
			if from == "" {
				fmt.Fprintln(d.stderr, "yolo update: --from needs a checkout path")
				return 2
			}
		default:
			fmt.Fprintf(d.stderr, "yolo update: unknown argument %q (see `yolo update --help`)\n", a)
			return 2
		}
	}
	if d.getenv("YOLO_VERSION") != "" {
		// The jail keeps the yolo it was launched with, so an update on the host alone leaves
		// this jail on the old one: the step is the host's update and a relaunch, in the words
		// every refusal of a newer yolo's file uses (updatehint).
		fmt.Fprintln(d.stderr, "yolo update: this is the jail's copy of the host's yolo, which the jail keeps "+
			"until it is relaunched; "+updatehint.InJailStep)
		return 1
	}
	ch := d.current()
	if from != "" {
		var err error
		if ch, err = fromCheckout(ch, from); err != nil {
			fmt.Fprintf(d.stderr, "yolo update: %v\n", err)
			return 1
		}
		if !checkOnly {
			// --from is also the recovery for a moved/re-cloned checkout. It
			// must redeploy even when that checkout is already at this binary's
			// commit, or the binary never records the new path.
			force = true
		}
	}
	if ch.Kind == selfupdate.KindUnknown {
		if ch.MissingSourceDir != "" {
			fmt.Fprintf(d.stderr, "yolo update: this yolo was built from %s, which no longer contains the yolo-jail checkout.\n"+
				"  If you moved or re-cloned it: yolo update --from <checkout>\n", ch.MissingSourceDir)
			return 1
		}
		fmt.Fprintf(d.stderr, "yolo update: cannot tell how this yolo (%s) was installed, so it cannot update itself.\n"+
			"  Reinstall it from %s\n", ch.Exe, selfupdate.ReleasesPage)
		return 1
	}
	if opts.Autostash && ch.Kind != selfupdate.KindSource {
		fmt.Fprintf(d.stderr, "yolo update: --autostash is only valid for a from-source install (this yolo is %s)\n", ch.Kind)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	st := d.check(ctx, ch, selfupdate.LoadState(d.statePath))
	cancel()
	_ = selfupdate.SaveState(d.statePath, st)
	if from != "" && st.Error != "" {
		opts.SkipPull = true
	}

	where := ch.Exe
	if ch.Kind == selfupdate.KindSource {
		where = ch.SourceDir + " → " + ch.Exe
	}
	fmt.Fprintf(d.stdout, "Installed via: %s (%s)\n", ch.Kind, where)
	switch {
	case st.Error != "":
		fmt.Fprintf(d.stdout, "⚠ could not check for updates: %s\n", st.Error)
	case st.Available:
		fmt.Fprintln(d.stdout, strings.TrimSuffix(selfupdate.Notice(st), " — run `yolo update`"))
	default:
		fmt.Fprintf(d.stdout, "✓ up to date (%s)\n", ch.Version)
	}
	if checkOnly {
		if st.Error != "" {
			return 1
		}
		return 0
	}
	if !st.Available && !force {
		if st.Error != "" {
			fmt.Fprintln(d.stdout, "  Pass --force to run the update anyway.")
			return 1
		}
		return 0
	}
	if opts.SkipPull {
		fmt.Fprintln(d.stdout, "  Deploying the named checkout as-is; no pull will run.")
	}
	if !ch.CanApply() {
		_, err := selfupdate.Plan(ch)
		fmt.Fprintf(d.stderr, "yolo update: %v\n", err)
		return 1
	}
	if err := updateRootConflict(ch, d.getenv("YOLO_REPO_ROOT")); err != nil {
		fmt.Fprintf(d.stderr, "yolo update: %v\n", err)
		return 1
	}
	if err := d.apply(context.Background(), ch, opts, d.stderr, d.stderr); err != nil {
		fmt.Fprintf(d.stderr, "✗ update failed: %v\n", err)
		return 1
	}
	if ch.Kind == selfupdate.KindHomebrew && st.Available {
		path, err := relaunchPath(d.argv[0], d.lookPath)
		if err != nil {
			fmt.Fprintf(d.stderr, "✗ update completed, but the installed yolo could not be located: %v\n", err)
			return 1
		}
		if err := verifyInstalledRelease(ch, st, path, d.versionAt); err != nil {
			fmt.Fprintf(d.stderr, "✗ update could not be verified: %v\n", err)
			return 1
		}
	}
	_ = selfupdate.InvalidateState(d.statePath)
	fmt.Fprintln(d.stdout, "✓ updated. "+selfupdate.RunningJailsNote(ch))
	return 0
}

// fromCheckout turns `--from <dir>` into the source channel to update through.
// It is refused for a package-manager install, whose binary is that manager's
// to replace: a source deploy "beside" it would write into the manager's tree.
// An unidentified install is refused for the same reason unless its binary
// records that it was built from source (MissingSourceDir): nothing else says
// the directory it runs from is one a source deploy may write into.
func fromCheckout(ch selfupdate.Channel, dir string) (selfupdate.Channel, error) {
	switch {
	case ch.Kind == selfupdate.KindSource:
	case ch.Kind == selfupdate.KindUnknown && ch.MissingSourceDir != "":
	case ch.Kind == selfupdate.KindUnknown:
		return ch, fmt.Errorf("--from is for from-source installs, and nothing shows this yolo (%s) was built from source; reinstall it from %s", ch.Exe, selfupdate.ReleasesPage)
	default:
		return ch, fmt.Errorf("--from is for from-source installs; this yolo is managed by %s (see `yolo update --help` for that channel's safe update path)", ch.Kind)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ch, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return ch, fmt.Errorf("--from %s: not a git checkout", dir)
	}
	if !selfupdate.IsSourceCheckout(abs) {
		return ch, fmt.Errorf("--from %s: not a yolo-jail checkout (no `module %s` in go.mod)", dir, selfupdate.Module)
	}
	// The stamped commit still counts from the right place if the new checkout
	// shares history; checkSource falls back to HEAD when it does not. No branch
	// is enforced: naming the checkout is naming what to deploy.
	return selfupdate.Channel{Kind: selfupdate.KindSource, Exe: ch.Exe, SourceDir: abs, Version: version.GitCommit}, nil
}

// internalCheckChannel and internalCheckReleaseURL are runInternalUpdateCheck's
// two seams, so a test can run the real verb end to end against a local
// release server without an installed binary.
var (
	internalCheckChannel    = selfupdate.Current
	internalCheckReleaseURL = selfupdate.LatestReleaseAPI
)

// runInternalUpdateCheck is the detached process SpawnBackgroundCheck starts. It
// writes the cache and releases the lock the spawner took. It prints nothing:
// no one is reading its stdio.
func runInternalUpdateCheck(args []string) int {
	statePath := selfupdate.StatePath()
	for i := 0; i < len(args); i++ {
		if args[i] == "--state" && i+1 < len(args) {
			statePath = args[i+1]
			i++
		}
	}
	defer os.Remove(selfupdate.LockPath(statePath))
	ch := internalCheckChannel()
	if ch.Kind == selfupdate.KindUnknown || ch.Kind == selfupdate.KindSource ||
		!selfupdate.Enabled(os.Getenv) || !config.UpdateCheckEnabled() {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	deps := selfupdate.DefaultCheckDeps()
	deps.ReleaseURL = internalCheckReleaseURL
	st := selfupdate.Check(ctx, ch, selfupdate.LoadState(statePath), deps)
	if err := selfupdate.SaveState(statePath, st); err != nil {
		return 1
	}
	return 0
}
