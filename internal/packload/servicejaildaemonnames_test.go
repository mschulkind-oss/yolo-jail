package packload

import "sort"

// ServiceJailDaemonNames is the name of every held pack service that declares a jail daemon,
// sorted: what HeldServices leaves a launch's payload to run, one name per service and only when
// the declaration holding it (the later one) declares a daemon.
//
// TEST-ONLY since its one production reader, `yolo check`'s prediction, moved to the launch's own
// composer (internal/launchservice's ServiceJailDaemons), which this package cannot import. It
// stays here so served_test.go and laterwins_test.go keep reading HeldServices' rule through
// the shape they were written against.
func ServiceJailDaemonNames(packs []*Pack) []string {
	var out []string
	held, _ := HeldServices(packs)
	for _, h := range held {
		if s := h.Service; s.JailDaemon != nil && len(s.JailDaemon.Cmd) > 0 {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}
