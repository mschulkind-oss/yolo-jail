package cli

// confighostproviderdoc_test.go pins HC-D3 (docs/design/host-computed-layer.md §7): config-ref's
// host-notch `provider` row says what is true — the files `yolo host apply` writes include ones
// that carry provider facts in a jail, and host apply composes no provider table, so they render
// without them.
//
// The row used to say "nothing in those files is a provider, so there is no derive to feed",
// which is false twice over: host apply runs every derive (for its key names), and five shipped
// surfaces carry provider rows or a provider's model list. The test does not take that list
// from the design doc. It MEASURES it, by running every shipped derive over the provider table
// the shipped packs compose and over an empty one, and requires the row to name each surface
// whose output differs — so a new provider-carrying surface fails here until the row names it.

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// providerCarryingSurfaces returns the shipped surfaces ("agent/name") whose derive output
// changes when the provider table does, with no selection, MCP or LSP input — the files that
// carry provider facts in a jail.
func providerCarryingSurfaces(t *testing.T) []string {
	t.Helper()
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	composed, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jsonx.DumpsCompact(composed)
	if err != nil {
		t.Fatal(err)
	}
	var providers map[string]any
	if err := json.Unmarshal([]byte(raw), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) == 0 {
		t.Fatal("the shipped packs compose no providers, so this measures nothing")
	}
	derive := func(script string, key manifest.SurfaceKey, providers map[string]any) map[string]any {
		out, err := (luahook.GopherLuaVM{}).DeriveLayer(script, &luahook.DeriveCtx{
			Agent: key.Agent, Surface: key.Name, Profile: map[string]string{},
			Tables: map[string]map[string]any{
				manifest.SourceProviders:   providers,
				manifest.SourceMCPServers:  {},
				manifest.SourceLSPServers:  {},
				manifest.SourceUseProfiles: {},
			},
		})
		if err != nil {
			t.Fatalf("derive %s/%s: %v", key.Agent, key.Name, err)
		}
		return out.Layer
	}
	var out []string
	for _, p := range packs {
		keys, err := packload.DerivedSurfaces(p)
		if err != nil {
			t.Fatal(err)
		}
		script := packload.DeriveScript(p)
		for _, key := range keys {
			if !reflect.DeepEqual(derive(script, key, providers), derive(script, key, map[string]any{})) {
				out = append(out, key.Agent+"/"+key.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// kindRow returns one row of a config-ref kind list: its first line and every continuation line
// indented deeper than the row's own name, up to the next row.
func kindRow(t *testing.T, section, kind string) string {
	t.Helper()
	lines := strings.Split(section, "\n")
	for i, line := range lines {
		rest := strings.TrimLeft(line, " ")
		if rest == line || !strings.HasPrefix(rest, kind) || !isKindColumnGap(rest[len(kind):]) {
			continue
		}
		indent := len(line) - len(rest)
		row := []string{line}
		for _, next := range lines[i+1:] {
			if len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
			row = append(row, next)
		}
		return strings.Join(row, "\n")
	}
	t.Fatalf("config-ref's host-notch list has no %q row", kind)
	return ""
}

func TestConfigRefHostProviderRowSaysWhatHostApplyRenders(t *testing.T) {
	row := kindRow(t, hostNotchDocSection(t, configRefContent), "provider")
	flat := strings.Join(strings.Fields(row), " ")
	if strings.Contains(flat, "nothing in those files is a provider") {
		t.Errorf("the host-notch provider row still says no host-rendered file carries a "+
			"provider, which five shipped surfaces contradict:\n%s", row)
	}
	if !strings.Contains(flat, "composes no provider") {
		t.Errorf("the host-notch provider row does not say host apply composes no provider "+
			"table, which is why those files render without provider facts:\n%s", row)
	}
	carrying := providerCarryingSurfaces(t)
	if len(carrying) == 0 {
		t.Fatal("no shipped surface's derive reads the provider table, so this gate is vacuous")
	}
	for _, id := range carrying {
		if !strings.Contains(flat, "`"+id+"`") {
			t.Errorf("the host-notch provider row does not name `%s`, a surface whose derive "+
				"writes provider facts in a jail and none at the host (measured set: %v):\n%s",
				id, carrying, row)
		}
	}
}
