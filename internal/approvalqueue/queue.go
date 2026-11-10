// Package approvalqueue is the read-only browser view of yolo's approval requests, started by
// "yolo approve --web" (experimental).
//
// It runs the current executable as "approve --json" and "approve --watch --json" child
// processes, keeps one in-memory model of what they report, and serves it to a browser on
// 127.0.0.1 behind a one-time link printed on the terminal that started it. It cannot decide
// anything: there is no mutation route.
package approvalqueue

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/hub"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/model"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/server"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/supervisor"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue/web"
)

// Env is everything run needs from the process, so tests can supply it.
type Env struct {
	// Yolo is the executable the supervisor runs as "approve --json"; empty means this
	// process's own executable. Tests name a fake.
	Yolo string
	// Open opens a browser on the launcher file at path; nil means the platform's opener.
	// Tests inject one. --no-open skips it.
	Open func(path string) error
	// After schedules f after d and returns its cancel; nil means time.AfterFunc. Tests inject a
	// clock they fire by hand.
	After func(d time.Duration, f func()) (stop func())
	// LaunchRoot is where the launcher directory is made; empty picks the runtime directory.
	LaunchRoot       string
	Args             []string
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	StdoutIsTerminal bool // the real main reads this from os.Stdout's mode; tests set it
}

// usageError is a refusal to start whose message already names the next step.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// ExitUsage is the exit code of a refusal to start whose message names the next step; it is
// the usage code of yolo approve itself.
const ExitUsage = 2

// Main runs the queue for "yolo approve --web", with args being the flags after --web, and
// returns the process exit code.
func Main(args []string) int {
	isTerm := isTerminal(os.Stdout)
	// Closing the terminal sends SIGHUP; every one of these ends the children's process groups
	// through the context.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	err := run(ctx, Env{Args: args, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, StdoutIsTerminal: isTerm})
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "yolo approve --web:", err)
	var ue *usageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	return 1
}

// checkListen accepts only 127.0.0.1:<port>. The Host check is exact (127.0.0.1:<port>), so no
// other spelling of loopback could be served, and anything else is not loopback at all.
func checkListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return &usageError{fmt.Sprintf("--listen %q is not host:port; use --listen 127.0.0.1:0", addr)}
	}
	if host == "127.0.0.1" {
		return nil
	}
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return &usageError{fmt.Sprintf("--listen %q is loopback but not 127.0.0.1; the page's Host check accepts 127.0.0.1 only; use --listen 127.0.0.1:0", addr)}
	}
	return &usageError{fmt.Sprintf("refusing --listen %q: it is not a loopback address, and anything that can reach the port could read your approval requests; use --listen 127.0.0.1:0", addr)}
}

func run(ctx context.Context, env Env) error {
	fs := flag.NewFlagSet("yolo approve --web", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	yoloPath := env.Yolo
	if yoloPath == "" {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot find this yolo executable: %w; run `yolo approve --json` to see whether yolo itself works", err)
		}
		yoloPath = self
	}
	listen := fs.String("listen", "127.0.0.1:0", "address to listen on; loopback only")
	reconcile := fs.Duration("reconcile", 15*time.Second, "how often to take a full snapshot")
	noOpen := fs.Bool("no-open", false, "do not open a browser; use the printed link")
	assets := fs.String("assets", "", "serve the page's files from this directory (development)")
	if err := fs.Parse(env.Args); err != nil {
		return &usageError{"bad flags; see --help"}
	}
	if *reconcile <= 0 {
		return &usageError{"--reconcile must be positive, for example --reconcile 15s"}
	}
	if err := checkListen(*listen); err != nil {
		return err
	}
	if !env.StdoutIsTerminal {
		return &usageError{"refusing to start: standard output is not a terminal. The one-time link must reach only the person at this terminal, never a pipe, a file or yolo host-run; run `yolo approve --web` directly in a terminal"}
	}

	var pageFS = web.FS()
	if *assets != "" {
		pageFS = os.DirFS(*assets)
	}
	logger := log.New(env.Stderr, "queue: ", log.Ltime)
	h := hub.New(model.Options{Reconcile: *reconcile, Loc: time.Local}, time.Now)
	sup := supervisor.New(supervisor.Config{
		Yolo: yoloPath, Reconcile: *reconcile, NoInitialSnapshot: true,
		Logf: func(f string, a ...any) { logger.Printf(f, a...) },
	}, h.Apply)

	// The first snapshot is taken here, before anything listens, so a missing yolo or the
	// in-jail refusal is the startup error. A store that cannot be read yet is not: the page
	// opens "Never loaded" with yolo's message.
	first := sup.Snapshot(ctx)
	if first.Cancelled {
		return nil
	}
	if first.NotFound {
		return &usageError{first.Message}
	}
	if first.Exit == 2 {
		return &usageError{first.Message + " (run this on the host, in a terminal)"}
	}
	h.Apply(first.Input)
	if ok, is := first.Input.(model.SnapshotOK); is {
		logger.Printf("first snapshot: %d pending, %d grants", len(ok.State.Requests), len(ok.State.Grants))
	} else {
		logger.Printf("first snapshot failed")
	}

	auth := server.NewAuth(time.Now)
	auth.OnWarn = func(n int) {
		fmt.Fprintf(env.Stdout, "%d failed attempts to use the link; someone on this machine or a jail may be guessing. "+
			"The code is 128-bit, so guessing cannot succeed; press Enter for a new link if yours stops working.\n", n)
	}
	srv := server.New(server.Config{Hub: h, Auth: auth, Assets: pageFS, Logger: logger})
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w; try --listen 127.0.0.1:0", *listen, err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	supDone := make(chan struct{})
	go func() { sup.Run(ctx); close(supDone) }()
	srvDone := make(chan error, 1)
	go func() { srvDone <- srv.Serve(l) }()

	var open func(string) error
	if !*noOpen {
		open = env.Open
		if open == nil {
			open = systemOpen
		}
	}
	dir := "" // --no-open needs no launcher directory
	if open != nil {
		dir, err = launchDir(env.LaunchRoot, runtime.GOOS, os.Getenv)
		if err != nil {
			srv.Close()
			return fmt.Errorf("cannot make a private directory for the browser launcher: %w; pass --no-open to skip the browser and use the printed link", err)
		}
	}
	lau := newLauncher(dir, open, env.After, func(f string, a ...any) { logger.Printf(f, a...) })
	defer lau.Close()

	// printLink mints a code, prints its URL (the fallback, and the only way with --no-open),
	// and has the browser opened on a launcher file so the code is never in an opener's argv.
	// Only a person pressing Enter, and the start, make a link; issueMu keeps two of them in
	// order. The launcher file is written and recorded before the code can be exchanged
	// (NewCodeWith), so an exchange always finds the file it must delete.
	var issueMu sync.Mutex
	printLink := func() {
		issueMu.Lock()
		defer issueMu.Unlock()
		urlFor := func(code string) string { return fmt.Sprintf("http://127.0.0.1:%d/#code=%s", port, code) }
		var path string
		code := auth.NewCodeWith(func(code string) { path = lau.Prepare(urlFor(code), server.CodeTTL) })
		fmt.Fprintf(env.Stdout, "Open: %s\n", urlFor(code))
		lau.Open(path)
	}
	auth.OnExchange = lau.Remove
	// The race alarm ends every session and warns, once per spent code. It makes no new link and
	// opens no browser: whoever replays a code could otherwise keep the person locked out and
	// spawn tabs without limit. The person asks for a new link, with Enter, once the cause is found.
	auth.OnRace = func() {
		fmt.Fprintln(env.Stdout, "WARNING: a link was used a second time. Something else on this machine had read it before your browser did. "+
			"Every session has been ended. If you did not open the link twice yourself, treat this as an attempt to read your approval requests; "+
			"press Enter for a new link once you have found the cause.")
	}
	fmt.Fprintln(env.Stdout, "yolo approval queue (experimental, read-only). The link below works once, for 120 seconds.")
	printLink()
	fmt.Fprintln(env.Stdout, "Press Enter for a new link. Ctrl-C quits.")
	if env.Stdin != nil {
		go func() {
			sc := bufio.NewScanner(env.Stdin)
			for sc.Scan() {
				printLink()
			}
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-srvDone:
		if err != nil {
			logger.Printf("server stopped: %v", err)
		}
	}
	srv.Close()
	<-supDone
	return nil
}
