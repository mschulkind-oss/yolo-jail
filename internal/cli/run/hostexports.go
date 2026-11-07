package run

// hostexports.go hands `yolo host --` (internal/cli) two of the launcher's own answers, so the
// host notch asks the jail's question rather than writing a second copy of it. Each is a thin
// wrapper and nothing more: the logic stays where the jail launch reads it, and a change there
// reaches the host by construction.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ClaudeSharedCredentialDir is claudeSharedCredentialDir: the home-relative machine-scope
// directory a pack in packs links Claude's credential into through its `shared_credentials` hook,
// or "" when none does. The host notch asks it of the launched program's own pack, to know the
// program is the Claude the credential view is for (CL-D27).
func ClaudeSharedCredentialDir(packs []*packload.Pack) string {
	return claudeSharedCredentialDir(packs)
}

// PromisedEndpoints is promisedEndpoints: the credential loopholes (`detach`, loopback-tls) that
// are active and whose pack may run host code, for runtime rt over cfg's `loopholes` block. The
// host notch asks it whether the claude-oauth-broker may run for a `yolo host -- claude` launch
// (CL-D27), the same predicate a jail launch's brokerLoopholeActive is. It was
// HostScopedEndpoints until HD-R1 retired `host_daemon.scope: "host"`, which keyed the set.
func PromisedEndpoints(rt string, cfg *jsonx.OrderedMap) []string {
	return promisedEndpoints(rt, cfg)
}
