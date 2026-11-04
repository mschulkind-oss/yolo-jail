package run

// guestcontainerstep_test.go pins the macos-user arm's container next steps under named setups
// (named-setups-plan.md slice 4a): every macos-user message whose next step is a container names
// a container SETUP (`--setup podman-self`, `YOLO_SETUP=podman-self`), which a launch takes as
// typed, and never the removed YOLO_RUNTIME or `--at jail`, which a launch refuses.
//
// Each case drives Run() with Options.IsMacOS set and the Seatbelt setup selected through
// YOLO_SETUP, the way a user's launch does. The step is counted on the output with its
// whitespace folded, so a printer that wraps a line cannot hide it.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// guestLaunchResult is what one launch printed and handed the backend.
type guestLaunchResult struct {
	rc      int
	out     string
	env     *jsonx.OrderedMap
	reached bool
}

// launchSeatbelt runs one macOS launch of seatbelt-yolo-account with HOME as the caller set it and
// wsExtra (a run of `, "key": value` pairs) in a fresh workspace config. tweak edits the options last.
func launchSeatbelt(t *testing.T, wsExtra string, tweak func(*Options)) guestLaunchResult {
	t.Helper()
	ws := resolvedGuestWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"packages": []`+wsExtra+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.IsMacOS, o.IsLinux = true, false
	o.AcceptConfigChanges = true // the workspace config is new to this machine
	// A Mac set up for this backend, so the arm's preflight admits the fork slot (macosUserPreflight).
	o.MacosUserLaunchProbes = setUpMacProbes
	var got guestLaunchResult
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		got.reached, got.env = true, launchEnv
		return 0
	}
	if tweak != nil {
		tweak(o)
	}
	got.rc = Run(*o)
	got.out = stdout.String() + stderr.String()
	return got
}

// folded is s with every run of whitespace one space.
func folded(s string) string { return strings.Join(strings.Fields(s), " ") }

// containerSetupSteps is how many times out names a container setup as the next step.
func containerSetupSteps(out string) int {
	return strings.Count(folded(out), "podman-self")
}

// removedSpelling reports a removed selection a step must never name.
func removedSpelling(out string) string {
	for _, bad := range []string{"YOLO_RUNTIME", "--at jail", "`runtime: \"podman\"`", "\"runtime\": \"podman\""} {
		if strings.Contains(out, bad) {
			return bad
		}
	}
	return ""
}

// TestTheContextMountRefusalNamesAContainerSetup: a context mount this backend cannot deliver is
// refused with a container setup as one fix, and that step is one a launch takes.
func TestTheContextMountRefusalNamesAContainerSetup(t *testing.T) {
	home := ctxLaunchHome(t, `, "mounts": ["~/code/ref-repo"]`)
	if err := os.MkdirAll(filepath.Join(home, "code", "ref-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := launchSeatbelt(t, "", refusingSiting(t))
	if got.rc == 0 || got.reached {
		t.Fatalf("the launch was not refused for its context mount (rc %d, reached %v):\n%s",
			got.rc, got.reached, got.out)
	}
	if !strings.Contains(got.out, "Refusing the macos-user launch") ||
		!strings.Contains(got.out, "use a container setup: yolo --setup podman-self -- claude") {
		t.Fatalf("the refusal does not name a container setup:\n%s", got.out)
	}
	if bad := removedSpelling(got.out); bad != "" {
		t.Errorf("the refusal names the removed %q:\n%s", bad, got.out)
	}
}

// TestMacosUserNotesNameAContainerSetup covers every other run-pipeline printer on the macos-user
// arm whose next step is a container. Deleting the step from any one of them fails its case.
func TestMacosUserNotesNameAContainerSetup(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T)
		wsExtra string
		// marker is a phrase of the message, proving the fixture reached it.
		marker string
		// want is how many of the launch's messages name a container setup.
		want int
		env  func(t *testing.T, env *jsonx.OrderedMap)
	}{
		{
			name: "ephemeral_storage tmpfs",
			setup: func(t *testing.T) {
				writeUserPacks(t, packHome(t), `["claude"]`)
			},
			wsExtra: `, "ephemeral_storage": "tmpfs"`,
			marker:  "is not read on macos-user",
			want:    0, // its step is Apple Container: `yolo --setup apple-container-self`
		},
		{
			name: "a fork",
			setup: func(t *testing.T) {
				forkLaunchHome(t, forkPinSource)
				pinFork(t, strings.Repeat("ef", 20))
			},
			marker: "tool is not delivered on macos-user",
			want:   1,
			// The sandbox's own launcher for the program repeats the launch's reason, step and all.
			env: func(t *testing.T, env *jsonx.OrderedMap) {
				if d := handedOnMacosUser(t, env)["tool"]; containerSetupSteps(d.Reason) != 1 || removedSpelling(d.Reason) != "" {
					t.Errorf("the sandbox's reason for tool does not name the container setup alone: %q", d.Reason)
				}
			},
		},
		{
			// A refused host half whose jail daemon the sandbox declines too: the refusal line and
			// the Declined: line each name a container setup (macosuserdoorways.go, jaildaemondecline.go).
			name: "a service the sandbox cannot serve",
			setup: func(t *testing.T) {
				home := packHome(t)
				src := fetchedPackSource(t, map[string]string{"pack.json": `{"name": "acme", "contributes": [
		{"kind": "adapter", "adapts": {"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:8299"},
		{"kind": "service", "name": "acme-svc", "endpoint": "acme-svc.endpoint",
		 "jail_daemon": {"cmd": ["acme-svc"]},
		 "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-svc"]}}]}`})
				writeUserConfigJSON(t, home, `{"packs": [{"name": "acme", "source": "`+src+`"}]}`)
			},
			marker: "declined in the sandbox too",
			want:   2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			got := launchSeatbelt(t, tc.wsExtra, nil)
			if got.rc != 0 || !got.reached {
				t.Fatalf("the launch did not reach the backend (rc %d):\n%s", got.rc, got.out)
			}
			if !strings.Contains(folded(got.out), tc.marker) {
				t.Fatalf("the fixture did not reach the message %q:\n%s", tc.marker, got.out)
			}
			if n := containerSetupSteps(got.out); n != tc.want {
				t.Errorf("the container setup is named %d times, want %d:\n%s", n, tc.want, got.out)
			}
			if bad := removedSpelling(got.out); bad != "" {
				t.Errorf("a step names the removed %q, which a launch refuses:\n%s", bad, got.out)
			}
			if tc.env != nil {
				tc.env(t, got.env)
			}
		})
	}
}
