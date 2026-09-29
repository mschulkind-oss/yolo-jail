package check

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// predictedServed is what `yolo check` predicts the configured runtime's launch SERVES AT ITS
// NOTCH (packload.ServedDaemons; docs/plans/notch-convergence.md §4 item 2): the jail daemons
// that launch's payload would name — every enabled loophole's, through the loophole set's own
// composer, and every selected pack service's — narrowed to the ones a jail on the runtime
// YOLO_RUNTIME or the config names actually SERVES, through the same
// loopholes.ServedJailDaemonNames the launch's servedDaemons asks. So on macos-user the
// prediction serves the daemons its sandbox runs (OQ-DP8/OQ-DP9) and the doorways the launch
// opens outside it (host-notch-services.md HS-D15), after the same admission of their host argvs
// the launch applies (launchservice.AdmitDoorways), declines the same ones the launch declines
// by name, and every gate it predicts refuses or withholds what that launch does.
//
// No runtime named predicts a container one, which is what a launch with none named resolves
// to on every platform: macos-user is only ever chosen by name.
func (o *Options) predictedServed(merged *jsonx.OrderedMap, packs []*packload.Pack) packload.ServedDaemons {
	rt := configRuntime(merged)
	if o.Getenv != nil {
		rt = o.configuredRuntimeName(merged)
	}
	set := loopholes.NewHostSet(subMap(merged, "loopholes"))
	// The pack services' daemons as specs of their own shape (Service), so the one split below
	// declines them where the launch does rather than by a second rule here.
	var services []loopholes.JailDaemonSpec
	for _, name := range packload.ServiceJailDaemonNames(packs) {
		services = append(services, loopholes.JailDaemonSpec{Name: name, Service: true})
	}
	// A DOORWAY'S HOST ARGV RUNS ONLY FROM A PACK YOLO SHIPS, and the launch clears every one it
	// refuses before anything reads its payload (launchservice.AdmitDoorways, the one admission
	// both readers apply): so a refused doorway is judged here as the jail daemon it becomes.
	specs, _ := launchservice.AdmitDoorways(packs, set.JailDaemons(set.Enabled(), rt, services))
	// Each daemon at its DECLARED listen address: a prediction binds nothing, so it has no
	// port of a shared namespace's launch to know, and a pointer naming {listen} composes to
	// the address a private namespace serves (docs/plans/notch-convergence.md NC-D41).
	names, listen := loopholes.ServedJailDaemonNames(rt, specs)
	return packload.ServedInJail(names).WithListen(listen)
}
