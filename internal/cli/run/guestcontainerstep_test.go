package run

// guestcontainerstep_test.go pins env-manager plan EMP-D5 on the run pipeline's side: at the guest
// notch on macOS, every macos-user message whose next step names a container runtime names the
// jail notch too (config.ContainerStepClause). The guest notch runs only on macos-user, and the
// notch gate refuses a container runtime beside it as a contradiction (EMP-D1), so "use a container
// runtime" alone sends the user from one refusal straight into another.
//
// Each case drives Run() with Options.IsMacOS set, `confinement: "guest"` in the workspace config
// and no runtime named, so the notch alone selects the backend, the way a user's launch does; and
// each has a control, the same fixture as a jail-notch macos-user launch (`runtime: "macos-user"`
// by YOLO_RUNTIME), whose wording must stay what it was. The clause is counted on the output with
// its whitespace folded, so a printer that wraps a line cannot hide it.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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

// launchMacosUserAt runs one macOS launch with HOME as the caller set it. guest true writes
// `confinement: "guest"` (and wsExtra, a run of `, "key": value` pairs) into a fresh workspace and
// names no runtime; false is the jail-notch control, `runtime: "macos-user"` through YOLO_RUNTIME
// with wsExtra alone. tweak edits the options last.
func launchMacosUserAt(t *testing.T, guest bool, wsExtra string, tweak func(*Options)) guestLaunchResult {
	t.Helper()
	ws := resolvedGuestWorkspace(t)
	body, ytoRuntime := `{"confinement": "jail"`+wsExtra+`}`, "macos-user"
	if guest {
		body, ytoRuntime = `{"confinement": "guest"`+wsExtra+`}`, ""
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, ytoRuntime, &stdout, &stderr, nil)
	o.IsMacOS, o.IsLinux = true, false
	o.AcceptConfigChanges = true // the workspace config is new to this machine
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

// guestClauseCount is how many times the guest's container-step clause appears in out.
func guestClauseCount(out string) int {
	return strings.Count(folded(out), folded(config.ContainerStepClause(config.ConfinementGuest)))
}

// TestMacosGuestContextMountRefusalNamesTheJailNotch is the reviewer's reproduction: a guest whose
// context mount this backend cannot deliver is refused with "use a container runtime", and that
// step must carry the jail notch. Then the step is followed — `--at jail` with YOLO_RUNTIME=podman
// — and the notch gate must pass it.
func TestMacosGuestContextMountRefusalNamesTheJailNotch(t *testing.T) {
	home := ctxLaunchHome(t, `, "mounts": ["~/code/ref-repo"]`)
	if err := os.MkdirAll(filepath.Join(home, "code", "ref-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := launchMacosUserAt(t, true, "", refusingSiting(t))
	if got.rc == 0 || got.reached {
		t.Fatalf("the guest launch was not refused for its context mount (rc %d, reached %v):\n%s",
			got.rc, got.reached, got.out)
	}
	if !strings.Contains(got.out, "Refusing the macos-user launch") || !strings.Contains(got.out, "container runtime") {
		t.Fatalf("the fixture did not reach the context-mount refusal:\n%s", got.out)
	}
	if n := guestClauseCount(got.out); n != 1 {
		t.Errorf("the guest's context-mount refusal names a container runtime without the jail "+
			"notch (clause seen %d times, want 1):\n%s", n, got.out)
	}

	followed := launchMacosUserAt(t, true, "", func(o *Options) {
		refusingSiting(t)(o)
		o.Notch = "jail"
		o.Getenv = func(k string) string {
			if k == "YOLO_RUNTIME" {
				return "podman"
			}
			return ""
		}
	})
	if strings.Contains(followed.out, "Refusing to launch: the guest notch") {
		t.Errorf("the step the refusal names is refused by the notch gate:\n%s", followed.out)
	}

	if ctl := runMacosUserExpectingRefusal(t, t.TempDir(), refusingSiting(t)); guestClauseCount(ctl) != 0 {
		t.Errorf("a jail-notch macos-user refusal grew the guest's clause:\n%s", ctl)
	}
}

// TestMacosGuestNotesNameTheJailNotchBesideAContainerRuntime covers every other run-pipeline printer
// on the macos-user arm whose next step names a container runtime. Deleting the clause from any
// one of them fails its case.
func TestMacosGuestNotesNameTheJailNotchBesideAContainerRuntime(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T)
		wsExtra string
		// marker is a phrase of the message the clause must follow, proving the fixture reached it.
		marker string
		// want is how many of the launch's messages name a container runtime.
		want int
		// env checks the launch env handed to the backend, at the guest notch only.
		env func(t *testing.T, env *jsonx.OrderedMap)
	}{
		{
			name: "ephemeral_storage tmpfs",
			setup: func(t *testing.T) {
				writeUserPacks(t, packHome(t), `["claude"]`)
			},
			wsExtra: `, "ephemeral_storage": "tmpfs"`,
			marker:  "is not read on macos-user",
			want:    1,
		},
		{
			name: "a directory host_files source",
			setup: func(t *testing.T) {
				home := ctxLaunchHome(t, `, "host_files": [{"path": ".config/big/", "source": "~/big/"}]`)
				writeHostFileAt(t, filepath.Join(home, "big", "a.txt"), "x\n", 0o644)
			},
			marker: "does not cross on macos-user",
			want:   1,
		},
		{
			name: "a fork",
			setup: func(t *testing.T) {
				forkLaunchHome(t, forkPinSource)
				pinFork(t, strings.Repeat("ef", 20))
			},
			marker: "tool is not delivered on macos-user",
			want:   1,
			// The sandbox's own launcher for the program repeats the launch's reason, clause and all.
			env: func(t *testing.T, env *jsonx.OrderedMap) {
				if d := handedOnMacosUser(t, env)["tool"]; guestClauseCount(d.Reason) != 1 {
					t.Errorf("the sandbox's reason for tool names a container runtime without the "+
						"jail notch: %q", d.Reason)
				}
			},
		},
		{
			name:   "a patched extension",
			setup:  func(t *testing.T) { treeLaunchHome(t, true) },
			marker: "extension " + treeKey + " is not delivered on macos-user",
			want:   1,
		},
		{
			// A refused host half whose jail daemon the guest declines too: the refusal line and the
			// Declined: line each name a container runtime (macosuserdoorways.go, jaildaemondecline.go).
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
			got := launchMacosUserAt(t, true, tc.wsExtra, nil)
			if got.rc != 0 || !got.reached {
				t.Fatalf("the guest launch did not reach the backend (rc %d):\n%s", got.rc, got.out)
			}
			if !strings.Contains(folded(got.out), tc.marker) {
				t.Fatalf("the fixture did not reach the message %q:\n%s", tc.marker, got.out)
			}
			if n := guestClauseCount(got.out); n != tc.want {
				t.Errorf("at the guest notch the clause naming the jail notch appears %d times, want %d — "+
					"a step names a container runtime alone, which the notch gate refuses:\n%s",
					n, tc.want, got.out)
			}
			if tc.env != nil {
				tc.env(t, got.env)
			}

			ctl := launchMacosUserAt(t, false, tc.wsExtra, nil)
			if ctl.rc != 0 || !strings.Contains(folded(ctl.out), tc.marker) {
				t.Fatalf("the jail-notch control did not reach %q (rc %d):\n%s", tc.marker, ctl.rc, ctl.out)
			}
			if n := guestClauseCount(ctl.out); n != 0 {
				t.Errorf("a jail-notch macos-user launch grew the guest's clause %d times:\n%s", n, ctl.out)
			}
		})
	}
}
