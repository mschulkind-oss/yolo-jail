package ghbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
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
// The read path is standing within host scope; an in-scope write files a pending request and
// waits for an exact host Allow-once decision before required audit and dispatch.

// Source is the GitHub broker's source name: the approval record's scope key, the store's
// directory and the audit log's `service`. It is also what packs/github's manifest declares
// as `brokered.source`, which is the spelling core reads.
const Source = "github"

// ReadCapability is what packs/github's manifest declares as `brokered.read_capability`: the
// capability `yolo allow github read` writes, which a refusal for want of a read grant names to
// a TOML user (config.BrokerNoReadRefusal). TestReadCapabilityIsTheManifests pins the two
// spellings together.
const ReadCapability = "read"

// SourceHost is the forge's hostname: what packs/github's manifest declares as
// `brokered.remote_host`, and what a wildcard scope note names (`*/*` on this host is "every
// repository on github.com"). The broker talks to no other host.
const SourceHost = "github.com"

// Request is the forwarder's frame, and the whole of what a jail can say: an argv, the
// repository it resolved, its stdin, and an agent name it reports. None of it is a
// decision, a grant, a jail identity or a duration (§9.3).
type Request struct {
	writeDeadline func(time.Time) // host connection sink bound, never decoded from a jail frame
	Invalid       string
	Operation     string
	RequestID     string
	Argv          []string
	Repo          string
	Stdin         []byte
	// StdinSent says the forwarder sent stdin, which is audited by digest even when the
	// command does not read it.
	StdinSent bool
	Agent     string
}

// Broker answers one jail's calls.
type Broker struct {
	authority *Authority
	delivery  RequiredDelivery
	execute   func(context.Context, []string, []byte, func([]byte), func([]byte)) RunResult
	waitLimit time.Duration
	shutdown  context.Context
	runner    *Runner // nil when the host has no gh, or one the broker will not run
	// unavailable is what a call is told when runner is nil; "" means the host has no gh.
	unavailable string
	scope       Scope
	workspace   string
	startID     string
	store       *brokerstore.Store
	lifetimes   brokercore.Lifetimes
	audit       *brokeraudit.Log
	log         io.Writer
	now         func() time.Time
}

// Main is `yolo internal daemon github-broker`: the core's daemon (brokercore.Daemon) around
// this source's broker.
func Main(argv []string) int {
	var b *Broker
	return brokercore.Daemon{
		Name:           "github-broker",
		Source:         Source,
		SelfCheck:      SelfCheck,
		SelfCheckUsage: "report the host gh and exit (used by `yolo check`)",
		NewHandler: func(ctx context.Context, scopeFile string, sf brokerscope.File, spawnCwd string, stderr io.Writer) (brokercore.Launch, int) {
			var cleanup func()
			b, cleanup = newBroker(sf, spawnCwd, stderr)
			l := brokercore.Launch{Handle: b.Handle, Store: b.store, Start: b.startID, Log: b.log, Cleanup: cleanup}
			var err error
			b.authority, err = BindAuthority(scopeFile, sf, b.startID)
			if err != nil {
				fmt.Fprintln(stderr, "github-broker: refusing host authority:", err, "; relaunch the jail")
				return l, 1
			}
			b.store.Revalidate = b.authority.Revalidate
			b.store.Audit = StoreAudit(b.audit)
			b.shutdown = ctx
			return l, 0
		},
		// TOLD TO STOP, END gh FIRST: a gh in its own process group never saw the SIGTERM, so
		// the broker kills its gh children itself, the calls return inside the grace, and the
		// deferred cleanup, which removes the copy of the host's hosts.yml, runs.
		OnStop: func() {
			if b != nil && b.runner != nil {
				b.runner.Shutdown()
			}
		},
	}.Main(argv)
}

// startOnRequest runs the user's on_request for each request this broker lifetime files
// (BB-D75), when one is set (brokercore.StartOnRequest).
func (b *Broker) startOnRequest(ctx context.Context, command string) <-chan struct{} {
	return brokercore.StartOnRequest(ctx, "github-broker", b.store, b.startID, command, b.log)
}

// newBroker builds a broker for one launch's scope file, logging what it starts with.
// spawnCwd is the directory the launch spawned it from, the workspace.
//
// THE LOG NAMES NO WORKSPACE, NO REPOSITORY AND NO HOST PATH. It is the daemon's stderr,
// which the launch appends to one file per loophole name under logs/, shared by every jail
// on the machine, and logs/ is the directory a `mounts` entry commonly exposes to a jail. So
// one jail could read every other workspace's path and approved private repositories there.
// The launch already discloses the scope, with each repository's sources, on its own terminal
// (writeScopeFiles), the jail's
// stderr names a refused gh, and the audit log, which yolo mounts into no jail, holds the rest.
func newBroker(sf brokerscope.File, spawnCwd string, log io.Writer) (*Broker, func()) {
	sweepRunDirs()
	runDir := newRunDir()
	startID, startErr := brokerstore.NewStartID()
	store := &brokerstore.Store{Dir: paths.BrokerSourceDir(Source), Source: Source}
	b := &Broker{
		// The start-time copy, used only by a broker no host authority is bound to; a bound
		// broker classifies against the file as it re-reads it (ServeContext).
		scope:     scopeOfFile(sf),
		workspace: sf.Workspace,
		startID:   startID,
		store:     store,
		audit: brokeraudit.Open(paths.BrokerAuditLog(), func(msg string) {
			fmt.Fprintln(log, "github-broker:", msg)
		}),
		// Production requests are surfaced in the host broker log; a configured on_request hook
		// may notify the user, while manual yolo approve remains the answer path without one.
		delivery: &logRequestDelivery{log: log},
		log:      log,
		now:      time.Now,
	}
	cleanup := func() {
		_ = b.authority.Close()
		b.endLifetimes()
		_ = os.RemoveAll(runDir)
	}
	if startErr != nil {
		b.unavailable = "could not create a fresh broker lifetime identity"
		fmt.Fprintln(log, "github-broker: refusing to start without a fresh broker lifetime identity:", startErr)
		return b, cleanup
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
		return b, cleanup
	}
	b.runner = r
	fmt.Fprintln(log, "github-broker:", r.Summary())
	return b, func() {
		r.Close()
		cleanup()
	}
}

// placementRefusal is the loophole placement rule applied to the host gh
// (brokercore.PlacementRefusal).
func placementRefusal(workspaces ...string) func(gh string) string {
	return brokercore.PlacementRefusal("gh", workspaces...)
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
	b.trackJail(b.holder(s.JailID))
	req := requestFrom(s)
	req.writeDeadline = func(deadline time.Time) { _ = s.Conn().SetWriteDeadline(deadline) }
	ctx := b.shutdown
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// One request per connection. Any EOF or extra client bytes cancel its wait.
	done := make(chan struct{})
	go func() { defer close(done); var one [1]byte; _, _ = s.Conn().Read(one[:]); cancel() }()
	code := b.ServeContext(ctx, req, s.JailID, s.StdoutBytes, func(p []byte) { s.Stderr(string(p)) })
	_ = s.Conn().SetReadDeadline(time.Now())
	<-done
	s.Exit(code)
}

func (b *Broker) trackJail(jail string) { b.lifetimes.Track(jail) }

// holder is the request holder for a connection whose host-asserted jail identity is crossing
// (docs/design/github-write-approval.md GWA-D1). The holder the request store, Authority.Revalidate
// and the standing check compare is paths.JailShortHash of the launch's container, taken from
// the scope file this broker bound; the crossing identity is the BASE NAME of the dir the
// launch published its endpoint in (svcendpoint's crossingIdentity), which is never that hash.
// So the crossing identity only GATES the holder: when it names a dir this launch publishes
// in — its container dir or one of its macos-user/`yolo host` session dirs — the holder is
// this launch's; any other value is returned unchanged, and since it is not this launch's
// holder, a connection through another launch's endpoint files, waits on and spends nothing
// for this one. The audit line keeps the crossing identity as its attribution.
func (b *Broker) holder(crossing string) string {
	if b.authority == nil {
		return crossing
	}
	return launchHolder(b.authority.scope.Container, crossing)
}

// launchHolder maps crossing to paths.JailShortHash(container) when it is one of the endpoint
// dir names a launch of container publishes under, and returns it unchanged otherwise.
func launchHolder(container, crossing string) string {
	if container == "" {
		return crossing
	}
	hash := paths.JailShortHash(container)
	dir := paths.HostServicesDirName(hash)
	if crossing == dir || (strings.HasPrefix(crossing, paths.HostServicesSessionPrefix(container)) && paths.IsHostServicesSessionDirName(crossing)) {
		return hash
	}
	return crossing
}

// endLifetimes refuses every request still pending from a jail served by this broker process;
// it ends no grant (brokercore.Lifetimes.EndAll).
func (b *Broker) endLifetimes() {
	b.lifetimes.EndAll(brokercore.LifetimeOwner{Name: "github-broker", Start: b.startID, Store: b.store, Log: b.log})
}

// requestFrom refuses malformed fields instead of dropping bytes or arguments.
func requestFrom(s *hostservice.Session) Request {
	var r Request
	str := func(key string, dst *string) {
		if v, has := s.Get(key); has {
			var ok bool
			*dst, ok = v.(string)
			if !ok {
				r.Invalid = "malformed " + key
			}
		}
	}
	str("operation", &r.Operation)
	str("request", &r.RequestID)
	str("repo", &r.Repo)
	str("agent", &r.Agent)
	if v, has := s.Get("argv"); has {
		list, ok := v.([]any)
		if !ok {
			r.Invalid = "malformed argv"
		} else {
			for _, v := range list {
				a, ok := v.(string)
				if !ok {
					r.Invalid = "malformed argv"
					break
				}
				r.Argv = append(r.Argv, a)
			}
		}
	}
	if v, has := s.Get("stdin"); has {
		str, ok := v.(string)
		if !ok {
			r.Invalid = "malformed stdin"
		} else {
			data, err := base64.StdEncoding.DecodeString(str)
			if err != nil {
				r.Invalid = "malformed stdin"
			} else {
				r.Stdin, r.StdinSent = data, true
			}
		}
	}
	return r
}

// Serve classifies and, for a standing read, runs one call, returning its exit code. It
// appends the audit line whatever happened (BB-P7).
func (b *Broker) Serve(req Request, jail string, stdout, stderr func([]byte)) int {
	return b.ServeContext(context.Background(), req, jail, stdout, stderr)
}

// ServeContext is the same production handler with a cancellable caller lifetime.
func (b *Broker) ServeContext(ctx context.Context, req Request, jail string, stdout, stderr func([]byte)) int {
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
		// A canceled host command may have partially completed. Record that terminal state
		// without implying it is safe to retry, using a bounded context for this audit only.
		if ctx.Err() != nil && ev.Outcome == "ran" && ev.Request != "" && ev.Grant != "" {
			ev.Outcome = "canceled"
			ev.Reason = "caller canceled while the host command was running; inspect its effect before any retry"
		}
		auditCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		b.audit.AppendContext(auditCtx, ev)
		return code
	}

	if req.Invalid != "" {
		ev.Set = "refused"
		ev.Outcome = "refused"
		ev.Reason = req.Invalid
		say(req.Invalid + "; retry with a valid forwarder frame")
		return finish(ExitUsage)
	}
	if req.Operation != "" {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		ctx = bounded
		ev.Set = "status"
		ev.Outcome = "status"
		if req.Operation != "status" && req.Operation != "wait" {
			say("unknown operation; use `yolo gh status <request>`")
			return finish(ExitUsage)
		}
		return finish(b.requestStatus(ctx, req, b.holder(jail), stdout, stderr))
	}

	if len(req.Stdin) > StdinCap {
		ev.Set, ev.Outcome, ev.Reason = "refused", "refused", "stdin over the 1 MiB cap"
		say(fmt.Sprintf("refused: standard input is %d bytes, over the %d-byte cap", len(req.Stdin), StdinCap))
		return finish(ExitUsage)
	}
	// A BOUND BROKER CLASSIFIES AGAINST THE SCOPE FILE AS IT READS IT NOW, not the copy it took
	// at start (docs/design/live-config-apply.md §7, LA-D10): a host `yolo config apply` that
	// rewrote the file's repositories widens or narrows this broker at this request.
	scope := b.scope
	if b.authority != nil {
		f, err := b.authority.live()
		if err != nil {
			ev.Set, ev.Outcome, ev.Reason = "refused", "refused", err.Error()
			say("current host authority refused: " + err.Error() + "; repair the host policy or relaunch the jail")
			return finish(ExitNoPerm)
		}
		scope = scopeOfFile(f)
	}
	d := Classify(req.Argv, req.Repo, scope)
	// READ IS THE FLOOR (docs/design/boundary-broker.md BB-D108): a selected pack's broker
	// grants nothing until a read grant covers the repository, so a command that would run, a
	// read or a write, is refused here, naming the grant. With no read grant at all, every
	// command is refused so, whatever else would refuse it: the workspace's gate approved no
	// scope (BB-D110), so the grant is the one next step that helps.
	if !brokerscope.HoldsRead(scope.grants) || d.Outcome == OutcomeStanding || d.Outcome == OutcomeWindowed {
		if repo, missing := scope.unread(d.Repos); missing {
			ev.Repo = strings.Join(d.Repos, ",")
			ev.Set, ev.Outcome, ev.Reason = "refused", "refused", "no read grant"
			say("refused: " + config.BrokerNoReadRefusal(Source, ReadCapability, repo, scope.live))
			return finish(ExitUsage)
		}
	}
	if b.authority != nil && (d.Outcome == OutcomeStanding || d.Outcome == OutcomeWindowed) {
		current, _, err := b.authority.Classify(req.Argv, req.Repo)
		if err != nil {
			ev.Set = "refused"
			ev.Outcome = "refused"
			ev.Reason = err.Error()
			say("current host authority refused: " + err.Error() + "; repair the host policy or relaunch the jail")
			return finish(ExitNoPerm)
		}
		// Authority.Classify rejects a standing read-write policy before reaching this point;
		// standing reads keep their original path and windowed writes stay request-gated.
		d = current
	}
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
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		ctx = bounded
		code, msg := b.window(ctx, req, b.holder(jail), d, stdout, stderr, &ev)
		if ev.Set == "" {
			// A write a user grant, working-set entry or timed grant covered names that set.
			ev.Set = d.Set
		}
		if ev.Outcome != "ran" {
			ev.Outcome = "refused"
			if code == ExitTempFail {
				ev.Outcome = "pending"
			}
		}
		ev.Reason = msg
		if msg != "" {
			say(msg)
		}
		return finish(code)
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
	if d.Path == "auth status" && !slices.Contains(d.Argv, "--help") {
		return finish(b.answerAuthStatus(d.Argv, scope, &ev, stdout, stderr, say))
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
func (b *Broker) answerAuthStatus(argv []string, scope Scope, ev *brokeraudit.Event, stdout, stderr func([]byte),
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
			"  - Repositories this jail can use: " + scope.describe() + "\n")
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
