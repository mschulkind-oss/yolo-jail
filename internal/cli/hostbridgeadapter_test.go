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

// bridgeConfig lists wire-bridge explicitly, beside claude, whose `needs` would join it anyway
// (notch-convergence item 6): the refusal then says it is in `packs`.
const bridgeConfig = `{"packs": ["claude", "copilot", "cerebras", "wire-bridge"], ` +
	`"env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`

func TestHostRefusesAProfilePointedAtTheInJailBridge(t *testing.T) {
	rc, env, errs := hostGateRun(t, bridgeConfig, wcShell(nil), []string{"-p", "cerebras"}, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("yolo host -p cerebras -- claude must refuse before the exec, not run claude at the "+
			"bridge's address: rc = %d, ANTHROPIC_BASE_URL = %q\n%s", rc, env["ANTHROPIC_BASE_URL"], errs)
	}
	for _, want := range []string{"refusing to launch", `profile "cerebras"`, "claude",
		"http://127.0.0.1:8214", `"wire-bridge"`, "container jail", "No host process serves it",
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
	if out.Len() != 0 || !strings.Contains(errw.String(), "container jail") {
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
// The user's pack is LISTED in `packs`. wire-bridge, which claude's `needs` joins, declares the
// same openai → anthropic pair, and a pair is sole-owned: where nothing refuses the duplicate,
// Adaptations keeps the first in the one precedence order (OQ-NC4: config order, then the
// closure's additions, then the conventional local pack). A configured pack precedes every
// addition, so its pair is the one kept; the conventional local pack, last since OQ-NC4, would
// yield the pair to wire-bridge (docs/plans/notch-convergence.md NC-D57).

// yield the pair to wire-bridge (docs/plans/notch-convergence.md NC-D59).

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

// With wire-bridge NOT listed, it joins through claude's `needs` (the one selection function,
// notch-convergence item 6), and the pairing refuses the same way, once, WORDING THE PACK AS JOINED
// (HS-D1): "though "wire-bridge" is in `packs`" would send the user to a config line that does not
// exist, and the ordinary pairing refusal's remedy (outcome 3, "Add it to `packs` and this pairing
// resolves") is false here, since listing it leads only to the refusal above. The chain is the
// same for `-p codex` through the bridge's openai-responses adapter, and for that profile the
// provider's own pack, openai-auth, joins the same way.
func TestHostNeverTellsTheUserToListTheBridge(t *testing.T) {
	for _, tc := range []struct {
		cfg, profile, address string
	}{
		{`{"packs": ["claude", "cerebras"], "env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`, "cerebras",
			"http://127.0.0.1:8214"},
		{`{"packs": ["claude"]}`, "codex", "http://127.0.0.1:8215"},
	} {
		rc, env, errs := hostGateRun(t, tc.cfg, wcShell(nil), []string{"-p", tc.profile}, "claude")
		if rc == 0 || env != nil {
			t.Fatalf("-p %s -- claude must refuse: rc = %d\n%s", tc.profile, rc, errs)
		}
		for _, never := range []string{"Add it to `packs`", "is in `packs`", "not in this launch's provider table"} {
			if strings.Contains(errs, never) {
				t.Errorf("-p %s: the refusal must not say %q:\n%s", tc.profile, never, errs)
			}
		}
		for _, want := range []string{`profile "` + tc.profile + `"`, tc.address, `"wire-bridge"`,
			"No host process serves it",
			`though "wire-bridge" joined this launch (+ wire-bridge (needed by claude))`,
			"`yolo -p claude=" + tc.profile + " -- claude`"} {
			if !strings.Contains(errs, want) {
				t.Errorf("-p %s: the refusal must say %q:\n%s", tc.profile, want, errs)
			}
		}
	}
}

// The refusal names the address the user's `adapters` override moves the adapter to, listed or
// not: the host hands the gate the override, and a refusal naming the manifest's default would
// point the user at the wrong port.
func TestHostUnservedRefusalNamesTheAdapterOverride(t *testing.T) {
	for _, packs := range []string{`"claude", "cerebras", "wire-bridge"`, `"claude", "cerebras"`} {
		cfg := `{"packs": [` + packs + `], "env_sources": [{"CEREBRAS_API_KEY": "tok-c"}], ` +
			`"adapters": {"openai->anthropic": {"address": "http://127.0.0.1:9214"}}}`
		rc, _, errs := hostGateRun(t, cfg, wcShell(nil), []string{"-p", "cerebras"}, "claude")
		if rc == 0 || !strings.Contains(errs, "http://127.0.0.1:9214") || strings.Contains(errs, ":8214") {
			t.Errorf("packs [%s]: the refusal must name the override address, not the default: rc = %d\n%s",
				packs, rc, errs)
		}
	}
}

// The jail launch the refusal names works only on a CONTAINER backend: macos-user starts no jail
// daemons, the bridge included (wire-bridge.md, "No macos-user bridge"), so there claude would
// be pointed at the same dead address. The refusal says so and names the dial that picks a
// container backend for one launch, rather than calling the profile one that works "in a jail".
func TestHostUnservedRefusalNamesOnlyAContainerJail(t *testing.T) {
	_, _, errs := hostGateRun(t, bridgeConfig, wcShell(nil), []string{"-p", "cerebras"}, "claude")
	for _, want := range []string{
		"a daemon yolo runs only in a container jail",
		"The profile works in a container jail (podman or Apple Container), where that service runs: " +
			"`yolo -p claude=cerebras -- claude`",
		"The macos-user backend starts no jail daemons, so the service does not run there either",
		"`YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container`",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "inside a jail") {
		t.Errorf("a macos-user jail runs no bridge either, so \"inside a jail\" is false of it:\n%s", errs)
	}
}
