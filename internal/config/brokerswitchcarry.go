package config

import (
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// brokerswitchcarry.go is the cleanup of a STALE BROKER SWITCH (docs/design/legacy-config-compat.md
// LC-D7, OQ-LC2): the `loopholes.<name>.enabled` an older yolo's `yolo loopholes enable` wrote into
// the per-workspace file (workspacefile.go) for a brokered loophole, which has had no switch since
// BB-D106. The file is yolo's own, so yolo cleans it up rather than asking the user to: the launch,
// `yolo check` and `yolo config apply` all validate the config, and the validator takes the switch
// out through the writer `yolo loopholes disable` uses (RemoveWorkspaceLoophole) and says so once.
//
// What replaces a switch that was on is the read grant it gave, for that workspace alone: the
// approval record's grant part carries it, under carriedBrokerRecordKey, the way `yolo config
// migrate` carries every other loophole's switch (loopholegrantcarried.go), and both scope
// producers read it beside the user config's own grants (NewScopeCheck, readableScopeCheck). A
// switch that was off gave nothing, so nothing is carried for it.

// carriedBrokerRecordKey is the record's key for the broker grants carried for source:
// `carried:<source>.grants`. It sits under the carried prefix, so a report of the recorded grants
// skips it (RecordedLoopholeGrants), and its `.` is a character no pack name holds, so no grant
// declaration reads it as its own.
func carriedBrokerRecordKey(source string) string {
	return carriedRecordPrefix + source + "." + brokeredGrantsKey
}

// carriedBrokerReadGrant is the read grant a broker switch gave: its workspace's own approved
// remotes, which is what `{"kind": "read", "repo": "*"}` in a user config gives every workspace.
var carriedBrokerReadGrant = brokerscope.Grant{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo}

// carriedBrokerGrantOrigin is where a carried broker grant's gate line says it came from.
const carriedBrokerGrantOrigin = "carried from an older yolo's switch, for this workspace"

// CarriedBrokerGrants is the broker grants workspace's approval record carries for source, from a
// switch an older yolo wrote (carryStaleBrokerSwitch). An entry that does not parse is skipped.
func CarriedBrokerGrants(workspace, source string) []brokerscope.Grant {
	rec, _, err := readLoopholeGrantRecord(workspace)
	if err != nil {
		return nil
	}
	var out []brokerscope.Grant
	for _, s := range rec[carriedBrokerRecordKey(source)] {
		if g, ok := brokerscope.ParseGrant(s); ok {
			out = append(out, g)
		}
	}
	return out
}

// carryBrokerGrant adds grant to workspace's approval record as carried for source.
func carryBrokerGrant(workspace, source string, grant brokerscope.Grant) error {
	rec, _, err := readLoopholeGrantRecord(workspace)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = loopholeGrantRecord{}
	}
	key := carriedBrokerRecordKey(source)
	rec[key] = appendUnique(rec[key], grant.String())
	return writeLoopholeGrantRecord(workspace, rec)
}

// carriedBrokerSwitchNotes is the line said for each switch this process took out, by file and
// loophole, so a later validation in the same process (a launch validates more than once) says it
// again rather than never, should the first pass's warnings not be the ones printed.
var carriedBrokerSwitchNotes sync.Map

// carryStaleBrokerSwitch takes name's switch out of the per-workspace file wsFile, carrying the read
// grant a switch that was on gave into the approval record first, and returns the one line that
// says so; "" when the file holds no switch for it. A file that cannot be rewritten whole keeps the
// switch, and the line names why and what was refused.
func carryStaleBrokerSwitch(name string, broker loopholedecl.Brokered, workspace string, wsFile *WorkspaceFile) string {
	memo := WorkspaceFilePath(workspace) + "\x00" + name
	on, set := wsFile.LoopholeSwitch(name)
	if !set {
		// The file this process emptied is gone, so the line is found by the workspace's path.
		if note, ok := carriedBrokerSwitchNotes.Load(memo); ok {
			return note.(string)
		}
		return ""
	}
	label := pytext.Repr(name)
	if on {
		if err := carryBrokerGrant(wsFile.Workspace, broker.Source, carriedBrokerReadGrant); err != nil {
			return wsFile.Path + ": loopholes." + name + ".enabled is the switch an older yolo wrote, and it stays: " +
				"its read grant could not be recorded for this workspace (" + err.Error() + "), so " +
				label + " grants nothing here until your config does. " + BrokerGrantAdviceFor(broker)
		}
	}
	if _, _, err := RemoveWorkspaceLoophole(wsFile.Workspace, name); err != nil {
		what := "it switched nothing"
		if on {
			what = "this workspace keeps the read grant it gave"
		}
		return wsFile.Path + ": the old " + name + " switch could not be removed, so this file keeps it (" + what +
			"): " + err.Error()
	}
	note := "Removed the old " + name + " switch from " + wsFile.Path + "; "
	if on {
		note += "this workspace keeps the read grant it gave"
	} else {
		note += "it was off, and this workspace holds no grant from it"
	}
	carriedBrokerSwitchNotes.Store(memo, note)
	return note
}
