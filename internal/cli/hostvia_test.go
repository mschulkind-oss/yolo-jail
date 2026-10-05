package cli

// hostvia_test.go pins `via` at the host notch (docs/design/wire-bridge-gateway.md WG-I12, as
// WG-I46 narrowed it; docs/design/host-notch-services.md HS-D30 to HS-D32). `yolo host env` and the
// footer's tables carry no via URL even when the user lists wire-bridge in `packs`, which gives
// ResolveProfiles a via_address to resolve there: neither owns a process a service could live
// beside. `yolo host --` starts the bridge's host half for a via or a carrier that routes its agent
// through it, unless the agent's own config file carries the route.

import (
	"bytes"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
)

// hostViaHome selects wire-bridge explicitly, plus a local pack whose agent's env derive
// reports ctx.via_url as VIA_URL and always sets DERIVE_RAN, so an absent VIA_URL is known
// to be the derive's input rather than a derive that never ran. The agent's active profile
// routes it through wire-bridge.
func hostViaHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"viaagent","contributes":[` +
		`{"kind":"program","bin":"viaagent","via":"npm","package":"@example/viaagent"},` +
		`{"kind":"provider","name":"up","endpoints":{"openai":{"base_url":"https://up.example/v1"}}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "derive.lua"), []byte(
		`yolo.env("viaagent", function(ctx)
  return { VIA_URL = ctx.via_url or "", DERIVE_RAN = "1" }
end)`), 0o644); err != nil {
		t.Fatal(err)
	}
	userCfg(t, home, `{
	  "packs": ["wire-bridge"],
	  "profiles": {"pv": {"provider": "up", "via": "wire-bridge"}},
	  "profile": {"viaagent": "pv"}
	}`)
}

// TestHostEnvDeriveGetsNoViaURL: `yolo host -- viaagent` composes the agent's env with no
// via URL, though its profile names wire-bridge and wire-bridge is selected.
func TestHostEnvDeriveGetsNoViaURL(t *testing.T) {
	hostViaHome(t)
	env, _, err := composeHostEnv("viaagent", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	for _, kv := range env {
		if kv == "DERIVE_RAN=1" {
			ran = true
		}
		if strings.HasPrefix(kv, "VIA_URL=") && kv != "VIA_URL=" {
			t.Errorf("the host notch handed the env derive a via URL nothing serves there: %s", kv)
		}
	}
	if !ran {
		t.Fatal("the agent's env derive did not run, so the via URL was never asked about")
	}
}

// TestHostFooterTablesCarryNoViaAddress: the footer's profile table names the via and no
// address for it, the table `yolo host env` composes.
func TestHostFooterTablesCarryNoViaAddress(t *testing.T) {
	hostViaHome(t)
	tables := hostFooterTables()
	if !strings.Contains(tables.Profiles, `"_via"`) {
		t.Fatalf("the footer's profile table lost the via itself: %s", tables.Profiles)
	}
	if strings.Contains(tables.Profiles, "_via_base") {
		t.Errorf("the footer's profile table carries a via address at the host notch: %s", tables.Profiles)
	}
}

// scrubAWS empties every AWS credential, pointer and region variable this process inherited, so a
// host half the launch spawns (whose environment is this process's under its input) signs with
// nothing but what the launch handed it: a jail running this suite may hold a live aws-auth
// pointer of its own.
func scrubAWS(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_PROFILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN", "AWS_REGION", "AWS_DEFAULT_REGION"} {
		t.Setenv(k, "")
	}
}

// deadLoopbackURL is an http URL on a loopback port nothing listens on: a credential pointer the
// bridge can sign with only by fetching it, which fails without leaving the machine.
func deadLoopbackURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr + "/credentials"
}

// copilotOnBedrockViaConfig is copilot beside the shipped bedrock provider (a region, no address)
// and the wire bridge, with a credential pointer delivered through env_sources that nothing
// answers, so the bridge binds its route and a request through it fails at the credential fetch,
// on loopback, instead of reaching AWS.
func copilotOnBedrockViaConfig(t *testing.T) string {
	return `{"packs": ["copilot", "bedrock", "wire-bridge"], ` +
		`"providers": {"bedrock": {"region": "eu-west-1"}}, ` +
		`"env_sources": [{"AWS_CONTAINER_CREDENTIALS_FULL_URI": "` + deadLoopbackURL(t) + `"}]}`
}

// COPILOT ON A BEDROCK BRIDGE PROFILE AT `yolo host` (docs/design/host-notch-services.md HS-D30):
// `-p bedrock-bridge -- copilot` (the profile's via) and `-p bedrock -- copilot` (the carrier,
// WG-I44) each start the bridge's host half for the launch, point copilot at the adapter address
// composed for the via at the port the launch picked, with the caller token as its key, and the
// bridge answers copilot's request with the token (503: the credential fetch fails on loopback)
// and refuses it without (401). The start line names only copilot's route (HS-D24). Before it,
// both profiles cleared the via at the host and copilot reached nothing. Deleting the via
// trigger in composeHostVarsWith fails this.
func TestHostCopilotOnABedrockBridgeProfileRunsThroughALaunchOwnedBridge(t *testing.T) {
	for _, profile := range []string{"bedrock-bridge", "bedrock"} {
		t.Run(profile, func(t *testing.T) {
			scrubAWS(t)
			l := runServiceLaunchAs(t, copilotOnBedrockViaConfig(t), []string{"-p", profile}, "copilot", "", nil, nil)
			if l.rc != 0 {
				t.Fatalf("rc = %d\n%s", l.rc, l.errs)
			}
			if len(l.started) != 1 || l.started[0].Plan.Service != "wire-bridge" || l.execed {
				t.Fatalf("started %d services, exec'd %v; want the wire bridge as a child\n%s",
					len(l.started), l.execed, l.errs)
			}
			plan := l.started[0].Plan
			picked := plan.Moved["127.0.0.1:8214"]
			u, err := url.Parse(l.report.Env["COPILOT_PROVIDER_BASE_URL"])
			if err != nil || picked == "" || u.Host != picked {
				t.Fatalf("COPILOT_PROVIDER_BASE_URL = %q, want the picked %s\n%s",
					l.report.Env["COPILOT_PROVIDER_BASE_URL"], picked, l.errs)
			}
			if l.report.Env["COPILOT_PROVIDER_API_KEY"] != plan.Token {
				t.Errorf("copilot's provider key is not the launch's caller token")
			}
			// packs/bedrock ships no list (MM-D32), so copilot starts on its own one open-weight
			// default (MM-D34), and the host writes no agent file (MM-D33).
			if got := l.report.Env["COPILOT_MODEL"]; got != "openai.gpt-oss-120b-1:0" {
				t.Errorf("COPILOT_MODEL = %q, want copilot's Bedrock default openai.gpt-oss-120b-1:0", got)
			}
			if got, set := l.report.Env["COPILOT_PROVIDERS_CONFIG"]; set {
				t.Errorf("COPILOT_PROVIDERS_CONFIG = %q at the host, which writes no agent file", got)
			}
			if l.report.WithToken != http.StatusServiceUnavailable {
				t.Errorf("copilot's request through the bridge got %d, want 503 (the credential fetch "+
					"fails on loopback): %s", l.report.WithToken, l.report.Body)
			}
			if l.report.WithoutAuth != http.StatusUnauthorized {
				t.Errorf("a request without the caller token got %d, want 401", l.report.WithoutAuth)
			}
			line := ""
			if i := strings.Index(l.errs, `started the "wire-bridge" service`); i >= 0 {
				line, _, _ = strings.Cut(l.errs[i:], "\n")
			}
			if !strings.Contains(line, "for copilot on "+picked+";") {
				t.Errorf("the start line must name copilot's route %s and no other:\n%s", picked, l.errs)
			}
			if strings.Contains(l.errs, `'s via — its service does not run here`) {
				t.Errorf("the launch still names the via as unserved:\n%s", l.errs)
			}
			assertServiceGone(t, l)
		})
	}
}

// A LIST AT THE HOST STAYS ONE MODEL (docs/design/model-lists-and-pickers.md MM-D33): copilot's
// providers.json carries its key as literal text and is an agent file, which only a jail writes, so
// at `yolo host` a Bedrock list a user supplies starts copilot on the list's first entry through
// its environment, bare, and no variable names a file. A jail on the same list writes the file
// (internal/cli/run's copilotbyok_test.go).
func TestHostCopilotOnASuppliedBedrockListKeepsItsEnvironmentsOneModel(t *testing.T) {
	scrubAWS(t)
	cfg := `{"packs": ["copilot", "bedrock", "wire-bridge"], ` +
		`"providers": {"bedrock": {"region": "eu-west-1", "models": {"sol": {"id": "us.openai.gpt-6.1-sol", "vendor": "openai"}}}}, ` +
		`"env_sources": [{"AWS_CONTAINER_CREDENTIALS_FULL_URI": "` + deadLoopbackURL(t) + `"}]}`
	l := runServiceLaunchAs(t, cfg, []string{"-p", "bedrock-bridge"}, "copilot", "", nil, nil)
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	if got := l.report.Env["COPILOT_MODEL"]; got != "us.openai.gpt-6.1-sol" {
		t.Errorf("COPILOT_MODEL = %q, want the list's one entry, bare", got)
	}
	if got, set := l.report.Env["COPILOT_PROVIDERS_CONFIG"]; set {
		t.Errorf("COPILOT_PROVIDERS_CONFIG = %q at the host, which writes no agent file", got)
	}
	assertServiceGone(t, l)
}

// A FILE-CARRIED VIA IS NOT SERVED AT `yolo host --` (HS-D31): pi reads its bedrock-bridge route from
// its own ~/.pi/agent/models.json, and oh-omp its plain-bedrock route (the carrier, WG-I44) from
// ~/.oh-omp/agent/models.yml, neither of which a host launch renders per launch, so the launch
// starts no bridge for either, the agent keeps its own client, and the "Not set at this notch"
// block says why and where the profile is served. A carrier ViaServedAt clears is never named by
// it, so the oh-omp case also fails if the launch's own line is dropped.
func TestHostPiOnABedrockBridgeProfileStartsNoBridgeAndSaysItIsFileCarried(t *testing.T) {
	for _, tc := range []struct {
		cfg, profile, agent string
		want                []string
	}{
		{`{"packs": ["pi", "bedrock", "wire-bridge"], "providers": {"bedrock": {"region": "eu-west-1"}}}`,
			"bedrock-bridge", "pi", []string{`profile "bedrock-bridge"'s via — pi reads its route from ~/.pi/agent/models.json`,
				"`yolo -p bedrock-bridge -- pi`"}},
		{`{"packs": ["claude", "omp"], "providers": {"bedrock": {"region": "eu-west-1"}}}`,
			"bedrock", "oh-omp", []string{`profile "bedrock"'s carrier "wire-bridge" — oh-omp reads its route from ~/.oh-omp/agent/models.yml`,
				"`yolo -p bedrock -- oh-omp`"}},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			scrubAWS(t)
			l := runServiceLaunchAs(t, tc.cfg, []string{"-p", tc.profile}, tc.agent, "", nil, nil)
			if l.rc != 0 || len(l.started) != 0 || !l.execed {
				t.Fatalf("rc = %d, started %d, exec'd %v; want an exec and no bridge\n%s",
					l.rc, len(l.started), l.execed, l.errs)
			}
			for _, want := range append(tc.want, "`yolo host --` renders no per-launch file",
				`"wire-bridge" service is not started for it`, "Not set at this notch") {
				if !strings.Contains(l.errs, want) {
					t.Errorf("the launch must say %q:\n%s", want, l.errs)
				}
			}
			if strings.Count(l.errs, `profile "`+tc.profile+`"'s `) != 1 {
				t.Errorf("the launch must name profile %q's cleared route once, in its own line:\n%s", tc.profile, l.errs)
			}
		})
	}
}

// THE AWS DOORWAY OPENS FOR THE BRIDGE THAT CARRIES COPILOT, AND ITS POINTER IS THE BRIDGE'S ALONE
// (HS-D32, narrowing HS-D23): copilot on `-p bedrock-bridge` (the profile's via) and on plain
// `-p bedrock` (the carrier, WG-I44) with aws-auth enabled. The launch opens aws-auth's doorway,
// which HS-D23 kept closed for an agent with no Bedrock client, because the bridge signs copilot's
// requests with it; the bridge's input carries the doorway's pointer and its caller token,
// copilot's environment carries neither, and the launch says so. The profile line counts the
// pointer the bridge alone is handed as delivered (profileLines' Reaches), so it warns of no
// missing credential: on `-p bedrock`, the carrier, dropping that clause prints one.
func TestHostOpensTheAWSDoorwayForTheBridgeCarryingCopilot(t *testing.T) {
	for _, profile := range []string{"bedrock-bridge", "bedrock"} {
		t.Run(profile, func(t *testing.T) { hostOpensTheAWSDoorwayForTheBridgeCarryingCopilot(t, profile) })
	}
}

func hostOpensTheAWSDoorwayForTheBridgeCarryingCopilot(t *testing.T, profile string) {
	scrubAWS(t)
	cfg := `{"packs": ["copilot", "bedrock", "wire-bridge"], ` +
		`"providers": {"bedrock": {"region": "eu-west-1"}}, ` +
		`"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "` + doorwayProfile +
		`", "unnarrowed": true}}}}`
	inputs := map[string]map[string]string{}
	l := runDoorwayLaunchAfter(t, cfg, nil, []string{"-p", profile}, "copilot", func() {
		inner := startLaunchService
		startLaunchService = func(p *launchservice.Plan, env map[string]string) (*launchservice.Running, error) {
			inputs[p.Service] = env
			return inner(p, env)
		}
		t.Cleanup(func() { startLaunchService = inner })
	})
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	var door *launchservice.Plan
	for _, r := range l.started {
		if r.Plan.Service == "aws-auth" {
			door = r.Plan
		}
	}
	if door == nil || inputs["wire-bridge"] == nil {
		t.Fatalf("started %v; want the aws-auth doorway and the wire bridge\n%s", l.started, l.errs)
	}
	in := inputs["wire-bridge"]
	if got, want := in["AWS_CONTAINER_CREDENTIALS_FULL_URI"], "http://"+door.Addresses()[0]+"/credentials"; got != want {
		t.Errorf("the bridge's input carries the pointer %q, want the doorway's %q", got, want)
	}
	if in["AWS_CONTAINER_AUTHORIZATION_TOKEN"] != door.Token {
		t.Errorf("the bridge's input does not carry the doorway's caller token")
	}
	for _, name := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"} {
		if v := l.report.Env[name]; v != "" {
			t.Errorf("copilot was handed %s=%q, a credential it has no client to use", name, v)
		}
	}
	for _, want := range []string{`opened the "aws-auth" doorway`,
		`AWS_CONTAINER_AUTHORIZATION_TOKEN, AWS_CONTAINER_CREDENTIALS_FULL_URI go to the "wire-bridge" service alone`} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, l.errs)
		}
	}
	if strings.Contains(l.errs, "delivers copilot no credential") {
		t.Errorf("the launch warns that copilot gets no credential, though the bridge carrying it is "+
			"handed the doorway's pointer:\n%s", l.errs)
	}
}

// A VIA THAT RE-POINTS NOTHING STARTS NOTHING (HS-D33): agy on bedrock-bridge has
// a via URL, but its derive reads none and its environment names no address of the bridge, so the
// bridge would carry none of its requests. `yolo host -- agy` starts no host process outside every
// sandbox for it, execs agy, and says the via has no effect on agy, where it used to start the
// bridge on three addresses nothing pointed at. Counting every agent ViaFor names
// (packload.ViaRoutedServices) fails this.
func TestHostAgyOnABedrockBridgeProfileStartsNoBridge(t *testing.T) {
	scrubAWS(t)
	l := runServiceLaunchAs(t, `{"packs": ["agy", "bedrock", "wire-bridge"], "providers": {"bedrock": {"region": "eu-west-1"}}}`,
		[]string{"-p", "bedrock-bridge"}, "agy", "", nil, nil)
	if l.rc != 0 || len(l.started) != 0 || !l.execed {
		t.Fatalf("rc = %d, started %d, exec'd %v; want an exec and no bridge\n%s", l.rc, len(l.started), l.execed, l.errs)
	}
	for _, want := range []string{`profile "bedrock-bridge"'s via — agy's config does not point it at the "wire-bridge" service`,
		"the via has no effect on agy"} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, l.errs)
		}
	}
	for _, bad := range []string{`started the "wire-bridge" service`, `through pack "wire-bridge"'s via route`} {
		if strings.Contains(l.errs, bad) {
			t.Errorf("the launch must not say %q:\n%s", bad, l.errs)
		}
	}
}

// `yolo host env` NAMES THE LAUNCH THAT SERVES A VIA OR CARRIER (HS-D33, OQ-HS3): copilot on
// `-p bedrock-bridge` (its via) and on plain `-p bedrock` (the carrier) is carried by the bridge at
// `yolo host --`, which host env cannot start, running no process. Its "Not set at this notch" line
// says so and names the launch that works, `yolo host -p <profile> -- copilot`, or `yolo host --
// copilot` when the user's `profile` key selected it, in place of the notch's line, which named no
// command. The env script is still written and starts no service.
func TestHostEnvNamesTheLaunchThatServesAViaOrCarrier(t *testing.T) {
	for _, tc := range []struct {
		name, profile, member string
		args                  []string
		spell                 string
	}{
		{"via", "bedrock-bridge", "", []string{"-p", "bedrock-bridge"}, "`yolo host -p bedrock-bridge -- copilot`"},
		{"carrier", "bedrock", "", []string{"-p", "bedrock"}, "`yolo host -p bedrock -- copilot`"},
		{"profile key", "bedrock-bridge", `, "profile": {"copilot": "bedrock-bridge"}`, nil, "`yolo host -- copilot`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scrubAWS(t)
			hostGateHome(t, `{"packs": ["copilot", "bedrock", "wire-bridge"], "providers": {"bedrock": {"region": "eu-west-1"}}`+
				tc.member+`}`, nil)
			origStart := startLaunchService
			startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
				t.Fatal("yolo host env started a service")
				return nil, nil
			}
			t.Cleanup(func() { startLaunchService = origStart })
			var out, errw bytes.Buffer
			rc := hostMain(append([]string{"env", "--agent", "copilot"}, tc.args...), &out, &errw, false, nil)
			if rc != 0 {
				t.Fatalf("rc = %d\n%s", rc, errw.String())
			}
			route := `profile "` + tc.profile + `"'s via`
			if tc.profile == "bedrock" {
				route = `profile "bedrock"'s carrier "wire-bridge"`
			}
			for _, want := range []string{route + ` — the "wire-bridge" service carries copilot's route`,
				"this command runs no process for it to live beside", tc.spell + " starts it for that launch"} {
				if !strings.Contains(errw.String(), want) {
					t.Errorf("host env must say %q:\n%s", want, errw.String())
				}
			}
			if strings.Contains(errw.String(), `profile "`+tc.profile+`"'s via — its service does not run here`) {
				t.Errorf("host env still gives the notch's line, which names no command:\n%s", errw.String())
			}
		})
	}
}
