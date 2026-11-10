package run

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// loweredScopeReads is what a launch makes of cfg for the settings RC-D55 lowers: the three
// provider wire tables the channel hands the jail (the provider table with every model row and
// model_facts entry composed in, the profiles, and the profile each agent starts on), and the
// settings each loophole is given. The channel reads the user's profile declarations from the
// user config on disk, so cfg must be the effective config of the HOME the test points at.
func loweredScopeReads(t *testing.T, o *Options, cfg *jsonx.OrderedMap, packs []*packload.Pack) map[string]string {
	t.Helper()
	c, err := o.composePackChannel(cfg, packs, hydrate(jsonx.NewOrderedMap()))
	if err != nil {
		t.Fatalf("the channel does not compose: %v", err)
	}
	out := c.wireTableValues()
	// A readable row carries its place in the model order as an `order` option, where a legacy
	// row's place is its name's; the meaning is the order they make, which is compared instead.
	if decoded, err := jsonx.Decode([]byte(out["YOLO_PROVIDERS"])); err == nil {
		if table, ok := decoded.(*jsonx.OrderedMap); ok {
			for _, name := range table.Keys() {
				entry, _ := table.Get(name)
				if m, ok := entry.(*jsonx.OrderedMap); ok {
					m.Set("(model order)", modelOrder(m))
					// A variant is a profile declaration, compared below as what each agent
					// starts on.
					m.Delete("variants")
				}
			}
			snapshot, err := jsonx.DumpsSnapshot(table)
			if err != nil {
				t.Fatal(err)
			}
			out["YOLO_PROVIDERS"] = snapshot
		}
	}
	// What each agent starts on: its selected profiles, resolved. The names are not compared:
	// a legacy profile is a provider's variant in TOML, named <provider>/<variant> (RC-D32).
	profiles, _ := jsonx.Decode([]byte(out["YOLO_PROFILES"]))
	use, _ := jsonx.Decode([]byte(out["YOLO_USE_PROFILES"]))
	declared, _ := profiles.(*jsonx.OrderedMap)
	starts := jsonx.NewOrderedMap()
	if selected, ok := use.(*jsonx.OrderedMap); ok && declared != nil {
		for _, agent := range selected.Keys() {
			v, _ := selected.Get(agent)
			names, isList := v.([]any)
			if !isList {
				names = []any{v}
			}
			var resolved []any
			for _, n := range names {
				name, _ := n.(string)
				p, _ := declared.Get(name)
				resolved = append(resolved, p)
			}
			starts.Set(agent, resolved)
		}
	}
	delete(out, "YOLO_PROFILES")
	delete(out, "YOLO_USE_PROFILES")
	snapshot, err := jsonx.DumpsSnapshot(starts)
	if err != nil {
		t.Fatal(err)
	}
	out["each agent starts on"] = snapshot
	settings := jsonx.NewOrderedMap()
	if block := loopholes.ConfigBlock(cfg); block != nil {
		for _, name := range block.Keys() {
			entry, _ := block.Get(name)
			if m, ok := entry.(*jsonx.OrderedMap); ok {
				if s, ok := m.Get("settings"); ok {
					settings.Set(name, s)
				}
			}
		}
	}
	out["loophole settings"] = jsonx.MustDumpsCompact(settings)
	return out
}

// modelOrder is the order a provider entry's models are offered in: by their `order` option,
// those with one first, then by name. It takes the `order` options out of entry.
func modelOrder(entry *jsonx.OrderedMap) []any {
	models, _ := entry.Get("models")
	m, _ := models.(*jsonx.OrderedMap)
	if m == nil {
		return nil
	}
	opts, _ := entry.Get("model_options")
	options, _ := opts.(*jsonx.OrderedMap)
	order := map[string]int{}
	if options != nil {
		for _, alias := range options.Keys() {
			row, _ := options.Get(alias)
			if r, ok := row.(*jsonx.OrderedMap); ok {
				if v, ok := r.Get("order"); ok {
					if n, err := strconv.Atoi(v.(string)); err == nil {
						order[alias] = n
					}
					r.Delete("order")
					if r.Len() == 0 {
						options.Delete(alias)
					}
				}
			}
		}
		if options.Len() == 0 {
			entry.Delete("model_options")
		}
	}
	aliases := slices.Clone(m.Keys())
	slices.SortStableFunc(aliases, func(a, b string) int {
		oa, ha := order[a]
		ob, hb := order[b]
		switch {
		case ha && hb && oa != ob:
			return oa - ob
		case ha != hb:
			if ha {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	out := make([]any, len(aliases))
	for i, a := range aliases {
		out[i] = a
	}
	return out
}

// RC-D55, LC-D4: a user config that mixes a TOML file with a legacy include holding a setting the
// readers keep in its legacy spelling (a model row's facts, model_facts, profile and profiles, a
// loophole block) crosses into the jail as the TOML `yolo config migrate` writes for it, and the
// jail resolves the same providers and profiles from it as the host; a loophole block crosses as
// nothing (LC-D6). Before, the scope could cross in neither format and the launch refused. Fails
// if the delivery stops passing the migration's lowering (inheritScopeFilesWithSelector's
// configmigrate.LowerInheritedUserScope): the providers and profiles then cross without the
// legacy settings.
func TestMixedFormatLegacyUserSettingsCrossAsTheirMigration(t *testing.T) {
	const provider = `"myp": {"endpoints": {"anthropic": {"base_url": "https://llm.example"}}, "api_key_env_name": "MYP_KEY"`
	for _, tc := range []struct{ name, include, read, holds string }{
		{"a legacy model row", `{"providers": {` + provider + `, "models": {"m1": {"id": "vendor-m1", "context_window": 1000, "input": ["text"]}}}}}`, "YOLO_PROVIDERS", `"context_window": "1000"`},
		{"model_facts", `{"providers": {` + provider + `, "models": {"m1": {"id": "vendor-m1", "extends": "vendor-m1"}}}}, "model_facts": {"vendor-m1": {"context_window": 2000, "cost": {"input": 1, "output": 2, "cache_read": 0.5, "cache_write": 1.5}}}}`, "YOLO_PROVIDERS", `"context_window": "2000"`},
		{"profile and profiles", `{"providers": {` + provider + `, "models": {"m1": {"id": "vendor-m1"}}}}, "profiles": {"work": {"provider": "myp", "model": "m1"}}, "profile": {"claude": "work"}}`, "each agent starts on", `"model": "m1"`},
		{"a loophole's settings", `{"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "stand-in"}}}}`, "loophole settings", `"stand-in"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emptyLoopholeDirs(t)
			_, wsState := inheritHome(t, "")
			host := os.Getenv("HOME")
			dir := filepath.Join(host, ".config", "yolo-jail")
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("packs = ['claude', 'aws-auth']\ninclude_if_found = ['extra.jsonc']\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "extra.jsonc"), []byte(tc.include), 0o644); err != nil {
				t.Fatal(err)
			}
			packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "claude-auth"), officialPack(t, "wire-bridge"), officialPack(t, "aws-auth")}
			o := goldenOptions(t.TempDir(), host)
			o.Stdout, o.Stderr = &strings.Builder{}, &strings.Builder{}
			effective, _, err := o.effectiveConfigForInherit()
			if err != nil {
				t.Fatal(err)
			}
			if errs, _ := config.ValidateConfig(effective, o.Workspace, nil); len(errs) > 0 {
				t.Fatalf("the host config does not launch: %v", errs)
			}
			want := loweredScopeReads(t, o, effective, packs)
			if !strings.Contains(want[tc.read], tc.holds) {
				t.Fatalf("the host's %s does not hold %s: %s", tc.read, tc.holds, want[tc.read])
			}
			args, err := o.userConfigMountArgsForHome("podman", wsState, nil)
			if err != nil {
				t.Fatalf("the launch refused a legacy include under a TOML user config: %v", err)
			}
			for _, dest := range []string{"inherited-launch.toml", "config.toml"} {
				body, source, ok := mountedBody(t, args, "/home/agent/.config/yolo-jail/"+dest)
				if !ok || filepath.Ext(source) != ".toml" {
					t.Fatalf("%s was not delivered as TOML; argv=%v", dest, args)
				}
				if dest != "inherited-launch.toml" {
					continue
				}
				// Read as the jail reads it: the user config of a home of its own.
				reread := t.TempDir()
				if err := os.MkdirAll(filepath.Join(reread, ".config", "yolo-jail"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(reread, ".config", "yolo-jail", "config.toml"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HOME", reread)
				jail := goldenOptions(t.TempDir(), reread)
				jail.Stdout, jail.Stderr = &strings.Builder{}, &strings.Builder{}
				delivered, _, err := jail.effectiveConfigForInherit()
				if err != nil {
					t.Fatalf("the delivered scope does not read: %v\n%s", err, body)
				}
				if errs, _ := config.ValidateConfig(delivered, jail.Workspace, nil); len(errs) > 0 {
					t.Fatalf("the delivered scope does not launch: %v\n%s", errs, body)
				}
				got := loweredScopeReads(t, jail, delivered, packs)
				if tc.read == "loophole settings" {
					// LC-D6: a legacy loopholes block crosses into a TOML delivery as its readable
					// reading, nothing: its settings are `allow`, which no jail inherits, the same
					// as the TOML table it converts to (X14). The host's own launch keeps them.
					want["loophole settings"] = "{}"
				}
				for k, w := range want {
					if got[k] != w {
						t.Errorf("%s: the jail reads\n  %s\nthe host\n  %s\ndelivered:\n%s", k, got[k], w, body)
					}
				}
				t.Setenv("HOME", host)
			}
		})
	}
}
