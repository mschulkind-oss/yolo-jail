package mcpsignin

import (
	"bytes"
	"fmt"
	"reflect"
)

// CheckPreserved permits ownership marks and a namespaced manifest name, but
// no native server identity-content change (MS-D9). It confers no load authority.
func CheckPreserved(original, delivered Inventory) error {
	if len(original.Problems) > 0 || len(delivered.Problems) > 0 {
		return fmt.Errorf("bundle identity could not be read")
	}
	if !bytes.Equal(original.RootMCP, delivered.RootMCP) {
		return fmt.Errorf("root .mcp.json changed during delivery")
	}
	if !bytes.Equal(original.ManifestMCP, delivered.ManifestMCP) {
		return fmt.Errorf("manifest mcpServers value changed during delivery")
	}
	if !reflect.DeepEqual(original.Servers, delivered.Servers) {
		return fmt.Errorf("referenced native server identity changed during delivery")
	}
	return nil
}
