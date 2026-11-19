package packload

import "github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"

// MCPAuthAdapterFor intersects this selected pack's declared programs with its
// exact Lua registrations. Ambiguous or unsupported stores have no adapter;
// logical selection handles and basenames never select one.
func MCPAuthAdapterFor(pack *Pack) string {
	if pack == nil || pack.Decl == nil || pack.Decl.MCPAuth == nil {
		return ""
	}
	script := DeriveScript(pack)
	adapter := ""
	for _, program := range pack.Decl.InstallContributions() {
		if !luahook.MCPAuthRegistered(script, program.Bin) {
			continue
		}
		if adapter != "" && adapter != program.Bin {
			return ""
		}
		adapter = program.Bin
	}
	return adapter
}

// MCPConsumerFor uses only the effective selected program owner's declaration.
// Helper form and source ownership are independent of bundle selection.
func MCPConsumerFor(packs []*Pack, agent string) (form string, source bool) {
	for _, pack := range packs {
		if pack != nil && pack.Decl != nil && pack.installsBin(agent) {
			return pack.Decl.MCPHeaderForm, pack.Decl.MCPAuth != nil
		}
	}
	return "", false
}
