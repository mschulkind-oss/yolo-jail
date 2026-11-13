package config

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ProviderViaRoute is an already-resolved via address and caller credential for one service.
type ProviderViaRoute struct {
	URL           string
	APIKeyEnvName string
}

// ProviderDeriveRequest names an actual selected pack and one of its declared derived surfaces.
// Context supplies only non-selection inputs; ViaRoutes carries already-resolved service routes.
// This seam is pure and is not wired into launch/runtime paths: a launch lowers readable model
// rows inside packload.ComposeProviders instead (docs/reference/config/rulings.md#RC-D29).
type ProviderDeriveRequest struct {
	Receiver  string
	Pack      *packload.Pack
	Surface   string
	ViaRoutes map[string]ProviderViaRoute
	Context   luahook.DeriveCtx
}

// ProviderDeriveOutput is one actual declared derive result, labeled by its owning pack surface.
type ProviderDeriveOutput struct {
	Receiver string
	Pack     string
	Surface  string
	Value    map[string]any
}

// ProjectProviderSemantics runs typed provider/model composition through each named selected
// receiver's actual declared derive. Unsupported canonical facts refuse before Lua runs and name
// the selected pack, receiver, and surface; this function does not activate or modify launch code.
func ProjectProviderSemantics(input ProviderSemanticInput, requests []ProviderDeriveRequest) (ProviderSemanticResult, []ProviderDeriveOutput, error) {
	semantic, err := ComposeProviderSemantics(input)
	if err != nil {
		return ProviderSemanticResult{}, nil, err
	}
	outputs := make([]ProviderDeriveOutput, 0, len(requests))
	for _, request := range requests {
		if request.Pack == nil || request.Pack.Decl == nil {
			return ProviderSemanticResult{}, nil, fmt.Errorf("provider derive receiver %s has no selected pack declaration", request.Receiver)
		}
		if request.Receiver == "" || request.Surface == "" {
			return ProviderSemanticResult{}, nil, fmt.Errorf("provider derive request needs a declared receiver and surface")
		}
		contractFound := false
		for _, receiver := range ProviderReceivers([]*packload.Pack{request.Pack}) {
			if receiver.Bin == request.Receiver {
				contractFound = true
				break
			}
		}
		if !contractFound {
			return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s does not declare %s as a provider-capable receiver", request.Pack.Name, request.Receiver)
		}
		declared := false
		surfaces, problems := request.Pack.SurfacesFor(true)
		if len(problems) > 0 {
			return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s surface declarations: %s", request.Pack.Name, problems[0])
		}
		for _, surface := range surfaces {
			if surface.Agent == request.Receiver && surface.Name == request.Surface {
				declared = true
				break
			}
		}
		if !declared {
			return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s does not declare derived surface %s/%s", request.Pack.Name, request.Receiver, request.Surface)
		}
		selected := semantic.Selection.ByReceiver[request.Receiver]
		if semantic.Selection.NativeReceivers[request.Receiver] || len(selected) == 0 {
			continue
		}
		if len(selected) > 1 {
			for i := 1; i < len(selected); i++ {
				if selected[i].Via != "" {
					return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s derive %s/%s: secondary selected provider %s uses via service %s, but only the primary selection can supply a via route; remove or reselect %s", request.Pack.Name, request.Receiver, request.Surface, selected[i].Provider, selected[i].Via, selected[i].Provider)
				}
			}
		}
		for _, target := range selected {
			projection := semantic.LegacyDerives[target.Provider]
			if err := projection.RequireSupportedFor(request.Receiver); err != nil {
				return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s derive %s: %w", request.Pack.Name, request.Surface, err)
			}
		}
		ctx := request.Context
		ctx.Agent, ctx.Surface = request.Receiver, request.Surface
		ctx.Tables = cloneDeriveTables(ctx.Tables)
		ctx.SelectedProvider, ctx.ProfileName, ctx.Profile, ctx.Subagents = "", "", map[string]string{}, luahook.Subagents{}
		ctx.ActiveSet, ctx.ModelsNotEnforced = nil, false
		ctx.ViaURL, ctx.ViaAPIKeyEnvName = "", ""
		providerTable := ctx.Tables["providers"]
		if providerTable == nil {
			providerTable = map[string]any{}
			ctx.Tables["providers"] = providerTable
		}
		for i, target := range selected {
			compat := target.LegacyCompatibility()
			selector := target.Provider
			if target.Target.Variant != "" {
				selector += "/" + target.Target.Variant
			}
			ctx.ActiveSet = append(ctx.ActiveSet, luahook.SetEntry{ProfileName: selector, Provider: target.Provider,
				Profile: cloneStringMap(compat.Profile.Options), ModelsNotEnforced: !packload.ModelsEnforced(compat.Profile)})
			if i == 0 {
				ctx.SelectedProvider, ctx.ProfileName = target.Provider, selector
				ctx.Profile, ctx.Subagents = cloneStringMap(compat.Profile.Options), compat.Profile.Subagents
				ctx.ModelsNotEnforced = !packload.ModelsEnforced(compat.Profile)
				if target.Via != "" {
					route, ok := request.ViaRoutes[target.Via]
					if !ok || route.URL == "" || route.APIKeyEnvName == "" {
						return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s derive %s/%s: selected provider %s uses via service %s, but no resolved URL and caller-token route was supplied; resolve that service for this receiver or remove via", request.Pack.Name, request.Receiver, request.Surface, target.Provider, target.Via)
					}
					ctx.ViaURL, ctx.ViaAPIKeyEnvName = route.URL, route.APIKeyEnvName
				}
			}
			entry, ok := providerTable[target.Provider].(map[string]any)
			if providerTable[target.Provider] != nil && !ok {
				return ProviderSemanticResult{}, nil, fmt.Errorf("provider %s has a non-object derive entry", target.Provider)
			}
			if !ok {
				entry = map[string]any{}
			}
			entry = cloneStringAnyMap(entry)
			projection := semantic.LegacyDerives[target.Provider]
			if projection.HasModelRows {
				delete(entry, "models")
				delete(entry, "model_options")
				delete(entry, "model_routing")
				delete(entry, "model_shared")
				for key, value := range jsonx.PlainMap(projection.ProviderEntry) {
					entry[key] = value
				}
			}
			if target.Capabilities != nil {
				entry["capabilities"] = stringValues(target.Capabilities)
			}
			providerTable[target.Provider] = entry
		}
		script := packload.DeriveScript(request.Pack)
		registrations, err := (luahook.GopherLuaVM{}).DeriveRegistrations(script)
		if err != nil {
			return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s derive registrations: %w", request.Pack.Name, err)
		}
		registered := false
		for _, registration := range registrations {
			if registration.Agent == request.Receiver && registration.Surface == request.Surface {
				registered = true
				break
			}
		}
		if !registered {
			return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s declares %s/%s but derive.lua registers no yolo.derive producer for it; register that surface or choose a supported derived surface", request.Pack.Name, request.Receiver, request.Surface)
		}
		value, err := (luahook.GopherLuaVM{}).Derive(script, &ctx)
		if err != nil {
			return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s derive %s/%s: %w", request.Pack.Name, request.Receiver, request.Surface, err)
		}
		if value == nil {
			return ProviderSemanticResult{}, nil, fmt.Errorf("packs/%s derive %s/%s registered but returned no object; return an object (use {} for an empty result)", request.Pack.Name, request.Receiver, request.Surface)
		}
		outputs = append(outputs, ProviderDeriveOutput{Receiver: request.Receiver, Pack: request.Pack.Name, Surface: request.Surface, Value: value})
	}
	return semantic, outputs, nil
}

func stringValues(values []string) []any {
	if values == nil {
		return nil
	}
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneDeriveTables(tables map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(tables))
	for name, values := range tables {
		out[name] = cloneStringAnyMap(values)
	}
	return out
}

func cloneStringAnyMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = cloneSemanticValue(value)
	}
	return out
}
