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
	settingsCheckFlag := fs.Bool("settings-check", false,
		"Validate settings without starting the daemon or contacting AWS")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *settingsCheckFlag {
		if !filepath.IsAbs(*settingsPath) {
			return writeSettingsCheckRefusal(os.Stdout, "The AWS settings snapshot could not be read.",
				"Check the host service settings file and run `yolo check --no-build` again.")
		}
		return SettingsCheck(*settingsPath, os.Stdout)
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

	// The startup-reason channel is the owner's and this process's alone: close-on-exec before
	// prepare runs `aws --version`, and released once nothing is left to refuse.
	hostservice.ProtectStartupReason()
	broker, rc := prepare(spawnOptions{
		SettingsPath: *settingsPath, StatePath: *statePath, AWSBinary: *awsBinary,
		ConfigPath: awsauth.DefaultConfigPath(), Runner: runner,
	}, os.Stderr)
	if rc != 0 {
		return rc
	}
	// No cooperative refusal follows prepare, so the channel closes here (the owner reads EOF, no
	// record) and its variables leave the environment before the proactive minter's `aws`
	// children could inherit them.
	hostservice.ReleaseStartupReason()
	// The state dir is created HERE, once, and never again: the daemon exits when it goes
	// (hostservice.WatchStateDir says why), and NoCreateDir stops a mint in the meantime
	// from bringing it back.
	stateDir := filepath.Dir(*statePath)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-aws-auth: create the state directory:", err)
		return 1
	}
	broker.NoCreateDir = true

	stop := make(chan struct{})
	var stopOnce sync.Once
	shutdown := func() { stopOnce.Do(func() { close(stop) }) }
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-signals; shutdown() }()
	if err := hostservice.WatchStateDir(stateDir, stop, func(reason string) {
		fmt.Fprintln(os.Stderr, hostservice.StateDirGoneExitLine("yolo-aws-auth", reason))
		shutdown()
	}); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-aws-auth: watch the state directory:", err)
		return 1
	}
	// ONE TRACKER for the proactive minter and the launch check, so a launch arriving while
	// the spawn-time mint runs waits for that mint instead of starting another (launchcheck.go).
	mints := newMintTracker(os.Stderr)
	if !*noBackground {
		go runProactive(broker, mints, *refreshInterval, stop)
	}
	handler := BuildHandler(HandlerConfig{Broker: broker, ConfigPath: awsauth.DefaultConfigPath(),
		Mints: mints, ModelLists: modelListSourceFor(broker, os.Stderr)})
	if err := serveSockets(handler, *socket, HostSocketPath(*socket), stop, shutdown); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-aws-auth:", err)
		return 1
	}
	return 0
}

// SettingsCheck is a resolver-only validator. It reads the supplied frozen snapshot, runs no
// AWS executable, performs no network/mint work, and projects typed refusals to fixed safe text.
func SettingsCheck(settingsPath string, out io.Writer) int {
	settings, err := awsauth.LoadSettings(settingsPath)
	if err != nil {
		return writeSettingsCheckRefusal(out, "The AWS settings snapshot could not be read.",
			"Check the host service settings file and run `yolo check --no-build` again.")
	}
	if _, err := settings.Resolve(); err == nil {
		return 0
	} else {
		var refusal *awsauth.ResolveRefusal
		if !errors.As(err, &refusal) {
			return writeSettingsCheckRefusal(out, "The AWS settings could not be resolved.",
				"Correct the user-scope AWS service settings and run `yolo check --no-build` again.")
		}
		reason, remedy := safeSettingsRefusal(refusal.Kind)
		return writeSettingsCheckRefusal(out, reason, remedy)
	}
}

func safeSettingsRefusal(kind awsauth.ResolveRefusalKind) (string, string) {
	scope := "loopholes.aws-auth.settings"
	switch kind {
	case awsauth.RefusalMissingProfile:
		return "No AWS profile is configured.", "Set " + scope + ".profile in ~/.config/yolo-jail/config.jsonc."
	case awsauth.RefusalMissingNarrowing:
		return "No AWS credential mode is configured.", "Set " + scope + ".role_arn (optionally with " + scope +
			".session_policy) to add restrictions, or explicitly set " + scope +
			".unnarrowed to true to use the assigned permission set as configured in ~/.config/yolo-jail/config.jsonc."
	case awsauth.RefusalPolicyWithoutRole:
		return "A session policy is configured without a role to assume.", "Set " + scope +
			".role_arn in ~/.config/yolo-jail/config.jsonc, or remove the session policy."
	case awsauth.RefusalConflictingNarrowing:
		return "AWS narrowing settings conflict.", "Choose either a role-based narrowing or " + scope +
			".unnarrowed in ~/.config/yolo-jail/config.jsonc."
	case awsauth.RefusalInvalidPolicy:
		return "The AWS session policy is not valid JSON.", "Correct " + scope +
			".session_policy in ~/.config/yolo-jail/config.jsonc."
	default:
		return "The AWS settings were refused.", "Correct the user-scope AWS service settings and run `yolo check --no-build` again."
	}
}

func writeSettingsCheckRefusal(out io.Writer, reason, remedy string) int {
	message, _ := json.Marshal(struct {
		Reason string `json:"reason"`
		Remedy string `json:"remedy"`
	}{reason, remedy})
	_, _ = out.Write(message)
	_, _ = out.Write([]byte("\n"))
	return 1
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
		refuseStartup(log, "configuration", "The AWS settings file could not be read.",
			"Check the user-scope AWS service settings file and run `yolo check --no-build` again.")
		return awsauth.Broker{}, 2
	}
	// STEP 2'S REFUSAL, at spawn, naming the key. Absence is never un-narrowed.
	config, err := settings.Resolve()
	if err != nil {
		var refusal *awsauth.ResolveRefusal
		if errors.As(err, &refusal) {
			reason, remedy := safeSettingsRefusal(refusal.Kind)
			refuseStartup(log, "configuration", reason, remedy)
		} else {
			refuseStartup(log, "configuration", "The AWS settings were refused.",
				"Correct the user-scope AWS service settings and run `yolo check --no-build` again.")
		}
		return awsauth.Broker{}, 2
	}

	// THE DEPENDENCY CHECK, and it is a real invocation rather than a PATH lookup:
	// `aws --version` needs no credentials, no network and no configuration, so it
	// answers "can this host run the CLI" and nothing else. See the package comment
	// for why this is a spawn refusal and not a manifest probe.
	if out := opts.Runner(context.Background(),
		[]string{opts.AWSBinary, "--version"}); !out.Spawned {
		refuseStartup(log, "dependency", "The AWS CLI v2 is not available on the host.",
			"Install AWS CLI v2 on the host, then retry the launch.")
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

func refuseStartup(log io.Writer, class, reason, remedy string) {
	_ = hostservice.WriteStartupReasonFromEnv(hostservice.StartupReason{
		Class: class, Reason: reason, Remedy: remedy,
	})
	fmt.Fprintf(log, "yolo-aws-auth: refusing to serve — %s Fix: %s\n", reason, remedy)
}

// modelListSourceFor is the `bedrock-models` action's source for broker: its cache beside the
// credential cache, its profile, and the same `aws` runner the mint uses.
func modelListSourceFor(broker awsauth.Broker, log io.Writer) ModelListSource {
	return ModelListSource{
		CachePath:   awsauth.ModelCachePath(broker.StatePath),
		Profile:     broker.Config.Profile,
		Lister:      awsauth.ModelLister{Run: broker.Minter.Run, Binary: broker.Minter.Binary},
		NoCreateDir: broker.NoCreateDir,
		Tracker:     NewModelListTracker(),
		Log:         log,
	}
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

// reportStartup writes what this daemon resolved: the profile and any narrowing mode
// (the explicitly configured permission-set mode is reserved for requested inspection),
// plus the SSO config form (so the LOGIN CADENCE is visible rather than discovered — §8).
//
// It goes to stderr, which the run pipeline redirects to
// ~/.local/share/yolo-jail/logs/host-service-aws-auth.log and names in every
// warning it prints about this service.
func reportStartup(w io.Writer, config awsauth.Config, configPath string) {
	fmt.Fprintf(w, "aws-auth: serving AWS profile %q", config.Profile)
	if config.Narrowing.Kind != awsauth.NarrowNone {
		fmt.Fprintf(w, "; narrowing: %s", config.Narrowing.Describe())
	}
	fmt.Fprintln(w)
	form, err := awsauth.DetectForm(configPath, config.Profile)
	if err != nil {
		fmt.Fprintf(w, "aws-auth: could not read %s: %v\n", configPath, err)
	} else {
		fmt.Fprintf(w, "aws-auth: %s form — %s\n", form, form.Cadence())
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
//
// It mints THROUGH mints, the tracker the launch check shares (launchcheck.go): a launch that
// arrives during this first mint waits for it rather than starting a second, a failure is
// remembered for the next launch to report, and the tracker writes the failure to its log.
func runProactive(broker awsauth.Broker, mints *mintTracker, interval time.Duration,
	stop <-chan struct{}) {
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
			<-mints.begin(broker, "proactive").done
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
