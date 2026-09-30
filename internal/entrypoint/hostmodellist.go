package entrypoint

// hostmodellist.go is the list half of a program's MODEL MENU at `yolo host --`
// (packdecl.ModelMenu; docs/design/model-lists-and-pickers.md MM-D24): the list a jail's boot
// renders into the file the pack's `model_menu.list` names, composed instead IN-PROCESS for one
// host launch, from that launch's own wire tables, and handed to the menu step without touching a
// file.
//
// WHY NOT THE FILE `yolo host apply` COULD RENDER. It would be written for the configured profile
// alone (OQ-HC3), never for the launch's `-p`, and it can be older than the launch, since only a
// wrapped launch with `host_apply_on_launch` on renders first. So the surface stays `notAtHost`,
// and this runs its derive for the launch instead.
//
// ONE CODE PATH WITH THE JAIL (NC-D1): the derive is the pack's own, found by the surface whose
// path the declaration's `list` names, and it runs through deriveComputedLayer over the jail's
// own readers of the wire tables (hostSources, HC-D13). What differs is only where the tables come
// from, and that nothing is written.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// HostModelList is yolo's list for the model menu menu declares on a program of p, as p's derive
// composes it over in, the launch's inputs (in.Vars its three wire tables, in.Packs its selected
// packs): nil when the derive
// names no model for the launch's provider, which is "no menu this launch". home is the real home
// the launch runs in; nothing is written into it. A derive's warnings are handed to warn, one line
// each.
//
// An error means the list could not be composed at all: p declares no surface at the path the
// declaration reads, its surfaces do not decode, or its derive failed. The caller warns and the
// program keeps its own menu, as for every other failure of the step.
func HostModelList(p *packload.Pack, menu packdecl.ModelMenu, home string, in *HostInputs,
	warn func(string)) ([]modelmenu.ListEntry, error) {
	surfaces, problems := p.SurfacesFor(render.ProfileFor(render.KindHost).AgentAutonomy)
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", problems[0])
	}
	// The path equality a jail relies on too: its boot writes the surface there, and its
	// launcher reads the declaration's `list` from there (TestCodexModelMenuReadsTheListItsSurfaceWrites).
	want := "~/" + menu.List
	for _, s := range surfaces {
		if s.Path != want {
			continue
		}
		e := &Env{Home: home, Vars: in.vars(), hostTarget: true, Stderr: warnWriter(warn)}
		sources := newHostSources(e, in)
		layer, _, err := deriveComputedLayer(e, s, packload.DeriveScript(p), sources.selectionFor(s),
			sources.forAgent(s.Agent).tables)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(layer)
		if err != nil {
			return nil, fmt.Errorf("surface %s/%s: its derive's output is not JSON: %v", s.Agent, s.Name, err)
		}
		return modelmenu.ParseList(data), nil
	}
	return nil, fmt.Errorf("pack %s declares no surface at %s, where its model_menu reads the list", p.Name, want)
}

// warnWriter is an io.Writer over a line callback: each line written is one call, without its
// newline. A nil callback discards.
type warnWriter func(string)

func (w warnWriter) Write(b []byte) (int, error) {
	if w == nil {
		return len(b), nil
	}
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line != "" {
			w(line)
		}
	}
	return len(b), nil
}
