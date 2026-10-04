package launchservice

// doorways.go is where a composed jail-daemon payload meets AdmitDoorway: which of its credential
// DOORWAYS may open outside a sandbox (docs/design/host-notch-services.md HS-D15; "doorway" is
// that ruling's word for the thin adapter an agent's client talks to, which checks the launch's
// caller token and forwards to the service's host daemon). A doorway is a loophole jail daemon
// that declares its host argv, `jail_daemon.host_cmd`.
//
// ONE FUNCTION FOR BOTH READERS OF A PAYLOAD. The launch composes its payload in
// internal/cli/run's jailDaemonsFor, and `yolo check` predicts what that launch serves in
// internal/cli/check's predictedServed. Admission decides whether a doorway is served outside the
// sandbox at all, so a reader that skipped it would count a doorway the launch never opens, and a
// pointer at it would compose in the prediction and be withheld at the launch. Both call
// AdmitDoorways on the payload before anything reads it.
//
// THE SAME TWO READERS COMPOSE A PACK SERVICE'S DAEMON and admit its host half here too
// (ServiceJailDaemons, AdmitServiceHosts), for the same reason: whether the macos-user guest runs
// a service's jail daemon turns on whether its host half runs instead, so a reader that composed
// the service differently, or skipped the admission, would predict a daemon the launch declines
// or decline one the launch runs.

import (
	"errors"
	"path"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// RefusedDoorway is a doorway whose host argv AdmitDoorways did not admit: its loophole, the pack
// that ships it ("" when no selected pack does), and why, in words a disclosure quotes. A pack
// service's host half AdmitServiceHosts did not admit is reported in the same shape, Name being
// the service.
type RefusedDoorway struct {
	Name, Pack, Why string
}

// AdmitDoorways applies AdmitDoorway to every doorway in specs and returns a copy of specs in
// which each refused doorway's host argv is cleared, with the refusals in order. A cleared one is
// an ordinary jail daemon again: a container runs it, and the macos-user guest runs it, confined,
// or declines it by name like any other (loopholes.JailDaemonsRunIn). A pack service's daemon and
// an intercepting loophole's are skipped, because loopholes.DoorwaysOutside never opens either,
// so there is nothing to refuse.
func AdmitDoorways(packs []*packload.Pack, specs []loopholes.JailDaemonSpec) ([]loopholes.JailDaemonSpec, []RefusedDoorway) {
	out := append([]loopholes.JailDaemonSpec(nil), specs...)
	var refused []RefusedDoorway
	var packOf map[string]string
	for i, s := range out {
		if len(s.HostCmd) == 0 || s.Service || s.Intercepts {
			continue
		}
		if packOf == nil {
			packOf = LoopholePacks(packs)
		}
		if _, err := AdmitDoorway(packs, packOf[s.Name], s.Name, s.HostCmd); err != nil {
			why := err.Error()
			var adm *AdmissionError
			if errors.As(err, &adm) {
				why = adm.Why
			}
			refused = append(refused, RefusedDoorway{Name: s.Name, Pack: packOf[s.Name], Why: why})
			out[i].HostCmd = nil
		}
	}
	return out, refused
}

// LoopholePacks maps each loophole a pack of packs ships to that pack's name. A pack loophole's
// name is its module directory's basename (packload.Pack.LoopholeModules), and when two packs ship
// one name the later wins, the pack a launch reads the declaration from; a launch refuses that
// collision before it composes a payload.
func LoopholePacks(packs []*packload.Pack) map[string]string {
	out := map[string]string{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, rel := range p.Decl.LoopholeSources() {
			out[path.Base(path.Clean(filepath.ToSlash(rel)))] = p.Name
		}
	}
	return out
}

// ServiceJailDaemons is the YOLO_JAIL_DAEMONS entry of every held pack service that declares a
// jail_daemon (packload.HeldServices: one per service name, the later pack's), sorted by name:
// THE ONE COMPOSER of a service's daemon, which the launch (internal/cli/run's
// serviceJailDaemons) and `yolo check`'s prediction both read. Restart defaults to "on-failure",
// the default the supervisor's ParseEnv and a loophole's load apply. CallerToken is always set,
// because every address a service serves names the service's caller token as its credential
// (wire-bridge.md WB-D18). HostHalf, ServesAdaptation and Endpoint are copied from the
// declaration for the macos-user guest's split (loopholes.JailDaemonsRunIn); none reaches the
// payload, so a container's argv is what it was.
func ServiceJailDaemons(packs []*packload.Pack) []loopholes.JailDaemonSpec {
	adapts := map[string]bool{}
	for _, a := range packload.ServiceAdaptations(packs, nil) {
		adapts[a.Service] = true
	}
	var entries []loopholes.JailDaemonSpec
	held, _ := packload.HeldServices(packs)
	for _, h := range held {
		s := h.Service
		if s.JailDaemon == nil || len(s.JailDaemon.Cmd) == 0 {
			continue
		}
		restart := s.JailDaemon.Restart
		if restart == "" {
			restart = "on-failure"
		}
		entries = append(entries, loopholes.JailDaemonSpec{
			Name: s.Name, Cmd: s.JailDaemon.Cmd, Restart: restart, CallerToken: true,
			Service:          true,
			HostHalf:         s.HostDaemon != nil && len(s.HostDaemon.Cmd) > 0,
			ServesAdaptation: adapts[s.Name],
			Endpoint:         s.Endpoint,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// AdmitServiceHosts applies Admit to every pack service's daemon in specs that declares a host
// half (loopholes.JailDaemonSpec.HostHalf) and returns a copy of specs in which each refused
// one's HostHalf is cleared, with the refusals in order (Name the service, Pack its holder, Why
// in words a disclosure quotes). AdmitDoorways' mirror, for a service: a cleared one is judged
// as the jail daemon it also is, so a container runs it, as it always did, and the macos-user
// guest runs it, confined (OQ-DP8, OQ-DP9), or declines it by name for a reason of its own,
// instead of declining it for a host half that never starts. Read by the same pair:
// internal/cli/run's jailDaemonsFor and `yolo check`'s predictedServed.
func AdmitServiceHosts(packs []*packload.Pack, specs []loopholes.JailDaemonSpec) ([]loopholes.JailDaemonSpec, []RefusedDoorway) {
	out := append([]loopholes.JailDaemonSpec(nil), specs...)
	var refused []RefusedDoorway
	for i, s := range out {
		if !s.Service || !s.HostHalf {
			continue
		}
		if _, err := Admit(packs, s.Name); err != nil {
			why, pack := err.Error(), ""
			var adm *AdmissionError
			if errors.As(err, &adm) {
				why, pack = adm.Why, adm.Pack
			}
			refused = append(refused, RefusedDoorway{Name: s.Name, Pack: pack, Why: why})
			out[i].HostHalf = false
		}
	}
	return out, refused
}
