package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalqueue"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const approveUsage = `Usage: yolo approve [request] [--for <duration|session> | --deny [--note <text>]]
       yolo approve --revoke <grant|w-entry|c-entry|all> [--workspace <path>]
       yolo approve --json
       yolo approve --watch [--json]
       yolo approve --web [--listen 127.0.0.1:<port>] [--reconcile <duration>] [--assets <dir>] [--no-open]

Host-only: list requests, grants and each workspace's working set, allow a
request once, grant its requested set for a bounded duration, deny it with optional human feedback,
revoke a grant or a working-set entry. --note is valid only with --deny, is limited to 500
characters, and is shown as human feedback, never executed or treated as a grant. Use
--note=<text> when feedback starts with a dash (for example, yolo approve r-<id> --deny
--note=--json). Without --for, Allow once covers that exact command for one use, for at most 15
minutes and never beyond its set's ceiling. --for hands over the whole requested set, within its policy ceiling
and use cap; session is 12 hours. For a GitHub write, --for <duration> (at
most 12h) allows that kind of write on its resource (the PR, the issue, PR or
issue creation, or the repository's other writes) for that long. A PR or issue
the agent creates under a create-pr or create-issue grant, a --for or an Allow
once joins the workspace's working set, where later writes to it run unasked
until the PR is merged or the entry revoked. A git push request lists
each ref it would change, old id -> new id, marked "needs approval" where no
grant or working-set entry covered it, then each push option it sends; Allow
once covers that exact push, every ref and option of it, once, and a new branch
it creates joins the working set. For a git push, --for <duration> (at most 24h) allows
every push to
the refs that needed approval for that long. For a branch the agent pushes again,
the lasting path is the project's own: the agent runs
` + "`yolo request git push <branch>`" + ` (or ` + "`push_tags <tag>`" + `) in the jail, and
` + "`yolo config apply`" + ` on the host approves the change. --add and --remember
are removed: either is refused with exit 2, deciding nothing, and names these
answers; branches, PRs and issues a human added with --add before its removal
stay in the working set until revoked. A grant
belongs to the request's workspace: every jail of that project may use it, and
it lasts through broker and jail restarts and relaunches until it expires, its
uses run out, or it is revoked.

The working set is the branches a workspace's git-broker pushes unasked: each
one the agent created through the broker under a create-branch grant or an
Allow once (by the grant's g-<id>), until
the branch is deleted or the entry revoked (a w-<id> entry), and each one a
branch grant declares in the project's config (a c-<id> line, marked "from
config", listed while a jail of that project runs). A workspace's GitHub
working set is the PRs and issues its github-broker writes unasked (w-<id>
entries, by create-pr, create-issue, a grant's g-<id>, a timed grant's w-<id>,
or human for an --add given before its removal), until the PR is merged or the
entry revoked; a GitHub --for is a w-<id> entry too, listed with its expiry. --revoke w-<id> ends one
entry; --revoke c-<id> refuses with exit 5 and names the config file to remove
the grant from (exit 4 when no running jail declares it). --revoke all ends
every live grant; --revoke all --workspace <path> ends that workspace's grants
and its recorded working set, and exits 4 when nothing in the store matched,
naming the config file when the workspace's working set holds only branches
declared there. Neither ends a create-branch, branch or tag grant
written in the project's config: remove it there with
` + "`yolo config unset requests.git.<capability> --project`" + ` (or one entry of the
list, ` + "`requests.git.push[<n>]`" + `), run in the project, then ` + "`yolo config apply`" + `; a
project still on yolo-jail.jsonc converts first with ` + "`yolo config migrate`" + `. Or
take the capability out of ` + "`allow.git.on_request`" + ` in your user config
(` + "`brokered.git.grant_kinds`" + ` in one still on JSONC).

--json prints pending requests, live grants and live working-set entries as one
JSON document; a grant's "workspace" is its holder, "jail" the jail whose
request it answered, and "used_by" the jail that spent its latest use; a branch declared in a running jail's config is a working-set
entry with "by": "config" and "file" naming the config that declares it. --watch prints the current state, then one line per event
(request filed, decided or expired; grant used, ended or revoked) until
interrupted; with --json, one "state" object holding every store, then one
JSON object per event, each on its own line. Every JSON object carries
"schema": 1. A request whose on_request command failed says so, in the listing
and as "hook_failure" and "hook_output" in its JSON. This is the surface a UI
built outside yolo uses. Such a UI must never accept approvals from the jails
it approves: a jail can reach a port the host listens on.

--web (experimental, read-only, host only) serves a browser view of the same
state on 127.0.0.1 and cannot allow, deny or revoke anything. Run it in a
terminal: it works only when standard output is a terminal, prints a one-time
link there, and opens your browser on a private launcher file that carries the
link, so the link is never in a process's argv. --no-open skips the browser.
Press Enter for a new link. A link used a second time ends every session and
warns once, and makes no new link and opens no browser: press Enter once you
have found the cause. --listen sets the address (127.0.0.1 only; default 127.0.0.1:0, a free
port), --reconcile how often it takes a full snapshot (default 15s), and
--assets serves the page's files from a directory for development. See
docs/design/approval-experience/README.md.

Exit codes: 0 done; 1 the store could not be read or written; 2 usage;
3 the request is already decided; 4 no request, grant or entry has that id
(or, with --workspace, that workspace path);
5 refused by policy (beyond the set's ceiling, or a revoke of a branch declared
in config); 6 the host can no longer confirm
the request (its jail's launch is gone or its scope changed), so it cannot be
allowed. A request whose jail stopped normally is already refused, which is 3.

Examples:
  yolo approve
  yolo approve r-<id>
  yolo approve r-<id> --for 15m
  yolo approve r-<id> --deny
  yolo approve r-<id> --deny --note "use the release branch"
  yolo approve r-<id> --deny --note=--json
  yolo approve --revoke w-<id>
  yolo approve --revoke c-<id>       # names the config file to edit instead
  yolo approve --revoke all
  yolo approve --revoke all --workspace ~/code/app
  yolo approve --watch --json
`

// The stable exit codes of `yolo approve` (boundary-broker.md §6.6). A UI keys on these.
const (
	approveExitFailed         = 1
	approveExitUsage          = 2
	approveExitAlreadyDecided = 3
	approveExitUnknownID      = 4
	approveExitPolicy         = 5
	approveExitNotConfirmed   = 6
)

// approveWatchInterval is how often `--watch` polls the store.
var approveWatchInterval = time.Second

// approveWebStart runs the browser queue; a variable so a test can see `yolo approve --web`
// reach it without starting a server.
var approveWebStart = approvalqueue.Main

func runApprove(args []string) int {
	if answerHelp("approve", args, os.Stdout) {
		return 0
	}
	if len(args) > 1 && args[1] == "--web" {
		return approveWeb(args[2:], config.InJail(), approveWebStart, os.Stderr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if config.InJail() {
		return approveAll(ctx, args[1:], true, nil, os.Stdout, os.Stderr)
	}
	stores, diagnostics, err := approvalStores()
	if err != nil {
		return approveFailure(err, os.Stderr)
	}
	return approveAllWithDiagnostics(ctx, args[1:], false, stores, diagnostics, os.Stdout, os.Stderr)
}

// approveWeb is `yolo approve --web`: the experimental read-only browser queue. It is host-only
// like every other form of yolo approve; start is the queue's entry point, a parameter so a test
// can see the dispatch reach it.
func approveWeb(args []string, inJail bool, start func([]string) int, errs io.Writer) int {
	if inJail {
		fmt.Fprintln(errs, "yolo approve --web is host-only: the request store is never mounted into a jail. Run `yolo approve --web` in a terminal on the host.")
		return approveExitUsage
	}
	return start(args)
}

// approveFailure maps a store error to its stable exit code and the next step its message
// names.
func approveFailure(err error, errs io.Writer) int {
	code, next := approveExitFailed, "check that the host store under "+paths.BrokerDir()+" is readable and writable, then retry"
	switch {
	case errors.Is(err, brokerstore.ErrAlreadyDecided):
		code, next = approveExitAlreadyDecided, "run `yolo approve` to see its state; the agent re-runs its command to file a new request"
	case errors.Is(err, brokerstore.ErrUnknownID):
		code, next = approveExitUnknownID, "run `yolo approve` to list the current ids; an ended record is kept 7 days"
	case errors.Is(err, brokerstore.ErrRefusedByPolicy):
		code, next = approveExitPolicy, "Allow it once or choose a shorter --for; to widen the set, edit its entry under `brokered` in "+paths.UserConfigDisplayPath()
	case errors.Is(err, brokerstore.ErrNotConfirmed):
		code, next = approveExitNotConfirmed, "deny it with `yolo approve <id> --deny`, or let it expire; if its jail still runs, restart it and have the agent re-run its command to file a new request"
	}
	if code == approveExitPolicy && strings.Contains(err.Error(), "Next:") {
		// The source's own refusal already names the answer that applies.
		fmt.Fprintf(errs, "yolo approve: %s.\n", termsafe.Visible(err.Error()))
		return code
	}
	fmt.Fprintf(errs, "yolo approve: %s. Next: %s.\n", termsafe.Visible(err.Error()), next)
	return code
}

// approveVia is who is answering request id, as its record keeps it (docs/design/jail-history.md
// JH-D11): the request's own on_request hook, whose environment names that request
// (brokerhook.Env sets YOLO_REQUEST_ID, and nothing else does), or the human at a host terminal.
func approveVia(id string, getenv func(string) string) string {
	if id != "" && getenv("YOLO_REQUEST_ID") == id {
		return brokerstore.ViaOnRequest
	}
	return brokerstore.ViaHost
}

// approveMain is approveAll over one store.
func approveMain(ctx context.Context, args []string, inJail bool, s *brokerstore.Store, out, errs io.Writer) int {
	return approveAll(ctx, args, inJail, []*brokerstore.Store{s}, out, errs)
}

// approveAll is `yolo approve` over every store in stores: it lists them all, and finds a
// request, grant or working-set entry by its id in whichever store holds it.
func approveAll(ctx context.Context, args []string, inJail bool, stores []*brokerstore.Store, out, errs io.Writer) int {
	return approveAllWithDiagnostics(ctx, args, inJail, stores, nil, out, errs)
}

func approveAllWithDiagnostics(ctx context.Context, args []string, inJail bool, stores []*brokerstore.Store, diagnostics []string, out, errs io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(errs, "yolo approve: %v. See `yolo approve --help`; run `yolo approve` on the host to list requests, grants and working sets.\n", err)
		return approveExitUsage
	}
	if inJail {
		fmt.Fprintln(errs, "yolo approve is host-only: the request store is never mounted into a jail. Run `yolo approve` in a terminal on the host.")
		return approveExitUsage
	}
	var request, revoke, workspace string
	var d brokerstore.Decision
	var removed string // the first removed answer flag given (GB-D28, OQ-GW2)
	forSeen, noteSeen, asJSON, watch, workspaceSeen := false, false, false, false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, eq := strings.Cut(a, "=")
		switch name {
		case "--for", "--revoke", "--workspace":
			if !eq {
				i++
				if i >= len(args) {
					return fail(fmt.Errorf("%s needs a value", name))
				}
				value = args[i]
			}
			switch name {
			case "--revoke":
				if revoke != "" || value == "" {
					return fail(fmt.Errorf("name one grant, one working-set entry, or all for --revoke"))
				}
				revoke = value
				continue
			case "--workspace":
				if workspaceSeen || value == "" {
					return fail(fmt.Errorf("give --workspace one workspace path"))
				}
				workspaceSeen, workspace = true, value
				continue
			}
			if forSeen {
				return fail(fmt.Errorf("specify --for only once"))
			}
			forSeen = true
			if value == "session" {
				d.Session = true
			} else {
				duration, err := time.ParseDuration(value)
				if err != nil || duration <= 0 {
					return fail(fmt.Errorf("give --for a positive duration or session"))
				}
				d.For = duration
			}
		case "--add", "--remember":
			if removed == "" {
				removed = name
			}
		case "--note":
			if noteSeen {
				return fail(fmt.Errorf("specify --note only once"))
			}
			if !eq {
				i++
				if i >= len(args) {
					return fail(fmt.Errorf("the note needs a value"))
				}
				value = args[i]
				if strings.HasPrefix(value, "-") {
					return fail(fmt.Errorf("%s value %q looks like a flag; use --note=<text> for feedback beginning with a dash", name, value))
				}
			}
			if strings.TrimSpace(value) == "" {
				return fail(fmt.Errorf("give --note non-empty human feedback"))
			}
			if !utf8.ValidString(value) || utf8.RuneCountInString(value) > brokerstore.MaxDenialNoteRunes {
				return fail(fmt.Errorf("the note must be at most %d characters; shorten the feedback and try again", brokerstore.MaxDenialNoteRunes))
			}
			noteSeen, d.DenialNote = true, value
		case "--deny":
			if eq || d.Deny {
				return fail(fmt.Errorf("specify --deny once, without a value"))
			}
			d.Deny = true
		case "--json", "--watch":
			if eq {
				return fail(fmt.Errorf("%s takes no value", name))
			}
			if name == "--json" {
				asJSON = true
			} else {
				watch = true
			}
		default:
			if strings.HasPrefix(a, "-") || request != "" {
				return fail(fmt.Errorf("unexpected argument %q", a))
			}
			request = a
		}
	}
	if removed != "" {
		return refuseRemovedApproveFlag(removed, request, errs)
	}
	if noteSeen && !d.Deny {
		return fail(fmt.Errorf("a note is only for --deny; remove it or pair it with --deny"))
	}
	// The answers to one request exclude each other: Deny, Allow for a time.
	answers := 0
	for _, seen := range []bool{d.Deny, forSeen} {
		if seen {
			answers++
		}
	}
	if answers > 1 || (revoke != "" && (request != "" || answers > 0)) || (answers > 0 && request == "") {
		return fail(fmt.Errorf("choose one request decision, or --revoke"))
	}
	d.Via = approveVia(request, os.Getenv)
	if request != "" && !d.Deny {
		source, found, err := pendingRequestSource(stores, request)
		if err != nil {
			return approveFailure(err, errs)
		}
		if found {
			if s := storeForSource(stores, source); s != nil && (s.Revalidate == nil || (s.ApprovalStatus != "" && s.ApprovalStatus != "selected")) {
				if s.ApprovalDiagnostic != "" {
					fmt.Fprintf(errs, "yolo approve: %s\n", termsafe.Visible(s.ApprovalDiagnostic))
				}
				fmt.Fprintf(errs, "yolo approve: source %s is %s and has no active core approval authority; it may be denied or revoked, but not granted. Next: keep it pending until its selected source and core authority can be confirmed, or deny it with `yolo approve %s --deny`.\n", termsafe.Visible(source), sourceStatusLabel(s), termsafe.Visible(request))
				return approveExitNotConfirmed
			}
		}
	}
	if workspaceSeen && revoke != "all" {
		return fail(fmt.Errorf("the --workspace flag narrows only --revoke all; revoke one grant or entry by its id alone"))
	}
	if (asJSON || watch) && (request != "" || revoke != "") {
		return fail(fmt.Errorf("decide or revoke without --json or --watch, which only list or follow the store"))
	}
	if request != "" && forSeen {
		source, found, err := pendingRequestSource(stores, request)
		if err != nil {
			return approveFailure(err, errs)
		}
		if descriptor, ok := descriptorForSource(stores, source); found && ok && descriptor.allowOnceOnly != "" && !descriptor.perResource {
			fmt.Fprintf(errs, "yolo approve: --for is not available for %s. Next: run `yolo approve %s` with no flags to Allow once.\n", descriptor.allowOnceOnly, termsafe.Visible(request))
			return approveExitPolicy
		}
		if descriptor, ok := descriptorForSource(stores, source); found && ok && descriptor.perResource && perRefStore(stores, source) != nil {
			return approvePerRef(stores, source, request, d, out, errs)
		}
	}
	if watch {
		return approveWatchWithDiagnostics(ctx, stores, diagnostics, asJSON, out, errs)
	}
	if revoke != "" {
		return approveRevoke(ctx, stores, revoke, workspace, out, errs)
	}
	if request != "" {
		var g brokerstore.Grant
		err := eachStore(stores, func(s *brokerstore.Store) error {
			var err error
			g, err = s.Decide(request, "pending", d)
			return err
		})
		if err != nil {
			return approveFailure(err, errs)
		}
		if d.Deny {
			fmt.Fprintf(out, "Denied %s. Run `yolo approve` to list requests and grants.\n", termsafe.Visible(request))
		} else if descriptor, ok := descriptorForSource(stores, g.Set); ok && descriptor.allowOnceOnly == "selected pack calls" {
			approveCallBrokerOnceOutcome(out, request, g)
		} else {
			approveOnceOutcome(out, request, g)
		}
		return 0
	}
	snaps := make([]brokerstore.Snapshot, len(stores))
	for i, s := range stores {
		snap, err := s.Snapshot()
		if err != nil {
			return approveFailure(err, errs)
		}
		snaps[i] = snap
	}
	if asJSON {
		return approveJSON(out, withConfigBranches(ctx, stores, mergedState(stores, snaps, diagnostics)), errs)
	}
	printApprovalDiagnostics(out, diagnostics)
	empty := true
	for i, s := range stores {
		printApprovalSource(out, s)
		printApproveList(out, snaps[i])
		printWorkingSet(out, s, snaps[i])
		declared := storeConfigBranches(ctx, s)
		printConfigBranches(out, declared)
		empty = empty && len(snaps[i].Requests) == 0 && len(snaps[i].Grants) == 0 && !anyLive(snaps[i].Working) && len(declared) == 0
	}
	if empty {
		if len(diagnostics) != 0 {
			fmt.Fprintln(out, "INCOMPLETE: no requests were found in readable stores; unresolved source stores may still exist. Next: run `yolo check`, repair or fetch the named packs, then list approvals again.")
		} else {
			fmt.Fprintln(out, "No requests, grants or working-set entries. A windowed GitHub write, or a git push no grant or working-set entry covers, can file a pending Allow-once request; decide it with `yolo approve <id>`. Run `yolo approve --help` for the host store commands.")
		}
	} else if len(diagnostics) != 0 || hasDenyOnlySources(stores) {
		fmt.Fprintln(out, "Requests are source-labeled: only sources with active core authority can be granted; pack sources without a core adapter and removed or unknown sources are deny/revoke only. Run `yolo check` to resolve incomplete selection, or use `yolo approve <id> --deny` for a refusal.")
		fmt.Fprintln(out, "Revoke a listed `w-` recorded working-set entry with `yolo approve --revoke <w-id>`. A listed `c-` entry is declared in config; run `yolo approve --revoke <c-id>` for the exact config change that removes it.")
	} else {
		fmt.Fprintln(out, "Decide pending requests with `yolo approve <id>` (a GitHub write takes `--for <duration>` for its resource; a selected pack's write takes `--for <duration>` up to the smallest max_for of the cells it writes; a git push takes `--for <duration>` to allow its refs for that long, and a branch the agent pushes again lasts through its `yolo request git push <branch>` and `yolo config apply`; `--for <duration>` grants a whole set only where supported), or `--deny`; revoke with `yolo approve --revoke <grant|w-entry|c-entry|all> [--workspace <path>]` (a c- entry is a working-set branch declared in config and is removed there).")
	}
	return 0
}

// approvalDescriptor carries the source-owned answer capabilities into the common approval
// path. Pack sources get only the generic Allow-once/Deny surface until core has an authority
// adapter; the two existing broker packages keep their established answer semantics.
type approvalDescriptor struct {
	allowOnceOnly string
	perResource   bool
}

func approvalDescriptorForStore(s *brokerstore.Store) approvalDescriptor {
	if s == nil {
		return approvalDescriptor{}
	}
	switch s.SourceName() {
	case ghbroker.Source:
		return approvalDescriptor{allowOnceOnly: "GitHub writes", perResource: s.Entries != nil}
	case gitbroker.Source:
		return approvalDescriptor{perResource: s.Entries != nil}
	default:
		if s.Revalidate != nil && s.ApprovalStatus == "selected" {
			return approvalDescriptor{allowOnceOnly: "selected pack calls", perResource: s.Entries != nil}
		}
		return approvalDescriptor{}
	}
}

func descriptorForSource(stores []*brokerstore.Store, source string) (approvalDescriptor, bool) {
	for _, s := range stores {
		if s.SourceName() == source {
			return approvalDescriptorForStore(s), true
		}
	}
	return approvalDescriptor{}, false
}

// perRefStore is the store of source whose requests take the per-resource answers, or nil.
func perRefStore(stores []*brokerstore.Store, source string) *brokerstore.Store {
	for _, s := range stores {
		if s.SourceName() == source && s.Entries != nil {
			return s
		}
	}
	return nil
}

// approvePerRef answers request id with `--for <duration>` (Allow push to its refs for a
// time): working-set entries per ref, in place of a command grant (GB-D26). The answer that
// added them for good is removed (GB-D28).
func approvePerRef(stores []*brokerstore.Store, source, id string, d brokerstore.Decision, out, errs io.Writer) int {
	if source == ghbroker.Source {
		return approveGitHubPerResource(perRefStore(stores, source), id, d, out, errs)
	}
	if descriptor, _ := descriptorForSource(stores, source); descriptor.allowOnceOnly == "selected pack calls" {
		return approvePackPerResource(perRefStore(stores, source), id, d, out, errs)
	}
	if d.Session {
		fmt.Fprintf(errs, "yolo approve: --for session is not available for git pushes, which allow a duration of at most 24h. Next: run `yolo approve %s --for 15m`.\n", termsafe.Visible(id))
		return approveExitPolicy
	}
	entries, err := perRefStore(stores, source).DecideWorking(id, d)
	if err != nil {
		return approveFailure(err, errs)
	}
	var refs []string
	for _, e := range entries {
		refs = append(refs, termsafe.Visible(e.Resource))
	}
	approveTimedOutcome(out, id, "pushes to "+strings.Join(refs, ", "), entries)
	return 0
}

// approvePackPerResource answers a selected pack call's request id with --for: one timed entry
// per classified write, kind call:<cell> on the write's chain, for at most the smallest max_for
// among its cells (pack-brokers.md BX-D17), stored for every write or none (BX-D18). A pack
// gets the answers yolo's own packs get, so no permanent Add (OQ-BX7).
func approvePackPerResource(s *brokerstore.Store, id string, d brokerstore.Decision, out, errs io.Writer) int {
	if d.Session {
		fmt.Fprintf(errs, "yolo approve: --for session is not available for pack calls: no grant is scoped to a session. "+
			"Next: run `yolo approve %s --for 15m`.\n", termsafe.Visible(id))
		return approveExitPolicy
	}
	entries, err := s.DecideWorking(id, d)
	if err != nil {
		return approveFailure(err, errs)
	}
	var writes []string
	for _, e := range entries {
		writes = append(writes, termsafe.Visible(strings.TrimPrefix(e.Kind, brokerstore.WorkingCallPrefix)+" on "+e.Resource))
	}
	approveTimedOutcome(out, id, "pack writes "+strings.Join(writes, "; "), entries)
	return 0
}

// refuseRemovedApproveFlag refuses `--add` or `--remember`, both removed (git-broker.md GB-D28,
// github-write-scopes.md OQ-GW2), naming the answers that replace them. It decides nothing.
func refuseRemovedApproveFlag(flag, request string, errs io.Writer) int {
	id := "<id>"
	if request != "" {
		id = termsafe.Visible(request)
	}
	what := "an answer no longer adds anything to the working set"
	if flag == "--remember" {
		what = "an answer no longer writes the project's config"
	}
	fmt.Fprintf(errs, "yolo approve: %s was removed: %s. Nothing was decided. "+
		"Next: run `yolo approve %s` to Allow once, or `yolo approve %s --for 15m` to allow it for a while; "+
		"for a git branch to push unasked for good, the agent runs `yolo request git push <branch>` (or `push_tags <tag>`) in the jail "+
		"and you run `yolo config apply` on the host, in the project. "+
		"Entries added with --add before its removal stay in the working set until `yolo approve --revoke w-<id>`.\n", flag, what, id, id)
	return approveExitUsage
}

// pendingRequestSource returns the source owning a currently visible pending request.
func pendingRequestSource(stores []*brokerstore.Store, id string) (string, bool, error) {
	for _, s := range stores {
		snap, err := s.Snapshot()
		if err != nil {
			return "", false, err
		}
		for _, q := range snap.Requests {
			if q.ID == id {
				return s.SourceName(), true, nil
			}
		}
	}
	return "", false, nil
}

func storeForSource(stores []*brokerstore.Store, source string) *brokerstore.Store {
	for _, s := range stores {
		if s.SourceName() == source {
			return s
		}
	}
	return nil
}

// ErrUnknownID, or the last unknown-id error when none holds it.
func eachStore(stores []*brokerstore.Store, fn func(*brokerstore.Store) error) error {
	if len(stores) == 0 {
		return brokerstore.ErrUnknownID
	}
	var err error
	for _, s := range stores {
		if err = fn(s); !errors.Is(err, brokerstore.ErrUnknownID) {
			return err
		}
	}
	return err
}

// approveRevoke is `--revoke <id|all> [--workspace <path>]`.
func approveRevoke(ctx context.Context, stores []*brokerstore.Store, revoke, workspace string, out, errs io.Writer) int {
	if strings.HasPrefix(revoke, configBranchIDPrefix) {
		for _, s := range stores {
			for _, b := range storeConfigBranches(ctx, s) {
				if b.ID == revoke {
					return refuseConfigBranchRevoke(b, errs)
				}
			}
		}
		// No running jail declares it: its jail stopped, or the grant left the config. The store
		// never held it, so the store's "ended record kept 7 days" does not apply.
		fmt.Fprintf(errs, "yolo approve: no running jail declares %s. A c- id is a working-set branch declared in config, "+
			"listed only while a jail of its project runs; the broker recorded nothing to revoke.\n"+
			"Next: to take the branch out, run `yolo config unset 'requests.git.push[<n>]' --project` in the project, "+
			"<n> being the branch's position from 0 as `yolo config why requests.git.push` lists it, then `yolo config apply` there "+
			"(a stopped jail reads it at its next launch); a project still on yolo-jail.jsonc, where it is a {\"kind\": \"branch\", …} "+
			"entry under `brokered.git.grants`, converts first with `yolo config migrate`. Run `yolo approve` to list the ids of running jails.\n",
			termsafe.Visible(revoke))
		return approveExitUnknownID
	}
	if revoke != "all" {
		if err := eachStore(stores, func(s *brokerstore.Store) error { return s.Revoke(revoke) }); err != nil {
			return approveFailure(err, errs)
		}
		fmt.Fprintf(out, "Revoked %s. Run `yolo approve` to list requests, grants and working sets.\n", termsafe.Visible(revoke))
		return 0
	}
	if workspace == "" {
		for _, s := range stores {
			if err := s.Revoke("all"); err != nil {
				return approveFailure(err, errs)
			}
		}
		fmt.Fprintln(out, "Revoked every live grant; working sets are unchanged (`--revoke all --workspace <path>` ends one workspace's). Run `yolo approve` to list requests, grants and working sets.")
		return 0
	}
	// A workspace is recorded by its host path as the launch resolved it; the path given here
	// is matched both as written (made absolute) and with its symlinks resolved.
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return approveFailure(err, errs)
	}
	forms := []string{filepath.Clean(abs)}
	if real, err := filepath.EvalSymlinks(abs); err == nil && real != forms[0] {
		forms = append(forms, real)
	}
	ended := 0
	for _, s := range stores {
		for _, ws := range forms {
			n, err := s.RevokeIn("all", ws)
			if err != nil {
				return approveFailure(err, errs)
			}
			ended += n
		}
	}
	shown := termsafe.Visible(forms[0])
	var declared []configBranch
	for _, s := range stores {
		for _, b := range storeConfigBranches(ctx, s) {
			if slices.Contains(forms, b.Workspace) {
				declared = append(declared, b)
			}
		}
	}
	if ended == 0 && len(declared) > 0 {
		// Only branches declared in config match: the listing would show the same path again,
		// so name what holds them instead.
		fmt.Fprintf(errs, "yolo approve: nothing in the store to revoke for %s: no live grant or recorded working-set entry. "+
			"It holds only branches and tags declared in config:\n", shown)
		for _, b := range declared {
			fmt.Fprintf(errs, "  %s of %s, declared in config in %s\n", termsafe.Visible(b.ref()), termsafe.Visible(b.Repo), termsafe.Visible(b.File))
		}
		fmt.Fprintln(errs, "Next: for each one,")
		for _, b := range declared {
			fmt.Fprintf(errs, "  %s: %s.\n", termsafe.Visible(b.ref()), configGrantRemoval(b.Workspace, []string{b.File, b.LocalFile}, b.Source, b.Kind, b.Branch))
		}
		fmt.Fprintln(errs, "Or take `push` and `push_tags` out of `allow.git.on_request` in your user config (`branch` and `tag` out of `brokered.git.grant_kinds` in one still on JSONC). The broker reads both at the next push.")
		return approveExitUnknownID
	}
	if ended == 0 {
		fmt.Fprintf(errs, "yolo approve: nothing to revoke for %s: no live grant or working-set entry records that workspace path.\n"+
			"Next: run `yolo approve` to see each working-set entry's workspace (in brackets), then give that path to --workspace.\n", shown)
		return approveExitUnknownID
	}
	fmt.Fprintf(out, "Revoked %d live grant(s) and working-set entr(ies) of %s.\n%s\nRun `yolo approve` to list requests, grants and working sets.\n", ended, shown, configGrantNote(forms[0]))
	return 0
}

// configGrantNote says what `--revoke all --workspace` cannot end: a grant written in config,
// which the launch puts in the scope file and the broker re-reads at every push (ID-9, ID-10),
// and the verb that removes it from workspace's config.
func configGrantNote(workspace string) string {
	project, local := configBranchFiles(workspace, "", "")
	return "A create-branch, branch or tag grant in the project's config stays, so the agent can still create " +
		"a new branch, every branch a branch grant declares stays in the working set, and every tag a tag grant names stays pushable: " +
		configGrantRemoval(workspace, []string{project, local}, gitbroker.Source, "", "") +
		"; or take the capability out of `allow.git.on_request` in your user config (`brokered.git.grant_kinds` in one still on JSONC). Either takes effect at the next push."
}

// configBranch is a working-set branch a `branch` grant declares in a workspace's config, or the
// tags a `tag` grant covers, as the scope file of a running broker holds it
// (docs/design/git-broker.md GB-D13, GB-D25). Branch is the grant's name, a pattern included, and
// Kind is `branch` or `tag`. It is never in the store: the config holds it, so `yolo approve`
// lists it and cannot revoke it.
type configBranch struct {
	ID, Source, Workspace, Repo, Branch, Kind string
	// File is the workspace's config file, where the grant is declared (or its local file).
	File, LocalFile string
}

// ref is the declared ref, or its pattern, under its namespace: refs/heads/main, refs/tags/v*.
func (b configBranch) ref() string { return brokerscope.RefNamespace(b.Kind) + b.Branch }

// storeConfigBranches is configBranches for the git source's store, the one source whose grants
// declare branches, and none for any other. The scope files sit beside the store, so a store
// anywhere but the source's own directory (a test's) has none.
func storeConfigBranches(ctx context.Context, s *brokerstore.Store) []configBranch {
	if s.SourceName() != gitbroker.Source || filepath.Clean(s.Dir) != filepath.Clean(paths.BrokerSourceDir(gitbroker.Source)) {
		return nil
	}
	return configBranches(ctx, s.SourceName())
}

// configBranchIDPrefix marks a config-declared branch's id, beside a store entry's `w-`.
const configBranchIDPrefix = "c-"

// configBranches is every branch a `branch` grant declares in the scope file of a running broker
// of source, once per workspace, repository and branch. A scope directory it cannot read lists
// none.
func configBranches(ctx context.Context, source string) []configBranch {
	files, err := brokerscope.EveryLiveFile(ctx, source)
	if err != nil {
		return nil
	}
	var out []configBranch
	seen := map[string]bool{}
	for _, lf := range files {
		for _, g := range lf.File.Grants {
			if !brokerscope.GrantTakesRef(g.Kind) {
				continue
			}
			key := lf.File.Workspace + "\x00" + g.Repo + "\x00" + g.Name()
			if g.Kind != brokerscope.KindBranch {
				// A branch grant's id is unchanged since it was the only kind listed.
				key = g.Kind + "\x00" + key
			}
			sum := sha256.Sum256([]byte(key))
			id := configBranchIDPrefix + hex.EncodeToString(sum[:16])
			if seen[id] {
				continue
			}
			seen[id] = true
			file, local := configBranchFiles(lf.File.Workspace, lf.File.ConfigFile, lf.File.LocalFile)
			out = append(out, configBranch{ID: id, Source: source, Workspace: lf.File.Workspace, Repo: g.Repo, Branch: g.Name(), Kind: g.Kind,
				File: file, LocalFile: local})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// printConfigBranches lists the config-declared working-set branches and tag grants, one line
// each, marked as from config and naming the file that declares them. A pattern is shown as
// written, under its namespace, and a repository "*" as written.
func printConfigBranches(out io.Writer, bs []configBranch) {
	for _, b := range bs {
		set := "working set"
		if b.Kind == brokerscope.KindTag {
			set = "tag grant"
		}
		fmt.Fprintf(out, "%s  %s  %s  [%s]  repo %s  %s %s  from config %s\n", b.ID, set,
			termsafe.Visible(b.Source), termsafe.Visible(b.Workspace), termsafe.Visible(b.Repo), b.Kind,
			termsafe.Visible(b.ref()), termsafe.Visible(b.File))
	}
}

// refuseConfigBranchRevoke answers a --revoke naming a config-declared branch: the broker
// recorded nothing to revoke, so it names the file the declaration is removed from.
func refuseConfigBranchRevoke(b configBranch, errs io.Writer) int {
	where := termsafe.Visible(b.File)
	if b.LocalFile != "" {
		where += " (or its local file " + termsafe.Visible(b.LocalFile) + ", if it is declared there)"
	}
	what := "a working-set branch"
	if b.Kind == brokerscope.KindTag {
		what = "a tag grant"
	}
	fmt.Fprintf(errs, "yolo approve: %s is %s declared in config in %s (%s of %s), not one the broker "+
		"recorded, so there is nothing to revoke here.\nNext: %s; the broker reads it at the next push.\n",
		b.ID, what, where, termsafe.Visible(b.ref()), termsafe.Visible(b.Repo),
		configGrantRemoval(b.Workspace, []string{b.File, b.LocalFile}, b.Source, b.Kind, b.Branch))
	return approveExitPolicy
}

func anyLive(ws []brokerstore.WorkingEntry) bool {
	for _, e := range ws {
		if e.LiveAt(time.Now()) {
			return true
		}
	}
	return false
}

func hasDenyOnlySources(stores []*brokerstore.Store) bool {
	for _, s := range stores {
		if s.Revalidate == nil || (s.ApprovalStatus != "" && s.ApprovalStatus != "selected") {
			return true
		}
	}
	return false
}

func sourceStatusLabel(s *brokerstore.Store) string {
	status := s.ApprovalStatus
	label := "selection not supplied by this caller"
	switch status {
	case "selected":
		label = "selected in current user-scope prediction (launch ownership not verified)"
	case "removed":
		label = "removed from current user-scope selection"
	case "unknown":
		label = "selection unknown"
	}
	if s.Revalidate == nil || (status != "" && status != "selected") {
		label += "; deny/revoke only, no active core approval authority"
	}
	return label
}

func printApprovalSource(out io.Writer, s *brokerstore.Store) {
	if s.ApprovalStatus == "" {
		return
	}
	fmt.Fprintf(out, "Source %s — %s.\n", termsafe.Visible(s.SourceName()), sourceStatusLabel(s))
}

func printApprovalDiagnostics(out io.Writer, diagnostics []string) {
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(out, "%s\n", termsafe.Visible(diagnostic))
	}
}

func printApproveList(out io.Writer, snap brokerstore.Snapshot) {
	for _, r := range snap.Requests {
		fmt.Fprintf(out, "%s  %s  jail %s  set %s  [%s]  repo %s: %s\n", termsafe.Visible(r.ID), termsafe.Visible(r.State), termsafe.Visible(r.Jail), termsafe.Visible(r.Set), termsafe.Visible(r.Workspace), termsafe.Visible(r.Repo), shquote.JoinDisplay(r.Argv))
		if r.Digest != "" || r.StdinDigest != "" {
			fmt.Fprintf(out, "    action identity: command %s; stdin SHA-256 %s\n", termsafe.Visible(r.Digest), termsafe.Visible(r.StdinDigest))
		}
		printPushRefs(out, r)
		if r.HookFailure != "" {
			fmt.Fprintf(out, "    your on_request failed, so you may not have been notified: %s (its output: `yolo approve --json`)\n", termsafe.Visible(r.HookFailure))
		}
	}
	for _, g := range snap.Grants {
		state := "live"
		if !g.Ended.IsZero() {
			state = "ended"
		}
		coverage := g.Set
		if g.Digest != "" {
			coverage += " (once)"
		}
		fmt.Fprintf(out, "%s  %s  [%s]  set %s  %d use(s)  expires %s\n", termsafe.Visible(g.ID), state, termsafe.Visible(g.Workspace), termsafe.Visible(coverage), g.Uses, g.Expires.Format(time.RFC3339))
	}
}

// printPushRefs lists a git push request's refs, one line each: the ref, its old and new ids
// to 12 hex digits, and whether it is what needs approval or covered already, then each push
// option the push sends, which the approval covers too (GB-D17, GB-D21). Any other request
// prints nothing.
func printPushRefs(out io.Writer, r brokerstore.Request) {
	if r.Set != "push" || len(r.Argv) < 2 || r.Argv[0] != "git-receive-pack" {
		return
	}
	short := func(id string) string {
		if len(id) > 12 {
			return id[:12]
		}
		return id
	}
	for _, line := range r.Argv[1:] {
		if o, ok := strings.CutPrefix(line, "push-option="); ok {
			fmt.Fprintf(out, "    push option: %s\n", termsafe.Visible(o))
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		mark := "covered"
		if slices.Contains(r.NeedsApproval, f[2]) {
			mark = "needs approval"
		}
		fmt.Fprintf(out, "    %s  %s -> %s  %s\n", termsafe.Visible(f[2]), termsafe.Visible(short(f[0])), termsafe.Visible(short(f[1])), mark)
	}
}

// printWorkingSet lists the store's live working-set entries, one line each: what a workspace
// may push unasked. An ended entry covers nothing and is not listed.
func printWorkingSet(out io.Writer, s *brokerstore.Store, snap brokerstore.Snapshot) {
	for _, e := range snap.Working {
		if !e.LiveAt(time.Now()) {
			continue
		}
		until := ""
		if e.Timed() {
			until = "  allowed until " + e.Expires.Format(time.RFC3339)
		}
		fmt.Fprintf(out, "%s  working set  %s  [%s]  repo %s  %s %s  by %s  since %s%s\n", termsafe.Visible(e.ID), termsafe.Visible(s.SourceName()), termsafe.Visible(e.Workspace), termsafe.Visible(e.Repo), termsafe.Visible(e.Kind), termsafe.Visible(e.Resource), termsafe.Visible(e.By), e.Added.Format(time.RFC3339), until)
	}
}

// approveJSON writes one object as one line.
func approveJSON(out io.Writer, v any, errs io.Writer) int {
	b, err := json.Marshal(v)
	if err != nil {
		return approveFailure(err, errs)
	}
	if _, err = out.Write(append(b, '\n')); err != nil {
		return approveExitFailed
	}
	return 0
}

// mergedState is every store's state as one view: what `--json` prints, and what `--watch
// --json` opens with as its one "state" object (boundary-broker.md BB-D72).
func mergedState(stores []*brokerstore.Store, snaps []brokerstore.Snapshot, diagnostics []string) brokerstore.StateView {
	v := brokerstore.StateView{Schema: brokerstore.Schema, Sources: []brokerstore.ApprovalSourceView{}, Diagnostics: append([]string(nil), diagnostics...), Requests: []brokerstore.RequestView{}, Grants: []brokerstore.GrantView{}, Working: []brokerstore.WorkingView{}}
	for _, s := range stores {
		authority := "deny-only"
		if s.Revalidate != nil && (s.ApprovalStatus == "" || s.ApprovalStatus == "selected") {
			authority = "core"
		}
		v.Sources = append(v.Sources, brokerstore.ApprovalSourceView{Source: s.SourceName(), Status: s.ApprovalStatus,
			Authority: authority, SelectionBasis: "user-scope prediction; request launch selection is not verified"})
	}
	for i, s := range stores {
		sv := s.StateView(snaps[i])
		v.Requests = append(v.Requests, sv.Requests...)
		v.Grants = append(v.Grants, sv.Grants...)
		v.Working = append(v.Working, sv.Working...)
	}
	return v
}

// withConfigBranches adds to v's working set every branch a `branch` grant declares in a running
// jail's config, as the text listing shows them: By "config", with the file that declares it.
func withConfigBranches(ctx context.Context, stores []*brokerstore.Store, v brokerstore.StateView) brokerstore.StateView {
	for _, s := range stores {
		for _, b := range storeConfigBranches(ctx, s) {
			v.Working = append(v.Working, brokerstore.WorkingView{Schema: brokerstore.Schema, ID: b.ID, Source: b.Source,
				Workspace: b.Workspace, Repo: b.Repo, Kind: b.Kind, Resource: b.ref(),
				By: configBranchBy, File: b.File})
		}
	}
	return v
}

// configBranchBy is a config-declared branch's By in `--json`, beside a store entry's
// "create-branch", grant id or "human".
const configBranchBy = "config"

// approveWatchWithDiagnostics is `--watch`: every store's current state, shown once, then each
// event, until ctx ends. It reads every store before showing anything, so `--json` opens with
// exactly one "state" object holding all of them, then follows each store from the snapshot it
// read (brokerstore.Store.WatchFrom), each polled on its own (brokerstore.Store.Watch says why).
func approveWatchWithDiagnostics(ctx context.Context, stores []*brokerstore.Store, diagnostics []string, asJSON bool, out, errs io.Writer) int {
	snaps := make([]brokerstore.Snapshot, len(stores))
	for i, s := range stores {
		snap, err := s.Snapshot()
		if err != nil {
			return approveFailure(err, errs)
		}
		snaps[i] = snap
	}
	if asJSON {
		v := withConfigBranches(ctx, stores, mergedState(stores, snaps, diagnostics))
		v.Event = "state"
		if code := approveJSON(out, v, errs); code != 0 {
			return code
		}
	} else {
		for i, s := range stores {
			printApprovalSource(out, s)
			printApproveList(out, snaps[i])
			printWorkingSet(out, s, snaps[i])
			printConfigBranches(out, storeConfigBranches(ctx, s))
		}
		printApprovalDiagnostics(out, diagnostics)
		fmt.Fprintln(errs, "Watching the store; one line per event. Press Ctrl-C to stop.")
	}

	var mu sync.Mutex
	code := 0
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	emit := func(v any, line string) {
		mu.Lock()
		defer mu.Unlock()
		if code != 0 {
			return
		}
		if asJSON {
			code = approveJSON(out, v, errs)
		} else if _, err := fmt.Fprintln(out, line); err != nil {
			code = approveExitFailed
		}
		if code != 0 {
			cancel() // the reader went away
		}
	}
	var wg sync.WaitGroup
	for i, s := range stores {
		s, prev := s, snaps[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			warned := false
			s.WatchFrom(ctx, prev, approveWatchInterval, func(ev brokerstore.Event) {
				now := time.Now().UTC()
				emit(s.EventView(ev, now), now.Format(time.RFC3339)+" "+s.Summary(ev))
			}, func(err error) {
				mu.Lock()
				defer mu.Unlock()
				if !warned {
					fmt.Fprintf(errs, "yolo approve: reading the store failed: %s; still watching, and retrying every %s.\n", termsafe.Visible(err.Error()), approveWatchInterval)
					warned = true
				}
			})
		}()
	}
	wg.Wait()
	return code
}
