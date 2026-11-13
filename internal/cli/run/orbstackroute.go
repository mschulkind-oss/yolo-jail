package run

// orbstackroute.go is component C of docs/research/orbstack-as-a-podman-host-plan.md: the
// HOST-SERVICE ROUTE of a jail whose Podman endpoint is an OrbStack Linux machine.
//
// On that endpoint the jail cannot reach the Mac's loopback through host.containers.internal:
// that name is the OrbStack machine's own gateway, and pasta's --map-host-loopback reaches the
// MACHINE's loopback, not the Mac's (MEASURED, docs/research/orbstack-as-a-podman-host.md §3.2).
// OrbStack gives the Mac's loopback its own name inside its VM, host.orb.internal, and a jail
// whose daemons advertised it reached all three of its host services (same section). So on a
// POSITIVELY identified OrbStack endpoint, yolo advertises that name and tells the jail
// `requested`: yolo took the step that routes the jail to the services, so an unreachable one is
// a fault the in-jail witness may report as one.
//
// The identification is the same `podman info` answer the macOS patient one-shot already got
// (podmanready.go), never a second probe: the selected connection (CONTAINER_CONNECTION, or
// podman's default) answered rootless AND named a kernel built by OrbStack. Anything else —
// no answer, a rootful answer, any other kernel — keeps today's behavior: no route, the
// gateway default, and `unknown` on the wire. That is the file-wide rule of hostloopback.go:
// the worst case is today's behavior.
//
// THE ADVERTISED NAME AND THE DISPOSITION COME FROM ONE PREDICATE (orbStackRoutes), read by
// the assembler through hostLoopbackFactsFor and by the loophole runtime through
// advertiseHostFor, the same pairing sharesLauncherNetns keeps for the shared namespace. The
// keeper starts the daemons in a fresh Options, so the fact crosses the keeper's plan
// (keeperPlan.OrbStack).

import (
	"strings"
)

// orbStackHostName is the name OrbStack resolves, inside its VM, to the Mac's loopback.
const orbStackHostName = "host.orb.internal"

// orbStackKernelMarker is what OrbStack puts in the kernel release of its VM, the one its
// machines share (`6.x.y-orbstack-…`, MEASURED in the research's trial). It is the positive
// fact: Podman Machine, a remote Linux host and a nested jail all report other kernels.
const orbStackKernelMarker = "-orbstack"

// isOrbStackPodman reports whether a parsed `podman info` answer names a rootless OrbStack
// endpoint. Both facts are positive: a missing security block is not rootless, and a missing
// kernel is not OrbStack's.
func isOrbStackPodman(info podmanInfo) bool {
	rootless := info.Host.Security.Rootless
	return rootless != nil && *rootless && strings.Contains(info.Host.Kernel, orbStackKernelMarker)
}

// acceptMachineFacts reads the macOS patient one-shot's answer for the one fact a macOS
// launch takes from it: whether the selected endpoint is OrbStack's. Everything else in that
// answer stays unread on macOS, as podmanready.go says.
func (o *Options) acceptMachineFacts(info string) {
	parsed, ok := parsePodmanInfo(info)
	o.orbStackPodman = ok && isOrbStackPodman(parsed)
}

// orbStackRoutes is THE predicate: this launch routes its jail to the Mac's host services
// through host.orb.internal. Podman, on an endpoint identified as OrbStack's, on the default
// bridge (an explicit mode is the user's, OQ-R1 in hostloopback.go), and not under the opt-out
// that turns every host-loopback step off.
func (o *Options) orbStackRoutes(rt, netMode string) bool {
	return rt == "podman" && o.orbStackPodman && netMode == "bridge" && // parity: NotApplicable — OrbStack is a Podman endpoint; no other backend has one to route through

		o.Getenv(hostLoopbackOptOutEnv) == ""
}
