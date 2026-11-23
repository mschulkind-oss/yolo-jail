package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

// The real Run emits the boot wire; execution stops at its keeper. The consumer
// is the shipped Claude surface loop, not a direct Lua-context test.
func TestRunManagedMCPRouteReachesProductionBootConsumer(t *testing.T) {
	for _, tc := range []struct {
		name, backend  string
		mac, reachable bool
	}{
		{"Linux bridge", "podman", false, true},
		{"Apple Container unreachable synthetic witness", "container", true, false},
		{"podman Mac unreachable synthetic witness", "podman", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			integrationRoot := managedSignInFixture(t, t.TempDir(), false)
			settings := map[string]any{"packs": []any{"claude", map[string]any{"source": integrationRoot, "name": "fixture-mcp"}}, "mcp_servers": map[string]any{"configured-alias": map[string]any{"url": "https://mcp.fixture.invalid/mcp", "sign_in": "once", "headers": map[string]any{"aUtHoRiZaTiOn": "stale", "X-Team": "t1"}}}}
			encoded, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			writeUserConfigJSON(t, home, string(encoded))
			workspace := t.TempDir()
			var out, errs bytes.Buffer
			o := dispatchOptions(t, workspace, tc.backend, &out, &errs, nil)
			o.IsMacOS = tc.mac
			o.IsLinux = !tc.mac
			if tc.mac {
				o.setupOS = "darwin"
			}
			o.AcceptConfigChanges = true
			o.NeverAttach = true
			repo, _ := o.RepoRoot()
			o.PathExists = func(path string) bool { return path == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				if len(argv) == 2 && argv[1] == "--version" && tc.backend == "container" {
					return ExecResult{Ran: true, RC: 0, Stdout: "container CLI version 1.1.0 (build: synthetic)"}
				}
				if tc.backend == "container" && len(argv) == 3 && argv[1] == "system" && argv[2] == "status" {
					return ExecResult{Ran: true, RC: 0, Stdout: "running"}
				}
				return ExecResult{Ran: true, RC: 0}
			}
			o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
			captures := 0
			o.CapturesDir = func() string { return "" }
			o.AutoCapture = func([]string, string, io.Writer, bool) { captures++ }
			var plan *keeperPlan
			previous := defaultKeeperSpawner
			t.Cleanup(func() { defaultKeeperSpawner = previous })
			defaultKeeperSpawner = func(_ *Options, path string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
				var err error
				plan, err = readKeeperPlan(path)
				if err != nil {
					return nil, err
				}
				return nil, errors.New("synthetic fixture stops before runtime creation")
			}
			Run(*o)
			if plan == nil || captures != 0 {
				t.Fatalf("did not reach frozen keeper safely: %s %s", out.String(), errs.String())
			}
			channel, err := os.ReadFile(filepath.Join(runtimeHome(workspace, tc.backend), "yolo-user-env.sh"))
			if err != nil {
				t.Fatal(err)
			}
			launched := overChannel(t, envPairs(plan.RunCmd), channel)
			vars := map[string]string{}
			for _, name := range []string{mcpsignin.WireEnv, entrypoint.MCPServersWireEnv} {
				vars[name] = launched[name]
			}
			// These are witness results, not a native backend reachability claim.
			// The Mac fixtures intentionally withhold the header route.
			if tc.reachable {
				vars["YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT"] = "/run/yolo/services/synthetic.endpoint"
			}
			var renderErrs bytes.Buffer
			e := &entrypoint.Env{Home: t.TempDir(), Workspace: workspace, Vars: vars, Stderr: &renderErrs}
			if err := entrypoint.ConfigurePackByName(e, "claude"); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(e.Home, ".claude.json"))
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal(body, &config); err != nil {
				t.Fatal(err)
			}
			remote := config["mcpServers"].(map[string]any)["configured-alias"].(map[string]any)
			if tc.reachable {
				if remote["headersHelper"] != "yolo mcp-auth header configured-alias --format json" || remote["oauth"] != nil {
					t.Fatalf("production boot did not consume Run facts: %#v; %s", remote, renderErrs.String())
				}
			} else if remote["headersHelper"] != nil || !strings.Contains(renderErrs.String(), "broker unreachable") {
				t.Fatalf("synthetic unreachable witness lost plain route/reason: %#v; %s", remote, renderErrs.String())
			}
		})
	}
}
