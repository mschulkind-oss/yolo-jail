package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// hostApplyOnLaunchKey is the top-level opt-in for re-rendering the host at a wrapped launch.
const hostApplyOnLaunchKey = "host_apply_on_launch"

// HostApplyOnLaunchEnabled reports whether the user has opted in to having
// `yolo host -- <bin>` check its own render before exec'ing
// (docs/design/host-apply-staleness.md §4.2).
//
// # What the key does, and what it deliberately does NOT do
//
// `yolo host apply` writes into the real $HOME once and nothing ever looks again, so the
// rendered and would-be-rendered states drift apart silently. Every generated wrapper already
// execs `yolo host -- <bin>`, and that is the only moment the content matters — agents read
// their config at startup and do not reload it. With this key on, that launch behaves like a
// jail launch: a re-apply that would change nothing execs silently, and one that would change
// something prompts on a TTY and refuses without one.
//
// > [!IMPORTANT]
// > THE KEY ENABLES THE MECHANISM. IT DOES NOT GRANT THE APPROVAL (§1 P4). A launch under an
// > enabled key still prompts on a TTY and still refuses off one. Treating the key as blanket
// > pre-authorization was proposed and REFUSED: a key is read on every launch forever with no
// > act of granting, which is exactly the standing consent
// > snapshot.go's AcceptConfigChangesFlag exists to prevent. The per-launch approval is an act
// > — a flag on the explicit apply, or the environment variable the wrapper path takes because
// > no flag can reach it.
//
// # It is read from the USER config directly, and that is the security boundary
//
// The same construction `host_wrappers` and `host_files` use, for the same reason and it is
// sharper here: this key licenses yolo to WRITE INTO THE REAL $HOME as a side effect of
// launching an agent. Of the places a config key can come from, two are jail-writable — the
// workspace `yolo-jail{,.local}.jsonc` (/workspace is bind-mounted rw, so an agent can edit
// it) and `<workspace>/.yolo/config-assembled.json`. Reading this key from the merged config
// would therefore let a cloned repository arrange for its own `packs` to be rendered into the
// user's home the next time they typed `claude`. Reading user scope directly makes workspace
// scope INEXPRESSIBLE rather than merely refused; validateHostApplyOnLaunch's workspace-scope
// error is defense-in-depth against a silent no-op, not the boundary itself.
//
// A user config that cannot be read or parsed yields false: an opt-in nobody could read has
// not been given, and defaulting a write claim to ON when the file is broken is the wrong
// direction to fail in.
func HostApplyOnLaunchEnabled() bool {
	return hostApplyOnLaunchValue(UserScopeConfigOrEmpty())
}

// hostApplyOnLaunchValue reads the key out of an already-loaded config map. Split out so the
// validator and the tests can exercise the same reading without touching the real home.
func hostApplyOnLaunchValue(cfg *jsonx.OrderedMap) bool {
	v, present := cfg.Get(hostApplyOnLaunchKey)
	if !present || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}
