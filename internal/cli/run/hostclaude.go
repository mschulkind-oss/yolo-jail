package run

import (
	"path/filepath"
)

// hostFileArgs mounts each selected agent's yolo-declared host files
// (AgentSpec.HostFiles) read-only at /ctx/host-<agent>/<file>. The set is a
// fixed per-agent constant baked into the agents registry — a CREDENTIAL
// BOUNDARY that no user/workspace config can widen (the retired
// host_claude_files/host_pi_files keys used to; plan §10.4). No config key is
// read, no YOLO_HOST_*_FILES env is emitted, and no scripts referenced by an
// agent's settings.json are auto-discovered: the entrypoint re-derives the
// identical list in-jail from the same baked registry.
func (o *Options) hostFileArgs(in *assembleInput) []string {
	var args []string
	for _, spec := range in.agentSpecs {
		hf := spec.HostFiles
		if hf.Dir == "" {
			continue
		}
		hostDir := filepath.Join(homeDir(), filepath.FromSlash(hf.Dir))
		for _, fname := range hf.Files {
			hostFile := filepath.Join(hostDir, fname)
			if isFile(hostFile) {
				args = append(args, ROFileMountArg(
					hostFile, "/ctx/host-"+spec.Name+"/"+fname, in.wsState,
					"ctx-host-"+spec.Name+"/"+fname, in.mountTargets, nil)...)
			}
		}
	}
	return args
}
