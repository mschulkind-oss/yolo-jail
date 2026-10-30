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
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	lps := set.BrokeredToStart(o.loopholeAllow(rt, cfg))
	if len(lps) == 0 {
		return nil
	}
	brokers := make([]config.ScopeBroker, 0, len(lps))
	for _, lp := range lps {
		brokers = append(brokers, config.ScopeBroker{Source: lp.Brokered.Source, Label: lp.Name,
			RemoteHost: lp.Brokered.RemoteHost, GrantKinds: lp.Brokered.Grants, Pack: lp.Pack,
			Brokered: *lp.Brokered, Settings: lp.Settings})
	}
	selected := make([]config.ReadablePackDeclaration, 0)
	selectedBrokers := make([]config.ReadableSelectedBroker, 0)
	for _, lp := range set.All() {
		if lp.Source == loopholes.SourcePack {
			selected = append(selected, config.ReadablePackDeclaration{Pack: lp.Pack, Settings: lp.Settings})
			if lp.Brokered != nil && lp.Brokered.ReadCapability != "" {
				selectedBrokers = append(selectedBrokers, config.ReadableSelectedBroker{Pack: lp.Pack, Label: lp.Name, Brokered: *lp.Brokered})
			}
		}
	}
	check := config.NewSelectedScopeCheck(o.Workspace, brokers, read, selected, selectedBrokers)
	for _, s := range check.Sources {
		if s.Read.Problem != "" {
			// Said at the gate, whether or not the approved scope differs: a scope that reads
			// empty because of a malformed worktree pointer is not the same as no remote. The
			// problem can name a path the agent chose, so its markup is escaped (its control
			// characters already are: brokerscope.Read).
			o.pr(o.Stderr).print("[yellow]" + s.Label + ": " + richtext.Escape(s.Read.Problem) + "[/yellow]")
		}
	}
	return check
}

// recordApprovedScopes keeps what the gate just approved, per loophole, for the spawn: the
// gate's own in-memory result (WW-P2), never a re-read of the approval record, which a
// concurrent macos-user session can write between this gate and the spawn. grants are the user
// grants are the user grants the same gate approved (config.ScopeCheck.ApprovedGrants), kept
// beside them, and readWithoutScope the loopholes whose read grant applies over no repository
// (config.ScopeCheck.ReadWithoutScope), for writeScopeFiles' line.
func (o *Options) recordApprovedScopes(approved map[string][]config.ScopeRepo, grants map[string][]brokerscope.Grant,
	readWithoutScope map[string]string) {
	if approved != nil {
		o.approvedScopes = approved
		o.approvedGrants = grants
		o.readWithoutScope = readWithoutScope
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
	out := o.pr(o.Stderr)
	// The names of the two workspace config files the loader reads, for the broker's
	// out-of-scope refusal to name exactly (WW-D23). A name, never a path, and no config read.
	_, configName := config.ResolveReadableWorkspaceConfigPath(o.Workspace, config.WorkspaceConfigName)
	localPath, localName := config.ResolveReadableWorkspaceConfigPath(o.Workspace, config.WorkspaceLocalConfigName)
	// A local file not written yet takes the format the workspace's config is in.
	if _, err := os.Lstat(localPath); err != nil && !strings.HasSuffix(configName, ".toml") {
		localName = config.WorkspaceLocalConfigName
	}
	for _, lp := range brokered {
		brokerscope.Sweep(lp.Brokered.Source)
		// The user-scope permission sets this source runs under are disclosed here (BB-D23): a
		// set decides what a write grant hands over, so it is named on every launch that starts
		// the broker, whether or not the workspace that reads it could see it. Read from the user
		// scope alone; a malformed set is the validator's to report and is skipped here.
		if sets := config.BrokeredSets(lp.Brokered.Source); len(sets) > 0 {
			out.printf("[dim]%s: sets from user config: %s[/dim]", lp.Name,
				richtext.Escape(config.BrokeredSetsDisclosure(sets)))
		}
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
		// The form is the manifest's, as the gate read it (config.NewScopeCheck): a source on any
		// host hands its broker exact remote URLs, which it must not read as OWNER/REPO.
		form := brokerscope.FormFor(lp.Brokered.RemoteHost)
		// The grants the same gate approved (docs/design/git-broker-plan.md ID-10), and none
		// where it approved no scope: the same fail-closed rule as the repositories.
		var grants []brokerscope.Grant
		if approved {
			grants = o.approvedGrants[lp.Name]
		}
		path, err := brokerscope.Write(brokerscope.File{Source: lp.Brokered.Source, LaunchID: id,
			PID: os.Getpid(), Container: cname, Workspace: o.Workspace, Form: form, Repos: repos,
			Grants: grants, ConfigFile: configName, LocalFile: localName})
		if err != nil {
			out.print("[yellow]" + lp.Name + ": cannot write this launch's scope file: " + err.Error() +
				" — the loophole will not start[/yellow]")
			continue
		}
		if o.scopeFiles == nil {
			o.scopeFiles = map[string]string{}
		}
		o.scopeFiles[lp.Name] = path
		// Each grant the broker starts with is disclosed beside its scope, on every launch: a
		// grant is standing access, which a human should see each time it is in force.
		if len(grants) > 0 {
			shown := make([]string, len(grants))
			for i, g := range grants {
				shown[i] = g.String()
			}
			out.printf("[dim]%s: grants for this workspace: %s[/dim]", lp.Name, richtext.Escape(strings.Join(shown, ", ")))
		}
		// A broker with no read grant grants nothing (BB-D108, BB-D110): its gate approved no
		// scope, and every call through it is refused, so the line says that and how to grant.
		if !brokerscope.HoldsRead(grants) {
			// Unless the read grant DOES apply and the workspace names no repository for it: the
			// gate approved nothing because there is nothing to serve, so the line names the
			// missing remote and how to add one, never the grant the user already holds.
			if entryKey, held := o.readWithoutScope[lp.Name]; held && approved {
				if form == brokerscope.FormRemoteURL {
					out.printf("[dim]%s: your read grant applies, but this workspace has no remote, so the "+
						"broker has no repository to serve and every call through it is refused; add one with "+
						"`git remote add`, and the next launch asks about it[/dim]", lp.Name)
				} else {
					out.printf("[dim]%s: your read grant applies, but this workspace has no remote on %s and "+
						"no `%s` entry, so the broker has no repository to serve and every call through it is "+
						"refused; add a remote with `git remote add` (or list the repository under `%s`), and "+
						"the next launch asks about it[/dim]", lp.Name, lp.Brokered.RemoteHost,
						richtext.Escape(entryKey), richtext.Escape(entryKey))
				}
				continue
			}
			out.printf("[dim]%s: grants nothing in this workspace: no read grant applies, so every call "+
				"through it is refused. %s[/dim]", lp.Name, richtext.Escape(config.BrokerGrantAdviceFor(*lp.Brokered)))
			continue
		}
		// The disclosure of what the broker starts with (§5.6): every repository, and where it
		// came from. A source label can name a file the agent chose, already free of control
		// characters (config.ScopeRepo), and its markup is escaped here.
		if len(approvedRepos) == 0 && form == brokerscope.FormRemoteURL {
			out.printf("[dim]%s: this workspace has no approved remote, so its scope is empty; add one "+
				"with `git remote add`, and the next launch asks about it[/dim]", lp.Name)
			continue
		}
		if len(approvedRepos) == 0 {
			out.printf("[dim]%s: this workspace has no approved remote on %s and no approved `%s` "+
				"entry, so its repository scope is empty; only commands that name no repository run[/dim]",
				lp.Name, lp.Brokered.RemoteHost, config.BrokeredReposKey(lp.Brokered.Source))
			continue
		}
		shown := make([]string, len(approvedRepos))
		for i, r := range approvedRepos {
			shown[i] = r.Repo
			// A wildcard entry admits every repository its token covers, so the line says so
			// rather than naming it as one more repository.
			var facts []string
			if n := brokerscope.WildcardNote(r.Repo, lp.Brokered.RemoteHost); n != "" && form == "" {
				facts = append(facts, n)
			}
			if len(r.Sources) > 0 {
				facts = append(facts, strings.Join(r.Sources, ", "))
			}
			if len(facts) > 0 {
				shown[i] += " (" + strings.Join(facts, "; ") + ")"
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
		_ = brokerscope.Remove(path)
		delete(o.scopeFiles, name)
	}
}
