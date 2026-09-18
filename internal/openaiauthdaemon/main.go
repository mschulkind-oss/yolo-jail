package openaiauthdaemon

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
)

func defaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, ".local/share/yolo-jail/openai-auth/credentials.json")
}

// HostSocketPath derives the private host-to-host socket from the daemon's
// fronted socket. The fronted socket requires a jail-identity preamble; this
// sibling accepts ordinary framed host requests and is protected by mode 0600.
//
// It is also the ONLY door the machine-wide mutations are served on: serveSockets gives this
// path a handler that serves `import` and `logout`, and the fronted path one that refuses
// them.
func HostSocketPath(frontedSocket string) string { return frontedSocket + ".host" }

// Main is the host OpenAI authentication daemon entry point.
//
// CLI contract: --socket (required), --state-file, --token-url,
// --authorize-url, --no-background-refresh,
// --refresh-interval, --login-timeout, and --self-check. Self-check is the one
// mode that does not require --socket and never refreshes credentials.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-openai-auth-host", flag.ContinueOnError)
	socket := fs.String("socket", "", "Unix socket to bind")
	statePath := fs.String("state-file", defaultStatePath(), "Canonical OpenAI credential state")
	tokenURL := fs.String("token-url", DefaultTokenURL, "OpenAI OAuth token endpoint")
	authorizeURL := fs.String("authorize-url", DefaultAuthorizeURL, "OpenAI OAuth authorization endpoint")
	noBackground := fs.Bool("no-background-refresh", false, "Disable proactive refresh")
	refreshInterval := fs.Duration("refresh-interval", time.Minute, "Proactive refresh check interval")
	loginTimeout := fs.Duration("login-timeout", 15*time.Minute, "Browser callback timeout")
	selfCheckFlag := fs.Bool("self-check", false, "Validate and report canonical state without refreshing")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if !filepath.IsAbs(*statePath) {
		fmt.Fprintln(os.Stderr, "yolo-openai-auth-host: --state-file must be an absolute path")
		return 2
	}
	if *selfCheckFlag {
		return selfCheck(*statePath, os.Stdout)
	}
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "yolo-openai-auth-host: --socket is required")
		return 2
	}
	upstream := Upstream{TokenURL: *tokenURL}
	broker := openaiauth.Broker{
		StatePath: *statePath, LockPath: filepath.Join(filepath.Dir(*statePath), "refresh.lock"), Refresher: upstream,
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	shutdown := func() { stopOnce.Do(func() { close(stop) }) }
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-signals; shutdown() }()
	if !*noBackground {
		go runProactive(broker, *refreshInterval, stop)
	}
	handlerConfig := HandlerConfig{
		Broker: broker, Upstream: upstream, AuthorizeURL: *authorizeURL,
		LoginTimeout: *loginTimeout,
	}
	if err := serveSockets(BuildHandler(handlerConfig), BuildHostHandler(handlerConfig),
		*socket, HostSocketPath(*socket), stop, shutdown); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-openai-auth-host:", err)
		return 1
	}
	return 0
}

// serveSockets runs both transports, and it takes TWO HANDLERS because there are two
// authorizations and a handler cannot see which socket carried its bytes.
//
// It took ONE until 2026-09-18, and hostservice's own doc says why that could never be made
// safe: "the handler still never learns which of the three carried its bytes", and on
// ServeUnix `Session.JailID` falls back to the client's own `jail_id` field — a jail-supplied
// string. So the only place the difference between the fronted socket and the private 0600 one
// can be expressed is HERE, in the choice of handler, before any request exists.
//
//   - frontedSocket is jail-facing (svcendpoint's front authenticates and splices to it), and
//     gets the handler that refuses every machine-wide mutation.
//   - hostSocket is host-to-host at mode 0600, where the filesystem is the authorization, and
//     gets the handler that also serves `import` and `logout`.
func serveSockets(jailHandler, hostHandler hostservice.Handler,
	frontedSocket, hostSocket string, stop <-chan struct{}, shutdown func()) error {
	errs := make(chan error, 2)
	go func() { errs <- hostservice.ServeFrontedUnix(jailHandler, frontedSocket, stop) }()
	go func() { errs <- hostservice.ServeUnix(hostHandler, hostSocket, stop) }()
	err := <-errs
	shutdown()
	<-errs
	return err
}

func selfCheck(statePath string, output io.Writer) int {
	state, err := openaiauth.ReadState(statePath)
	if err != nil {
		fmt.Fprintln(output, "OpenAI authentication: unavailable:", err)
		return 1
	}
	if err := json.NewEncoder(output).Encode(statusView(state)); err != nil {
		return 1
	}
	if state.LoginRequired {
		return 1
	}
	return 0
}

func runProactive(broker openaiauth.Broker, interval time.Duration, stop <-chan struct{}) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if broker.RefreshDue() {
			_, _ = broker.Refresh(context.Background(), openaiauth.Request{Caller: "proactive"})
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}
