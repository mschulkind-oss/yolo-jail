package entrypoint

// prism_convert.go bridges yolo's runtime value model (jsonx — order-preserving
// *OrderedMap, integer-literal-preserving jsonInt) to the plain, stdlib-native
// generic model the agentcfg engine merges and its JSON codec re-encodes.
//
// This is the "connecting seam" of the config-composition cutover: the computed
// layer (Inputs.Computed) is yolo's per-boot DYNAMIC content — the reconciled
// MCP-server table, the LSP-derived toggles — and it originates as jsonx values
// (LoadMCPServers returns an *OrderedMap; env-sourced config decodes via jsonx).
//
// The conversion itself lives in internal/jsonx (jsonx.PlainMap / jsonx.Plain),
// because internal/config needs the identical lowering for a user-declared
// host_files entry's defaults/managed/content layers and the two must not drift —
// both feed the same engine. See that file for WHY the two jsonx types cannot be
// handed to the engine raw (deepMerge type-switches on map[string]any; jsonInt
// marshals as a quoted string). The aliases below are kept because ~40 call sites
// in this package read prismMap/prismValue, and the local names say "this is the
// prism's input conversion" at the point of use.

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// prismMap deeply converts a jsonx OrderedMap into the engine's plain
// map[string]any model. A nil map yields nil (an absent computed layer).
var prismMap = jsonx.PlainMap

// prismValue deeply converts one jsonx value into the engine's generic model.
var prismValue = jsonx.Plain
