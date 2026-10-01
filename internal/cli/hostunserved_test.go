package cli

// hostunserved_test.go pins the host notch's half of served-address composition
// (docs/plans/notch-convergence.md §4 item 2): a pack env variable that points at a jail daemon
// (`served_by`) is withheld at the host, where no jail daemon runs, and named. MEASURED
// 2026-09-27 before this: `yolo host env --agent claude -p bedrock` exported
// AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/credentials, rc 0, no warning — a
// pointer at a port nothing on the host serves, so whatever local process bound it first would
// be handed the SDK's credential request. Through the real composeHostVarsGranting, so deleting
// the gate's Served input or the host's FoldFor call fails it.

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
)

func hostUnservedHome(t *testing.T, cfg string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	t.Setenv("CODEX_REFRESH_TOKEN_URL_OVERRIDE", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, cfg)
}

// setsVarManagedLaunch is a managed host launch that serves CODEX_REFRESH_TOKEN_URL_OVERRIDE
// itself when serves is true, as openaiauthhost's Codex launch does, and refuses the exec.
type setsVarManagedLaunch struct{ serves bool }

func (f setsVarManagedLaunch) Environ(base []string) []string {
	if f.serves {
		return append(base, "CODEX_REFRESH_TOKEN_URL_OVERRIDE=http://127.0.0.1:5555/oauth/token")
	}
	return base
}

func (setsVarManagedLaunch) Argv(argv []string) ([]string, []string) { return argv, nil }

func (setsVarManagedLaunch) NotServed(string) string { return "" }

func (setsVarManagedLaunch) Run(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, bool) {
	return 23, true
}

// A LAUNCH THAT SERVES A POINTER ITSELF IS NOT TOLD IT IS MISSING. `yolo host -- codex` runs
// its own refresh adapter (openaiauthhost) and sets the URL the codex pack's withheld pointer
// names, so naming that pointer as unserved would be false there, while `yolo host env`, which
// starts no adapter, names it. Through the real hostExec, so deleting managedHostVars at the
// call site fails the first case.
func TestAHostLaunchThatServesThePointerItselfDoesNotNameIt(t *testing.T) {
	for _, serves := range []bool{true, false} {
		hostUnservedHome(t, `{"packs": ["codex"]}`)
		original := prepareOpenAIAuthHost
		prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) {
			return setsVarManagedLaunch{serves: serves}, nil
		}
		var errw bytes.Buffer
		rc := hostExec(nil, []string{"true"}, io.Discard, &errw, nil)
		prepareOpenAIAuthHost = original
		if rc != 23 {
			t.Fatalf("serves=%v: hostExec = %d; the managed launch was not reached\n%s", serves, rc, errw.String())
		}
		named := strings.Contains(errw.String(), "CODEX_REFRESH_TOKEN_URL_OVERRIDE")
		if named == serves {
			t.Errorf("serves=%v: the launch named the withheld refresh URL = %v, want %v:\n%s",
				serves, named, !serves, errw.String())
		}
	}
}

// A MANAGED LAUNCH THAT DID NOT START SAYS SO, and not HS-D22's reason. `yolo host -p codex --
// codex` with no OpenAI login and no terminal starts no managed launch (openaiauthhost logs in
// only where a human can answer), so codex runs without the refresh URL. Its line used to give
// the reason that holds for an agent other than codex, that `yolo host --` opens the doorway for
// no selection (HS-D22); for codex the doorway is closed because the managed launch serves the
// URL itself (HS-D20), and what is missing is that launch, for want of a login. pi under the
// same conditions keeps HS-D22's clause: no managed launch of pi's serves that URL.
//
// Through the real hostMain and the real prelaunch, against the real OpenAI credential service
// holding no login in this test's home (spawned in this package's private singleton dir, and
// stopped here), so deleting the reason where the prelaunch decides or where the host prints
// fails it. No login starts: there is no terminal.
func TestAHostLaunchWhoseManagedLaunchDidNotStartSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		agent        string
		managedVoice bool
	}{{"codex", true}, {"pi", false}} {
		t.Run(tc.agent, func(t *testing.T) {
			hostUnservedHome(t, `{"packs": ["codex", "pi"]}`)
			setGateTTY(t, false)
			t.Cleanup(func() {
				broker.BrokerKill(broker.SingletonDeps(openaiauthhost.BrokerName, nil), syscall.SIGTERM, 5*time.Second)
			})
			rc, execed, errs := hostExecRun(t, tc.agent, "-p", "codex")
			if rc != 0 || !execed {
				t.Fatalf("yolo host -p codex -- %s: rc=%d exec'd=%v\n%s", tc.agent, rc, execed, errs)
			}
			if !strings.Contains(errs, tc.agent+": OpenAI login is required, and this is not an interactive terminal") {
				t.Fatalf("the fixture is not the no-login, no-terminal launch:\n%s", errs)
			}
			var line string
			for _, l := range strings.Split(errs, "\n") {
				if strings.Contains(l, "CODEX_REFRESH_TOKEN_URL_OVERRIDE — points at") {
					line = l
				}
			}
			if line == "" {
				t.Fatalf("the withheld refresh URL is not named:\n%s", errs)
			}
			managed := []string{"no OpenAI login", "not an interactive terminal",
				"run 'yolo host -- " + tc.agent + "' once from a terminal to log in", "HS-D20"}
			hsd22 := []string{"opens for no selection", "HS-D22"}
			want, not := hsd22, managed[:1]
			if tc.managedVoice {
				want, not = managed, hsd22
			}
			for _, w := range want {
				if !strings.Contains(line, w) {
					t.Errorf("the refresh URL's line must say %q:\n%s", w, line)
				}
			}
			for _, n := range not {
				if strings.Contains(line, n) {
					t.Errorf("the refresh URL's line says %q, the wrong reason for %s:\n%s", n, tc.agent, line)
				}
			}
		})
	}
}

func TestTheHostWithholdsAPointerAtAJailDaemonAndNamesIt(t *testing.T) {
	for _, tc := range []struct {
		name, packs, agent, profile string
		withheld                    []string
		daemon                      string
	}{
		{"the bedrock pointer", `["claude", "aws-auth"]`, "claude", "bedrock",
			[]string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"}, "aws-auth"},
		{"codex's refresh URL", `["codex"]`, "codex", "",
			[]string{"CODEX_REFRESH_TOKEN_URL_OVERRIDE"}, "openai-auth-broker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostUnservedHome(t, `{"packs": `+tc.packs+`}`)
			vars, disclosure, err := hostEnvDelta(tc.agent, tc.profile, nil, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range vars {
				for _, k := range tc.withheld {
					if v.Key == k && !v.Unset {
						t.Errorf("the host exported %s=%s, an address no host process serves", v.Key, v.Value)
					}
				}
			}
			joined := strings.Join(disclosure, "\n")
			for _, want := range append(tc.withheld, `"`+tc.daemon+`"`, "Not set at this notch") {
				if !strings.Contains(joined, want) {
					t.Errorf("the host's disclosure does not name %s:\n%s", want, joined)
				}
			}
		})
	}
}
