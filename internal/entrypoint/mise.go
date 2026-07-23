package entrypoint

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// mise.go holds the helpers the mise prism surface (prism_mise.go) needs to
// build its dynamic [tools] computed layer. The global mise config
// (~/.config/mise/config.toml) is now composed by ConfigureMisePrism through the
// agentcfg engine — the old in-place GenerateMiseConfig editor (and its stale-
// runtime scrub, dedupe, retire, and base-tool machinery) is gone; the prism's
// first-migration seed replaces it (docs/design/config-migration-to-prism.md
// §4.1).

// loadInjectedTools parses YOLO_MISE_TOOLS as a JSON object (default {}). It is
// the source of the mise surface's COMPUTED [tools] layer: the per-workspace
// runtime pins yolo injects.
//
// yolo owns NO default runtime. node, python, and go are ALL baked into the OCI
// image (flake.nix imagePkgs.nodejs_24 / python3 / go), RPATH-self-contained, so
// mise must never install a second copy — a duplicate mise runtime is the
// non-nix binary behind the LD_LIBRARY_PATH / MCP-wrapper whack-a-mole
// (docs/design/mise-node-dynamic-linking.md) and the host↔baked version skew.
// Bare node/python/go resolve to the baked /bin/<tool>, the same binaries the
// MCP wrappers and Go tooling target — one of each. A workspace MAY still pin
// its own node/python/go via YOLO_MISE_TOOLS or /workspace/mise.toml (the
// intentional per-workspace override); mise then installs that non-nix version
// and its shim wins. That override is the ONLY case that reintroduces a non-nix
// runtime — exactly the case nix-ld makes robust (env-free libstdc++). See
// docs/research/tool-provisioning.md §2.
func loadInjectedTools(e *Env) *jsonx.OrderedMap {
	raw := e.Getenv("YOLO_MISE_TOOLS")
	if raw == "" {
		raw = "{}"
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return jsonx.NewOrderedMap()
	}
	return m
}

// miseValueString renders an injected tool's version value as a string;
// versions are always strings in practice, so non-strings fall back to pyStr
// for completeness (and so a JSON number reaches the TOML codec as its string
// form, not as an int that would change the value's shape).
func miseValueString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return pyStr(v)
}
