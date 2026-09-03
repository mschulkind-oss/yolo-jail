package hostskills

// plugin.go delivers an EXISTING agent plugin — someone else's tree, carrying its own
// `.claude-plugin/plugin.json` — into a real skills dir, by COPYING IT VERBATIM rather than
// translating it.
//
// Why verbatim is the whole design, not an optimization: a tier-A destination is a skills
// dir whose tool already loads a per-directory plugin manifest and namespaces what it finds.
// So the correct render of a plugin is the plugin. Lowering it into yolo's own kinds — skills
// here, a hook there, MCP servers into a config surface — would silently drop everything
// yolo does not model (sub-agents, output styles, commands, an `.mcp.json`), and would need a
// new lowering rule every time the plugin schema grows. Copying needs neither.
//
// The single deviation from byte-identical: yolo injects its ownership marker into the copied
// manifest (markManifest below). Without it a later apply cannot tell its own output from a
// plugin the user installed by hand, and would either refuse to update what it wrote or feel
// entitled to rewrite what it did not. An unknown extra field is inert to the tools — they
// ignore what they do not model — which is exactly why the marker lives there rather than in
// a sidecar file that a second ownership mechanism would have to be invented for.
//
// TIER B gets an honest degradation, not a quiet one. A flat skills dir has no way to carry a
// plugin manifest, so only the plugin's SKILLS are deliverable; every other component it
// declares is refused BY NAME. That is the never-silent rule the rest of this work enforces:
// a user whose plugin ships hooks must hear that the hooks are not arriving, because the
// alternative is a plugin that looks installed and half works.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// PluginRequest is one wrapped plugin's delivery into one skills dir.
type PluginRequest struct {
	// Pack is the wrapping pack's name — the ownership record's key, and what the report
	// attributes the delivery to. NOT the destination name: the plugin's own name is, because
	// that is what the tools namespace its skills by.
	Pack string
	// Plugin is the recognized tree.
	Plugin *pluginpack.Plugin
	// SkillsDir is the absolute destination skills dir (the tool's own).
	SkillsDir string
	// Tier is the DECLARED tier of that destination; ProbeTier decides what is used.
	Tier Tier
	// Composed, Legacy, PreOwned and Claimed are the composition's ownership plumbing — see
	// Request, whose fields these are passed straight through to. Claimed matters at BOTH tiers
	// here, unlike plain skill delivery: it is what catches a second pack claiming one plugin
	// name, which the per-directory marker alone cannot distinguish from yolo's own earlier apply.
	Composed *Manifest
	Legacy   *Manifest
	PreOwned map[string]bool
	Claimed  map[string]string
	// ArchiveRoot is where a retired entry is moved.
	ArchiveRoot ArchiveRoot
	// Stamp names the archive generation.
	Stamp string
	// Observe computes without writing.
	Observe bool
}

// DeliverPlugin renders one plugin tree into one skills dir and returns one Result per thing
// considered — the tree itself, each component that could not come along, each flat skill.
//
// An error is returned only for a condition that makes the delivery impossible; anything
// per-entry becomes a Result so the user sees the whole picture in one run.
func DeliverPlugin(req PluginRequest) ([]Result, error) {
	name := req.Plugin.Name()
	tier, downgrade := ProbeTier(req.Tier, req.SkillsDir, name)
	var out []Result
	if downgrade != "" {
		out = append(out, Result{
			Name: name, Path: filepath.Join(req.SkillsDir, name), Action: ActionRefused,
			Detail: "plugin delivery downgraded to flat skills — " + downgrade,
		})
	}
	if tier == TierNamespaced {
		res, err := deliverPluginTree(req, name)
		return append(out, res...), err
	}
	res, err := deliverPluginFlat(req, name)
	return append(out, res...), err
}

// deliverPluginTree is the tier-A path: the plugin dir lands at <skillsDir>/<plugin-name>/,
// contents and manifest included, and the tool loads it in place.
func deliverPluginTree(req PluginRequest, name string) ([]Result, error) {
	dest := filepath.Join(req.SkillsDir, name)

	// A plugin NAME is exclusive at a destination even though `skills` as a kind merges: two
	// packs wrapping plugins that call themselves the same thing want one directory, and
	// whichever applied last would win with no report. The per-directory marker cannot catch
	// this (both copies are genuinely yolo's), so the run's claim set is the only evidence.
	//
	// Keyed on THIS RUN's claims rather than on a saved owner, which is what composition changed:
	// the record now names a pseudo-owner (ComposedOwner) for every composed path, so "does the
	// record name a different pack?" can no longer be asked of it. Within one composition, though,
	// the question is exactly "has another layer already claimed this directory?" — which is
	// sharper: it catches the collision on the FIRST apply rather than on the second.
	if owner, claimed := req.Claimed[dest]; claimed {
		return []Result{{
			Name: name, Path: dest, Action: ActionRefused,
			Detail: "pack " + owner + " already delivers a plugin named " + name +
				" here — rename one, or they would overwrite each other every apply",
		}}, nil
	}

	skills := req.Plugin.SkillDirs()
	// THE CHANGE PREDICATE for the tree, computed before anything is written so both postures
	// agree (Result.WouldChange). An unchanged tree keeps its own action rather than becoming
	// ActionUnchanged only where nothing is copied — see the write gate below.
	changed := changedPluginTree(req.Plugin.Dir, dest, req.Plugin.ManifestRel())
	action := wroteAction(req.Observe)
	if !changed {
		action = ActionUnchanged
	}
	out := []Result{{
		Name: name, Path: dest, Action: action,
		Detail: pluginTreeDetail(name, skills), WouldChange: changed,
	}}
	// Claimed BEFORE the writes, and in both postures: the claim is what a later layer and the
	// composition's retire pass read, so gating it on the write succeeding would make an
	// unwritable plugin dir look like an orphan to retire.
	claimPath(req.Composed, req.Claimed, dest, req.Pack, req.Observe)
	// Components that RUN are delivered here (the destination tool loads the manifest, which
	// is the point of tier A) — so say so. The install-time approval decided whether they may
	// come at all; this is the always-warn half, because "a pack put a hook in my real home"
	// is not something a user should have to read a lockfile to discover.
	for _, c := range req.Plugin.Components() {
		if !c.RunsCode {
			continue
		}
		detail := "delivered — " + c.Detail + " once your tool loads the plugin"
		if req.Observe {
			detail = "would be delivered — " + c.Detail + " once your tool loads the plugin"
		}
		out = append(out, Result{
			Name: name + ":" + c.Name, Path: req.Plugin.ManifestPath,
			Action: wroteAction(req.Observe), Detail: detail,
			// The components arrive with the tree, in the same copy, so they carry the tree's
			// verdict. The ACTION stays the always-warn wording whatever the verdict — "a pack
			// put a hook in my real home" is a fact about what is there, not about what this
			// apply moved, and rewording it to `unchanged` would retire the warning the moment
			// it became a standing one.
			WouldChange: changed,
		})
	}

	// A dangling link standing where the skills dir or the plugin's own dir has to be is cleared
	// and reported, for the same reason as in Deliver: MkdirAll can neither use the name nor
	// create it, so the raw `file exists` was the entire user-visible output.
	out = append(out, clearDanglingDirs(req.SkillsDir, dest, req.Observe)...)

	if req.Observe || !changed {
		// !changed short-circuits the write for the same reason the flat and namespaced skill
		// deliveries do: the RemoveAll below is a delete-and-rewrite of a whole plugin tree in a
		// real home, and doing it for content that has not moved is churn with no result.
		return out, nil
	}
	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		return out, mkdirError(req.SkillsDir, err)
	}
	// Replace wholesale rather than merging into the previous copy. Exact mirroring is what
	// makes a plugin that DROPPED a skill stop offering it, and it is legitimate only because
	// ProbeTier already established that this path is yolo's own output (or free). The
	// alternative — diffing the two trees — would be the same result with more ways to leave
	// a stale file behind.
	if err := os.RemoveAll(dest); err != nil {
		out[0].Action, out[0].Detail, out[0].WouldChange = ActionRefused, err.Error(), false
		return out, nil
	}
	if err := copyTree(req.Plugin.Dir, dest); err != nil {
		out[0].Action, out[0].Detail, out[0].WouldChange = ActionRefused, err.Error(), false
		return out, nil
	}
	if err := markManifest(dest, req.Plugin.ManifestRel()); err != nil {
		out[0].Action, out[0].Detail, out[0].WouldChange = ActionRefused, err.Error(), false
		return out, nil
	}
	return out, nil
}

// deliverPluginFlat is the tier-B path: only the plugin's skills survive the trip, each one
// written directly into the skills dir under the same ownership rules any other flat skill
// gets, and every other component refused by name.
func deliverPluginFlat(req PluginRequest, name string) ([]Result, error) {
	var out []Result
	// The refusals come FIRST, before the successes, because they are the part a user needs
	// to read: a plugin whose hooks did not arrive is not the plugin they installed.
	for _, c := range req.Plugin.Components() {
		out = append(out, Result{
			Name: name + ":" + c.Name, Path: req.SkillsDir, Action: ActionRefused,
			Detail: "plugin " + c.Name + " needs a tool that loads a plugin manifest; " +
				"this skills dir is flat, so it cannot arrive (" + c.Detail + ")",
		})
	}

	skills := req.Plugin.SkillDirs()
	if len(skills) == 0 {
		out = append(out, Result{
			Name: name, Path: req.SkillsDir, Action: ActionRefused,
			Detail: "plugin declares no skills — nothing about it is deliverable to a flat " +
				"skills dir",
		})
		return out, nil
	}
	// Reuse the flat delivery wholesale rather than reimplementing the ownership rules: a
	// plugin's skill in a flat dir is a skill in a flat dir, and a second copy of "is this
	// entry the user's?" is a second chance to get it wrong.
	// exclude is the sharp part, and it was found by RUNNING this rather than by reading it.
	// When the plugin's ROOT is itself a skill (`skills: ["./"]`, the layout the real
	// scaffolder emits), the root is delivered as an ordinary skill dir — and a plain
	// recursive copy of it drags the whole plugin along: manifest, hooks, agents, every
	// component the loop above just refused BY NAME. The refusal would print while the
	// components arrived anyway, which is the one outcome worse than either honest answer.
	res, err := deliverFlat(Request{
		Pack:         req.Pack,
		SkillsDir:    req.SkillsDir,
		Composed:     req.Composed,
		Legacy:       req.Legacy,
		PreOwned:     req.PreOwned,
		Claimed:      req.Claimed,
		ArchiveRoot:  req.ArchiveRoot,
		Stamp:        req.Stamp,
		Observe:      req.Observe,
		excludePaths: req.Plugin.ComponentPaths(),
	}, skills)
	return append(out, res...), err
}

// pluginTreeDetail describes what a delivered plugin tree gives the user: the qualified names
// its skills invoke under, which is the one fact that is not obvious from the path.
func pluginTreeDetail(name string, skills map[string]string) string {
	if len(skills) == 0 {
		return "plugin tree copied verbatim (declares no skills)"
	}
	names := make([]string, 0, len(skills))
	for s := range skills {
		names = append(names, "/"+name+":"+s)
	}
	sort.Strings(names)
	detail := "plugin tree copied verbatim — invoke as " + names[0]
	if len(names) > 1 {
		detail += fmt.Sprintf(" (+%d more)", len(names)-1)
	}
	return detail
}

// markManifest injects yolo's ownership marker into the manifest of a freshly copied plugin
// tree, preserving every other field.
//
// Decoded into a map, not into a struct: a struct round-trip would drop every field yolo does
// not model, which is precisely the plugin content this whole file exists to carry through
// intact. rel is the manifest's location WITHIN the tree, so a plugin whose manifest lives at
// `.plugin/plugin.json` is not silently given a second one under `.claude-plugin/`.
func markManifest(destDir, rel string) error {
	path := filepath.Join(destDir, filepath.FromSlash(rel))
	marked, changes, err := markedManifestBytes(path)
	if err != nil || !changes {
		// A manifest yolo cannot re-encode is left exactly as copied. The cost is that the
		// next apply will not recognize the dir as yolo's and will leave it alone — the safe
		// direction, and better than rewriting someone's file from a partial parse.
		return err
	}
	return os.WriteFile(path, marked, 0o644)
}

// markedManifestBytes returns what markManifest WOULD write for the manifest at path, plus
// whether it would write at all (false for a manifest it cannot re-encode, which is left as
// copied).
//
// Split out of markManifest so the change predicate can ask the question without writing:
// a delivered plugin tree is NOT a byte copy of the pack's — this marker is added to it — so
// comparing the two trees directly reports every plugin as changed forever. changedPluginTree
// is the caller, and it compares the source manifest's MARKED form against what is on disk.
func markedManifestBytes(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return data, false, nil
	}
	marker, err := json.Marshal(yoloManagedMarker)
	if err != nil {
		return nil, false, err
	}
	raw["x-yolo-managed-by"] = marker
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// changedPluginTree reports whether delivering the plugin at src over dest would alter it —
// the change predicate for a tier-A plugin delivery (Result.WouldChange).
//
// It cannot be a plain tree comparison, and that is the whole reason it exists: the delivery
// copies the tree verbatim and then REWRITES the manifest to carry yolo's ownership marker
// (markManifest), so the destination is deliberately one file different from the source. A
// digest of the two would report CHANGED on every apply forever, which as a change predicate
// means a prompt on every launch (docs/design/host-apply-staleness.md R3).
//
// So the tree is compared with the manifest excluded, and the manifest is compared against its
// MARKED form. Every failure to read either side reads as CHANGED, matching Changed's
// direction: the cost of a false positive is one redundant copy, and of a false negative
// content nobody compared.
func changedPluginTree(src, dest, manifestRel string) bool {
	rel := filepath.FromSlash(manifestRel)
	// Skipped on BOTH sides, unlike changedExcept's src-only skip: the manifest is present in
	// the destination (rewritten, not omitted), so leaving it in the destination's digest would
	// re-introduce exactly the difference this function exists to hold aside.
	if changedTreeSkipping(src, dest, map[string]bool{rel: true}) {
		return true
	}
	want, _, err := markedManifestBytes(filepath.Join(src, rel))
	if err != nil {
		return true
	}
	got, err := os.ReadFile(filepath.Join(dest, rel))
	if err != nil {
		return true
	}
	return !bytes.Equal(want, got)
}
