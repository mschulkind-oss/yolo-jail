package callbroker

import (
	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// neverReadVerdict refuses a read-only call whose read the host user wrote in `never` (design 3.7:
// `read` and `read.<cell>`, at the source or in any scope of it). The live broker has no scoped
// decision engine yet (plan U3), so a scoped never refuses the read for the whole source: it may
// over-refuse and never runs. The host user config is re-read per call, so a never added while a
// jail runs applies before its next read (design section 8). Unreadable config refuses.
func neverReadVerdict(source string, call Classification) (Verdict, bool) {
	if len(call.Writes) != 0 {
		return Verdict{}, false
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil || user == nil {
		return Verdict{Verdict: "refused", Rule: "host user allowance is unreadable", Next: "Run `yolo check` on the host, fix the config it names, and retry"}, true
	}
	cells := []string{""}
	for _, read := range call.ReadDescriptors {
		cells = append(cells, read.Cell)
	}
	for _, op := range call.Operations {
		if op.Kind == brokerpolicy.Read {
			cells = append(cells, op.Cell)
		}
	}
	for _, cell := range cells {
		if config.CallBrokerReadNeverRefuses(user, source, cell) {
			return Verdict{Verdict: "refused", Rule: "the host config says never for this read",
				Next: "Remove the `never` that names this read with `yolo config unset`, or ask the human to; a scoped never applies to the whole source until scoped reads are enforced"}, true
		}
	}
	return Verdict{}, false
}

// readNeverNamed reports whether the host user config refuses a read of this cell by a
// never, at the source or on the cell. An unreadable config refuses.
func readNeverNamed(source, cell string) bool {
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil || user == nil {
		return true
	}
	return config.CallBrokerReadNeverRefuses(user, source, "") || cell != "" && config.CallBrokerReadNeverRefuses(user, source, cell)
}
