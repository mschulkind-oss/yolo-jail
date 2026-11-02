package config

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ReadableSemanticInputSource carries one invocation's already loaded selected pack closure and
// the explicit offline facts/configuration read by its caller. Pack roots must remain leased
// until BuildReadableSemanticInputs returns; the builder never stages, resolves, or materializes
// packs and never substitutes an empty catalog for a missing one.
type ReadableSemanticInputSource struct {
	Config           *jsonx.OrderedMap
	User             *jsonx.OrderedMap
	Workspace        *jsonx.OrderedMap
	UserOrigins      map[string][]string
	WorkspaceOrigins map[string][]string
	Packs            []*packload.Pack
	Catalog          map[string]map[string]packdecl.ProviderModel
}

// ReadableSemanticInputs is the immutable typed F/G boundary produced for one config invocation.
// AllowancePlan and ProviderConsumer prove the existing semantic consumers accepted these inputs;
// the input fields remain available to callers that need the exact typed declarations.
type ReadableSemanticInputs struct {
	AllowanceInput   ReadableAllowanceInput
	AllowancePlan    ReadableAllowancePlan
	ProviderInput    ProviderSemanticInput
	ProviderConsumer ProviderConsumerComposition
}

// BuildReadableSemanticInputs projects declarations from already loaded selected packs, clones
// caller-owned facts, and validates both consumers through their existing semantic paths. It does
// not infer grants, activate runtime approvals, stage packs, or fetch catalog facts.
func BuildReadableSemanticInputs(source ReadableSemanticInputSource) (ReadableSemanticInputs, error) {
	if source.Catalog == nil {
		return ReadableSemanticInputs{}, fmt.Errorf("readable semantic inputs require an explicit offline provider catalog")
	}
	packs := append([]*packload.Pack(nil), source.Packs...)
	brokerDeclarations, packSettings, err := ReadableDeclarationsForPacks(packs)
	if err != nil {
		return ReadableSemanticInputs{}, err
	}

	selected := make([]string, 0, len(packs))
	for _, pack := range packs {
		selected = append(selected, pack.Name)
	}
	var authoredProviders *jsonx.OrderedMap
	if raw, present := configValue(source.Config, "providers"); present {
		var ok bool
		authoredProviders, ok = raw.(*jsonx.OrderedMap)
		if !ok {
			return ReadableSemanticInputs{}, fmt.Errorf("config.providers: expected a table")
		}
	}
	providerDeclarations, err := packload.ComposeProviderDeclarations(authoredProviders, packs)
	if err != nil {
		return ReadableSemanticInputs{}, err
	}
	selection, err := ConfigActiveProviderSelection(source.Config)
	if err != nil {
		return ReadableSemanticInputs{}, err
	}
	providerInput := ProviderSemanticInput{
		Declarations: providerDeclarations,
		Selection:    selection,
		Receivers:    ProviderReceivers(packs),
		Catalog:      cloneProviderCatalog(source.Catalog),
	}

	// The shared F validator checks every authored provider graph even when no active_provider
	// key exists; declarations never become selectors by being present.
	providerConsumer, err := ConfigProviderConsumer(source.Config, providerDeclarations, packs, providerInput.Catalog)
	if err != nil {
		return ReadableSemanticInputs{}, err
	}
	if providerConsumer.Readable == nil {
		if _, err := ComposeProviderSemantics(providerInput); err != nil {
			return ReadableSemanticInputs{}, err
		}
		_, legacySelection := configValue(source.Config, ProfileKey)
		_, userProfiles := configValue(source.User, "profiles")
		if legacySelection || userProfiles {
			if err := validateLegacyProviderInputs(source.Config, source.User, packs, providerInput.Catalog); err != nil {
				return ReadableSemanticInputs{}, err
			}
		}
	}

	allowanceInput := ReadableAllowanceInput{
		User:             cloneReadableMap(source.User),
		Workspace:        cloneReadableMap(source.Workspace),
		SelectedPacks:    append([]string(nil), selected...),
		Declarations:     cloneReadableBrokerDeclarations(brokerDeclarations),
		PackSettings:     cloneReadablePackDeclarations(packSettings),
		UserOrigins:      cloneReadableOrigins(source.UserOrigins),
		WorkspaceOrigins: cloneReadableOrigins(source.WorkspaceOrigins),
	}
	allowancePlan, err := ResolveReadableAllowance(allowanceInput)
	if err != nil {
		return ReadableSemanticInputs{}, err
	}
	return ReadableSemanticInputs{
		AllowanceInput: allowanceInput, AllowancePlan: allowancePlan,
		ProviderInput: providerInput, ProviderConsumer: providerConsumer,
	}, nil
}

func validateLegacyProviderInputs(config, user *jsonx.OrderedMap, packs []*packload.Pack,
	catalog map[string]map[string]packdecl.ProviderModel) error {
	var userProviders *jsonx.OrderedMap
	if raw, present := configValue(config, "providers"); present {
		var ok bool
		userProviders, ok = raw.(*jsonx.OrderedMap)
		if !ok {
			return fmt.Errorf("config.providers: expected an object")
		}
	}
	userProfiles := map[string]packload.UserProfile{}
	if raw, present := configValue(user, "profiles"); present && raw != nil {
		var problems []string
		userProfiles, problems = checkProfiles(raw)
		if len(problems) != 0 {
			return fmt.Errorf("%s", strings.Join(problems, "; "))
		}
	}
	composed, err := packload.ComposeProviders(userProviders, packs)
	if err != nil {
		return err
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, composed)
	if err != nil {
		return err
	}
	normalized := NormalizeLegacyProviderConsumers(composed, resolved, catalog)
	if len(normalized.Differences) != 0 {
		first := normalized.Differences[0]
		return fmt.Errorf("legacy provider consumers cannot be represented canonically at %s: %s", first.Path, first.Reason)
	}
	return nil
}

func configValue(config *jsonx.OrderedMap, key string) (any, bool) {
	if config == nil {
		return nil, false
	}
	return config.Get(key)
}

func cloneReadableMap(source *jsonx.OrderedMap) *jsonx.OrderedMap {
	if source == nil {
		return nil
	}
	cloned := jsonx.NewOrderedMap()
	for _, key := range source.Keys() {
		value, _ := source.Get(key)
		cloned.Set(key, cloneReadableValue(value))
	}
	return cloned
}

func cloneReadableValue(value any) any {
	switch typed := value.(type) {
	case *jsonx.OrderedMap:
		return cloneReadableMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i := range typed {
			cloned[i] = cloneReadableValue(typed[i])
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneReadableValue(item)
		}
		return cloned
	default:
		return value
	}
}

func cloneProviderCatalog(source map[string]map[string]packdecl.ProviderModel) map[string]map[string]packdecl.ProviderModel {
	if source == nil {
		return nil
	}
	cloned := make(map[string]map[string]packdecl.ProviderModel, len(source))
	for provider, rows := range source {
		cloned[provider] = cloneOfflineModelFacts(rows)
	}
	return cloned
}

func cloneReadableOrigins(source map[string][]string) map[string][]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string][]string, len(source))
	for path, files := range source {
		cloned[path] = append([]string(nil), files...)
	}
	return cloned
}

func cloneReadableBrokerDeclarations(source []ReadableBrokerDeclaration) []ReadableBrokerDeclaration {
	if source == nil {
		return nil
	}
	cloned := make([]ReadableBrokerDeclaration, len(source))
	for i, declaration := range source {
		cloned[i] = readableBrokerDeclaration(declaration.Pack, declaration.Label, declaration.Brokered, declaration.Settings)
	}
	return cloned
}

func cloneReadablePackDeclarations(source []ReadablePackDeclaration) []ReadablePackDeclaration {
	if source == nil {
		return nil
	}
	cloned := make([]ReadablePackDeclaration, len(source))
	for i, declaration := range source {
		cloned[i] = declaration.clone()
	}
	return cloned
}
