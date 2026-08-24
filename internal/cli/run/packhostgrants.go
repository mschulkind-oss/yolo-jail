package run

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// packhostgrants.go mounts what a PACK declared it may read from the host — the
// `host_files` and `mount` contribution kinds — read-only under /ctx.
//
// The file was `hostclaude.go` until 2026-08-17, from when this was a per-agent constant
// in the Go registry and claude was the only entry. Nothing in it has been claude-specific
// since: it reads pack declarations and switches on nothing (pack-code-separation.md §3.5).
// The §3.5 ruling said `hostfiles.go`, and that name was already taken by a DIFFERENT
// mechanism — the user's `host_files` config key, which resolves user-scope entries into
// /ctx/host-user/<slug> and is gated by config rather than by pack origin. Two host-file
// paths with one filename between them is exactly the confusion the rename was meant to
// end, so this took the `packhost*` prefix its sibling tests already use.
//
// hostFileArgs mounts each pack's DECLARED host files read-only under /ctx.
//
// THE CREDENTIAL BOUNDARY, and the enforcement moved rather than loosened. It used to
// be a fixed per-agent constant in the Go registry, unwidenable by config (which is what
// retiring host_claude_files/host_pi_files bought). It is now a pack declaration gated
// on the pack's content ORIGIN: an EMBEDDED pack ships with yolo, so its declaration
// carries yolo's own authority — the same authority the Go table had. A FETCHED pack is
// refused, because installing a third-party pack approves distributing content, not
// handing that repository your host config.
//
// Still no config key is read and no YOLO_HOST_*_FILES env is emitted.
func (o *Options) hostFileArgs(in *assembleInput) []string {
	var args []string
	for _, p := range in.packs {
		// HonoredHostFiles enforces the ORIGIN gate: an embedded or local pack may name
		// a host file, a fetched one never can. The refusal is reported at staging time,
		// so reaching here with an empty grant is already accounted for.
		granted, _ := p.HonoredHostFiles()
		for _, hf := range granted {
			hostFile := filepath.Join(homeDir(), filepath.FromSlash(hf.From))
			if !isFile(hostFile) {
				// An absent host file is a NORMAL state (the user has not created it),
				// and the surface falls back to its defaults layer. Mounting a missing
				// source would kill the container with a bare statfs error.
				continue
			}
			// packload.CtxPath is THE definition of where this lands, shared with the
			// entrypoint's host-layer read. Two copies of this derivation would silently
			// compose the wrong user file (or none) into the surface.
			dest := packload.CtxPath(p.Name, hf)
			// APPLE CONTAINER CANNOT BIND A SINGLE FILE (apple/container#1089), and this
			// grant is always exactly one file. Left as a bind it does not error — it
			// silently does not arrive, and the surface then composes from its defaults
			// layer because the entrypoint reads the host layer fail-open
			// (packsurfaces.go hostSurfaceBytes). The user's whole ~/.claude/settings.json
			// disappears from the composition with nothing in the launch to say so —
			// while the disclosure line still prints "reads-host .claude/settings.json",
			// which is the part that makes it worse than an omission.
			//
			// Same answer the other five single-file sites already reached: copy it into
			// wsState (which IS /home/agent on this backend) and tell the entrypoint where
			// to look. The read side was already parameterized for tests; YOLO_CTX_ROOT
			// promotes that seam to a production one.
			if in.rt == "container" {
				acMaterialize(hostFile, filepath.Join(acCtxDirRel,
					filepath.FromSlash(strings.TrimPrefix(dest, packload.CtxRoot+"/"))), in.wsState)
				in.acCtxMaterialized = true
				continue
			}
			args = append(args, ROFileMountArg(
				hostFile, dest, in.wsState,
				"ctx-"+strings.ReplaceAll(strings.TrimPrefix(dest, "/ctx/"), "/", "-"),
				in.mountTargets, nil)...)
		}
	}
	return args
}

// acCtxDirRel is where Apple Container's materialized /ctx host-file copies live,
// relative to the home (= wsState on that backend). A dotted name because it sits in
// the agent's home rather than in a mount namespace it cannot see.
const acCtxDirRel = ".yolo-ctx"

// hostMountArgs mounts each pack's DECLARED `mount` contributions read-only under
// /ctx. Same credential boundary as hostFileArgs (HonoredMounts applies the origin
// gate — a fetched pack is refused), but the source may be a whole DIRECTORY and the
// destination is the pack's chosen /ctx path rather than a config-surface feed.
//
// A directory is mounted directly (a dir source is not the single-file nested-bind
// case ROFileMountArg guards against); a single-file mount reuses ROFileMountArg so
// the nested-jail inode-copy dance still applies. An absent source is skipped rather
// than mounted — a missing bind source kills the container with a bare statfs error.
//
// On Apple Container BOTH forms are dropped with a reason, via roBindsUnsupported —
// the same rule the config `mounts` key has always applied, reached from here at last.
// A `mount` is the one host grant with no relocation available: `reads-host` and
// `host_files` materialize into .yolo-ctx because their reader is the ENTRYPOINT,
// which YOLO_CTX_ROOT can redirect. This grant's reader is the AGENT, following the
// /ctx path its own briefing names, so there is nowhere else to put it.
func (o *Options) hostMountArgs(in *assembleInput) []string {
	var args []string
	for _, p := range in.packs {
		granted, _ := p.HonoredMounts()
		for _, mt := range granted {
			src := filepath.Join(homeDir(), filepath.FromSlash(mt.From))
			dest := "/ctx/" + strings.TrimPrefix(mt.To, "/")
			if reason := roBindsUnsupported(in.rt); reason != "" && (isDir(src) || isFile(src)) {
				o.pr(o.Stdout).print("[yellow]Skipping pack " + p.Name + " mount ~/" +
					mt.From + " → " + dest + ": " + reason + "[/yellow]")
				continue
			}
			switch {
			case isDir(src):
				args = append(args, "-v", src+":"+dest+":ro")
			case isFile(src):
				args = append(args, ROFileMountArg(
					src, dest, in.wsState,
					"ctx-"+strings.ReplaceAll(strings.TrimPrefix(dest, "/ctx/"), "/", "-"),
					in.mountTargets, nil)...)
			default:
				// Absent source: skip. The pack's content simply is not present; a
				// missing bind source would otherwise abort the container start.
				o.pr(o.Stdout).print("[yellow]Warning: pack " + p.Name + " mount source " +
					"does not exist, skipping: ~/" + mt.From + "[/yellow]")
			}
		}
	}
	return args
}
