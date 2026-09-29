package check

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// durableCheckBudget bounds the whole section's size walks: each item gets the launch line's
// own budget, and none starts once this much has gone.
const durableCheckBudget = 10 * time.Second

// sectionDurableDir is the fuller view the maintainer's OQ-DS2 ruling asked for beside the
// launch line: every durable worktree with its size, idle time, branch, unique commits and
// changed files, the other entries, and the registrations that point at nothing
// (docs/design/durable-scratch-space.md §5.4). It prints NOTHING when the workspace has no
// durable dir, so a check of a workspace that never launched reads as it always did.
//
// THE TWO FRAMES COMPUTE DIFFERENT COLUMNS (DS-D3). In a jail every column is computed, git
// included. On the host the size, idle and branch columns are read from metadata and git's
// admin files beneath an os.Root, and unique commits and changed files print "in-jail only":
// the workspace's .git/config is jail-writable, and git runs programs a config names, so host
// yolo runs no git there.
func (o *Options) sectionDurableDir(r *reporter, workspace string) {
	inJail := o.inJail()
	dir := durable.HostPath(workspace)
	// Opened the one way that refuses a link at `.yolo` as well as at `durable`: the jail can
	// replace the workspace's `.yolo` with a link to any host directory, and a by-path open
	// would have this section list and size that directory on the host's terminal.
	root, err := durable.Open(workspace)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		r.sectionHeader("Durable dir")
		r.line("  " + dir + "  not read: " + durable.Reason(err, workspace))
		r.blank()
		return
	}
	root.Close()
	// A worktree made in a container jail records /workspace/…, and one made on the host
	// records the host's path; both name this workspace. YOLO_HOST_DIR is the host's spelling
	// of THIS jail's workspace only, so it applies only when the workspace checked is the
	// one the jail exported the durable dir of — not a nested workspace checked from in here.
	aliases := map[string]string{"/workspace": workspace}
	ownWorkspace := inJail && o.Getenv(durable.EnvVar) == dir
	if host := o.Getenv("YOLO_HOST_DIR"); ownWorkspace && host != "" && host != workspace {
		aliases[host] = workspace
	}
	opts := durable.ScanOptions{Workspace: workspace, Aliases: aliases}
	if inJail {
		// Only here, where the agent's own filesystem answers, and only below the workspace
		// and the per-launch set: a worktree the host made beside the repository is invisible
		// from a jail, not gone.
		opts.GoneRoots = append([]string{workspace}, paths.ScratchDests()...)
	}
	sc := durable.ScanDir(opts)
	start := o.Now()
	budgetLeft := func() bool { return o.Now().Sub(start) < durableCheckBudget }

	r.sectionHeader("Durable dir")
	total, err := durable.Measure(workspace, durable.WalkBudget, o.Now)
	summary := []string{durableSizeCell(total, err)}
	switch {
	case sc.NotGit:
		summary = append(summary, durableCount(sc.WorktreeDirs, "directory", "directories")+" under worktrees/ (not a git repository)")
	default:
		summary = append(summary, durableCount(len(sc.Worktrees), "worktree", "worktrees"))
	}
	if len(sc.Others) > 0 {
		summary = append(summary, durableCount(len(sc.Others), "other entry", "other entries"))
	}
	r.line("  " + dir + "  " + strings.Join(summary, ", "))

	for _, w := range sc.Worktrees {
		// ONE BUDGET FOR THE WHOLE SECTION, git included: each git call may take its own
		// timeout, so without this a check grew by two timeouts per worktree.
		size, commits, changed := "not measured", "in-jail only", "in-jail only"
		if inJail {
			commits, changed = "not computed", "not computed"
		}
		if budgetLeft() {
			sz, err := durable.MeasureRel(workspace, w.Rel, durable.WalkBudget, o.Now)
			size = durableSizeCell(sz, err)
		}
		if inJail && budgetLeft() {
			commits = o.durableUniqueCommits(w, o.durableGitTimeout(start))
		}
		if inJail && budgetLeft() {
			changed = o.durableChangedFiles(w, o.durableGitTimeout(start))
		}
		idle := "idle unknown"
		if !w.LastActive.IsZero() {
			idle = "idle " + durable.HumanIdle(o.Now().Sub(w.LastActive))
		}
		lock := ""
		if w.Locked {
			lock = ", locked"
		}
		r.line(fmt.Sprintf("  %s  %s  %s  %s%s  %s  %s", w.RelName(), size, idle, w.Describe(), lock, commits, changed))
	}
	if sc.Capped {
		r.line("  (the repository has more worktree registrations than one check reads; these are the first by name)")
	}
	if len(sc.Others) > 0 {
		r.line("  other entries  " + durableList(sc.Others))
	}
	if len(sc.Prunable) > 0 {
		r.line(fmt.Sprintf("  prunable registrations  %d  %s — `git worktree prune -n -v` lists what a prune would drop",
			len(sc.Prunable), durableList(sc.Prunable)))
	}
	r.line("  yolo deletes nothing here. " + durable.RemoveAdvice)
	r.blank()
}

// durableShown bounds how many names one line of the section lists.
const durableShown = 20

func durableList(names []string) string {
	if len(names) <= durableShown {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:durableShown], ", ") + fmt.Sprintf(", … (%d more)", len(names)-durableShown)
}

// durableGitTimeout is one git call's timeout: 10 s, or what is left of the section's budget
// if that is less, so the last call cannot overrun it by a full timeout.
func (o *Options) durableGitTimeout(start time.Time) time.Duration {
	left := durableCheckBudget - o.Now().Sub(start)
	if left < 10*time.Second {
		return left
	}
	return 10 * time.Second
}

func durableSizeCell(s durable.Size, err error) string {
	switch {
	case err != nil:
		return "size unknown"
	case s.Partial:
		return "≥ " + durable.HumanBytes(s.Bytes) + " (walk stopped)"
	}
	return durable.HumanBytes(s.Bytes) + " measured"
}

func durableCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// durableUniqueCommits is a coined column (docs/design/durable-scratch-space.md §5.4): the
// commits reachable from the worktree's HEAD and from no other local branch and no
// remote-tracking ref — what deleting the worktree AND its branch would lose. In-jail only.
func (o *Options) durableUniqueCommits(w durable.Worktree, timeout time.Duration) string {
	argv := []string{"git", "-C", w.Path, "rev-list", "--count", "HEAD", "--not"}
	if w.Branch != "" {
		argv = append(argv, "--exclude="+w.Branch)
	}
	argv = append(argv, "--branches", "--remotes")
	res := o.Exec(argv, "", nil, timeout)
	n, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if !res.Ran || res.RC != 0 || err != nil {
		return "unique commits unknown"
	}
	return durableCount(n, "unique commit", "unique commits")
}

// durableChangedFiles is the line count of `git status --porcelain` in the worktree.
func (o *Options) durableChangedFiles(w durable.Worktree, timeout time.Duration) string {
	res := o.Exec([]string{"git", "-C", w.Path, "status", "--porcelain"}, "", nil, timeout)
	if !res.Ran || res.RC != 0 {
		return "changes unknown"
	}
	n := 0
	for _, l := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	if n == 0 {
		return "clean"
	}
	return durableCount(n, "changed file", "changed files")
}
