// Package awsauthdaemon is the host `aws-auth` credential service: the daemon
// wrapper around internal/awsauth, spawned by the run pipeline as
// `yolo internal daemon aws-auth`.
//
// It mirrors internal/openaiauthdaemon — the same flag set, the same fronted
// socket plus private `.host` sibling, the same `--self-check` and the same
// proactive ticker — and shares no code with it, deliberately: the two handle
// different providers and copying the shape while keeping the packages separate is
// what the design asks for.
//
// # What it decides at spawn, and why each is a spawn decision
//
//  1. The NARROWING must be configured, or it refuses (OQ-SSO1). A widening
//     retrofitted later breaks every setup that came to depend on the old default,
//     so the requirement lands with the first release rather than after it.
//  2. The `aws` CLI must be RUNNABLE, or it refuses. Loudly, at spawn — not as a
//     manifest `requires.command_on_path` probe, which is the shape that removed the
//     Claude broker for exactly the user it existed for. The rule that replaced it
//     (docs/reference/agent-credentials.md) is that "a loophole whose program is
//     missing must fail loudly at spawn, not disappear from `yolo loopholes list`",
//     and a daemon that says which dependency is absent and exits is that rule.
//  3. A LAPSED SSO SESSION IS NOT A SPAWN FAILURE. §8: the launch warns with the
//     login command and proceeds, because the human may be about to log in and a
//     jail that will not start is worse than a first request that fails clearly.
//     Expiry is a runtime state, not a configuration error.
package awsauthdaemon

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

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// LoopholeName is the name the shipped pack gives this loophole, so a refusal can
// spell a config path the reader can act on.
const LoopholeName = "aws-auth"

func defaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	return filepath.Join(home, ".local/share/yolo-jail", LoopholeName, awsauth.StateFileName)
}

// HostSocketPath derives the private host-to-host socket from the daemon's fronted
// socket. The fronted socket requires a jail-identity preamble; this sibling accepts
// ordinary framed host requests and is protected by mode 0600.
func HostSocketPath(frontedSocket string) string { return frontedSocket + ".host" }

// Main is the host AWS credential daemon entry point.
//
// CLI contract: --socket (required except under --self-check), --settings,
// --state-file, --no-background-refresh, --refresh-interval, --aws-binary and
// --self-check.
func Main(argv []string) int {
	fs := flag.NewFlagSet("yolo-aws-auth", flag.ContinueOnError)
	socket := fs.String("socket", "", "Unix socket to bind (behind yolo's front)")
	settingsPath := fs.String("settings", "",
		"Resolved settings file written by yolo (the manifest's {settings} token)")
	statePath := fs.String("state-file", defaultStatePath(), "Minted-credential cache")
	noBackground := fs.Bool("no-background-refresh", false, "Disable the proactive minter")
	refreshInterval := fs.Duration("refresh-interval", 0,
		"Proactive mint check interval (default: half the re-mint lead)")
	awsBinary := fs.String("aws-binary", "aws", "The AWS CLI v2 executable to run")
	selfCheckFlag := fs.Bool("self-check", false,
		"Report configuration and mint once, printing the four keys with the secret elided")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	// A RELATIVE state path is refused before anything is created, for
	// openaiauthdaemon's reason: the manifest substitutes an absolute {state}, so a
	// relative value means an unsubstituted token or a hand-run from an arbitrary
	// cwd, and either would scatter a credential cache wherever the process started.
	if !filepath.IsAbs(*statePath) {
		fmt.Fprintln(os.Stderr, "yolo-aws-auth: --state-file must be an absolute path")
		return 2
	}

	runner := awsauth.ExecRunner()
	if *selfCheckFlag {
		return SelfCheck(SelfCheckOptions{
			SettingsPath: *settingsPath, StatePath: *statePath,
			Runner: runner, AWSBinary: *awsBinary, ConfigPath: awsauth.DefaultConfigPath(),
		}, os.Stdout)
	}
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "yolo-aws-auth: --socket is required")
		return 2
	}

	broker, rc := prepare(spawnOptions{
		SettingsPath: *settingsPath, StatePath: *statePath, AWSBinary: *awsBinary,
		ConfigPath: awsauth.DefaultConfigPath(), Runner: runner,
	}, os.Stderr)
	if rc != 0 {
		return rc
	}

	stop := make(chan struct{})
	var stopOnce sync.Once
	shutdown := func() { stopOnce.Do(func() { close(stop) }) }
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-signals; shutdown() }()
	if !*noBackground {
		go runProactive(broker, *refreshInterval, stop, os.Stderr)
	}
	handler := BuildHandler(HandlerConfig{Broker: broker, ConfigPath: awsauth.DefaultConfigPath()})
	if err := serveSockets(handler, *socket, HostSocketPath(*socket), stop, shutdown); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-aws-auth:", err)
		return 1
	}
	return 0
}

// spawnOptions is what prepare needs, named rather than passed as six strings.
type spawnOptions struct {
	SettingsPath string
	StatePath    string
	AWSBinary    string
	ConfigPath   string
	Runner       awsauth.Runner
}

// prepare makes every SPAWN DECISION and returns the broker to serve with, or a
// non-zero exit code. Split out of Main so the decisions and — more to the point —
// THEIR ORDER are testable without binding a socket.
//
// The order is the contract. A configuration fault is reported as a configuration
// fault (exit 2) even on a host that also lacks the `aws` CLI, because the person
// reading the log has to fix the thing that is actually wrong first. The startup
// report comes last, after both refusals, so nothing is disclosed for a daemon that
// is not going to serve.
func prepare(opts spawnOptions, log io.Writer) (awsauth.Broker, int) {
	settings, err := awsauth.LoadSettings(opts.SettingsPath)
	if err != nil {
		fmt.Fprintln(log, "yolo-aws-auth:", err)
		return awsauth.Broker{}, 2
	}
	// STEP 2'S REFUSAL, at spawn, naming the key. Absence is never un-narrowed.
	config, err := settings.Resolve()
	if err != nil {
		fmt.Fprintln(log, "yolo-aws-auth: refusing to serve —", err)
		return awsauth.Broker{}, 2
	}

	// THE DEPENDENCY CHECK, and it is a real invocation rather than a PATH lookup:
	// `aws --version` needs no credentials, no network and no configuration, so it
	// answers "can this host run the CLI" and nothing else. See the package comment
	// for why this is a spawn refusal and not a manifest probe.
	if out := opts.Runner(context.Background(),
		[]string{opts.AWSBinary, "--version"}); !out.Spawned {
		fmt.Fprintln(log, "yolo-aws-auth: cannot run the `aws` CLI ("+opts.AWSBinary+"): "+
			"install AWS CLI v2 on the HOST. This loophole depends on it and deliberately does "+
			"not probe for it in its manifest — a loophole that vanishes when its program is "+
			"missing is worse than one that says so.")
		return awsauth.Broker{}, 1
	}

	reportStartup(log, config, opts.ConfigPath)
	return awsauth.Broker{
		StatePath: opts.StatePath,
		LockPath:  filepath.Join(filepath.Dir(opts.StatePath), awsauth.LockFileName),
		Config:    config,
		Minter:    awsauth.Minter{Run: opts.Runner, Binary: opts.AWSBinary},
	}, 0
}

func serveSockets(handler hostservice.Handler, frontedSocket, hostSocket string,
	stop <-chan struct{}, shutdown func()) error {
	errs := make(chan error, 2)
	go func() { errs <- hostservice.ServeFrontedUnix(handler, frontedSocket, stop) }()
	go func() { errs <- hostservice.ServeUnix(handler, hostSocket, stop) }()
	err := <-errs
	shutdown()
	<-errs
	return err
}

// reportStartup writes what this daemon resolved: the profile, the narrowing, the
// SSO config form (so the LOGIN CADENCE a user is signing up for is visible rather
// than discovered — §8), and the un-narrowed disclosure when it applies.
//
// It goes to stderr, which the run pipeline redirects to
// ~/.local/share/yolo-jail/logs/host-service-aws-auth.log and names in every
// warning it prints about this service.
func reportStartup(w io.Writer, config awsauth.Config, configPath string) {
	fmt.Fprintf(w, "aws-auth: serving AWS profile %q; narrowing: %s\n",
		config.Profile, config.Narrowing.Describe())
	form, err := awsauth.DetectForm(configPath, config.Profile)
	if err != nil {
		fmt.Fprintf(w, "aws-auth: could not read %s: %v\n", configPath, err)
	} else {
		fmt.Fprintf(w, "aws-auth: %s form — %s\n", form, form.Cadence())
	}
	if line := config.Narrowing.DisclosureLine(config.Profile); line != "" {
		fmt.Fprintln(w, line)
	}
}

// runProactive is the PRE-MINT TICKER — the design's R1. The mint is the slow step
// and the adapter's whole serve budget is under 200 ms, so the cache is kept warm
// ahead of every request rather than filled by one.
//
// It mints IMMEDIATELY on start, before the first tick, so a freshly spawned daemon
// is warm by the time the jail's first request arrives. A failure here is LOGGED AND
// NOT FATAL: a lapsed session is a runtime state the human fixes with one host-side
// command, and the message carries that command.
func runProactive(broker awsauth.Broker, interval time.Duration, stop <-chan struct{}, log io.Writer) {
	if interval <= 0 {
		interval = broker.TickInterval()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if broker.RemintDue() {
			if _, err := broker.Fetch(context.Background(), "proactive"); err != nil && log != nil {
				fmt.Fprintln(log, "aws-auth: proactive mint failed:", err)
			}
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// SelfCheckOptions is what a self-check needs to answer for a machine.
type SelfCheckOptions struct {
	SettingsPath string
	StatePath    string
	ConfigPath   string
	Runner       awsauth.Runner
	AWSBinary    string
}

// SelfCheck is the `doctor_cmd` health check `yolo check` runs.
//
// Its lines are GRADED by internal/cli/check's reportSelfCheckLines — "OK:" passes,
// "NOTE:" warns, "FAIL:" fails — so each line below is written to be read by that
// grader and by a human in the same pass.
//
// # A MISSING settings file is not a failure, and getting that wrong would be loud
//
// yolo writes the settings file when a jail LAUNCHES this loophole, so on a machine
// that has not launched one the file is simply absent — the normal state of a fresh
// install, which `yolo check` runs on. Reporting it as FAIL would put a red line
// under every fresh machine for a condition the user cannot act on and that the next
// `yolo` invocation fixes. A file that EXISTS and does not describe a servable
// configuration is the real fault.
func SelfCheck(opts SelfCheckOptions, out io.Writer) int {
	if opts.SettingsPath == "" {
		fmt.Fprintln(out, "OK: daemon present; no settings file in scope "+
			"(pass --settings to check one)")
		return 0
	}
	if info, err := os.Stat(opts.SettingsPath); err != nil || !info.Mode().IsRegular() {
		fmt.Fprintln(out, "OK: daemon present; no settings resolved yet at "+opts.SettingsPath+
			" — yolo writes it when a jail launches this loophole")
		return 0
	}
	settings, err := awsauth.LoadSettings(opts.SettingsPath)
	if err != nil {
		fmt.Fprintln(out, "FAIL: "+err.Error())
		return 1
	}
	config, err := settings.Resolve()
	if err != nil {
		fmt.Fprintln(out, "FAIL: "+err.Error())
		return 1
	}
	fmt.Fprintf(out, "OK: AWS profile %q; narrowing: %s\n", config.Profile,
		config.Narrowing.Describe())
	if form, formErr := awsauth.DetectForm(opts.ConfigPath, config.Profile); formErr == nil {
		fmt.Fprintf(out, "OK: %s form — %s\n", form, form.Cadence())
	}
	// A WIDENING IS A NOTE, not a pass. It is the one line `yolo check` should
	// surface every time (OQ-SSO1's "disclosed at every launch"), and a warn is how
	// this report says "working as configured, and you should know".
	if line := config.Narrowing.DisclosureLine(config.Profile); line != "" {
		fmt.Fprintln(out, "NOTE: "+line)
	}

	broker := awsauth.Broker{
		StatePath: opts.StatePath,
		LockPath:  filepath.Join(filepath.Dir(opts.StatePath), awsauth.LockFileName),
		Config:    config,
		Minter:    awsauth.Minter{Run: opts.Runner, Binary: opts.AWSBinary},
	}
	// IT MINTS. That is the whole value of this check over reading a file: it is the
	// only thing short of an agent turn that proves the host's SSO session is live
	// and the role is assumable. It therefore wants a host with an `aws` login, and
	// says what to do when there is not one.
	result, err := broker.Fetch(context.Background(), "self-check")
	if err != nil {
		fmt.Fprintln(out, "FAIL: "+err.Error())
		return 1
	}
	remaining := time.Until(result.Credential.ExpiresAt()).Round(time.Minute)
	fmt.Fprintf(out, "OK: credential %s (%s), %s of session lifetime remaining\n",
		result.Credential.Fingerprint(), result.Decision, remaining)
	// THE FOUR KEYS, WITH THE SECRET ELIDED. Printing the field names is the point:
	// a reader can see the protocol shape — including that the session token is
	// called `Token` here and `SessionToken` in `aws` output — without seeing a
	// credential.
	encoded, err := json.Marshal(result.Credential.Elided())
	if err != nil {
		return 1
	}
	fmt.Fprintln(out, "OK: container-credentials body "+string(encoded))
	return 0
}
