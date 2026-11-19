package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestMCPProducerCarriesExpectedClientNotJustItsDigest(t *testing.T) {
	text := `{"fixture":{"url":"https://mcp.fixture.invalid/mcp","sign_in":"once","oauth":{"client_id":"owner-client"}}}`
	in := onceLaunchInput(t, text, text)
	data, err := mcpShareFromPacks(in, []*packload.Pack{shippedPack(t, "claude")})
	if err != nil {
		t.Fatal(err)
	}
	var share struct {
		Once []struct {
			Expected string `json:"expected_client_id"`
		} `json:"once"`
	}
	if err := json.Unmarshal(data, &share); err != nil {
		t.Fatal(err)
	}
	if len(share.Once) != 1 || share.Once[0].Expected != "owner-client" {
		t.Fatalf("producer lost expected client: %s", data)
	}
}

// This wraps the production fresh-launch path but stops at the frozen keeper
// plan. No runtime, program or installer is executed.
func TestRunResolvesOnlyUserSelectedDeliveredSignInPoints(t *testing.T) {
	t.Setenv("YOLO_NO_PROGRAM_READINESS", "1")
	t.Setenv("YOLO_NO_AUTO_CAPTURE", "1")
	// The no-canonical-state assertion below is about launch INSPECTION on Linux podman,
	// where the resident broker owns the store. On any other host podman takes the
	// boundary tier (onceTierMode, ID-19), whose start pass legitimately opens the store
	// before the keeper, so pin the Linux tier rather than inherit the test host's OS.
	setOnceGOOS(t, "linux")
	for _, tc := range []struct {
		name, namespace, native                                                                                     string
		workspaceOnly, changedURL, view, unselected, ownClient, nonOnce, removed, capturable, usable, expired, dead bool
		skillOnlyAgent, skillOnlyIntegration                                                                        bool
	}{
		{name: "bare", native: "plugin:fixture:fixture", capturable: true},
		{name: "skill-only agent", skillOnlyAgent: true},
		{name: "skill-only integration", skillOnlyIntegration: true, capturable: true},
		{name: "usable record", native: "plugin:fixture:fixture", capturable: true, usable: true},
		{name: "expired record", native: "plugin:fixture:fixture", capturable: true, expired: true},
		{name: "dead record", native: "plugin:fixture:fixture", capturable: true, dead: true},
		{name: "namespaced", namespace: "team", native: "plugin:team-fixture:fixture", capturable: true},
		{name: "workspace-only", workspaceOnly: true},
		{name: "pack unselected", unselected: true, capturable: true},
		{name: "own client mismatch", ownClient: true, capturable: true},
		{name: "not once", nonOnce: true},
		{name: "server removed", removed: true},
		{name: "workspace redirect", changedURL: true},
		{name: "credential view", native: "plugin:fixture:fixture", view: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			root := t.TempDir()
			manifest := `name="claude"
mcp_header_form="json"
mcp_sign_in_paste=true
[[program]]
bin="claude"
via="installer"
url="file:///ctx/packs/fixture/never-install.sh"
[native_sign_in]
formats=["claude-plugin"]
[[skills]]
agent="claude"
into=".claude/skills"
[[skills]]
from="skills"
[[mcp_auth]]
store=".claude/.credentials.json"
format="json"
empty_store="{}"
entries_at=["mcpOAuth"]
lock={protocol="proper-lockfile",path=".claude/.storage-write.lock",stale_seconds=15}
fields={access_token="accessToken",refresh_token="refreshToken",expires_at={path="expiresAt",unit="ms"},client_id="clientId"}
login_hint="run /mcp in claude and authenticate {name}"
[[state]]
at=".claude-shared-credentials"
scope="machine"
because="synthetic sign-in fixture"
[[hook]]
from=".claude/.credentials.json"
at=".claude-shared-credentials"
hook="shared_credentials"
`
			script, err := os.ReadFile(filepath.Join(shippedPack(t, "claude").Root, "derive.lua"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte(manifest), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "derive.lua"), script, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "skills", "probe"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "skills", "probe", "SKILL.md"), []byte("inert projection fixture\n"), 0644); err != nil {
				t.Fatal(err)
			}
			integrationSource := t.TempDir()
			integrationRoot := managedSignInFixture(t, integrationSource, tc.skillOnlyIntegration)
			selected := []any{root}
			integration := any(map[string]any{"source": integrationRoot, "name": "fixture-mcp"})
			if tc.namespace != "" {
				integration = map[string]any{"source": integrationRoot, "name": tc.namespace}
			}
			if !tc.workspaceOnly && !tc.unselected {
				selected = append(selected, integration)
			}
			settings := map[string]any{"packs": selected, "mcp_servers": map[string]any{"fixture": map[string]any{"url": "https://mcp.fixture.invalid/mcp", "sign_in": "once"}}}
			if tc.view {
				selected = append(selected, "claude-auth")
				settings["packs"] = selected
			}
			server := settings["mcp_servers"].(map[string]any)["fixture"].(map[string]any)
			if tc.ownClient {
				server["oauth"] = map[string]any{"client_id": "owner-client"}
			}
			if tc.nonOnce {
				delete(server, "sign_in")
			}
			if tc.removed {
				settings["mcp_servers"] = map[string]any{}
			}
			body, _ := json.Marshal(settings)
			writeUserConfigJSON(t, home, string(body))
			if tc.namespace != "" {
				if err := os.Remove(filepath.Join(home, ".config/yolo-jail/config.jsonc")); err != nil {
					t.Fatal(err)
				}
				toml := fmt.Sprintf(`packs=["fixture/%s","team/fixture-mcp"]
pack_sources={fixture={path=%q,bare=true},team={path=%q,bare=false}}
[mcp_servers.fixture]
url="https://mcp.fixture.invalid/mcp"
sign_in="once"
`, filepath.Base(root), filepath.Dir(root), integrationSource)
				if err := os.WriteFile(filepath.Join(home, ".config/yolo-jail/config.toml"), []byte(toml), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.skillOnlyAgent || tc.skillOnlyIntegration {
				if err := os.Remove(filepath.Join(home, ".config/yolo-jail/config.jsonc")); err != nil {
					t.Fatal(err)
				}
				handle := "fixture/" + filepath.Base(root)
				agent, integration := fmt.Sprintf("%q", handle), `"team/fixture-mcp"`
				if tc.skillOnlyAgent {
					agent = fmt.Sprintf(`{pack=%q,skills={only=["probe"]}}`, handle)
				}
				if tc.skillOnlyIntegration {
					integration = `{pack="team/fixture-mcp",skills={only=["fixture-api"]}}`
				}
				toml := fmt.Sprintf("packs=[%s,%s]\npack_sources={fixture={path=%q,bare=true},team={path=%q,bare=false}}\n[mcp_servers.fixture]\nurl=\"https://mcp.fixture.invalid/mcp\"\nsign_in=\"once\"\n", agent, integration, filepath.Dir(root), integrationSource)
				if err := os.WriteFile(filepath.Join(home, ".config/yolo-jail/config.toml"), []byte(toml), 0644); err != nil {
					t.Fatal(err)
				}
			}
			ws := t.TempDir()
			if tc.changedURL {
				writeWorkspaceConfig(t, ws, `{"mcp_servers":{"fixture":{"url":"https://attacker.invalid/mcp"}}}`)
			}
			var out, errs bytes.Buffer
			o := dispatchOptions(t, ws, "podman", &out, &errs, nil)
			if tc.workspaceOnly {
				workspacePack, _ := json.Marshal(map[string]any{"packs": []any{map[string]any{"source": integrationRoot, "name": "fixture-mcp"}}})
				writeWorkspaceConfig(t, ws, string(workspacePack))
			}
			if tc.view {
				withViewSwitch(o)
			}
			o.AcceptConfigChanges = true
			o.NeverAttach = true
			repo, _ := o.RepoRoot()
			o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
			o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
			o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
			captures := 0
			o.CapturesDir = func() string { return "" }
			o.AutoCapture = func([]string, string, io.Writer, bool) { captures++ }
			var plan *keeperPlan
			before := defaultKeeperSpawner
			t.Cleanup(func() { defaultKeeperSpawner = before })
			defaultKeeperSpawner = func(_ *Options, p string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
				var err error
				plan, err = readKeeperPlan(p)
				if err != nil {
					return nil, err
				}
				return nil, errors.New("fixture stops before runtime creation")
			}
			if tc.usable || tc.expired || tc.dead {
				identity, err := mcpIdentity("https://mcp.fixture.invalid/mcp", "http", map[string]string{}, "")
				if err != nil {
					t.Fatal(err)
				}
				store, err := mcpauth.NewOnceStore(filepath.Join(loopholes.StateDirFor(selectedMCPHostService), "once"))
				if err != nil {
					t.Fatal(err)
				}
				if err := store.WithLock(func(file *mcpauth.CanonicalFile) error {
					file.Records = []mcpauth.Record{{Server: "fixture", Identity: identity, URL: "https://mcp.fixture.invalid/mcp", Generation: 1, State: "live", Entry: json.RawMessage(`{}`), AccessToken: "fixture-access", ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli()}}
					if tc.expired {
						file.Records[0].ExpiresAtMS = time.Now().Add(-time.Hour).UnixMilli()
					}
					if tc.dead {
						file.Records[0].State = "dead"
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			rc := Run(*o)
			if tc.workspaceOnly {
				if rc == 0 || plan != nil || captures != 0 || !strings.Contains(errs.String(), "user-scope only") {
					t.Fatalf("workspace pack authority was not refused: rc=%d plan=%v %s", rc, plan, errs.String())
				}
				return
			}
			if plan == nil {
				t.Fatalf("did not reach keeper: %s %s", out.String(), errs.String())
			}
			if captures != 0 {
				t.Fatal("fixture ran auto-capture")
			}
			if (tc.skillOnlyAgent || tc.skillOnlyIntegration) && len(plan.CallbackLeases) != 0 {
				t.Fatalf("skill-only projection acquired callback authority: %+v", plan.CallbackLeases)
			}
			if tc.skillOnlyIntegration && plan.LaunchInputMCP != nil && plan.LaunchInputMCP.SignIn != nil {
				for _, bundle := range plan.LaunchInputMCP.SignIn.Bundles {
					if bundle.Pack == "team/fixture-mcp" {
						t.Fatalf("skill-only integration retained native sign-in bundle: %+v", bundle)
					}
				}
				if advice := plan.LaunchInputMCP.SignIn.Advice; advice != nil && len(advice.Formats) != 0 {
					t.Fatalf("skill-only integration retained sign-in advice formats: %+v", advice)
				}
			}
			facts, _ := json.Marshal(plan.LaunchInputMCP)
			t.Logf("snapshot: %s", facts)
			native := ""
			capturable := false
			if plan.LaunchInputMCP != nil && plan.LaunchInputMCP.SignIn != nil {
				for _, d := range plan.LaunchInputMCP.SignIn.Decisions {
					if d.Server == "fixture" && d.Agent == "claude" {
						if d.Point != nil {
							native = d.Point.NativeName
						}
						capturable = d.Capturable
					}
				}
			}
			if plan.LaunchInputMCP != nil && plan.LaunchInputMCP.SignIn != nil {
				wire, present := envArgValue(plan.RunCmd, mcpsignin.WireEnv)
				expected, _ := json.Marshal(plan.LaunchInputMCP.SignIn)
				if !present || wire != string(expected) {
					t.Fatalf("frozen keeper facts not on actual boot argv: %s", wire)
				}
			}
			if tc.usable || tc.expired || tc.dead {
				var projected struct {
					Decisions []struct {
						Usable bool `json:"usable_record"`
					} `json:"decisions"`
				}
				body, _ := json.Marshal(plan.LaunchInputMCP.SignIn)
				if err := json.Unmarshal(body, &projected); err != nil {
					t.Fatal(err)
				}
				if len(projected.Decisions) != 1 || projected.Decisions[0].Usable != tc.usable {
					t.Fatalf("usable canonical fact missing from actual launch: %s", body)
				}
				if strings.Contains(string(body), "fixture-access") {
					t.Fatal("launch wire leaked canonical access token")
				}
			}
			if !tc.usable && !tc.expired && !tc.dead {
				if _, err := os.Stat(filepath.Join(loopholes.StateDirFor(selectedMCPHostService), "once")); !os.IsNotExist(err) {
					t.Fatalf("launch inspection created canonical state: %v", err)
				}
			}
			if native != tc.native || capturable != tc.capturable {
				t.Fatalf("actual Run native=%q capturable=%v want native=%q; frozen=%+v; %s", native, capturable, tc.native, plan.LaunchInputMCP, errs.String())
			}
		})
	}
}
