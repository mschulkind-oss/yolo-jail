package cli

// configcapture.go is E3's second half: capture on TERMINATE.
//
// `yolo config capture <agent>` (configdiff.go) records the current on-disk edits
// into the overlay on demand, from inside the jail that owns the workspace. This
// file does the same fold automatically once a jail is torn down, from the HOST,
// so `yolo config diff` answers about the session that just ended instead of the
// one before it.
//
// UNCONDITIONAL, NOT OPT-IN — the argument, since it is the decision E3 turns on:
//
//  1. It is not a new behavior, only an earlier one. A capture-mode surface is
//     captured by the next boot render regardless; this runs the SAME engine call
//     (ComposeStateful, via captureSurfaceAt) over the SAME three files with the
//     SAME baseline. So it is exactly idempotent with the boot capture and cannot
//     promote a key the next launch would not have promoted. That disposes of the
//     "capturing silently changes what the next launch composes" objection: the
//     next launch composes what it was going to compose either way.
//  2. It only touches surfaces already declaring `mode: capture`. Capture is never
//     implicit (docs/plans/host-file-staging.md, "Overlay capture is the exception,
//     never a default") — a user had to write the mode. This does not widen that
//     set by one file.
//  3. The gap it closes is a DEFAULT-correctness property of a reporting command.
//     `yolo config capture`'s own doc says the cost of not capturing is that
//     "`yolo config diff` cannot show an edit made this session, so a user checking
//     their own divergence sees a stale answer with no indication it is stale". A
//     flag nobody sets leaves that answer stale for everyone who did not already
//     know the flag existed — which is precisely the population the stale answer
//     misleads.
//  4. The BACKLOG entry's "nothing is lost today" is the reason this must never be
//     load-bearing, not a reason to make it opt-in. It is why every failure below
//     warns and proceeds (R7): a jail that would not exit cleanly because an
//     observability fold failed is a worse bug than the stale `diff` it fixes.
//
// WHERE IT READS FROM, and why not the obvious place. The composed surfaces live in
// the JAIL's home, and by teardown the container may be gone — so this reads the
// HOST-side dirs that back that home, all of which outlive the container:
// <workspace>/.yolo/home/... for the writable overlays and <workspace>/.yolo/prism/
// for the sidecars, both plain host directories on a live bind. It must NOT use
// expandHome/paths.Home(): host-side that is the invoking human's REAL home, and
// reading it here would copy the human's own dotfiles into the workspace overlay —
// the G2 privacy defect that refuseHostSideWrite refuses interactive `config
// capture` at the host to prevent.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// captureOnTerminate folds this session's in-jail edits into the overlay sidecars
// for the jail launched from workspace, which has just been torn down. runtime is
// the resolved container runtime, needed because the two container backends back
// the jail home differently (see jailHomeHostPath).
//
// It NEVER returns an error and never panics out: every failure is one warn line
// and the loop continues. See R7 in the file comment — the teardown this hangs off
// must not become failable because an audit convenience did.
func captureOnTerminate(workspace, runtime string, warn func(string)) {
	defer func() {
		if r := recover(); r != nil {
			warn(fmt.Sprintf("could not capture in-jail config edits: %v", r))
		}
	}()
	sidecarDir := filepath.Join(workspace, ".yolo", "prism")
	if st, err := os.Stat(sidecarDir); err != nil || !st.IsDir() {
		// No jail has ever rendered into this workspace, so there is no baseline to
		// diff against and nothing to capture. Silent: this is the ordinary state of
		// a workspace whose packs compose no capture surface, not a problem.
		return
	}
	for _, s := range terminateCaptureSurfaces(sidecarDir) {
		path, ok := jailHomeHostPath(workspace, runtime, s.Path)
		if !ok {
			// The surface's file is not in this workspace's host-side home backing —
			// it was never composed here, or it lives in a machine-scope shared dir
			// (see jailHomeHostPath). Skipping loses nothing: the next boot captures.
			continue
		}
		if _, err := captureSurfaceAt(s, captureLocation{
			surface:    path,
			lastRender: filepath.Join(sidecarDir, s.Agent+"-"+s.Name+".last_render"),
			overlay:    filepath.Join(sidecarDir, s.Agent+"-"+s.Name+".overlay.json"),
		}); err != nil {
			warn(fmt.Sprintf("could not capture %s/%s: %v (the next launch will capture it)",
				s.Agent, s.Name, err))
		}
	}
}

// terminateCaptureSurfaces is every surface capture-on-terminate considers: the
// manifest's capture-mode surfaces, plus the `user` host_files surfaces, which are
// discovered from the sidecar dir because their destinations live in config rather
// than the manifest (the same discovery userSidecarSurfaces does, over an explicit
// dir instead of the cwd's).
//
// Deliberately NOT gated on this launch's loaded packs. A surface no pack composed
// has no host-side file and no baseline, so it is already a no-op twice over —
// while threading the loaded set through teardown would put a second, drift-prone
// answer to "which surfaces exist" next to the manifest's.
func terminateCaptureSurfaces(sidecarDir string) []manifest.Surface {
	var out []manifest.Surface
	for _, s := range surfaceManifest().Surfaces() {
		if surfaceMode(s) == "capture" {
			out = append(out, s)
		}
	}
	entries, err := os.ReadDir(sidecarDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "user-") || !strings.HasSuffix(name, ".overlay.json") {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "user-"), ".overlay.json")
		out = append(out, manifest.Surface{
			Agent: "user", Name: slug, Path: "~/" + unslugHostFilePath(slug),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// jailHomeHostPath resolves a jail-home surface path (`~/.claude/settings.json`) to
// the HOST file backing it for the jail launched from workspace, or ok=false when
// this workspace has no such file.
//
// The two container backends lay the home out differently, so the runtime decides
// rather than a filesystem guess:
//
//   - Apple Container ("container") binds the whole ws_state dir AT /home/agent
//     (`-v wsState:/home/agent`), so `~/X` is `<ws_state>/X` verbatim.
//   - podman binds a :ro GlobalHome base and nests a per-workspace writable overlay
//     per dir, each named with the LEADING DOT STRIPPED
//     (`<ws_state>/claude:/home/agent/.claude`, and the same for .config, .local,
//     .npm-global, go, and every pack-declared writable dir). So `~/.X/rest` is
//     `<ws_state>/X/rest`.
//
// Existence is required, and that is what keeps the rule honest: a path that is not
// one of those binds simply has no file at the derived location and is skipped,
// rather than being captured from somewhere it does not live.
//
// The :ro GlobalHome base and the machine-scope shared dirs are deliberately NOT
// searched. The jail cannot write the :ro base at all, so a "capture" from it would
// record yolo's own old output as a user edit; and no shipped capture surface lives
// in a shared dir (they hold credentials). Declining is free here — the next boot,
// which reads the real jail home, captures either way.
func jailHomeHostPath(workspace, runtime, surfacePath string) (string, bool) {
	rel, ok := strings.CutPrefix(surfacePath, "~/")
	if !ok {
		return "", false // an absolute surface path is not in the jail home
	}
	rel = filepath.FromSlash(rel)
	if runtime != "container" {
		// Strip the leading dot of the FIRST segment only: that is the overlay's
		// name, and the rest of the path is unchanged inside it.
		seg, tail, hasTail := strings.Cut(rel, string(filepath.Separator))
		seg = strings.TrimPrefix(seg, ".")
		rel = seg
		if hasTail {
			rel = filepath.Join(seg, tail)
		}
	}
	path := filepath.Join(workspace, ".yolo", "home", rel)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}
