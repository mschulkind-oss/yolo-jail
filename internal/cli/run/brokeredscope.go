package run

import (
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// brokeredscope.go is the launch's half of a brokered loophole's repository scope
// (docs/design/boundary-broker.md §5.6; docs/design/workspace-widening.md; loopholedecl/brokered.go):
// read the workspace's remotes and its `brokered.<source>.repos` entry at every FRESH launch that
// starts the loophole, put them in front of a human in the config-change gate, and hand what the
// gate approved to the daemon in this launch's scope file. An attach does none of it: it reads
// nothing and writes nothing, so a remote added or an entry edited mid-session changes nothing
// until the next fresh launch shows it.

// brokeredToStart is the set of brokered loopholes this launch will start — the SAME
// predicate the spawn applies (loopholes.Set.BrokeredToStart over the backend's allow), so
// the gate asks about a scope exactly when a broker will be handed one.
func (o *Options) brokeredToStart(rt string, cfg *jsonx.OrderedMap) []*loopholes.Loophole {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	return set.BrokeredToStart(o.loopholeAllow(rt, cfg))
}

// brokeredScopeCheck reads each brokered loophole's scope for the gate, through the helper
// `yolo check` shares (config.NewScopeCheck): its remotes, and its entry from the gate's own read
// of the workspace config. nil when no broker starts, which leaves the gate exactly what it was
// before the scope part existed.
func (o *Options) brokeredScopeCheck(rt string, cfg *jsonx.OrderedMap, read *config.WorkspaceRead) *config.ScopeCheck {
	lps := o.brokeredToStart(rt, cfg)
	if len(lps) == 0 {
		return nil
	}
	brokers := make([]config.ScopeBroker, 0, len(lps))
	for _, lp := range lps {
		brokers = append(brokers, config.ScopeBroker{Source: lp.Brokered.Source, Label: lp.Name,
			RemoteHost: lp.Brokered.RemoteHost})
	}
	check := config.NewScopeCheck(o.Workspace, brokers, read)
	for _, s := range check.Sources {
		if s.Read.Problem != "" {
			// Said at the gate, whether or not the approved scope differs: a scope that reads
			// empty because of a malformed worktree pointer is not the same as no remote. The
			// problem can name a path the agent chose, so its markup is escaped (its control
			// characters already are: brokerscope.Read).
			o.pr(o.Stdout).print("[yellow]" + s.Label + ": " + richtext.Escape(s.Read.Problem) + "[/yellow]")
		}
	}
	return check
}

// recordApprovedScopes keeps what the gate just approved, per loophole, for the spawn: the
// gate's own in-memory result (WW-P2), never a re-read of the approval record, which a
// concurrent macos-user session can write between this gate and the spawn.
func (o *Options) recordApprovedScopes(approved map[string][]config.ScopeRepo) {
	if approved != nil {
		o.approvedScopes = approved
	}
}

// writeScopeFiles writes this launch's scope file for every brokered loophole about to
// spawn (BB-D32), BEFORE the spawn, since the daemon's argv names the file. It collects the
// files of launches known gone first, never by age.
//
// brokered is the caller's decision, and the whole of it: the loopholes the spawn will
// start (loopholes.Set.BrokeredToStart, less any the placement rule refused). This function
// filters nothing, so it cannot drift from the predicate the gate asked with.
//
// THE FILE HOLDS WHAT THIS LAUNCH'S GATE APPROVED, and nothing else (WW-P2, WW-D12): the
// remotes and the entry alike, from the gate's in-memory result. Nothing here reads a config
// file or the approval record, so an edit landing after the y waits for the next fresh launch.
//
// FAIL CLOSED: a loophole whose gate did not record an approved scope — a spawn path that
// skipped the gate — gets no repository at all, so its broker runs only commands that name
// none, and the launch says so.
func (o *Options) writeScopeFiles(cname string, brokered []*loopholes.Loophole) {
	out := o.pr(o.Stdout)
	// The names of the two workspace config files the loader reads, for the broker's
	// out-of-scope refusal to name exactly (WW-D23). A name, never a path, and no config read.
	_, configName := config.ResolveWorkspaceConfigPath(o.Workspace, config.WorkspaceConfigName)
	_, localName := config.ResolveWorkspaceConfigPath(o.Workspace, config.WorkspaceLocalConfigName)
	for _, lp := range brokered {
		brokerscope.Sweep(lp.Brokered.Source)
		approvedRepos, approved := o.approvedScopes[lp.Name]
		if !approved {
			out.print("[yellow]" + lp.Name + ": no repository scope was approved for this launch, " +
				"so its scope holds no repository[/yellow]")
		}
		repos := make([]string, len(approvedRepos))
		for i, r := range approvedRepos {
			repos[i] = r.Repo
		}
		id, err := brokerscope.NewLaunchID()
		if err != nil {
			out.print("[yellow]" + lp.Name + ": cannot draw a launch id: " + err.Error() + "[/yellow]")
			continue
		}
		path, err := brokerscope.Write(brokerscope.File{Source: lp.Brokered.Source, LaunchID: id,
			PID: os.Getpid(), Container: cname, Workspace: o.Workspace, Repos: repos,
			ConfigFile: configName, LocalFile: localName})
		if err != nil {
			out.print("[yellow]" + lp.Name + ": cannot write this launch's scope file: " + err.Error() +
				" — the loophole will not start[/yellow]")
			continue
		}
		if o.scopeFiles == nil {
			o.scopeFiles = map[string]string{}
		}
		o.scopeFiles[lp.Name] = path
		// The disclosure of what the broker starts with (§5.6): every repository, and where it
		// came from. A source label can name a file the agent chose, already free of control
		// characters (config.ScopeRepo), and its markup is escaped here.
		if len(approvedRepos) == 0 {
			out.printf("[dim]%s: this workspace has no approved remote on %s and no approved `%s` "+
				"entry, so its repository scope is empty; only commands that name no repository run[/dim]",
				lp.Name, lp.Brokered.RemoteHost, config.BrokeredReposKey(lp.Brokered.Source))
			continue
		}
		shown := make([]string, len(approvedRepos))
		for i, r := range approvedRepos {
			shown[i] = r.Repo
			if len(r.Sources) > 0 {
				shown[i] += " (" + strings.Join(r.Sources, ", ") + ")"
			}
		}
		out.printf("[dim]%s: scope for this workspace: %s[/dim]", lp.Name, richtext.Escape(strings.Join(shown, ", ")))
	}
}

// scopeTokenArg substitutes a loophole's scope file into one argv word, reporting false
// when the word names the token and this launch wrote no file for the loophole.
func (o *Options) scopeTokenArg(name, word string) (string, bool) {
	if !strings.Contains(word, loopholedecl.TokenRepositoryScope) {
		return word, true
	}
	path, ok := o.scopeFiles[name]
	if !ok {
		return word, false
	}
	return strings.ReplaceAll(word, loopholedecl.TokenRepositoryScope, path), true
}

// removeScopeFile is a brokered loophole's teardown half: the scope file goes with the
// daemon it was written for.
func (o *Options) removeScopeFile(name string) {
	if path, ok := o.scopeFiles[name]; ok {
		_ = os.Remove(path)
		delete(o.scopeFiles, name)
	}
}
