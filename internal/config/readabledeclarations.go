package config

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ReadableDeclarationsForPacks projects explicit declarations from already loaded selected
// packs. Pack roots must remain leased for the duration of the call; this function never stages
// or resolves packs itself.
func ReadableDeclarationsForPacks(packs []*packload.Pack) ([]ReadableBrokerDeclaration, []ReadablePackDeclaration, error) {
	var brokers []ReadableBrokerDeclaration
	var settings []ReadablePackDeclaration
	seen := make(map[string]bool, len(packs))
	for _, pack := range packs {
		if pack == nil || pack.Decl == nil || pack.Name == "" {
			return nil, nil, fmt.Errorf("readable declarations require loaded named pack declarations")
		}
		if seen[pack.Name] {
			return nil, nil, fmt.Errorf("readable declarations contain pack %q more than once", pack.Name)
		}
		seen[pack.Name] = true
		modules, refusals, warnings, err := packload.ReadableLoopholeModules(pack)
		if err != nil {
			return nil, nil, err
		}
		if len(refusals) != 0 || len(warnings) != 0 {
			return nil, nil, fmt.Errorf("pack %s readable declarations: %v", pack.Name, append(refusals, warnings...))
		}
		for _, module := range modules {
			if module.Decl == nil {
				return nil, nil, fmt.Errorf("pack %s loophole %s has no loaded declaration", pack.Name, module.Name)
			}
			moduleSettings := cloneReadableSettingDecls(module.Decl.Settings)
			settings = append(settings, ReadablePackDeclaration{Pack: pack.Name, Settings: moduleSettings,
				Loophole: module.Name, Access: module.Decl.Access, Grants: append([]loopholedecl.Grant(nil), module.Decl.Grants...)})
			if module.Decl.Brokered != nil {
				brokers = append(brokers, readableBrokerDeclaration(pack.Name, module.Name, *module.Decl.Brokered, moduleSettings))
			}
		}
	}
	return brokers, settings, nil
}

func cloneReadableSettingDecls(settings []loopholedecl.Setting) []loopholedecl.Setting {
	if settings == nil {
		return nil
	}
	cloned := append([]loopholedecl.Setting(nil), settings...)
	for i := range cloned {
		if values, ok := settings[i].Default.([]string); ok && values != nil {
			cloned[i].Default = append(make([]string, 0, len(values)), values...)
		}
	}
	return cloned
}

// readableBrokerDeclaration projects only explicitly declared canonical metadata. Pending-only
// grant kinds are added to the detached broker value because ResolveReadableAllowance validates
// pending capabilities against its mapped-grant set; the source declaration remains untouched.
func readableBrokerDeclaration(pack, label string, brokered loopholedecl.Brokered, settings []loopholedecl.Setting) ReadableBrokerDeclaration {
	declaration := ReadableBrokerDeclaration{
		Pack:                pack,
		Label:               label,
		Brokered:            brokered,
		ReadCapability:      brokered.ReadCapability,
		KindCapabilities:    cloneStringMap(brokered.KindCapabilities),
		PendingCapabilities: append([]string(nil), brokered.PendingCapabilities...),
		Settings:            cloneReadableSettingDecls(settings),
	}
	declaration.Brokered.CredentialPaths = append([]string(nil), brokered.CredentialPaths...)
	declaration.Brokered.Grants = append([]string(nil), brokered.Grants...)
	declaration.Brokered.PendingCapabilities = append([]string(nil), brokered.PendingCapabilities...)
	declaration.Brokered.KindCapabilities = cloneStringMap(brokered.KindCapabilities)
	pending := make(map[string]bool, len(brokered.PendingCapabilities))
	for _, capability := range brokered.PendingCapabilities {
		pending[capability] = true
	}
	var pendingOnlyKinds []string
	for kind, capability := range brokered.KindCapabilities {
		if pending[capability] && !containsString(declaration.Brokered.Grants, kind) {
			pendingOnlyKinds = append(pendingOnlyKinds, kind)
		}
	}
	sort.Strings(pendingOnlyKinds)
	declaration.Brokered.Grants = append(declaration.Brokered.Grants, pendingOnlyKinds...)
	return declaration
}
