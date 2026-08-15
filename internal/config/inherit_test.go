package config

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE CENSUS DRIFT TEST (OQ-LP9 R3). A new top-level config key must be classified in
// inheritCensus — into one file, both, or explicitly NEITHER — or this fails. That is the
// forcing function the design asks for: Go has no exhaustiveness check over a map, so the
// only way "every key is classified once" stays true is a test that walks the schema's own
// key set.
//
// It is deliberately keyed on knownTopLevelConfigKeys rather than on a hand-written list,
// because that set is what `yolo check` validates against — so the census cannot fall
// behind the schema without the schema's own gatekeeper noticing.
func TestInheritCensusIsTotal(t *testing.T) {
	var unclassified []string
	for key := range knownTopLevelConfigKeys {
		if _, ok := inheritCensus[key]; !ok {
			unclassified = append(unclassified, key)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Fatalf("config keys with no inherit classification: %v\n\n"+
			"Every top-level key must be assigned in inheritCensus (internal/config/inherit.go) to\n"+
			"  • the PREFLIGHT file  — an in-jail read-only command evaluates it meaningfully, or\n"+
			"  • the NESTED file     — an inner launcher composes a jail from it, or\n"+
			"  • BOTH, or\n"+
			"  • NEITHER, with the reason (a host referent the container lacks, or a grant\n"+
			"    whose meaning changes across the boundary).\n"+
			"Assigning to neither is a decision on the record, not an omission — write the reason.",
			unclassified)
	}
}

// The census must not classify a key the SCHEMA does not know. This is the other direction
// of the same drift: a key removed from the config would otherwise leave a census entry
// nothing reads, which is exactly the kind of assertion-nobody-checks that
// render/fieldset.go's comment argues against.
func TestInheritCensusHasNoPhantomKeys(t *testing.T) {
	var phantom []string
	for key := range inheritCensus {
		if _, ok := knownTopLevelConfigKeys[key]; !ok {
			phantom = append(phantom, key)
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("inheritCensus classifies keys the config schema does not have: %v — "+
			"drop the entry, or add the key to knownTopLevelConfigKeys", phantom)
	}
}

// EVERY classification carries a reason, including (especially) the exclusions. A key
// dropped with no stated reason is indistinguishable from a key someone forgot, which is
// the whole failure the census exists to prevent.
func TestInheritCensusReasonsAreStated(t *testing.T) {
	for key, d := range inheritCensus {
		if strings.TrimSpace(d.reason) == "" {
			t.Errorf("inheritCensus[%q] has no reason — state which consumer reads it, or "+
				"why neither can", key)
		}
	}
}

// THE FALSE-ERROR CLASS THIS FEATURE EXISTS TO KILL (OQ-LP9 R1/R9). Each key below was
// MEASURED, 2026-08-14, to make an in-jail `yolo check` report a problem the user does not
// have — a host referent evaluated against a container that legitimately lacks it. The
// filter is what kills them: the key is simply not in the file, so nothing evaluates it.
//
// This is the unit half of the integration case R9 asks for; the command-level half is
// TestInJailCheckIsSilentAboutFilteredHostKeys in internal/cli/check.
func TestPreflightFilterDropsTheFalseErrorKeys(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	// cache_relocations — the named class: "parent directory of the target does not exist".
	effective.Set("cache_relocations", mapOf(t, "npm", "/mnt/bigdisk/caches/npm"))
	// gpu — the loudest: four fails about nvidia-smi/nvidia-ctk/runc/CDI.
	effective.Set("gpu", mapOf(t, "enabled", true))
	// mounts — "host path does not exist and will be skipped", once per entry.
	effective.Set("mounts", []any{"/Volumes/bigdisk/data:/ctx/data:ro"})
	// devices — host /dev/bus/usb paths and lsusb, neither present in a container.
	effective.Set("devices", []any{mapOf(t, "usb", "1234:5678")})
	// kvm — a host device passthrough.
	effective.Set("kvm", true)
	// env_sources — "env_sources file not found, skipping" for a host dotenv path.
	effective.Set("env_sources", []any{"~/.acme.env"})
	// host_files — the misread grant: "the host home" becomes the jail's own.
	effective.Set("host_files", []any{mapOf(t, "path", ".config/acme/creds", "source", "~/.config/acme/creds")})
	// runtime — a host value naming a machine this container is not.
	effective.Set("runtime", "macos-user")
	// And one key that MUST survive, so the test proves a filter rather than an empty file.
	effective.Set("loopholes", mapOf(t, "acme", jsonx.NewOrderedMap()))

	got, unknown := FilterInherit(effective, InheritPreflight)
	if len(unknown) > 0 {
		t.Fatalf("fixture uses keys the census does not know: %v", unknown)
	}
	for _, key := range []string{
		"cache_relocations", "gpu", "mounts", "devices", "kvm", "env_sources",
		"host_files", "runtime",
	} {
		if _, present := got.Get(key); present {
			_, _, reason, _ := InheritDisposition(key)
			t.Errorf("%q survived into the PREFLIGHT file — it is excluded because %s. "+
				"An in-jail reader evaluating it reports a problem the user does not have.",
				key, reason)
		}
	}
	if _, present := got.Get("loopholes"); !present {
		t.Error("loopholes must survive into the preflight file — `yolo loopholes list` " +
			"reads it in-jail, and dropping it makes the command blind to the human's installs")
	}
}

// The NESTED file carries what an inner LAUNCHER composes a jail from, and nothing a
// launcher cannot act on. `packages` is the case that makes the split worth having: it is
// meaningless to an in-jail reader (an in-jail `yolo check --no-build` skips the image
// section entirely, measured) and essential to an inner launcher.
func TestNestedFilterCarriesTheLaunchComposition(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	effective.Set("packages", []any{"postgresql"})
	effective.Set("mise_tools", mapOf(t, "node", "22"))
	effective.Set("packs", []any{"claude"})
	effective.Set("resources", mapOf(t, "memory", "8g"))
	effective.Set("cache_relocations", mapOf(t, "npm", "/mnt/bigdisk/npm"))
	// agents_md_extra is genuinely preflight-only: it is prose rendered into THIS jail's
	// briefing, and an inner launcher composes its child's briefing from its own config.
	effective.Set("agents_md_extra", "## a note for this jail's agents\n")

	got, unknown := FilterInherit(effective, InheritNested)
	if len(unknown) > 0 {
		t.Fatalf("fixture uses keys the census does not know: %v", unknown)
	}
	for _, want := range []string{"packages", "mise_tools", "packs", "resources"} {
		if _, present := got.Get(want); !present {
			t.Errorf("%q missing from the NESTED file — an inner launcher composes its jail "+
				"from it, so a nested jail would silently lose it", want)
		}
	}
	// The rw host grant must not travel to a nested launcher either: a nested jail's cache
	// is its own per-workspace dir, and inheriting the target would hand podman a bind
	// source that does not exist in the container.
	if _, present := got.Get("cache_relocations"); present {
		t.Error("cache_relocations must not reach the nested file — it is a rw HOST mount, " +
			"and its target is absent from the container an inner launcher runs in")
	}
	// A preflight-only key must not leak into the nested file: the two are separate
	// consumers, so "in one" must not silently mean "in both". This is the assertion that
	// keeps the split real — several keys legitimately earn BOTH memberships (security,
	// mise_tools, the MCP/LSP trio, measured against check's entrypoint dry-run), and
	// without a case like this one nothing would notice the filters collapsing into one.
	if _, present := got.Get("agents_md_extra"); present {
		t.Error("agents_md_extra is preflight-only (prose for THIS jail's briefing); " +
			"finding it in the nested file means the two filters are not distinct")
	}
}

// include_if_found is CONSUMED, never emitted. Its entries are paths relative to the file
// that named them, so a jail re-resolving them would hunt for host-relative siblings of a
// generated file — the accident the old raw bind made by omission (only two filenames
// crossed, so includes stayed host-side and the in-jail scope was neither the effective
// config nor a designed subset).
func TestInheritNeverEmitsIncludeIfFound(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	effective.Set("include_if_found", []any{"overrides.jsonc"})
	effective.Set("packs", []any{"claude"})
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		got, _ := FilterInherit(effective, scope)
		if _, present := got.Get("include_if_found"); present {
			t.Errorf("%s: include_if_found must be consumed by composition, not emitted", scope)
		}
	}
}

// INCLUDE CONTENT NOW CROSSES, which is the third of the three defects OQ-LP9 names in the
// old raw bind and the only one that was a straight LOSS rather than a false error.
//
// The old mount loop named exactly two files (config.jsonc, config.lua), so a user whose
// config's first line is `include_if_found: ["overrides.jsonc"]` had the included half stay
// host-side — the in-jail "user scope" was neither the effective config nor a designed
// subset. Rendering from the EFFECTIVE config fixes it for free, because the includes are
// already merged in by the time the filter runs. Verified against the real CLI 2026-08-14:
// an `mcp_servers` block living in overrides.jsonc appears in `yolo config dump`.
//
// This is the unit pin: the include's CONTENT survives while the include DIRECTIVE does not.
func TestIncludedContentCrossesEvenThoughTheDirectiveDoesNot(t *testing.T) {
	// The effective config as LoadConfig produces it: includes already merged, the
	// directive itself consumed by LoadJSONCWithIncludes... except when a caller hands us a
	// map that still carries it, which is why FilterInherit drops the key explicitly.
	effective := jsonx.NewOrderedMap()
	effective.Set("include_if_found", []any{"overrides.jsonc"})
	effective.Set("mcp_servers", mapOf(t, "tavily", mapOf(t, "command", "npx"))) // from the include
	effective.Set("packs", []any{"claude"})

	got, unknown := FilterInherit(effective, InheritPreflight)
	if len(unknown) > 0 {
		t.Fatalf("unknown keys: %v", unknown)
	}
	if _, present := got.Get("mcp_servers"); !present {
		t.Error("content that arrived via include_if_found did not cross — the in-jail user " +
			"scope is again \"whatever happened to be in the top file\", which is the third " +
			"defect OQ-LP9 names in the raw bind")
	}
	if _, present := got.Get("include_if_found"); present {
		t.Error("the include DIRECTIVE crossed; only its merged content may")
	}
}

// An UNCLASSIFIED key is dropped AND NAMED. Silently passing it through would defeat the
// census (a host-shaped key would reach a jail unreviewed); silently dropping it would hide
// a schema addition. This is the same all-or-nothing-plus-report contract loopholedecl.Decode
// took for manifest keys.
func TestFilterInheritNamesUnknownKeys(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	effective.Set("packs", []any{"claude"})
	effective.Set("brand_new_key", "whatever")
	got, unknown := FilterInherit(effective, InheritPreflight)
	if _, present := got.Get("brand_new_key"); present {
		t.Error("an unclassified key must not pass through the filter")
	}
	if len(unknown) != 1 || unknown[0] != "brand_new_key" {
		t.Errorf("unknown = %v, want [brand_new_key] — a dropped key has to be reportable", unknown)
	}
}

// The header names PURPOSE, GENERATOR and launch-frozen-ness (the maintainer's three), and
// the two scopes' headers must DIFFER — a nested-launch file that reads like a preflight
// file gives the reader no way to tell which consumer it serves.
func TestInheritHeaderNamesPurposeGeneratorAndFreezing(t *testing.T) {
	pre := InheritHeader(InheritPreflight, "2026-08-14T12:00:00Z")
	nest := InheritHeader(InheritNested, "2026-08-14T12:00:00Z")
	for _, want := range []string{"GENERATED", "PURPOSE", "GENERATOR", "LAUNCH-FROZEN", "config drift"} {
		if !strings.Contains(pre, want) {
			t.Errorf("preflight header is missing %q", want)
		}
		if !strings.Contains(nest, want) {
			t.Errorf("nested header is missing %q", want)
		}
	}
	// Each states its OWN reason for existing, which is what the ruling asks be marked.
	if !strings.Contains(pre, "yolo check") {
		t.Error("the preflight header must name the readers it is for")
	}
	if !strings.Contains(nest, "JAIL-IN-JAIL") {
		t.Error("the nested header must say it exists for nesting — that is the mark the ruling asks for")
	}
	if !strings.Contains(nest, "cannot nest") {
		t.Error("the nested header must say a non-nesting backend gets no file at all")
	}
	if pre == nest {
		t.Error("the two headers are identical — a reader cannot tell which consumer the file serves")
	}
	// The timestamp is omissible so a golden can be byte-stable.
	if strings.Contains(InheritHeader(InheritPreflight, ""), "GENERATED AT") {
		t.Error("an empty launchedAt must omit the timestamp line")
	}
}

// A rendered file PARSES as the config it claims to be — through the ordinary loader, with
// no special case. If it did not, every in-jail reader would need a bespoke parser and the
// "it is just the user config" property would be a fiction.
func TestRenderedInheritFileParsesAsConfig(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	effective.Set("packs", []any{"claude"})
	effective.Set("loopholes", mapOf(t, "acme", mapOf(t, "enabled", true)))
	effective.Set("gpu", mapOf(t, "enabled", true)) // filtered out

	rendered, unknown, err := RenderInherit(effective, InheritPreflight, "2026-08-14T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) > 0 {
		t.Fatalf("unknown keys: %v", unknown)
	}
	dir := t.TempDir()
	path := dir + "/config.jsonc"
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := LoadJSONCFile(path, "generated user config", true, nil)
	if err != nil {
		t.Fatalf("the generated file does not parse through the ordinary loader: %v\n%s", err, rendered)
	}
	if _, present := parsed.Get("packs"); !present {
		t.Error("packs did not survive the render/parse round trip")
	}
	if _, present := parsed.Get("gpu"); present {
		t.Error("gpu survived into a parsed preflight file")
	}
	// The header has to be COMMENTS, or the JSONC parse above would have failed — assert
	// it is actually present, so a future change that drops the header (making this test
	// pass trivially) is caught.
	if !strings.HasPrefix(rendered, "// GENERATED") {
		t.Error("the rendered file must open with its generated-at-launch header comment")
	}
}

// RECURSION IS BY COMPOSITION, NOT STACKING (OQ-LP9 R6). Filtering an already-filtered
// config must be a no-op, because that is what makes depth-N identical to depth-1: jail A
// filters its effective config for B, B filters ITS effective config (which includes what A
// gave it) for C. If the filter were not idempotent, each level would lose or gain keys and
// there would be a rule that changes with nesting.
func TestInheritFilterIsIdempotentAcrossNestingLevels(t *testing.T) {
	level0 := jsonx.NewOrderedMap()
	level0.Set("packs", []any{"claude"})
	level0.Set("packages", []any{"postgresql"})
	level0.Set("loopholes", mapOf(t, "acme", mapOf(t, "enabled", true)))
	level0.Set("gpu", mapOf(t, "enabled", true))
	level0.Set("cache_relocations", mapOf(t, "npm", "/mnt/big/npm"))

	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		level1, _ := FilterInherit(level0, scope)
		level2, _ := FilterInherit(level1, scope)
		level3, _ := FilterInherit(level2, scope)
		one, err := SnapshotJSON(level1)
		if err != nil {
			t.Fatal(err)
		}
		three, err := SnapshotJSON(level3)
		if err != nil {
			t.Fatal(err)
		}
		if one != three {
			t.Errorf("%s: depth 1 and depth 3 differ, so the rule changes with nesting:\n"+
				"depth1=%s\ndepth3=%s", scope, one, three)
		}
	}
}

// mapOf builds an OrderedMap from alternating key/value pairs (test sugar).
func mapOf(t *testing.T, kv ...any) *jsonx.OrderedMap {
	t.Helper()
	if len(kv)%2 != 0 {
		t.Fatal("mapOf needs key/value pairs")
	}
	m := jsonx.NewOrderedMap()
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			t.Fatalf("mapOf key %v is not a string", kv[i])
		}
		m.Set(k, kv[i+1])
	}
	return m
}
