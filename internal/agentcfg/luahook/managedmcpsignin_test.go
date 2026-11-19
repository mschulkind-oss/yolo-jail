package luahook

import "testing"

func TestManagedMCPNativeNameThroughSandboxRegistration(t *testing.T) {
	script := `yolo.mcp_sign_in("fixture", { native_name = function(name, key) return "native:" .. name .. ":" .. key end })`
	got, err := MCPNativeName(script, "fixture", "team-original", "server")
	if err != nil || got != "native:team-original:server" {
		t.Fatalf("native name %q: %v", got, err)
	}
	for _, script := range []string{`yolo.mcp_sign_in("fixture", {native_name=function() return {} end})`, `yolo.mcp_sign_in("fixture", {native_name=function() return io.open("ignored") end})`, `yolo.mcp_sign_in("fixture", {native_name=function() return "" end})`} {
		if _, err := MCPNativeName(script, "fixture", "name", "server"); err == nil {
			t.Fatal("invalid/impure native function accepted")
		}
	}
}
