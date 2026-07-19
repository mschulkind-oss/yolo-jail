package oauthbroker

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

func defaultCredsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, ".local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json")
}

// Main is the per-host Claude OAuth refresh daemon entry point.
//
// CLI contract: --socket, --creds-file, --init-ca, --force-init-ca,
// --self-check, --no-background-refresh, -v/--verbose.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-claude-oauth-broker-host", flag.ExitOnError)
	socket := fs.String("socket", "", "Unix socket to bind (set by `yolo run`)")
	credsFile := fs.String("creds-file", defaultCredsPath(), "Shared credentials file")
	initCA := fs.Bool("init-ca", false, "Generate CA + leaf cert and exit (idempotent)")
	forceInitCA := fs.Bool("force-init-ca", false, "Regenerate CA + leaf even if they exist")
	selfCheck := fs.Bool("self-check", false, "Emit status and exit")
	noBgRefresh := fs.Bool("no-background-refresh", false, "Disable the proactive refresh loop")
	logFile := fs.String("log-file", "", "append the operational log here (default: stderr)")
	verbose := fs.Bool("verbose", false, "Verbose logging")
	verboseShort := fs.Bool("v", false, "Verbose logging")
	_ = fs.Parse(argv)

	// Configure the operational log. Matches the cgd/journald --log-file
	// convention (commit ec888c6): empty path -> stderr (captured by the
	// loophole supervisor); -v -> DEBUG. Set up before any broker call so the
	// incident-forensics lines (refresh attempts, token fingerprints, upstream
	// status codes) are captured for an unattended soak.
	SetupLog(*logFile, *verbose || *verboseShort)

	// Stamp the versioned User-Agent (Python-urllib's default UA triggers
	// Cloudflare 1010 on platform.claude.com).
	if v := version.Get(""); v != "" && v != "unknown" {
		SetUserAgent("yolo-jail-oauth-broker/" + v)
	}

	if *selfCheck {
		return SelfCheck(*credsFile)
	}
	if *initCA || *forceInitCA {
		if err := EnsureCAAndLeaf(*forceInitCA); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		dir := BrokerDir()
		fmt.Printf("CA: %s\nleaf: %s\n", filepath.Join(dir, "ca.crt"), filepath.Join(dir, "server.crt"))
		return 0
	}

	if *socket == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --socket is required when running as a daemon.")
		fmt.Fprintln(os.Stderr, "       Use --init-ca for first-time setup.")
		return 2
	}

	// Ensure CA + leaf exist (jails need the CA at boot).
	if err := EnsureCAAndLeaf(false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// The refresh flock lives in the broker state dir — SAME path as Python's
	// REFRESH_LOCK, so a Python and Go broker mutually exclude during rollout.
	RefreshLockPath = filepath.Join(BrokerDir(), "refresh.lock")

	// Startup snapshot of the shared creds file — lets tomorrow's debugger see
	// the starting state and cross-reference it with do_refresh's drift lines.
	LogStartup(*credsFile)

	stop := make(chan struct{})
	if !*noBgRefresh {
		go RunBackgroundRefresher(*credsFile, stop,
			BackgroundRefreshTickSeconds, BackgroundRefreshLeadSeconds)
	}

	// hostservice.Serve installs its own SIGTERM/SIGINT handler; wire a stop
	// channel too so the background refresher goroutine exits with the process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; close(stop) }()

	if err := hostservice.Serve(BuildHandler(*credsFile), *socket, stop); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-claude-oauth-broker-host:", err)
		return 1
	}
	return 0
}
