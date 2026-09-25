package run

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// emptyLoopholeDirs empties every loophole source this process can see, so the golden
// argv is hermetic (no loophole runtime args at all). Real production discovers the
// official packs' loopholes; the loophole runtime-args builder is exercised by
// internal/loopholes' own tests.
//
// It used to point BundledLoopholesDir at an empty temp dir. With that channel retired
// (docs/design/broker-as-a-pack.md OQ-BP4), the source that would otherwise leak in is the
// PACK-MODULE RECORD — including this package's own lazily-registered resolver, which
// reads the developer's real user config — so that is what has to be emptied.
//
// It also points the RETIRED hand-placed dir (OQ-LP10) at an empty tree. That directory
// contributes no loopholes any more, but it does produce a one-off migration WARNING when
// it is populated — and a developer whose real home still has one would otherwise see that
// line inside these tests.
func emptyLoopholeDirs(t *testing.T) {
	t.Helper()
	empty := t.TempDir()
	origR := loopholes.RetiredUserLoopholesDir
	loopholes.RetiredUserLoopholesDir = func() string { return empty }
	loopholes.SetPackModuleResolver(nil)
	loopholes.ResetPackModules()
	t.Cleanup(func() {
		loopholes.RetiredUserLoopholesDir = origR
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
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
		// The persistent timing opt-in reads the REAL user config unless pinned,
		// so a maintainer with `perf_logging: true` in their own
		// ~/.config/yolo-jail/config.jsonc would flip every fixture here to a
		// timing launch — failing the off-path assertions on their machine and
		// nowhere else. Pinned for the same reason Getenv and PathExists are.
		PerfLoggingConfig: func() bool { return false },
	}
	fillDefaults(o)
	// fillDefaults would set real Getenv etc.; re-apply the deterministic stubs.
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(string) (string, bool) { return "", false }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.PerfLoggingConfig = func() bool { return false }
	return o
}

// TestAssembleRunCmdPodmanLinuxGolden pins the ordered container argv for a
// minimal podman/linux launch (single claude agent, empty security, no
// network/mounts/devices). The whole argv is a frozen contract — it must not
// drift, since the entrypoint depends on the exact flags and env.
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
		cfg:          cfg,
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		homeSkeleton: goldenHomeSkeleton,
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
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
				packs:        claudePackFixture(t),
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

// cacheRelocationMounts returns the relocation mounts in argv order. It matches
// on the -v FLAG, not on the string alone, so the NPM_CONFIG_CACHE env value
// (/home/agent/.cache/npm) can never be mistaken for a mount.
func cacheRelocationMounts(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.Contains(argv[i+1], ":/home/agent/.cache/") {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// relocationInput builds the minimal assembleInput used by the cache-relocation
// cases (same fixture shape as the golden, minus the pieces they don't touch).
func relocationInput(t *testing.T, rt, wsState string, rels []config.CacheRelocation) *assembleInput {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return &assembleInput{
		cfg:              newConfig("agents", []any{"claude"}, "security", sec),
		rt:               rt,
		cname:            "yolo-ws-abcd1234",
		imageRef:         goldenImageRef,
		jailPrefix:       goldenJailPrefix,
		packs:            claudePackFixture(t),
		agentsPath:       "/agents/yolo-ws-abcd1234",
		homeSkeleton:     goldenHomeSkeleton,
		wsState:          wsState,
		miseStore:        "/mise-store",
		yoloVersion:      "9.9.9-test",
		mountTargets:     map[string]struct{}{},
		cacheRelocations: rels,
	}
}

// TestAssembleCacheRelocations covers the emitted -v pairs. It asserts the pairs
// are PRESENT, never their position relative to the .cache mount: podman sorts
// mounts by destination depth (proven on 5.8.4), so argv order is not an
// invariant and pinning it would freeze a non-contract. What IS ours to
// guarantee is that two relocations come out in a deterministic (sorted) order.
func TestAssembleCacheRelocations(t *testing.T) {
	cases := []struct {
		name string
		rels []config.CacheRelocation
		want []string
	}{
		{"none", nil, nil},
		{
			"one",
			[]config.CacheRelocation{{Subdir: "huggingface", Target: "/data/relocated/huggingface"}},
			[]string{"/data/relocated/huggingface:/home/agent/.cache/huggingface"},
		},
		{
			// Deliberately reverse-ordered: the emitter sorts, so the argv does
			// not depend on how the caller collected the map.
			"two sorted by subdir",
			[]config.CacheRelocation{
				{Subdir: "uv", Target: "/data/relocated/uv"},
				{Subdir: "huggingface", Target: "/data/relocated/huggingface"},
			},
			[]string{
				"/data/relocated/huggingface:/home/agent/.cache/huggingface",
				"/data/relocated/uv:/home/agent/.cache/uv",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)

			got := cacheRelocationMounts(o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", tc.rels)))
			if !slices.Equal(got, tc.want) {
				t.Errorf("relocation mounts = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAssembleCacheRelocationsNoneMatchesGolden pins the no-relocations case
// against the frozen argv: adding the feature must be a pure no-op for every
// user who has not configured it.
func TestAssembleCacheRelocationsNoneMatchesGolden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	got := o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", nil))
	if !slices.Equal(got, podmanLinuxGolden(home)) {
		t.Errorf("argv drifted from the golden with no relocations:\ngot:  %v\nwant: %v", got, podmanLinuxGolden(home))
	}
}

// TestAssembleCacheRelocationsAppleContainerSkips is work item 6: the Apple
// Container path must emit no relocation mount at all and say so once —
// half-applying one would leave the jail writing to the filesystem the user
// moved the cache off, silently.
func TestAssembleCacheRelocationsAppleContainerSkips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	var buf bytes.Buffer
	o.Stderr = &buf

	// A real ws_state dir: the Apple Container branch materializes files into it.
	got := o.assembleRunCmd(relocationInput(t, "container", t.TempDir(), []config.CacheRelocation{
		{Subdir: "uv", Target: "/data/relocated/uv"},
		{Subdir: "huggingface", Target: "/data/relocated/huggingface"},
	}))

	if mounts := cacheRelocationMounts(got); mounts != nil {
		t.Errorf("Apple Container emitted relocation mounts: %v", mounts)
	}
	warning := buf.String()
	if strings.Count(warning, "Skipping cache_relocations") != 1 {
		t.Errorf("want exactly one skip warning, got:\n%s", warning)
	}
	for _, want := range []string{"huggingface, uv", "YOLO_RUNTIME=podman"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning missing %q; got:\n%s", want, warning)
		}
	}
}

// writableHomeDirMounts extracts the -v pairs that bind a writable-home backing
// dir over a /home/agent path (the writable-home subdir on the source side is
// the discriminator, so this never catches the base overlays).
func writableHomeDirMounts(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.Contains(argv[i+1], "/writable-home/") {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// TestAssembleWritableHomeDirs covers the emitted -v pairs: each declared path
// binds <wsState>/writable-home/<path> over /home/agent/<path>, sorted.
func TestAssembleWritableHomeDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	// Deliberately reverse-ordered: the emitter sorts.
	in.writableHomeDirs = []string{".pi-lens", ".foo/bar"}
	got := writableHomeDirMounts(o.assembleRunCmd(in))
	want := []string{
		"/ws/.yolo/home/writable-home/.foo/bar:/home/agent/.foo/bar",
		"/ws/.yolo/home/writable-home/.pi-lens:/home/agent/.pi-lens",
	}
	if !slices.Equal(got, want) {
		t.Errorf("writable-home mounts = %v, want %v", got, want)
	}
}

// TestAssembleWritableHomeDirsNoneMatchesGolden pins the empty case against the
// frozen argv: the feature must be a pure no-op for anyone not using it.
func TestAssembleWritableHomeDirsNoneMatchesGolden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	got := o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", nil))
	if !slices.Equal(got, podmanLinuxGolden(home)) {
		t.Errorf("argv drifted from the golden with no writable_home_dirs:\ngot:  %v\nwant: %v", got, podmanLinuxGolden(home))
	}
}

// TestAssembleNeverBindsRepoSource guards the invariant that replaced the old
// D2 repo-bind gate: the in-jail CLI now finds its repo via the baked
// /opt/yolo-jail bundle (exe-relative, internal/reporoot), so the assembled
// argv must NEVER bind a /opt/yolo-jail source tree and NEVER set
// YOLO_REPO_ROOT — regardless of host repo-root resolution. --workdir
// /workspace stays (it's the container cwd, unrelated to any source bind).
func TestAssembleNeverBindsRepoSource(t *testing.T) {
	ws := "/ws"
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	got := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "unknown",
		mountTargets: map[string]struct{}{},
	})

	for _, a := range got {
		if strings.HasPrefix(a, "YOLO_REPO_ROOT=") {
			t.Errorf("argv set %q; YOLO_REPO_ROOT must never be present", a)
		}
		if strings.HasSuffix(a, ":/opt/yolo-jail:ro") {
			t.Errorf("argv bound a repo source at %q; want no /opt bind", a)
		}
	}
	// --workdir /workspace must still be present.
	if i := slices.Index(got, "--workdir"); i < 0 || i+1 >= len(got) || got[i+1] != "/workspace" {
		t.Errorf("--workdir /workspace missing; argv: %v", got)
	}
}

// goldenHomeSkeleton is the fixture's per-jail home skeleton (buildHomeSkeleton), the
// directory the golden argv binds :ro at /home/agent. A constant rather than a built
// directory because assembly only EMITS the path; TestEveryHomeBindHasASkeletonEntry is
// where a real skeleton is built and checked against the argv.
const goldenHomeSkeleton = "/agents/yolo-ws-abcd1234/home/20260925T000000Z-golden"

// podmanLinuxGolden is the expected ordered argv.
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
	// podman base mounts. The home root is THIS JAIL'S skeleton, not the machine store
	// (<state>/home) every podman jail used to share: that store's only mounts left are the
	// selected packs' shared dirs, below (docs/design/base-home-legacy-state.md#29-backends).
	add("-v", ws+":/workspace",
		"-v", goldenHomeSkeleton+":/home/agent:ro",
		"-v", wsState+"/npm-global:/home/agent/.npm-global",
		"-v", wsState+"/local:/home/agent/.local",
		"-v", wsState+"/go:/home/agent/go",
		// ONE anchor for BOTH generated-script dirs: blockers in bin/block (first on
		// PATH), lazy installers in bin/launch (last, after /bin). They are separate
		// DIRECTORIES for that ordering, but one rw bind suffices because they share a
		// parent — and a bind is needed at all only because /home/agent is :ro.
		"-v", wsState+"/yolo-bin:/home/agent/.yolo/bin",
		"-v", wsState+"/config:/home/agent/.config",
		"-v", globalCache+":/home/agent/.cache",
		"-v", wsState+"/yolo-bootstrap.sh:/home/agent/.yolo-bootstrap.sh",
		"-v", wsState+"/yolo-venv-precreate.sh:/home/agent/.yolo-venv-precreate.sh",
		"-v", wsState+"/yolo-perf.log:/home/agent/.yolo-perf.log",
		"-v", wsState+"/yolo-socat.log:/home/agent/.yolo-socat.log",
		"-v", wsState+"/yolo-entrypoint.lock:/home/agent/.yolo-entrypoint.lock",
		"-v", wsState+"/yolo-ca-bundle.crt:/home/agent/.yolo-ca-bundle.crt",
		"-v", wsState+"/bash_history:/home/agent/.bash_history",
		"-v", wsState+"/ssh:/home/agent/.ssh",
		"-v", "/mise-store:/mise")
	// THE INSTALL PREFIX — yolo's own binaries and the flake bundle beside them,
	// bind-mounted rather than baked (jailprefix.go). Two mounts because the host
	// layout (`bin/linux-<arch>/` beside flake.nix, what `just install` stages) is
	// not the prefix layout (`bin/` beside `share/yolo-jail/`), and the in-jail
	// resolver only ever asks for <exeDir>/../share/yolo-jail.
	//
	// This is the pair whose absence means the container has no pid1: the argv's
	// last element names /opt/yolo-jail/bin/yolo-entrypoint inside it. It is
	// pinned HERE, in the frozen argv, because that is the only place a launch
	// that stopped emitting it would show up before a container failed to start.
	add("-v", "/host/prefix/bin/linux-test:/opt/yolo-jail/bin:ro",
		"-v", "/host/prefix:/opt/yolo-jail/share/yolo-jail:ro")
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
		// yolo ships no default mise tool — every default runtime is a baked nix
		// package instead (config.go defaultMiseToolsKeys is deliberately empty).
		"-e", "YOLO_MISE_TOOLS={}",
		"-e", "YOLO_LSP_SERVERS={}",
		"-e", "YOLO_MCP_SERVERS={}",
		"-e", "YOLO_MCP_PRESETS=[]",
		// Empty because no user config declares `agent_updates`; the jail defaults OPEN.
		"-e", "YOLO_AGENT_UPDATES=",
		// The three provider/profile wire tables are NOT on the argv: they cross in
		// yolo-user-env.sh's channel section with the pack env fold and the shape
		// vars (writeUserEnvFile), so the container's frozen environment holds no
		// provider state for a later exec to inherit — per-entry delivery. The
		// tables still cross on every launch (bedrock's empty shape included,
		// because packs/claude ships it); the file is where.
		// No YOLO_REQUIRED_CAPABILITIES: `required_capabilities` is judged on the HOST
		// now (preflight.go's refuseUnmetCapabilities, OQ-CAP2) and nothing in the jail
		// ever read the variable. This golden is what pins the absence — re-adding the
		// export fails here, which is the only place that would notice.
		"-e", "YOLO_RUNTIME=podman",
	)
	// yolo-user-env.sh mount.
	add("-v", wsState+"/yolo-user-env.sh:/home/agent/.config/yolo-user-env.sh")
	// container cwd only — no repo source bind (the image bakes the flake bundle
	// at /opt/yolo-jail; internal/reporoot resolves it exe-relative in-jail).
	add("--workdir", "/workspace")
	// podman nesting (host branch; /dev/net/tun absent).
	add("--security-opt", "label=disable",
		"--device", "/dev/fuse",
		"--uidmap", "0:0:1", "--uidmap", "1:1:65536",
		"--gidmap", "0:0:1", "--gidmap", "1:1:65536",
		"--cap-add", "SYS_ADMIN", "--cap-add", "MKNOD", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW")
	// no host nix (paths absent), bridge net (no --net flag), no identity env,
	// no gitignore (~/.config/git/ignore absent), no publish/mounts.
	//
	// The host-loopback disposition, on EVERY launch (OQ-R6). This fixture answers no
	// LookPath, so the decision reaches no conclusion — which is a state with a
	// spelling of its own now rather than an omission, precisely so that an absent
	// variable can mean one thing only: a launcher older than it. The in-jail witness
	// never escalates on this value (internal/entrypoint/reachability.go).
	add("-e", paths.HostLoopbackEnvVar+"="+paths.HostLoopbackUnknown)
	// host services sockets dir mount (podman, always).
	add("-v", hostServiceSocketsDir("yolo-ws-abcd1234", false)+":/run/yolo-services:rw")
	// devices/gpu/kvm: none. resources: podman always gets --pids-limit 32768.
	add("--pids-limit", "32768")
	// nvim/overmind/workspace_readonly: none (the fixture has no .overmind.sock).
	// per-side shadows, sorted: .venv then node_modules (host paths absent → dir
	// mounts added anyway, which is the same unconditional behaviour .venv has
	// always had). node_modules joined the DEFAULT set on 2026-08-23 — see
	// venvShadowMountArgs for why it is a correctness fix before it is a security
	// one.
	add("-v", wsState+"/venv-shadows/.venv:/workspace/.venv")
	add("-v", wsState+"/venv-shadows/node_modules:/workspace/node_modules")
	// user config mount: none (no ~/.config/yolo-jail/config.jsonc). MISE_DISABLE
	// defaults to "pnpm".
	add("-e", "MISE_DISABLE_TOOLS=pnpm")
	// skills mount (claude has .claude/skills).
	add("-v", agentsPath+"/skills-claude:/home/agent/.claude/skills:ro")
	// The host-layer report, on EVERY launch (OQ-CO10) — the same always-emit rule the
	// loopback disposition above follows, and for the same reason: the jail's host-layer
	// read fails closed, so an ABSENT variable has to mean "a launcher older than it" and
	// nothing else. `delivered` is empty here because this fixture's home has no
	// ~/.claude/settings.json, which is the state the jail must NOT refuse for.
	add("-e", packload.HostLayerEnvVar+`={"delivery":"supported"}`)
	// PACK-DECLARED briefing mount: the claude pack declares AGENTS.md -> .claude/CLAUDE.md.
	// The staged name is per-DESTINATION since docs/reference/agent-briefings.md#ba-r2 — RFC 6901-escaped (`/`
	// → `~1`), injective, computed by run.briefingStagingName, which the write half uses too.
	// It was per-PACK while every destination received the same composed body; once the body
	// varies with the destination's audience, the pack no longer identifies a file. The literal
	// is spelled out rather than computed so this golden still pins the ENCODING: a change to
	// the escape is a host-side staging rename, and R2 is about exactly that going unnoticed.
	add("-v", agentsPath+"/briefing-.claude~1CLAUDE.md:/home/agent/.claude/CLAUDE.md:ro")
	// image + entrypoint. The ref is the fixture's, NOT a constant: since C2 the
	// image is addressed by the hash of the store path it was built from, so an
	// assembler that re-derived a name here — the pre-C2 jailImageRef(rt) — would
	// put a stale :latest on the argv while the load pipeline had prepared a
	// different image. That is the drift this element exists to catch.
	add(goldenImageRef, JailEntrypointPath)
	return a
}

// A13: the user scope must actually REACH the jail. The entrypoint and the in-jail verbs
// read $HOME/.config/yolo-jail/config.jsonc, and for the feature's first week nothing
// mounted anything there — a documented input with no channel.
//
// What it delivers is now GENERATED per consumer rather than bound off the human's disk
// (OQ-LP9), and its CONTENT is asserted in inheritscope_test.go. This is the one assertion
// that stays here: that the destination path appears on the argv at all. It used to name
// the host's config.lua as a second destination; that bind went with the Lua transform
// (docs/reference/pack-system.md#oq-lt1), and its absence is pinned by
// TestUserConfigLuaNoLongerCrosses.
func TestUserConfigMountDeliversTheGeneratedConfig(t *testing.T) {
	_, wsState := inheritHome(t, `{"packs": ["claude"]}`)
	o := inheritOptions(t)
	joined := strings.Join(o.userConfigMountArgs("podman", wsState), " ")
	if want := "/home/agent/.config/yolo-jail/config.jsonc"; !strings.Contains(joined, want) {
		t.Errorf("mount args missing %s:\n%s", want, joined)
	}
}

// claudePackFixture is the OFFICIAL claude pack, loaded the way a real run loads it.
//
// The golden argv asserts pack-DECLARED mounts (writable .claude, the shared
// credentials dir), so the fixture has to come from the real pack manifest rather than
// a hand-written stub — otherwise the golden would pin what the test author believed
// the pack says instead of what it says.
// claudeBriefingDest is the destination packs/claude declares for its briefing, and since
// docs/reference/agent-briefings.md#ba-r2 it is also the STAGING KEY (briefingStagingName is keyed by
// destination, not by pack — the composed content now varies per destination, so the pack no
// longer identifies a file). Named rather than repeated in five test files so a change to the
// shipped pack's `into` breaks in one place.
const claudeBriefingDest = ".claude/CLAUDE.md"

func claudePackFixture(t *testing.T) []*packload.Pack {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == "claude" {
			return []*packload.Pack{p}
		}
	}
	t.Fatal("official claude pack not found")
	return nil
}

// --- Apple Container's pack-tree delivery (issue #44) --------------------------------

// acPackInput builds the minimal assembleInput for an Apple Container launch whose
// staged pack tree is `staging`, and returns it with the options that assemble it.
//
// The staged tree is the REAL official claude pack copied through the REAL production
// copier (copyTree, the one stagePacks uses for an embedded pack), laid out at the
// `_official/<name>` path stagePacks produces — so what these tests deliver is what a
// launch delivers, rather than a hand-written pack.json the entrypoint might reject.
func acPackInput(t *testing.T, ws, home string) (*Options, *assembleInput, string) {
	t.Helper()
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)
	o.IsMacOS = true
	o.IsLinux = false

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	packs := claudePackFixture(t)
	staging := filepath.Join(t.TempDir(), "packs")
	if err := copyTree(packs[0].Root, filepath.Join(staging, "_official", "claude")); err != nil {
		t.Fatalf("staging the fixture pack: %v", err)
	}

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return o, &assembleInput{
		cfg:          newConfig("security", sec),
		rt:           "container",
		cname:        "yolo-ws-abcd1234",
		packs:        packs,
		agentsPath:   filepath.Join(ws, "agents"),
		packStaging:  staging,
		wsState:      wsState,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}, wsState
}

// packRootFromArgv returns the YOLO_PACK_ROOT value the argv names, or "".
func packRootFromArgv(argv []string) string {
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, "YOLO_PACK_ROOT="); ok {
			return v
		}
	}
	return ""
}

// TestAppleContainerDeliversThePackTreeIntoTheJail is the regression for issue #44, and
// it is deliberately written as "the jail can open what it was told", not as "the argv
// contains a string".
//
// The bug was a FALSE PREMISE, not a typo: the branch named the launcher's own host path
// in YOLO_PACK_ROOT on the belief that "the AC host filesystem is visible". Apple
// Container exposes only what the launch shares, so the variable pointed at nothing, the
// entrypoint read an absent root as "no packs", and a fresh Mac on the backend the README
// recommends got a jail with no agent in it — while every surface that needs no pack tree
// (guardrails shims, briefings, the managed settings.json) kept rendering, so it looked
// provisioned.
//
// A string assertion could not have caught that and cannot protect against its return:
// the old argv was internally consistent. So this test follows the variable — it maps the
// in-jail path back through the ws_state → /home/agent bind and hands it to the
// entrypoint's own LoadJailPacks, which is the reader the launcher is talking to. Delete
// the materialize call and the variable names an empty tree; delete the whole arm and
// there is no variable at all. Both fail here.
func TestAppleContainerDeliversThePackTreeIntoTheJail(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	o, in, wsState := acPackInput(t, ws, home)

	got := o.assembleRunCmd(in)

	root := packRootFromArgv(got)
	if root == "" {
		t.Fatalf("Apple Container launch names no YOLO_PACK_ROOT at all, so the jail "+
			"renders no pack:\n%s", strings.Join(got, " "))
	}
	if root == in.packStaging {
		t.Fatalf("issue #44: YOLO_PACK_ROOT=%s is a HOST path, and Apple Container shares "+
			"only what the launch shares — nothing is at that path in the jail", root)
	}
	// The one thing the jail is guaranteed to see on this backend is ws_state, mounted
	// whole at /home/agent. Resolve the promise the launcher just made through that bind.
	inJail, ok := strings.CutPrefix(root, "/home/agent/")
	if !ok {
		t.Fatalf("YOLO_PACK_ROOT=%s is not under /home/agent, which is the only tree "+
			"Apple Container's base mounts put in the jail", root)
	}
	delivered := filepath.Join(wsState, inJail)

	// WHAT IS AT THAT PATH, file for file. A relative-path-and-bytes comparison against
	// the staged tree is what catches the layout mistakes a "the directory exists" check
	// would wave through — a copy that nested the tree one level deeper (the `mv src dst`
	// trap StagePackCommands names), or one that dropped a pack's nested files. The
	// entrypoint walks <root>/_official/<name> and <root>/<slug>, so the LAYOUT is the
	// contract, not the top directory.
	if diff := treeDiff(t, in.packStaging, delivered); diff != "" {
		t.Fatalf("the jail's pack root does not hold what the launch staged, so the "+
			"entrypoint renders something other than what the host read:\n%s", diff)
	}
	// And each pack in it still LOADS — the delivered copy is what LoadJailPacks parses,
	// and a copier that mangled a manifest would pass the byte comparison only by
	// mangling both sides, which it cannot: the left side is the launcher's own input.
	officialDir := filepath.Join(delivered, "_official")
	ents, err := os.ReadDir(officialDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("no embedded pack was delivered under %s (err=%v)", officialDir, err)
	}
	for _, e := range ents {
		p, problems := packload.LoadDir(filepath.Join(officialDir, e.Name()), e.Name())
		if len(problems) > 0 {
			t.Fatalf("the delivered copy of pack %s does not load: %v", e.Name(), problems)
		}
		if p.Name != e.Name() {
			t.Fatalf("delivered pack dir %s loads as %q", e.Name(), p.Name)
		}
	}

	// And NOT podman's answer: a `-v …:/ctx/packs:ro` on this backend would be a bind
	// whose `:ro` Apple Container ignores (roBindsUnsupported), handing the jail write
	// access to the launcher's own staging tree — which is the escalation the podman
	// arm's `:ro` exists to prevent, not a copy of the protection.
	if joined := strings.Join(got, " "); strings.Contains(joined, ":"+packCtxDir+":ro") {
		t.Errorf("Apple Container ignores :ro, so the staged tree must not be bound:\n%s",
			joined)
	}
}

// TestAppleContainerPackTreeReplacesThePreviousLaunchs pins the rm-before-copy half of
// acMaterializeTree, which is the same rule macosuser.StagePackCommands states for the
// same content: ws_state PERSISTS across launches, so a merge would keep delivering a
// pack the user dropped from `packs` forever — the jail-side twin of the bug
// pruneDroppedPackStaging exists to prevent on the host.
//
// It fails if the RemoveAll goes, while the test above stays green: a leftover tree is
// invisible to "can the jail read what I staged".
func TestAppleContainerPackTreeReplacesThePreviousLaunchs(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	o, in, wsState := acPackInput(t, ws, home)

	// What a previous launch left behind: a pack the config no longer names.
	dropped := filepath.Join(wsState, acPackRootRel, "_official", "dropped")
	if err := os.MkdirAll(dropped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropped, "pack.json"),
		[]byte(`{"name":"dropped"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	o.assembleRunCmd(in)

	if _, err := os.Stat(dropped); err == nil {
		t.Errorf("a pack the user dropped from `packs` is still delivered at %s — "+
			"ws_state persists across launches, so the copy has to REPLACE the tree, "+
			"not merge into it", dropped)
	}
	if _, err := os.Stat(filepath.Join(wsState, acPackRootRel, "_official", "claude",
		"pack.json")); err != nil {
		t.Errorf("the replace took the live pack with it: %v", err)
	}
}

// TestAppleContainerSaysSoWhenThePackTreeCannotBeStaged pins the NO-SILENT-DROP half.
//
// The defect this whole cluster is about was not that packs failed to arrive; it was that
// nothing said so, and everything that needs no pack tree kept rendering, so the jail
// looked provisioned. A copy that cannot be made has to reach the terminal, and the argv
// must NOT then name a root: a half-copied tree renders SOME packs, which is the same
// lie one pack smaller.
//
// The fixture is the failure this repo has actually MEASURED rather than an invented one:
// the staging root vanishing mid-launch, which a concurrent capture sub-launch's
// housekeeping sweep really did do on 2026-09-09 (touchAgentStagingDir, and
// packFilesSkipWarning's second sentence). On podman it kills the container at `statfs`;
// on Apple Container it is a copy with no source, and this is what the user is told.
func TestAppleContainerSaysSoWhenThePackTreeCannotBeStaged(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	o, in, _ := acPackInput(t, ws, home)
	// STDERR: assembleRunCmd's notices go there by ruling (its `out` printer says why).
	var stderr bytes.Buffer
	o.Stderr = &stderr

	if err := os.RemoveAll(in.packStaging); err != nil {
		t.Fatal(err)
	}

	got := o.assembleRunCmd(in)

	if root := packRootFromArgv(got); root != "" {
		t.Errorf("the copy failed but the jail is still told YOLO_PACK_ROOT=%s, so it "+
			"will render whatever partially arrived", root)
	}
	if !strings.Contains(stderr.String(), "NO pack will render") {
		t.Errorf("a jail with no packs came up with nothing said about it:\n%s", stderr.String())
	}
}

// treeDiff returns a human-readable description of every relative path that differs
// between two trees (present on one side only, or different bytes), or "" when they hold
// the same files with the same contents. Modes are deliberately NOT compared: copyTree
// normalizes them to 0644/0755 by design, which is the rule packs.go states.
func treeDiff(t *testing.T, want, got string) string {
	t.Helper()
	read := func(root string) map[string]string {
		out := map[string]string{}
		if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return relErr
			}
			b, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			out[filepath.ToSlash(rel)] = string(b)
			return nil
		}); err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
		return out
	}
	w, g := read(want), read(got)
	var problems []string
	for rel, text := range w {
		switch other, ok := g[rel]; {
		case !ok:
			problems = append(problems, "missing from the jail's copy: "+rel)
		case other != text:
			problems = append(problems, "different bytes: "+rel)
		}
	}
	for rel := range g {
		if _, ok := w[rel]; !ok {
			problems = append(problems, "in the jail's copy but not staged: "+rel)
		}
	}
	slices.Sort(problems)
	return strings.Join(problems, "\n")
}

func TestAssembleForwardsTermAndColorterm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	cases := []struct {
		name          string
		term          string
		colorterm     string
		wantTerm      bool
		wantColorterm bool
	}{
		{"both present", "xterm-kitty", "truecolor", true, true},
		{"only term", "xterm-256color", "", true, false},
		{"only colorterm", "", "24bit", false, true},
		{"neither", "", "", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := goldenOptions("/ws", home)
			o.Getenv = func(k string) string {
				switch k {
				case "TERM":
					return tc.term
				case "COLORTERM":
					return tc.colorterm
				default:
					return ""
				}
			}

			sec := jsonx.NewOrderedMap()
			sec.Set("blocked_tools", []any{})
			got := o.assembleRunCmd(&assembleInput{
				cfg:          newConfig("agents", []any{"claude"}, "security", sec),
				rt:           "podman",
				cname:        "yolo-ws-abcd1234",
				packs:        claudePackFixture(t),
				agentsPath:   "/agents/yolo-ws-abcd1234",
				wsState:      "/ws/.yolo/home",
				miseStore:    "/mise-store",
				yoloVersion:  "9.9.9-test",
				mountTargets: map[string]struct{}{},
			})

			hasTerm := tc.term != "" && slices.Contains(got, "TERM="+tc.term)
			if hasTerm != tc.wantTerm {
				t.Errorf("TERM=%q present=%v, want %v; argv: %v", tc.term, hasTerm, tc.wantTerm, got)
			}
			hasColorterm := tc.colorterm != "" && slices.Contains(got, "COLORTERM="+tc.colorterm)
			if hasColorterm != tc.wantColorterm {
				t.Errorf("COLORTERM=%q present=%v, want %v; argv: %v", tc.colorterm, hasColorterm, tc.wantColorterm, got)
			}
		})
	}
}

// argvMount is one bind the argv makes: its host source and whether it is read-only.
type argvMount struct {
	src string
	ro  bool
}

// mountSources returns every bind the argv makes: `-v`/`--volume` specs and `--mount`
// fields (source=/src=, readonly/ro).
func mountSources(argv []string) []argvMount {
	var out []argvMount
	volume := func(spec string) {
		parts := strings.Split(spec, ":")
		m := argvMount{src: parts[0]}
		if len(parts) > 2 {
			for _, o := range strings.Split(parts[2], ",") {
				m.ro = m.ro || o == "ro"
			}
		}
		out = append(out, m)
	}
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; {
		case (a == "-v" || a == "--volume") && i+1 < len(argv):
			i++
			volume(argv[i])
		case strings.HasPrefix(a, "--volume="):
			volume(strings.TrimPrefix(a, "--volume="))
		case a == "--mount" && i+1 < len(argv):
			i++
			var m argvMount
			for _, kv := range strings.Split(argv[i], ",") {
				if v, ok := strings.CutPrefix(kv, "source="); ok {
					m.src = v
				} else if v, ok := strings.CutPrefix(kv, "src="); ok {
					m.src = v
				} else if kv == "readonly" || kv == "ro" || kv == "readonly=true" || kv == "ro=true" {
					m.ro = true
				}
			}
			out = append(out, m)
		}
	}
	return out
}

// TestAssembleNeverMountsTheEmbeddedPackCache pins the SECURITY half of where the embedded
// pack cache lives (paths.EmbeddedPacksDir). Host yolo loads that tree with a shipped pack's
// authority — host_files grants, loophole host exec — so a jail that could WRITE it could
// plant what the host then executes. It is safe because no launch mounts it writable: not
// the dir, nothing under it, and not the state dir wholesale above it (the state dir's
// cache/ child IS mounted rw, which is why the tree must never move there).
//
// Two pack sets. The launch's own (run.go hands assemble the STAGED set, whose roots live
// in the launch's staging dir): no mount may come from the cache at all. And the EMBEDDED
// set itself, adopted from the real location under this HOME — the shape a future caller
// passing packload.Embedded() would produce: still nothing WRITABLE. It is not "nothing":
// a pack `files` contribution binds its file out of Pack.Root read-only (packfiles.go), so
// such a caller would bind a cache file into the jail, pinning an inode `yolo prune` may
// later unlink. That is why launches stage.
func TestAssembleNeverMountsTheEmbeddedPackCache(t *testing.T) {
	check := func(t *testing.T, home string, argv []string, allowReadOnlyFiles bool) {
		t.Helper()
		state := paths.GlobalStorageUnder(home)
		cache := paths.EmbeddedPacksDirUnder(home)
		under := func(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) }
		sawState := false
		for _, m := range mountSources(argv) {
			if m.src == "" || !under(m.src, home) {
				continue
			}
			if under(m.src, state) && m.src != state {
				sawState = true
			}
			switch {
			case m.src == state || under(cache, m.src):
				t.Errorf("the launch bind-mounts %s (ro=%v), an ancestor of the embedded pack "+
					"cache %s — the jail would see it wholesale", m.src, m.ro, cache)
			case under(m.src, cache) && !(allowReadOnlyFiles && m.ro):
				t.Errorf("the launch bind-mounts %s (ro=%v) out of the embedded pack cache %s",
					m.src, m.ro, cache)
			}
		}
		if !sawState {
			t.Fatalf("no mount under the state dir %s was parsed out of the argv — the "+
				"assertions above are vacuous", state)
		}
	}
	adoptRealCache := func(t *testing.T, home string) []*packload.Pack {
		t.Helper()
		t.Cleanup(packload.OverrideEmbeddedCacheDir(paths.EmbeddedPacksDirUnder(home)))
		packs := packload.Embedded()
		if root, fb := packload.EmbeddedLocation(); fb || !strings.HasPrefix(root, paths.EmbeddedPacksDirUnder(home)) {
			t.Fatalf("setup: embedded tree at %s (fallback=%v): %v", root, fb, packload.EmbeddedProblems())
		}
		return packs
	}
	// Homes resolved where minted: the loader resolves its base through symlinks, and on
	// darwin t.TempDir() is under the /var -> /private/var symlink.
	resolvedTemp := func(t *testing.T) string {
		d, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	podman := func(t *testing.T, embedded bool) {
		home := resolvedTemp(t)
		t.Setenv("HOME", home)
		emptyLoopholeDirs(t)
		packs := claudePackFixture(t)
		adopted := adoptRealCache(t, home)
		if embedded {
			packs = adopted
		}
		o := goldenOptions("/ws", home)
		sec := jsonx.NewOrderedMap()
		sec.Set("blocked_tools", []any{})
		check(t, home, o.assembleRunCmd(&assembleInput{
			cfg:          newConfig("security", sec),
			rt:           "podman",
			cname:        "yolo-ws-abcd1234",
			packs:        packs,
			agentsPath:   "/agents/yolo-ws-abcd1234",
			wsState:      "/ws/.yolo/home",
			miseStore:    "/mise-store",
			yoloVersion:  "unknown",
			mountTargets: map[string]struct{}{},
		}), embedded)
	}
	apple := func(t *testing.T, embedded bool) {
		ws, home := t.TempDir(), resolvedTemp(t)
		t.Setenv("HOME", home)
		o, in, _ := acPackInput(t, ws, home)
		adopted := adoptRealCache(t, home)
		if embedded {
			in.packs = adopted
		}
		check(t, home, o.assembleRunCmd(in), embedded)
	}
	t.Run("podman, staged packs", func(t *testing.T) { podman(t, false) })
	t.Run("podman, embedded packs", func(t *testing.T) { podman(t, true) })
	t.Run("apple container, staged packs", func(t *testing.T) { apple(t, false) })
	t.Run("apple container, embedded packs", func(t *testing.T) { apple(t, true) })
}
