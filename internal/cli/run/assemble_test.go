package run

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// emptyLoopholeDirs points BundledLoopholesDir + UserLoopholesDir at empty temp
// dirs so the golden argv is hermetic (no bundled-loophole runtime args). Real
// production discovers the bundled loopholes; the loophole runtime-args builder
// is exercised by internal/loopholes' own tests.
func emptyLoopholeDirs(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	origB, origU := loopholes.BundledLoopholesDir, loopholes.UserLoopholesDir
	loopholes.BundledLoopholesDir = func() string { return empty }
	loopholes.UserLoopholesDir = func() string { return empty }
	t.Cleanup(func() {
		loopholes.BundledLoopholesDir = origB
		loopholes.UserLoopholesDir = origU
	})
}

// newConfig builds a config OrderedMap from key/value pairs.
func newConfig(pairs ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// goldenOptions returns Options wired for a deterministic podman/linux fixture:
// no binaries, no subprocesses, no tty, no device nodes present.
//
// Every platform/host-environment seam is pinned so the fixture describes a
// LINUX host regardless of the host the test runs on: IsMacOS/IsLinux (the
// compile-time platform), PathExists (device nodes, /run/.containerenv, the
// host nix store), Getenv, LookPath and Exec (host binaries), plus the tty
// probes. Assembly code must read these fields — never paths.IsLinux /
// paths.IsMacOS — or the golden argv silently diverges off Linux.
func goldenOptions(workspace, home string) *Options {
	o := &Options{
		Network:     "bridge",
		IsMacOS:     false,
		IsLinux:     true,
		Workspace:   workspace,
		Getenv:      func(string) string { return "" },
		LookPath:    func(string) (string, bool) { return "", false },
		Exec:        func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} },
		PathExists:  func(string) bool { return false },
		Now:         func() time.Time { return time.Unix(0, 0) },
		Getpid:      func() int { return 1 },
		IsTTYStdout: func() bool { return false },
		IsTTYStdin:  func() bool { return false },
	}
	fillDefaults(o)
	// fillDefaults would set real Getenv etc.; re-apply the deterministic stubs.
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(string) (string, bool) { return "", false }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	return o
}

// TestAssembleRunCmdPodmanLinuxGolden pins the ordered container argv for a
// minimal podman/linux launch (single claude agent, empty security, no
// network/mounts/devices). The whole argv must be byte-identical to the Python
// run() assembly.
func TestAssembleRunCmdPodmanLinuxGolden(t *testing.T) {
	ws := "/ws"
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	cfg := newConfig(
		"agents", []any{"claude"},
		"security", sec,
	)

	in := &assembleInput{
		cfg:           cfg,
		rt:            "podman",
		cname:         "yolo-ws-abcd1234",
		repoRoot:      "/repo",
		agentsList:    []string{"claude"},
		agentSpecs:    agents.ResolveAgents([]string{"claude"}),
		agentsPath:    "/agents/yolo-ws-abcd1234",
		wsState:       "/ws/.yolo/home",
		miseStore:     "/mise-store",
		yoloVersion:   "9.9.9-test",
		mountTargets:  map[string]struct{}{},
		lspNPMInstall: "",
		lspGoInstall:  "",
	}

	got := o.assembleRunCmd(in)
	want := podmanLinuxGolden(home)
	if len(got) != len(want) {
		t.Fatalf("argv length mismatch: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestAssemblePlatformSeamsInjectable is the regression for the host-dependent
// golden: the argv's two platform-conditional elements must follow the Options
// seams, so BOTH platform shapes are reachable from a single host. When the
// assembler read paths.IsLinux/paths.IsMacOS instead, this table was
// unwritable — every row produced the host's own answer, and the Linux golden
// failed on the macOS runner (--read-only-tmpfs=false dropped, the mise bind
// mount swapped for the named volume).
func TestAssemblePlatformSeamsInjectable(t *testing.T) {
	cases := []struct {
		name            string
		isLinux         bool
		isMacOS         bool
		wantROTmpfs     bool
		wantMiseMountAt string
	}{
		{"linux", true, false, true, "/mise-store:/mise"},
		{"macos", false, true, false, miseStoreVolume + ":/mise"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)
			o.IsLinux, o.IsMacOS = tc.isLinux, tc.isMacOS

			sec := jsonx.NewOrderedMap()
			sec.Set("blocked_tools", []any{})
			got := o.assembleRunCmd(&assembleInput{
				cfg:          newConfig("agents", []any{"claude"}, "security", sec),
				rt:           "podman",
				cname:        "yolo-ws-abcd1234",
				repoRoot:     "/repo",
				agentsList:   []string{"claude"},
				agentSpecs:   agents.ResolveAgents([]string{"claude"}),
				agentsPath:   "/agents/yolo-ws-abcd1234",
				wsState:      "/ws/.yolo/home",
				miseStore:    "/mise-store",
				yoloVersion:  "9.9.9-test",
				mountTargets: map[string]struct{}{},
			})

			if slices.Contains(got, "--read-only-tmpfs=false") != tc.wantROTmpfs {
				t.Errorf("--read-only-tmpfs=false present=%v, want %v (IsLinux=%v)",
					!tc.wantROTmpfs, tc.wantROTmpfs, tc.isLinux)
			}
			if !slices.Contains(got, tc.wantMiseMountAt) {
				t.Errorf("mise mount %q missing (IsMacOS=%v); argv: %v",
					tc.wantMiseMountAt, tc.isMacOS, got)
			}
		})
	}
}

// podmanLinuxGolden is the expected ordered argv, derived by reading run_cmd.py.
func podmanLinuxGolden(home string) []string {
	ws := "/ws"
	wsState := "/ws/.yolo/home"
	globalHome := filepath.Join(home, ".local", "share", "yolo-jail", "home")
	globalCache := filepath.Join(home, ".local", "share", "yolo-jail", "cache")
	claudeShared := filepath.Join(globalHome, ".claude-shared-credentials")
	agentsPath := "/agents/yolo-ws-abcd1234"

	var a []string
	add := func(xs ...string) { a = append(a, xs...) }

	// run_flags: base ["--rm","-i","--init","--read-only","--name",cname] then
	// insert("--cgroupns=private", 3) → --rm -i --init --cgroupns=private
	// --read-only --name cname; then --read-only-tmpfs=false --pull=never
	// --log-driver none --security-opt unmask=/proc/sys  (no -t: not a tty).
	add("podman", "run",
		"--rm", "-i", "--init", "--cgroupns=private", "--read-only", "--name", "yolo-ws-abcd1234",
		"--read-only-tmpfs=false", "--pull=never", "--log-driver", "none",
		"--security-opt", "unmask=/proc/sys")
	// podman base mounts.
	add("-v", ws+":/workspace",
		"-v", globalHome+":/home/agent:ro",
		"-v", wsState+"/npm-global:/home/agent/.npm-global",
		"-v", wsState+"/local:/home/agent/.local",
		"-v", wsState+"/go:/home/agent/go",
		"-v", wsState+"/yolo-shims:/home/agent/.yolo-shims",
		"-v", wsState+"/config:/home/agent/.config",
		"-v", globalCache+":/home/agent/.cache",
		"-v", wsState+"/yolo-bootstrap.sh:/home/agent/.yolo-bootstrap.sh",
		"-v", wsState+"/yolo-venv-precreate.sh:/home/agent/.yolo-venv-precreate.sh",
		"-v", wsState+"/yolo-perf.log:/home/agent/.yolo-perf.log",
		"-v", wsState+"/yolo-socat.log:/home/agent/.yolo-socat.log",
		"-v", wsState+"/yolo-entrypoint.lock:/home/agent/.yolo-entrypoint.lock",
		"-v", wsState+"/yolo-ca-bundle.crt:/home/agent/.yolo-ca-bundle.crt",
		"-v", wsState+"/yolo-installed-lsps:/home/agent/.yolo-installed-lsps",
		"-v", wsState+"/bash_history:/home/agent/.bash_history",
		"-v", wsState+"/ssh:/home/agent/.ssh",
		"-v", "/mise-store:/mise")
	// scratch mounts (volume mode default).
	add("-v", "/tmp", "-v", "/var/tmp", "-v", "/var/lib/containers", "-v", "/var/cache/containers",
		"--tmpfs", "/run", "--tmpfs", "/dev/shm:size=2g")
	// per-agent overlay dirs (claude → .claude).
	add("-v", wsState+"/claude:/home/agent/.claude")
	// claude shared credentials.
	add("-v", claudeShared+":/home/agent/.claude-shared-credentials")
	// common env block.
	add(
		"-e", "JAIL_HOME=/home/agent",
		"-e", "NPM_CONFIG_PREFIX=/home/agent/.npm-global",
		"-e", "NPM_CONFIG_CACHE=/home/agent/.cache/npm",
		"-e", "GOPATH=/home/agent/go",
		"-e", "MISE_DATA_DIR=/mise",
		"-e", "MISE_CACHE_DIR=/tmp/mise-cache",
		"-e", "MISE_PYTHON_PRECOMPILED_FLAVOR=install_only",
		"-e", "MISE_PYTHON_GITHUB_ATTESTATIONS=false",
		"-e", "MISE_TRUSTED_CONFIG_PATHS=/workspace",
		"-e", "MISE_ENV=jail",
		"-e", "RUSTUP_HOME=/mise/rustup",
		"-e", "CARGO_HOME=/mise/cargo",
		"-e", "MISE_YES=1",
		"-e", "COPILOT_ALLOW_ALL=true",
		"-e", "IS_SANDBOX=1",
		"-e", "LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/"+storage.LinuxMultilib(),
		"-e", "HOME=/home/agent",
		"-e", "EDITOR=cat",
		"-e", "VISUAL=nvim",
		"-e", "PI_TELEMETRY=0",
		"-e", "PAGER=cat",
		"-e", "GIT_PAGER=cat",
		"-e", "YOLO_BLOCK_CONFIG=[]",
		// no TZ (DetectHostTimezone off in fixture? env TZ unset, /etc probed by
		// real fs — see note in the test body).
		"-e", "YOLO_HOST_DIR="+ws,
		"-e", "YOLO_VERSION=9.9.9-test",
		"-e", "OVERMIND_SOCKET=/tmp/overmind.sock",
		"-e", "YOLO_MISE_TOOLS={\"neovim\": \"stable\"}",
		"-e", "YOLO_LSP_SERVERS={}",
		"-e", "YOLO_LSP_NPM_INSTALL=",
		"-e", "YOLO_LSP_GO_INSTALL=",
		"-e", "YOLO_MCP_SERVERS={}",
		"-e", "YOLO_MCP_PRESETS=[]",
		"-e", "YOLO_AGENTS=[\"claude\"]",
		"-e", "YOLO_RUNTIME=podman",
		"-e", "YOLO_REPO_ROOT=/opt/yolo-jail",
	)
	// yolo-user-env.sh mount.
	add("-v", wsState+"/yolo-user-env.sh:/home/agent/.config/yolo-user-env.sh")
	// repo mount (repoRoot has no flake.nix in fixture, workspace isn't a yolo
	// source tree → falls back to workspace).
	add("--workdir", "/workspace", "-v", ws+":/opt/yolo-jail:ro")
	// podman nesting (host branch; /dev/net/tun absent).
	add("--security-opt", "label=disable",
		"--device", "/dev/fuse",
		"--uidmap", "0:0:1", "--uidmap", "1:1:65536",
		"--gidmap", "0:0:1", "--gidmap", "1:1:65536",
		"--cap-add", "SYS_ADMIN", "--cap-add", "MKNOD", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW")
	// no host nix (paths absent), bridge net (no --net flag), no identity env,
	// no gitignore (~/.config/git/ignore absent), no publish/mounts.
	// host services sockets dir mount (podman, always).
	add("-v", hostServiceSocketsDir("yolo-ws-abcd1234", false)+":/run/yolo-services:rw")
	// devices/gpu/kvm: none. resources: podman always gets --pids-limit 32768.
	add("--pids-limit", "32768")
	// nvim/vscode/overmind/workspace_readonly: none.
	// per-side venv shadow: .venv (host /ws/.venv absent → dir mount added).
	add("-v", wsState+"/venv-shadows/.venv:/workspace/.venv")
	// user config mount: none (no ~/.config/yolo-jail/config.jsonc). MISE_DISABLE
	// defaults to "pnpm".
	add("-e", "MISE_DISABLE_TOOLS=pnpm")
	// skills mount (claude has .claude/skills).
	add("-v", agentsPath+"/skills-claude:/home/agent/.claude/skills:ro")
	// briefing mount (claude → .claude/CLAUDE.md).
	add("-v", agentsPath+"/CLAUDE.md:/home/agent/.claude/CLAUDE.md:ro")
	// image + entrypoint.
	add("localhost/yolo-jail:latest", "yolo-entrypoint")
	return a
}
