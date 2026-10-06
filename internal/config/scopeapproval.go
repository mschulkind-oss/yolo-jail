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
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// scopeapproval.go is the approval record's SCOPE PART (docs/design/boundary-broker.md
// §5.6, BB-D30, BB-D31; docs/design/workspace-widening.md §3.2): the repository scope a
// brokered loophole's daemon is fenced to, read at every fresh launch that starts one from the
// workspace's two inputs, its remotes and its `brokered.<source>.repos` entry, and approved in
// the same config-change gate and the same y/N as the workspace config.
//
// Both inputs are workspace state the agent can edit, which is the case the gate exists for
// (config-safety.md P2), so the scope takes the gate's path rather than a new one. It is a
// SECOND FILE beside the snapshot, not a key inside it, because the snapshot's bytes are a
// frozen contract: one byte of drift re-prompts every workspace on the machine.
//
// A THIRD FILE RECORDS WHERE EACH REPOSITORY CAME FROM, the SOURCES RECORD, beside the scope
// part (WW-D11). The scope part keeps its flat per-source shape, so no record written before it
// reads differently. The sources record makes a change of source a row and a prompt: without
// it, an agent could give an approved repository a second, unseen source, and the human's later
// removal of the visible one would silently leave it in scope.
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
	// RemoteHost is the forge whose remotes were read: `github.com`.
	RemoteHost string
	// Read is what the launch read from the workspace's remotes.
	Read brokerscope.Read
	// Entry is what the workspace's `brokered.<Source>.repos` lists, from the gate's one read
	// of the workspace config (WorkspaceRead.Entry).
	Entry []EntryRepo
}

// ScopeCheck is the scope part in play for one gate.
type ScopeCheck struct {
	Sources []ScopeSource
}

func (s *ScopeCheck) inPlay() bool { return s != nil && len(s.Sources) > 0 }

// ScopeRepo is one approved repository and where it came from, as a launch hands it to the
// spawn: Sources are display labels (`remote "origin"`, `yolo-jail.jsonc`), every one already
// escaped for a terminal (termsafe.Visible), since a file name is the agent's to choose.
type ScopeRepo struct {
	Repo    string   `json:"repo"`
	Sources []string `json:"sources"`
}

// scopeRepo is one repository of a source's current scope, with its sources.
type scopeRepo struct {
	repo    string
	remotes []string // remote names, in the read's order
	files   []string // workspace-relative files, in merge order
}

// current is the source's scope as read: the union of the remotes and the entry, each
// repository once, compared without case, sorted. The spelling kept is a remote's when a remote
// has it, else the entry's first.
func (s ScopeSource) current() []scopeRepo {
	at := map[string]int{}
	var out []scopeRepo
	get := func(repo string) *scopeRepo {
		k := strings.ToLower(repo)
		i, ok := at[k]
		if !ok {
			i = len(out)
			at[k] = i
			out = append(out, scopeRepo{repo: repo})
		}
		return &out[i]
	}
	for _, rm := range s.Read.Remotes {
		r := get(rm.Repo)
		if !inList(r.remotes, rm.Name) {
			r.remotes = append(r.remotes, rm.Name)
		}
	}
	for _, e := range s.Entry {
		r := get(e.Repo)
		for _, f := range e.Files {
			if !inList(r.files, f) {
				r.files = append(r.files, f)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].repo) < strings.ToLower(out[j].repo) })
	return out
}

// Repos is the source's scope as read, each repository once, sorted: the remotes' and the
// entry's.
func (s ScopeSource) Repos() []string {
	cur := s.current()
	out := make([]string, len(cur))
	for i, r := range cur {
		out[i] = r.repo
	}
	return out
}

// EntryFiles are the files the source's entry was read from, in merge order.
func (s ScopeSource) EntryFiles() []string {
	var out []string
	for _, e := range s.Entry {
		for _, f := range e.Files {
			if !inList(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// Source keys, the sources record's spelling of one source: a remote by its name, a file by
// its workspace-relative path. One function, so OQ-WW1's other answer, a remote recorded by its
// kind alone, changes remoteSourceKey and nothing else.
const (
	remoteSourcePrefix = "remote:"
	fileSourcePrefix   = "file:"
)

func remoteSourceKey(name string) string { return remoteSourcePrefix + name }
func fileSourceKey(rel string) string    { return fileSourcePrefix + rel }

// keys is r's sources as the sources record spells them, sorted.
func (r scopeRepo) keys() []string {
	out := make([]string, 0, len(r.remotes)+len(r.files))
	for _, n := range r.remotes {
		out = append(out, remoteSourceKey(n))
	}
	for _, f := range r.files {
		out = append(out, fileSourceKey(f))
	}
	sort.Strings(out)
	return out
}

// labels is r's sources for display: its remotes, then its files, each escaped.
func (r scopeRepo) labels() []string {
	var out []string
	if len(r.remotes) > 0 {
		out = append(out, remoteLabel(r.remotes))
	}
	for _, f := range r.files {
		out = append(out, termsafe.Visible(f))
	}
	return out
}

// remoteLabel is `remote "origin"`, or `remote "origin", "upstream"`: each name quoted, which
// writes a control character in it as an escape.
func remoteLabel(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return "remote " + strings.Join(quoted, ", ")
}

// labelOfKeys is a recorded source set for display, as labels spells a current one.
func labelOfKeys(keys []string) string {
	var remotes, files []string
	for _, k := range keys {
		switch {
		case strings.HasPrefix(k, remoteSourcePrefix):
			remotes = append(remotes, strings.TrimPrefix(k, remoteSourcePrefix))
		case strings.HasPrefix(k, fileSourcePrefix):
			files = append(files, termsafe.Visible(strings.TrimPrefix(k, fileSourcePrefix)))
		}
	}
	var out []string
	if len(remotes) > 0 {
		out = append(out, remoteLabel(remotes))
	}
	return strings.Join(append(out, files...), ", ")
}

// filesOfKeys is the files in a recorded source set.
func filesOfKeys(keys []string) []string {
	var out []string
	for _, k := range keys {
		if f, ok := strings.CutPrefix(k, fileSourcePrefix); ok {
			out = append(out, f)
		}
	}
	return out
}

// ApprovalScopePath is the scope part of a workspace's approval record:
// $HOME/.local/share/yolo-jail/approvals/<container-name>.scope.json, beside
// ApprovalSnapshotPath and keyed the same way.
func ApprovalScopePath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(paths.ApprovalsDir(), runtime.FromWorkspace(workspace)+scopePartSuffix)
}

// ApprovalScopeSourcesPath is the approval record's sources record (WW-D11):
// $HOME/.local/share/yolo-jail/approvals/<container-name>.scope-sources.json, beside the scope
// part.
func ApprovalScopeSourcesPath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(paths.ApprovalsDir(), runtime.FromWorkspace(workspace)+sourcesRecordSuffix)
}

const (
	configPartSuffix    = ".json"
	scopePartSuffix     = ".scope.json"
	sourcesRecordSuffix = ".scope-sources.json"
)

// ApprovalRecordFiles is every part of the approval record of the workspace whose container
// name is cname: the config part, the scope part and the sources record. A path that deletes the
// record deletes each of them; deleting one alone fails safe, since a missing part diffs against
// none and asks (BB-D30). EW-D33's sidecar part joins this list when it is built.
func ApprovalRecordFiles(cname string) []string {
	return []string{
		filepath.Join(paths.ApprovalsDir(), cname+configPartSuffix),
		filepath.Join(paths.ApprovalsDir(), cname+scopePartSuffix),
		filepath.Join(paths.ApprovalsDir(), cname+sourcesRecordSuffix),
	}
}

// scopeRecord is the scope part's content: an approved `owner/repo` list per source.
type scopeRecord map[string][]string

// sourcesRecord is the sources record's content: per source, per approved repository spelled in
// lower case, its sources' keys, sorted.
type sourcesRecord map[string]map[string][]string

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

// readSourcesRecord reads the sources record. None, or one that does not decode, is a record of
// no sources, which fails safe: a repository with no recorded sources passes unasked only when
// every current source is a remote, the one kind a record from before the sources record knew.
func readSourcesRecord(path string) (sourcesRecord, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return sourcesRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	rec := sourcesRecord{}
	if err := json.Unmarshal(data, &rec); err != nil {
		return sourcesRecord{}, nil
	}
	return rec, nil
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

// sourcesRecordJSON serializes the sources record through SnapshotJSON, sorted throughout.
func sourcesRecordJSON(rec sourcesRecord) (string, error) {
	m := jsonx.NewOrderedMap()
	sources := make([]string, 0, len(rec))
	for s := range rec {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	for _, s := range sources {
		repos := make([]string, 0, len(rec[s]))
		for r := range rec[s] {
			repos = append(repos, r)
		}
		sort.Strings(repos)
		per := jsonx.NewOrderedMap()
		for _, r := range repos {
			keys := append([]string(nil), rec[s][r]...)
			sort.Strings(keys)
			items := make([]any, len(keys))
			for i, k := range keys {
				items[i] = k
			}
			per.Set(r, items)
		}
		m.Set(s, per)
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

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EntryEdit is a changed `brokered.<source>.repos` entry, for the next step a decline and a
// refusal name: the key, and the workspace-relative files the change is in.
type EntryEdit struct {
	Key   string
	Files []string
}

// scopeOutcome is what comparing the in-play sources against the record found.
type scopeOutcome struct {
	changed bool
	// next and nextSources are the records to write: the old ones with every in-play source
	// replaced by what was read. Sources not in play keep their entries.
	next        scopeRecord
	nextSources sourcesRecord
	block       []string
	// counts are one line per changed source, for directly above the question.
	counts []string
	// gitConfigs are the files the changed sources were read from, for the refusal.
	gitConfigs []string
	// labels are the loopholes whose scope changed, for the next step a decline names.
	labels []string
	// entryEdits are the entries whose rows changed, for the decline's and the refusal's step.
	entryEdits []EntryEdit
	// approved is each in-play loophole's scope as read, which is what a passing gate
	// approved or confirmed, by loophole name.
	approved map[string][]ScopeRepo
}

func compareScope(check *ScopeCheck, old scopeRecord, oldSources sourcesRecord) scopeOutcome {
	out := scopeOutcome{next: scopeRecord{}, nextSources: sourcesRecord{}, approved: map[string][]ScopeRepo{}}
	for k, v := range old {
		out.next[k] = append([]string(nil), v...)
	}
	for k, v := range oldSources {
		out.nextSources[k] = v
	}
	for _, s := range check.Sources {
		cur := s.current()
		prev, had := old[s.Source]
		recorded := oldSources[s.Source]
		names := make([]string, len(cur))
		srcs := map[string][]string{}
		approved := make([]ScopeRepo, len(cur))
		for i, r := range cur {
			names[i] = r.repo
			srcs[strings.ToLower(r.repo)] = r.keys()
			approved[i] = ScopeRepo{Repo: r.repo, Sources: r.labels()}
		}
		out.next[s.Source] = names
		out.nextSources[s.Source] = srcs
		out.approved[s.Label] = approved
		if !had && len(cur) == 0 {
			// Nothing to approve and no record yet: recorded silently, as an empty config is.
			continue
		}
		block, count, edit, changed := scopeBlock(s, cur, prev, had, recorded)
		if !changed {
			continue
		}
		out.changed = true
		out.labels = append(out.labels, s.Label)
		out.block = append(out.block, block...)
		out.counts = append(out.counts, count)
		if s.Read.GitConfig != "" {
			out.gitConfigs = append(out.gitConfigs, s.Read.GitConfig)
		}
		if len(edit.Files) > 0 {
			out.entryEdits = append(out.entryEdits, edit)
		}
	}
	return out
}

// scopeBlock renders one source's LABELED SCOPE BLOCK (BB-D31; WW-D4): each repository added,
// removed or with a changed source, with every source it came from, and the unchanged ones for
// context. Never JSON lines inside the config diff, where a new `owner/repo` string reads like
// any other value. It returns the block, the count line, the entry files a change touched, and
// whether anything changed.
//
// A repository's sources are compared with the sources record (WW-D11). One with no recorded
// sources, as on the first launch after the record existed, passes unasked only when every
// source it has now is a remote, the only kind there was before; one an entry lists asks.
func scopeBlock(s ScopeSource, cur []scopeRepo, prev []string, had bool,
	recorded map[string][]string) (block []string, count string, edit EntryEdit, changed bool) {
	from := "this workspace's .git/config"
	if s.Read.GitConfig != "" {
		from = s.Read.GitConfig
	}
	if files := s.EntryFiles(); len(files) > 0 {
		shown := make([]string, len(files))
		for i, f := range files {
			shown[i] = termsafe.Visible(f)
		}
		from += " and " + strings.Join(shown, ", ")
	}
	lines := []string{s.Label + " repository scope, read from " + from + ":"}
	type row struct{ mark, repo, sources, what string }
	var rows []row
	inPrev := map[string]bool{}
	for _, r := range prev {
		inPrev[strings.ToLower(r)] = true
	}
	edit.Key = brokeredReposPath(s.Source)
	addFiles := func(files []string) {
		for _, f := range files {
			if !inList(edit.Files, f) {
				edit.Files = append(edit.Files, f)
			}
		}
	}
	added, removed, moved := 0, 0, 0
	for _, r := range cur {
		k := strings.ToLower(r.repo)
		label := strings.Join(r.labels(), ", ")
		if !inPrev[k] {
			rows = append(rows, row{"+", r.repo, label, "added"})
			added++
			addFiles(r.files)
			continue
		}
		delete(inPrev, k)
		was, known := recorded[k]
		switch {
		case known && sameStrings(was, r.keys()):
		case !known && len(r.files) == 0:
		default:
			old := "sources not recorded before"
			if known {
				old = "was " + labelOfKeys(was)
			}
			rows = append(rows, row{"~", r.repo, label + " (" + old + ")", "source changed"})
			moved++
			addFiles(r.files)
			addFiles(filesOfKeys(was))
			continue
		}
		rows = append(rows, row{" ", r.repo, label, "unchanged"})
	}
	for _, r := range prev {
		if inPrev[strings.ToLower(r)] {
			rows = append(rows, row{"-", r, "", "removed"})
			removed++
			addFiles(filesOfKeys(recorded[strings.ToLower(r)]))
		}
	}
	changed = !had || added+removed+moved > 0
	width, swidth := 0, 0
	for _, r := range rows {
		width = max(width, len(r.repo))
		swidth = max(swidth, len(r.sources))
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %s %-*s  %-*s  %s", r.mark, width, r.repo, swidth, r.sources, r.what))
	}
	if !had {
		lines = append(lines, "  (no scope was approved for this workspace before)")
	}
	if s.Read.Problem != "" {
		lines = append(lines, "  (the scope read as empty: "+s.Read.Problem+")")
	}
	count = fmt.Sprintf("%s: %d added, %d removed, %d source changed", s.Label, added, removed, moved)
	return lines, count, edit, changed
}

// readScopeRecords reads both scope-side parts of the record.
func readScopeRecords(workspace string) (scopeRecord, bool, sourcesRecord, error) {
	old, exists, err := readScopeRecord(ApprovalScopePath(workspace))
	if err != nil {
		return nil, false, nil, err
	}
	oldSources, err := readSourcesRecord(ApprovalScopeSourcesPath(workspace))
	if err != nil {
		return nil, false, nil, err
	}
	return old, exists, oldSources, nil
}

// ScopeChanges is what a gate would ask about the scope, compared with the record, for a caller
// that records without asking and must show the change first: host `yolo check
// --accept-config-changes` (WW-D20). Only the scope half of the report is set.
func ScopeChanges(workspace string, scope *ScopeCheck) (ChangeReport, error) {
	if !scope.inPlay() {
		return ChangeReport{}, nil
	}
	old, _, oldSources, err := readScopeRecords(workspace)
	if err != nil {
		return ChangeReport{}, err
	}
	sc := compareScope(scope, old, oldSources)
	return ChangeReport{ScopeChanged: sc.changed, ScopeBlock: sc.block, ScopeCounts: sc.counts,
		ScopeLabels: sc.labels, EntryEdits: sc.entryEdits, Workspace: workspaceOrCwd(workspace)}, nil
}

// RecordApproval writes the approval record: the workspace config part, and the scope part and
// sources record when a scope is in play. It is the one writer every part shares, used by a `y`,
// by --accept-config-changes, and by a host `yolo check --accept-config-changes`, so the parts
// land together.
func RecordApproval(workspace string, wsConfig *jsonx.OrderedMap, scope *ScopeCheck) error {
	currentJSON, err := SnapshotJSON(approvalConfigPart(wsConfig))
	if err != nil {
		return err
	}
	if scope.inPlay() {
		old, _, oldSources, err := readScopeRecords(workspace)
		if err != nil {
			return err
		}
		if err := writeScopeRecords(workspace, compareScope(scope, old, oldSources)); err != nil {
			return err
		}
	}
	return writeSnapshot(ApprovalSnapshotPath(workspace), currentJSON)
}

// writeScopeRecords writes the sources record, then the scope part. A crash between the two
// leaves a scope part with its sources unrecorded, or none, both of which fail safe.
func writeScopeRecords(workspace string, sc scopeOutcome) error {
	srcJSON, err := sourcesRecordJSON(sc.nextSources)
	if err != nil {
		return err
	}
	if err := writeSnapshot(ApprovalScopeSourcesPath(workspace), srcJSON); err != nil {
		return err
	}
	js, err := scopeRecordJSON(sc.next)
	if err != nil {
		return err
	}
	return writeSnapshot(ApprovalScopePath(workspace), js)
}

// ApprovedScope returns the repositories the approval record holds for a source. It is a
// report of the record, for a test or a reader that is not a launch: a launch hands its broker
// the gate's in-memory result instead (WW-P2), since a concurrent macos-user session can write
// the record between one launch's gate and its spawn.
func ApprovedScope(workspace, source string) []string {
	rec, _, err := readScopeRecord(ApprovalScopePath(workspace))
	if err != nil {
		return nil
	}
	return append([]string(nil), rec[source]...)
}
