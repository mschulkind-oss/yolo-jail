package config

// assembled.go owns the HOST → JAIL delivery of the merged config.
//
// It exists because one file used to do two unrelated jobs. Until OQ-D1
// (docs/design/config-safety.md) <workspace>/.yolo/config-snapshot.json was BOTH
// the record of what a human last approved AND the copy of the assembled config
// an in-jail LoadConfig reads back for its own workspace. Those two jobs pull in
// opposite directions:
//
//   - the APPROVAL record must be somewhere the jail cannot write, or it is not a
//     record at all — that is the whole ruling, and it moved to
//     ApprovalSnapshotPath, host-side, unmounted;
//   - the DELIVERY copy must be somewhere the jail CAN read, which on every
//     backend means inside the bind-mounted workspace.
//
// So the file split in two. Nothing about the delivery copy's integrity is
// load-bearing: a jail that rewrites its own assembled config has only lied to
// itself about a config it could already edit at the source (yolo-jail.jsonc is
// in the same read-write mount). The security boundary was never here — it is
// that the loaders which hand out host access (LoadCacheRelocations,
// LoadHostFiles) read paths.UserConfigPath() DIRECTLY and never the merged
// config, which makes workspace scope inexpressible rather than merely rejected.
//
// Why the delivery copy is needed at all: the user-level `include_if_found`
// overrides (a machine-local overrides.jsonc carrying mcp_servers, say) live on
// the HOST and are never mounted, so an in-jail re-assemble silently produces a
// REDUCED config. LoadConfig's short-circuit reads this file verbatim instead, so
// the in-jail view is byte-identical to the host's. See LoadConfig for the two
// gates on that short-circuit (own workspace only; no --user-layer set).

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// WorkspaceAssembledConfigPath is <workspace>/.yolo/config-assembled.json — the
// merged (user + workspace) config the HOST assembled for this launch, delivered
// into the jail through the workspace bind mount.
//
// Beside config-boot.json, and distinct from it in both content and lifetime:
// the boot baseline is WORKSPACE-ONLY and frozen for the jail's life so
// `yolo config drift` has a fixed thing to diff against, while this is the MERGE
// and is rewritten on every fresh launch.
//
// The name is deliberately not the old config-snapshot.json. "Snapshot" is the
// approval vocabulary in docs/design/config-safety.md, and leaving that word on
// the workspace-side file would keep pointing readers at the mount the ruling
// just moved the approval record out of.
func WorkspaceAssembledConfigPath(workspace string) string {
	if workspace == "" {
		workspace = cwd()
	}
	return filepath.Join(workspace, ".yolo", "config-assembled.json")
}

// WriteAssembledConfig writes the merged config in canonical snapshot form to
// WorkspaceAssembledConfigPath. Called by the HOST at fresh launch, after the
// approval gate, with the very config the launch is using — passing it in rather
// than re-reading is what keeps the delivered copy identical to what the host
// acted on.
//
// UNCONDITIONAL, unlike the approval snapshot it split from. The old file was
// written only on a first run or an accepted change, which was sound while it was
// the approval record (an unchanged config needs no new record) and is wrong for a
// delivery copy: an absent or stale copy silently degrades the in-jail read to a
// reduced re-assemble, and "the config was unchanged" is precisely the launch on
// which nobody would look.
func WriteAssembledConfig(workspace string, merged *jsonx.OrderedMap) error {
	j, err := SnapshotJSON(merged)
	if err != nil {
		return err
	}
	return writeSnapshot(WorkspaceAssembledConfigPath(workspace), j)
}
