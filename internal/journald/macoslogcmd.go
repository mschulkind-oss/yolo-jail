package journald

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// MacosLogMain is the macos-log bridge's entry point (`yolo internal daemon macos-log`), the
// host daemon packs/macos-log's loophole spawns. Its CLI is the journal bridge's: --socket (the
// AF_UNIX socket yolo's own front dials), --settings (the grant file; grant.go) and
// --log-file. Requests, frames and the fronted socket are
// the journal bridge's too; what differs is the program (Apple's `/usr/bin/log`) and the user
// scope (macoslog.go).
func MacosLogMain(argv []string) int {
	return macosLogMain(argv, signalStop)
}

// macosLogMain is MacosLogMain with its stop source passed in (journalMain's shape).
func macosLogMain(argv []string, stopFn func() <-chan struct{}) int {
	fs := flag.NewFlagSet("yolo-macos-log", flag.ExitOnError)
	socket := fs.String("socket", "", "AF_UNIX socket to bind (behind yolo's front)")
	settings := fs.String("settings", "", "The grant file yolo writes for this launch")
	logFile := fs.String("log-file", "", "append per-request audit log here (default: stderr)")
	_ = fs.Parse(argv)
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "yolo-macos-log: --socket is required (the manifest's host_daemon.cmd "+
			"passes it as {socket}; this daemon is started by a launch, not by hand)")
		return 2
	}
	setupLog(*logFile)

	// The settings file is the grant file, re-read at every request so a live apply lands at
	// the next one. One without a `grants` key grants nothing: there is no settings mode to
	// fall back to (grant.go).
	cfg := macosLogMainConfig(LoadPolicy(*settings))

	err := ServeMacosLogFrontedUnix(*socket, cfg, stopFn())
	if err != nil {
		fmt.Fprintln(os.Stderr, "yolo-macos-log:", err)
		return 1
	}
	return 0
}

// macosLogMainConfig is the config MacosLogMain serves with: the real `log`, the sandbox
// account looked up on this Mac, and the kernel's process owner.
func macosLogMainConfig(p Policy) MacosLogConfig {
	uid, uidErr := lookupSandboxUID()
	return MacosLogConfig{
		Bin: macosLogBin, GrantFile: p.GrantFile, SandboxUID: uid, SandboxUIDErr: uidErr,
		Owner: processOwner,
	}
}

// MacosLogConfig is what one macos-log bridge serves with, resolved once at startup.
type MacosLogConfig struct {
	// Bin is the `log` program; MacosLogMain passes /usr/bin/log.
	Bin string
	// GrantFile is the grant file, re-read at every request (Policy.GrantFile); empty grants
	// nothing.
	GrantFile string
	// SandboxUID is the sandbox account's uid; SandboxUIDErr non-nil means the account does
	// not exist here, so the user scope has nobody to show.
	SandboxUID    uint32
	SandboxUIDErr error
	// Owner reports a live process's uid and start time (processOwner).
	Owner ownerFunc
}

// ServeMacosLogFrontedUnix binds the AF_UNIX socket only yolo's front dials and serves the
// macos-log bridge on it until stop is closed: ServeFrontedUnix's shape, preamble included.
func ServeMacosLogFrontedUnix(socket string, cfg MacosLogConfig, stop <-chan struct{}) error {
	return serveUnixConns(socket, stop, func(conn net.Conn) {
		if _, perr := svcendpoint.ReadPreamble(conn); perr != nil {
			// yolo's readiness probe is a bare connect-and-close; see ServeFrontedUnix.
			logf("[macos-log] connection preamble rejected; connection dropped")
			_ = conn.Close()
			return
		}
		handleMacosLogConn(conn, cfg)
	})
}

// lookupSandboxUID resolves the macos-user sandbox account's uid on this Mac, through the
// directory service (`id -u`), because a CGO-free os/user reads only /etc/passwd, where an
// account yolo created with dscl does not appear. A var for tests.
var lookupSandboxUID = func() (uint32, error) {
	out, err := exec.Command("/usr/bin/id", "-u", macosuser.SandboxUser).Output()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}

// handleMacosLogConn serves one request: read the header, resolve it under mode, run `log`.
func handleMacosLogConn(conn net.Conn, cfg MacosLogConfig) {
	defer conn.Close()
	header, foundNL := readHeaderCapped(conn, MaxHeaderBytes)
	if len(header) == 0 && !foundNL {
		return
	}
	if !foundNL {
		_ = WriteFrame(conn, FrameStderr, []byte(macosLogPrefix+": malformed request\n"))
		_ = WriteExit(conn, 2)
		return
	}
	// The grant decides before anything runs (grant.go).
	mode, refusal, rc := Policy{GrantFile: cfg.GrantFile}.decide(macosLogBridge)
	if refusal != "" {
		_ = WriteFrame(conn, FrameStderr, []byte(refusal))
		_ = WriteExit(conn, rc)
		return
	}
	plan := ParseMacosLogRequest(header, mode)
	if plan.ErrText != "" {
		_ = WriteFrame(conn, FrameStderr, []byte(plan.ErrText))
		_ = WriteExit(conn, plan.ExitCode)
		return
	}
	var keep lineFilter
	if plan.Filter {
		if cfg.SandboxUIDErr != nil {
			_ = WriteFrame(conn, FrameStderr, []byte(macosLogPrefix+": the user scope shows the "+
				macosuser.SandboxUser+" account's processes, and this Mac has no such account ("+
				cfg.SandboxUIDErr.Error()+"). A macos-user launch creates it: run `yolo` once with "+
				"runtime = \"macos-user\". Or read every entry instead: the human allows it on "+
				"the host with `yolo allow macos-log on_request read_all`, this project asks with "+
				"`yolo request macos-log read_all`, and the human runs `yolo config apply` on "+
				"the host, in this workspace, and answers y.\n"))
			_ = WriteExit(conn, 1)
			return
		}
		keep = macosLogKeep(cfg.SandboxUID, plan.Live, cfg.Owner)
	}
	logf("[macos-log] mode=%s args=%s", mode, ArgsJSON(plan.Args))
	spawnAndStream(conn, cfg.Bin, plan.Args,
		macosLogPrefix+": "+cfg.Bin+" not found on the host — this bridge reads the unified log "+
			"of a Mac, and the loophole runs only there.\n",
		macosLogPrefix+": spawn failed: ", keep)
}
