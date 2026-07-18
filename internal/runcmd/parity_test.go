package runcmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/naming"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// TestAssembleRunCmdLivePythonParity runs the hermetic Python argv oracle
// (tools/parity/run_argv_oracle.py) over a matrix of workspace configs and
// byte-compares its ordered container argv against the Go assembler with matched
// inputs. This is the plan's ordered-argv golden gate against LIVE Python. Skips
// without uv/python3.
func TestAssembleRunCmdLivePythonParity(t *testing.T) {
	root := repoRootForTest(t)
	oracle := filepath.Join(root, "tools", "parity", "run_argv_oracle.py")
	if _, err := os.Stat(oracle); err != nil {
		t.Skip("argv oracle not present")
	}
	pyRun := pythonRunner(t, root)
	if pyRun == nil {
		t.Skip("python unavailable")
	}

	cases := []struct {
		name    string
		config  string
		network string
		env     string // comma-separated KEY=VALUE env overrides (seam gates)
	}{
		{"claude_minimal", `{ "agents": ["claude"], "security": { "blocked_tools": [] } }`, "bridge", ""},
		{"default_config", `{}`, "bridge", ""},
		{"multi_agent", `{ "agents": ["claude", "copilot", "gemini"] }`, "bridge", ""},
		{"ports_and_forward", `{ "agents": ["claude"], "network": { "ports": ["8000:8000", "3000:3000"], "forward_host_ports": [5432, "6000:6001"] } }`, "bridge", ""},
		{"resources", `{ "agents": ["claude"], "resources": { "memory": "8g", "cpus": 4, "pids_limit": 4096 } }`, "bridge", ""},
		{"mounts", `{ "agents": ["claude"], "mounts": ["/etc:/ctx/etc"] }`, "bridge", ""},
		{"per_side_paths", `{ "agents": ["claude"], "per_side_paths": ["node_modules", "target"] }`, "bridge", ""},
		{"network_host", `{ "agents": ["claude"] }`, "host", ""},
		{"lsp_and_mcp", `{ "agents": ["claude"], "lsp_servers": { "python": { "command": "pyright-langserver", "args": ["--stdio"], "fileExtensions": {".py": "python"} }, "go": { "command": "gopls", "args": [], "fileExtensions": {".go": "go"} } }, "mcp_presets": ["sequential-thinking"] }`, "bridge", ""},
		{"kvm_ephemeral_tmpfs", `{ "agents": ["claude"], "kvm": true, "ephemeral_storage": "tmpfs" }`, "bridge", ""},
		// Seam #11: YOLO_IMPL=go must inject the 4 in-jail Go-CLI forward vars,
		// byte-identically on both sides.
		{"go_impl_forward", `{ "agents": ["claude"] }`, "bridge", "YOLO_IMPL=go"},
		// Seam #10: YOLO_ENTRYPOINT_IMPL=go forwards the entrypoint selector.
		{"entrypoint_impl_forward", `{ "agents": ["claude"] }`, "bridge", "YOLO_ENTRYPOINT_IMPL=go"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertArgvParity(t, pyRun, oracle, tc.config, tc.network, tc.env)
		})
	}
}

func assertArgvParity(t *testing.T, pyRun func(...string) *exec.Cmd, oracle, wsConfig, network, envOverride string) {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	wsResolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		wsResolved = ws
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(wsConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := pyRun(oracle, home, ws, network, envOverride).Output()
	if err != nil {
		// The oracle RAN (python/uv present — that precondition is checked in
		// the caller via pythonRunner) but errored: that is exactly the drift
		// this gate exists to catch, so FAIL, never skip (audit 2026-07-18 §B5:
		// live oracles must fail closed, like committed goldens).
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("argv oracle failed to run: %v\n%s", err, stderr)
	}
	var pyArgv []string
	if err := json.Unmarshal(out, &pyArgv); err != nil {
		t.Fatalf("decode oracle output: %v\n%s", err, out)
	}
	if len(pyArgv) == 0 {
		t.Fatal("oracle produced empty argv")
	}

	t.Setenv("HOME", home)
	o := &Options{
		Network:     network,
		IsMacOS:     false,
		Workspace:   wsResolved,
		PathExists:  hermeticPathExists,
		Now:         func() time.Time { return time.Unix(0, 0) },
		Getpid:      func() int { return 1 },
		IsTTYStdout: func() bool { return false },
		IsTTYStdin:  func() bool { return false },
		Stdout:      discardBuf(),
		Stderr:      discardBuf(),
	}
	fillDefaults(o)
	// Mirror the oracle's env overrides on the Go side so env-gated argv blocks
	// (seam #10/#11) are exercised identically.
	envOver := parseEnvOverride(envOverride)
	o.Getenv = func(k string) string { return envOver[k] }
	o.LookPath = func(string) (string, bool) { return "", false }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	o.PathExists = hermeticPathExists
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	emptyLoopholeDirs(t)

	cfg := loadWSConfig(t, wsResolved)
	agentsList := config.SelectedAgents(cfg)
	cname := naming.FromWorkspace(wsResolved)
	agentsPath := filepath.Join(paths.AgentsDir(), cname)
	npm, goPkgs := resolveLSPInstalls(cfg)

	in := &assembleInput{
		cfg:           cfg,
		rt:            "podman",
		cname:         cname,
		repoRoot:      "/repo",
		agentsList:    agentsList,
		agentSpecs:    agents.ResolveAgents(agentsList),
		agentsPath:    agentsPath,
		wsState:       filepath.Join(wsResolved, ".yolo", "home"),
		miseStore:     paths.GlobalMise(),
		hostTZ:        "", // oracle stubs _detect_host_timezone → None
		yoloVersion:   "9.9.9-test",
		mountTargets:  map[string]struct{}{},
		lspNPMInstall: npm,
		lspGoInstall:  goPkgs,
	}
	goArgv := o.assembleRunCmd(in)

	if len(goArgv) != len(pyArgv) {
		t.Fatalf("argv length: go=%d py=%d\n%s", len(goArgv), len(pyArgv), firstDiff(goArgv, pyArgv))
	}
	for i := range pyArgv {
		if goArgv[i] != pyArgv[i] {
			t.Errorf("argv[%d]: go=%q py=%q", i, goArgv[i], pyArgv[i])
		}
	}
	_ = storage.LinuxMultilib
}

// hermeticPathExists mirrors the oracle's fake_exists: the nesting/nix/device
// probes read false; everything else uses the real filesystem.
func hermeticPathExists(p string) bool {
	switch p {
	case "/run/.containerenv", "/.dockerenv",
		"/nix/var/nix/daemon-socket", "/nix/store",
		"/dev/net/tun", "/dev/kvm", "/dev/kfd":
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func loadWSConfig(t *testing.T, ws string) *jsonx.OrderedMap {
	t.Helper()
	cfg, err := config.LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func firstDiff(a, b []string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return "argv[" + itoaSmall(i) + "]: go=" + q(a[i]) + " py=" + q(b[i])
		}
	}
	if len(a) != len(b) {
		if len(a) > len(b) {
			return "go has extra: " + q(a[n])
		}
		return "py has extra: " + q(b[n])
	}
	return "(no diff)"
}

func q(s string) string { return `"` + s + `"` }
func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// pythonRunner returns a runner for the argv oracle (uv run python, else python3).
func pythonRunner(t *testing.T, root string) func(args ...string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "--no-sync", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// parseEnvOverride turns a comma-separated "KEY=VALUE,KEY2=VALUE2" string into a
// map, matching the oracle's 4th-arg parsing so both sides see identical env.
func parseEnvOverride(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	for _, pair := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(pair, "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestArgvOraclePresent is the audit's canary (2026-07-18 §B5): the parity gate
// skips only when python/uv is ABSENT, never when the oracle file is missing. If
// the oracle ever disappears from the repo, this fails loudly instead of the
// whole parity suite silently green-skipping.
func TestArgvOraclePresent(t *testing.T) {
	oracle := filepath.Join(repoRootForTest(t), "tools", "parity", "run_argv_oracle.py")
	if _, err := os.Stat(oracle); err != nil {
		t.Fatalf("argv oracle missing from repo (%s): %v — the parity gate would silently skip", oracle, err)
	}
}
