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

import (
	"errors"
	"path"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// RefusedDoorway is a doorway whose host argv AdmitDoorways did not admit: its loophole, the pack
// that ships it ("" when no selected pack does), and why, in words a disclosure quotes.
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
