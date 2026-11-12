package cli

// configwhy.go is `yolo config why <key>` (docs/design/config-commands.md §1 and §4): the
// effective value of one key, every file:line that sets it (highest precedence first, from
// the loader's own provenance record, config.Sources), and what would apply without it.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const configWhyUsage = `usage: yolo config why <key>

Print the effective value of one config key, every file and line that sets it (highest
precedence first), and what would apply without it.

Example:
  yolo config why active_provider.claude`

func configWhy(args []string, out, errw io.Writer) int {
	var positionals []string
	for _, a := range args {
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, configWhyUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo config why: unknown flag %q\n\n%s\n", a, configWhyUsage)
			return 2
		default:
			positionals = append(positionals, a)
		}
	}
	if len(positionals) != 1 {
		fmt.Fprintln(errw, configWhyUsage)
		return 2
	}
	raw := positionals[0]
	steps, err := parseConfigEditKey(raw)
	if err != nil {
		fmt.Fprintf(errw, "yolo config why: invalid config key %q: %v\n  Spell it as dotted keys, such as active_provider.claude or packs[0].\n", raw, err)
		return 2
	}
	workspace := configEditWorkspace()
	cfg, sources, err := config.LoadReadableConfigWithSources(workspace, false, func(string) {})
	if err != nil {
		fmt.Fprintf(errw, "yolo config why: cannot read this workspace's config: %v\n  Run `yolo check` for the full report.\n", err)
		return 1
	}
	path := configWhyPath(steps)
	value, present := configWhyValue(cfg, steps)
	if present {
		fmt.Fprintf(out, "%s = %s\n", raw, configWhyRenderForPath(value, steps))
		locations := sources.Locations("config." + path)
		switch {
		case len(locations) == 0:
			fmt.Fprintln(out, "  set by no file yolo can name (a computed or inherited value)")
		default:
			fmt.Fprintf(out, "  set in  %s\n", locations[0])
			for _, loc := range locations[1:] {
				fmt.Fprintf(out, "  under it, also set in  %s\n", loc)
			}
		}
	} else {
		fmt.Fprintf(out, "%s is not set in any config file this workspace reads.\n", raw)
	}
	fmt.Fprintf(out, "  %s\n", configWhyDefault(cfg, sources, steps, present))
	if line := configWhyMCPScope(cfg, sources, steps); line != "" {
		fmt.Fprintf(out, "  %s\n", line)
	}
	return 0
}

func configWhyMCPScope(effectiveConfig *jsonx.OrderedMap, sources *config.Sources, steps []configdoc.Step) string {
	if len(steps) != 2 || steps[0].IsIndex || steps[0].Key != "mcp_servers" || steps[1].IsIndex {
		return ""
	}
	if config.InJail() {
		root := os.Getenv("YOLO_PACK_ROOT")
		if root == "" {
			return configWhyMCPJailUnavailable(steps[1].Key, "the running session's immutable pack tree is not named")
		}
		actualPacks, recorded, err := configWhyJailPacks(root)
		if err != nil || !recorded {
			return configWhyMCPJailUnavailable(steps[1].Key, "the running session's immutable pack tree could not be read")
		}
		reason := "this jail reads a generated merged preflight config, not a user-only config, and its mutable launch snapshot does not prove which selection produced the running immutable pack tree"
		var effectiveServers *jsonx.OrderedMap
		if effectiveValue, ok := effectiveConfig.Get("mcp_servers"); ok {
			effectiveServers, _ = effectiveValue.(*jsonx.OrderedMap)
		}
		composed := packload.ComposeMCPServers(effectiveServers, actualPacks, paths.Home())
		if value, present := composed.Get(steps[1].Key); present && value != nil {
			source := configWhyMCPSource(steps[1].Key, actualPacks)
			if source != "" {
				reason += "; pack source is " + source
			}
		}
		return configWhyMCPJailUnavailable(steps[1].Key, reason)
	}
	userConfig, err := config.UserScopeConfig(false, func(string) {})
	if err != nil {
		return "scope: unavailable — yolo cannot read the user-scope config needed to compare MCP identity; run `yolo check` and fix the config finding"
	}
	userPacks, complete := config.UserScopeSelectedPacks()
	if !complete {
		return "scope: unavailable — yolo could not resolve the user-selected packs; run `yolo check` and resolve its pack findings"
	}
	actualPacks := userPacks
	userValue, _ := userConfig.Get("mcp_servers")
	userServers, _ := userValue.(*jsonx.OrderedMap)
	effectiveValue, _ := effectiveConfig.Get("mcp_servers")
	effectiveServers, _ := effectiveValue.(*jsonx.OrderedMap)
	userOnly := packload.ComposeMCPServers(userServers, userPacks, paths.Home())
	effective := packload.ComposeMCPServers(effectiveServers, actualPacks, paths.Home())
	scope, ok := packload.MCPServerScopes(effective, userOnly)[steps[1].Key]
	if !ok {
		var removed any
		var present bool
		if effectiveServers != nil {
			removed, present = effectiveServers.Get(steps[1].Key)
		}
		if present && (removed == nil || removed == "off") {
			return "scope: absent — this server is removed by the effective config"
		}
		if !config.InJail() {
			return "scope: unavailable — this standalone command cannot know packs added only to a launch; run `yolo config why " + configWhyPath(steps) + "` inside the running jail"
		}
		return ""
	}
	if scope.Level == "user" {
		if pack := configWhyMCPSource(steps[1].Key, userPacks); pack != "" {
			return "scope: user — selected pack " + pack + " supplies this server"
		}
		return "scope: user — " + scope.Reason
	}
	if pack := configWhyMCPSource(steps[1].Key, actualPacks); pack != "" && !configWhyHasPack(pack, userPacks) {
		return "scope: workspace — pack " + pack + " is in the running pack tree but not the user-selected pack list"
	}
	field := ""
	switch {
	case strings.Contains(scope.Reason, "url differs"):
		field = "url"
	case strings.Contains(scope.Reason, "headers differ"):
		field = "headers"
	case strings.Contains(scope.Reason, "transport differs"):
		field = "transport"
	case strings.Contains(scope.Reason, "OAuth client id differs"):
		field = "oauth.client_id"
	case strings.Contains(scope.Reason, "command differs"):
		field = "command"
	case strings.Contains(scope.Reason, "arguments differ"):
		field = "args"
	}
	if field != "" && sources != nil {
		locations := sources.Locations("config.mcp_servers." + steps[1].Key + "." + field)
		if len(locations) > 0 {
			loc := strings.SplitN(locations[0], ":", 2)[0]
			return fmt.Sprintf("scope: workspace — %s changes its %s, so it signs in separately from your user-level %s", filepath.Base(loc), field, steps[1].Key)
		}
	}
	return "scope: workspace — " + scope.Reason
}

func configWhyJailPacks(root string) ([]*packload.Pack, bool, error) {
	entries, recorded, err := packload.ReadPackTreeRecord(root)
	if err != nil || !recorded {
		return nil, false, err
	}
	packs := make([]*packload.Pack, 0, len(entries))
	for _, entry := range entries {
		dir := filepath.Join(root, filepath.FromSlash(entry.Dir))
		pack, problems := packload.LoadDirForUse(dir, entry.Name)
		if pack == nil || len(problems) > 0 {
			return nil, false, fmt.Errorf("load staged pack %s: %v", entry.Name, problems)
		}
		entry.ApplyNaming(pack)
		packs = append(packs, pack)
	}
	return packs, true, nil
}

func configWhyMCPJailUnavailable(server, reason string) string {
	return "scope: unavailable — " + reason + "; run `yolo config why mcp_servers." + server +
		"` on the host to inspect the current user-only config. This readout is diagnostic, not authorization."
}

func configWhyMCPSource(server string, packs []*packload.Pack) string {
	for _, held := range packload.HeldMCPServers(packs) {
		if held.Server.Name == server {
			return held.Pack
		}
	}
	return ""
}

func configWhyHasPack(name string, packs []*packload.Pack) bool {
	for _, pack := range packs {
		if pack.Name == name {
			return true
		}
	}
	return false
}

// configWhyPath spells steps as Sources.Locations reads them: dotted keys and [index].
func configWhyPath(steps []configdoc.Step) string {
	var b strings.Builder
	for i, step := range steps {
		if step.IsIndex {
			fmt.Fprintf(&b, "[%d]", step.Index)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(step.Key)
	}
	return b.String()
}

func configWhyValue(cfg *jsonx.OrderedMap, steps []configdoc.Step) (any, bool) {
	var value any = cfg
	for _, step := range steps {
		if step.IsIndex {
			items, ok := value.([]any)
			if !ok || step.Index < 0 || step.Index >= len(items) {
				return nil, false
			}
			value = items[step.Index]
			continue
		}
		object, ok := value.(*jsonx.OrderedMap)
		if !ok || object == nil {
			return nil, false
		}
		value, ok = object.Get(step.Key)
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func configWhyRenderForPath(value any, steps []configdoc.Step) string {
	value = config.RedactPrivateEnvAt(steps, value)
	inMCP, inHeaders, atHeaderMap := false, false, false
	for _, step := range steps {
		if step.IsIndex {
			continue
		}
		if strings.EqualFold(step.Key, "mcp_servers") {
			inMCP = true
		}
		if inMCP && strings.EqualFold(step.Key, "headers") {
			inHeaders, atHeaderMap = true, true
		} else if inHeaders {
			atHeaderMap = false
		}
	}
	if inHeaders && !atHeaderMap {
		return `"<redacted>"`
	}
	if inMCP {
		value = redactMCPHeaderValues(value, atHeaderMap)
	}
	return configWhyRender(value)
}

func redactMCPHeaderValues(value any, inHeaders bool) any {
	if inHeaders {
		switch object := value.(type) {
		case *jsonx.OrderedMap:
			out := jsonx.NewOrderedMap()
			for _, key := range object.Keys() {
				child, _ := object.Get(key)
				out.Set(key, redactMCPHeaderValues(child, true))
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(object))
			for key, child := range object {
				out[key] = redactMCPHeaderValues(child, true)
			}
			return out
		case []any:
			out := make([]any, len(object))
			for i, child := range object {
				out[i] = redactMCPHeaderValues(child, true)
			}
			return out
		default:
			return "<redacted>"
		}
	}
	switch object := value.(type) {
	case *jsonx.OrderedMap:
		out := jsonx.NewOrderedMap()
		for _, key := range object.Keys() {
			child, _ := object.Get(key)
			if strings.EqualFold(key, "headers") {
				out.Set(key, redactMCPHeaderValues(child, true))
			} else {
				out.Set(key, redactMCPHeaderValues(child, false))
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(object))
		for key, child := range object {
			if strings.EqualFold(key, "headers") {
				out[key] = redactMCPHeaderValues(child, true)
			} else {
				out[key] = redactMCPHeaderValues(child, false)
			}
		}
		return out
	case []any:
		out := make([]any, len(object))
		for i, child := range object {
			out[i] = redactMCPHeaderValues(child, false)
		}
		return out
	default:
		return value
	}
}

func configWhyRender(value any) string {
	text, err := jsonx.DumpsCompact(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return text
}

// configWhyDefault says what applies without the key, from the one rule that decides it:
// active_provider.<agent> falls back to active_provider.default, an unset default leaves the
// agent to its own login ("native"), a pack setting falls back to the pack's declared default,
// and any other key falls back to the next file under the one that sets it.
func configWhyDefault(cfg *jsonx.OrderedMap, sources *config.Sources, steps []configdoc.Step, present bool) string {
	keys := make([]string, 0, len(steps))
	for _, step := range steps {
		if step.IsIndex {
			keys = nil
			break
		}
		keys = append(keys, step.Key)
	}
	if len(keys) == 2 && keys[0] == "active_provider" {
		hint := ""
		if !present {
			hint = "; set it with `" + configWhySetCommand(steps, keys) + "`"
		}
		if keys[1] != "default" {
			if value, ok := configValueAt(cfg, "active_provider", "default"); ok {
				where := ""
				if locations := sources.Locations("config.active_provider.default"); len(locations) > 0 {
					where = ", set in " + locations[0]
				}
				return "default would be: active_provider.default, " + configWhyRender(value) + where + hint
			}
		}
		return `default would be: "native" (no provider is selected, so the agent is left to its own login)` + hint
	}
	if len(keys) == 3 && keys[0] == "allow" && keys[2] != "always" && keys[2] != "on_request" {
		_, settings, err := allowSelectedBrokers()
		if err == nil {
			for _, pack := range settings {
				if pack.Pack != keys[1] {
					continue
				}
				for _, setting := range pack.Settings {
					if setting.Key == keys[2] {
						return "default would be: " + configWhyRender(setting.Default) + " (declared by the " + keys[1] + " pack)"
					}
				}
			}
		}
	}
	if present {
		if locations := sources.Locations("config." + configWhyPath(steps)); len(locations) > 1 {
			switch configWhyValueOf(cfg, steps).(type) {
			case []any, *jsonx.OrderedMap:
				return "these files merge: removing one file's part leaves the others'"
			}
			return "without the first, the value set in " + locations[1] + " would apply"
		}
		return "without it, the key is unset; `yolo config-ref` describes what an unset key means"
	}
	if replacement := config.RenamedKeyReplacement(keys); replacement != "" {
		return configWhyPath(steps) + " was renamed, so use " + replacement + " instead"
	}
	return "`yolo config-ref` describes what an unset key means; set it with `" + configWhySetCommand(steps, keys) + "`"
}

// configWhySetCommand is the command that sets an unset key: the key's named verb where one
// exists (docs/design/config-commands.md section 4, OQ-RC10) and the generic `yolo config set`
// where none does.
func configWhySetCommand(steps []configdoc.Step, keys []string) string {
	generic := "yolo config set " + configWhyPath(steps) + " <value>"
	if len(steps) == 0 || steps[0].IsIndex {
		return generic
	}
	switch steps[0].Key {
	case "packs":
		return "yolo pack add <pack>"
	case "active_provider":
		if len(keys) == 2 {
			return "yolo active-provider " + keys[1] + " <provider|native>"
		}
		return "yolo active-provider <agent|default> <provider|native>"
	case "allow":
		switch {
		case len(keys) == 1:
			return "yolo allow <source> [always|on_request] <capability>"
		case len(keys) == 2:
			return "yolo allow " + keys[1] + " <capability>"
		case len(keys) == 3 && (keys[2] == "always" || keys[2] == "on_request"):
			return "yolo allow " + keys[1] + " " + keys[2] + " <capability>"
		}
	case "repos":
		return configWhyReposSetCommand(keys, generic)
	case "requests":
		switch {
		case len(keys) == 1:
			return "yolo request <source> <capability> [resource]"
		case len(keys) == 2:
			return "yolo request " + keys[1] + " <capability> [resource]"
		case len(keys) == 3 && keys[2] == "push":
			return "yolo request " + keys[1] + " push <ref>"
		case len(keys) == 3 && (keys[2] == "runs" || keys[2] == "dispatch_workflows"):
			return "yolo request " + keys[1] + " runs <workflow file>"
		case len(keys) == 3 && keys[2] == "approve_runs":
			return "yolo request " + keys[1] + " approve_runs <actor login>"
		case len(keys) == 3:
			return "yolo request " + keys[1] + " " + keys[2]
		}
	}
	return generic
}

// configWhyReposSetCommand is the named verb for a `repos` key (GW-D77): the repository's line, its
// request under a name, or its scoped allow in the user config. Other keys keep the generic verb.
func configWhyReposSetCommand(keys []string, generic string) string {
	switch {
	case len(keys) == 3 && keys[2] == "repo":
		return "yolo request repos " + keys[1] + " <host>/<path>"
	case len(keys) == 5 && keys[2] == "requests":
		return configWhyNamedRequestCommand(keys[1], keys[3], keys[4])
	case len(keys) == 5 && keys[2] == "allow" && keys[4] == "always", len(keys) == 5 && keys[2] == "allow" && keys[4] == "on_request":
		return "yolo allow repos." + keys[1] + "." + keys[3] + " " + keys[4] + " <capability>"
	}
	return generic
}

// configWhyNamedRequestCommand is the named request verb for capability under repos.<alias>.<source>.
func configWhyNamedRequestCommand(alias, source, capability string) string {
	cmd := "yolo request repos." + alias + "." + source
	switch capability {
	case "runs", "dispatch_workflows":
		return cmd + " runs <workflow file>"
	case "approve_runs":
		return cmd + " approve_runs <actor login>"
	case "push":
		return cmd + " push <ref>"
	}
	return cmd + " " + capability
}

func configWhyValueOf(cfg *jsonx.OrderedMap, steps []configdoc.Step) any {
	value, _ := configWhyValue(cfg, steps)
	return value
}
