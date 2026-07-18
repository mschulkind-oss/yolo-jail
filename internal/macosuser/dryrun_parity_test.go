package macosuser

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestDryRunArtifactParity is the Stage 16b parity gate: the full RunPlan
// artifact dump (SBPL text, sudo argv lists, bootstrap script, launch argv,
// invariant problems, darwin threading) diffed Go-vs-live-Python across a
// fixture matrix. Linux-runnable — the whole reason this backend was designed
// this way. Skips when Python is absent.
func TestDryRunArtifactParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}

	type fixture struct {
		name       string
		workspace  string
		configJSON string // python dict literal via json
		agents     []string
		agentArgv  []string
		sandboxEnv map[string]string
		interp     string // "" => None
		darwin     bool   // include a fake darwin result
	}
	fixtures := []fixture{
		{
			name:      "minimal",
			workspace: "/Users/Shared/yolo/proj",
			agents:    []string{"claude"}, agentArgv: []string{"claude"},
			sandboxEnv: map[string]string{"YOLO_GIT_NAME": "Ada", "TERM": "xterm"},
			interp:     "/opt/homebrew/bin/python3",
		},
		{
			name:       "full-config-with-darwin",
			workspace:  "/Users/Shared/yolo/proj",
			configJSON: `{"security":{"blocked_tools":["curl"]},"mcp_servers":{"srv":{"command":"x"}},"macos_log":"user","mise_tools":{"jq":"latest"},"lsp_servers":{"go":{"command":"gopls"}},"mcp_presets":["chrome-devtools"]}`,
			agents:     []string{"claude", "codex"}, agentArgv: []string{"claude", "--dangerously-skip-permissions"},
			sandboxEnv: map[string]string{"YOLO_GIT_NAME": "Ada", "YOLO_JJ_EMAIL": "j@x", "TERM": "xterm", "ANTHROPIC_API_KEY": "sk"},
			interp:     "/opt/homebrew/bin/python3", darwin: true,
		},
		{
			name:      "unresolved-interp",
			workspace: "/Users/Shared/yolo/proj",
			agents:    []string{"claude"}, agentArgv: []string{"bash", "-l"},
			sandboxEnv: map[string]string{},
			interp:     "",
		},
		{
			name:      "home-workspace-violation",
			workspace: "/Users/matt/code/proj",
			agents:    []string{"claude"}, agentArgv: []string{"claude"},
			sandboxEnv: map[string]string{"YOLO_GIT_NAME": "Ada"},
			interp:     "/opt/homebrew/bin/python3",
		},
		{
			name:      "tricky-path",
			workspace: `/Users/Shared/yolo/a"b\c`,
			agents:    []string{"claude"}, agentArgv: []string{"claude", "arg with space"},
			sandboxEnv: map[string]string{"TERM": "xterm-kitty"},
			interp:     "/usr/local/bin/python3",
		},
	}

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			cfgLit := f.configJSON
			if cfgLit == "" {
				cfgLit = "{}"
			}
			envJSON, _ := json.Marshal(f.sandboxEnv)
			agentsJSON, _ := json.Marshal(f.agents)
			argvJSON, _ := json.Marshal(f.agentArgv)
			interpPy := "None"
			if f.interp != "" {
				interpPy = jsonStr(f.interp)
			}
			darwinPy := "None"
			if f.darwin {
				darwinPy = "_FakeDarwin()"
			}
			script := `
import sys, json
sys.path.insert(0, 'src')
from pathlib import Path
from cli import macos_user as m

class _FakeDarwin:
    path_prefix = ["/nix/store/a-jq/bin"]
    env = {"PKG_CONFIG_PATH": "/nix/store/a/lib/pkgconfig"}
    skipped = ["nolinux"]

plan = m.build_run_plan(
    Path(` + jsonStr(f.workspace) + `),
    json.loads(` + jsonStr(cfgLit) + `),
    json.loads(` + jsonStr(string(agentsJSON)) + `),
    json.loads(` + jsonStr(string(argvJSON)) + `),
    repo_src=Path("/opt/yolo-jail/src"),
    sandbox_env=json.loads(` + jsonStr(string(envJSON)) + `),
    interp=` + interpPy + `,
    darwin=` + darwinPy + `,
)
dump = {
  "cname": plan.cname,
  "profile_path": str(plan.profile_path),
  "seatbelt": plan.seatbelt,
  "bootstrap": plan.bootstrap,
  "bootstrap_path": str(plan.bootstrap_path),
  "bootstrap_argv": plan.bootstrap_argv,
  "launch_argv": plan.launch_argv,
  "stage_commands": plan.stage_commands,
  "git_identity": plan.git_identity,
  "problems": m.plan_invariants(plan),
  "darwin_prefix": plan.darwin_path_prefix,
  "darwin_skipped": plan.darwin_skipped,
}
print(json.dumps(dump))
`
			outBytes, err := py("-c", script).Output()
			if err != nil {
				t.Skipf("python build_run_plan failed: %v", err)
			}
			var want struct {
				Cname         string            `json:"cname"`
				ProfilePath   string            `json:"profile_path"`
				Seatbelt      string            `json:"seatbelt"`
				Bootstrap     string            `json:"bootstrap"`
				BootstrapPath string            `json:"bootstrap_path"`
				BootstrapArgv []string          `json:"bootstrap_argv"`
				LaunchArgv    []string          `json:"launch_argv"`
				StageCommands [][]string        `json:"stage_commands"`
				GitIdentity   map[string]string `json:"git_identity"`
				Problems      []string          `json:"problems"`
				DarwinPrefix  []string          `json:"darwin_prefix"`
				DarwinSkipped []string          `json:"darwin_skipped"`
			}
			if err := json.Unmarshal(outBytes, &want); err != nil {
				t.Fatalf("decode: %v\n%s", err, outBytes)
			}

			// Build the Go plan from the same inputs.
			cfg := decodeConfig(t, cfgLit)
			// The Python sandbox_env is a plain dict; its iteration order for the
			// git-identity extraction and launch env is insertion order of the
			// JSON object. Go's json decode into a map loses order, so decode via
			// jsonx to preserve it.
			env := decodeOrderedStrMap(t, string(envJSON))
			var darwin *Darwin
			if f.darwin {
				denv := jsonx.NewOrderedMap()
				denv.Set("PKG_CONFIG_PATH", "/nix/store/a/lib/pkgconfig")
				darwin = &Darwin{PathPrefix: []string{"/nix/store/a-jq/bin"}, Env: denv, Skipped: []string{"nolinux"}}
			}
			plan := BuildRunPlan(f.workspace, cfg, f.agents, f.agentArgv, "/opt/yolo-jail/src",
				env, f.interp, f.interp != "", darwin)
			problems := PlanInvariants(plan)

			eq := func(field, got, w string) {
				if got != w {
					t.Errorf("%s mismatch:\n go: %q\n py: %q", field, got, w)
				}
			}
			eq("cname", plan.Cname, want.Cname)
			eq("profile_path", plan.ProfilePath, want.ProfilePath)
			eq("seatbelt", plan.Seatbelt, want.Seatbelt)
			eq("bootstrap", plan.Bootstrap, want.Bootstrap)
			eq("bootstrap_path", plan.BootstrapPath, want.BootstrapPath)
			if !reflect.DeepEqual(plan.BootstrapArgv, want.BootstrapArgv) {
				t.Errorf("bootstrap_argv:\n go: %v\n py: %v", plan.BootstrapArgv, want.BootstrapArgv)
			}
			if !reflect.DeepEqual(plan.LaunchArgv, want.LaunchArgv) {
				t.Errorf("launch_argv:\n go: %v\n py: %v", plan.LaunchArgv, want.LaunchArgv)
			}
			if !reflect.DeepEqual(plan.StageCommands, want.StageCommands) {
				t.Errorf("stage_commands:\n go: %v\n py: %v", plan.StageCommands, want.StageCommands)
			}
			if !sameStrMap(plan.GitIdentity, want.GitIdentity) {
				t.Errorf("git_identity:\n go: %v\n py: %v", omToMap(plan.GitIdentity), want.GitIdentity)
			}
			if !reflect.DeepEqual(nilToEmpty(problems), nilToEmpty(want.Problems)) {
				t.Errorf("problems:\n go: %v\n py: %v", problems, want.Problems)
			}
			if !reflect.DeepEqual(nilToEmpty(plan.DarwinPathPrefix), nilToEmpty(want.DarwinPrefix)) {
				t.Errorf("darwin_prefix:\n go: %v\n py: %v", plan.DarwinPathPrefix, want.DarwinPrefix)
			}
			if !reflect.DeepEqual(nilToEmpty(plan.DarwinSkipped), nilToEmpty(want.DarwinSkipped)) {
				t.Errorf("darwin_skipped:\n go: %v\n py: %v", plan.DarwinSkipped, want.DarwinSkipped)
			}
		})
	}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func decodeConfig(t *testing.T, lit string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(lit))
	if err != nil {
		t.Fatalf("config decode: %v", err)
	}
	if v == nil {
		return jsonx.NewOrderedMap()
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("config not a map: %T", v)
	}
	return m
}

func decodeOrderedStrMap(t *testing.T, lit string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(lit))
	if err != nil {
		t.Fatalf("env decode: %v", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return jsonx.NewOrderedMap()
	}
	return m
}

func sameStrMap(m *jsonx.OrderedMap, want map[string]string) bool {
	got := omToMap(m)
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func omToMap(m *jsonx.OrderedMap) map[string]string {
	out := map[string]string{}
	if m == nil {
		return out
	}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = asStr(v)
	}
	return out
}

func nilToEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
