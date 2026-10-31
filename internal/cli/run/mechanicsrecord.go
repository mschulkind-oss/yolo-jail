package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

const mechanicsRecordFile = "mechanics.json"

type mechanicsAppliedRecord struct {
	Token string                `json:"token"`
	Facts jailcontent.FactSheet `json:"facts"`
}

func mechanicsRecordPath(cname string) string {
	return filepath.Join(filepath.Dir(ctxMountRecordPath(cname)), mechanicsRecordFile)
}

// writeMechanicsApplied saves the effective launch sheet beside the live-mount record, outside
// jail binds. Podman snapshots are trusted only when the inspected token matches both records.
func writeMechanicsApplied(cname, token string, facts *jailcontent.FactSheet) error {
	if token == "" || facts == nil {
		return nil
	}
	body, err := json.Marshal(mechanicsAppliedRecord{Token: token, Facts: *facts})
	if err != nil {
		return err
	}
	dir := filepath.Dir(mechanicsRecordPath(cname))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if err := writeFileBeneathMode(dir, mechanicsRecordFile, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("could not write applied mechanics snapshot: %w", err)
	}
	return nil
}

func readMechanicsApplied(cname string, envLines []string) (*jailcontent.FactSheet, bool) {
	token := envLineValue(envLines, ctxMountsTokenEnv)
	if token == "" {
		return nil, false
	}
	body, err := os.ReadFile(mechanicsRecordPath(cname))
	if err != nil {
		return nil, false
	}
	var record mechanicsAppliedRecord
	if json.Unmarshal(body, &record) != nil || record.Token != token {
		return nil, false
	}
	return &record.Facts, true
}

func applyLiveMountsToMechanics(facts *jailcontent.FactSheet, cname string, envLines []string) bool {
	entries, ok := readCtxMountRecord(cname, envLines)
	if !ok {
		return false
	}
	// Remove only user context mounts represented by the old authenticated briefing.
	// Pack-owned /ctx rows came from the running pack view and remain effective.
	userPaths := make(map[string]bool)
	mounts := facts.Briefing.ContextMounts[:0]
	for _, mount := range facts.Briefing.ContextMounts {
		if mount.Pack == "" {
			userPaths[mount.Path] = true
			continue
		}
		mounts = append(mounts, mount)
	}
	paths := facts.Paths[:0]
	for _, p := range facts.Paths {
		if userPaths[p.Path] {
			continue
		}
		paths = append(paths, p)
	}
	for _, entry := range entries {
		mode := "read-only"
		if entry.RW {
			mode = "read-write"
		}
		paths = append(paths, jailcontent.PathFact{Path: entry.Dest, HostSource: entry.Source,
			Mode: mode, Lifetime: "host filesystem", Delivery: facts.SkillsDelivery})
		mounts = append(mounts, jailcontent.ContextMount{Path: entry.Dest, Host: entry.Source, ReadWrite: entry.RW})
	}
	facts.Paths = paths
	facts.Briefing.ContextMounts = mounts
	return true
}

// updateMechanicsAfterApply publishes the authenticated applied state after any effective
// provisioned change. Pending outcomes, record-only applies and no-op key summaries do not
// rewrite the running jail's guidance.
func (o *Options) updateMechanicsAfterApply(run *applyRun, changes []applyChange, folded []foldedAppliedKey) {
	if run == nil || !run.mode.running || run.mode.unknown || run.mode.recordOnly != "" || !mechanicsApplyChanged(changes, folded) {
		return
	}
	prior, ok := readMechanicsApplied(run.cname, run.envLines)
	if !ok {
		o.mechanicsRefreshWarning("the applied mechanics snapshot is missing or does not match this jail", "restart the jail, then run `yolo` again")
		return
	}
	facts, ok := readMechanicsApplied(run.cname, run.envLines)
	if !ok {
		o.mechanicsRefreshWarning("the authenticated applied mechanics snapshot could not be reread", "restart the jail, then run `yolo` again")
		return
	}
	if !applyLiveMountsToMechanics(facts, run.cname, run.envLines) {
		o.mechanicsRefreshWarning("the authenticated live-mount record is unavailable", "restart the jail, then run `yolo` again")
		return
	}
	for _, key := range folded {
		if key.key != "agents_md_extra" {
			continue
		}
		if !key.present {
			facts.AppliedAgentsMDExtra = ""
			continue
		}
		value, ok := key.value.(string)
		if ok {
			facts.AppliedAgentsMDExtra = value
		}
	}
	targets := mechanicsStagingTargets(*facts)
	if len(targets) == 0 {
		return
	}
	if err := writeMechanicsApplied(run.cname, envLineValue(run.envLines, ctxMountsTokenEnv), facts); err != nil {
		o.mechanicsRefreshWarning("the applied mechanics snapshot could not be updated: "+err.Error(), "fix the host state directory, then attach with `yolo` again")
		return
	}
	if err := refreshMechanicsFiles(targets, *facts); err != nil {
		if rollbackErr := writeMechanicsApplied(run.cname, envLineValue(run.envLines, ctxMountsTokenEnv), prior); rollbackErr != nil {
			o.mechanicsRefreshWarning("the applied-state skill could not be refreshed: "+err.Error()+"; the prior snapshot could not be restored: "+rollbackErr.Error(), "fix the named source or staging issue and host state directory, then attach with `yolo` again")
			return
		}
		o.mechanicsRefreshWarning("the applied-state skill could not be refreshed: "+err.Error(), "fix the named source or staging issue, then attach with `yolo` again")
	}
}

var refreshMechanicsFiles = jailcontent.RefreshMechanicsFiles

// mechanicsApplyChanged reports a settled effective change, not merely a desired config key.
// Successful key folds are the producer's proof that the baseline write succeeded. Mount and
// scope operations that ran applied-now are effective even when an unrelated key fold failed;
// their next-session forms also require a successful fold. Key-only mount/scope summaries do
// not prove a live operation.
func mechanicsApplyChanged(changes []applyChange, folded []foldedAppliedKey) bool {
	foldedKeys := make(map[string]bool, len(folded))
	for _, key := range folded {
		foldedKeys[key.key] = true
	}
	for _, change := range changes {
		if change.outcome != outcomeAppliedNow && change.outcome != outcomeNextSession {
			continue
		}
		switch change.kind {
		case changeMountAdd, changeMountRemove, changeScope:
			if change.outcome == outcomeAppliedNow || foldedKeys[change.key] {
				return true
			}
		case changeKey:
			if change.key != "" && change.key != "mounts" && change.key != "brokered" && foldedKeys[change.key] {
				return true
			}
		}
	}
	return false
}

func mechanicsStagingTargets(facts jailcontent.FactSheet) []string {
	seen := make(map[string]bool)
	var targets []string
	for _, path := range facts.Paths {
		if path.Origin != "pack skills staging" || path.HostSource == "" || seen[path.HostSource] {
			continue
		}
		seen[path.HostSource] = true
		targets = append(targets, path.HostSource)
	}
	return targets
}

func (o *Options) mechanicsRefreshWarning(reason, remedy string) {
	o.pr(o.Stderr).printf("[yellow]Warning: %s still describes the prior applied state: %s. %s.[/yellow]",
		jailcontent.BuiltinSkillName(config.ShippedSkillPrefix(), "jail-mechanics"), richtext.Escape(reason), remedy)
}
