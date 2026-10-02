package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// scopeapproval.go is the approval record's SCOPE PART (docs/design/boundary-broker.md
// §5.6, BB-D30, BB-D31): the repository scope a brokered loophole's daemon is fenced to,
// read from the workspace's remotes at every fresh launch that starts one, approved in the
// same config-change gate and the same y/N as the workspace config.
//
// The remotes are workspace state the agent can edit, which is the case the gate exists
// for (config-safety.md P2), so the scope takes the gate's path rather than a new one. It
// is a SECOND FILE beside the snapshot, not a key inside it, because the snapshot's bytes
// are a frozen contract: one byte of drift re-prompts every workspace on the machine.
//
// IN PLAY ONLY WHERE A BROKER STARTS. A launch or a host `yolo check` whose brokered
// loopholes (loopholes.Set.BrokeredToStart) are none passes a nil ScopeCheck, and then
// this file writes and compares nothing: a workspace whose launches never start a broker
// never gains the scope part and is never asked about one.

// ScopeSource is one brokered loophole's repository scope as a fresh launch read it.
type ScopeSource struct {
	// Source is the approval record's key for this list (`brokered.source`): `github`.
	Source string
	// Label is the loophole that reads it (`github-broker`), naming the block.
	Label string
	// Read is what the launch read from the workspace's remotes.
	Read brokerscope.Read
}

// ScopeCheck is the scope part in play for one gate.
type ScopeCheck struct {
	Sources []ScopeSource
}

func (s *ScopeCheck) inPlay() bool { return s != nil && len(s.Sources) > 0 }

// ApprovalScopePath is the scope part of a workspace's approval record:
// $HOME/.local/share/yolo-jail/approvals/<container-name>.scope.json, beside
// ApprovalSnapshotPath and keyed the same way.
func ApprovalScopePath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(paths.ApprovalsDir(), runtime.FromWorkspace(workspace)+".scope.json")
}

// scopeRecord is the scope part's content: an approved `owner/repo` list per source.
type scopeRecord map[string][]string

// readScopeRecord reads the scope part; exists=false when there is none yet.
func readScopeRecord(path string) (rec scopeRecord, exists bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return scopeRecord{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rec = scopeRecord{}
	if err := json.Unmarshal(data, &rec); err != nil {
		// A scope part that does not decode is a record of nothing, which is the direction
		// to fail: every repository reads as added and the human is asked.
		return scopeRecord{}, false, nil
	}
	return rec, true, nil
}

// scopeRecordJSON serializes the record through SnapshotJSON, sorted keys and sorted lists.
func scopeRecordJSON(rec scopeRecord) (string, error) {
	m := jsonx.NewOrderedMap()
	for _, k := range sortedScopeKeys(rec) {
		list := append([]string(nil), rec[k]...)
		sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i]) < strings.ToLower(list[j]) })
		items := make([]any, 0, len(list))
		for _, r := range list {
			items = append(items, r)
		}
		m.Set(k, items)
	}
	return SnapshotJSON(m)
}

func sortedScopeKeys(rec scopeRecord) []string {
	keys := make([]string, 0, len(rec))
	for k := range rec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sameRepos compares two repository lists as GitHub does: case-insensitively, as sets.
func sameRepos(a, b []string) bool {
	norm := func(l []string) []string {
		out := make([]string, 0, len(l))
		for _, r := range l {
			out = append(out, strings.ToLower(r))
		}
		sort.Strings(out)
		return out
	}
	na, nb := norm(a), norm(b)
	if len(na) != len(nb) {
		return false
	}
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

// scopeOutcome is what comparing the in-play sources against the record found.
type scopeOutcome struct {
	changed bool
	// next is the record to write: the old one with every in-play source's list replaced
	// by what was read. Sources not in play keep their entries.
	next  scopeRecord
	block []string
	// gitConfigs are the files the changed sources were read from, for the refusal.
	gitConfigs []string
	// labels are the loopholes whose scope changed, for the next step a decline names.
	labels []string
}

func compareScope(check *ScopeCheck, old scopeRecord) scopeOutcome {
	out := scopeOutcome{next: scopeRecord{}}
	for k, v := range old {
		out.next[k] = append([]string(nil), v...)
	}
	for _, s := range check.Sources {
		cur := s.Read.Repos()
		prev, had := old[s.Source]
		out.next[s.Source] = cur
		if !had && len(cur) == 0 {
			// Nothing to approve and no record yet: recorded silently, as an empty config is.
			continue
		}
		if had && sameRepos(prev, cur) {
			continue
		}
		out.changed = true
		out.labels = append(out.labels, s.Label)
		out.block = append(out.block, scopeBlock(s, prev, had)...)
		if s.Read.GitConfig != "" {
			out.gitConfigs = append(out.gitConfigs, s.Read.GitConfig)
		}
	}
	return out
}

// scopeBlock renders one source's LABELED SCOPE BLOCK (BB-D31): each repository added or
// removed, with the remote an added one came from, and the unchanged ones for context.
// Never JSON lines inside the config diff, where a new `owner/repo` string reads like any
// other value.
func scopeBlock(s ScopeSource, prev []string, had bool) []string {
	from := "this workspace's .git/config"
	if s.Read.GitConfig != "" {
		from = s.Read.GitConfig
	}
	lines := []string{s.Label + " repository scope, read from " + from + ":"}
	type row struct{ mark, repo, remote, what string }
	var rows []row
	inPrev := map[string]bool{}
	for _, r := range prev {
		inPrev[strings.ToLower(r)] = true
	}
	for _, r := range s.Read.Repos() {
		remote := ""
		if names := s.Read.RemoteNames(r); len(names) > 0 {
			quoted := make([]string, len(names))
			for i, n := range names {
				quoted[i] = fmt.Sprintf("%q", n)
			}
			remote = "remote " + strings.Join(quoted, ", ")
		}
		if inPrev[strings.ToLower(r)] {
			rows = append(rows, row{" ", r, remote, "unchanged"})
			delete(inPrev, strings.ToLower(r))
			continue
		}
		rows = append(rows, row{"+", r, remote, "added"})
	}
	for _, r := range prev {
		if inPrev[strings.ToLower(r)] {
			rows = append(rows, row{"-", r, "", "removed"})
		}
	}
	width, rwidth := 0, 0
	for _, r := range rows {
		width = max(width, len(r.repo))
		rwidth = max(rwidth, len(r.remote))
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %s %-*s  %-*s  %s", r.mark, width, r.repo, rwidth, r.remote, r.what))
	}
	if !had {
		lines = append(lines, "  (no scope was approved for this workspace before)")
	}
	if s.Read.Problem != "" {
		lines = append(lines, "  (the scope read as empty: "+s.Read.Problem+")")
	}
	return lines
}

// RecordApproval writes the approval record: the workspace config part, and the scope part
// when a scope is in play. It is the one writer both parts share, used by a `y`, by
// --accept-config-changes, and by a host `yolo check --accept-config-changes`, so the two
// parts land together.
func RecordApproval(workspace string, wsConfig *jsonx.OrderedMap, scope *ScopeCheck) error {
	if wsConfig == nil {
		wsConfig = jsonx.NewOrderedMap()
	}
	currentJSON, err := SnapshotJSON(wsConfig)
	if err != nil {
		return err
	}
	if scope.inPlay() {
		old, _, err := readScopeRecord(ApprovalScopePath(workspace))
		if err != nil {
			return err
		}
		if err := writeScopeRecord(workspace, compareScope(scope, old).next); err != nil {
			return err
		}
	}
	return writeSnapshot(ApprovalSnapshotPath(workspace), currentJSON)
}

func writeScopeRecord(workspace string, rec scopeRecord) error {
	js, err := scopeRecordJSON(rec)
	if err != nil {
		return err
	}
	return writeSnapshot(ApprovalScopePath(workspace), js)
}

// ApprovedScope returns the repositories the approval record holds for a source, for the
// launch that writes the scope file once the gate has passed.
func ApprovedScope(workspace, source string) []string {
	rec, _, err := readScopeRecord(ApprovalScopePath(workspace))
	if err != nil {
		return nil
	}
	return append([]string(nil), rec[source]...)
}
