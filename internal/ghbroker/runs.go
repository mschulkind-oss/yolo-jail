package ghbroker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// runs.go is the `runs` user grant (docs/design/github-write-scopes.md GW-D14, GW-D78): a grant
// naming a workflow file, or `*`, on a repository lets the broker, unasked,
//
//   - run that workflow on the repository's default branch (dispatch.go), and on no other ref;
//   - re-run a run of that workflow (`gh run rerun <id>`, with --failed, --job or --debug) when
//     the broker's own read of the run shows its head is the default branch of the repository
//     itself: a re-run executes the workflow file of the run's own commit, so a run of another
//     branch, or of a fork, could run code no one put on the default branch;
//   - cancel any run in the repository (`gh run cancel <id>`): cancelling executes no code, so
//     any runs grant on the repository covers it, whatever workflow it names.
//
// Every other workflow command (workflow enable and disable, run delete, a dispatch or re-run
// on another branch, the same writes spelled through `gh api` other than a dispatch) is not
// covered and falls through to a timed grant or Allow once, as any repository-level write does.

// GrantRuns is the user grant kind for running, re-running and cancelling workflow runs, as
// packs/github declares it; a covered command is audited under the set of the same name.
const GrantRuns = brokerscope.KindRuns

// The commands a runs grant can cover.
const (
	runsDispatch = "dispatch"
	runsRerun    = "rerun"
	runsCancel   = "cancel"
)

// runsAction is one command a runs grant can cover, as the canonical argv asks for it.
type runsAction struct {
	Op       string           // runsDispatch, runsRerun or runsCancel
	Dispatch workflowDispatch // for runsDispatch
	RunID    string           // for runsRerun and runsCancel: the run the argv names, "" for a re-run by --job
	JobID    string           // for runsRerun by --job: the job whose run it re-runs
}

// runsOf is the command the canonical argv asks for that a runs grant can cover, ok false for
// anything else: a dispatch dispatchOf reads; `gh run rerun <run-id>` or `gh run rerun --job
// <job-id>` (exactly one of them, numeric); or `gh run cancel <run-id>`, numeric. A command
// naming no run, which gh would prompt for, is not one.
func runsOf(argv []string, t writeTarget) (runsAction, bool) {
	if w, ok := dispatchOf(argv, t); ok {
		return runsAction{Op: runsDispatch, Dispatch: w}, true
	}
	if t.Kind != targetRepo || t.Repo == "" {
		return runsAction{}, false
	}
	p, err := parseArgv(argv)
	if err != nil || p.cmd == nil {
		return runsAction{}, false
	}
	switch p.cmd.path {
	case "run rerun":
		jobs := p.values("job")
		switch {
		case len(jobs) == 0 && len(p.positionals) == 1 && numericID(p.positionals[0]):
			return runsAction{Op: runsRerun, RunID: p.positionals[0]}, true
		case len(jobs) == 1 && len(p.positionals) == 0 && numericID(jobs[0]):
			return runsAction{Op: runsRerun, JobID: jobs[0]}, true
		}
	case "run cancel":
		if len(p.positionals) == 1 && numericID(p.positionals[0]) {
			return runsAction{Op: runsCancel, RunID: p.positionals[0]}, true
		}
	}
	return runsAction{}, false
}

// runsGrantHeld reports whether grants hold a runs grant on repo (or `*`) naming workflow or
// `*`. An empty workflow matches any runs grant on repo: a cancel, which any grant covers, and
// the look at a re-run before its run is read.
func runsGrantHeld(grants []brokerscope.Grant, repo, workflow string) bool {
	for _, g := range grants {
		if g.Kind == GrantRuns && (g.Repo == brokerscope.AnyRepo || sameRepo(g.Repo, repo)) &&
			(workflow == "" || g.Workflow == brokerscope.AnyWorkflow || g.Workflow == workflow) {
			return true
		}
	}
	return false
}

// runHead is what the broker's own read of a run says about what a re-run would execute.
type runHead struct {
	ID         string // the run's id, as read (a re-run by --job learns it from the job)
	Workflow   string // its workflow's file name in .github/workflows
	HeadBranch string // head_branch
	HeadRepo   string // head_repository.full_name
}

// readRunHead is the broker's own host read of the run a re-run names: repos/{o}/{r}/actions/
// runs/{id}, reached through repos/{o}/{r}/actions/jobs/{id} for a re-run by --job. Any failed or
// unreadable read, or one naming another run, no head branch, no head repository, or a workflow
// file outside .github/workflows, is an error, and the re-run asks.
func (b *Broker) readRunHead(ctx context.Context, repo string, a runsAction) (runHead, error) {
	runID := a.RunID
	if a.JobID != "" {
		var job struct {
			ID    uint64 `json:"id"`
			RunID uint64 `json:"run_id"`
		}
		if err := b.hostRead(ctx, "repos/"+repo+"/actions/jobs/"+a.JobID, &job); err != nil {
			return runHead{}, fmt.Errorf("job %s: %w", a.JobID, err)
		}
		if strconv.FormatUint(job.ID, 10) != a.JobID || job.RunID == 0 {
			return runHead{}, fmt.Errorf("the answer for job %s names job %d of run %d", a.JobID, job.ID, job.RunID)
		}
		runID = strconv.FormatUint(job.RunID, 10)
	}
	var obj struct {
		ID             uint64 `json:"id"`
		Path           string `json:"path"`
		HeadBranch     string `json:"head_branch"`
		HeadRepository *struct {
			FullName string `json:"full_name"`
		} `json:"head_repository"`
	}
	if err := b.hostRead(ctx, "repos/"+repo+"/actions/runs/"+runID, &obj); err != nil {
		return runHead{}, err
	}
	file, inWorkflows := strings.CutPrefix(obj.Path, ".github/workflows/")
	switch {
	case strconv.FormatUint(obj.ID, 10) != runID:
		return runHead{}, fmt.Errorf("the answer names run %d, not %s", obj.ID, runID)
	case obj.HeadBranch == "":
		return runHead{}, fmt.Errorf("the answer names no head branch")
	case obj.HeadRepository == nil || obj.HeadRepository.FullName == "":
		return runHead{}, fmt.Errorf("the answer names no head repository")
	case !inWorkflows || file == brokerscope.AnyWorkflow || !brokerscope.ValidGrantWorkflow(file):
		return runHead{}, fmt.Errorf("the run's workflow %q is not a file in .github/workflows", obj.Path)
	}
	return runHead{ID: runID, Workflow: file, HeadBranch: obj.HeadBranch, HeadRepo: obj.HeadRepository.FullName}, nil
}

// defaultBranch is the repository's default branch, read by the broker's own host read of
// repos/{o}/{r}. A name the broker does not read plainly is an error.
func (b *Broker) defaultBranch(ctx context.Context, repo string) (string, error) {
	var repoObj struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := b.hostRead(ctx, "repos/"+repo, &repoObj); err != nil {
		return "", err
	}
	if !plainBranchName(repoObj.DefaultBranch) {
		return "", fmt.Errorf("the default branch's name %q is not one the broker reads plainly", repoObj.DefaultBranch)
	}
	return repoObj.DefaultBranch, nil
}

// runsCovered is the check a held runs grant needs before it covers the command, past the look
// at the grant (userGrantCovers): a dispatch must run on the default branch and is pinned to it;
// a re-run's run, read by the broker, must have the default branch of the repository itself as
// its head and be of a workflow a runs grant names; a cancel needs nothing more. It returns the
// argv that runs, the workflow the transaction re-checks the grant against ("" for a cancel),
// and true; or says on stderr why the grant does not cover the command, which then asks.
func (b *Broker) runsCovered(ctx context.Context, t writeTarget, argv []string, stderr func([]byte)) ([]string, string, bool) {
	a, ok := runsOf(argv, t)
	if !ok {
		return nil, "", false
	}
	switch a.Op {
	case runsCancel:
		return argv, "", true
	case runsDispatch:
		w := a.Dispatch
		def, onDefault, err := b.onDefaultBranch(ctx, t.Repo, w)
		switch {
		case err != nil:
			stderr([]byte("github-broker: could not read " + t.Repo + "'s default branch (" + err.Error() +
				"), so this workspace's runs grant does not cover this run, which asks\n"))
			return nil, "", false
		case !onDefault:
			stderr([]byte("github-broker: this workspace's runs grant covers a run of " + w.Workflow +
				" on " + t.Repo + "'s default branch (" + def + ") only; this run on " + w.Ref + " asks\n"))
			return nil, "", false
		}
		pinned, pinOK := pinnedDispatchArgv(argv, t, def)
		if !pinOK {
			stderr([]byte("github-broker: could not pin this run of " + w.Workflow + " to refs/heads/" + def +
				", so this workspace's runs grant does not cover it, which asks\n"))
			return nil, "", false
		}
		return pinned, w.Workflow, true
	}
	named := "run " + a.RunID
	if a.JobID != "" {
		named = "the run of job " + a.JobID
	}
	run, err := b.readRunHead(ctx, t.Repo, a)
	if err != nil {
		stderr([]byte("github-broker: could not read " + named + " on " + t.Repo + " (" + err.Error() +
			"), so this workspace's runs grant does not cover re-running it, which asks\n"))
		return nil, "", false
	}
	if a.JobID != "" {
		named = "run " + run.ID + " (of job " + a.JobID + ")"
	}
	if !strings.EqualFold(run.HeadRepo, t.Repo) {
		stderr([]byte("github-broker: " + named + "'s head is in " + strconv.Quote(run.HeadRepo) + ", not " + t.Repo +
			" itself; a runs grant never covers re-running a fork's run, so this re-run asks\n"))
		return nil, "", false
	}
	def, err := b.defaultBranch(ctx, t.Repo)
	if err != nil {
		stderr([]byte("github-broker: could not read " + t.Repo + "'s default branch (" + err.Error() +
			"), so this workspace's runs grant does not cover re-running " + named + ", which asks\n"))
		return nil, "", false
	}
	if run.HeadBranch != def {
		stderr([]byte("github-broker: " + named + " ran on " + strconv.Quote(run.HeadBranch) + ", not " + t.Repo +
			"'s default branch (" + def + "); a runs grant re-runs a run of the default branch only, so this re-run asks\n"))
		return nil, "", false
	}
	if _, held := b.userGrantCovers(t, argv, run.Workflow); !held {
		stderr([]byte("github-broker: " + named + " is a run of " + run.Workflow + ", which no runs grant of this workspace names, " +
			"so re-running it asks (`yolo request github runs " + run.Workflow + "`, then `yolo config apply` on the host, grants it)\n"))
		return nil, "", false
	}
	return argv, run.Workflow, true
}
