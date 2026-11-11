package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokeridentity"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type explicitPackLoopholeResolver struct{ packs []*packload.Pack }

// SelectedPackLoopholeResolver returns a config-only resolver over declarations already read for
// the user selection. It does not discover live services or execute their health checks.
func SelectedPackLoopholeResolver(packs []*packload.Pack) LoopholeResolver {
	return explicitPackLoopholeResolver{packs: packs}
}

func (r explicitPackLoopholeResolver) Known() (map[string]LoopholeInfo, bool) {
	known := map[string]LoopholeInfo{}
	for _, pack := range r.packs {
		modules, _, _, err := packload.ReadableLoopholeModules(pack)
		if err != nil {
			return nil, false
		}
		for _, module := range modules {
			info := LoopholeInfo{Name: module.Name}
			if module.Decl != nil {
				info.HasHostDaemon = module.Decl.HostDaemon != nil
				info.CallBroker = module.Decl.CallBroker
				if info.CallBroker != nil {
					identities, err := callbrokeridentity.Resolve(pack.Root, pack.Name, module.Dir, info.CallBroker)
					if err != nil {
						info.CallBroker.ProgramIdentityError = err.Error()
						info.CallBroker.ProgramIdentityPending = callbrokeridentity.IsStagedPackRootUnavailable(err)
					} else {
						info.CallBroker.ProgramIdentities = identities
					}
				}
				if info.HasHostDaemon || info.CallBroker != nil {
					info.StateDir = paths.LoopholeStateDir(module.Name)
				}
				info.Settings = module.Decl.Settings
				info.Brokered = module.Decl.Brokered
				info.Grants = module.Decl.Grants
			}
			known[module.Name] = info
		}
	}
	return known, true
}
