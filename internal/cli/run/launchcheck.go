package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// launchcheck.go is the LAUNCH half of the launch check: internal/hostservice/launchcheck.go
// coins the term and states the protocol, and a loophole opts in with
// `host_daemon.launch_check` (internal/loopholedecl). After the host services start, the launch
// asks each daemon that declares it what this launch should warn about, and prints the answer.
//
// It is keyed on each daemon's `host_daemon.launch_check` declaration, never on a
// loophole's name. A healthy configuration is silent here; current service failures and
// indeterminate checks are rendered from this attempt's bounded response.
//
// # Who is asked
//
// A daemon whose record declares the check, that this launch started or ensured (for an attach:
// whose front the running jail's launch published, runAttachLaunchChecks), and whose jail
// daemon this launch SERVES (loopholes.ServedJailDaemons: the container runs it, or on
// macos-user it runs in the guest or opens as a doorway outside it), or that declares no jail
// daemon at all. aws-auth's jail daemon is served only when some agent's provider is on
// Bedrock (providers.md OQ-CN7 (b)), so a launch with no such agent asks
// nothing: a warning about a service none of its agents reaches has nobody to warn.
//
// # What a daemon answers never refuses, and it is never silent about what it could not do
//
// Every warning prints and the launch proceeds (§8: a jail that will not start is worse than a
// first request that fails clearly). A daemon that could not be asked, or did not answer within
// the budget, gets one dim line saying so: the check is a disclosure, so its absence must not
// read as a clean bill. No flag hides either (docs/reference/report-tiers.md, OQ-RO3).
//
// # A host-wide daemon older than this yolo REFUSES a fresh launch
//
// Nothing restarts a host-wide daemon when yolo is upgraded, so the one a previous yolo started
// keeps running and answers every launch check `unknown action: launch-check`. A launch that went
// on would start agents without the check it promises them, so a FRESH launch REFUSES, naming
// broker.CycleCommand, which is safe for the jails already running: each one's front dials the
// daemon's socket per connection (HD-D2 (3)), so it reaches the restarted daemon on its next
// request. The ruling is OQ-HD11's (docs/design/host-daemon-ownership.md, 2026-10-05): the
// maintainer's "don't want to launch without a feature that is promised". What it covers is
// HD-D5's:
//
//   - THE OTHER "PREDATES THIS YOLO" STATE REFUSES THE SAME WAY (HD-D5 (3)): a host-wide daemon
//     started before yolo's front (startHostSingleton's predatesPreamble) accepts every connection
//     and fails every request. It is not asked the check, whose request it would misread too.
//   - AN ATTACH KEEPS THE YELLOW LINE, with the same command (HD-D5 (1)): it starts nothing and
//     restarts nothing, so refusing it would leave the running jail exactly as it is.
//   - "OLDER" IS A RECORDED FACT, not the answer alone (HD-D6): only a daemon whose launch-check
//     stamp is missing or stale (broker.SpawnedKnowingLaunchCheck) predates this yolo. One that a
//     yolo knowing the check spawned, and that still does not answer, runs a program lacking what
//     its manifest declares; the restart a refusal names would start the same program and refuse
//     again, so it gets the per-launch daemon's line instead. A per-launch daemon is always that
//     case: this launch just started it from its manifest.
//
// NO LAUNCH RESTARTS THE DAEMON ITSELF: two yolos on one host would restart each other's (the
// no-kill rule of modes 3 and 4), and only the user knows which yolo should own it. No hatch
// reaches the refusal (HD-D5 (4)): the daemon is yolo's own state, and the fix is one command.
//
// # What it costs
//
// One loopback TLS dial through the front the launch just published, and one framed request,
// for each daemon asked, all in parallel. A daemon answering from what it knows (aws-auth with
// a warm cache) replies in milliseconds. One that has to find out is bounded by
// hostservice.LaunchCheckBudget, and the launch stops reading launchCheckMargin after that.

// launchCheckMargin is how long past the budget the launch keeps reading: the dial, the TLS
// handshake and the framing on a loaded host, never the daemon's own work.
const launchCheckMargin = time.Second

// launchCheckTextMax caps one printed line from a daemon, in runes.
const launchCheckTextMax = 600

// launchCheckEntry is who asks the launch check, which decides what a daemon older than this yolo
// costs: a fresh launch refuses, and an attach says so and goes on (HD-D5 (1)).
type launchCheckEntry int

const (
	freshLaunchCheck launchCheckEntry = iota
	attachLaunchCheck
)

// runLaunchChecks asks every started daemon that is due one (see the file comment) and prints
// the answers in the order the daemons started. For a fresh launch it returns the refusal when a
// host-wide daemon it started or ensured predates this yolo, by the preamble or by the check, and
// nil otherwise; the caller prints it and stops, since what it must unwind (a keeper's services, a
// macos-user session) is the caller's. An attach is never refused: it prints the line instead.
func (o *Options) runLaunchChecks(rt string, started []loopholeDaemon, payload []loopholes.JailDaemonSpec,
	entry launchCheckEntry) *olderDaemonRefusal {
	lacks := preambleLacks(started)
	served := map[string]bool{}
	for _, s := range loopholes.ServedJailDaemons(rt, payload) {
		served[s.Name] = true
	}
	var due []loopholeDaemon
	for _, h := range started {
		if !h.launchCheck || h.hostPath == "" || h.predatesPreamble {
			continue
		}
		if h.hasJailDaemon && !served[h.name] {
			continue
		}
		due = append(due, h)
	}
	type answer struct {
		report hostservice.LaunchCheckReport
		err    error
	}
	answers := make([]answer, len(due))
	if len(due) > 0 {
		budget := hostservice.LaunchCheckBudget
		o.withStderrProgress("Checking host services", func() bool {
			var wg sync.WaitGroup
			for i, h := range due {
				wg.Add(1)
				go func(i int, h loopholeDaemon) {
					defer wg.Done()
					r, err := askLaunchCheck(h.hostPath, budget)
					answers[i] = answer{r, err}
				}(i, h)
			}
			wg.Wait()
			return true
		})
	}
	out := o.pr(o.Stderr)
	for i, h := range due {
		a := answers[i]
		var unknown *launchCheckUnknownError
		if errors.As(a.err, &unknown) {
			// ONLY A DAEMON AN OLDER YOLO LEFT RUNNING is one a restart fixes (HD-D6). A per-launch
			// daemon, and a host-wide one a yolo knowing the check spawned, lack it in their program.
			if h.hostWide && !broker.SpawnedKnowingLaunchCheck(broker.SingletonDeps(h.name, nil)) {
				if entry == freshLaunchCheck {
					lacks[h.name] = lacksLaunchCheck
					continue
				}
				out.print("[bold yellow]loophole " + h.name + ": " +
					richtext.Escape(launchCheckText(predatesLaunchCheckLine(h.name))) + "[/bold yellow]")
				continue
			}
			out.print("[bold yellow]loophole " + h.name + ": " +
				richtext.Escape(launchCheckText(unknownLaunchCheckLine(unknown))) + "[/bold yellow]")
			continue
		}
		if a.err != nil {
			out.print("[dim]loophole " + h.name + ": could not ask the host service what this " +
				"launch should know: " + richtext.Escape(launchCheckText(a.err.Error())) + "[/dim]")
			continue
		}
		for _, w := range a.report.Warnings {
			out.print("[bold yellow]loophole " + h.name + ": " +
				richtext.Escape(launchCheckText(w)) + "[/bold yellow]")
		}
		for _, n := range a.report.Notes {
			out.print("[dim]loophole " + h.name + ": " + richtext.Escape(launchCheckText(n)) + "[/dim]")
		}
	}
	if entry != freshLaunchCheck {
		return nil
	}
	return olderDaemonsIn(started, lacks)
}

// What a host-wide daemon older than this yolo lacks, each worded as the refusal's clause.
const (
	lacksLaunchCheck = "does not answer the launch check, so yolo cannot warn you about what " +
		"would fail your agents' requests"
	lacksPreamble = "does not speak the connection preamble, so it would accept your agents' " +
		"connections and fail every request"
)

// olderDaemon is one daemon a launch refuses, and what it lacks (lacksLaunchCheck, lacksPreamble).
type olderDaemon struct {
	name  string
	lacks string
}

// olderDaemonRefusal is the refusal of a fresh launch whose host-wide daemons predate this yolo
// (see the file comment): each by loophole name, in the order they started.
type olderDaemonRefusal struct {
	older   []olderDaemon
	startup *hostStartupRefusal
}

// hostStartupRefusal is an attempt-attributed configuration refusal from an opted-in daemon.
type hostStartupRefusal struct {
	name   string
	class  string
	reason string
	remedy string
}

func (r *hostStartupRefusal) Error() string {
	if r == nil {
		return ""
	}
	text := "host service '" + r.name + "' refused startup (" + r.class + "): " + r.reason
	if r.remedy != "" {
		text += "\nRemedy: " + r.remedy
	}
	// Every refusal ends with the retry, and names `yolo check --no-build` once: a remedy that
	// already names it gets only the retry.
	if strings.Contains(text, "yolo check --no-build") {
		text += "\nThen retry the launch."
	} else {
		text += "\nCorrect the settings, run `yolo check --no-build`, then retry the launch."
	}
	return text
}

// olderDaemonsIn is the refusal for the daemons in started that lacks names, in start order, or
// nil when it names none.
func olderDaemonsIn(started []loopholeDaemon, lacks map[string]string) *olderDaemonRefusal {
	var older []olderDaemon
	for _, h := range started {
		if l, ok := lacks[h.name]; ok {
			older = append(older, olderDaemon{name: h.name, lacks: l})
			delete(lacks, h.name)
		}
	}
	if len(older) == 0 {
		return nil
	}
	return &olderDaemonRefusal{older: older}
}

// preambleRefusal is the refusal for the daemons in started that predate the connection preamble,
// or nil: the one half of the refusal a launch that asks no launch check still owes (`yolo host`,
// HostDoorways.Start).
func preambleRefusal(started []loopholeDaemon) *olderDaemonRefusal {
	return olderDaemonsIn(started, preambleLacks(started))
}

// preambleLacks names lacksPreamble for each daemon in started that startHostSingleton found
// predating the connection preamble.
func preambleLacks(started []loopholeDaemon) map[string]string {
	lacks := map[string]string{}
	for _, h := range started {
		if h.predatesPreamble {
			lacks[h.name] = lacksPreamble
		}
	}
	return lacks
}

// text is the refusal after its headline: the daemons, that they predate this yolo and what each
// lacks, the one command line that restarts them, and that the jails already running survive it.
func (r *olderDaemonRefusal) text() string {
	if r.startup != nil {
		return r.startup.Error()
	}
	quoted := make([]string, len(r.older))
	cmds := make([]string, len(r.older))
	for i, d := range r.older {
		quoted[i] = "'" + d.name + "'"
		cmds[i] = broker.CycleCommand(d.name)
	}
	what := "the host-wide daemon for " + quoted[0] + " predates this yolo and " + r.older[0].lacks + "."
	it := "it"
	if len(r.older) > 1 {
		what = "the host-wide daemons for " + strings.Join(quoted[:len(quoted)-1], ", ") + " and " +
			quoted[len(quoted)-1] + " predate this yolo."
		for i, d := range r.older {
			what += "\n  " + quoted[i] + " " + d.lacks + "."
		}
		it = "them"
	}
	return what + "\n  Restart " + it + " with: " + strings.Join(cmds, " && ") +
		"\n  Jails already running reconnect to " + it + " on their next request. Then launch again."
}

// markup is the refusal as the launch stream prints it; headline names who refuses ("Refusing this
// launch", "Refusing the macos-user launch").
func (r *olderDaemonRefusal) markup(headline string) string {
	return "[bold red]" + richtext.Escape(headline+": "+r.text()) + "[/bold red]"
}

// predatesLaunchCheckLine is an attach's line for a host-wide daemon an older yolo left running,
// which a fresh launch refuses for (HD-D5 (1)): the attach goes on, and the line names the restart.
func predatesLaunchCheckLine(name string) string {
	return "the host-wide daemon predates this yolo and does not answer the launch check, so " +
		"this launch cannot warn about what would fail its agents' requests. Fix it with: " +
		broker.CycleCommand(name)
}

// launchCheckUnknownError is a daemon's answer that it does not know the launch-check action
// (hostservice.IsUnknownLaunchCheck).
type launchCheckUnknownError struct {
	rc     int
	detail string
}

func (e *launchCheckUnknownError) Error() string {
	return fmt.Sprintf("it exited %d: %s", e.rc, e.detail)
}

// unknownLaunchCheckLine is the warning for a daemon that does not know the action and that a yolo
// knowing the check started: a per-launch one, which this launch started from its manifest, or a
// host-wide one whose launch-check stamp is current (HD-D6). Its manifest declares a check its
// program does not answer, and no restart changes the program, so there is no command to name. A
// host-wide one an older yolo left running is refused for instead (olderDaemonRefusal).
func unknownLaunchCheckLine(e *launchCheckUnknownError) string {
	return "its daemon does not answer the launch check its manifest declares (it said: " +
		e.detail + "), so this launch cannot warn about what would fail its agents' requests"
}

// launchCheckText makes a daemon's string safe to print as one terminal line: control
// characters become spaces, and it is capped at launchCheckTextMax runes. A daemon is host code
// the launch already runs, but its words still must not move the cursor or forge a second line.
func launchCheckText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if n == launchCheckTextMax {
			b.WriteString("…")
			break
		}
		// Format characters (Cf) too: a bidi override or zero-width character from a daemon
		// would reorder or hide the launch-check text a reader sees.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// askLaunchCheck dials the endpoint file as a host-side client (svcendpoint.DialLocal, the
// probe shape that authenticates with the 0600 file this process published) and sends one
// launch-check request. The whole exchange is bounded by budget plus launchCheckMargin.
func askLaunchCheck(endpointPath string, budget time.Duration) (hostservice.LaunchCheckReport, error) {
	deadline := time.Now().Add(budget + launchCheckMargin)
	conn, err := svcendpoint.DialLocal(endpointPath, launchCheckMargin)
	if err != nil {
		return hostservice.LaunchCheckReport{}, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)
	return exchangeLaunchCheck(conn, budget)
}

// exchangeLaunchCheck is the framed conversation on an open connection.
func exchangeLaunchCheck(conn net.Conn, budget time.Duration) (hostservice.LaunchCheckReport, error) {
	body, err := json.Marshal(map[string]any{
		"action":                         hostservice.LaunchCheckAction,
		hostservice.LaunchCheckBudgetKey: budget.Milliseconds(),
	})
	if err != nil {
		return hostservice.LaunchCheckReport{}, err
	}
	if err := frameproto.WriteRequest(conn, body); err != nil {
		return hostservice.LaunchCheckReport{}, fmt.Errorf("send: %w", err)
	}
	var stdout, stderr bytes.Buffer
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return hostservice.LaunchCheckReport{}, fmt.Errorf("no answer within %s", budget+launchCheckMargin)
			}
			return hostservice.LaunchCheckReport{}, fmt.Errorf("read: %w", err)
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			stdout.Write(frame.Payload)
		case frameproto.StreamStderr:
			stderr.Write(frame.Payload)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(frame.Payload)
			if err != nil {
				return hostservice.LaunchCheckReport{}, err
			}
			if rc != 0 {
				detail := firstNonEmptyLine(stderr.String(), "no diagnostic")
				// A daemon started by a yolo that predates the launch check answers this way.
				if hostservice.IsUnknownLaunchCheck(detail) {
					return hostservice.LaunchCheckReport{}, &launchCheckUnknownError{rc, detail}
				}
				return hostservice.LaunchCheckReport{}, fmt.Errorf("it exited %d: %s", rc, detail)
			}
			var report hostservice.LaunchCheckReport
			if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &report); err != nil {
				return hostservice.LaunchCheckReport{}, fmt.Errorf("malformed answer: %w", err)
			}
			return report, nil
		}
	}
}

// firstNonEmptyLine is s's first non-blank line, or fallback.
func firstNonEmptyLine(s, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return fallback
}

// runAttachLaunchChecks is the launch check for an ATTACH: a new entry into a jail that is
// already running, whose host services its own launch started and still fronts. Attaching is
// how a user re-enters a long-lived jail (`yolo -p bedrock -- claude` in a second terminal), and
// an SSO session that was live at the launch may have lapsed since, so the entry is asked about
// the same way a launch is. The daemons asked are the enabled loopholes declaring the check
// whose front the running jail's launch published in its services dir, and the served test is
// this entry's payload (what its selection runs), exactly as runLaunchChecks applies it.
//
// IT ASKS AND NEVER STARTS. An attach runs above the config-change approval gate and never
// starts, ensures or restarts a service (noteSingletonSettingsDrift's rule), and a front belongs
// to the jail's keeper. So a loophole with no published endpoint is not asked: its launch did not
// start it, or the backend does not run it (Apple Container starts no aws-auth service). An
// endpoint file left by a keeper that has since died is dialled and fails, which prints the dim
// "could not ask" line, and that is true: the jail cannot reach the service either.
//
// IT NEVER REFUSES (HD-D5 (1)): a host-wide daemon an older yolo left running gets the yellow line
// naming the restart, where a fresh launch refuses. Refusing the entry would leave the running jail
// exactly as it is, and the attach restarts nothing to change that.
func (o *Options) runAttachLaunchChecks(cname, rt string, cfg *jsonx.OrderedMap, payload []loopholes.JailDaemonSpec) {
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	var running []loopholeDaemon
	for _, lp := range loopholes.NewHostSet(cfgMap(cfg, "loopholes")).Enabled() {
		if lp.HostDaemon == nil || !lp.HostDaemon.LaunchCheck ||
			lp.Transport != loopholes.TransportLoopbackTLS {
			continue
		}
		hostPath := filepath.Join(socketsDir, lp.Name+paths.ServiceEndpointExt)
		if !fileExists(hostPath) {
			continue
		}
		running = append(running, markLaunchCheck(loopholeDaemon{name: lp.Name, hostPath: hostPath}, lp))
	}
	o.runLaunchChecks(rt, running, payload, attachLaunchCheck)
}
