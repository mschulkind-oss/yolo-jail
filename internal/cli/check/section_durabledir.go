package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
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
	if v := o.Getenv(durable.EnvVar); inJail && v != "" {
		dir = v
		workspace = filepath.Dir(filepath.Dir(v))
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return
	}
	// A worktree made in a container jail records /workspace/…, and one made on the host
	// records the host's path; both name this workspace.
	aliases := map[string]string{"/workspace": workspace}
	if host := o.Getenv("YOLO_HOST_DIR"); inJail && host != "" && host != workspace {
		aliases[host] = workspace
	}
	sc := durable.ScanDir(durable.ScanOptions{Workspace: workspace, Durable: dir, Aliases: aliases, CheckGone: inJail})
	start := o.Now()
	budgetLeft := func() bool { return o.Now().Sub(start) < durableCheckBudget }

	r.sectionHeader("Durable dir")
	total, err := durable.Measure(dir, durable.WalkBudget, o.Now)
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
		size := "not measured"
		if budgetLeft() {
			sz, err := durable.MeasureRel(dir, w.Rel, durable.WalkBudget, o.Now)
			size = durableSizeCell(sz, err)
		}
		idle := "idle unknown"
		if !w.LastActive.IsZero() {
			idle = "idle " + durable.HumanIdle(o.Now().Sub(w.LastActive))
		}
		commits, changed := "in-jail only", "in-jail only"
		if inJail {
			commits, changed = o.durableUniqueCommits(w), o.durableChangedFiles(w)
		}
		lock := ""
		if w.Locked {
			lock = ", locked"
		}
		r.line(fmt.Sprintf("  %s  %s  %s  %s%s  %s  %s", w.Rel, size, idle, w.Describe(), lock, commits, changed))
	}
	if len(sc.Others) > 0 {
		r.line("  other entries  " + strings.Join(sc.Others, ", "))
	}
	if len(sc.Prunable) > 0 {
		r.line(fmt.Sprintf("  prunable registrations  %d  %s — `git worktree prune -n -v` lists what a prune would drop",
			len(sc.Prunable), strings.Join(sc.Prunable, ", ")))
	}
	r.line("  yolo deletes nothing here. Remove one with `git worktree remove <path>` (`-f -f` if it is locked).")
	r.blank()
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
func (o *Options) durableUniqueCommits(w durable.Worktree) string {
	argv := []string{"git", "-C", w.Path, "rev-list", "--count", "HEAD", "--not"}
	if w.Branch != "" {
		argv = append(argv, "--exclude="+w.Branch)
	}
	argv = append(argv, "--branches", "--remotes")
	res := o.Exec(argv, "", nil, 10*time.Second)
	n, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if !res.Ran || res.RC != 0 || err != nil {
		return "unique commits unknown"
	}
	return durableCount(n, "unique commit", "unique commits")
}

// durableChangedFiles is the line count of `git status --porcelain` in the worktree.
func (o *Options) durableChangedFiles(w durable.Worktree) string {
	res := o.Exec([]string{"git", "-C", w.Path, "status", "--porcelain"}, "", nil, 10*time.Second)
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
