package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type explicitPackLoopholeResolver struct{ packs []*packload.Pack }

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
				if info.HasHostDaemon {
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
