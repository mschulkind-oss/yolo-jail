package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
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
// defect (docs/design/config-safety.md, OQ-D1). The workspace is bind-mounted
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
// It survives only as a MIGRATION SIGNAL, and it is never read as a baseline. Its
// content is exactly what the ruling declares untrustworthy — a file the jail
// could have written — so adopting it once "to avoid a prompt" would carry the
// defect across the very change that closes it. Its PRESENCE is what we read
// instead: it says this workspace has been launched before, so the absence of a
// host-side record is a migration and not a first run. That distinction matters,
// because first run accepts silently and a migration must not.
//
// KNOWN RESIDUAL HOLE, stated rather than implied. Presence is not more
// trustworthy than content — it is the same bit, held by the same rw bind mount.
// An agent that edits yolo-jail.jsonc AND deletes this file turns its own change
// into a genuine-looking first run, which accepts silently and records the result
// as approved. The window is per-workspace and one-shot (it closes the first time
// a host-side record is written) but it is open on every workspace that has not
// been launched since OQ-D1 landed, which on upgrade day is all of them.
//
// It is documented and not closed because closing it is a DESIGN decision, not a
// bug fix: there is no host-side per-workspace artifact that predates the ruling
// and survives (paths.ContainerDir's tracking files are pruned when the container
// stops, and paths.AgentsDir's staging is recreated by THIS launch before the gate
// runs), so the only sound repair is to stop letting a missing record mean "first
// run" — i.e. to prompt on the very first launch of a brand-new workspace, which
// docs/design/config-safety.md deliberately ruled against.
func LegacyWorkspaceSnapshotPath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(workspace, ".yolo", "config-snapshot.json")
}

// AcceptConfigChangesFlag is the CLI flag that grants config-change approval on a
// launch with nobody to prompt (docs/design/config-safety.md, OQ-D2).
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

// ChangedNonInteractiveError is the refused launch of OQ-D2: the merged config
// changed since the last approval, there is no terminal to ask on, and
// AcceptConfigChangesFlag was not passed.
//
// It carries the diff as LINES rather than as one pre-rendered blob so the caller
// can colour it exactly as the interactive prompt does — the two paths show the
// same change to the same reader, and only the ending differs. Error() still
// renders the whole thing (headline, plain diff, advice) so a caller that only
// knows how to print an error loses nothing.
type ChangedNonInteractiveError struct {
	// WorkspaceConfig, WorkspaceLocalConfig and UserConfig name the files the
	// merged config comes from. All of them are named because ANY of them can be
	// what moved: the snapshot stores the MERGE, so a user-level edit shows up here
	// exactly like a workspace-level one, and a reader told only about
	// yolo-jail.jsonc would go looking in the wrong file.
	//
	// WorkspaceLocalConfig is yolo-jail.local.jsonc and is empty when that file does
	// not exist. It is listed at all because LoadWorkspaceConfig merges it OVER
	// yolo-jail.jsonc — it is the file that WINS — so a reader who diffs only
	// yolo-jail.jsonc against git and finds it clean has been sent to the one file
	// that cannot explain the change. Naming it only when present is deliberate: a
	// path that does not exist reads as "look here" and would send the same reader
	// to a second wrong place.
	WorkspaceConfig      string
	WorkspaceLocalConfig string
	UserConfig           string
	// SnapshotPath is the host-side approval record this was diffed against.
	SnapshotPath string
	// DiffLines is the unified diff, previous approved config → current.
	DiffLines []string
}

// Headline states what happened and why the launch stopped.
func (e *ChangedNonInteractiveError) Headline() string {
	return "Jail config changed since the last approved launch, and this launch has no " +
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
	files += "  user config:      " + e.UserConfig + "\n" +
		"  approved config:  " + e.SnapshotPath + "\n"
	return "A changed config is never accepted without a human — an auto-accept here would " +
		"make the approval promise conditional on somebody happening to have a terminal " +
		"attached, and a scripted launch is exactly where nobody is watching.\n\n" +
		files +
		"\nAny file those configs `include` counts as part of the merge too.\n\n" +
		"Revert the change, or approve it for THIS LAUNCH ONLY by re-running with\n" +
		"  " + AcceptConfigChangesFlag + "\n" +
		"which records the new config as approved exactly as answering `y` would."
}

func (e *ChangedNonInteractiveError) Error() string {
	var b strings.Builder
	b.WriteString(e.Headline())
	b.WriteString("\n\n")
	for _, line := range e.DiffLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(e.Advice())
	return b.String()
}

// CheckConfigChanges compares config against the last-approved snapshot; returns
// true to proceed, false to abort.
//   - First run / no snapshot and no legacy one: write current + "\n", true.
//   - Migration (no host-side snapshot, a legacy workspace-side one present):
//     treated as CHANGED against an empty previous config, so the whole config is
//     shown once and approved once. See LegacyWorkspaceSnapshotPath for why the
//     legacy file's content is never adopted.
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
	snapshotPath := ApprovalSnapshotPath(workspace)
	currentJSON, err := SnapshotJSON(config)
	if err != nil {
		return false, err
	}

	// fromLabel is the diff's "---" line. It doubles as the explanation for the
	// migration prompt: a human who changed nothing still needs to know why they
	// are being asked, and the label sits directly above the diff they are
	// approving, which is cheaper than a second channel through ChangePrompter.
	oldJSON := ""
	fromLabel := "previous config"

	oldBytes, readErr := os.ReadFile(snapshotPath)
	switch {
	case readErr == nil:
		oldJSON = pyRstrip(string(oldBytes))
	case !os.IsNotExist(readErr):
		return false, readErr
	// pathExists (helpers.go) asks the only question the legacy record is allowed
	// to answer: mere presence. Its CONTENT is deliberately never read — see
	// LegacyWorkspaceSnapshotPath.
	case !pathExists(LegacyWorkspaceSnapshotPath(workspace)):
		// Genuine first run — nothing has ever been approved for this workspace
		// and there is nothing to show. Accept and save.
		if err := writeSnapshot(snapshotPath, currentJSON); err != nil {
			return false, err
		}
		return true, nil
	default:
		// Migration: an approval record exists, in the place the ruling says we
		// cannot trust. Diff against EMPTY rather than against its content, so the
		// human approves the whole current config once, on the evidence in front of
		// them, rather than on a file the jail could have written.
		fromLabel = "previous config (none — the old workspace-side record is no longer trusted)"
	}

	if oldJSON == currentJSON {
		return true, nil
	}

	diffLines := unifiedDiff(
		splitLines(oldJSON), splitLines(currentJSON),
		fromLabel, "current config")

	if !isTTY {
		if !acceptNonInteractive {
			localPath := filepath.Join(workspaceOrCwd(workspace), WorkspaceLocalConfigName)
			if !pathExists(localPath) {
				localPath = ""
			}
			return false, &ChangedNonInteractiveError{
				WorkspaceConfig:      filepath.Join(workspaceOrCwd(workspace), WorkspaceConfigName),
				WorkspaceLocalConfig: localPath,
				UserConfig:           paths.UserConfigPath(),
				SnapshotPath:         snapshotPath,
				DiffLines:            diffLines,
			}
		}
		// Granted by the flag: record it exactly as a `y` does, or the next
		// launch prompts (or refuses) over the same change all over again.
		if err := writeSnapshot(snapshotPath, currentJSON); err != nil {
			return false, err
		}
		retireLegacyWorkspaceSnapshot(workspace)
		return true, nil
	}

	accept := false
	if prompter != nil {
		accept = prompter.Prompt(diffLines)
	}
	if accept {
		if err := writeSnapshot(snapshotPath, currentJSON); err != nil {
			return false, err
		}
		retireLegacyWorkspaceSnapshot(workspace)
		return true, nil
	}
	return false, nil
}

// workspaceOrCwd is the "" => cwd default LoadConfig and the path helpers share.
func workspaceOrCwd(workspace string) string {
	if workspace == "" {
		return cwd()
	}
	return workspace
}

// retireLegacyWorkspaceSnapshot deletes the pre-OQ-D1 workspace-side record once a
// trustworthy host-side one exists.
//
// Best-effort on purpose: a launch must not fail because a stale file could not be
// unlinked. It runs only after a successful write, never after a refusal — a
// rejected change leaves the migration signal in place so the next launch asks
// again instead of silently sliding into the first-run branch.
//
// It is deleted rather than left alone because a file named config-snapshot.json
// sitting in the workspace is exactly what makes a future reader believe the
// approval record still lives there. The one thing lost is that a jail booted from
// a STALE image, whose baked in-jail yolo still looks for that filename as its
// assembled-config source, falls back to re-assembling — the documented fallback
// (LoadConfig), not a new failure mode.
func retireLegacyWorkspaceSnapshot(workspace string) {
	_ = os.Remove(LegacyWorkspaceSnapshotPath(workspace))
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
