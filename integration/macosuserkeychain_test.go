package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserKeychainProbe RECORDS what the sandbox account can do with a keychain, and
// asserts nothing about it: it is the measurement docs/design/keychain-from-a-jail.md OQ-KC4's
// leaning asks for before that question is ruled ("Measure first"), taken from inside a real
// macos-user sandbox, as the account `_yolojail`, under the session Seatbelt profile. It never
// runs Copilot, or any agent.
//
// What it asks, one step each, every step bounded so that a keychain call waiting on a dialog
// nobody can answer ends instead of hanging the job:
//
//   - whether the account has a default keychain and what its search list holds (§4.1 infers
//     neither, E17);
//   - whether it can create a keychain on a throwaway file in its own temp dir, set it never to
//     lock, lock it, and unlock it with the password fed to `security -i` on stdin (option B's
//     mechanism);
//   - whether an item added by one process is read back by a SECOND one, which is the property
//     an unlocked keychain without a login session has to have for a later agent process to use
//     it;
//   - and it deletes the keychain, which also drops it from the search list; a delete that did
//     not answer is tried once more on exit, under the same bound. It never sets the default
//     keychain; it reads the default again after the create, so a create that changed it is
//     itself recorded.
//
// Every answer goes to t.Logf. The test fails only when the probe could not be taken (the launch
// did not run it, or its shell printed none of its steps) or when it left its keychain in the
// account's search list, which is the one change to the account it promises not to make.
//
// ⚠ A GitHub-hosted runner is not a Terminal launch, which is what the leaning names, and whether
// the two give the same answer is not known. An answer here is an input for OQ-KC4, not the whole
// measurement.
func TestMacosUserKeychainProbe(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)
	r := macosUserRunProbe(t, "keychain", ws, keychainProbeScript(keychainProbeStepSeconds))

	probe := section(r.stdout, "=== KEYCHAIN ===", "=== END ===")
	steps := 0
	for _, line := range strings.Split(probe, "\n") {
		if strings.HasPrefix(line, "STEP ") {
			steps++
		}
	}
	t.Logf("MEASUREMENT (keychain-from-a-jail.md OQ-KC4), as %s inside the sandbox:\n%s",
		macosuser.SandboxUser, probe)
	if steps == 0 {
		t.Fatalf("the probe printed none of its steps, so nothing was measured:\nstdout:\n%s\nstderr:\n%s",
			r.stdout, r.stderr)
	}
	if kc := kvLine(probe, "KEYCHAIN"); kc != "" {
		after := section(probe, "STEP list-keychains-after ", "STEP ")
		if strings.Contains(after, kc) {
			u := macosuser.SandboxUser
			t.Errorf("the probe left its keychain %s in %s's search list. Remove it with "+
				"`sudo -u %s security delete-keychain %s` (or drop it from the list with "+
				"`sudo -u %s security list-keychains -d user -s <the others>`).\n%s",
				kc, u, u, kc, u, probe)
		}
	}
}

// keychainProbeStepSeconds bounds each step of the probe on a Mac: long enough for a keychain
// call that is merely slow, short enough that the probe still ends well inside the launch's
// deadline (macosUserTimeout) when every step waits on a dialog nobody can answer.
const keychainProbeStepSeconds = 20

// keychainProbeScript is the probe's shell, run by the sandbox's login bash, every step bounded
// by stepSeconds.
//
// THE BOUND IS bash's OWN, because a stock macOS has no timeout(1) and the macos-user floor
// leaves GNU coreutils out by policy: step runs its command in the background with a watchdog
// that sends SIGTERM after stepSeconds and SIGKILL two seconds later, and reports the command's
// status (143 or 137 for one the watchdog ended). The watchdog's output is /dev/null, so a sleep
// it leaves behind holds no pipe the harness waits on. The exit trap's cleanup is bounded the
// same way: it deletes the keychain again only when the delete step did not answer rc=0, and then
// as a step of its own (cleanup-delete-keychain, printed after the probe's END), since a delete
// that waited on a dialog once will wait on it again.
//
// EVERY STEP MARKER BEGINS A LINE: a command's output is copied with awk, which ends every line
// it prints, so output with no final newline (`security -i`'s prompt, a password read back with
// -w) cannot carry the next `STEP` marker onto its own last line.
//
// STDIN IS AN EMPTY PIPE, not /dev/null: under the session profile an isatty(0) on the
// /dev/null DEVICE is a denied file-ioctl (macosuserseatbelt_test.go's header), and a tool that
// treated that error as fatal would be measuring the profile instead of the keychain.
//
// The keychain password and the item's value are drawn per run and are throwaway; printing them
// is how the second-process read is checked.
func keychainProbeScript(stepSeconds int) string {
	return strings.ReplaceAll(keychainProbeShell, "__BOUND__", strconv.Itoa(stepSeconds))
}

// keychainProbeShell is keychainProbeScript before its bound is spliced in.
const keychainProbeShell = `
echo "=== KEYCHAIN ==="
base=$(getconf DARWIN_USER_TEMP_DIR 2>/dev/null)
base=${base:-${TMPDIR:-/tmp}/}
kcdir=$(mktemp -d "${base%/}/yolo-kc-probe.XXXXXX")
kc="$kcdir/probe.keychain"
pw="yolo-kc-probe-$$-$RANDOM$RANDOM"
item="yolo-kc-item-$$-$RANDOM$RANDOM"
echo "KEYCHAIN=$kc"
deleted=no
cleanup() {
    [ "$deleted" = yes ] || step cleanup-delete-keychain security delete-keychain "$kc"
    rm -rf "$kcdir"
}
trap cleanup EXIT

step() {
    name=$1 secs=__BOUND__
    shift
    out="$kcdir/$name.out"
    printf '' | "$@" >"$out" 2>&1 &
    pid=$!
    ( sleep "$secs"; kill -TERM "$pid" 2>/dev/null; sleep 2; kill -KILL "$pid" 2>/dev/null ) >/dev/null 2>&1 &
    dog=$!
    rc=0
    wait "$pid" || rc=$?
    kill "$dog" 2>/dev/null
    wait "$dog" 2>/dev/null
    echo "STEP $name rc=$rc"
    awk '{ print "  | " $0 }' "$out"
    LAST_RC=$rc
    LAST_OUT=$(cat "$out")
}

step default-keychain security default-keychain -d user
default_rc=$LAST_RC default_out=$LAST_OUT
step login-keychain security login-keychain -d user
step list-keychains security list-keychains -d user
step create-keychain security create-keychain -p "$pw" "$kc"
create_rc=$LAST_RC
step default-after-create security default-keychain -d user
step set-keychain-settings security set-keychain-settings "$kc"
step lock-keychain security lock-keychain "$kc"
step unlock-via-stdin bash -c 'printf "unlock-keychain -p %s %s\n" "$1" "$2" | security -i' _ "$pw" "$kc"
unlock_rc=$LAST_RC
step show-keychain-info security show-keychain-info "$kc"
step add-generic-password security add-generic-password -a yolo-kc-probe -s yolo-kc-probe -w "$item" "$kc"
step find-from-a-second-process bash -c 'security find-generic-password -a yolo-kc-probe -s yolo-kc-probe -w "$1"' _ "$kc"
find_out=$LAST_OUT
step delete-keychain security delete-keychain "$kc"
if [ "$LAST_RC" = 0 ]; then deleted=yes; fi
step list-keychains-after security list-keychains -d user
echo "STEP done rc=0"

if [ "$default_rc" = 0 ] && [ -n "$default_out" ]; then
    echo "VERDICT default-keychain: yes ($default_out)"
else
    echo "VERDICT default-keychain: no (rc $default_rc)"
fi
if [ "$create_rc" = 0 ] && [ "$unlock_rc" = 0 ]; then
    echo "VERDICT created-and-unlocked: yes"
else
    echo "VERDICT created-and-unlocked: no (create rc $create_rc, unlock rc $unlock_rc)"
fi
if [ "$find_out" = "$item" ]; then
    echo "VERDICT second-process-read: yes"
else
    echo "VERDICT second-process-read: no"
fi
echo "=== END ==="
`

// TestMacosUserKeychainProbeShellRunsAgainstAStandIn runs the probe's shell on the machine that
// develops this repo, against a stand-in `security` first on PATH, so the shell itself is
// checked where its author can run it: the steps, the watchdog that bounds each one, the
// verdicts, the log's shape and the cleanup. It asserts nothing about a keychain. Not behind
// requireMacosUser, so it runs under -short on Linux and in check-macos, and the stand-in, not
// the Mac's /usr/bin/security, answers every call on both.
//
// The stand-in hangs on `lock-keychain`, so that step is the watchdog's to end, at a one-second
// bound; every other call answers as an account with a working keychain would, and the item it
// reads back ends with no newline, as `security -i`'s prompt does.
func TestMacosUserKeychainProbeShellRunsAgainstAStandIn(t *testing.T) {
	run := runKeychainProbeAgainstAStandIn(t, "lock-keychain")
	probe := section(run.out, "=== KEYCHAIN ===", "=== END ===")
	for _, want := range []string{
		"STEP default-keychain rc=0", "STEP create-keychain rc=0", "STEP unlock-via-stdin rc=0",
		"STEP find-from-a-second-process rc=0", "STEP delete-keychain rc=0", "STEP done rc=0",
		"VERDICT default-keychain: yes", "VERDICT created-and-unlocked: yes",
		"VERDICT second-process-read: yes",
	} {
		if !strings.Contains(probe, want) {
			t.Errorf("the probe does not say %q:\n%s", want, run.out)
		}
	}
	if !watchdogEnded(probe, "lock-keychain") {
		t.Errorf("the hanging step was not ended by the watchdog (want rc 143 or 137):\n%s", run.out)
	}
	if got, _ := os.ReadFile(filepath.Join(run.state, "interactive")); strings.TrimSpace(string(got)) != "unlock-keychain" {
		t.Errorf("security -i was handed %q on stdin, want one unlock-keychain command", got)
	}
	kc := kvLine(probe, "KEYCHAIN")
	if kc == "" || !strings.HasPrefix(kc, run.tmp+"/") {
		t.Errorf("the probe's keychain %q is not in the temp dir it was given (%s)", kc, run.tmp)
	}
	if after := section(probe, "STEP list-keychains-after ", "STEP "); kc == "" || strings.Contains(after, kc) {
		t.Errorf("the keychain is still in the search list after the probe:\n%s", run.out)
	}
	if left, _ := os.ReadDir(run.tmp); len(left) != 0 {
		t.Errorf("the probe left %v in its temp dir", left)
	}
	// The delete step answered, so the exit trap has nothing to retry.
	if strings.Contains(run.out, "STEP cleanup-delete-keychain") {
		t.Errorf("the exit trap deleted the keychain again after a delete step that answered rc=0:\n%s", run.out)
	}
	// A STEP marker joined to the end of the previous step's last line of output is one the
	// Mac test's count misses and a reader of the logged measurement has to hunt for.
	if markers, atLineStart := strings.Count(probe, "STEP "), strings.Count("\n"+probe, "\nSTEP "); markers != atLineStart {
		t.Errorf("%d of the probe's %d STEP markers do not begin a line: a step's output that ends "+
			"with no newline swallowed the next marker:\n%s", markers-atLineStart, markers, probe)
	}
}

// TestMacosUserKeychainProbeShellBoundsItsCleanup is the case the watchdog exists for, met at
// the probe's last call: the stand-in hangs on `delete-keychain`, the one call the probe's exit
// trap makes again. The delete step is the watchdog's to end, and so is the trap's retry, so the
// whole probe still ends within a few seconds of its one-second bound and still removes its temp
// dir. An unbounded retry is a probe that hangs the launch until macosUserTimeout and turns a
// measurement into a timeout.
func TestMacosUserKeychainProbeShellBoundsItsCleanup(t *testing.T) {
	run := runKeychainProbeAgainstAStandIn(t, "delete-keychain")
	probe := section(run.out, "=== KEYCHAIN ===", "=== END ===")
	if !strings.Contains(probe, "STEP done rc=0") {
		t.Errorf("the probe did not reach its last step:\n%s", run.out)
	}
	if !watchdogEnded(probe, "delete-keychain") {
		t.Errorf("the hanging delete step was not ended by the watchdog (want rc 143 or 137):\n%s", run.out)
	}
	if !watchdogEnded(run.out, "cleanup-delete-keychain") {
		t.Errorf("the exit trap did not retry the delete under the watchdog (want a "+
			"cleanup-delete-keychain step ending rc 143 or 137):\n%s", run.out)
	}
	if left, _ := os.ReadDir(run.tmp); len(left) != 0 {
		t.Errorf("the probe left %v in its temp dir", left)
	}
}

// keychainTwinDeadline is how long the twin's probe may take: a few seconds over its one-second
// bound per hanging step, and well under the stand-in's 30-second hang, so a step or a cleanup
// left unbounded fails it rather than passing slowly.
const keychainTwinDeadline = 15 * time.Second

// keychainTwinRun is one run of the probe's shell against the stand-in: its combined output, the
// stand-in's state dir and the temp dir the probe was given.
type keychainTwinRun struct {
	out, state, tmp string
}

// runKeychainProbeAgainstAStandIn runs the probe's shell, at a one-second bound, against a
// stand-in `security` that hangs on each verb in hang and answers every other call as an account
// with a working keychain would. A stand-in `getconf` answers nothing, so the probe's temp dir is
// the test's own on every OS. It fails the test when the shell fails or takes longer than
// keychainTwinDeadline.
func runKeychainProbeAgainstAStandIn(t *testing.T, hang ...string) keychainTwinRun {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	bin := resolvedTempDir(t)
	run := keychainTwinRun{state: resolvedTempDir(t), tmp: resolvedTempDir(t)}
	standIn := `#!/bin/sh
state="$KC_STANDIN_STATE"
for a; do last=$a; done
case " $KC_STANDIN_HANG " in *" $1 "*) exec sleep 30 ;; esac
case "$1" in
  default-keychain|login-keychain) echo "    \"$state/login.keychain-db\"" ;;
  list-keychains) echo "    \"$state/login.keychain-db\""; cat "$state/list" 2>/dev/null ;;
  create-keychain) : > "$last"; echo "    \"$last\"" >> "$state/list" ;;
  set-keychain-settings|lock-keychain|show-keychain-info) ;;
  -i) while read -r cmd rest; do echo "$cmd" >> "$state/interactive"; done ;;
  add-generic-password)
    while [ $# -gt 0 ]; do [ "$1" = -w ] && printf '%s' "$2" > "$state/item"; shift; done ;;
  find-generic-password) cat "$state/item" ;;
  delete-keychain)
    rm -f "$last"
    grep -v -F "$last" "$state/list" > "$state/list.new" 2>/dev/null
    mv "$state/list.new" "$state/list" ;;
  *) echo "stand-in security: unexpected argv: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte(standIn), 0o755); err != nil {
		t.Fatal(err)
	}
	// And a getconf that answers nothing, so the probe takes TMPDIR as its temp dir on a Mac
	// too, where the real one would name the invoking user's own.
	if err := os.WriteFile(filepath.Join(bin, "getconf"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "-c", keychainProbeScript(1))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KC_STANDIN_STATE="+run.state, "TMPDIR="+run.tmp,
		"KC_STANDIN_HANG="+strings.Join(hang, " "))
	cmd.Stdin = strings.NewReader("")
	begun := time.Now()
	raw, err := cmd.CombinedOutput()
	run.out = string(raw)
	if err != nil {
		t.Fatalf("the probe's shell failed: %v\n%s", err, run.out)
	}
	if took := time.Since(begun); took > keychainTwinDeadline {
		t.Errorf("the probe took %s against a one-second bound with %v hanging: a step or the "+
			"cleanup was not bounded\n%s", took, hang, run.out)
	}
	return run
}

// watchdogEnded reports whether out records the step name as ended by the probe's watchdog,
// SIGTERM's 143 or SIGKILL's 137.
func watchdogEnded(out, name string) bool {
	return strings.Contains(out, "STEP "+name+" rc=143") || strings.Contains(out, "STEP "+name+" rc=137")
}
