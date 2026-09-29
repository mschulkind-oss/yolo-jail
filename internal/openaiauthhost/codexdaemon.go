package openaiauthhost

// codexdaemon.go is OQ-CDX1's host arm (docs/research/codex-background-service.md §5, ruled
// 2026-09-29): Codex's background server stays OFF in a `yolo host -- codex` launch.
//
// THE DAEMON is Codex's word-for-word "background Codex service": since Codex 0.157 an
// interactive `codex` starts, or attaches to, a second copy of itself, detached with setsid, and
// becomes a front end to it (the doc's §2). A managed launch cannot share one. This launch's
// refresh adapter listens on a port the launch picks and closes when the agent exits, and a
// daemon inherits the environment of the launch that STARTED it, so it keeps posting refreshes
// to that first launch's closed port — carrying whatever marker, with whatever launch's caller
// token, auth.json holds by then (the doc's §3.3). So three things, all confined to yolo's
// managed CODEX_HOME and never the user's own ~/.codex (OQ-CDX2: a Codex the user runs
// directly is never touched):
//
//  1. the managed config.toml carries `features.daemon_auto_start = false` (writeManagedCodexConfig),
//     which stops the TUI's own start;
//  2. the launch passes `--no-daemon` (Launch.Argv), because that key does NOT stop the TUI
//     attaching to a daemon an earlier launch left running (tui/src/startup_orchestration.rs:
//     the socket is probed whenever no exclusion applies, whatever the feature says), and does
//     not stop `codex agents` starting one (noDaemonRefusers says why that gets the flag too);
//  3. the updater is turned off in the home's daemon settings, which a running loop rereads at
//     its next wake and an explicit start reads before starting one, and the leftovers an
//     earlier launch started are shut down once (retireManagedDaemon): a running server or
//     updater the home's own records name is stopped now, then the daemon's package copy is
//     removed once nothing it records is running.
//
// Every Codex fact here was read from Codex's source at rust-v0.159.0, unchanged in 0.159.1; no
// Codex was run.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const (
	// noDaemonFlag runs one Codex launch with an in-process server, "even if it is already
	// running" (codex-rs/tui/src/cli.rs). It is a root option of the `codex` binary (TuiCli is
	// flattened into the root parser, codex-rs/cli/src/main.rs), which is why Argv puts it
	// straight after argv[0], where every subcommand's root options go.
	noDaemonFlag = "--no-daemon"
	// daemonAutoStartFeature is the `[features]` key that stops an interactive codex STARTING the
	// daemon (codex-rs/features/src/lib.rs, Feature::DaemonAutoStart, stable, on by default).
	daemonAutoStartFeature = "daemon_auto_start"
)

// noDaemonRefusers are the argv words with which Codex REFUSES a root `--no-daemon` and which
// withoutDaemon therefore leaves it off, read from codex-rs at rust-v0.159.0: `codex queue`
// without a remote (tui/src/session_queue_commands.rs), and `--remote` (noDaemonRemoteFlag),
// with the interactive TUI, resume and fork (tui/src/startup_orchestration.rs) and with archive,
// unarchive and delete (tui/src/session_archive_commands.rs). Neither STARTS a background server:
// queue only attaches to one already running, and --remote names another. Every other subcommand
// either honors the flag (resume, fork, archive, unarchive, delete) or never reads it (exec,
// review, login, mcp, app-server, …), and clap accepts a root option before any of them.
//
// `codex agents` IS NOT HERE, though Codex refuses the flag with it too ("--no-daemon cannot be
// used with codex agents", cli/src/main.rs run_interactive_tui). Without a remote, the agents
// overview starts the background server itself, calling the daemon's start directly, and no
// feature is read on that path — the TUI's auto-start, the one reader of daemon_auto_start,
// excludes the overview (tui/src/startup_orchestration.rs). Leaving the flag off it would start a
// server in the managed home that outlives the launch with this launch's refresh address, which
// is what OQ-CDX1 rules out ("You lose codex queue, codex agents"). So the managed launch hands it
// the flag and Codex's own refusal, which starts nothing, says the overview needs a shared
// server. `codex agents --remote …` starts no local server, and --remote keeps it unchanged.
//
// The match is on ANY word of the argv, not on the parsed subcommand, so a prompt that is
// exactly "queue" also goes without the flag. That errs toward Codex's own default for that one
// launch, which the managed config's key already keeps from starting a daemon; the other
// direction would be an error the user never asked for.
var noDaemonRefusers = []string{"queue"}

// noDaemonRemoteFlag is Codex's flag naming another app server; with it `--no-daemon` is refused.
const noDaemonRemoteFlag = "--remote"

// withoutDaemon is argv with `--no-daemon` added after argv[0], and whether it added it: not when
// the flag is already there (as `--no-daemon` or `--no-daemon=…`, packload.hasFlag's rule), and
// not beside a word Codex refuses it with that starts no server (noDaemonRefusers).
func withoutDaemon(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return argv, false
	}
	for _, a := range argv[1:] {
		if a == noDaemonFlag || strings.HasPrefix(a, noDaemonFlag+"=") {
			return argv, false
		}
		if a == noDaemonRemoteFlag || strings.HasPrefix(a, noDaemonRemoteFlag+"=") {
			return argv, false
		}
		for _, word := range noDaemonRefusers {
			if a == word {
				return argv, false
			}
		}
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[0], noDaemonFlag)
	return append(out, argv[1:]...), true
}

// Argv is the managed launch's rewrite of the command it runs, and the disclosure of it (nil when
// nothing changed). Only a managed Codex launch rewrites anything: it adds `--no-daemon`
// (withoutDaemon). A pi launch, and a nil launch, return argv untouched.
//
// The disclosure is the jail's argv-rewrite wording (packload.LaunchInjection.DisclosureLines):
// the verdict, the two argvs one above the other, and who added the flag. A launch has no quiet
// mode (OQ-RO3), so it is printed on every launch that rewrites.
func (l *Launch) Argv(argv []string) ([]string, []string) {
	if l == nil || !l.noDaemon {
		return argv, nil
	}
	out, added := withoutDaemon(argv)
	if !added {
		return argv, nil
	}
	return out, []string{
		"yolo CHANGED the command you asked for:",
		"  you asked for: " + shquote.Join(argv),
		"  yolo will run: " + shquote.Join(out),
		"  added by yolo's managed Codex launch: " + noDaemonFlag +
			" (a Codex launch yolo manages never uses Codex's background server)",
	}
}

// setDaemonAutoStartOff sets `features.daemon_auto_start = false` in a decoded Codex config,
// keeping every other `[features]` key, whatever the user's own config said about this one.
func setDaemonAutoStartOff(root map[string]any) {
	features, ok := root["features"].(map[string]any)
	if !ok {
		features = map[string]any{}
		root["features"] = features
	}
	features[daemonAutoStartFeature] = false
}

// The daemon's files under a CODEX_HOME (codex-rs/app-server-daemon/src/lib.rs at rust-v0.159.0):
// its state directory, the settings file in it, and the pid records, current and legacy.
const (
	daemonStateDir     = "app-server-daemon"
	daemonSettingsFile = "settings.json"
	// daemonPackagesDir is the daemon's own copy of Codex, about 424 MiB per release, selected by
	// a `current` link (prepare_install.rs). With the daemon off nothing runs it.
	daemonPackagesDir = "packages/app-server-daemon"
)

// daemonRecord is one pid record the daemon keeps, and whether it names the updater loop (whose
// whole process group Codex signals, backend/pid.rs) rather than the server.
type daemonRecord struct {
	file    string
	updater bool
}

var daemonRecords = []daemonRecord{
	{"daemon.pid", false},
	{"app-server.pid", false},
	{"daemon-updater.pid", true},
	{"app-server-updater.pid", true},
}

// daemonProcs is how the retirement reads and signals a process; a struct of funcs so a test
// can stand in for ps while signalling a real child.
type daemonProcs struct {
	// alive reports whether pid names a process at all: kill(pid, 0) succeeds or says EPERM.
	alive func(pid int) bool
	// facts returns what ps reports for pid: its start time exactly as `ps -o lstart=` prints it,
	// which is what Codex records as processStartTime (backend/pid.rs, read_process_details), and
	// its command line.
	facts func(pid int) (start, command string, err error)
	// pgid is pid's process group.
	pgid func(pid int) (int, error)
	// signal delivers sig to pid, or to process group -pid.
	signal func(pid int, sig syscall.Signal) error
}

func realDaemonProcs() daemonProcs {
	return daemonProcs{
		alive: func(pid int) bool {
			err := syscall.Kill(pid, 0)
			return err == nil || errors.Is(err, syscall.EPERM)
		},
		facts: func(pid int) (string, string, error) {
			ps := func(field string) (string, error) {
				out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", field+"=").Output()
				return strings.TrimSpace(string(out)), err
			}
			start, err := ps("lstart")
			if err != nil {
				return "", "", err
			}
			command, err := ps("command")
			return start, command, err
		},
		pgid:   syscall.Getpgid,
		signal: syscall.Kill,
	}
}

// recordState is what one pid record says once checked.
type recordState int

const (
	// recordStale: no process has the pid, or the one that has it is not Codex's app server
	// (its command line says so). Nothing to stop, and nothing running from the package copy.
	recordStale recordState = iota
	// recordRunning: the recorded daemon process is running: an app server, started at the
	// recorded time.
	recordRunning
	// recordUnknown: a process has the pid and yolo cannot tell whether it is the recorded one —
	// ps could not describe it, or it is an app server whose start time is not the recorded one.
	// Not stopped, and it holds the package copy in place: "unreferenced" and "could not ask" are
	// not one answer.
	recordUnknown
)

// checkRecord reads one pid record and says what it names, and for recordUnknown why.
//
// A start time that differs is recordUnknown, never recordStale, while the command line is an
// app server's. `ps -o lstart=` prints local time in the caller's locale, and Codex recorded it
// from the environment of whichever launch started the daemon, so a different time zone, locale
// or clock step makes a live daemon's time differ from its record. Codex's own check says
// "cannot verify … PID record retained" there rather than calling the record stale
// (backend/pid.rs, process_matches_record, rust-v0.159.0). Calling it stale removed the package
// copy from under a server still running from it.
func checkRecord(path string, procs daemonProcs) (int, recordState, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, recordStale, ""
	}
	var rec struct {
		PID              int    `json:"pid"`
		ProcessStartTime string `json:"processStartTime"`
	}
	if json.Unmarshal(data, &rec) != nil || rec.PID <= 1 || strings.TrimSpace(rec.ProcessStartTime) == "" {
		return 0, recordStale, ""
	}
	if !procs.alive(rec.PID) {
		return rec.PID, recordStale, ""
	}
	start, command, err := procs.facts(rec.PID)
	if err != nil {
		return rec.PID, recordUnknown, "ps could not say whether it is still Codex's background server"
	}
	if !strings.Contains(command, "app-server") {
		return rec.PID, recordStale, ""
	}
	if start != strings.TrimSpace(rec.ProcessStartTime) {
		return rec.PID, recordUnknown, "it is an app server whose start time is not the recorded one, " +
			"which a different time zone, locale or clock can cause as well as a reused pid"
	}
	return rec.PID, recordRunning, ""
}

// retireManagedDaemon shuts down, once, what Codex's daemon left in the managed CODEX_HOME
// (this file's comment, item 3). Nothing here can refuse the launch: every failure is a line on
// stderr and the launch goes on, since --no-daemon already keeps this launch off any daemon.
//
// alone is whether this is the only live launch of the home (sharedCallerToken): a process is
// stopped, and the package copy removed, only then, so an older yolo's session still attached to
// its daemon is never cut off mid-turn. The settings write needs no such guard: it touches only
// the updater, and a later launch that is alone does the rest.
//
// The settings are written whether or not a daemon ever ran here, making the directory when it is
// missing: the config key and --no-daemon stop only the TUI, and an explicit start —
// `codex app-server daemon start`, which reads no feature — seeds the daemon and then starts its
// updater unless these settings say otherwise (app-server-daemon/src/lib.rs, start and
// ensure_managed_updater). Written first, they keep that updater from ever starting.
func retireManagedDaemon(home string, alone bool, procs daemonProcs, stderr io.Writer) {
	state := filepath.Join(home, daemonStateDir)
	settings := filepath.Join(state, daemonSettingsFile)
	switch changed, err := turnUpdaterOff(settings); {
	case err != nil:
		fmt.Fprintf(stderr, "yolo host: could not turn Codex's background updater off in %s: %v\n", settings, err)
	case changed:
		fmt.Fprintf(stderr, "yolo host: turned Codex's background updater off in yolo's managed Codex home (%s)\n", settings)
	}
	if !alone {
		return
	}
	holding := false
	for _, r := range daemonRecords {
		pid, st, why := checkRecord(filepath.Join(state, r.file), procs)
		switch st {
		case recordUnknown:
			holding = true
			fmt.Fprintf(stderr, "yolo host: left pid %d alone: %s in yolo's managed Codex home names it, and %s. "+
				"If it is Codex's, `kill %d` stops it, and a later launch removes its copy\n", pid, r.file, why, pid)
		case recordRunning:
			holding = true
			what := "background server"
			target := pid
			if r.updater {
				what = "background updater"
				// Codex signals the updater's whole group, which it leads (setsid): its installer
				// runs as a child. A pid that does not lead its group gets the signal alone.
				if g, err := procs.pgid(pid); err == nil && g == pid {
					target = -pid
				}
			}
			if err := procs.signal(target, syscall.SIGTERM); err != nil {
				if !errors.Is(err, syscall.ESRCH) { // ESRCH: it exited since the check
					fmt.Fprintf(stderr, "yolo host: could not stop Codex's %s (pid %d) in yolo's managed Codex home: %v\n",
						what, pid, err)
				}
				continue
			}
			fmt.Fprintf(stderr, "yolo host: stopped Codex's %s (pid %d), which an earlier launch left running in "+
				"yolo's managed Codex home; a launch yolo manages keeps it off\n", what, pid)
		}
	}
	if holding {
		return // the copy goes at a later launch, once nothing it records is running
	}
	packages := filepath.Join(home, daemonPackagesDir)
	if _, err := os.Lstat(packages); err != nil {
		return
	}
	size := treeSize(packages)
	if err := os.RemoveAll(packages); err != nil {
		fmt.Fprintf(stderr, "yolo host: could not remove Codex's background-server copy %s: %v\n", packages, err)
		return
	}
	fmt.Fprintf(stderr, "yolo host: removed Codex's background-server copy from yolo's managed Codex home "+
		"(%s, %d MiB); nothing yolo launches runs it\n", packages, size>>20)
}

// turnUpdaterOff merges {"updater":{"autoUpdateEnabled":false}} into the daemon's settings file,
// keeping every other key, and reports whether it wrote. Codex's updater loop rereads this file
// at each wake and exits before installing, a start reads it before starting the loop, and
// Codex's own settings writer keeps a key it does not manage (codex-rs/app-server-daemon/src/
// settings.rs, update_loop.rs, lib.rs). A file that is not a JSON object, or whose `updater` is
// not one, is left exactly as it is. A missing directory is made 0700, like the managed home.
func turnUpdaterOff(path string) (bool, error) {
	root := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return false, fmt.Errorf("it is not JSON (%v), so it was left as it is", err)
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return false, errors.New("it is not a JSON object, so it was left as it is")
		}
		root = obj
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	updater := map[string]any{}
	if raw, present := root["updater"]; present {
		obj, ok := raw.(map[string]any)
		if !ok {
			return false, errors.New(`its "updater" is not a JSON object, so it was left as it is`)
		}
		updater = obj
	}
	if updater["autoUpdateEnabled"] == false {
		return false, nil
	}
	updater["autoUpdateEnabled"] = false
	root["updater"] = updater
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := atomicWritePrivate(path, append(out, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

// treeSize is the bytes of the regular files under root, for the removal's line.
func treeSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
