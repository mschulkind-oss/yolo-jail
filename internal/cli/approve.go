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

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const approveUsage = `Usage: yolo approve [request] [--for <duration|session> | --add | --remember | --deny]
       yolo approve --revoke <grant|w-entry|c-entry|all> [--workspace <path>]
       yolo approve --json
       yolo approve --watch [--json]

Host-only: list requests, grants and each workspace's working set, allow a
request once, grant its requested set for a bounded duration, deny it, or
revoke a grant or a working-set entry. Without --for, Allow once covers that
exact command for one use, for at most 15 minutes and never beyond its set's
ceiling. --for hands over the whole requested set, within its policy ceiling
and use cap; session is 12 hours. For a GitHub write, --for <duration> (at
most 12h) allows that kind of write on its resource (the PR, the issue, PR or
issue creation, or the repository's other writes) for that long, and --add adds
the PR or issue it writes to the workspace's working set, where later writes to
it run unasked until the PR is merged or the entry revoked; a merge, a
creation and a repository-level write refuse --add, naming --for. A PR or issue
the agent creates under a create-pr or create-issue grant, a --for or an Allow
once joins the working set the same way. A git push request lists
each ref it would change, old id -> new id, marked "needs approval" where no
grant or working-set entry covered it, then each push option it sends; Allow
once covers that exact push, every ref and option of it, once, and a new branch
it creates joins the working set. For a git push, --for <duration> (at most 24h) allows
every push to
the refs that needed approval for that long, and --add adds those branches to
the workspace's working set for good; --add is refused for a tag and for a new
branch, naming --for as the next step. --remember allows that push once and
remembers each ref it needed approval for in the project's config, writing what
` + "`yolo request git push <branch>`" + ` (or ` + "`push_tags <tag>`" + `) writes there, through
that verb, so later pushes to it need no approval once ` + "`yolo config apply`" + ` approves
the change; it is refused, with nothing written, while the project's config is
still in the deprecated JSONC format. A grant
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
or human for --add), until the PR is merged or the entry revoked; a GitHub
--for is a w-<id> entry too, listed with its expiry. --revoke w-<id> ends one
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

Exit codes: 0 done; 1 the store could not be read or written; 2 usage;
3 the request is already decided; 4 no request, grant or entry has that id
(or, with --workspace, that workspace path);
5 refused by policy (beyond the set's ceiling, a revoke of a branch declared
in config, or a --remember that cannot be written); 6 the host can no longer confirm
the request (its jail's launch is gone or its scope changed), so it cannot be
allowed. A request whose jail stopped normally is already refused, which is 3.

No requests, grants or working-set entries. A GitHub write, or a git push no
grant or working-set entry covers, files a pending Allow-once request while its
host broker runs. Run yolo approve --help for the host store commands.

Examples:
  yolo approve
  yolo approve r-<id>
  yolo approve r-<id> --for 15m
  yolo approve r-<id> --remember     # a git push: allow it and remember it in the project's config
  yolo approve r-<id> --deny
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

func runApprove(args []string) int {
	if answerHelp("approve", args, os.Stdout) {
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Every registered source's store: the GitHub broker's requests and grants, and the git
	// broker's push requests, grants and working set, each decided against its live host
	// authority and audited.
	stores := []*brokerstore.Store{
		ghbroker.ApprovalStore(),
		gitbroker.ApprovalStore(),
	}
	return approveAll(ctx, args[1:], config.InJail(), stores, os.Stdout, os.Stderr)
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

// approveMain is approveAll over one store.
func approveMain(ctx context.Context, args []string, inJail bool, s *brokerstore.Store, out, errs io.Writer) int {
	return approveAll(ctx, args, inJail, []*brokerstore.Store{s}, out, errs)
}

// approveAll is `yolo approve` over every store in stores: it lists them all, and finds a
// request, grant or working-set entry by its id in whichever store holds it.
func approveAll(ctx context.Context, args []string, inJail bool, stores []*brokerstore.Store, out, errs io.Writer) int {
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
	forSeen, addSeen, rememberSeen, asJSON, watch, workspaceSeen := false, false, false, false, false, false
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
		case "--add":
			if eq || addSeen {
				return fail(fmt.Errorf("specify --add once, without a value"))
			}
			addSeen = true
		case "--remember":
			if eq || rememberSeen {
				return fail(fmt.Errorf("specify --remember once, without a value"))
			}
			rememberSeen = true
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
	// The answers to one request exclude each other: Deny, Allow for a time, Add, Remember.
	answers := 0
	for _, seen := range []bool{d.Deny, forSeen, addSeen, rememberSeen} {
		if seen {
			answers++
		}
	}
	if answers > 1 || (revoke != "" && (request != "" || answers > 0)) || (answers > 0 && request == "") {
		return fail(fmt.Errorf("choose one request decision, or --revoke"))
	}
	if workspaceSeen && revoke != "all" {
		return fail(fmt.Errorf("the --workspace flag narrows only --revoke all; revoke one grant or entry by its id alone"))
	}
	if (asJSON || watch) && (request != "" || revoke != "") {
		return fail(fmt.Errorf("decide or revoke without --json or --watch, which only list or follow the store"))
	}
	if request != "" && (forSeen || addSeen || rememberSeen) {
		source, found, err := pendingRequestSource(stores, request)
		if err != nil {
			return approveFailure(err, errs)
		}
		if what, onceOnly := allowOnceOnly[source]; found && onceOnly && perRefStore(stores, source) == nil {
			flag := "--for"
			if addSeen {
				flag = "--add"
			} else if rememberSeen {
				flag = "--remember"
			}
			fmt.Fprintf(errs, "yolo approve: %s is not available for %s. Next: run `yolo approve %s` with no flags to Allow once.\n", flag, what, termsafe.Visible(request))
			return approveExitPolicy
		}
		if rememberSeen {
			return approveRemember(stores, request, out, errs)
		}
		if !found && addSeen {
			// No request has this id: --add is answered by the store that takes it, which says so.
			source, found = gitbroker.Source, true
		}
		if found && perRefStore(stores, source) != nil {
			return approvePerRef(stores, source, request, d, addSeen, out, errs)
		}
		if addSeen {
			return fail(fmt.Errorf("unexpected argument %q", "--add"))
		}
	}
	if watch {
		return approveWatch(ctx, stores, asJSON, out, errs)
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
		} else {
			coverage := "one exact command"
			if g.Digest == "" {
				coverage = "whole set " + termsafe.Visible(g.Set)
			}
			fmt.Fprintf(out, "Allowed %s: %s, %d use(s) for every jail of %s, across restarts, expires %s (%s). Run `yolo approve` to list or `yolo approve --revoke %s` to revoke.\n", termsafe.Visible(request), coverage, g.Uses, termsafe.Visible(g.Workspace), g.Expires.Format(time.RFC3339), g.ID, g.ID)
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
		return approveJSON(out, withConfigBranches(ctx, stores, mergedState(stores, snaps)), errs)
	}
	empty := true
	for i, s := range stores {
		printApproveList(out, snaps[i])
		printWorkingSet(out, s, snaps[i])
		declared := storeConfigBranches(ctx, s)
		printConfigBranches(out, declared)
		empty = empty && len(snaps[i].Requests) == 0 && len(snaps[i].Grants) == 0 && !anyLive(snaps[i].Working) && len(declared) == 0
	}
	if empty {
		fmt.Fprintln(out, "No requests, grants or working-set entries. A windowed GitHub write, or a git push no grant or working-set entry covers, can file a pending Allow-once request; decide it with `yolo approve <id>`. Run `yolo approve --help` for the host store commands.")
	} else {
		fmt.Fprintln(out, "Decide pending requests with `yolo approve <id>` (a GitHub write takes `--for <duration>` for its resource or `--add` for its PR or issue; a git push takes `--for <duration>` to allow its refs for that long, `--add` to add its branches to the working set, or `--remember` to allow it and remember its refs in the project's config; `--for <duration>` grants a whole set only where supported), or `--deny`; revoke with `yolo approve --revoke <grant|w-entry|c-entry|all> [--workspace <path>]` (a c- entry is a working-set branch declared in config and is removed there).")
	}
	return 0
}

// allowOnceOnly is each source whose requests take Allow once and Deny alone unless its store
// answers per resource (an Entries hook), with how the refusal of --for and --add names its
// requests (BB-D77). A GitHub write takes Allow for a time and Add on its resource (BB-D79,
// BB-D92) through ghbroker.ApprovalStore's Entries; a GitHub store without that hook never
// falls through to a whole-set grant. A git push is not here: it takes per-ref answers (GB-D26).
var allowOnceOnly = map[string]string{ghbroker.Source: "GitHub writes"}

// perRefStore is the store of source whose requests take the per-resource answers, or nil.
func perRefStore(stores []*brokerstore.Store, source string) *brokerstore.Store {
	for _, s := range stores {
		if s.SourceName() == source && s.Entries != nil {
			return s
		}
	}
	return nil
}

// approvePerRef answers request id with `--for <duration>` (Allow push to its refs for a time)
// or `--add` (Add them to the working set): working-set entries per ref, in place of a command
// grant (GB-D26).
func approvePerRef(stores []*brokerstore.Store, source, id string, d brokerstore.Decision, add bool, out, errs io.Writer) int {
	if source == ghbroker.Source {
		return approveGitHubPerResource(perRefStore(stores, source), id, d, add, out, errs)
	}
	if d.Session {
		fmt.Fprintf(errs, "yolo approve: --for session is not available for git pushes, which allow a duration of at most 24h. Next: run `yolo approve %s --for 15m`.\n", termsafe.Visible(id))
		return approveExitPolicy
	}
	d.Add = add
	entries, err := perRefStore(stores, source).DecideWorking(id, d)
	if err != nil {
		return approveFailure(err, errs)
	}
	var refs []string
	for _, e := range entries {
		refs = append(refs, termsafe.Visible(e.Resource))
	}
	e := entries[0]
	if add {
		fmt.Fprintf(out, "Allowed %s: added %s to the working set of %s, across restarts, until the branch is deleted or the entry is revoked (%s). Run `yolo approve` to list or `yolo approve --revoke %s` to revoke.\n",
			termsafe.Visible(id), strings.Join(refs, ", "), termsafe.Visible(e.Workspace), e.ID, e.ID)
		return 0
	}
	fmt.Fprintf(out, "Allowed %s: pushes to %s for every jail of %s until %s, across restarts (%s). Run `yolo approve` to list or `yolo approve --revoke %s` to end it.\n",
		termsafe.Visible(id), strings.Join(refs, ", "), termsafe.Visible(e.Workspace), e.Expires.Format(time.RFC3339), e.ID, e.ID)
	return 0
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

// ErrUnknownID, or the last unknown-id error when none holds it.
func eachStore(stores []*brokerstore.Store, fn func(*brokerstore.Store) error) error {
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
func mergedState(stores []*brokerstore.Store, snaps []brokerstore.Snapshot) brokerstore.StateView {
	v := brokerstore.StateView{Schema: brokerstore.Schema, Requests: []brokerstore.RequestView{}, Grants: []brokerstore.GrantView{}, Working: []brokerstore.WorkingView{}}
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

// approveWatch is `--watch`: every store's current state, shown once, then each event, until
// ctx ends. It reads every store before showing anything, so `--json` opens with exactly one
// "state" object holding all of them, then follows each store from the snapshot it read
// (brokerstore.Store.WatchFrom), each polled on its own (brokerstore.Store.Watch says why).
func approveWatch(ctx context.Context, stores []*brokerstore.Store, asJSON bool, out, errs io.Writer) int {
	snaps := make([]brokerstore.Snapshot, len(stores))
	for i, s := range stores {
		snap, err := s.Snapshot()
		if err != nil {
			return approveFailure(err, errs)
		}
		snaps[i] = snap
	}
	if asJSON {
		v := withConfigBranches(ctx, stores, mergedState(stores, snaps))
		v.Event = "state"
		if code := approveJSON(out, v, errs); code != 0 {
			return code
		}
	} else {
		for i, s := range stores {
			printApproveList(out, snaps[i])
			printWorkingSet(out, s, snaps[i])
			printConfigBranches(out, storeConfigBranches(ctx, s))
		}
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
