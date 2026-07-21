package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// miseTomlKey quote a key only if it contains chars
// that aren't valid in a bare key (anything outside [A-Za-z0-9_-]).
var miseBareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func miseTomlKey(key string) string {
	if miseBareKeyRe.MatchString(key) {
		return key
	}
	return `"` + key + `"`
}

// miseBaseTools is the ordered base tool set injected into the global mise
// config. It lists ONLY runtimes that are NOT baked into the image. As of
// 2026-07-20 ALL default runtimes (node, python, AND go) are baked, so this is
// now EMPTY — mise is the override-only path, never the source of a default
// runtime:
//
//   - node, python, and go are ALL BAKED (flake.nix `imagePkgs.nodejs_24`,
//     `python3`, and `imagePkgs.go`), RPATH-self-contained, so mise must NOT
//     install a second copy — a duplicate mise runtime is the non-nix binary
//     behind the LD_LIBRARY_PATH / MCP wrapper whack-a-mole
//     (docs/design/mise-node-dynamic-linking.md) and the host↔baked version
//     skew. Bare `node`/`python`/`go` resolve to the baked `/bin/…`, the same
//     binaries the MCP wrappers and Go tooling target — one of each.
//   - NOTE: the flake's `pkgs.go` (flake.nix:85, nativeBuildInputs) is still
//     ONLY the host cross-compiler for the yolo-jail-go derivation; the JAIL's
//     go is the separate `imagePkgs.go` added to corePackages.
//
// A workspace may still pin its own `node`/`python`/`go` in `mise.toml` (the
// intentional per-workspace override); mise then installs that non-nix version
// and its shim wins. That override is the ONLY case that reintroduces a non-nix
// runtime — exactly the case nix-ld makes robust (env-free libstdc++). See
// docs/research/tool-provisioning.md §2.
var miseBaseTools = []struct{ tool, version string }{}

// bakedRuntimes are the runtimes now baked into the image (flake.nix
// corePackages: nodejs_24, python3, go). yolo used to write these into the
// global mise config as base tools; the migration cleanup in GenerateMiseConfig
// strips a leftover default line for any of these UNLESS it is an intentional
// per-workspace/injected pin. See docs/design/config-migration-to-prism.md §4.1.
var bakedRuntimes = []string{"node", "python", "go"}

// workspaceMisePath is the workspace mise.toml consulted for intentional
// per-workspace runtime pins (and the retire surgery). A package var so tests
// can point it at a fixture; production is always the live bind mount.
var workspaceMisePath = "/workspace/mise.toml"

// It does NOT run the `mise uninstall` subprocesses (that is a side effect, not
// content generation — orchestration). It DOES perform the workspace
// /workspace/mise.toml retired-tool surgery when that file exists, matching the
// Python writer's in-place edits.
// injected tools come from YOLO_MISE_TOOLS (a JSON object); retired tools come
// from agents.AllMiseRetire.
func GenerateMiseConfig(e *Env) error {
	configPath := filepath.Join(e.MiseConfigDir(), "config.toml")

	injected := loadInjectedTools(e)
	retired := agents.AllMiseRetire

	if !pathExists(configPath) {
		if err := os.MkdirAll(e.MiseConfigDir(), 0o755); err != nil {
			return err
		}
		// merged = {**base_tools, **injected_tools}: base order first, then any
		// injected keys not already present appended in injected order; an
		// injected key that matches a base key updates in place.
		merged := jsonx.NewOrderedMap()
		for _, bt := range miseBaseTools {
			merged.Set(bt.tool, bt.version)
		}
		for _, k := range injected.Keys() {
			v, _ := injected.Get(k)
			merged.Set(k, v)
		}
		lines := []string{"[tools]"}
		for _, tool := range merged.Keys() {
			v, _ := merged.Get(tool)
			lines = append(lines, miseTomlKey(tool)+" = \""+miseValueString(v)+"\"")
		}
		content := strings.Join(lines, "\n") + "\n"
		return writeInPlaceString(configPath, content)
	}

	// Update existing config.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	content := string(raw)
	changed := false

	// Self-heal: drop duplicate tool-key lines (keep first).
	keyRe := regexp.MustCompile(`^\s*"?([^"\s=]+)"?\s*=`)
	seen := map[string]struct{}{}
	var deduped []string
	for _, line := range splitKeepNL(content) {
		if m := keyRe.FindStringSubmatch(line); m != nil {
			key := m[1]
			if _, ok := seen[key]; ok {
				changed = true
				continue
			}
			seen[key] = struct{}{}
		}
		deduped = append(deduped, line)
	}
	if changed {
		content = strings.Join(deduped, "")
	}

	// Remove retired tools.
	for _, tool := range retired {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(tool) + `\s*=\s*"[^"]*"\n?`)
		newContent := pattern.ReplaceAllString(content, "")
		if newContent != content {
			content = newContent
			changed = true
		}
	}

	// Migration cleanup (docs/design/config-migration-to-prism.md §4.1): the
	// baked runtimes (node, python, go) used to be written here as base tools;
	// now that they are all baked into the image, an existing jail's persistent
	// config.toml still carries a stale `node = "22"` / `python = "3.13"` /
	// `go = "latest"` line that shadows the baked /bin/<tool> — the exact
	// version-skew the bake was meant to end. Strip that yolo-written default,
	// but ONLY when the tool is not an intentional pin: an entry in
	// YOLO_MISE_TOOLS (injected) or /workspace/mise.toml is a deliberate
	// per-workspace override and MUST be preserved (mise then installs it and
	// its shim wins — the one supported case). The injected-tools re-application
	// below re-adds any YOLO_MISE_TOOLS pin after this pass as a second safety
	// net.
	for _, tool := range bakedRuntimes {
		if _, pinned := injected.Get(tool); pinned {
			continue // intentional YOLO_MISE_TOOLS override — keep it
		}
		if workspacePinsTool(tool) {
			continue // intentional /workspace/mise.toml override — keep it
		}
		pattern := regexp.MustCompile(`(?m)^"?` + regexp.QuoteMeta(tool) + `"?\s*=\s*"[^"]*"\n?`)
		newContent := pattern.ReplaceAllString(content, "")
		if newContent != content {
			content = newContent
			changed = true
		}
	}

	// Ensure base tools present and not "system".
	for _, bt := range miseBaseTools {
		tk := miseTomlKey(bt.tool)
		pattern := regexp.MustCompile(`(?m)^"?` + regexp.QuoteMeta(bt.tool) + `"?\s*=\s*"[^"]*"`)
		loc := pattern.FindStringIndex(content)
		if loc == nil {
			content = strings.TrimRight(content, "\n") + "\n" + tk + " = \"" + bt.version + "\"\n"
			changed = true
		} else if strings.Contains(content[loc[0]:loc[1]], `"system"`) {
			content = content[:loc[0]] + tk + " = \"" + bt.version + "\"" + content[loc[1]:]
			changed = true
		}
	}

	// Injected tools always override.
	for _, tool := range injected.Keys() {
		v, _ := injected.Get(tool)
		version := miseValueString(v)
		tk := miseTomlKey(tool)
		pattern := regexp.MustCompile(`(?m)^"?` + regexp.QuoteMeta(tool) + `"?\s*=\s*"[^"]*"`)
		if pattern.MatchString(content) {
			// ReplaceAllLiteralString, NOT ReplaceAllString: Go's ReplaceAllString
			// expands `$1`/`$name` in the replacement, corrupting a mise version
			// (or tool key) containing `$`. Python's re.sub inserts this literal
			// f-string verbatim (it expands backslash refs, never `$`), so the
			// literal replacement is the faithful match (audit 2026-07-18 §C).
			newContent := pattern.ReplaceAllLiteralString(content, tk+` = "`+version+`"`)
			if newContent != content {
				content = newContent
				changed = true
			}
		} else {
			content = strings.TrimRight(content, "\n") + "\n" + tk + " = \"" + version + "\"\n"
			changed = true
		}
	}

	if changed {
		if err := writeInPlaceString(configPath, content); err != nil {
			return err
		}
	}

	// Retire from the workspace mise.toml if present.
	wsMise := workspaceMisePath
	if pathExists(wsMise) {
		wsRaw, err := os.ReadFile(wsMise)
		if err == nil {
			wsContent := string(wsRaw)
			wsChanged := false
			for _, tool := range retired {
				pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(tool) + `\s*=\s*"[^"]*"\n?`)
				newWs := pattern.ReplaceAllString(wsContent, "")
				if newWs != wsContent {
					wsContent = newWs
					wsChanged = true
				}
			}
			if wsChanged {
				if err := writeInPlaceString(wsMise, wsContent); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// workspacePinsTool reports whether /workspace/mise.toml pins the given tool in
// its [tools] table — an intentional per-workspace override the migration
// cleanup must never strip. A missing/unreadable workspace mise.toml means no
// pin (false). It matches a bare or quoted key at line start, e.g. `node = ` or
// `"node" = ` (mirroring the retire surgery's key handling), so it does not
// false-match a substring like `nodejs`.
func workspacePinsTool(tool string) bool {
	raw, err := os.ReadFile(workspaceMisePath)
	if err != nil {
		return false
	}
	pattern := regexp.MustCompile(`(?m)^"?` + regexp.QuoteMeta(tool) + `"?\s*=`)
	return pattern.Match(raw)
}

// loadInjectedTools parses YOLO_MISE_TOOLS as a JSON object (default {}).
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

// miseValueString renders an injected tool's version value as Python's f-string
// interpolation would (str(value)); versions are always strings in practice, so
// non-strings fall back to pyStr for completeness.
func miseValueString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return pyStr(v)
}

// splitKeepNL splits into lines keeping the trailing newline on each, mirroring
// Python's str.splitlines(keepends=True).
func splitKeepNL(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
