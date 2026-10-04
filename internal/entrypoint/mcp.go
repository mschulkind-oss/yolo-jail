package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// (empty) merged with the dict in YOLO_LSP_SERVERS. Returns an OrderedMap so
// insertion order (defaults then overrides) is preserved for byte-parity.
func LoadLSPServers(e *Env) *jsonx.OrderedMap {
	servers := jsonx.NewOrderedMap() // DEFAULT_LSP_SERVERS == {}
	extraJSON := e.Getenv("YOLO_LSP_SERVERS")
	if extraJSON == "" {
		return servers
	}
	decoded, err := jsonx.Decode([]byte(extraJSON))
	if err != nil {
		return servers
	}
	extra, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return servers
	}
	for _, k := range extra.Keys() {
		v, _ := extra.Get(k)
		servers.Set(k, v)
	}
	return servers
}

// ${VAR} INTERPOLATION WAS REMOVED (2026-08-03), deliberately and by ruling. Do not add it
// back as a convenience; the reason is structural, not stylistic.
//
// yolo used to expand ${VAR} in an MCP server's `env`, `headers`, and `url` against e.Vars
// (hydrated from env_sources at boot). Two independent reasons it was wrong:
//
//  1. THE VALUE HAD NO LAYER. Every other value in a rendered surface has a provenance
//     answer — defaults / host / config-overlay:<pack> / managed / computed / retired:<layer>
//     — and the whole host-render story depends on being able to ask "who set this key?".
//     An interpolated secret entered the file without passing through any layer, so
//     `config diff` could not attribute it and the orphan-key prune could not tell yolo's
//     output from the user's. It was a value sneaking in the side door.
//  2. IT SOURCED CONFIG CONTENT FROM PROCESS ENV AT RENDER TIME. env_sources is a
//     jail-PROVISIONING input (what the container's environment contains). Using it as a
//     rendering input made the bytes written to a config file depend on the ambient
//     environment of whoever ran the render — the one input the confinement model
//     deliberately does not treat as configuration.
//
// It was also unnecessary, which is what made the tradeoff one-sided. hydrateEnvFromUserEnvFile
// does os.Setenv for every env_sources var before any generator runs (boot.go), so those
// variables are already in the environment of every process the entrypoint spawns — verified:
// a non-interactive `sh -c` and a bare execve'd python both see them, `env -i` clears them
// (proving process-env inheritance rather than a sourced rc file), and boot-time daemons carry
// them in /proc/<pid>/environ. So the consuming agent can resolve ${VAR} itself. yolo resolving
// first bought nothing and wrote a plaintext secret into a config file it does not own.
//
// Consequence to expect: yolo writes the literal ${VAR} and the consumer resolves it or does
// not. If it does not, that is the consumer's decision to own, not a gap for yolo to paper
// over. If a real need for declared secret references appears, the honest form is a LAYER with
// provenance resolved at launch — not a string substitution during render.

// bakedChromiumPath is the path the IMAGE gives chromium — the /usr/bin symlink
// mkBinPathLinks lays down. It is created inside that function's `withChromium` block, and
// `binPathLinksLean = mkBinPathLinks { withChromium = false; }` is what `mkOciImage` picks
// for `withExtras = false`, so on `.#ociImageLean` this path does not exist (flake.nix).
const bakedChromiumPath = "/usr/bin/chromium"

// chromiumExecutablePath is where chromium is for THIS launch, and the wired
// chrome-devtools entry RESOLVES it rather than pinning it because C5 moved the answer.
//
// This argv used to carry the literal bakedChromiumPath. A YOLO_STORE_PACKAGES=1 launch
// builds `.#ociImageLean`, which sets `variantFullPackages = []` and takes the lean
// bin-path links — so it bakes neither chromium nor the /usr/bin symlink — and delivers
// `fullPackages` (chromium among them) as the `.#yoloImageExtras` store profile, whose
// `bin/` the boot links into the /run/yolo/packages farm (storepackages.go;
// docs/reference/image-staging-vs-baking.md, "Store-delivered packages"). The pinned path
// is therefore absent on exactly the launches that have a chromium, and the MCP server was
// handed a path with nothing behind it.
//
// THE RESOLUTION IS imageProbePath's, NOT A SECOND SEARCH ORDER, and that is the whole
// point of reusing it: it is already the one place that knows the farm counts as
// provision (gated on StoreProfiles, so a launch that bakes is unaffected) while the
// per-home install prefixes do not. A private search order here is how two answers to
// "where did this launch's packages come from" start disagreeing.
//
// imageProbePath rather than agentPath, and the reason is the ANSWER TIME. This value is
// baked into a config file by a boot-time generator, and every dir imageProbePath names is
// already populated when it is asked: the image's own bins were there before PID 1 ran, and
// generate_store_packages is the FIRST generator in the boot's list precisely so its farm
// exists before anything stats it. agentPath's extra entries — the install prefixes and the
// mise shim dir — are the ones the bootstrap has not filled in yet on a cold boot (the same
// ordering fact declaredMiseBins documents), so asking them here would answer from a
// filesystem that is still being built. Nothing installs chromium into them anyway.
//
// The fat `chrome-devtools-mcp-wrapper` GenerateMCPWrappers writes has resolved chromium at
// RUN time all along — in shell, `[ -x /usr/bin/chromium ]` then `command -v chromium` — and
// nothing yolo generates spawns it: no `command` names it, and its only other references are
// the boot catalog's declared-orphan entry and tests. So the idea reaching this argv is not a
// new one; it is the one that has been sitting unreachable in the same package.
//
// The fallback is bakedChromiumPath rather than "" or a dropped flag: when nothing
// resolves, the entry stays byte-for-byte what it always was, so a jail with no chromium
// at all fails the way it already failed instead of a new way.
func chromiumExecutablePath(e *Env) string {
	if p := lookPathIn(imageProbePath(e), "chromium"); p != "" {
		return p
	}
	return bakedChromiumPath
}

func (e *Env) chromeDevtoolsArgs() []any {
	npmBin := e.NpmBin()
	return []any{
		filepath.Join(npmBin, "chrome-devtools-mcp"),
		"--headless",
		"--isolated",
		"--executablePath",
		chromiumExecutablePath(e),
		"--chrome-arg=--no-sandbox",
		"--chrome-arg=--disable-setuid-sandbox",
		"--chrome-arg=--disable-gpu",
	}
}

// LoadMCPServers presets (opt-in via
// YOLO_MCP_PRESETS) merged with YOLO_MCP_SERVERS (overrides / additions /
// null-removals), then requires_env gating.
// Returns an OrderedMap whose key order follows insertion order.
//
// NO ${VAR} INTERPOLATION HAPPENS HERE. This line used to claim it did; the
// interpolation was REMOVED on 2026-08-03 by ruling, and the file header (see
// the block at the top of this file) says why and why not to add it back.
// yolo writes the literal ${VAR} and the consuming agent resolves it.
func (e *Env) LoadMCPServers() *jsonx.OrderedMap {
	servers, skipped := e.mcpServersWith(e.Lookup)
	for _, s := range skipped {
		e.warn(s.notice())
	}
	return servers
}

// mcpSkip is one server the requires_env gate removed, and the variables it lacked.
type mcpSkip struct {
	name    string
	missing []string
}

func (s mcpSkip) notice() string {
	return "notice: MCP server '" + s.name + "' skipped — required env not set: " +
		strings.Join(s.missing, ", ")
}

// mcpServersWith is LoadMCPServers with the requires_env gate asking lookup rather than the
// boot's environment, returning what it removed instead of warning. The per-agent tables
// (mcpTablesFor) ask a lookup that also sees one agent's own env file.
func (e *Env) mcpServersWith(lookup func(string) (string, bool)) (*jsonx.OrderedMap, []mcpSkip) {
	mcpWrappers := e.McpWrappersBin()
	npmBin := e.NpmBin()

	presets := map[string]*jsonx.OrderedMap{
		"chrome-devtools": func() *jsonx.OrderedMap {
			m := jsonx.NewOrderedMap()
			m.Set("command", filepath.Join(mcpWrappers, "node"))
			m.Set("args", e.chromeDevtoolsArgs())
			return m
		}(),
		"sequential-thinking": func() *jsonx.OrderedMap {
			m := jsonx.NewOrderedMap()
			m.Set("command", filepath.Join(mcpWrappers, "node"))
			m.Set("args", []any{filepath.Join(npmBin, "mcp-server-sequential-thinking")})
			return m
		}(),
	}

	servers := jsonx.NewOrderedMap()

	// Expand requested presets (order follows the YOLO_MCP_PRESETS list) — UNLESS this
	// environment does not generate the preset wrappers (Env.SkipMCPPresets, macos-user). Every
	// preset's `command` is the mcp-wrappers `node`, so an entry here would send each agent to
	// a file that is never written: the server is left out instead, and the bootstrap's
	// mcp_presets_declined warning names it. The user's own mcp_servers below still merge,
	// null-removals included.
	if presetsJSON := e.Getenv("YOLO_MCP_PRESETS"); presetsJSON != "" && !e.SkipMCPPresets {
		if decoded, err := jsonx.Decode([]byte(presetsJSON)); err == nil {
			if arr, ok := decoded.([]any); ok {
				for _, n := range arr {
					if name, isStr := n.(string); isStr {
						if p, exists := presets[name]; exists {
							servers.Set(name, p)
						}
					}
				}
			}
		}
	}

	// Merge custom servers (overrides, additions, null-removals).
	if extraJSON := e.Getenv("YOLO_MCP_SERVERS"); extraJSON != "" {
		if decoded, err := jsonx.Decode([]byte(extraJSON)); err == nil {
			if extra, ok := decoded.(*jsonx.OrderedMap); ok {
				for _, name := range extra.Keys() {
					cfg, _ := extra.Get(name)
					if cfg == nil {
						servers.Delete(name)
					} else if _, isMap := cfg.(*jsonx.OrderedMap); isMap {
						servers.Set(name, cfg)
					}
				}
			}
		}
	}

	// Conditional loading: requires_env gate. Iterate a snapshot of the keys,
	// mutating servers as we go.
	var skipped []mcpSkip
	for _, name := range append([]string(nil), servers.Keys()...) {
		v, _ := servers.Get(name)
		cfg, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		reqVal, has := cfg.Get("requires_env")
		if !has {
			continue
		}
		required, ok := reqVal.([]any)
		if !ok {
			continue
		}
		var missing []string
		for _, rv := range required {
			if s, isStr := rv.(string); isStr {
				if val, present := lookup(s); !present || val == "" {
					missing = append(missing, s)
				}
			}
		}
		if len(missing) > 0 {
			skipped = append(skipped, mcpSkip{name: name, missing: missing})
			servers.Delete(name)
		} else {
			// Strip requires_env, preserving other keys' order.
			stripped := jsonx.NewOrderedMap()
			for _, k := range cfg.Keys() {
				if k == "requires_env" {
					continue
				}
				kv, _ := cfg.Get(k)
				stripped.Set(k, kv)
			}
			servers.Set(name, stripped)
		}
	}

	return servers, skipped
}

// mcpTables is the MCP server table each surface renders: the jail-wide one, and, for each
// agent the credential gate wrote an env file for, that agent's own.
type mcpTables struct {
	shared   *jsonx.OrderedMap
	perAgent map[string]*jsonx.OrderedMap
}

// loadMCPTables evaluates requires_env PER AGENT (providers.md, OQ-CN6).
//
// WHY PER AGENT. A server's `requires_env` names the variables its entry needs, and yolo
// writes the entry's ${VAR} references literally for the consuming agent to resolve from ITS
// environment (no interpolation, above). Since the credential gate, a provider-claimed
// variable (ZAI_API_KEY, the AWS pair) reaches only the agent that selected that provider, in
// ~/.config/yolo-agent-env/<agent>.sh, and never the boot's environment — so a jail-wide gate
// skipped such a server on every launch, even for the agent that holds the key. The gate now
// asks, for each agent with a file, the boot's environment plus that file: the server is
// written into that agent's config and no other, which is exactly the set of agents whose
// process can resolve it.
//
// ONE NOTICE PER SERVER, jail-wide: skipped everywhere keeps the old line; configured for
// some agents only names them.
//
// "The boot's environment" is scopedMCPView's, not e's own, and on the container the two are
// the same. On macos-user the bootstrap also hydrated the session env file, which is the
// LAUNCHED agent's environment and so carries that agent's scoped values too; the view leaves
// those out, so they reach the agent whose file sets them and no other.
func loadMCPTables(e *Env) mcpTables {
	view := scopedMCPView(e)
	shared, skipped := e.mcpServersWith(view.Lookup)
	t := mcpTables{shared: shared, perAgent: map[string]*jsonx.OrderedMap{}}
	agents := agentsWithEnvFiles(e)
	for _, agent := range agents {
		own, _ := e.mcpServersWith(agentEnvLookup(view, agent))
		t.perAgent[agent] = own
	}
	for _, s := range skipped {
		var got []string
		for _, agent := range agents {
			if _, ok := t.perAgent[agent].Get(s.name); ok {
				got = append(got, agent)
			}
		}
		if len(got) == 0 {
			e.warn(s.notice())
			continue
		}
		e.warn("notice: MCP server '" + s.name + "' configured only for " +
			strings.Join(got, ", ") + " — its required env (" + strings.Join(s.missing, ", ") +
			") reaches only the agent that selected its provider")
	}
	return t
}

// scopedMCPView is the environment loadMCPTables asks for the jail-wide table, and under each
// agent's own file: e itself, unless the macos-user bootstrap hydrated a session env file
// (e.sessionEnvKeys) holding a key some agent's per-agent env file names. Such a key is dropped
// from the view, because the file it came from is one agent's launch environment: the
// credential gate scoped the value to that agent, which is why its per-agent file sets it too
// (the launch writes every profiled agent's file before the bootstrap runs). Taken into the
// shared view it put a server gated on it in every agent's config and lost the "configured only
// for" notice; dropped, the server reaches the agents whose file sets the key, as it does in a
// container, whose boot environment never holds a scoped value.
//
// A key BOTH shared and named by some agent's file is dropped too, which can only withhold a
// server, never grant one (fail closed), and the notice then names the agents that have it.
//
// The view is a separate Env holding only what agentEnvLookup and Lookup read (the home and
// the variables), so nothing the gate does writes through it.
func scopedMCPView(e *Env) *Env {
	if len(e.sessionEnvKeys) == 0 {
		return e
	}
	scoped := agentEnvFileNames(e)
	var drop []string
	for k := range e.sessionEnvKeys {
		if scoped[k] {
			drop = append(drop, k)
		}
	}
	if len(drop) == 0 {
		return e
	}
	vars := make(map[string]string, len(e.Vars))
	for k, v := range e.Vars {
		vars[k] = v
	}
	for _, k := range drop {
		delete(vars, k)
	}
	return &Env{Home: e.Home, Vars: vars}
}

// agentEnvFileNameRe finds each variable a per-agent env file line sets or removes: the
// writer's def-form and plain exports, the `case … ) export K=… ;; esac` a composed value
// takes when yolo set its name elsewhere, and `unset K` in either shape
// (internal/cli/run's agentEnvFileContent). Matching a name inside a quoted value as well can
// only drop one more key from the shared view, which withholds rather than grants.
var agentEnvFileNameRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:export|unset)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// agentEnvFileNames is the set of variable names any agent's env file under this home sets or
// removes (agentsWithEnvFiles; scopedMCPView says why). A file it cannot read names nothing.
func agentEnvFileNames(e *Env) map[string]bool {
	out := map[string]bool{}
	for _, agent := range agentsWithEnvFiles(e) {
		data, err := os.ReadFile(AgentEnvFile(e.Home, agent))
		if err != nil {
			continue
		}
		for _, line := range splitLines(string(data)) {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for _, m := range agentEnvFileNameRe.FindAllStringSubmatch(line, -1) {
				out[m[1]] = true
			}
		}
	}
	return out
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// LoadMCPPresetNames returns the enabled MCP preset names from YOLO_MCP_PRESETS, in
// config order. Empty when none are enabled.
//
// Split out so the bootstrap script's npm install can be gated on the SAME
// declaration that builds the server table (D6), rather than hardcoding a package
// list beside it and letting the two drift.
func (e *Env) LoadMCPPresetNames() []string {
	presetsJSON := e.Getenv("YOLO_MCP_PRESETS")
	if presetsJSON == "" {
		return nil
	}
	decoded, err := jsonx.Decode([]byte(presetsJSON))
	if err != nil {
		return nil
	}
	arr, ok := decoded.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range arr {
		if name, isStr := v.(string); isStr {
			out = append(out, name)
		}
	}
	return out
}
