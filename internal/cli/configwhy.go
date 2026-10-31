package cli

// configwhy.go is `yolo config why <key>` (docs/design/config-commands.md §1 and §4): the
// effective value of one key, every file:line that sets it (highest precedence first, from
// the loader's own provenance record, config.Sources), and what would apply without it.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
		fmt.Fprintf(out, "%s = %s\n", raw, configWhyRender(value))
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
	return 0
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
	case "requests":
		switch {
		case len(keys) == 1:
			return "yolo request <source> <capability> [resource]"
		case len(keys) == 2:
			return "yolo request " + keys[1] + " <capability> [resource]"
		case len(keys) == 3 && keys[2] == "repos":
			return "yolo request " + keys[1] + " repos <OWNER/REPO>"
		case len(keys) == 3 && keys[2] == "push":
			return "yolo request " + keys[1] + " push <ref>"
		case len(keys) == 3 && keys[2] == "dispatch_workflows":
			return "yolo request " + keys[1] + " dispatch_workflows <workflow file>"
		case len(keys) == 3:
			return "yolo request " + keys[1] + " " + keys[2]
		}
	}
	return generic
}

func configWhyValueOf(cfg *jsonx.OrderedMap, steps []configdoc.Step) any {
	value, _ := configWhyValue(cfg, steps)
	return value
}
