package config

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const resourceGroupsKey = "resource_groups"

var resourceGroupUnitName = regexp.MustCompile(`^[A-Za-z0-9_.@:-]+$`)

type ResourceGroupSelection struct {
	Unit                 string
	Source               string
	AgentPercent         int
	AgentPercentExplicit bool
}

type resourceGroupConfig struct {
	Default              string
	Maintenance          string
	AgentPercent         int
	AgentPercentExplicit bool
	Workspaces           map[string]string
	Projects             map[string]string
}

func resourceGroupsWorkspaceRefusal(fileScope string) string {
	return "user-scope only; first remove the `resource_groups` table from " + fileScope +
		", then retry the command. After the workspace config graph is repaired, if you independently choose an owner-level mapping, set it on the host using `yolo config set resource_groups.default '\"example.slice\"' --user` (example only; replace it with your independently chosen unit; do not copy a project value)."
}

func resourceGroupsWorkspaceFileScope(label, via string) string {
	if via != "" {
		return "this included file"
	}
	if filepath.Base(label) == WorkspaceLocalTOMLConfigName || filepath.Base(label) == WorkspaceLocalLegacyConfigName {
		return "this local file"
	}
	return "this project file"
}

// ValidateResourceGroups checks the owner-only selection table without consulting the host
// manager, controllers, devices, or group state.
func ValidateResourceGroups(cfg *jsonx.OrderedMap) []string {
	_, errs := parseResourceGroupConfig(cfg)
	return errs
}

// ResourceGroupMaySelect reports whether owner config contains a workspace selection source.
// It is a cheap local check used to keep the ordinary host path from resolving its cwd for this
// feature when no mapping could apply.
func ResourceGroupMaySelect(cfg *jsonx.OrderedMap) bool {
	if cfg == nil {
		return false
	}
	value, present := cfg.Get(resourceGroupsKey)
	if !present || value == nil {
		return false
	}
	table, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return true
	}
	for _, key := range []string{"default", "workspaces", "projects"} {
		if v, exists := table.Get(key); exists {
			if key == "default" {
				return true
			}
			mapping, ok := v.(*jsonx.OrderedMap)
			if !ok || mapping.Len() > 0 {
				return true
			}
		}
	}
	return false
}

// SelectResourceGroup resolves a workspace mapping in specificity order. It performs local
// path canonicalization only; it never queries a resource manager or applies policy.
func SelectResourceGroup(cfg *jsonx.OrderedMap, workspace string) (ResourceGroupSelection, error) {
	rg, errs := parseResourceGroupConfig(cfg)
	if len(errs) != 0 {
		return ResourceGroupSelection{}, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	if len(rg.Workspaces) == 0 && len(rg.Projects) == 0 && rg.Default == "" {
		return ResourceGroupSelection{}, nil
	}
	canonical, err := resolveResourcePath(workspace)
	if err != nil {
		return ResourceGroupSelection{}, fmt.Errorf("cannot resolve workspace path: %w", err)
	}
	if unit, ok := rg.Workspaces[canonical]; ok {
		return ResourceGroupSelection{Unit: unit, Source: "workspace", AgentPercent: rg.AgentPercent, AgentPercentExplicit: rg.AgentPercentExplicit}, nil
	}
	bestRoot, bestUnit := "", ""
	for root, unit := range rg.Projects {
		if resourcePathWithin(canonical, root) && len(root) > len(bestRoot) {
			bestRoot, bestUnit = root, unit
		}
	}
	if bestRoot != "" {
		return ResourceGroupSelection{Unit: bestUnit, Source: "project " + bestRoot, AgentPercent: rg.AgentPercent, AgentPercentExplicit: rg.AgentPercentExplicit}, nil
	}
	if rg.Default != "" {
		return ResourceGroupSelection{Unit: rg.Default, Source: "default", AgentPercent: rg.AgentPercent, AgentPercentExplicit: rg.AgentPercentExplicit}, nil
	}
	return ResourceGroupSelection{}, nil
}

func parseResourceGroupConfig(cfg *jsonx.OrderedMap) (resourceGroupConfig, []string) {
	out := resourceGroupConfig{AgentPercent: 5, Workspaces: map[string]string{}, Projects: map[string]string{}}
	value, present := cfg.Get(resourceGroupsKey)
	if !present {
		return out, nil
	}
	if value == nil {
		return out, []string{"resource_groups must be a table"}
	}
	table, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return out, []string{"resource_groups must be a table"}
	}
	var errs []string
	reportUnknownKeys(table, knownResourceGroupsKeys, "resource_groups", &errs)
	if v, exists := table.Get("default"); exists {
		out.Default = parseResourceGroupUnit("resource_groups.default", v, &errs)
	}
	if v, exists := table.Get("maintenance"); exists {
		out.Maintenance = parseResourceGroupUnit("resource_groups.maintenance", v, &errs)
	}
	if v, exists := table.Get("agent_percent"); exists {
		percent, valid := resourceGroupPercent(v)
		if !valid || percent < 1 || percent > 99 {
			errs = append(errs, "resource_groups.agent_percent must be an integer from 1 through 99")
		} else {
			out.AgentPercent = percent
			out.AgentPercentExplicit = true
		}
	}
	for _, entry := range []struct {
		name string
		into map[string]string
	}{{"workspaces", out.Workspaces}, {"projects", out.Projects}} {
		v, exists := table.Get(entry.name)
		if !exists {
			continue
		}
		m, ok := v.(*jsonx.OrderedMap)
		if !ok {
			errs = append(errs, "resource_groups."+entry.name+" must be a path-to-unit table")
			continue
		}
		seen := map[string]string{}
		for _, rawPath := range m.Keys() {
			unit := parseResourceGroupUnit("resource_groups."+entry.name+"."+rawPath, mustGet(m, rawPath), &errs)
			canonical, err := canonicalResourcePath(rawPath)
			if err != nil {
				errs = append(errs, fmt.Sprintf("resource_groups.%s path %q must name an existing canonical absolute path: %v", entry.name, rawPath, err))
				continue
			}
			if previous, found := seen[canonical]; found {
				if previous != unit {
					errs = append(errs, fmt.Sprintf("resource_groups.%s has conflicting aliases for canonical path %q", entry.name, canonical))
				} else {
					errs = append(errs, fmt.Sprintf("resource_groups.%s repeats canonical path %q through an alias", entry.name, canonical))
				}
				continue
			}
			seen[canonical] = unit
			entry.into[canonical] = unit
		}
	}
	return out, errs
}

func mustGet(m *jsonx.OrderedMap, key string) any {
	v, _ := m.Get(key)
	return v
}

func parseResourceGroupUnit(key string, value any, errs *[]string) string {
	unit, ok := value.(string)
	if !ok || unit == "" || strings.TrimSpace(unit) != unit || !resourceGroupUnitName.MatchString(unit) {
		*errs = append(*errs, key+" must be a non-empty owner unit name without whitespace or path separators")
		return ""
	}
	return unit
}

func resourceGroupPercent(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		if n > math.MaxInt || n < math.MinInt {
			return 0, false
		}
		return int(n), true
	case float64:
		return 0, false
	default:
		typ := reflect.TypeOf(value)
		if typ != nil && typ.Name() == "jsonInt" {
			parsed, err := strconv.Atoi(reflect.ValueOf(value).String())
			return parsed, err == nil
		}
		return 0, false
	}
}

func canonicalResourcePath(path string) (string, error) {
	resolved, err := resolveResourcePath(path)
	if err != nil {
		return "", err
	}
	if resolved != path {
		return "", fmt.Errorf("path resolves to %q; write its canonical path", resolved)
	}
	return resolved, nil
}

func resolveResourcePath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("path must be absolute and canonical")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func resourcePathWithin(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, strings.TrimRight(root, string(filepath.Separator))+string(filepath.Separator))
}
