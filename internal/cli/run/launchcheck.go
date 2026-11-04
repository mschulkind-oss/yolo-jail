package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
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
// # An older yolo's machine-wide daemon, still running, is LEFT ALONE and NAMED
//
// Since HD-R1 (docs/design/host-daemon-ownership.md) every launch starts its own copy of each host
// daemon, so the daemon a fresh launch asks is always this yolo's build: one that answers
// `unknown action: launch-check` has a manifest declaring a check its program lacks, and gets the
// line saying so (unknownLaunchCheckLine), naming no restart. OQ-HD11's refusal (HD-D5), which
// named the restart of a host-wide daemon too old to answer, has no such daemon left to meet.
//
// THE HOST-WIDE DAEMON THAT CAN STILL EXIST is the one an older yolo started and left running
// through the upgrade; only a jail that yolo launched can use it, and whether one still runs is not
// visible here. By the owner's coexistence decision (2026-10-16, which supersedes HD-D21's refusal
// and restores HD-D14), it is left alone until those jails end or the host reboots: a launch never
// starts, fronts, restarts or stops it, and a fresh launch beside it PROCEEDS. It works on the same
// credential file as this launch's copy and takes the same lock, which serializes the two. A fresh
// launch that started a copy of a loophole such a copy is alive for prints one dim line saying what
// is known (noteOlderHostWideCopies, HD-D31), naming the optional cleanup once no older jail is
// running, broker.StopHostWideCommand. It is keyed on the fact, a live
// copy of this home's at the name-keyed rendezvous (broker.LegacyDeps), and never on the
// loophole's name.
//
//   - AN ATTACH SAYS SO TOO (HD-D5 (1)). An attach into a jail an older yolo launched dials that
//     jail's front, which leads to the older copy; when that copy does not answer the check, the
//     yellow line names relaunching the jail with this yolo, then the same optional stop
//     (olderHostWideAttachLine).
//   - NO LAUNCH STOPS THE OLDER COPY ITSELF: a jail an older yolo launched may still use it, and
//     only the user knows when those have ended.
//
// # What it costs
//
// One loopback TLS dial through the front the launch just published, and one framed request,
// for each daemon asked, all in parallel. A daemon answering from what it knows (aws-auth with
// a warm cache) replies in milliseconds. One that has to find out is bounded by
// launchCheckBudget, and the launch stops reading launchCheckMargin after that.

// launchCheckMargin is how long past the budget the launch keeps reading: the dial, the TLS
// handshake and the framing on a loaded host, never the daemon's own work.
const launchCheckMargin = time.Second

// launchCheckBudget is the budget runLaunchChecks sends each daemon, hostservice.LaunchCheckBudget.
// It is a var only so a test whose daemon does real local work (a canned mint still takes the
// state lock and fsyncs the state file) can raise it to the daemon's cap and assert the daemon's
// answer rather than how fast a loaded test machine reached it.
var launchCheckBudget = hostservice.LaunchCheckBudget

// launchCheckTextMax caps one printed line from a daemon, in runes.
const launchCheckTextMax = 600

// launchCheckEntry is who asks the launch check: a fresh launch, which names an older yolo's
// machine-wide copy of a loophole it started (noteOlderHostWideCopies), or an attach, whose front
// may lead to one (HD-D5 (1)). Neither refuses.
type launchCheckEntry int

const (
	freshLaunchCheck launchCheckEntry = iota
	attachLaunchCheck
)

// runLaunchChecks asks every started daemon that is due one (see the file comment) and prints
// the answers in the order the daemons started. A fresh launch first names each older yolo's
// machine-wide copy of a loophole it started (noteOlderHostWideCopies). It refuses nothing: the
// coexistence decision (HD-D26) leaves that copy serving the jails its yolo launched.
func (o *Options) runLaunchChecks(rt string, started []loopholeDaemon, payload []loopholes.JailDaemonSpec,
	entry launchCheckEntry) {
	if entry == freshLaunchCheck {
		o.noteOlderHostWideCopies(started)
	}
	served := map[string]bool{}
	for _, s := range loopholes.ServedJailDaemons(rt, payload) {
		served[s.Name] = true
	}
	var due []loopholeDaemon
	for _, h := range started {
		if !h.launchCheck || h.hostPath == "" {
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
		budget := launchCheckBudget
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
			// ONLY AN ATTACH'S FRONT CAN LEAD TO AN OLDER YOLO'S COPY (runAttachLaunchChecks sets
			// hostWide); a fresh launch's daemon is its own build, whose manifest is what is wrong.
			if h.hostWide {
				out.print("[bold yellow]loophole " + h.name + ": " +
					richtext.Escape(launchCheckText(olderHostWideAttachLine(h.name))) + "[/bold yellow]")
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
}

// olderHostWideAlive reports whether the loophole called name still has a machine-wide copy an
// older yolo left running for THIS home: a live PID file and a socket that accepts at the
// name-keyed rendezvous (broker.LegacyDeps), which no yolo since HD-R1 writes, whose process runs
// under this launch's home (broker.LegacyCopyServesHome). A copy under another home shares the
// rendezvous paths, which are keyed by the name in /tmp, but none of this launch's credentials.
func olderHostWideAlive(name string) bool {
	deps := broker.LegacyDeps(name)
	return broker.BrokerIsAlive(deps) && broker.LegacyCopyServesHome(deps, paths.Home())
}

// servicesRefusal is the refusal startPlannedLoopholes returns beside its handles. Since the
// owner's coexistence decision (2026-10-16, HD-D26) an older yolo's machine-wide copy refuses
// nothing, so the one refusal it carries is an opted-in daemon's startup refusal.
type servicesRefusal struct {
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
	// Every refusal ends with the retry. Only a configuration refusal blames the settings, and
	// `yolo check --no-build` is named once: a remedy that already names it gets only the retry.
	switch {
	case r.class != "configuration" || strings.Contains(text, "yolo check --no-build"):
		text += "\nThen retry the launch."
	default:
		text += "\nCorrect the settings, run `yolo check --no-build`, then retry the launch."
	}
	return text
}

// noteOlderHostWideCopies prints one dim line for each loophole in started, once and in start
// order, that still has a machine-wide copy an older yolo left running for this home
// (olderHostWideCopyLine). It refuses nothing and stops nothing: the owner's coexistence decision
// (2026-10-16) leaves that copy to the jails its yolo launched, if any still run. Every fresh launch that starts
// host services owes it: the keeper and the macos-user arm through runLaunchChecks, and `yolo
// host`'s HostDoorways.Start, which asks no launch check. A launch has no quiet mode (OQ-RO3), so
// no flag hides it.
func (o *Options) noteOlderHostWideCopies(started []loopholeDaemon) {
	var named []string
	for _, h := range started {
		if slices.Contains(named, h.name) || !olderHostWideAlive(h.name) {
			continue
		}
		named = append(named, h.name)
		o.pr(o.Stderr).print("[dim]loophole " + h.name + ": " +
			richtext.Escape(olderHostWideCopyLine(h.name)) + "[/dim]")
	}
}

// olderHostWideCopyLine is a fresh launch's one line beside an older yolo's machine-wide copy of
// the loophole called name, saying only what the launch knows (HD-D31): the copy is running, no
// jail this yolo launches uses it, only a jail an older yolo launched can, and the stop once none
// is. Whether such a jail is still running is not something the launch can see.
func olderHostWideCopyLine(name string) string {
	return "an older yolo's machine-wide daemon is still running; jails this yolo launches do not " +
		"use it, and only a jail an older yolo launched can. Once none is " +
		"running, `" + broker.StopHostWideCommand(name) + "` ends it"
}

// text is the refusal after its headline: the startup refusal's own words.
func (r *servicesRefusal) text() string {
	return r.startup.Error()
}

// markup is the refusal as the launch stream prints it; headline names who refuses ("Refusing this
// launch", "Refusing the macos-user launch").
func (r *servicesRefusal) markup(headline string) string {
	return "[bold red]" + richtext.Escape(headline+": "+r.text()) + "[/bold red]"
}

// olderHostWideAttachLine is an attach's line when the jail it enters fronts a machine-wide copy an
// older yolo left running and that copy does not answer the launch check (HD-D5 (1)): the attach
// goes on, and the line names the coexistence order (the owner's decision, 2026-10-16): relaunch
// this jail with this yolo, then, once no jail the older yolo launched is left, the optional stop.
func olderHostWideAttachLine(name string) string {
	return "this jail was launched by an older yolo, whose machine-wide daemon does not answer the " +
		"launch check, so this entry cannot warn about what would fail its agents' requests. Leave " +
		"this jail and launch it again with this yolo, which gives it its own; once no jail the " +
		"older yolo launched is left, `" + broker.StopHostWideCommand(name) + "` ends that daemon " +
		"(a reboot does too)"
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

// unknownLaunchCheckLine is the warning for a daemon of this yolo's that does not know the action:
// this launch started it from its manifest, which declares a check its program does not answer,
// and no restart changes the program, so there is no command to name.
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
// starts or restarts a service (HD-D2 part 5 in docs/design/host-daemon-ownership.md), and a front belongs
// to the jail's keeper. So a loophole with no published endpoint is not asked: its launch did not
// start it, or the backend does not run it (Apple Container starts no aws-auth service). An
// endpoint file left by a keeper that has since died is dialled and fails, which prints the dim
// "could not ask" line, and that is true: the jail cannot reach the service either.
//
// IT NEVER REFUSES (HD-D5 (1)). A jail an older yolo launched fronts that yolo's machine-wide copy,
// and while such a copy is alive the entry's handle says so (hostWide), so a copy that does not
// answer the check gets the line naming the relaunch and then the optional stop
// (olderHostWideAttachLine). Refusing the entry would leave the running jail exactly as it is.
func (o *Options) runAttachLaunchChecks(cname, rt string, cfg *jsonx.OrderedMap, payload []loopholes.JailDaemonSpec) {
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	var running []loopholeDaemon
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(cfg)).Enabled() {
		if lp.HostDaemon == nil || !lp.HostDaemon.LaunchCheck ||
			lp.Transport != loopholes.TransportLoopbackTLS {
			continue
		}
		hostPath := filepath.Join(socketsDir, lp.Name+paths.ServiceEndpointExt)
		if !fileExists(hostPath) {
			continue
		}
		running = append(running, markLaunchCheck(loopholeDaemon{name: lp.Name, hostPath: hostPath,
			hostWide: olderHostWideAlive(lp.Name)}, lp))
	}
	o.runLaunchChecks(rt, running, payload, attachLaunchCheck)
}
