package run

// hostkeychainenv_test.go pins OQ-KC5's macos-user route (docs/design/keychain-from-a-jail.md
// §4.1): a macos-user launch that selects copilot reads the copilot pack's declared keychain item
// through Options.HostKeychainRead and writes it as COPILOT_GITHUB_TOKEN into copilot's own env
// file, saying so on one line. Every test drives the real Run with a FAKE reader: no test reaches
// a keychain, changes one, or starts an agent. Deleting the call site in run.go fails the first.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const fakeCopilotToken = "gho_FAKEtokenForTests0123456789"

// macosUserKeychainLaunch runs one real macos-user launch selecting packsJSON with read as the
// keychain, and returns the services asked for, the launch's stderr and the workspace.
func macosUserKeychainLaunch(t *testing.T, packsJSON string, read func(string) (string, error)) ([]string, string, string) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, packsJSON)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = false
	o.MacosUserHostHome = t.TempDir()
	o.MacosUserLaunchProbes = setUpMacProbes
	var asked []string
	o.HostKeychainRead = func(service string) (string, error) {
		asked = append(asked, service)
		return read(service)
	}
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		reached = true
		return 0
	}
	if rc := Run(*o); rc != 0 || !reached {
		t.Fatalf("Run() = %d, reached backend %v\nstdout:\n%s\nstderr:\n%s", rc, reached, stdout.String(), stderr.String())
	}
	return asked, stderr.String(), ws
}

func copilotEnvFile(ws string) string {
	return filepath.Join(paths.WorkspaceHomeState(ws), entrypoint.AgentEnvDirRel, "copilot.sh")
}

func TestMacosUserLaunchPassesTheHostCopilotLoginAsCOPILOT_GITHUB_TOKEN(t *testing.T) {
	asked, stderr, ws := macosUserKeychainLaunch(t, `["copilot"]`, func(service string) (string, error) {
		if service == "copilot-cli" {
			return fakeCopilotToken, nil
		}
		return "", ErrHostKeychainItemNotFound
	})
	if len(asked) != 1 || asked[0] != "copilot-cli" {
		t.Fatalf("keychain asked for %v, want exactly the copilot pack's declared service copilot-cli", asked)
	}
	path := copilotEnvFile(ws)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("copilot's env file was not written: %v\nstderr:\n%s", err, stderr)
	}
	want := "export COPILOT_GITHUB_TOKEN=${COPILOT_GITHUB_TOKEN:-'" + fakeCopilotToken + "'}\n"
	if !strings.Contains(string(raw), want) {
		t.Errorf("copilot.sh does not carry the host login def-form:\n%s", raw)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != agentEnvFileMode {
		t.Errorf("copilot.sh mode = %v (%v), want %o", fi.Mode().Perm(), err, agentEnvFileMode)
	}
	if !strings.Contains(stderr, "Host login passed in") || !strings.Contains(stderr, "copilot-cli") ||
		!strings.Contains(stderr, "COPILOT_GITHUB_TOKEN") {
		t.Errorf("the launch did not disclose the host login it passed in:\n%s", stderr)
	}
	// The secret is in copilot's file alone: never on the launch stream, its log, or any other
	// file under the workspace's state.
	if strings.Contains(stderr, fakeCopilotToken) {
		t.Error("the launch stream printed the token")
	}
	_ = filepath.WalkDir(filepath.Join(ws, ".yolo"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == path {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(fakeCopilotToken)) {
			t.Errorf("the token reached %s, a file other than copilot's env file", p)
		}
		return nil
	})
}

func TestMacosUserLaunchWithNoHostCopilotLoginSetsNothingAndNamesTheLogin(t *testing.T) {
	_, stderr, ws := macosUserKeychainLaunch(t, `["copilot"]`, func(string) (string, error) {
		return "", ErrHostKeychainItemNotFound
	})
	if raw, err := os.ReadFile(copilotEnvFile(ws)); err == nil && strings.Contains(string(raw), "COPILOT_GITHUB_TOKEN") {
		t.Errorf("a launch with no host login set COPILOT_GITHUB_TOKEN:\n%s", raw)
	}
	if !strings.Contains(stderr, "No host copilot login in your keychain") || !strings.Contains(stderr, "`copilot login`") {
		t.Errorf("the launch did not say there is no host login and how to make one:\n%s", stderr)
	}
}

func TestMacosUserLaunchWithALockedKeychainSetsNothingAndNamesTheNextStep(t *testing.T) {
	_, stderr, ws := macosUserKeychainLaunch(t, `["copilot"]`, func(string) (string, error) {
		return "", securityExitError(36)
	})
	if raw, err := os.ReadFile(copilotEnvFile(ws)); err == nil && strings.Contains(string(raw), "COPILOT_GITHUB_TOKEN") {
		t.Errorf("a refused read set COPILOT_GITHUB_TOKEN:\n%s", raw)
	}
	if !strings.Contains(stderr, "Could not read your host copilot login") || !strings.Contains(stderr, "locked") ||
		!strings.Contains(stderr, "unlock your login keychain") || !strings.Contains(stderr, "`copilot login`") {
		t.Errorf("the launch did not name the refusal and the next step:\n%s", stderr)
	}
}

func TestMacosUserLaunchWithoutCopilotReadsNoKeychain(t *testing.T) {
	asked, stderr, _ := macosUserKeychainLaunch(t, `["claude"]`, func(string) (string, error) {
		return fakeCopilotToken, nil
	})
	if len(asked) != 0 {
		t.Errorf("a launch that does not select copilot read the keychain for %v", asked)
	}
	if strings.Contains(stderr, "Host login passed in") {
		t.Errorf("a launch without copilot disclosed a host login:\n%s", stderr)
	}
}

func TestContainerLaunchWithCopilotReadsNoHostKeychain(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot"]`)
	var asked []string
	argv, out := fakePodmanLaunch(t, func(o *Options) {
		o.HostKeychainRead = func(service string) (string, error) {
			asked = append(asked, service)
			return fakeCopilotToken, nil
		}
	})
	if len(argv) == 0 {
		t.Fatalf("the podman launch never reached its run:\n%s", out)
	}
	if len(asked) != 0 {
		t.Errorf("a podman launch read the host keychain for %v (OQ-KC5 is macos-user's route alone)", asked)
	}
	if strings.Contains(strings.Join(argv, "\n"), fakeCopilotToken) {
		t.Error("the token reached the container argv")
	}
}

func TestHostKeychainLinesYieldToTheUsersOwnValue(t *testing.T) {
	body := withHostKeychainLines("", "copilot", map[string]string{"COPILOT_GITHUB_TOKEN": "it's"})
	if !strings.HasPrefix(body, agentEnvFileHeader("copilot")) {
		t.Errorf("a file of host lines alone has no header:\n%s", body)
	}
	if !strings.Contains(body, `export COPILOT_GITHUB_TOKEN=${COPILOT_GITHUB_TOKEN:-'it'\''s'}`+"\n") {
		t.Errorf("the host line is not def-form and quoted:\n%s", body)
	}
	if got := withHostKeychainLines("x\n", "copilot", map[string]string{"BAD-NAME": "v"}); got != "x\n" {
		t.Errorf("an invalid name was written: %q", got)
	}
}

func TestSecurityExitStatusesNameTheirCause(t *testing.T) {
	if !errors.Is(securityExitError(44), ErrHostKeychainItemNotFound) {
		t.Error("exit 44 (errSecItemNotFound) is not read as no item")
	}
	for code, want := range map[int]string{36: "locked", 51: "refused", 128: "declined", 7: "exited 7"} {
		if err := securityExitError(code); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("securityExitError(%d) = %v, want it to say %q", code, err, want)
		}
	}
}

func TestAMultiLineKeychainValueIsNotWritten(t *testing.T) {
	var stderr bytes.Buffer
	o := &Options{Stderr: &stderr, HostKeychainRead: func(string) (string, error) { return "a\nexport X=1", nil }}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "packs", "copilot", packdecl.ManifestTOMLName))
	if err != nil {
		t.Fatal(err)
	}
	m, problems := packdecl.DecodeTOML(raw)
	if len(problems) > 0 {
		t.Fatalf("the shipped copilot pack does not decode: %v", problems)
	}
	copilot := []*packload.Pack{{Name: "copilot", Decl: m}}
	if got := o.readHostKeychainEnv(copilot); got != nil {
		t.Errorf("a multi-line value was passed in: %v", got)
	}
	if !strings.Contains(stderr.String(), "not a single-line token") {
		t.Errorf("the launch did not say why it passed nothing:\n%s", stderr.String())
	}
}
