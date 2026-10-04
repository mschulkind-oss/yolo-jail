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
//   - and it deletes the keychain, which also drops it from the search list. It never sets the
//     default keychain; it reads the default again after the create, so a create that changed it
//     is itself recorded.
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
// it leaves behind holds no pipe the harness waits on.
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
trap 'security delete-keychain "$kc" >/dev/null 2>&1; rm -rf "$kcdir"' EXIT

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
    sed 's/^/  | /' "$out"
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
// verdicts and the cleanup. It asserts nothing about a keychain. Not behind requireMacosUser, so
// it runs under -short on Linux and in check-macos, and the stand-in, not the Mac's
// /usr/bin/security, answers every call on both.
//
// The stand-in hangs on `lock-keychain`, so that step is the watchdog's to end, at a one-second
// bound; every other call answers as an account with a working keychain would. A stand-in
// `getconf` answers nothing, so the probe's temp dir is the test's own on every OS.
func TestMacosUserKeychainProbeShellRunsAgainstAStandIn(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	bin := resolvedTempDir(t)
	state := resolvedTempDir(t)
	tmp := resolvedTempDir(t)
	standIn := `#!/bin/sh
state="$KC_STANDIN_STATE"
for a; do last=$a; done
case "$1" in
  default-keychain|login-keychain) echo "    \"$state/login.keychain-db\"" ;;
  list-keychains) echo "    \"$state/login.keychain-db\""; cat "$state/list" 2>/dev/null ;;
  create-keychain) : > "$last"; echo "    \"$last\"" >> "$state/list" ;;
  set-keychain-settings|show-keychain-info) ;;
  lock-keychain) exec sleep 30 ;;
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
		"KC_STANDIN_STATE="+state, "TMPDIR="+tmp)
	cmd.Stdin = strings.NewReader("")
	begun := time.Now()
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("the probe's shell failed: %v\n%s", err, out)
	}
	if took := time.Since(begun); took > 30*time.Second {
		t.Errorf("the probe took %s against a one-second bound: a step was not bounded\n%s", took, out)
	}
	probe := section(out, "=== KEYCHAIN ===", "=== END ===")
	for _, want := range []string{
		"STEP default-keychain rc=0", "STEP create-keychain rc=0", "STEP unlock-via-stdin rc=0",
		"STEP find-from-a-second-process rc=0", "STEP delete-keychain rc=0", "STEP done rc=0",
		"VERDICT default-keychain: yes", "VERDICT created-and-unlocked: yes",
		"VERDICT second-process-read: yes",
	} {
		if !strings.Contains(probe, want) {
			t.Errorf("the probe does not say %q:\n%s", want, out)
		}
	}
	if !strings.Contains(probe, "STEP lock-keychain rc=143") && !strings.Contains(probe, "STEP lock-keychain rc=137") {
		t.Errorf("the hanging step was not ended by the watchdog (want rc 143 or 137):\n%s", out)
	}
	if got, _ := os.ReadFile(filepath.Join(state, "interactive")); strings.TrimSpace(string(got)) != "unlock-keychain" {
		t.Errorf("security -i was handed %q on stdin, want one unlock-keychain command", got)
	}
	kc := kvLine(probe, "KEYCHAIN")
	if kc == "" || !strings.HasPrefix(kc, tmp+"/") {
		t.Errorf("the probe's keychain %q is not in the temp dir it was given (%s)", kc, tmp)
	}
	if after := section(probe, "STEP list-keychains-after ", "STEP "); kc == "" || strings.Contains(after, kc) {
		t.Errorf("the keychain is still in the search list after the probe:\n%s", out)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the probe left %v in its temp dir", left)
	}
}
