package entrypoint

// hostlayerlabel.go is the FIFTH disposition of the launcher's host-layer report: these
// staged bytes are YOLO'S OWN RENDER, so the jail treats them as a BASELINE and never
// composes them as a layer ([OQ-CR6], docs/reference/config-target-resolution.md).
//
// # Why the bytes are not always the user's
//
// A `readsHost` surface composes the user's own copy of its file, staged under /ctx by the
// launch. The moment yolo WRITES that file — `assert` rewrites its declared keys into it,
// `own` composes it whole — those bytes stop being purely the user's. Folding them back in
// makes a key yolo wrote indistinguishable from one the user wrote, so a pack overlay that
// later changes or is removed leaves its old value in the file forever, because it came
// back as "the user's". That is the circularity SkillTarget.HostSource was DELETED for, one
// file over (internal/jailcontent/skills.go), and [P6] is the rule stated on the WRITE
// rather than on the notch's name.
//
// # THE JAIL CANNOT DERIVE WHICH CASE IT IS IN, which is why the LAUNCH says so
//
// `host_management` is deliberately NOT inherited into a jail
// (internal/config/inherit.go refuses it, because in here the referent rebinds to the
// container's disposable home), so a boot render holding a /ctx copy has no way to tell the
// user's file from yolo's own output. The fact is the HOST's to state, and it travels as a
// property of the DELIVERY on the report the boot render already switches on: the jail
// needs to know what the bytes ARE, never which posture the user chose.
//
// # It is the MARK that decides, not the posture — and that is one step sharper than the ruling
//
// [OQ-CR6]'s table names `assert`/`own`. Taken as a posture test it would be wrong on
// almost every machine: an ABSENT `host_management` resolves to `assert`
// (config.HostManagementMode, [OQ-CO2]), so a jail on a default install would stop
// composing the ~/.claude/settings.json its user already has — which is exactly the
// onboarding path [P7] says must stay frictionless, and the design's own table mislabels
// `none` as the default.
//
// [P6] states the rule on the WRITE, and a write leaves a mark: the host PROVENANCE record,
// which hostProvenanceExists already reads as *"has yolo EVER asserted this surface in this
// home"* and which is written by every host render and by no dry run. So a surface yolo has
// rendered into this home is delivered as a render, one it has not is delivered as the
// user's own bytes, and the posture decides only whether there can be a next one.
//
// # Why the fifth lives here when the other four live in packload
//
// packload's report answers *"what did this launch deliver, and where"* — a fact about the
// mount, which is packload's. This one answers *"what are those bytes"*, which is a fact
// about a RENDER: what makes them yolo's is that this home's host render wrote them, and
// the host render is this package's (RenderHostPack, hostProvenanceExists). packload cannot
// compute it without importing the package that imports it. A reader gets all five through
// one call — HostLayerWire.DispositionFor — so the switch stays single.

import (
	"encoding/json"
	"os"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// HostLayerRender is the fifth disposition: the launcher delivered this path AND says the
// bytes there are yolo's own render. The surface composes WITHOUT a host layer, and the
// copy stands as the baseline the jail can report divergence against.
//
// It never refuses, and that is the same judgement the `rmw` arm of renderDeclaredSurface
// makes: a refusal over bytes the render would discard is an over-refusal. What the
// fail-closed read protects is a composition that would silently drop the user's own keys,
// and a render composed from packs alone drops none of them — they are the CAPTURE, which
// is already its own layer.
const HostLayerRender packload.HostLayerDisposition = "render"

// HostLayerWire is the YOLO_HOST_LAYERS value: packload's four-disposition report plus the
// labels the fifth needs. ONE variable, one producer, one consumer — a second environment
// variable would be a second channel for one fact about one delivery.
//
// The embedding is load-bearing: encoding/json inlines an anonymous struct field, so the
// wire is the same flat object packload.ParseHostLayerReport already reads, and a reader
// that only knows the four fields (macosuser's plan builder, an older jail) sees exactly
// what it saw before. Adding the label can therefore never break the four.
type HostLayerWire struct {
	packload.HostLayerReport
	// Rendered lists the /ctx destinations whose bytes are yolo's own render. A subset of
	// Delivered: a labelled path is still delivered, because the label says what arrived
	// rather than whether anything did.
	Rendered []string `json:"rendered,omitempty"`
}

// Marshal renders the wire for the environment. Deterministic, for the reason packload's
// own Marshal is: the field set is fixed and both lists are in the launcher's emission
// order.
func (w HostLayerWire) Marshal() (string, error) {
	b, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParseHostLayerWire reads the wire out of an environment value, with packload's contract
// unchanged: an empty or unparseable value is UNKNOWN (ok=false), never "nothing was
// delivered". A jail that read a garbled variable as an empty delivery list would refuse
// every host layer on the machine.
func ParseHostLayerWire(wire string) (HostLayerWire, bool) {
	if wire == "" {
		return HostLayerWire{}, false
	}
	var w HostLayerWire
	if err := json.Unmarshal([]byte(wire), &w); err != nil || w.Delivery == "" {
		return HostLayerWire{}, false
	}
	return w, true
}

// DispositionFor answers for one surface's /ctx destination, across all five.
//
// The label is checked FIRST because it is a statement about bytes that did arrive: a
// rendered path is in Delivered too, and reading the delivery answer first would compose
// exactly the file this exists to keep out of the fold.
func (w HostLayerWire) DispositionFor(ctxPath string) packload.HostLayerDisposition {
	if slices.Contains(w.Rendered, ctxPath) {
		return HostLayerRender
	}
	return w.HostLayerReport.DispositionFor(ctxPath)
}

// HostLayerDispositionIn is the one reading of a report value, for the two processes that
// have one: the boot render (through Env.Getenv) and the host CLI (through os.Getenv).
//
// An absent or garbled report is UNKNOWN, which composes the layer and refuses nothing —
// the behaviour that shipped before the variable existed, because absence means only
// "launcher older than this variable".
func HostLayerDispositionIn(wire, ctxPath string) packload.HostLayerDisposition {
	w, ok := ParseHostLayerWire(wire)
	if !ok {
		return packload.HostLayerUnknown
	}
	return w.DispositionFor(ctxPath)
}

// StagedHostLayer is what THIS process can say about a surface's host bytes: where they are
// staged for it, and what they are. It reads the process environment, so it answers for the
// host CLI (`yolo config render`) exactly as hostSurfaceBytes answers for the boot render,
// off one definition rather than two agreeing by hand.
//
// The path is remapped through the ctx root, so Apple Container's copy-into-the-home
// delivery resolves here too — the same one root both /ctx readers go through.
func StagedHostLayer(s manifest.Surface) (string, packload.HostLayerDisposition) {
	if s.HostSource == "" {
		return "", packload.HostLayerUnknown
	}
	return remapCtx(s.HostSource),
		HostLayerDispositionIn(os.Getenv(packload.HostLayerEnvVar), s.HostSource)
}

// HostSurfaceRendered reports whether yolo has EVER rendered this surface into home — the
// discriminator the launcher labels a delivery with, and the file header's argument for why
// it is the mark rather than the posture.
//
// It is hostProvenanceExists over a host-notch Env built for the home in question, rather
// than a second stat of a hand-joined path: which file records a host render is
// render.Target.ProvenancePath's answer, and a launcher that re-derived it would label
// deliveries off a path the renderer had moved.
//
// OwnershipUnstated is right here and is not an oversight: the provenance path is the same
// at all three contracts (render.Target.ProvenanceDir switches on the NOTCH, not the
// contract), and this asks what yolo has already written rather than what it may write
// next.
func HostSurfaceRendered(home string, s manifest.Surface) bool {
	if home == "" {
		return false
	}
	return hostProvenanceExists(&Env{Home: home, hostTarget: true, Vars: map[string]string{}}, s)
}
