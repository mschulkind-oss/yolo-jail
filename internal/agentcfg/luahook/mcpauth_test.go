package luahook

import (
	"strings"
	"testing"
)

func TestMCPAuthCanonicalJSONEscapesOnlyActualSeparators(t *testing.T) {
	script := `yolo.mcp_auth("test", {
  key = function()
    local j = yolo.canonical_json({
      ["actual key"] = "nested\\ actual ",
      ["literal\\u2028"] = "value\\u2029",
      mixed = "\\\\u2028 / \\u2029",
    })
    return j .. "|" .. yolo.sha256(j)
  end,
  match = function() return nil end,
})`
	got, err := MCPAuthKeys(script, "test", []MCPServerRef{{Name: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"actual key\":\"nested\\\\ actual \",\"literal\\\\u2028\":\"value\\\\u2029\",\"mixed\":\"\\\\\\\\u2028 / \\\\u2029\"}|1f3839507f5726d1358cd2605bf1a372a2baf365a4f7935615ea3f44f2866008"
	if got["x"] != want {
		t.Fatalf("canonical JSON and hash = %q, want %q", got["x"], want)
	}
}

func TestMCPAuthRuntimeRejectsUnknownAPI(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{name: "key", call: func() error {
			_, err := MCPAuthKeys(`yolo.mcp_auth("test", {
  key = function() return yolo.typo() end,
  match = function() return nil end,
})`, "test", []MCPServerRef{{Name: "x"}})
			return err
		}},
		{name: "match", call: func() error {
			_, err := MCPAuthMatch(`yolo.mcp_auth("test", {
  key = function() return "key" end,
  match = function() return yolo.typo() end,
})`, "test", "key", nil)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !strings.Contains(err.Error(), "typo") {
				t.Fatalf("unknown runtime API error = %v, want error naming typo", err)
			}
		})
	}
}

func TestCodexStoreKeyVectors(t *testing.T) {
	script := `
yolo.mcp_auth("codex", {
  key = function(s)
    local j = yolo.canonical_json({ type = "http", url = s.url, headers = {} })
    return s.name .. "|" .. string.sub(yolo.sha256(j), 1, 16)
  end,
  match = function(key, entry)
    local name = string.match(key, "^(.*)|%x+$")
    if name == nil or type(entry.server_url) ~= "string" then return nil end
    return { name = name, url = entry.server_url }
  end,
})`
	for _, tc := range []struct{ name, url, want string }{
		{"linear", "https://mcp.linear.app/mcp", "linear|9436f96375b1259b"},
		{"odd", "https://mcp.example.test/mcp?a=1&b=<2>", "odd|8ac55be545c4b620"},
	} {
		got, err := MCPAuthKeys(script, "codex", []MCPServerRef{{Name: tc.name, URL: tc.url}})
		if err != nil {
			t.Fatal(err)
		}
		if got[tc.name] != tc.want {
			t.Errorf("key(%s) = %q, want %q", tc.name, got[tc.name], tc.want)
		}
	}
	m, err := MCPAuthMatch(script, "codex", "linear|9436f96375b1259b",
		map[string]any{"server_url": "https://mcp.linear.app/mcp"})
	if err != nil || m == nil || m.Name != "linear" || m.URL != "https://mcp.linear.app/mcp" {
		t.Fatalf("match = %+v, %v", m, err)
	}
}

func TestMCPAuthCoreJSONHelpers(t *testing.T) {
	script := `yolo.mcp_auth("test", {
  key = function(s)
    return yolo.sha256("") .. "|" ..
      yolo.canonical_json({b=1,a={2,3}}) .. "|" ..
      yolo.canonical_json({})
  end,
  match = function() return nil end,
})`
	got, err := MCPAuthKeys(script, "test", []MCPServerRef{{Name: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855|{\"a\":[2,3],\"b\":1}|{}"
	if got["x"] != want {
		t.Fatalf("core helpers = %q, want %q", got["x"], want)
	}
}

func TestMCPAuthMatchNilAndErrors(t *testing.T) {
	script := `yolo.mcp_auth("test", {
  key = function() return "key" end,
  match = function(key, entry)
    if key == "miss" then return nil end
    return {name = "server", url = entry.url}
  end,
})`
	if got, err := MCPAuthMatch(script, "test", "miss", nil); err != nil || got != nil {
		t.Fatalf("unmatched entry = %+v, %v; want nil, nil", got, err)
	}
	if _, err := MCPAuthMatch(script, "test", "hit", map[string]any{"url": "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	_, err := MCPAuthKeys(`yolo.mcp_auth("test", {
  key = function() return {} end,
  match = function() return nil end,
})`, "test", []MCPServerRef{{Name: "x"}})
	if err == nil || !strings.Contains(err.Error(), "string") {
		t.Fatalf("non-string key result error = %v, want string result refusal", err)
	}
}
