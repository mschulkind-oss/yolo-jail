package run

// hostdoorwaysets.go answers two questions about a pack set's loopholes for `yolo host apply`'s
// notch line (internal/cli's hostapplynotch.go), which plans no launch and so cannot ask
// PlanHostDoorways: which loopholes' doorways a `yolo host --` launch can open, and which
// loopholes of the user's own config are inline ones (a `loopholes.<name>` entry with a
// `command` and no manifest), whose only client is a jail.
//
// THE DOORWAYS ARE PlanHostDoorways' COMPOSITION, re-run here rather than read from it because
// hostdoorways.go plans a launch (it reserves ports and mints tokens) and the apply plans none:
// the set's jail daemons at the host runtime, their host argv admitted by
// launchservice.AdmitDoorways, kept by loopholes.Doorways. TestHostDoorwayLoopholesIsThePlansOwnSet
// pins that, for every doorway a launch's selection asks the plan for, the plan and this set give
// one answer.
//
// TWO DIFFERENCES FROM A LAUNCH, both on purpose: the notch line states what the NOTCH does with
// an enabled declaration, not what one launch does with it.
//
//   - EVERY SELECTED PACK'S LOOPHOLE IS SWITCHED ON. The user's `enabled` switch turns a loophole
//     off at every notch alike, a jail's included. Read through the switch, a pi-only config would
//     print aws-auth's doorway (off by default) as "does not apply at the host", which is false:
//     `yolo host --` opens it once it is on, and a launch whose agent asks for it while it is off
//     names the switch (notOpenedWhy).
//   - THERE IS NO SELECTION FILTER. PlanHostDoorways opens a doorway only for a profile-served
//     daemon whose gate the launched agent's selection satisfies (HS-D22), and the apply runs no
//     agent, so every composed doorway is named. That is why openai-auth-broker is named:
//     PlanHostDoorways opens it for no selection, its pointer being ungated, and `yolo host --
//     codex` serves that address from its managed launch instead (HS-D20, HS-D22).
//
// DISCOVERY SAYS ITS OWN WARNINGS, ON THE PROCESS'S STDERR. Both functions run loopholes.NewSet,
// and the loader says what it finds wrong (a module dir that is missing or does not load, a
// manifest key this build does not know, a supersession nothing matches, a retired user loophole)
// through the loopholes package's one sink, each distinct line once per process. Every verb that
// discovers loopholes says them that way: a jail launch, `yolo host --`'s doorways, `yolo check`.
// So `yolo host apply` says them too, on stderr just above its notch line, whose loophole outcome
// they explain (TestHostApplySaysLoopholeDiscoveryWarningsOnStderrAboveTheNotchLine), and so does
// `yolo host --`'s launch gate when `host_apply_on_launch` is on, since it runs the apply's
// observe pass (internal/cli's hostapplygate.go). They are not the report's lines: the sink is
// process-wide, and swapping it for one apply would race with that gate, whose observe pass runs
// on a goroutine the launch can abandon while it discovers doorways of its own. Routing them into
// the report takes a per-call sink in loopholes.DiscoverOptions, which is not built.
//
// Implementation decisions, taken under the maintainer's 2026-10-04 delegation, reversible
// (docs/design/declaration-parity.md's ledger).

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// HostDoorwayLoopholes names each loophole of packs whose credential doorway the host notch
// composes, the one a `yolo host --` launch opens for an agent whose selection asks for it
// (docs/design/host-notch-services.md HS-D15, HS-D21), with every selected pack's loophole
// switched on and no selection filter (this file's header says why). cfg is the user-scope
// config, whose loophole settings the set is discovered with. Empty when no pack ships a
// loophole, in which case nothing is discovered.
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
