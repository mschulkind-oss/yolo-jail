package cli

// hostbridgeadapter_test.go pins the host notch's answer to an adapter only a jail serves
// (docs/design/credential-sources-separation.md ES-D18; docs/reference/wire-bridge.md, "No
// host-side bridge"). packs/wire-bridge declares its `openai → anthropic` adaptation beside the
// `service` whose in-jail daemon serves it, and no host process serves that address. So with
// wire-bridge listed in `packs`, `yolo host -p cerebras -- claude` must REFUSE, naming why and
// that the profile works in a jail, rather than run claude pointed at http://127.0.0.1:8214.
// Every cell runs `yolo host` through hostMain, as the other host cells do.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bridgeConfig lists wire-bridge explicitly: the one way it reaches the host notch, which runs
// no pack's `needs`.
const bridgeConfig = `{"packs": ["claude", "copilot", "cerebras", "wire-bridge"], ` +
	`"env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`

func TestHostRefusesAProfilePointedAtTheInJailBridge(t *testing.T) {
	rc, env, errs := hostGateRun(t, bridgeConfig, wcShell(nil), []string{"-p", "cerebras"}, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("yolo host -p cerebras -- claude must refuse before the exec, not run claude at the "+
			"bridge's address: rc = %d, ANTHROPIC_BASE_URL = %q\n%s", rc, env["ANTHROPIC_BASE_URL"], errs)
	}
	for _, want := range []string{"refusing to launch", `profile "cerebras"`, "claude",
		"http://127.0.0.1:8214", `"wire-bridge"`, "inside a jail", "No host process serves it",
		"`yolo -p claude=cerebras -- claude`"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

// `yolo host env` composes the same way, so it refuses the same profile, as an error and with
// nothing on stdout for a shell to eval.
func TestHostEnvRefusesAProfilePointedAtTheInJailBridge(t *testing.T) {
	hostGateHome(t, bridgeConfig, wcShell(nil))
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "cerebras"}, &out, &errw, false, nil); rc == 0 {
		t.Fatalf("yolo host env --agent claude -p cerebras must refuse:\n%s", out.String())
	}
	if out.Len() != 0 || !strings.Contains(errw.String(), "inside a jail") {
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
	if strings.Contains(line, "-- claude`") || !strings.Contains(line, "`yolo host -p cerebras -- bash`") {
		t.Errorf("claude cannot run on cerebras at this notch even with wire-bridge listed: %q", line)
	}
	assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
}

// An adapter whose pack runs NO service is one the user serves themselves (a proxy on the host,
// a remote gateway — protocol-resolution.md's other two shapes), so the host still composes it:
// only an adaptation the pack's own service daemon serves is left out.
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
