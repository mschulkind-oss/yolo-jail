package run

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestMCPAuthShareNamesTheLaunchsHome: the share tells the broker which of the workspace's homes
// holds the replicas (named-setups.md §9): a setup's home by its path under .yolo, the legacy home
// by omission, which older brokers read as before. Red if a share literal drops shareHomeRel.
func TestMCPAuthShareNamesTheLaunchsHome(t *testing.T) {
	ws := t.TempDir()
	for _, c := range []struct{ home, want string }{
		{paths.SetupHomeState(ws, "podman-self"), "setups/podman-self/home"},
		{paths.LegacyHomeState(ws), ""},
	} {
		in := LaunchInputContext{Container: "jail-synthetic", Workspace: ws, Home: c.home, Runtime: "container", EffectiveMCPServers: jsonx.NewOrderedMap()}
		data, err := produceMCPAuthShare(in)
		if err != nil {
			t.Fatal(err)
		}
		share, err := mcpauth.DecodeShare(data)
		if err != nil {
			t.Fatalf("home %s: the share does not validate: %v", c.home, err)
		}
		if share.Home != c.want {
			t.Errorf("home %s: share.Home = %q, want %q", c.home, share.Home, c.want)
		}
		if got := filepath.Join(paths.WorkspaceStateDir(ws), share.HomeRel()); got != c.home {
			t.Errorf("home %s: the broker would read %s", c.home, got)
		}
		data, err = mcpShareFromPacks(in, nil)
		if err != nil {
			t.Fatal(err)
		}
		if share, err = mcpauth.DecodeShare(data); err != nil || share.Home != c.want {
			t.Errorf("home %s: the pack-built share names home %q (%v), want %q", c.home, share.Home, err, c.want)
		}
	}
}

func TestMCPAuthNativeKeyCannotProveHeaderIdentity(t *testing.T) {
	url := "https://mcp.example/linear"
	one, err := mcpIdentity(url, "http", map[string]string{"X-Workspace": "a"}, "same-client")
	if err != nil {
		t.Fatal(err)
	}
	two, err := mcpIdentity(url, "http", map[string]string{"X-Workspace": "b"}, "same-client")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("header changes did not change the approved identity")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "packs", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDirForUse(root, "codex")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	var firstKey string
	for _, headers := range []string{`{"X-Workspace":"a"}`, `{"X-Workspace":"b"}`} {
		servers, err := jsonx.Decode([]byte(`{"linear":{"url":"` + url + `","headers":` + headers + `,"oauth":{"client_id":"same-client"}}}`))
		if err != nil {
			t.Fatal(err)
		}
		table := servers.(*jsonx.OrderedMap)
		in := LaunchInputContext{Container: "jail-test", Workspace: t.TempDir(), Runtime: "podman", MCPProven: true,
			EffectiveMCPServers: table, UserOnlyMCPServers: table, MCPScopes: packload.MCPServerScopes(table, table)}
		data, err := mcpShareFromPacks(in, []*packload.Pack{pack})
		if err != nil {
			t.Fatal(err)
		}
		var share struct {
			Replicas []struct {
				Shared []struct {
					Key, Identity string
					ClientID      string `json:"client_id"`
				} `json:"shared"`
			} `json:"replicas"`
		}
		if err := json.Unmarshal(data, &share); err != nil {
			t.Fatal(err)
		}
		if len(share.Replicas) != 1 || len(share.Replicas[0].Shared) != 1 {
			t.Fatalf("share = %+v", share)
		}
		entry := share.Replicas[0].Shared[0]
		if entry.ClientID != "same-client" || entry.Identity != one && entry.Identity != two {
			t.Fatalf("share = %+v", entry)
		}
		if headers == `{"X-Workspace":"a"}` {
			firstKey = entry.Key
		} else if firstKey != entry.Key {
			t.Fatal("Codex key unexpectedly proves header identity")
		}
	}
}

func TestMCPAuthShareDoesNotSeedConditionallyWithheldServers(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "packs", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDirForUse(root, "codex")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	for _, tc := range []struct{ name, server string }{
		{"requires_env", `{"url":"https://mcp.example/linear","requires_env":["PRIVATE_KEY"]}`},
		{"provider capability", `{"url":"https://mcp.example/linear","provides":"search"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			effective, _ := jsonx.Decode([]byte(`{"linear":` + tc.server + `,"plain":{"url":"https://mcp.example/plain"}}`))
			servers := effective.(*jsonx.OrderedMap)
			in := LaunchInputContext{Container: "jail-test", Workspace: t.TempDir(), Runtime: "podman", MCPProven: true,
				EffectiveMCPServers: servers, UserOnlyMCPServers: servers, MCPScopes: packload.MCPServerScopes(servers, servers)}
			data, err := mcpShareFromPacks(in, []*packload.Pack{pack})
			if err != nil {
				t.Fatal(err)
			}
			var share struct {
				Replicas []struct {
					Shared []struct{ Name string } `json:"shared"`
				} `json:"replicas"`
			}
			if err := json.Unmarshal(data, &share); err != nil {
				t.Fatal(err)
			}
			if len(share.Replicas) != 1 || len(share.Replicas[0].Shared) != 1 || share.Replicas[0].Shared[0].Name != "plain" {
				t.Fatalf("conditional server must not receive a token replica: %+v", share)
			}
		})
	}
}

func TestMCPAuthShareUsesFrozenUserScopeAndSelectedPack(t *testing.T) {
	effective, _ := jsonx.Decode([]byte(`{"linear":{"url":"https://mcp.example/linear"},"private":{"url":"https://other.example/private"}}`))
	user, _ := jsonx.Decode([]byte(`{"linear":{"url":"https://mcp.example/linear"}}`))
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "packs", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDirForUse(root, "codex")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	in := LaunchInputContext{Container: "jail-synthetic", Workspace: t.TempDir(), Runtime: "podman", MCPProven: true,
		EffectiveMCPServers: effective.(*jsonx.OrderedMap), UserOnlyMCPServers: user.(*jsonx.OrderedMap), MCPScopes: packload.MCPServerScopes(effective.(*jsonx.OrderedMap), user.(*jsonx.OrderedMap))}
	data, err := mcpShareFromPacks(in, []*packload.Pack{pack})
	if err != nil {
		t.Fatal(err)
	}
	var share struct {
		Replicas []struct {
			Shared []struct{ Name, Key string } `json:"shared"`
		} `json:"replicas"`
	}
	if err := json.Unmarshal(data, &share); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" && (len(share.Replicas) != 1 || len(share.Replicas[0].Shared) != 1 || share.Replicas[0].Shared[0].Name != "linear" || share.Replicas[0].Shared[0].Key == "") {
		t.Fatalf("only composed user-level linear is shared: %+v", share)
	}
	in.Runtime = "container"
	data, err = produceMCPAuthShare(in)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &share); err != nil {
		t.Fatal(err)
	}
	if len(share.Replicas) != 0 {
		t.Fatalf("Apple Container has no replica in this slice: %+v", share)
	}
	lines := backendInertLines([]*packload.Pack{pack}, "inert on this backend")
	if !strings.Contains(strings.Join(lines, "\n"), "MCP sign-ins for codex are not shared on Apple Container") {
		t.Fatalf("the selected store has no not-shared line: %q", lines)
	}
	lines = mcpUnavailableLines([]*packload.Pack{pack}, "macos-user")
	if len(lines) != 1 || !strings.Contains(lines[0], "not shared on macos-user") {
		t.Fatalf("macos-user must not claim a replica: %q", lines)
	}
	var stderr bytes.Buffer
	o := &Options{Stderr: &stderr}
	o.notePackLoopholesInert("macos-user", []*packload.Pack{pack}, nil)
	if !strings.Contains(stderr.String(), "MCP sign-ins for codex are not shared on macos-user") {
		t.Fatalf("unsupported backend launch omitted its sign-in disclosure: %q", stderr.String())
	}
}
