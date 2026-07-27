package run

import (
	"path/filepath"
	"strings"
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
			dest := hf.To
			if dest == "" {
				dest = "host-" + p.Name + "/" + filepath.Base(hf.From)
			}
			args = append(args, ROFileMountArg(
				hostFile, "/ctx/"+dest, in.wsState,
				"ctx-"+strings.ReplaceAll(dest, "/", "-"), in.mountTargets, nil)...)
		}
	}
	return args
}
