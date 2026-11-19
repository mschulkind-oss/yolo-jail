package hostskills

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManagedMCPPluginDeliveryKeepsIdentityAndOwnership(t *testing.T) {
	root := `{"mcpServers":{"server":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"public-fixture","callbackPort":3118}}}}`
	for _, native := range []string{`{"server":{"url":"https://mcp.fixture.invalid/mcp"}}`, `".mcp.json"`} {
		for _, namespace := range []string{"", "team"} {
			t.Run(native+namespace, func(t *testing.T) {
				req, _ := testPluginReq(t, TierNamespaced, `{"name":"original","mcpServers":`+native+`,"someFutureFeature":{"deeply":["nested"]}}`)
				req.SignInFormat = "claude-plugin"
				req.Namespace = namespace
				if err := os.WriteFile(filepath.Join(req.Plugin.Dir, ".mcp.json"), []byte(root), 0600); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(req.Plugin.ManifestPath)
				if err != nil {
					t.Fatal(err)
				}
				results, err := DeliverPlugin(req)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range results {
					if r.Action == ActionRefused {
						t.Fatalf("accepted native identity refused: %+v", r)
					}
				}
				name := "original"
				if namespace != "" {
					name = namespace + "-" + name
				}
				delivered := filepath.Join(req.SkillsDir, name)
				body, err := os.ReadFile(filepath.Join(delivered, ".claude-plugin/plugin.json"))
				if err != nil {
					t.Fatal(err)
				}
				var original, got map[string]any
				if err := json.Unmarshal(before, &original); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if got["name"] != name || got["x-yolo-managed-by"] != "yolo-jail" || got["x-yolo-pack"] != req.Pack || !reflect.DeepEqual(original["someFutureFeature"], got["someFutureFeature"]) || !reflect.DeepEqual(original["mcpServers"], got["mcpServers"]) {
					t.Fatalf("manifest identity/ownership changed: %s", body)
				}
				actual, err := os.ReadFile(filepath.Join(delivered, ".mcp.json"))
				if err != nil || string(actual) != root {
					t.Fatal("root MCP bytes changed")
				}
				source, err := os.ReadFile(req.Plugin.ManifestPath)
				if err != nil || !bytes.Equal(source, before) {
					t.Fatal("source manifest changed")
				}
			})
		}
	}
}

func TestManagedMCPPluginRefusesIdentityChangingCopy(t *testing.T) {
	previous := copyPluginTree
	t.Cleanup(func() { copyPluginTree = previous })
	for _, file := range []string{".mcp.json", ".claude-plugin/plugin.json"} {
		t.Run(file, func(t *testing.T) {
			req, _ := testPluginReq(t, TierNamespaced, `{"name":"original","mcpServers":{"server":{"url":"https://mcp.fixture.invalid/mcp"}}}`)
			req.SignInFormat = "claude-plugin"
			if err := os.WriteFile(filepath.Join(req.Plugin.Dir, ".mcp.json"), []byte(`{"server":{"url":"https://mcp.fixture.invalid/mcp"}}`), 0644); err != nil {
				t.Fatal(err)
			}
			copyPluginTree = func(src, dest string) error {
				if err := previous(src, dest); err != nil {
					return err
				}
				body := `{"server":{"url":"https://changed.invalid/mcp"}}`
				if strings.HasSuffix(file, "plugin.json") {
					body = `{"name":"original","mcpServers":{"server":{"url":"https://changed.invalid/mcp"}}}`
				}
				return os.WriteFile(filepath.Join(dest, filepath.FromSlash(file)), []byte(body), 0644)
			}
			results, err := DeliverPlugin(req)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Action != ActionRefused || !strings.Contains(results[0].Detail, "changed during delivery") {
				t.Fatalf("identity-changing copy accepted: %+v", results)
			}
			if _, err := os.Lstat(filepath.Join(req.SkillsDir, "original")); !os.IsNotExist(err) {
				t.Fatal("refused changed tree retained")
			}
		})
	}
}

func TestManagedMCPPluginRefusesLinkedIdentityBeforeCopy(t *testing.T) {
	req, _ := testPluginReq(t, TierNamespaced, `{"name":"original"}`)
	req.SignInFormat = "claude-plugin"
	original := filepath.Join(req.Plugin.Dir, ".mcp.json")
	if err := os.Rename(original, original+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".mcp.json.real", original); err != nil {
		t.Fatal(err)
	}
	results, err := DeliverPlugin(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != ActionRefused || !strings.Contains(results[0].Detail, "sign-in bundle not delivered") {
		t.Fatalf("linked identity was delivered: %+v", results)
	}
	if _, err := os.Lstat(filepath.Join(req.SkillsDir, "original")); !os.IsNotExist(err) {
		t.Fatal("refused native tree placed")
	}
}

func TestManagedMCPPluginReportsFlatAndForeignRefusals(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		req, _ := testPluginReq(t, TierFlat, `{"name":"original"}`)
		req.SignInFormat = "claude-plugin"
		if foreign {
			req.Tier = TierNamespaced
			dest := filepath.Join(req.SkillsDir, "original/.claude-plugin")
			if err := os.MkdirAll(dest, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, "plugin.json"), []byte(`{"name":"foreign"}`), 0644); err != nil {
				t.Fatal(err)
			}
		}
		results, err := DeliverPlugin(req)
		if err != nil {
			t.Fatal(err)
		}
		refused := false
		for _, r := range results {
			refused = refused || (r.Action == ActionRefused && strings.Contains(r.Detail, "sign-in bundle not delivered"))
		}
		if !refused {
			t.Fatalf("flat/foreign native refusal not exposed: %+v", results)
		}
	}
}
