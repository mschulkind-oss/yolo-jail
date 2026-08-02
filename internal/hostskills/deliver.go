package hostskills

// deliver.go is the render itself: put one pack's skills into one real skills dir, at the
// tier that dir supports, without ever touching content yolo cannot prove it owns.
//
// The invariants, in priority order — each overrides the ones below it:
//
//  1. NEVER touch what yolo did not write. A destination entry that is not provably yolo's
//     (a marked plugin dir at tier A, a manifest entry at tier B) is the user's. It is
//     reported and skipped, never overwritten and never removed.
//  2. NEVER delete; archive. Retiring goes through archive.go so being wrong about
//     ownership costs a `mv` back rather than the user's work.
//  3. NEVER write in observe posture. Everything computes identically in both postures and
//     only the writes are gated, so the dry run cannot disagree with the real one.
//  4. ALWAYS report. Every entry produces a line — written, skipped, archived, refused —
//     because a silent skip is the failure mode this whole body of work exists to remove.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Action is what a delivery did (or would do) to one entry.
type Action string

const (
	// ActionWrote is a skill yolo delivered.
	ActionWrote Action = "rendered"
	// ActionSkippedUser is an entry that exists and is not provably yolo's — the user's
	// own skill, left exactly as it is.
	ActionSkippedUser Action = "skipped (yours)"
	// ActionArchived is a previously-delivered entry the pack no longer ships, moved to
	// the archive.
	ActionArchived Action = "archived"
	// ActionRefused is an entry yolo declined to deliver, with a reason.
	ActionRefused Action = "refused"
)

// Result is one entry's outcome, for the caller to print.
type Result struct {
	// Name is the skill's directory name (the identity a tool keys on: in every real skill
	// checked, the dir name is what the tool invokes, with frontmatter `name` being a
	// display label at best).
	Name string
	// Path is the absolute destination.
	Path string
	// Action is what happened.
	Action Action
	// Detail carries the reason for a refusal, the archive path for an archived entry, or
	// the qualified invocation name for a tier-A write — whatever makes the line useful.
	Detail string
}

// Request is one pack's delivery into one skills dir.
type Request struct {
	// Pack is the pack's name. At tier A it becomes the subtree name AND the invocation
	// namespace, so it is user-visible.
	Pack string
	// Description is the pack's description, written into the tier-A plugin manifest.
	Description string
	// Sources are the directories to read skills from, in precedence order (later wins on
	// a same-named skill), each containing one subdir per skill. Built-ins are deliberately
	// NOT included by the caller for a host render: yolo's own jail-oriented skills are
	// noise in a real home.
	Sources []string
	// SkipSources are absolute source paths to leave out even when they sit inside a Source.
	// Today that means a WRAPPED PLUGIN's subtree (plugin.go): it is delivered verbatim as a
	// plugin, and without this it would ALSO be picked up here as an ordinary skill dir named
	// after the plugin — the same content delivered twice, once loadable and once not.
	SkipSources []string
	// SkillsDir is the absolute destination skills dir (the tool's own, e.g.
	// ~/.claude/skills).
	SkillsDir string
	// Tier is the DECLARED tier; ProbeTier decides what is actually used.
	Tier Tier
	// Manifest is the tier-B provenance record. Required at tier B; unused at tier A.
	Manifest *Manifest
	// ArchiveRoot is where retired entries are moved.
	ArchiveRoot ArchiveRoot
	// Stamp names the archive generation (see Archive).
	Stamp string
	// Observe computes without writing.
	Observe bool

	// excludePaths are absolute source paths to omit from a copied skill's subtree. Unexported
	// because it exists for exactly one caller (plugin.go's flat path) and one reason: a
	// plugin whose ROOT is a skill would otherwise carry its whole manifest and every
	// component along inside that "skill", arriving at a destination that just refused them by
	// name. A general knob would invite using it to drop content for less honest reasons.
	excludePaths []string
}

// Deliver renders one pack's skills into one real skills dir and returns one Result per
// entry considered. It returns an error only for a condition that makes the whole delivery
// impossible (an unwritable skills dir); a per-entry problem becomes a Result so the rest
// still proceeds and the user sees the whole picture at once.
func Deliver(req Request) ([]Result, error) {
	skills, err := collectSkills(req.Sources, req.SkipSources)
	if err != nil {
		return nil, err
	}

	// A pack that carries NO skills must leave no trace. The six shipped agent packs declare
	// a `skills` contribution to name the destination their agent reads from and ship no
	// content of their own, so without this an apply would litter the user's skills dir with
	// empty plugin directories — visible in `/plugin`, loadable, and containing nothing.
	// Returning early also means such a pack cannot "retire" anything, which is right: it
	// never owned anything to retire.
	if len(skills) == 0 {
		return nil, nil
	}

	tier, downgrade := ProbeTier(req.Tier, req.SkillsDir, req.Pack)
	var out []Result
	if downgrade != "" {
		// A downgrade is REPORTED, never silent: it changes how yolo treats the user's
		// files (from "owns a subtree" to "writes beside your entries"), which the user
		// should hear about at the moment it happens.
		out = append(out, Result{
			Name: req.Pack, Path: filepath.Join(req.SkillsDir, req.Pack),
			Action: ActionRefused,
			Detail: "namespaced delivery downgraded to flat — " + downgrade,
		})
	}

	if tier == TierNamespaced {
		res, derr := deliverNamespaced(req, skills)
		return append(out, res...), derr
	}
	res, derr := deliverFlat(req, skills)
	return append(out, res...), derr
}

// deliverNamespaced writes the pack's skills as one owned subtree: <skillsDir>/<pack>/ with
// a marked plugin manifest and the skills beneath it. Because the subtree is unambiguously
// yolo's, a full rewrite is legitimate — the same posture a config surface has over its own
// managed keys — and yolo never so much as stats a sibling entry.
func deliverNamespaced(req Request, skills map[string]string) ([]Result, error) {
	packDir := filepath.Join(req.SkillsDir, req.Pack)
	nested := filepath.Join(packDir, "skills")

	var out []Result
	// Retire first: a skill the pack no longer ships must stop being loaded, and inside
	// yolo's own subtree the set of stale entries is simply "what is there minus what we
	// ship" — no manifest needed, which is tier A's whole advantage.
	if existing, err := os.ReadDir(nested); err == nil {
		for _, e := range existing {
			if !e.IsDir() || skills[e.Name()] != "" {
				continue
			}
			stale := filepath.Join(nested, e.Name())
			r := Result{Name: e.Name(), Path: stale, Action: ActionArchived}
			if !req.Observe {
				at, aerr := Archive(req.ArchiveRoot, req.Stamp, req.Pack, stale)
				if aerr != nil {
					r.Action, r.Detail = ActionRefused, aerr.Error()
				} else {
					r.Detail = "moved to " + at
				}
			} else {
				r.Detail = "would move to the archive"
			}
			out = append(out, r)
		}
	}

	if !req.Observe {
		if err := os.MkdirAll(nested, 0o755); err != nil {
			return out, err
		}
		// Write yolo's synthetic manifest only when there is not already a REAL one here.
		//
		// A pack that wraps an agent plugin has its tree delivered verbatim by DeliverPlugin,
		// manifest included — and that manifest carries the plugin's own `description`,
		// `hooks`, `mcpServers`, and anything else yolo does not model. This function then
		// runs at the same destination for the pack's ordinary skills, and overwriting the
		// manifest with a generated one silently DESTROYED all of it: the delivered plugin
		// kept its hooks/ and .mcp.json directories on disk while the manifest that points at
		// them was replaced, so the tool loaded a plugin with none of its components. Verified
		// by inspecting the delivered file — the copy is verbatim, then this undid it.
		//
		// Keeping the existing manifest is right in both directions: it is either the plugin's
		// (must not be clobbered) or one yolo wrote on a previous apply (already correct, and
		// re-writing it identical buys nothing).
		if !hasPluginManifest(packDir) {
			if err := writePluginManifest(packDir, req.Pack, req.Description); err != nil {
				return out, err
			}
		}
	}

	for _, name := range sortedKeys(skills) {
		dest := filepath.Join(nested, name)
		r := Result{
			Name: name, Path: dest, Action: ActionWrote,
			Detail: "invoke as /" + req.Pack + ":" + name,
		}
		if !req.Observe {
			// Replace yolo's own previous copy wholesale. Safe here and only here: the
			// path is inside a subtree whose manifest marks it as yolo's output.
			if err := os.RemoveAll(dest); err != nil {
				r.Action, r.Detail = ActionRefused, err.Error()
				out = append(out, r)
				continue
			}
			if err := copyTree(skills[name], dest); err != nil {
				r.Action, r.Detail = ActionRefused, err.Error()
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// deliverFlat writes the pack's skills directly into the skills dir, beside the user's own,
// with the manifest as the only record of which is which.
func deliverFlat(req Request, skills map[string]string) ([]Result, error) {
	man := req.Manifest
	if man == nil {
		man = &Manifest{Entries: map[string]string{}}
	}

	var out []Result
	// Retire what the pack no longer ships — but ONLY entries the record says yolo wrote.
	// An entry absent from the record is the user's by definition, even if its name
	// matches something the pack used to ship.
	for _, dest := range man.EntriesFor(req.Pack, req.SkillsDir) {
		if skills[filepath.Base(dest)] != "" {
			continue // still shipped; handled below
		}
		if _, err := os.Lstat(dest); err != nil {
			man.Forget(dest) // already gone; drop the stale record
			continue
		}
		r := Result{Name: filepath.Base(dest), Path: dest, Action: ActionArchived}
		if req.Observe {
			r.Detail = "would move to the archive"
		} else {
			at, aerr := Archive(req.ArchiveRoot, req.Stamp, req.Pack, dest)
			if aerr != nil {
				r.Action, r.Detail = ActionRefused, aerr.Error()
			} else {
				r.Detail = "moved to " + at
				man.Forget(dest)
			}
		}
		out = append(out, r)
	}

	if !req.Observe {
		if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
			return out, err
		}
	}

	for _, name := range sortedKeys(skills) {
		dest := filepath.Join(req.SkillsDir, name)
		_, statErr := os.Lstat(dest)
		occupied := statErr == nil
		// THE tier-B rule: an existing entry yolo cannot prove it wrote belongs to the
		// user. Skipped, reported, never overwritten. This is what makes "can I still add
		// my own skill?" a yes even on a tool with no namespace — the cost is that yolo
		// cannot update its own entry if the record was lost, which is the right way round.
		if occupied && !man.OwnedBy(dest, req.Pack) {
			owner, recorded := man.Owner(dest)
			detail := "exists and yolo has no record of writing it — left untouched"
			if recorded {
				detail = fmt.Sprintf("exists and belongs to pack %q — left untouched", owner)
			}
			out = append(out, Result{Name: name, Path: dest, Action: ActionSkippedUser, Detail: detail})
			continue
		}
		r := Result{Name: name, Path: dest, Action: ActionWrote, Detail: "invoke as /" + name}
		if !req.Observe {
			if occupied {
				// Ours from a previous apply: archive the old copy before replacing it, so
				// an in-place edit the user made to a yolo-written skill is recoverable.
				if at, aerr := Archive(req.ArchiveRoot, req.Stamp, req.Pack, dest); aerr == nil {
					r.Detail += " (previous copy archived to " + at + ")"
				}
			}
			if err := copyTreeExcept(skills[name], dest, req.excludePaths); err != nil {
				r.Action, r.Detail = ActionRefused, err.Error()
			} else {
				man.Record(dest, req.Pack)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// collectSkills walks the source dirs in order and returns {skill name -> source path},
// later sources winning a name. Only directories count: a loose .md file in a skills dir is
// not a skill to any of these tools, and copying it would put unreadable content in a real
// home. Entries listed in skip are left out (see Request.SkipSources).
func collectSkills(sources, skip []string) (map[string]string, error) {
	skipSet := map[string]bool{}
	for _, s := range skip {
		skipSet[filepath.Clean(s)] = true
	}
	out := map[string]string{}
	for _, src := range sources {
		entries, err := os.ReadDir(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue // a pack with no skills/ dir is normal
			}
			return nil, err
		}
		for _, e := range entries {
			full := filepath.Join(src, e.Name())
			if skipSet[filepath.Clean(full)] {
				continue
			}
			// Stat, not the DirEntry: a symlink to a directory is a legitimate skill (the
			// tools follow them), and Lstat-based IsDir would drop it.
			fi, err := os.Stat(full)
			if err != nil || !fi.IsDir() {
				continue
			}
			out[e.Name()] = full
		}
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
