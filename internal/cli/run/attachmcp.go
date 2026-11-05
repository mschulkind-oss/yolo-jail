package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// runningTreeExcludesMCPTeardown is only a negative fallback when the keeper's
// start record is unavailable. An unrecorded or incomplete running tree cannot
// establish what its keeper started. Never substitute this entry's staged packs.
func runningTreeExcludesMCPTeardown(cname, rt string) bool {
	dir, _ := runningJailPackTree(cname, rt)
	if dir == "" {
		return false
	}
	_, recorded, err := packload.ReadPackTreeRecord(dir)
	if err != nil || !recorded {
		return false
	}
	packs, err := loadPackTree(dir)
	if err != nil {
		return false
	}
	for _, pack := range packs {
		mods, refusals, warnings := pack.LoopholeModules()
		if len(refusals) != 0 || len(warnings) != 0 {
			return false
		}
		for _, mod := range mods {
			if mod.Decl == nil || mod.Name == "mcp-auth-broker" ||
				(mod.Decl.HostDaemon != nil && mod.Decl.HostDaemon.LaunchInput == "mcp-auth-share") {
				return false
			}
		}
	}
	return true
}

// keeperRecordOwnerMatches binds both selected and known-off facts to the
// current kernel process, not merely to a reusable PID or a clock in a JSON file.
func keeperRecordOwnerMatches(cname string, rec keeperRecord) bool {
	pid, known := readOwnerPID(cname)
	return known && pid == rec.PID && rec.Birth != "" && rec.Birth == keeperProcessBirth(pid)
}

// captureAttachedMCPTeardown pins the current keeper's selected outcome before
// this attach executes an agent. The result ID is an observation, never a grant.
func (o *Options) captureAttachedMCPTeardown(cname, rt string) bool {
	rec, ok := readKeeperRecord(cname)
	if ok && rec.Workspace == o.Workspace && rec.Runtime == rt && keeperRecordOwnerMatches(cname, rec) {
		if rec.MCPTeardownID == "" {
			if !rec.MCPTeardownRequired && rec.MCPTeardownKnown {
				return true
			}
		} else if (!rec.MCPTeardownKnown || rec.MCPTeardownRequired) &&
			mcpTeardownOutcomePath(cname, rec.MCPTeardownID) != "" {
			if err := o.registerMCPOutcomeReader(cname, rec.MCPTeardownID, rt); err != nil {
				o.pr(o.Stderr).printf("[bold red]Refusing to attach to %s: cannot register a selected MCP teardown reader (%v). Repair its private owner state, then relaunch fresh.[/bold red]", cname, err)
				return false
			}
			o.mcpTeardownID = rec.MCPTeardownID
			return true
		}
	}
	if !ok && probeKeeper(cname) == keeperGone {
		// A pre-keeper legacy jail has neither a live keeper nor keeper-era
		// records. It never started a selected keeper teardown to confirm.
		if _, era := o.keeperEra(cname); !era {
			return true
		}
	}
	// A record from an older generation, an unreadable one, or an incomplete
	// current record says nothing positive. Only the frozen running tree can
	// independently prove absence; current configured packs cannot do so.
	if runningTreeExcludesMCPTeardown(cname, rt) {
		return true
	}
	o.pr(o.Stderr).printf("[bold red]Refusing to attach to %s: the running keeper's selected MCP teardown requirement and outcome ID cannot be verified.[/bold red]", cname)
	o.pr(o.Stderr).printf("[dim]Restore the running keeper's private start record if it is damaged, or run %s and relaunch the jail fresh; do not rely on its best-effort host service log to confirm final harvest.[/dim]", stopRemedy(rt, cname))
	return false
}
