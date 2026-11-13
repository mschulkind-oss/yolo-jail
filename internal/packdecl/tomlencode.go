package packdecl

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The grouped-TOML ENCODER: the inverse of DecodeTOML, so `yolo pack migrate` can rewrite a
// legacy pack.json as the grouped manifest docs/design/pack-manifests.md §2–§5 shows, and a
// test can pin DecodeTOML(EncodeTOML(m)) against m for every manifest yolo ships.
//
// The shape it writes, decided here rather than in the design (whose examples it follows):
//
//   - Pack-wide keys first (name, description, …) in the order the input carries them, since
//     TOML has no root key after the first table header.
//   - Then one `[[<kind>]]` table per contribution, IN CONTRIBUTION ORDER. The design calls the
//     shape "grouped by kind with the identity once": the kind is the table's name, said once,
//     instead of a `"kind"` key in every entry. It is NOT a re-sort by kind — declaration order
//     is the fold's later-wins order (OverlayContributions), and DecodeTOML restores it from
//     document position, so the encoder keeps it.
//   - Obvious defaults unwritten (pack-manifests.md §6 principle 5): no install hint equal to
//     the bin, `run` equal to `bin` on a pack-tree program, `scope = "workspace"`, and a
//     `config` surface's codec when its path's extension implies it.
//   - `replace = true` wherever a legacy contribution's REPLACING semantics would otherwise
//     become the grouped manifest's APPENDING default: an overlay body or posture config that
//     holds an array, and an autonomy posture that declares launch rows (packload's
//     launchFlagClaims resets a bin's earlier rows for every legacy row). Writing it only
//     there keeps the migration equal without stating what nothing changes.
//
//   - A manager the legacy install_hints does not name gets NO hint, so it is written `false`
//     (per manager, as `default = false`, or `install = false` for none): the grouped reader
//     gives an unwritten manager the bin, which would invent a recipe.
//
// TOML has no null, so the legacy nulls with a grouped spelling are respelled: a
// built_in_providers plan's null is "off", a provider option's null (declared, no default) is
// `false`, and a null inside a surface row's `managed` merge patch (a deletion) moves to the
// row's `unset` list of JSON Pointers. What TOML still cannot say is refused with its exact path
// (UnrepresentableError), never guessed: any other JSON null, an install hint for a manager the
// grouped `install` key does not take, an install hint on a source fork, and an overlay body
// that is not exactly `{"managed": …}`.

// TOMLLine is one line the encoder writes, with the legacy JSON path it came from. Source is
// nil for a line no legacy key produced (a blank separator, a closing bracket). A migration
// uses Source to put each legacy comment before the next line written from a key that follows
// it in the old file (docs/design/config-migration.md#4-what-yolo-config-migrate-does, comment migration).
type TOMLLine struct {
	Text   string
	Source []configdoc.Step
}

// EncodeOptions are the encoder's choices a caller may make.
type EncodeOptions struct {
	// KeepInstallHints writes every manager a legacy install_hints names as its own `install`
	// entry instead of folding the ones equal to the bin or a shared `default`. Shipped packs
	// keep the per-manager form (pack-manifests.md §6 principle 5), because each hint's source
	// citation sits on its own manager's line.
	KeepInstallHints bool
}

// UnrepresentableError names one legacy value grouped TOML cannot say losslessly.
type UnrepresentableError struct {
	// Path is the value's legacy JSON path, `contributes[3].options.pin_model` style.
	Path   string
	Reason string
}

func (e *UnrepresentableError) Error() string {
	return fmt.Sprintf("%s %s, which a grouped pack.toml cannot say; keep this pack on its legacy manifest, which this release still reads, until the value has a grouped spelling", e.Path, e.Reason)
}

// tomlLineWidth is the length past which an array value is written one element per line and an
// object value as one dotted key per member.
const tomlLineWidth = 100

// EncodeTOML writes m as a grouped pack.toml with the default options.
func EncodeTOML(m *Manifest) ([]byte, error) {
	return EncodeTOMLWith(m, EncodeOptions{})
}

// EncodeTOMLWith writes m as a grouped pack.toml.
func EncodeTOMLWith(m *Manifest, opts EncodeOptions) ([]byte, error) {
	lines, err := EncodeTOMLLines(m, opts)
	if err != nil {
		return nil, err
	}
	return JoinTOMLLines(lines), nil
}

// EncodeTOMLLines is EncodeTOMLWith before the lines are joined. A manifest that already came
// from grouped TOML keeps its own per-contribution replace bits and its provider tables; one
// from legacy JSON gets the legacy replacing semantics written where they matter.
func EncodeTOMLLines(m *Manifest, opts EncodeOptions) ([]TOMLLine, error) {
	if m == nil {
		return nil, fmt.Errorf("packdecl: cannot encode a nil manifest")
	}
	tree, meta, err := manifestTree(m)
	if err != nil {
		return nil, err
	}
	return encodeGroupedLines(tree, meta, opts)
}

// EncodeLegacyTOMLLines encodes a legacy pack.json document tree (configdoc.Parse's Values,
// with its authored key order) as grouped TOML lines. Each line's Source is a path into tree.
func EncodeLegacyTOMLLines(tree *jsonx.OrderedMap, opts EncodeOptions) ([]TOMLLine, error) {
	if tree == nil {
		return nil, fmt.Errorf("packdecl: cannot encode a nil manifest document")
	}
	return encodeGroupedLines(tree, nil, opts)
}

// JoinTOMLLines joins encoded lines into the file's bytes, ending in one newline.
func JoinTOMLLines(lines []TOMLLine) []byte {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line.Text)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// contributionMeta carries what a typed grouped-TOML manifest knows and its JSON form does not.
type contributionMeta struct {
	grouped         bool // the manifest came from grouped TOML: Replace is authored
	replace         bool
	groupedProvider bool // the grouped provider facts are in the tree (alias, model rows, variants)
}

// manifestTree renders m as the legacy JSON document tree the encoder reads. json.Marshal is
// the legacy shape for every field except two it cannot round-trip: OptionDefault (no
// MarshalJSON; a null option is "declared, no default") and the grouped provider facts, which
// are json:"-" because legacy JSON never carried them.
func manifestTree(m *Manifest) (*jsonx.OrderedMap, []contributionMeta, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	doc, err := configdoc.Parse(data, configdoc.JSONC)
	if err != nil {
		return nil, nil, err
	}
	tree := doc.Values
	raw, _ := tree.Get("contributes")
	items, _ := raw.([]any)
	meta := make([]contributionMeta, len(m.Contributes))
	for i := range m.Contributes {
		if i >= len(items) {
			break
		}
		c := &m.Contributes[i]
		fields, ok := items[i].(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		meta[i].grouped = c.Origin.Format == ManifestFormatTOML
		meta[i].replace = c.Replace
		if c.Options != nil {
			fields.Set("options", optionTree(c.Options))
		}
		if c.Kind == KindProvider {
			meta[i].groupedProvider = true
			if err := addGroupedProviderFields(fields, c); err != nil {
				return nil, nil, fmt.Errorf("contributes[%d]: %w", i, err)
			}
		}
	}
	return tree, meta, nil
}

func optionTree(options map[string]OptionDefault) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, key := range sortedKeys(options) {
		if options[key].Defaulted {
			out.Set(key, options[key].Value)
		} else {
			out.Set(key, nil)
		}
	}
	return out
}

func addGroupedProviderFields(fields *jsonx.OrderedMap, c *Contribution) error {
	if _, legacyModels := fields.Get("models"); legacyModels && c.ProviderModels != nil {
		return fmt.Errorf("provider %q carries both legacy flat models and grouped model rows", c.Name)
	}
	if len(c.ProviderAliases) == 1 {
		fields.Set("alias", c.ProviderAliases[0])
	} else if len(c.ProviderAliases) > 1 {
		names := make([]any, len(c.ProviderAliases))
		for i, name := range c.ProviderAliases {
			names[i] = name
		}
		fields.Set("alias", names)
	}
	if c.ProviderModel != "" {
		fields.Set("model", c.ProviderModel)
	}
	if c.EveryProviderModel != nil {
		value, err := jsonTreeValue(c.EveryProviderModel)
		if err != nil {
			return err
		}
		fields.Set("every_model", value)
	}
	if c.ProviderModels != nil {
		rows := jsonx.NewOrderedMap()
		for _, id := range orderedNames(c.ProviderModelOrder, c.ProviderModels) {
			value, err := jsonTreeValue(c.ProviderModels[id])
			if err != nil {
				return err
			}
			rows.Set(id, value)
		}
		fields.Set("models", rows)
	}
	if c.ProviderVariants != nil {
		rows := jsonx.NewOrderedMap()
		for _, name := range orderedNames(c.ProviderVariantOrder, c.ProviderVariants) {
			variant := c.ProviderVariants[name]
			value, err := jsonTreeValue(variant)
			if err != nil {
				return err
			}
			if table, ok := value.(*jsonx.OrderedMap); ok && variant.Options != nil {
				table.Set("options", optionTree(variant.Options))
			}
			rows.Set(name, value)
		}
		fields.Set("variants", rows)
	}
	return nil
}

func orderedNames[V any](order []string, rows map[string]V) []string {
	out := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, name := range order {
		if _, ok := rows[name]; ok && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	for _, name := range sortedKeys(rows) {
		if !seen[name] {
			out = append(out, name)
		}
	}
	return out
}

func jsonTreeValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	doc, err := configdoc.Parse([]byte(`{"v":`+string(data)+`}`), configdoc.JSONC)
	if err != nil {
		return nil, err
	}
	v, _ := doc.Values.Get("v")
	return v, nil
}

// encodeGroupedLines is the encoder proper. meta may be nil (or short) for a legacy document.
func encodeGroupedLines(tree *jsonx.OrderedMap, meta []contributionMeta, opts EncodeOptions) ([]TOMLLine, error) {
	var lines []TOMLLine
	for _, key := range tree.Keys() {
		if key == "contributes" {
			continue
		}
		value, _ := tree.Get(key)
		path := []configdoc.Step{{Key: key}}
		emitted, err := encodeMember(key, value, path, false)
		if err != nil {
			return nil, err
		}
		lines = append(lines, emitted...)
	}
	raw, _ := tree.Get("contributes")
	if raw == nil {
		return lines, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, &UnrepresentableError{Path: "contributes", Reason: "is not an array"}
	}
	for i, item := range items {
		fields, ok := item.(*jsonx.OrderedMap)
		if !ok {
			return nil, &UnrepresentableError{Path: fmt.Sprintf("contributes[%d]", i), Reason: "is not an object"}
		}
		var m contributionMeta
		if i < len(meta) {
			m = meta[i]
		}
		entry, err := encodeContribution(i, fields, m, opts)
		if err != nil {
			return nil, err
		}
		if len(lines) > 0 {
			lines = append(lines, TOMLLine{})
		}
		lines = append(lines, entry...)
	}
	return lines, nil
}

func encodeContribution(index int, original *jsonx.OrderedMap, meta contributionMeta, opts EncodeOptions) ([]TOMLLine, error) {
	base := []configdoc.Step{{Key: "contributes"}, {Index: index, IsIndex: true}}
	label := fmt.Sprintf("contributes[%d]", index)
	fields := shallowCopy(original)
	kindValue, _ := fields.Get("kind")
	kindText, ok := kindValue.(string)
	if !ok || kindText == "" {
		return nil, &UnrepresentableError{Path: label + ".kind", Reason: "is missing or not a string"}
	}
	kind := Kind(kindText)
	fields.Delete("kind")

	// sources maps an emitted key to the legacy key it was written from, when they differ.
	sources := map[string]string{}
	var err error
	switch kind {
	case KindProgram:
		err = encodeProgramFields(label, fields, sources, opts)
	case KindState:
		if scope, _ := fields.Get("scope"); scope == "workspace" {
			fields.Delete("scope")
		}
	case KindProvider:
		encodeOptionsWithoutDefault(fields)
		if variants, ok := getMap(fields, "variants"); ok {
			copied := shallowCopy(variants)
			for _, name := range variants.Keys() {
				if variant, ok := getMap(variants, name); ok {
					variant = shallowCopy(variant)
					encodeOptionsWithoutDefault(variant)
					copied.Set(name, variant)
				}
			}
			fields.Set("variants", copied)
		}
	case KindConfig:
		if value, ok := fields.Get("config"); ok {
			fields.Set("config", omitInferredCodecs(surfaceRowsWithUnset(value)))
		}
	case KindAutonomy:
		for _, posture := range []string{string(PostureAutonomous), string(PostureGuarded)} {
			decl, ok := getMap(fields, posture)
			if !ok {
				continue
			}
			if value, ok := decl.Get("config"); ok {
				copied := shallowCopy(decl)
				copied.Set("config", omitInferredCodecs(surfaceRowsWithUnset(value)))
				fields.Set(posture, copied)
			}
		}
	case KindConfigOverlay:
		err = encodeOverlayFields(label, fields, sources)
	}
	if err != nil {
		return nil, err
	}
	if !meta.grouped && legacyReplaceMatters(kind, original) {
		fields.Set("replace", true)
	} else if meta.grouped && meta.replace {
		fields.Set("replace", true)
	}

	var lines []TOMLLine
	// Every provider is written keyed, `[providers.<id>]`: `[[provider]] name =` is the legacy
	// spelling (RC-D69), read but never written.
	if kind == KindProvider {
		nameValue, _ := fields.Get("name")
		name, _ := nameValue.(string)
		if name == "" {
			return nil, &UnrepresentableError{Path: label + ".name", Reason: "a provider table needs its name"}
		}
		key, err := tomlKeyText(name)
		if err != nil {
			return nil, err
		}
		fields.Delete("name")
		lines = append(lines, TOMLLine{Text: "[providers." + key + "]", Source: base})
	} else {
		lines = append(lines, TOMLLine{Text: "[[" + groupName(kind) + "]]", Source: base})
	}
	for _, key := range fields.Keys() {
		value, _ := fields.Get(key)
		sourceKey := key
		if mapped, ok := sources[key]; ok {
			sourceKey = mapped
		}
		path := append(append([]configdoc.Step(nil), base...), configdoc.Step{Key: sourceKey})
		if key == "replace" {
			path = nil
		}
		emitted, err := encodeMember(key, value, path, key == "set")
		if err != nil {
			return nil, err
		}
		lines = append(lines, emitted...)
	}
	return lines, nil
}

func groupName(kind Kind) string {
	if kind == KindConfigOverlay {
		return "overlay"
	}
	return strings.ReplaceAll(string(kind), "-", "_")
}

// encodeProgramFields lowers install_hints to `install`, drops a pack-tree `run` equal to the
// bin, and spells a null built-in plan as the reserved "off".
func encodeProgramFields(label string, fields *jsonx.OrderedMap, sources map[string]string, opts EncodeOptions) error {
	binValue, _ := fields.Get("bin")
	bin, _ := binValue.(string)
	if via, _ := fields.Get("via"); via == string(ViaPack) {
		if run, _ := fields.Get("run"); run == bin && bin != "" {
			fields.Delete("run")
		}
	}
	if declaration, ok := getMap(fields, "built_in_providers"); ok {
		if plans, ok := getMap(declaration, "plans"); ok {
			copiedPlans := shallowCopy(plans)
			for _, name := range plans.Keys() {
				if value, _ := plans.Get(name); value == nil {
					copiedPlans.Set(name, "off")
				}
			}
			copiedDecl := shallowCopy(declaration)
			copiedDecl.Set("plans", copiedPlans)
			fields.Set("built_in_providers", copiedDecl)
		}
	}
	hintsValue, hasHints := fields.Get("install_hints")
	_, isFork := fields.Get("fork_of")
	if isFork {
		if hasHints {
			return &UnrepresentableError{Path: label + ".install_hints", Reason: "is set on a source fork, where grouped TOML takes no install"}
		}
		return nil
	}
	hints := map[string]string{}
	var declared []string
	if hasHints {
		table, ok := hintsValue.(*jsonx.OrderedMap)
		if !ok {
			return &UnrepresentableError{Path: label + ".install_hints", Reason: "is not an object"}
		}
		for _, manager := range table.Keys() {
			value, _ := table.Get(manager)
			text, ok := value.(string)
			if !ok {
				return &UnrepresentableError{Path: label + ".install_hints." + manager, Reason: "is not a string"}
			}
			if !installManagerNames[manager] {
				return &UnrepresentableError{Path: label + ".install_hints." + manager, Reason: fmt.Sprintf("names manager %q, which grouped install does not take (default, brew, apt, dnf, pacman, nix, brew-cask)", manager)}
			}
			hints[manager] = text
			declared = append(declared, manager)
		}
	}
	install := foldInstall(bin, hints, declared, opts.KeepInstallHints)
	if install == nil {
		if hasHints {
			fields.Delete("install_hints")
		}
		return nil
	}
	if hasHints {
		renameKey(fields, "install_hints", "install", install)
	} else {
		fields.Set("install", install)
	}
	sources["install"] = "install_hints"
	return nil
}

// foldInstall returns the grouped `install` value for one program's hints, or nil when every
// manager's package is the bin (the reader's default, pack-manifests.md §4, "default to the bin
// name"). A manager the legacy hints do not name has NO hint, and the grouped reader would give
// it the bin, so it is written `false` — per manager, or as `default = false` when that is what
// most managers share, or as `install = false` when no manager has one.
func foldInstall(bin string, hints map[string]string, declared []string, keep bool) any {
	// noHint marks a manager without a hint; written as false.
	const noHint = ""
	hintValue := func(value string) any {
		if value == noHint {
			return false
		}
		return value
	}
	effective := make(map[string]string, len(installManagers))
	counts := map[string]int{}
	for _, manager := range installManagers {
		value, ok := hints[manager]
		if !ok {
			value = noHint
		}
		effective[manager] = value
		counts[value]++
	}
	cask, hasCask := hints["brew-cask"]
	if keep {
		// Every named manager keeps its own line; the managers it does not name are said once,
		// as `default = false`, or the whole key is `false` when no manager has a hint.
		if counts[noHint] == len(installManagers) && !hasCask {
			return false
		}
		table := jsonx.NewOrderedMap()
		if counts[noHint] > 0 {
			table.Set("default", false)
		}
		for _, manager := range declared {
			table.Set(manager, hints[manager])
		}
		return table
	}
	// The shared default is the value most managers take; a tie keeps the bin, which needs no
	// `default` line.
	shared := bin
	for _, manager := range installManagers {
		value := effective[manager]
		if counts[value] > counts[shared] {
			shared = value
		}
	}
	exceptions := jsonx.NewOrderedMap()
	for _, manager := range installManagers {
		if effective[manager] != shared {
			exceptions.Set(manager, hintValue(effective[manager]))
		}
	}
	if hasCask {
		exceptions.Set("brew-cask", cask)
	}
	switch {
	case exceptions.Len() == 0 && shared == bin:
		return nil
	case exceptions.Len() == 0:
		return hintValue(shared)
	}
	if shared != bin {
		table := jsonx.NewOrderedMap()
		table.Set("default", hintValue(shared))
		for _, manager := range exceptions.Keys() {
			value, _ := exceptions.Get(manager)
			table.Set(manager, value)
		}
		return table
	}
	return exceptions
}

func encodeOverlayFields(label string, fields *jsonx.OrderedMap, sources map[string]string) error {
	value, ok := fields.Get("config")
	if !ok {
		return nil
	}
	body, ok := value.(*jsonx.OrderedMap)
	if !ok || body.Len() != 1 {
		return &UnrepresentableError{Path: label + ".config", Reason: "is not exactly {\"managed\": …}, the one overlay body grouped `set` says"}
	}
	managed, ok := body.Get("managed")
	if !ok {
		return &UnrepresentableError{Path: label + ".config", Reason: "has no \"managed\" body, the one overlay body grouped `set` says"}
	}
	renameKey(fields, "config", "set", managed)
	sources["set"] = "config"
	return nil
}

// legacyReplaceMatters reports whether a legacy contribution's replacing semantics differ from
// the grouped appending default for this payload.
func legacyReplaceMatters(kind Kind, fields *jsonx.OrderedMap) bool {
	switch kind {
	case KindConfigOverlay:
		value, _ := fields.Get("config")
		return containsArray(value)
	case KindAutonomy:
		for _, posture := range []string{string(PostureAutonomous), string(PostureGuarded)} {
			decl, ok := getMap(fields, posture)
			if !ok {
				continue
			}
			if launch, ok := decl.Get("launch"); ok {
				if rows, _ := launch.([]any); len(rows) > 0 {
					return true
				}
			}
			if config, ok := decl.Get("config"); ok && configBodiesHoldArrays(config) {
				return true
			}
		}
	}
	return false
}

// configBodiesHoldArrays reads a posture `config` list: its rows are surface definitions, and
// only their managed bodies fold with the append-or-replace rule.
func configBodiesHoldArrays(value any) bool {
	rows, ok := value.([]any)
	if !ok {
		return containsArray(value)
	}
	for _, row := range rows {
		table, ok := row.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		if managed, ok := table.Get("managed"); ok && containsArray(managed) {
			return true
		}
	}
	return false
}

func containsArray(value any) bool {
	switch v := value.(type) {
	case []any:
		return true
	case *jsonx.OrderedMap:
		for _, key := range v.Keys() {
			item, _ := v.Get(key)
			if containsArray(item) {
				return true
			}
		}
	}
	return false
}

// omitInferredCodecs drops a surface row's codec where DecodeTOML infers the same one from its
// path's extension.
// encodeOptionsWithoutDefault spells each null in a provider's options (declared, no default)
// as the grouped `false` the reader lowers back to it (lowerOptionsWithoutDefault).
func encodeOptionsWithoutDefault(fields *jsonx.OrderedMap) {
	options, ok := getMap(fields, "options")
	if !ok {
		return
	}
	copied := shallowCopy(options)
	for _, key := range options.Keys() {
		if value, _ := options.Get(key); value == nil {
			copied.Set(key, false)
		}
	}
	fields.Set("options", copied)
}

// surfaceRowsWithUnset moves every null in a surface row's `managed` merge patch (a deletion)
// into the row's `unset` list of JSON Pointers, the spelling lowerSurfaceUnset reads back. A
// table left empty only because its nulls moved out is dropped too: the reader recreates it
// on the pointer's way down, so the managed body reads back identical.
func surfaceRowsWithUnset(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = surfaceRowsWithUnset(item)
		}
		return out
	case *jsonx.OrderedMap:
		managed, ok := getMap(v, "managed")
		if !ok {
			return v
		}
		var pointers []any
		stripped, _ := stripNulls(managed, "", &pointers)
		if len(pointers) == 0 {
			return v
		}
		out := jsonx.NewOrderedMap()
		for _, key := range v.Keys() {
			item, _ := v.Get(key)
			if key == "managed" {
				if stripped.Len() > 0 {
					out.Set("managed", stripped)
				}
				out.Set("unset", pointers)
				continue
			}
			out.Set(key, item)
		}
		return out
	}
	return value
}

// stripNulls copies table without its null leaves, appending each one's pointer, and reports
// whether the copy is empty only because nulls were removed from it.
func stripNulls(table *jsonx.OrderedMap, prefix string, pointers *[]any) (*jsonx.OrderedMap, bool) {
	out := jsonx.NewOrderedMap()
	removed := false
	for _, key := range table.Keys() {
		item, _ := table.Get(key)
		pointer := prefix + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		switch child := item.(type) {
		case nil:
			*pointers = append(*pointers, pointer)
			removed = true
			continue
		case *jsonx.OrderedMap:
			copied, emptied := stripNulls(child, pointer, pointers)
			if emptied {
				removed = true
				continue
			}
			out.Set(key, copied)
			continue
		}
		out.Set(key, item)
	}
	return out, removed && out.Len() == 0
}

func omitInferredCodecs(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = omitInferredCodecs(item)
		}
		return out
	case *jsonx.OrderedMap:
		codec, hasCodec := v.Get("codec")
		pathValue, _ := v.Get("path")
		path, _ := pathValue.(string)
		if !hasCodec || path == "" {
			return v
		}
		inferred, ok := inferredSurfaceCodec(path)
		if !ok || inferred != codec {
			return v
		}
		out := shallowCopy(v)
		out.Delete("codec")
		return out
	}
	return value
}

// encodeMember writes one `key = value` member, splitting a long array one element per line
// and a long object (or, with dotted set, every object) into one dotted key per member.
func encodeMember(key string, value any, path []configdoc.Step, dotted bool) ([]TOMLLine, error) {
	if err := refuseNull(value, path); err != nil {
		return nil, err
	}
	keyText, err := tomlKeyText(key)
	if err != nil {
		return nil, err
	}
	return encodeKeyed(keyText, value, path, dotted)
}

func encodeKeyed(keyText string, value any, path []configdoc.Step, dotted bool) ([]TOMLLine, error) {
	inline, err := tomlInline(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pathLabel(path), err)
	}
	single := keyText + " = " + inline
	switch v := value.(type) {
	case *jsonx.OrderedMap:
		if v.Len() > 0 && (dotted || len(single) > tomlLineWidth) {
			var lines []TOMLLine
			for _, member := range v.Keys() {
				item, _ := v.Get(member)
				memberText, err := tomlKeyText(member)
				if err != nil {
					return nil, err
				}
				var memberPath []configdoc.Step
				if path != nil {
					memberPath = append(append([]configdoc.Step(nil), path...), configdoc.Step{Key: member})
				}
				emitted, err := encodeKeyed(keyText+"."+memberText, item, memberPath, false)
				if err != nil {
					return nil, err
				}
				lines = append(lines, emitted...)
			}
			return lines, nil
		}
	case []any:
		if len(v) > 1 && len(single) > tomlLineWidth {
			lines := []TOMLLine{{Text: keyText + " = [", Source: path}}
			for i, item := range v {
				text, err := tomlInline(item)
				if err != nil {
					return nil, err
				}
				var itemPath []configdoc.Step
				if path != nil {
					itemPath = append(append([]configdoc.Step(nil), path...), configdoc.Step{Index: i, IsIndex: true})
				}
				lines = append(lines, TOMLLine{Text: "  " + text + ",", Source: itemPath})
			}
			return append(lines, TOMLLine{Text: "]"}), nil
		}
	}
	return []TOMLLine{{Text: single, Source: path}}, nil
}

// tomlInline writes one value on one line, keeping an object's authored member order.
func tomlInline(value any) (string, error) {
	switch v := value.(type) {
	case *jsonx.OrderedMap:
		if v.Len() == 0 {
			return "{}", nil
		}
		parts := make([]string, 0, v.Len())
		for _, key := range v.Keys() {
			item, _ := v.Get(key)
			keyText, err := tomlKeyText(key)
			if err != nil {
				return "", err
			}
			text, err := tomlInline(item)
			if err != nil {
				return "", fmt.Errorf("%s: %w", key, err)
			}
			parts = append(parts, keyText+" = "+text)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			text, err := tomlInline(item)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	}
	if jsonx.IsInt(value) {
		n, ok := jsonx.AsInt(value)
		if !ok {
			literal, _ := jsonx.AsIntLiteral(value)
			return "", fmt.Errorf("integer %s does not fit a TOML integer", literal)
		}
		value = n
	}
	text, err := configdoc.EncodeValue(value, configdoc.TOML)
	if err != nil {
		return "", err
	}
	return string(text), nil
}

func tomlKeyText(key string) (string, error) {
	bare := key != ""
	for _, r := range key {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			bare = false
			break
		}
	}
	if bare {
		return key, nil
	}
	quoted, err := configdoc.EncodeValue(key, configdoc.TOML)
	if err != nil {
		return "", err
	}
	return string(quoted), nil
}

func refuseNull(value any, path []configdoc.Step) error {
	switch v := value.(type) {
	case nil:
		return &UnrepresentableError{Path: pathLabel(path), Reason: "is null"}
	case *jsonx.OrderedMap:
		for _, key := range v.Keys() {
			item, _ := v.Get(key)
			if err := refuseNull(item, appendStep(path, configdoc.Step{Key: key})); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range v {
			if err := refuseNull(item, appendStep(path, configdoc.Step{Index: i, IsIndex: true})); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendStep(path []configdoc.Step, step configdoc.Step) []configdoc.Step {
	return append(append([]configdoc.Step(nil), path...), step)
}

// pathLabel spells a legacy path as `contributes[3].options.pin_model`.
func pathLabel(path []configdoc.Step) string {
	var b strings.Builder
	for _, step := range path {
		if step.IsIndex {
			b.WriteString("[" + strconv.Itoa(step.Index) + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(step.Key)
	}
	return b.String()
}

// PathLabel is pathLabel for callers outside the package (a migration's messages).
func PathLabel(path []configdoc.Step) string { return pathLabel(path) }

func getMap(table *jsonx.OrderedMap, key string) (*jsonx.OrderedMap, bool) {
	value, ok := table.Get(key)
	if !ok {
		return nil, false
	}
	out, ok := value.(*jsonx.OrderedMap)
	return out, ok
}

func shallowCopy(table *jsonx.OrderedMap) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, key := range table.Keys() {
		value, _ := table.Get(key)
		out.Set(key, value)
	}
	return out
}

// renameKey replaces old with key at old's position.
func renameKey(table *jsonx.OrderedMap, old, key string, value any) {
	keys := append([]string(nil), table.Keys()...)
	rebuilt := jsonx.NewOrderedMap()
	for _, k := range keys {
		if k == old {
			rebuilt.Set(key, value)
			continue
		}
		v, _ := table.Get(k)
		rebuilt.Set(k, v)
	}
	for _, k := range keys {
		table.Delete(k)
	}
	for _, k := range rebuilt.Keys() {
		v, _ := rebuilt.Get(k)
		table.Set(k, v)
	}
}
