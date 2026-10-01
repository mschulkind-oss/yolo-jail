package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// applyhostuserfiles.go is `host_files` and `mise_tools` at `yolo host apply` (OQ-NC8,
// docs/plans/notch-convergence.md, ruled 2026-09-28 by parity, A). A source-less entry renders
// into the real home through the one render loop (entrypoint.RenderHostUserFiles), under the
// declared `host_management`, and is reported by the rows a pack surface gets
// (reportConfigResult). An entry that leaves the config has what the earlier apply wrote removed,
// on the provenance record that apply kept (entrypoint.RetireHostUserFiles). A source-bearing
// entry does nothing here, and the notch line names it by destination; `mise_tools`, which does
// nothing here either, is named there by the config-key census (render's configkeys.go), with
// every other key the host leaves undone.
//
// USER SCOPE ONLY, as `agents_md_extra` is at the host (NC-D30): host apply reads no workspace.

// hostUserFiles is what the user config's `host_files` declares that this file answers for.
type hostUserFiles struct {
	// render are the source-less entries this notch writes.
	render []config.HostFileEntry
	// inert are the source-bearing entries (a directory entry is always one): each mirrors a
	// host file INTO a jail, and at the host that file is already the user's.
	inert []config.HostFileEntry
	// cfg is the user-scope config the entries were read from, so a refusal names an entry by
	// its place in it (config.SurfaceCollisions).
	cfg *jsonx.OrderedMap
}

// readHostUserFiles reads host_files from the user-scope config.
func readHostUserFiles(cfg *jsonx.OrderedMap) hostUserFiles {
	f := hostUserFiles{cfg: cfg}
	for _, e := range config.HostFilesIn(cfg) {
		if e.SourceBearing() || e.IsDir {
			f.inert = append(f.inert, e)
			continue
		}
		f.render = append(f.render, e)
	}
	return f
}

// inertNames are the host_files entries this notch leaves inert, for the tier-1 notch line
// (notchFacts.InertConfig): the source-bearing entries named by destination, because the key
// is the unit a reader looks for and the destination is which of their entries it means.
func (f hostUserFiles) inertNames() []string {
	var out []string
	if len(f.inert) > 0 {
		dests := make([]string, len(f.inert))
		for i, e := range f.inert {
			dests[i] = "~/" + e.Path
		}
		out = append(out, "host_files with a source ("+strings.Join(dests, ", ")+")")
	}
	return out
}

// hostUserFileCollisions is the jail's two-writers refusal (config.SurfaceCollisions, OQ-LM6)
// over the packs this apply renders: a host_files destination a selected pack also composes.
func hostUserFileCollisions(f hostUserFiles, packs []*packload.Pack) []string {
	var paths []string
	for _, p := range packs {
		surfaces, probs := p.Surfaces()
		if len(probs) > 0 {
			continue
		}
		for _, s := range surfaces {
			paths = append(paths, s.Path)
		}
	}
	cols := config.SurfaceCollisions(f.render, paths, nil, nil)
	if len(cols) == 0 {
		return nil
	}
	// Named by its place in the user scope and led by the file and line it is written at
	// (config's sources.go), which is read again only for a refusal.
	return config.SurfaceCollisions(f.render, paths, f.cfg, config.UserScopeSources())
}

// applyHostUserFiles renders the source-less entries, reports each through reportConfigResult,
// then retires what an earlier apply wrote for an entry that is gone. It returns an rc
// contribution. write=false is the observe posture: every line is what an --assert would do.
func applyHostUserFiles(pr richtext.Printer, survey *hostApplySurvey, f hostUserFiles,
	home string, write bool) int {
	rc := 0
	results, err := entrypoint.RenderHostUserFiles(f.render, home, hostOwnership(), !write)
	if err != nil {
		survey.noteRenderFailure(hostUserFilesOwner, err.Error())
		rc = 1
	}
	for _, r := range results {
		reportConfigResult(pr, survey, hostUserFilesOwner, r, home, write)
	}
	retired, err := entrypoint.RetireHostUserFiles(f.render, home, !write)
	for _, r := range retiredUserFileRows(retired, write) {
		reportConfigResult(pr, survey, hostUserFilesOwner, r, home, write)
	}
	for _, k := range retired.Keys {
		pr.Printf("    [yellow]%s %s[/yellow] [dim](%s)[/dim]", k.Action, k.Key, k.Layer)
	}
	if err != nil {
		survey.noteRenderFailure(hostUserFilesOwner, err.Error())
		rc = 1
	}
	return rc
}

// hostUserFilesOwner is the name the report attributes the user's entries to, where a pack
// surface names its pack: the config key they come from.
const hostUserFilesOwner = "host_files"

// retiredUserFileRows turns a retirement into one config row per file it changes, so the change
// predicate, the destination line and the verdict's count read it as they read a render. The
// keys are itemized under it by the caller.
func retiredUserFileRows(rev entrypoint.HostRevert, write bool) []entrypoint.HostRenderResult {
	count := map[string]int{}
	paths := map[string]string{}
	for _, k := range rev.Keys {
		count[k.Surface]++
		paths[k.Surface] = k.Path
	}
	ids := make([]string, 0, len(count))
	for id := range count {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	verb := "would remove"
	if write {
		verb = "removed"
	}
	out := make([]entrypoint.HostRenderResult, 0, len(ids))
	for _, id := range ids {
		out = append(out, entrypoint.HostRenderResult{Surface: id, Path: paths[id],
			WouldChange: true,
			Action: fmt.Sprintf("%s %d %s yolo wrote (the entry left host_files)", verb,
				count[id], plural(count[id], "key", "keys"))})
	}
	return out
}
