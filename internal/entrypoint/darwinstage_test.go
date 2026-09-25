package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// darwinstage_test.go covers step 8 of macos-user-provisioning.md half two: the native
// bootstrap generating the script the confined provisioning stage runs.
//
// It drives the REAL boot entry (RunDarwinBootstrap) against a real filesystem, through
// the helper the home-tier tests use, for the reason that file's header gives: a test
// that called GenerateDarwinBootstrapScript directly would stay green when the genStep is
// deleted, and a deleted genStep is a stage that execs a file which does not exist.

// THE CALL-SITE TEST: delete the genStep from RunDarwinBootstrap and this fails.
func TestDarwinBootstrapWritesTheStagesScript(t *testing.T) {
	_, ws := darwinBootstrapHome(t, nil)
	script := DarwinBootstrapScriptPath(filepath.Join(ws, ".yolo", "home"))

	fi, err := os.Stat(script)
	if err != nil {
		t.Fatalf("the native bootstrap generated no bootstrap script: %v\n"+
			"The provisioning stage execs this path, so without it the stage fails on its "+
			"first line and `lsp_servers` install nothing — which is the gap half two "+
			"exists to close.", err)
	}
	if fi.Mode()&0o100 == 0 {
		t.Errorf("the bootstrap script is not executable (%v); the stage execs it directly",
			fi.Mode())
	}
}

// ⚠ AND NOT IN THE SHARED ACCOUNT HOME. The container reaches this script at
// ~/.yolo-bootstrap.sh, which is a BIND of <ws>/.yolo/home/yolo-bootstrap.sh — the home
// path is the bind's appearance. macos-user has one account home shared by every
// workspace, so generating it home-rooted would put one workspace's script where the next
// workspace's launch overwrites it: the cross-workspace write-write race the home split
// exists to end, re-created by half two rather than inherited from it.
func TestTheStagesScriptIsPerWorkspaceNotPerAccount(t *testing.T) {
	home, ws := darwinBootstrapHome(t, nil)
	if _, err := os.Lstat(filepath.Join(home, ".yolo-bootstrap.sh")); err == nil {
		t.Error("the bootstrap script was written into the shared account home at " +
			"~/.yolo-bootstrap.sh; every workspace on this machine shares that path")
	}
	if _, err := os.Stat(DarwinBootstrapScriptPath(filepath.Join(ws, ".yolo", "home"))); err != nil {
		t.Errorf("nothing was written into the workspace sidecar either: %v", err)
	}
}

// NO LSP INSTALL LOOP ON EITHER BACKEND. The loop, the YOLO_LSP_*_INSTALL lists it read and
// the ~/.yolo-installed-lsps sentinel it kept (per-workspace on both backends: a bind in the
// container, a sidecar path here) are deleted with the recipe table
// (docs/reference/mcp-configuration.md#oq-lsp1). This asks both generators, because the
// macos-user script is the stage's own and a darwin-only seam could keep an arm the
// container's script lost.
func TestNeitherBackendsScriptInstallsALanguageServer(t *testing.T) {
	_, ws := darwinBootstrapHome(t, nil)
	darwin, err := os.ReadFile(DarwinBootstrapScriptPath(filepath.Join(ws, ".yolo", "home")))
	if err != nil {
		t.Fatal(err)
	}
	container := BootstrapScript(NewEnv(map[string]string{"HOME": "/home/agent"}))
	for name, body := range map[string]string{"macos-user": string(darwin), "container": container} {
		for _, gone := range []string{"yolo-installed-lsps", "YOLO_LSP_NPM_INSTALL",
			"YOLO_LSP_GO_INSTALL", "SENTINEL=", "go install"} {
			if strings.Contains(body, gone) {
				t.Errorf("the %s bootstrap script still carries %q — an LSP install path "+
					"survived the recipe table's deletion", name, gone)
			}
		}
	}
}

// MCP PRESET PACKAGES follow the WRAPPERS. macos-user does not generate the preset
// wrappers (their bodies are Linux-absolute), so installing the npm packages behind them
// would be a download for an executable this backend never writes — and would make the
// launch's own "mcp_presets are not delivered" warning a half-truth.
func TestNoMCPPresetInstallsWhereThereAreNoWrappers(t *testing.T) {
	vars := map[string]string{
		"HOME":              "/home/agent",
		"YOLO_MCP_PRESETS":  `["chrome-devtools"]`,
		"YOLO_BLOCK_CONFIG": `[]`,
	}
	// The assertion is on the INSTALL LIST, not on the package name appearing anywhere:
	// the script's `case` arm mentions chrome-devtools-mcp unconditionally, so a naive
	// substring test passes on both backends and proves nothing.
	container := NewEnv(vars)
	if want := `YOLO_MCP_NPM="chrome-devtools-mcp"`; !strings.Contains(BootstrapScript(container), want) {
		t.Fatalf("the container stopped installing an enabled preset's npm package (%s); "+
			"this test's premise is that the two backends differ, not that neither installs",
			mcpNpmLine(BootstrapScript(container)))
	}
	darwin := DarwinEnvFrom(vars, "/Users/_yolojail")
	if got := mcpNpmLine(BootstrapScript(darwin)); got != `YOLO_MCP_NPM=""` {
		t.Errorf("macos-user installs the npm package behind an MCP preset whose wrapper it "+
			"never generates; nothing can ever exec it: %s", got)
	}
}

// mcpNpmLine pulls the YOLO_MCP_NPM assignment out of a script for a readable failure.
func mcpNpmLine(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "YOLO_MCP_NPM=") {
			return l
		}
	}
	return "(no YOLO_MCP_NPM= line at all)"
}
