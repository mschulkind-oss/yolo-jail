package run

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// setupgate_test.go pins the setup gate (setupgate.go; named-setups-plan.md slice 4a): a launch
// runs one named setup, and a setup this machine cannot run is refused before anything is built,
// naming the next step.
//
// ⚠ THE CALL SITE IS WHAT THESE ASSERT, not the predicate: every case calls Run() and
// discriminates on WHICH refusal came back. The Linux fixture has no resolvable repo root, so a
// launch that gets past the gate refuses a few lines later with the repo-root message instead.
// Delete the gate's call site in Run and the refusal cases fail on that substitution.

// setupGateOptions is a Linux launch whose seams reach the gate deterministically: podman on
// PATH and answering, env from envs, and RepoRoot FAILING, which is what makes "did the gate
// fire?" observable as a difference between two refusals.
func setupGateOptions(t *testing.T, ws string, envs map[string]string, stdout, stderr *bytes.Buffer) *Options {
	t.Helper()
	o := &Options{Workspace: ws, IsLinux: true, Stdout: stdout, Stderr: stderr}
	fillDefaults(o)
	o.Stdout, o.Stderr = stdout, stderr
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.Now = func() time.Time { return time.Unix(0, 0) }
	o.Getpid = func() int { return 1 }
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	answeringPodman(o, minimalPodmanInfo)
	o.Getenv = func(k string) string { return envs[k] }
	o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
	return o
}

// setupGateWorkspace is a workspace with body as its yolo-jail.jsonc and an empty HOME.
func setupGateWorkspace(t *testing.T, body string) string {
	t.Helper()
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

const pastTheGate = "Cannot find yolo-jail repo root"

// TestTheSetupGateRefusesWhatLinuxCannotRun: each refusal carries the design's sentence and its
// next step: rc 2 for what --setup typed (misuse), 1 for what came from elsewhere.
func TestTheSetupGateRefusesWhatLinuxCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag string
		env  map[string]string
		rc   int
		want []string
	}{
		{name: "seatbelt-self on Linux", flag: "seatbelt-self", rc: 2,
			want: []string{`setup "seatbelt-self" uses Seatbelt, which only macOS has. On this machine:`, "yolo --setup podman-self -- claude"}},
		{name: "landlock-self", flag: "landlock-self", rc: 2,
			want: []string{`setup "landlock-self" is not built yet`, "yolo --setup podman-self -- claude"}},
		{name: "host-self", flag: "host-self", rc: 2,
			want: []string{`setup "host-self" puts no walls around the agent, and only ` + "`yolo host`" + ` runs one:`, "yolo host -- claude"}},
		{name: "host-self from YOLO_SETUP", env: map[string]string{"YOLO_SETUP": "host-self"}, rc: 1,
			want: []string{`setup "host-self" puts no walls`, "yolo host -- claude"}},
		{name: "an unknown --setup", flag: "fast", rc: 2,
			want: []string{`setup "fast" is not defined. Defined here: podman-self`}},
		{name: "a removed YOLO_RUNTIME on the host", env: map[string]string{"YOLO_RUNTIME": "podman"}, rc: 1,
			want: []string{"YOLO_RUNTIME is removed", "YOLO_SETUP=podman-self", "unset YOLO_RUNTIME"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := setupGateWorkspace(t, "{}\n")
			var stdout, stderr bytes.Buffer
			o := setupGateOptions(t, ws, tc.env, &stdout, &stderr)
			o.SetupName = tc.flag
			rc := Run(*o)
			if rc != tc.rc {
				t.Fatalf("Run() = %d, want %d\n%s", rc, tc.rc, stderr.String())
			}
			for _, w := range tc.want {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("missing %q in\n%s", w, stderr.String())
				}
			}
			if strings.Contains(stderr.String(), pastTheGate) {
				t.Errorf("the launch got PAST the setup gate — it is not wired into Run:\n%s", stderr.String())
			}
		})
	}
}

// TestTheSetupGatePassesARunnableSetup is the control: podman-self from the flag, from
// YOLO_SETUP, from the user config and as the Linux default all walk past the gate; and a
// removed YOLO_RUNTIME inside a launched session warns once and launches (SUP-D6).
func TestTheSetupGatePassesARunnableSetup(t *testing.T) {
	for _, tc := range []struct {
		name, flag, user string
		env              map[string]string
		warn             string
	}{
		{name: "the default"},
		{name: "the flag", flag: "podman-self"},
		{name: "YOLO_SETUP", env: map[string]string{"YOLO_SETUP": "podman-self"}},
		{name: "the user config", user: `{"setup": "podman-self"}`},
		{name: "a user setup", user: `{"setup": "mine", "setups": {"mine": {"confinement": "podman"}}}`},
		{name: "YOLO_RUNTIME in a session", env: map[string]string{"YOLO_RUNTIME": "podman", "YOLO_VERSION": "1.0"},
			warn: "YOLO_RUNTIME=podman is removed and ignored here"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := setupGateWorkspace(t, "{}\n")
			if tc.user != "" {
				writeUserConfigJSON(t, os.Getenv("HOME"), tc.user)
			}
			var stdout, stderr bytes.Buffer
			o := setupGateOptions(t, ws, tc.env, &stdout, &stderr)
			o.SetupName = tc.flag
			Run(*o)
			if !strings.Contains(stderr.String(), pastTheGate) {
				t.Errorf("a runnable setup did not reach the repo-root gate:\n%s", stderr.String())
			}
			if tc.warn != "" && !strings.Contains(stderr.String(), tc.warn) {
				t.Errorf("missing the warning %q:\n%s", tc.warn, stderr.String())
			}
		})
	}
}

// TestTheMissingDefaultNamesTheInstall: with no podman on PATH the Linux default refuses with the
// design's default-setup sentence, and a named podman-self with the named one.
func TestTheMissingDefaultNamesTheInstall(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{"", "The default setup on this machine, podman-self, needs Podman, which is not installed."},
		{"podman-self", `setup "podman-self" needs Podman, which is not installed.`},
	} {
		ws := setupGateWorkspace(t, "{}\n")
		var stdout, stderr bytes.Buffer
		o := setupGateOptions(t, ws, nil, &stdout, &stderr)
		o.LookPath = func(string) (string, bool) { return "", false }
		o.SetupName = tc.flag
		if rc := Run(*o); rc != 1 || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("--setup %q: rc %d, want 1 and %q in\n%s", tc.flag, rc, tc.want, stderr.String())
		}
		if !strings.Contains(stderr.String(), "https://podman.io/docs/installation") {
			t.Errorf("the refusal does not name the install:\n%s", stderr.String())
		}
	}
}

// TestTheRemovedKeysStillChooseTheirSetup is the bridge until slice 5 refuses them: a config that
// has not moved launches what it launched before. `confinement = "guest"` on Linux means a Seatbelt
// setup, which Linux refuses naming podman-self; `confinement = "host"` refuses naming `yolo host`.
// A --setup outranks them.
func TestTheRemovedKeysStillChooseTheirSetup(t *testing.T) {
	for _, tc := range []struct {
		body, flag string
		rc         int
		want       string
	}{
		{body: `{"confinement": "guest"}`, rc: 1, want: `setup "seatbelt-yolo-account" uses Seatbelt, which only macOS has`},
		{body: `{"confinement": "host"}`, rc: 1, want: "yolo host -- claude"},
		{body: `{"runtime": "container"}`, rc: 1, want: `setup "apple-container-self" uses Apple Container, which only macOS has`},
		{body: `{"confinement": "guest"}`, flag: "podman-self", rc: 1, want: pastTheGate},
		{body: `{"runtime": "podman"}`, rc: 1, want: pastTheGate},
	} {
		ws := setupGateWorkspace(t, tc.body)
		var stdout, stderr bytes.Buffer
		o := setupGateOptions(t, ws, nil, &stdout, &stderr)
		o.SetupName = tc.flag
		if rc := Run(*o); rc != tc.rc || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%s --setup %q: rc %d, want %d and %q in\n%s", tc.body, tc.flag, rc, tc.rc, tc.want, stderr.String())
		}
	}
}

// TestTheContainerSessionIsToldItsSetup (SUP-D4): a container launch hands its jail
// YOLO_SETUP=<name>, the session's marker, and no longer the removed YOLO_RUNTIME. Deleting the
// export from assemble.go fails it.
func TestTheContainerSessionIsToldItsSetup(t *testing.T) {
	installerLaunchHome(t)
	argv, out := fakePodmanLaunch(t, func(*Options) {})
	if argv == nil {
		t.Fatalf("the launch never reached podman run:\n%s", out)
	}
	env := argvValues(argv, "-e")
	if !slices.Contains(env, setup.EnvVar+"=podman-self") {
		t.Errorf("the jail is not told YOLO_SETUP=podman-self: %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, config.RemovedRuntimeEnv+"=") {
			t.Errorf("the jail is still handed the removed %s", e)
		}
	}
}

// ─── macOS: the Seatbelt setups launch the macos-user backend ───

// macosSetupReach is what the macos-user handler saw.
type macosSetupReach struct {
	reached         bool
	runtimeResolved string
	briefing        string
	setupEnv        string
	notchEnv        string
}

// runMacosSetup runs one macOS launch with the claude pack selected, wsConfig as the workspace
// config, flag as --setup and env as the environment.
func runMacosSetup(t *testing.T, wsConfig, flag string, env map[string]string, tweak func(*Options)) (int, macosSetupReach, string) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := resolvedGuestWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(wsConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "", &stdout, &stderr, nil)
	o.IsMacOS, o.IsLinux = true, false
	o.SetupName = flag
	o.AcceptConfigChanges = true
	o.Getenv = func(k string) string { return env[k] }
	var got macosSetupReach
	o.OnRuntimeResolved = func(rt string) error {
		got.runtimeResolved = rt
		return nil
	}
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string,
		overlay macosuser.HomeOverlay, _ macosuser.HostContext, _ bool,
		launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		got.reached = true
		if launchEnv != nil {
			if v, ok := launchEnv.Get(setup.EnvVar); ok {
				got.setupEnv, _ = v.(string)
			}
			if v, ok := launchEnv.Get(config.NotchEnv); ok {
				got.notchEnv, _ = v.(string)
			}
		}
		if overlay.Tree != "" {
			if b, err := os.ReadFile(filepath.Join(overlay.Tree, ".claude", "CLAUDE.md")); err == nil {
				got.briefing = string(b)
			}
		}
		return 0
	}
	if tweak != nil {
		tweak(o)
	}
	rc := Run(*o)
	return rc, got, stdout.String() + stderr.String()
}

// resolvedGuestWorkspace is a workspace directory with its symlinks resolved where it is
// minted, so a darwin TMPDIR under /var cannot make two spellings of it.
func resolvedGuestWorkspace(t *testing.T) string {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

const guestBriefingHeader = "# YOLO Environment — guest"

// TestASeatbeltSetupLaunchesTheMacosUserBackend: every spelling that selects a Seatbelt setup
// reaches the macos-user handler with no container runtime on PATH, its mode is the setup's
// run_as, the briefing states the guest notch, and the session is told its setup (SUP-D4).
func TestASeatbeltSetupLaunchesTheMacosUserBackend(t *testing.T) {
	for _, tc := range []struct {
		name, ws, flag string
		env            map[string]string
		wantSetup      string
		wantMode       macosuser.Mode
	}{
		{name: "--setup seatbelt-self", ws: `{}`, flag: "seatbelt-self", wantSetup: "seatbelt-self", wantMode: macosuser.ModeSelf},
		{name: "YOLO_SETUP", ws: `{}`, env: map[string]string{"YOLO_SETUP": "seatbelt-yolo-account"},
			wantSetup: "seatbelt-yolo-account", wantMode: macosuser.ModeAccount},
		{name: "the removed confinement key", ws: `{"confinement": "guest"}`, wantSetup: "seatbelt-yolo-account", wantMode: macosuser.ModeAccount},
		{name: "the removed runtime key", ws: `{"runtime": "macos-user"}`, wantSetup: "seatbelt-yolo-account", wantMode: macosuser.ModeAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mode macosuser.Mode = -1
			rc, got, out := runMacosSetup(t, tc.ws, tc.flag, tc.env, func(o *Options) {
				ref := &SetupRef{}
				o.SetupRef = ref
				inner := o.MacosUserRun
				o.MacosUserRun = func(c *jsonx.OrderedMap, ws string, a, b []string, r, p string,
					ov macosuser.HomeOverlay, hc macosuser.HostContext, d bool,
					le *jsonx.OrderedMap, bt []packload.BlockedTool, jd macosuser.JailDaemons) int {
					mode = macosuser.ModeFor(ref.Setup.RunAs)
					return inner(c, ws, a, b, r, p, ov, hc, d, le, bt, jd)
				}
			})
			if rc != 0 || !got.reached {
				t.Fatalf("the launch did not reach the macos-user backend (rc %d):\n%s", rc, out)
			}
			if got.runtimeResolved != "macos-user" {
				t.Errorf("resolved runtime %q, want macos-user", got.runtimeResolved)
			}
			if got.setupEnv != tc.wantSetup {
				t.Errorf("the session is told YOLO_SETUP=%q, want %q", got.setupEnv, tc.wantSetup)
			}
			if mode != tc.wantMode {
				t.Errorf("the handler read mode %v from the SetupRef, want %v", mode, tc.wantMode)
			}
			if got.notchEnv != string(config.ConfinementGuest) {
				t.Errorf("YOLO_CONFINEMENT=%q, want guest (its readers move to YOLO_SETUP in slice 9)", got.notchEnv)
			}
			if !strings.Contains(got.briefing, guestBriefingHeader) {
				t.Errorf("the briefing does not carry the guest header:\n%s", got.briefing)
			}
		})
	}
}

// TestAMacWithoutAppleContainerRefusesTheDefault: the Mac default is apple-container-self (OQ-SU4),
// and with no `container` on PATH it refuses with design §6's sentence rather than probing for
// podman.
func TestAMacWithoutAppleContainerRefusesTheDefault(t *testing.T) {
	rc, got, out := runMacosSetup(t, `{}`, "", nil, nil)
	if rc != 1 || got.reached {
		t.Fatalf("rc %d, reached %v, want a refusal:\n%s", rc, got.reached, out)
	}
	if !strings.Contains(out, "The default setup on this Mac, apple-container-self, needs Apple Container, which is not installed.") {
		t.Errorf("not design §6's default refusal:\n%s", out)
	}
}

// TestRemovedKeysThatNameTwoSetupsRefuse: `confinement = "guest"` beside `runtime = "podman"`
// names two setups, and the refusal names a --setup for each.
func TestRemovedKeysThatNameTwoSetupsRefuse(t *testing.T) {
	rc, got, out := runMacosSetup(t, `{"confinement": "guest", "runtime": "podman"}`, "", nil, nil)
	if rc != 1 || got.reached {
		t.Fatalf("rc %d, reached %v, want a refusal:\n%s", rc, got.reached, out)
	}
	for _, w := range []string{"name two different setups", "yolo --setup seatbelt-yolo-account -- claude", "yolo --setup podman-self -- claude"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
}
