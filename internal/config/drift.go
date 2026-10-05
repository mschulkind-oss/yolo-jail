package config

// drift.go answers "has the WORKSPACE config changed since this jail was built?"
//
// The workspace config (yolo-jail.jsonc + yolo-jail.local.jsonc + their includes)
// is the one piece an in-jail agent can read live and correctly: it lives under the
// bind-mounted /workspace, and LoadWorkspaceConfig has no user-config dependency. So
// drift needs just one new artifact — a FROZEN copy of the workspace config as the
// jail was built — to diff the live config against.
//
// Why workspace-only and not the merged config-assembled.json: that file folds the
// user config UNDER the workspace config per key (MergeConfig), and the user half is
// not visible in-jail, so it cannot be cleanly separated back out. The boot baseline
// is deliberately just the workspace layer, so the live re-read is an apples-to-apples
// comparison an agent can trust.
//
// The baseline is immutable for a jail's life: the host writes it once at fresh
// launch (WriteWorkspaceBootBaseline), and nothing in-jail rewrites it — unlike
// config-assembled.json, which every launch rewrites. That is what lets "the config
// that started THIS jail" stay fixed while the live config moves under it.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// WorkspaceConfigBootPath is <workspace>/.yolo/config-boot.json — the frozen
// workspace config a jail was built from. Beside config-assembled.json, but distinct:
// that file is the merged (user+workspace) config and is rewritten every launch;
// this is workspace-only and written once per fresh launch.
func WorkspaceConfigBootPath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(workspace, ".yolo", "config-boot.json")
}

// WriteWorkspaceBootBaseline freezes the workspace-only config as canonical snapshot
// JSON at WorkspaceConfigBootPath. Called by the HOST at fresh launch (not attach),
// so an in-jail `config drift` has an immutable record of what the jail was built
// from. wsCfg is the already-loaded workspace config (LoadWorkspaceConfig); passing
// it in rather than re-reading keeps the baseline identical to what the launch used.
//
// It returns the baseline's digest (baselineDigest), for a launch to hand its session in
// BootBaselineDigestEnv. A container launch ignores it; see that constant for the backend
// that needs it.
func WriteWorkspaceBootBaseline(workspace string, wsCfg *jsonx.OrderedMap) (string, error) {
	j, err := SnapshotJSON(wsCfg)
	if err != nil {
		return "", err
	}
	if err := writeWorkspaceSnapshot(workspace, WorkspaceConfigBootPath(workspace), j); err != nil {
		return "", err
	}
	return baselineDigest(j), nil
}

// BootBaselineDigestEnv names the variable that carries, into a session, the digest of the
// boot baseline its launch wrote. It is a wire variable between a launch and the `yolo config
// drift` run inside it, not a setting.
//
// IT EXISTS FOR A BACKEND WHOSE SESSIONS OVERLAP. A container jail's baseline is written at its
// fresh launch only, and a second fresh launch of the workspace cannot happen while that
// container runs (an invocation attaches instead), so the file stays the one the jail started
// from. macos-user has no attach: each invocation is a fresh launch, and it releases the launch
// lock before its agent starts, so a second session of the workspace can rewrite the file under
// the first. Without the digest, the first session's drift would then compare the live config
// against the SECOND launch's baseline and could say "in sync" about a config it never ran.
const BootBaselineDigestEnv = "YOLO_CONFIG_BOOT_DIGEST"

// ErrBaselineReplaced is WorkspaceConfigDrift's answer when the baseline on disk is not the one
// this session was launched with (BootBaselineDigestEnv): a later launch of the workspace
// replaced it, so no comparison against it speaks for this session.
var ErrBaselineReplaced = errors.New("a later launch of this workspace replaced the boot baseline this session started from")

// baselineDigest is the hex SHA-256 of a baseline's canonical JSON with its trailing
// whitespace stripped, which is the form WorkspaceConfigDrift compares, so the writer's digest
// and the reader's agree about one file whatever newline the writer appended.
func baselineDigest(canonical string) string {
	sum := sha256.Sum256([]byte(pyRstrip(canonical)))
	return hex.EncodeToString(sum[:])
}

// WorkspaceConfigDrift compares the frozen boot baseline against the workspace config
// on disk NOW, and returns the unified diff plus whether they differ.
//
//   - No baseline (a jail started before this feature, or config-boot.json removed):
//     ok=false, so a caller can say "cannot determine drift" rather than "no drift".
//   - A baseline a later launch of this workspace wrote, in a session that carries its own
//     baseline's digest (BootBaselineDigestEnv): ok=false and ErrBaselineReplaced.
//   - Baseline present, live config equal: hasDrift=false, no diff lines.
//   - Differ: hasDrift=true, diffLines is the unified diff (baseline → live).
//
// The comparison is on the canonical SnapshotJSON form of each — sorted keys, stable
// formatting — so cosmetic reordering or whitespace in the source file is not drift;
// only a real value/structure change is. List order (the order that matters) is
// preserved by the canonical form.
func WorkspaceConfigDrift(workspace string) (diffLines []string, hasDrift, ok bool, err error) {
	if workspace == "" {
		workspace = cwd()
	}
	baselineBytes, readErr := os.ReadFile(WorkspaceConfigBootPath(workspace))
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil, false, false, nil
		}
		return nil, false, false, readErr
	}
	baseline := pyRstrip(string(baselineBytes))
	// THE SESSION'S OWN BASELINE, OR NO ANSWER (BootBaselineDigestEnv). Only for the jail's own
	// workspace, which is the one the launch wrote the digest for: a `config drift` run in
	// another workspace from inside the session would otherwise be refused about a baseline
	// that was never this session's to begin with.
	if want := os.Getenv(BootBaselineDigestEnv); want != "" && inJail() && jailOwnWorkspace(workspace) &&
		want != baselineDigest(baseline) {
		return nil, false, false, ErrBaselineReplaced
	}

	live, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil {
		return nil, false, true, err
	}
	liveJSON, err := SnapshotJSON(live)
	if err != nil {
		return nil, false, true, err
	}

	if baseline == liveJSON {
		return nil, false, true, nil
	}
	diff := unifiedDiff(
		splitLines(baseline), splitLines(liveJSON),
		"config that started this jail", "workspace config on disk now")
	return diff, true, true, nil
}
