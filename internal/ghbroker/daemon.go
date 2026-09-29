package ghbroker

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// daemon.go is the github-broker's host daemon, `yolo internal daemon github-broker`
// (docs/design/boundary-broker.md §4, step 1 of §11): one per jail, a loophole's host daemon
// behind yolo's own loopback-TLS front, so every connection carries the host-asserted jail
// id in its preamble (BB-D1).
//
// Step 1 is the read path: a command in the standing `read-only` set, in scope, runs the
// host's gh and its stdout, stderr and exit code cross verbatim (OQ-C); a refused or
// out-of-scope command exits 64; every read-write command exits 77, "writes need approval,
// which this version cannot ask for". Every call is audited.

// Source is the GitHub broker's source name: the approval record's scope key, the store's
// directory and the audit log's `service`. It is also what packs/github's manifest declares
// as `brokered.source`, which is the spelling core reads.
const Source = "github"

// Request is the forwarder's frame, and the whole of what a jail can say: an argv, the
// repository it resolved, its stdin, and an agent name it reports. None of it is a
// decision, a grant, a jail identity or a duration (§9.3).
type Request struct {
	Argv  []string
	Repo  string
	Stdin []byte
	// StdinSent says the forwarder sent stdin, which is audited by digest even when the
	// command does not read it.
	StdinSent bool
	Agent     string
}

// Broker answers one jail's calls.
type Broker struct {
	runner    *Runner // nil when the host has no gh
	scope     Scope
	workspace string
	audit     *brokeraudit.Log
	log       io.Writer
	now       func() time.Time
}

// Main is `yolo internal daemon github-broker`.
func Main(argv []string) int {
	fs := flag.NewFlagSet("github-broker", flag.ContinueOnError)
	socket := fs.String("socket", "", "AF_UNIX socket to bind (behind yolo's front)")
	scopeFile := fs.String("scope-file", "", "this launch's scope file, written by the launch")
	selfCheck := fs.Bool("self-check", false, "report the host gh and exit (used by `yolo check`)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *selfCheck {
		return SelfCheck(os.Stdout)
	}
	if *socket == "" || *scopeFile == "" {
		fmt.Fprintln(os.Stderr, "github-broker: --socket and --scope-file are required")
		return 2
	}
	// FAIL CLOSED: a broker whose launch handed it no readable scope runs nothing, rather
	// than guessing a scope from anywhere else (BB-D32: it never reads the remotes or the
	// approval record).
	sf, err := brokerscope.ReadFile(*scopeFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "github-broker: refusing to start:", err)
		return 1
	}
	b, cleanup := newBroker(sf, os.Stderr)
	defer cleanup()
	stop := make(chan struct{})
	if err := hostservice.ServeFrontedUnix(b.Handle, *socket, stop); err != nil {
		fmt.Fprintln(os.Stderr, "github-broker:", err)
		return 1
	}
	return 0
}

// newBroker builds a broker for one launch's scope file, logging what it starts with.
func newBroker(sf brokerscope.File, log io.Writer) (*Broker, func()) {
	startID, _ := brokerscope.NewLaunchID()
	runDir := filepath.Join(paths.BrokerSourceDir(Source), "run", startID)
	b := &Broker{
		scope:     NewScope(append(append([]string(nil), sf.Repos...), sf.Widened...)),
		workspace: sf.Workspace,
		audit: brokeraudit.Open(paths.BrokerAuditLog(), func(msg string) {
			fmt.Fprintln(log, "github-broker:", msg)
		}),
		log: log,
		now: time.Now,
	}
	fmt.Fprintf(log, "github-broker: scope for %s: %s\n", sf.Workspace, b.scope.describe())
	r, err := NewRunner(RunnerOptions{RunDir: runDir, Getenv: os.Getenv})
	if err != nil {
		fmt.Fprintln(log, "github-broker: no host gh:", err, "— every call answers 69")
		return b, func() { _ = os.RemoveAll(runDir) }
	}
	b.runner = r
	fmt.Fprintln(log, "github-broker:", r.Describe())
	return b, r.Close
}

// SelfCheck is `doctor_cmd`: it reports the host gh the broker would run, and fails only
// when there is none.
func SelfCheck(w io.Writer) int {
	dir, err := os.MkdirTemp("", "github-broker-check-")
	if err != nil {
		fmt.Fprintln(w, "github-broker: cannot make a scratch dir:", err)
		return 1
	}
	r, err := NewRunner(RunnerOptions{RunDir: dir, Getenv: os.Getenv})
	if err != nil {
		_ = os.RemoveAll(dir)
		fmt.Fprintln(w, "github-broker: no gh on the host's PATH; install the GitHub CLI and run `gh auth login`")
		return 1
	}
	defer r.Close()
	fmt.Fprintln(w, "github-broker: "+r.Describe())
	if !r.TokenRead {
		fmt.Fprintln(w, "github-broker: the host gh reports no GitHub login it can use; run `gh auth status` on the host")
	}
	return 0
}

// Handle is the hostservice handler: one frame in, gh's output and an exit code out.
func (b *Broker) Handle(s *hostservice.Session) {
	req := requestFrom(s)
	code := b.Serve(req, s.JailID, s.StdoutBytes, func(p []byte) { s.Stderr(string(p)) })
	s.Exit(code)
}

// requestFrom reads the forwarder's frame. A malformed field reads as absent, and a
// missing argv is an empty one, which the classifier refuses.
func requestFrom(s *hostservice.Session) Request {
	var r Request
	if v, ok := s.Get("argv"); ok {
		if list, ok := v.([]any); ok {
			for _, a := range list {
				if str, ok := a.(string); ok {
					r.Argv = append(r.Argv, str)
				}
			}
		}
	}
	if v, ok := s.Get("repo"); ok {
		r.Repo, _ = v.(string)
	}
	if v, ok := s.Get("agent"); ok {
		r.Agent, _ = v.(string)
	}
	if v, ok := s.Get("stdin"); ok {
		if str, ok := v.(string); ok {
			if data, err := base64.StdEncoding.DecodeString(str); err == nil {
				r.Stdin, r.StdinSent = data, true
			}
		}
	}
	return r
}

// Serve classifies and, for a standing read, runs one call, returning its exit code. It
// appends the audit line whatever happened (BB-P7).
func (b *Broker) Serve(req Request, jail string, stdout, stderr func([]byte)) int {
	start := b.now()
	ev := brokeraudit.Event{Event: "call", Service: Source, Jail: jail, Workspace: b.workspace,
		Agent: agentName(req.Agent), AgentReported: agentName(req.Agent) != "", Argv: req.Argv}
	if b.runner != nil {
		ev.GHVersion = b.runner.Version
	}
	if req.StdinSent {
		sum := sha256.Sum256(req.Stdin)
		ev.Stdin = &brokeraudit.Stdin{Bytes: len(req.Stdin), SHA256: hex.EncodeToString(sum[:])}
	}
	say := func(msg string) { stderr([]byte("github-broker: " + msg + "\n")) }
	finish := func(code int) int {
		ev.Exit = &code
		ev.ElapsedMs = b.now().Sub(start).Milliseconds()
		b.audit.Append(ev)
		return code
	}

	if len(req.Stdin) > StdinCap {
		ev.Set, ev.Outcome, ev.Reason = "refused", "refused", "stdin over the 1 MiB cap"
		say(fmt.Sprintf("refused: standard input is %d bytes, over the %d-byte cap", len(req.Stdin), StdinCap))
		return finish(ExitUsage)
	}
	d := Classify(req.Argv, req.Repo, b.scope)
	ev.Repo = strings.Join(d.Repos, ",")
	if d.Argv != nil {
		ev.Argv = d.Argv
	}
	switch d.Outcome {
	case OutcomeRefused:
		ev.Set, ev.Outcome, ev.Reason = "refused", "refused", d.Reason
		say("refused: " + d.Reason)
		return finish(ExitUsage)
	case OutcomeOutOfScope:
		ev.Set, ev.Outcome, ev.Reason = "out-of-scope", "refused", d.Reason
		say("out of scope: " + d.Reason)
		return finish(ExitUsage)
	case OutcomeWindowed:
		ev.Set, ev.Outcome = d.Set, "denied"
		ev.Reason = "writes need approval, which this version cannot ask for"
		say("`gh " + d.Path + "` is in the " + d.Set + " set, and writes need approval, which " +
			"this version cannot ask for. Nothing ran.")
		return finish(ExitNoPerm)
	}

	// A standing read.
	ev.Set = d.Set
	if b.runner == nil {
		ev.Outcome, ev.Reason = "unavailable", "no gh on the host"
		say("the host has no gh on its PATH; install the GitHub CLI on the host and run `gh auth login` there")
		return finish(ExitUnavailable)
	}
	if !b.runner.Tested {
		// §5.1 rule 6: outside the tested range no set applies, so even a read needs its own
		// Allow once — which is a request this version cannot file.
		ev.Outcome = "denied"
		ev.Reason = "host gh " + b.runner.Version + " is outside the tested range " + testedMinor + ".x"
		say("the host's gh is version " + b.runner.Version + ", outside the " + testedMinor +
			".x range this broker was measured against, so no set applies and every command needs " +
			"its own approval, which this version cannot ask for. Nothing ran.")
		return finish(ExitNoPerm)
	}
	var stdin []byte
	if d.ReadsStdin {
		stdin = req.Stdin
		if stdin == nil {
			stdin = []byte{}
		}
	}
	res := b.runner.Run(d.Argv, stdin, stdout, stderr)
	ev.Outcome = "ran"
	ev.BytesOut, ev.Redactions = res.BytesOut, res.Redactions
	if res.Redactions > 0 {
		fmt.Fprintf(b.log, "github-broker: redacted the host token %d time(s) from `gh %s` — a "+
			"classifier bug: that command should have been refused\n", res.Redactions, d.Path)
	}
	code := res.Exit
	if code == ghExitAuth {
		// §3.5: no host login is the broker's unavailability, not the jail's missing token.
		say("the host's gh has no usable GitHub login; run `gh auth status` on the host. The broker " +
			"never starts a login.")
		ev.Reason = "gh exited 4: no host login"
		code = ExitUnavailable
	}
	return finish(code)
}

// agentName keeps a reported agent name to something printable and short, since it is
// the jail's word.
func agentName(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return ""
		}
	}
	return s
}
