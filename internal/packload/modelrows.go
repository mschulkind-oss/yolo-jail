package packload

// modelrows.go composes a provider's readable model rows (docs/design/pack-manifests.md §4, "a
// model said once"): catalog facts (config-user.md §8 C7), then `every_model`, then the row's
// own, with `twin_of` inheriting a row whole and `compact_at` the window an agent compacts at.
// It lives here, beside ComposeProviders, because the launch's one provider composition lowers
// those rows into the table every derive reads (lowerReadableModels); internal/config's
// semantic seam calls the same functions, so validation and the launch cannot compose a row
// two ways.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// ReadableModel is one selectable canonical model identity and its fully composed facts. CompactAt
// is selection metadata, not a replacement id; WireID is the inherited/overridden wire identity.
type ReadableModel struct {
	ID string
	// Model is the id the model is listed and registered under: the row's `model`, or its key
	// when it says none (docs/design/model-facts-migration.md MF-D1). ID is the key.
	Model          string
	WireID         string
	WireIDAuthored bool
	CompactAt      int64
	Facts          packdecl.ProviderModel
}

// ReadableModels is provider model composition keyed by selectable id, plus declaration order.
type ReadableModels struct {
	Declared bool
	ByID     map[string]ReadableModel
	Order    []string
}

// ValidateProviderModelGraph validates authored twin identities and model ordering, whether the
// provider is selected or not. It does not require catalog facts or project a consumer.
func ValidateProviderModelGraph(provider packdecl.ProviderContribution) error {
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 2 {
			return nil
		}
		if state[id] == 1 {
			return fmt.Errorf("provider %s model %q: twin cycle detected", provider.Name, id)
		}
		row, ok := provider.ModelRows[id]
		if !ok {
			return fmt.Errorf("provider %s model %q: twin_of names a missing model row", provider.Name, id)
		}
		state[id] = 1
		if row.TwinOf != "" {
			if row.TwinOf == id {
				return fmt.Errorf("provider %s model %q: twin_of cannot name itself", provider.Name, id)
			}
			if err := visit(row.TwinOf); err != nil {
				return fmt.Errorf("provider %s model %q twin_of %q: %w", provider.Name, id, row.TwinOf, err)
			}
		}
		state[id] = 2
		return nil
	}
	for id := range provider.ModelRows {
		if id == "" {
			return fmt.Errorf("provider %s has a model with an empty id", provider.Name)
		}
		if err := visit(id); err != nil {
			return err
		}
	}
	order := append([]string(nil), provider.ModelOrder...)
	if len(order) == 0 {
		for id := range provider.ModelRows {
			order = append(order, id)
		}
		sort.Strings(order)
	}
	seen := map[string]bool{}
	for _, id := range order {
		if _, exists := provider.ModelRows[id]; !exists {
			return fmt.Errorf("provider %s model order names undeclared row %q", provider.Name, id)
		}
		if seen[id] {
			return fmt.Errorf("provider %s model order repeats row %q", provider.Name, id)
		}
		seen[id] = true
	}
	if len(order) != len(provider.ModelRows) {
		return fmt.Errorf("provider %s model order does not cover every declared row", provider.Name)
	}
	if provider.EveryModel != nil {
		if err := ValidateProviderModelFacts(*provider.EveryModel); err != nil {
			return fmt.Errorf("provider %s every_model: %w", provider.Name, err)
		}
	}
	for id, row := range provider.ModelRows {
		if err := ValidateProviderModelFacts(row); err != nil {
			return fmt.Errorf("provider %s model %q: %w", provider.Name, id, err)
		}
	}
	return nil
}

// ComposeProviderModels composes offline catalog facts, provider every_model facts, explicit rows,
// then twin inheritance and overrides. Catalog is an explicit offline input; no files or network
// are read here. Facts absent at every layer stay absent rather than receiving invented defaults.
//
// It is the SELECTION's strict composition: a row whose maker cannot be inferred, or whose price
// is incomplete, is refused, as edit validation refuses it for a selected provider.
func ComposeProviderModels(provider packdecl.ProviderContribution, catalog map[string]packdecl.ProviderModel) (ReadableModels, error) {
	return composeProviderModels(provider, catalog, true)
}

// composeLaunchModels is the launch table's composition of the same rows (lowerReadableModels):
// every layer composes exactly as ComposeProviderModels composes it, but a fact no layer states
// stays unstated rather than refusing the launch. The table carries every provider, selected or
// not, and a legacy alias may omit its maker and price too; the derives already read an absent
// fact as unsaid (docs/design/readable-config.md RC-D29).
func composeLaunchModels(provider packdecl.ProviderContribution, catalog map[string]packdecl.ProviderModel) (ReadableModels, error) {
	return composeProviderModels(provider, catalog, false)
}

func composeProviderModels(provider packdecl.ProviderContribution, catalog map[string]packdecl.ProviderModel, strict bool) (ReadableModels, error) {
	if err := ValidateProviderModelGraph(provider); err != nil {
		return ReadableModels{}, err
	}
	rows := ReadableModels{Declared: provider.ModelRows != nil, ByID: map[string]ReadableModel{}}
	order := append([]string(nil), provider.ModelOrder...)
	if len(order) == 0 {
		for id := range provider.ModelRows {
			order = append(order, id)
		}
		sort.Strings(order)
	}
	state := map[string]uint8{}
	var resolve func(string) (ReadableModel, error)
	resolve = func(id string) (ReadableModel, error) {
		if state[id] == 2 {
			return rows.ByID[id], nil
		}
		row := provider.ModelRows[id]
		state[id] = 1
		// The id the row lists: its `model`, else its key (MF-D1). The catalog, the maker and the
		// wire id follow from it, never from a name.
		listed := row.Model
		if listed == "" {
			listed = id
		}
		facts := packdecl.ProviderModel{}
		compactAt := int64(0)
		if catalogFacts, ok := catalog[listed]; ok {
			if err := ValidateProviderModelFacts(catalogFacts); err != nil {
				return ReadableModel{}, fmt.Errorf("provider %s model %q catalog facts: %w", provider.Name, id, err)
			}
			facts = CloneProviderModel(catalogFacts)
			compactAt = catalogFacts.CompactAt
		}
		if provider.EveryModel != nil {
			facts = MergeProviderModel(facts, *provider.EveryModel)
			if provider.EveryModel.CompactAt != 0 {
				compactAt = provider.EveryModel.CompactAt
			}
		}
		var inheritedWireID string
		if row.TwinOf != "" {
			base, err := resolve(row.TwinOf)
			if err != nil {
				return ReadableModel{}, fmt.Errorf("provider %s model %q twin_of %q: %w", provider.Name, id, row.TwinOf, err)
			}
			facts = CloneProviderModel(base.Facts)
			inheritedWireID = base.WireID
		}
		facts = MergeProviderModel(facts, row) // never `model`: a row's own, not a fact
		if row.CompactAt != 0 {
			compactAt = row.CompactAt
		}
		if facts.Vendor == "" {
			if maker := inferProviderMaker(listed); maker != "" {
				facts.Vendor = maker
			}
		}
		if facts.Vendor == "" && strict {
			return ReadableModel{}, fmt.Errorf("provider %s model %q: maker is ambiguous; author vendor", provider.Name, id)
		}
		if facts.Vendor != "" && !packdecl.ValidModelVendor(facts.Vendor) {
			return ReadableModel{}, fmt.Errorf("provider %s model %q: vendor %q is not a valid maker token", provider.Name, id, facts.Vendor)
		}
		if err := ValidateProviderModelFacts(facts); err != nil {
			return ReadableModel{}, fmt.Errorf("provider %s model %q: %w", provider.Name, id, err)
		}
		if err := CompleteProviderPrice(facts.Price); err != nil && strict {
			return ReadableModel{}, fmt.Errorf("provider %s model %q: %w", provider.Name, id, err)
		}
		wireID := facts.WireID
		if row.TwinOf != "" {
			wireID = inheritedWireID
		}
		if row.WireID != "" {
			wireID = row.WireID
		}
		if wireID == "" {
			wireID = listed
		}
		resolved := ReadableModel{ID: id, Model: listed, WireID: wireID, WireIDAuthored: facts.WireID != "" || row.TwinOf != "", CompactAt: compactAt, Facts: CloneProviderModel(facts)}
		rows.ByID[id] = resolved
		state[id] = 2
		return resolved, nil
	}
	for _, id := range order {
		if _, err := resolve(id); err != nil {
			return ReadableModels{}, err
		}
		rows.Order = append(rows.Order, id)
	}
	return rows, nil
}

func ValidateProviderModelFacts(model packdecl.ProviderModel) error {
	if model.Vendor != "" && !packdecl.ValidModelVendor(model.Vendor) {
		return fmt.Errorf("vendor %q is not a valid lowercase maker token", model.Vendor)
	}
	if model.API != "" {
		if strings.TrimSpace(model.API) != model.API || strings.IndexFunc(model.API, func(r rune) bool { return r == '\n' || r == '\r' || r == '\t' }) >= 0 {
			return fmt.Errorf("api must be a non-empty, whitespace-trimmed string")
		}
		if !packdecl.KnownWireAPI(model.API) {
			return fmt.Errorf("api %q is unknown; use one of %s", model.API, strings.Join(packdecl.KnownWireAPIs(), ", "))
		}
	}
	for _, field := range []struct {
		name  string
		value int64
	}{{"context", model.Context}, {"max_tokens", model.MaxTokens}, {"compact_at", model.CompactAt}} {
		if field.value < 0 {
			return fmt.Errorf("%s must be positive when authored", field.name)
		}
	}
	if model.Thinking != nil {
		seen := map[string]bool{}
		for _, value := range model.Thinking {
			if value == "" || seen[value] {
				return fmt.Errorf("thinking must contain unique non-empty values")
			}
			seen[value] = true
		}
	}
	if model.Accepts != nil {
		seen := map[string]bool{}
		for _, value := range model.Accepts {
			if (value != "text" && value != "image") || seen[value] {
				return fmt.Errorf("accepts must contain unique text and/or image values")
			}
			seen[value] = true
		}
	}
	if err := validateProviderImage(model.Images); err != nil {
		return err
	}
	if err := validateProviderPrice(model.Price); err != nil {
		return err
	}
	return nil
}

func validateProviderPrice(price *packdecl.ProviderPrice) error {
	if price == nil {
		return nil
	}
	for _, field := range []struct {
		name  string
		value *float64
	}{
		{"input", price.Input}, {"output", price.Output}, {"cache_read", price.CacheRead}, {"cache_write", price.CacheWrite},
	} {
		if field.value != nil && (math.IsNaN(*field.value) || math.IsInf(*field.value, 0) || *field.value < 0) {
			return fmt.Errorf("price.%s must be finite and non-negative", field.name)
		}
	}
	return nil
}

func CompleteProviderPrice(price *packdecl.ProviderPrice) error {
	if price == nil {
		return fmt.Errorf("model has no authored price facts; add price with input, output, cache_read and cache_write rather than relying on consumer defaults")
	}
	for _, field := range []struct {
		name  string
		value *float64
	}{
		{"input", price.Input}, {"output", price.Output}, {"cache_read", price.CacheRead}, {"cache_write", price.CacheWrite},
	} {
		if field.value == nil {
			return fmt.Errorf("price.%s is missing; author all four price rates to avoid a consumer-invented zero", field.name)
		}
	}
	return nil
}

func validateProviderImage(image *packdecl.ProviderImage) error {
	if image == nil {
		return nil
	}
	for _, field := range []struct {
		name  string
		value int64
	}{{"max_width", image.MaxWidth}, {"max_height", image.MaxHeight}, {"max_bytes", image.MaxBytes}} {
		if field.value <= 0 {
			return fmt.Errorf("images.%s must be a positive integer", field.name)
		}
	}
	if image.JPEGQuality < 1 || image.JPEGQuality > 100 {
		return fmt.Errorf("images.jpeg_quality must be between 1 and 100")
	}
	return nil
}

// InferModelVendor is the maker a model id names by itself (a Bedrock `global.<maker>.` prefix or
// a `<maker>/` route), the one a readable row leaves unwritten; "" when the id names none.
func InferModelVendor(id string) string { return inferProviderMaker(id) }

func inferProviderMaker(id string) string {
	if strings.HasPrefix(id, "global.") {
		parts := strings.Split(id, ".")
		if len(parts) >= 3 && parts[1] != "" && parts[2] != "" && packdecl.ValidModelVendor(parts[1]) {
			return parts[1]
		}
	}
	if maker, model, found := strings.Cut(id, "/"); found && model != "" && packdecl.ValidModelVendor(maker) {
		return maker
	}
	return ""
}

func MergeProviderModel(base, overlay packdecl.ProviderModel) packdecl.ProviderModel {
	if overlay.Name != "" {
		base.Name = overlay.Name
	}
	if overlay.Description != "" {
		base.Description = overlay.Description
	}
	if overlay.Vendor != "" {
		base.Vendor = overlay.Vendor
	}
	if overlay.API != "" {
		base.API = overlay.API
	}
	if overlay.Thinking != nil {
		base.Thinking = modelRowStrings(overlay.Thinking)
	}
	if overlay.Images != nil {
		image := *overlay.Images
		base.Images = &image
	}
	if overlay.Price != nil {
		base.Price = mergeProviderPrice(base.Price, overlay.Price)
	}
	if overlay.Context != 0 {
		base.Context = overlay.Context
	}
	if overlay.MaxTokens != 0 {
		base.MaxTokens = overlay.MaxTokens
	}
	if overlay.Reasoning != nil {
		value := *overlay.Reasoning
		base.Reasoning = &value
	}
	if overlay.Accepts != nil {
		base.Accepts = modelRowStrings(overlay.Accepts)
	}
	if overlay.Base != "" {
		base.Base = overlay.Base
	}
	if overlay.WireID != "" {
		base.WireID = overlay.WireID
	}
	if overlay.Extends != "" {
		base.Extends = overlay.Extends
	}
	if overlay.OpenRouterRouting != nil {
		base.OpenRouterRouting = modelRowAnyMap(overlay.OpenRouterRouting)
	}
	return base
}

func mergeProviderPrice(base, overlay *packdecl.ProviderPrice) *packdecl.ProviderPrice {
	out := cloneProviderPrice(base)
	if out == nil {
		out = &packdecl.ProviderPrice{}
	}
	if overlay.Input != nil {
		value := *overlay.Input
		out.Input = &value
	}
	if overlay.Output != nil {
		value := *overlay.Output
		out.Output = &value
	}
	if overlay.CacheRead != nil {
		value := *overlay.CacheRead
		out.CacheRead = &value
	}
	if overlay.CacheWrite != nil {
		value := *overlay.CacheWrite
		out.CacheWrite = &value
	}
	return out
}

func cloneProviderPrice(price *packdecl.ProviderPrice) *packdecl.ProviderPrice {
	if price == nil {
		return nil
	}
	out := &packdecl.ProviderPrice{}
	if price.Input != nil {
		value := *price.Input
		out.Input = &value
	}
	if price.Output != nil {
		value := *price.Output
		out.Output = &value
	}
	if price.CacheRead != nil {
		value := *price.CacheRead
		out.CacheRead = &value
	}
	if price.CacheWrite != nil {
		value := *price.CacheWrite
		out.CacheWrite = &value
	}
	return out
}

func CloneProviderModel(model packdecl.ProviderModel) packdecl.ProviderModel {
	copy := MergeProviderModel(packdecl.ProviderModel{}, model)
	copy.Price = cloneProviderPrice(model.Price)
	copy.Thinking = modelRowStrings(model.Thinking)
	copy.Accepts = modelRowStrings(model.Accepts)
	copy.Model, copy.TwinOf, copy.CompactAt = model.Model, model.TwinOf, model.CompactAt
	return copy
}

// LegacyDeriveProjection is the compatibility shape current pack derives consume through
// ctx.providers: provider model ids plus flat per-model facts. Unsupported names are explicit;
// callers must not silently claim a receiver consumed those canonical facts.
type LegacyDeriveProjection struct {
	ProviderEntry *jsonx.OrderedMap
	HasModelRows  bool
	Unsupported   map[string][]string
}

// RequireSupportedFor turns the structured census into an actionable refusal for callers that
// cannot accept a partial projection.
func (projection LegacyDeriveProjection) RequireSupportedFor(receiver string) error {
	ids := make([]string, 0, len(projection.Unsupported))
	for id := range projection.Unsupported {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil
	}
	var details []string
	for _, id := range ids {
		details = append(details, fmt.Sprintf("%s: %s", id, strings.Join(projection.Unsupported[id], ", ")))
	}
	return fmt.Errorf("selected receiver %s cannot consume canonical model facts in the legacy derive projection (%s); omit those facts or add a receiver-specific mapping before selection", receiver, strings.Join(details, "; "))
}

// ProjectProviderModelsForLegacyDerives lowers only the canonical facts understood by the
// existing shared derive vocabulary. Facts not represented there are returned by model id so a
// receiver can refuse them actionably; this does not activate or invoke a derive.
func ProjectProviderModelsForLegacyDerives(models ReadableModels) (LegacyDeriveProjection, error) {
	entry := jsonx.NewOrderedMap()
	if !models.Declared {
		return LegacyDeriveProjection{ProviderEntry: entry, Unsupported: map[string][]string{}}, nil
	}
	ids, options, routing := jsonx.NewOrderedMap(), jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
	unsupported := map[string][]string{}
	order := append([]string(nil), models.Order...)
	if len(order) == 0 {
		for id := range models.ByID {
			order = append(order, id)
		}
		sort.Strings(order)
	}
	for ordinal, id := range order {
		model, ok := models.ByID[id]
		if !ok {
			return LegacyDeriveProjection{}, fmt.Errorf("model order names missing row %q", id)
		}
		if model.ID != "" && model.ID != id {
			return LegacyDeriveProjection{}, fmt.Errorf("model key %q disagrees with row id %q", id, model.ID)
		}
		listed := model.Model
		if listed == "" {
			listed = id
		}
		ids.Set(id, listed)
		facts := model.Facts
		flat := jsonx.NewOrderedMap()
		flat.Set("order", strconv.Itoa(ordinal))
		setText := func(key, value string) {
			if value != "" {
				flat.Set(key, value)
			}
		}
		setText("name", facts.Name)
		setText("description", facts.Description)
		setText("vendor", facts.Vendor)
		// `context_window` is the window each derive compacts at (claude's
		// CLAUDE_CODE_AUTO_COMPACT_WINDOW, pi's contextWindow), so a row's `compact_at` is
		// lowered there, over its context: the compact twin of pack-manifests.md §4 is one model
		// at a narrower window, which is how a legacy twin row says it too (RC-D29).
		if model.CompactAt != 0 {
			flat.Set("context_window", strconv.FormatInt(model.CompactAt, 10))
		} else if facts.Context != 0 {
			flat.Set("context_window", strconv.FormatInt(facts.Context, 10))
		}
		if facts.MaxTokens != 0 {
			flat.Set("max_tokens", strconv.FormatInt(facts.MaxTokens, 10))
		}
		if facts.Reasoning != nil {
			flat.Set("reasoning", strconv.FormatBool(*facts.Reasoning))
		}
		if len(facts.Accepts) > 0 {
			flat.Set("input", strings.Join(facts.Accepts, ","))
		}
		if facts.Price != nil {
			for _, rate := range []struct {
				name  string
				value *float64
			}{{"input", facts.Price.Input}, {"output", facts.Price.Output}, {"cache_read", facts.Price.CacheRead}, {"cache_write", facts.Price.CacheWrite}} {
				if rate.value != nil {
					flat.Set("cost_"+rate.name, strconv.FormatFloat(*rate.value, 'f', -1, 64))
				}
			}
		}
		setText("base", facts.Base)
		wireID := model.WireID
		if facts.WireID != "" {
			wireID = facts.WireID
		}
		if model.WireIDAuthored {
			setText("wire_id", wireID)
		}
		setText("extends", facts.Extends)
		if flat.Len() > 0 {
			options.Set(id, flat)
		}
		if facts.OpenRouterRouting != nil {
			routing.Set(id, modelRowOrderedValue(facts.OpenRouterRouting))
		}
		fields := []string{}
		if facts.Accepts != nil && len(facts.Accepts) == 0 {
			fields = append(fields, "accepts")
		}
		if facts.API != "" {
			fields = append(fields, "api")
		}
		if facts.Thinking != nil {
			fields = append(fields, "thinking")
		}
		if facts.Images != nil {
			fields = append(fields, "images")
		}
		if len(fields) > 0 {
			sort.Strings(fields)
			unsupported[id] = fields
		}
	}
	entry.Set("models", ids)
	if options.Len() > 0 {
		entry.Set("model_options", options)
	}
	if routing.Len() > 0 {
		entry.Set("model_routing", routing)
	}
	return LegacyDeriveProjection{ProviderEntry: entry, HasModelRows: true, Unsupported: unsupported}, nil
}

func modelRowOrderedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := jsonx.NewOrderedMap()
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out.Set(key, modelRowOrderedValue(typed[key]))
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = modelRowOrderedValue(child)
		}
		return out
	default:
		return value
	}
}

func modelRowStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func modelRowAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = modelRowValue(value)
	}
	return out
}

func modelRowValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return modelRowAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = modelRowValue(child)
		}
		return out
	default:
		return value
	}
}
