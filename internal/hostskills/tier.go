// Package hostskills delivers a pack's skills into a REAL home, which is a different
// problem from delivering them into a jail.
//
// In a jail, `agents.PrepareSkills` merges built-in < pack < user's own into one flat
// staging dir and bind-mounts it :ro. Every path there is disposable, so clearing the
// destination and replacing the tree wholesale is safe. On a host none of that holds: the
// destination is the user's own `~/.claude/skills`, a `clearDirContents` there deletes
// hand-written skills, and there is no mount to make the result read-only.
//
// The design question this package answers is "can a user still add their own skill to an
// agent whose skills a pack manages?" — and the answer turns out to be a property of the
// TARGET TOOL, not of yolo. Some agents namespace plugin-supplied skills and some do not,
// so how nice yolo can be is bounded by the tool, and the code says so explicitly rather
// than picking one global rule and being wrong half the time.
//
//	TIER A (namespaced). The tool loads a plugin manifest from within its skills dir and
//	  qualifies those skills by the plugin's name. yolo writes ONE directory per pack —
//	  <skills-dir>/<pack>/ with a .claude-plugin/plugin.json — and the pack's skills invoke
//	  as <pack>:<skill>. Collision with a user's own skill is then STRUCTURALLY impossible
//	  (different namespace, and yolo never writes a sibling entry), so yolo can safely
//	  update and even remove within its own subtree, and needs no provenance record to know
//	  what it owns: the path says so.
//
//	TIER B (flat). The tool has no namespace; every skill is a bare name in one directory,
//	  first- or last-found wins depending on the tool. yolo must write entries directly
//	  alongside the user's, so it CANNOT tell its own output from theirs by path alone.
//	  That is what the manifest (manifest.go) is for, and why removal archives rather than
//	  deletes: "yolo wrote this" rests on a record that can go stale (the user edited the
//	  file, the state dir was pruned, two machines share one config), and a stale record
//	  plus rm is data loss.
//
// Tier is DECLARED by the pack and then PROBED, never inferred from the destination path.
// Inference (".claude/skills means tier A") would hardcode a tool's name into core, which
// is the coupling AGENTS.md forbids — core knows the domain, not the tool. And because the
// tier-A mechanism is an undocumented implementation detail of the tools involved, a
// declaration that does not hold up is downgraded rather than trusted.
package hostskills

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Tier is how much namespacing the destination tool supports. The zero value is TierFlat:
// an unstated tier gets the SAFE treatment, never the permissive one.
type Tier int

const (
	// TierFlat is a skills dir with no namespace: yolo's entries sit beside the user's,
	// tracked by the manifest, removed by archiving.
	TierFlat Tier = iota
	// TierNamespaced is a skills dir whose tool loads a per-directory plugin manifest,
	// letting one pack own one subtree under its own name.
	TierNamespaced
)

func (t Tier) String() string {
	if t == TierNamespaced {
		return "namespaced"
	}
	return "flat"
}

// ParseTier lowers a pack's declared tier string. An empty or unrecognized value is
// TierFlat with ok=false, so a typo degrades to the safe tier AND is reportable — a
// silently-misread tier would hand a pack more power over a real home than it asked for.
func ParseTier(s string) (Tier, bool) {
	switch s {
	case "":
		return TierFlat, true // unstated is a legitimate choice, not an error
	case "flat":
		return TierFlat, true
	case "namespaced":
		return TierNamespaced, true
	default:
		return TierFlat, false
	}
}

// pluginManifestDir is the per-directory manifest a tier-A tool looks for. The name is the
// tools' convention, not yolo's invention: both agents known to support this scan for
// `.claude-plugin/plugin.json` inside a skills directory.
const pluginManifestDir = ".claude-plugin"

// pluginManifestName is the manifest file inside pluginManifestDir.
const pluginManifestName = "plugin.json"

// ProbeTier verifies a DECLARED tier against the destination before it is trusted, and
// returns the tier to actually use plus a reason when it was downgraded.
//
// Only one direction is possible: namespaced → flat. A declared flat tier is already the
// conservative choice and needs no check. The probe is deliberately cheap and structural —
// it cannot run the target tool — so it answers "is this destination one where a per-pack
// plugin dir is plausible?" rather than "will the tool definitely honor it":
//
//   - the destination's parent must exist or be creatable (a skills dir yolo cannot write
//     is not a tier question at all, and the caller reports it);
//   - a path already occupied by a NON-plugin directory of the pack's name is not
//     available for tier A, because taking it over would mean absorbing whatever the user
//     put there.
//
// The reason this is a probe rather than an assumption: tier A rests on behavior that is
// undocumented in the tools that implement it (verified by reading a shipped bundle, not a
// spec), so it can regress without notice. Failing to FLAT on any doubt keeps the failure
// mode "yolo was less clever than it could be" instead of "yolo scattered files into a
// namespace the tool ignores".
func ProbeTier(declared Tier, skillsDir, pack string) (Tier, string) {
	if declared != TierNamespaced {
		return TierFlat, ""
	}
	packDir := filepath.Join(skillsDir, pack)
	fi, err := os.Stat(packDir)
	if err != nil {
		if os.IsNotExist(err) {
			return TierNamespaced, "" // free to create it
		}
		return TierFlat, "cannot inspect " + packDir + ": " + err.Error()
	}
	if !fi.IsDir() {
		return TierFlat, packDir + " exists and is not a directory"
	}
	if IsYoloPluginDir(packDir) {
		return TierNamespaced, "" // ours from a previous apply
	}
	return TierFlat, packDir + " already exists and was not written by yolo"
}

// pluginManifest is the subset of a tier-A plugin manifest yolo writes. `skills: ["./"]`
// points the tool at the pack dir itself, which is what makes a nested skills/ subtree
// resolve; the marker field is yolo's own and is what IsYoloPluginDir keys on.
type pluginManifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Skills      []string `json:"skills"`
	// ManagedBy marks the directory as yolo's OUTPUT rather than a user's own plugin.
	// Without it, "is this dir mine?" would be answered by "does it have a plugin.json",
	// which is also true of a plugin the user authored by hand — and yolo would then feel
	// entitled to rewrite it. An unknown extra field is harmless to the tools (they
	// ignore what they do not model) and is the whole ownership record tier A needs.
	ManagedBy string `json:"x-yolo-managed-by"`
}

// yoloManagedMarker is the ManagedBy value yolo writes and recognizes.
const yoloManagedMarker = "yolo-jail"

// IsYoloPluginDir reports whether dir is a plugin directory YOLO wrote, by reading its
// manifest's marker field. A user's hand-authored plugin, or a directory with no manifest,
// is not yolo's — and must never be rewritten or removed.
//
// Errors read as "not ours", which is the safe direction: an unreadable or malformed
// manifest means yolo cannot prove ownership, so it keeps its hands off.
func IsYoloPluginDir(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, pluginManifestDir, pluginManifestName))
	if err != nil {
		return false
	}
	var m pluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	return m.ManagedBy == yoloManagedMarker
}

// writePluginManifest writes the tier-A manifest for a pack's subtree.
func writePluginManifest(packDir, pack, description string) error {
	dir := filepath.Join(packDir, pluginManifestDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	m := pluginManifest{
		Name:        pack,
		Description: description,
		Skills:      []string{"./"},
		ManagedBy:   yoloManagedMarker,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, pluginManifestName), append(data, '\n'), 0o644)
}
