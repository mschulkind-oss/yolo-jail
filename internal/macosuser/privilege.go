package macosuser

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

// THE ONE-RULE CONTRACT. A macos-user launch crosses into the yolo account through exactly one
// sudo rule, which `yolo yolo-account create` installs and nothing else widens:
//
//	<host user> ALL=(_yolojail) NOPASSWD: /usr/bin/env
//
// It runs commands as _yolojail, never as root, so every per-launch file is written and removed by
// the host user in a staging root it owns, and only the crossing itself goes through sudo. The
// ruling is OQ-MP10 in docs/design/macos-user-privilege-lifecycle.md; the build decisions (PL-1 to
// PL-9) are in docs/design/macos-user-privilege-lifecycle-plan.md. Every function here is pure:
// the launch probes, this file judges.

// SudoersFile is where setup installs the rule. ⚠ sudo silently ignores a file in sudoers.d
// whose name contains a '.' or ends in '~', so the name has neither; setup stages it as
// `.yolo-jail.new`, which sudo ignores for the same reason until the rename (PL-4).
const SudoersFile = "/etc/sudoers.d/yolo-jail"

// SudoersStagingFile is the inert name setup validates the rule under before renaming it into
// place (PL-4).
const SudoersStagingFile = "/etc/sudoers.d/.yolo-jail.new"

// sudoBin is sudo by absolute path (PL-9): a PATH-shadowed sudo would make the run-as audit
// meaningless.
const sudoBin = "/usr/bin/sudo"

// envBin is the one command the rule names. Every crossing runs it, and whatever runs as the
// sandbox account is its argument.
const envBin = "/usr/bin/env"

// stagingRoot is the host-owned directory every launch stages into (PL-1). It lives under
// /Users/Shared because _yolojail cannot traverse the host's home, and it is not
// /Users/Shared/yolo, which is the _yolojail-writable workspace root.
const stagingRoot = "/Users/Shared/yolo-staged"

// legacyStateDir is where yolo before the one-rule contract staged, root-owned. A launch never
// touches it: sessions started by an older launcher still write there (PL-8).
const legacyStateDir = "/var/yolo-jail"

// StagingRoot is the host-owned staging root, /Users/Shared/yolo-staged.
func StagingRoot() string { return stagingRoot }

// LegacyStateDir is the root-owned state dir older yolo staged into, /var/yolo-jail.
func LegacyStateDir() string { return legacyStateDir }

// RunAsSandboxArgv is the only spelling of a crossing into the yolo account:
// `/usr/bin/sudo -n [--set-home] --user=_yolojail /usr/bin/env <argv…>`. `-n` makes a missing
// rule fail instead of prompting. sudoFlags admits `--set-home` alone; anything else is a
// programming error and panics, because every other sudo flag either leaves the rule's shape
// (`-E` needs SETENV, `--login` runs a shell) or targets another account.
//
// Account mode's half of RunAs (mode.go): a launch's crossings call RunAs.Argv, never this
// directly, so one value decides which user every crossing runs as
// (TestEveryCrossingGoesThroughRunAs).
func RunAsSandboxArgv(sudoFlags []string, argv ...string) []string {
	out := []string{sudoBin, "-n"}
	for _, f := range sudoFlags {
		if f != "--set-home" {
			panic(fmt.Sprintf("macosuser.RunAsSandboxArgv: sudo flag %q is outside the rule's shape", f))
		}
		out = append(out, f)
	}
	out = append(out, "--user="+SandboxUser, envBin)
	return append(out, argv...)
}

// isRunAsSandboxArgv reports whether argv is RunAsSandboxArgv's shape: sudo by absolute path
// whose `--user=` is the yolo account. commandFor (real.go) starts such a crossing in `/`.
func isRunAsSandboxArgv(argv []string) bool {
	if len(argv) == 0 || argv[0] != sudoBin {
		return false
	}
	for _, a := range argv[1:] {
		if a == envBin {
			return false
		}
		if a == "--user="+SandboxUser {
			return true
		}
	}
	return false
}

// RunAsGrantProbeArgv is readiness probe G2: `/usr/bin/sudo -n --user=_yolojail /usr/bin/env
// /usr/bin/true`. It proves the rule is installed and matches without doing anything. ⚠ A bare
// `/usr/bin/true` would not match: sudo matches the command's path, and the rule names env.
func RunAsGrantProbeArgv() []string { return RunAsSandboxArgv(nil, "/usr/bin/true") }

// AccountUserTempDirArgv asks the yolo account's own DARWIN_USER_TEMP_DIR through the rule:
// `/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/getconf DARWIN_USER_TEMP_DIR`. Only
// the account can answer it — the directory is derived from the asking user — and the account
// mode's profile opens it (MMP-D29).
func AccountUserTempDirArgv() []string {
	return RunAsSandboxArgv(nil, "/usr/bin/getconf", "DARWIN_USER_TEMP_DIR")
}

// RunAsArgvProblems reports how argv departs from the rule's shape. An argv that does not run
// sudo at all is not this check's business and has no problems; any sudo argv must be exactly
// what RunAsSandboxArgv builds: sudo by absolute path, `-n`, at most `--set-home`, `--user=` the
// sandbox account, and /usr/bin/env as the command. label prefixes each problem.
func RunAsArgvProblems(label string, argv []string) []string {
	if len(argv) == 0 || path.Base(argv[0]) != "sudo" {
		return nil
	}
	var problems []string
	add := func(format string, a ...any) {
		problems = append(problems, label+": "+fmt.Sprintf(format, a...)+" in "+strings.Join(argv, " "))
	}
	if argv[0] != sudoBin {
		add("sudo is not named %s", sudoBin)
	}
	nonInteractive, user := false, ""
	i := 1
	for ; i < len(argv) && strings.HasPrefix(argv[i], "-"); i++ {
		switch f := argv[i]; {
		case f == "-n" || f == "--non-interactive":
			nonInteractive = true
		case f == "--set-home":
		case strings.HasPrefix(f, "--user="):
			user = strings.TrimPrefix(f, "--user=")
		default:
			add("sudo flag %q is outside the rule's shape", f)
		}
	}
	if !nonInteractive {
		add("no -n, so a missing rule prompts for a password")
	}
	switch user {
	case "":
		add("runs as root, which the rule never grants")
	case SandboxUser:
	default:
		add("runs as %q, not %s", user, SandboxUser)
	}
	if i >= len(argv) || argv[i] != envBin {
		add("the command is not %s, the only one the rule names", envBin)
	}
	return problems
}

// sudoersUserName is what a host user name must look like before it is written into a root-owned
// sudo rule. visudo validates the file again at setup; this check exists so a hostile name never
// reaches the file at all.
var sudoersUserName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// SudoersRule is the exact content of SudoersFile for hostUser: a comment naming who wrote it, a
// secure_path scoped to the run-as account (so the admin's other sudo is unchanged, PL-4), and
// the one rule. It refuses a name that is not a plain user name, and root, ALL and the sandbox
// account itself.
func SudoersRule(hostUser string) (string, error) {
	if !sudoersUserName.MatchString(hostUser) || hostUser == "root" || hostUser == "ALL" ||
		hostUser == SandboxUser {
		return "", fmt.Errorf("user name %q cannot be written into a sudo rule safely", hostUser)
	}
	return "# Written by `yolo yolo-account create` for " + hostUser + ". Lets " + hostUser +
		" run commands as " + SandboxUser + ",\n" +
		"# never as root, without a password. `yolo yolo-account remove` removes it; setup rewrites it.\n" +
		"Defaults>" + SandboxUser + " secure_path=\"/usr/bin:/bin:/usr/sbin:/sbin\"\n" +
		hostUser + " ALL=(" + SandboxUser + ") NOPASSWD: " + envBin + "\n", nil
}

// StagingRootFacts is what the launch observed about a staging root, gathered without following
// a link (the impure half lives in real.go).
type StagingRootFacts struct {
	Exists, IsDir, IsSymlink, HasACL bool
	UID                              uint32
	Mode                             os.FileMode // permission bits only
	Err                              error       // a failure other than not-exist
}

// StagingRootVerdict is JudgeStagingRoot's answer.
type StagingRootVerdict int

const (
	// StagingRootOK: a directory the caller owns, writable by nobody else, with no access list.
	StagingRootOK StagingRootVerdict = iota
	// StagingRootAbsent: nothing there; setup creates it.
	StagingRootAbsent
	// StagingRootForeign: it belongs to another account, so whoever owns it controls what the
	// sandbox reads. A launch refuses it; setup never adopts it.
	StagingRootForeign
	// StagingRootUnsafe: a link, not a directory, writable by group or others, or carrying an
	// access list that could let another account write.
	StagingRootUnsafe
	// StagingRootUnknown: it could not be inspected. Unknown is a refusal, never a pass.
	StagingRootUnknown
)

func (v StagingRootVerdict) String() string {
	switch v {
	case StagingRootOK:
		return "ok"
	case StagingRootAbsent:
		return "absent"
	case StagingRootForeign:
		return "foreign"
	case StagingRootUnsafe:
		return "unsafe"
	case StagingRootUnknown:
		return "unknown"
	}
	return fmt.Sprintf("StagingRootVerdict(%d)", int(v))
}

// JudgeStagingRoot decides whether a launch running as euid may stage into the root f describes.
// A link is unsafe whoever owns it, since its owner is not the target's; otherwise ownership is
// judged before the mode, so a squatted root is named as someone else's rather than as unsafe.
func JudgeStagingRoot(f StagingRootFacts, euid int) StagingRootVerdict {
	switch {
	case f.Err != nil:
		return StagingRootUnknown
	case !f.Exists:
		return StagingRootAbsent
	case f.IsSymlink:
		return StagingRootUnsafe
	case euid < 0 || f.UID != uint32(euid):
		return StagingRootForeign
	case !f.IsDir, f.Mode.Perm()&0o022 != 0, f.HasACL:
		return StagingRootUnsafe
	}
	return StagingRootOK
}
