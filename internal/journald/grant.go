package journald

import (
	"encoding/json"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// grant.go is how both bridges judge a request against the project's grant
// (docs/design/loophole-access-approval.md §4.3, §4.6; plan PD-11, PD-12).
//
// The launch writes the grant file, the private settings snapshot it already hands the daemon
// as --settings, with a reserved `grants` key carrying the standing of each kind the manifest
// declares. The daemon reads ONLY that file, never the config, and reads it AT EVERY REQUEST,
// so a `yolo config apply` that rewrites it is taken at the next request, as a broker takes a
// scope at its next call. The mode follows PD-11:
//
//   - `read_all` granted: ModeFull, the client's arguments unchanged;
//   - else `read` granted: ModeUser, the launching user's own entries;
//   - else refuse, exit 64, naming `read` and the step its standing calls for (§5's text,
//     rendered by loopholegrant.Refusal, so the client prints the daemon's words).
//
// PD-12, TRANSITIONAL: a settings file with no `grants` key keeps today's behavior, the mode
// its `full` setting names, read once at startup. The loophole's flip deletes that path.

// Grant kinds both bridges declare.
const (
	kindRead    = "read"
	kindReadAll = "read_all"
)

// refusalExit is a grant refusal's exit code, the brokers' and `gh`'s (plan PD-7).
const refusalExit = 64

// bridge is what one bridge's refusals name: its in-jail client, the pack it ships in, the
// access it gives (the manifest's `access`), and the prefix of its own error lines.
type bridge struct {
	client, pack, access, prefix string
}

var (
	journalBridge  = bridge{client: "yolo-journalctl", pack: "journal", access: "read the host's journal", prefix: "yolo-journal"}
	macosLogBridge = bridge{client: "yolo-log", pack: "macos-log", access: "read the Mac's unified log", prefix: macosLogPrefix}
)

// Policy is what a bridge judges each request by.
type Policy struct {
	// Mode is the mode every request runs in when GrantFile is empty: today's settings path
	// (PD-12), ModeUser or ModeFull.
	Mode string
	// GrantFile is the grant file, re-read at every request. Set when the settings file the
	// launch handed over carries a `grants` key; Mode is then never consulted.
	GrantFile string
}

// LoadPolicy reads the settings file the launch handed over (--settings). A file carrying a
// `grants` key makes a grant policy, decided per request from that file alone; a `grants` key
// that does not read still makes one, so every request fails closed rather than falling back
// to the `full` setting beside it. Without the key it is today's mode (LoadSettings).
func LoadPolicy(settingsPath string) Policy {
	if hasGrantsKey(settingsPath) {
		return Policy{GrantFile: settingsPath}
	}
	return Policy{Mode: LoadSettings(settingsPath)}
}

// hasGrantsKey reports whether the file is a JSON object with the reserved key, whatever its
// value. It parses no grant: loopholegrant.Read is the one reader of the block.
func hasGrantsKey(path string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return false
	}
	_, ok := top[loopholegrant.ReservedKey]
	return ok
}

// decide returns the mode this request runs in, or the refusal text and exit code it gets.
func (p Policy) decide(br bridge) (mode, refusal string, rc int) {
	if p.GrantFile == "" {
		return p.Mode, "", 0
	}
	_, block, err := loopholegrant.Read(p.GrantFile)
	if err != nil {
		// The error names a host path: it goes to the host's log, never to the jail.
		logf("[%s] grant file unreadable, request refused: %v", br.pack, err)
		return "", br.prefix + ": this launch's grant file cannot be read, so nothing ran.\n" +
			"  The human restarts the jail, which writes it again.\n", 1
	}
	if block == nil {
		// The key was there at startup and is gone now: nothing grants anything.
		block = &loopholegrant.Block{}
	}
	switch {
	case block.Kinds[kindReadAll].Standing == loopholegrant.Granted:
		return ModeFull, "", 0
	case block.Kinds[kindRead].Standing == loopholegrant.Granted:
		return ModeUser, "", 0
	}
	standing := block.Kinds[kindRead].Standing
	if !standing.Valid() {
		standing = loopholegrant.NotAllowed // a kind the block does not list is outside the ceiling
	}
	pack := block.Pack
	if pack == "" {
		pack = br.pack
	}
	logf("[%s] refused: %s is %s", br.pack, kindRead, standing)
	return "", loopholegrant.Refusal{
		Client: br.client, Pack: pack, Access: br.access, Kind: kindRead,
		Dir: block.Workspace, Standing: standing,
	}.Text(), refusalExit
}
