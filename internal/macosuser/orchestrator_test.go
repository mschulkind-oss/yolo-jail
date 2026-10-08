package macosuser

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// mockDeps returns Deps wired to no-op probes with recording hooks. Callers
// override fields per test. The default is a clean macOS host with a resolved
// interpreter (so the plan is viable), recording every Run/RunBash/RunWithProxy.
func mockDeps(rec *[]string) Deps {
	// The process disk policy the mock "holds": SetDiskIOPolicy records the call and sets it,
	// and DiskIOPolicy reads it back, as the real pair does on a Mac.
	policy := 0
	return Deps{
		SetDiskIOPolicy: func(p int) error {
			if rec != nil {
				*rec = append(*rec, "iopol:"+strconv.Itoa(p))
			}
			policy = p
			return nil
		},
		DiskIOPolicy:      func() (int, error) { return policy, nil },
		IsMacOS:           func() bool { return true },
		Geteuid:           func() int { return 501 },
		Which:             func(string) bool { return true },
		SandboxUserExists: func() bool { return true },
		SelfExe:           func() string { return "/opt/yolo-jail/dist-go/darwin-arm64/yolo" },
		GitConfig:         func(string) (string, bool) { return "", false },
		Getenv:            func(string) string { return "" },
		HostUser:          func() string { return "matt" },
		Run: func(argv []string) int {
			if rec != nil {
				*rec = append(*rec, "run:"+strings.Join(argv, " "))
			}
			return 0
		},
		RunBash: func(s string) int {
			if rec != nil {
				*rec = append(*rec, "bash:"+s)
			}
			return 0
		},
		RunWithProxy: func(argv []string) int {
			if rec != nil {
				*rec = append(*rec, "proxy:"+strings.Join(argv, " "))
			}
			return 42
		},
		InstallRootFile: func(path, content, mode string) bool {
			if rec != nil {
				*rec = append(*rec, "install:"+path)
			}
			return true
		},
		// A REALISTIC materialize result, not nil. Since the floor landed, every
		// macos-user launch builds a closure and every closure contributes a bin
		// dir; a mock returning nil describes a launch that cannot happen, and the
		// orchestrator now refuses it.
		MaterializeDarwin: func(string, []any) (*Darwin, bool, error) {
			return mockDarwin(), true, nil
		},
		TakenIDs:          func() map[int]struct{} { return map[int]struct{}{} },
		SetRandomPassword: func() bool { return true },
		PathIsDir:         func(string) bool { return true },
		PathExists:        func(string) bool { return true },
		// Discarded unless a test reads it: every launch prints, and a production Deps always
		// has a writer.
		Out: io.Discard,
	}
}

// mockDarwin is the materialize result a real launch produces: the floor profile's
// single bin dir. The exact store path is fake; what matters is that it is
// non-empty, because that is what the plan invariants and the generators read.
func mockDarwin() *Darwin {
	return &Darwin{
		PathPrefix: []string{"/nix/store/000mock-yolo-noncontainer-profile/bin"},
		System:     "aarch64-darwin",
	}
}

func newOpts(ws string) Options {
	return Options{
		Workspace: ws,
		Config:    jsonx.NewOrderedMap(),
		Agents:    []string{"claude"},
		AgentArgv: []string{"claude"},
		RepoRoot:  "/opt/yolo-jail",
	}
}

// fixtureStateDeps executes the pure staging argv against a private temp root, replacing the
// production state dir inside argv. Bootstrap is a fake boundary: it returns successfully but
// does not start a guest. Darwin ACL commands are skipped on Linux; all mkdir/copy/chmod/remove
// commands retain their real filesystem effects.
func fixtureStateDeps(t *testing.T, state string, rec *[]string, fail func([]string) bool) Deps {
	t.Helper()
	d := mockDeps(rec)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d.SelfExe = func() string { return exe }
	d.Run = func(argv []string) int {
		original := append([]string(nil), argv...)
		joined := strings.Join(original, " ")
		if rec != nil {
			*rec = append(*rec, "run:"+joined)
		}
		if fail != nil && fail(original) {
			return 1
		}
		if strings.Contains(joined, "internal darwin-bootstrap") ||
			strings.Contains(joined, "internal darwin-provision") {
			return 0
		}
		if len(argv) > 0 && argv[0] == "sudo" {
			argv = argv[1:]
			if len(argv) > 0 && argv[0] == "-n" {
				argv = argv[1:]
			}
		}
		if len(argv) == 0 {
			return 0
		}
		if argv[0] == chmodBin && strings.Contains(strings.Join(argv[1:], " "), "+a") {
			return 0
		}
		mapped := make([]string, len(argv))
		for i, arg := range argv {
			mapped[i] = strings.ReplaceAll(arg, stateDir, state)
		}
		cmd := exec.Command(mapped[0], mapped[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("fixture command %v failed: %v\n%s", mapped, err, output)
			return 1
		}
		return 0
	}
	return d
}

func writeFixturePackTree(t *testing.T, body string, suffix ...string) string {
	t.Helper()
	label := strings.TrimSpace(strings.TrimPrefix(body, "#!/bin/sh\necho "))
	if len(suffix) != 0 {
		label += "-" + suffix[0]
	}
	root := filepath.Join(t.TempDir(), "pack-tree-"+label)
	module := filepath.Join(root, "local", "loopholes", "hello", "bin", "hello")
	if err := os.MkdirAll(filepath.Dir(module), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRunMacosUserPackTreeOwnershipBoundaries checks the actual call site: an exclusive
// reservation establishes ownership, but only before any pack writer is dispatched can that
// owned tree be removed. Writer or consumer dispatch requires conservative exact-path retention.
func TestRunMacosUserPackTreeOwnershipBoundaries(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "project")
	cname := cnameFor(workspace)
	state := t.TempDir()
	hostA := writeFixturePackTree(t, "#!/bin/sh\necho A\n")
	sibling := StagedPackTreeRoot(cname, hostA, state)
	legacy := StagedPackRoot(cname, state)
	for _, dir := range []string{sibling, legacy} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte(dir), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("writer failure retains only the owned tree", func(t *testing.T) {
		hostB := writeFixturePackTree(t, "#!/bin/sh\necho B\n")
		owned := StagedPackTreeRoot(cname, hostB, state)
		var rec []string
		d := fixtureStateDeps(t, state, &rec, func(argv []string) bool {
			if len(argv) < 5 || argv[0] != "sudo" || argv[1] != cpBin || argv[2] != "-R" ||
				!strings.Contains(argv[3], hostB) {
				return false
			}
			privateRoot := strings.ReplaceAll(argv[4], stateDir, state)
			if err := os.WriteFile(filepath.Join(privateRoot, "partial-copy"), []byte("writer may still be active"), 0o644); err != nil {
				t.Errorf("write partial fixture payload: %v", err)
			}
			return true
		})
		var out bytes.Buffer
		d.Out = &out
		opts := newOpts(workspace)
		opts.HostPackRoot = hostB
		if rc := RunMacosUser(d, opts); rc == 0 {
			t.Fatal("the injected pack copy failure returned success")
		}
		if got, err := os.ReadFile(filepath.Join(owned, "partial-copy")); err != nil || string(got) != "writer may still be active" {
			t.Errorf("an unknown writer outcome did not preserve its sentinel: %q, err %v", got, err)
		}
		for _, dir := range []string{sibling, legacy} {
			if _, err := os.Stat(filepath.Join(dir, "sentinel")); err != nil {
				t.Errorf("cleanup changed sibling %s: %v", dir, err)
			}
		}
		if !strings.Contains(out.String(), "Could not stage entrypoint") ||
			!strings.Contains(out.String(), StagedPackTreeRoot(cname, hostB, "")) ||
			!strings.Contains(out.String(), "Retaining guest pack tree") {
			t.Errorf("unknown writer outcome was not disclosed: %s", out.String())
		}
	})

	t.Run("failed reservation does not claim collision", func(t *testing.T) {
		hostC := writeFixturePackTree(t, "#!/bin/sh\necho C\n")
		collision := StagedPackTreeRoot(cname, hostC, state)
		if err := os.MkdirAll(collision, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(collision, "sentinel")
		if err := os.WriteFile(sentinel, []byte("other owner"), 0o644); err != nil {
			t.Fatal(err)
		}
		var rec []string
		d := fixtureStateDeps(t, state, &rec, func(argv []string) bool {
			return len(argv) == 3 && argv[0] == "sudo" && argv[1] == mkdirBin && argv[2] ==
				StagedPackTreeRoot(cname, hostC, "")
		})
		var out bytes.Buffer
		d.Out = &out
		opts := newOpts(workspace)
		opts.HostPackRoot = hostC
		if rc := RunMacosUser(d, opts); rc == 0 {
			t.Fatal("the failed exclusive reservation returned success")
		}
		if got, err := os.ReadFile(sentinel); err != nil || string(got) != "other owner" {
			t.Errorf("the unowned collision tree was changed: %q, err %v", got, err)
		}
		if !strings.Contains(out.String(), "Could not reserve guest pack tree") ||
			!strings.Contains(out.String(), StagedPackTreeRoot(cname, hostC, "")) ||
			!strings.Contains(out.String(), "fresh invocation") ||
			!strings.Contains(out.String(), "sudo ls -la --") || !strings.Contains(out.String(), "Do not remove") {
			t.Errorf("reservation refusal omitted safe retry/inspection guidance: %s", out.String())
		}
		for _, call := range rec {
			if strings.Contains(call, "sudo "+rmBin+" -rf -- "+StagedPackTreeRoot(cname, hostC, "")) {
				t.Errorf("the existing collision was removed: %s", call)
			}
		}
	})

	for _, agentRC := range []int{0, 42} {
		t.Run("consumer return "+strconv.Itoa(agentRC)+" retains", func(t *testing.T) {
			hostD := writeFixturePackTree(t, "#!/bin/sh\necho D\n", strconv.Itoa(agentRC))
			retainedProduction := StagedPackTreeRoot(cname, hostD, "")
			retainedFixture := StagedPackTreeRoot(cname, hostD, state)
			var rec []string
			d := fixtureStateDeps(t, state, &rec, nil)
			d.RunWithProxy = func(argv []string) int {
				rec = append(rec, "proxy:"+strings.Join(argv, " "))
				return agentRC
			}
			var out bytes.Buffer
			d.Out = &out
			opts := newOpts(workspace)
			opts.HostPackRoot = hostD
			if rc := RunMacosUser(d, opts); rc != agentRC {
				t.Fatalf("RunMacosUser = %d, want agent status %d\n%s", rc, agentRC, out.String())
			}
			if _, err := os.Stat(filepath.Join(retainedFixture, "local", "loopholes", "hello", "bin", "hello")); err != nil {
				t.Errorf("the consumer's pack tree was not retained: %v", err)
			}
			for _, dir := range []string{sibling, legacy} {
				if _, err := os.Stat(filepath.Join(dir, "sentinel")); err != nil {
					t.Errorf("launch changed sibling %s: %v", dir, err)
				}
			}
			if !strings.Contains(out.String(), "Retaining guest pack tree") ||
				!strings.Contains(out.String(), retainedProduction) || !strings.Contains(out.String(), "restart capability") {
				t.Errorf("the retained path was not disclosed: %s", out.String())
			}
			for _, call := range rec {
				if strings.Contains(call, "sudo "+rmBin+" -rf -- "+StagedPackTreeRoot(cname, hostD, "")) {
					t.Errorf("post-consumer tree was removed: %s", call)
				}
			}
		})
	}
}

// macos-user had NEITHER MISE_TRUSTED_CONFIG_PATHS nor a `mise trust` call, so a
// repo-committed mise.toml under the workspace could stop its agent with an untrusted-config
// prompt the container path never sees. We enter this environment through a `yolo` command, so
// the launch env is ours to set.
//
// The negative half is the load-bearing one: the value must be the WORKSPACE, not a blanket
// "trust anything", or the fix trades a prompt for a hole.
func TestSandboxPlanTrustsTheWorkspaceMiseConfigs(t *testing.T) {
	// The launch env crosses in the SESSION ENV FILE, so that is where the assertion
	// belongs — a value that never reaches the file never reaches the agent. It used to be
	// asserted on LaunchArgv, which is exactly where composed values no longer go
	// (envfile.go).
	env := buildPlan(mockDeps(nil), newOpts("/Users/Shared/proj"), nil).EnvFileContent
	if !strings.Contains(env, "MISE_TRUSTED_CONFIG_PATHS='/Users/Shared/proj'") {
		t.Errorf("macos-user must trust the workspace's mise configs; env file = %s", env)
	}
	// A wider value would trust configs outside the tree yolo was pointed at.
	if strings.Contains(env, "MISE_TRUSTED_CONFIG_PATHS='/'") ||
		strings.Contains(env, "MISE_TRUSTED_CONFIG_PATHS='$HOME'") {
		t.Errorf("the trust path must be the WORKSPACE, not a blanket root: %s", env)
	}
}

// The host's NO_COLOR crosses into the sandbox's session env file, beside TERM and
// COLORTERM: a user who asked for no color asked it of the sandboxed programs too
// (https://no-color.org). Unset or empty, it does not cross — empty is the
// convention's "not set", and a program testing presence would misread it.
func TestSandboxPlanCarriesNoColor(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"1", true}, {"", false}} {
		d := mockDeps(nil)
		d.Getenv = func(k string) string {
			if k == "NO_COLOR" {
				return tc.value
			}
			return ""
		}
		env := buildPlan(d, newOpts("/Users/Shared/proj"), nil).EnvFileContent
		if got := strings.Contains(env, "NO_COLOR="); got != tc.want {
			t.Errorf("host NO_COLOR=%q: env file carries NO_COLOR = %v, want %v:\n%s",
				tc.value, got, tc.want, env)
		}
		if tc.want && !strings.Contains(env, "NO_COLOR='1'") {
			t.Errorf("NO_COLOR crossed with the wrong value:\n%s", env)
		}
	}
}

// A user who sets it explicitly wins: it goes in BEFORE env_sources and SandboxEnv precisely so
// it stays a default rather than an override.
func TestSandboxTrustPathIsOverridable(t *testing.T) {
	opts := newOpts("/Users/Shared/proj")
	opts.SandboxEnv = jsonx.NewOrderedMap()
	opts.SandboxEnv.Set("MISE_TRUSTED_CONFIG_PATHS", "/Users/Shared/proj/only-here")
	env := buildPlan(mockDeps(nil), opts, nil).EnvFileContent
	if !strings.Contains(env, "MISE_TRUSTED_CONFIG_PATHS='/Users/Shared/proj/only-here'") {
		t.Errorf("an explicit sandbox_env value must win: %s", env)
	}
}

func TestRunMacosUserFailsClosedOffMacOS(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	rc := RunMacosUser(d, newOpts("/tmp/ws"))
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if len(rec) != 0 {
		t.Errorf("must not shell out: %v", rec)
	}
}

func TestRunMacosUserRefusesRoot(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.Geteuid = func() int { return 0 }
	var buf bytes.Buffer
	d.Out = &buf
	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/ws"))
	if rc != 1 || len(rec) != 0 {
		t.Errorf("rc=%d rec=%v (want refuse before any subprocess)", rc, rec)
	}
}

func TestDryRunPrintsAndExecutesNothing(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false } // dry-run works off-macOS
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.DryRun = true
	rc := RunMacosUser(d, opts)
	if rc != 0 {
		t.Errorf("clean plan rc = %d, want 0", rc)
	}
	if len(rec) != 0 {
		t.Errorf("dry-run must not shell out: %v", rec)
	}
	if !strings.Contains(buf.String(), "macos-user run plan") {
		t.Error("dry-run should print the plan")
	}
}

func TestDryRunNonzeroWhenPlanBroken(t *testing.T) {
	d := mockDeps(nil)
	d.IsMacOS = func() bool { return false }
	// An empty SelfExe yields an unstaged bootstrap binary — the B2 plan
	// invariant fires, so the dry-run exits non-zero.
	d.SelfExe = func() string { return "" }
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.DryRun = true
	if rc := RunMacosUser(d, opts); rc != 1 {
		t.Errorf("broken plan rc = %d, want 1", rc)
	}
}

func TestDryRunRejectsHomeWorkspace(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/matt/code/proj")
	opts.DryRun = true
	rc := RunMacosUser(d, opts)
	if rc != 1 || len(rec) != 0 {
		t.Errorf("home workspace rc=%d rec=%v", rc, rec)
	}
}

func TestRunHappyPathLaunches(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj"))
	if rc != 42 {
		t.Errorf("rc = %d, want 42 (proxy exit)", rc)
	}
	// Must install profile + bootstrap, stage entrypoint (3 cmds), run
	// bootstrap, launch under proxy.
	joined := strings.Join(rec, "\n")
	if !strings.Contains(joined, "install:/var/yolo-jail/profile-") {
		t.Error("profile not installed")
	}
	// No `--login`: it rewrote every forwarded command (see
	// TestNeitherArgvForwardsThroughSudoLogin), and sudo execve's the argv without it.
	if !strings.Contains(joined, "proxy:sudo --set-home --user=_yolojail") {
		t.Errorf("proxy launch missing:\n%s", joined)
	}
}

func TestRunMaterializeFailureAborts(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return nil, false, errFake("boom")
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("packages", []any{"ripgrep"})
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.Config = cfg
	rc := RunMacosUser(d, opts)
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Error("must not launch when materialize failed")
	}
	if !strings.Contains(buf.String(), "Could not materialize packages natively: boom") {
		t.Errorf("abort message missing:\n%s", buf.String())
	}
}

func TestMacosSetupRefusesRootAndOffMacOS(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 1 || len(rec) != 0 {
		t.Errorf("off-macOS rc=%d rec=%v", rc, rec)
	}

	rec = nil
	d = mockDeps(&rec)
	d.Geteuid = func() int { return 0 }
	d.Out = &buf
	if rc := MacosSetup(d); rc != 1 || len(rec) != 0 {
		t.Errorf("root rc=%d rec=%v", rc, rec)
	}
}

func TestMacosSetupCreatesWhenMissing(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	joined := strings.Join(rec, "\n")
	if !strings.Contains(joined, "run:sudo dscl . -create /Users/_yolojail") {
		t.Errorf("user creation not run:\n%s", joined)
	}
	if !strings.Contains(joined, "run:sudo mkdir -p /Users/Shared/yolo") {
		t.Error("shared root not provisioned")
	}
}

// TestMacosSetupReprovisionsAMissingHome pins the repair for a state THE RUNBOOK'S OWN
// REMEDY CREATES. `sudo rm -rf /Users/_yolojail && yolo macos-setup` — prescribed by
// docs/plans/runbooks/macos-user-manual-checks.md item 5 for an account predating the
// home-tier layout — takes the HOME and leaves the ACCOUNT. Every home step lived in the
// account-creation branch, so that state got none of them, and setup still reported
// "✓ macos-user backend ready … preconditions pass" over a machine that cannot launch:
// /Users is root-owned 0755, so the sandbox uid cannot create its own home.
//
// MEASURED ON HARDWARE 2026-09-12 (macOS 26.5, arm64), following item 5 as written: the
// next launch built the whole native closure and then failed twenty config generators at
// once with `mkdir /Users/_yolojail: permission denied`.
func TestMacosSetupReprovisionsAMissingHome(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return true }
	d.PathIsDir = func(p string) bool { return p != SandboxHome() }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	joined := strings.Join(rec, "\n")
	for _, want := range []string{
		"run:sudo createhomedir -c -u _yolojail",
		"run:sudo chown -R _yolojail:_yolojail /Users/_yolojail",
		"run:sudo chmod 750 /Users/_yolojail",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q:\n%s", want, joined)
		}
	}
	// The ACCOUNT is not the thing that went missing, and recreating it would assign a
	// fresh UUID that silently voids every inherited ACL under the shared root.
	if strings.Contains(joined, "dscl . -create /Users/_yolojail") {
		t.Errorf("recreated an account that already exists:\n%s", joined)
	}
	if !strings.Contains(buf.String(), "is missing") {
		t.Errorf("the repair is silent; setup should say what it reprovisioned:\n%s", buf.String())
	}
}

// TestMacosSetupLeavesAnExistingHomeAlone is the other half: `chown -R` over a populated
// account home is O(files) and buys nothing on the common re-run, so the repair above must
// be gated on the home actually being absent rather than run unconditionally.
func TestMacosSetupLeavesAnExistingHomeAlone(t *testing.T) {
	var rec []string
	d := mockDeps(&rec) // SandboxUserExists and PathIsDir both true
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if joined := strings.Join(rec, "\n"); strings.Contains(joined, "createhomedir") ||
		strings.Contains(joined, "chown -R") {
		t.Errorf("reprovisioned a home that was already there:\n%s", joined)
	}
}

// TestMacosSetupAbortsWhenTheHomeCannotBeReprovisioned: a failed repair must not be
// followed by the "✓ ready … preconditions pass" verdict, which is exactly what the
// unrepaired state used to get.
func TestMacosSetupAbortsWhenTheHomeCannotBeReprovisioned(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return true }
	d.PathIsDir = func(p string) bool { return p != SandboxHome() }
	d.Run = func(argv []string) int {
		rec = append(rec, "run:"+strings.Join(argv, " "))
		if strings.Contains(strings.Join(argv, " "), "createhomedir") {
			return 1
		}
		return 0
	}
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 1 {
		t.Errorf("rc = %d, want 1 (a failed home repair aborts setup)", rc)
	}
	if strings.Contains(buf.String(), "backend ready") {
		t.Errorf("reported ready after a failed home repair:\n%s", buf.String())
	}
}

// TestRunRefusesWhenTheSandboxHomeIsMissing is the launch half of the same defect. The
// launch checked that the ACCOUNT exists and never that its HOME does, so a deleted home
// was diagnosed at the far end of the boot — after the full nix build — by a message that
// blamed the WORKSPACE ACL and prescribed `yolo macos-fix-permissions <workspace>`, which
// cannot reach /Users/_yolojail. Same rule item 10's fix applied: the remedy has to reach
// the path it names.
func TestRunRefusesWhenTheSandboxHomeIsMissing(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.PathIsDir = func(p string) bool { return p != SandboxHome() }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 1 {
		t.Errorf("rc = %d, want 1\n%s", rc, buf.String())
	}
	if joined := strings.Join(rec, "\n"); strings.Contains(joined, "proxy:") {
		t.Errorf("launched anyway:\n%s", joined)
	}
	out := buf.String()
	if !strings.Contains(out, SandboxHome()) || !strings.Contains(out, "yolo macos-setup") {
		t.Errorf("refusal must name the missing home and the remedy that reaches it:\n%s", out)
	}
	if strings.Contains(out, "macos-fix-permissions") {
		t.Errorf("refusal prescribes the remedy that cannot reach the home:\n%s", out)
	}
}

// TestMacosSetupAbortsOnPasswordFailure is the finding-6 fix: a failed
// SetRandomPassword must abort setup loudly (was silently dropped, leaving the
// account potentially password-less).
func TestMacosSetupAbortsOnPasswordFailure(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return false }
	d.SetRandomPassword = func() bool { return false } // password apply fails
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 1 {
		t.Errorf("password failure should abort setup (rc=1), got %d", rc)
	}
	if !strings.Contains(buf.String(), "could not set a random password") {
		t.Errorf("expected a loud password-failure message:\n%s", buf.String())
	}
}

func TestMacosTeardownNoUser(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.SandboxUserExists = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosTeardown(d); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if len(rec) != 0 {
		t.Errorf("nothing to delete: %v", rec)
	}
}

func TestMacosUnshareRunsStrip(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosUnshare(d, "/Users/Shared/yolo/proj"); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if !strings.Contains(strings.Join(rec, "\n"), "bash:set -euo pipefail\nws='/Users/Shared/yolo/proj'") {
		t.Errorf("strip script not run: %v", rec)
	}
}

func TestMacosFixPermissionsRejectsHome(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosFixPermissions(d, "/Users/matt/proj"); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if len(rec) != 0 {
		t.Errorf("must reject before shelling out: %v", rec)
	}
}

func TestMacosFixPermissionsDefaultRoot(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosFixPermissions(d, ""); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if !strings.Contains(strings.Join(rec, "\n"), "bash:") {
		t.Error("fix script not run")
	}
}

// TestPrinterColorRendersANSI verifies the printer routes through the shared
// richtext renderer: color=true renders known style tags to ANSI escapes,
// color=false strips them to plain text, and a literal bracketed token (not a
// style tag) is preserved verbatim in both modes.
func TestPrinterColorRendersANSI(t *testing.T) {
	const ansiGreen = "\x1b[32m"
	const ansiReset = "\x1b[0m"

	var col bytes.Buffer
	printer{w: &col, color: true}.print("[green]ok[/green] see [y/N] and [path]")
	got := col.String()
	if !strings.Contains(got, ansiGreen) || !strings.Contains(got, ansiReset) {
		t.Errorf("color=true should emit ANSI escapes, got %q", got)
	}
	if !strings.Contains(got, "[y/N]") || !strings.Contains(got, "[path]") {
		t.Errorf("color=true must preserve literal bracketed tokens verbatim, got %q", got)
	}
	if strings.Contains(got, "[green]") || strings.Contains(got, "[/green]") {
		t.Errorf("color=true must consume known style tags, got %q", got)
	}

	var plain bytes.Buffer
	printer{w: &plain, color: false}.print("[green]ok[/green] see [y/N] and [path]")
	gotPlain := plain.String()
	if strings.Contains(gotPlain, "\x1b[") {
		t.Errorf("color=false must not emit ANSI escapes, got %q", gotPlain)
	}
	if strings.Contains(gotPlain, "[green]") || strings.Contains(gotPlain, "[/green]") {
		t.Errorf("color=false must strip known style tags, got %q", gotPlain)
	}
	if gotPlain != "ok see [y/N] and [path]\n" {
		t.Errorf("color=false plain text mismatch, got %q", gotPlain)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

// THE CHANNEL LAYERING (fix B of the 2026-09-01 review, the macos-user half). The run
// pipeline composes the profile/provider environment above the backend dispatch and hands
// it to BOTH arms; here is where this arm is proved to receive it. Three properties, each
// one a way the delivery could silently half-happen:
//
//  1. the channel reaches the launch env at all (a `-p` launch that composes the variant
//     env on the container and nothing here is the defect);
//  2. the channel is the WHOLE of what this backend delivers from the run pipeline's
//     composition: buildPlan hydrates no env_sources of its own any more, because the
//     credential gate (docs/reference/providers.md, OQ-CN5) narrows them
//     above the dispatch and the channel carries them — so an env_sources entry the
//     channel does not carry must NOT reach the sandbox. Re-adding a hydration here is the
//     second delivery vehicle §2.3 names, bypassing the gate, and it fails this test;
//  3. the order is kept as handed over — the channel's one ordered composition is what makes a
//     user's own dotenv entry beat a pack's default here (the run pipeline's launchEnv).
func TestPackEnvReachesTheLaunchEnvAheadOfEnvSources(t *testing.T) {
	opts := newOpts("/Users/Shared/proj")
	opts.Config = jsonx.NewOrderedMap()
	inline := jsonx.NewOrderedMap()
	inline.Set("ZAI_API_KEY", "from-envsource")
	inline.Set("AWS_ACCESS_KEY_ID", "withheld-by-the-gate")
	opts.Config.Set("env_sources", []any{inline})
	opts.PackEnv = jsonx.NewOrderedMap()
	opts.PackEnv.Set("ZAI_API_KEY", "from-pack")
	opts.PackEnv.Set("ANTHROPIC_BASE_URL", "https://api.z.ai/api/anthropic")
	opts.PackEnv.Set("YOLO_PROVIDERS", `{"zai": {}}`)
	opts.PackEnv.Set("YOLO_USE_PROFILES", `{"claude": "zai"}`)
	// The channel's env_sources half, last, as launchEnv writes it: the gate kept ZAI_API_KEY
	// (claude selected zai) and withheld the AWS pair (nobody selected bedrock).
	opts.PackEnv.Set("ZAI_API_KEY", "from-envsource")

	plan := buildPlan(mockDeps(nil), opts, nil)
	env := plan.EnvFileContent
	for _, want := range []string{
		"ANTHROPIC_BASE_URL='https://api.z.ai/api/anthropic'",
		"YOLO_PROVIDERS='{\"zai\": {}}'",
		"YOLO_USE_PROFILES='{\"claude\": \"zai\"}'",
		"ZAI_API_KEY='from-envsource'",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the composed channel never reached the launch env (%s); env file = %s", want, env)
		}
	}
	if strings.Contains(env, "AWS_ACCESS_KEY_ID") || strings.Contains(env, "withheld-by-the-gate") {
		t.Errorf("buildPlan delivered an env_sources entry the channel did not carry — a second "+
			"hydration bypassing the credential gate; env file = %s", env)
	}
	if strings.Contains(env, "from-pack") {
		t.Errorf("the channel's own later env_sources value must win: %s", env)
	}
	// AND NONE OF IT IS ON A COMMAND LINE. The delivery moved for a reason; a test that
	// only followed it to the file would pass for a launch that put the values in BOTH
	// places, which is the shape that ships the leak with the fix.
	if argv := strings.Join(plan.LaunchArgv, " "); strings.Contains(argv, "from-envsource") ||
		strings.Contains(argv, "from-pack") {
		t.Errorf("a composed credential is on the launch argv: %s", argv)
	}
}

// The relay half, asserted on the plan the way the runner executes it: the bootstrap argv
// is the env -i the sandbox user's yolo self-execs with, and EnvFromOS is how the darwin
// entry reads the tables back. PlanInvariants carries the same check; this is the
// direct pin on the produced argv.
func TestPackEnvWireTablesReachTheBootstrapEnv(t *testing.T) {
	opts := newOpts("/Users/Shared/proj")
	opts.PackEnv = jsonx.NewOrderedMap()
	opts.PackEnv.Set("YOLO_PROVIDERS", `{"zai": {"api_key_env_name": "ZAI_API_KEY"}}`)
	opts.PackEnv.Set("YOLO_USE_PROFILES", `{"claude": "zai"}`)

	plan := buildPlan(mockDeps(nil), opts, nil)
	for _, wire := range []string{`YOLO_PROVIDERS={"zai": {"api_key_env_name": "ZAI_API_KEY"}}`, `YOLO_USE_PROFILES={"claude": "zai"}`} {
		if !containsArg(plan.BootstrapArgv, wire) {
			t.Errorf("%s is in the launch env but never reached the bootstrap argv %v — "+
				"the native bootstrap would render every pack surface as if no profile "+
				"were selected", wire, plan.BootstrapArgv)
		}
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Errorf("a plan carrying the channel must satisfy every invariant: %v", problems)
	}
}

// The negative half: no channel, no wire tables anywhere, which is the pre-channel shape
// every existing plan still renders.
func TestNoPackEnvMeansNoWireTables(t *testing.T) {
	plan := buildPlan(mockDeps(nil), newOpts("/Users/Shared/proj"), nil)
	joined := strings.Join(plan.BootstrapArgv, " ")
	for _, wire := range []string{"YOLO_PROVIDERS=", "YOLO_USE_PROFILES="} {
		if strings.Contains(joined, wire) {
			t.Errorf("a launch with no channel must not invent %s: %s", wire, joined)
		}
	}
}

// A FAILED BOOTSTRAP SENDS ITS READER TO THE BOOT LOG, which the bootstrap now keeps in the
// workspace (entrypoint.BootLogPath): the launch line alone said that it failed, and the
// bootstrap's own output — the refusal and every line before it — was on a terminal the user
// may have closed. And the session env file is installed BEFORE the bootstrap runs, because
// the bootstrap reads it (hydrate_session_env).
//
// HEDGED, because the log is this launch's only if the bootstrap got as far as opening it: a
// refusal before RunDarwinBootstrap (the workspace-scope check), a sudo or exec failure, or a
// linked `.yolo` leaves the PREVIOUS launch's log at that path, which may well end "boot
// complete". So the line says how to tell (the log's first line carries the time it started)
// and where the output is otherwise.
func TestABootstrapFailureNamesTheBootLog(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	run := d.Run
	d.Run = func(argv []string) int {
		if strings.Contains(strings.Join(argv, " "), "internal darwin-bootstrap") {
			rec = append(rec, "run:"+strings.Join(argv, " "))
			return 1
		}
		return run(argv)
	}
	var buf bytes.Buffer
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	if rc := RunMacosUser(d, opts); rc != 1 {
		t.Fatalf("rc = %d after a failed bootstrap, want 1\n%s", rc, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "entrypoint bootstrap failed") ||
		!strings.Contains(out, entrypoint.BootLogPath(resolvePathAbs(opts.Workspace))) {
		t.Errorf("the bootstrap-failed line does not name the boot log %s:\n%s",
			entrypoint.BootLogPath(resolvePathAbs(opts.Workspace)), out)
	}
	for _, want := range []string{"If it got as far as opening its log", "first line", "the lines above"} {
		if !strings.Contains(out, want) {
			t.Errorf("the bootstrap-failed line claims the log is this launch's without saying how "+
				"to tell, or where the output is otherwise (missing %q):\n%s", want, out)
		}
	}

	envInstall, boot := -1, -1
	for i, r := range rec {
		switch {
		case envInstall < 0 && strings.HasPrefix(r, "install:"+stateDir+"/"+sandboxEnvLeaf+"/"):
			envInstall = i
		case boot < 0 && strings.Contains(r, "internal darwin-bootstrap"):
			boot = i
		}
	}
	if envInstall < 0 || boot < 0 || envInstall > boot {
		t.Errorf("the session env file must be installed before the bootstrap that reads it "+
			"(install at %d, bootstrap at %d):\n%s", envInstall, boot, strings.Join(rec, "\n"))
	}
}

// TestRealDepsWiresTheDiskPolicyCalls: the launch's Deps reach internal/ioprio's
// setiopolicy_np pair. Unwired, a declared resources.io would warn "no call wired" on every
// macos-user launch while every mock-driven test stayed green. Off darwin the pair refuses
// with ioprio's own error, which is how this test tells the wiring from a stand-in.
func TestRealDepsWiresTheDiskPolicyCalls(t *testing.T) {
	d := RealDeps(nil, nil, false)
	if d.SetDiskIOPolicy == nil || d.DiskIOPolicy == nil {
		t.Fatal("RealDeps leaves the disk policy seams nil")
	}
	if runtime.GOOS == "darwin" {
		return // the real call; internal/ioprio's darwin test drives it in a child process
	}
	if err := d.SetDiskIOPolicy(3); err == nil || !strings.Contains(err.Error(), "setiopolicy_np") {
		t.Errorf("SetDiskIOPolicy off darwin = %v, want internal/ioprio's refusal", err)
	}
	if _, err := d.DiskIOPolicy(); err == nil || !strings.Contains(err.Error(), "setiopolicy_np") {
		t.Errorf("DiskIOPolicy off darwin = %v, want internal/ioprio's refusal", err)
	}
}

// TestRealDepsWiresTheHostCPUCount: the cap on resources.cpus's parallelism defaults reads the
// launcher's own CPU count, which is the count each of those variables defaults to on the Mac
// the sandbox shares. Unwired, a declared cpus above it would raise them again while every
// mock-driven test stayed green.
func TestRealDepsWiresTheHostCPUCount(t *testing.T) {
	d := RealDeps(nil, nil, false)
	if d.HostCPUs == nil {
		t.Fatal("RealDeps leaves HostCPUs nil, so resources.cpus is never capped")
	}
	if got := d.HostCPUs(); got != runtime.NumCPU() {
		t.Errorf("HostCPUs() = %d, want runtime.NumCPU() = %d", got, runtime.NumCPU())
	}
}
