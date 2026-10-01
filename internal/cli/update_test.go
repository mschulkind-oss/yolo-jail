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
var sourceTestChannel = selfupdate.Channel{Kind: selfupdate.KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf"}

type updateHarness struct {
	d          updateDeps
	env        map[string]string
	stderr     bytes.Buffer
	stdout     bytes.Buffer
	spawned    int
	applied    int
	applyOpts  selfupdate.ApplyOptions
	applyErr   error
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
		apply: func(_ context.Context, _ selfupdate.Channel, opts selfupdate.ApplyOptions, _, _ io.Writer) error {
			h.applied++
			h.applyOpts = opts
			return h.applyErr
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
	if ch.Kind == selfupdate.KindSource {
		st.Latest, st.Upstream, st.Behind = "abc1234d", "origin/main", 12
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

func TestSourceUpdateChecksOnlyOnRequest(t *testing.T) {
	h := newUpdateHarness(t, sourceTestChannel)
	h.notify("run")
	if h.spawned != 0 || h.stderr.Len() != 0 {
		t.Errorf("an automatic source check ran: spawned=%d stderr=%q", h.spawned, h.stderr.String())
	}

	h.seed(t, sourceTestChannel, func(s *selfupdate.State) {
		s.CheckedAt = h.d.now().Add(-25 * time.Hour)
	})
	h.notify("run")
	if h.spawned != 0 || h.stderr.Len() != 0 {
		t.Errorf("a stale source answer triggered automatic work: spawned=%d stderr=%q", h.spawned, h.stderr.String())
	}

	h.seed(t, sourceTestChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("run")
	if !strings.Contains(h.stderr.String(), "new commits") ||
		!strings.Contains(h.stderr.String(), "relaunch?") {
		t.Errorf("a fresh explicit source answer must still drive the notice and prompt:\n%s", h.stderr.String())
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
	if h.applyOpts.Autostash {
		t.Error("the launch prompt must never autostash a checkout behind the user's back")
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

func TestUpdatePromptSaysWhatASourceUpdateCosts(t *testing.T) {
	h := newUpdateHarness(t, sourceTestChannel)
	h.seed(t, sourceTestChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("run")
	out := h.stderr.String()
	if !strings.Contains(out, "Source checkout: /src/yolo-jail") {
		t.Errorf("the source path is not disclosed before consent:\n%s", out)
	}
	if !strings.Contains(out, "any jail using it loses it briefly") {
		t.Errorf("the broker impact is not stated conditionally:\n%s", out)
	}
	if !strings.Contains(out, "rebuilds the jail image") {
		t.Errorf("the rebuild cost is not stated:\n%s", out)
	}
	if strings.Index(out, "relaunch?") < strings.Index(out, "rebuilds") {
		t.Error("the cost must be stated BEFORE the question")
	}

	h = newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("run")
	if strings.Contains(h.stderr.String(), "OAuth broker") {
		t.Errorf("a Homebrew update does not run just deploy:\n%s", h.stderr.String())
	}
}

// What an update does to jails already running differs by channel, and both
// the launch prompt and `yolo update`'s success line must say which.
func TestUpdateSaysWhatHappensToRunningJailsPerChannel(t *testing.T) {
	for _, c := range []struct {
		ch   selfupdate.Channel
		want string
	}{
		{testChannel, "The previous version stays installed, so running jails keep their binaries until you restart them; `brew cleanup yolo-jail` removes it after that."},
		{sourceTestChannel, "`just deploy` installs a new bundle beside the one they use"},
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
				return selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Current: ch.Version, Latest: "0.11.0", Behind: 1, Available: true}
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

// A from-source check or update runs the checkout's own git config, hooks and
// Justfile on the host. `yolo update --help` and the source launch prompt say
// so, and name workspace_readonly; a Homebrew prompt runs none of it and must
// not claim to.
func TestUpdateSaysASourceUpdateRunsTheCheckoutsGitConfigAndJustfile(t *testing.T) {
	var help bytes.Buffer
	if !answerHelp("update", []string{"update", "--help"}, &help) {
		t.Fatal("`yolo update --help` printed no help")
	}
	for _, want := range []string{"own git config and hooks", "runs its Justfile", "workspace_readonly"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("`yolo update --help` does not say %q:\n%s", want, help.String())
		}
	}

	h := newUpdateHarness(t, sourceTestChannel)
	h.seed(t, sourceTestChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("run")
	out := h.stderr.String()
	for _, want := range []string{"this checkout's own git config and hooks", "runs its Justfile", "workspace_readonly"} {
		if !strings.Contains(out, want) || strings.Index(out, want) > strings.Index(out, "relaunch?") {
			t.Errorf("the source launch prompt does not say %q before asking:\n%s", want, out)
		}
	}

	h = newUpdateHarness(t, testChannel)
	h.seed(t, testChannel, nil)
	h.d.stdin = strings.NewReader("n\n")
	h.notify("run")
	if strings.Contains(h.stderr.String(), "workspace_readonly") {
		t.Errorf("a Homebrew prompt runs no checkout code:\n%s", h.stderr.String())
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
		if h.applied != 0 || !strings.Contains(h.stderr.String(), "separate jail source together") {
			t.Errorf("want coordinated manual guidance and no prompt: %q", h.stderr.String())
		}
	})
}

func TestUpdateRootConflictAllowsTheSourceCheckoutItself(t *testing.T) {
	root := fakeCheckout(t, selfupdate.Module)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	ch := selfupdate.Channel{Kind: selfupdate.KindSource, SourceDir: resolved}
	if err := updateRootConflict(ch, root); err != nil {
		t.Errorf("matching source override was refused: %v", err)
	}
	if err := updateRootConflict(testChannel, root); err == nil {
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

func TestUpdateCommand(t *testing.T) {
	available := func(_ context.Context, ch selfupdate.Channel, _ selfupdate.State) selfupdate.State {
		st := selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Current: ch.Version, Latest: "0.11.0", Available: true, CheckedAt: time.Now()}
		if ch.Kind == selfupdate.KindSource {
			st.Latest, st.Upstream, st.Behind = "abc1234d", "origin/main", 2
		}
		return st
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
	t.Run("an available update is applied, and --autostash reaches it", func(t *testing.T) {
		h := newUpdateHarness(t, sourceTestChannel)
		h.d.check = available
		if rc := updateMain([]string{"update", "--autostash"}, h.d); rc != 0 || h.applied != 1 {
			t.Errorf("exit %d, applied %d; want 0, 1", rc, h.applied)
		}
		if !h.applyOpts.Autostash {
			t.Error("--autostash was parsed but not passed on")
		}
	})
	t.Run("binary-only channels refuse a partial update", func(t *testing.T) {
		ch := selfupdate.Channel{Kind: selfupdate.KindPipx, Exe: "/home/u/.local/bin/yolo", Version: "0.10.0"}
		h := newUpdateHarness(t, ch)
		h.d.check = available
		if rc := updateMain([]string{"update"}, h.d); rc != 1 || h.applied != 0 {
			t.Errorf("exit %d, applied %d; want 1, 0", rc, h.applied)
		}
		if !strings.Contains(h.stderr.String(), "YOLO_REPO_ROOT") {
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
		if !strings.Contains(h.stderr.String(), "overrides this install's bundle") {
			t.Errorf("missing override explanation: %q", h.stderr.String())
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
	t.Run("a moved checkout names --from", func(t *testing.T) {
		h := newUpdateHarness(t, selfupdate.Channel{Kind: selfupdate.KindUnknown, Exe: "/home/u/.local/bin/yolo", MissingSourceDir: "/src/gone"})
		if rc := updateMain([]string{"update"}, h.d); rc != 1 {
			t.Errorf("exit %d, want 1", rc)
		}
		if !strings.Contains(h.stderr.String(), "/src/gone, which no longer contains the yolo-jail checkout") || !strings.Contains(h.stderr.String(), "--from") {
			t.Errorf("stderr: %q", h.stderr.String())
		}
	})
	t.Run("unknown argument", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		if rc := updateMain([]string{"update", "--yes"}, h.d); rc != 2 {
			t.Errorf("exit %d, want 2", rc)
		}
	})
	t.Run("--autostash is source-only", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		if rc := updateMain([]string{"update", "--autostash"}, h.d); rc != 2 || h.applied != 0 {
			t.Errorf("exit %d, applied %d; want 2, 0", rc, h.applied)
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

func TestUpdateFrom(t *testing.T) {
	good := fakeCheckout(t, selfupdate.Module)
	resolvedGood, err := filepath.EvalSymlinks(good)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("repoints a moved checkout", func(t *testing.T) {
		h := newUpdateHarness(t, selfupdate.Channel{Kind: selfupdate.KindUnknown, Exe: "/home/u/.local/bin/yolo", MissingSourceDir: "/src/gone"})
		var checked selfupdate.Channel
		h.d.check = func(_ context.Context, ch selfupdate.Channel, _ selfupdate.State) selfupdate.State {
			checked = ch
			return selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Current: ch.Version}
		}
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 0 {
			t.Fatalf("exit %d: %s", rc, h.stderr.String())
		}
		if checked.Kind != selfupdate.KindSource || checked.SourceDir != resolvedGood || checked.Exe != "/home/u/.local/bin/yolo" {
			t.Errorf("checked %+v, want a source channel at %s deploying beside the running binary", checked, resolvedGood)
		}
		if checked.Branch != "" {
			t.Error("naming the checkout names what to deploy; no branch may be enforced")
		}
		if h.applied != 1 {
			t.Errorf("applied %d, want 1", h.applied)
		}
	})
	t.Run("--from=<dir> spelling", func(t *testing.T) {
		h := newUpdateHarness(t, sourceTestChannel)
		if rc := updateMain([]string{"update", "--check", "--from=" + good}, h.d); rc != 0 {
			t.Errorf("exit %d: %s", rc, h.stderr.String())
		}
	})
	t.Run("a checkout with no usable upstream is deployed as named", func(t *testing.T) {
		h := newUpdateHarness(t, selfupdate.Channel{Kind: selfupdate.KindUnknown, Exe: "/home/u/.local/bin/yolo", MissingSourceDir: "/src/gone"})
		h.d.check = func(_ context.Context, ch selfupdate.Channel, _ selfupdate.State) selfupdate.State {
			return selfupdate.State{Identity: ch.Identity(), Kind: ch.Kind, Current: ch.Version, Error: "branch has no upstream"}
		}
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 0 {
			t.Fatalf("exit %d: %s", rc, h.stderr.String())
		}
		if !h.applyOpts.SkipPull {
			t.Error("--from must deploy the named checkout when no upstream can be checked")
		}
		if !strings.Contains(h.stdout.String(), "no pull will run") {
			t.Errorf("the skip-pull behavior was not disclosed: %q", h.stdout.String())
		}
	})
	t.Run("refused for an install nothing shows was built from source", func(t *testing.T) {
		h := newUpdateHarness(t, selfupdate.Channel{Kind: selfupdate.KindUnknown, Exe: "/usr/local/bin/yolo"})
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 1 || h.applied != 0 {
			t.Errorf("exit %d, applied %d; want 1, 0", rc, h.applied)
		}
		if !strings.Contains(h.stderr.String(), "nothing shows this yolo") {
			t.Errorf("stderr %q", h.stderr.String())
		}
	})
	t.Run("refused for a package-manager install", func(t *testing.T) {
		h := newUpdateHarness(t, testChannel)
		if rc := updateMain([]string{"update", "--from", good}, h.d); rc != 1 || !strings.Contains(h.stderr.String(), "managed by homebrew") {
			t.Errorf("exit %d, stderr %q", rc, h.stderr.String())
		}
	})
	t.Run("refused for something that is not a yolo-jail checkout", func(t *testing.T) {
		h := newUpdateHarness(t, sourceTestChannel)
		other := fakeCheckout(t, "example.com/other")
		if rc := updateMain([]string{"update", "--from", other}, h.d); rc != 1 || !strings.Contains(h.stderr.String(), "not a yolo-jail checkout") {
			t.Errorf("exit %d, stderr %q", rc, h.stderr.String())
		}
		if rc := updateMain([]string{"update", "--from", t.TempDir()}, h.d); rc != 1 || !strings.Contains(h.stderr.String(), "not a git checkout") {
			t.Errorf("exit %d, stderr %q", rc, h.stderr.String())
		}
	})
	t.Run("--from with no value", func(t *testing.T) {
		h := newUpdateHarness(t, sourceTestChannel)
		if rc := updateMain([]string{"update", "--from"}, h.d); rc != 2 {
			t.Errorf("exit %d, want 2", rc)
		}
		h = newUpdateHarness(t, sourceTestChannel)
		if rc := updateMain([]string{"update", "--from="}, h.d); rc != 2 {
			t.Errorf("--from=: exit %d, want 2", rc)
		}
	})
}
