package hostskills

// deliver.go is the render of ONE LAYER of a composition: put one pack's skills into one real
// skills dir, at the tier that dir supports, without ever touching content yolo cannot prove it
// owns.
//
// The invariants, in priority order — each overrides the ones below it:
//
//  1. NEVER touch what yolo did not write. A destination entry that is not provably yolo's
//     (a marked plugin dir at tier A, a recorded or claimed path at tier B) is the user's. It is
//     reported and skipped, never overwritten and never removed. The ONE exception is a
//     DANGLING symlink, which is not content at all — see dangling.go for why that follows
//     from this rule rather than bending it.
//  2. NEVER delete; archive. Retiring goes through archive.go so being wrong about
//     ownership costs a `mv` back rather than the user's work.
//  3. NEVER write in observe posture. Everything computes identically in both postures and
//     only the writes are gated, so the dry run cannot disagree with the real one.
//  4. ALWAYS report. Every entry produces a line — written, skipped, archived, refused —
//     because a silent skip is the failure mode this whole body of work exists to remove.
//
// WHAT COMPOSITION CHANGED HERE, and it is one rule rather than a rewrite (compose.go): the
// tier-B question stopped being "did THIS PACK write this entry?" and became "did yolo compose
// it?". The old form refused a later pack overwriting an earlier pack's recorded name at all,
// which was right for two shared packs contesting a name and wrong for the local pack, the layer
// defined as outranking everything (§6a-5). Now precedence lives in the LAYER ORDER and this file
// only asks whether the name is yolo's to write.

import (
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
	// ActionCleared is a DANGLING symlink yolo removed to make room. Distinct from a plain
	// write because "yolo unlinked something in my home" is a fact the user must be able to
	// scan for, not one to find inside another line's detail.
	ActionCleared Action = "cleared a dangling symlink"
	// ActionRefused is an entry yolo declined to deliver, with a reason.
	ActionRefused Action = "refused"
	// ActionMoved is a user's skill MIGRATED into the local pack — the §6a-2 answer, and the one
	// action that describes content LEAVING a destination without being archived. Distinct from
	// ActionArchived precisely because the outcome is different in kind: an archived skill is
	// recoverable, a moved one is still being composed back into every destination.
	ActionMoved Action = "moved to your local pack"
	// ActionUnioned is a redundant per-agent duplicate: byte-identical to a copy already in the
	// local pack, so the union absorbed it. Its own action because "yolo removed a directory from
	// my home" must be scannable even when nothing was lost by it.
	ActionUnioned Action = "unioned into your local pack"
	// ActionRenamed is a real name conflict resolved by keeping BOTH under a suffix. The loudest
	// migration outcome, and the only one the user has to make a judgement about.
	ActionRenamed Action = "kept both (renamed)"
)

// The OBSERVE-posture wordings. The tense lives in the ACTION, not only in the detail, because
// the action column is what a reader scans — and a dry run that says `rendered` reads as though
// it mutated the home, which is precisely the fear a dry run exists to allay. Every other kind
// already speaks in the future here (`would render`, `would archive`); skills did not.
//
// Only the actions that describe a WRITE get a future form. `skipped (yours)` and `refused`
// describe the ABSENCE of an action and are identical in both postures, so there is no tense to
// correct: rewording them to "would skip" would imply the assert run might do something else.
const (
	ActionWouldWrite   Action = "would render"
	ActionWouldArchive Action = "would archive"
	ActionWouldClear   Action = "would clear a dangling symlink"
	ActionWouldMove    Action = "would move to your local pack"
	ActionWouldUnion   Action = "would union into your local pack"
	ActionWouldRename  Action = "would keep both (renamed)"
)

// wroteAction and friends pick the action word for the posture. Separate tiny functions rather
// than one map so a caller cannot accidentally ask for the wrong pair.
func wroteAction(observe bool) Action {
	if observe {
		return ActionWouldWrite
	}
	return ActionWrote
}

func archivedAction(observe bool) Action {
	if observe {
		return ActionWouldArchive
	}
	return ActionArchived
}

func clearedAction(observe bool) Action {
	if observe {
		return ActionWouldClear
	}
	return ActionCleared
}

func movedAction(observe bool) Action {
	if observe {
		return ActionWouldMove
	}
	return ActionMoved
}

func unionedAction(observe bool) Action {
	if observe {
		return ActionWouldUnion
	}
	return ActionUnioned
}

func renamedAction(observe bool) Action {
	if observe {
		return ActionWouldRename
	}
	return ActionRenamed
}

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
	// Composed is the composition's ownership record (path -> the pack that composed it). It
	// answers the ONE question a layer render asks — "is this name yolo's to write?" — and is
	// recorded into as the layer writes, so the composition's retire pass can tell its own output
	// from the residue of an older one.
	Composed *Manifest
	// Legacy is the pre-composition per-entry record, read-only. A path it names is yolo's own
	// output from before this mechanism, so overwriting it is an update rather than a first
	// adoption of the user's work.
	Legacy *Manifest
	// PreOwned is the set of paths the composed record owned BEFORE this apply, snapshotted by the
	// caller. It is what makes "archive the previous copy before replacing it" mean the previous
	// APPLY's copy: without it the second layer to claim a name would archive the first layer's
	// brand-new write, producing an archive entry per apply for content that was never in the
	// user's home.
	PreOwned map[string]bool
	// Claimed maps every path this composition wrote to the PACK that wrote it, shared across the
	// destination's layers. Three jobs, and they are one fact read three ways: the composition's
	// retire pass subtracts its key set from the record to find stale entries; an EARLIER layer's
	// claim tells a later one that a name is yolo's this run, before the record is saved; and the
	// pack NAME is what lets a plugin-name collision say whose plugin it is colliding with.
	Claimed map[string]string
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
//
// The SUBTREE, not each skill inside it, is what the composed record names: it is the unit the
// composition owns, and it is what has to be archived whole when the pack stops contributing here.
// The leaf-level retire below needs no record at all — inside yolo's own subtree the stale set is
// simply "what is there minus what we ship", which is tier A's whole advantage.
func deliverNamespaced(req Request, skills map[string]string) ([]Result, error) {
	packDir := filepath.Join(req.SkillsDir, req.Pack)
	nested := filepath.Join(packDir, "skills")
	claim(req, packDir)

	// Clear dangling directory links FIRST, before the retire scan reads the subtree: a stale
	// link at <skillsDir>, at <skillsDir>/<pack> or at its skills/ is a name MkdirAll can
	// neither use nor create, which aborted the whole delivery with `mkdir …: file exists`.
	// Reported per link, so an unlink in a real home is never silent.
	out := clearDanglingDirs(req.SkillsDir, nested, req.Observe)

	// Retire next: a skill the pack no longer ships must stop being loaded, and inside
	// yolo's own subtree the set of stale entries is simply "what is there minus what we
	// ship" — no manifest needed, which is tier A's whole advantage.
	if existing, err := os.ReadDir(nested); err == nil {
		for _, e := range existing {
			if !e.IsDir() || skills[e.Name()] != "" {
				continue
			}
			stale := filepath.Join(nested, e.Name())
			r := Result{Name: e.Name(), Path: stale, Action: archivedAction(req.Observe)}
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
			return out, mkdirError(nested, err)
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
			Name: name, Path: dest, Action: wroteAction(req.Observe),
			Detail: "invoke as /" + req.Pack + ":" + name,
		}
		if !req.Observe {
			// Replace yolo's own previous copy wholesale. Safe here and only here: the
			// path is inside a subtree whose manifest marks it as yolo's output. RemoveAll
			// also clears a dangling link at this name without following it, and needs no
			// report: inside yolo's own subtree it was never the user's to begin with.
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

// deliverFlat writes the pack's skills directly into the skills dir, beside the user's own, with
// the composed record as the only evidence of which is which.
//
// THERE IS NO PER-PACK RETIRE HERE any more, and its absence is the composition (compose.go's
// retireComposed). A per-pack retire could only see "what this pack recorded last time minus what
// it ships now", which at flat tier is wrong in both directions once several packs merge into one
// dir: a name that moved from pack A to pack B between applies read as A retiring it and B being
// refused. The destination-wide pass sees the union, so a name that moved simply changes hands.
func deliverFlat(req Request, skills map[string]string) ([]Result, error) {
	// The skills DIR ITSELF may be a dangling link — the `~/.pi/agent/skills` shape in the
	// field report, where the whole directory was deployed as one link. That aborted the entire
	// delivery with a bare `mkdir …: file exists` and no per-entry report, which is the same
	// defect as the leaf case but louder and less legible.
	out := clearDanglingDirs(req.SkillsDir, req.SkillsDir, req.Observe)
	if !req.Observe {
		if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
			return out, mkdirError(req.SkillsDir, err)
		}
	}

	for _, name := range sortedKeys(skills) {
		dest := filepath.Join(req.SkillsDir, name)
		_, statErr := os.Lstat(dest)
		occupied := statErr == nil
		// A dangling symlink is ABSENT, not occupied. It reads as occupied to Lstat (which is
		// the right call for every other shape — a broken link does hold the name), so without
		// this it fell into the rule below and the pack was reported "skipped (yours)" against a
		// pointer to a file that no longer existed: permanently inert, and phrased as a safe
		// no-op. Clearing it is AUTOMATIC rather than offered, because there is no content to
		// weigh: the alternative is prompting the user about a file that is not there.
		if occupied {
			if target := danglingLink(dest); target != "" {
				out = append(out, Result{
					Name: name, Path: dest, Action: clearedAction(req.Observe),
					Detail: "was → " + target + ", which no longer exists (a stale dotfile-manager " +
						"link, most likely) — nothing to archive, delivering over it",
				})
				if !req.Observe {
					if err := clearLinks([]clearedLink{{Path: dest, Target: target}}); err != nil {
						out[len(out)-1].Action = ActionRefused
						out[len(out)-1].Detail = err.Error()
						continue
					}
				}
				// The name is free now. In observe posture nothing was unlinked, but the
				// computation must match the write path exactly or the dry run would report a
				// skip the real run does not make.
				occupied = false
				forget(req, dest)
			}
		}
		// THE tier-B RULE, and composition rewrote it: an existing entry YOLO cannot prove it
		// composed belongs to the user. Skipped, reported, never overwritten. This is still what
		// makes "can I add my own skill?" answerable on a tool with no namespace — but the question
		// is no longer "did THIS PACK write it", which is what made a later pack unable to overwrite
		// an earlier one's name regardless of order (§6a-5). Under composition the layer order IS
		// the precedence, so a later layer legitimately takes a name an earlier one just wrote.
		//
		// The user's own entry is nonetheless only ever REACHED here on a destination whose
		// adoption was declined or unconfirmable — the confirmed path migrates it out first — which
		// is why this stays a report rather than becoming an impossibility.
		if occupied && !ownedHere(req, dest) {
			out = append(out, Result{Name: name, Path: dest, Action: ActionSkippedUser,
				Detail: "exists and yolo has no record of composing it — left untouched " +
					"(confirm the adoption to migrate it into your local pack)"})
			continue
		}
		r := Result{Name: name, Path: dest, Action: wroteAction(req.Observe),
			Detail: "invoke as /" + name}
		if !req.Observe {
			// Ours from a PREVIOUS APPLY, and about to CHANGE: archive the old copy before replacing
			// it, so an in-place edit the user made to a yolo-written skill is recoverable. Two
			// conditions, and each rules out an archive of something that was never at risk:
			//
			//   - PreOwned, not `occupied`: a name two layers of THIS composition both claim would
			//     otherwise archive the earlier layer's brand-new write, which was never in the
			//     user's home to lose.
			//   - a CHANGED digest. The old rule archived on every apply whatever the content, so an
			//     unchanged home grew one archive copy of every skill per `yolo host apply` forever.
			//     Pre-existing, and composition made it louder rather than caused it: this pass now
			//     visits every destination in one run instead of one pack's at a time.
			if occupied && req.PreOwned[dest] && Changed(skills[name], dest) {
				if at, aerr := Archive(req.ArchiveRoot, req.Stamp, "skills", dest); aerr == nil {
					r.Detail += " (previous copy archived to " + at + ")"
				}
			}
			if err := copyTreeExcept(skills[name], dest, req.excludePaths); err != nil {
				r.Action, r.Detail = ActionRefused, err.Error()
				out = append(out, r)
				continue
			}
		}
		claim(req, dest)
		out = append(out, r)
	}
	return out, nil
}

// ownedHere reports whether dest is YOLO'S to write — not whether it is THIS PACK's, which is the
// whole of the §6a-5 fix (see compose.go's record comment). Three ways to be yolo's: claimed by an
// earlier layer of this composition, composed by a previous apply, or delivered by the
// pre-composition per-entry record.
//
// The Claimed half is what makes layer order the precedence. Without it the second layer to reach a
// name would consult only the saved record — which in observe posture is never written at all — and
// report `skipped (yours)` against content the first layer had just composed, so the dry run would
// disagree with the write on exactly the collision this change is about.
func ownedHere(req Request, dest string) bool {
	if _, claimed := req.Claimed[dest]; claimed {
		return true
	}
	if req.Composed != nil {
		if _, ok := req.Composed.Owner(dest); ok {
			return true
		}
	}
	if req.Legacy != nil {
		_, ok := req.Legacy.Owner(dest)
		return ok
	}
	return false
}

// claim records dest as this composition's output, in both the record and the run's claim set.
// The claim set is populated even in OBSERVE (a later layer must resolve the same way in both
// postures); the record is not, since the caller only persists it after a real write.
func claim(req Request, dest string) {
	claimPath(req.Composed, req.Claimed, dest, req.Pack, req.Observe)
}

// claimPath is claim over the fields rather than the Request, so the plugin delivery — whose
// request is a different type carrying the same four fields — cannot grow a second copy of the
// rule.
//
// Both get the real pack name, so `yolo host apply` can still answer ruling R1's question ("did a pack
// that LEFT my config put this here?"). What makes that safe — where the old per-pack rule was not —
// is that the write gate reads MEMBERSHIP rather than equality (ownedHere), so recording a name
// never means refusing a later layer's claim to it.
func claimPath(composed *Manifest, claimed map[string]string, dest, pack string, observe bool) {
	if claimed != nil {
		claimed[dest] = pack
	}
	if !observe && composed != nil {
		composed.Record(dest, pack)
	}
}

// forget drops dest from the composed record, after its content has been cleared or archived.
func forget(req Request, dest string) {
	if !req.Observe && req.Composed != nil {
		req.Composed.Forget(dest)
	}
}

// Changed reports whether delivering src over dest would alter it, by content digest.
//
// An UNREADABLE side reads as CHANGED, which is the direction that archives rather than skips: the
// only consequence of a false positive is one recoverable copy in the archive, while a false
// negative would replace content nobody could compare without keeping a copy of it.
//
// Exported for the `files` kind (internal/entrypoint/hostfilestree.go), which archived its
// destination on EVERY apply because it asked only whether the path was occupied — see F7. The
// two kinds ask the same question of a tree, so a second digest would be a second thing to drift.
func Changed(src, dest string) bool {
	a, aerr := treeDigest(src)
	b, berr := treeDigest(dest)
	return aerr != nil || berr != nil || a != b
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
