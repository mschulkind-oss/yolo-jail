package run

// seal_test.go pins THE SEAL (docs/design/forked-programs-as-packs.md FP-D9, seal.go) at the one
// place it can be seen whole: the container argv a sealed launch actually hands its runtime. The
// launch runs end to end through Run, with a fake `podman` on PATH recording what it is handed, under
// a user config declaring every crossing FP-D9 names — env_sources, host_files, mounts, a loophole
// pack with a machine-scope shared dir and host-layer surfaces, cache relocations, host port
// forwards — so each would cross on an ordinary launch.
//
// THE -e ASSERTION IS AN ALLOWLIST, so a crossing added to the pipeline later fails this test until
// someone decides whether a fork build may have it. The -v assertion is a containment rule: every
// bind source is this launch's own (its workspace, its state, its staged trees, yolo's binaries),
// never a host directory.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// sealedEnvAllowlist is every -e NAME a sealed launch may hand its jail. Adding one is a decision
// about what a fork's build may see; the test says which name was not on the list.
var sealedEnvAllowlist = map[string]bool{
	"CARGO_HOME": true, "COPILOT_ALLOW_ALL": true, "EDITOR": true, "GIT_PAGER": true, "GOPATH": true,
	"HOME": true, "IS_SANDBOX": true, "JAIL_HOME": true, "LD_LIBRARY_PATH": true, "MISE_CACHE_DIR": true,
	"MISE_DATA_DIR": true, "MISE_DISABLE_TOOLS": true, "MISE_ENV": true,
	"MISE_PYTHON_GITHUB_ATTESTATIONS": true, "MISE_PYTHON_PRECOMPILED_FLAVOR": true,
	"MISE_TRUSTED_CONFIG_PATHS": true, "MISE_YES": true, "NPM_CONFIG_CACHE": true, "NPM_CONFIG_PREFIX": true,
	"OVERMIND_SOCKET": true, "PAGER": true, "RUSTUP_HOME": true, "TZ": true,
	"VISUAL": true, "YOLO_AGENT_UPDATES": true, "YOLO_BLOCK_CONFIG": true, "YOLO_CONTEXT_DIR": true,
	"YOLO_CONTRACT_TAGS": true, "YOLO_DURABLE_DIR": true, "YOLO_HOST_DIR": true, "YOLO_HOST_LAYERS": true,
	"YOLO_HOST_LOOPBACK": true, "YOLO_JAIL_MAIN": true, "YOLO_LSP_SERVERS": true, "YOLO_MCP_PRESETS": true,
	"YOLO_MCP_SERVERS": true, "YOLO_MISE_TOOLS": true, "YOLO_PACK_ROOT": true, "YOLO_RUNTIME": true,
	"YOLO_VERSION": true,
}

// sealedLaunch runs one sealed launch under a user config declaring every crossing, with a fake
// podman recording its argv, and returns the argv, the workspace and what the launch printed.
func sealedLaunch(t *testing.T, sealed bool) (argv []string, ws, home, printed string) {
	t.Helper()
	home = packHome(t)
	for _, d := range []string{"notes", ".config/nvim", ".config/git", ".claude", "bigdisk/hf"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for rel, body := range map[string]string{
		".config/git/ignore":    "sealtest-host-ignore-pattern\n",
		".npmrc":                "//registry.example/:_authToken=npm-secret-token\n",
		".claude/CLAUDE.md":     "the user's own house rules\n",
		".claude/settings.json": `{"env": {"SEALTEST_IN_SETTINGS": "settings-secret"}}`,
	} {
		if err := os.WriteFile(filepath.Join(home, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{
  "packs": ["claude"],
  "env_sources": [{"SEALTEST_SECRET": "sealtest-secret-value"}],
  "host_files": ["~/.npmrc"],
  "mounts": ["~/notes"],
  "cache_relocations": {"huggingface": "~/bigdisk/hf"},
  "network": {"forward_host_ports": [5432], "ports": ["8080:8080"]},
  "devices": ["` + sealTestDevice + `"],
  "kvm": true
}
`
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	// Resolved like packHome's home and dispatchOptions' repo root: every -v source is compared
	// against it, and on darwin t.TempDir() is under a /var symlink the launch may resolve.
	ws = floortest.ResolvedTemp(t)
	cname := yoloruntime.FromWorkspace(ws)
	bin, rec := t.TempDir(), t.TempDir()
	argvFile := filepath.Join(rec, "argv")
	// The fake runtime also notes whether the jail's host-services dir exists as its container
	// starts: the keeper starts every host service before the container, and its teardown removes
	// the dir, so that moment is the one a whole launch shows a started service at.
	script := "#!/bin/sh\nif [ \"$1\" = run ]; then printf '%s\\n' \"$@\" > '" + argvFile + "'; " +
		"if [ -e '" + hostServiceSocketsDir(cname, false) + "' ]; then : > '" + servicesAtRun(ws) + "'; fi; fi\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	o := dispatchOptions(t, ws, "podman", new(bytes.Buffer), new(bytes.Buffer), nil)
	// Locked, since a launch past its bound is still writing them when the failure reads them.
	var stdout, stderr lockedBuffer
	o.Stdout, o.Stderr = &stdout, &stderr
	repo, _ := o.RepoRoot()
	// The host has a nix daemon, a device node the config passes through and /dev/kvm, so each of
	// those crossings is one an unsealed launch makes (TestTheSealFixtureCrossesUnsealed).
	o.PathExists = func(p string) bool {
		switch p {
		case filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint"), hostNixSocket, hostNixStore,
			sealTestDevice, "/dev/kvm":
			return true
		}
		return false
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.CapturesDir = func() string { return "" }
	o.NeverAttach, o.AcceptConfigChanges = true, true
	o.Sealed = sealed
	o.Args = []string{"yolo", "internal", "capture-run", "--out=/workspace/out", "--", "true"}
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	// BOUNDED: the launch runs its keeper (in-process here, inProcessKeeper) and relays it until the
	// keeper ends, so a keeper that never ends, or a pipe something else still holds, would otherwise
	// hold this test for the package's whole timeout, as it once did.
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		Run(*o)
	}()
	select {
	case <-returned:
	case <-time.After(sealedLaunchBound):
		t.Fatalf("the launch (sealed=%v) had not returned after %s\nstdout:\n%s\nstderr:\n%s",
			sealed, sealedLaunchBound, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the sealed launch never ran its runtime (%v)\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n"), ws, home, stdout.String() + stderr.String()
}

// sealTestDevice is the device node the seal fixture's config passes through.
const sealTestDevice = "/dev/sealtest0"

// servicesAtRun is the file the fixture's fake runtime leaves in ws when the jail's host-services
// dir existed as its container started.
func servicesAtRun(ws string) string { return filepath.Join(ws, ".sealtest-services-at-run") }

// sealedLaunchBound bounds one fixture launch, which takes well under a second when it works.
const sealedLaunchBound = 90 * time.Second

// keeperLineHolding is how the fixture launch's keeper line begins, up to what the keeper will hold
// (keeperLine): the container alone, or the host services and the container.
const keeperLineHolding = "keeper: yolo internal daemon " + KeeperVerb + " will hold "

// sealedDeviceAllowlist is every --device a sealed launch may hand its jail: the nesting devices
// every podman jail gets (podmanNestingArgs), never one the config passes through.
var sealedDeviceAllowlist = map[string]bool{"/dev/fuse": true, "/dev/net/tun": true}

// pairs collects the values of every occurrence of flag in argv.
func argvValues(argv []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// bindSources is every -v source in argv, a named volume included as its name.
func sealedBindSources(argv []string) map[string]string {
	out := map[string]string{}
	for _, v := range argvValues(argv, "-v") {
		src, rest, _ := strings.Cut(v, ":")
		dest, _, _ := strings.Cut(rest, ":")
		out[dest] = src
	}
	return out
}

func sealWithin(dir, p string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func TestASealedLaunchHandsTheJailNoCrossing(t *testing.T) {
	argv, ws, _, printed := sealedLaunch(t, true)
	cname := yoloruntime.FromWorkspace(ws)

	// -e: THE ALLOWLIST.
	for _, e := range argvValues(argv, "-e") {
		name, _, _ := strings.Cut(e, "=")
		if !sealedEnvAllowlist[name] {
			t.Errorf("a sealed launch hands the jail -e %s, which is not on the seal's allowlist", name)
		}
	}
	// -v: every source is this launch's own — its workspace (the build's workspace, state and
	// private stores), its staged trees under the agents dir, yolo's own binaries — or a named
	// scratch volume. Never a host directory.
	repo := strings.TrimSuffix(sealedBindSources(argv)["/opt/yolo-jail/share/yolo-jail"], "/")
	for dest, src := range sealedBindSources(argv) {
		switch {
		case !strings.HasPrefix(src, "/"): // a named per-launch scratch volume
		case sealWithin(ws, src), sealWithin(filepath.Join(paths.AgentsDir(), cname), src), repo != "" && sealWithin(repo, src):
		case src == hostNixStore && dest == hostNixStore:
			// FP-D11 keeps the host's store, read-only, for store-delivered packages: toolchain.
		default:
			t.Errorf("a sealed launch binds the host's %s at %s", src, dest)
		}
	}
	// The nix store stays, and only read-only: never the daemon socket that builds into it.
	for _, v := range argvValues(argv, "-v") {
		if strings.HasPrefix(v, hostNixStore+":") && !strings.HasSuffix(v, ":ro") {
			t.Errorf("a sealed launch binds the host's nix store writable: %s", v)
		}
	}
	// The git identity is a name and an address (FP-D11); the host's global gitignore is a file of
	// the user's home, so it neither crosses nor is named by the composed config.
	if _, ok := sealedBindSources(argv)["/home/agent/.config/git/ignore"]; ok {
		t.Error("a sealed launch binds the host's global gitignore")
	}
	if body, _ := os.ReadFile(filepath.Join(paths.WorkspaceHomeState(ws), "yolo-gitconfig")); strings.Contains(string(body), "excludesFile") {
		t.Errorf("a sealed launch's git config names the host's gitignore:\n%s", body)
	}
	// No host layer crossed, and the jail is told so rather than that the user has none.
	if v, _ := envValue(argv, packload.HostLayerEnvVar); v != packload.HostLayersUnsupportedWire() {
		t.Errorf("a sealed launch reports %s=%q, want the no-host-layer report", packload.HostLayerEnvVar, v)
	}
	// --device: nothing but the nesting devices every jail gets.
	for _, dev := range argvValues(argv, "--device") {
		if !sealedDeviceAllowlist[dev] {
			t.Errorf("a sealed launch passes the host's device %s through", dev)
		}
	}
	// ~/.cache and /mise are the build's own.
	for _, dest := range []string{"/home/agent/.cache", "/mise"} {
		if src := sealedBindSources(argv)[dest]; !sealWithin(ws, src) {
			t.Errorf("%s is bound from %q, not from the build's own workspace", dest, src)
		}
	}
	if slices.Contains(argv, "-p") {
		t.Errorf("a sealed launch publishes a port: %q", argv)
	}
	// The channel's one crossing is the env file, and it carries no env_sources.
	if body, err := os.ReadFile(filepath.Join(paths.WorkspaceHomeState(ws), "yolo-user-env.sh")); err != nil ||
		strings.Contains(string(body), "sealtest-secret-value") {
		t.Errorf("the sealed channel file (err %v) carries the user's env_sources", err)
	}
	// No briefing prepends the user's own host file.
	if stagedBriefingsHold(t, cname, "the user's own house rules") {
		t.Error("a sealed launch's briefing prepends the user's host CLAUDE.md")
	}
	// No host service was started, or disclosed as about to be.
	if strings.Contains(printed, "runs pack code on your machine") {
		t.Errorf("a sealed launch reached the host-service start:\n%s", printed)
	}
	// THE KEEPER STILL RUNS IT (FP-D15), and holds the container alone: its plan names no service,
	// and it refused none, since the launch ran its runtime above.
	if !strings.Contains(printed, keeperLineHolding+"this jail's container until") {
		t.Errorf("a sealed launch's keeper line does not say it holds the container alone:\n%s", printed)
	}
	if _, err := os.Stat(hostServiceSocketsDir(cname, false)); !os.IsNotExist(err) {
		t.Errorf("a sealed launch made the host-services dir (err %v)", err)
	}
	if _, err := os.Stat(servicesAtRun(ws)); err == nil {
		t.Error("a sealed launch's keeper started host services before its container (their dir existed at the run)")
	}
}

// TestTheSealFixtureCrossesUnsealed is the fixture's own proof: the same config, UNSEALED, does
// hand the jail the crossings the test above says the seal withholds — so a gate removed from any
// of those sites is a failure there rather than a fixture that never exercised it.
func TestTheSealFixtureCrossesUnsealed(t *testing.T) {
	argv, ws, home, printed := sealedLaunch(t, false)
	// The keeper's host services: unsealed, the plan names the claude pack's broker, so a sealed
	// plan's naming none is the seal's doing.
	if !strings.Contains(printed, keeperLineHolding+"this jail's host services (") ||
		!strings.Contains(printed, "the claude-oauth-broker service") {
		t.Errorf("the unsealed fixture's keeper holds no host service, so that site is unexercised:\n%s", printed)
	}
	if _, err := os.Stat(servicesAtRun(ws)); err != nil {
		t.Errorf("the unsealed fixture's keeper started no host service before its container (%v), so that site is unexercised", err)
	}
	srcs := sealedBindSources(argv)
	var sawHome, sawCache bool
	for _, src := range srcs {
		sawHome = sawHome || sealWithin(filepath.Join(home, "notes"), src)
		sawCache = sawCache || src == paths.GlobalCache()
	}
	if !sawHome {
		t.Errorf("the unsealed fixture does not bind ~/notes, so the `mounts` site is unexercised: %v", srcs)
	}
	if !sawCache {
		t.Errorf("the unsealed fixture does not bind the machine cache: %v", srcs)
	}
	if _, ok := srcs["/home/agent/.config/git/ignore"]; !ok {
		t.Errorf("the unsealed fixture does not bind the host's gitignore, so that site is unexercised: %v", srcs)
	}
	if _, ok := srcs[hostNixSocket]; !ok {
		t.Errorf("the unsealed fixture does not bind the nix daemon socket, so that site is unexercised: %v", srcs)
	}
	for _, dev := range []string{sealTestDevice, "/dev/kvm"} {
		if !slices.Contains(argvValues(argv, "--device"), dev) {
			t.Errorf("the unsealed fixture does not pass %s through, so that site is unexercised", dev)
		}
	}
	if !slices.Contains(argv, "-p") {
		t.Error("the unsealed fixture publishes no port, so the publish site is unexercised")
	}
	body, _ := os.ReadFile(filepath.Join(paths.WorkspaceHomeState(ws), "yolo-user-env.sh"))
	if !strings.Contains(string(body), "sealtest-secret-value") {
		t.Error("the unsealed fixture's env_sources never reach the channel file")
	}
	if !stagedBriefingsHold(t, yoloruntime.FromWorkspace(ws), "the user's own house rules") {
		t.Error("the unsealed fixture's briefing never prepends the host CLAUDE.md")
	}
}

// stagedBriefingsHold reports whether any briefing the launch staged for cname contains text.
func stagedBriefingsHold(t *testing.T, cname, text string) bool {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(paths.AgentsDir(), cname, "briefing-*"))
	for _, m := range matches {
		if body, err := os.ReadFile(m); err == nil && strings.Contains(string(body), text) {
			return true
		}
	}
	return false
}

// TestASealedBuildReachesNoHostServiceThroughTheNetwork: the seal withholds a port forward because
// a forward reaches a host service (FP-D11), and the host's network reaches every one of them. So a
// sealed build is handed neither the host's loopback — the forwarding option a rootless pasta host
// gets on the default bridge — nor the host's namespace a user config's `network.mode: "host"` asks
// for: either puts every service the host binds to 127.0.0.1 in reach of the build. The build keeps
// the network itself, on the runtime's own bridge, for its dependencies.
func TestASealedBuildReachesNoHostServiceThroughTheNetwork(t *testing.T) {
	for name, cfgNet := range map[string]any{"the default bridge": nil, `network.mode "host"`: newConfig("mode", "host")} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			for _, sealed := range []bool{false, true} {
				o, _ := pastaHostOptions(t, "/ws", home, false)
				o.Sealed = sealed
				in := relocationInput(t, "podman", t.TempDir(), nil)
				in.sealed = sealed
				if cfgNet != nil {
					in.cfg.Set("network", cfgNet)
				}
				argv := o.assembleRunCmd(in)
				selectors := networkSelectors(argv)
				loopback, _ := envValue(argv, paths.HostLoopbackEnvVar)
				if !sealed {
					// The fixture's own proof: unsealed, this host does reach the host's network.
					if len(selectors) == 0 {
						t.Fatalf("the unsealed fixture emits no network selector, so the site is unexercised")
					}
					continue
				}
				if len(selectors) != 0 {
					t.Errorf("a sealed build is handed the host's network: %v", selectors)
				}
				if loopback != paths.HostLoopbackUnknown {
					t.Errorf("a sealed build is told %s=%q, want %q: yolo asked for no forwarding",
						paths.HostLoopbackEnvVar, loopback, paths.HostLoopbackUnknown)
				}
			}
		})
	}
}
