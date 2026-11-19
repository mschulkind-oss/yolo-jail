package mcpsignin

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

const remote = `{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"fixture-client","callbackPort":3118}}`

func TestClaudePluginInventoryIdentityShapes(t *testing.T) {
	for _, tc := range []struct {
		name, root, manifest, extra, reason string
		count                               int
		problem                             string
	}{
		{name: "direct", root: `{"server":` + remote + `}`, count: 1},
		{name: "wrapper", root: `{"mcpServers":{"server":` + remote + `}}`, count: 1},
		{name: "inline", manifest: `{"name":"original","mcpServers":{"server":` + remote + `}}`, count: 1},
		{name: "reference", manifest: `{"name":"original","mcpServers":"config/native.json"}`, extra: `{"server":` + remote + `}`, count: 1},
		{name: "duplicate sources", root: `{"server":` + remote + `}`, manifest: `{"name":"original","mcpServers":{"server":` + remote + `}}`, count: 1, reason: "ambiguous"},
		{name: "duplicate within source", root: `{"server":` + remote + `,"server":` + remote + `}`, problem: "ambiguous duplicate key"},
		{name: "duplicate identity", root: `{"server":{"url":"https://a.invalid","url":"https://b.invalid"}}`, problem: "ambiguous duplicate key"},
		{name: "headers", root: `{"server":{"url":"https://a.invalid","headers":{}}}`, count: 1, reason: "unsupported shape"},
		{name: "URL variable", root: `{"server":{"url":"https://${HOST}/mcp"}}`, count: 1, reason: "unsupported shape"},
		{name: "client variable", root: `{"server":{"url":"https://a.invalid","oauth":{"clientId":"${CLIENT}"}}}`, count: 1, reason: "unsupported shape"},
		{name: "unknown identity", root: `{"server":{"url":"https://a.invalid","auth":"oauth"}}`, count: 1, reason: "unsupported shape"},
		{name: "invalid port", root: `{"server":{"url":"https://a.invalid","oauth":{"callbackPort":80}}}`, count: 1, reason: "unsupported shape"},
		{name: "fractional port", root: `{"server":{"url":"https://a.invalid","oauth":{"callbackPort":3118.5}}}`, count: 1, reason: "unsupported shape"},
		{name: "omitted transport", root: `{"server":{"url":"https://a.invalid"}}`, count: 1},
		{name: "sse", root: `{"server":{"url":"https://a.invalid","type":"sse"}}`, count: 1},
		{name: "stdio", root: `{"server":{"command":"inert","args":[]}}`},
		{name: "escape", manifest: `{"name":"original","mcpServers":"../escape.json"}`, problem: "path escape refused"},
		{name: "malformed manifest", manifest: `{"name":`, problem: "malformed manifest"},
		{name: "two keys", root: `{"a":` + remote + `,"b":` + remote + `}`, count: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := tc.manifest
			if manifest == "" {
				manifest = `{"name":"original"}`
			}
			original := fstest.MapFS{"bundle/.claude-plugin/plugin.json": {Data: []byte(manifest)}}
			if tc.root != "" {
				original["bundle/.mcp.json"] = &fstest.MapFile{Data: []byte(tc.root)}
			}
			if tc.extra != "" {
				original["bundle/config/native.json"] = &fstest.MapFile{Data: []byte(tc.extra)}
			}
			got := ReadInventory(FSReader{original}, "bundle", "claude-plugin")
			if len(got.Servers) != tc.count {
				t.Fatalf("servers=%+v problems=%v", got.Servers, got.Problems)
			}
			for _, s := range got.Servers {
				if s.Reason != tc.reason {
					t.Fatalf("reason=%q want %q", s.Reason, tc.reason)
				}
			}
			if tc.problem != "" && !strings.Contains(strings.Join(got.Problems, ";"), tc.problem) {
				t.Fatalf("problems=%v want %s", got.Problems, tc.problem)
			}
			if tc.root != "" && tc.problem == "" && string(got.RootMCP) != tc.root {
				t.Fatal("raw root bytes changed")
			}
			// Same parser and confinement boundary for selected originals and disk delivery.
			dir := t.TempDir()
			for name, f := range original {
				full := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, f.Data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			reader, err := OpenDisk(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			disk := ReadInventory(reader, "bundle", "claude-plugin")
			if !reflect.DeepEqual(got, disk) {
				t.Fatalf("disk/FS differ: %+v %+v", got, disk)
			}
		})
	}
}

func TestClaudePluginInventoryRefusesEveryLinkedComponent(t *testing.T) {
	for _, component := range []string{"bundle", "bundle/.claude-plugin", "bundle/.claude-plugin/plugin.json", "bundle/.mcp.json", "bundle/config", "bundle/config/native.json"} {
		t.Run(component, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"bundle/.claude-plugin/plugin.json", "bundle/.mcp.json", "bundle/config/native.json"} {
				full := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
					t.Fatal(err)
				}
				body := `{"server":` + remote + `}`
				if strings.HasSuffix(name, "plugin.json") {
					body = `{"name":"original","mcpServers":"config/native.json"}`
				}
				if err := os.WriteFile(full, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			old := filepath.Join(dir, filepath.FromSlash(component))
			target := old + "-real"
			if err := os.Rename(old, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(target), old); err != nil {
				t.Fatal(err)
			}
			reader, err := OpenDisk(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			got := ReadInventory(reader, "bundle", "claude-plugin")
			if len(got.Problems) == 0 {
				t.Fatal("linked identity accepted")
			}
			for _, s := range got.Servers {
				if s.Reason == "" {
					t.Fatal("partial linked inventory became eligible")
				}
			}
		})
	}
	linked := fstest.MapFS{"bundle/.claude-plugin/plugin.json": {Data: []byte(`{"name":"original"}`)}, "bundle/.mcp.json": {Mode: fs.ModeSymlink, Data: []byte("elsewhere")}}
	got := ReadInventory(FSReader{linked}, "bundle", "claude-plugin")
	if len(got.Problems) == 0 {
		t.Fatal("FS symlink accepted")
	}
}
