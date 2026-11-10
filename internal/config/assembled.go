package config

// assembled.go owns the HOST → JAIL delivery of the merged config.
//
// It exists because one file used to do two unrelated jobs. Until OQ-D1
// (docs/reference/config-safety.md) <workspace>/.yolo/config-snapshot.json was BOTH
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// WorkspaceAssembledConfigPath is <workspace>/.yolo/setups/<setup>/config-assembled.json — the
// merged (user + workspace) config the HOST assembled for this launch of the setup, delivered
// into the jail through the workspace bind mount (setupstate.go; setupName "" is this session's
// setup).
//
// Beside config-boot.json, and distinct from it in both content and lifetime:
// the boot baseline is WORKSPACE-ONLY and frozen for the jail's life so
// `yolo config drift` has a fixed thing to diff against, while this is the MERGE
// and is rewritten on every fresh launch.
//
// The name is deliberately not the old config-snapshot.json. "Snapshot" is the
// approval vocabulary in docs/reference/config-safety.md, and leaving that word on
// the workspace-side file would keep pointing readers at the mount the ruling
// just moved the approval record out of.
func WorkspaceAssembledConfigPath(workspace, setupName string) string {
	return setupRecordPath(workspace, setupName, assembledConfigLeaf)
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
//
// The private env set's inline values are written as RedactedEnvValue: the copy sits in the
// workspace, which the jail reads, and a private value reaches only the agents that claim it.
func WriteAssembledConfig(workspace, setupName string, merged *jsonx.OrderedMap) error {
	j, err := SnapshotJSON(RedactPrivateEnvValues(merged))
	if err != nil {
		return err
	}
	return writeSetupRecord(workspace, setupName, assembledConfigLeaf, j)
}

// WorkspaceAssembledOriginsPath is <workspace>/.yolo/setups/<setup>/config-assembled-origins.json: the launch
// origins of the assembled config beside it (launchorigins.go), which host file set each value,
// so a refusal in the jail about a key of that copy names the file to edit.
func WorkspaceAssembledOriginsPath(workspace, setupName string) string {
	return setupRecordPath(workspace, setupName, assembledOriginsLeaf)
}

// WriteAssembledOrigins writes the launch origins of merged, the config WriteAssembledConfig
// wrote, from src, its provenance. Written on every launch beside that copy, as an empty record
// when src knows nothing (a sealed launch's empty copy), so a record from an earlier launch never
// describes this one.
//
// The record names the digest of the copy it describes, and a reader ignores a record whose
// digest is not the copy's, so a copy rewritten by a launcher that writes no record (an older
// yolo) is never described by the record an earlier launch left.
func WriteAssembledOrigins(workspace, setupName string, merged *jsonx.OrderedMap, src *Sources) error {
	j, err := SnapshotJSON(RedactPrivateEnvValues(merged))
	if err != nil {
		return err
	}
	rec := assembledOrigins{Copy: copyDigest([]byte(j + "\n"))}
	if record := LaunchOriginsJSON(merged, src); record != "" {
		rec.Origins = json.RawMessage(record)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return writeSetupRecord(workspace, setupName, assembledOriginsLeaf, string(b))
}

// assembledOrigins is the file WriteAssembledOrigins writes: the sha256 of the copy's bytes, and
// the copy's launch origins (absent when nothing was recorded).
type assembledOrigins struct {
	Copy    string          `json:"copy_sha256"`
	Origins json.RawMessage `json:"origins,omitempty"`
}

func copyDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// assembledOriginsTree is the provenance tree of snap, the assembled copy, from the launch
// origins beside it, both this session's setup's; nil when there are none.
func assembledOriginsTree(workspace string, snap *jsonx.OrderedMap) *srcNode {
	data, err := readSetupRecord(workspace, "", assembledOriginsLeaf)
	if err != nil {
		return nil
	}
	var rec assembledOrigins
	if json.Unmarshal(data, &rec) != nil {
		return nil
	}
	copyPath := WorkspaceAssembledConfigPath(workspace, "")
	copyData, err := readSetupRecord(workspace, "", assembledConfigLeaf)
	if err != nil || rec.Copy != copyDigest(copyData) {
		return nil // a record of another copy
	}
	t := parseOrigins(rec.Origins)
	if t == nil || (t.W == nil && t.K == nil && t.E == nil) {
		return nil
	}
	return launchCopyTree(&srcFile{path: copyPath, data: copyData}, snap, t)
}
