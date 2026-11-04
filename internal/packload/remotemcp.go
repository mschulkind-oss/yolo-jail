package packload

// RemoteMCPTransportsFor returns the remote MCP transports declared by the pack installing
// agent. A selected agent without that declaration has no remote form yet.
func RemoteMCPTransportsFor(packs []*Pack, agent string) []string {
	for _, pack := range packs {
		if pack != nil && pack.Decl != nil && pack.installsBin(agent) {
			return pack.Decl.MCPRemoteTransports()
		}
	}
	return nil
}
