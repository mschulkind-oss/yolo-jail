package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Architecture and invariants: docs/reference/host-import-exclusions.md

const hostImportExclusionsKey = "host_import_exclusions"

// LoadHostImportExclusions strictly reads the user-scope exclusions, including config
// includes and inherited user snapshots. It never reads merged workspace config.
func LoadHostImportExclusions() (map[string][]string, error) {
	userConfig, err := UserScopeConfig(true, func(string) {})
	if err != nil {
		return nil, err
	}
	policy, problems := checkHostImportExclusions(userConfig)
	if len(problems) != 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	if lenHostImportExclusions(policy) != 0 {
		if mode, _ := hostManagementValue(userConfig); mode == HostManagementOwn {
			return nil, fmt.Errorf("%s", hostImportOwnershipRefusal(policy))
		}
	}
	return policy, nil
}

// checkHostImportExclusions normalizes one already-loaded config map. ValidateConfig uses
// this against its effective map; the production reader above supplies only user scope.
func checkHostImportExclusions(config *jsonx.OrderedMap) (map[string][]string, []string) {
	if config == nil {
		return nil, nil
	}
	value, present := config.Get(hostImportExclusionsKey)
	if !present {
		return nil, nil
	}
	entries, ok := asMap(value)
	if !ok {
		return nil, []string{"config." + hostImportExclusionsKey + ": expected an object mapping surface addresses to JSON Pointer lists"}
	}

	capabilities := shippedHostImportSurfaces()
	addresses := append([]string(nil), entries.Keys()...)
	sort.Strings(addresses)
	policy := make(map[string][]string, len(addresses))
	var problems []string
	for _, address := range addresses {
		path := "config." + hostImportExclusionsKey + "." + address
		if !validSurfaceAddress(address) {
			problems = append(problems, path+": expected a surface address spelled agent/name")
			continue
		}
		capable, known := capabilities[address]
		if !known {
			problems = append(problems, path+": unknown shipped config surface")
			continue
		}
		if !capable {
			problems = append(problems, path+": this surface cannot receive an object host layer (it must declare readsHost and use an object codec)")
			continue
		}

		raw, _ := entries.Get(address)
		items, ok := asList(raw)
		if !ok {
			problems = append(problems, path+": expected a list of JSON Pointer strings")
			continue
		}
		pointers := make([]string, 0, len(items))
		for i, item := range items {
			pointer, ok := asStr(item)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s[%d]: expected a JSON Pointer string", path, i))
				continue
			}
			tokens, err := jsonptr.Parse(pointer)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s[%d]: %v", path, i, err))
				continue
			}
			if len(tokens) == 0 {
				problems = append(problems, fmt.Sprintf("%s[%d]: the root pointer is not allowed; name an object member", path, i))
				continue
			}
			pointers = append(pointers, pointer)
		}
		sort.Strings(pointers)
		policy[address] = uniqueStrings(pointers)
	}
	return policy, problems
}

func shippedHostImportSurfaces() map[string]bool {
	out := make(map[string]bool)
	for _, surface := range agentcfg.BuiltinManifest().Surfaces() {
		address := surface.Agent + "/" + surface.Name
		codecImpl, codecKnown := codec.LookupCodec(surface.Codec)
		out[address] = surface.HasHostLayer() && codecKnown && codecImpl != nil && surface.Kind() == codec.KindObject
	}
	for _, pack := range embeddedPackDeclarationsFromFS(packload.EmbeddedFS()) {
		surfaces, _ := pack.Surfaces()
		for _, surface := range surfaces {
			address := surface.Agent + "/" + surface.Name
			codecImpl, codecKnown := codec.LookupCodec(surface.Codec)
			capable := surface.HasHostLayer() && codecKnown && codecImpl != nil && surface.Kind() == codec.KindObject
			if previous, exists := out[address]; !exists || capable {
				out[address] = capable || previous
			}
		}
	}
	return out
}

func validSurfaceAddress(address string) bool {
	agent, name, ok := strings.Cut(address, "/")
	return ok && agent != "" && name != "" && !strings.Contains(name, "/")
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func lenHostImportExclusions(policy map[string][]string) int {
	n := 0
	for _, pointers := range policy {
		n += len(pointers)
	}
	return n
}

func hostImportOwnershipRefusal(policy map[string][]string) string {
	addresses := make([]string, 0, len(policy))
	for address, pointers := range policy {
		if len(pointers) != 0 {
			addresses = append(addresses, address)
		}
	}
	sort.Strings(addresses)
	var affected []string
	for _, address := range addresses {
		affected = append(affected, address+" "+fmt.Sprintf("%q", policy[address]))
	}
	return "config." + hostImportExclusionsKey + ": non-empty exclusions " + strings.Join(affected, ", ") +
		" cannot be used with host_management: own; host-owned values must use the pack contribution's existing host-versus-jail targeting, or remove the exclusions"
}

func validateHostImportExclusions(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	policy, problems := checkHostImportExclusions(config)
	for _, problem := range problems {
		add(errs, problem)
	}
	if len(problems) == 0 && lenHostImportExclusions(policy) != 0 {
		if mode, _ := hostManagementValue(config); mode == HostManagementOwn {
			add(errs, hostImportOwnershipRefusal(policy))
		}
	}
	workspaceConfig, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || workspaceConfig == nil {
		return
	}
	if _, declared := workspaceConfig.Get(hostImportExclusionsKey); declared && WorkspaceUserScopeOnlyKey(hostImportExclusionsKey) {
		add(errs, workspaceHostImportExclusionsRefusal())
	}
}

func workspaceHostImportExclusionsRefusal() string {
	return "config." + hostImportExclusionsKey + ": user-scope only — exclusions filter user host config imports, so declare them in " +
		paths.UserConfigDisplayPath() + " and remove this workspace declaration"
}

func hostImportExclusionsOrderedMap(policy map[string][]string) *jsonx.OrderedMap {
	if policy == nil {
		return nil
	}
	out := jsonx.NewOrderedMap()
	keys := make([]string, 0, len(policy))
	for key := range policy {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := make([]any, len(policy[key]))
		for i, pointer := range policy[key] {
			values[i] = pointer
		}
		out.Set(key, values)
	}
	return out
}
