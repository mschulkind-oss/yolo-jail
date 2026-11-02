package configmigrate

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// layeredEnvRead is what a launch in ws makes of the env, with layer as its --user-layer ("" for
// none): the ordered env walk (each path's base name, each inline variable as NAME=value or
// NAME=<removed>) and the resolved env.
func layeredEnvRead(t *testing.T, ws, layer string) ([]string, map[string]string) {
	t.Helper()
	var merged *jsonx.OrderedMap
	var err error
	withUserLayer(layer, func() { merged, err = config.LoadConfig(ws, false, func(string) {}) })
	if err != nil {
		t.Fatal(err)
	}
	var walk []string
	for _, e := range config.EnvEntries(merged) {
		switch e := e.(type) {
		case string:
			walk = append(walk, filepath.Base(e))
		case *jsonx.OrderedMap:
			for _, k := range e.Keys() {
				v, _ := e.Get(k)
				if v == nil {
					v = "<removed>"
				}
				walk = append(walk, fmt.Sprintf("%s=%v", k, v))
			}
		}
	}
	env, removals := config.ResolveEnvSourcesFull(ws, merged, nil)
	resolved := map[string]string{}
	for _, k := range env.Keys() {
		v, _ := env.Get(k)
		resolved[k], _ = v.(string)
	}
	for _, k := range removals {
		resolved[k] = "<removed>"
	}
	return walk, resolved
}

// The owner's ruling of 2026-11-02 (RC-D50): piecemeal migration always works. Over every mix of
// legacy and TOML layers (user config, --user-layer, workspace config), `yolo config migrate`
// converts each legacy file exactly: the merged effective env walk and the env it resolves are
// the same before and after, and so equal across every stage of a piecemeal migration. Each layer
// carries an inline table on both sides of a file, so a conversion that moved an entry, or
// re-ordered a layer against its neighbours, changes what wins.
func TestMigrationKeepsTheLayeredEnvRead(t *testing.T) {
	const (
		userLegacy = `{"env_sources": [{"A": "user", "GONE": null}, "user.env", {"B": "user"}]}`
		userTOML   = "[env]\nsources = [{ set = { A = 'user' }, unset = ['GONE'] }, 'user.env', { set = { B = 'user' } }]\n"
		wsLegacy   = `{"env_sources": [{"B": "ws"}, "dev.env", {"C": null}]}`
		wsTOML     = "[env]\nsources = [{ set = { B = 'ws' } }, 'dev.env', { unset = ['C'] }]\n"
		layerJSONC = `{"env_sources": ["layer.env", {"A": "layer"}]}`
	)
	for _, tc := range []struct {
		name               string
		userJSONC, wsJSONC bool
		layer              bool
	}{
		{"user legacy, workspace TOML", true, false, false},
		{"user TOML, workspace legacy", false, true, false},
		{"both legacy", true, true, false},
		{"a legacy --user-layer over a TOML user config", false, false, true},
		{"a legacy --user-layer over a legacy user config", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			write(t, filepath.Join(dir, "user.env"), "A=userfile\nB=userfile\nC=userfile\nGONE=back\n")
			if tc.userJSONC {
				write(t, filepath.Join(dir, "config.jsonc"), userLegacy+"\n")
			} else {
				write(t, filepath.Join(dir, "config.toml"), userTOML)
			}
			ws := realDir(t)
			write(t, filepath.Join(ws, "dev.env"), "B=devfile\nC=devfile\n")
			if tc.wsJSONC {
				write(t, filepath.Join(ws, "yolo-jail.jsonc"), wsLegacy+"\n")
			} else {
				write(t, filepath.Join(ws, "yolo-jail.toml"), wsTOML)
			}
			layer := ""
			if tc.layer {
				layer = filepath.Join(home, "layers", "work.jsonc")
				write(t, filepath.Join(home, "layers", "layer.env"), "A=layerfile\nD=layerfile\n")
				write(t, layer, layerJSONC+"\n")
			}
			walkBefore, envBefore := layeredEnvRead(t, ws, layer)
			wantWalk := []string{"A=user", "GONE=<removed>", "user.env", "B=user"}
			if tc.layer {
				wantWalk = append(wantWalk, "layer.env", "A=layer")
			}
			wantWalk = append(wantWalk, "B=ws", "dev.env", "C=<removed>")
			if !reflect.DeepEqual(walkBefore, wantWalk) {
				t.Fatalf("the fixture's env walk = %v, want %v", walkBefore, wantWalk)
			}

			p := plan(t, Request{UserConfigDir: dir, Workspace: ws, Layer: layer})
			if len(p.NotConverted) > 0 {
				t.Fatalf("not converted: %+v", p.NotConverted)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if layer != "" {
				layer = strings.TrimSuffix(layer, ".jsonc") + ".toml"
			}
			walkAfter, envAfter := layeredEnvRead(t, ws, layer)
			if !reflect.DeepEqual(walkAfter, walkBefore) {
				t.Errorf("the converted config walks the env in a different order:\n  before %v\n  after  %v", walkBefore, walkAfter)
			}
			if !reflect.DeepEqual(envAfter, envBefore) {
				t.Errorf("the converted config resolves a different env:\n  before %v\n  after  %v", envBefore, envAfter)
			}
		})
	}
}

// A legacy env_sources null replaces the lists the layers beneath wrote (inline tables
// included), leaving env.set alone. Migrate writes it as TOML's reset, an empty env.sources, and
// the read is the same before and after.
func TestMigrationKeepsAnEnvSourcesNullReset(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "user.env"), "A=userfile\n")
	write(t, filepath.Join(dir, "config.toml"), "[env]\nsources = ['user.env', { set = { B = 'user' } }]\nset = { C = 'user' }\n")
	ws := realDir(t)
	write(t, filepath.Join(ws, "dev.env"), "D=devfile\n")
	layer := filepath.Join(home, "layers", "work.jsonc")
	write(t, layer, `{"env_sources": null}`+"\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"env_sources": ["dev.env"]}`+"\n")
	walkBefore, envBefore := layeredEnvRead(t, ws, layer)
	if want := []string{"dev.env", "C=user"}; !reflect.DeepEqual(walkBefore, want) {
		t.Fatalf("the fixture's env walk = %v, want %v: the null must drop every list beneath it", walkBefore, want)
	}
	if want := map[string]string{"C": "user", "D": "devfile"}; !reflect.DeepEqual(envBefore, want) {
		t.Fatalf("the fixture resolves %v, want %v", envBefore, want)
	}
	for _, req := range []Request{{UserConfigDir: dir, Workspace: ws, Layer: layer}, {UserConfigDir: dir, Workspace: ws}} {
		p := plan(t, req)
		if len(p.NotConverted) > 0 {
			t.Fatalf("not converted: %+v", p.NotConverted)
		}
		if _, err := Apply(p, ApplyOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	newLayer := strings.TrimSuffix(layer, ".jsonc") + ".toml"
	if text := readFile(t, newLayer); !strings.Contains(text, "sources = []") {
		t.Errorf("the null did not convert to an empty env.sources:\n%s", text)
	}
	walkAfter, envAfter := layeredEnvRead(t, ws, newLayer)
	if !reflect.DeepEqual(walkAfter, walkBefore) || !reflect.DeepEqual(envAfter, envBefore) {
		t.Errorf("the converted config reads differently:\n  before %v %v\n  after  %v %v", walkBefore, envBefore, walkAfter, envAfter)
	}
}

// Migrate and every read share ONE lowering of env_sources (config.LowerEnvSources), so they
// cannot disagree about what a list means. Fails if lowerEnvSources stops calling it: the written
// list must be exactly the shared lowering of the old one, verbatim under env.sources.
func TestMigrateWritesTheSharedEnvLowering(t *testing.T) {
	for _, raw := range []string{
		`["a.env", "b.env"]`,
		`[{"A": "1", "B": null}, "a.env", {"C": "3"}]`,
		`[{"Z": null}]`,
	} {
		decoded, err := jsonx.Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		list := decoded.([]any)
		wantList, err := config.LowerEnvSources(list)
		if err != nil {
			t.Fatal(err)
		}
		out := jsonx.NewOrderedMap()
		place := func(newPath []string, value any, _ []string) bool { put(out, newPath, value); return true }
		lowerEnvSources(list, place, func([]string, any, string) { t.Fatalf("%s was not converted", raw) })
		if _, split := get(out, []string{"env", "files"}); split {
			t.Errorf("%s: migrate split the list into env.files: %s", raw, jsonx.MustDumpsCompact(out))
		}
		got, present := get(out, []string{"env", "sources"})
		if !present {
			t.Fatalf("%s: migrate wrote %s, not env.sources", raw, jsonx.MustDumpsCompact(out))
		}
		if jsonx.MustDumpsCompact(got) != jsonx.MustDumpsCompact(wantList) {
			t.Errorf("%s: migrate wrote %s, the shared lowering is %s", raw, jsonx.MustDumpsCompact(got), jsonx.MustDumpsCompact(wantList))
		}
	}
}

// A legacy null that drops what a lower layer wrote converts to its TOML removal spelling
// (config.LowerLegacyNulls, the lowering the inherited delivery uses too): `"packages": null` to
// `install.nix = []`, a server's null to "off". The plan proves the merged config reads the same,
// so it writes the file; it fails if migrate stops using the shared lowering, which refused both.
func TestMigrateConvertsANullToItsTOMLRemovalSpelling(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), "[install]\nnix = ['hello']\n[mcp_servers.foo]\ncommand = ['foo-mcp']\n")
	ws := realDir(t)
	old := filepath.Join(ws, "yolo-jail.jsonc")
	write(t, old, `{"packages": null, "mcp_servers": {"foo": null}}`+"\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if len(p.NotConverted) > 0 {
		t.Fatalf("not converted: %+v", p.NotConverted)
	}
	text := string(target(t, p, old).Bytes)
	for _, want := range []string{"nix = []", `foo = "off"`} {
		if !strings.Contains(text, want) {
			t.Errorf("the converted file does not carry %q:\n%s", want, text)
		}
	}
}
