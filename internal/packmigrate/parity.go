// Package packmigrate rewrites a pack's legacy pack.json as the grouped pack.toml and moves its
// plain-copy `files` content into the conventional home/ folder — `yolo pack migrate`'s library
// (docs/design/pack-manifests.md §5).
//
// A migration PROVES EQUALITY OR WRITES NOTHING. Equality is not the manifest text: it is the
// normalized manifest accessors (ManifestDifferences), the content and mode of every delivered
// file (Delivery), and the selected posture's replace-or-append semantics, compared before the
// first write and again after the last one.
package packmigrate

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// ManifestDifferences lists every way got's normalized meaning differs from old's, or nil when
// the two say the same thing. Both sides are normalized by the grouped reader's accepted
// defaults first — an omitted pack-tree run is the bin, an omitted state scope is workspace, an
// omitted surface codec is its path's — so a fact written on one side and defaulted on the other
// is not a difference. Install hints are NOT normalized: a hint is a recipe check-deps prints and
// the provisioner counts, so a manager the legacy manifest names no hint for must read back with
// none, and the grouped reader's bin default filling one in is a difference. Format provenance (origin,
// location) is not compared; what the format CHANGES is: an overlay row whose arrays append in
// one and replace in the other, and a launch row that resets earlier packs' rows in one and adds
// to them in the other.
func ManifestDifferences(old, got *packdecl.Manifest) []string {
	if old == nil || got == nil {
		return []string{"a manifest is missing"}
	}
	var diffs []string
	oldRoot, err := canonicalRoot(old)
	if err != nil {
		return []string{err.Error()}
	}
	gotRoot, err := canonicalRoot(got)
	if err != nil {
		return []string{err.Error()}
	}
	if !reflect.DeepEqual(oldRoot, gotRoot) {
		diffs = append(diffs, fmt.Sprintf("pack-wide keys differ: %s vs %s", compact(oldRoot), compact(gotRoot)))
	}
	if len(old.Contributes) != len(got.Contributes) {
		return append(diffs, fmt.Sprintf("contribution count %d vs %d", len(old.Contributes), len(got.Contributes)))
	}
	for i := range old.Contributes {
		a, b := old.Contributes[i], got.Contributes[i]
		if a.Kind != b.Kind {
			diffs = append(diffs, fmt.Sprintf("contribution %d: kind %q vs %q", i, a.Kind, b.Kind))
			continue
		}
		ca, err := canonicalContribution(a)
		if err != nil {
			return append(diffs, err.Error())
		}
		cb, err := canonicalContribution(b)
		if err != nil {
			return append(diffs, err.Error())
		}
		if !reflect.DeepEqual(ca, cb) {
			diffs = append(diffs, fmt.Sprintf("contribution %d (%s): %s vs %s", i, a.Kind, compact(ca), compact(cb)))
		}
		if !reflect.DeepEqual(groupedProviderFacts(a), groupedProviderFacts(b)) {
			diffs = append(diffs, fmt.Sprintf("contribution %d (%s): provider model rows differ", i, a.Kind))
		}
	}
	diffs = append(diffs, overlaySemanticsDifferences(old, got)...)
	diffs = append(diffs, launchSemanticsDifferences(old, got)...)
	return diffs
}

func canonicalRoot(m *packdecl.Manifest) (any, error) {
	copied := *m
	copied.Contributes = nil
	copied.Origin = packdecl.ManifestOrigin{}
	return canonicalJSON(copied)
}

// canonicalContribution is one contribution's JSON meaning with the grouped defaults applied
// and the format's provenance cleared.
func canonicalContribution(c packdecl.Contribution) (any, error) {
	c.Origin, c.Location, c.Replace = packdecl.ManifestOrigin{}, packdecl.ManifestLocation{}, false
	switch c.Kind {
	case packdecl.KindProgram:
		if c.Via == packdecl.ViaPack && c.Run == "" {
			c.Run = c.Bin
		}
	case packdecl.KindState:
		if c.Scope == "" {
			c.Scope = "workspace"
		}
	}
	value, err := canonicalJSON(c)
	if err != nil {
		return nil, err
	}
	fields, _ := value.(map[string]any)
	switch c.Kind {
	case packdecl.KindConfig:
		fillCodecs(fields["config"])
	case packdecl.KindAutonomy:
		for _, posture := range []string{"autonomous", "guarded"} {
			if decl, ok := fields[posture].(map[string]any); ok {
				fillCodecs(decl["config"])
			}
		}
	}
	return value, nil
}

// fillCodecs applies the grouped reader's extension-inferred codec to every surface row that
// omits one.
func fillCodecs(value any) {
	rows, ok := value.([]any)
	if !ok {
		if row, isRow := value.(map[string]any); isRow {
			rows = []any{row}
		}
	}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, explicit := row["codec"]; explicit {
			continue
		}
		path, _ := row["path"].(string)
		if codec := inferredCodec(path); codec != "" {
			row["codec"] = codec
		}
	}
}

func inferredCodec(path string) string {
	ext := ""
	for i := len(path) - 1; i >= 0 && path[i] != '/' && path[i] != '\\'; i-- {
		if path[i] == '.' {
			ext = path[i+1:]
			break
		}
	}
	switch ext {
	case "json", "jsonc", "JSON", "JSONC":
		return "json"
	case "toml", "TOML":
		return "toml"
	case "yaml", "yml", "YAML", "YML":
		return "yaml"
	}
	return ""
}

type providerFacts struct {
	Aliases      []string
	Model        string
	Every        *packdecl.ProviderModel
	Models       map[string]packdecl.ProviderModel
	ModelOrder   []string
	Variants     map[string]packdecl.ProviderVariant
	VariantOrder []string
}

func groupedProviderFacts(c packdecl.Contribution) providerFacts {
	return providerFacts{Aliases: c.ProviderAliases, Model: c.ProviderModel, Every: c.EveryProviderModel,
		Models: c.ProviderModels, ModelOrder: c.ProviderModelOrder, Variants: c.ProviderVariants, VariantOrder: c.ProviderVariantOrder}
}

// appendsArrays is the fold's rule (packoverlay.overlayAppendsArrays, packload's posture fold).
func appendsArrays(origin packdecl.ManifestOrigin, replace bool) bool {
	return origin.Format == packdecl.ManifestFormatTOML && !replace
}

func overlaySemanticsDifferences(old, got *packdecl.Manifest) []string {
	a, b := old.OverlayContributions(), got.OverlayContributions()
	if len(a) != len(b) {
		return []string{fmt.Sprintf("overlay row count %d vs %d", len(a), len(b))}
	}
	var diffs []string
	for i := range a {
		if appendsArrays(a[i].Origin, a[i].Replace) == appendsArrays(b[i].Origin, b[i].Replace) {
			continue
		}
		var body any
		if err := json.Unmarshal(b[i].Config, &body); err == nil && !holdsArray(body) {
			continue
		}
		diffs = append(diffs, fmt.Sprintf("overlay %s row %d: its arrays %s in one manifest and %s in the other; write replace = true to keep replacing",
			a[i].Surface, i, verb(appendsArrays(a[i].Origin, a[i].Replace)), verb(appendsArrays(b[i].Origin, b[i].Replace))))
	}
	return diffs
}

func verb(appends bool) string {
	if appends {
		return "append"
	}
	return "replace"
}

func holdsArray(value any) bool {
	switch v := value.(type) {
	case []any:
		return true
	case map[string]any:
		for _, item := range v {
			if holdsArray(item) {
				return true
			}
		}
	}
	return false
}

// launchSemanticsDifferences compares, for each posture, whether each launch row resets a bin's
// earlier rows (packload.launchFlagClaims: Replace, or any legacy row) or adds to them.
func launchSemanticsDifferences(old, got *packdecl.Manifest) []string {
	var diffs []string
	for _, enabled := range []bool{true, false} {
		a, b := old.PostureFor(enabled), got.PostureFor(enabled)
		var ra, rb []packdecl.AutonomyLaunch
		if a != nil {
			ra = a.Launch
		}
		if b != nil {
			rb = b.Launch
		}
		if len(ra) != len(rb) {
			diffs = append(diffs, fmt.Sprintf("posture (autonomy %t): launch row count %d vs %d", enabled, len(ra), len(rb)))
			continue
		}
		for i := range ra {
			if resets(ra[i], old) != resets(rb[i], got) {
				diffs = append(diffs, fmt.Sprintf("posture (autonomy %t): launch row %d for %q replaces earlier packs' flags in one manifest and adds to them in the other; write replace = true to keep replacing",
					enabled, i, ra[i].Bin))
			}
		}
	}
	return diffs
}

func resets(row packdecl.AutonomyLaunch, m *packdecl.Manifest) bool {
	origin := row.Origin
	if origin.Format == 0 {
		origin = m.Origin
	}
	return row.Replace || origin.Format != packdecl.ManifestFormatTOML
}

func canonicalJSON(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func compact(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	if len(data) > 400 {
		return string(data[:400]) + "…"
	}
	return string(data)
}
