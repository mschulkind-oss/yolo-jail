package config

// providersemantic.go contains the unactivated semantic seam for the readable provider config.
// Its inputs are the typed declarations B2 will produce; it does not replace profile readers or
// alter launch composition. Provider-capable receivers must come from pack-declared contracts,
// never from every installed program name.

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ProviderTarget is a typed selection of a provider, one of its variants, or the agent's native
// login. Exactly one of Native and Provider is set. Variant retains the provider/variant identity.
type ProviderTarget struct {
	Provider string
	Variant  string
	Native   bool
}

// IsVariant reports whether target names a provider variant.
func (target ProviderTarget) IsVariant() bool { return target.Provider != "" && target.Variant != "" }

// ActiveProviderSelection is the typed form B2's active_provider decoder supplies. Default
// reaches only receivers declared by selected packs; Named overrides Default for that receiver.
// An empty selection makes no choice, even when declarations exist.
type ActiveProviderSelection struct {
	Default []string
	Named   map[string][]string
}

// SelectedProvider is one resolved provider choice. Variant fields are copied as typed values so
// capabilities/protocols survive the compatibility bridge instead of being flattened into options.
type SelectedProvider struct {
	Target        ProviderTarget
	Provider      string
	Model         string
	Via           string
	Options       map[string]packdecl.OptionDefault
	Subagents     luahook.Subagents
	EnforceModels *bool
	Capabilities  []string
	Protocols     []string
}

// LegacyProviderCompatibility is the typed bridge for existing profile consumers. The fields
// absent from packload.ResolvedProfile remain alongside it so the compatibility view cannot drop
// capabilities or protocol/wire contracts.
type LegacyProviderCompatibility struct {
	Profile      packload.ResolvedProfile
	Capabilities []string
	Protocols    []string
}

// LegacyCompatibility projects a resolved direct provider/variant into the old profile-shaped
// resolver contract while retaining fields ResolvedProfile does not model. Native has no profile.
func (selected SelectedProvider) LegacyCompatibility() LegacyProviderCompatibility {
	options := map[string]string{}
	for name, value := range selected.Options {
		if value.Defaulted {
			options[name] = value.Value
		}
	}
	if selected.Model != "" {
		options["model"] = selected.Model
	}
	profile := packload.ResolvedProfile{Provider: selected.Provider, Options: options, Via: selected.Via,
		EnforceModels: selected.EnforceModels, Subagents: selected.Subagents}
	if selected.Target.Native {
		profile = packload.ResolvedProfile{}
	}
	return LegacyProviderCompatibility{Profile: profile, Capabilities: cloneStrings(selected.Capabilities), Protocols: cloneStrings(selected.Protocols)}
}

// ProviderReceiver is the selected pack's explicit receiver contract for provider sets.
type ProviderReceiver struct {
	Bin               string
	HoldsProviderSets bool
}

// ProviderSelectionResult is the per-receiver selected set. NativeReceivers distinguishes an
// explicit native selection (which means preserve the agent's own config) from no declaration.
type ProviderSelectionResult struct {
	ByReceiver        map[string][]SelectedProvider
	NativeReceivers   map[string]bool
	WholeReceivers    []string
	NarrowedReceivers []string
}

// ProviderSemanticInput is the public F1/F2 input B2 will normalize into typed declarations. Catalog
// facts are supplied by an explicit offline source such as modelcatalog.ReadCanonicalFacts; empty entries remain absent.
type ProviderSemanticInput struct {
	Declarations []packdecl.ProviderContribution
	Selection    ActiveProviderSelection
	Receivers    []ProviderReceiver
	Catalog      map[string]map[string]packdecl.ProviderModel
	// ReportModelGaps composes each selected provider's models as a launch does: a model with an
	// unknown maker or an incomplete price composes, and each such gap is listed in
	// ProviderSemanticResult.ModelGaps instead of refusing (docs/reference/config/rulings.md
	// RC-D29 (6)). Edit validation sets it, so it can refuse only a gap the edit introduces.
	ReportModelGaps bool
}

// ProviderModelGap is one selected provider's model whose maker or price no layer states
// (packload.ModelGap), named with its provider.
type ProviderModelGap struct {
	Provider string
	Model    string
	Problem  string
}

// ProviderSemanticResult contains validated selections and the fully composed canonical model facts.
type ProviderSemanticResult struct {
	Selection     ProviderSelectionResult
	Models        map[string]ReadableModels
	LegacyDerives map[string]LegacyDeriveProjection
	ModelGaps     []ProviderModelGap
}

// ComposeProviderSemantics is the public, inactive semantic entry point: validates and resolves
// provider selectors, then composes each declared provider's offline/catalog/every_model/row facts.
// It performs no I/O and does not alter legacy profile resolution or the runtime provider table.
func ComposeProviderSemantics(input ProviderSemanticInput) (ProviderSemanticResult, error) {
	for _, declaration := range input.Declarations {
		if err := ValidateProviderModelGraph(declaration); err != nil {
			return ProviderSemanticResult{}, err
		}
	}
	selection, err := ResolveActiveProviders(input.Declarations, input.Selection, input.Receivers)
	if err != nil {
		return ProviderSemanticResult{}, err
	}
	result := ProviderSemanticResult{Selection: selection, Models: map[string]ReadableModels{}, LegacyDerives: map[string]LegacyDeriveProjection{}}
	selectedProviders := map[string]bool{}
	for _, selected := range selection.ByReceiver {
		for _, target := range selected {
			selectedProviders[target.Provider] = true
		}
	}
	for _, declaration := range input.Declarations {
		if !selectedProviders[declaration.Name] {
			continue
		}
		var models ReadableModels
		if input.ReportModelGaps {
			var gaps []packload.ModelGap
			models, gaps, err = packload.ComposeProviderModelsWithGaps(declaration, input.Catalog[declaration.Name])
			for _, gap := range gaps {
				result.ModelGaps = append(result.ModelGaps, ProviderModelGap{Provider: declaration.Name, Model: gap.Model, Problem: gap.Problem})
			}
		} else {
			models, err = ComposeProviderModels(declaration, input.Catalog[declaration.Name])
		}
		if err != nil {
			return ProviderSemanticResult{}, err
		}
		result.Models[declaration.Name] = models
		projection, err := ProjectProviderModelsForLegacyDerives(models)
		if err != nil {
			return ProviderSemanticResult{}, fmt.Errorf("provider %s legacy derive projection: %w", declaration.Name, err)
		}
		result.LegacyDerives[declaration.Name] = projection
	}
	return result, nil
}

// ProviderReceivers returns program bins whose selected pack explicitly declares a wire protocol
// or built-in provider contract. It excludes unrelated installed utility programs. This is a
// conservative first receiver contract; callers may pass a stricter pack-declared projection.
func ProviderReceivers(packs []*packload.Pack) []ProviderReceiver {
	set := map[string]bool{}
	setCapable := map[string]bool{}
	for _, pack := range packs {
		if pack == nil || pack.Decl == nil {
			continue
		}
		for _, c := range pack.Decl.Contributions() {
			if !c.Provides() || c.Bin == "" {
				continue
			}
			if c.Protocols != nil || c.ProviderSets || pack.Decl.BuiltInProvidersFor(c.Bin) != nil {
				set[c.Bin] = true
				setCapable[c.Bin] = pack.Decl.HoldsProviderSets(c.Bin)
			}
		}
	}
	out := make([]ProviderReceiver, 0, len(set))
	for bin := range set {
		out = append(out, ProviderReceiver{Bin: bin, HoldsProviderSets: setCapable[bin]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bin < out[j].Bin })
	return out
}

// ResolveActiveProviders validates declarations and resolves the readable selectors against one
// shared provider/alias/variant namespace. The receiver list must be derived from selected pack
// declarations (ProviderReceivers), not installed bins. Provider declarations never select.
func ResolveActiveProviders(declarations []packdecl.ProviderContribution, selection ActiveProviderSelection,
	receivers []ProviderReceiver) (ProviderSelectionResult, error) {
	ns, err := buildProviderNamespace(declarations)
	if err != nil {
		return ProviderSelectionResult{}, err
	}
	resolve := func(name string) (SelectedProvider, error) {
		if problem := providerSelectorProblem(name); problem != "" {
			return SelectedProvider{}, fmt.Errorf("selector %q: %s", name, problem)
		}
		if name == "native" {
			return SelectedProvider{Target: ProviderTarget{Native: true}}, nil
		}
		target, ok := ns.names[name]
		if !ok {
			if refusal, replaced := ns.replaced[name]; replaced {
				return SelectedProvider{}, fmt.Errorf("%s", refusal)
			}
			return SelectedProvider{}, fmt.Errorf("unknown active provider %q. Declared: %s", name, strings.Join(ns.display, ", "))
		}
		declaration := ns.byName[target.provider]
		if target.variant == "" {
			return SelectedProvider{Target: ProviderTarget{Provider: target.provider}, Provider: target.provider, Model: declaration.Model,
				Via: declaration.Via, Options: cloneOptionDefaults(declaration.Options), Capabilities: cloneStrings(declaration.Capabilities)}, nil
		}
		variant := declaration.Variants[target.variant]
		subagents, err := selectedSubagents(variant.Subagents)
		if err != nil {
			return SelectedProvider{}, fmt.Errorf("provider %s variant %s: %w", target.provider, target.variant, err)
		}
		options := cloneOptionDefaults(declaration.Options)
		for key, value := range variant.Options {
			if options == nil {
				options = map[string]packdecl.OptionDefault{}
			}
			options[key] = value
		}
		capabilities := declaration.Capabilities
		if variant.Capabilities != nil {
			capabilities = variant.Capabilities
		}
		return SelectedProvider{Target: ProviderTarget{Provider: target.provider, Variant: target.variant}, Provider: target.provider,
			Model: firstString(variant.Model, declaration.Model), Via: firstString(variant.Via, declaration.Via),
			Options: options, Subagents: subagents, EnforceModels: variant.EnforceModels,
			Capabilities: cloneStrings(capabilities), Protocols: cloneStrings(variant.Protocols)}, nil
	}
	result := ProviderSelectionResult{ByReceiver: map[string][]SelectedProvider{}, NativeReceivers: map[string]bool{}}
	receiverSet := map[string]ProviderReceiver{}
	for _, receiver := range receivers {
		if receiver.Bin != "" {
			receiverSet[receiver.Bin] = receiver
		}
	}
	for receiver, names := range selection.Named {
		if receiver == "" {
			return ProviderSelectionResult{}, fmt.Errorf("active_provider has an empty receiver name")
		}
		if _, exists := receiverSet[receiver]; !exists {
			return ProviderSelectionResult{}, fmt.Errorf("active_provider.%s: no selected pack declares this provider-capable receiver", receiver)
		}
		if err := validateSelectionNames("active_provider."+receiver, names); err != nil {
			return ProviderSelectionResult{}, err
		}
		for _, name := range names {
			if _, err := resolve(name); err != nil {
				return ProviderSelectionResult{}, fmt.Errorf("active_provider.%s: %w", receiver, err)
			}
		}
		if len(names) > 1 && !receiverSet[receiver].HoldsProviderSets {
			return ProviderSelectionResult{}, fmt.Errorf("active_provider.%s: selected pack does not declare provider_sets, so this receiver can select only one provider", receiver)
		}
		if len(names) > 1 && containsString(names, "native") {
			return ProviderSelectionResult{}, fmt.Errorf("active_provider.%s: native cannot be mixed with selectable providers", receiver)
		}
	}
	if err := validateSelectionNames("active_provider.default", selection.Default); err != nil {
		return ProviderSelectionResult{}, err
	}
	for _, name := range selection.Default {
		if _, err := resolve(name); err != nil {
			return ProviderSelectionResult{}, fmt.Errorf("active_provider.default: %w", err)
		}
	}
	if len(selection.Default) > 1 && containsString(selection.Default, "native") {
		return ProviderSelectionResult{}, fmt.Errorf("active_provider.default: native cannot be mixed with selectable providers")
	}
	for receiver := range receiverSet {
		names := selection.Default
		if named, exists := selection.Named[receiver]; exists {
			names = named
		}
		if len(names) == 0 {
			continue
		}
		if len(names) == 1 && names[0] == "native" {
			result.NativeReceivers[receiver] = true
			continue
		}
		if len(names) > 1 {
			if receiverSet[receiver].HoldsProviderSets {
				result.WholeReceivers = append(result.WholeReceivers, receiver)
			} else {
				result.NarrowedReceivers = append(result.NarrowedReceivers, receiver)
				names = names[:1]
			}
		}
		selected := make([]SelectedProvider, 0, len(names))
		seenProviders := map[string]bool{}
		for _, name := range names {
			item, err := resolve(name)
			if err != nil {
				return ProviderSelectionResult{}, fmt.Errorf("active_provider.%s: %w", receiver, err)
			}
			if item.Provider != "" {
				if seenProviders[item.Provider] {
					return ProviderSelectionResult{}, fmt.Errorf("active_provider.%s: provider %s is selected more than once through different selectors; keep one", receiver, item.Provider)
				}
				seenProviders[item.Provider] = true
			}
			selected = append(selected, item)
		}
		result.ByReceiver[receiver] = selected
	}
	sort.Strings(result.WholeReceivers)
	sort.Strings(result.NarrowedReceivers)
	return result, nil
}

func providerSelectorProblem(name string) string {
	if name == "native" {
		return ""
	}
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		if len(parts) != 2 {
			return "provider/variant selector must have exactly one '/'"
		}
		if problem := providerNameProblem(parts[0]); problem != "" {
			return problem
		}
		return providerNameProblem(parts[1])
	}
	return providerNameProblem(name)
}

func providerNameProblem(name string) string {
	if name == "" {
		return "name must not be empty"
	}
	if strings.TrimSpace(name) != name || strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return "name cannot contain whitespace"
	}
	if strings.ContainsAny(name, ",=") {
		return "name cannot contain ',' or '=' because -p uses them as separators"
	}
	return ""
}

func validateSelectionNames(path string, names []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("%s: provider selection %q is repeated", path, name)
		}
		seen[name] = true
	}
	return nil
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func selectedSubagents(raw map[string]any) (luahook.Subagents, error) {
	var out luahook.Subagents
	if raw == nil {
		return out, nil
	}
	for key, value := range raw {
		switch key {
		case "default":
			text, ok := value.(string)
			if !ok || text == "" {
				return out, fmt.Errorf("subagents.default must be a non-empty model name")
			}
			out.Default = text
		case "roles":
			roles, ok := value.(map[string]any)
			if !ok {
				return out, fmt.Errorf("subagents.roles must map role names to model names")
			}
			out.Roles = map[string]string{}
			for role, rawModel := range roles {
				model, ok := rawModel.(string)
				if role == "" || !ok || model == "" {
					return out, fmt.Errorf("subagents.roles entries need non-empty role and model names")
				}
				out.Roles[role] = model
			}
		default:
			return out, fmt.Errorf("subagents has unknown field %q", key)
		}
	}
	if out.Default == "" && len(out.Roles) == 0 {
		return out, fmt.Errorf("subagents must declare default or roles")
	}
	return out, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func cloneOptionDefaults(values map[string]packdecl.OptionDefault) map[string]packdecl.OptionDefault {
	if values == nil {
		return nil
	}
	out := make(map[string]packdecl.OptionDefault, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = cloneSemanticValue(value)
	}
	return out
}

func cloneSemanticValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = cloneSemanticValue(child)
		}
		return out
	default:
		return value
	}
}
