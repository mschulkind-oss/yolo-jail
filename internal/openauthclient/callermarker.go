package openauthclient

// callermarker.go binds the OpenAI auth adapter's CALLER TOKEN into the refresh marker Codex
// holds (docs/plans/notch-convergence.md §2.3, NC-D3; the token is coined in
// docs/reference/wire-bridge.md, WB-D18).
//
// WHY THE MARKER. The adapter (openaiauthadapter) answers Codex's refresh on a loopback port,
// and a loopback port is reachable by every process sharing that loopback: the host itself, a
// jail on `network.mode: host`, a nested podman forced onto `--net=host`. Without a check, the
// broker's stale-caller arm answers any generation marker with the current access and id
// tokens, so anyone who can reach the port can read the user's ChatGPT tokens. Codex cannot be
// told to send a header, but it does send back, unchanged, the refresh token yolo wrote into
// its auth.json. So the writer appends this launch's token to the marker, and the adapter
// refuses a marker that does not carry it before the broker is asked anything.
//
// THE SHAPE is `yolo-broker:<generation>.<token>`: the broker's own marker, unchanged, then a
// dot, then the 64-hex caller token. The broker never sees the suffix — the adapter strips it
// before forwarding and binds the broker's next marker before answering — so the host
// service's wire contract does not move. Pi's marker does not carry it: pi refreshes through
// the authenticated endpoint front, never through this adapter.

import (
	"crypto/subtle"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// BrokerLoopholeName is the loophole whose jail daemon is the adapter, and whose name composes
// the caller token's variable.
const BrokerLoopholeName = "openai-auth-broker"

// CallerTokenEnv is the variable carrying the adapter's per-launch caller token in a jail:
// YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN. The launcher mints it because the loophole's manifest
// declares `jail_daemon.caller_token`, and the per-entry channel hands it to the adapter and to
// the Codex launcher's auth.json writer alike.
var CallerTokenEnv = paths.ServiceCallerTokenEnv(BrokerLoopholeName)

const callerMarkerSep = "."

// BindCallerToken appends token to a broker generation marker. With no token it returns the
// marker unchanged: a launch whose adapter demands none (it is not running) writes the plain
// marker, which is all the broker ever reads.
func BindCallerToken(marker, token string) string {
	if token == "" {
		return marker
	}
	return marker + callerMarkerSep + token
}

// SplitCallerMarker separates a presented refresh token into the broker's marker and the
// caller token bound to it. ok is false when nothing well-formed is bound, so a plain marker, a
// real OpenAI refresh token and garbage all read as "no caller token".
func SplitCallerMarker(presented string) (marker, token string, ok bool) {
	i := strings.LastIndex(presented, callerMarkerSep)
	if i < 0 {
		return presented, "", false
	}
	marker, token = presented[:i], presented[i+len(callerMarkerSep):]
	if !brokerRefreshMarker.MatchString(marker) || !svcendpoint.IsToken(token) {
		return presented, "", false
	}
	return marker, token, true
}

// CallerTokenMatches compares a presented caller token with the one this launch minted, in
// constant time. An empty want never matches.
func CallerTokenMatches(presented, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(want)) == 1
}
