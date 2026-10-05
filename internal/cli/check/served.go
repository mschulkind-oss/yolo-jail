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
// and of each pack service's host half the launch applies (launchservice.AdmitDoorways,
// AdmitServiceHosts), declines the same ones the launch declines by name, and every gate it
// predicts refuses or withholds what that launch does. The bound loopholes (loopholes'
// JailBoundNames) are served by the one rule too: by name wherever the argv would bind them.
//
// No runtime named predicts a container one, which is what a launch with none named resolves
// to on every platform: macos-user is only ever chosen by name.
func (o *Options) predictedServed(merged *jsonx.OrderedMap, packs []*packload.Pack) packload.ServedDaemons {
	rt := configRuntime(merged)
	if o.Getenv != nil {
		rt = o.configuredRuntimeName(merged)
	}
	set := loopholes.NewHostSet(subMap(merged, "loopholes"))
	// The pack services' daemons from the launch's own composer (launchservice.ServiceJailDaemons),
	// so the one split below declines them where the launch does rather than by a second rule here.
	services := launchservice.ServiceJailDaemons(packs)
	// A DOORWAY'S HOST ARGV RUNS ONLY FROM A PACK YOLO SHIPS, and the launch clears every one it
	// refuses before anything reads its payload (launchservice.AdmitDoorways, the one admission
	// both readers apply): so a refused doorway is judged here as the jail daemon it becomes. A
	// SERVICE'S HOST HALF is admitted the same way (launchservice.AdmitServiceHosts), so one the
	// launch will not run is judged as the jail daemon the macos-user guest then runs.
	specs, _ := launchservice.AdmitDoorways(packs, set.JailDaemons(set.Enabled(), rt, services))
	specs, _ = launchservice.AdmitServiceHosts(packs, specs)
	// Each daemon at its DECLARED listen address: a prediction binds nothing, so it has no
	// port of a shared namespace's launch to know, and a pointer naming {listen} composes to
	// the address a private namespace serves (docs/plans/notch-convergence.md NC-D41).
	names, listen := loopholes.ServedJailDaemonNames(rt, specs)
	// The BOUND LOOPHOLES the launch's argv would bind, served by name as the launch's
	// servedDaemons serves them (docs/design/loophole-packaging.md LP-D1), and the macos-user
	// mark the launch sets, so a pointer at one is predicted withheld, in the launch's words.
	served := packload.ServedInJail(append(names, set.JailBoundNames(set.Enabled(), rt)...)).WithListen(listen)
	if rt == "macos-user" {
		served = served.MountsNothing()
	}
	return served
}
