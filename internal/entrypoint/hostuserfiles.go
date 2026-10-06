package entrypoint

// hostuserfiles.go is `host_files` at the host notch (OQ-NC8, docs/plans/notch-convergence.md,
// ruled 2026-09-28 by parity, A): a SOURCE-LESS entry — inline `content`, or only `defaults` and
// `managed` — renders into the real home through the same surface engine a jail's boot runs,
// under the declared `host_management` contract, and a later apply removes what it wrote once the
// entry is gone, on the authority of the provenance record the write keeps.
//
// THE ONE LOOP, NOT A FIFTH ONE. The entries are planned by planSurface, the census's one
// planner, and rendered by renderHostPlans, the host half of the loop RenderHostPack runs, so a
// user surface is probed, refused, reported and written exactly as a pack's is
// (surfaceloop_test.go pins both callers).
//
// WHAT CHANGES FROM THE JAIL, and why, each an input rather than a branch:
//
//   - THE MECHANISM IS THE CONTRACT'S, not the entry's `mode`. A jail's four modes decide what
//     happens across BOOTS to a file in a disposable home; at the host the census decides, as it
//     does for every pack surface: `own` composes the whole file with the capture store, and
//     `none` writes nothing (the retired `assert` read-modify-wrote the entry's keys). So the
//     surface declares `stateful`, which each contract runs through its own mechanism.
//   - `content` IS THE ENTRY'S OWN LAYER, NOT A HOST LAYER. In a jail the literal is the `host`
//     layer. At the host the file's existing content already is (`own`'s adoption, as it was
//     the retired `assert`'s rmw read), so the literal is a declaration yolo writes, lowered by the one question the
//     entry's mode answers — does an edit to the file survive the next render? `once` and
//     `capture` say yes, so the literal fills `defaults` (a key the file lacks); `copy` and
//     `readonly` say no, so it fills `managed` (re-asserted every apply). The entry's own
//     `defaults`/`managed` win over the literal key by key, as they outrank it in a jail.
//   - FILE PERMISSIONS ARE NOT SET. `readonly`'s 0444 and an executable source's bit are a
//     jail-home policy; the engine's writers keep the real file's mode.
//
// SOURCE-BEARING ENTRIES ARE INERT HERE, and the caller names them (internal/cli): an entry with
// a `source` mirrors a host file INTO a jail, and at the host that file is already the user's.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostUserFilesAgent is the pseudo-agent every host_files surface is owned by, the jail's own
// (HostFileSurface): no real agent is named "user", and the provenance record's file name
// (user-<slug>.provenance) is what a later apply finds a dropped entry's record by.
const hostUserFilesAgent = "user"

// hostUserFilesOwner is the owner renderHostPlans reads for the user's surfaces: no root (so no
// derive.lua and no derived tables) and no declaration (so no posture patch outranks a key).
var hostUserFilesOwner = &packload.Pack{Name: "host_files"}

// HostFileSurfaceAtHost lowers a source-less entry to the surface the host renders: the jail's
// lowering (HostFileSurface) with the literal folded into the layer its mode names, and the
// `stateful` declaration every contract runs through its own mechanism. See the file header.
func HostFileSurfaceAtHost(entry config.HostFileEntry) manifest.Surface {
	s := HostFileSurface(entry)
	s.Mode = manifest.ModeStateful
	if !entry.HasContent || s.Kind() != codec.KindObject {
		// A keyless codec (raw, lines) keeps no key a later apply could remove, and both host
		// mechanisms refuse it by name (rmwCodecRefusal, hostStatefulRefusal): the row says why.
		return s
	}
	literal := agentcfg.DecodeSurfaceObject(entry.Codec, []byte(entry.Content))
	if len(literal) == 0 {
		return s
	}
	switch entry.Mode {
	case config.HostFileModeCopy, config.HostFileModeReadonly:
		s.Managed = mergeUnder(literal, s.Managed)
	default: // once, capture: an edit survives, so the literal only fills what is absent
		s.Defaults = mergeUnder(literal, s.Defaults)
	}
	return s
}

// mergeUnder is base with over deep-merged on top, as a new map: over's value wins a key both
// declare, and an object both hold merges key by key. over that is not an object is returned
// unchanged (the entry's layer was shape-checked against its codec already).
func mergeUnder(base map[string]any, over any) any {
	if over == nil {
		return base
	}
	o, ok := over.(map[string]any)
	if !ok {
		return over
	}
	out := make(map[string]any, len(base)+len(o))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range o {
		if bm, isMap := out[k].(map[string]any); isMap {
			out[k] = mergeUnder(bm, v)
			continue
		}
		out[k] = v
	}
	return out
}

// planHostFileSurfaces is the one loop's head for the user's source-less entries: each entry's
// host surface, planned by planSurface (the census's mechanism at this target), with no other
// pack's contributions — a config-overlay names a surface a pack owns, never the user's own.
func planHostFileSurfaces(e *Env, entries []config.HostFileEntry) []surfacePlan {
	var plans []surfacePlan
	for _, entry := range entries {
		if entry.SourceBearing() || entry.IsDir {
			continue
		}
		plans = append(plans, planSurface(e, hostUserFilesOwner, HostFileSurfaceAtHost(entry), nil))
	}
	return plans
}

// RenderHostUserFiles renders every source-less host_files entry into homeDir through the one
// render loop, under the declared contract, and returns one result per entry in the rows a pack
// surface gets. observe computes what an --assert would do and writes nothing. Source-bearing
// and directory entries are not rendered here and get no row: the caller names them inert.
//
// It takes no HostInputs: no derive runs over a user surface, so the host's derive tables are
// not an input, and handing them in would report the MCP gate's skips against agent "user".
func RenderHostUserFiles(entries []config.HostFileEntry, homeDir string,
	ownership render.HostOwnership, observe bool) ([]HostRenderResult, error) {
	e := hostRenderEnv(homeDir, ownership, nil)
	return renderHostPlans(e, hostUserFilesOwner, planHostFileSurfaces(e, entries), observe, nil)
}

// RetireHostUserFiles removes, from homeDir, the keys an earlier apply wrote for every
// host_files entry no longer in current, on the authority of that entry's provenance record,
// and deletes the record. It is `yolo host apply --revert`'s walk (withdrawHostSurface) over the
// user's dropped entries, so it removes only what the record attributes to a layer yolo wrote,
// never a key recorded `host`, and it leaves an emptied file in place. observe reports what an
// --assert would remove and writes nothing.
//
// current is the source-less set this apply renders; nil is read as none, which is the honest
// answer for a config with no host_files at all: every record then names a dropped entry.
func RetireHostUserFiles(current []config.HostFileEntry, homeDir string,
	observe bool) (HostRevert, error) {
	e := hostRenderEnv(homeDir, render.OwnershipUnstated, nil)
	live := map[string]bool{}
	for _, entry := range current {
		if !entry.SourceBearing() && !entry.IsDir {
			live[entry.Slug()] = true
		}
	}
	var out HostRevert
	for _, s := range hostUserFileRecords(e) {
		if live[s.Name] {
			continue
		}
		if err := withdrawHostSurface(e, s, observe, &out); err != nil {
			return out, err
		}
		if !observe && !hostProvenanceExists(e, s) {
			forgetOwnedCapture(homeDir, s)
		}
	}
	return out, nil
}

// forgetOwnedCapture deletes the `host_management: own` capture sidecars of a user surface whose
// record was just withdrawn. Nothing reads them once the entry is gone, and an entry declared
// again later must start from adoption, not replay a capture of a file yolo has since edited.
// The paths are the `own` target's whatever the current contract, since the sidecars were
// written under `own` if they exist at all.
func forgetOwnedCapture(homeDir string, s manifest.Surface) {
	t := render.Host(homeDir, nil, render.OwnershipOwn)
	for _, p := range []string{t.OverlayPath(s.Agent, s.Name), t.LastRenderPath(s.Agent, s.Name),
		t.SelectionPath(s.Agent, s.Name), t.ListCapturePath(s.Agent, s.Name)} {
		if p != "" {
			_ = os.Remove(p)
		}
	}
}

// hostUserFileRecords lowers every host_files provenance record in e's home back to the surface
// its file is: the path from the slug (config.HostFilePathFromSlug; the slug is an injective
// escape of it), the codec the first object codec the file decodes as, starting from the one
// its extension names. A record whose slug does not decode names no file and is skipped.
func hostUserFileRecords(e *Env) []manifest.Surface {
	dir := e.renderTarget().ProvenanceDir()
	if dir == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, hostUserFilesAgent+"-*.provenance"))
	sort.Strings(matches)
	var out []manifest.Surface
	for _, m := range matches {
		slug := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), hostUserFilesAgent+"-"),
			".provenance")
		rel, ok := config.HostFilePathFromSlug(slug)
		if !ok {
			continue
		}
		s := manifest.Surface{Agent: hostUserFilesAgent, Name: slug, Path: "~/" + rel,
			Mode: manifest.ModeStateful}
		s.Codec = recordedFileCodec(expandHomePath(e, s.Path), rel)
		out = append(out, s)
	}
	return out
}

// recordedFileCodec is the codec a dropped entry's file is read back with. The record keeps no
// codec, and only an object codec ever wrote one (a keyless surface is refused before its
// write), so the answer is the first object codec the file decodes as, the extension's first.
// A file none decodes keeps the extension's answer, which the withdrawal then leaves untouched
// with its record, as a revert leaves a file it cannot parse.
func recordedFileCodec(path, rel string) string {
	byExt := config.HostFileCodecFor(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return byExt
	}
	for _, c := range []string{byExt, "json", "toml", "yaml"} {
		if k, _ := codec.KindOf(c); k != codec.KindObject {
			continue
		}
		if agentcfg.DecodeSurfaceObject(c, data) != nil {
			return c
		}
	}
	return byExt
}
