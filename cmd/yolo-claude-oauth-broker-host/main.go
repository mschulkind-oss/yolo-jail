// Command yolo-claude-oauth-broker-host is the Go port of src/oauth_broker.py —
// the per-host Claude OAuth refresh daemon. Selected during the go-port soak by
// YOLO_GO_DAEMONS + YOLO_GO_BIN_DIR (the daemon resolution rule lands with
// host-processes, Stage 5); the binary name equals the Python console-script
// name so the manifest/doctor contract holds.
//
// CLI contract (byte-frozen against the Python argparse): --socket, --creds-file,
// --init-ca, --force-init-ca, --self-check, --no-background-refresh, -v/--verbose.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

func defaultCredsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, ".local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json")
}

func main() {
	socket := flag.String("socket", "", "Unix socket to bind (set by `yolo run`)")
	credsFile := flag.String("creds-file", defaultCredsPath(), "Shared credentials file")
	initCA := flag.Bool("init-ca", false, "Generate CA + leaf cert and exit (idempotent)")
	forceInitCA := flag.Bool("force-init-ca", false, "Regenerate CA + leaf even if they exist")
	selfCheck := flag.Bool("self-check", false, "Emit status and exit")
	noBgRefresh := flag.Bool("no-background-refresh", false, "Disable the proactive refresh loop")
	logFile := flag.String("log-file", "", "append the operational log here (default: stderr)")
	verbose := flag.Bool("verbose", false, "Verbose logging")
	verboseShort := flag.Bool("v", false, "Verbose logging")
	flag.Parse()

	// Configure the operational log. Matches the cgd/journald --log-file
	// convention (commit ec888c6): empty path -> stderr (captured by the
	// loophole supervisor); -v -> DEBUG. Set up before any broker call so the
	// incident-forensics lines (refresh attempts, token fingerprints, upstream
	// status codes) are captured for an unattended soak.
	oauthbroker.SetupLog(*logFile, *verbose || *verboseShort)

	// Stamp the versioned User-Agent (Python-urllib's default UA triggers
	// Cloudflare 1010 on platform.claude.com).
	if v := version.Get(""); v != "" && v != "unknown" {
		oauthbroker.SetUserAgent("yolo-jail-oauth-broker/" + v)
	}

	if *selfCheck {
		os.Exit(oauthbroker.SelfCheck(*credsFile))
	}
	if *initCA || *forceInitCA {
		if err := oauthbroker.EnsureCAAndLeaf(*forceInitCA); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		dir := oauthbroker.BrokerDir()
		fmt.Printf("CA: %s\nleaf: %s\n", filepath.Join(dir, "ca.crt"), filepath.Join(dir, "server.crt"))
		return
	}

	if *socket == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --socket is required when running as a daemon.")
		fmt.Fprintln(os.Stderr, "       Use --init-ca for first-time setup.")
		os.Exit(2)
	}

	// Ensure CA + leaf exist (jails need the CA at boot).
	if err := oauthbroker.EnsureCAAndLeaf(false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// The refresh flock lives in the broker state dir — SAME path as Python's
	// REFRESH_LOCK, so a Python and Go broker mutually exclude during rollout.
	oauthbroker.RefreshLockPath = filepath.Join(oauthbroker.BrokerDir(), "refresh.lock")

	// Startup snapshot of the shared creds file — lets tomorrow's debugger see
	// the starting state and cross-reference it with do_refresh's drift lines.
	oauthbroker.LogStartup(*credsFile)

	stop := make(chan struct{})
	if !*noBgRefresh {
		go oauthbroker.RunBackgroundRefresher(*credsFile, stop,
			oauthbroker.BackgroundRefreshTickSeconds, oauthbroker.BackgroundRefreshLeadSeconds)
	}

	// hostservice.Serve installs its own SIGTERM/SIGINT handler; wire a stop
	// channel too so the background refresher goroutine exits with the process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; close(stop) }()

	if err := hostservice.Serve(oauthbroker.BuildHandler(*credsFile), *socket, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-claude-oauth-broker-host:", err)
		os.Exit(1)
	}
}
