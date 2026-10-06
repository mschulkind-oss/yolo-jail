package entrypoint

// callerauth_test.go pins the jail side of a pack service's caller token
// (docs/reference/wire-bridge.md WB-D18): boot.log records which services demand one, and every
// OpenAI-speaking derive sends a via route's token variable — never the provider's key — in the
// spelling its agent expands.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const bridgeTokenVar = "YOLO_SERVICE_WIRE_BRIDGE_TOKEN"

func TestTheBootLogSaysWhichServicesRequireCallerAuth(t *testing.T) {
	tok := strings.Repeat("ab", 32)
	e, stderr, logOnly := loudEnv(t)
	e.Vars[bridgeTokenVar] = tok
	noteServiceCallerAuth(e)
	mustContain(t, "boot.log", logOnly, "wire-bridge requires caller auth", "$"+bridgeTokenVar)
	if strings.Contains(logOnly.String(), tok) {
		t.Errorf("THE CALLER TOKEN REACHED boot.log:\n%s", logOnly)
	}
	if stderr.Len() != 0 {
		t.Errorf("a healthy launch's caller-auth record belongs in boot.log only, not on the terminal:\n%s", stderr)
	}

	e, _, logOnly = loudEnv(t)
	e.Vars[bridgeTokenVar] = "local"
	noteServiceCallerAuth(e)
	mustContain(t, "boot.log", logOnly, "wire-bridge was handed a malformed caller token")

	e, _, logOnly = loudEnv(t)
	noteServiceCallerAuth(e)
	if logOnly.Len() != 0 {
		t.Errorf("a launch with no caller token records none:\n%s", logOnly)
	}
}

// The call site: the container boot records the caller-auth line before it starts the
// supervisor that runs the daemons demanding the token. The step's own body is run, so a step
// that stopped calling noteServiceCallerAuth fails here as well as a step that moved.
func TestTheBootRecordsCallerAuthBeforeStartingTheSupervisor(t *testing.T) {
	assertStepBefore(t, bootContainer, "note_service_caller_auth", "start_jail_daemon_supervisor",
		"boot.log would say the bridge requires caller auth only after the daemons demanding it started")
	if !isRun(mustBootStep(t, "start_jail_daemon_supervisor"), startJailDaemons) {
		t.Error("start_jail_daemon_supervisor no longer runs startJailDaemons")
	}
	e, _, logOnly := loudEnv(t)
	e.Vars[bridgeTokenVar] = strings.Repeat("ab", 32)
	mustBootStep(t, "note_service_caller_auth").run(&bootRun{e: e, target: bootContainer})
	mustContain(t, "boot.log", logOnly, "wire-bridge requires caller auth")
}

// The per-surface selection hands a via agent's derive the via service's token variable,
// read off the launch's packs, and hands it nothing when the profile is not a via profile.
func TestSurfaceSelectionNamesTheViaServicesCallerToken(t *testing.T) {
	m, problems := packdecl.Decode([]byte(`{"contributes":[{"kind":"service","name":"wire-bridge",` +
		`"endpoint":"wire-bridge.endpoint","jail_daemon":{"cmd":["yolo-jaild","wire-bridge"]},` +
		`"via_address":"` + viaBase + `"}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	packs := []*packload.Pack{{Name: "wire-bridge", Decl: m}}
	resolved := map[string]packload.ResolvedProfile{
		"pz":    {Provider: "zai", Via: "wire-bridge", ViaBase: viaBase},
		"plain": {Provider: "zai"},
	}
	profiles := map[string]string{"pi": "pz", "opencode": "plain"}
	if got := surfaceSelectionFor(packs, resolved, profiles, nil, manifest.Surface{Agent: "pi", Name: "models"}).ViaAPIKeyEnvName; got != bridgeTokenVar {
		t.Errorf("pi's via credential = %q, want %s", got, bridgeTokenVar)
	}
	if got := surfaceSelectionFor(packs, resolved, profiles, nil, manifest.Surface{Agent: "opencode"}).ViaAPIKeyEnvName; got != "" {
		t.Errorf("opencode's via credential = %q, want none (its profile is not a via profile)", got)
	}
}

func viaTokenSelection(agent string) surfaceSelection {
	return surfaceSelection{Profile: "pz", Provider: "zai", ViaURL: viaBase + "/agent/" + agent,
		ViaAPIKeyEnvName: bridgeTokenVar}
}

// Each via row references the token variable in its own agent's spelling; the provider's own
// key variable (a table with one: "other") never becomes a via row's key.
func TestEveryViaDeriveSendsTheCallerTokenVariable(t *testing.T) {
	e := &Env{Vars: map[string]string{}}
	pi := piModelsFor(t, viaTokenSelection("pi"), e, viaProvidersTable())["zai"].(map[string]any)
	if pi["apiKey"] != "${"+bridgeTokenVar+"}" {
		t.Errorf("pi via row apiKey = %v, want ${%s}", pi["apiKey"], bridgeTokenVar)
	}

	for _, tc := range []struct{ pack, surface, agent string }{
		{"omp", "oh-omp/models", "omp"},
		{"opencode", "opencode/config", "opencode"},
	} {
		script, s := deriveSurface(t, tc.pack, tc.surface)
		got, _, err := deriveComputedLayer(e, s, script, viaTokenSelection(tc.agent),
			map[string]map[string]any{manifest.SourceProviders: viaProvidersTable()})
		if err != nil {
			t.Fatal(err)
		}
		switch tc.agent {
		case "omp":
			row := got["providers"].(map[string]any)["zai"].(map[string]any)
			if row["apiKey"] != bridgeTokenVar {
				t.Errorf("omp via row apiKey = %v, want the variable name %s (omp resolves it)", row["apiKey"], bridgeTokenVar)
			}
		case "opencode":
			opts := got["provider"].(map[string]any)["zai"].(map[string]any)["options"].(map[string]any)
			if opts["apiKey"] != "{env:"+bridgeTokenVar+"}" {
				t.Errorf("opencode via row apiKey = %v, want {env:%s}", opts["apiKey"], bridgeTokenVar)
			}
		}
	}

	codex := codexRow(t, codexConfigFor(t, surfaceSelection{Profile: "pr", Provider: "router",
		ViaURL: viaBase + "/agent/codex", ViaAPIKeyEnvName: bridgeTokenVar}), "router")
	if codex["env_key"] != bridgeTokenVar {
		t.Errorf("codex via row env_key = %v, want %s — never the provider's ROUTER_API_KEY", codex["env_key"], bridgeTokenVar)
	}
}
