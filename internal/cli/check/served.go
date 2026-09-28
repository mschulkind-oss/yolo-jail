package check

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// predictedServed is what `yolo check` predicts the configured runtime's launch SERVES AT ITS
// NOTCH (packload.ServedDaemons; docs/plans/notch-convergence.md §4 item 2): the jail daemons
// that launch's payload would name — every enabled loophole's, through the loophole set's own
// composer, and every selected pack service's — on the runtime YOLO_RUNTIME or the config
// names, through the same packload.ServedAtRuntime the launch asks. So on macos-user the
// prediction serves nothing, and every gate it predicts refuses or withholds what that launch
// does.
//
// No runtime named predicts a container one, which is what a launch with none named resolves
// to on every platform: macos-user is only ever chosen by name.
func (o *Options) predictedServed(merged *jsonx.OrderedMap, packs []*packload.Pack) packload.ServedDaemons {
	rt := configRuntime(merged)
	if o.Getenv != nil {
		rt = o.configuredRuntimeName(merged)
	}
	set := loopholes.NewHostSet(subMap(merged, "loopholes"))
	var names []string
	for _, spec := range set.JailDaemons(set.Enabled(), rt, nil) {
		names = append(names, spec.Name)
	}
	names = append(names, packload.ServiceJailDaemonNames(packs)...)
	return packload.ServedAtRuntime(rt, names)
}
