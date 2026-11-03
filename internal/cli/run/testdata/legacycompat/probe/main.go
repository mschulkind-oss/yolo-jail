// accept runs v0.15.1's own config load + validate over generated per-key cases and prints
// the ones v0.15.1 accepted (no error) as JSON, plus a report of the refused ones on stderr.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

type kase struct {
	ID        string          `json:"id"`
	User      json.RawMessage `json:"user,omitempty"`
	Workspace json.RawMessage `json:"workspace,omitempty"`
	// WorkspaceFile is the per-workspace file's loophole switches (yolo loopholes enable).
	WorkspaceFile map[string]bool `json:"workspace_file,omitempty"`
	// V0151 is what v0.15.1's launch made of the case.
	V0151 *meaning `json:"v0151,omitempty"`
}

type meaning struct {
	Enabled  []string                  `json:"enabled"`
	Settings map[string]map[string]any `json:"settings,omitempty"`
}

var loopholePack = map[string]string{
	"aws-auth": "aws-auth", "audio": "audio", "cgroup-delegate": "cgroup-delegate",
	"claude-oauth-broker": "claude", "github-broker": "github", "hello-daemon": "hello-daemon",
	"host-processes": "host-processes", "journal": "journal", "macos-log": "macos-log",
	"openai-auth-broker": "openai-auth", "serial": "serial",
}

func settingValue(name, key, typ string) any {
	switch typ {
	case "string_list":
		switch key {
		case "fields":
			return []any{"pid", "comm", "args"}
		case "visible":
			return []any{"sway", "postgres"}
		default:
			return []any{"/dev/ttyUSB*"}
		}
	case "bool":
		return true
	case "int":
		return 9600
	case "string":
		return "example-" + key
	}
	panic(typ)
}

func raw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func main() {
	var cases []kase
	// Top-level keys, both scopes.
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var cand map[string]json.RawMessage
	if err := json.Unmarshal(data, &cand); err != nil {
		panic(err)
	}
	deps := map[string][]string{"profile": {"profiles", "providers"}, "profiles": {"providers"}}
	keys := make([]string, 0, len(cand))
	for k := range cand {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		obj := map[string]json.RawMessage{k: cand[k]}
		for _, d := range deps[k] {
			obj[d] = cand[d]
		}
		cases = append(cases, kase{ID: "toplevel/" + k + "/user", User: raw(obj)})
		cases = append(cases, kase{ID: "toplevel/" + k + "/workspace", Workspace: raw(obj)})
	}
	// Loophole switches and settings.
	files, _ := filepath.Glob("packs/*/loopholes/*/manifest.jsonc")
	sort.Strings(files)
	for _, f := range files {
		data, _ := os.ReadFile(f)
		v, err := json5.Decode(data)
		if err != nil {
			panic(err)
		}
		m := v.(*jsonx.OrderedMap)
		nv, _ := m.Get("name")
		name := nv.(string)
		pack := loopholePack[name]
		enabled := map[string]any{"loopholes": map[string]any{name: map[string]any{"enabled": true}}}
		cases = append(cases, kase{ID: "loophole/" + name + "/enabled/user",
			User: raw(map[string]any{"packs": []any{pack}, "loopholes": map[string]any{name: map[string]any{"enabled": true}}})})
		cases = append(cases, kase{ID: "loophole/" + name + "/enabled/workspace",
			User: raw(map[string]any{"packs": []any{pack}}), Workspace: raw(enabled)})
		cases = append(cases, kase{ID: "loophole/" + name + "/enabled/per-workspace-file",
			User: raw(map[string]any{"packs": []any{pack}}), WorkspaceFile: map[string]bool{name: true}})
		s, ok := m.Get("settings")
		if !ok {
			continue
		}
		sm := s.(*jsonx.OrderedMap)
		for _, k := range sm.Keys() {
			d, _ := sm.Get(k)
			dm := d.(*jsonx.OrderedMap)
			tv, _ := dm.Get("type")
			sv, _ := dm.Get("scope")
			scope, _ := sv.(string)
			if scope == "" {
				scope = "user"
			}
			val := settingValue(name, k, tv.(string))
			setting := map[string]any{"loopholes": map[string]any{name: map[string]any{"settings": map[string]any{k: val}}}}
			c := kase{ID: "loophole/" + name + "/settings." + k + "/" + scope}
			if scope == "user" {
				c.User = raw(map[string]any{"packs": []any{pack}, "loopholes": map[string]any{name: map[string]any{
					"enabled": true, "settings": map[string]any{k: val}}}})
			} else {
				c.User = raw(map[string]any{"packs": []any{pack}, "loopholes": map[string]any{name: map[string]any{"enabled": true}}})
				c.Workspace = raw(setting)
			}
			cases = append(cases, c)
		}
	}

	if len(os.Args) > 2 {
		realistic(os.Args[2])
		return
	}
	var accepted []kase
	for _, c := range cases {
		home, _ := os.MkdirTemp("", "lc-home-")
		ws := filepath.Join(home, "ws")
		_ = os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755)
		_ = os.MkdirAll(ws, 0o755)
		_ = os.MkdirAll(filepath.Join(home, "reloc"), 0o755)
		os.Setenv("HOME", home)
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		if c.User != nil {
			_ = os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), []byte(strings.ReplaceAll(string(c.User), "@HOME@", home)), 0o644)
		}
		if c.Workspace != nil {
			_ = os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(strings.ReplaceAll(string(c.Workspace), "@HOME@", home)), 0o644)
		}
		for n, on := range c.WorkspaceFile {
			if _, err := config.SetWorkspaceLoophole(ws, n, on); err != nil {
				panic(err)
			}
		}
		m, err := judge(ws)
		if err != nil {
			fmt.Fprintf(os.Stderr, "REFUSED %s: %v\n", c.ID, err)
			continue
		}
		c.V0151 = m
		accepted = append(accepted, c)
	}
	out, _ := json.MarshalIndent(accepted, "", "  ")
	fmt.Println(string(out))
}

// judge loads and validates ws's config as v0.15.1's launch does and returns its meaning.
func judge(ws string) (*meaning, error) {
	cfg, err := config.LoadConfig(ws, true, func(string) {})
	if err != nil {
		return nil, err
	}
	loopholes.ResetPackModules()
	errs, _ := config.ValidateConfig(cfg, ws, loopholes.NewResolver())
	if len(errs) > 0 {
		return nil, fmt.Errorf("%v", errs)
	}
	// What v0.15.1 made of it: the loopholes its launch started and the settings each was
	// handed (the launch's own calls: NewHostSet over the merged loopholes block, then
	// ResolveSettings over that block's settings).
	var block *jsonx.OrderedMap
	if v, ok := cfg.Get("loopholes"); ok {
		block, _ = v.(*jsonx.OrderedMap)
	}
	set := loopholes.NewHostSet(block)
	m := &meaning{Enabled: []string{}, Settings: map[string]map[string]any{}}
	for _, lp := range set.Enabled() {
		m.Enabled = append(m.Enabled, lp.Name)
		if len(lp.Settings) == 0 {
			continue
		}
		vals, _ := loopholes.ResolveSettings(lp, loopholes.SettingsFromConfig(block, lp.Name))
		plain := map[string]any{}
		for _, k := range vals.Keys() {
			v, _ := vals.Get(k)
			plain[k] = jsonx.Plain(v)
		}
		m.Settings[lp.Name] = plain
	}
	sort.Strings(m.Enabled)
	return m, nil
}

type realCase struct {
	Links         map[string]string `json:"links"`
	Dirs          []string          `json:"dirs"`
	WorkspaceFile map[string]bool   `json:"workspace_file"`
	Renames       map[string]string `json:"renames"`
	CurrentOnly   bool              `json:"current_only"`
}

// realistic judges every realistic/<case>/ tree (home/, ws/, case.json) and prints
// {case: {"v0151": meaning} | {"refused": text}}.
func realistic(root string) {
	ents, _ := os.ReadDir(root)
	out := map[string]any{}
	for _, e := range ents {
		dir := filepath.Join(root, e.Name())
		var rc realCase
		if b, err := os.ReadFile(filepath.Join(dir, "case.json")); err == nil {
			if err := json.Unmarshal(b, &rc); err != nil {
				panic(err)
			}
		}
		if rc.CurrentOnly {
			continue
		}
		base, _ := os.MkdirTemp("", "lc-home-")
		_ = os.CopyFS(base, os.DirFS(dir))
		home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
		_ = os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755)
		_ = os.MkdirAll(ws, 0o755)
		for from, to := range rc.Renames {
			_ = os.Rename(filepath.Join(base, from), filepath.Join(base, to))
		}
		for l, target := range rc.Links {
			_ = os.Symlink(target, filepath.Join(base, l))
		}
		for _, d := range rc.Dirs {
			_ = os.MkdirAll(filepath.Join(base, d), 0o755)
		}
		os.Setenv("HOME", home)
		os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		for n, on := range rc.WorkspaceFile {
			if _, err := config.SetWorkspaceLoophole(ws, n, on); err != nil {
				panic(err)
			}
		}
		m, err := judge(ws)
		if err != nil {
			out[e.Name()] = map[string]any{"refused": err.Error()}
			continue
		}
		out[e.Name()] = map[string]any{"v0151": m}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}
