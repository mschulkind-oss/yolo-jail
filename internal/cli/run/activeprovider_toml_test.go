package run

// activeprovider_toml_test.go pins the one selection namespace at a launch whose user config is
// TOML and selects through `active_provider` (config.ConfigActiveSelection, admitted at launch by
// config.validateActiveProvider; docs/reference/config/user-config.md#which-provider-each-agent-runs-on).

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

// THE CALL-SITE PIN: real Runs whose user config is TOML and selects through `active_provider`. A
// provider no profile names is selectable by its own name (OQ-RC3), the launch record names the
// key entry it came from (launchcontext.go), and "native" (OQ-RC20) leaves the agent with no
// profile. Fails if the launch records the legacy `profile` key's source, or if the channel
// resolves the selection without the implied profiles (checkProfileDeclarations then refuses the
// provider's name as undeclared).
func TestALaunchSelectsThroughActiveProvider(t *testing.T) {
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
	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.toml")
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

	if got := launch("[active_provider]\ndefault = \"acmecloud\"\n"); !strings.Contains(got,
		"  profile: acme=acmecloud (config active_provider.default)") {
		t.Errorf("a provider selected by its own name was not recorded from active_provider.default:\n%s", got)
	}
	if got := launch("[active_provider]\ndefault = \"acmecloud\"\nacme = \"native\"\n"); !strings.Contains(got,
		"  profile: acme=none (config active_provider.acme)") {
		t.Errorf("native did not leave acme with no profile:\n%s", got)
	}
}
