// Package packdecl is the pack MANIFEST: what a pack declares about itself.
//
// This is the schema that replaces agents.AgentSpec. The core deliberately does not
// know what an "agent" is — a pack declares PATHS and CONTENT, and core mounts and
// stages them. A pack that installs a coding agent is just a pack whose declarations
// happen to describe one.
//
// That framing is the whole point, so it is worth stating what it buys: every
// per-agent loop in the mount assembler only ever needed paths (mount this staged
// tree there, make that dir writable, one host file may cross). None of them needed
// the concept. Keeping "agent" out of the core means adding a seventh tool is a pack
// file, not a Go change.
//
// It lives in its own package, dependency-free on the rest of the repo, because both
// the host CLI (mount assembly, `yolo pack lint`) and the in-jail entrypoint (surface
// rendering) read it, and neither should have to import the other's world.
package packdecl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ManifestName is the file a pack declares itself in, at its root.
const ManifestName = "pack.json"

func cleanJSON5(data []byte) ([]byte, error) {
	v, err := json5.Decode(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(jsonx.Plain(v))
}

// Manifest is a pack's self-declaration.
//
// Every field is OPTIONAL. A pack with only skills/ and an AGENTS.md needs no manifest
// at all — that is the common case and it must stay zero-ceremony. A manifest is how a
// pack asks for something more: a writable dir, a mount target, a host file.
type Manifest struct {
	// Name is the pack's own name for itself. The `packs` config entry may override
	// it; this is the default and what `yolo pack ls` shows.
	Name string `json:"name,omitempty"`

	// Description is one line, shown by `yolo pack ls`.
	Description string `json:"description,omitempty"`

	// SkillsTier is the pack's POSITIVE OPT-IN to having its own skills namespaced in a
	// real home (maintainer ruling 2026-08-05, roadmap.md S1/S2). Values:
	//
	//   ""            unnamespaced — every skill this pack ships is a bare name, and a
	//                 collision with another pack's bare name is FATAL at apply time
	//                 (internal/hostskills' Collisions) rather than silently resolved
	//   "flat"        the same thing said out loud
	//   "namespaced"  yolo writes ONE subtree per destination — <skills-dir>/<pack>/ with a
	//                 plugin manifest — and this pack's skills invoke as <pack>:<skill>
	//
	// PER PACK, not per contribution, and that is the whole of S2. A tier decides what a
	// skill is CALLED, which is a global property; declared per contribution it could not
	// express a consistent name, and a zero-ceremony pack (which declares no destinations at
	// all, borrowing them from the other selected packs) INHERITED a tier per destination —
	// which is how the local pack came to be namespaced in Claude and flat everywhere else
	// without ever choosing either.
	//
	// So it is the PACK's choice at every destination it reaches, never yolo's per
	// destination. The consequence, stated because it is a real trade: a pack that opts in
	// gets a namespaced subtree at EVERY destination it names, including one whose tool may
	// not load a plugin manifest. ProbeTier still refuses the shape where the destination
	// itself rules it out, but it cannot ask a tool what it supports — and under this ruling
	// guessing on the pack's behalf is the thing being removed.
	SkillsTier string `json:"skills_tier,omitempty"`

	// Supersedes is the pack's claim that some capability's job no longer needs doing,
	// so whichever loophole SERVES it can stop (docs/design/pack-capabilities.md §2).
	// Each entry carries a MANDATORY `because` — see supersedes.go for why it is a
	// top-level key rather than a 16th contribution kind, and why the asymmetry with
	// `serves` is enforced rather than merely recommended.
	Supersedes []Supersession `json:"supersedes,omitempty"`

	// Contributes is the pack's effects: one list of typed contributions, each with
	// an explicit `kind` from the closed set (see contributes.go / kinds.go). It
	// each with an explicit kind from the closed core-owned set
	// (docs/design/pack-system.md §2-§3). Read it through Contributions().
	Contributes []Contribution `json:"contributes,omitempty"`
}

// Hook is one requested imperative capability. Its extra fields are the parameters that
// hook needs; an unused one for a given hook name is an error rather than ignored, so a
// misplaced field is not a declaration that silently does nothing.
type Hook struct {
	// Name is the hook, from core's closed set (see internal/entrypoint/packhooks.go).
	Name string `json:"name"`
	// File is a home-relative file the hook acts on.
	File string `json:"file,omitempty"`
	// SharedDir is a home-relative directory from the pack's own sharedDirs, for a hook
	// that links into the machine-global tier.
	SharedDir string `json:"sharedDir,omitempty"`
}

// KnownHooks is the closed set of hook names, so a manifest can be validated on the HOST
// (where `yolo check` runs) without importing the entrypoint's implementation.
//
// Duplicating the names is the lesser evil versus a package dependency from the host CLI
// into the entrypoint; HookSetsAgree pins them together so the duplicate cannot drift.
var KnownHooks = []string{"shared_credentials", "per_jail_history", "claude_plugins"}

// Install declares a program the pack wants present in the jail.
type Install struct {
	// Kind is "npm" or "native".
	Kind string `json:"kind"`
	// Bin is the binary name on PATH, and the lazy-launcher filename.
	Bin string `json:"bin"`
	// Package is the npm package (kind == "npm"), optionally carrying a version
	// selector: `foo`, `foo@1.2.3`, `foo@next`, `@scope/foo@^1.0.0`.
	//
	// No selector means `@latest`, re-checked hourly by the launcher. A selector turns
	// that poll OFF — the registry's `latest` is not an answer to a declaration that
	// named its own version. (Until 2026-08-17 the launcher appended `@latest`
	// unconditionally, so a version was not expressible at all: `foo@1.2.3` was
	// installed as `foo@1.2.3@latest`. See internal/entrypoint/npmspec.go.)
	Package string `json:"package,omitempty"`
	// Flags are extra npm install flags.
	Flags []string `json:"flags,omitempty"`
	// InstallerURL is a curl-piped installer (kind == "native").
	//
	// This is the sharpest thing a manifest can name: a URL whose contents run as a
	// shell script. Honored only under the same origin rule as HostFiles — a fetched
	// pack cannot introduce one, because that would let a git ref execute arbitrary
	// code in the jail.
	InstallerURL string `json:"installerUrl,omitempty"`
}

// Mount stages one of the pack's own files or directories and mounts it read-only.
type Mount struct {
	// From is the pack-relative source path.
	From string `json:"from"`
	// To is the home-relative jail destination.
	To string `json:"to"`
	// HostOverlay is an optional host-home path whose content is PREPENDED to the
	// staged file (the "your own AGENTS.md first, then the pack's" case).
	//
	// Part of the credential boundary: it reads the host home, so it obeys the same
	// origin rule as HostFiles.
	HostOverlay string `json:"hostOverlay,omitempty"`
}

// HostFile is one host-home file to mount read-only into the jail.
type HostFile struct {
	// From is the host-home-relative source (e.g. ".claude/settings.json").
	From string `json:"from"`
	// To is the jail destination under /ctx. Empty means /ctx/host-<pack>/<basename>,
	// which is what the built-in agents used.
	To string `json:"to,omitempty"`
}

// Decode parses a manifest, reporting EVERY problem rather than the first so a pack
// author fixing one gets the whole list instead of one edit-check cycle per mistake.
//
// Strict about unknown fields: a misspelled key would otherwise be a declaration that
// silently does nothing, and the author would have no signal at all.
//
// Use DecodeTolerant instead when reading a manifest that some OTHER build wrote — see its
// doc for why the strictness has to stop at the version boundary.
func Decode(data []byte) (*Manifest, []string) {
	clean, err := cleanJSON5(data)
	if err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(clean))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}
	}
	return &m, append(m.retiredFieldProblems(), m.Validate()...)
}

// retiredFieldProblems reports a field this build has RETIRED — decodable, so the strict
// decoder does not answer it with an unhelpful `json: unknown field`, but refused with the
// migration named.
//
// AUTHORING-ONLY, and that boundary is the whole point rather than a detail. It is NOT part of
// Validate, so DecodeTolerant does not run it: a retired field is a VERSION-SKEW fact, which is
// exactly the class the tolerant decoder exists to survive. Making it a validation problem
// reproduced the original `tier` incident in mirror image — a manifest yolo SHIPS gained
// `skills_tier`, and an older baked entrypoint reading the newly-staged tree refused it:
//
//	yolo-entrypoint: refusing to start the jail: 2 config generator(s) failed:
//	  - load_packs: pack claude: contributes[2]: "tier" is no longer a contribution field …
//
// Caught by running a nested jail against the previous baked image. The asymmetry is right in
// both directions: an author must hear that their declaration is retired, and a jail must still
// boot when the two ends of the version boundary disagree about which fields exist.
func (m *Manifest) retiredFieldProblems() []string {
	var problems []string
	for i, c := range m.Contributes {
		if c.Tier == "" {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"contributes[%d]: \"tier\" is no longer a contribution field — a tier decides what a "+
				"skill is CALLED, which is one fact about the whole pack rather than one per "+
				"destination. Move it to the manifest's top-level \"skills_tier\": %q", i, c.Tier))
	}
	return problems
}

// DecodeTolerant parses a manifest, IGNORING fields — and SKIPPING contribution kinds —
// this build does not know.
//
// The strictness in Decode is right for authoring — a misspelled key that silently does
// nothing is the worst outcome for a pack author — but it is wrong across a VERSION
// BOUNDARY, and the jail is exactly that. The host CLI and the in-jail `yolo-entrypoint`
// come from different places (the CLI is `go install`ed or freshly built; the entrypoint is
// baked into the image at the last `just load`), so a newer CLI staging a pack that uses a
// newer manifest field is a NORMAL state, not a corruption.
//
// Verified the hard way: adding the `tier` field to `skills` made every jail refuse to start
// against an older baked image —
//
//	yolo-entrypoint: refusing to start the jail: 2 config generator(s) failed:
//	  - load_packs: pack claude: pack.json: json: unknown field "tier"
//
// with no route to recovery except rebuilding the image, since the failing manifest is one
// yolo SHIPS. A field the entrypoint cannot use is a feature it cannot render, which is a
// degraded jail; a field it refuses to read is no jail at all. The first is recoverable and
// the second is not, so the version boundary reads tolerantly.
//
// An unknown `via` VALUE on a `program` is the same class one level DOWN, and it is paid
// here ahead of the third delivery mechanism rather than after it (program-delivery.md §6.2,
// risk R6). `via` is a closed two-value set, so a pack declaring `via: "uv"` staged for an
// older baked entrypoint would be a refused boot — the `tier` shape a fourth time. An EMPTY
// `via` is NOT skew and stays a hard problem on both paths: a program that names no
// mechanism installs nothing, which is a defect both ends of the version boundary understand.
//
// An unknown contribution KIND is the same class, one level up (loophole-packaging §3.3a —
// the `tier` incident's shape, third time): a newer build's kind staged for an older baked
// entrypoint is skew, not corruption, and validating it as structure made the jail refuse
// to boot. So a contributes entry whose kind this build does not know is DROPPED from the
// returned manifest and reported in `skipped`, one note per entry, naming the kind — never
// returned as a problem, because the boot path treats any problem as fatal (A12). The
// asymmetry is right in both directions: an author must hear that their declaration is
// unknown (Decode → Validate still refuses it loudly), and a jail must boot when the two
// ends of the version boundary disagree about which kinds exist.
//
// Structural validation still runs over what is KEPT, so a manifest that is malformed in a
// way BOTH builds understand (a missing "kind", a missing required field) still fails
// loudly here — with each problem labeled by the entry's ORIGINAL index, the one the
// author sees in pack.json.
func DecodeTolerant(data []byte) (m *Manifest, problems, skipped []string) {
	clean, err := cleanJSON5(data)
	if err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}, nil
	}
	var man Manifest
	if err := json.Unmarshal(clean, &man); err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}, nil
	}
	problems = append(man.validateSkillsTier(), man.validateSupersedes()...)
	kept := make([]Contribution, 0, len(man.Contributes))
	for i, c := range man.Contributes {
		if c.Kind != "" && !KnownKind(c.Kind) {
			skipped = append(skipped, fmt.Sprintf(
				"contributes[%d]: skipping unknown kind %q — this build does not know it, "+
					"so the contribution is not rendered (version skew; a build that "+
					"knows the kind will render it)", i, c.Kind))
			continue
		}
		if note := unknownViaSkip(i, c); note != "" {
			skipped = append(skipped, note)
			continue
		}
		problems = append(problems, validateContributionAt(i, c)...)
		kept = append(kept, c)
	}
	if len(skipped) > 0 {
		man.Contributes = kept
	}
	return &man, problems, skipped
}

// unknownViaSkip returns the skew note for a `program` whose `via` names a delivery
// mechanism this build does not know, or "" when there is nothing to skip.
//
// The VALUE-level twin of the unknown-KIND rule in DecodeTolerant, deliberately kept
// beside it: both drop the contribution and report it, and the strict path
// (validateContribution) still refuses both loudly, so an author hears and a jail boots.
//
// An empty `via` returns "" — it is a hard problem on BOTH paths, never skew.
//
// The set it tests against is KnownVia's and not a third spelling of the two values:
// the day a `uv` lands, this skip and validateContribution's refusal have to change
// together or a manifest validates for its author and installs nothing in the jail
// (knownVias in contributes.go records the measurement).
func unknownViaSkip(i int, c Contribution) string {
	if c.Kind != KindProgram || c.Via == "" || KnownVia(c.Via) {
		return ""
	}
	return fmt.Sprintf(
		"contributes[%d]: skipping unknown via %q for program %q — this build does not know it, "+
			"so the contribution is not rendered (version skew; a build that "+
			"knows the via will render it)", i, c.Via, c.Bin)
}

// Validate reports every structural problem — the pack-level fields, then per-kind over
// contributes[].
func (m *Manifest) Validate() []string {
	problems := m.validateSkillsTier()
	problems = append(problems, m.validateSupersedes()...)
	return append(problems, m.validateContributions()...)
}

// validateSkillsTier rejects a misspelled `skills_tier`.
//
// An ERROR, not a silent downgrade, for the reason the per-contribution field's check gave and
// one more the move makes sharper: this is now a PACK-level declaration, so reading it as flat
// would unnamespace every skill the pack ships at every destination — and the author's only
// symptom would be collisions they did not cause.
//
// THE ENUM IS SKEW-SENSITIVE, and this check runs unconditionally on the TOLERANT path too.
// That is right while there are exactly two values (a third spelling is a typo both ends of
// the version boundary agree about), and it becomes the `tier` incident a FOURTH time the day
// a third tier VALUE ships: a newer host staging it bricks every jail on a pre-`just load`
// image, exactly as an unknown contribution kind used to (loophole-packaging.md §3.3a).
// Whoever adds a value extends the tolerance first — unknown values skipped-and-reported
// under DecodeTolerant, refused loudly under Decode — and only then the value.
func (m *Manifest) validateSkillsTier() []string {
	switch m.SkillsTier {
	case "", "flat", "namespaced":
		return nil
	}
	return []string{fmt.Sprintf("skills_tier: unknown tier %q (flat or namespaced)", m.SkillsTier)}
}

// WantsNamespacedSkills reports whether this pack opted in to namespacing. THE accessor for
// the tier, so no reader compares the string itself — the `tier` field this replaced was read
// in four places and one of them (mergedest's inheritance) is exactly what S2 removed.
func (m *Manifest) WantsNamespacedSkills() bool { return m.SkillsTier == "namespaced" }

// appendPathProblems rejects a path that escapes the tree it is relative to.
//
// Absolute paths and `..` are both refused: every path in a manifest is relative to
// either the pack root, the jail home, or the host home, and a pack must not be able
// to reach outside whichever one it was given. A fetched pack could otherwise name
// "../../etc/shadow" and have core mount it.
func appendPathProblems(problems []string, field, p string) []string {
	if p == "" {
		return problems
	}
	if strings.HasPrefix(p, "/") {
		return append(problems, field+": must be relative, not absolute ("+p+")")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return append(problems, field+": must not contain \"..\" ("+p+")")
		}
	}
	if strings.Contains(p, ":") {
		// A colon would be parsed as a mount-option separator by the container
		// runtime, silently turning part of the path into a flag.
		return append(problems, field+": must not contain \":\" ("+p+")")
	}
	return problems
}

// appendJailPathProblems refuses a home-relative destination that would land on the
// jail's PATH — the dir itself, a parent of it, or anything inside it.
//
// All three, because all three reach PATH. Naming `.local/bin` IS the PATH dir; naming
// `.local` mounts a tree whose own `bin/` then sits on PATH; naming `.local/bin/tools`
// puts files in it. A rule that caught only the exact match would be a rule an author
// routes around by accident on the first try.
//
// THE REFUSAL POINTS AT `program`, because that is what this is for. A name on PATH is
// something a pack DECLARES (kind "program": it owns the launcher filename, it is
// exclusive by that name, it is disclosed at launch and recorded in the footprint), not
// something it achieves by dropping a file where the shell will find it. The second
// route claims nothing, collides silently, and makes the footprint a lie. It is NOT a
// containment boundary and the message must never imply one — see paths.JailPathHomeDirs.
func appendJailPathProblems(problems []string, field, p string) []string {
	if p == "" {
		return problems
	}
	clean := path.Clean(strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(p)), "/"))
	if clean == "" || clean == "." {
		return problems
	}
	for _, dir := range paths.JailPathHomeDirs {
		if clean == dir ||
			strings.HasPrefix(clean, dir+"/") || // inside a PATH dir
			strings.HasPrefix(dir, clean+"/") { // a parent of one
			return append(problems, field+": "+p+" is on the jail's PATH (~/"+dir+
				"). A pack puts a name on PATH by DECLARING it — kind \"program\", which "+
				"owns the launcher and says so in the footprint — not by delivering a file "+
				"where the shell happens to look.")
		}
	}
	return problems
}

// NeedsHostAccess reports whether honoring this manifest requires reading the host
// home or running a fetched installer — the declarations gated on pack ORIGIN.
//
// Collected in one predicate so a caller cannot check two of the three and believe it
// covered the boundary. That mistake already happened once, when "the credential
// boundary" was treated as AgentSpec.HostFiles alone while Briefing.HostSource and
// Skills read the host home too.
func (m *Manifest) NeedsHostAccess() []string {
	// Routes through the contributions (NeedsHostAccessContributions): the origin
	// gate is "any reads-host, program-via-installer, or host-prepending briefing"
	// (docs/design/pack-system.md §9).
	return m.NeedsHostAccessContributions()
}

func knownHook(name string) bool {
	for _, k := range KnownHooks {
		if k == name {
			return true
		}
	}
	return false
}
