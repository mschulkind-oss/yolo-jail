package openaiauthdaemon

import (
	"context"
	"errors"
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

// selfCheck is the openai-auth-broker loophole's `doctor_cmd`, which `yolo check` runs.
//
// Its lines are GRADED by internal/cli/check's reportSelfCheckLines — "OK:" passes, "NOTE:"
// warns, "FAIL:" fails — the protocol the aws-auth and host-processes self-checks already
// speak. It printed a JSON status view and an ungraded "unavailable" line until 2026-09-25,
// neither of which that grader reads, so every non-zero exit rendered as
// `self-check failed (rc=1)` over the note "no output".
//
// # A MISSING state file is not a failure
//
// It is the normal state of a machine that has never logged in — the fresh HOME `yolo check`
// runs on — and the daemon's own `status` action already answers it as `logged_in: false`
// rather than as an error. So it is a NOTE naming the two ways to log in, exit 0: a warn is
// how this report says "working as configured, and you should know" (awsauthdaemon's
// SelfCheck), and here the reader does need to know, because nothing logs in for them.
// A state file that exists and cannot be read, and a grant OpenAI has refused to refresh,
// are the real faults, and FAIL.
//
// It never prints a token, only the fingerprints the status view carries.
func selfCheck(statePath string, output io.Writer) int {
	state, err := openaiauth.ReadState(statePath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(output, "NOTE: no OpenAI subscription login on this machine yet (no "+
			"credential state at "+statePath+") — `yolo host codex` signs in through the "+
			"browser, or `yolo openai-auth import --from <auth.json>` installs an existing "+
			"Codex login")
		return 0
	}
	if err != nil {
		fmt.Fprintln(output, "FAIL: OpenAI credential state at "+statePath+" is unusable: "+
			err.Error()+" — `yolo openai-auth logout` deletes it, then log in again")
		return 1
	}
	if state.LoginRequired {
		code := state.LastErrorCode
		if code == "" {
			code = "no error code recorded"
		}
		fmt.Fprintln(output, "FAIL: OpenAI refused to refresh the machine-wide grant ("+code+
			"), so every jail borrowing it fails — log in again with `yolo host codex`, or "+
			"`yolo openai-auth import --from <auth.json>`")
		return 1
	}
	account := state.AccountID
	if account == "" {
		account = "unknown account"
	}
	fmt.Fprintf(output, "OK: logged in (%s, generation %d); access token expires %s\n",
		account, state.Generation,
		time.UnixMilli(state.ExpiresAtMS).UTC().Format(time.RFC3339))
	fmt.Fprintf(output, "OK: token fingerprints — access %s, refresh %s\n",
		openaiauth.TokenFingerprint(state.AccessToken),
		openaiauth.TokenFingerprint(state.RefreshToken))
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
