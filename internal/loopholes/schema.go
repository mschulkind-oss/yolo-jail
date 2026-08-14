package loopholes

// schema.go re-exports the manifest SCHEMA that now lives in
// internal/loopholedecl.
//
// Aliases and one-line delegations rather than new definitions, deliberately:
// there is ONE vocabulary for a transport value and ONE HostDaemon type, and the
// twenty-odd call sites across internal/cli that already spell them
// `loopholes.TransportLoopbackTLS` and `*loopholes.HostDaemon` are talking about
// the same things. Two definitions would be two things a manifest could disagree
// with — which is the failure the extraction exists to prevent, not to relocate.
//
// Which package to import: reach for `loopholedecl` when you only need to READ a
// manifest (the pack footprint, `pack lint`, a host-side validator — none of which
// may import this package: it depends on internal/config, which depends on
// internal/packload). Reach for this package when you need a RESOLVED loophole —
// paths substituted, requirements evaluated, runtime args emitted.

import "github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"

// Manifest field types (see internal/loopholedecl for the schema reference).
type (
	Intercept     = loopholedecl.Intercept
	JailDaemon    = loopholedecl.JailDaemon
	HostDaemon    = loopholedecl.HostDaemon
	HostBindMount = loopholedecl.HostBindMount
	Requires      = loopholedecl.Requires
	EnvMap        = loopholedecl.EnvMap
)

// Manifest enum values and the broker_ip default.
const (
	DefaultBrokerIP      = loopholedecl.DefaultBrokerIP
	TransportLoopbackTLS = loopholedecl.TransportLoopbackTLS
	TransportNone        = loopholedecl.TransportNone
	PublishesEndpoint    = loopholedecl.PublishesEndpoint
	PublishesSocket      = loopholedecl.PublishesSocket
	RequestEndFramed     = loopholedecl.RequestEndFramed
	RequestEndEOF        = loopholedecl.RequestEndEOF
)

// NewEnvMap returns an empty EnvMap.
func NewEnvMap() *EnvMap { return loopholedecl.NewEnvMap() }

// JailLoopholeDir returns the CONTAINER path where a loophole's module dir is
// bind-mounted (RuntimeArgsFor emits the -v). It is what {jail_loophole_dir}
// resolves to in jail_daemon.cmd — a separate token from the host-side
// {loophole_dir} on purpose: one token with two resolutions is the kind of
// asymmetry an author discovers by debugging.
func JailLoopholeDir(name string) string { return loopholedecl.JailLoopholeDir(name) }
