package journald

// macoslog.go is the request policy of the `macos-log` bridge (packs/macos-log): the host
// half that runs Apple's `/usr/bin/log` for a jail and streams its output back over the
// journal bridge's own framing (1=stdout, 2=stderr, 3=exit). It lives in this package
// because everything below the policy — the request header, the frames, the spawn and the
// fronted socket — is the journal bridge's, unchanged (packs/macos-log/README.md).
//
// # Why a bridge at all
//
// The macos-user sandbox runs as the `_yolojail` account, and that account cannot read the
// unified log even with no Seatbelt profile at all: measured on macos-user CI run
// 37940733418, `log show` and `log stream` as `_yolojail` read NOTHING (rc 1) bare, under
// the old "user" profile and under the "off" one, while the runner, an admin, read the store
// bare. So the in-sandbox `yolo-log` wrapper the retired `macos_log` key dialled could never
// work for its "user" or "full" settings; the log has to be read by the host user, here.
//
// # What the user scope is
//
// `full` (user config only) passes the client's arguments to `log` unchanged. The default,
// the user scope, is the macOS counterpart of journalctl's `--user`: entries logged by
// processes running AS THE SANDBOX ACCOUNT, which on this backend is every process every
// macos-user sandbox on the Mac runs. It is enforced on the OUTPUT, never on the arguments,
// so no predicate the client writes can widen it:
//
//   - the bridge forces `--style ndjson` and judges every line (macosLogKeep). A line is sent
//     only if it is a JSON object whose owner is the sandbox account: its `userID` field when
//     the entry carries one, otherwise the live owner of its `processID`. A line it cannot
//     attribute — not JSON, no owner, a process already gone — is DROPPED, so a failure here
//     narrows and never widens.
//   - the arguments are an allowlist (macosLogUserFlags): `show` and `stream`, and only flags
//     that narrow or format. A positional argument is refused because `log show <archive>`
//     reads a host file, as are `--archive` and every verb that writes (`config`, `erase`)
//     or collects (`collect`, `stats`).
//
// ⚠ UNMEASURED: whether a current macOS's ndjson carries `userID`. Without it the owner is the
// live process's, so an entry from a sandbox process that has already exited is dropped; the
// macos-user integration test records which (TestMacosUserMacosLogBridgeScopesToTheSandbox).

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// macosLogBin is Apple's `log`, by absolute path: the host daemon runs with the launching
// user's PATH, and a bridge that resolves a host program by name runs whatever a writable
// PATH entry put first. A var for tests, which point it at a fake.
var macosLogBin = "/usr/bin/log"

// macosLogPrefix names this bridge in every line it writes to the client's stderr.
const macosLogPrefix = "yolo-log"

// macosLogDefault is what an empty request runs: the last five minutes, as the retired
// in-sandbox wrapper did.
var macosLogDefault = []string{"show", "--last", "5m"}

// macosLogVerbs are the `log` subcommands. A first argument that is none of them and starts
// with "-" is a `show` flag, so the bridge prepends `show`, as the retired wrapper did.
var macosLogVerbs = map[string]bool{
	"collect": true, "config": true, "erase": true, "show": true, "stream": true,
	"stats": true, "help": true, "repack": true,
}

// macosLogUserFlags is the user scope's flag allowlist: true for a flag that takes a value.
// Every entry narrows what `log` reads or changes how it prints, and none names a file.
var macosLogUserFlags = map[string]bool{
	"--start": true, "--end": true, "--last": true, "--predicate": true, "--process": true,
	"--style": true, "--color": true, "--timezone": true, "--level": true, "--type": true,
	"--timeout": true,
	"--info":    false, "--debug": false, "--signpost": false, "--backtrace": false,
	"--loss": false, "--source": false, "--no-pager": false, "--mach-continuous-time": false,
}

// macosLogWiden is the sentence every user-scope refusal ends with: the next step.
const macosLogWiden = "The user scope reads only `show` and `stream`, narrowed to the " +
	"sandbox account's processes. To pass arguments to `log` unchanged, a human sets " +
	`"loopholes": {"macos-log": {"settings": {"full": true}}} in ~/.config/yolo-jail/config.jsonc ` +
	"(user config only) and relaunches.\n"

// MacosLogPlan is one request's resolved run: the argv after `log`, and whether stdout goes
// through the user scope's filter.
type MacosLogPlan struct {
	Args     []string
	Filter   bool
	ErrText  string
	ExitCode int
}

// ParseMacosLogRequest decodes a request header and resolves it under mode (ModeUser or
// ModeFull; anything else is ModeUser, the narrow end).
func ParseMacosLogRequest(header []byte, mode string) MacosLogPlan {
	v := decodeArgs(header, macosLogPrefix)
	if v.ErrText != "" {
		return MacosLogPlan{ErrText: v.ErrText, ExitCode: v.ExitCode}
	}
	return PlanMacosLog(v.Args, mode)
}

// PlanMacosLog resolves a validated argument list under mode.
func PlanMacosLog(args []string, mode string) MacosLogPlan {
	if len(args) == 0 {
		args = append([]string(nil), macosLogDefault...)
	} else if !macosLogVerbs[args[0]] && strings.HasPrefix(args[0], "-") {
		args = append([]string{"show"}, args...)
	}
	if mode == ModeFull {
		return MacosLogPlan{Args: args}
	}
	refuse := func(why string) MacosLogPlan {
		return MacosLogPlan{ErrText: macosLogPrefix + ": " + why + "\n  " + macosLogWiden, ExitCode: 2}
	}
	verb := args[0]
	if verb != "show" && verb != "stream" {
		return refuse("`log " + verb + "` is not available in the macos-log bridge's user scope.")
	}
	out := []string{verb}
	for i := 1; i < len(args); i++ {
		a := args[i]
		takesValue, known := macosLogUserFlags[a]
		if !known {
			if strings.HasPrefix(a, "-") {
				return refuse("`" + a + "` is not one of the flags the user scope passes to `log " + verb + "`.")
			}
			return refuse("`" + a + "` is a positional argument, which `log " + verb +
				"` reads as a log archive on the host; the user scope reads only the live system log.")
		}
		if !takesValue {
			out = append(out, a)
			continue
		}
		if i+1 >= len(args) {
			return MacosLogPlan{ErrText: macosLogPrefix + ": `" + a + "` needs a value.\n", ExitCode: 2}
		}
		val := args[i+1]
		i++
		if a == "--style" {
			// The filter reads ndjson and nothing else, so it is the one style the scope
			// prints; asking for it is allowed and changes nothing.
			if val != "ndjson" {
				return refuse("the user scope prints `--style ndjson` only (got `" + val +
					"`), because every line is judged as one JSON entry.")
			}
			continue
		}
		out = append(out, a, val)
	}
	out = append(out, "--style", "ndjson")
	return MacosLogPlan{Args: out, Filter: true}
}

// ownerFields are the two fields of an ndjson entry the user scope attributes it by.
type ownerFields struct {
	// Raw, so a quoted value is not read as a number: a JSON string fails ParseUint.
	UserID    json.RawMessage `json:"userID"`
	ProcessID json.RawMessage `json:"processID"`
}

// macosLogKeep returns the user scope's line filter for the sandbox account's uid. owner
// reports a live process's uid; its answers are cached for ownerTTL, so a stream does not ask
// the kernel once per line, and not longer, so a recycled pid is not trusted for long.
func macosLogKeep(sandboxUID uint32, owner func(pid int) (uint32, bool)) lineFilter {
	type cached struct {
		uid uint32
		ok  bool
		at  time.Time
	}
	var mu sync.Mutex
	cache := map[int]cached{}
	return func(line []byte) bool {
		var f ownerFields
		if err := json.Unmarshal(line, &f); err != nil {
			return false
		}
		if f.UserID != nil {
			uid, err := strconv.ParseUint(string(f.UserID), 10, 32)
			return err == nil && uint32(uid) == sandboxUID
		}
		if f.ProcessID == nil {
			return false
		}
		pid, err := strconv.Atoi(string(f.ProcessID))
		if err != nil || pid <= 0 {
			return false
		}
		mu.Lock()
		c, hit := cache[pid]
		mu.Unlock()
		if !hit || time.Since(c.at) > ownerTTL {
			uid, ok := owner(pid)
			c = cached{uid: uid, ok: ok, at: time.Now()}
			mu.Lock()
			cache[pid] = c
			mu.Unlock()
		}
		return c.ok && c.uid == sandboxUID
	}
}

// ownerTTL bounds how long one pid's owner is trusted.
const ownerTTL = time.Second
