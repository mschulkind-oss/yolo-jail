package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// SnapshotJSON returns the config-snapshot bytes: 2-space indent, sorted keys,
// ASCII-escaped (jsonx.DumpsSnapshot). Frozen contract (must not drift — a
// single byte of drift fires a spurious config-approval prompt).
func SnapshotJSON(config *jsonx.OrderedMap) (string, error) {
	return jsonx.DumpsSnapshot(config)
}

// ApprovalSnapshotPath is the HOST-SIDE record of the merged config a human last
// approved for a workspace:
// $HOME/.local/share/yolo-jail/approvals/<container-name>.json.
//
// It used to live at <workspace>/.yolo/config-snapshot.json and that was the
// defect (docs/reference/config-safety.md, OQ-D1). The workspace is bind-mounted
// READ-WRITE, so whatever can edit yolo-jail.jsonc could also rewrite the only
// record of what was last approved — and the next launch then had nothing to
// show. Note the shape of it: DELETING the record has always failed safe, so
// what was missing is integrity, not secrecy, which is why moving the file is
// the whole fix and signing it or mounting it read-only would be machinery the
// problem does not need.
//
// The key is runtime.FromWorkspace's deterministic container name, the same key
// paths.ContainerDir and paths.AgentsDir use for their per-workspace state, and
// for the same reason: the name already resolves symlinks and an absolute path
// to one stable string, and a second keying scheme would be a second thing to
// keep in step with it. The one cost of moving host-side is that a workspace
// copied or moved elsewhere loses its approval baseline and re-prompts — the
// direction to fail in.
func ApprovalSnapshotPath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(paths.ApprovalsDir(), runtime.FromWorkspace(workspace)+".json")
}

// LegacyWorkspaceSnapshotPath is where the approval snapshot lived BEFORE OQ-D1:
// <workspace>/.yolo/config-snapshot.json.
//
// NOTHING IN PRODUCTION READS IT ANY MORE — neither its content nor its
// presence. It briefly survived OQ-D1 as a migration signal (presence meant "not
// a first run"), which left a residual hole: an agent could delete it alongside
// a config edit and pass the edit off as a silent first run. OQ-S3
// (docs/reference/config-safety.md) closed that on 2026-08-29 by making a
// fresh workspace with a NON-EMPTY config prompt regardless, at which point the
// signal meant nothing and CheckConfigChanges stopped consulting it.
//
// The function is kept for the tests that assert the old path is never written
// again (a regression here would silently resurrect a jail-writable record).
func LegacyWorkspaceSnapshotPath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(workspace, ".yolo", "config-snapshot.json")
}

// AcceptConfigChangesFlag is the CLI flag that grants config-change approval on a
// launch with nobody to prompt (docs/reference/config-safety.md, OQ-D2).
//
// A FLAG AND NOT AN ENVIRONMENT VARIABLE, deliberately, even though this repo's
// other bypasses (YOLO_ALLOW_STALE_IMAGE, YOLO_ALLOW_UNREACHABLE_SERVICES) are env
// vars. Those suppress a DIAGNOSIS; this one grants an APPROVAL. An env var is
// inherited by every child process and survives in a shell for the rest of a
// session — precisely the property a per-launch approval must not have.
//
// The spelling lives here rather than in internal/cli because the REFUSAL MESSAGE
// has to name it (its reader is by construction someone who cannot be prompted),
// and the message is composed in this package. internal/cli's parser reads the
// constant back, so the flag a user is told to pass and the flag the parser
// accepts cannot drift apart.
const AcceptConfigChangesFlag = "--accept-config-changes"

// ChangePrompter decides interactive config-change acceptance. It receives the
// rendered unified diff lines (fromfile "previous config", tofile "current
// config", lineterm "") and returns true to accept. It is only invoked on a
// TTY; the non-interactive path refuses (or is granted by the flag) without ever
// calling it.
type ChangePrompter interface {
	// Prompt renders the diff and asks "Accept these config changes? [y/N]".
	// Returns accept=true iff the user answered y/yes.
	Prompt(diffLines []string) bool
}

// ChangedNonInteractiveError is the refused launch of OQ-D2: the workspace config
// changed since the last approval, there is no terminal to ask on, and
// AcceptConfigChangesFlag was not passed.
//
// It carries the diff as LINES rather than as one pre-rendered blob so the caller
// can colour it exactly as the interactive prompt does — the two paths show the
// same change to the same reader, and only the ending differs. Error() still
// renders the whole thing (headline, plain diff, advice) so a caller that only
// knows how to print an error loses nothing.
type ChangedNonInteractiveError struct {
	// WorkspaceConfig and WorkspaceLocalConfig name the workspace files the
	// config comes from.
	//
	// WorkspaceLocalConfig is yolo-jail.local.jsonc and is empty when that file does
	// not exist. It is listed at all because LoadWorkspaceConfig merges it OVER
	// yolo-jail.jsonc — it is the file that WINS — so a reader who diffs only
	// yolo-jail.jsonc against git and finds it clean has been sent to the one file
	// that cannot explain the change.
	WorkspaceConfig      string
	WorkspaceLocalConfig string
	// SnapshotPath is the host-side approval record this was diffed against.
	SnapshotPath string
	// DiffLines is the unified diff, previous approved config → current.
	DiffLines []string

	// ConfigChanged and ScopeChanged say which part of the approval record changed
	// (docs/design/boundary-broker.md BB-D31). Both false reads as the config-only refusal
	// this error always was, so a caller that predates the scope part is unchanged.
	ConfigChanged, ScopeChanged bool
	// ScopeBlock is the labeled scope block, printed before the diff, and ScopeCounts its count
	// lines (WW-D20); GitConfigs the files the remotes were read from; EntryEdits the
	// `brokered.<source>.repos` entries a change touched; ScopePath the scope part it was
	// compared against.
	ScopeBlock  []string
	ScopeCounts []string
	GitConfigs  []string
	EntryEdits  []EntryEdit
	ScopePath   string
	// ScopeLabels are the brokered loopholes whose scope changed: the advice names the
	// command that launches this project without each one.
	ScopeLabels []string
	// Workspace is the launch's workspace, which that command names, so it works from
	// wherever it is pasted.
	Workspace string
}

// Headline states what happened and why the launch stopped. It names the repository scope
// whenever the scope changed, and only the scope when the config did not: a headline
// announcing a config change that did not happen, above the block meant to be read first,
// is the trap the block exists to close (BB-D31).
func (e *ChangedNonInteractiveError) Headline() string {
	switch {
	case e.ScopeChanged && e.ConfigChanged:
		return "Workspace config and repository scope changed since the last approved launch, and " +
			"this launch has no terminal to approve them on."
	case e.ScopeChanged:
		// It names the scope without claiming the remotes alone made it: an entry in the
		// workspace config is the other input (WW-D13).
		return "The repository scope changed since the last approved launch, and this launch has " +
			"no terminal to approve it on."
	}
	return "Workspace config changed since the last approved launch, and this launch has no " +
		"terminal to approve it on."
}

// Advice names the flag, the files, and the snapshot. It is the half of the
// message that tells a reader who cannot be prompted what to do next, so the flag
// is spelled out in full rather than referred to.
func (e *ChangedNonInteractiveError) Advice() string {
	files := "  workspace config: " + e.WorkspaceConfig + "\n"
	if e.WorkspaceLocalConfig != "" {
		files += "  workspace local:  " + e.WorkspaceLocalConfig + "  (merged OVER the above)\n"
	}
	files += "  approved config:  " + e.SnapshotPath + "\n"
	if e.ScopeChanged {
		for _, g := range e.GitConfigs {
			files += "  git config:       " + g + "  (the remotes the repository scope is read from)\n"
		}
		for _, ed := range e.EntryEdits {
			for _, f := range ed.Files {
				files += "  scope entry:      " + termsafe.Visible(filepath.Join(e.Workspace, f)) + "  (" +
					ed.Key + ")\n"
			}
		}
		files += "  approved scope:   " + e.ScopePath + "\n"
	}
	what, recorded := "workspace config", "the new config"
	if e.ScopeChanged {
		what, recorded = "workspace config or repository scope", "the new config and repository scope"
	}
	return "A changed " + what + " is never accepted without a human — an auto-accept here would " +
		"make the approval promise conditional on somebody happening to have a terminal " +
		"attached, and a scripted launch is exactly where nobody is watching.\n\n" +
		files +
		"\nAny file those configs `include` counts as part of the merge too.\n\n" +
		"Revert the change, or approve it for THIS LAUNCH ONLY by re-running with\n" +
		"  " + AcceptConfigChangesFlag + "\n" +
		"which records " + recorded + " as approved exactly as answering `y` would." +
		withoutBrokerSteps(e.ScopeLabels, e.Workspace)
}

// withoutBrokerSteps is the other next step a changed repository scope has: launching the
// project without the brokered loophole that reads it, with the command that does that, one
// line per loophole; "" when no scope changed.
func withoutBrokerSteps(labels []string, workspace string) string {
	var b strings.Builder
	for _, l := range labels {
		b.WriteString("\nTo launch this project without " + l + " instead, run `" +
			DisableLoopholeCommand(l, workspace) + "`: it is off for this project from the next " +
			"launch, which then asks nothing about its repositories.")
	}
	return b.String()
}

// DisableLoopholeCommand is `yolo loopholes disable <name> --workspace <workspace>`, the path
// quoted for a shell and a control character in it written as an escape, so the command works
// from whatever folder it is pasted in.
func DisableLoopholeCommand(name, workspace string) string {
	return "yolo loopholes disable " + name + " --workspace " + shquote.QuoteDisplay(workspace)
}

func (e *ChangedNonInteractiveError) Error() string {
	var b strings.Builder
	b.WriteString(e.Headline())
	b.WriteString("\n\n")
	for _, line := range e.ScopeBlock {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range e.ScopeCounts {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range e.DiffLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(e.Advice())
	return b.String()
}

// CheckConfigChanges compares workspace config against the last-approved snapshot; returns
// true to proceed, false to abort.
//   - First run (no host-side snapshot):
//   - If workspace config is empty (currentJSON == "{}"): write snapshot, return true.
//   - If workspace config is non-empty: diff against empty ("none (initial launch)" -> "workspace config"),
//     prompt or check acceptNonInteractive (OQ-S3).
//   - Unchanged (old.rstrip() == current, no trailing "\n" on the compare):
//     return true.
//   - Changed + non-tty + acceptNonInteractive: accept, rewrite snapshot, true.
//   - Changed + non-tty without it: (false, *ChangedNonInteractiveError) — the
//     OQ-D2 refusal. The snapshot is NOT rewritten, so a later interactive launch
//     still shows the same diff.
//   - Changed + tty: delegate to prompter; on accept rewrite snapshot + return
//     true, else return false (snapshot NOT rewritten).
//
// The rstrip-compare asymmetry is deliberate: the stored file has a trailing
// "\n" (written as current+"\n"), but the comparison rstrips the OLD text and
// compares to current (which has NO trailing "\n"). isTTY, acceptNonInteractive
// and prompter are injected so every branch is testable without a real terminal.
func CheckConfigChanges(workspace string, config *jsonx.OrderedMap, isTTY, acceptNonInteractive bool, prompter ChangePrompter) (bool, error) {
	return CheckConfigAndScopeChanges(workspace, config, nil, isTTY, acceptNonInteractive, prompter)
}

// ChangeReport is everything one fresh launch is asked to approve: the workspace config's
// diff and the repository scope's labeled block (BB-D31), each only when that part changed.
type ChangeReport struct {
	ConfigChanged bool
	DiffLines     []string
	ScopeChanged  bool
	ScopeBlock    []string
	// ScopeCounts are the count lines, one per changed source, printed directly above the
	// question, since a long config diff can scroll the block out of view (WW-D20).
	ScopeCounts []string
	// EntryEdits are the `brokered.<source>.repos` entries whose rows changed, with the files
	// the change is in, for the next step a decline names.
	EntryEdits []EntryEdit
	// ConfigFiles are the workspace config files the config part was read from, in merge order
	// (the local file, when there is one, wins), for the next step a declined config change
	// names.
	ConfigFiles []string
	// ScopeLabels are the brokered loopholes whose scope changed, for the next step a declined
	// scope names (`yolo loopholes disable <label> --workspace <Workspace>`).
	ScopeLabels []string
	// Workspace is the launch's workspace, for that step.
	Workspace string
}

// ReportPrompter is a ChangePrompter that can show the whole report, so the header and the
// question can name the scope when it changed, and only the scope when the config did not.
// A prompter without it is shown the scope block above the diff through Prompt.
type ReportPrompter interface {
	PromptReport(ChangeReport) bool
}

// AcceptedReporter is a prompter that is shown what --accept-config-changes approves on a launch
// with no terminal, before it is recorded: the repository scope block and its count lines, for
// remotes and entries alike (WW-D20, after EW-D33's "a launch that the flag approves prints the
// section anyway"). Called only when the scope changed.
type AcceptedReporter interface {
	ReportAccepted(ChangeReport)
}

// CheckConfigAndScopeChanges is CheckConfigChanges with the approval record's scope part
// in play (docs/design/boundary-broker.md BB-D30): scope is the repository scope a fresh
// launch that starts a brokered loophole read from the workspace's remotes and its
// `brokered.<source>.repos` entry, or nil when no broker starts, which makes this exactly
// CheckConfigChanges.
//
// One decision covers every part. Unchanged → proceed. A part with no record yet and
// nothing to approve (a `{}` config, an empty scope) is recorded silently. Any change —
// a config diff, a repository added or removed, a repository's source changed — asks, with
// the scope block first; y, or --accept-config-changes with no terminal, records EVERY part;
// N or a refusal records NONE, so the no-record `{}` branch no longer writes its config part
// before the scope part is decided. The config part leaves `brokered` out
// (approvalConfigPart): the entry is approved as scope rows, never as JSON lines.
func CheckConfigAndScopeChanges(workspace string, config *jsonx.OrderedMap, scope *ScopeCheck,
	isTTY, acceptNonInteractive bool, prompter ChangePrompter) (bool, error) {
	ok, _, err := ApproveConfigAndScope(workspace, config, scope, isTTY, acceptNonInteractive, prompter)
	return ok, err
}

// ApproveConfigAndScope is CheckConfigAndScopeChanges, also returning what a passing gate
// approved or confirmed for each in-play brokered loophole, by its name: its repositories and
// their sources. That in-memory result is the whole of what the launch hands the broker (WW-P2,
// WW-D12): nothing re-reads a config file or the record to get it, because an edit, or a
// concurrent macos-user session's approval, can land between this gate and the spawn. nil when
// no scope is in play or the gate did not pass.
//
// config is the workspace config as the gate read it, `brokered` included: the config part is
// its projection (approvalConfigPart), and the entry is already in scope.
func ApproveConfigAndScope(workspace string, config *jsonx.OrderedMap, scope *ScopeCheck,
	isTTY, acceptNonInteractive bool, prompter ChangePrompter) (bool, map[string][]ScopeRepo, error) {
	snapshotPath := ApprovalSnapshotPath(workspace)
	currentJSON, err := SnapshotJSON(approvalConfigPart(config))
	if err != nil {
		return false, nil, err
	}

	oldJSON := ""
	fromLabel := "previous workspace config"
	toLabel := "current workspace config"
	configSilent := false

	oldBytes, readErr := os.ReadFile(snapshotPath)
	switch {
	case readErr == nil:
		oldJSON = pyRstrip(string(oldBytes))
	case !os.IsNotExist(readErr):
		return false, nil, readErr
	default:
		// First run / no host-side snapshot (OQ-S3). An empty workspace config is recorded
		// with zero prompts — once the scope part, if in play, is decided too.
		if currentJSON == "{}" {
			configSilent = true
			oldJSON = currentJSON
		} else {
			// Fresh workspace with declared configuration: prompt to confirm initial workspace config.
			fromLabel = "none (initial launch)"
			toLabel = "workspace config"
		}
	}
	configChanged := oldJSON != currentJSON

	var sc scopeOutcome
	scopePath := ApprovalScopePath(workspace)
	oldScopeJSON, oldSourcesJSON := "", ""
	if scope.inPlay() {
		old, exists, oldSources, err := readScopeRecords(workspace)
		if err != nil {
			return false, nil, err
		}
		if exists {
			oldScopeJSON, _ = scopeRecordJSON(old)
		}
		oldSourcesJSON, _ = sourcesRecordJSON(oldSources)
		sc = compareScope(scope, old, oldSources)
	}

	recordAll := func() error {
		if scope.inPlay() {
			if err := writeScopeRecords(workspace, sc); err != nil {
				return err
			}
		}
		return writeSnapshot(snapshotPath, currentJSON)
	}

	if !configChanged && !sc.changed {
		// Nothing to ask. Record whatever part had no record yet — silently, and together,
		// which is the only branch that writes without a human: an empty config's part, an
		// empty scope's, and the sources of a scope whose repositories are unchanged and come
		// from remotes alone (scopeBlock's upgrade rule).
		if configSilent {
			if err := writeSnapshot(snapshotPath, currentJSON); err != nil {
				return false, nil, err
			}
		}
		if scope.inPlay() {
			nextJSON, err1 := scopeRecordJSON(sc.next)
			nextSourcesJSON, err2 := sourcesRecordJSON(sc.nextSources)
			if err1 == nil && err2 == nil && (nextJSON != oldScopeJSON || nextSourcesJSON != oldSourcesJSON) {
				if err := writeScopeRecords(workspace, sc); err != nil {
					return false, nil, err
				}
			}
		}
		return true, sc.approvedOrNil(scope), nil
	}

	var diffLines []string
	if configChanged {
		if oldJSON == "" && fromLabel == "none (initial launch)" {
			diffLines = unifiedDiff(nil, splitLines(currentJSON), fromLabel, toLabel)
		} else {
			diffLines = unifiedDiff(splitLines(oldJSON), splitLines(currentJSON), fromLabel, toLabel)
		}
	}
	localPath, _ := ResolveWorkspaceConfigPath(workspaceOrCwd(workspace), WorkspaceLocalConfigName)
	if !pathExists(localPath) {
		localPath = ""
	}
	wsPath, _ := ResolveWorkspaceConfigPath(workspaceOrCwd(workspace), WorkspaceConfigName)
	configFiles := []string{wsPath}
	if localPath != "" {
		configFiles = append(configFiles, localPath)
	}
	report := ChangeReport{ConfigChanged: configChanged, DiffLines: diffLines,
		ScopeChanged: sc.changed, ScopeBlock: sc.block, ScopeCounts: sc.counts, EntryEdits: sc.entryEdits,
		ConfigFiles: configFiles, ScopeLabels: sc.labels, Workspace: workspaceOrCwd(workspace)}

	if !isTTY {
		if !acceptNonInteractive {
			e := &ChangedNonInteractiveError{
				WorkspaceConfig:      wsPath,
				WorkspaceLocalConfig: localPath,
				SnapshotPath:         snapshotPath,
				DiffLines:            diffLines,
				ConfigChanged:        configChanged,
				ScopeChanged:         sc.changed,
				ScopeBlock:           sc.block,
				ScopeCounts:          sc.counts,
				GitConfigs:           sc.gitConfigs,
				EntryEdits:           sc.entryEdits,
				ScopeLabels:          sc.labels,
				Workspace:            workspaceOrCwd(workspace),
			}
			if sc.changed {
				e.ScopePath = scopePath
			}
			return false, nil, e
		}
		// Granted by the flag: shown, then recorded exactly as a `y` does, or the next
		// launch prompts (or refuses) over the same change all over again.
		if ar, ok := prompter.(AcceptedReporter); ok && sc.changed {
			ar.ReportAccepted(report)
		}
		if err := recordAll(); err != nil {
			return false, nil, err
		}
		return true, sc.approvedOrNil(scope), nil
	}

	if prompter == nil {
		return false, nil, nil
	}
	accepted := false
	if rp, ok := prompter.(ReportPrompter); ok {
		accepted = rp.PromptReport(report)
	} else {
		accepted = prompter.Prompt(append(append([]string(nil), sc.block...), diffLines...))
	}
	if !accepted {
		return false, nil, nil
	}
	if err := recordAll(); err != nil {
		return false, nil, err
	}
	return true, sc.approvedOrNil(scope), nil
}

// approvedOrNil is the gate's approved scope, nil when no scope was in play.
func (sc scopeOutcome) approvedOrNil(scope *ScopeCheck) map[string][]ScopeRepo {
	if !scope.inPlay() {
		return nil
	}
	return sc.approved
}

// workspaceOrCwd is the "" => cwd default LoadConfig and the path helpers share.
func workspaceOrCwd(workspace string) string {
	if workspace == "" {
		return cwd()
	}
	return workspace
}

// writeSnapshot writes currentJSON + "\n", creating the containing directory as
// needed. It serves three destinations now — the host-side approvals dir, the
// workspace's config-assembled.json and its config-boot.json — so it names the
// PARENT of whatever path it is handed rather than any one of them.
func writeSnapshot(path, currentJSON string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(currentJSON+"\n"), 0o644)
}

// writeWorkspaceSnapshot is writeSnapshot for the two destinations under
// <workspace>/.yolo — config-assembled.json and config-boot.json — and it differs in the
// one way that matters: the directory comes from paths.EnsureWorkspaceStateDir instead of a
// bare MkdirAll.
//
// TWO THINGS THAT BUYS, and the first is why writeSnapshot cannot simply be changed:
// writeSnapshot's third caller is the host-side approvals dir, which lives INSIDE
// ~/.local/share/yolo-jail by design, so routing every caller through a helper that refuses
// boundary directories would refuse the approval record. Splitting the workspace half off
// gets it the state dir's .gitignore (a bare MkdirAll skips it, so a workspace whose first
// artifact was a config snapshot had an un-ignored .yolo) and the scope refusal, which for
// these two is defence in depth — both are on the launch path, behind the guard.
//
// AND THE FILE IS WRITTEN BENEATH A ROOT ON `.yolo` (paths.WriteWorkspaceStateFile), never
// by path: `.yolo` is jail-writable, and os.WriteFile followed a link the last jail left at
// either name, truncating the host file it named and writing this config JSON into it. path
// must name a file directly in the workspace's `.yolo`, which both destinations do.
func writeWorkspaceSnapshot(workspace, path, currentJSON string) error {
	ws := workspaceOrCwd(workspace)
	if filepath.Dir(path) != paths.WorkspaceStateDir(ws) {
		return fmt.Errorf("internal: workspace snapshot %s is not directly in %s", path, paths.WorkspaceStateDir(ws))
	}
	return paths.WriteWorkspaceStateFile(ws, filepath.Base(path), []byte(currentJSON+"\n"), 0o644)
}

// pyRstrip trims trailing whitespace using the same whitespace set as
// str.strip() (the ASCII set plus a few unicode spaces). For the snapshot file
// the only trailing whitespace is the "\n" we wrote, but the full set keeps the
// rstrip-compare robust.
func pyRstrip(s string) string {
	return strings.TrimRightFunc(s, isPySpace)
}

func isPySpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f',
		0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0,
		0x2028, 0x2029:
		return true
	}
	// Broader unicode whitespace also removed by str.strip().
	switch {
	case r >= 0x2000 && r <= 0x200a:
		return true
	case r == 0x1680 || r == 0x202f || r == 0x205f || r == 0x3000:
		return true
	}
	return false
}

// splitLines splits on the same line boundaries difflib uses.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if isLineBoundary(r) {
			if r == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
