package integration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE DENY-BY-DEFAULT PROFILE'S REGISTRY AND POLICY SUITE (docs/design/macos-user-model-plan.md
// slices 4 and 10).
//
// The same convention as macosuserseatbelt_test.go's seatbeltRules, for the profile
// macosuser.DenyDefaultProfile renders: every #seatbelt-test-id: it carries is named here,
// with either the runtime cases that prove it or a written reason none does. Its own
// registry, not more entries in seatbeltRules, because slice 13 deletes the old profile and
// its registry whole (the plan's Traps).
//
// The old profile's ids that this one keeps appear in both registries: the same id on a rule
// with the same effect. The ids the old profile has and this one does not are SUBSUMED by
// deny-default (write-outside-deny, volumes-read-deny, users-read-deny, file-ioctl-deny) or
// RETIRED (boot-volume-read-allow); the plan's Test-id ledger records each.
//
// Slice 10 ported the prototype runbook's checks (a, b, c, d and e of
// docs/plans/runbooks/macos-user-model-prototype/run-checks.sh) into the runtime cases below,
// each with its bare control, and added a case for every rule the runbook did not reach. Case
// names keep the runbook's check id where one exists (b4, d2, …), so a result here and a line
// of a human's run-checks.sh output can be read side by side.
//
// A REFUSAL CARRIES THE ID OF THE RULE THAT DECIDES IT. Under `(deny default)` most refusals
// are the posture's, not a named deny's: nothing allows the access in the first place. Those
// cases carry deny-default. A named deny is proved only by a case where an allow above it
// would otherwise let the access through, which is why several cases render a VARIANT of the
// profile (a context source or machine dir that names the denied path). A named deny that no
// input can put under an allow stays unproven below, with that reason: its outcome case exists
// and carries deny-default, and deleting the named deny changes no outcome.
//
// THE RUN IS SELF MODE: the profile runs as the invoking user, as the ruled model's default
// does. Account mode runs the same profile text (the ruling), and its crossing is the launch
// suites' (macosuserseatbelt_test.go's gate, macosuseraccounthome_test.go).

// denyDefaultBeltAndBraces is the reason a closing deny has no case of its own.
func denyDefaultBeltAndBraces(what, outcomeCase string) string {
	return "belt and braces under (deny default): " + what + ", so no ProfileParams input can " +
		"put this deny under an allow, and deleting it changes no outcome. " + outcomeCase +
		" measures the refusal and carries deny-default, the rule that decides it."
}

// denyDefaultRules must name every id the deny-default profile carries, and only those.
var denyDefaultRules = []seatbeltRule{
	// The posture, and the allow list (the prototype's blocks, in order).
	{id: "deny-default"},
	{id: "system-read-allow"},
	{id: "host-software-hide-deny"},
	{id: "host-software-read-allow"},
	{id: "host-software-metadata-allow"},
	{id: "host-preferences-read-allow"},
	{id: "apple-toolchain-read-allow"},
	{id: "ancestor-literal-allow"},
	{id: "workspace-read-allow"},
	{id: "workspace-write-allow"},
	{id: "home-allow"},
	{id: "tmp-allow"},
	{id: "user-tmp-allow"},
	{id: "shared-tmp-allow"},
	{id: "machine-dir-allow"},
	{id: "dev-io-allow"},
	{id: "device-ioctl-allow"},
	{id: "context-write-allow"},
	{id: "cache-relocation-write-allow"},
	{id: "cache-alias-allow"},
	// The write carve-outs, after every write allow.
	{id: "workspace-readonly-deny"},
	{id: "home-content-write-deny"},
	{id: "home-content-anchor-deny"},
	{id: "context-readonly-deny"},
	{id: "file-ioctl-tty-allow"},
	{id: "staging-root-read-allow"},
	{id: "context-read-allow"},
	// This session's host-services dir, by exact path (MMP-D15), and its liveness lock.
	{id: "services-dir-read-allow"},
	{id: "services-lock-deny"},
	{id: "cache-relocation-read-allow"},
	// Processes, services, network.
	{id: "process-allow"},
	{id: "cross-process-procargs-deny"},
	{id: "cross-process-pidinfo-deny", unproven: denyDefaultBeltAndBraces(
		"process-allow grants process-info* only on (target same-sandbox), so another process's "+
			"pidinfo is already refused", "b13_pidinfo_of_your_process_refused")},
	{id: "same-sandbox-pidinfo-allow"},
	{id: "mach-lookup-allow"},
	{id: "network-allow"},
	{id: "loopback-allow"},
	{id: "unix-socket-allow"},
	{id: "nix-daemon-allow"},
	// The closing block of explicit denies.
	{id: "host-secrets-deny"},
	{id: "library-keychains-deny"},
	{id: "system-keychains-deny"},
	{id: "raw-device-deny"},
	{id: "macos-log-deny", unproven: denyDefaultBeltAndBraces(
		"no allow names the unified log's stores under /private/var/db", "macos_log_store_read_refused")},
	{id: "launchd-listener-deny"},
	{id: "ssh-agent-socket-deny", unproven: denyDefaultBeltAndBraces(
		"no allow admits a unix-socket connect but mDNSResponder's and the sandbox's own "+
			"roots' (workspace, HOME, TMPDIR), and no input makes ~/.ssh one of those",
		"ssh_agent_socket_connect_refused")},
	{id: "container-socket-deny"},
	{id: "container-socket-connect-deny", unproven: denyDefaultBeltAndBraces(
		"no allow admits a unix-socket connect but mDNSResponder's and the sandbox's own "+
			"roots' (workspace, HOME, TMPDIR), and no input makes ~/.docker one of those",
		"container_socket_connect_refused")},
	{id: "service-deny", unproven: denyDefaultBeltAndBraces(
		"mach-lookup-allow lists none of securityd, the pasteboard, Apple Events or "+
			"LaunchServices", "b4_keychain_item_unreadable")},
	{id: "appleevent-send-deny"},
}

// denyDefaultCaseIDs is the rule id of every runtime case against the deny-default profile.
func denyDefaultCaseIDs() map[string]int {
	ids := map[string]int{}
	for _, c := range denyDefaultCases() {
		ids[c.id]++
	}
	return ids
}

// ---------------------------------------------------------------------------
// The cases.
// ---------------------------------------------------------------------------

// denyDefaultFixtures is the tree, processes and host facts every case is written against.
type denyDefaultFixtures struct {
	root     string // /Users/Shared/yolo-dd-XXXX — the workspace's parent, traversal only
	ws       string // <root>/ws                  — the workspace, read-write
	home     string // <ws>/.yolo/home            — the agent's HOME (MMP-D5)
	tmp      string // <root>/tmp                 — the launch's own TMPDIR
	outside  string // <root>/outside             — a sibling checkout, granted by nothing
	readonly string // <ws>/vendored              — workspace_readonly
	stateDir string // <home>/.claude             — an agent's state dir, holding staged content
	skills   string // <stateDir>/skills          — staged, write-protected
	machine  string // <root>/machine/.cache      — a machine-root shared dir (MMP-D6)
	ctxRO    string // <root>/ctx/ro              — read-only context source
	ctxRW    string // <root>/ctx/rw              — read-write context source
	ctxROWS  string // <ws>/ctxro                 — a read-only source inside the workspace (variant)
	reloc    string // <root>/caches/hf           — a cache relocation target
	// fakeHome is a stand-in for the human's home, under the fixture root. The closing denies
	// name paths under the HostHome input; a variant that names fakeHome as HostHome AND grants
	// it whole as a machine dir is the one profile where an allow covers ~/.ssh, so a refusal
	// there is the closing deny's and nothing else's.
	fakeHome  string
	hostHome  string // the invoking human's real home, resolved
	decoy     string // a 0600 file of yours in hostHome (b1)
	project   string // a decoy project outside /Users: /private/var/tmp/yolo-dd-clientX-… (c1)
	darwinTmp string // getconf DARWIN_USER_TEMP_DIR, resolved; "" when it could not be read
	user      string // the invoking user, who the sandbox runs as
	nonce     string
	// staged is a directory under the real staging root holding a seed file; stagedWhy says
	// why it is empty when it is (seatbeltStagingFixture).
	staged, stagedWhy string
	canaryPID         int    // a process of yours OUTSIDE the sandbox (b12, b13)
	canaryWord        string // a word on its command line
	loopPort          int    // a TCP listener of yours on 127.0.0.1, outside the sandbox (d1)
	loopPort6         int    // the same on [::1], or 0 when this Mac has no IPv6 loopback (d2)
	// services is this session's host-services dir, /private/tmp/yolo-host-services-<8hex>-<n>,
	// holding an endpoint file; sibling is another session's, granted by nothing (MMP-D15).
	services, sibling string
	python            string // a python3 the profile lets run (Homebrew or nix), or ""
	listener          string // a launchd Listeners socket path, or "" when none exists
	keychainService   string // the throwaway login-keychain item (b4), "" when not added
	keychainSecret    string
	keychainWhy       string // why the item could not be added
}

type denyDefaultCase struct {
	name string
	id   string // the #seatbelt-test-id: this case proves
	why  string // printed on failure
	// script is run by /bin/bash, bare and then under the profile, from the workspace with
	// denyDefaultEnv's environment.
	script func(denyDefaultFixtures) string
	want   seatbeltWant
	// refusal, when set, is text a wantRefused run's output must carry.
	refusal string
	// marker, for wantMarkerHidden: what the bare run must print and the sandboxed run must not.
	marker func(denyDefaultFixtures) string
	// ran, for wantMarkerHidden: what the sandboxed run must print to show the probe ran at
	// all, so a probe the profile could not even start does not pass as "hidden".
	ran string
	// needs returns why this host cannot run the case, or "". The case then skips, countably.
	needs func(denyDefaultFixtures) string
	// variant edits this case's profile inputs; nil runs the base profile.
	variant func(denyDefaultFixtures, *macosuser.ProfileParams)
	// optionalControl: a bare control that fails means this host cannot do the thing at all
	// (no Full Disk Access, no Automation consent, a locked keychain), so the case skips
	// naming why rather than failing. Every other case fails on a broken control.
	optionalControl bool
	// escaped, when set, is checked after the sandboxed run and returns a description of an
	// effect that outlived it (b11's launchd job), or "".
	escaped         func(*testing.T, denyDefaultFixtures) string
	needsUnifiedLog bool
}

// denyDefaultCLT is the Command Line Tools root the apple-toolchain block names.
const denyDefaultCLT = "/Library/Developer/CommandLineTools"

// denyDefaultPath is the PATH every run gets, the runbook's SB_PATH.
const denyDefaultPath = "/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin:" +
	"/nix/var/nix/profiles/default/bin:/run/current-system/sw/bin"

// denyDefaultProbePy is written into the workspace: `procargs PID` prints another process's
// argv through sysctl kern.procargs2 (no setuid binary: /bin/ps is setuid root, and the first
// Mac run showed the sandbox refusing to exec it at all), `pidpath [PID]` prints a process's
// executable through proc_pidpath, which is process-info-pidinfo. It prints denyDefaultProbeRan
// first, so a refusal and a probe that never started are told apart.
const denyDefaultProbePy = `import ctypes, os, sys
print("DD_PROBE_RAN", flush=True)
libc = ctypes.CDLL(None, use_errno=True)
what = sys.argv[1]
pid = int(sys.argv[2]) if len(sys.argv) > 2 else os.getpid()
if what == "procargs":
    mib = (ctypes.c_int * 3)(1, 49, pid)  # CTL_KERN, KERN_PROCARGS2, pid
    size = ctypes.c_size_t(0)
    if libc.sysctl(mib, 3, None, ctypes.byref(size), None, 0) != 0:
        sys.exit("sysctl kern.procargs2: errno %d" % ctypes.get_errno())
    buf = ctypes.create_string_buffer(size.value)
    if libc.sysctl(mib, 3, buf, ctypes.byref(size), None, 0) != 0:
        sys.exit("sysctl kern.procargs2: errno %d" % ctypes.get_errno())
    print(buf.raw[4:size.value].replace(b"\0", b" ").decode(errors="replace"))
elif what == "pidpath":
    buf = ctypes.create_string_buffer(4096)
    if libc.proc_pidpath(pid, buf, 4096) <= 0:
        sys.exit("proc_pidpath(%d): errno %d" % (pid, ctypes.get_errno()))
    print(buf.value.decode())
else:
    sys.exit("unknown probe " + what)
`

const denyDefaultProbeRan = "DD_PROBE_RAN"

// unixConnectProbe exits 0 only when a unix-socket connect to the path succeeded.
func unixConnectProbe(p string) string {
	return "/usr/bin/perl -MIO::Socket::UNIX -e 'IO::Socket::UNIX->new(Peer => $ARGV[0]) or " +
		"die \"connect: $!\\n\"; print \"connected\\n\"' " + sh(p)
}

// tcpConnectProbe exits 0 only when a TCP connect to 127.0.0.1:port succeeded.
func tcpConnectProbe(port int) string {
	return "(exec 3<>/dev/tcp/127.0.0.1/" + strconv.Itoa(port) + ") && echo connected"
}

// needsPath skips a case on a host without p.
func needsPath(p, what string) func(denyDefaultFixtures) string {
	return func(denyDefaultFixtures) string {
		if _, err := os.Stat(p); err != nil {
			return fmt.Sprintf("%s: %s is not on this Mac (%v)", what, p, err)
		}
		return ""
	}
}

func needsPython(f denyDefaultFixtures) string {
	if f.python == "" {
		return "no python3 the profile lets run: neither /opt/homebrew/bin, /usr/local/bin nor the " +
			"default nix profile has one (the Command Line Tools' python3 lives outside the " +
			"apple-toolchain allow)"
	}
	return ""
}

// unixBindConnectProbe exits 0 only when a unix socket bound at p (a shell word, expanded)
// accepted a connect to it.
func unixBindConnectProbe(p string) string {
	return "p=" + p + `; rm -f "$p"; /usr/bin/perl -MIO::Socket::UNIX -e '` +
		`$l = IO::Socket::UNIX->new(Local => $ARGV[0], Listen => 1) or die "bind: $!\n"; ` +
		`$c = IO::Socket::UNIX->new(Peer => $ARGV[0]) or die "connect: $!\n"; ` +
		`$l->accept or die "accept: $!\n"; print "connected\n"' "$p" && rm -f "$p" && echo ` + seatbeltOK
}

// sharedTmp is the `macos_user_shared_tmp` hatch's profile (OQ-MM8, MMP-D18): the shared /tmp
// granted read-write, which the production profile closes.
func sharedTmp(_ denyDefaultFixtures, p *macosuser.ProfileParams) { p.Tmp = macosuser.TmpShared }

// grantFakeHome is the closing-deny variant: the stand-in home is the profile's HostHome and is
// granted whole, read-write, as a machine dir.
func grantFakeHome(f denyDefaultFixtures, p *macosuser.ProfileParams) {
	p.HostHome = f.fakeHome
	p.MachineDirs = append(p.MachineDirs, f.fakeHome)
}

// denyDefaultCases is the whole suite. A pure function of the fixtures, so the registry test
// walks it on Linux.
func denyDefaultCases() []denyDefaultCase {
	return []denyDefaultCase{
		// --- (a) the agent's own files and tools.
		{
			name: "a1_workspace_write_allowed",
			id:   "workspace-write-allow",
			why:  "the positive control for the write policy: a profile denying every write would pass every refusal here.",
			script: func(f denyDefaultFixtures) string {
				p := sh(f.ws + "/a1.txt")
				return "echo hi >" + p + " && cat " + p + " >/dev/null && rm " + p + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name:   "a1_workspace_read_allowed",
			id:     "workspace-read-allow",
			why:    "the workspace sits under /Users/Shared, which nothing else in the profile allows.",
			script: func(f denyDefaultFixtures) string { return "cat " + sh(f.ws+"/seed") },
			want:   wantAllowed,
		},
		{
			name: "a2_home_read_write_allowed",
			id:   "home-allow",
			why:  "the agent's HOME is <workspace>/.yolo/home; its own state lives there as real directories (MMP-D5).",
			script: func(denyDefaultFixtures) string {
				return `mkdir -p "$HOME/.config/x" && echo hi >"$HOME/.config/x/f" && cat "$HOME/.config/x/f" >/dev/null && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a2_agent_state_beside_staged_content_writable",
			id:   "home-allow",
			why:  "the other half of write-protecting staged content: the agent's own state beside it stays writable.",
			script: func(f denyDefaultFixtures) string {
				p := sh(f.stateDir + "/projects/probe")
				return "touch " + p + " && rm " + p + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a3_own_tmpdir_writable",
			id:   "tmp-allow",
			why:  "the launch's own TMPDIR sits under the fixture root, which only tmp-allow grants.",
			script: func(denyDefaultFixtures) string {
				return `f=$(mktemp) && echo hi >"$f" && cat "$f" >/dev/null && rm "$f" && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a3_user_temp_dir_writable",
			id:   "user-tmp-allow",
			why: "the user's DARWIN_USER_TEMP_DIR stays open with /tmp closed (OQ-MM8): frameworks " +
				"write there regardless of TMPDIR.",
			needs: func(f denyDefaultFixtures) string {
				if f.darwinTmp == "" {
					return "getconf DARWIN_USER_TEMP_DIR answered nothing on this Mac"
				}
				return ""
			},
			script: func(f denyDefaultFixtures) string {
				return `g=$(mktemp ` + sh(f.darwinTmp+"/yolo-dd-a3.XXXXXX") + `) && echo hi >"$g" && rm "$g" && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "shared_tmp_write_refused",
			id:   "deny-default",
			why: "OQ-MM8 closes the shared /tmp: nothing in the production profile grants it, so a " +
				"write to /tmp/x is refused while $TMPDIR (a3_own_tmpdir_writable) works.",
			script: func(f denyDefaultFixtures) string { return "echo hi >" + sh("/tmp/yolo-dd-mm8-"+f.nonce) },
			want:   wantRefused,
		},
		{
			name: "a3_shared_tmp_writable_with_the_hatch",
			id:   "shared-tmp-allow",
			why: "the macos_user_shared_tmp hatch reopens /tmp, which many tools hardcode, read and " +
				"write, in both spellings.",
			variant: sharedTmp,
			script: func(f denyDefaultFixtures) string {
				p := sh("/tmp/yolo-dd-a3-" + f.nonce)
				q := sh("/private/tmp/yolo-dd-a3p-" + f.nonce)
				return "echo hi >" + p + " && cat " + p + " >/dev/null && rm " + p +
					" && echo hi >" + q + " && rm " + q + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a4_signal_own_child_allowed",
			id:   "process-allow",
			why:  "signals are allowed on (target same-sandbox): a shell must still kill its own jobs.",
			script: func(denyDefaultFixtures) string {
				return `sleep 30 & p=$!; kill $p || exit 1; wait $p; [ $? -eq 143 ] && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a5_git_commit_with_command_line_tools",
			id:   "apple-toolchain-read-allow",
			why: "the Command Line Tools' git, run by its own path so xcrun and an Xcode under " +
				"/Applications (a host software tree) are not what makes it work.",
			needs: needsPath(denyDefaultCLT+"/usr/bin/git", "Command Line Tools git"),
			script: func(denyDefaultFixtures) string {
				g := denyDefaultCLT + "/usr/bin/git"
				return `d=$(mktemp -d "$PWD/repo.XXXXXX") && cd "$d" && ` + g + ` init -q && echo x >f && ` +
					g + ` add f && ` + g + ` -c user.name=p -c user.email=p@example.com commit -qm init && ` +
					g + ` log --oneline >/dev/null && cd .. && rm -rf "$d" && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a6_build_c_with_command_line_tools",
			id:   "apple-toolchain-read-allow",
			why:  "a build: the Command Line Tools' cc against their own SDK, by path for a5's reason.",
			needs: func(denyDefaultFixtures) string {
				for _, p := range []string{denyDefaultCLT + "/usr/bin/cc", denyDefaultCLT + "/SDKs/MacOSX.sdk"} {
					if _, err := os.Stat(p); err != nil {
						return "Command Line Tools build: " + p + " is not on this Mac"
					}
				}
				return ""
			},
			script: func(denyDefaultFixtures) string {
				return `d=$(mktemp -d "$PWD/cc.XXXXXX") && printf 'int main(void){return 0;}\n' >"$d/m.c" && ` +
					`SDKROOT=` + denyDefaultCLT + `/SDKs/MacOSX.sdk ` + denyDefaultCLT + `/usr/bin/cc -o "$d/m" "$d/m.c" && ` +
					`"$d/m" && rm -rf "$d" && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name:  "a7_homebrew_binary_runs",
			id:    "host-software-read-allow",
			why:   "OQ-MM9's production value leaves /opt/homebrew readable, so its tools run.",
			needs: func(f denyDefaultFixtures) string { return needsBrewBinary() },
			script: func(denyDefaultFixtures) string {
				return sh(brewBinary()) + " --version >/dev/null && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name:  "a8_nix_binary_runs",
			id:    "system-read-allow",
			why:   "/nix/store and the nix profiles are system read roots: every nix-installed tool.",
			needs: needsPath("/nix/var/nix/profiles/default/bin/nix", "nix"),
			script: func(denyDefaultFixtures) string {
				return "/nix/var/nix/profiles/default/bin/nix --version >/dev/null && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a9_https_with_certificate_trust",
			id:   "network-allow",
			why:  "outbound IP and trustd: every HTTPS client (gh, mise, curl) needs both.",
			script: func(denyDefaultFixtures) string {
				return "curl -sSI --max-time 20 https://github.com >/dev/null && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "a12_system_fonts_readable",
			id:   "system-read-allow",
			why:  "/System/Library and /Library/Fonts are read roots; fonts.conf names them.",
			script: func(denyDefaultFixtures) string {
				return "ls /System/Library/Fonts >/dev/null && ls /Library/Fonts >/dev/null && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "user_lookup_allowed",
			id:   "mach-lookup-allow",
			why: "getpwuid goes to opendirectoryd's libinfo service, on the allowlist; a runner's or a " +
				"directory user is not in /etc/passwd, so nothing else can answer.",
			script: func(denyDefaultFixtures) string { return "id -un >/dev/null && echo " + seatbeltOK },
			want:   wantAllowed,
		},
		{
			name: "xcode_shim_make_runs",
			id:   "system-read-allow",
			why: "/usr/bin/make, cc and lldb are xcrun shims that read Xcode's license record, " +
				"/Library/Preferences/com.apple.dt.Xcode.plist; refused it, they print \"You have not " +
				"agreed to the Xcode license agreements\" on a Mac where the license is accepted, so " +
				"nothing builds from source. The fixture's TMPDIR is fresh, so no xcrun_db from an " +
				"earlier run answers for the shim.",
			needs:  needsPath("/Library/Preferences/com.apple.dt.Xcode.plist", "an accepted Xcode license"),
			script: func(denyDefaultFixtures) string { return "/usr/bin/make --version >/dev/null && echo " + seatbeltOK },
			want:   wantAllowed,
		},
		{
			name: "user_preferences_read_refused",
			id:   "deny-default",
			why: "the Xcode shims' allow names one file in /Library/Preferences; your own " +
				"~/Library/Preferences stays closed, by path and through cfprefsd alike.",
			needs: func(f denyDefaultFixtures) string {
				return needsPath(filepath.Join(f.hostHome, "Library/Preferences/com.apple.dock.plist"), "your Dock preferences")(f)
			},
			script: func(f denyDefaultFixtures) string {
				return "/usr/bin/defaults read " + sh(filepath.Join(f.hostHome, "Library/Preferences/com.apple.dock.plist")) + " >/dev/null"
			},
			want: wantRefused,
		},
		{
			name: "workspace_parent_listing_allowed",
			id:   "ancestor-literal-allow",
			why: "each ancestor of a granted root is a literal: git and Claude Code readdir() them at " +
				"start. The sibling case below is the other half: a literal never reaches downward.",
			script: func(f denyDefaultFixtures) string { return "ls " + sh(f.root) + " >/dev/null && echo " + seatbeltOK },
			want:   wantAllowed,
		},
		{
			name:   "sibling_of_the_workspace_refused",
			id:     "deny-default",
			why:    "a checkout beside the workspace: its name is listed by the ancestor literal, its contents granted by nothing.",
			script: func(f denyDefaultFixtures) string { return "cat " + sh(f.outside+"/secret") },
			want:   wantRefused,
		},
		{
			name: "machine_dir_writable",
			id:   "machine-dir-allow",
			why:  "a machine-root shared dir (MMP-D6) sits outside the workspace and HOME; only this allow opens it.",
			script: func(f denyDefaultFixtures) string {
				p := sh(f.machine + "/probe")
				return "touch " + p + " && rm " + p + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "cache_alias_target_writable",
			id:   "cache-alias-allow",
			why: "a shared package cache (MMP-D12) is a host directory outside the workspace and HOME; " +
				"the run-as-you launch's alias opens it, and the sibling case above shows nothing else does.",
			variant: func(f denyDefaultFixtures, p *macosuser.ProfileParams) {
				p.CacheAliases = []macosuser.CacheAlias{{Tool: "pip", Link: f.home + "/Library/Caches/pip", Target: f.outside}}
			},
			script: func(f denyDefaultFixtures) string {
				p := sh(f.outside + "/alias-probe")
				return "touch " + p + " && rm " + p + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "nix_daemon_socket_reachable",
			id:   "nix-daemon-allow",
			why: "the sandbox's nix is a client of the host daemon (NIX_REMOTE=daemon); the network " +
				"section allows no other unix-socket connect but mDNSResponder's.",
			needs: needsPath(macosuser.NixDaemonSocket, "the host nix daemon"),
			script: func(denyDefaultFixtures) string {
				return unixConnectProbe(macosuser.NixDaemonSocket) + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name:   "dev_null_writable",
			id:     "dev-io-allow",
			why:    "every shell redirects to /dev/null; the /dev metadata allow does not grant a write.",
			script: func(denyDefaultFixtures) string { return "printf x >/dev/null && echo " + seatbeltOK },
			want:   wantAllowed,
		},
		{
			name:   "undeclared_device_write_refused",
			id:     "deny-default",
			why:    "/dev/zero is read-allowed for entropy probes, never written, unless config.devices names it.",
			script: func(denyDefaultFixtures) string { return "printf x >/dev/zero" },
			want:   wantRefused,
		},
		{
			name:    "declared_device_write_allowed",
			id:      "device-ioctl-allow",
			why:     "the same write under a profile whose devices list names /dev/zero: a declared node gets read, write and ioctl.",
			variant: func(_ denyDefaultFixtures, p *macosuser.ProfileParams) { p.Devices = []string{"/dev/zero"} },
			script:  func(denyDefaultFixtures) string { return "printf x >/dev/zero && echo " + seatbeltOK },
			want:    wantAllowed,
		},
		{
			name: "dtracehelper_ioctl_allowed",
			id:   "file-ioctl-tty-allow",
			why: "dyld's DTRACEHIOC_ADDDOF on /dev/dtracehelper is the one ioctl Seatbelt is measured to " +
				"judge (macosuserseatbelt_test.go); this profile allows it by name.",
			script: func(denyDefaultFixtures) string { return dtraceHelperIoctlProbe() },
			want:   wantAllowed,
		},
		{
			name: "pty_ioctls_allowed",
			id:   "file-ioctl-tty-allow",
			why:  "`script` allocates a real pty and `stty` on it is the terminal ioctl every agent TUI needs.",
			script: func(denyDefaultFixtures) string {
				return "script -q /dev/null /bin/sh -c 'stty -a >/dev/null 2>&1 && echo " + seatbeltOK + "'"
			},
			want: wantAllowed,
		},
		{
			name: "staging_root_read_allowed",
			id:   "staging-root-read-allow",
			why:  "the guest execs the staged yolo and reads its env files from the host-owned staging root (MMP-D8).",
			script: func(f denyDefaultFixtures) string {
				if f.staged == "" {
					return "echo " + sh("no staging-root fixture: "+f.stagedWhy) + " >&2; exit 1"
				}
				return "cat " + sh(f.staged+"/seed")
			},
			want: wantAllowed,
		},
		{
			name:   "context_ro_source_readable",
			id:     "context-read-allow",
			why:    "a context source beside the workspace is opened by its read allow and nothing else.",
			script: func(f denyDefaultFixtures) string { return "cat " + sh(f.ctxRO+"/seed") },
			want:   wantAllowed,
		},
		{
			name: "context_rw_source_writable",
			id:   "context-write-allow",
			why:  "a read-write context source is outside every other writable root.",
			script: func(f denyDefaultFixtures) string {
				p := sh(f.ctxRW + "/probe")
				return "touch " + p + " && rm " + p + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "context_ro_source_inside_the_workspace_write_refused",
			id:   "context-readonly-deny",
			why: "a read-only source inside the workspace (the launch's siting refuses one; the profile " +
				"text does not depend on that): the workspace write allow covers it, so only the " +
				"read-only deny after every write allow can refuse the write.",
			variant: func(f denyDefaultFixtures, p *macosuser.ProfileParams) {
				p.Context = append(p.Context, macosuser.ContextLink{Dest: "/ctx/inws", Source: f.ctxROWS, Dir: true})
			},
			script: func(f denyDefaultFixtures) string {
				p := sh(f.ctxROWS + "/probe")
				return "touch " + p + " && rm -f " + p
			},
			want:    wantRefused,
			refusal: "Operation not permitted",
		},
		{
			name:   "cache_relocation_target_readable",
			id:     "cache-relocation-read-allow",
			why:    "a relocation target beside the workspace is opened by its read allow and nothing else.",
			script: func(f denyDefaultFixtures) string { return "cat " + sh(f.reloc+"/seed") },
			want:   wantAllowed,
		},
		{
			name: "cache_relocation_target_writable",
			id:   "cache-relocation-write-allow",
			why:  "a relocation target is outside every other writable root.",
			script: func(f denyDefaultFixtures) string {
				p := sh(f.reloc + "/probe")
				return "touch " + p + " && rm " + p + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name:    "workspace_readonly_write_refused",
			id:      "workspace-readonly-deny",
			why:     "workspace_readonly is inside the writable workspace, so only the later deny refuses it.",
			script:  func(f denyDefaultFixtures) string { return "touch " + sh(f.readonly+"/hook") },
			want:    wantRefused,
			refusal: "Operation not permitted",
		},
		{
			name:    "home_content_skill_write_refused",
			id:      "home-content-write-deny",
			why:     "a staged skill is a copy in a HOME the agent owns; the file mode cannot protect it, only this deny.",
			script:  func(f denyDefaultFixtures) string { return "printf x >>" + sh(f.skills+"/demo/SKILL.md") },
			want:    wantRefused,
			refusal: "Operation not permitted",
		},
		{
			name: "host_preferences_readable",
			id:   "host-preferences-read-allow",
			why:  "CoreFoundation finds the locale through the account's home, not $HOME; locale files only.",
			needs: func(f denyDefaultFixtures) string {
				return needsPath(f.hostHome+"/.CFUserTextEncoding", "locale file")(f)
			},
			script: func(f denyDefaultFixtures) string {
				return "cat " + sh(f.hostHome+"/.CFUserTextEncoding") + " >/dev/null && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		// --- (c) outside /Users, and the software trees.
		{
			name:   "c3_homebrew_tree_readable",
			id:     "host-software-read-allow",
			why:    "the install tree itself, as the runbook's c3 reads it.",
			needs:  needsPath("/opt/homebrew/bin", "Homebrew"),
			script: func(denyDefaultFixtures) string { return "ls /opt/homebrew/bin >/dev/null && echo " + seatbeltOK },
			want:   wantAllowed,
		},
		{
			name:   "host_software_parent_stat_allowed",
			id:     "host-software-metadata-allow",
			why:    "realpath() lstats /opt on the way to /opt/homebrew; metadata only on that parent.",
			needs:  needsPath("/opt/homebrew", "Homebrew"),
			script: func(denyDefaultFixtures) string { return "test -d /opt && echo " + seatbeltOK },
			want:   wantAllowed,
		},
		{
			name:   "host_software_parent_listing_refused",
			id:     "deny-default",
			why:    "a stat of /opt is not its listing: /opt/src/clientX lives there (OQ-MM2).",
			needs:  needsPath("/opt/homebrew", "Homebrew"),
			script: func(denyDefaultFixtures) string { return "ls /opt" },
			want:   wantRefused,
		},
		{
			name: "usr_local_hidden_when_not_listed",
			id:   "host-software-hide-deny",
			why: "under OQ-MM9's other value /usr/local is hidden, and the /usr read allow covers it, so " +
				"only the hide deny right after that allow can refuse it.",
			needs: needsPath("/usr/local", "/usr/local"),
			variant: func(_ denyDefaultFixtures, p *macosuser.ProfileParams) {
				p.HostSoftware = []string{"/opt/homebrew", "/Applications"}
			},
			script: func(denyDefaultFixtures) string { return "ls /usr/local" },
			want:   wantRefused,
		},
		{
			name:  "c4_nix_store_readable",
			id:    "system-read-allow",
			why:   "a store path, as the runbook's c4 reads it.",
			needs: needsPath("/nix/var/nix/profiles/default", "nix"),
			script: func(denyDefaultFixtures) string {
				return `s=$(cd /nix/var/nix/profiles/default && pwd -P) && ls "$s" >/dev/null && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name:   "c1_project_outside_users_refused",
			id:     "deny-default",
			why:    "a project outside /Users (the runbook's /opt/src/clientX); here under /private/var/tmp, which no rule names.",
			script: func(f denyDefaultFixtures) string { return "cat " + sh(f.project+"/main.c") },
			want:   wantRefused,
		},
		{
			name:   "c2_project_parent_listing_refused",
			id:     "deny-default",
			why:    "the decoy project's parent: listing it would name every other project there.",
			script: func(f denyDefaultFixtures) string { return "ls " + sh(filepath.Dir(f.project)) },
			want:   wantRefused,
		},
		// --- (b) your secrets and your session.
		{
			name:   "b1_decoy_secret_in_your_home_refused",
			id:     "deny-default",
			why:    "a file in your home is granted by nothing; the agent's HOME is the workspace's.",
			script: func(f denyDefaultFixtures) string { return "cat " + sh(f.decoy) },
			want:   wantRefused,
		},
		{
			name:   "b2_your_ssh_dir_refused",
			id:     "deny-default",
			why:    "~/.ssh; under the production inputs nothing allows it, so the posture refuses it first.",
			needs:  func(f denyDefaultFixtures) string { return needsPath(f.hostHome+"/.ssh", "~/.ssh")(f) },
			script: func(f denyDefaultFixtures) string { return "ls -la " + sh(f.hostHome+"/.ssh") },
			want:   wantRefused,
		},
		{
			name: "b3_write_in_your_home_refused",
			id:   "deny-default",
			why:  "a write in your home. Self-cleaning: the bare control really creates the file.",
			script: func(f denyDefaultFixtures) string {
				p := sh(f.hostHome + "/yolo-dd-write-" + f.nonce)
				return "touch " + p + " && rm -f " + p
			},
			want: wantRefused,
		},
		{
			name: "b4_keychain_item_unreadable",
			id:   "deny-default",
			why: "a throwaway generic password in your login keychain, readable bare by /usr/bin/security " +
				"with no prompt (-T). An exit code cannot tell a refusal from \"not found\" (the first Mac " +
				"run got 44 either way), so the verdict is whether the secret comes out.",
			needs: func(f denyDefaultFixtures) string {
				if f.keychainService == "" {
					return "could not add a throwaway item to the login keychain (locked?): " + f.keychainWhy
				}
				return ""
			},
			script: func(f denyDefaultFixtures) string {
				svc := sh(f.keychainService)
				return "security find-generic-password -s " + svc + " -w; security find-generic-password -s " +
					svc + " -w " + sh(f.hostHome+"/Library/Keychains/login.keychain-db") + "; true"
			},
			want:            wantMarkerHidden,
			marker:          func(f denyDefaultFixtures) string { return f.keychainSecret },
			optionalControl: true,
		},
		{
			name: "b5_login_keychain_file_refused",
			id:   "deny-default",
			why:  "the login keychain's file; nothing allows it under the production inputs.",
			needs: func(f denyDefaultFixtures) string {
				return needsPath(f.hostHome+"/Library/Keychains/login.keychain-db", "login keychain")(f)
			},
			script: func(f denyDefaultFixtures) string {
				return "head -c 16 " + sh(f.hostHome+"/Library/Keychains/login.keychain-db") + " >/dev/null"
			},
			want: wantRefused,
		},
		{
			name: "b8_apple_events_to_finder_refused",
			id:   "appleevent-send-deny",
			why: "counting Finder's windows needs a real Apple Event, which needs Automation consent this " +
				"host may not have. Not `get name`: AppleScript answers that from the specifier without " +
				"sending an event, so it succeeds under any profile (measured on macOS 26.5, 2026-10-23).",
			script: func(denyDefaultFixtures) string {
				return `osascript -e 'with timeout of 5 seconds' -e 'tell application "Finder" to count windows' -e 'end timeout'`
			},
			want:            wantRefused,
			optionalControl: true,
		},
		{
			name: "b11_launchd_job_submit_refused",
			id:   "deny-default",
			why: "a launchd job runs outside the sandbox. The verdict is also checked from outside: a " +
				"submit that escaped while the in-sandbox `list` was refused is still a failure.",
			script: func(f denyDefaultFixtures) string {
				l := "yolo.dd." + f.nonce
				return "launchctl submit -l " + l + " -- /usr/bin/true && launchctl list " + l +
					" >/dev/null; rc=$?; launchctl remove " + l + " 2>/dev/null; exit $rc"
			},
			want: wantRefused,
			escaped: func(t *testing.T, f denyDefaultFixtures) string {
				l := "yolo.dd." + f.nonce
				if err := exec.Command("launchctl", "list", l).Run(); err != nil {
					return ""
				}
				_ = exec.Command("launchctl", "remove", l).Run()
				return "the launchd job " + l + " exists outside the sandbox (removed now)"
			},
		},
		{
			name: "b12_signal_your_process_refused",
			id:   "deny-default",
			why:  "signals are allowed on (target same-sandbox) only; a process of yours is out of reach.",
			script: func(f denyDefaultFixtures) string {
				return "kill -0 " + strconv.Itoa(f.canaryPID)
			},
			want: wantRefused,
		},
		{
			name: "b13_argv_of_your_process_hidden",
			id:   "cross-process-procargs-deny",
			why: "sysctl-read is allowed and then narrowed: kern.procargs2 of another process is where a " +
				"token passed as a flag sits. The refusal is invisible, so the case asserts a missing marker.",
			needs: needsPython,
			script: func(f denyDefaultFixtures) string {
				return sh(f.python) + " " + sh(f.ws+"/.ddprobe.py") + " procargs " + strconv.Itoa(f.canaryPID) + "; true"
			},
			want:   wantMarkerHidden,
			marker: func(f denyDefaultFixtures) string { return f.canaryWord },
			ran:    denyDefaultProbeRan,
		},
		{
			name:  "b13_pidinfo_of_your_process_refused",
			id:    "deny-default",
			why:   "proc_pidpath of a process of yours is process-info-pidinfo, granted only on (target same-sandbox).",
			needs: needsPython,
			script: func(f denyDefaultFixtures) string {
				return sh(f.python) + " " + sh(f.ws+"/.ddprobe.py") + " pidpath " + strconv.Itoa(f.canaryPID)
			},
			want: wantRefused,
		},
		{
			name: "own_pidinfo_allowed",
			id:   "same-sandbox-pidinfo-allow",
			why: "the re-allow after the pidinfo deny: a process may still inspect itself, which lsof, " +
				"ps of a child and runtimes' self-inspection need.",
			needs: needsPython,
			script: func(f denyDefaultFixtures) string {
				return sh(f.python) + " " + sh(f.ws+"/.ddprobe.py") + " pidpath && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		// --- (d) loopback: OQ-MM7, ruled 2026-10-23, allows all of it (MMP-D14).
		{
			name:   "d1_loopback_listener_reachable",
			id:     "loopback-allow",
			why:    "OQ-MM7: every port of your loopback is reachable, as a jail on the host network would reach it; no port is listed anywhere.",
			script: func(f denyDefaultFixtures) string { return tcpConnectProbe(f.loopPort) + " && echo " + seatbeltOK },
			want:   wantAllowed,
		},
		{
			name: "d2_ipv6_loopback_listener_reachable",
			id:   "loopback-allow",
			why:  `the same on ::1: "localhost:*" must cover the IPv6 loopback too.`,
			needs: func(f denyDefaultFixtures) string {
				if f.loopPort6 == 0 {
					return "this Mac has no IPv6 loopback to listen on"
				}
				return ""
			},
			script: func(f denyDefaultFixtures) string {
				return `/usr/bin/perl -MIO::Socket::IP -e 'IO::Socket::IP->new(PeerHost => "::1", PeerPort => $ARGV[0]) or ` +
					`die "connect: $!\n"; print "connected\n"' ` + strconv.Itoa(f.loopPort6) + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name: "d3_loopback_bind_and_accept",
			id:   "loopback-allow",
			why:  "a dev server in the sandbox binds a loopback port and accepts a connection to it.",
			script: func(denyDefaultFixtures) string {
				return `/usr/bin/perl -MIO::Socket::INET -e '` +
					`$l = IO::Socket::INET->new(LocalAddr => "127.0.0.1", LocalPort => 0, Listen => 1, ReuseAddr => 1) or die "bind: $!\n"; ` +
					`$c = IO::Socket::INET->new(PeerAddr => "127.0.0.1", PeerPort => $l->sockport) or die "connect: $!\n"; ` +
					`$l->accept or die "accept: $!\n"; print "connected\n"' && echo ` + seatbeltOK
			},
			want: wantAllowed,
		},
		// --- Unix sockets under the sandbox's own roots (MMP-D16).
		{
			name: "unix_socket_under_tmpdir_bind_and_connect",
			id:   "unix-socket-allow",
			why: "tmux, overmind and Python's multiprocessing bind a unix socket under $TMPDIR and " +
				"connect to it; under (deny default) both need the unix-socket allow.",
			script: func(denyDefaultFixtures) string { return unixBindConnectProbe(`"$TMPDIR"dd.sock`) },
			want:   wantAllowed,
		},
		{
			name:   "unix_socket_in_workspace_bind_and_connect",
			id:     "unix-socket-allow",
			why:    "overmind's ./.overmind.sock: a socket in the workspace, named by a relative path.",
			script: func(denyDefaultFixtures) string { return unixBindConnectProbe(".dd.sock") },
			want:   wantAllowed,
		},
		// --- This session's host-services dir (MMP-D15).
		{
			name: "own_session_endpoint_readable_without_shared_tmp",
			id:   "services-dir-read-allow",
			why: "the agent's clients read their endpoint files in this session's services dir; the " +
				"grant stands alone, the production profile closing the shared /tmp (OQ-MM8).",
			script: func(f denyDefaultFixtures) string {
				return "cat " + sh(f.services+"/demo.endpoint") + " && echo " + seatbeltOK
			},
			want: wantAllowed,
		},
		{
			name:   "sibling_session_endpoint_unreadable",
			id:     "deny-default",
			why:    "another session's services dir is granted by nothing with the shared /tmp closed.",
			script: func(f denyDefaultFixtures) string { return "cat " + sh(f.sibling+"/demo.endpoint") },
			want:   wantRefused,
		},
		{
			name:   "services_dir_parent_unlistable",
			id:     "deny-default",
			why:    "the grant is a stat of /private/tmp, never a listing that names every other session's dir.",
			script: func(denyDefaultFixtures) string { return "ls /private/tmp >/dev/null" },
			want:   wantRefused,
		},
		{
			name: "services_liveness_lock_refused",
			id:   "services-lock-deny",
			why: "a read-only fd can flock: the session's liveness lock is denied after the grant and " +
				"after the hatch's shared-/tmp allow, so the agent cannot hold its session alive.",
			variant: sharedTmp,
			script:  func(f denyDefaultFixtures) string { return "cat " + sh(f.services+"/.session.lock") },
			want:    wantRefused,
		},
		// --- (e) Full Disk Access.
		{
			name: "e1_full_disk_access_does_not_open_tcc",
			id:   "deny-default",
			why: "a process with Full Disk Access can read TCC.db; the sandbox it starts must still be " +
				"refused. Skipped where this test process has no Full Disk Access (the bare control fails).",
			script: func(f denyDefaultFixtures) string {
				return "head -c 16 " + sh(f.hostHome+"/Library/Application Support/com.apple.TCC/TCC.db") + " >/dev/null"
			},
			want:            wantRefused,
			optionalControl: true,
		},
		// --- The closing denies, each under an allow that would otherwise admit it.
		{
			name:    "granted_home_stand_in_readable",
			id:      "machine-dir-allow",
			why:     "the variant's premise: the stand-in home IS readable, so a refusal under it below is the closing deny's.",
			variant: grantFakeHome,
			script:  func(f denyDefaultFixtures) string { return "cat " + sh(f.fakeHome+"/plain") },
			want:    wantAllowed,
		},
		{
			name:    "ssh_key_under_a_granted_home_refused",
			id:      "host-secrets-deny",
			why:     "~/.ssh stays denied when an allow above names the whole home: the closing block beats every allow.",
			variant: grantFakeHome,
			script:  func(f denyDefaultFixtures) string { return "cat " + sh(f.fakeHome+"/.ssh/id_decoy") },
			want:    wantRefused,
		},
		{
			name:    "keychain_file_under_a_granted_home_refused",
			id:      "host-secrets-deny",
			why:     "~/Library/Keychains, under the same variant.",
			variant: grantFakeHome,
			script: func(f denyDefaultFixtures) string {
				return "cat " + sh(f.fakeHome+"/Library/Keychains/login.keychain-db")
			},
			want: wantRefused,
		},
		{
			name:    "container_socket_under_a_granted_home_hidden",
			id:      "container-socket-deny",
			why:     "~/.docker/run/docker.sock: a container runtime is a way out. A stat is file-read*, refused under a granted home.",
			variant: grantFakeHome,
			script:  func(f denyDefaultFixtures) string { return "test -e " + sh(f.fakeHome+"/.docker/run/docker.sock") },
			want:    wantRefused,
		},
		{
			name:    "container_socket_connect_refused",
			id:      "deny-default",
			why:     "a connect to that socket, live and accepting bare.",
			variant: grantFakeHome,
			script:  func(f denyDefaultFixtures) string { return unixConnectProbe(f.fakeHome + "/.docker/run/docker.sock") },
			want:    wantRefused,
		},
		{
			name:    "ssh_agent_socket_connect_refused",
			id:      "deny-default",
			why:     "a connect to an ssh agent socket under ~/.ssh/agent, live and accepting bare.",
			variant: grantFakeHome,
			script:  func(f denyDefaultFixtures) string { return unixConnectProbe(f.fakeHome + "/.ssh/agent/s") },
			want:    wantRefused,
		},
		{
			name: "library_keychains_under_a_context_grant_refused",
			id:   "library-keychains-deny",
			why: "System.keychain is 0644, so the deny is load-bearing; under a context source naming " +
				"/Library/Keychains, read-allowed above, the closing deny is what refuses.",
			needs: needsPath("/Library/Keychains", "/Library/Keychains"),
			variant: func(_ denyDefaultFixtures, p *macosuser.ProfileParams) {
				p.Context = append(p.Context, macosuser.ContextLink{Dest: "/ctx/kc", Source: "/Library/Keychains", Dir: true})
			},
			script: func(denyDefaultFixtures) string { return "ls /Library/Keychains" },
			want:   wantRefused,
		},
		{
			name:   "library_keychains_listing_refused",
			id:     "deny-default",
			why:    "the same listing under the production inputs, where nothing allows it.",
			needs:  needsPath("/Library/Keychains", "/Library/Keychains"),
			script: func(denyDefaultFixtures) string { return "ls /Library/Keychains" },
			want:   wantRefused,
		},
		{
			name:   "system_keychains_refused",
			id:     "system-keychains-deny",
			why:    "/System/Library/Keychains is under the /System/Library read root; the closing deny re-closes it.",
			script: func(denyDefaultFixtures) string { return "ls /System/Library/Keychains" },
			want:   wantRefused,
		},
		{
			name:   "raw_device_metadata_refused",
			id:     "raw-device-deny",
			why:    "the /dev metadata allow would admit a stat of /dev/disk0; the raw-device deny refuses it.",
			script: func(denyDefaultFixtures) string { return "ls -l /dev/disk0" },
			want:   wantRefused,
		},
		{
			name:            "macos_log_store_read_refused",
			id:              "deny-default",
			why:             "the unified log is the macos-log loophole's, read on the host; the sandbox never reads its store.",
			script:          func(denyDefaultFixtures) string { return macosLogStoreProbe },
			want:            wantRefused,
			needsUnifiedLog: true,
		},
		{
			name: "launchd_listener_socket_hidden",
			id:   "launchd-listener-deny",
			why: "launchd's Listeners socket (the ssh agent's and others') sits under /private/tmp, which " +
				"the hatch's shared /tmp allows (so the case runs under it), or under /private/var/run, " +
				"whose metadata is allowed; the deny hides even its stat.",
			variant: sharedTmp,
			needs: func(f denyDefaultFixtures) string {
				if f.listener == "" {
					return "no launchd Listeners socket under /private/tmp or /private/var/run on this Mac"
				}
				return ""
			},
			script: func(f denyDefaultFixtures) string { return "test -e " + sh(f.listener) },
			want:   wantRefused,
		},
		// LAST, deliberately: if the kernel lets the rename-away through and refuses only the
		// replacement, the state dir is left at its .moved name.
		{
			name: "home_content_anchor_replace_refused",
			id:   "home-content-anchor-deny",
			why: "a path deny protects a path, not an inode: moving the state dir aside and putting a " +
				"directory of the agent's own in its place would bypass the content deny. The anchor " +
				"refuses the unlink and the create.",
			script: func(f denyDefaultFixtures) string {
				d, moved := sh(f.stateDir), sh(f.stateDir+".moved")
				return "mv " + d + " " + moved + " && mkdir " + d + " && rmdir " + d + " && mv " + moved + " " + d
			},
			want: wantRefused,
		},
	}
}

// brewBinaries are the Homebrew tools a7 tries, the runbook's list.
var brewBinaries = []string{"jq", "rg", "fd", "wget", "gh", "git", "curl", "python3", "node"}

func brewBinary() string {
	for _, b := range brewBinaries {
		if p := "/opt/homebrew/bin/" + b; isExecutable(p) {
			return p
		}
	}
	return ""
}

func needsBrewBinary() string {
	if brewBinary() == "" {
		return "no Homebrew binary: none of " + strings.Join(brewBinaries, ", ") + " under /opt/homebrew/bin"
	}
	return ""
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// ---------------------------------------------------------------------------
// The runtime test.
// ---------------------------------------------------------------------------

// TestMacosUserDenyDefaultProfileEnforcesItsRules loads the deny-default profile, rendered by
// macosuser.DenyDefaultProfile with the production rulings, with sandbox-exec, and asks the
// kernel about every case above: each case runs bare first (the control) and then under the
// profile, as the invoking user.
func TestMacosUserDenyDefaultProfileEnforcesItsRules(t *testing.T) {
	requireMacosUserSeatbelt(t)
	f := denyDefaultFixture(t)
	base := denyDefaultParams(f)
	baseProfile := writeDenyDefaultProfile(t, base)
	if text, err := os.ReadFile(baseProfile); err == nil {
		t.Logf("deny-default profile under test (%s):\n%s", baseProfile, text)
	}

	// The runbook's check 0: nothing below means anything if the profile does not load.
	if out, rc := runDenyDefaultScript(t, f, "echo "+seatbeltOK, baseProfile); rc != 0 || !strings.Contains(out, seatbeltOK) {
		logSandboxDenials(t)
		t.Fatalf("the profile did not load, or /bin/bash could not start under it (rc %d):\n%s", rc, out)
	}

	redact := func(s string) string {
		if f.keychainSecret != "" {
			s = strings.ReplaceAll(s, f.keychainSecret, "<the keychain secret>")
		}
		return s
	}
	for _, tc := range denyDefaultCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsUnifiedLog {
				requireUnifiedLog(t, macosUserGateSeatbelt)
			}
			if tc.needs != nil {
				if why := tc.needs(f); why != "" {
					macosUserSkipCountably(t, macosUserGateSeatbelt, why)
				}
			}
			profile := baseProfile
			if tc.variant != nil {
				p := denyDefaultParams(f)
				tc.variant(f, &p)
				profile = writeDenyDefaultProfile(t, p)
			}
			script := tc.script(f)
			marker := ""
			if tc.marker != nil {
				marker = tc.marker(f)
			}

			bareOut, bareRC := runDenyDefaultScript(t, f, script, "")
			controlBroken := ""
			switch {
			case tc.want == wantMarkerHidden && !strings.Contains(bareOut, marker):
				controlBroken = "the marker is absent from the unsandboxed output, so its absence under the profile would prove nothing"
			case tc.want != wantMarkerHidden && bareRC != 0:
				controlBroken = fmt.Sprintf("it exits %d unsandboxed, so this case cannot tell a policy refusal from a machine that never allowed it", bareRC)
			case tc.want == wantAllowed && !strings.Contains(bareOut, seatbeltOK):
				controlBroken = "it does not print " + seatbeltOK + " unsandboxed"
			}
			if controlBroken != "" {
				if tc.optionalControl {
					macosUserSkipCountably(t, macosUserGateSeatbelt, "the bare control failed on this host ("+
						controlBroken+"): "+strings.TrimSpace(redact(bareOut)))
				}
				t.Fatalf("the CONTROL failed: %s.\nscript: %s\nrule: %s\nwhy:  %s\noutput:\n%s",
					controlBroken, script, tc.id, tc.why, redact(bareOut))
			}

			sbOut, sbRC := runDenyDefaultScript(t, f, script, profile)
			switch tc.want {
			case wantRefused:
				if sbRC == 0 {
					t.Errorf("NOT REFUSED. `%s` succeeded under the profile.\nrule: #seatbelt-test-id:%s#\nwhy:  %s\noutput:\n%s",
						script, tc.id, tc.why, redact(sbOut))
				} else if tc.refusal != "" && !strings.Contains(sbOut, tc.refusal) {
					t.Errorf("REFUSED, but not with %q, so something other than the profile may have refused it.\n"+
						"rule: #seatbelt-test-id:%s#\nwhy:  %s\noutput:\n%s", tc.refusal, tc.id, tc.why, redact(sbOut))
				}
			case wantAllowed:
				if !strings.Contains(sbOut, seatbeltOK) {
					t.Errorf("REFUSED, and should not have been. `%s` did not report %s under the profile (rc %d).\n"+
						"rule: #seatbelt-test-id:%s#\nwhy:  %s\noutput:\n%s", script, seatbeltOK, sbRC, tc.id, tc.why, redact(sbOut))
				}
			case wantMarkerHidden:
				if strings.Contains(sbOut, marker) {
					t.Errorf("NOT HIDDEN. The marker is readable from inside the sandbox.\n"+
						"rule: #seatbelt-test-id:%s#\nwhy:  %s\noutput:\n%s", tc.id, tc.why, redact(sbOut))
				} else if tc.ran != "" && !strings.Contains(sbOut, tc.ran) {
					t.Errorf("the probe did not start under the profile (no %q), so the hidden marker proves nothing.\n"+
						"rule: #seatbelt-test-id:%s#\noutput:\n%s", tc.ran, tc.id, redact(sbOut))
				}
			}
			if tc.escaped != nil {
				if what := tc.escaped(t, f); what != "" {
					t.Errorf("ESCAPED: %s.\nrule: #seatbelt-test-id:%s#\nwhy:  %s", what, tc.id, tc.why)
				}
			}
		})
	}
	if t.Failed() {
		logSandboxDenials(t)
	}
}

// TestMacosUserDenyDefaultCasesAreWellFormed walks the cases on Linux under -short: a case the
// runtime test could not judge (no marker for a hidden-marker case, a duplicate name) is
// caught where the repo is developed, not on the first macOS run.
func TestMacosUserDenyDefaultCasesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	var f denyDefaultFixtures
	for _, c := range denyDefaultCases() {
		if c.name == "" || seen[c.name] {
			t.Errorf("case name %q is empty or used twice", c.name)
		}
		seen[c.name] = true
		if c.id == "" || c.why == "" || c.script == nil {
			t.Errorf("case %s lacks an id, a why or a script", c.name)
			continue
		}
		if (c.want == wantMarkerHidden) != (c.marker != nil) {
			t.Errorf("case %s: a marker is set exactly when want is wantMarkerHidden", c.name)
		}
		if c.ran != "" && c.want != wantMarkerHidden {
			t.Errorf("case %s: ran means something only for wantMarkerHidden", c.name)
		}
		if c.refusal != "" && c.want != wantRefused {
			t.Errorf("case %s: refusal means something only for wantRefused", c.name)
		}
		if strings.TrimSpace(c.script(f)) == "" {
			t.Errorf("case %s renders an empty script", c.name)
		}
	}
}

// ---------------------------------------------------------------------------
// Fixtures and the runner.
// ---------------------------------------------------------------------------

// denyDefaultParams is the profile a self-mode launch of the fixture workspace would render
// with every optional input it has, at the production rulings.
func denyDefaultParams(f denyDefaultFixtures) macosuser.ProfileParams {
	return macosuser.ProfileParams{
		Workspace:         f.ws,
		Home:              f.home,
		HostHome:          f.hostHome,
		SandboxTmp:        f.tmp,
		DarwinUserTempDir: f.darwinTmp,
		StagingRoot:       macosuser.StagingRoot(),
		MachineDirs:       []string{f.machine},
		Context: []macosuser.ContextLink{
			{Dest: "/ctx/ro", Source: f.ctxRO, Dir: true},
			{Dest: "/ctx/rw", Source: f.ctxRW, RW: true, Dir: true},
		},
		Relocations:  []macosuser.CacheRelocation{{Subdir: "huggingface", Target: f.reloc}},
		ReadonlyRels: []string{"vendored"},
		// The self-mode layout keeps an agent's state as a real directory in HOME (MMP-D5), so
		// the staged content's physical paths are HOME's own and the state dir is the anchor.
		HomeReadonly: macosuser.HomeReadonly{
			Paths:   []string{f.skills, f.stateDir + "/CLAUDE.md"},
			Anchors: []string{f.stateDir},
		},
		RunAsUser:    f.user,
		ServicesDirs: []string{f.services},
	}.WithProductionRulings()
}

// writeDenyDefaultProfile renders p where sandbox-exec can read it.
func writeDenyDefaultProfile(t *testing.T, p macosuser.ProfileParams) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deny-default.sb")
	if err := os.WriteFile(path, []byte(macosuser.DenyDefaultProfile(p)), 0o644); err != nil {
		t.Fatalf("writing the profile: %v", err)
	}
	return path
}

// denyDefaultEnv is every run's environment, the runbook's: a clean one with HOME the agent's.
func denyDefaultEnv(f denyDefaultFixtures) []string {
	return []string{"HOME=" + f.home, "PATH=" + denyDefaultPath, "TMPDIR=" + f.tmp + "/",
		"USER=" + f.user, "LOGNAME=" + f.user, "LANG=en_US.UTF-8", "TERM=xterm-256color"}
}

// runDenyDefaultScript runs script with /bin/bash from the workspace, under profile when it is
// not "", and returns the combined output and exit code. Stdin is an empty pipe, for
// runScript's reason.
func runDenyDefaultScript(t *testing.T, f denyDefaultFixtures, script, profile string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	argv := []string{"/bin/bash", "-c", script}
	if profile != "" {
		argv = append([]string{"/usr/bin/sandbox-exec", "-f", profile}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = f.ws
	cmd.Env = denyDefaultEnv(f)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("`%s` did not finish within 60s", strings.Join(argv, " "))
	}
	rc := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("running `%s`: %v", strings.Join(argv, " "), err)
		}
		rc = ee.ExitCode()
	}
	return string(out), rc
}

// denyDefaultFixture builds the tree under /Users/Shared (not t.TempDir(), which is under the
// user's DARWIN_USER_TEMP_DIR, a writable root of this profile), the decoys, the listeners, the
// canary and the throwaway keychain item, and removes every one at cleanup.
func denyDefaultFixture(t *testing.T) denyDefaultFixtures {
	t.Helper()
	root, err := os.MkdirTemp(sharedUsersDir, "yolo-dd-")
	if err != nil {
		t.Fatalf("creating the fixture root under %s: %v", sharedUsersDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolving %s: %v", root, err)
	}
	f := denyDefaultFixtures{
		root:     resolved,
		ws:       filepath.Join(resolved, "ws"),
		tmp:      filepath.Join(resolved, "tmp"),
		outside:  filepath.Join(resolved, "outside"),
		machine:  filepath.Join(resolved, "machine", ".cache"),
		ctxRO:    filepath.Join(resolved, "ctx", "ro"),
		ctxRW:    filepath.Join(resolved, "ctx", "rw"),
		reloc:    filepath.Join(resolved, "caches", "hf"),
		fakeHome: filepath.Join(resolved, "fakehome"),
		nonce:    fmt.Sprintf("%d%d", os.Getpid(), time.Now().UnixNano()%1000000),
	}
	f.home = filepath.Join(f.ws, ".yolo", "home")
	f.readonly = filepath.Join(f.ws, "vendored")
	f.ctxROWS = filepath.Join(f.ws, "ctxro")
	f.stateDir = filepath.Join(f.home, ".claude")
	f.skills = filepath.Join(f.stateDir, "skills")
	for _, d := range []string{f.ws, f.home, f.tmp, f.outside, f.readonly, f.machine, f.ctxRO, f.ctxRW,
		f.ctxROWS, f.reloc, filepath.Join(f.skills, "demo"), filepath.Join(f.stateDir, "projects"),
		filepath.Join(f.fakeHome, ".ssh", "agent"), filepath.Join(f.fakeHome, "Library", "Keychains"),
		filepath.Join(f.fakeHome, ".docker", "run")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}
	for p, content := range map[string]string{
		filepath.Join(f.ws, "seed"):                                            seatbeltOK + "\n",
		filepath.Join(f.ws, ".ddprobe.py"):                                     denyDefaultProbePy,
		filepath.Join(f.outside, "secret"):                                     "a sibling checkout's private file\n",
		filepath.Join(f.skills, "demo", "SKILL.md"):                            "demo skill\n",
		filepath.Join(f.stateDir, "CLAUDE.md"):                                 "the briefing\n",
		filepath.Join(f.ctxRO, "seed"):                                         seatbeltOK + "\n",
		filepath.Join(f.ctxRW, "seed"):                                         seatbeltOK + "\n",
		filepath.Join(f.reloc, "seed"):                                         seatbeltOK + "\n",
		filepath.Join(f.fakeHome, "plain"):                                     seatbeltOK + "\n",
		filepath.Join(f.fakeHome, ".ssh", "id_decoy"):                          "a decoy key\n",
		filepath.Join(f.fakeHome, "Library", "Keychains", "login.keychain-db"): "a decoy keychain\n",
	} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}

	if u, err := user.Current(); err == nil {
		f.user = u.Username
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("finding your home: %v", err)
	}
	if f.hostHome, err = filepath.EvalSymlinks(home); err != nil {
		t.Fatalf("resolving your home %s: %v", home, err)
	}
	f.decoy = filepath.Join(f.hostHome, "yolo-dd-decoy-"+f.nonce+".txt")
	if err := os.WriteFile(f.decoy, []byte("yolo deny-default decoy secret\n"), 0o600); err != nil {
		t.Fatalf("writing the decoy secret in your home: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(f.decoy) })
	project, err := os.MkdirTemp("/private/var/tmp", "yolo-dd-clientX-")
	if err != nil {
		t.Fatalf("creating the decoy project under /private/var/tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(project) })
	f.project = project
	if err := os.WriteFile(filepath.Join(project, "main.c"), []byte("client source\n"), 0o644); err != nil {
		t.Fatalf("writing the decoy project: %v", err)
	}
	if out, err := exec.Command("/usr/bin/getconf", "DARWIN_USER_TEMP_DIR").Output(); err == nil {
		if d, err := filepath.EvalSymlinks(strings.TrimSpace(string(out))); err == nil {
			f.darwinTmp = d
		}
	}
	for _, py := range []string{"/opt/homebrew/bin/python3", "/usr/local/bin/python3", "/nix/var/nix/profiles/default/bin/python3"} {
		if isExecutable(py) {
			f.python = py
			break
		}
	}
	for _, pattern := range []string{"/private/tmp/com.apple.launchd.*/Listeners", "/private/var/run/com.apple.launchd.*/Listeners"} {
		if m, _ := filepath.Glob(pattern); len(m) > 0 {
			f.listener = m[0]
			break
		}
	}

	f.staged, f.stagedWhy = seatbeltStagingFixture(t)
	f.canaryPID, f.canaryWord = startSeatbeltCanary(t)
	f.loopPort = listenAccepting(t, "tcp", "127.0.0.1:0")
	if l, err := net.Listen("tcp", "[::1]:0"); err == nil {
		_ = l.Close()
		f.loopPort6 = listenAccepting(t, "tcp", "[::1]:0")
	}
	f.services, f.sibling = denyDefaultServicesDirs(t, f.nonce)
	listenAccepting(t, "unix", filepath.Join(f.fakeHome, ".docker", "run", "docker.sock"))
	listenAccepting(t, "unix", filepath.Join(f.fakeHome, ".ssh", "agent", "s"))
	f.keychainService, f.keychainSecret, f.keychainWhy = addThrowawayKeychainItem(t, f)
	return f
}

// denyDefaultServicesDirs makes two session-shaped host-services dirs under /private/tmp, each
// holding an endpoint file and a liveness lock: this session's and a sibling's.
func denyDefaultServicesDirs(t *testing.T, nonce string) (own, sibling string) {
	t.Helper()
	for i, dst := range []*string{&own, &sibling} {
		d := fmt.Sprintf("/private/tmp/yolo-host-services-dd%06x-%s%d", os.Getpid()%0xffffff, nonce, i)
		if !paths.IsHostServicesSessionDirName(filepath.Base(d)) {
			t.Fatalf("%s is not session-shaped", d)
		}
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatalf("creating the fake services dir %s: %v", d, err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(d) })
		for name, content := range map[string]string{"demo.endpoint": seatbeltOK + "\n", ".session.lock": ""} {
			if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o600); err != nil {
				t.Fatalf("writing %s: %v", name, err)
			}
		}
		*dst = d
	}
	return own, sibling
}

// listenAccepting listens on network/address, accepts and closes every connection until
// cleanup, and returns the TCP port (0 for a unix socket).
func listenAccepting(t *testing.T, network, address string) int {
	t.Helper()
	l, err := net.Listen(network, address)
	if err != nil {
		t.Fatalf("listening on %s %s: %v", network, address, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if a, ok := l.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// addThrowawayKeychainItem adds a generic password with a random secret to your login keychain,
// readable by /usr/bin/security with no prompt (-T), and deletes it at cleanup, whatever the
// test's outcome. It returns the service name and the secret, or why it could not add one.
func addThrowawayKeychainItem(t *testing.T, f denyDefaultFixtures) (string, string, string) {
	t.Helper()
	kc := filepath.Join(f.hostHome, "Library", "Keychains", "login.keychain-db")
	service := "yolo-dd-" + f.nonce
	secret := fmt.Sprintf("yolo-dd-secret-%d-%d", time.Now().UnixNano(), os.Getpid())
	out, err := exec.Command("/usr/bin/security", "add-generic-password", "-s", service, "-a", "yolo",
		"-w", secret, "-T", "/usr/bin/security", kc).CombinedOutput()
	if err != nil {
		return "", "", fmt.Sprintf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() {
		_ = exec.Command("/usr/bin/security", "delete-generic-password", "-s", service, "-a", "yolo", kc).Run()
	})
	return service, secret, ""
}

// denyDefaultFixtureParams sets every optional input, so every conditional rule renders.
func denyDefaultFixtureParams() macosuser.ProfileParams {
	ws := "/Users/Shared/proj"
	home := ws + "/.yolo/home"
	return macosuser.ProfileParams{
		Workspace:         ws,
		Home:              home,
		HostHome:          "/Users/you",
		SandboxTmp:        "/private/var/folders/ab/xyz/T/yolo-proj",
		DarwinUserTempDir: "/private/var/folders/ab/xyz/T",
		StagingRoot:       macosuser.StagingRoot(),
		MachineDirs:       []string{"/Users/you/.local/share/yolo-jail/macos-user/.cache"},
		Context: []macosuser.ContextLink{
			{Dest: "/ctx/lib", Source: "/Users/Shared/ci/lib", Dir: true},
			{Dest: "/ctx/data", Source: "/Users/Shared/yolo/data", RW: true, Dir: true},
		},
		Relocations: []macosuser.CacheRelocation{{Subdir: "huggingface", Target: "/Volumes/Data/hf"}},
		CacheAliases: []macosuser.CacheAlias{{Tool: "pip", Link: home + "/Library/Caches/pip",
			Target: "/Users/you/Library/Caches/pip"}},
		ReadonlyRels: []string{"vendored"},
		HomeReadonly: macosuser.ResolveHomeReadonly(home, paths.LegacyHomeState(ws),
			[]string{".claude"}, []string{".claude/skills", ".claude/CLAUDE.md"}),
		Devices:      seatbeltFixtureDevices,
		RunAsUser:    "you",
		ServicesDirs: []string{"/private/tmp/yolo-host-services-1a2b3c4d-987654"},
		Tmp:          macosuser.TmpShared,
		HostSoftware: []string{"/opt/homebrew", "/usr/local"},
	}
}

// TestMacosUserDenyDefaultRegistryMatchesTheProfile mirrors
// TestMacosUserSeatbeltRegistryMatchesTheProfile for the deny-default profile, in both
// directions, on Linux under -short. It renders the profile twice, once under each value of
// every pending ruling (/tmp shared and private, host software
// visible and hidden), and takes the union of the ids, since each ruling renders rules the
// other omits.
func TestMacosUserDenyDefaultRegistryMatchesTheProfile(t *testing.T) {
	prototype := denyDefaultFixtureParams()
	alternate := denyDefaultFixtureParams()
	alternate.Tmp = macosuser.TmpPrivate
	alternate.HostSoftware = nil

	inProfile := map[string]bool{}
	for _, pp := range []macosuser.ProfileParams{prototype, alternate} {
		for _, m := range seatbeltIDPattern.FindAllStringSubmatch(macosuser.DenyDefaultProfile(pp), -1) {
			inProfile[m[1]] = true
		}
	}
	if len(inProfile) == 0 {
		t.Fatal("the deny-default profile carries no #seatbelt-test-id:…# markers at all")
	}

	registered := map[string]seatbeltRule{}
	for _, r := range denyDefaultRules {
		if _, dup := registered[r.id]; dup {
			t.Errorf("rule %q is registered twice", r.id)
		}
		registered[r.id] = r
		if !inProfile[r.id] {
			t.Errorf("rule %q is registered here but is in neither rendering of the deny-default "+
				"profile; either the directive was deleted and this entry outlived it, or the id was "+
				"renamed on one side only", r.id)
		}
	}
	for id := range inProfile {
		if _, ok := registered[id]; !ok {
			t.Errorf("the deny-default profile carries #seatbelt-test-id:%s# and nothing here claims "+
				"it. Add a denyDefaultRules entry, with a runtime case or the reason none proves it yet.", id)
		}
	}

	proved := denyDefaultCaseIDs()
	for id := range proved {
		if _, ok := registered[id]; !ok {
			t.Errorf("a case names rule %q, which is in no registry entry", id)
		}
	}
	for _, r := range denyDefaultRules {
		switch {
		case r.unproven == "" && proved[r.id] == 0:
			t.Errorf("rule %q claims to be proved at runtime and no case carries its id", r.id)
		case r.unproven != "" && proved[r.id] > 0:
			t.Errorf("rule %q is registered as unproven and %d case(s) carry its id; delete "+
				"the reason or delete the cases, but the file must not say both", r.id, proved[r.id])
		}
	}
}
