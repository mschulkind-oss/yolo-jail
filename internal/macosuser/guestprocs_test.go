package macosuser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// guestprocs_test.go pins whose processes are the sandbox's in each mode (guestprocs.go). In the
// run-as-you mode a uid says nothing, so a reader keyed on the second account's uid kept every
// pack tree on a Mac without the account, and on a Mac with it missed the sandbox entirely.

const (
	gpAccount = 299
	gpYou     = 501
	gpOther   = 502
)

func gpRow(pid, ppid int, uid uint32, env ...string) guestProcRow {
	return guestProcRow{PID: pid, PPID: ppid, UIDs: [3]uint32{uid, uid, uid}, Start: time.Unix(100, 0),
		Env: env, EnvKnown: true}
}

func sandboxPIDs(procs []GuestProc) []int {
	var out []int
	for _, p := range procs {
		if p.Sandbox {
			out = append(out, p.PID)
		}
	}
	slices.Sort(out)
	return out
}

func TestSelfModeFindsTheSandboxByItsMarkerNotItsUID(t *testing.T) {
	envDir := "/Users/Shared/yolo-staged/env"
	machine := "/Users/you/.local/share/yolo-jail/macos-user"
	marker := SandboxEnvFileEnv + "=" + envDir + "/yolo-alpha-1.env"
	rows := []guestProcRow{
		gpRow(10, 1, gpYou, "TERM=xterm"),                                      // your shell: not the sandbox
		gpRow(11, 10, gpYou, marker),                                           // the agent, inside the profile
		gpRow(12, 11, gpYou, "PATH=/bin"),                                      // its child that dropped the env: still inside
		gpRow(13, 1, gpYou, entrypoint.DarwinMachineHomeEnv+"="+machine),       // a launch with no env file
		gpRow(14, 1, gpYou, SandboxEnvFileEnv+"=/tmp/elsewhere.env"),           // a marker naming no session file
		gpRow(20, 1, gpAccount),                                                // an earlier account-mode session
		gpRow(30, 1, gpOther, marker),                                          // another user's process: never yours
		{PID: 40, PPID: 1, UIDs: [3]uint32{gpYou, gpYou, gpYou}, Alive: true},  // yours, unreadable, alive
		{PID: 41, PPID: 1, UIDs: [3]uint32{gpYou, gpYou, gpYou}, Alive: false}, // gone meanwhile
	}
	self := guestProcClassifier{self: true, selfUID: gpYou, markerDir: envDir, machineHome: machine,
		account: gpAccount, accountKnown: true}
	if got, want := sandboxPIDs(self.classify(rows)), []int{11, 12, 13, 20, 40}; !slices.Equal(got, want) {
		t.Errorf("self mode's sandbox = %v, want %v", got, want)
	}
	// On a Mac without the account, the account's half is simply empty.
	self.accountKnown = false
	if got, want := sandboxPIDs(self.classify(rows)), []int{11, 12, 13, 40}; !slices.Equal(got, want) {
		t.Errorf("self mode without the account = %v, want %v", got, want)
	}
	// The account mode is the uid alone, as it always was.
	account := guestProcClassifier{account: gpAccount, accountKnown: true, markerDir: envDir}
	if got, want := sandboxPIDs(account.classify(rows)), []int{20}; !slices.Equal(got, want) {
		t.Errorf("account mode's sandbox = %v, want %v", got, want)
	}
}

func procArgs2(argc int32, exe string, argv, env []string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, argc)
	b.WriteString(exe)
	b.Write([]byte{0, 0, 0, 0})
	for _, a := range argv {
		b.WriteString(a)
		b.WriteByte(0)
	}
	for _, e := range env {
		b.WriteString(e)
		b.WriteByte(0)
	}
	b.Write([]byte{0, 0})
	return b.Bytes()
}

func TestParseProcArgs2ReadsTheEnvironmentAfterTheArguments(t *testing.T) {
	buf := procArgs2(2, "/bin/sh", []string{"sh", "-c"}, []string{"HOME=/h", SandboxEnvFileEnv + "=/x"})
	argv, env, ok := parseProcArgs2(buf)
	if !ok || !slices.Equal(argv, []string{"sh", "-c"}) || !slices.Equal(env, []string{"HOME=/h", SandboxEnvFileEnv + "=/x"}) {
		t.Errorf("parse = %q, %q, %v", argv, env, ok)
	}
	if _, _, ok := parseProcArgs2([]byte{1, 0}); ok {
		t.Error("a buffer too short for argc parsed")
	}
}

// THE REAPER IS TOLD THE MODE, and only a run-as-you launch's argv says so: the account mode's
// argv is what it was.
func TestThePackTreeReaperArgvCarriesTheRunAsYouMode(t *testing.T) {
	anchor := time.Unix(1760740184, 0)
	acct := PackTreeReaperArgv("/y", "/Users/Shared/yolo-staged/packs/a.b", anchor, ModeAccount)
	if slices.Contains(acct, "--mode") {
		t.Errorf("the account-mode reaper argv changed: %q", acct)
	}
	self := PackTreeReaperArgv("/y", "/Users/Shared/yolo-staged/packs/a.b", anchor, ModeSelf)
	if _, _, mode, err := parsePackTreeReapArgs(self[3:]); err != nil || mode != ModeSelf {
		t.Errorf("parse(%q) = %v, %v; want self", self, mode, err)
	}
	if _, _, _, err := parsePackTreeReapArgs([]string{"--root", "x", "--anchor", "1", "--mode", "jail"}); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

// A RUN-AS-YOU LAUNCH ASKS FOR ITS OWN MODE'S SANDBOX, and hands the reaper the same mode: with
// no holder it removes its tree; with one it hands the tree over with `--mode self`. Fails if
// beginPackTree's mode is dropped at the launch or in the handoff.
func TestASelfModeLaunchReadsTheSandboxInItsOwnMode(t *testing.T) {
	selfLaunch := func(holder bool, modes *[]Mode, reaped *[]string) func(*Deps, *Options) {
		return func(d *Deps, o *Options) {
			o.Mode, o.HostHome, o.HostUser = ModeSelf, "/Users/you", "you"
			d.SandboxUserExists = func() bool { return false }
			d.RunAsGrant = func() bool { return false }
			d.GuestProcesses = func(m Mode) ([]GuestProc, error) {
				*modes = append(*modes, m)
				if !holder {
					return nil, nil
				}
				return []GuestProc{{PID: 9100, PPID: 1, Sandbox: true, Start: time.Now(), Name: "node"}}, nil
			}
			d.StartPackTreeReaper = func(argv []string, _ *os.File) error {
				*reaped = append(*reaped, strings.Join(argv, " "))
				return errors.New("not started in a test")
			}
		}
	}
	var modes []Mode
	var reaped []string
	tree, _, out, _ := runPackTreeLaunch(t, 0, nil, selfLaunch(false, &modes, &reaped))
	if len(modes) == 0 || slices.ContainsFunc(modes, func(m Mode) bool { return m != ModeSelf }) {
		t.Errorf("the launch read the process table in modes %v, want self only\n%s", modes, out.String())
	}
	if _, err := os.Stat(tree); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a self-mode launch with no live sandbox process kept its tree (%v)\n%s", err, out.String())
	}

	modes, reaped = nil, nil
	_, _, out, _ = runPackTreeLaunch(t, 0, nil, selfLaunch(true, &modes, &reaped))
	if len(reaped) != 1 || !strings.HasSuffix(reaped[0], " --mode self") {
		t.Errorf("the reaper was not handed the run-as-you mode: %q\n%s", reaped, out.String())
	}
}

// AN EMPTY ENVIRONMENT IS AN UNREAD ONE (the Mac run of 2026-11-03, §6). macOS 26 gives a
// platform binary's kern.procargs2 with its arguments and NO environment, to its owner too, so a
// `sh` or `sleep` the sandbox started read as "known, no marker" and was dropped from the holder
// proof; the tri-state above says an unreadable environment of a live process counts. Every
// process yours that the sandbox starts carries at least the marker, so empty never proves
// "not the sandbox's".
func TestAnEmptyProcArgs2EnvironmentIsUnknown(t *testing.T) {
	if env, known := procArgs2Env(procArgs2(1, "/bin/sleep", []string{"sleep"}, nil)); known || env != nil {
		t.Errorf("a hidden environment = %q, known %v; want unknown", env, known)
	}
	if _, known := procArgs2Env([]byte{1, 0}); known {
		t.Error("a short buffer read as a known environment")
	}
	marker := SandboxEnvFileEnv + "=/Users/Shared/yolo-staged/env/a.env"
	if env, known := procArgs2Env(procArgs2(1, "/x/node", []string{"node"}, []string{marker})); !known || !slices.Equal(env, []string{marker}) {
		t.Errorf("a readable environment = %q, known %v", env, known)
	}
}
