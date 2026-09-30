package loopholes

// brokered.go is the one predicate for "which brokered loopholes will this launch start"
// (loopholedecl/brokered.go; docs/design/boundary-broker.md BB-D30). The config-change
// gate reads the repository scope of exactly these, `yolo check --accept-config-changes`
// records it for exactly these, and the spawn writes a scope file for exactly these, so
// the three cannot disagree about whether a broker is in play: a workspace whose launches
// never start one never gains a scope part and is never asked about one.

// BrokeredToStart returns the enabled, active, gate-admitted loopholes that declare a
// `brokered` block and a host daemon, among the names allow admits — allow being the
// backend's filter, the same one the spawn applies.
func (s Set) BrokeredToStart(allow func(name string) bool) []*Loophole {
	var out []*Loophole
	for _, lp := range s.Enabled() {
		if lp.Brokered == nil || lp.HostDaemon == nil {
			continue
		}
		if allow != nil && !allow(lp.Name) {
			continue
		}
		if !lp.Active() || !s.MayRunHostCode(lp) {
			continue
		}
		// A broker whose host build is missing here is one the spawn leaves out
		// (manifestHostDaemonSpecs, the same gate), so it is not one that will start.
		if _, unready := lp.hostBinariesUnready(); unready {
			continue
		}
		out = append(out, lp)
	}
	return out
}
