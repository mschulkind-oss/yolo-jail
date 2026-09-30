package packdecl

// SendsUnlistedModels reports whether the program installing bin declares
// `unlisted_background_models` (Contribution.UnlistedBackgroundModels): some of its requests name
// models off its provider's list, so the wire bridge's model allowlist refuses none of them
// (docs/design/wire-bridge-gateway.md WG-I41). False when the manifest installs no such program.
func (m *Manifest) SendsUnlistedModels(bin string) bool {
	for _, c := range m.Contributions() {
		if c.Kind == KindProgram && c.Bin == bin {
			return c.UnlistedBackgroundModels
		}
	}
	return false
}
