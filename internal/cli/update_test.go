package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
	"github.com/mschulkind-oss/yolo-jail/internal/updatehint"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// The CALL SITE, pinned: deleting the hook from dispatchNative fails this, which
// no test of maybeNotifyUpdate alone could notice.
func TestDispatchRunsTheUpdateHook(t *testing.T) {
	orig := updateHook
	t.Cleanup(func() { updateHook = orig })
	t.Setenv("YOLO_NO_BANNER", "1")

	var saw [][]string
	updateHook = func(sub string, args []string) (int, bool) {
		saw = append(saw, append([]string{sub}, args...))
		return 0, false
	}
	if rc := dispatchNative("update", []string{"update", "--help"}); rc != 0 {
		t.Fatalf("update --help exited %d", rc)
	}
	if len(saw) != 1 || !slices.Equal(saw[0], []string{"update", "update", "--help"}) {
		t.Errorf("hook saw %v, want the sub and its args", saw)
	}

	// A hook that has to stop the process (a relaunch failed after an update)
	// must stop it before the handler runs against the new install.
	updateHook = func(string, []string) (int, bool) { return 7, true }
	if rc := dispatchNative("update", []string{"update", "--help"}); rc != 7 {
		t.Errorf("dispatchNative returned %d, want the hook's 7", rc)
	}
}

var testChannel = selfupdate.Channel{Kind: selfupdate.KindHomebrew, Exe: "/opt/homebrew/Cellar/yolo-jail/0.10.0/bin/yolo", Version: "0.10.0"}
var snapshotTestChannel = selfupdate.Channel{Kind: selfupdate.KindSnapshot, Exe: "/home/u/go/bin/yolo", Version: "0.10.0+510.gcfefa8bf"}

type updateHarness struct {
	d          updateDeps
	env        map[string]string
	stderr     bytes.Buffer
	stdout     bytes.Buffer
	spawned    int
	applied    int
	applyErr   error
	deployed   int
	deployExe  string
	deployDir  string
	deployOpts selfupdate.CheckoutOptions
	deployErr  error
	version    string
	versionErr error
	execPath   string
	execArgv   []string
	execEnv    []string
}

func newUpdateHarness(t *testing.T, ch selfupdate.Channel) *updateHarness {
	t.Helper()
	h := &updateHarness{env: map[string]string{}, version: "0.11.0"}
	h.d = updateDeps{
		getenv:        func(k string) string { return h.env[k] },
		unsetenv:      func(k string) error { delete(h.env, k); return nil },
		environ:       func() []string { return []string{"HOME=/home/u"} },
		configEnabled: func() bool { return true },
		current:       func() selfupdate.Channel { return ch },
		statePath:     filepath.Join(t.TempDir(), "state.json"),
		now:           func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) },
		spawn:         func(string, string, time.Time) error { h.spawned++; return nil },
		stderrTTY:     func() bool { return true },
		interactive:   func() bool { return true },
		stdin:         strings.NewReader(""),
		stdout:        &h.stdout,
		stderr:        &h.stderr,
		check: func(_ context.Context, ch selfupdate.Channel, _ selfupdate.State) selfupdate.State {
			return selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Current: ch.Version, CheckedAt: time.Now()}
		},
		apply: func(_ context.Context, _ selfupdate.Channel, _, _ io.Writer) error {
			h.applied++
			return h.applyErr
		},
		deploy: func(_ context.Context, exe, dir string, opts selfupdate.CheckoutOptions, _, _ io.Writer) error {
			h.deployed++
			h.deployExe, h.deployDir, h.deployOpts = exe, dir, opts
			return h.deployErr
		},
		versionAt: func(string) (string, error) {
			return h.version, h.versionErr
		},
		exec: func(path string, argv, env []string) error {
			h.execPath, h.execArgv, h.execEnv = path, argv, env
			return errors.New("exec is faked")
		},
		lookPath: func(name string) (string, error) { return "/opt/homebrew/bin/" + name, nil },
		argv:     []string{"yolo", "--", "claude"},
	}
	return h
}

// seed caches a fresh "an update is available" answer for ch.
func (h *updateHarness) seed(t *testing.T, ch selfupdate.Channel, mutate func(*selfupdate.State)) {
	t.Helper()
	st := selfupdate.State{
		CheckedAt: h.d.now().Add(-time.Hour), Identity: ch.Identity(), Kind: ch.Kind,
		Current: ch.Version, Latest: "0.11.0", Available: true, Disclosed: true,
	}
	if mutate != nil {
		mutate(&st)
	}
	if err := selfupdate.SaveState(h.d.statePath, st); err != nil {
		t.Fatal(err)
	}
}

func (h *updateHarness) notify(sub string, args ...string) (int, bool) {
	return maybeNotifyUpdate(sub, append([]string{sub}, args...), h.d)
}

func TestUpdateNoticeStaleCacheDisclosesOnceThenSpawnsQuietly(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.notify("check")
	if h.spawned != 1 {
		t.Errorf("spawned %d background checks, want 1", h.spawned)
	}
	if !strings.Contains(h.stderr.String(), "checks GitHub's releases API for a newer version, normally once a day") {
		t.Errorf("the first check must be disclosed: %q", h.stderr.String())
	}
	if !selfupdate.LoadState(h.d.statePath).Disclosed {
		t.Error("the disclosure must be recorded before the spawn")
	}

	h.stderr.Reset()
	h.notify("check")
	if h.stderr.Len() != 0 {
		t.Errorf("disclosed twice: %q", h.stderr.String())
	}
	if h.spawned != 2 {
		t.Errorf("a still-stale cache must spawn again (spawned %d)", h.spawned)
	}
}

func TestUpdateNoticeFreshCacheNeitherSpawnsNorPrompts(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.notify("check")
	if h.spawned != 0 {
		t.Errorf("a fresh cache must not spawn a check (spawned %d)", h.spawned)
	}
	if !strings.Contains(h.stderr.String(), "yolo-jail 0.11.0 is available") {
		t.Errorf("notice missing: %q", h.stderr.String())
	}
	if strings.Contains(h.stderr.String(), "relaunch?") {
		t.Error("only a launch prompts; `check` must not")
	}
}

func TestStaleCachedUpdateIsNotOfferedBeforeRefresh(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, func(s *selfupdate.State) {
		s.CheckedAt = h.d.now().Add(-25 * time.Hour)
	})
	h.d.stdin = strings.NewReader("y\n")
	h.notify("run")
	if h.spawned != 1 || h.applied != 0 {
		t.Errorf("stale answer spawned=%d applied=%d; want refresh only", h.spawned, h.applied)
	}
	if strings.Contains(h.stderr.String(), "available") || strings.Contains(h.stderr.String(), "relaunch?") {
		t.Errorf("a stale answer was presented as current:\n%s", h.stderr.String())
	}
}

func TestDevelopmentBundleSkipsAutomaticUpdateChecksNoticesAndPrompts(t *testing.T) {
	ch := selfupdate.Channel{Kind: selfupdate.KindDevelopment, Exe: "/home/u/dev-bundles/dirty/bin/yolo", Version: "0.10.0"}
	h := newUpdateHarness(t, ch)
	h.d.stdin = strings.NewReader("y\n")
	h.notify("run")
	if h.spawned != 0 || h.applied != 0 {
		t.Errorf("automatic update path spawned=%d applied=%d, want 0/0", h.spawned, h.applied)
	}
	if h.stderr.Len() != 0 {
		t.Errorf("automatic update path disclosed, noticed, or prompted: %q", h.stderr.String())
	}

	h.seed(t, ch, nil)
	h.stderr.Reset()
	h.notify("run")
	if h.spawned != 0 || h.applied != 0 || h.stderr.Len() != 0 {
		t.Errorf("fresh automatic cache caused side effects: spawned=%d applied=%d stderr=%q", h.spawned, h.applied, h.stderr.String())
	}
}

func TestExplicitUpdateCheckStillRunsForDevelopmentBundle(t *testing.T) {
	ch := selfupdate.Channel{Kind: selfupdate.KindDevelopment, Exe: "/home/u/dev-bundles/dirty/bin/yolo", Version: "0.10.0"}
	h := newUpdateHarness(t, ch)
	h.d.check = func(_ context.Context, got selfupdate.Channel, _ selfupdate.State) selfupdate.State {
		return selfupdate.State{Identity: got.Identity(), Kind: got.Kind, Current: got.Version, Latest: "0.11.0", Available: true, CheckedAt: time.Now()}
	}
	if rc := updateMain([]string{"update", "--check"}, h.d); rc != 0 {
		t.Fatalf("explicit development update --check exit = %d, want 0: %s", rc, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "0.11.0 is available") {
		t.Errorf("explicit update check did not run: %q", h.stdout.String())
	}
}

// The notice's gate: a stderr that is a contract (a wrapper, a harness, an
// editor) is not a terminal, and it gets no notice — while YOLO_NO_BANNER stays
// the version line and nothing else.
func TestUpdateNoticeOnlyOnATerminal(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, func(s *selfupdate.State) { s.CheckedAt = time.Time{}; s.Disclosed = false })
	h.d.stderrTTY = func() bool { return false }
	h.notify("run")
	if h.stderr.Len() != 0 {
		t.Errorf("printed to a non-terminal stderr: %q", h.stderr.String())
	}
	if h.spawned != 0 {
		t.Error("the first automatic check must wait until its disclosure has a terminal")
	}
	if selfupdate.LoadState(h.d.statePath).Disclosed {
		t.Error("an undisplayed disclosure must not be recorded as shown")
	}

	h.d.stderrTTY = func() bool { return true }
	h.notify("run")
	if h.spawned != 1 || !strings.Contains(h.stderr.String(), "GitHub's releases API") {
		t.Errorf("the first terminal invocation must disclose and spawn: spawned=%d stderr=%q", h.spawned, h.stderr.String())
	}
}

// A snapshot of an arbitrary commit has no release to compare against, so it
// gets no background check, no disclosure, no notice and no prompt — not even
// from a cached answer an earlier channel left behind.
func TestSnapshotGetsNoAutomaticChecksNoticesOrPrompts(t *testing.T) {
	h := newUpdateHarness(t, snapshotTestChannel)
	h.d.stdin = strings.NewReader("y\n")
	h.notify("run")
	if h.spawned != 0 || h.stderr.Len() != 0 {
		t.Errorf("an automatic snapshot check ran: spawned=%d stderr=%q", h.spawned, h.stderr.String())
	}

	h.seed(t, snapshotTestChannel, nil)
	h.notify("run")
	if h.spawned != 0 || h.applied != 0 || h.deployed != 0 || h.stderr.Len() != 0 {
		t.Errorf("a cached answer drove a snapshot notice or prompt: spawned=%d applied=%d deployed=%d stderr=%q",
			h.spawned, h.applied, h.deployed, h.stderr.String())
	}
}

func TestUpdateHookSkipsSubcommandHelp(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("y\n")
	h.notify("run", "--help")
	if h.applied != 0 || h.spawned != 0 || h.stderr.Len() != 0 {
		t.Errorf("run help had update side effects: applied=%d spawned=%d stderr=%q", h.applied, h.spawned, h.stderr.String())
	}
}

func TestUpdateNoticeIsSilentWhenDisabledOrUnidentified(t *testing.T) {
	for name, setup := range map[string]func(h *updateHarness){
		"in a jail":         func(h *updateHarness) { h.env["YOLO_VERSION"] = "0.10.0" },
		"in CI":             func(h *updateHarness) { h.env["CI"] = "true" },
		"opted out, env":    func(h *updateHarness) { h.env[selfupdate.DisableEnv] = "1" },
		"opted out, config": func(h *updateHarness) { h.d.configEnabled = func() bool { return false } },
		"unknown channel": func(h *updateHarness) {
			h.d.current = func() selfupdate.Channel { return selfupdate.Channel{Kind: selfupdate.KindUnknown} }
		},
		"the update command": func(*updateHarness) {}, // sub is "update" below
	} {
		t.Run(name, func(t *testing.T) {
			h := newUpdateHarness(t, testChannel)
			h.seed(t, testChannel, func(s *selfupdate.State) { s.CheckedAt = time.Time{} })
			setup(h)
			sub := "run"
			if name == "the update command" {
				sub = "update"
			}
			h.notify(sub)
			if h.spawned != 0 || h.stderr.Len() != 0 {
				t.Errorf("spawned=%d stderr=%q, want neither", h.spawned, h.stderr.String())
			}
		})
	}
}

func TestUpdatePromptDeclineIsRemembered(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("run")
	if h.applied != 0 {
		t.Error("a no must not update")
	}
	if got := selfupdate.LoadState(h.d.statePath).DeclinedFor; got != "0.11.0" {
		t.Errorf("DeclinedFor = %q, want 0.11.0", got)
	}

	// The next launch shows the notice but does not ask again.
	h.stderr.Reset()
	h.d.stdin = strings.NewReader("y\n")
	h.notify("run")
	if h.applied != 0 || strings.Contains(h.stderr.String(), "relaunch?") {
		t.Errorf("re-prompted for a declined version: %q", h.stderr.String())
	}
}

func TestUpdatePromptYesUpdatesAndRelaunchesTheSameCommand(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("y\n")
	rc, stop := h.notify("run")
	if h.applied != 1 {
		t.Fatalf("applied %d times, want 1", h.applied)
	}
	if h.execPath != "/opt/homebrew/bin/yolo" {
		t.Errorf("relaunched %q, want the PATH lookup of argv[0] (the Cellar path still holds the old version after an upgrade)", h.execPath)
	}
	if !slices.Equal(h.execArgv, []string{"yolo", "--", "claude"}) {
		t.Errorf("relaunched with %v, want the original argv", h.execArgv)
	}
	if !slices.Contains(h.execEnv, selfupdate.ReexecEnv+"=1") {
		t.Errorf("relaunch env %v lacks %s, so the new process would prompt again", h.execEnv, selfupdate.ReexecEnv)
	}
	if got := selfupdate.LoadState(h.d.statePath); got != (selfupdate.State{Disclosed: true}) {
		t.Errorf("the cached answer must be invalidated without losing disclosure: %+v", got)
	}
	// exec is faked to fail, which is the one case that stops the process.
	if !stop || rc != 1 {
		t.Errorf("a failed relaunch returned (%d, %v), want (1, true)", rc, stop)
	}

	h.stderr.Reset()
	h.env[selfupdate.ReexecEnv] = "1"
	h.notify("run")
	if h.spawned != 1 {
		t.Errorf("the new binary must refresh the invalidated cache once, spawned=%d", h.spawned)
	}
	if strings.Contains(h.stderr.String(), "checks GitHub") {
		t.Errorf("the machine-wide disclosure repeated after update: %q", h.stderr.String())
	}
}

func TestUpdatePromptOnAHostLaunchButNotOtherHostVerbs(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("host", "--", "claude")
	if !strings.Contains(h.stderr.String(), "relaunch?") {
		t.Errorf("`yolo host -- claude` is a launch and must be offered the update: %q", h.stderr.String())
	}

	h = newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.notify("host", "apply")
	if strings.Contains(h.stderr.String(), "relaunch?") {
		t.Error("`yolo host apply` is not a launch")
	}
}

func TestUpdatePromptSaysWhatAnUpdateCosts(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("run")
	out := h.stderr.String()
	if !strings.Contains(out, "rebuilds the jail image") {
		t.Errorf("the rebuild cost is not stated:\n%s", out)
	}
	if strings.Index(out, "relaunch?") < strings.Index(out, "rebuilds") {
		t.Error("the cost must be stated BEFORE the question")
	}
	if strings.Contains(out, "OAuth broker") {
		t.Errorf("a Homebrew update does not run just deploy:\n%s", out)
	}
}

// What an update does to jails already running must be said by both the launch
// prompt and `yolo update`'s success line.
func TestUpdateSaysWhatHappensToRunningJailsPerChannel(t *testing.T) {
	for _, c := range []struct {
		ch   selfupdate.Channel
		want string
	}{
		{testChannel, "The previous version stays installed, so running jails keep their binaries until you restart them; `brew cleanup yolo-jail` removes it after that."},
	} {
		t.Run(string(c.ch.Kind), func(t *testing.T) {
			h := newUpdateHarness(t, c.ch)
			h.seed(t, c.ch, nil)
			h.d.stdin = strings.NewReader("n\n")
			h.notify("run")
			if out := h.stderr.String(); !strings.Contains(out, c.want) || strings.Index(out, c.want) > strings.Index(out, "relaunch?") {
				t.Errorf("the launch prompt must state %q before asking:\n%s", c.want, out)
			}

			h = newUpdateHarness(t, c.ch)
			h.d.check = func(_ context.Context, ch selfupdate.Channel, _ selfupdate.State) selfupdate.State {
				return selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Current: ch.Version, Latest: "0.11.0", Available: true}
			}
			if rc := updateMain([]string{"update"}, h.d); rc != 0 {
				t.Fatalf("exit %d: %s", rc, h.stderr.String())
			}
			if !strings.Contains(h.stdout.String(), "✓ updated. ") || !strings.Contains(h.stdout.String(), c.want) {
				t.Errorf("the success line must state %q:\n%s", c.want, h.stdout.String())
			}
		})
	}
}

// `yolo update --from` runs the checkout's own git config, hooks and Justfile on
// the host. `yolo update --help` says what it runs and stops there: building a
// checkout runs what is in it, which is the point of naming it
// (docs/reference/host-execution-from-the-workspace.md, "the product cell"), so
// the help does not advise locking those files from a jail, which would stop the
// jail developing them.
func TestUpdateSaysWhatASourceUpdateRunsWithoutAdvisingALock(t *testing.T) {
	var help bytes.Buffer
	if !answerHelp("update", []string{"update", "--help"}, &help) {
		t.Fatal("`yolo update --help` printed no help")
	}
	for _, want := range []string{"own git config and hooks", "runs its Justfile"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("`yolo update --help` does not say %q:\n%s", want, help.String())
		}
	}
	if strings.Contains(help.String(), "workspace_readonly") {
		t.Errorf("`yolo update --help` advises locking the checkout from a jail:\n%s", help.String())
	}
}

func TestUpdatePromptFailedUpdateContinuesOnThisVersion(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("y\n")
	h.applyErr = errors.New("brew exploded")
	if _, stop := h.notify("run"); stop {
		t.Error("a failed update must fall through to the launch")
	}
	if h.execPath != "" {
		t.Error("must not relaunch after a failed update")
	}
	if !strings.Contains(h.stderr.String(), "brew exploded") {
		t.Errorf("failure not reported: %q", h.stderr.String())
	}
}

// A Homebrew upgrade that changed nothing (the tap has not published the
// release yet) leaves the running binary installed, so the launch continues —
// and remembers, so the next launch does not rerun the same no-op and ask again.
func TestUpdatePromptContinuesWhenTheUpgradeChangedNothing(t *testing.T) {
	h := newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("y\n")
	h.version = testChannel.Version
	rc, stop := h.notify("run")
	if stop || rc != 0 || h.execPath != "" {
		t.Errorf("unchanged install = (%d, %v), exec=%q; want the launch to continue without a relaunch", rc, stop, h.execPath)
	}
	if !strings.Contains(h.stderr.String(), "Homebrew formula may not be published yet") ||
		!strings.Contains(h.stderr.String(), "Continuing with this version") {
		t.Errorf("the no-op is not reported:\n%s", h.stderr.String())
	}
	if got := selfupdate.LoadState(h.d.statePath); got.DeclinedFor != "0.11.0" || !got.Available {
		t.Errorf("state after a no-op = %+v; want DeclinedFor 0.11.0 and the notice kept", got)
	}

	h.stderr.Reset()
	h.d.stdin = strings.NewReader("y\n")
	h.notify("run")
	if h.applied != 1 || strings.Contains(h.stderr.String(), "relaunch?") {
		t.Errorf("the next launch re-offered the same release (applied %d):\n%s", h.applied, h.stderr.String())
	}
}

// An upgrade that DID change the install, but not to the release expected,
// still stops: continuing would run this binary against a different install.
func TestUpdatePromptStopsWhenTheInstalledVersionCannotBeVerified(t *testing.T) {
	for name, setup := range map[string]func(h *updateHarness){
		"changed to something else": func(h *updateHarness) { h.version = "0.10.5" },
		"probe failed":              func(h *updateHarness) { h.versionErr = errors.New("exec format error") },
	} {
		t.Run(name, func(t *testing.T) {
			h := newUpdateHarness(t, testChannel)
			h.seed(t, testChannel, nil)
			h.d.stdin = strings.NewReader("y\n")
			setup(h)
			rc, stop := h.notify("run")
			if !stop || rc != 1 || h.execPath != "" {
				t.Errorf("verification failure = (%d, %v), exec=%q; want stop before relaunch", rc, stop, h.execPath)
			}
			if !strings.Contains(h.stderr.String(), "could not be verified") {
				t.Errorf("verification failure not reported:\n%s", h.stderr.String())
			}
		})
	}
}

func TestUpdatePromptNotOfferedWhenItCannotOrShouldNot(t *testing.T) {
	override := t.TempDir()
	if err := os.WriteFile(filepath.Join(override, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, setup := range map[string]func(h *updateHarness){
		"not interactive":    func(h *updateHarness) { h.d.interactive = func() bool { return false } },
		"already relaunched": func(h *updateHarness) { h.env[selfupdate.ReexecEnv] = "1" },
		"external repo root": func(h *updateHarness) { h.env["YOLO_REPO_ROOT"] = override },
	} {
		t.Run(name, func(t *testing.T) {
			h := newUpdateHarness(t, testChannel)
			h.seed(t, testChannel, nil)
			h.d.stdin = strings.NewReader("y\n")
			setup(h)
			h.notify("run")
			if h.applied != 0 || strings.Contains(h.stderr.String(), "relaunch?") {
				t.Errorf("prompted: %q", h.stderr.String())
			}
			if h.env[selfupdate.ReexecEnv] != "" {
				t.Error("the relaunch marker must be cleared so nothing downstream inherits it")
			}
		})
	}

	t.Run("release archive: notice only", func(t *testing.T) {
		archive := selfupdate.Channel{Kind: selfupdate.KindArchive, Exe: "/opt/yolo/yolo", Version: "0.10.0"}
		h := newUpdateHarness(t, archive)
		h.seed(t, archive, nil)
		h.d.stdin = strings.NewReader("y\n")
		h.notify("run")
		if h.applied != 0 || !strings.Contains(h.stderr.String(), selfupdate.ReleasesPage) {
			t.Errorf("want the download link and no prompt: %q", h.stderr.String())
		}
	})
	t.Run("binary-only install: coordinated-update notice only", func(t *testing.T) {
		ch := selfupdate.Channel{Kind: selfupdate.KindGoInstall, Exe: "/home/u/go/bin/yolo", Version: "0.10.0"}
		h := newUpdateHarness(t, ch)
		h.seed(t, ch, nil)
		h.d.stdin = strings.NewReader("y\n")
		h.notify("run")
		if h.applied != 0 || !strings.Contains(h.stderr.String(), "reinstall from Homebrew or a release archive") {
			t.Errorf("want coordinated manual guidance and no prompt: %q", h.stderr.String())
		}
	})
}

func TestUpdateRootConflictAllowsTheNamedCheckoutItself(t *testing.T) {
	root := fakeCheckout(t, selfupdate.Module)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateRootConflict(resolved, root); err != nil {
		t.Errorf("an override naming the --from checkout was refused: %v", err)
	}
	if err := updateRootConflict("", root); err == nil {
		t.Error("a Homebrew update must refuse a live-source override it cannot update")
	}
}

func TestInstalledVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yolo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'yolo-jail 0.11.0+3.gabc\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := installedVersion(path); err != nil || got != "0.11.0+3.gabc" {
		t.Errorf("installedVersion = %q, %v", got, err)
	}
}

// The probe asks the installed binary for ITS version: a YOLO_VERSION in this process's
// environment would be printed in its place, and reported as what the update installed.
func TestInstalledVersionIgnoresYOLOVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yolo")
	script := "#!/bin/sh\nif [ -n \"${YOLO_VERSION:-}\" ]; then echo \"yolo-jail $YOLO_VERSION\"; " +
		"else echo 'yolo-jail 0.11.0+3.gabc'; fi\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_VERSION", "9.9.9")
	if got, err := installedVersion(path); err != nil || got != "0.11.0+3.gabc" {
		t.Errorf("installedVersion = %q, %v; want the binary's own 0.11.0+3.gabc", got, err)
	}
}

func TestUpdateCommand(t *testing.T) {
	available := func(_ context.Context, ch selfupdate.Channel, _ selfupdate.State) selfupdate.State {
		return selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Current: ch.Version, Latest: "0.11.0", Available: true, CheckedAt: time.Now()}
	}

	t.Run("--check reports without applying", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		h.d.check = available
		if rc := updateMain([]string{"update", "--check"}, h.d); rc != 0 {
			t.Fatalf("exit %d", rc)
		}
		if h.applied != 0 {
			t.Error("--check must not update")
		}
		if !strings.Contains(h.stdout.String(), "0.11.0 is available") {
			t.Errorf("report: %q", h.stdout.String())
		}
		if !selfupdate.LoadState(h.d.statePath).Available {
			t.Error("--check must refresh the cache")
		}
	})
	t.Run("binary-only channels refuse a partial update", func(t *testing.T) {
		ch := selfupdate.Channel{Kind: selfupdate.KindPipx, Exe: "/home/u/.local/bin/yolo", Version: "0.10.0"}
		h := newUpdateHarness(t, ch)
		h.d.check = available
		if rc := updateMain([]string{"update"}, h.d); rc != 1 || h.applied != 0 {
			t.Errorf("exit %d, applied %d; want 1, 0", rc, h.applied)
		}
		if !strings.Contains(h.stderr.String(), "Homebrew or a release archive") || strings.Contains(h.stderr.String(), "YOLO_REPO_ROOT") {
			t.Errorf("missing coordinated-update explanation: %q", h.stderr.String())
		}
	})
	t.Run("a Homebrew no-op is not reported as updated", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		h.d.check = available
		h.version = "0.10.0"
		if rc := updateMain([]string{"update"}, h.d); rc != 1 {
			t.Errorf("exit %d, want 1", rc)
		}
		if !strings.Contains(h.stderr.String(), "could not be verified") {
			t.Errorf("missing verification failure: %q", h.stderr.String())
		}
	})
	t.Run("a repo-root override refuses a partial Homebrew update", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		h.d.check = available
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		h.env["YOLO_REPO_ROOT"] = root
		if rc := updateMain([]string{"update"}, h.d); rc != 1 || h.applied != 0 {
			t.Errorf("exit %d, applied %d; want 1, 0", rc, h.applied)
		}
		if !strings.Contains(h.stderr.String(), "is obsolete and does not select launch sources") {
			t.Errorf("missing obsolete-variable explanation: %q", h.stderr.String())
		}
	})
	t.Run("the config opt-out does not disable the command", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		h.d.check = available
		h.d.configEnabled = func() bool { return false }
		if rc := updateMain([]string{"update"}, h.d); rc != 0 || h.applied != 1 {
			t.Errorf("exit %d, applied %d; want 0, 1", rc, h.applied)
		}
	})
	t.Run("up to date does nothing unless forced", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		if rc := updateMain([]string{"update"}, h.d); rc != 0 || h.applied != 0 {
			t.Errorf("exit %d, applied %d; want 0, 0", rc, h.applied)
		}
		if !strings.Contains(h.stdout.String(), "up to date") {
			t.Errorf("report: %q", h.stdout.String())
		}
		if rc := updateMain([]string{"update", "--force"}, h.d); rc != 0 || h.applied != 1 {
			t.Errorf("--force: exit %d, applied %d; want 0, 1", rc, h.applied)
		}
	})
	t.Run("refused in a jail", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		h.env["YOLO_VERSION"] = "0.10.0"
		if rc := updateMain([]string{"update"}, h.d); rc != 1 || h.applied != 0 {
			t.Errorf("exit %d, applied %d; want 1, 0", rc, h.applied)
		}
		// The jail keeps the yolo it was launched with, so the host's update alone leaves this
		// jail on the old one: the step is the host's update AND a relaunch, in updatehint's
		// words, which every refusal of a newer yolo's file prints too.
		if !strings.Contains(h.stderr.String(), updatehint.InJailStep) {
			t.Errorf("the in-jail refusal does not name the host's update and the relaunch (%q):\n%s",
				updatehint.InJailStep, h.stderr.String())
		}
	})
	t.Run("unknown channel", func(t *testing.T) {
		h := newUpdateHarness(t, selfupdate.Channel{Kind: selfupdate.KindUnknown, Exe: "/tmp/yolo"})
		if rc := updateMain([]string{"update"}, h.d); rc != 1 {
			t.Errorf("exit %d, want 1", rc)
		}
		if !strings.Contains(h.stderr.String(), selfupdate.ReleasesPage) || strings.Contains(h.stderr.String(), "--from") {
			t.Errorf("want the releases page and no --from, which is only for a binary built from source: %q", h.stderr.String())
		}
	})
	t.Run("unknown argument", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		if rc := updateMain([]string{"update", "--yes"}, h.d); rc != 2 {
			t.Errorf("exit %d, want 2", rc)
		}
	})
	t.Run("--autostash needs --from", func(t *testing.T) {
		for _, ch := range []selfupdate.Channel{testChannel, snapshotTestChannel} {
			h := newUpdateHarness(t, ch)
			if rc := updateMain([]string{"update", "--autostash"}, h.d); rc != 2 || h.applied != 0 || h.deployed != 0 {
				t.Errorf("%s: exit %d, applied %d, deployed %d; want 2, 0, 0", ch.Kind, rc, h.applied, h.deployed)
			}
			if !strings.Contains(h.stderr.String(), "yolo update --from <checkout> --autostash") {
				t.Errorf("%s: the refusal does not name the form that works: %q", ch.Kind, h.stderr.String())
			}
		}
	})
}

// A snapshot install has no release to compare against: `yolo update` and its
// --check change nothing — no check, no state write, no install — and print the
// two ways to update it. --check succeeds (it answered the question); a plain
// update exits 1, because it installed nothing it was asked to.
func TestUpdateOnASnapshotChangesNothingAndNamesTheNextSteps(t *testing.T) {
	for _, c := range []struct {
		args   []string
		wantRC int
	}{
		{[]string{"update", "--check"}, 0},
		{[]string{"update"}, 1},
		{[]string{"update", "--force"}, 1},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			h := newUpdateHarness(t, snapshotTestChannel)
			checked := 0
			h.d.check = func(_ context.Context, ch selfupdate.Channel, _ selfupdate.State) selfupdate.State {
				checked++
				return selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Latest: "0.11.0", Available: true}
			}
			if rc := updateMain(c.args, h.d); rc != c.wantRC {
				t.Errorf("exit %d, want %d: %s", rc, c.wantRC, h.stderr.String())
			}
			if checked != 0 || h.applied != 0 || h.deployed != 0 {
				t.Errorf("checked %d, applied %d, deployed %d; a snapshot update changes nothing", checked, h.applied, h.deployed)
			}
			if _, err := os.Stat(h.d.statePath); !os.IsNotExist(err) {
				t.Errorf("a snapshot update wrote the update cache (%v)", err)
			}
			out := h.stdout.String()
			for _, want := range []string{"Installed via: snapshot (/home/u/go/bin/yolo)", "just install", "yolo update --from <checkout>"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
			for _, unwanted := range []string{"go install", "cannot tell how this yolo", "release archive", selfupdate.ReleasesPage} {
				if strings.Contains(out+h.stderr.String(), unwanted) {
					t.Errorf("a snapshot was described as another channel (%q):\n%s%s", unwanted, out, h.stderr.String())
				}
			}
		})
	}
}

// The real command path: `yolo update` dispatched as a user runs it, by a binary
// stamped BuildKind=installed with no other channel marker. Deleting the snapshot
// case from Detect, or BuildKind from selfupdate.Current, or the snapshot branch
// from updateMain, fails this.
func TestUpdateCommandOnAnInstalledBuildPrintsTheSnapshotSteps(t *testing.T) {
	origKind := version.BuildKind
	t.Cleanup(func() { version.BuildKind = origKind })
	version.BuildKind = "installed"
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_NO_BANNER", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")

	for _, c := range []struct {
		args   []string
		wantRC int
	}{
		{[]string{"update", "--check"}, 0},
		{[]string{"update"}, 1},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			var rc int
			out := captureStdout(t, func() { rc = dispatchNative("update", c.args) })
			if rc != c.wantRC {
				t.Errorf("exit %d, want %d:\n%s", rc, c.wantRC, out)
			}
			for _, want := range []string{"Installed via: snapshot", selfupdate.SnapshotSummary, selfupdate.SnapshotSteps} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	}
}

func TestUpdateFrom(t *testing.T) {
	good := fakeCheckout(t, selfupdate.Module)
	resolvedGood, err := filepath.EvalSymlinks(good)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("deploys the named checkout beside the running binary, with no check", func(t *testing.T) {
		h := newUpdateHarness(t, snapshotTestChannel)
		checked := 0
		h.d.check = func(context.Context, selfupdate.Channel, selfupdate.State) selfupdate.State {
			checked++
			return selfupdate.State{}
		}
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 0 {
			t.Fatalf("exit %d: %s", rc, h.stderr.String())
		}
		if h.deployed != 1 || h.deployDir != resolvedGood || h.deployExe != snapshotTestChannel.Exe {
			t.Errorf("deployed %d times, %q beside %q; want once, %q beside %q", h.deployed, h.deployDir, h.deployExe, resolvedGood, snapshotTestChannel.Exe)
		}
		if checked != 0 || h.applied != 0 {
			t.Errorf("checked %d, applied %d; --from deploys the checkout as it stands", checked, h.applied)
		}
		if h.deployOpts.Autostash {
			t.Error("autostash without --autostash")
		}
		if !strings.Contains(h.stdout.String(), "just deploy") || !strings.Contains(h.stdout.String(), "✓ deployed.") {
			t.Errorf("the deploy is not stated: %q", h.stdout.String())
		}
		if !strings.Contains(h.stderr.String(), "any jail using it loses it briefly") {
			t.Errorf("the broker impact is not stated: %q", h.stderr.String())
		}
	})
	t.Run("--autostash reaches the deploy", func(t *testing.T) {
		h := newUpdateHarness(t, snapshotTestChannel)
		if rc := updateMain([]string{"update", "--from", good, "--autostash"}, h.d); rc != 0 || !h.deployOpts.Autostash {
			t.Errorf("exit %d, autostash %v; want 0, true", rc, h.deployOpts.Autostash)
		}
	})
	t.Run("--check prints the deploy and changes nothing", func(t *testing.T) {
		h := newUpdateHarness(t, snapshotTestChannel)
		if rc := updateMain([]string{"update", "--check", "--from=" + good}, h.d); rc != 0 || h.deployed != 0 {
			t.Errorf("exit %d, deployed %d; want 0, 0: %s", rc, h.deployed, h.stderr.String())
		}
		if !strings.Contains(h.stdout.String(), "GOBIN=/home/u/go/bin just deploy") {
			t.Errorf("the planned deploy is not printed: %q", h.stdout.String())
		}
	})
	t.Run("a failed deploy is reported", func(t *testing.T) {
		h := newUpdateHarness(t, snapshotTestChannel)
		h.deployErr = errors.New("uncommitted changes")
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 1 || !strings.Contains(h.stderr.String(), "uncommitted changes") {
			t.Errorf("exit %d, stderr %q", rc, h.stderr.String())
		}
	})
	t.Run("refused for an install nothing shows was built from source", func(t *testing.T) {
		h := newUpdateHarness(t, selfupdate.Channel{Kind: selfupdate.KindUnknown, Exe: "/usr/local/bin/yolo"})
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 1 || h.deployed != 0 {
			t.Errorf("exit %d, deployed %d; want 1, 0", rc, h.deployed)
		}
		if !strings.Contains(h.stderr.String(), "nothing shows this yolo") || !strings.Contains(h.stderr.String(), "just install") {
			t.Errorf("stderr %q", h.stderr.String())
		}
	})
	t.Run("refused for a package-manager install", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 1 || !strings.Contains(h.stderr.String(), "managed by homebrew") || h.deployed != 0 {
			t.Errorf("exit %d, deployed %d, stderr %q", rc, h.deployed, h.stderr.String())
		}
	})
	t.Run("refused for something that is not a yolo-jail checkout", func(t *testing.T) {
		h := newUpdateHarness(t, snapshotTestChannel)
		other := fakeCheckout(t, "example.com/other")
		if rc := updateMain([]string{"update", "--from", other}, h.d); rc != 1 || !strings.Contains(h.stderr.String(), "not a yolo-jail checkout") {
			t.Errorf("exit %d, stderr %q", rc, h.stderr.String())
		}
		if rc := updateMain([]string{"update", "--from", t.TempDir()}, h.d); rc != 1 || !strings.Contains(h.stderr.String(), "not a git checkout") {
			t.Errorf("exit %d, stderr %q", rc, h.stderr.String())
		}
		if h.deployed != 0 {
			t.Errorf("deployed %d", h.deployed)
		}
	})
	t.Run("--from with no value", func(t *testing.T) {
		h := newUpdateHarness(t, snapshotTestChannel)
		if rc := updateMain([]string{"update", "--from"}, h.d); rc != 2 {
			t.Errorf("exit %d, want 2", rc)
		}
		h = newUpdateHarness(t, snapshotTestChannel)
		if rc := updateMain([]string{"update", "--from="}, h.d); rc != 2 {
			t.Errorf("--from=: exit %d, want 2", rc)
		}
	})
}

func fakeCheckout(t *testing.T, module string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
