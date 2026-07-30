package run

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

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
			args = append(args, ROFileMountArg(
				hostFile, dest, in.wsState,
				"ctx-"+strings.ReplaceAll(strings.TrimPrefix(dest, "/ctx/"), "/", "-"),
				in.mountTargets, nil)...)
		}
	}
	return args
}

// hostMountArgs mounts each pack's DECLARED `mount` contributions read-only under
// /ctx. Same credential boundary as hostFileArgs (HonoredMounts applies the origin
// gate — a fetched pack is refused), but the source may be a whole DIRECTORY and the
// destination is the pack's chosen /ctx path rather than a config-surface feed.
//
// A directory is mounted directly (a dir source is not the single-file nested-bind
// case ROFileMountArg guards against); a single-file mount reuses ROFileMountArg so
// the nested-jail inode-copy dance still applies. An absent source is skipped rather
// than mounted — a missing bind source kills the container with a bare statfs error.
func (o *Options) hostMountArgs(in *assembleInput) []string {
	var args []string
	for _, p := range in.packs {
		granted, _ := p.HonoredMounts()
		for _, mt := range granted {
			src := filepath.Join(homeDir(), filepath.FromSlash(mt.From))
			dest := "/ctx/" + strings.TrimPrefix(mt.To, "/")
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
