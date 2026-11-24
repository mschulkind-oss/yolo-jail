package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"slices"
)

func cloneDeliveredAsks(asks map[string]brokerscope.DeliveredAsks) map[string]brokerscope.DeliveredAsks {
	if asks == nil {
		return nil
	}
	clone := make(map[string]brokerscope.DeliveredAsks, len(asks))
	for key, value := range asks {
		value.Requests = slices.Clone(value.Requests)
		value.Scopes = slices.Clone(value.Scopes)
		value.PackRead = slices.Clone(value.PackRead)
		clone[key] = value
	}
	return clone
}

func (o *Options) captureCallBrokerDelivered(merged, workspace *jsonx.OrderedMap) error {
	o.callBrokerDelivered = map[string]brokerscope.DeliveredAsks{}
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(merged)).All() {
		if lp.CallBroker == nil {
			continue
		}
		model := "legacy/1"
		if config.PolicyModelApproved(o.Workspace, lp.CallBroker.Source) {
			model = "scoped/1"
		}
		asks, err := callbrokerinput.CaptureDelivered(lp.CallBroker.Source, lp.Pack, model, workspace)
		if err != nil {
			return err
		}
		o.callBrokerDelivered[lp.Name] = asks
	}
	return nil
}
