package macosuser

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const fakeNixStoreBin = "/nix/store/000fake-nix-2.24.9/bin"

// fakeHostNixProbes is a stock multi-user install: `nix` found through the default profile,
// resolving into the store, with the daemon socket present.
func fakeHostNixProbes() (func(string) (string, error), func(string) (string, error), func(string) bool) {
	return func(name string) (string, error) {
			if name != "nix" {
				return "", errors.New("not found")
			}
			return "/nix/var/nix/profiles/default/bin/nix", nil
		}, func(p string) (string, error) {
			return fakeNixStoreBin + "/nix", nil
		}, func(p string) bool {
			return p == hostNixDaemonSocket
		}
}

func TestResolveHostNixDeliversTheStoreDirOfTheClientOnPath(t *testing.T) {
	look, eval, sock := fakeHostNixProbes()
	got := resolveHostNix(hostNixStoreDir, hostNixDaemonSocket, look, eval, sock)
	if got.BinDir != fakeNixStoreBin || got.Absent != "" {
		t.Fatalf("resolveHostNix = %+v, want BinDir %q — the RESOLVED store dir, not the "+
			"profile dir it was found through", got, fakeNixStoreBin)
	}
}

// Each arm that must deliver NO nix, one broken fact at a time. The single-user case is the
// one the task names: a nix on the sandbox PATH there fails on its first store operation.
func TestResolveHostNixDeliversNothingItCannotBackWithADaemon(t *testing.T) {
	look, eval, sock := fakeHostNixProbes()
	cases := []struct {
		name string
		look func(string) (string, error)
		eval func(string) (string, error)
		sock func(string) bool
		want string
	}{
		{"no nix on PATH", func(string) (string, error) { return "", errors.New("nope") }, eval, sock,
			"no `nix` on the launching user's PATH"},
		{"unresolvable", look, func(string) (string, error) { return "", errors.New("dangling") }, sock,
			"could not be resolved"},
		{"outside the store", look, func(string) (string, error) { return "/Users/matt/bin/nix", nil }, sock,
			"outside /nix/store"},
		{"single-user (no daemon socket)", look, eval, func(string) bool { return false },
			"no nix daemon socket at " + hostNixDaemonSocket},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveHostNix(hostNixStoreDir, hostNixDaemonSocket, c.look, c.eval, c.sock)
			if got.BinDir != "" {
				t.Fatalf("delivered %q; a host with this fact must get no nix", got.BinDir)
			}
			if !strings.Contains(got.Absent, c.want) {
				t.Errorf("Absent = %q, want it to say %q", got.Absent, c.want)
			}
		})
	}
}

func pathPairOf(argv []string) string {
	for _, a := range argv {
		if strings.HasPrefix(a, "PATH=") {
			return strings.TrimPrefix(a, "PATH=")
		}
	}
	return ""
}

func indexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

func darwinWithNix() *Darwin {
	d := mockDarwin()
	d.Nix = HostNix{BinDir: fakeNixStoreBin}
	return d
}

// THROUGH THE PRODUCTION PLAN BUILDER: the client dir reaches all three PATH copies — the
// launch, the provisioning stage and the bootstrap's login PATH — after the floor (so a
// `packages:` nix still wins) and ahead of the system dirs; and the env that makes it a
// no-flags daemon client reaches the session env file.
func TestBuildRunPlanPutsTheHostNixClientOnEverySandboxPath(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	mise := jsonx.NewOrderedMap()
	mise.Set("jq", "latest")
	cfg.Set("mise_tools", mise) // so the provisioning stage exists
	plan := BuildRunPlan("/Users/Shared/yolo/ws", cfg, []string{"claude"}, []string{"claude"},
		"/opt/yolo", "", "", HostContext{}, jsonx.NewOrderedMap(), darwinWithNix(), nil)

	if plan.NixClientDir != fakeNixStoreBin {
		t.Errorf("plan.NixClientDir = %q, want %q", plan.NixClientDir, fakeNixStoreBin)
	}
	if len(plan.ProvisionArgv) == 0 {
		t.Fatal("fixture error: the config was meant to need a provisioning stage")
	}
	floor := mockDarwin().PathPrefix[0]
	for label, argv := range map[string][]string{"launch": plan.LaunchArgv, "provisioning stage": plan.ProvisionArgv} {
		dirs := strings.Split(pathPairOf(argv), ":")
		n, f, usr := indexOf(dirs, fakeNixStoreBin), indexOf(dirs, floor), indexOf(dirs, "/usr/bin")
		if n < 0 {
			t.Errorf("the %s PATH does not carry the host nix client %s:\n%v", label, fakeNixStoreBin, dirs)
			continue
		}
		if !(f < n && n < usr) {
			t.Errorf("the %s PATH orders floor=%d nix=%d /usr/bin=%d; want floor < nix < /usr/bin", label, f, n, usr)
		}
	}
	login := ""
	for _, a := range plan.BootstrapArgv {
		if strings.HasPrefix(a, entrypoint.DarwinLoginPathEnv+"=") {
			login = a
		}
	}
	if !strings.Contains(login, fakeNixStoreBin) {
		t.Errorf("the bootstrap's %s does not carry the nix client, so a login shell and the "+
			"generators would not see it: %q", entrypoint.DarwinLoginPathEnv, login)
	}
	for k, v := range map[string]string{
		"NIX_REMOTE": "daemon",
		"NIX_CONFIG": "extra-experimental-features = nix-command flakes",
	} {
		if !SandboxEnvFileSets(plan.EnvFileContent, k, v) {
			t.Errorf("the session env file does not export %s=%q:\n%s", k, v, plan.EnvFileContent)
		}
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Errorf("a plan carrying the nix client is not viable: %v", problems)
	}
}

// A host with no daemon (or no nix) gets NO nix: nothing on PATH that is not the floor, and
// no NIX_* variable suggesting there is a daemon to reach.
func TestBuildRunPlanWithoutAHostNixDeliversNoNix(t *testing.T) {
	d := mockDarwin()
	d.Nix = HostNix{Absent: "no nix daemon socket"}
	plan := BuildRunPlan("/Users/Shared/yolo/ws", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo", "", "", HostContext{}, jsonx.NewOrderedMap(), d, nil)
	if plan.NixClientDir != "" || strings.Contains(pathPairOf(plan.LaunchArgv), "nix-2.") {
		t.Errorf("no client was resolved, yet the plan delivers one: %q / %s",
			plan.NixClientDir, pathPairOf(plan.LaunchArgv))
	}
	for _, k := range SandboxEnvFileKeys(plan.EnvFileContent) {
		if k == "NIX_REMOTE" || k == "NIX_CONFIG" {
			t.Errorf("%s is set with no nix delivered:\n%s", k, plan.EnvFileContent)
		}
	}
}

// A user's own NIX_REMOTE wins whole; a user's own NIX_CONFIG is kept, and gains yolo's
// extra-experimental-features line only when it names no features itself.
func TestHostNixEnvIsADefaultTheUserOverrides(t *testing.T) {
	env := jsonx.NewOrderedMap()
	env.Set("NIX_CONFIG", "access-tokens = github.com=x")
	env.Set("NIX_REMOTE", "unix:///elsewhere")
	plan := BuildRunPlan("/Users/Shared/yolo/ws", jsonx.NewOrderedMap(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo", "", "", HostContext{}, env, darwinWithNix(), nil)
	merged := "access-tokens = github.com=x\nextra-experimental-features = nix-command flakes"
	if !SandboxEnvFileSets(plan.EnvFileContent, "NIX_CONFIG", merged) {
		t.Errorf("the user's NIX_CONFIG must be kept AND gain yolo's features line, want %q:\n%s",
			merged, plan.EnvFileContent)
	}
	if !SandboxEnvFileSets(plan.EnvFileContent, "NIX_REMOTE", "unix:///elsewhere") {
		t.Errorf("the user's NIX_REMOTE was replaced:\n%s", plan.EnvFileContent)
	}
	// The embedded newline must not make the file look like it sets a second key, or lose
	// NIX_CONFIG: the plan invariant and the dry-run key list both read the file this way.
	n := 0
	for _, k := range SandboxEnvFileKeys(plan.EnvFileContent) {
		if k == "NIX_CONFIG" {
			n++
		}
		if strings.HasPrefix(k, "extra-") {
			t.Errorf("the appended line was read as a key %q:\n%s", k, plan.EnvFileContent)
		}
	}
	if n != 1 {
		t.Errorf("SandboxEnvFileKeys lists NIX_CONFIG %d times, want 1:\n%s", n, plan.EnvFileContent)
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Errorf("a plan carrying a merged NIX_CONFIG is not viable: %v", problems)
	}
}

// A user NIX_CONFIG that names the features — in either spelling, including to turn them
// off — has decided, and is left exactly as written.
func TestHostNixEnvLeavesAUserFeatureChoiceAlone(t *testing.T) {
	for _, user := range []string{
		"experimental-features =",
		"access-tokens = x\n  experimental-features = nix-command",
		"extra-experimental-features = ca-derivations",
	} {
		env := jsonx.NewOrderedMap()
		env.Set("NIX_CONFIG", user)
		got, _ := withHostNixEnv(env).Get("NIX_CONFIG")
		if got != user {
			t.Errorf("NIX_CONFIG %q became %q; a user who names the features must win whole", user, got)
		}
	}
}

// THE PRODUCTION PROBE, with the real LookPath, EvalSymlinks and socket check: a PATH entry
// linking through a profile into a temp store resolves to the store bin dir, and only when a
// real unix socket sits at the daemon's path. Fails if hostNixProbe stops resolving symlinks
// (the profile path is outside the store, so every stock install would get no nix) or stops
// requiring a SOCKET (a regular file there is not a daemon).
func TestHostNixProbeResolvesARealSymlinkChainIntoTheStore(t *testing.T) {
	// Resolved where it is minted: on darwin t.TempDir() is under the /var symlink, and the
	// probe compares an EvalSymlinks'd path against this prefix.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "store")
	storeBin := filepath.Join(store, "abc-nix-2.24.9", "bin")
	profileBin := filepath.Join(root, "profile", "bin")
	pathDir := filepath.Join(root, "path")
	for _, d := range []string{storeBin, profileBin, pathDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(storeBin, "nix"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(storeBin, "nix"), filepath.Join(profileBin, "nix")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(profileBin, "nix"), filepath.Join(pathDir, "nix")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)

	// A SHORT socket dir, not t.TempDir(): sun_path is 104 bytes on darwin, 108 on Linux.
	sockDir, err := os.MkdirTemp("", "hn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	if got := hostNixProbe(store, sock); got.BinDir != storeBin || got.Absent != "" {
		t.Errorf("hostNixProbe = %+v, want BinDir %q (the resolved store dir)", got, storeBin)
	}

	regular := filepath.Join(root, "not-a-socket")
	if err := os.WriteFile(regular, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{regular, filepath.Join(root, "missing")} {
		if got := hostNixProbe(store, p); got.BinDir != "" {
			t.Errorf("hostNixProbe with %s at the socket path delivered %q; only a socket is a daemon", p, got.BinDir)
		}
	}
}

func TestIsUnixSocketAcceptsOnlyASocket(t *testing.T) {
	sockDir, err := os.MkdirTemp("", "hn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	regular := filepath.Join(sockDir, "f")
	if err := os.WriteFile(regular, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{sock: true, regular: false, sockDir: false, filepath.Join(sockDir, "x"): false} {
		if got := isUnixSocket(p); got != want {
			t.Errorf("isUnixSocket(%s) = %v, want %v", p, got, want)
		}
	}
}

// THE CALL SITE: RunMacosUser asks Deps.HostNix and the answer reaches the agent's argv and
// env file. Fails if the orchestrator stops asking.
func TestRunMacosUserDeliversTheHostNixClientToTheAgent(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var out bytes.Buffer
	d.Out = &out
	d.HostNix = func() HostNix { return HostNix{BinDir: fakeNixStoreBin} }
	envFiles := map[string]string{}
	d.InstallRootFile = func(path, content, mode string) bool {
		envFiles[path] = content
		return true
	}
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/ws")); rc != 42 {
		t.Fatalf("rc = %d, want the agent's 42\n%s", rc, out.String())
	}
	var launch string
	for _, r := range rec {
		if strings.HasPrefix(r, "proxy:") {
			launch = r
		}
	}
	if !strings.Contains(launch, fakeNixStoreBin) {
		t.Errorf("the agent launch does not carry the host nix client on its PATH:\n%s", launch)
	}
	found := false
	for _, c := range envFiles {
		if SandboxEnvFileSets(c, "NIX_REMOTE", "daemon") {
			found = true
		}
	}
	if !found {
		t.Errorf("no installed file exports NIX_REMOTE=daemon: %v", envFiles)
	}
	if !strings.Contains(out.String(), "nix: the host's client ("+fakeNixStoreBin+")") {
		t.Errorf("the launch did not disclose the nix client:\n%s", out.String())
	}
}

// ...and says why when there is none, rather than leaving `nix: command not found` to explain.
func TestRunMacosUserSaysWhyThereIsNoNix(t *testing.T) {
	d := mockDeps(nil)
	var out bytes.Buffer
	d.Out = &out
	d.HostNix = func() HostNix { return HostNix{Absent: "no nix daemon socket at X"} }
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/ws")); rc != 42 {
		t.Fatalf("a host without a daemon must still launch; rc = %d\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "nix is not available inside the sandbox: no nix daemon socket at X") {
		t.Errorf("the launch did not name why nix is absent:\n%s", out.String())
	}
}

// RealDeps is the only production Deps a launch gets; unwired, the whole feature is off.
func TestRealDepsWiresTheHostNixProbe(t *testing.T) {
	if RealDeps(nil, nil, false).HostNix == nil {
		t.Fatal("RealDeps leaves HostNix nil, so no macos-user launch would ever deliver nix")
	}
}
