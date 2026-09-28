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
	"path/filepath"
	"strings"
	"testing"
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

func TestTheHostWithholdsAPointerAtAJailDaemonAndNamesIt(t *testing.T) {
	for _, tc := range []struct {
		name, packs, agent, profile string
		withheld                    []string
		daemon                      string
	}{
		{"the bedrock pointer", `["claude", "aws-auth"]`, "claude", "bedrock",
			[]string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"}, "aws-auth"},
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
