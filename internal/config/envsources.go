package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ParseDotenv parses KEY=VALUE dotenv content into an ordered map.
// Comment/blank lines ignored; `export ` prefix stripped; matching
// single/double quote wrappers removed; malformed lines (no `=`, invalid var
// name) silently skipped. Returns keys in first-seen order (later assignment to
// an existing key updates value, keeps position).
func ParseDotenv(text string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, raw := range splitLines(text) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimLeft(line[len("export "):], " \t\n\r\v\f")
		}
		if !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		if !envVarNameRe.MatchString(key) {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if len(value) >= 2 && value[0] == value[len(value)-1] &&
			(value[0] == '\'' || value[0] == '"') {
			value = value[1 : len(value)-1]
		}
		out.Set(key, value)
	}
	return out
}

// AnchorEnvSources rewrites every RELATIVE env_sources file entry in cfg to an absolute
// path under dir — which the loader passes as the directory of the file that DECLARED
// the entry. It is the load-time half of the ruling that a relative path means "beside
// my declaring file" (agent-credentials.md, OQ-E1/E2, 2026-08-30), the same
// convention `include_if_found` already uses.
//
// Doing this AT LOAD TIME is what makes per-file anchoring possible at all: by the time
// the merge concatenates the user config, its includes, any layer, and the workspace
// config into one list, per-entry provenance is gone — but absolute paths survive a
// concat, so each file anchors its own entries before the lists ever meet. The include
// case is the proof the anchor must be per-file: an include's relative entry anchored at
// the TOP config's dir would look beside the wrong file.
//
// Anchoring is why the WORKSPACE config's entries do not move: yolo-jail.jsonc sits at
// the workspace root, so beside-the-file IS workspace-relative for them. The entry whose
// meaning changes is a USER-config entry inside a jail launch — workspace-relative
// before 2026-08-30, config-dir now — which was the fix's whole point: a cloned repo
// could otherwise put a prod.env in the workspace that a user config's relative entry
// fed into the jail's environment.
//
// Absolute and ~-relative entries pass through untouched, and inline dict entries are
// not paths. Unanchored relative entries that reach ResolveEnvSources anyway (a
// pre-ruling assembled snapshot read verbatim, a hand-built config) fall back to the
// workspace root — the pre-ruling behavior, deliberately, for artifacts a newer loader
// never touched.
func AnchorEnvSources(cfg *jsonx.OrderedMap, dir string) {
	anchorEnvSourceList(cfg, "env_sources", dir)
}

func anchorReadableEnvSources(cfg *jsonx.OrderedMap, dir string) {
	AnchorEnvSources(cfg, dir)
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	if envValue, ok := cfg.Get("env"); ok {
		if env, ok := asMap(envValue); ok {
			anchorEnvSourceList(env, "sources", dir)
			anchorEnvSourceList(env, "files", dir)
		}
	}
}

func anchorEnvSourceList(cfg *jsonx.OrderedMap, key, dir string) {
	if cfg == nil {
		return
	}
	value, present := cfg.Get(key)
	if !present {
		return
	}
	list, ok := asList(value)
	if !ok {
		return
	}
	changed := false
	out := make([]any, len(list))
	for i, entry := range list {
		if s, isString := entry.(string); isString && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "~") {
			out[i] = filepath.Join(dir, s)
			changed = true
			continue
		}
		out[i] = entry
	}
	if changed {
		cfg.Set(key, out)
	}
}

// envEntry is one entry of the ordered list the env resolver walks, with the config key that
// spelled it: a dotenv path (a string) or an inline table of assignments, a nil value being a
// removal.
type envEntry struct {
	key   string
	value any
}

func (e envEntry) describeInline(m *jsonx.OrderedMap) string {
	label := "inline dict"
	if e.key != "env_sources" && e.key != "env.sources" {
		label = e.key
	}
	return label + " (keys: " + strings.Join(m.Keys(), ", ") + ")"
}

// envEntries is the ONE ordered entry list every env reader walks, whichever spelling the
// config used, so the legacy and readable forms cannot drift apart:
//
//  1. every `env_sources` entry, in order (the legacy JSONC spelling), then every `env.sources`
//     entry (its readable spelling, LowerEnvSources), all layers' in layer order;
//  2. every `env.files` path, in order (the readable spelling of its file entries);
//  3. `env.set` as one inline table, applied after every file — the reading the migrator
//     converts by (configmigrate.lowerEnvSources) and config-project.md §1 shows;
//  4. `env.unset` as one inline table of removals, applied last, so an unset wins over a
//     set of the same name from any layer (readable-config-plan.md's "unset precedence",
//     RC-D23).
//
// The merge has already joined the layers: `env.files` is the user's entries then the
// workspace's, `env.set` is later-wins per name, and `env.unset` is their union.
func envEntries(cfg *jsonx.OrderedMap) []envEntry {
	var out []envEntry
	for _, entry := range getListOrNilFalsy(cfg, "env_sources") {
		out = append(out, envEntry{key: "env_sources", value: entry})
	}
	env, ok := envTable(cfg)
	if !ok || env == nil {
		return out
	}
	// env.sources is env_sources' readable spelling and holds its slot (RC-D50): every read of a
	// legacy file lowers env_sources into it (lowerLegacyEnvSources), so the merge has already
	// joined every layer's list in layer order, whichever format each layer is written in.
	for _, entry := range getListOrNilFalsy(env, "sources") {
		if _, isPath := asStr(entry); isPath {
			out = append(out, envEntry{key: "env.sources", value: entry})
			continue
		}
		if table, isTable := asMap(entry); isTable {
			if inline, ok := envSourcesInline(table); ok {
				out = append(out, envEntry{key: "env.sources", value: inline})
			}
		}
	}
	for _, entry := range getListOrNilFalsy(env, "files") {
		if _, isPath := asStr(entry); isPath {
			out = append(out, envEntry{key: "env.files", value: entry})
		}
	}
	if setValue, present := env.Get("set"); present {
		if set, ok := asMap(setValue); ok {
			assignments := jsonx.NewOrderedMap()
			for _, k := range set.Keys() {
				v, _ := set.Get(k)
				if vs, ok := asStr(v); ok {
					assignments.Set(k, vs)
				}
			}
			if assignments.Len() > 0 {
				out = append(out, envEntry{key: "env.set", value: assignments})
			}
		}
	}
	removals := jsonx.NewOrderedMap()
	for _, name := range getListOrNilFalsy(env, "unset") {
		if s, ok := asStr(name); ok {
			removals.Set(s, nil)
		}
	}
	if removals.Len() > 0 {
		out = append(out, envEntry{key: "env.unset", value: removals})
	}
	return out
}

// EnvEntries is envEntries for a reader outside this package that must compare what the
// launch resolves without reading any file — the migration's equality proof. Each entry is a
// path string or an inline table whose nil values are removals.
func EnvEntries(cfg *jsonx.OrderedMap) []any {
	entries := envEntries(cfg)
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = e.value
	}
	return out
}

// HasEnvFileEntry reports whether cfg names any dotenv file, in either spelling.
func HasEnvFileEntry(cfg *jsonx.OrderedMap) bool {
	for _, e := range envEntries(cfg) {
		if _, ok := asStr(e.value); ok {
			return true
		}
	}
	return false
}

// HasEnvEntries reports whether cfg configures any environment, in either spelling.
func HasEnvEntries(cfg *jsonx.OrderedMap) bool {
	return len(envEntries(cfg)) > 0
}

// ResolveEnvSourcePath expands ~, passes absolute paths through, and resolves
// relative paths against the workspace root — the FALLBACK for entries no loader
// anchored (AnchorEnvSources); every config loaded from disk arrives anchored.
func ResolveEnvSourcePath(entry, workspace string) string {
	expanded := expandUser(entry)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	joined := filepath.Join(workspace, expanded)
	if r, err := resolve(joined); err == nil {
		return r
	}
	return joined
}

// ResolveEnvSources iterates the config's env entries in order (envEntries: env_sources, then
// the readable [env] table's files, set and unset) — inline dicts apply
// directly; string entries read as dotenv files; later
// entries override earlier; missing/unreadable files warn (via warn) and skip.
// Returns the final env map as an OrderedMap (later-wins on key, position kept).
func ResolveEnvSources(workspace string, config *jsonx.OrderedMap, warn Warn) *jsonx.OrderedMap {
	merged, _ := ResolveEnvSourcesFull(workspace, config, warn)
	return merged
}

// ResolveEnvSourcesFull is BOTH answers from ONE pass — the assignments map and the
// removal list — for callers that need the two together. Calling ResolveEnvSources and
// EnvSourceRemovals separately runs the pass twice, reading every dotenv file twice and
// warning twice; a caller composing an environment (the host notch's hostEnvVars) uses
// this instead.
// One pass, because the two answers are defined by the SAME ordering and computing them
// separately let them disagree. Later entries win for both: an assignment after a null
// sets the variable and drops the removal; a null after an assignment removes it and drops
// the assignment. That holds across entry KINDS too — a dotenv file listed after an inline
// null cancels that null, which a dict-only scan could not see.
func ResolveEnvSourcesFull(workspace string, config *jsonx.OrderedMap, warn Warn) (*jsonx.OrderedMap, []string) {
	if warn == nil {
		warn = func(string) {} // discard warnings by default
	}
	merged := jsonx.NewOrderedMap()
	removed := map[string]bool{}
	var order []string
	remove := func(k string) {
		if !removed[k] {
			order = append(order, k)
		}
		removed[k] = true
		merged.Delete(k)
	}
	assign := func(k, v string) {
		removed[k] = false
		merged.Set(k, v)
	}

	for _, e := range envEntries(config) {
		entry := e.value
		if em, ok := asMap(entry); ok {
			for _, k := range em.Keys() {
				v, _ := em.Get(k)
				if v == nil {
					// The REMOVAL spelling. Only an inline dict can express one: a
					// dotenv FILE has no syntax for "unset", and inventing one would
					// make yolo's dialect differ from everyone else's.
					remove(k)
					continue
				}
				// Decoded JSON keys are always strings, so only the value type gates:
				// apply the entry only when the value is a string.
				if vs, vok := asStr(v); vok {
					assign(k, vs)
				}
			}
			continue
		}
		if s, ok := asStr(entry); ok {
			p := ResolveEnvSourcePath(s, workspace)
			data, err := os.ReadFile(p)
			if err != nil {
				if os.IsNotExist(err) {
					// The label names no key: a legacy env_sources list reads lowered to env.files
					// or env.sources (RC-D50), so the key a reader holds is not always the one
					// the file wrote. The path names the entry in every spelling.
					warn("dotenv file not found, skipping: " + s + " (resolved to " + p + ")")
				} else {
					warn("dotenv file unreadable, skipping: " + s + ": " + err.Error())
				}
				continue
			}
			parsed := ParseDotenv(string(data))
			for _, k := range parsed.Keys() {
				v, _ := parsed.Get(k)
				vs, _ := v.(string)
				assign(k, vs)
			}
		}
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		if removed[k] {
			out = append(out, k)
		}
	}
	return merged, out
}

// HydrateEnvSources is ResolveEnvSourcesFull's two answers in ONE ordered map: the assignments
// in hydration order, then each removal as a nil value. It is the form a jail launch keeps the
// hydration in (run's packChannel.userEnv), so the attach and pack-skew paths that compose the
// channel again from that map keep the removals the first pass found, rather than walking every
// env_sources entry a second time. SplitHydratedEnvSources reads it back.
//
// The removals cross into a jail at all because a null ranks with env_sources in the one ordered
// composition (packload's envcompose.go): it takes out a pack env fold's value of the same name,
// at every notch, where the jail used to drop it.
func HydrateEnvSources(workspace string, config *jsonx.OrderedMap, warn Warn) *jsonx.OrderedMap {
	merged, removals := ResolveEnvSourcesFull(workspace, config, warn)
	for _, k := range removals {
		merged.Set(k, nil)
	}
	return merged
}

// SplitHydratedEnvSources is HydrateEnvSources' map back as its two answers: the string
// assignments, in order, and the names held as nil, the removals. A map of assignments alone
// (any caller that hydrated through ResolveEnvSources) yields no removals. A nil map yields an
// empty one.
func SplitHydratedEnvSources(m *jsonx.OrderedMap) (*jsonx.OrderedMap, []string) {
	assignments := jsonx.NewOrderedMap()
	var removals []string
	if m == nil {
		return assignments, nil
	}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		if v == nil {
			removals = append(removals, k)
			continue
		}
		assignments.Set(k, v)
	}
	return assignments, removals
}

// DescribeEnvSources returns one description per env_sources entry in cfg, in order: a
// file entry as the path it resolves to, an inline dict as the keys it assigns. Empty
// when cfg declares none.
//
// It is the "here is where I looked" half of the launch credential pre-flight
// (docs/reference/providers.md#the-credential-preflight): a refusal that says only "ZAI_API_KEY is not set"
// sends the reader hunting through their config for the channel that was supposed to
// deliver it, which is the debugging nightmare providers.md#the-credential-preflight records. Naming the entries consulted
// is the same message discipline as the reachability witness's — say what was checked,
// not only what failed.
func DescribeEnvSources(workspace string, cfg *jsonx.OrderedMap) []string {
	var out []string
	for _, e := range envEntries(cfg) {
		entry := e.value
		if em, ok := asMap(entry); ok {
			out = append(out, e.describeInline(em))
			continue
		}
		if s, ok := asStr(entry); ok {
			out = append(out, ResolveEnvSourcePath(s, workspace))
		}
	}
	return out
}

// EnvSourceRemovals returns the variable names an `env_sources` entry asks to be REMOVED
// — the keys whose value is JSON null.
//
// # Why a removal exists at all
//
// ResolveEnvSources returns a map of assignments, and a map cannot express "and delete
// this one". A null is therefore skipped there, which is right for the JAIL: a container
// starts from an empty environment, so "removed" is already the default state of anything
// yolo does not pass in.
//
// On the HOST it is the opposite. `yolo host -- claude` starts from the invoking shell's
// os.Environ(), which may well carry an AWS_PROFILE that has to go — the motivating case
// in docs/reference/host-agent-environment.md §2.2, where the hand-written wrapper's first
// act is `unset AWS_PROFILE`. No config SURFACE can express a removal at all, which is
// one of the reasons the process-env channel has to exist (§1 P1).
//
// A removal is not an empty assignment: `AWS_PROFILE=` and no AWS_PROFILE behave
// differently in every AWS SDK, which is why this returns names to unset rather than
// pairs to set empty.
//
// It shares ONE pass with ResolveEnvSources (resolveEnvSources below) so the two can never
// disagree about ordering. That mattered: an earlier version walked only the inline dicts,
// so a dotenv FILE listed after a null did assign the variable in the map while the
// removal still fired — later-wins in one half and earlier-wins in the other, and the
// unset silently won.
func EnvSourceRemovals(workspace string, config *jsonx.OrderedMap, warn Warn) []string {
	_, removals := ResolveEnvSourcesFull(workspace, config, warn)
	return removals
}

// expandUser expands a leading "~". Only "~" and "~/..." are expanded (a
// "~user" form is left untouched, matching the common case). HOME resolution
// uses $HOME when set, else the passwd entry — the same HOME/pwd logic
// internal/paths uses.
func expandUser(p string) string {
	if len(p) == 0 || p[0] != '~' {
		return p
	}
	// Find end of the ~ component.
	i := 1
	for i < len(p) && p[i] != '/' {
		i++
	}
	if i == 1 {
		// bare "~" or "~/..." — home with trailing slashes stripped + the rest,
		// or "/" when that is empty.
		home := strings.TrimRight(homeForExpand(), "/")
		res := home + p[i:]
		if res == "" {
			return "/"
		}
		return res
	}
	// "~user/..." — best effort: leave untouched (config env_sources use ~/…).
	return p
}

// homeForExpand returns $HOME if set (even empty), else the passwd entry. Empty
// HOME with "~/x" yields "/x" after the rstrip+`or "/"` in expandUser.
func homeForExpand() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		return h
	}
	if u, err := userHomeDir(); err == nil {
		return u
	}
	return ""
}
