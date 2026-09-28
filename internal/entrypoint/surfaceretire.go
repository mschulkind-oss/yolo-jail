package entrypoint

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// surfaceretire.go holds the two deletes a jail boot makes that READ BEFORE THEY DELETE, and
// so may touch a file whose name is not yolo's alone: RetireIfMatchesRender (the old location
// of a render that moved) and an unselected WhenListed surface's own previous render. Both
// delete only a file whose content says it is yolo's, and both answer every doubt — a read
// failure, a decode failure, a missing record — by leaving the file where it is.

// retireMatchingCopies deletes each of the surface's RetireIfMatchesRender siblings that holds
// exactly what the surface's own file holds now, compared decoded with the surface's codec
// (manifest.Surface.RetireIfMatchesRender states the rule and why it is the decoded value).
// Its one caller runs it only after the surface's own write succeeded, so "what the surface
// holds now" is this boot's render.
func retireMatchingCopies(e *Env, surface manifest.Surface) {
	if len(surface.RetireIfMatchesRender) == 0 {
		return
	}
	own := expandHomePath(e, surface.Path)
	rendered, ok := decodedSurfaceFile(surface, own)
	if !ok {
		return
	}
	dir := filepath.Dir(own)
	for _, name := range surface.RetireIfMatchesRender {
		path := filepath.Join(dir, name)
		held, ok := decodedSurfaceFile(surface, path)
		if !ok || !reflect.DeepEqual(held, rendered) {
			continue
		}
		// Ignored for retireOrphanSidecars' reason: the copy is unread by the surface that
		// replaced it, so a failed delete leaves it exactly as inert as it was.
		if os.Remove(path) == nil {
			e.note("retired " + path + ": it held exactly the " + surface.Agent + "/" +
				surface.Name + " render, which now lives at " + own)
		}
	}
}

// decodedSurfaceFile is path decoded as one of surface's files: its top-level object, with
// any yolo banner ignored (a comment, which a TOML decode drops anyway). ok is false for an
// absent, unreadable or undecodable file, or one that is not an object.
func decodedSurfaceFile(surface manifest.Surface, path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	m := agentcfg.DecodeSurfaceObject(surface.Codec, data)
	return m, m != nil
}

// retireUnselectedRender is what an unselected WhenListed surface does besides writing
// nothing: it removes its OWN previous render, so the selection's end does not leave a stale
// copy for every other reader of the path to keep loading. It removes the file, and the two
// capture sidecars beside it, only when all of this holds:
//
//   - the surface is `stateful`, the one mode that keeps a record of what yolo last wrote;
//   - the file is byte-identical to that record, so nothing has edited it since;
//   - the capture overlay holds no edit, so no key in that render came from anyone but yolo —
//     a user file the first render adopted, or an edit a later boot captured, keeps the file.
//
// Any other state leaves everything where it is, the sidecars included: they are what lets a
// re-selection compose the user's edits back rather than lose them.
func retireUnselectedRender(e *Env, surface manifest.Surface) {
	if surface.ResolvedMode() != manifest.ModeStateful {
		return
	}
	t := e.renderTarget()
	surface = t.Prepare(surface)
	path := t.SurfacePath(surface)
	current, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lastPath := prismLastRenderPath(e, surface.Agent, surface.Name)
	last, err := os.ReadFile(lastPath)
	if err != nil || !bytes.Equal(render.StripGeneratedHeader(current), last) {
		return
	}
	overlayPath := prismOverlayPath(e, surface.Agent, surface.Name)
	if !overlayIsEmpty(overlayPath) {
		return
	}
	if os.Remove(path) != nil {
		return
	}
	_ = os.Remove(lastPath)
	_ = os.Remove(overlayPath)
	e.note("removed " + path + ": " + surface.Agent + "/" + surface.Name + " is no longer " +
		"selected, and the file was still exactly yolo's last render of it")
}

// overlayIsEmpty reports whether a capture overlay sidecar records no edit: absent, or a JSON
// object with no keys. Anything else, an unreadable or corrupt file included, counts as an
// edit, since the answer decides whether a file is deleted.
func overlayIsEmpty(path string) bool {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	var m map[string]any
	return json.Unmarshal(data, &m) == nil && m != nil && len(m) == 0
}
