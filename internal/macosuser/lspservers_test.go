package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// lspservers_test.go pins what `lsp_servers` does on this backend now that yolo installs no
// language server on ANY backend (docs/reference/mcp-configuration.md#oq-lsp1). It replaced
// lspinstall_test.go, which pinned the opposite: that the two install lists
// (YOLO_LSP_NPM_INSTALL / YOLO_LSP_GO_INSTALL, resolved from a three-entry recipe table in
// internal/config) crossed into both the bootstrap env and the session env file.
//
// The key still RENDERS, which is the half that stays. YOLO_LSP_SERVERS is the table the
// in-sandbox derives project (Copilot's lsp-config.json), and Claude's generated plugin is
// rendered host-side from the same config. What is gone is every request to INSTALL: a
// configured `command` must already resolve on PATH, and the user brings the server through
// `mise_tools`, a pack program or an absolute path.

// lspOnlyConfig is a workspace whose only declaration is one LSP server, spelled the way
// config.validateLSPServers accepts it. It names the server the deleted recipe table used to
// install (`python` → pyright), so a surviving install path would have something to do.
func lspOnlyConfig(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(`{"lsp_servers": {"python": {"command": "pyright-langserver",
  "args": ["--stdio"], "fileExtensions": {".py": "python"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("fixture is not an object: %T", v)
	}
	return cfg
}

func lspOnlyPlan(t *testing.T, cfg *jsonx.OrderedMap) RunPlan {
	t.Helper()
	return BuildRunPlan("/Users/Shared/proj", cfg, []string{"claude"}, []string{"claude"},
		"/opt/yolo-jail/bin/yolo", "", "", HostContext{}, jsonx.NewOrderedMap(), nil, nil)
}

// THE HALF THAT STAYS: the table still reaches the bootstrap, whose derives render it.
func TestLSPServersStillReachTheBootstrapAsATable(t *testing.T) {
	plan := lspOnlyPlan(t, lspOnlyConfig(t))

	wire, ok := argvEnvValue(plan.BootstrapArgv, "YOLO_LSP_SERVERS")
	if !ok {
		t.Fatalf("the bootstrap env carries no YOLO_LSP_SERVERS, so no in-sandbox derive can "+
			"render the configured server:\n%v", plan.BootstrapArgv)
	}
	if !strings.Contains(wire, `"pyright-langserver"`) {
		t.Errorf("YOLO_LSP_SERVERS does not carry the configured server's command: %s", wire)
	}
}

// THE HALF THAT WENT: nothing on any argv, and nothing in the env file, asks for an install —
// and a config whose only declaration is an LSP server starts no provisioning stage, because
// that stage would have nothing to do.
func TestLSPServersAskNothingToInstall(t *testing.T) {
	cfg := lspOnlyConfig(t)
	plan := lspOnlyPlan(t, cfg)

	for name, argv := range map[string][]string{
		"bootstrap":    plan.BootstrapArgv,
		"provisioning": plan.ProvisionArgv,
		"launch":       plan.LaunchArgv,
	} {
		for _, word := range argv {
			if strings.Contains(word, "YOLO_LSP_NPM_INSTALL") ||
				strings.Contains(word, "YOLO_LSP_GO_INSTALL") {
				t.Errorf("the %s argv still carries an LSP install list (%s); yolo installs no "+
					"language server on any backend", name, word)
			}
		}
	}
	if strings.Contains(plan.EnvFileContent, "YOLO_LSP_") {
		t.Errorf("a workspace declaring only an LSP server composed LSP variables into the "+
			"session env file:\n%s", plan.EnvFileContent)
	}

	if ProvisionNeeded(cfg) {
		t.Errorf("ProvisionNeeded is true for a config whose only declaration is lsp_servers; " +
			"the stage it starts would run a privileged step that installs nothing")
	}
	if len(plan.ProvisionArgv) != 0 {
		t.Errorf("the plan starts a provisioning stage for an lsp_servers-only config:\n%v",
			plan.ProvisionArgv)
	}
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("an lsp_servers-only plan is not viable: %v", problems)
	}
}
