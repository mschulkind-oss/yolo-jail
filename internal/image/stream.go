package image

import "strings"

// LinuxBuilderFromMachines parses the /etc/nix/machines contents and returns the
// first builder whose second field names a linux system, as (builderURI,
// sshHost, true). Returns ("", "", false) when no linux builder line is present.
// Mirrors the parse loop in _stream_image_command: skip blank/`#` lines, require
// >=2 fields, match "linux" in field[1]; sshHost strips the ssh-ng:// / ssh://
// scheme from field[0].
//
// The macOS-only orchestration around this (nix copy to the builder, then
// running the store-path script over ssh, with local-execution fallbacks) stays
// in the run/image wiring; this is the byte-exact selection logic.
func LinuxBuilderFromMachines(machinesText string) (builderURI, sshHost string, ok bool) {
	for _, raw := range strings.Split(machinesText, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 && strings.Contains(parts[1], "linux") {
			uri := parts[0]
			host := strings.Replace(uri, "ssh-ng://", "", 1)
			host = strings.Replace(host, "ssh://", "", 1)
			return uri, host, true
		}
	}
	return "", "", false
}
