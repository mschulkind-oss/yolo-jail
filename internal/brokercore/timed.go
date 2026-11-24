package brokercore

import (
	"fmt"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// SelectedTimedLimit loads only cap metadata. Selection is not approval
// authority: the store still requires its source-specific live revalidator.
func SelectedTimedLimit(source string, q brokerstore.Request) (brokerstore.TimedBound, error) {
	packs, complete := config.UserScopeSelectedPacks()
	defer packload.ReleaseEmbedded()
	if !complete {
		return brokerstore.TimedBound{}, fmt.Errorf("selected source %s is unavailable; run yolo check and retry", source)
	}
	var selected *loopholedecl.Manifest
	for _, pack := range packs {
		modules, refusals, warnings, err := packload.ReadableLoopholeModules(pack)
		if err != nil || len(refusals)+len(warnings) != 0 {
			return brokerstore.TimedBound{}, fmt.Errorf("cannot read current selected declarations; run yolo check and retry")
		}
		for _, module := range modules {
			if module.Decl == nil {
				continue
			}
			matches := module.Decl.CallBroker != nil && module.Decl.CallBroker.Source == source || module.Decl.Brokered != nil && module.Decl.Brokered.Source == source
			if !matches {
				continue
			}
			if selected != nil {
				return brokerstore.TimedBound{}, fmt.Errorf("source %s has multiple selected declarations; repair the selected packs and retry", source)
			}
			selected = module.Decl
		}
	}
	if selected == nil {
		return brokerstore.TimedBound{}, fmt.Errorf("source %s is not selected; use yolo pack add and retry", source)
	}
	if selected.Brokered != nil {
		cap := time.Duration(0)
		if selected.Brokered.ReadMaxFor != "" {
			var err error
			cap, err = time.ParseDuration(selected.Brokered.ReadMaxFor)
			if err != nil || cap <= 0 {
				return brokerstore.TimedBound{}, fmt.Errorf("selected read_max_for is invalid; repair the pack and retry")
			}
		}
		return brokerstore.RoleTimedLimit(q, nil, true, cap, q.Policy.MaxWindow)
	}
	var cells []brokerstore.TimedCell
	for _, cell := range selected.CallBroker.Cells {
		kind := cell.Kind
		if kind == "" {
			kind = "write"
		}
		cap := time.Duration(0)
		if cell.MaxFor != "" {
			var err error
			cap, err = time.ParseDuration(cell.MaxFor)
			if err != nil || cap <= 0 {
				return brokerstore.TimedBound{}, fmt.Errorf("cell %s has an invalid max_for; repair the selected pack and retry", cell.Name)
			}
		}
		cells = append(cells, brokerstore.TimedCell{Name: cell.Name, Kind: kind, Maximum: cap})
	}
	return brokerstore.RoleTimedLimit(q, cells, false, 0, 0)
}
