package macosuser

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// --- Go-native unit tests (mirror tests/test_macos_user.py assertions) ------

func TestSeatbeltProfile(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "")
	for _, want := range []string{
		"(allow default)",
		`(deny file-write* (subpath "/"))`,
		`(subpath "/Users/Shared/proj")`,
		`(subpath "/Users/_yolojail")`,
		`(subpath "/tmp")`,
		`(subpath "/var/folders")`,
		`(deny file-read* (subpath "/Library/Keychains"))`,
		`(deny file-read* (subpath "/Users"))`,
		`(literal "/Users")`,
		`#"^/dev/r?disk"`,
		`#"^/dev/bpf"`,
		`(deny file-read* (subpath "/Volumes"))`,
		`(allow file-read* (subpath "/Volumes/Macintosh HD"))`,
		"(allow process-info*)",
		"(allow sysctl-read)",
	} {
		if !contains(p, want) {
			t.Errorf("seatbelt missing %q", want)
		}
	}
	// deny precedes re-allow (last-match-wins ordering).
	if idx(p, `(deny file-write* (subpath "/"))`) >= idx(p, "(allow file-write*") {
		t.Error("write deny must precede re-allow")
	}
	if idx(p, `(deny file-read* (subpath "/Users"))`) >= idx(p, `(literal "/Users")`) {
		t.Error("/Users read deny must precede re-allow")
	}
	// No ancestor-metadata block.
	if contains(p, "file-read-metadata") {
		t.Error("no per-ancestor metadata block expected")
	}
}

func TestSeatbeltEscapesPath(t *testing.T) {
	p := SeatbeltProfile(`/Users/Shared/a"b\c`, "")
	if !contains(p, `\"`) || !contains(p, `\\`) {
		t.Errorf("SBPL escaping absent: %q", p)
	}
}

func TestLaunchArgv(t *testing.T) {
	env := jsonx.NewOrderedMap()
	env.Set("HOME", "/evil")
	env.Set("USER", "root")
	env.Set("SHELL", "/x")
	env.Set("PATH", "/evil/bin")
	env.Set("OK", "1")
	argv := LaunchArgv([]string{"claude", "--x"}, "/var/yolo-jail/p.sb", env,
		"/Users/Shared/proj", "", "", []string{"/nix/store/a-jq/bin"})
	if argv[0] != "sudo" || !inSlice(argv, "--user=_yolojail") {
		t.Error("must run as sandbox via sudo")
	}
	i := idxSlice(argv, "/usr/bin/env")
	if i < 0 || argv[i+1] != "-i" {
		t.Error("env -i must follow /usr/bin/env")
	}
	if !inSlice(argv, "HOME=/Users/_yolojail") || inSlice(argv, "HOME=/evil") {
		t.Error("HOME must be protected")
	}
	if inSlice(argv, "USER=root") || inSlice(argv, "PATH=/evil/bin") {
		t.Error("USER/PATH must be protected")
	}
	if !inSlice(argv, "OK=1") {
		t.Error("non-protected env passes through")
	}
	// PATH order: shims < darwin prefix < /usr/bin.
	var pathVal string
	for _, a := range argv {
		if len(a) > 5 && a[:5] == "PATH=" {
			pathVal = a[5:]
		}
	}
	dirs := splitColon(pathVal)
	if idxStr(dirs, "/Users/_yolojail/.yolo-shims") >= idxStr(dirs, "/nix/store/a-jq/bin") {
		t.Error("shims must precede darwin prefix")
	}
	if idxStr(dirs, "/nix/store/a-jq/bin") >= idxStr(dirs, "/usr/bin") {
		t.Error("darwin prefix must precede /usr/bin")
	}
	// Inner shell is workspace-centric.
	inner := argv[len(argv)-1]
	if argv[len(argv)-3] != "/bin/zsh" || argv[len(argv)-2] != "-c" {
		t.Error("last argv triple must be /bin/zsh -c <inner>")
	}
	if !hasPrefix(inner, "cd '/Users/Shared/proj' && exec ") || !contains(inner, "'claude' '--x'") {
		t.Errorf("inner = %q", inner)
	}
}

func TestResolvePython(t *testing.T) {
	if got, _ := ResolvePython(func(string) bool { return true }); got != "/opt/homebrew/bin/python3" {
		t.Errorf("homebrew must win: %q", got)
	}
	if got, _ := ResolvePython(func(p string) bool { return p == "/usr/bin/python3" }); got != "/usr/bin/python3" {
		t.Errorf("fallback: %q", got)
	}
	if _, ok := ResolvePython(func(string) bool { return false }); ok {
		t.Error("none exist => not ok")
	}
	c := PythonCandidates()
	if c[len(c)-1] != "/usr/bin/python3" {
		t.Error("stub must be last")
	}
}

func TestNextFreeID(t *testing.T) {
	if got := NextFreeID(map[int]struct{}{600: {}, 601: {}, 603: {}}, 600); got != 602 {
		t.Errorf("= %d", got)
	}
	if got := NextFreeID(map[int]struct{}{}, 600); got != 600 {
		t.Errorf("= %d", got)
	}
}

func TestHomeContaining(t *testing.T) {
	if h, ok := HomeContaining("/Users/matt/code/proj", ""); !ok || h != "/Users/matt" {
		t.Errorf("= %q %v", h, ok)
	}
	if h, ok := HomeContaining("/Users/matt", ""); !ok || h != "/Users/matt" {
		t.Errorf("home itself = %q %v", h, ok)
	}
	if _, ok := HomeContaining("/Users/Shared/yolo/proj", ""); ok {
		t.Error("shared is neutral")
	}
	if _, ok := HomeContaining("/opt/yolo/proj", ""); ok {
		t.Error("non-/Users is neutral")
	}
}

func TestMacosLogModes(t *testing.T) {
	if MacosLogWrapperScript("bogus") != MacosLogWrapperScript("off") {
		t.Error("unknown falls back to off")
	}
	if contains(MacosLogWrapperScript("off"), "/usr/bin/log") {
		t.Error("off must not exec log")
	}
	if !contains(MacosLogWrapperScript("full"), `exec /usr/bin/log "$@"`) {
		t.Error("full passthrough")
	}
	if !contains(MacosLogWrapperScript("user"), "/usr/bin/log show") {
		t.Error("user defaults to show")
	}
}

func TestShQuoteNotShlex(t *testing.T) {
	// _sh_quote always wraps and uses '\'' escaping, unlike shlex.quote.
	if got := shQuote("abc"); got != "'abc'" {
		t.Errorf("shQuote always wraps: %q", got)
	}
	if got := shQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("= %q", got)
	}
	if got := shQuote(""); got != "''" {
		t.Errorf("empty = %q", got)
	}
}

// --- Live-Python differential parity (skips without Python) -----------------

func TestParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys, json
sys.path.insert(0, 'src')
from pathlib import Path
from cli import macos_user as m

out = {}
out["seatbelt"] = m.seatbelt_profile(Path('/Users/Shared/a"b\\c'))
out["seatbelt_plain"] = m.seatbelt_profile(Path('/Users/Shared/yolo/proj'))
out["launch_argv"] = m.launch_argv(
    ["claude","--x"],
    profile_path=Path("/var/yolo-jail/p.sb"),
    sandbox_env={"OK":"1","HOME":"/evil","ZK":"z"},
    workspace=Path("/Users/Shared/proj"),
    path_prefix=["/nix/store/a-jq/bin"],
)
out["create_user"] = m.create_user_commands(601,601,host_user="matt")
out["delete_user"] = m.delete_user_commands(host_user="matt")
out["shared_root"] = m.shared_root_provision_commands(host_user="matt")
out["stage_entrypoint"] = m.stage_entrypoint_commands(Path("/opt/yolo-jail/src"))
out["broker_grant"] = m.broker_socket_grant_commands(Path("/tmp/yolo-broker/broker.sock"))
out["aces"] = m.workspace_acl_aces()
out["fix_perms"] = m.fix_permissions_script(Path("/Users/Shared/yolo"))
out["strip"] = m.workspace_acl_strip_script(Path("/Users/Shared/yolo/proj"))
out["log_off"] = m.macos_log_wrapper_script("off")
out["log_user"] = m.macos_log_wrapper_script("user")
out["log_full"] = m.macos_log_wrapper_script("full")
out["sandbox_path"] = m.sandbox_path()
out["session_profile"] = str(m.session_profile_path("yolo-proj-abcd1234"))
out["bootstrap_argv"] = m._bootstrap_argv("/opt/homebrew/bin/python3", Path("/var/yolo-jail/b.py"))
out["bootstrap"] = m.entrypoint_bootstrap_script(
    Path("/opt/yolo-jail/src"),
    workspace=Path("/Users/Shared/proj"),
    sandbox_home=m.SANDBOX_HOME,
    agents=["claude","codex"],
    macos_log="user",
    git_identity={"YOLO_GIT_NAME":"Ada O'Brien","YOLO_GIT_EMAIL":"ada@x.dev"},
    bootstrap_env={"YOLO_BLOCK_CONFIG":'[{"name": "grep"}]'},
    path_prefix=["/nix/store/a-jq/bin"],
)
out["bootstrap_nopkg"] = m.entrypoint_bootstrap_script(
    Path("/opt/yolo-jail/src"),
    workspace=Path("/Users/matt/code/proj"),
    sandbox_home=m.SANDBOX_HOME,
    agents=["claude"],
)
print(json.dumps(out))
`
	outBytes, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python macos_user import failed: %v", err)
	}
	var want map[string]json.RawMessage
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}

	eqStr := func(name, got string) {
		var w string
		if err := json.Unmarshal(want[name], &w); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
		if got != w {
			t.Errorf("%s mismatch:\n go: %q\n py: %q", name, got, w)
		}
	}
	eqArgv := func(name string, got []string) {
		var w []string
		if err := json.Unmarshal(want[name], &w); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s mismatch:\n go: %v\n py: %v", name, got, w)
		}
	}
	eqCmds := func(name string, got [][]string) {
		var w [][]string
		if err := json.Unmarshal(want[name], &w); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s mismatch:\n go: %v\n py: %v", name, got, w)
		}
	}

	eqStr("seatbelt", SeatbeltProfile(`/Users/Shared/a"b\c`, ""))
	eqStr("seatbelt_plain", SeatbeltProfile("/Users/Shared/yolo/proj", ""))

	launchEnv := jsonx.NewOrderedMap()
	launchEnv.Set("OK", "1")
	launchEnv.Set("HOME", "/evil")
	launchEnv.Set("ZK", "z")
	eqArgv("launch_argv", LaunchArgv([]string{"claude", "--x"}, "/var/yolo-jail/p.sb", launchEnv,
		"/Users/Shared/proj", "", "", []string{"/nix/store/a-jq/bin"}))

	eqCmds("create_user", CreateUserCommands(601, 601, "matt"))
	eqCmds("delete_user", DeleteUserCommands("matt"))
	eqCmds("shared_root", SharedRootProvisionCommands("", "matt"))
	eqCmds("stage_entrypoint", StageEntrypointCommands("/opt/yolo-jail/src", ""))
	eqCmds("broker_grant", BrokerSocketGrantCommands("/tmp/yolo-broker/broker.sock", ""))

	// aces map
	var wantAces map[string]string
	if err := json.Unmarshal(want["aces"], &wantAces); err != nil {
		t.Fatalf("aces decode: %v", err)
	}
	if !reflect.DeepEqual(WorkspaceACLAces(""), wantAces) {
		t.Errorf("aces mismatch:\n go: %v\n py: %v", WorkspaceACLAces(""), wantAces)
	}

	eqStr("fix_perms", FixPermissionsScript("/Users/Shared/yolo", ""))
	eqStr("strip", WorkspaceACLStripScript("/Users/Shared/yolo/proj"))
	eqStr("log_off", MacosLogWrapperScript("off"))
	eqStr("log_user", MacosLogWrapperScript("user"))
	eqStr("log_full", MacosLogWrapperScript("full"))
	eqStr("sandbox_path", SandboxPath("", nil))
	eqStr("session_profile", SessionProfilePath("yolo-proj-abcd1234", ""))
	eqArgv("bootstrap_argv", BootstrapArgv("/opt/homebrew/bin/python3", "/var/yolo-jail/b.py", ""))

	gitID := jsonx.NewOrderedMap()
	gitID.Set("YOLO_GIT_NAME", "Ada O'Brien")
	gitID.Set("YOLO_GIT_EMAIL", "ada@x.dev")
	bootEnv := jsonx.NewOrderedMap()
	bootEnv.Set("YOLO_BLOCK_CONFIG", `[{"name": "grep"}]`)
	eqStr("bootstrap", EntrypointBootstrapScript("/opt/yolo-jail/src", "/Users/Shared/proj", SandboxHome(),
		[]string{"claude", "codex"}, "user", gitID, bootEnv, []string{"/nix/store/a-jq/bin"}, ""))
	eqStr("bootstrap_nopkg", EntrypointBootstrapScript("/opt/yolo-jail/src", "/Users/matt/code/proj", SandboxHome(),
		[]string{"claude"}, "off", nil, nil, nil, ""))
}

// --- test helpers -----------------------------------------------------------

func contains(s, sub string) bool { return idx(s, sub) >= 0 }
func idx(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func inSlice(sl []string, x string) bool {
	for _, v := range sl {
		if v == x {
			return true
		}
	}
	return false
}
func idxSlice(sl []string, x string) int {
	for i, v := range sl {
		if v == x {
			return i
		}
	}
	return -1
}
func idxStr(sl []string, x string) int { return idxSlice(sl, x) }
func splitColon(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ':' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
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

func repoRoot(t *testing.T) string {
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
