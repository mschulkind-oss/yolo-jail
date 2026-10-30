package ghbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workingset.go is what covers a GitHub write before it asks (docs/design/boundary-broker.md
// §5.7, §5.8, §7, BB-D76, BB-D92, BB-D95, BB-D97), checked in the design's order, so an Allow
// once is spent only when nothing lasting covers the write:
//
//  1. a user grant: a `create-pr` or `create-issue` grant the live scope file holds on the
//     repository (or on `*`), while the user's own config still allows its kind, covers that
//     creation;
//  2. a working-set entry: a PR or issue in the workspace's working set covers every
//     non-merge write to it (comment, edit, review, ready, close, reopen, and `gh api` writes
//     under its pulls/{n} or issues/{n}), unless one host read of issues/{n} shows it merged,
//     which ends the entry and makes the write ask;
//  3. a timed grant: a human's `yolo approve <id> --for <duration>` on the request's kind and
//     resource, until its clock runs out;
//  4. Allow once, as built (window.go).
//
// A covered write is authorized and dispatched under the store's one transaction lock, the
// live host authority re-read inside it, and a required audit line written before gh runs,
// as the standing path and an Allow once are. What the agent creates under any of them joins
// the working set, by the number gh reports (createdNumber); a merge through the broker ends
// the PR's entry. The scope, the refused commands and the read floor are the classifier's and
// Authority.Classify's, and nothing here reaches past them (BB-P8).

// The audit sets a covered write falls in, as the git source names its own.
const (
	setCreatePR    = GrantCreatePR
	setCreateIssue = GrantCreateIssue
	setWorkingSet  = "working-set"
	setTimed       = "timed-grant"
)

// createGrantKind is the user grant kind that covers a creation target, "" for any other.
func createGrantKind(t writeTarget) string {
	switch t.Kind {
	case targetCreatePR:
		return GrantCreatePR
	case targetCreateIssue:
		return GrantCreateIssue
	}
	return ""
}

// userGrantCovers reports whether a user grant covers the creation target t now: the live
// scope file holds a grant of its kind on the repository or on `*`, and the host user's own
// config still allows that kind (BB-D96). Every failure to read either covers nothing.
func (b *Broker) userGrantCovers(t writeTarget) (string, bool) {
	kind := createGrantKind(t)
	if kind == "" || b.authority == nil {
		return "", false
	}
	f, err := b.authority.live()
	if err != nil {
		return "", false
	}
	held := false
	for _, g := range f.Grants {
		if g.Kind == kind && (g.Repo == brokerscope.AnyRepo || sameRepo(g.Repo, t.Repo)) {
			held = true
		}
	}
	if !held {
		return "", false
	}
	kinds, _, err := config.BrokeredGrantKindsUsingReadablePolicy(Source)
	if err != nil || !slices.Contains(kinds, kind) {
		return "", false
	}
	return kind, true
}

// issueState is what one host read of a repository's issues/{n} says about the number.
type issueState struct {
	PR     bool
	Merged bool
}

// readIssue is the one host read the broker makes of t's issues/{n} (§5.7, BB-D97): whether
// the number is a PR, and whether that PR is merged. A failed or unreadable answer is an
// error, and the write it was read for asks.
func (b *Broker) readIssue(ctx context.Context, t writeTarget) (issueState, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	argv := []string{"api", "repos/" + t.Repo + "/issues/" + strconv.Itoa(t.Number)}
	sink := func(p []byte) {
		if out.Len() < 1<<20 {
			out.Write(p)
		}
	}
	var res RunResult
	switch {
	case b.execute != nil:
		res = b.execute(ctx, argv, nil, sink, func([]byte) {})
	case b.runner != nil:
		res = b.runner.RunContext(ctx, argv, nil, sink, func([]byte) {})
	default:
		return issueState{}, errors.New("no host gh")
	}
	if res.Exit != 0 {
		return issueState{}, fmt.Errorf("gh api exited %d", res.Exit)
	}
	var obj struct {
		Number      int `json:"number"`
		PullRequest *struct {
			MergedAt *string `json:"merged_at"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(out.Bytes(), &obj); err != nil || obj.Number != t.Number {
		return issueState{}, errors.New("unreadable issue")
	}
	s := issueState{PR: obj.PullRequest != nil}
	s.Merged = s.PR && obj.PullRequest.MergedAt != nil && *obj.PullRequest.MergedAt != ""
	return s, nil
}

// coveredWrite runs the write when a user grant, a working-set entry or a timed grant covers
// it, and reports its exit code, a message for the agent, and true. False means nothing
// lasting covers it, and the write goes on to Allow once or a request.
func (b *Broker) coveredWrite(ctx context.Context, req Request, jail string, d Decision, t writeTarget,
	stdout, stderr func([]byte), ev *brokeraudit.Event) (int, string, bool) {
	if b.store == nil || b.authority == nil || b.audit == nil {
		return 0, "", false
	}
	if jail != paths.JailShortHash(b.authority.scope.Container) || b.startID != b.authority.start {
		return 0, "", false
	}
	// gate re-reads the live host authority inside the transaction: the scope, the read floor,
	// the refusals and the user's sets all still admit this exact command as a write of the
	// same kind on the same resource.
	gate := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, _, err := b.authority.Classify(req.Argv, req.Repo)
		if err != nil {
			return err
		}
		if current.Outcome != OutcomeWindowed || !slices.Equal(current.Argv, d.Argv) || targetOf(current.Argv, current.Repos) != t {
			return errors.New("current classification differs; run the command again")
		}
		return nil
	}
	code := ExitTempFail
	var captured bytes.Buffer
	run := func(set, by string) error {
		if err := b.audit.RequiredContext(ctx, brokeraudit.Event{Event: "covered-authorized", Service: Source, Jail: jail,
			Workspace: b.workspace, Argv: d.Argv, Repo: strings.Join(d.Repos, ","), Set: set, Grant: by,
			Outcome: "authorized-before-dispatch"}); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		ev.Set, ev.Grant = set, by
		code = b.dispatch(ctx, req, d, captureInto(&captured, stdout), stderr, ev)
		return nil
	}
	finish := func(err error, by string) (int, string, bool) {
		if err != nil {
			return ExitUnavailable, "authorization or required audit failed: " + err.Error() + "; nothing ran; run the command again", true
		}
		if code == ghExitAuth {
			return ExitUnavailable, noHostLogin, true
		}
		b.afterWrite(t, d.Argv, code, captured.Bytes(), by, stderr)
		return code, "", true
	}

	// 1. A user grant.
	if kind, ok := b.userGrantCovers(t); ok {
		if afterGrantLook != nil {
			afterGrantLook()
		}
		err := brokerlock.WithTransactionContext(ctx, b.store.Dir, func() error {
			if err := gate(); err != nil {
				return err
			}
			if _, still := b.userGrantCovers(t); !still {
				return errors.New("this workspace's " + kind + " grant no longer holds")
			}
			return run(kind, kind)
		})
		return finish(err, kind)
	}

	// 2 and 3. A working-set entry or a timed grant on the resource.
	kinds, resource := t.coveringKinds(), t.resource()
	if len(kinds) == 0 || resource == "" {
		return 0, "", false
	}
	if _, live, err := b.store.LiveWorkingKind(b.workspace, t.storeRepo(), resource, kinds...); err != nil || !live {
		return 0, "", false
	}
	if t.Kind == targetPR || t.Kind == targetIssue || t.Kind == targetIssueOrPR {
		state, err := b.readIssue(ctx, t)
		if err != nil {
			stderr([]byte("github-broker: could not read whether " + t.describe() + " is still open (" + err.Error() +
				"), so its working-set entry does not cover this write, which asks\n"))
			return 0, "", false
		}
		if state.Merged {
			// A PR leaves the working set on merge (BB-D97), and the write asks.
			_, _ = b.store.EndWorkingResource(b.workspace, t.storeRepo(), resource, brokerstore.EndMerged,
				brokerstore.WorkingPR, brokerstore.WorkingIssue, brokerstore.WorkingMerge)
			stderr([]byte("github-broker: " + t.describe() + " is merged, so it has left this workspace's working set; this write asks\n"))
			return 0, "", false
		}
	}
	by := ""
	e, covered, err := b.store.DispatchWorkingContext(ctx, b.workspace, t.storeRepo(), resource, kinds,
		func(brokerstore.WorkingEntry) error { return gate() },
		func(e brokerstore.WorkingEntry) error {
			set := setWorkingSet
			if e.Timed() {
				set = setTimed
			}
			by = e.ID
			return run(set, e.ID)
		})
	if !covered && err == nil {
		// Revoked or expired between the look and the transaction: nothing ran.
		return 0, "", false
	}
	if by == "" {
		by = e.ID
	}
	return finish(err, by)
}

// afterGrantLook, nil outside tests, runs between the look at a user grant and the
// transaction that re-checks it, where a test withdraws the grant.
var afterGrantLook func()

// captureInto is stdout, also copied into buf up to 64 KiB: enough for gh's report of a
// creation, and a longer one is not read (createdNumber fails on it).
func captureInto(buf *bytes.Buffer, stdout func([]byte)) func([]byte) {
	return func(p []byte) {
		stdout(p)
		if buf.Len() < 64<<10 {
			buf.Write(p)
		}
	}
}

// afterWrite updates the working set from a write that ran and exited 0: a creation joins it
// by the number gh reported, by what covered the creation (`create-pr`, a timed grant's w-id,
// or an Allow once's g-id); a merge through the broker ends the PR's entries (BB-D97). It runs
// outside the store's lock.
func (b *Broker) afterWrite(t writeTarget, argv []string, code int, out []byte, by string, stderr func([]byte)) {
	if code != 0 || b.store == nil {
		return
	}
	say := func(msg string) { stderr([]byte("github-broker: " + msg + "\n")) }
	switch t.Kind {
	case targetCreatePR, targetCreateIssue:
		if p, err := parseArgv(argv); err == nil && p.has("dry-run") {
			return
		}
		what, kind := "PR", brokerstore.WorkingPR
		if t.Kind == targetCreateIssue {
			what, kind = "issue", brokerstore.WorkingIssue
		}
		n := createdNumber(t, argv, out)
		if n == 0 {
			say("could not read the new " + what + "'s number from gh's output, so it was not added to this workspace's " +
				"working set; the next write to it asks (`yolo approve <id> --add` on the host adds it)")
			return
		}
		e, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace, Repo: t.storeRepo(), Kind: kind,
			Resource: "#" + strconv.Itoa(n), By: by})
		if err != nil {
			say(fmt.Sprintf("%s %s#%d was created, but could not be recorded in this workspace's working set (%v); "+
				"the next write to it asks", what, t.Repo, n, err))
			return
		}
		next := "comments, edits, reviews and closing it now run without asking; a merge still asks"
		if kind == brokerstore.WorkingIssue {
			next = "comments, edits and closing it now run without asking"
		}
		say(fmt.Sprintf("%s %s#%d joined this workspace's working set (%s): %s", what, t.Repo, n, e.ID, next))
	case targetMerge:
		if p, err := parseArgv(argv); err == nil && (p.has("auto") || p.has("disable-auto")) {
			// Auto-merge only arms or disarms a later merge; the next write reads whether it happened.
			return
		}
		if _, err := b.store.EndWorkingResource(b.workspace, t.storeRepo(), t.resource(), brokerstore.EndMerged,
			brokerstore.WorkingPR, brokerstore.WorkingIssue, brokerstore.WorkingMerge); err != nil {
			say("the merge ran, but " + t.describe() + "'s working-set entry could not be ended (" + err.Error() +
				"); the next write to it reads that it is merged and asks")
		}
	}
}

// answerHint is the clause a pending GitHub request's message adds, naming the answers that
// fit its kind (§6.4): Add for a PR or issue, a duration for a timed grant, and nothing beyond
// Allow once for a write the broker cannot name the resource of.
func answerHint(t writeTarget, id string) string {
	switch t.Kind {
	case targetPR, targetIssue, targetIssueOrPR:
		return fmt.Sprintf(" The user may answer `yolo approve %s --add` to add %s to this workspace's working set, "+
			"after which writes to it run without asking, or `--for 15m` to allow them for 15 minutes.", id, t.describe())
	case targetMerge:
		return fmt.Sprintf(" A merge asks per PR: `yolo approve %s` allows it once, `--for 15m` for 15 minutes.", id)
	case targetCreatePR, targetCreateIssue:
		return fmt.Sprintf(" `yolo approve %s --for 15m` allows %s for 15 minutes; a lasting right is a %s grant "+
			"(`yolo request github %s`, then `yolo config apply` on the host).", id, t.describe(), createGrantKind(t),
			strings.ReplaceAll(createGrantKind(t), "-", "_"))
	case targetRepo:
		return fmt.Sprintf(" `yolo approve %s --for 15m` allows %s for 15 minutes.", id, t.describe())
	}
	return ""
}
