package journald

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// settings.go is this daemon's half of docs/design/pack-config-keys.md: the
// `journal` loophole's manifest declares the keys, yolo validates the user's values
// and writes them to a flat JSON file, and this reads that file ONCE at startup.
//
// # What it replaced, and why the replacement is the security half of OQ-K4
//
// The mode used to arrive as `--mode user|full`, resolved by the run pipeline from a
// TOP-LEVEL `journal` config key. That key had no scope rule of any kind, so
// `"journal": "full"` — read the whole host journal, every unit, every user — was
// settable from a workspace `yolo-jail.jsonc`, which is a file the agent inside the
// jail can rewrite. Making it a declared setting is what gives it a scope
// (docs/design/pack-config-keys.md §5.2, OQ-K4), and `full` is declared
// `scope: "user"`: a workspace may switch the bridge ON, but only the user config may
// widen what it can read.
//
// # It is a BOOLEAN, not the old three-valued string, and that is deliberate
//
// The old vocabulary was `off | user | full` (plus the bool `true` for "user"). Two of
// those three are now said by the loophole's own `enabled` switch — off IS
// `"enabled": false` — so what is left to declare is one bit: may this bridge read
// past the launching user's own journal.
//
// The type system decided the rest. The settings type set is closed —
// `string`, `bool`, `int`, `string_list` — with no `enum`, so a `string` mode could
// carry any word at all and core could not refuse one. That matters here more than
// almost anywhere, because ParseRequest's test is `mode == "user"`: EVERY other
// string, including a typo like "usr" or an empty one, behaves as FULL. A free-string
// mode would therefore have been a config typo that silently widens host access. A
// bool cannot spell itself wrong, and core type-checks it before it is ever written.

// Journal modes — what a request's args are allowed to reach. These are the strings
// ParseRequest takes, not config values: the config says `full: true|false` and this
// package maps it.
const (
	// ModeUser forces `--user` onto every invocation, so journalctl reads only the
	// launching user's own journal. It is the SAFE end, and the fallback for every
	// way of failing to read a settings file.
	ModeUser = "user"
	// ModeFull passes the client's args through unchanged, which needs host journal
	// read access. Reachable only from the user config (`scope: "user"`).
	ModeFull = "full"
)

// LoadSettings reads the flat settings file yolo wrote for this loophole and returns
// the mode the bridge runs in.
//
// EVERY FAILURE RESOLVES TO ModeUser, and the cases share an answer on purpose. An
// absent file, an unreadable one, one that does not parse, one whose `full` is not a
// boolean — none of them is a statement that the operator wanted the whole host
// journal, and the one direction this daemon must never fail in is the widening one.
// It mirrors internal/hostprocesses' `disabled()`: refusing to start would turn a
// transient read failure into a readiness-probe timeout rather than a sentence, while
// guessing "full" would hand the jail a capability nobody wrote down.
//
// Every value here was type-checked against the manifest declaration before it was
// written, so this read is defensive rather than validating.
func LoadSettings(settingsPath string) string {
	if settingsPath == "" {
		return ModeUser
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return ModeUser
	}
	decoded, err := json5.Decode(data)
	if err != nil {
		return ModeUser
	}
	root, isMap := decoded.(*jsonx.OrderedMap)
	if !isMap {
		return ModeUser
	}
	v, ok := root.Get("full")
	if !ok || v == nil {
		return ModeUser
	}
	// A REAL BOOLEAN, never a truthiness coercion. The same rule `default_enabled` and
	// `host_daemon.preamble` state one layer up: a coercion would read the string
	// "false" as true, and here that is the difference between this user's journal and
	// the machine's.
	if b, isBool := v.(bool); isBool && b {
		return ModeFull
	}
	return ModeUser
}
