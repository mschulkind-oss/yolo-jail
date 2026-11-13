package run

// activeprovider_test.go pins the one selection namespace at a launch (config.ConfigActiveSelection,
// config.LoadSelectableProfiles; docs/reference/config/user-config.md#which-provider-each-agent-runs-on).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE CALL-SITE PIN: real Runs that select a provider no profile names, by its own name (OQ-RC3),
// and then leave the agent to its own login with "native" (OQ-RC20). Fails if the channel
// resolves the selection without the implied profiles (profilechannel.go: checkProfileDeclarations
// then refuses the provider's name as undeclared), or if the fold stops reading "native" as no
// profile. The config is the legacy JSONC `profile` key; the TOML `active_provider` form is
// TestALaunchSelectsThroughActiveProvider (activeprovider_toml_test.go).
func TestALaunchSelectsAProviderByNameAndNative(t *testing.T) {
	home := packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"local","contributes":[` +
		`{"kind":"program","bin":"acme","via":"npm","package":"@acme/acme","protocols":["openai-responses"]},` +
		`{"kind":"autonomy","autonomous":{"launch":[{"bin":"acme","flags":["--static"]}]}},` +
		`{"kind":"provider","name":"acmecloud","platform":"acmecloud"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	ws := floortest.ResolvedTemp(t)
	launch := func(body string) string {
		if err := os.WriteFile(userCfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.Args = []string{"acme"}
		o.Invocation = []string{"--", "acme"}
		o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
			return 0
		}
		if rc := Run(*o); rc != 0 {
			t.Fatalf("Run() = %d with\n%s\nstdout:\n%s\nstderr:\n%s", rc, body, stdout.String(), stderr.String())
		}
		log := readLaunchLog(t, ws)
		return log[strings.LastIndex(log, launchRunPrefix):]
	}

	if got := launch(`{"profile": "acmecloud"}`); !strings.Contains(got,
		"  profile: acme=acmecloud (config profile)") {
		t.Errorf("a provider selected by its own name was not selected:\n%s", got)
	}
	if got := launch(`{"profile": {"*": "acmecloud", "acme": "native"}}`); !strings.Contains(got,
		"  profile: acme=none (config profile.acme)") {
		t.Errorf("native did not leave acme with no profile:\n%s", got)
	}
}

// THE SELECTION CLOSURE reads the one namespace too (launchSelection): a `<provider>/<variant>`
// whose variant names a via pulls that service pack into the launch, as a profile's via does.
// Fails if the closure's declarations are the declared profiles alone: the variant then resolves
// to nothing, no via is active, and the service the channel will route codex through is never
// staged.
func TestTheSelectionClosureAddsAVariantsVia(t *testing.T) {
	o := goldenOptions(t.TempDir(), packHome(t))
	o.UseProfiles = map[string]string{"codex": "bedrock-eu/bridged"}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("active_provider", jsonx.NewOrderedMap())
	providers := jsonx.NewOrderedMap()
	eu := jsonx.NewOrderedMap()
	eu.Set("platform", "aws-bedrock")
	variants := jsonx.NewOrderedMap()
	bridged := jsonx.NewOrderedMap()
	bridged.Set("via", "wire-bridge")
	variants.Set("bridged", bridged)
	eu.Set("variants", variants)
	providers.Set("bedrock-eu", eu)
	cfg.Set("providers", providers)

	sel := o.launchSelectionFor(cfg)
	sel.Embedded = func(name string) (*packload.Pack, bool) { return officialPack(t, name), true }
	added, causes, err := sel.Close([]*packload.Pack{officialPack(t, "codex"), officialPack(t, "codex-auth")})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range added {
		if p.Name == "wire-bridge" {
			return
		}
	}
	t.Errorf("codex on bedrock-eu/bridged did not pull in wire-bridge: added %d pack(s), causes %v",
		len(added), causes)
}
