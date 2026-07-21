package agentcfg

import "github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"

// builtin.go holds the yolo-shipped surface manifests — the per-agent
// "defaults + managed + path + codec" data of docs/plans/agent-settings-composition.md
// §1.1 / §6.5 ①. These are Go-declared (the manifest package documents why:
// defaults/managed carry arbitrary decoded values best expressed as literals,
// and a leaf library needs no file I/O). Phase B lands them one agent at a time;
// pi is the proof-of-concept surface (§Config-composition build in the roadmap).
//
// A surface's Path uses a leading "~/" that the CLI expands to the jail home;
// this package stays path-policy-free (it never touches the filesystem).

// piSettings is the pi settings surface (§6.5 ①): the host mirrors
// ~/.pi/agent/settings.json, yolo defaults theme to "system", and the jail
// enforces defaultProjectTrust=always (the container is the trust boundary, so
// pi should not re-prompt).
var piSettings = manifest.Surface{
	Agent:    "pi",
	Name:     "settings",
	Path:     "~/.pi/agent/settings.json",
	Codec:    "json",
	Defaults: map[string]any{"theme": "system"},
	Managed:  map[string]any{"defaultProjectTrust": "always"},
}

// BuiltinManifest returns the yolo-shipped manifest of all surfaces yolo knows
// how to compose. Phase B grows this list (claude, gemini, copilot, opencode,
// codex, mcp, lsp, mise); today it is pi's settings surface only. It panics on
// a malformed builtin (a programming error in this file, caught by tests), never
// at runtime for user input.
func BuiltinManifest() *manifest.Manifest {
	m, err := manifest.New(
		piSettings,
	)
	if err != nil {
		panic("agentcfg: malformed builtin manifest: " + err.Error())
	}
	return m
}
