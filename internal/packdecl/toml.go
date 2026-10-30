package packdecl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ManifestFormat records which manifest reader produced a normalized Manifest.
type ManifestFormat uint8

const (
	ManifestFormatJSON ManifestFormat = iota + 1
	ManifestFormatTOML
)

// ManifestOrigin is carried with the normalized manifest so consumers can keep
// legacy and grouped-TOML semantics distinct during the reader transition.
type ManifestOrigin struct {
	Format ManifestFormat
	File   string
}

// ManifestLocation identifies a grouped TOML declaration in the source file.
type ManifestLocation struct {
	File         string
	Key          string
	Line, Column int
}

// ProviderVariant is one named provider configuration in grouped TOML.
type ProviderVariant struct {
	Model         string                   `json:"model,omitempty"`
	Via           string                   `json:"via,omitempty"`
	Options       map[string]OptionDefault `json:"options,omitempty"`
	Subagents     map[string]any           `json:"subagents,omitempty"`
	EnforceModels *bool                    `json:"enforce_models,omitempty"`
	Capabilities  []string                 `json:"capabilities,omitempty"`
	Protocols     []string                 `json:"protocols,omitempty"`
}

// ProviderModel contains one model row's shared, provider-independent facts.
type ProviderModel struct {
	// Model is the id a row keyed by a name lists and registers its model under
	// (docs/design/model-facts-migration.md MF-D1); a row without it is the model its key names.
	// It is a row's own, so `every_model` and a `model_facts` entry refuse it.
	Model             string         `json:"model,omitempty"`
	Name              string         `json:"name,omitempty"`
	Description       string         `json:"description,omitempty"`
	Vendor            string         `json:"vendor,omitempty"`
	API               string         `json:"api,omitempty"`
	Thinking          []string       `json:"thinking,omitempty"`
	Images            *ProviderImage `json:"images,omitempty"`
	Price             *ProviderPrice `json:"price,omitempty"`
	Context           int64          `json:"context,omitempty"`
	MaxTokens         int64          `json:"max_tokens,omitempty"`
	Reasoning         *bool          `json:"reasoning,omitempty"`
	Accepts           []string       `json:"accepts,omitempty"`
	Base              string         `json:"base,omitempty"`
	WireID            string         `json:"wire_id,omitempty"`
	Extends           string         `json:"extends,omitempty"`
	OpenRouterRouting map[string]any `json:"openrouter_routing,omitempty"`
	TwinOf            string         `json:"twin_of,omitempty"`
	CompactAt         int64          `json:"compact_at,omitempty"`
}

// ProviderImage bounds image input for a model.
type ProviderImage struct {
	MaxWidth    int64 `json:"max_width,omitempty"`
	MaxHeight   int64 `json:"max_height,omitempty"`
	MaxBytes    int64 `json:"max_bytes,omitempty"`
	JPEGQuality int64 `json:"jpeg_quality,omitempty"`
}

// ProviderPrice contains per-million-token rates for a model.
type ProviderPrice struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

type manifestReadMode uint8

const (
	manifestStrict manifestReadMode = iota
	manifestUse
	manifestTolerant
)

type groupedContribution struct {
	kind            Kind
	group           string
	index           int
	start           int
	value           map[string]any
	replace         bool
	location        ManifestLocation
	provider        *providerShape
	launchMetadata  map[Posture][]AutonomyLaunch
	configLocations map[Posture][]ManifestLocation
	configReplaces  map[Posture][]bool
}

type groupedLaunch struct {
	row   AutonomyLaunch
	value any
}

type providerShape struct {
	alias        string
	model        string
	variants     map[string]ProviderVariant
	variantOrder []string
	everyModel   *ProviderModel
	models       map[string]ProviderModel
	modelOrder   []string
}

// DecodeTOML reads a strict authoring manifest from grouped TOML.
func DecodeTOML(data []byte) (*Manifest, []string) {
	m, problems, _ := decodeTOML(data, manifestStrict)
	return m, problems
}

// DecodeTOMLForUse reads a grouped TOML manifest with the host-use skew policy.
func DecodeTOMLForUse(data []byte) (*Manifest, []string, []string) {
	return decodeTOML(data, manifestUse)
}

// DecodeTOMLTolerant reads a grouped TOML manifest across the jail version boundary.
func DecodeTOMLTolerant(data []byte) (*Manifest, []string, []string) {
	return decodeTOML(data, manifestTolerant)
}

func decodeTOML(data []byte, mode manifestReadMode) (*Manifest, []string, []string) {
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		return nil, []string{ManifestTOMLName + ": " + err.Error()}, nil
	}
	root, ok := jsonx.Plain(doc.Values).(map[string]any)
	if !ok {
		return nil, []string{ManifestTOMLName + ": expected a TOML table at the root"}, nil
	}
	normalized, groups, problems, preSkipped := normalizeGrouped(root, doc, mode)
	if len(problems) > 0 {
		return nil, problems, preSkipped
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, []string{ManifestTOMLName + ": " + err.Error()}, nil
	}
	var m *Manifest
	var skipped []string
	var retainedIndexes []int
	switch mode {
	case manifestStrict:
		m, problems = Decode(encoded)
		if m != nil {
			retainedIndexes = make([]int, len(m.Contributes))
			for i := range retainedIndexes {
				retainedIndexes[i] = i
			}
		}
	case manifestUse:
		m, problems, skipped, retainedIndexes = decodeForUseWithIndexes(encoded)
	case manifestTolerant:
		m, problems, skipped, retainedIndexes = decodeTolerantWithIndexes(encoded)
	}
	if m == nil {
		return nil, locateTOMLProblems(problems, groups, root, doc), append(preSkipped, locateTOMLProblems(skipped, groups, root, doc)...)
	}
	m.Origin = ManifestOrigin{Format: ManifestFormatTOML, File: ManifestTOMLName}
	for i, groupIndex := range retainedIndexes {
		if i >= len(m.Contributes) || groupIndex < 0 || groupIndex >= len(groups) {
			continue
		}
		entry := groups[groupIndex]
		m.Contributes[i].Origin = m.Origin
		m.Contributes[i].Location = entry.location
		m.Contributes[i].Replace = entry.replace
		if entry.provider != nil {
			m.Contributes[i].ProviderAlias = entry.provider.alias
			m.Contributes[i].ProviderModel = entry.provider.model
			m.Contributes[i].ProviderVariants = entry.provider.variants
			m.Contributes[i].ProviderVariantOrder = append([]string(nil), entry.provider.variantOrder...)
			m.Contributes[i].EveryProviderModel = entry.provider.everyModel
			m.Contributes[i].ProviderModels = entry.provider.models
			m.Contributes[i].ProviderModelOrder = append([]string(nil), entry.provider.modelOrder...)
		}
		if m.Contributes[i].Kind == KindAutonomy {
			for _, half := range []struct {
				posture Posture
				decl    *AutonomyPosture
			}{{PostureAutonomous, m.Contributes[i].Autonomous}, {PostureGuarded, m.Contributes[i].Guarded}} {
				if half.decl == nil {
					continue
				}
				half.decl.configLocations = append([]ManifestLocation(nil), entry.configLocations[half.posture]...)
				half.decl.configReplaces = append([]bool(nil), entry.configReplaces[half.posture]...)
				for rowIndex, row := range entry.launchMetadata[half.posture] {
					if rowIndex < len(half.decl.Launch) {
						half.decl.Launch[rowIndex].Origin = m.Origin
						half.decl.Launch[rowIndex].Location = row.Location
						half.decl.Launch[rowIndex].Replace = row.Replace
					}
				}
			}
		}
	}
	return m, locateTOMLProblems(problems, groups, root, doc), append(preSkipped, locateTOMLProblems(skipped, groups, root, doc)...)
}

func normalizeGrouped(root map[string]any, doc *configdoc.Document, mode manifestReadMode) (map[string]any, []groupedContribution, []string, []string) {
	out := make(map[string]any, len(root)+1)
	var groups []groupedContribution
	var launchRows []groupedLaunch
	var problems []string
	var skipped []string
	for _, key := range sortedKeys(root) {
		value := root[key]
		switch {
		case key == "launch":
			rows, rowProblems := normalizeGroupedLaunches(value, doc)
			launchRows = append(launchRows, rows...)
			problems = append(problems, rowProblems...)
		case key == "contributes":
			loc := locationFor(doc, []configdoc.Step{{Key: key}}, key)
			problems = append(problems, formatManifestLocation(loc, "\"contributes\" is the legacy pack.json shape; group entries by kind as [[program]], [[overlay]], and similar tables"))
		case key == "providers":
			providerTable, ok := value.(map[string]any)
			if !ok {
				loc := locationFor(doc, []configdoc.Step{{Key: key}}, key)
				problems = append(problems, formatManifestLocation(loc, "providers must be a table keyed by provider name"))
				continue
			}
			for _, name := range orderedTableKeys(doc, []configdoc.Step{{Key: "providers"}}, providerTable) {
				raw := providerTable[name]
				fields, ok := raw.(map[string]any)
				if !ok {
					loc := locationFor(doc, []configdoc.Step{{Key: "providers"}, {Key: name}}, "providers."+name)
					problems = append(problems, formatManifestLocation(loc, "provider declaration must be a table"))
					continue
				}
				loc := locationForProvider(doc, name)
				copyFields := cloneMap(fields)
				replace, err := takeReplace(copyFields)
				if err != nil {
					problems = append(problems, formatManifestLocation(loc, "replace: "+err.Error()))
					continue
				}
				if renamed := providerRenamedField(copyFields); renamed != nil {
					fieldLoc := providerUnknownLocation(doc, name, copyFields, renamed.Path)
					problems = append(problems, formatManifestLocation(fieldLoc, renameMessage(*renamed.Rename)))
					continue
				}
				if unknown := providerShapeUnknownField(copyFields); unknown != "" {
					problem := "unknown provider model field " + strconv.Quote(unknown)
					if mode == manifestStrict {
						problems = append(problems, formatManifestLocation(providerUnknownLocation(doc, name, copyFields, unknown), problem))
					} else {
						skipped = append(skipped, formatManifestLocation(loc, "skipping provider declaration with "+problem+"; update yolo to read it"))
					}
					continue
				}
				value, shape, err := normalizeProvider(name, copyFields, doc)
				if err != nil {
					problems = append(problems, formatManifestLocation(loc, err.Error()))
					continue
				}
				groups = append(groups, groupedContribution{kind: KindProvider, group: "providers." + name, index: 0, start: locOffset(doc, loc), value: value, replace: replace, location: loc, provider: shape})
			}
		case isGroupedKind(key, value):
			kind := groupKind(key)
			if kind == "" {
				kind = Kind(strings.ReplaceAll(key, "_", "-"))
			}
			items, ok := value.([]any)
			if !ok {
				loc := locationFor(doc, []configdoc.Step{{Key: key}}, key)
				problems = append(problems, formatManifestLocation(loc, fmt.Sprintf("[[%s]] must be an array of tables", key)))
				continue
			}
			for i, item := range items {
				fields, ok := item.(map[string]any)
				if !ok {
					loc := locationFor(doc, []configdoc.Step{{Key: key}, {Index: i, IsIndex: true}}, fmt.Sprintf("%s[%d]", key, i))
					problems = append(problems, formatManifestLocation(loc, "contribution entry must be a table"))
					continue
				}
				loc := locationFor(doc, []configdoc.Step{{Key: key}, {Index: i, IsIndex: true}}, fmt.Sprintf("%s[%d]", key, i))
				copyFields := cloneMap(fields)
				if rename, old := groupedRename(kind, copyFields); rename != nil {
					problems = append(problems, formatManifestLocation(loc, renameMessage(*rename)+" (field "+strconv.Quote(old)+")"))
					continue
				}
				if kind == KindConfigOverlay {
					if set, ok := copyFields["set"]; ok {
						copyFields["config"] = map[string]any{"managed": set}
						delete(copyFields, "set")
					}
				}
				if kind == KindProgram {
					if err := normalizeProgramDefaults(copyFields); err != nil {
						problems = append(problems, formatManifestLocation(loc, err.Error()))
						continue
					}
					if err := normalizeBuiltInProviderPlanOff(copyFields); err != nil {
						problems = append(problems, formatManifestLocation(loc, err.Error()))
						continue
					}
				} else if kind == KindState {
					if _, authored := copyFields["scope"]; !authored {
						copyFields["scope"] = "workspace"
					}
				} else if kind == KindProvider {
					if err := lowerOptionsWithoutDefault(copyFields); err != nil {
						problems = append(problems, formatManifestLocation(loc, err.Error()))
						continue
					}
				}
				var nestedLocations map[Posture][]ManifestLocation
				var nestedLaunches map[Posture][]AutonomyLaunch
				if kind == KindConfig {
					if err := lowerSurfaceUnset(copyFields["config"], fmt.Sprintf("%s[%d].config", key, i)); err != nil {
						problems = append(problems, formatManifestLocation(loc, err.Error()))
						continue
					}
					if err := defaultSurfaceCodecs(copyFields["config"], doc, []configdoc.Step{{Key: key}, {Index: i, IsIndex: true}, {Key: "config"}}, fmt.Sprintf("%s[%d].config", key, i)); err != nil {
						problems = append(problems, err.Error())
						continue
					}
				} else if kind == KindAutonomy {
					var err error
					nestedLocations, err = normalizeAutonomySurfaceCodecs(copyFields, doc, key, i)
					if err != nil {
						problems = append(problems, err.Error())
						continue
					}
					nestedLaunches = autonomyLaunchMetadata(copyFields, doc, key, i)
				}
				replace, err := takeReplace(copyFields)
				if err != nil {
					problems = append(problems, formatManifestLocation(loc, "replace: "+err.Error()))
					continue
				}
				copyFields["kind"] = string(kind)
				for posture, rows := range nestedLaunches {
					for j := range rows {
						rows[j].Replace = replace
					}
					nestedLaunches[posture] = rows
				}
				entry := groupedContribution{kind: kind, group: key, index: i, start: locOffset(doc, loc), value: copyFields, replace: replace, location: loc,
					launchMetadata: nestedLaunches, configLocations: nestedLocations, configReplaces: make(map[Posture][]bool)}
				for posture, locations := range nestedLocations {
					entry.configReplaces[posture] = make([]bool, len(locations))
					for j := range entry.configReplaces[posture] {
						entry.configReplaces[posture][j] = replace
					}
				}
				groups = append(groups, entry)
			}
		default:
			out[key] = value
		}
	}
	if len(launchRows) > 0 {
		groups = lowerGroupedLaunches(groups, launchRows, doc, &problems)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].start < groups[j].start })
	contributes := make([]any, 0, len(groups))
	for _, entry := range groups {
		contributes = append(contributes, entry.value)
	}
	if len(contributes) > 0 {
		out["contributes"] = contributes
	}
	return out, groups, problems, skipped
}

func autonomyLaunchMetadata(fields map[string]any, doc *configdoc.Document, group string, index int) map[Posture][]AutonomyLaunch {
	out := make(map[Posture][]AutonomyLaunch)
	for _, posture := range []Posture{PostureAutonomous, PostureGuarded} {
		decl, ok := fields[string(posture)].(map[string]any)
		if !ok {
			continue
		}
		rows, ok := decl["launch"].([]any)
		if !ok {
			continue
		}
		for i := range rows {
			key := fmt.Sprintf("%s[%d].%s.launch[%d]", group, index, posture, i)
			path := []configdoc.Step{{Key: group}, {Index: index, IsIndex: true}, {Key: string(posture)}, {Key: "launch"}, {Index: i, IsIndex: true}}
			out[posture] = append(out[posture], AutonomyLaunch{Location: locationFor(doc, path, key)})
		}
	}
	return out
}

func normalizeGroupedLaunches(value any, doc *configdoc.Document) ([]groupedLaunch, []string) {
	items, ok := value.([]any)
	if !ok {
		loc := locationFor(doc, []configdoc.Step{{Key: "launch"}}, "launch")
		return nil, []string{formatManifestLocation(loc, "[[launch]] must be an array of tables")}
	}
	var rows []groupedLaunch
	var problems []string
	for i, item := range items {
		key := fmt.Sprintf("launch[%d]", i)
		path := []configdoc.Step{{Key: "launch"}, {Index: i, IsIndex: true}}
		loc := locationFor(doc, path, key)
		fields, ok := item.(map[string]any)
		if !ok {
			problems = append(problems, formatManifestLocation(loc, "launch entry must be a table"))
			continue
		}
		copyFields := cloneMap(fields)
		replace, err := takeReplace(copyFields)
		if err != nil {
			problems = append(problems, formatManifestLocation(loc, "replace: "+err.Error()))
			continue
		}
		fieldsJSON, err := json.Marshal(copyFields)
		if err != nil {
			problems = append(problems, formatManifestLocation(loc, err.Error()))
			continue
		}
		var row AutonomyLaunch
		if err := json.Unmarshal(fieldsJSON, &row); err != nil {
			problems = append(problems, formatManifestLocation(loc, err.Error()))
			continue
		}
		row.Location = loc
		row.Replace = replace
		rows = append(rows, groupedLaunch{row: row, value: copyFields})
	}
	return rows, problems
}

func lowerGroupedLaunches(groups []groupedContribution, rows []groupedLaunch, doc *configdoc.Document, problems *[]string) []groupedContribution {
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].start < groups[j].start })
	index := -1
	for i := range groups {
		if groups[i].kind == KindAutonomy {
			index = i
			break
		}
	}
	if index < 0 {
		first := rows[0].row
		value := map[string]any{"kind": string(KindAutonomy)}
		groups = append(groups, groupedContribution{kind: KindAutonomy, group: "launch", start: locOffset(doc, first.Location), value: value,
			location: first.Location, configLocations: make(map[Posture][]ManifestLocation), configReplaces: make(map[Posture][]bool)})
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].start < groups[j].start })
		for i := range groups {
			if groups[i].group == "launch" && groups[i].kind == KindAutonomy {
				index = i
				break
			}
		}
	}
	entry := &groups[index]
	if entry.launchMetadata == nil {
		entry.launchMetadata = make(map[Posture][]AutonomyLaunch)
	}
	for _, posture := range []Posture{PostureAutonomous, PostureGuarded} {
		key := string(posture)
		decl, ok := entry.value[key].(map[string]any)
		if !ok {
			if _, exists := entry.value[key]; exists {
				*problems = append(*problems, formatManifestLocation(entry.location, fmt.Sprintf("%s posture must be a table", key)))
				continue
			}
			decl = make(map[string]any)
			entry.value[key] = decl
		}
		existing, exists := decl["launch"]
		var launch []any
		if exists {
			var valid bool
			launch, valid = existing.([]any)
			if !valid {
				*problems = append(*problems, formatManifestLocation(entry.location, fmt.Sprintf("%s.launch must be an array of tables", key)))
				continue
			}
		}
		metadata := entry.launchMetadata[posture]
		combined := make([]groupedLaunch, 0, len(launch)+len(rows))
		for i, item := range launch {
			row := AutonomyLaunch{}
			if i < len(metadata) {
				row = metadata[i]
			}
			combined = append(combined, groupedLaunch{row: row, value: item})
		}
		for _, row := range rows {
			combined = append(combined, groupedLaunch{row: row.row, value: row.value})
		}
		sort.SliceStable(combined, func(i, j int) bool {
			return locOffset(doc, combined[i].row.Location) < locOffset(doc, combined[j].row.Location)
		})
		launch = make([]any, 0, len(combined))
		entry.launchMetadata[posture] = make([]AutonomyLaunch, 0, len(combined))
		for _, row := range combined {
			if fields, ok := row.value.(map[string]any); ok {
				launch = append(launch, cloneMap(fields))
			} else {
				launch = append(launch, row.value)
			}
			entry.launchMetadata[posture] = append(entry.launchMetadata[posture], row.row)
		}
		decl["launch"] = launch
	}
	return groups
}

// lowerSurfaceUnset reads each config surface row's `unset`: a list of RFC 6901 JSON Pointers
// (the config-list `path` spelling, since real keys hold dots) naming keys the row's `managed`
// body DELETES. It is legacy JSON's null inside a managed merge patch, which TOML cannot write:
// each pointer becomes a null at that path in `managed`, creating the tables on the way, so the
// row reads exactly as the legacy row did. A pointer through a non-table, or onto a key
// `managed` also sets, is refused: the row would say two things about one key.
func lowerSurfaceUnset(value any, key string) error {
	rows, isList := value.([]any)
	if !isList {
		if row, ok := value.(map[string]any); ok {
			rows = []any{row}
		}
	}
	for i, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		unsetRaw, has := row["unset"]
		if !has {
			continue
		}
		delete(row, "unset")
		label := fmt.Sprintf("%s[%d].unset", key, i)
		pointers, ok := unsetRaw.([]any)
		if !ok {
			return fmt.Errorf("%s must be a list of JSON Pointers into managed, such as [\"/tui/notice\"]", label)
		}
		managed, _ := row["managed"].(map[string]any)
		if _, present := row["managed"]; present && managed == nil {
			return fmt.Errorf("%s: managed is not a table, so it has no key to unset", label)
		}
		managed = cloneMap(managed)
		for _, item := range pointers {
			pointer, ok := item.(string)
			if !ok {
				return fmt.Errorf("%s must be a list of JSON Pointers into managed, such as [\"/tui/notice\"]", label)
			}
			steps, err := parseUnsetPointer(pointer)
			if err != nil {
				return fmt.Errorf("%s: %q %v", label, pointer, err)
			}
			table := managed
			for depth, step := range steps[:len(steps)-1] {
				next, exists := table[step]
				switch child := next.(type) {
				case nil:
					if exists {
						return fmt.Errorf("%s: %q passes through a key unset at %q", label, pointer, "/"+strings.Join(steps[:depth+1], "/"))
					}
					table[step] = map[string]any{}
				case map[string]any:
					table[step] = cloneMap(child)
				default:
					return fmt.Errorf("%s: %q passes through managed key %q, which is not a table", label, pointer, step)
				}
				table = table[step].(map[string]any)
			}
			leaf := steps[len(steps)-1]
			if _, set := table[leaf]; set {
				return fmt.Errorf("%s: %q names a key managed also sets or unsets; say it once", label, pointer)
			}
			table[leaf] = nil
		}
		row["managed"] = managed
	}
	return nil
}

// parseUnsetPointer splits an RFC 6901 pointer into its keys, refusing the root and an empty
// key as config-list's path does.
func parseUnsetPointer(pointer string) ([]string, error) {
	if !strings.HasPrefix(pointer, "/") || pointer == "/" {
		return nil, fmt.Errorf("is not a JSON Pointer to a key (write \"/<key>/<key>\")")
	}
	parts := strings.Split(pointer[1:], "/")
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("has an empty key")
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func defaultSurfaceCodecs(value any, doc *configdoc.Document, basePath []configdoc.Step, key string) error {
	rows, single := value.([]any)
	if !single {
		if row, ok := value.(map[string]any); ok {
			rows = []any{row}
		} else {
			return nil
		}
	}
	for i, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, explicit := row["codec"]; explicit {
			continue
		}
		path, ok := row["path"].(string)
		if !ok || path == "" {
			continue
		}
		codec, ok := inferredSurfaceCodec(path)
		rowPath := append(append([]configdoc.Step(nil), basePath...), configdoc.Step{Index: i, IsIndex: true})
		if !single {
			rowPath = basePath
		}
		loc := locationFor(doc, rowPath, fmt.Sprintf("%s[%d]", key, i))
		if !ok {
			return fmt.Errorf("%s", formatManifestLocation(loc, fmt.Sprintf("cannot infer config codec from path %q; set codec explicitly (supported surface formats include json/jsonc, toml, yaml/yml)", path)))
		}
		row["codec"] = codec
	}
	return nil
}

func inferredSurfaceCodec(path string) (string, bool) {
	switch strings.ToLower(strings.TrimPrefix(surfacePathExtension(path), ".")) {
	case "json", "jsonc":
		return "json", true
	case "toml":
		return "toml", true
	case "yaml", "yml":
		return "yaml", true
	default:
		return "", false
	}
}

func surfacePathExtension(path string) string {
	base := path
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		return base[i:]
	}
	return ""
}

func normalizeAutonomySurfaceCodecs(fields map[string]any, doc *configdoc.Document, group string, index int) (map[Posture][]ManifestLocation, error) {
	locations := make(map[Posture][]ManifestLocation)
	for _, posture := range []Posture{PostureAutonomous, PostureGuarded} {
		decl, ok := fields[string(posture)].(map[string]any)
		if !ok {
			continue
		}
		config, exists := decl["config"]
		if !exists {
			continue
		}
		base := []configdoc.Step{{Key: group}, {Index: index, IsIndex: true}, {Key: string(posture)}, {Key: "config"}}
		key := fmt.Sprintf("%s[%d].%s.config", group, index, posture)
		if err := lowerSurfaceUnset(config, key); err != nil {
			return nil, fmt.Errorf("%s", formatManifestLocation(locationFor(doc, base, key), err.Error()))
		}
		if err := defaultSurfaceCodecs(config, doc, base, key); err != nil {
			return nil, err
		}
		rows, ok := config.([]any)
		if !ok {
			continue
		}
		for row := range rows {
			path := append(append([]configdoc.Step(nil), base...), configdoc.Step{Index: row, IsIndex: true})
			locations[posture] = append(locations[posture], locationFor(doc, path, fmt.Sprintf("%s[%d]", key, row)))
		}
	}
	return locations, nil
}

func normalizeBuiltInProviderPlanOff(fields map[string]any) error {
	declaration, ok := fields["built_in_providers"].(map[string]any)
	if !ok {
		return nil
	}
	plans, ok := declaration["plans"].(map[string]any)
	if !ok {
		return nil
	}
	for _, name := range sortedKeys(plans) {
		value, ok := plans[name].(string)
		if !ok {
			continue
		}
		if value != "off" {
			return fmt.Errorf("built_in_providers.plans.%s must be a table or the reserved string %q", name, "off")
		}
		plans[name] = nil
	}
	return nil
}

// normalizeProgramDefaults lowers grouped TOML's concise host package hints and pack-tree
// executable default into the legacy Contribution shape; it never supplies a delivery via.
//
// `install` is a package name, `false`, or a table of managers. A manager the table does not
// name takes the table's `default`, and the default is the bin unless written. `false` — as the
// whole value, as `default`, or for one manager — is "no hint": the manager gets no
// install_hints entry, exactly as a legacy install_hints that does not name it. A program with
// no hint at all is `install = false`; one with hints for some managers only is
// `install = { default = false, brew = "…" }`. `false` and not a reserved string, because a
// package name is a string (readable-config.md RC-D23).
func normalizeProgramDefaults(fields map[string]any) error {
	bin, _ := fields["bin"].(string)
	install, hasInstall := fields["install"]
	delete(fields, "install")
	if _, isFork := fields["fork_of"]; isFork {
		if hasInstall {
			return fmt.Errorf("install is not available on a source fork")
		}
		return nil
	}

	// A manager mapped to "" gets no hint (installHintPackage reads false as "").
	packages := make(map[string]string, len(installManagers)+1)
	defaultPackage := bin
	if hasInstall {
		switch value := install.(type) {
		case string:
			if value == "" {
				return fmt.Errorf("install must name a package; write install = false for no hint on any manager")
			}
			defaultPackage = value
		case bool:
			if value {
				return fmt.Errorf("install must be a package name, false (no hint for any manager), or a table of managers")
			}
			defaultPackage = ""
		case map[string]any:
			for _, manager := range sortedKeys(value) {
				packageName, ok := installHintPackage(value[manager])
				if !ok {
					return fmt.Errorf("install.%s must be a package name, or false for no hint on that manager", manager)
				}
				if manager == "default" {
					defaultPackage = packageName
					continue
				}
				if !installManagerNames[manager] {
					return fmt.Errorf("install has unknown manager %q (use default, brew, apt, dnf, pacman, nix, or brew-cask)", manager)
				}
				packages[manager] = packageName
			}
		default:
			return fmt.Errorf("install must be a package name, false (no hint for any manager), or a table of managers")
		}
	}
	for _, manager := range installManagers {
		if _, overridden := packages[manager]; !overridden {
			packages[manager] = defaultPackage
		}
	}
	for manager, packageName := range packages {
		if packageName == "" {
			delete(packages, manager)
		}
	}
	if len(packages) > 0 {
		fields["install_hints"] = packages
	}
	if fields["via"] == "pack" {
		if _, authored := fields["run"]; !authored {
			fields["run"] = bin
		}
	}
	return nil
}

// lowerOptionsWithoutDefault reads a provider's grouped `options` table: a string is the
// option's default, and `false` declares the option with NO default — legacy JSON's null
// (OptionDefault), which TOML cannot write. It is `false` and not a reserved string because
// "" is a real default (readable-config.md RC-D23), and not an omitted key because dropping
// the key un-declares the option, so a profile that sets it is refused. fields["options"] is
// replaced with a lowered copy holding a nil for each false; the caller's table is untouched.
func lowerOptionsWithoutDefault(fields map[string]any) error {
	raw, ok := fields["options"]
	if !ok {
		return nil
	}
	options, ok := raw.(map[string]any)
	if !ok {
		return nil // the strict decode words the shape error
	}
	lowered := make(map[string]any, len(options))
	for _, key := range sortedKeys(options) {
		switch v := options[key].(type) {
		case bool:
			if v {
				return fmt.Errorf("options.%s must be a string default, or false to declare the option with no default", key)
			}
			lowered[key] = nil
		default:
			lowered[key] = v
		}
	}
	fields["options"] = lowered
	return nil
}

// installHintPackage reads one grouped install value: a non-empty package name, or false for
// "no hint" (returned as ""). An empty string and true are refused, so "" never becomes a hint.
func installHintPackage(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, v != ""
	case bool:
		return "", !v
	}
	return "", false
}

var installManagers = []string{"brew", "apt", "dnf", "pacman", "nix"}

var installManagerNames = map[string]bool{
	"brew": true, "apt": true, "dnf": true, "pacman": true, "nix": true, "brew-cask": true,
}

func isGroupedKind(group string, value any) bool {
	if groupKind(group) != "" {
		return true
	}
	switch group {
	case "name", "description", "skills_tier", "supersedes", "needs", "formerly", "model_list_check", "provider_keys", "provider_env", "launch_computed", "session_homes":
		return false
	}
	_, isArray := value.([]any)
	return isArray
}

func groupKind(group string) Kind {
	if group == "overlay" {
		return KindConfigOverlay
	}
	candidate := Kind(strings.ReplaceAll(group, "_", "-"))
	if KnownKind(candidate) {
		return candidate
	}
	return ""
}

func groupedRename(kind Kind, fields map[string]any) (*fieldRename, string) {
	scope := ""
	if kind == KindConfigOverlay {
		scope = "overlay"
	} else if kind == KindProgram {
		scope = "program"
	}
	for _, key := range sortedKeys(fields) {
		if rename, ok := renamedFieldInScope(scope, key); ok {
			return &rename, key
		}
	}
	return nil, ""
}

func providerRenamedField(fields map[string]any) *scopedRename {
	if raw, ok := fields["every_model"]; ok {
		if path := modelRename(raw, "every_model"); path != nil {
			return path
		}
	}
	if raw, ok := fields["models"].(map[string]any); ok {
		for _, id := range sortedKeys(raw) {
			if path := modelRename(raw[id], "models."+id); path != nil {
				return path
			}
		}
	}
	return nil
}

func modelRename(raw any, prefix string) *scopedRename {
	model, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range sortedKeys(model) {
		if rename, ok := renamedFieldInScope("provider-model", key); ok {
			return &scopedRename{Path: prefix + "." + key, Rename: &rename}
		}
	}
	return nil
}

func orderedTableKeys(doc *configdoc.Document, path []configdoc.Step, table map[string]any) []string {
	if keys, ok := orderedValueKeys(doc.Values, path, table); ok {
		return keys
	}
	keys := sortedKeys(table)
	positions := make(map[string]int, len(keys))
	for _, key := range keys {
		memberPath := append(append([]configdoc.Step(nil), path...), configdoc.Step{Key: key})
		span, ok, err := doc.Index.Table(memberPath...)
		if err != nil || !ok {
			span, ok, err = doc.Index.Member(memberPath...)
		}
		if err != nil || !ok {
			span, ok, err = doc.Index.Value(memberPath...)
		}
		if err != nil || !ok {
			positions[key] = len(doc.Bytes) + len(keys)
			continue
		}
		positions[key] = span.Start
	}
	sort.SliceStable(keys, func(i, j int) bool {
		return positions[keys[i]] < positions[keys[j]]
	})
	return keys
}

func orderedValueKeys(root *jsonx.OrderedMap, path []configdoc.Step, table map[string]any) ([]string, bool) {
	var value any = root
	for _, step := range path {
		if step.IsIndex {
			return nil, false
		}
		current, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return nil, false
		}
		value, ok = current.Get(step.Key)
		if !ok {
			return nil, false
		}
	}
	ordered, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	keys := ordered.Keys()
	if len(keys) != len(table) {
		return nil, false
	}
	for _, key := range keys {
		if _, ok := table[key]; !ok {
			return nil, false
		}
	}
	return append([]string(nil), keys...), true
}

func providerShapeUnknownField(fields map[string]any) string {
	if raw, ok := fields["every_model"]; ok {
		if path := unknownModelField(raw, "every_model"); path != "" {
			return path
		}
	}
	if raw, ok := fields["models"]; ok {
		if rows, ok := raw.(map[string]any); ok {
			for _, id := range sortedKeys(rows) {
				if path := unknownModelField(rows[id], "models."+id); path != "" {
					return path
				}
			}
		}
	}
	if raw, ok := fields["variants"]; ok {
		if variants, ok := raw.(map[string]any); ok {
			for _, name := range sortedKeys(variants) {
				if variant, ok := variants[name].(map[string]any); ok {
					if key := firstUnknownKey(variant, "model", "via", "options", "subagents", "enforce_models", "capabilities", "protocols"); key != "" {
						return "variants." + name + "." + key
					}
				}
			}
		}
	}
	return ""
}

func providerUnknownLocation(doc *configdoc.Document, provider string, fields map[string]any, unknown string) ManifestLocation {
	path := []configdoc.Step{{Key: "providers"}, {Key: provider}}
	key := "providers." + provider + "." + unknown
	if strings.HasPrefix(unknown, "every_model.") {
		path = append(path, configdoc.Step{Key: "every_model"})
	} else if strings.HasPrefix(unknown, "models.") {
		if rows, ok := fields["models"].(map[string]any); ok {
			for _, id := range sortedKeys(rows) {
				if strings.HasPrefix(unknown, "models."+id+".") {
					path = append(path, configdoc.Step{Key: "models"}, configdoc.Step{Key: id})
					break
				}
			}
		}
	} else if strings.HasPrefix(unknown, "variants.") {
		if variants, ok := fields["variants"].(map[string]any); ok {
			for _, name := range sortedKeys(variants) {
				if strings.HasPrefix(unknown, "variants."+name+".") {
					path = append(path, configdoc.Step{Key: "variants"}, configdoc.Step{Key: name})
					break
				}
			}
		}
	}
	return locationFor(doc, path, key)
}

func unknownModelField(raw any, path string) string {
	model, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if key := firstUnknownKey(model, "model", "name", "description", "vendor", "api", "thinking", "images", "price", "context", "max_tokens", "reasoning", "accepts", "base", "wire_id", "extends", "openrouter_routing", "twin_of", "compact_at"); key != "" {
		return path + "." + key
	}
	if images, ok := model["images"].(map[string]any); ok {
		if key := firstUnknownKey(images, "max_width", "max_height", "max_bytes", "jpeg_quality"); key != "" {
			return path + ".images." + key
		}
	}
	if price, ok := model["price"].(map[string]any); ok {
		if key := firstUnknownKey(price, "input", "output", "cache_read", "cache_write"); key != "" {
			return path + ".price." + key
		}
	}
	return ""
}

func firstUnknownKey(fields map[string]any, allowed ...string) string {
	known := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		known[key] = true
	}
	for _, key := range sortedKeys(fields) {
		if !known[key] {
			return key
		}
	}
	return ""
}

// ProviderTable is the typed canonical projection of config's providers table. Disabled names
// retain table tombstones so a caller can remove a lower pack declaration without treating the
// tombstone as a selectable provider.
type ProviderTable struct {
	Declarations     []ProviderContribution
	Disabled         []string
	DisabledVariants map[string][]string
}

// DecodeProviderTable decodes canonical authored provider tables through the same pure provider
// field decoder used by grouped pack TOML. root is the complete ordered config map so provider,
// model, and variant declaration order survives lowering.
func DecodeProviderTable(root *jsonx.OrderedMap) (ProviderTable, error) {
	var result ProviderTable
	if root == nil {
		return result, nil
	}
	raw, present := root.Get("providers")
	if !present {
		return result, nil
	}
	_, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return result, fmt.Errorf("providers must be a table keyed by provider name")
	}
	orderedRoot, _ := jsonx.DeepCopy(root).(*jsonx.OrderedMap)
	orderedProvidersRaw, _ := orderedRoot.Get("providers")
	orderedProviders := orderedProvidersRaw.(*jsonx.OrderedMap)
	providers := orderedProviders
	doc := &configdoc.Document{Values: orderedRoot}
	for _, name := range providers.Keys() {
		value, _ := orderedProviders.Get(name)
		if off, isString := value.(string); isString {
			if off != "off" {
				return ProviderTable{}, fmt.Errorf("providers.%s: expected a provider table or \"off\"", name)
			}
			result.Disabled = append(result.Disabled, name)
			orderedProviders.Delete(name)
			continue
		}
		_, ok := orderedProviderMap(value)
		if !ok {
			return ProviderTable{}, fmt.Errorf("providers.%s: provider declaration must be a table", name)
		}
		var legacyModels map[string]string
		providerValue, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return ProviderTable{}, fmt.Errorf("providers.%s: provider declaration must be a table", name)
		}
		if rawModels, present := providerValue.Get("models"); present {
			modelMap, ok := rawModels.(*jsonx.OrderedMap)
			if !ok {
				return ProviderTable{}, fmt.Errorf("providers.%s.models must be a table keyed by model id", name)
			}
			for _, id := range append([]string(nil), modelMap.Keys()...) {
				if wireID, ok := func() (string, bool) { value, _ := modelMap.Get(id); text, ok := value.(string); return text, ok }(); ok {
					if legacyModels == nil {
						legacyModels = make(map[string]string)
					}
					legacyModels[id] = wireID
					modelMap.Delete(id)
				}
			}
		}
		variantsOff := []string(nil)
		if rawVariants, present := providerValue.Get("variants"); present {
			variantMap, ok := rawVariants.(*jsonx.OrderedMap)
			if !ok {
				return ProviderTable{}, fmt.Errorf("providers.%s.variants must be a table keyed by variant name", name)
			}
			for _, variant := range append([]string(nil), variantMap.Keys()...) {
				variantValue, _ := variantMap.Get(variant)
				if off, isString := variantValue.(string); isString {
					if off != "off" {
						return ProviderTable{}, fmt.Errorf("providers.%s.variants.%s: expected a variant table or \"off\"", name, variant)
					}
					variantsOff = append(variantsOff, variant)
					variantMap.Delete(variant)
				}
			}
			if len(variantsOff) != 0 {
				result.DisabledVariants = ensureProviderDisabledVariantMap(result.DisabledVariants)
				result.DisabledVariants[name] = variantsOff
			}
		}
		fields, _ := orderedProviderMap(value)
		if unknown := providerShapeUnknownField(fields); unknown != "" {
			return ProviderTable{}, fmt.Errorf("unknown provider model field %q", "providers."+name+"."+unknown)
		}
		if renamed := providerRenamedField(fields); renamed != nil {
			return ProviderTable{}, fmt.Errorf("providers.%s: %s", name, renameMessage(*renamed.Rename))
		}
		normalized, shape, err := normalizeProvider(name, fields, doc)
		if err != nil {
			return ProviderTable{}, fmt.Errorf("providers.%s: %w", name, err)
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return ProviderTable{}, fmt.Errorf("providers.%s: %w", name, err)
		}
		var contribution Contribution
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&contribution); err != nil {
			return ProviderTable{}, fmt.Errorf("providers.%s: %w", name, err)
		}
		contribution.ProviderAlias = shape.alias
		contribution.ProviderModel = shape.model
		contribution.ProviderVariants = shape.variants
		contribution.ProviderVariantOrder = append([]string(nil), shape.variantOrder...)
		contribution.EveryProviderModel = shape.everyModel
		contribution.ProviderModels = shape.models
		contribution.ProviderModelOrder = append([]string(nil), shape.modelOrder...)
		if legacyModels != nil {
			contribution.Models = legacyModels
		}
		projected := (&Manifest{Contributes: []Contribution{contribution}}).Providers()
		if len(projected) != 1 {
			return ProviderTable{}, fmt.Errorf("providers.%s: decoder produced no provider declaration", name)
		}
		result.Declarations = append(result.Declarations, projected[0])
	}
	return result, nil
}

func ensureProviderDisabledVariantMap(value map[string][]string) map[string][]string {
	if value == nil {
		return make(map[string][]string)
	}
	return value
}

func orderedProviderMap(value any) (map[string]any, bool) {
	ordered, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	out := make(map[string]any, ordered.Len())
	for _, key := range ordered.Keys() {
		item, _ := ordered.Get(key)
		if nested, isTable := item.(*jsonx.OrderedMap); isTable {
			converted, ok := orderedProviderMap(nested)
			if !ok {
				return nil, false
			}
			out[key] = converted
		} else if entries, isArray := item.([]any); isArray {
			copy := make([]any, len(entries))
			for i := range entries {
				if nested, isTable := entries[i].(*jsonx.OrderedMap); isTable {
					converted, ok := orderedProviderMap(nested)
					if !ok {
						return nil, false
					}
					copy[i] = converted
				} else {
					copy[i] = orderedProviderScalar(entries[i])
				}
			}
			out[key] = copy
		} else {
			out[key] = orderedProviderScalar(item)
		}
	}
	return out, true
}

func orderedProviderScalar(value any) any {
	if jsonx.IsInt(value) {
		if number, ok := jsonx.AsInt(value); ok {
			return number
		}
	}
	return value
}

func normalizeProvider(name string, fields map[string]any, doc *configdoc.Document) (map[string]any, *providerShape, error) {
	providerName := name
	shape := &providerShape{}
	if via, ok := fields["via"].(string); ok && !ValidPackName(via) {
		return nil, nil, fmt.Errorf("providers.%s.via must be a valid pack name", name)
	}
	if err := lowerOptionsWithoutDefault(fields); err != nil {
		return nil, nil, err
	}
	if raw, ok := fields["alias"]; ok {
		value, ok := raw.(string)
		if !ok || value == "" || strings.ContainsAny(value, " \t\r\n") {
			return nil, nil, fmt.Errorf("alias must be a non-empty name without whitespace")
		}
		shape.alias = value
		delete(fields, "alias")
	}
	if raw, ok := fields["model"]; ok {
		value, ok := raw.(string)
		if !ok || value == "" || strings.ContainsAny(value, " \t\r\n") {
			return nil, nil, fmt.Errorf("model must be a non-empty provider model name")
		}
		shape.model = value
		delete(fields, "model")
	}
	if raw, ok := fields["every_model"]; ok {
		model, err := decodeProviderModel(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("every_model: %w", err)
		}
		if err := validateProviderModel("every_model", raw, model); err != nil {
			return nil, nil, err
		}
		shape.everyModel = &model
		delete(fields, "every_model")
	}
	if raw, ok := fields["models"]; ok {
		rows, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("models must be a table keyed by model id")
		}
		shape.models = make(map[string]ProviderModel, len(rows))
		for _, id := range orderedTableKeys(doc, []configdoc.Step{{Key: "providers"}, {Key: name}, {Key: "models"}}, rows) {
			value := rows[id]
			if id == "" {
				return nil, nil, fmt.Errorf("models contains an empty model id")
			}
			model, err := decodeProviderModel(value)
			if err != nil {
				return nil, nil, fmt.Errorf("models.%s: %w", id, err)
			}
			if err := validateProviderModel("models."+id, value, model); err != nil {
				return nil, nil, err
			}
			shape.models[id] = model
			shape.modelOrder = append(shape.modelOrder, id)
		}
		delete(fields, "models")
	}
	if raw, ok := fields["variants"]; ok {
		variants, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("variants must be a table keyed by variant name")
		}
		shape.variants = make(map[string]ProviderVariant, len(variants))
		for _, name := range orderedTableKeys(doc, []configdoc.Step{{Key: "providers"}, {Key: providerName}, {Key: "variants"}}, variants) {
			value := variants[name]
			if name == "" {
				return nil, nil, fmt.Errorf("variants contains an empty name")
			}
			if table, ok := value.(map[string]any); ok {
				table = cloneMap(table)
				if err := lowerOptionsWithoutDefault(table); err != nil {
					return nil, nil, fmt.Errorf("variants.%s.%w", name, err)
				}
				value = table
			}
			var variant ProviderVariant
			if err := decodeStrictValue(value, &variant); err != nil {
				return nil, nil, fmt.Errorf("variants.%s: %w", name, err)
			}
			if variant.Model == "" && variant.Via == "" {
				return nil, nil, fmt.Errorf("variants.%s must set model or via", name)
			}
			if variant.Model != "" && strings.ContainsAny(variant.Model, " \t\r\n") {
				return nil, nil, fmt.Errorf("variants.%s.model must be a name", name)
			}
			if variant.Via == "" && hasKey(value, "via") {
				return nil, nil, fmt.Errorf("variants.%s.via must be non-empty", name)
			}
			if variant.Via != "" && !ValidPackName(variant.Via) {
				return nil, nil, fmt.Errorf("variants.%s.via must be a valid pack name", name)
			}
			if rawSubagents, present := mapValue(value, "subagents"); present {
				if err := validateProviderSubagents(name, rawSubagents); err != nil {
					return nil, nil, err
				}
			}
			if err := uniqueNonempty("variants."+name+".capabilities", variant.Capabilities); err != nil {
				return nil, nil, err
			}
			if err := uniqueNonempty("variants."+name+".protocols", variant.Protocols); err != nil {
				return nil, nil, err
			}
			shape.variants[name] = variant
			shape.variantOrder = append(shape.variantOrder, name)
		}
		delete(fields, "variants")
	}
	fields["kind"] = string(KindProvider)
	fields["name"] = name
	return fields, shape, nil
}

func decodeProviderModel(raw any) (ProviderModel, error) {
	var model ProviderModel
	err := decodeStrictValue(raw, &model)
	return model, err
}

// ModelFactsRouteOnlyKeys are the row keys a top-level `model_facts` entry refuses: what only one
// route of a model can say, never a fact every route of it shares
// (docs/design/model-facts-migration.md MF-D2).
var ModelFactsRouteOnlyKeys = []string{"model", "wire_id", "twin_of", "compact_at", "extends"}

// readableModelFactKeys are the keys only the readable spelling of a `model_facts` entry writes
// (MF-D2); the legacy spelling writes `input`, `cost` and `context_window` instead, and the keys
// the two share mean the same in both.
var readableModelFactKeys = map[string]bool{"accepts": true, "price": true, "context": true, "api": true,
	"thinking": true, "images": true, "vendor": true}

// IsReadableModelFacts is whether a `model_facts` entry is in the readable spelling: it writes a
// key only that spelling has. One that writes none reads the same either way.
func IsReadableModelFacts(entry *jsonx.OrderedMap) bool {
	for _, key := range entry.Keys() {
		if readableModelFactKeys[key] {
			return true
		}
	}
	return false
}

// DecodeModelFacts decodes one readable `model_facts.<name>` entry (MF-D2) through the decoder a
// row and `every_model` take, so one spelling cannot be accepted by one and refused by another.
// It refuses the keys only a route can say (ModelFactsRouteOnlyKeys).
func DecodeModelFacts(name string, entry *jsonx.OrderedMap) (ProviderModel, error) {
	label := "model_facts." + name
	for _, key := range ModelFactsRouteOnlyKeys {
		if _, ok := entry.Get(key); ok {
			return ProviderModel{}, fmt.Errorf("%s.%s: only a route says %s; a model_facts entry says only what every route of the model shares, so write it on the row", label, key, key)
		}
	}
	raw, ok := orderedProviderMap(entry)
	if !ok {
		return ProviderModel{}, fmt.Errorf("%s: expected a table", label)
	}
	model, err := decodeProviderModel(raw)
	if err != nil {
		return ProviderModel{}, fmt.Errorf("%s: %w", label, err)
	}
	if err := validateProviderModel(label, raw, model); err != nil {
		return ProviderModel{}, err
	}
	return model, nil
}

func decodeStrictValue(raw any, dst any) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func validateProviderModel(label string, raw any, model ProviderModel) error {
	facts, _ := raw.(map[string]any)
	if value, ok := facts["model"]; ok {
		if label == "every_model" {
			return fmt.Errorf("every_model.model: only a row says which model it is; write `model` on a row under models")
		}
		if text, valid := value.(string); !valid || text == "" || strings.ContainsAny(text, " \t\r\n") {
			return fmt.Errorf("%s.model must be a non-empty model id without whitespace", label)
		}
	}
	if value, ok := facts["api"]; ok {
		api, valid := value.(string)
		if !valid || api == "" {
			return fmt.Errorf("%s.api must be a non-empty string", label)
		}
	}
	if value, ok := facts["context"]; ok && !positiveInteger(value) {
		return fmt.Errorf("%s.context must be a positive integer", label)
	}
	if value, ok := facts["max_tokens"]; ok && !positiveInteger(value) {
		return fmt.Errorf("%s.max_tokens must be a positive integer", label)
	}
	if value, ok := facts["compact_at"]; ok && !positiveInteger(value) {
		return fmt.Errorf("%s.compact_at must be a positive integer", label)
	}
	if model.TwinOf != "" && label == "models."+model.TwinOf {
		return fmt.Errorf("%s.twin_of cannot name itself", label)
	}
	if value, ok := facts["vendor"]; ok {
		vendor, valid := value.(string)
		if !valid || !ValidModelVendor(vendor) {
			return fmt.Errorf("%s.vendor must be a non-empty lowercase vendor token", label)
		}
	}
	if value, ok := facts["base"]; ok {
		if text, valid := value.(string); !valid || text == "" {
			return fmt.Errorf("%s.base must be a non-empty model id string", label)
		}
	}
	if value, ok := facts["wire_id"]; ok {
		if text, valid := value.(string); !valid || text == "" {
			return fmt.Errorf("%s.wire_id must be a non-empty model id string", label)
		}
	}
	if value, ok := facts["extends"]; ok {
		if text, valid := value.(string); !valid || text == "" {
			return fmt.Errorf("%s.extends must be a non-empty shared model name", label)
		}
	}
	if value, ok := facts["openrouter_routing"]; ok && value != nil {
		if _, valid := value.(map[string]any); !valid {
			return fmt.Errorf("%s.openrouter_routing must be a table", label)
		}
	}
	if err := uniqueNonempty(label+".thinking", model.Thinking); err != nil {
		return err
	}
	if err := uniqueNonempty(label+".accepts", model.Accepts); err != nil {
		return err
	}
	for _, value := range model.Accepts {
		if value != "text" && value != "image" {
			return fmt.Errorf("%s.accepts has unsupported value %q (want text or image)", label, value)
		}
	}
	if model.Images != nil {
		images, _ := facts["images"].(map[string]any)
		for _, key := range []string{"max_width", "max_height", "max_bytes"} {
			if value, ok := images[key]; !ok || !positiveInteger(value) {
				return fmt.Errorf("%s.images.%s must be a positive integer", label, key)
			}
		}
		if value, ok := images["jpeg_quality"]; !ok || !positiveInteger(value) || model.Images.JPEGQuality > 100 {
			return fmt.Errorf("%s.images.jpeg_quality must be between 1 and 100", label)
		}
	}
	if model.Price != nil {
		for name, value := range map[string]*float64{
			"input": model.Price.Input, "output": model.Price.Output,
			"cache_read": model.Price.CacheRead, "cache_write": model.Price.CacheWrite,
		} {
			if value != nil && *value < 0 {
				return fmt.Errorf("%s.price.%s must be non-negative", label, name)
			}
		}
	}
	return nil
}

func hasKey(value any, key string) bool {
	_, exists := mapValue(value, key)
	return exists
}

func mapValue(value any, key string) (any, bool) {
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	entry, exists := fields[key]
	return entry, exists
}

func validateProviderSubagents(variant string, raw any) error {
	path := "variants." + variant + ".subagents"
	fields, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be a table declaring default and/or roles", path)
	}
	declares := false
	for _, key := range sortedKeys(fields) {
		value := fields[key]
		switch key {
		case "default":
			model, valid := value.(string)
			if !valid || model == "" {
				return fmt.Errorf("%s.default must be a non-empty string", path)
			}
			declares = true
		case "roles":
			roles, valid := value.(map[string]any)
			if !valid {
				return fmt.Errorf("%s.roles must be a table mapping role names to models", path)
			}
			for _, role := range sortedKeys(roles) {
				model, valid := roles[role].(string)
				if role == "" {
					return fmt.Errorf("%s.roles contains an empty role name", path)
				}
				if !valid || model == "" {
					return fmt.Errorf("%s.roles.%s must be a non-empty string", path, role)
				}
			}
			if len(roles) > 0 {
				declares = true
			}
		default:
			return fmt.Errorf("%s.%s is unknown; only default and roles are accepted", path, key)
		}
	}
	if !declares {
		return fmt.Errorf("%s declares nothing; set default or a roles entry", path)
	}
	return nil
}

func positiveInteger(value any) bool {
	number, ok := value.(int64)
	if ok {
		return number > 0
	}
	float, ok := value.(float64)
	return ok && float >= 1 && float == float64(int64(float))
}

func (m *Manifest) setLegacyOrigin() {
	m.Origin = ManifestOrigin{Format: ManifestFormatJSON, File: ManifestName}
	for i := range m.Contributes {
		c := &m.Contributes[i]
		c.Origin = m.Origin
		c.Location = ManifestLocation{File: ManifestName, Key: fmt.Sprintf("contributes[%d]", i)}
		for _, half := range []struct {
			posture Posture
			decl    *AutonomyPosture
		}{{PostureAutonomous, c.Autonomous}, {PostureGuarded, c.Guarded}} {
			if half.decl == nil {
				continue
			}
			for j := range half.decl.Launch {
				row := &half.decl.Launch[j]
				row.Origin = m.Origin
				row.Location = ManifestLocation{File: ManifestName, Key: fmt.Sprintf("contributes[%d].%s.launch[%d]", i, half.posture, j)}
			}
		}
	}
}

func uniqueNonempty(label string, values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			return fmt.Errorf("%s must contain unique non-empty strings", label)
		}
		seen[value] = true
	}
	return nil
}

func takeReplace(fields map[string]any) (bool, error) {
	raw, ok := fields["replace"]
	if !ok {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("must be a boolean")
	}
	delete(fields, "replace")
	return value, nil
}

func locationForProvider(doc *configdoc.Document, name string) ManifestLocation {
	path := []configdoc.Step{{Key: "providers"}, {Key: name}}
	location := locationFor(doc, path, "providers."+name)
	if location.Line > 0 {
		return location
	}
	start, found := firstIndexedDescendant(doc, path, valueAtPath(doc.Values, path))
	if !found {
		return location
	}
	line, column := doc.Index.Position(start)
	location.Line, location.Column = line, column
	return location
}

func firstIndexedDescendant(doc *configdoc.Document, path []configdoc.Step, value any) (int, bool) {
	var first int
	found := false
	consider := func(span configdoc.Span, ok bool, err error) {
		if err == nil && ok && (!found || span.Start < first) {
			first, found = span.Start, true
		}
	}
	span, ok, err := doc.Index.Item(path...)
	consider(span, ok, err)
	span, ok, err = doc.Index.Table(path...)
	consider(span, ok, err)
	span, ok, err = doc.Index.Member(path...)
	consider(span, ok, err)
	span, ok, err = doc.Index.Value(path...)
	consider(span, ok, err)
	if table, ok := value.(*jsonx.OrderedMap); ok {
		for _, key := range table.Keys() {
			childPath := append(append([]configdoc.Step(nil), path...), configdoc.Step{Key: key})
			child, _ := table.Get(key)
			childStart, childFound := firstIndexedDescendant(doc, childPath, child)
			if childFound && (!found || childStart < first) {
				first, found = childStart, true
			}
		}
	}
	return first, found
}

func valueAtPath(root *jsonx.OrderedMap, path []configdoc.Step) any {
	var current any = root
	for _, step := range path {
		table, ok := current.(*jsonx.OrderedMap)
		if !ok || step.IsIndex {
			return nil
		}
		current, _ = table.Get(step.Key)
	}
	return current
}

func locationFor(doc *configdoc.Document, path []configdoc.Step, key string) ManifestLocation {
	span, ok, err := doc.Index.Item(path...)
	if err != nil || !ok {
		span, ok, err = doc.Index.Table(path...)
	}
	if err != nil || !ok {
		span, ok, _ = doc.Index.Member(path...)
	}
	if !ok {
		return ManifestLocation{File: ManifestTOMLName, Key: key}
	}
	line, column := doc.Index.Position(span.Start)
	return ManifestLocation{File: ManifestTOMLName, Key: key, Line: line, Column: column}
}

func locOffset(doc *configdoc.Document, loc ManifestLocation) int {
	if loc.Line < 1 {
		return len(doc.Bytes)
	}
	offset, line := 0, 1
	for offset < len(doc.Bytes) && line < loc.Line {
		if doc.Bytes[offset] == '\n' {
			line++
		}
		offset++
	}
	if loc.Column > 1 {
		offset += loc.Column - 1
		if offset > len(doc.Bytes) {
			return len(doc.Bytes)
		}
	}
	return offset
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func formatManifestLocation(location ManifestLocation, problem string) string {
	if location.Line > 0 {
		return fmt.Sprintf("%s:%d:%d (%s): %s", location.File, location.Line, location.Column, location.Key, problem)
	}
	return fmt.Sprintf("%s (%s): %s", location.File, location.Key, problem)
}

func locateTOMLProblems(messages []string, groups []groupedContribution, root map[string]any, doc *configdoc.Document) []string {
	out := append([]string(nil), messages...)
	for i, message := range out {
		message = strings.ReplaceAll(message, ManifestName+":", ManifestTOMLName+":")
		if location, ok := launchDiagnosticLocation(message, groups); ok {
			if separator := strings.Index(message, ": "); separator >= 0 {
				message = formatManifestLocation(location, message[separator+2:])
			}
		} else if index := parseIndex(message); index >= 0 && index < len(groups) {
			group := groups[index]
			if group.location.Line > 0 {
				message = fmt.Sprintf("%s:%d:%d (%s): %s", ManifestTOMLName, group.location.Line, group.location.Column, group.location.Key, strings.TrimSpace(strings.TrimPrefix(message, "contributes["+strconv.Itoa(index)+"]:")))
			}
		}
		if strings.HasPrefix(message, ManifestTOMLName+":") && strings.Contains(message, "unknown field ") {
			field := fieldNamedByUnknownField(message)
			detail := strings.TrimSpace(strings.TrimPrefix(message, ManifestTOMLName+":"))
			if separator := strings.Index(detail, "): "); separator >= 0 {
				detail = detail[separator+3:]
			}
			if location, ok := launchLocationForUnknownField(field, groups); ok {
				message = formatManifestLocation(location, detail)
			} else if group, ok := groupForUnknownField(message, groups); ok {
				message = formatManifestLocation(group.location, detail)
			} else {
				for key := range root {
					if strings.Contains(message, fmt.Sprintf("unknown field %q", key)) {
						loc := locationFor(doc, []configdoc.Step{{Key: key}}, key)
						message = formatManifestLocation(loc, detail)
						break
					}
				}
			}
		}
		if !strings.HasPrefix(message, ManifestTOMLName+":") && !strings.HasPrefix(message, ManifestTOMLName+" ") {
			message = ManifestTOMLName + ": " + message
		}
		out[i] = message
	}
	return out
}

func launchDiagnosticLocation(message string, groups []groupedContribution) (ManifestLocation, bool) {
	contribution := parseIndex(message)
	if contribution < 0 || contribution >= len(groups) {
		return ManifestLocation{}, false
	}
	for _, posture := range []Posture{PostureAutonomous, PostureGuarded} {
		marker := "." + string(posture) + ".launch["
		start := strings.Index(message, marker)
		if start < 0 {
			continue
		}
		start += len(marker)
		end := strings.IndexByte(message[start:], ']')
		if end < 0 {
			continue
		}
		rowIndex, err := strconv.Atoi(message[start : start+end])
		if err != nil {
			continue
		}
		rows := groups[contribution].launchMetadata[posture]
		if rowIndex >= 0 && rowIndex < len(rows) && rows[rowIndex].Location.Line > 0 {
			return rows[rowIndex].Location, true
		}
	}
	return ManifestLocation{}, false
}

func launchLocationForUnknownField(field string, groups []groupedContribution) (ManifestLocation, bool) {
	if field == "" {
		return ManifestLocation{}, false
	}
	for _, group := range groups {
		if group.kind != KindAutonomy {
			continue
		}
		for _, posture := range []Posture{PostureAutonomous, PostureGuarded} {
			decl, ok := group.value[string(posture)].(map[string]any)
			if !ok {
				continue
			}
			launch, ok := decl["launch"].([]any)
			if !ok {
				continue
			}
			metadata := group.launchMetadata[posture]
			for i, raw := range launch {
				row, ok := raw.(map[string]any)
				if !ok || i >= len(metadata) {
					continue
				}
				if _, exists := row[field]; exists && metadata[i].Location.Line > 0 {
					return metadata[i].Location, true
				}
			}
		}
	}
	return ManifestLocation{}, false
}

func fieldNamedByUnknownField(message string) string {
	const marker = `unknown field "`
	start := strings.Index(message, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.IndexByte(message[start:], '"')
	if end < 0 {
		return ""
	}
	return message[start : start+end]
}

func groupForUnknownField(message string, groups []groupedContribution) (groupedContribution, bool) {
	field := fieldNamedByUnknownField(message)
	if field == "" {
		return groupedContribution{}, false
	}
	for _, group := range groups {
		if _, ok := group.value[field]; ok {
			return group, true
		}
	}
	return groupedContribution{}, false
}

func parseIndex(message string) int {
	start := strings.Index(message, "contributes[")
	if start < 0 {
		return -1
	}
	start += len("contributes[")
	end := strings.IndexByte(message[start:], ']')
	if end < 0 {
		return -1
	}
	i, _ := strconv.Atoi(message[start : start+end])
	return i
}
