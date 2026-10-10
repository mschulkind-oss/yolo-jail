//go:build darwin

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserMacosLogBridgeScopesToTheSandbox is the macos-log loophole on macos-user, end to
// end (packs/macos-log/README.md): the launch stages a darwin `yolo-log` into the sandbox because
// the session env carries the loophole's endpoint, and the client, run by the agent inside the
// Seatbelt profile, reads the Mac's unified log through the host daemon — which runs
// /usr/bin/log as the runner, an admin, since the sandbox account cannot read the log itself
// (TestMacosUserMacosLogAsTheSandboxAccountMeasurement).
//
// TWO ENTRIES, ONE PER ACCOUNT, logged before the launch: one as the sandbox account (sudo -u),
// one as the runner. `show` in the user scope attributes an entry by its own `userID` alone (a
// pid's owner today says nothing about history), so it must never return the runner's entry,
// and returns the sandbox's only if this macOS's ndjson carries `userID` — recorded as a
// MEASUREMENT, and asserted when it does. `full` must return both.
//
// THEN A LIVE STREAM: the sandbox starts `yolo-log stream`, logs a third entry from inside the
// sandbox with a process that stays alive, and kills the client. The stream must deliver that
// entry, by `userID` or by its live process's owner. Killing the client is also the abandoned-
// stream path: the bridge must then stop its `log stream`. A user-scope `show` of the live entry
// afterwards separates "never logged" from "logged, not streamed" when the stream misses it.
//
// MEASURED (CI run 37986991379): this macOS's ndjson carries `userID` (the sandbox's entry
// came back from `show` as `"userID":600`) and spells `timestamp` as
// "2026-10-09 22:16:08.389816+0000", the layout macoslog.go's parseEntryTime reads.
//
// Darwin-only by build constraint, like the serial test: requireMacosUser skips everywhere else.
func TestMacosUserMacosLogBridgeScopesToTheSandbox(t *testing.T) {
	requireMacosUser(t)
	token := fmt.Sprintf("yolo-it-maclog-%d-%d", os.Getpid(), time.Now().UnixNano())
	mine, theirs := token+"-sandbox", token+"-host"
	syslogAs(t, true, mine)
	syslogAs(t, false, theirs)

	script := strings.Join([]string{
		`echo "=== CLIENT ==="`,
		`command -v yolo-log || echo MISSING`,
		`echo "=== SHOW ==="`,
		`yolo-log show --last 10m --predicate 'eventMessage CONTAINS "` + token + `"'; echo "SHOW_RC=$?"`,
		`echo "=== STREAM ==="`,
		`f=$(mktemp /tmp/yolo-it-ylog.XXXXXX)`,
		`yolo-log stream --predicate 'eventMessage CONTAINS "` + token + `"' >"$f" 2>&1 & p=$!`,
		`sleep 5`, // the client dials the bridge and the bridge starts `log stream` first
		`/usr/bin/perl -MSys::Syslog -e 'openlog("yolo-it", "pid", "user"); syslog("notice", "%s", $ARGV[0]); closelog(); sleep 6' ` + token + `-live & q=$!`,
		`sleep 4; kill "$p" 2>/dev/null; wait "$p" 2>/dev/null; kill "$q" 2>/dev/null; cat "$f"; rm -f "$f"`,
		`echo "=== LIVESHOW ==="`,
		`yolo-log show --last 5m --predicate 'eventMessage CONTAINS "` + token + `-live"'; echo "LIVESHOW_RC=$?"`,
		`echo "=== COLLECT ==="`,
		`yolo-log collect 2>&1; echo "COLLECT_RC=$?"`,
		`echo "=== END ==="`,
	}, "\n")

	packHome(t, `{"packs": ["macos-log"], "loopholes": {"macos-log": {"enabled": true}}}`)
	r := macosUserRunProbe(t, "macos-log user", macosUserWorkspace(t, `{}`), script)
	diag := func() string {
		return fmt.Sprintf("\n--- launch stdout:\n%s\n--- launch stderr:\n%s", r.stdout, r.stderr)
	}
	if client := strings.TrimSpace(section(r.stdout, "=== CLIENT ===", "=== SHOW ===")); client !=
		macosuser.GuestBinaryPath("yolo-log", "") {
		t.Errorf("yolo-log resolves to %q in the sandbox, want the staged guest binary %s%s",
			client, macosuser.GuestBinaryPath("yolo-log", ""), diag())
	}
	show := section(r.stdout, "=== SHOW ===", "=== STREAM ===")
	hasUserID := strings.Contains(show, `"userID"`)
	t.Logf("MEASUREMENT (macos-log), the user scope's `show` returned entries carrying `userID`: %v "+
		"(without it, `show` returns nothing in the user scope, by design)", hasUserID)
	if !strings.Contains(show, "SHOW_RC=0") || (hasUserID && !strings.Contains(show, mine)) {
		t.Errorf("the user scope's show failed, or carried userID and still missed the sandbox "+
			"account's entry %q:\n%s%s", mine, show, diag())
	}
	// LIVESHOW reads the live entry back from the STORE after the stream, so a stream that
	// missed it says which half failed: an entry `show` finds was logged and the stream did not
	// deliver it; one `show` cannot find either was never logged from inside the sandbox or
	// cannot be attributed to it (CI run 37986991379 failed here with no way to tell).
	liveShow := section(r.stdout, "=== LIVESHOW ===", "=== COLLECT ===")
	inStore := strings.Contains(liveShow, token+"-live")
	t.Logf("MEASUREMENT (macos-log), the sandbox's live entry is in the store (user-scope `show` "+
		"after the stream): %v", inStore)
	if stream := section(r.stdout, "=== STREAM ===", "=== LIVESHOW ==="); !strings.Contains(stream, token+"-live") {
		cause := "`show` cannot find it either, so the sandbox's syslog(3) entry never reached " +
			"the store, or reached it without the sandbox account's userID: the profile " +
			"refusing the sandbox's logging is the first suspect (the kernel's Sandbox lines follow)"
		if inStore {
			cause = "`show` finds it in the store, so the sandbox logged it and the STREAM did not " +
				"deliver it: `log stream` holding its output in a pipe buffer until it exits, the " +
				"stream starting after the entry, or the entry missing the stream's attribution"
		}
		t.Errorf("the user scope's stream did not deliver the sandbox's live entry %q: %s.\n"+
			"stream:\n%s\nshow after it:\n%s%s", token+"-live", cause, stream, liveShow, diag())
		logSandboxDenials(t)
	}
	if strings.Contains(show, theirs) {
		t.Errorf("the user scope returned the runner's entry %q, which is not the sandbox "+
			"account's:\n%s%s", theirs, show, diag())
	}
	if c := section(r.stdout, "=== COLLECT ===", "=== END ==="); !strings.Contains(c, "COLLECT_RC=2") ||
		!strings.Contains(c, `"full": true`) {
		t.Errorf("`yolo-log collect` was not refused with the setting that widens it:\n%s%s", c, diag())
	}

	packHome(t, `{"packs": ["macos-log"], "loopholes": {"macos-log": {"enabled": true, `+
		`"settings": {"full": true}}}}`)
	r = macosUserRunProbe(t, "macos-log full", macosUserWorkspace(t, `{}`), script)
	show = section(r.stdout, "=== SHOW ===", "=== STREAM ===")
	if !strings.Contains(show, "SHOW_RC=0") || !strings.Contains(show, mine) || !strings.Contains(show, theirs) {
		t.Errorf("`full` did not return both accounts' entries (%q, %q):\n%s%s", mine, theirs, show, diag())
	}
}

// syslogAs logs msg through syslog(3) from a process that stays alive until the test ends, as the
// sandbox account (sudo -n -u) or as the runner. Kept alive so a bridge attributing an entry by
// its live process can still find the owner.
func syslogAs(t *testing.T, asSandbox bool, msg string) {
	t.Helper()
	argv := []string{"/usr/bin/perl", "-MSys::Syslog", "-e",
		`openlog("yolo-it", "pid", "user"); syslog("notice", "%s", $ARGV[0]); closelog(); sleep 900`, msg}
	if asSandbox {
		argv = append([]string{"/usr/bin/sudo", "-n", "--user=" + macosuser.SandboxUser}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("logging %q: %v", msg, err)
	}
	t.Cleanup(func() {
		if asSandbox {
			_ = runQuiet(time.Minute, "/usr/bin/sudo", "-n", "/usr/bin/pkill", "-u", macosuser.SandboxUser, "-f", msg)
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	time.Sleep(time.Second) // let the entry land before the launch reads it
}
