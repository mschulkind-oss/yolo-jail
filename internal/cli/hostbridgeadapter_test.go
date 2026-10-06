package cli

// hostbridgeadapter_test.go pins the host notch's answer to an adapter a pack's own service serves
// (docs/design/host-notch-services.md; ES-D18 as HS-D5 narrows it). A host launch starts the
// service's host half for its command (hostservices_test.go has that half); what is pinned here
// is what stays as it was: an agent that speaks the provider's own wire is not bridged, an
// adapter no pack service serves still composes, and a service this launch cannot start (a
// fetched pack's, since a local pack's runs: HS-D27) refuses, naming why and the container jail
// where the profile works.
// Every cell runs `yolo host` through hostMain, as the other host cells do.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bridgeConfig lists wire-bridge explicitly, beside claude, whose `needs` would join it anyway
// (notch-convergence item 6): the refusal then says it is in `packs`.
const bridgeConfig = `{"packs": ["claude", "copilot", "cerebras", "wire-bridge"], ` +
	`"env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`

// `yolo host env` owns no process, so it refuses a profile the bridge serves (OQ-HS3), as an error
// naming the launch that works and with nothing on stdout for a shell to eval.
func TestHostEnvRefusesAProfilePointedAtTheBridge(t *testing.T) {
	hostGateHome(t, bridgeConfig, wcShell(nil))
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "cerebras"}, &out, &errw, false, nil); rc == 0 {
		t.Fatalf("yolo host env --agent claude -p cerebras must refuse:\n%s", out.String())
	}
	if out.Len() != 0 || !strings.Contains(errw.String(), "`yolo host -p cerebras -- claude`") {
		t.Errorf("stdout = %q, stderr = %q", out.String(), errw.String())
	}
}

// The adapter's address is never composed at the host, so an agent that speaks the provider's
// own wire as well resolves to it directly, as it does with wire-bridge unlisted: copilot on
// cerebras runs against cerebras's own openai endpoint, never against the dead bridge address.
func TestHostComposesNoBridgeAddressForAnyAgent(t *testing.T) {
	env, errs := hostGateLaunchWith(t, bridgeConfig, wcShell(nil), []string{"-p", "cerebras"}, "copilot")
	for k, v := range env {
		if strings.Contains(v, "127.0.0.1:8214") {
			t.Errorf("copilot on cerebras was handed %s=%q, the bridge's address no host process serves\n%s", k, v, errs)
		}
	}
	if got := env["COPILOT_PROVIDER_BASE_URL"]; got != "https://api.cerebras.ai/v1" {
		t.Errorf("copilot speaks openai, so it runs on cerebras's own endpoint: COPILOT_PROVIDER_BASE_URL = %q\n%s", got, errs)
	}
}

// The ES-D10 remedy asks the same gate, so with wire-bridge listed it still never names a claude
// launch on cerebras, and every command it does name runs.
func TestHostRemedyNeverNamesTheBridgedProfileForClaude(t *testing.T) {
	_, errs := hostGateLaunchWith(t, bridgeConfig, wcShell(nil), nil, "claude")
	line := scopeLine(t, errs, "CEREBRAS_API_KEY")
	if strings.Contains(line, "-- claude`") || !strings.Contains(line, "`yolo host --with-credentials cerebras -- bash`") {
		t.Errorf("claude cannot run on cerebras at this notch even with wire-bridge listed: %q", line)
	}
	assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
}

// An adapter whose pack runs NO service is one the user serves themselves (a proxy on the host,
// a remote gateway — protocol-resolution.md's other two shapes), so the host still composes it:
// only an adaptation the pack's own service daemon serves is left out.
//
// The adapter is the conventional local pack's. wire-bridge, which claude's `needs` joins,
// declares the same openai → anthropic pair; a duplicated sole-owned claim is held by the LATER
// pack in the one precedence order (OQ-NC4: config order, then the closure's additions, then the
// local pack last), so the local pack's address wins (docs/plans/notch-convergence.md NC-D59).
func TestHostStillComposesAnAdapterNoPackServiceServes(t *testing.T) {
	home := hostGateHome(t, `{"packs": ["claude", "cerebras"], "env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`, wcShell(nil))
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name":"local","contributes":[`+
		`{"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"http://127.0.0.1:9999"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, env, errs := runRemedy(t, []string{"-p", "cerebras", "--", "claude"})
	if rc != 0 || env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:9999" {
		t.Errorf("a user-served adapter still pairs claude with cerebras at the host: rc = %d, "+
			"ANTHROPIC_BASE_URL = %q\n%s", rc, env["ANTHROPIC_BASE_URL"], errs)
	}
}

// THE LAUNCH-CHOSEN ADDRESS OVERRIDES THE USER'S `adapters` OVERRIDE (OQ-HS4): at the host the
// bridge's port is one this launch picked, listed or joined, and neither the manifest's 8214 nor
// the user's 9214.
func TestHostLaunchChosenAddressOverridesTheAdapterOverride(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	for _, packs := range []string{`"claude", "cerebras", "wire-bridge"`, `"claude", "cerebras"`} {
		cfg := `{"packs": [` + packs + `], "env_sources": [{"CEREBRAS_API_KEY": "tok-c"}], ` +
			`"adapters": {"openai->anthropic": {"address": "http://127.0.0.1:9214"}}, ` +
			`"providers": {"cerebras": {"endpoints": {"openai": {"base_url": "` + upstream.URL + `/v1"}}}}}`
		l := runServiceLaunch(t, cfg, []string{"-p", "cerebras"}, "", nil)
		base := l.report.Env["ANTHROPIC_BASE_URL"]
		if l.rc != 0 || base == "" || strings.Contains(base, ":9214") || strings.Contains(base, ":8214") {
			t.Errorf("packs [%s]: rc = %d, ANTHROPIC_BASE_URL = %q; want a port this launch picked\n%s",
				packs, l.rc, base, l.errs)
		}
		if l.report.WithToken != 200 {
			t.Errorf("packs [%s]: the bridge at the picked port did not serve: %d", packs, l.report.WithToken)
		}
	}
}

// fetchedBridgeSource is a FETCHED pack (git+file://, so a real fetch with no network) named
// like the bridge, declaring the bridge's service with a host half beside adapter: a pack yolo
// does not ship and no `packs` line selects by path, so its host half never runs (OQ-HS4; a
// local pack's does since HS-D27). The launch's own pack refresh fetches it into the test HOME's
// store, YOLO_PACK_ROOT cleared so a jail's staged tree cannot answer for it.
func fetchedBridgeSource(t *testing.T, adapter string) string {
	t.Helper()
	return fetchedBridgeSourceWith(t, adapter,
		`{"kind": "service", "name": "wire-bridge", "host_daemon": {"cmd": ["yolo", "internal", "daemon", "wire-bridge"]}}`)
}

// fetchedBridgeSourceWith is fetchedBridgeSource with the service contribution given.
func fetchedBridgeSourceWith(t *testing.T, adapter, service string) string {
	t.Helper()
	t.Setenv("YOLO_PACK_ROOT", "")
	repo := gitPackRepoWith(t, map[string]string{"pack.json": `{"contributes": [` + adapter + `,
  ` + service + `]}`})
	return "git+file://" + repo + "?ref=main"
}

// fetchedBridgeConfig lists claude and cerebras with fetchedBridgeSource's pack as wire-bridge.
func fetchedBridgeConfig(t *testing.T, extra string) string {
	t.Helper()
	src := fetchedBridgeSource(t,
		`{"kind": "adapter", "adapts": {"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:8214"}`)
	return `{"packs": ["claude", "cerebras", {"source": "` + src + `", "name": "wire-bridge"}]` + extra + `}`
}

// A service this launch cannot start refuses naming the address it would have used, the user's
// `adapters` override included, since nothing here picks a port for it.
func TestHostUnservedRefusalNamesTheAdapterOverride(t *testing.T) {
	cfg := fetchedBridgeConfig(t, `, "env_sources": [{"CEREBRAS_API_KEY": "tok-c"}], `+
		`"adapters": {"openai->anthropic": {"address": "http://127.0.0.1:9214"}}`)
	rc, _, errs := hostGateRun(t, cfg, wcShell(nil), []string{"-p", "cerebras"}, "claude")
	if rc == 0 || !strings.Contains(errs, "http://127.0.0.1:9214") || strings.Contains(errs, ":8214") {
		t.Errorf("the refusal must name the override address, not the default: rc = %d\n%s", rc, errs)
	}
}

// localBridgeConfig lists claude and cerebras with a LOCAL pack (a file:// entry) named like the
// bridge, declaring the bridge's openai → anthropic adapter beside the service contribution given.
func localBridgeConfig(t *testing.T, service string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wire-bridge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name": "wire-bridge", "contributes": [`+
		`{"kind": "adapter", "adapts": {"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:8214"}, `+
		service+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return `{"packs": ["claude", "cerebras", {"source": "file://` + dir + `", "name": "wire-bridge"}], ` +
		`"env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`
}

// The jail launch the refusal names is a CONTAINER backend's when the macos-user guest declines
// the service's jail daemon: here the fetched pack's service publishes an endpoint file, a
// container path the sandbox has no counterpart of (JD-9 (b)), so macos-user refuses the same
// profile, and the refusal names the dial that picks a container backend for one launch rather
// than calling the profile one that works "in a jail".
func TestHostUnservedRefusalNamesOnlyAContainerJail(t *testing.T) {
	src := fetchedBridgeSourceWith(t,
		`{"kind": "adapter", "adapts": {"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:8214"}`,
		`{"kind": "service", "name": "wire-bridge", "endpoint": "wire-bridge.endpoint", `+
			`"jail_daemon": {"cmd": ["yolo-jaild", "wire-bridge"]}, `+
			`"host_daemon": {"cmd": ["yolo", "internal", "daemon", "wire-bridge"]}}`)
	cfg := `{"packs": ["claude", "cerebras", {"source": "` + src + `", "name": "wire-bridge"}], ` +
		`"env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`
	_, _, errs := hostGateRun(t, cfg, wcShell(nil), []string{"-p", "cerebras"}, "claude")
	for _, want := range []string{
		"which this launch cannot start: its pack was fetched",
		"select a local checkout of the pack by its file:// path",
		"The profile works in a container jail (podman or Apple Container), where that service's " +
			"jail daemon runs: `yolo -p claude=cerebras -- claude`",
		"A macos-user launch runs a pack service only through its host half",
		"`YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container`",
		`though "wire-bridge" is in ` + "`packs`",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "inside a jail") {
		t.Errorf("a macos-user jail runs no bridge either, so \"inside a jail\" is false of it:\n%s", errs)
	}
}

// A SERVICE THE MACOS-USER GUEST RUNS IS ONE THE REFUSAL SENDS THERE (JD-9): a local pack's service
// with a jail daemon, no host half and no endpoint file runs confined in the sandbox, which serves
// the pairing, so the refusal names macos-user as a launch where the profile works instead of
// saying it refuses the profile too.
func TestHostUnservedRefusalNamesMacosUserWhenItsGuestRunsTheService(t *testing.T) {
	cfg := localBridgeConfig(t, `{"kind": "service", "name": "wire-bridge", `+
		`"jail_daemon": {"cmd": ["yolo-jaild", "wire-bridge"]}}`)
	rc, _, errs := hostGateRun(t, cfg, wcShell(nil), []string{"-p", "cerebras"}, "claude")
	if rc == 0 {
		t.Fatalf("a service with no host half cannot serve `yolo host`; the launch must refuse:\n%s", errs)
	}
	for _, want := range []string{
		"which this launch cannot start: it declares no host half",
		"The profile works in a container jail (podman or Apple Container), where that service's " +
			"jail daemon runs: `yolo -p claude=cerebras -- claude`",
		"A macos-user launch runs that service's jail daemon in its sandbox, so the profile works " +
			"there too (`YOLO_RUNTIME=macos-user` for one launch)",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "refuses this profile too") {
		t.Errorf("the macos-user guest runs this service, so it does not refuse the profile:\n%s", errs)
	}
}

// A SERVICE NO JAIL RUNS IS NOT SENT TO ONE: the held declaration has no jail daemon (a fetched
// pack's host half alone, which the launch refuses), and packload.HeldServices makes it the name's
// holder, so neither a container nor the macos-user guest serves the address as declared. The
// refusal says so and keeps the next step that is true, a profile the agent speaks to directly.
func TestHostUnservedRefusalNamesNoJailForAServiceWithNoJailDaemon(t *testing.T) {
	cfg := fetchedBridgeConfig(t, `, "env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]`)
	_, _, errs := hostGateRun(t, cfg, wcShell(nil), []string{"-p", "cerebras"}, "claude")
	for _, want := range []string{
		"which this launch cannot start: its pack was fetched",
		"No jail runs it either: the \"wire-bridge\" service pack \"wire-bridge\" declares has no " +
			"jail daemon (`jail_daemon`)",
		"At the host, choose a profile whose provider claude speaks to directly",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
	for _, not := range []string{"The profile works in a container jail", "A macos-user launch"} {
		if strings.Contains(errs, not) {
			t.Errorf("no jail runs a service with no jail daemon, so the refusal must not say %q:\n%s", not, errs)
		}
	}
}

// A PACK NOTHING SELECTS IS JUDGED BY ITS SHIPPED DECLARATION: the selection holds no service of
// that name, so asking it whether a jail runs the service would answer no for the bridge, whose
// shipped declaration has a jail daemon a container runs. The refusal reads the embedded set for
// an unselected pack, as its `packs` remedy does.
func TestHostUnservedRefusalJudgesAnUnselectedPackByItsShippedDeclaration(t *testing.T) {
	e := &packload.UnservedAdapterError{Agent: "claude", Provider: "cerebras", Adaptation: packload.Adaptation{
		Pack: "wire-bridge", From: "openai", To: "anthropic", Address: "http://127.0.0.1:8214",
		Service: "wire-bridge"}}
	errs := unservedAdapterRefusal(e, nil, "cerebras", hostPackSet{}, "").Error()
	for _, want := range []string{
		"because nothing selects pack \"wire-bridge\"",
		"The profile works in a container jail (podman or Apple Container), where that service's " +
			"jail daemon runs: `yolo -p claude=cerebras -- claude`",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "No jail runs it either") {
		t.Errorf("the shipped bridge declares a jail daemon, so a jail runs it:\n%s", errs)
	}
}
