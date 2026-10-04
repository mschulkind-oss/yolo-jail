package hostprocesses

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SelfCheck is the `yolo doctor` health check, run through the loophole's
// `doctor_cmd`.
//
// It reports on the SETTINGS FILE the daemon would actually read — the same file,
// resolved from the same {settings} token, that the daemon's argv names. It used to
// hunt for a yolo-jail.jsonc via $YOLO_HOST_PROCESSES_CONFIG or the cwd, which meant
// the doctor's answer and the daemon's behaviour came from two different searches and
// could disagree about which file was in force. There is one file now, and the
// caller names it.
//
// # A MISSING file is not a failure, and getting that wrong would be loud
//
// yolo writes the settings file when a jail LAUNCHES this loophole, so on a machine
// that has not launched one yet the file is simply absent — the normal state of a
// fresh install, and `yolo check` runs there. Reporting it as FAIL would put a red
// line under every fresh machine for a condition the user cannot act on and that the
// very next `yolo` invocation fixes.
//
// What IS a failure is a file that exists and does not parse: the daemon collapses
// that to an empty allowlist and keeps running, so `yolo-ps` would show nothing while
// everything looked healthy. That is the one thing this check can see that nothing
// else reports.
//
// # It also asks the host's ps the daemon's own first question
//
// The settings file is half of what the daemon needs; the host's ps is the other half,
// and it is the half that failed silently. On macOS this loophole used to start,
// publish, pass the reachability witness and then fail every call, because nothing had
// ever asked the Mac's ps a GNU question, and this check said OK throughout because all
// it read was the settings file. So psCheck runs, against the ps on PATH, the query the
// daemon's list mode is built on in the dialect this host gets, and passes only if the
// answer names THIS process: "ps ran" is not "ps answered".
//
// Exit codes: 0 for every knowable state, 1 for a settings file that is present and
// unreadable as JSON, or a ps that cannot answer the daemon's queries.
func SelfCheck(settingsPath string) int {
	return selfCheck(settingsPath, dialectFor(hostOS), os.Stdout)
}

func selfCheck(settingsPath string, d dialect, out io.Writer) int {
	return max(settingsCheck(settingsPath, out), psCheck(d, out))
}

func settingsCheck(settingsPath string, out io.Writer) int {
	if settingsPath == "" {
		// No path means the daemon was run by hand rather than through the manifest.
		// Not a fault — there is simply nothing to report on.
		fmt.Fprintln(out, "OK: daemon present; no settings file in scope "+
			"(pass --settings to check one)")
		return 0
	}
	if !isFile(settingsPath) {
		fmt.Fprintln(out, "OK: daemon present; no settings resolved yet at "+settingsPath+
			" — yolo writes it when a jail launches this loophole")
		return 0
	}
	cfg, ok := loadSettings(settingsPath)
	if !ok {
		fmt.Fprintln(out, "FAIL: settings file at "+settingsPath+" is not readable JSON — the "+
			"daemon would start with an EMPTY allowlist and look healthy while yolo-ps "+
			"showed nothing")
		return 1
	}
	if len(cfg.Visible) == 0 {
		fmt.Fprintln(out, "OK: settings at "+settingsPath+
			" allowlist no process names (loopholes.host-processes.settings.visible is empty)")
		return 0
	}
	fmt.Fprintln(out, "OK: "+strconv.Itoa(len(cfg.Visible))+" comms allowlisted at "+settingsPath)
	return 0
}

// psCheck asks the host's ps the question list mode depends on and grades the answer:
//
//   - BSD: the list snapshot itself (bsdListSnapshotArgv), read by the parser list mode
//     uses, must list this process with a name.
//   - GNU: `ps -o pid= -C <this process's comm>` must print this pid. -C is the
//     selector GNU list mode is built on, and the comm comes from /proc, which pid and
//     tree mode read.
func psCheck(d dialect, out io.Writer) int {
	where := "no ps on PATH"
	if p, err := exec.LookPath("ps"); err == nil {
		where = p
	}
	self := strconv.Itoa(os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), psDeadlineSeconds*time.Second)
	defer cancel()

	if d == bsdPS {
		argv := bsdListSnapshotArgv
		if why := bsdAnswers(ctx, argv, os.Getpid()); why != "" {
			fmt.Fprintln(out, "FAIL: the host's ps ("+where+") did not list this process for "+
				pyReprStrList(argv)+" ("+why+"), so yolo-ps would match no name on this Mac")
			fmt.Fprintln(out, "The daemon runs the first ps on PATH, and macOS's own is /bin/ps. "+
				"Put /bin ahead of any other ps on yolo's PATH, then rerun `yolo check`.")
			return 1
		}
		fmt.Fprintln(out, "OK: the host's ps ("+where+") answers the BSD queries the daemon runs on macOS")
		return 0
	}

	comm, ok := linuxComm(self)
	if !ok {
		fmt.Fprintln(out, "FAIL: /proc/"+self+"/comm is unreadable, and the daemon names a "+
			"process from /proc in pid and tree mode")
		fmt.Fprintln(out, "Run `yolo` on a host with procfs mounted at /proc, then rerun `yolo check`.")
		return 1
	}
	argv := []string{"ps", "-o", "pid=", "-C", comm}
	if why := gnuAnswers(ctx, argv, self); why != "" {
		fmt.Fprintln(out, "FAIL: the host's ps ("+where+") did not answer "+pyReprStrList(argv)+
			" with this process ("+why+"), and yolo-ps list mode is built on GNU procps' -C")
		fmt.Fprintln(out, "Install procps (GNU ps) and put it first on yolo's PATH, then rerun `yolo check`.")
		return 1
	}
	fmt.Fprintln(out, "OK: the host's ps ("+where+") answers the GNU procps queries the daemon runs")
	return 0
}

// bsdAnswers runs a BSD snapshot and says why it is unusable, or "" when it lists pid
// with a name.
func bsdAnswers(ctx context.Context, argv []string, pid int) string {
	run, err := runPS(ctx, psDeadlineSeconds, argv)
	if err != nil {
		return err.Error()
	}
	for _, p := range parseBSDSnapshot(run.stdout, false) {
		if p.pid == pid && p.comm != "" {
			return ""
		}
	}
	return unanswered(run)
}

// gnuAnswers runs a `ps -o pid= …` selection and says why it is unusable, or "" when
// its answer includes pid.
func gnuAnswers(ctx context.Context, argv []string, pid string) string {
	run, err := runPS(ctx, psDeadlineSeconds, argv)
	if err != nil {
		return err.Error()
	}
	for _, f := range strings.Fields(string(run.stdout)) {
		if f == pid {
			return ""
		}
	}
	return unanswered(run)
}

// unanswered describes a ps that ran and did not name this process.
func unanswered(run psRun) string {
	why := "exit " + strconv.Itoa(run.rc) + ", and this process was not in its answer"
	if msg := strings.TrimSpace(string(run.stderr)); msg != "" {
		why += ": " + msg
	}
	return why
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}
