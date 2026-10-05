//go:build linux

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// capturelandlock_linux.go is the HOST CAPTURE on Linux (docs/design/host-tool-provisioning.md
// HP-D18): `yolo capture <installer bin>` on a machine with no container runtime, the vendor's
// installer run once on the host, confined by Landlock to a throwaway home (internal/capture's
// landlock_linux.go says what the confinement allows, and what it leaves open).
//
// # The shape, beside a capture jail's
//
//	Store.Stage(<bin>)            the same staging dir; here it holds home/, out/, bin/, tmp/
//	the launcher                  entrypoint.NativeCaptureLauncher, at home/.yolo/bin/launch/<bin>
//	landlock-exec --supervise     this binary, a subreaper outside the confinement (capture.Supervise)
//	landlock-exec                 this binary, restricting itself, then exec'ing the driver
//	capture-run                   the same driver, HOME=home, --scan-content-refs
//	Store.AdmitEntry              unchanged, in captureHost; the receipt's platform carries "+host"
//
// What a jail gives the installer for free is built here by hand: an empty HOME (home/, which the
// confinement makes the one place under the user's home it may write) and a TMPDIR beside it (the
// real /tmp is not the installer's to read or write), no terminal (notty, which starts the chain in a
// session of its own, and its output through pipes, never the terminal's own descriptors), an
// environment with nothing of the user's in it but locale, terminal type, proxies and the CA bundles
// TLS needs — no token, no agent socket, no session bus — and a lifetime that ends with the capture
// (the supervisor kills whatever the installer leaves running, as a container's teardown would).

// landlockExecUsage is the whole surface of the confining verb: a supervisor, or the confinement.
const landlockExecUsage = "usage: yolo internal " + landlockExecVerb + " --supervise -- /absolute/command [args...]\n" +
	"       yolo internal " + landlockExecVerb +
	" [--rw=PATH]... [--rx=PATH]... [--ro=PATH]... -- /absolute/command [args...]"

// landlockSuperviseFlag selects the verb's SUPERVISOR: run the command as this process's child, this
// process a child subreaper outside the confinement, and kill whatever the command leaves behind once
// it exits (capture.Supervise).
const landlockSuperviseFlag = "--supervise"

// runLandlockExec is `yolo internal landlock-exec`, in one of its two forms, or exits non-zero having
// run nothing. With --supervise it runs the command under capture.Supervise and ends as the command
// did. Otherwise it restricts this process to the policy its flags name and execs the command. Hidden:
// its caller is the host capture, whose chain is the supervisor, then the confinement, then the
// capture driver (runLandlockCapture), and a confinement nobody asked for is no use to anyone else.
func runLandlockExec(args []string) int {
	if len(args) > 0 && args[0] == landlockSuperviseFlag {
		return superviseLandlockExec(args[1:])
	}
	p, argv, err := capture.ParseLandlockArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n%s\n", landlockExecVerb, err, landlockExecUsage)
		return 2
	}
	err = capture.ExecConfined(p, argv, os.Environ())
	fmt.Fprintf(os.Stderr, "%s: %v — nothing was run\n", landlockExecVerb, err)
	return 1
}

// superviseLandlockExec is the verb's --supervise form: `-- /absolute/command [args...]`, run with
// this process's streams and environment, its status this process's. When the command died of a
// signal a stop sends, this process dies of the same one (notty.WrapperExit), so the capture that
// started it reads a Ctrl-C as one. A process the sweep could not end fails the run.
func superviseLandlockExec(args []string) int {
	if len(args) < 2 || args[0] != "--" {
		fmt.Fprintf(os.Stderr, "%s %s: expected -- and a command\n%s\n", landlockExecVerb, landlockSuperviseFlag,
			landlockExecUsage)
		return 2
	}
	if !filepath.IsAbs(args[1]) {
		fmt.Fprintf(os.Stderr, "%s %s: the supervised command must start with an absolute path — nothing was run\n",
			landlockExecVerb, landlockSuperviseFlag)
		return 1
	}
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := capture.Supervise(cmd)
	var lingering *capture.LingeringError
	if errors.As(err, &lingering) {
		fmt.Fprintf(os.Stderr, "%s: %v\n", landlockExecVerb, lingering)
		if lingering.Err == nil {
			return 1
		}
		err = lingering.Err
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			switch ws.Signal() {
			case syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP:
				return notty.WrapperExit(&notty.Stopped{Signal: ws.Signal(), Err: err})
			}
		}
		return notty.ExitCode(err)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s: %v — nothing was run\n", landlockExecVerb, landlockSuperviseFlag, err)
		return 1
	}
	return 0
}

// hostCaptureSelf is the argv that runs this yolo, for the confined chain to re-exec. A var so a test
// binary can name itself as yolo (testAsYoloArg) rather than run its own tests again.
var hostCaptureSelf = func() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("finding the running yolo: %w", err)
	}
	return []string{exe}, nil
}

// runLandlockCapture is the host capture's middle (runHostCapture's default): lay out staging, write
// the launcher and the stand-in `yolo` the launcher calls, and run the capture driver under Landlock,
// beneath the supervisor that ends whatever it leaves running. It returns the driver's exit status,
// leaving the proto-entry at staging/out, where captureHost's admit reads a capture jail's.
func runLandlockCapture(staging string, target *captureTarget, abi int, out, errw io.Writer) int {
	fail := func(format string, args ...any) int {
		fmt.Fprintf(errw, "yolo capture: "+format+"\n", args...)
		return 1
	}
	self, err := hostCaptureSelf()
	if err != nil {
		return fail("%v", err)
	}
	home := filepath.Join(staging, "home")
	launchDir := filepath.Join(home, ".yolo", "bin", "launch")
	binDir := filepath.Join(staging, "bin")
	tmpDir := filepath.Join(staging, "tmp")
	for _, d := range []string{launchDir, binDir, tmpDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fail("preparing the capture's staging home: %v", err)
		}
	}
	launcher := entrypoint.NativeCaptureLauncher(target.Pack, target.Install, home,
		filepath.Join(staging, "receipts.jsonl"))
	if err := os.WriteFile(filepath.Join(launchDir, target.Bin), []byte(launcher), 0o755); err != nil {
		return fail("writing %s's launcher: %v", target.Bin, err)
	}
	// THE `yolo` THE LAUNCHER CALLS (`yolo internal no-terminal`): this binary, through a script in a
	// directory of its own, so the installer's PATH gains this one name and nothing else that sits
	// beside yolo's real path, which may be ~/.local/bin, holding the user's own programs.
	if err := os.WriteFile(filepath.Join(binDir, "yolo"),
		[]byte("#!/bin/sh\nexec "+shquote.Join(self)+" \"$@\"\n"), 0o755); err != nil {
		return fail("writing the capture's yolo: %v", err)
	}
	env := hostCaptureEnv(os.Environ(), home, launchDir, binDir, tmpDir)
	policy, err := capture.CapturePolicy(capture.CapturePolicyOptions{
		Staging: staging,
		Home:    paths.Home(),
		Exclude: append(accountHome(), runtimeDir()),
		Exec:    []string{self[0]},
		Read:    caTrustPaths(env),
	})
	if err != nil {
		return fail("building the capture's confinement: %v", err)
	}
	// THE CHAIN: the supervisor, outside the confinement, so it outlives and sweeps whatever the
	// installer leaves running (capture.Supervise); the confinement; the capture driver.
	argv := append(append([]string{}, self...), "internal", landlockExecVerb, landlockSuperviseFlag, "--")
	argv = append(append(argv, self...), "internal", landlockExecVerb)
	argv = append(append(argv, policy.Args()...), "--")
	argv = append(argv, self...)
	argv = append(argv, "internal", "capture-run",
		"--home="+home,
		"--out="+filepath.Join(staging, captureOutLeaf),
		// The full reference scan, as a capture jail's: the floor materializes this entry into its
		// own prefix, a home other than the staging one it was captured in (captureJailArgv).
		"--scan-content-refs",
		"--", "env", entrypoint.InstallOnlyEnv+"=1", target.Bin)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = home
	// PIPES, NOT THE TERMINAL'S DESCRIPTORS: a writer that is not an *os.File makes os/exec copy
	// through a pipe, so nothing the installer starts holds the user's terminal, even with no
	// controlling one (notty) to push input into.
	cmd.Stdout, cmd.Stderr = struct{ io.Writer }{out}, struct{ io.Writer }{errw}
	if err := notty.Run(cmd); err != nil {
		var stopped *notty.Stopped
		if errors.As(err, &stopped) {
			return 130
		}
		return notty.ExitCode(err)
	}
	return 0
}

// hostCaptureEnv is the environment the confined installer runs with: env -i's, plus what a download
// needs from the user's — locale, terminal type, time zone, user name, the proxies and the CA bundles
// TLS reads — with HOME the staging home, TMPDIR beside it, and PATH the launcher's directory, the
// stand-in yolo's, then the system baseline (hostfloor.BaselinePath). Nothing else of the user's
// crosses: no token, no SSH_AUTH_SOCK, no DBUS_SESSION_BUS_ADDRESS, no XDG_RUNTIME_DIR, no XDG
// directory naming the real home.
func hostCaptureEnv(environ []string, home, launchDir, binDir, tmpDir string) []string {
	keep := map[string]bool{"TERM": true, "LANG": true, "LANGUAGE": true, "TZ": true, "USER": true,
		"LOGNAME": true, "SSL_CERT_DIR": true, macosuser.NodeExtraCAVar: true}
	for _, v := range macosuser.CABundleVars {
		keep[v] = true
	}
	for _, v := range []string{"http_proxy", "https_proxy", "no_proxy", "all_proxy", "ftp_proxy"} {
		keep[v], keep[strings.ToUpper(v)] = true, true
	}
	var out []string
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if ok && (keep[k] || strings.HasPrefix(k, "LC_")) {
			out = append(out, kv)
		}
	}
	path := append([]string{launchDir, binDir}, hostfloor.BaselinePath()...)
	return append(out, "HOME="+home, "TMPDIR="+tmpDir,
		"PATH="+strings.Join(path, string(os.PathListSeparator)))
}

// caTrustPaths are the CA files and directories env names, which the confined installer may read
// wherever they are — this machine's may be under the home, which it may not read otherwise.
func caTrustPaths(env []string) []string {
	var out []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case v == "":
		case k == "SSL_CERT_DIR", k == macosuser.NodeExtraCAVar:
			// OpenSSL reads SSL_CERT_DIR as a colon-separated list; the launch pipeline treats Node's
			// extras that way too (entrypoint's CA composition).
			for _, p := range strings.Split(v, ":") {
				if filepath.IsAbs(p) {
					out = append(out, p)
				}
			}
		default:
			for _, cav := range macosuser.CABundleVars {
				if k == cav && filepath.IsAbs(v) {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// runtimeDir is the user's runtime directory, which the confined installer may not read: its
// sockets and keyrings are the user's session's.
func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join("/run/user", fmt.Sprint(os.Getuid()))
}

// accountHome is the account's home from the user database when it is not HOME — a HOME pointed
// elsewhere still leaves the account's own home outside the confinement.
func accountHome() []string {
	h := accountHomeDir()
	if h == "" || h == "/" || h == paths.Home() {
		return nil
	}
	return []string{h}
}

// accountHomeDir is the account's home as the user database records it, "" when it cannot be read. A
// var so a test can stand in an account whose home is somewhere the confinement would otherwise grant.
var accountHomeDir = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}
