package run

// doorwayplacement.go is the one refusal every backend makes when the placement rule
// (docs/reference/loophole-system.md#the-placement-rule) refuses the host service a planned
// doorway forwards to. A DOORWAY is docs/design/host-notch-services.md HS-D15's word: the thin
// adapter an agent's client talks to, which checks the launch's caller token and forwards to a
// credential service's host daemon (aws-auth's container-credentials adapter, the Codex refresh
// adapter). Where it runs differs by backend: inside a container jail as a jail daemon, outside the
// Seatbelt sandbox as a macos-user launch's listener, and beside the agent at `yolo host`. What it
// forwards to does not: the loophole's host service, which the spawn skips when the rule refuses
// it. A doorway left open behind that refusal answers every request ServiceUnreachable, pointed at
// a service that never started, so every backend refuses the launch instead (HS-D34).

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// doorwayRef names one doorway a launch serves: its loophole (the service it forwards to) and the
// pack that declares it.
type doorwayRef struct {
	service, pack string
}

// doorwayRefsOf names each planned doorway outside a jail (the macos-user and `yolo host` plans).
func doorwayRefsOf(plans []*launchservice.Plan) []doorwayRef {
	out := make([]doorwayRef, 0, len(plans))
	for _, p := range plans {
		out = append(out, doorwayRef{service: p.Service, pack: p.Pack})
	}
	return out
}

// refusePlacementOfDoorways refuses the launch, before anything starts, when the placement rule
// refuses the host service a doorway in doorways forwards to. set is the loophole set the launch's
// spawn reads. The spawn would skip that service and still open the doorway, which the composition
// has already pointed the agent at: a doorway with no service behind it, announced as forwarding to
// one, answering every request with an error (the Mac lab report of 2026-10-19, §4, saw "Refusing
// to start loophole aws-auth" and then "opened the aws-auth doorway"). A refusal that lets its
// doorway open refuses nothing, so a doorway whose service cannot start is one that cannot start,
// and that refuses the launch (host-notch-services.md §4.5), naming the placement to fix.
//
// The answer is decided from the pack's files, the jail home tree and the launch's workspace
// (placementWorkspace), before any spawn, so it is the same for every session of a workspace: a
// keeper changes who would hold the doorway, never whether its service may start (HS-D34). A
// service that fails at spawn for any other reason is not this refusal: its doorway still opens and
// answers each request with the reason (HS-D19).
func (o *Options) refusePlacementOfDoorways(set loopholes.Set, doorways []doorwayRef) error {
	var refused []string
	for _, d := range doorways {
		lp, ok := set.Lookup(d.service)
		if !ok {
			continue
		}
		problems := lp.PlacementProblems(o.placementWorkspace())
		for _, problem := range problems {
			o.pr(o.Stderr).print("[red]Refusing to start loophole " + lp.Name + ": " + problem + "[/red]")
		}
		if len(problems) > 0 {
			refused = append(refused, fmt.Sprintf("the %q doorway (pack %q) forwards to loophole %q's "+
				"host service, which the placement rule refuses to start (the lines above name the "+
				"file and the tree), so the doorway is not opened", d.service, d.pack, lp.Name))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return errors.New(strings.Join(refused, "; ") + ". Move the pack's files out of that tree, " +
		"then run the launch again")
}

// refusePlacementOfJailDoorways is refusePlacementOfDoorways for a launch with a jail, container or
// macos-user, over payload, the launch's composed jail-daemon payload (jailDaemonsFor). Its
// doorways are every one the launch serves (loopholes.ServedJailDaemons): the ones a container runs
// inside, the ones macos-user opens outside its sandbox, and a doorway whose host argv the
// admission rule cleared (o.refusedDoorways), which still forwards to its loophole's host service
// from wherever its jail daemon runs. Only a doorway whose host service this backend's spawn would
// start is judged (loopholeAllow): on Apple Container, which starts the OpenAI service alone, the
// placement rule is not what leaves aws-auth's adapter unanswered. Nothing under the seal, which
// runs no loophole (seal.go).
func (o *Options) refusePlacementOfJailDoorways(rt string, cfg *jsonx.OrderedMap, packs []*packload.Pack,
	payload []loopholes.JailDaemonSpec) error {
	if o.Sealed {
		return nil
	}
	allow := o.loopholeAllow(rt, cfg)
	packOf := launchservice.LoopholePacks(packs)
	cleared := map[string]string{}
	for _, r := range o.refusedDoorways {
		cleared[r.Name] = r.Pack
	}
	var refs []doorwayRef
	for _, s := range loopholes.ServedJailDaemons(rt, payload) {
		pack, wasCleared := cleared[s.Name]
		if !wasCleared {
			if len(loopholes.Doorways([]loopholes.JailDaemonSpec{s})) == 0 {
				continue
			}
			pack = packOf[s.Name]
		}
		if !allow(s.Name) || slices.ContainsFunc(refs, func(r doorwayRef) bool { return r.service == s.Name }) {
			continue
		}
		refs = append(refs, doorwayRef{service: s.Name, pack: pack})
	}
	if len(refs) == 0 {
		return nil
	}
	return o.refusePlacementOfDoorways(loopholes.NewHostSet(loopholes.LaunchBlock(cfg)), refs)
}
