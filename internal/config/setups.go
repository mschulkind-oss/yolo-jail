package config

import (
	"fmt"
	"runtime"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// setups.go is the config half of named setups (docs/design/named-setups.md; plan slice 2):
// `setup` names your default setup and `[setups.<name>]` defines one of your own. Both are
// user scope only, defined and chosen (design §8, OQ-MT5): a project file holding either is an
// error naming the fix, never ignored. The model — the fields, the built-ins, the refusal
// texts — is internal/setup's; this file reads the keys and says where they may live.

const (
	setupKey  = "setup"
	setupsKey = "setups"
)

// SelectedSetupFromConfig reports the user config's `setup` (and whether it is set) and the
// user's `[setups.*]` tables, decoded. It never consults a workspace config: a project cannot
// choose or define a setup, and /workspace is agent-editable. A table that does not decode is
// left out; the validator is what reports it.
func SelectedSetupFromConfig() (name string, user map[string]setup.Setup, set bool) {
	cfg := UserScopeConfigOrEmpty()
	user = UserSetups(cfg)
	if v, ok := cfg.Get(setupKey); ok {
		if s, isString := v.(string); isString && s != "" {
			return s, user, true
		}
	}
	return "", user, false
}

// UserSetups decodes every `[setups.<name>]` table in cfg through setup.Decode, leaving out
// any that does not decode.
func UserSetups(cfg *jsonx.OrderedMap) map[string]setup.Setup {
	out := map[string]setup.Setup{}
	tables, ok := cfg.Get(setupsKey)
	if !ok {
		return out
	}
	m, ok := asMap(tables)
	if !ok {
		return out
	}
	for _, name := range m.Keys() {
		raw, _ := m.Get(name)
		fields, ok := setupFields(raw)
		if !ok {
			continue
		}
		if s, err := setup.Decode(name, fields); err == nil {
			out[name] = s
		}
	}
	return out
}

// setupFields is a table's fields as strings; a non-string value reads as "" so Decode still
// names an unknown field, and the validator reports a known one's type itself.
func setupFields(raw any) (map[string]string, bool) {
	m, ok := asMap(raw)
	if !ok {
		return nil, false
	}
	fields := map[string]string{}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		s, _ := v.(string)
		fields[k] = s
	}
	return fields, true
}

// setupProjectRefusal is design §6's refusal of a `setup` line in a project file, after the
// "<file>:<line>: setup: " prefix its caller adds. unsetFlag is the file's own scope flag.
func setupProjectRefusal(value any, unsetFlag string) string {
	name, _ := value.(string)
	if !setup.ValidName(name) {
		name = setup.Default(runtime.GOOS).Name
	}
	return "a project cannot choose where its agent runs; that is yours to choose.\n" +
		"For this launch:       yolo --setup " + name + " -- claude\n" +
		"As your own default:   yolo config set setup " + name + " --user\n" +
		"Then remove the line:  yolo config unset setup " + unsetFlag
}

// setupsProjectRefusal is design §6's refusal of a `[setups.<name>]` table in a project file,
// after the "<file>:<line>: setups.<name>: " prefix its caller adds. Its next step copies the
// built-in the table's own fields come nearest to, each field defaulting on its own.
func setupsProjectRefusal(name string, table any) string {
	if !setup.ValidName(name) || setup.IsBuiltIn(name) {
		name = "my-" + setup.SuggestName(name)
		if !setup.ValidName(name) {
			name = "my-setup"
		}
	}
	c, r := setup.DefaultConfinement, setup.DefaultRunAs
	if fields, ok := setupFields(table); ok {
		for _, known := range setup.Confinements() {
			if fields["confinement"] == string(known) {
				c = known
			}
		}
		for _, known := range setup.RunAsValues() {
			if fields["run_as"] == string(known) {
				r = known
			}
		}
	}
	from := string(c) + "-" + string(r)
	if !setup.IsBuiltIn(from) {
		from = string(c) + "-" + string(setup.RunAsSelf)
	}
	return "a project cannot define a setup; setups are yours.\n" +
		"Define it in your user config instead:\n" +
		"    yolo setup new " + name + " --from " + from
}

// hostSetupRefusal is design §6's refusal of a host setup chosen by config (§8: a setup that
// puts no walls around the agent is never selected by config, only by `yolo host`).
func hostSetupRefusal(s setup.Setup) string {
	line := "yolo host --setup " + s.Name + " -- claude"
	if s.Name == "host-self" {
		line = "yolo host -- claude"
	}
	return fmt.Sprintf("setup %q puts no walls around the agent, and only `yolo host` runs one:\n    %s", s.Name, line)
}

// readableUserOnlyAdvice is the per-key refusal the readable walk gives a user-only key in a
// workspace file, in place of its generic "user-scope only" line, or nil for a key with none.
// It returns the path the refusal names (a `[setups.*]` refusal names the table, not the
// root) and the message, given the file's own scope flag for an unset.
func readableUserOnlyAdvice(path []string, value any) ([]string, func(unsetFlag string) string) {
	if len(path) != 1 {
		return nil, nil
	}
	switch path[0] {
	case setupKey:
		return path, func(flag string) string { return setupProjectRefusal(value, flag) }
	case setupsKey:
		m, ok := asMap(value)
		if !ok || len(m.Keys()) == 0 {
			return nil, nil
		}
		name := m.Keys()[0]
		table, _ := m.Get(name)
		return []string{setupsKey, name}, func(string) string { return setupsProjectRefusal(name, table) }
	}
	return nil, nil
}

// validateSetups refuses `setup` and `[setups.*]` in a workspace file (the legacy JSONC path;
// a TOML file is refused at load by the readable walk with the same text), and checks the
// user's own values: each table decodes, and `setup` names a defined setup that is not a host
// setup.
func validateSetups(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	wsHasSetup := false
	wsTables := map[string]bool{}
	if wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...); err == nil && wsCfg != nil {
		if v, ok := wsCfg.Get(setupKey); ok && v != nil {
			wsHasSetup = true
			add(errs, "config.setup: "+setupProjectRefusal(v, "--project"))
		}
		if v, ok := wsCfg.Get(setupsKey); ok && v != nil {
			if m, isMap := asMap(v); isMap {
				for _, name := range m.Keys() {
					table, _ := m.Get(name)
					wsTables[name] = true
					add(errs, "config.setups."+name+": "+setupsProjectRefusal(name, table))
				}
			} else {
				add(errs, "config.setups: a project cannot define a setup; setups are yours. Remove it:\n    yolo config unset setups --project")
			}
		}
	}

	user := map[string]setup.Setup{}
	undecoded := map[string]bool{}
	if v, ok := config.Get(setupsKey); ok && v != nil {
		m, isMap := asMap(v)
		if !isMap {
			if len(wsTables) == 0 {
				add(errs, "config.setups: expected a table of setups (got "+pyReprValue(v)+")")
			}
		} else {
			names := append([]string(nil), m.Keys()...)
			sort.Strings(names)
			for _, name := range names {
				if wsTables[name] {
					continue
				}
				raw, _ := m.Get(name)
				tm, isTable := asMap(raw)
				if !isTable {
					undecoded[name] = true
					add(errs, "config.setups."+name+": expected a table with `confinement` and `run_as` (got "+pyReprValue(raw)+")")
					continue
				}
				typed := true
				for _, field := range []string{"confinement", "run_as"} {
					if fv, present := tm.Get(field); present {
						if _, isString := fv.(string); !isString {
							add(errs, "config.setups."+name+"."+field+": expected a string (got "+pyReprValue(fv)+")")
							typed = false
						}
					}
				}
				if !typed {
					undecoded[name] = true
					continue
				}
				fields, _ := setupFields(raw)
				s, err := setup.Decode(name, fields)
				if err != nil {
					undecoded[name] = true
					add(errs, err.Error())
					continue
				}
				user[name] = s
			}
		}
	}

	if wsHasSetup {
		return
	}
	v, ok := config.Get(setupKey)
	if !ok || v == nil {
		return
	}
	name, isString := v.(string)
	if !isString {
		add(errs, "config.setup: expected a setup name (got "+pyReprValue(v)+"). See the defined setups:\n    yolo setup list")
		return
	}
	if undecoded[name] {
		return // its table's own refusal is the one to fix
	}
	s, _, err := setup.Select("", "", name, user, runtime.GOOS)
	if err != nil {
		add(errs, err.Error())
		return
	}
	if s.Kind() == setup.KindHost {
		add(errs, hostSetupRefusal(s))
	}
}
