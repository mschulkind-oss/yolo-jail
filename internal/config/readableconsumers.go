package config

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// readableconsumers.go holds the ONE reader for each setting the readable config layout
// (docs/reference/config/rulings.md) moved, so every backend asks the same function. The loaded
// config carries the readable spelling (`machine.resources`, `install.mise`, `install.nix`);
// each reader falls back to the legacy top-level spelling only for callers that hand it an
// unprojected legacy map directly. The rename table these follow is RenameDescriptors.

// MachineValue reads the readable `machine.<key>` setting, falling back to the legacy
// `<legacySection>.<legacyKey>` (or the top-level `<legacyKey>` when legacySection is empty).
func MachineValue(cfg *jsonx.OrderedMap, key, legacySection, legacyKey string) (any, bool) {
	if cfg == nil {
		return nil, false
	}
	if machine := sectionMap(cfg, "machine"); machine != nil {
		if value, ok := machine.Get(key); ok {
			return value, true
		}
	}
	section := cfg
	if legacySection != "" {
		section = sectionMap(cfg, legacySection)
	}
	if section == nil {
		return nil, false
	}
	return section.Get(legacyKey)
}

// Resources is the declared `machine.resources` table (legacy: top-level `resources`), or nil
// when none is declared or it is not a table.
func Resources(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	value, present := MachineValue(cfg, "resources", "", "resources")
	if !present {
		return nil
	}
	resources, _ := value.(*jsonx.OrderedMap)
	return resources
}

// OwnMiseTools is the config's own `install.mise` table (legacy: top-level `mise_tools`),
// without yolo's defaults, or nil when none is declared or it is not a table. A present
// `install.mise` wins even when it is not a table, so a malformed new spelling never resurrects
// an old one beside it.
func OwnMiseTools(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	if install := sectionMap(cfg, "install"); install != nil {
		if value, present := install.Get("mise"); present {
			tools, _ := value.(*jsonx.OrderedMap)
			return tools
		}
	}
	return sectionMap(cfg, "mise_tools")
}

// OwnNixPackages is the config's own `install.nix` list (legacy: top-level `packages`), before
// any platform filtering, or nil when none is declared or it is falsy.
func OwnNixPackages(cfg *jsonx.OrderedMap) []any {
	if cfg == nil {
		return nil
	}
	if install := sectionMap(cfg, "install"); install != nil {
		if _, present := install.Get("nix"); present {
			return getListOrNilFalsy(install, "nix")
		}
	}
	return getListOrNilFalsy(cfg, "packages")
}

func sectionMap(cfg *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	value, ok := cfg.Get(key)
	if !ok {
		return nil
	}
	section, _ := value.(*jsonx.OrderedMap)
	return section
}
