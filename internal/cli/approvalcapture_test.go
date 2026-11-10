package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalfixtures"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerhook"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// approvalcapture_test.go builds the real-supported fixtures of internal/approvalfixtures from
// what yolo actually prints. Each scenario files its request into a real brokerstore (the
// source's own ApprovalStore, its own scope file and lease, a clock the test sets), runs the
// real `yolo approve` entry (approveMain, the function `registry["approve"]` calls with the
// host's stores), and records the request object `--json` printed, the hook environment
// brokerhook.Env builds, and, for a decision, the call and what it returned.
//
// With YOLO_UPDATE_APPROVAL_FIXTURES=1 it rewrites internal/approvalfixtures/fixtures/<id>.json:
//
//	YOLO_UPDATE_APPROVAL_FIXTURES=1 go test -count=1 -run TestApprovalCapture ./internal/cli/
//
// Without it, it regenerates in memory and fails when a committed fixture's captured part
// differs from what yolo prints now.

const (
	acUpdateEnv = "YOLO_UPDATE_APPROVAL_FIXTURES"
	acUpdateCmd = acUpdateEnv + "=1 go test -count=1 -run TestApprovalCapture ./internal/cli/"
	acStart     = "0123456789abcdef0123456789abcdef"
	acWorkspace = "/home/dev/code/app"
	acGitRemote = "git@example.test:example-org/app.git"
	acGHRepo    = "example-org/app"
)

var acBase = time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)

// acResult is what one scenario captured.
type acResult struct {
	capturedWith string
	request      json.RawMessage
	hookEnv      map[string]string
	decision     *approvalfixtures.Decision
}

// acScenario is one fixture: its hand fields and how to capture the rest. capture is nil for
// a fixture with nothing to capture (an operation that files no request).
type acScenario struct {
	id, title, family, note, prompt, state, story string
	source                                        string // gitbroker.Source or ghbroker.Source
	capture                                       func(t *testing.T, w *acWorld) acResult
}

// acWorld is one launch of one source: a temp HOME, the scope file and lease the broker holds,
// the source's real approval store on a clock the test sets.
type acWorld struct {
	t         *testing.T
	home      string
	store     *brokerstore.Store
	sf        brokerscope.File
	clock     atomic.Int64 // unix nanoseconds
	authority *ghbroker.Authority
}

func newACWorld(t *testing.T, source string) *acWorld {
	t.Helper()
	w := &acWorld{t: t, home: t.TempDir()}
	t.Setenv("HOME", w.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(w.home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	w.clock.Store(acBase.UnixNano())
	w.sf = brokerscope.File{Source: source, LaunchID: "capture-" + source, PID: os.Getpid(), Container: "capture-" + source + "-jail",
		Workspace: acWorkspace, Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "*"}}}
	switch source {
	case gitbroker.Source:
		w.sf.Form, w.sf.Repos = brokerscope.FormRemoteURL, []string{acGitRemote}
	case ghbroker.Source:
		w.sf.Repos = []string{acGHRepo}
	default:
		t.Fatalf("no capture world for source %q", source)
	}
	path, err := brokerscope.Write(w.sf)
	if err != nil {
		t.Fatal(err)
	}
	if source == ghbroker.Source {
		a, err := ghbroker.BindAuthority(path, w.sf, acStart)
		if err != nil {
			t.Fatal(err)
		}
		w.authority = a
		t.Cleanup(func() { a.Close() })
		w.store = ghbroker.ApprovalStore()
	} else {
		_, lease, err := brokercore.BindLease(gitbroker.Source, path, w.sf, acStart)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { brokercore.CloseLease(gitbroker.Source, lease) })
		w.store = gitbroker.ApprovalStore()
	}
	w.store.PublishRecords = false // the jail-readable copy would write under the sanitized workspace
	w.store.Now = func() time.Time { return time.Unix(0, w.clock.Load()).UTC() }
	return w
}

func (w *acWorld) setTime(d time.Duration) { w.clock.Store(acBase.Add(d).UnixNano()) }

// acPushRef is one ref command of a push.
type acPushRef struct{ old, new, ref string }

// gitPush files a git push request. gitbroker's pushRequest and pushAbout are unexported, so
// this mirrors them exactly (internal/gitbroker/approvalauthority.go pushRequest and pushArgv,
// approval.go askForPush and pushAbout): argv is `git-receive-pack`, one "<old> <new> <ref>"
// per ref and one "push-option=<value>" per option; the repository is gitproto.Identity of the
// remote; the set is "push"; the policy is Allow once for 15 minutes; the stdin digest is that
// of nothing; NeedsApproval and About.Refs are the refs nothing else covered; the headline
// counts the refs and names the repository.
func (w *acWorld) gitPush(refs []acPushRef, options, needs []string) brokerstore.Request {
	w.t.Helper()
	repo, err := gitproto.Identity(acGitRemote)
	if err != nil {
		w.t.Fatal(err)
	}
	argv := []string{"git-receive-pack"}
	for _, r := range refs {
		argv = append(argv, r.old+" "+r.new+" "+r.ref)
	}
	for _, o := range options {
		argv = append(argv, "push-option="+o)
	}
	empty := sha256.Sum256(nil)
	count := "1 ref"
	if len(needs) != 1 {
		count = fmt.Sprintf("%d refs", len(needs))
	}
	q, err := w.store.File(brokerstore.Request{Jail: paths.JailShortHash(w.sf.Container), Start: acStart, Workspace: w.sf.Workspace,
		LaunchID: w.sf.LaunchID, Argv: argv, Repo: repo, Set: "push", StdinDigest: hex.EncodeToString(empty[:]),
		Policy:        brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 24 * time.Hour, Uses: 1},
		NeedsApproval: needs,
		About: brokerstore.About{Action: brokerstore.ActionPush, Target: repo, Headline: "push " + count + " to " + repo,
			Refs: append([]string(nil), needs...)}})
	if err != nil {
		w.t.Fatal(err)
	}
	return q
}

// ghWrite files a GitHub write request the way ghbroker's window.go does: the broker's own
// classification (Authority.Classify) gives the canonical argv, set, repositories and policy,
// and ghbroker.RequestAbout gives the About it files (the same aboutOf window.go calls).
func (w *acWorld) ghWrite(argv ...string) brokerstore.Request {
	w.t.Helper()
	d, policy, err := w.authority.Classify(argv, acGHRepo)
	if err != nil {
		w.t.Fatalf("classify %v: %v", argv, err)
	}
	if d.Outcome != ghbroker.OutcomeWindowed {
		w.t.Fatalf("%v is not a windowed write: %+v", argv, d)
	}
	sum := sha256.Sum256(nil)
	q, err := w.store.File(brokerstore.Request{Jail: paths.JailShortHash(w.sf.Container), Start: acStart, Workspace: w.sf.Workspace,
		LaunchID: w.sf.LaunchID, Argv: d.Argv, Repo: strings.Join(d.Repos, ","), Set: d.Set, Policy: policy,
		StdinDigest: hex.EncodeToString(sum[:]), About: ghbroker.RequestAbout(d.Argv, d.Repos)})
	if err != nil {
		w.t.Fatal(err)
	}
	return q
}

// approve runs `yolo approve args...` over the world's store, as the CLI does.
func (w *acWorld) approve(args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	rc := approveMain(context.Background(), args, false, w.store, &out, &errs)
	return rc, out.String(), errs.String()
}

// listed is request id as `yolo approve --json` prints it, verbatim.
func (w *acWorld) listed(id string) json.RawMessage {
	w.t.Helper()
	rc, out, errs := w.approve("--json")
	if rc != 0 {
		w.t.Fatalf("approve --json: rc %d %s", rc, errs)
	}
	var doc struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		w.t.Fatal(err)
	}
	for _, r := range doc.Requests {
		var h struct{ ID string }
		if json.Unmarshal(r, &h) == nil && h.ID == id {
			return r
		}
	}
	w.t.Fatalf("request %s not in `approve --json`: %s", id, out)
	return nil
}

// watched runs `yolo approve --watch --json`, calls do once its opening state is out, and
// returns the request object of the first event after it, which must be of kind want.
func (w *acWorld) watched(want string, do func()) json.RawMessage {
	w.t.Helper()
	fastWatch(w.t)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errs syncBuffer
	done := make(chan int, 1)
	go func() { done <- approveMain(ctx, []string{"--watch", "--json"}, false, w.store, &out, &errs) }()
	waitLines(w.t, &out, 1)
	do()
	lines := waitLines(w.t, &out, 2)
	cancel()
	<-done
	var ev struct {
		Event   string          `json:"event"`
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil || ev.Event != want || len(ev.Request) == 0 {
		w.t.Fatalf("want a %s event with its request, got %v %q", want, err, lines[1])
	}
	return ev.Request
}

// hookEnv is the YOLO_REQUEST_* environment the on_request hook gets for q as filed.
func (w *acWorld) hookEnv(q brokerstore.Request) map[string]string {
	env := map[string]string{}
	for _, kv := range brokerhook.Env(w.store, q, "") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "YOLO_REQUEST_") {
			env[k] = v
		}
	}
	return env
}

// decide runs `yolo approve args...` and records the call.
func (w *acWorld) decide(args ...string) *approvalfixtures.Decision {
	rc, _, errs := w.approve(args...)
	return &approvalfixtures.Decision{Argv: append([]string{"approve"}, args...), Exit: rc, Stderr: errs}
}

const acZero = "0000000000000000000000000000000000000000"

var acIDs = []string{"1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222",
	"3333333333333333333333333333333333333333", "4444444444444444444444444444444444444444"}

func acScenarios() []acScenario {
	git, gh := gitbroker.Source, ghbroker.Source
	pushTwo := func(w *acWorld) brokerstore.Request {
		return w.gitPush([]acPushRef{{acIDs[0], acIDs[1], "refs/heads/main"}, {acIDs[0], acIDs[2], "refs/heads/agent/fix-retry"}},
			nil, []string{"refs/heads/main"})
	}
	comment := func(w *acWorld) brokerstore.Request {
		return w.ghWrite("pr", "comment", "42", "--repo", acGHRepo, "--body", "Looks good to me.")
	}
	const gitNote = "A push no grant or working-set entry covers whole files one request for the whole push: internal/gitbroker/approval.go askForPush, which builds the About with pushAbout and the request with pushRequest (approvalauthority.go)."
	const ghNote = "A GitHub write under the default windowed read-write set files a request: internal/ghbroker/window.go (the Request and its About, aboutOf) through Authority.Classify (authority.go)."
	return []acScenario{
		{id: "git-push-working-set-miss", title: "Git: push to a branch outside the working set", family: "git", note: gitNote, prompt: "request", state: "pending", source: git,
			story: "An agent pushes a fix to an existing branch of example-org/app that its working set does not cover, in the same push as a branch that is covered; only the uncovered ref needs approval.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := pushTwo(w)
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "git-push-new-branch", title: "Git: push that creates a new branch", family: "git", note: gitNote, prompt: "request", state: "pending", source: git,
			story: "An agent pushes a branch of example-org/app that does not exist on the remote yet, and no create-branch grant covers it.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := w.gitPush([]acPushRef{{acZero, acIDs[1], "refs/heads/agent/add-retry-queue"}}, nil, []string{"refs/heads/agent/add-retry-queue"})
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "git-push-force", title: "Git: force push to an existing branch", family: "git", prompt: "request", state: "pending", source: git,
			note:  gitNote + " FINDING: the request does not say a push is forced. A push is the ref commands the client sent, each '<old> <new> <ref>' (gitproto.Command, gitbroker pushArgv); the broker never checks that <new> descends from <old>, so a force push and a fast-forward are the same request, and the headline and refs read the same.",
			story: "An agent rewrites the history of a branch of example-org/app and force-pushes it. The request shows the old and new ids and nothing that marks the update as non-fast-forward.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := w.gitPush([]acPushRef{{acIDs[0], acIDs[3], "refs/heads/agent/fix-retry"}}, nil, []string{"refs/heads/agent/fix-retry"})
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "git-push-long-names", title: "Git: long branch names and a push option", family: "git", note: gitNote + " The headline counts refs and never names them (pushAbout); the long names reach a notifier only through refs and description.", prompt: "request", state: "pending", source: git,
			story: "An agent pushes two branches with very long names and a push option the server acts on, to exercise how a notifier truncates.",
			capture: func(t *testing.T, w *acWorld) acResult {
				a := "refs/heads/agent/" + strings.Repeat("investigate-flaky-integration-test-in-the-retry-queue-", 3) + "part-1"
				b := "refs/heads/agent/" + strings.Repeat("rework-the-ingestion-pipeline-backpressure-handling-", 3) + "part-2"
				q := w.gitPush([]acPushRef{{acZero, acIDs[1], a}, {acIDs[0], acIDs[2], b}},
					[]string{"ci.variable=DEPLOY_ENVIRONMENT_OVERRIDE_FOR_THIS_PUSH_ONLY=staging-eu-west-1-canary"}, []string{a, b})
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "github-pr-comment", title: "GitHub: comment on a PR", family: "github", note: ghNote + " The comment body is in the canonical argv (--body), so it reaches `command` whole; About carries no title for a comment, so the headline and description name only the PR.", prompt: "request", state: "pending", source: gh,
			story: "An agent posts a long review comment on a pull request of example-org/app.",
			capture: func(t *testing.T, w *acWorld) acResult {
				body := "Thanks for the update. I re-ran the ingestion benchmarks against this branch and the p95 latency is unchanged, but the retry queue now drains in about half the time. Two follow-ups before merging: the backoff constant should come from config rather than a literal, and the new test should assert the dead-letter path as well as the happy path. I have not touched either; tell me which you want me to take. " + strings.Repeat("Detail line for the benchmark table. ", 6)
				q := w.ghWrite("pr", "comment", "42", "--repo", acGHRepo, "--body", body)
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "github-workflow-run", title: "GitHub: run a workflow on another ref", family: "github", note: ghNote + " `gh workflow run` on the default branch runs unasked under a runs grant; with none, or on another ref, it files this request, with the workflow file as title and the ref in refs (aboutOf's dispatch branch).", prompt: "request", state: "pending", source: gh,
			story: "An agent asks to run the deploy workflow of example-org/app on an experiment branch, which no runs grant covers.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := w.ghWrite("workflow", "run", "deploy.yml", "--repo", acGHRepo, "--ref", "feature/experiment")
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "github-approve-run", title: "GitHub: approve a workflow run", family: "github", note: ghNote + " FINDING: the request is `gh api -X POST repos/.../actions/runs/<id>/approve` and does not record whose run it is: a fork's run (which always asks) and a run of the repository itself that no approve-run grant covers file the same object, because the broker reads the run's head repository only to decide, never to describe (access.go approveRunOf, readRun).", prompt: "request", state: "pending", source: gh,
			story: "A contributor's pull request waits for a workflow run to be approved, and the agent asks to approve it.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := w.ghWrite("api", "-X", "POST", "repos/"+acGHRepo+"/actions/runs/9876543210/approve")
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "circleci-run-list", title: "CircleCI: list a project's runs (no prompt)", family: "circleci", prompt: "none", state: "no-prompt", source: "",
			note:  "Policy files no request for it, so there is nothing to capture. The circleci classifier (internal/circleciclassify/classifier.go Classify) answers a project-scoped `run list` with Reads only and no Writes; callbroker's serveCLI (internal/callbroker/handler.go) files a request only for a call with Writes, and runs a read under the read grant or refuses it (read grant missing, classifier refused) without ever asking. The gallery must not invent a prompt.",
			story: "An agent lists the recent runs of an approved example-org/app project; the call runs under the workspace's read grant and the human is never asked."},
		{id: "state-denied", title: "Denied with a note", family: "cross-cutting", note: gitNote + " The denial is `yolo approve <id> --deny --note <text>`; the request object is the request-decided event of `--watch --json`.", prompt: "request", state: "denied", source: git,
			story: "The human denies a push and tells the agent why; the request ends denied.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := pushTwo(w)
				var d *approvalfixtures.Decision
				req := w.watched(brokerstore.EventRequestDecided, func() {
					w.setTime(2 * time.Minute)
					d = w.decide(q.ID, "--deny", "--note", "Do not push to main from an agent; open a branch instead.")
				})
				return acResult{"yolo approve --watch --json (request-decided)", req, w.hookEnv(q), d}
			}},
		{id: "state-expired", title: "Expired unanswered", family: "cross-cutting", note: gitNote + " A request nobody answers expires after an hour (brokerstore.PendingLife); the request object is the request-expired event of `--watch --json`, with the store's clock moved past it.", prompt: "request", state: "expired", source: git,
			story: "Nobody answers a push request for an hour, and it expires.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := pushTwo(w)
				req := w.watched(brokerstore.EventRequestExpired, func() { w.setTime(brokerstore.PendingLife + time.Minute) })
				return acResult{"yolo approve --watch --json (request-expired)", req, w.hookEnv(q), nil}
			}},
		{id: "state-decided-elsewhere", title: "Answered by another front end first", family: "cross-cutting", note: ghNote + " The request object is the allowed request after the first answer (request-decided event); the recorded call is the second `yolo approve <id>`, which exits 3.", prompt: "request", state: "decided-elsewhere", source: gh,
			story: "The human allows a request from a terminal while its notification is still on screen; a click on the notification then finds it already decided.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := comment(w)
				req := w.watched(brokerstore.EventRequestDecided, func() {
					w.setTime(2 * time.Minute)
					if rc, _, errs := w.approve(q.ID); rc != 0 {
						t.Fatalf("first answer: rc %d %s", rc, errs)
					}
				})
				return acResult{"yolo approve --watch --json (request-decided), then a second yolo approve <id>", req, w.hookEnv(q), w.decide(q.ID)}
			}},
		{id: "state-stale", title: "Pending but no longer confirmable", family: "cross-cutting", note: ghNote + " The launch's scope file is gone, so the host can no longer confirm the request: `yolo approve <id>` revalidates against it and exits 6 (approveExitNotConfirmed).", prompt: "request", state: "stale", source: gh,
			story: "The jail that filed a request is gone, so its notification can no longer be answered with Allow; only a denial works.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := comment(w)
				req := w.listed(q.ID)
				if err := os.Remove(paths.BrokerScopeFile(ghbroker.Source, w.sf.LaunchID)); err != nil {
					t.Fatal(err)
				}
				return acResult{"yolo approve --json, then the launch's scope file removed", req, w.hookEnv(q), w.decide(q.ID)}
			}},
		{id: "state-hook-failure", title: "The on_request hook failed", family: "cross-cutting", note: gitNote + " The request is filed under a real brokerhook.Runner whose hook exits 1; the runner records hook_failure and hook_output on the request (brokerhook.go noteFailure), and `--json` prints them.", prompt: "request", state: "pending", source: git,
			story: "The user's notification hook cannot reach a notification daemon, so the request waits and the listing says the user may not have been notified.",
			capture: func(t *testing.T, w *acWorld) acResult {
				r := &brokerhook.Runner{Store: w.store, Start: acStart, Dir: t.TempDir(), Interval: 10 * time.Millisecond,
					Command: `echo "notify-send: Cannot autolaunch D-Bus without X11 \$DISPLAY" >&2; exit 1`}
				ctx, cancel := context.WithCancel(context.Background())
				finished := make(chan struct{})
				go func() { r.Run(ctx); close(finished) }()
				defer func() { cancel(); <-finished }()
				q := pushTwo(w)
				deadline := time.Now().Add(30 * time.Second)
				for {
					rs, _, err := w.store.List()
					if err != nil {
						t.Fatal(err)
					}
					failed := false
					for _, x := range rs {
						failed = failed || (x.ID == q.ID && x.HookFailure != "")
					}
					if failed {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("the hook's failure was never recorded")
					}
					time.Sleep(10 * time.Millisecond)
				}
				return acResult{"yolo approve --json", w.listed(q.ID), w.hookEnv(q), nil}
			}},
		{id: "state-allowed-once", title: "Allowed once", family: "cross-cutting", note: ghNote + " The answer is `yolo approve <id>`; the request object is the request-decided event of `--watch --json`.", prompt: "request", state: "allowed", source: gh,
			story: "The human allows a PR comment once; the agent's retry of the same command then runs.",
			capture: func(t *testing.T, w *acWorld) acResult {
				q := comment(w)
				var d *approvalfixtures.Decision
				req := w.watched(brokerstore.EventRequestDecided, func() {
					w.setTime(2 * time.Minute)
					d = w.decide(q.ID)
				})
				return acResult{"yolo approve --watch --json (request-decided)", req, w.hookEnv(q), d}
			}},
	}
}

var acIDPattern = regexp.MustCompile(`[rgw]-[0-9a-f]{32}`)

// acNormalize replaces every request, grant and working-set id in the captured parts with a
// stable fake of the same shape derived from the fixture id, and the temp HOME with /home/dev.
func acNormalize(id, home string, r acResult) acResult {
	fakes := map[string]string{}
	var order []string
	note := func(s string) {
		for _, m := range acIDPattern.FindAllString(s, -1) {
			if _, ok := fakes[m]; !ok {
				sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", id, len(fakes))))
				fakes[m] = m[:2] + hex.EncodeToString(sum[:])[:32]
				order = append(order, m)
			}
		}
	}
	note(string(r.request))
	keys := make([]string, 0, len(r.hookEnv))
	for k := range r.hookEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		note(r.hookEnv[k])
	}
	if r.decision != nil {
		note(strings.Join(r.decision.Argv, "\x00") + r.decision.Stderr)
	}
	sub := func(s string) string {
		if home != "" {
			s = strings.ReplaceAll(s, home, "/home/dev")
		}
		return acIDPattern.ReplaceAllStringFunc(s, func(m string) string { return fakes[m] })
	}
	out := acResult{capturedWith: r.capturedWith, request: json.RawMessage(sub(string(r.request)))}
	out.hookEnv = map[string]string{}
	for k, v := range r.hookEnv {
		out.hookEnv[k] = sub(v)
	}
	if r.decision != nil {
		d := &approvalfixtures.Decision{Exit: r.decision.Exit, Stderr: sub(r.decision.Stderr)}
		for _, a := range r.decision.Argv {
			d.Argv = append(d.Argv, sub(a))
		}
		out.decision = d
	}
	_ = order
	return out
}

func acCompact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func acFixturesDir(t *testing.T) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the source directory")
	}
	return filepath.Join(filepath.Dir(file), "..", "approvalfixtures", "fixtures")
}

func acEvidence() []approvalfixtures.Evidence {
	return []approvalfixtures.Evidence{
		{Platform: "browser", Kind: "preview", Renderer: "owner baseline layout (HTML preview of the on_request script's toast)", File: "index.html", Tested: "visual"},
		{Platform: "linux", Kind: "missing", Renderer: "owner baseline: on_request script via notify-send to swaync", Note: "Not yet captured."},
		{Platform: "macos", Kind: "missing", Renderer: "terminal-notifier 3", Note: "No Mac capture runner yet (mac-capture-runner.md)."},
	}
}

func TestApprovalCapture(t *testing.T) {
	update := os.Getenv(acUpdateEnv) == "1"
	committed, err := approvalfixtures.Load()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]approvalfixtures.Fixture{}
	for _, f := range committed {
		byID[f.ID] = f
	}
	dir := acFixturesDir(t)
	for _, sc := range acScenarios() {
		t.Run(sc.id, func(t *testing.T) {
			fx := approvalfixtures.Fixture{Schema: approvalfixtures.Schema, ID: sc.id, Title: sc.title, Family: sc.family,
				Support: "real", SupportNote: sc.note, Prompt: sc.prompt, State: sc.state, Story: sc.story, Evidence: acEvidence()}
			old, have := byID[sc.id]
			if sc.capture != nil {
				w := newACWorld(t, sc.source)
				res := acNormalize(sc.id, w.home, sc.capture(t, w))
				fx.Origin, fx.CapturedWith, fx.Request, fx.HookEnv, fx.Decision = "captured", res.capturedWith, res.request, res.hookEnv, res.decision
			}
			if have {
				if !update {
					// Drift detection: the captured part must be what yolo prints now.
					if sc.capture != nil {
						if old.CapturedWith != fx.CapturedWith || acCompact(t, old.Request) != acCompact(t, fx.Request) ||
							!reflect.DeepEqual(old.HookEnv, fx.HookEnv) || !reflect.DeepEqual(old.Decision, fx.Decision) {
							t.Fatalf("fixtures/%s.json differs from what yolo prints now.\nupdate it with:\n  %s\ncommitted request: %s\nregenerated:       %s",
								sc.id, acUpdateCmd, acCompact(t, old.Request), acCompact(t, fx.Request))
						}
					}
					return
				}
				// Keep the hand fields of an existing file; regenerate only the captured ones.
				old.CapturedWith, old.Request, old.HookEnv, old.Decision = fx.CapturedWith, fx.Request, fx.HookEnv, fx.Decision
				fx = old
			} else if !update {
				t.Fatalf("fixtures/%s.json is not committed.\ncreate it with:\n  %s", sc.id, acUpdateCmd)
			}
			if err := fx.Validate(); err != nil {
				t.Fatalf("%s: %v", sc.id, err)
			}
			b, err := approvalfixtures.Encode(fx)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, sc.id+".json"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
