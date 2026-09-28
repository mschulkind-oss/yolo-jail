package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// sharednetwork.go is the launch half of OQ-NC3 (docs/plans/notch-convergence.md, ruled
// 2026-09-28, A): a jail on a shared network namespace — `network.mode: "host"`, a nested podman
// forced onto the launcher's namespace, and every macos-user jail — keeps the autonomous posture,
// and the launch SAYS SO. The briefing says it to the agent from the same profile
// (jailcontent.LaunchProfile), in the same sentence (render.SharedNetworkFact).

// launchProfile is this launch's confinement profile with the network primitive set from
// sharesLauncherNetns, the one predicate the assembler, the advertised addresses and the
// briefing's network mode already read.
func (o *Options) launchProfile(cfg *jsonx.OrderedMap, rt string) render.Profile {
	notch, _ := render.KindForNotch(string(config.ResolveConfinement(cfg)))
	return jailcontent.LaunchProfile(notch, rt, o.IsMacOS,
		sharesLauncherNetns(rt, o.resolveNetMode(cfg), o.inContainer()))
}

// noteSharedNetwork prints the shared-network fact on a launch whose jail shares the host's
// network, and nothing on one whose loopback is its own (a bridged podman, Apple Container).
//
// ABOVE THE BACKEND DISPATCH, so the container arm, an attach and the macos-user arm all print it
// from one call site, the placement the launch-flag disclosure takes for the same reason
// (launchflagdisclosure.go). NOT SUPPRESSIBLE: a launch has no quiet mode (OQ-RO3), and this is a
// disclosure about what the agent can reach, not progress.
func (o *Options) noteSharedNetwork(cfg *jsonx.OrderedMap, rt string) {
	if fact := render.SharedNetworkFact(o.launchProfile(cfg, rt)); fact != "" {
		o.pr(o.Stderr).print("[yellow]" + fact + "[/yellow]")
	}
}
