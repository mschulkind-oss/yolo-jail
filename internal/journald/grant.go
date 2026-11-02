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
// PD-12: the journal bridge is flipped. Its manifest declares `[[grants]]` and no setting, so a
// settings file with no `grants` key, or no settings file at all, grants nothing: every request
// fails closed with the not_allowed text. TRANSITIONAL for macos-log alone, until its flip: a
// settings file with no `grants` key keeps today's behavior, the mode its `full` setting names,
// read once at startup.

// Grant kinds both bridges declare.
const (
	kindRead    = "read"
	kindReadAll = "read_all"
)

// refusalExit is a grant refusal's exit code, the brokers' and `gh`'s (plan PD-7).
const refusalExit = loopholegrant.RefusedExit

// bridge is what one bridge's refusals name: its in-jail client, the pack it ships in, the
// access it gives (the manifest's `access`, used when the grant file does not carry it), and
// the prefix of its own error lines. grantsOnly marks a flipped bridge: with no grant file it
// refuses, where one not yet flipped runs in its settings mode (PD-12).
type bridge struct {
	client, pack, access, prefix string
	grantsOnly                   bool
}

var (
	journalBridge  = bridge{client: "yolo-journalctl", pack: "journal", access: "read the host's journal", prefix: "yolo-journal", grantsOnly: true}
	macosLogBridge = bridge{client: "yolo-log", pack: "macos-log", access: "read the Mac's unified log", prefix: macosLogPrefix}
)

// LoadJournalPolicy is the journal bridge's policy: the settings file the launch handed over
// is its grant file, whatever it holds. A file without a `grants` key grants nothing, and so
// does an empty path; neither falls back to a mode (PD-12, the journal's flip).
func LoadJournalPolicy(settingsPath string) Policy {
	return Policy{GrantFile: settingsPath}
}

// Policy is what a bridge judges each request by.
type Policy struct {
	// Mode is the mode every request runs in when GrantFile is empty, for a bridge not yet
	// flipped (macos-log): today's settings path (PD-12), ModeUser or ModeFull. A flipped
	// bridge never consults it.
	Mode string
	// GrantFile is the grant file, re-read at every request: always the journal's settings
	// file, and macos-log's when it carries a `grants` key. Mode is then never consulted.
	GrantFile string
}

// LoadPolicy is the macos-log bridge's policy until its flip; the journal bridge's is
// LoadJournalPolicy. It reads the settings file the launch handed over (--settings). A file carrying a
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
	if p.GrantFile == "" && !br.grantsOnly {
		return p.Mode, "", 0
	}
	var block *loopholegrant.Block
	if p.GrantFile != "" {
		var err error
		_, block, err = loopholegrant.Read(p.GrantFile)
		if err != nil {
			// The error names a host path: it goes to the host's log, never to the jail.
			logf("[%s] grant file unreadable, request refused: %v", br.pack, err)
			return "", br.prefix + ": this launch's grant file cannot be read, so nothing ran.\n" +
				"  The human restarts the jail, which writes it again.\n", 1
		}
	}
	if block == nil {
		// No grant file, or one with no `grants` key: nothing grants anything.
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
	access := block.Access
	if access == "" {
		access = br.access // a file an older launch wrote carries no `access`
	}
	logf("[%s] refused: %s is %s", br.pack, kindRead, standing)
	return "", loopholegrant.Refusal{
		Client: br.client, Pack: pack, Access: access, Kind: kindRead,
		Dir: block.Workspace, Standing: standing,
	}.Text(), refusalExit
}
