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
//     backend that cannot carry host bytes is not refused for what it cannot do (the
//     reachability witness's OQ-R3, same sentence).
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
	// Delivery is the BACKEND's capability: "supported" when this launch can carry a host
	// file into the jail, "unsupported" when the backend has no mechanism for it at all
	// (macos-user, which has no bind mounts and no /ctx).
	Delivery string `json:"delivery"`
	// Delivered lists the /ctx destinations the launcher delivered, as packload.CtxPath
	// computes them — the exact strings the entrypoint opens. A surface's destination
	// missing from a "supported" report means the user has no such file on the host, which
	// is a normal state and not a fault.
	Delivered []string `json:"delivered,omitempty"`
}

// The two Delivery values. A third would be a new fact about a backend, not a new severity.
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
	// HostLayerUnsupported: this backend carries no host layers at all. Composes without
	// one, and the launch says so by name on the host side (run.noteMacosUserHostByteGaps)
	// and in the agent's own briefing (run.backendLimits).
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

// HostLayersUnsupportedWire is the entire report a backend with no delivery mechanism
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
