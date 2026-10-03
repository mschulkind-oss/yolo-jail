package ghbroker

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
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
	runner *Runner // nil when the host has no gh, or one the broker will not run
	// unavailable is what a call is told when runner is nil; "" means the host has no gh.
	unavailable string
	scope       Scope
	workspace   string
	audit       *brokeraudit.Log
	log         io.Writer
	now         func() time.Time
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
	// The launch spawns this daemon from the workspace. Leave it for a directory the broker
	// owns, so nothing this process does later resolves a relative path in the agent's tree;
	// the workspace is still what the host gh's placement is checked against.
	spawnCwd, _ := os.Getwd()
	if dir := paths.BrokerSourceDir(Source); os.MkdirAll(dir, 0o700) == nil {
		_ = os.Chdir(dir)
	}
	b, cleanup := newBroker(sf, spawnCwd, os.Stderr)
	defer cleanup()
	// TOLD TO STOP, END gh FIRST. The launcher stops a daemon with SIGTERM to its process
	// group and SIGKILLs it after a grace; the listener then waits for every call in flight,
	// and a gh in its own process group never saw the SIGTERM. So the broker kills its gh
	// children itself, the calls return inside the grace, and the deferred cleanup, which
	// removes the copy of the host's hosts.yml, runs.
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		if b.runner != nil {
			b.runner.Shutdown()
		}
		close(stop)
	}()
	if err := hostservice.ServeFrontedUnix(b.Handle, *socket, stop); err != nil {
		fmt.Fprintln(os.Stderr, "github-broker:", err)
		return 1
	}
	return 0
}

// newBroker builds a broker for one launch's scope file, logging what it starts with.
// spawnCwd is the directory the launch spawned it from, the workspace.
//
// THE LOG NAMES NO WORKSPACE, NO REPOSITORY AND NO HOST PATH. It is the daemon's stderr,
// which the launch appends to one file per loophole name under logs/, shared by every jail
// on the machine, and logs/ is the directory a `mounts` entry commonly exposes to a jail. So
// one jail could read every other workspace's path and approved private repositories there.
// The launch already discloses the scope on its own terminal (writeScopeFiles), the jail's
// stderr names a refused gh, and the audit log, which yolo mounts into no jail, holds the rest.
func newBroker(sf brokerscope.File, spawnCwd string, log io.Writer) (*Broker, func()) {
	sweepRunDirs()
	runDir := newRunDir()
	b := &Broker{
		scope:     NewScope(append(append([]string(nil), sf.Repos...), sf.Widened...)).ForWorkspace(sf.Workspace),
		workspace: sf.Workspace,
		audit: brokeraudit.Open(paths.BrokerAuditLog(), func(msg string) {
			fmt.Fprintln(log, "github-broker:", msg)
		}),
		log: log,
		now: time.Now,
	}
	r, err := NewRunner(RunnerOptions{RunDir: runDir, Getenv: os.Getenv,
		Refuse: placementRefusal(sf.Workspace, spawnCwd)})
	if err != nil {
		var refused *RefusedGHError
		if errors.As(err, &refused) {
			b.unavailable = refused.Why
			fmt.Fprintln(log, "github-broker: the host gh on PATH is inside a tree an agent writes, so the "+
				"broker will not run it; every call answers 69 and says why")
		} else {
			fmt.Fprintln(log, "github-broker: no gh on the host's PATH; every call answers 69")
		}
		return b, func() { _ = os.RemoveAll(runDir) }
	}
	b.runner = r
	fmt.Fprintln(log, "github-broker:", r.Summary())
	return b, r.Close
}

// placementRefusal is the loophole placement rule (config.AgentWritableTreeOf, the tree
// comparison config.LoopholePlacementProblems makes, without its argv-reading skips;
// docs/reference/loophole-system.md#the-placement-rule) applied to the host gh: a gh inside
// the workspace or the jail home tree is a program an agent can rewrite, and the broker runs
// it on the host as the user, so it refuses it by name. Each workspace is checked as given
// and with its symlinks resolved, because NewRunner asks about the gh both ways. A directory
// no launch may use as a workspace (the home, or one holding it: paths.WorkspaceScopeBreach)
// is skipped, since `yolo check` run from the home would otherwise read every host gh under
// it as the agent's.
func placementRefusal(workspaces ...string) func(gh string) string {
	var trees []string
	for _, ws := range workspaces {
		if ws == "" || paths.WorkspaceScopeBreach(ws) != nil {
			continue
		}
		trees = append(trees, ws)
		if real, err := filepath.EvalSymlinks(ws); err == nil && real != ws {
			trees = append(trees, real)
		}
	}
	if len(trees) == 0 {
		trees = []string{""} // the jail home tree alone
	}
	return func(gh string) string {
		for _, ws := range trees {
			if what := config.AgentWritableTreeOf(gh, ws); what != "" {
				return "the broker will not run the host gh: " + gh + " is inside " + what +
					", where an agent can rewrite it between launches (the placement rule, " +
					"docs/reference/loophole-system.md#the-placement-rule). Put a gh installed outside " +
					"that tree first on the PATH yolo is launched with."
			}
		}
		return ""
	}
}

// SelfCheck is `doctor_cmd`: it reports the host gh the broker would run, and fails only
// when there is none.
func SelfCheck(w io.Writer) int {
	// Under run/ like a broker's, not the system temp dir: the check copies the host's
	// hosts.yml too, and run/ is where a copy a killed check left is collected.
	sweepRunDirs()
	dir := newRunDir()
	// `yolo check` runs this from the workspace, as a launch spawns the daemon, so the cwd is
	// checked as the workspace.
	cwd, _ := os.Getwd()
	r, err := NewRunner(RunnerOptions{RunDir: dir, Getenv: os.Getenv, Refuse: placementRefusal(cwd)})
	if err != nil {
		_ = os.RemoveAll(dir)
		var refused *RefusedGHError
		if errors.As(err, &refused) {
			fmt.Fprintln(w, "github-broker: "+refused.Why)
			return 1
		}
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
		// BB-D66: at once, never a wait. Step 1 has no request store and no notifier, so
		// there is nothing to wait for; the 30-second wait of §3.2 arrives with them.
		ev.Set, ev.Outcome = d.Set, "denied"
		ev.Reason = "writes need approval, which this version cannot ask for"
		say("`gh " + d.Path + "` is a write (the " + d.Set + " set), and no write runs from a jail yet: " +
			"a write acts on GitHub with the host user's login, so it needs that user's approval on the " +
			"host, and that approval step is not built yet. Nothing ran and nothing is waiting (exit " +
			"77). To make the change, ask the user to run it on the host; `yolo audit --set read-write` " +
			"there shows the exact command.")
		return finish(ExitNoPerm)
	}

	// A standing read.
	ev.Set = d.Set
	if b.runner == nil {
		ev.Outcome, ev.Reason = "unavailable", "no gh on the host"
		msg := "the host has no gh on its PATH; install the GitHub CLI on the host and run `gh auth login` there"
		if b.unavailable != "" {
			ev.Reason, msg = "host gh refused", b.unavailable
		}
		say(msg)
		return finish(ExitUnavailable)
	}
	if !b.runner.Tested {
		// §5.1 rule 6: outside the tested range no set applies, so even a read needs its own
		// Allow once — which is a request this version cannot file.
		ev.Outcome = "denied"
		ev.Reason = "host gh " + b.runner.Version + " is outside the tested range " + testedMinor + ".x"
		say("the host's gh is version " + b.runner.Version + ", outside the " + testedMinor +
			".x range this broker was measured against, so no set applies and every command needs " +
			"its own approval, which this version cannot ask for. Nothing ran and nothing is waiting " +
			"(exit 77). Ask the user to install gh " + testedMinor + ".x on the host (`yolo check` there " +
			"shows the version the broker found), or to run the command there.")
		return finish(ExitNoPerm)
	}
	if d.Path == "auth status" && !slices.Contains(d.Argv, "--help") {
		return finish(b.answerAuthStatus(d.Argv, &ev, stdout, stderr, say))
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
		say(noHostLogin)
		ev.Reason = "gh exited 4: no host login"
		code = ExitUnavailable
	}
	return finish(code)
}

// noHostLogin is what a call is told when the host gh has no login the broker can use.
const noHostLogin = "the host's gh has no usable GitHub login; run `gh auth status` on the host. " +
	"The broker never starts a login."

// answerAuthStatus is a jail's `gh auth status`, which the broker answers itself (BB-D65):
// who the jail is logged in as, through what, and which repositories it may use. gh's own
// answer describes the host's login, the path of the run dir's hosts.yml copy, the token's
// prefix and its scopes, none of which means anything in the jail or decides anything there:
// the broker's sets and scope do. The host gh is asked only for the active account, and only
// the login and the state of gh's check of it are used.
//
// The text keeps gh's own first lines (`github.com`, then `✓ Logged in to github.com account
// <login>`), so a script that greps for them still matches; `--json hosts` keeps gh's shape,
// with the token source naming the broker and no scopes. Exit 0 when logged in, as gh's; no
// usable host login is the broker's 69, as for every other call.
func (b *Broker) answerAuthStatus(argv []string, ev *brokeraudit.Event, stdout, stderr func([]byte),
	say func(string)) int {
	jsonFields := ""
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, "--json="); ok {
			jsonFields = v
		}
	}
	if jsonFields != "" {
		for _, f := range strings.Split(jsonFields, ",") {
			if f = strings.TrimSpace(f); f != "hosts" {
				// gh's own message for a field it does not have, before it does anything.
				stderr([]byte(fmt.Sprintf("Unknown JSON field: %q\nAvailable fields:\n  hosts\n", f)))
				ev.Outcome, ev.Reason = "refused", "auth status: unknown JSON field"
				return 1
			}
		}
	}
	acct, err := b.runner.ActiveAccount()
	ev.Outcome = "ran"
	if err != nil || acct.Login == "" || acct.State != "success" {
		ev.Reason = "auth status: no usable host login"
		say(noHostLogin)
		return ExitUnavailable
	}
	ev.Reason = "auth status: answered by the broker"
	login := termsafe.Visible(acct.Login)
	var out []byte
	if jsonFields != "" {
		type entry struct {
			State       string `json:"state"`
			Active      bool   `json:"active"`
			Host        string `json:"host"`
			Login       string `json:"login"`
			TokenSource string `json:"tokenSource"`
			GitProtocol string `json:"gitProtocol"`
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]any{"hosts": map[string][]entry{"github.com": {{State: "success",
			Active: true, Host: "github.com", Login: acct.Login, TokenSource: "github-broker (host)",
			GitProtocol: acct.GitProtocol}}}})
		out = buf.Bytes()
	} else {
		out = []byte("github.com\n" +
			"  ✓ Logged in to github.com account " + login + " through yolo's github-broker; the token " +
			"stays on the host\n" +
			"  - Repositories this jail can use: " + b.scope.describe() + "\n")
	}
	ev.BytesOut = int64(len(out))
	stdout(out)
	return 0
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
