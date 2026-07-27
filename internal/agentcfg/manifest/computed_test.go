package manifest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/project"
)

// mcpTable is a two-entry MCP table in the canonical shape, one entry with env and one
// without — the shape difference the projections actually branch on.
func mcpTable() map[string]map[string]any {
	return map[string]map[string]any{
		SourceMCPServers: {
			"seq":    map[string]any{"command": "npx", "args": []any{"-y", "seq"}},
			"chrome": map[string]any{"command": "node", "args": []any{"cdp.js"}, "env": map[string]any{"K": "v"}},
		},
	}
}

// TestOmitEmptyAndTombstoneAreDifferentThings is the distinction the two fields exist
// to keep apart, and it is the one a reader is most likely to assume is cosmetic.
//
// omitEmpty says nothing about the key, so a lower layer's value SURVIVES. A tombstone
// emits null, which the compose engine reads as RFC-7386 delete, so the lower layer's
// value is REMOVED. claude's settings.json needs the removal (a host settings.json may
// carry an mcpServers block that belongs in .claude.json); opencode needs the omission.
// A design that treated them as the same thing would silently break one of the two.
func TestOmitEmptyAndTombstoneAreDifferentThings(t *testing.T) {
	empty := map[string]map[string]any{SourceMCPServers: {}}

	omit, err := BuildComputed([]Computed{{From: SourceMCPServers, To: "mcp", OmitEmpty: true}}, empty)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := omit["mcp"]; present {
		t.Errorf("omitEmpty over an empty table: want no %q key at all, got %#v", "mcp", omit)
	}

	tomb, err := BuildComputed([]Computed{{To: "mcpServers", Tombstone: true}}, empty)
	if err != nil {
		t.Fatal(err)
	}
	v, present := tomb["mcpServers"]
	if !present {
		t.Fatalf("tombstone: want the key PRESENT (a null is the deletion), got %#v", tomb)
	}
	if v != nil {
		t.Errorf("tombstone: want a nil value (the RFC-7386 delete), got %#v", v)
	}
}

// TestOmitEmptyOffKeepsTheEmptyObject pins the other half: without omitEmpty, an empty
// table still emits its wrapper. A surface whose agent requires the key to exist (even
// empty) depends on this, so it must not be "optimized" into an omission.
func TestOmitEmptyOffKeepsTheEmptyObject(t *testing.T) {
	got, err := BuildComputed([]Computed{{From: SourceMCPServers, To: "mcpServers"}},
		map[string]map[string]any{SourceMCPServers: {}})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("want an empty object at mcpServers, got %#v", got["mcpServers"])
	}
	if len(m) != 0 {
		t.Errorf("want it empty, got %#v", m)
	}
}

// TestPassthroughPreservesEntriesVerbatim covers the surfaces (claude .claude.json,
// copilot mcp-config.json) that consume the canonical shape with no reshape at all.
func TestPassthroughPreservesEntriesVerbatim(t *testing.T) {
	got, err := BuildComputed([]Computed{{From: SourceMCPServers, To: "mcpServers"}}, mcpTable())
	if err != nil {
		t.Fatal(err)
	}
	m := got["mcpServers"].(map[string]any)
	seq, ok := m["seq"].(map[string]any)
	if !ok {
		t.Fatalf("want seq entry, got %#v", m)
	}
	if seq["command"] != "npx" {
		t.Errorf("passthrough altered the entry: %#v", seq)
	}
}

// TestOpencodeProjectionIsExpressibleAsData is the C6 claim applied to the hardest of
// the real projections — the one needing a RENAME, a FOLD of two fields into one array,
// and two INJECTED constants. If this cannot be said as data, the six Go builders
// cannot be deleted, so it is the load-bearing case.
func TestOpencodeProjectionIsExpressibleAsData(t *testing.T) {
	proj := &project.Projection{Ops: []project.Op{
		{Fold: &project.FoldOp{Froms: []string{"command", "args"}, To: "command"}},
		{Copy: &project.CopyOp{From: "env", To: "environment", OmitEmpty: true}},
		{Inject: &project.InjectOp{To: "type", Value: "local"}},
		{Inject: &project.InjectOp{To: "enabled", Value: true}},
	}}
	got, err := BuildComputed([]Computed{
		{From: SourceMCPServers, To: "mcp", Project: proj, OmitEmpty: true},
	}, mcpTable())
	if err != nil {
		t.Fatal(err)
	}
	table := got["mcp"].(map[string]any)

	seq := table["seq"].(map[string]any)
	cmd, ok := seq["command"].([]any)
	if !ok || len(cmd) != 3 || cmd[0] != "npx" || cmd[2] != "seq" {
		t.Errorf("fold: want [npx -y seq] in one array, got %#v", seq["command"])
	}
	if _, present := seq["environment"]; present {
		t.Errorf("omitEmpty: an entry with no env must not get an environment key: %#v", seq)
	}
	if seq["type"] != "local" || seq["enabled"] != true {
		t.Errorf("inject: want type=local enabled=true, got %#v", seq)
	}

	chrome := table["chrome"].(map[string]any)
	if _, present := chrome["env"]; present {
		t.Errorf("rename: env must be GONE (renamed), got %#v", chrome)
	}
	env, ok := chrome["environment"].(map[string]any)
	if !ok || env["K"] != "v" {
		t.Errorf("rename: want env moved to environment, got %#v", chrome["environment"])
	}
}

// TestRootMergePlacesEntriesAtTopLevel covers To:"" — a table whose entries are the
// surface's own top-level keys, with no wrapper.
func TestRootMergePlacesEntriesAtTopLevel(t *testing.T) {
	got, err := BuildComputed([]Computed{{From: SourceMCPServers}}, mcpTable())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["seq"]; !ok {
		t.Errorf("root merge: want seq at the top level, got %#v", got)
	}
}

// TestRootMergeCollisionIsLoud: two root-merging declarations that both produce the
// same key would silently drop one. A pack bug that renders a subtly wrong file is
// worse than a failed boot (A12), so it errors.
func TestRootMergeCollisionIsLoud(t *testing.T) {
	tables := map[string]map[string]any{
		SourceMCPServers: {"dup": map[string]any{}},
		SourceLSPServers: {"dup": map[string]any{}},
	}
	_, err := BuildComputed([]Computed{
		{From: SourceMCPServers}, {From: SourceLSPServers},
	}, tables)
	if err == nil {
		t.Fatal("want an error for two root declarations colliding on a key")
	}
}

// TestAbsentSourceBehavesLikeEmpty: a caller that did not supply a table (macos-user,
// or a boot with no MCP configured) must render the same as an empty one. Erroring
// would make a pack's behavior depend on which host it booted on.
func TestAbsentSourceBehavesLikeEmpty(t *testing.T) {
	got, err := BuildComputed([]Computed{{From: SourceMCPServers, To: "mcp", OmitEmpty: true}}, nil)
	if err != nil {
		t.Fatalf("absent source must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want nothing rendered, got %#v", got)
	}
}

// TestUnknownSourceIsRejected: an unknown `from` would otherwise yield a silently empty
// layer, which surfaces to the user as "my MCP servers stopped working" with nothing to
// grep for. The source set is closed so a typo is caught at load.
func TestUnknownSourceIsRejected(t *testing.T) {
	probs := Computed{From: "mcp_server", To: "mcp"}.Validate()
	if len(probs) == 0 {
		t.Fatal("want a problem for a misspelled source")
	}
}

// TestTombstoneRejectsFromAndProject: a tombstone deletes a key rather than deriving
// one, so naming a source alongside it means the author expected a derivation. Silently
// ignoring `from` would leave them debugging a table that never appears.
func TestTombstoneRejectsFromAndProject(t *testing.T) {
	probs := Computed{To: "x", Tombstone: true, From: SourceMCPServers}.Validate()
	if len(probs) == 0 {
		t.Error("want a problem for tombstone+from")
	}
	if probs := (Computed{Tombstone: true}).Validate(); len(probs) == 0 {
		t.Error("want a problem for a tombstone with no \"to\"")
	}
}

// TestComputedDecodesFromASurfaceDTO proves the whole declaration survives the wire —
// a pack ships JSON, so a field that decodes but is dropped on the way into Surface
// would make the feature look supported and do nothing.
func TestComputedDecodesFromASurfaceDTO(t *testing.T) {
	data := []byte(`[{
	  "agent": "example", "name": "config", "path": "~/.example/config.json", "codec": "json",
	  "computed": [
	    {"from": "mcp_servers", "to": "mcp", "omitEmpty": true,
	     "project": {"ops": [{"inject": {"to": "type", "value": "local"}}]}},
	    {"to": "mcpServers", "tombstone": true}
	  ]
	}]`)
	surfaces, problems := DecodeSurfaces(data)
	if len(problems) != 0 {
		t.Fatalf("decode problems: %v", problems)
	}
	if len(surfaces) != 1 {
		t.Fatalf("want 1 surface, got %d", len(surfaces))
	}
	c := surfaces[0].Computed
	if len(c) != 2 {
		t.Fatalf("want 2 computed decls on the Surface, got %d", len(c))
	}
	if c[0].From != SourceMCPServers || c[0].To != "mcp" || !c[0].OmitEmpty {
		t.Errorf("first decl lost fields: %#v", c[0])
	}
	if c[0].Project == nil || len(c[0].Project.Ops) != 1 {
		t.Errorf("projection did not survive decode: %#v", c[0].Project)
	}
	if !c[1].Tombstone {
		t.Errorf("tombstone did not survive decode: %#v", c[1])
	}
}

// TestBadComputedFailsSurfaceDecode: a surface carrying an invalid computed decl must
// not load. Reporting the problem but keeping the surface would render it with a missing
// layer, which is the silent-wrong-file outcome A12 rules out.
func TestBadComputedFailsSurfaceDecode(t *testing.T) {
	data := []byte(`[{"agent":"x","name":"c","path":"~/c.json","codec":"json",
	  "computed":[{"from":"nope","to":"mcp"}]}]`)
	surfaces, problems := DecodeSurfaces(data)
	if len(problems) == 0 {
		t.Fatal("want a problem for an unknown computed source")
	}
	if len(surfaces) != 0 {
		t.Errorf("a surface with a bad computed decl must not load, got %#v", surfaces)
	}
}

// TestComputedRejectsUnknownField: strict decoding, same reason as the rest of the DTO
// — a misspelled key would be a declaration that does nothing, with no signal.
func TestComputedRejectsUnknownField(t *testing.T) {
	var c Computed
	dec := json.NewDecoder(strings.NewReader(`{"from":"mcp_servers","omit_empty":true}`))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err == nil {
		t.Error("want an error for a snake_case misspelling of omitEmpty")
	}
}
