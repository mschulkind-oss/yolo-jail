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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const fakeCopilotToken = "gho_FAKEtokenForTests0123456789"

// macosUserKeychainLaunch runs one real macos-user launch selecting packsJSON with read as the
// keychain, and returns the services asked for, the launch's stderr and the workspace.
func macosUserKeychainLaunch(t *testing.T, packsJSON string, read func(string) (string, error)) ([]string, string, string) {
	t.Helper()
	return macosUserKeychainLaunchEnv(t, packsJSON, nil, read)
}

// macosUserKeychainLaunchEnv is macosUserKeychainLaunch with env layered over the launch's own
// Getenv, which is how yolo's environment reaches the keychain line.
func macosUserKeychainLaunchEnv(t *testing.T, packsJSON string, env map[string]string, read func(string) (string, error)) ([]string, string, string) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, packsJSON)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	if env != nil {
		inner := o.Getenv
		o.Getenv = func(k string) string {
			if v, ok := env[k]; ok {
				return v
			}
			return inner(k)
		}
	}
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

// Exit 36 (errSecInteractionNotAllowed) is macOS refusing to let `security` show its dialog, not
// a locked keychain: the owner's keychain was unlocked and no dialog appeared
// (docs/lab-reports/2026-11-03-mac-slice-12a-witness-w1.md §14). So the line must not send the
// human to unlock it. It names the cause (ssh leads when yolo's own environment shows ssh: the
// owner measured rc=36 over ssh and rc=0 from a Terminal on the Mac's display), the one-time
// Always Allow from a Terminal on the Mac itself, the exact test for the declared service, and
// the fact that Always Allow's effect on later SSH reads remains unmeasured.
func TestMacosUserLaunchWhoseKeychainReadCannotShowADialogNamesTheCauseAndTheTest(t *testing.T) {
	sshCause := "this shell is reached over ssh, outside your desktop session, so macOS cannot show the access dialog here"
	generalCause := "this shell runs in a session outside your desktop login"
	for _, tc := range []struct {
		name            string
		env             map[string]string
		want, notWanted string
	}{
		{"ssh-connection", map[string]string{"SSH_CONNECTION": "10.0.0.2 51000 10.0.0.1 22", "SSH_TTY": ""}, sshCause, generalCause},
		{"ssh-tty", map[string]string{"SSH_CONNECTION": "", "SSH_TTY": "/dev/ttys004"}, sshCause, generalCause},
		{"no-ssh", map[string]string{"SSH_CONNECTION": "", "SSH_TTY": ""}, generalCause, "over ssh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, ws := macosUserKeychainLaunchEnv(t, `["copilot"]`, tc.env, func(string) (string, error) {
				return "", securityExitError(36)
			})
			if raw, err := os.ReadFile(copilotEnvFile(ws)); err == nil && strings.Contains(string(raw), "COPILOT_GITHUB_TOKEN") {
				t.Errorf("a refused read set COPILOT_GITHUB_TOKEN:\n%s", raw)
			}
			for _, want := range []string{
				"Could not read your host copilot login",
				"macOS refused to let `security` show its keychain access dialog to this process",
				tc.want,
				"Run this launch once from a Terminal on the Mac itself and choose Always Allow (not Allow",
				"whether Always Allow also enables later reads over SSH has not been measured",
				"`security find-generic-password -s copilot-cli -w >/dev/null; echo rc=$?`",
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the launch line does not say %q:\n%s", want, stderr)
				}
			}
			for _, wrong := range []string{"locked", "unlock your login keychain", "later reads should then need no dialog", tc.notWanted} {
				if strings.Contains(stderr, wrong) {
					t.Errorf("the launch line says %q:\n%s", wrong, stderr)
				}
			}
		})
	}
}

// Every other refusal keeps the general next step.
func TestMacosUserLaunchWhoseKeychainRefusedTheReadNamesTheNextStep(t *testing.T) {
	_, stderr, _ := macosUserKeychainLaunch(t, `["copilot"]`, func(string) (string, error) {
		return "", securityExitError(51)
	})
	if !strings.Contains(stderr, "Could not read your host copilot login") || !strings.Contains(stderr, "refused the request") ||
		!strings.Contains(stderr, "allow the request when macOS asks") || !strings.Contains(stderr, "`copilot login`") {
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
	if err := securityExitError(36); !errors.Is(err, ErrHostKeychainNoDialog) || strings.Contains(err.Error(), "locked") {
		t.Errorf("exit 36 (errSecInteractionNotAllowed) = %v, want ErrHostKeychainNoDialog, never \"locked\"", err)
	}
	for code, want := range map[int]string{51: "refused", 128: "declined", 7: "exited 7"} {
		if err := securityExitError(code); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("securityExitError(%d) = %v, want it to say %q", code, err, want)
		}
	}
}

func TestNoDialogProbeShellQuotesDeclaredService(t *testing.T) {
	service := `example key; printf PWNED 'x'`
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "packs", "copilot", packdecl.ManifestTOMLName))
	if err != nil {
		t.Fatal(err)
	}
	old := []byte(`services = ["copilot-cli"]`)
	if !bytes.Contains(raw, old) {
		t.Fatal("copilot manifest fixture no longer has its expected service declaration")
	}
	raw = bytes.Replace(raw, old, []byte(`services = ["`+service+`"]`), 1)
	m, problems := packdecl.DecodeTOML(raw)
	if len(problems) > 0 {
		t.Fatalf("printable shell-metacharacter service should be valid: %v", problems)
	}

	var stderr bytes.Buffer
	var asked string
	o := &Options{
		Stderr: &stderr,
		HostKeychainRead: func(got string) (string, error) {
			asked = got
			return "", ErrHostKeychainNoDialog
		},
	}
	if got := o.readHostKeychainEnv([]*packload.Pack{{Name: "copilot", Decl: m}}); got != nil {
		t.Fatalf("a refused read passed an env value: %v", got)
	}
	if asked != service {
		t.Fatalf("reader received service %q, want exact one argv word %q", asked, service)
	}
	wantProbe := shquote.Join([]string{"security", "find-generic-password", "-s", service, "-w"}) + " >/dev/null; echo rc=$?"
	if !strings.Contains(stderr.String(), wantProbe) {
		t.Fatalf("launch probe does not safely quote the declared service; want %q in:\n%s", wantProbe, stderr.String())
	}

	// Execute only the printed probe against a harmless fake `security`; a semicolon or quote
	// escaping defect must neither split the service argv nor execute the injected printf.
	binDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeSecurity := "#!/bin/sh\nprintf '%s\\n' \"$#\" > \"$ARGV_FILE\"\nprintf '<%s>\\n' \"$@\" >> \"$ARGV_FILE\"\nexit 36\n"
	if err := os.WriteFile(filepath.Join(binDir, "security"), []byte(fakeSecurity), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", wantProbe)
	cmd.Env = []string{"PATH=" + binDir + ":" + os.Getenv("PATH"), "ARGV_FILE=" + argvFile}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fake probe shell failed: %v; output %q", err, output)
	}
	if string(output) != "rc=36\n" {
		t.Errorf("probe executed extra shell syntax: output %q, want only rc=36", output)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := "4\n<find-generic-password>\n<-s>\n<" + service + ">\n<-w>\n"
	if string(argv) != wantArgv {
		t.Errorf("fake security received argv %q, want %q", argv, wantArgv)
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
