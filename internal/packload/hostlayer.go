package packload

// hostlayer.go carries what the LAUNCHER decided about this launch's host layers across
// the container wall, so the jail's read can fail CLOSED.
//
// # Why the jail cannot decide this alone
//
// A surface with `readsHost` composes the user's own copy of its file, mounted under /ctx.
// When that path is not there, the jail is looking at two situations it cannot tell apart:
//
//   - the user has no such file — the overwhelmingly common case, and a perfectly good
//     one: the surface composes from its lower layers and nothing is wrong;
//   - the bytes were supposed to arrive and did not — a wrong mount path, a copy that
//     failed, a backend with no mounts at all — and the surface then composes a config
//     file that is missing the user's own settings, which looks exactly like a working one.
//
// The read was fail-open for that reason, and it cost a real bug: a pack named `my_pack`
// had its file mounted at /ctx/host-my_pack/ while the entrypoint read /ctx/host-my_5fpack/
// (2026-09-05), and the surface composed in silence while the launch banner still printed
// the grant. OQ-CO10 (docs/design/config-ownership-and-promotion.md) rules the read fails
// closed, which is only answerable with the one fact the launcher has and the jail does not:
// what it delivered.
//
// # This is YOLO_HOST_LOOPBACK's shape, on purpose
//
// Same three properties, for the same reasons (internal/cli/run/hostloopback.go, and the
// witness that reads it in internal/entrypoint/reachability.go):
//
//   - EMITTED ON EVERY LAUNCH by a launcher that has it, so an ABSENT variable means only
//     "launcher older than the variable" and never "nothing was delivered". The two halves
//     deploy on different cadences (AGENTS.md, "the two halves deploy on different
//     cadences"), so absence has to be survivable.
//   - SEVERITY IS THE DISPOSITION'S ALONE. `unsupported` and absent never escalate — a
//     launch that carried no host bytes is not refused for a delivery nobody attempted
//     (the reachability witness's OQ-R3, same sentence).
//   - THE JAIL IS A WITNESS, NOT A SECOND DECIDER. It compares what it was told against
//     what it finds; it does not re-derive the decision.

import (
	"encoding/json"
	"slices"
)

// HostLayerEnvVar names the launcher's report in the jail environment.
const HostLayerEnvVar = "YOLO_HOST_LAYERS"

// HostLayerReport is that report: whether this backend delivers host layers at all, and
// which /ctx destinations this launch actually put there.
type HostLayerReport struct {
	// Delivery is what THIS LAUNCH did: "supported" when it had a mechanism for carrying a
	// host file into the jail and used it, "unsupported" when it carried none and so
	// delivered nothing.
	//
	// ⚠ IT WAS A BACKEND CAPABILITY UNTIL 2026-09-13, and macos-user was the entire
	// reason: host bytes crossed on a /ctx mount, that backend has none, so its plan
	// builder reported "unsupported" unconditionally. DP-L1 gave it a mechanism — a
	// host-side COPY into a root-owned tree named to the jail by YOLO_CTX_ROOT — and the
	// premise went with it. It now answers "supported" the moment the host CLI composed a
	// tree and "unsupported" when it composed none (macosuser.hostLayerWire), so the value
	// is keyed on the delivery rather than on the platform. No backend reports
	// "unsupported" unconditionally any more; what still emits it is a caller that staged
	// no tree at all — the install capture is the shipped one.
	Delivery string `json:"delivery"`
	// Delivered lists the /ctx destinations the launcher delivered, as packload.CtxPath
	// computes them — the exact strings the entrypoint opens. A surface's destination
	// missing from a "supported" report means the user has no such file on the host, which
	// is a normal state and not a fault.
	Delivered []string `json:"delivered,omitempty"`
}

// The two Delivery values. A third would be a new fact about what a launch delivered, not
// a new severity.
const (
	HostLayersSupported   = "supported"
	HostLayersUnsupported = "unsupported"
)

// HostLayerDisposition is what the report says about ONE surface's host layer — the
// four-way answer the jail's read branches on.
type HostLayerDisposition string

const (
	// HostLayerDelivered: the launcher put this file at this path. If it is not readable
	// there, something between the two halves is broken — the one case that refuses.
	HostLayerDelivered HostLayerDisposition = "delivered"
	// HostLayerNoHostFile: the launch could have delivered it and there was nothing to
	// deliver. The user has not created the file. Composes without the host layer.
	HostLayerNoHostFile HostLayerDisposition = "no-host-file"
	// HostLayerUnsupported: this LAUNCH carried no host layers at all, so there is nothing
	// to have arrived. Composes without one and refuses nothing, which is OQ-R3's rule
	// unchanged: a delivery nobody attempted is not a delivery that failed.
	//
	// ⚠ NOBODY NAMES A BACKEND HERE ANY MORE, and the two printers this comment used to
	// send the reader to are the evidence rather than a casualty of it.
	// run.noteMacosUserHostByteGaps was NARROWED on 2026-09-13 to the one shape DP-L1 did
	// not deliver — a `host_files` entry whose source is a DIRECTORY — and lost its last
	// caller on 2026-10-05, when macos-user began copying that shape too; and
	// run.backendLimits had its host-byte paragraph ("your agent config files were
	// rendered from DEFAULTS, not from the human's own") DELETED outright. What retired
	// both texts is that macos-user now delivers host bytes by copy, so neither the human
	// nor the agent can still be told the backend carries none.
	HostLayerUnsupported HostLayerDisposition = "unsupported"
	// HostLayerUnknown: no report in the environment — a launcher older than this variable.
	// Composes without refusing, which is exactly the behaviour that shipped before it.
	HostLayerUnknown HostLayerDisposition = "unknown"
)

// Marshal renders the report for the environment. Deterministic: Delivered is in the
// launcher's own emission order and the field set is fixed.
func (r HostLayerReport) Marshal() (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// HostLayersUnsupportedWire is the entire report a launch that carried no host bytes
// emits. A function rather than a literal so the wire has ONE producer, and error-free
// because a two-field struct of a string and a nil slice cannot fail to marshal.
func HostLayersUnsupportedWire() string {
	wire, _ := HostLayerReport{Delivery: HostLayersUnsupported}.Marshal()
	return wire
}

// ParseHostLayerReport reads the report out of an environment value.
//
// An empty or unparseable value yields ok=false, which every caller must read as UNKNOWN
// rather than as "nothing was delivered". A jail that treated a garbled variable as an
// empty delivery list would refuse every host layer on the machine, which is the opposite
// of the conservative answer.
func ParseHostLayerReport(wire string) (HostLayerReport, bool) {
	if wire == "" {
		return HostLayerReport{}, false
	}
	var r HostLayerReport
	if err := json.Unmarshal([]byte(wire), &r); err != nil || r.Delivery == "" {
		return HostLayerReport{}, false
	}
	return r, true
}

// DispositionFor answers for one surface's /ctx destination.
func (r HostLayerReport) DispositionFor(ctxPath string) HostLayerDisposition {
	if r.Delivery == HostLayersUnsupported {
		return HostLayerUnsupported
	}
	if slices.Contains(r.Delivered, ctxPath) {
		return HostLayerDelivered
	}
	return HostLayerNoHostFile
}
