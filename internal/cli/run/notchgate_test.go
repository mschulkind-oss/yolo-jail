package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// notchgate_test.go pins the refusal docs/design/declaration-parity.md's OQ-DP3 ruled:
// a launch whose configured `confinement` is not `jail` REFUSES instead of starting a
// container and then telling the agent it is not in one (DP-B16).
//
// ⚠ THE CALL SITE IS WHAT THESE ASSERT, not the predicate. AGENTS.md's "a test that pins
// the CALLEE while the CALL SITE is unpinned is not a test" is the exact shape this feature
// would otherwise take — refuseUnbuiltNotch is one switch and a direct unit test of it
// passes with the `if` deleted from Run. So every case here calls Run() and discriminates
// on WHICH refusal came back: the workspace has no resolvable repo root, so a launch that
// gets past the notch gate refuses a few lines later with the repo-root message instead.
// Delete the gate's call site and the two refusal cases fail on that substitution.

// notchGateOptions builds an Options whose seams reach the notch gate deterministically:
// storage and config load trivially (a workspace with only a yolo-jail.jsonc), the runtime
// is named explicitly so no real daemon is consulted, and RepoRoot FAILS — which is what
// makes "did the gate fire?" observable as a difference between two refusals.
//
// Self-contained rather than reusing reporoot_fatal_test.go's sibling: the discriminator
// these tests rest on is that helper's SUBJECT, and a shared fixture would let a change
// there quietly rewrite what this file is measuring.
func notchGateOptions(t *testing.T, ws string, stdout, stderr *bytes.Buffer) *Options {
	t.Helper()
	o := &Options{
		Workspace: ws,
		IsLinux:   true,
		Stdout:    stdout,
		Stderr:    stderr,
	}
	fillDefaults(o)
	// fillDefaults re-installs the real seams; re-apply the deterministic stubs (the
	// pattern goldenOptions and runFatalOptions both use).
	o.Stdout = stdout
	o.Stderr = stderr
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
	// podman answers the readiness gate at once (podmanready.go).
	answeringPodman(o, minimalPodmanInfo)
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "podman"
		}
		return ""
	}
	// No flake anywhere: a launch that survives the notch gate refuses on THIS instead,
	// which is the discriminator every case below reads.
	o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{}, false }
	return o
}

// notchGateWorkspace writes a workspace whose yolo-jail.jsonc declares `confinement`.
func notchGateWorkspace(t *testing.T, notch string) string {
	t.Helper()
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	body := "{}\n"
	if notch != "" {
		body = "{\n  \"confinement\": \"" + notch + "\"\n}\n"
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

// TestLaunchRefusesTheGuestNotch: `confinement: guest` stops the launch, with the sentence
// `yolo apply --at guest` has printed since Phase 2 — VERBATIM, from render.NotchUnbuilt.
//
// Before OQ-DP3 this value started a container and then had jailcontent.confinementHeader
// tell the agent "a restricted account on the real machine, NOT a disposable container …
// your home is real and persists" — four sentences, every one false of what ran.
func TestLaunchRefusesTheGuestNotch(t *testing.T) {
	ws := notchGateWorkspace(t, "guest")
	var stdout, stderr bytes.Buffer
	o := notchGateOptions(t, ws, &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 {
		t.Fatalf("Run() = %d, want 1 (a guest-notch launch is refused)\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
	want := render.NotchUnbuilt("launch")
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("the guest refusal does not carry render.NotchUnbuilt's sentence.\n"+
			"want substring: %q\ngot stderr:\n%s", want, stderr.String())
	}
	// THE DISCRIMINATOR. This workspace has no repo root, so a launch that reached the
	// container arm would refuse with that instead. Seeing it here means the notch gate
	// did not run — which is what deleting its call site in Run looks like.
	if strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
		t.Errorf("the launch got PAST the notch gate and refused for another reason — the "+
			"gate is not wired into Run:\n%s", stderr.String())
	}
}

// TestLaunchRefusesTheHostNotch: `confinement: host` is refused too, and NOT with the
// guest sentence — the host notch is built, it is simply not something a launch does, so
// the refusal names the two verbs that are (`yolo host -- <cmd>`, `yolo apply --at host`).
func TestLaunchRefusesTheHostNotch(t *testing.T) {
	ws := notchGateWorkspace(t, "host")
	var stdout, stderr bytes.Buffer
	o := notchGateOptions(t, ws, &stdout, &stderr)

	rc := Run(*o)

	if rc != 1 {
		t.Fatalf("Run() = %d, want 1 (a host-notch launch is refused)\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "yolo host -- <cmd>") {
		t.Errorf("the host refusal does not name the host notch's own exec verb:\n%s",
			stderr.String())
	}
	if strings.Contains(stderr.String(), "not built yet") {
		t.Errorf("the host refusal borrowed the GUEST sentence — `yolo host -- <cmd>` and "+
			"`yolo apply --at host` both ship, so \"not built yet\" is false here:\n%s",
			stderr.String())
	}
	if strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
		t.Errorf("the launch got PAST the notch gate and refused for another reason — the "+
			"gate is not wired into Run:\n%s", stderr.String())
	}
}

// TestLaunchAtTheJailNotchIsNotRefused is the CONTROL, and without it the two cases above
// are satisfied by a gate that refuses every launch. Both the explicit `jail` and the
// absent key must walk straight past it into the ordinary pipeline — observed here as the
// repo-root refusal, the next thing this fixture hits.
func TestLaunchAtTheJailNotchIsNotRefused(t *testing.T) {
	for _, notch := range []string{"jail", ""} {
		name := notch
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(t *testing.T) {
			ws := notchGateWorkspace(t, notch)
			var stdout, stderr bytes.Buffer
			o := notchGateOptions(t, ws, &stdout, &stderr)

			Run(*o)

			if !strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
				t.Errorf("a %s-notch launch did not reach the repo-root gate — the notch "+
					"gate is refusing launches it must let through:\nstdout:\n%s\nstderr:\n%s",
					name, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "Refusing to launch: launch at the guest") {
				t.Errorf("a %s-notch launch was refused as guest:\n%s", name, stderr.String())
			}
		})
	}
}

// TestLaunchRefusesTheNotchTheFlagAsked is DP-B22's other half: `--at` must be JUDGED, not
// merely consumed. cli.parseRunArgs folds the flag onto Options.Notch (its own tests pin
// that); this is the only thing that makes the value mean anything.
//
// The workspace declares NO confinement, so the config resolves to `jail` and a launch
// reaching the pipeline refuses on the repo root. Every refusal below therefore proves the
// override ran — delete the `if o.Notch != ""` block in refuseUnbuiltNotch and all of them
// fall through to that other message.
func TestLaunchRefusesTheNotchTheFlagAsked(t *testing.T) {
	cases := []struct {
		notch  string
		rc     int
		want   string
		reject string
	}{
		{notch: "guest", rc: 1, want: render.NotchUnbuilt("launch")},
		{notch: "host", rc: 1, want: "yolo host -- <cmd>"},
		// An unknown value fails CLOSED. config.ResolveConfinement answers `jail` for a
		// value it does not know, so without this a typo'd `--at gest` would silently
		// launch a jail — an override that failed open. rc 2, the usage code `yolo
		// apply --at` already uses for the same mistake.
		{notch: "gest", rc: 2, want: "is not a confinement level"},
	}
	for _, tc := range cases {
		t.Run(tc.notch, func(t *testing.T) {
			ws := notchGateWorkspace(t, "") // no `confinement` key: the flag is the only input
			var stdout, stderr bytes.Buffer
			o := notchGateOptions(t, ws, &stdout, &stderr)
			o.Notch = tc.notch

			rc := Run(*o)

			if rc != tc.rc {
				t.Fatalf("Run() with --at %s = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					tc.notch, rc, tc.rc, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("--at %s: want substring %q\ngot stderr:\n%s",
					tc.notch, tc.want, stderr.String())
			}
			if strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
				t.Errorf("--at %s was ignored — the launch ran on to the next refusal, "+
					"which is the flag being swallowed with extra steps:\n%s",
					tc.notch, stderr.String())
			}
		})
	}
}

// TestTheRefusalNamesWhatTheReaderCanChange: a user who typed `--at guest` must not be told
// to edit a `confinement` key they never wrote, and a user whose config carries the key
// must not be told to drop a flag they never passed. Same notch, two remedies.
func TestTheRefusalNamesWhatTheReaderCanChange(t *testing.T) {
	// (1) From the flag.
	ws := notchGateWorkspace(t, "")
	var so, se bytes.Buffer
	o := notchGateOptions(t, ws, &so, &se)
	o.Notch = "guest"
	Run(*o)
	if !strings.Contains(se.String(), "--at guest") || !strings.Contains(se.String(), "Drop the flag") {
		t.Errorf("a flag-driven refusal must name the flag and say to drop it:\n%s", se.String())
	}

	// (2) From the config, with no flag in sight.
	ws2 := notchGateWorkspace(t, "guest")
	var so2, se2 bytes.Buffer
	o2 := notchGateOptions(t, ws2, &so2, &se2)
	Run(*o2)
	if !strings.Contains(se2.String(), "in your config") {
		t.Errorf("a config-driven refusal must name the config key:\n%s", se2.String())
	}
	if strings.Contains(se2.String(), "Drop the flag") {
		t.Errorf("a config-driven refusal told the reader to drop a flag they never "+
			"passed:\n%s", se2.String())
	}
}

// TestTheFlagOutranksTheConfigWhenBothAreJail is the control for the override itself: `--at
// jail` over a `confinement: guest` config must LAUNCH (reaching the repo-root refusal),
// or the override is not an override — it is a second refusal condition.
func TestTheFlagOutranksTheConfigWhenBothAreJail(t *testing.T) {
	ws := notchGateWorkspace(t, "guest")
	var stdout, stderr bytes.Buffer
	o := notchGateOptions(t, ws, &stdout, &stderr)
	o.Notch = "jail"

	Run(*o)

	if !strings.Contains(stderr.String(), "Cannot find yolo-jail repo root") {
		t.Errorf("`--at jail` did not override `confinement: guest` — the flag is the "+
			"per-launch escape valve and must win:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}
