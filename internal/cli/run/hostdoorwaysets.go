package run

// hostdoorwaysets.go answers two questions about a pack set's loopholes for `yolo host apply`'s
// notch line (internal/cli's hostapplynotch.go), which plans no launch and so cannot ask
// PlanHostDoorways: which loopholes' doorways a `yolo host --` launch opens, and which
// loopholes of the user's own config are inline ones (a `loopholes.<name>` entry with a
// `command` and no manifest), whose only client is a jail.
//
// THE DOORWAYS ARE PlanHostDoorways' OWN FILTER, re-run here rather than read from it because
// hostdoorways.go plans a launch (it reserves ports and mints tokens) and the apply plans none:
// the set's jail daemons at the host runtime, their host argv admitted by
// launchservice.AdmitDoorways, kept by loopholes.Doorways. TestHostDoorwayLoopholesIsThePlansOwnSet
// pins the two to one answer, so the notch line and the launch cannot disagree about which
// loophole the host notch delivers.
//
// WITH EVERY SELECTED PACK'S LOOPHOLE SWITCHED ON, the one place this differs from a launch, and
// on purpose: the notch line states what the NOTCH does with a declaration, and the user's
// `enabled` switch turns a loophole off at every notch alike, a jail's included. Read through the
// switch, a pi-only config would print aws-auth's doorway (off by default) as "does not apply at
// the host" — false, since `yolo host --` opens it once it is on, and the launch that does not
// open it says so and names the switch (notOpenedWhy). Implementation decision, taken under the
// maintainer's 2026-10-04 delegation, reversible (docs/design/declaration-parity.md's ledger).

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// HostDoorwayLoopholes names each loophole of packs whose credential doorway a `yolo host --`
// launch opens for the agent whose selection asks for it (docs/design/host-notch-services.md
// HS-D15, HS-D21), with every selected pack's loophole switched on (this file's header says
// why). cfg is the user-scope config, whose loophole settings the set is discovered with. Empty
// when no pack ships a loophole, in which case nothing is discovered.
func HostDoorwayLoopholes(cfg *jsonx.OrderedMap, packs []*packload.Pack) map[string]bool {
	out := map[string]bool{}
	decls := packLoopholeDecls(packs)
	if len(decls) == 0 {
		return out
	}
	switchedOn := jsonx.NewOrderedMap()
	if block := cfgMap(cfg, "loopholes"); block != nil {
		switchedOn = jsonx.DeepCopyMap(block)
	}
	for _, d := range decls {
		entry, _ := switchedOn.Get(d.Name)
		m, ok := entry.(*jsonx.OrderedMap)
		if !ok {
			m = jsonx.NewOrderedMap()
		}
		m.Set("enabled", true)
		switchedOn.Set(d.Name, m)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		LoopholesConfig:   switchedOn,
		PackModules:       packLoopholeModules(packs),
		PackSupersessions: packSupersessions(packs),
	})
	specs, _ := launchservice.AdmitDoorways(packs,
		set.JailDaemons(set.Enabled(), hostNotchRuntime, nil))
	for _, s := range loopholes.Doorways(specs) {
		out[s.Name] = true
	}
	return out
}

// HostInlineLoopholes names, in the order the config lists them, each enabled INLINE loophole of
// cfg's `loopholes` block: an entry that names no loophole a selected pack ships and declares a
// `command`, which discovery turns into a host daemon with no manifest
// (loopholes.Loophole.FromConfig). Its only client is a jail — a jail launch publishes its
// endpoint into the container, and no host verb starts it — so at the host notch it does not
// apply, while the `loopholes` key itself is honored (a pack loophole's doorway, its settings).
// Nil when cfg declares no loophole entry, in which case nothing is discovered.
func HostInlineLoopholes(cfg *jsonx.OrderedMap, packs []*packload.Pack) []string {
	block := cfgMap(cfg, "loopholes")
	if block == nil || block.Len() == 0 {
		return nil
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		LoopholesConfig:   block,
		PackModules:       packLoopholeModules(packs),
		PackSupersessions: packSupersessions(packs),
	})
	var out []string
	for _, lp := range set.Enabled() {
		if lp.FromConfig() && lp.HostDaemon != nil {
			out = append(out, lp.Name)
		}
	}
	return out
}
