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
// (docs/design/boundary-broker.md §5.6; loopholedecl/brokered.go): read the workspace's
// remotes at every FRESH launch that starts the loophole, put them in front of a human in
// the config-change gate, and hand the approved list to the daemon in this launch's scope
// file. An attach does none of it: it reads no remotes and writes nothing, so a remote
// added mid-session changes nothing until the next fresh launch shows it.

// brokeredToStart is the set of brokered loopholes this launch will start — the SAME
// predicate the spawn applies (loopholes.Set.BrokeredToStart over the backend's allow), so
// the gate asks about a scope exactly when a broker will be handed one.
func (o *Options) brokeredToStart(rt string, cfg *jsonx.OrderedMap) []*loopholes.Loophole {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	return set.BrokeredToStart(o.loopholeAllow(rt, cfg))
}

// brokeredScopeCheck reads each brokered loophole's remotes for the gate. nil when no broker
// starts, which leaves the gate exactly what it was before the scope part existed.
func (o *Options) brokeredScopeCheck(rt string, cfg *jsonx.OrderedMap) *config.ScopeCheck {
	lps := o.brokeredToStart(rt, cfg)
	if len(lps) == 0 {
		return nil
	}
	check := &config.ScopeCheck{}
	for _, lp := range lps {
		read := brokerscope.ReadRemotes(o.Workspace, lp.Brokered.RemoteHost)
		if read.Problem != "" {
			// Said at the gate, whether or not the approved scope differs: a scope that reads
			// empty because of a malformed worktree pointer is not the same as no remote. The
			// problem can name a path the agent chose, so its markup is escaped (its control
			// characters already are: brokerscope.Read).
			o.pr(o.Stdout).print("[yellow]" + lp.Name + ": " + richtext.Escape(read.Problem) + "[/yellow]")
		}
		check.Sources = append(check.Sources, config.ScopeSource{
			Source: lp.Brokered.Source, Label: lp.Name, Read: read})
	}
	return check
}

// recordApprovedScopes keeps what the gate just approved, per loophole, for the spawn.
func (o *Options) recordApprovedScopes(check *config.ScopeCheck) {
	if check == nil {
		return
	}
	o.approvedScopes = map[string][]string{}
	for _, s := range check.Sources {
		o.approvedScopes[s.Label] = config.ApprovedScope(o.Workspace, s.Source)
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
// FAIL CLOSED: a loophole whose gate did not record an approved scope — a spawn path that
// skipped the gate — gets none of the workspace's remotes, so its broker runs only commands
// that name no repository and any the widening entry admits, and the launch says so.
//
// THE WIDENING ENTRY (BB-D33) is the user config's, read here at the fresh launch and never at
// an attach, which writes no scope file. It needs no approval, user config never prompting, so
// it is no part of the gate the fail-closed rule guards; it is disclosed instead, on its own
// line, naming only what it adds beyond the approved remotes.
func (o *Options) writeScopeFiles(cname string, brokered []*loopholes.Loophole) {
	out := o.pr(o.Stdout)
	for _, lp := range brokered {
		brokerscope.Sweep(lp.Brokered.Source)
		repos, approved := o.approvedScopes[lp.Name]
		if !approved {
			out.print("[yellow]" + lp.Name + ": no repository scope was approved for this launch, " +
				"so its scope holds none of this workspace's remotes[/yellow]")
		}
		widened := beyond(config.BrokeredWidening(lp.Brokered.Source, o.Workspace), repos)
		id, err := brokerscope.NewLaunchID()
		if err != nil {
			out.print("[yellow]" + lp.Name + ": cannot draw a launch id: " + err.Error() + "[/yellow]")
			continue
		}
		path, err := brokerscope.Write(brokerscope.File{Source: lp.Brokered.Source, LaunchID: id,
			PID: os.Getpid(), Container: cname, Workspace: o.Workspace, Repos: repos, Widened: widened})
		if err != nil {
			out.print("[yellow]" + lp.Name + ": cannot write this launch's scope file: " + err.Error() +
				" — the loophole will not start[/yellow]")
			continue
		}
		if o.scopeFiles == nil {
			o.scopeFiles = map[string]string{}
		}
		o.scopeFiles[lp.Name] = path
		// The disclosure of what the broker starts with (§5.6). Every name printed is
		// OWNER/REPO, checked by brokerscope.ValidRepo, so none carries markup.
		switch {
		case len(repos) == 0 && len(widened) == 0:
			out.printf("[dim]%s: this workspace has no approved remote on %s, so its repository scope "+
				"is empty; only commands that name no repository run[/dim]", lp.Name, lp.Brokered.RemoteHost)
		case len(repos) == 0:
			out.printf("[dim]%s: this workspace has no approved remote on %s[/dim]", lp.Name, lp.Brokered.RemoteHost)
		default:
			out.printf("[dim]%s: scope for this workspace: %s[/dim]", lp.Name, strings.Join(repos, ", "))
		}
		if len(widened) > 0 {
			out.printf("%s: scope widened by user config: %s", lp.Name, strings.Join(widened, ", "))
		}
	}
}

// beyond is widened less every repository already in approved, compared as GitHub compares
// names, without case: what the widening entry adds, which is what the scope file's `widened`
// list and the disclosure name.
func beyond(widened, approved []string) []string {
	have := map[string]bool{}
	for _, r := range approved {
		have[strings.ToLower(r)] = true
	}
	var out []string
	for _, r := range widened {
		if !have[strings.ToLower(r)] {
			out = append(out, r)
		}
	}
	return out
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
