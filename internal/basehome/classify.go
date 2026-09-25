// Package basehome detects legacy per-workspace agent state in the BASE home, <state>/home
// (paths.GlobalHome) — the one host directory every podman jail used to mount as its home,
// a union of tiers that were never supposed to share a directory. Nothing mounts it at
// /home/agent any more: each podman jail gets its own read-only skeleton
// (docs/design/base-home-legacy-state.md#1-the-question-and-the-answer), and <state>/home
// stays only as the machine store of the shared dirs and the Claude login seed. So the
// legacy bytes this package finds are unmounted and unread, and its one consumer is `yolo
// check`'s detection-only report, whose printed `mv` is optional cleanup (the maintainer's
// OQ-BH13 ruling).
//
// THE BARE § NUMBERS IN THIS PACKAGE, code and tests, cite the PRE-REWRITE design: the
// quarantine that was dropped once the shared base itself was found to be the defect. Read
// them in `git show 030c8f52:docs/design/base-home-legacy-state.md` — §5.1 Detection, §5.2
// Classification, §5.8 Disclosure, §6 Prevention, §8 What this does not cover, §11
// Sequencing. The current doc, rewritten in 8c7eb2b9, numbers its sections differently and
// keeps none of those rules as design; they survive here only as what the report
// classifies by.
//
// THIS PACKAGE OBSERVES. It opens no file, follows no symlink, and writes nothing: the
// walk is Lstat/ReadDir only. Every mutating verb the pre-rewrite design named — the move,
// the manifest, the marker, the confirmation prompt — was never built, and the current
// design drops them, so no amount of misuse of this package can touch a byte of the base
// home.
//
// The DECLARATION SETS ARE INPUTS (Decls), not reads of the shipped packs. Classification
// is a derive across three separate declaration systems (state dirs, config surfaces,
// content destinations) plus the packs' own credential hooks, and the design's warning is
// that the derive is the risky part. Taking it as a value means the classification table
// is testable against a fixture that states exactly what it assumes, rather than against
// whatever the shipped manifests happen to say this month. DeclsFromPacks (decls.go) is
// the one place that reads the real packs.
package basehome

import (
	"path/filepath"
	"strings"
)

// Class is what the migration may do with a path.
//
// The zero value is Unclassified on purpose. The two alternatives both hide a bug: a zero
// value meaning Credential would make a forgotten assignment look like a successful "keep
// everything", and a zero value meaning Runtime would make it look like a candidate nobody
// examined. Unclassified is a candidate that says so, and §5.1 already requires it to be
// counted and reported.
type Class int

const (
	// Unclassified is an entry the walk could not decide about — unreadable, or a path it
	// could not make home-relative. A candidate, and named in the disclosure as one.
	Unclassified Class = iota
	// Credential is login state: excluded outright, permanently (§8).
	Credential
	// Config is a path a pack's config surface names: kept in place.
	Config
	// Content is a declared skills/briefing/files destination: kept in place.
	Content
	// Runtime is per-workspace churn — transcripts, session stores, caches. The bytes the
	// quarantine exists to evict.
	Runtime
)

func (c Class) String() string {
	switch c {
	case Credential:
		return "CREDENTIAL"
	case Config:
		return "CONFIG"
	case Content:
		return "CONTENT"
	case Runtime:
		return "RUNTIME"
	default:
		return "UNCLASSIFIED"
	}
}

// Candidate reports whether a class would be moved by the apply — §5.1's rule exactly:
// RUNTIME, or not classifiable at all.
func (c Class) Candidate() bool { return c == Runtime || c == Unclassified }

// Decls is the declaration set the classifier reads. Every path is HOME-RELATIVE (for
// example ".claude/settings.json"), never absolute and never "~/"-prefixed: HomeRel does
// that normalization once, where the declarations are read.
type Decls struct {
	// StateDirs are the packs' workspace-scope state dirs: the walk roots (§5.1 bullet 1).
	StateDirs []string
	// SharedDirs are the machine-scope shared dirs. RULE 1 IS STRUCTURAL over this set:
	// the walk cannot descend into one even when handed it as a root, and a path under one
	// is CREDENTIAL. These dirs are home-root siblings of the state dirs, so nothing under
	// a state dir reaches them except a symlink or a nested copy.
	SharedDirs []string
	// CredentialFiles are the home-relative `from` paths of the packs' shared_credentials
	// hook declarations. Matched two ways: at-or-under the path, and by basename anywhere
	// (a credential that moved one directory is still a credential).
	CredentialFiles []string
	// ConfigSurfaces are the home-relative paths of the packs' config surfaces. A surface
	// names a leaf file or a directory that owns its subtree; one containment test serves
	// both, because a leaf has no children.
	ConfigSurfaces []string
	// ContentDests are the home-relative skills/briefing/files destinations.
	ContentDests []string
	// Redirects maps a home-root file to where the base home actually keeps it, so the
	// surface `~/.claude.json` is matched against `.claude/claude.json`. Without it the
	// file holding `oauthAccount` classifies RUNTIME.
	Redirects []Redirect
	// NonPackDirs are the home-relative directories core provisions or owns. Only their
	// TOP-LEVEL segment is used, and only by the unknown-top-level sweep.
	NonPackDirs []string
	// SweepUnknownTopLevel enables §5.1's third root bullet: every other top-level
	// directory under the home that is neither core's nor a declared dir — the
	// retired/unknown-pack case.
	SweepUnknownTopLevel bool
	// Problems are declaration-side degradations the disclosure must carry: a pack whose
	// surfaces could not be decoded (its config files would classify RUNTIME), or an empty
	// pack set (detection is inert). Recorded rather than returned, because detection never
	// aborts.
	Problems []string
}

// Redirect is one home-root file and where the base home keeps its bytes. It mirrors
// paths.HomeFileRedirect, kept as a local type so this package's inputs are plain data.
type Redirect struct {
	Name   string
	Target string
}

// coreCredentialBasenames is the small core fallback of §5.2 step 2, and it is NOT a
// belt-and-braces addition: only two shipped packs declare a shared_credentials hook, so
// this list is what carries codex (`.codex/auth.json`) and anything else whose login file
// no pack declares. A classifier that is purely derived archives those logins.
var coreCredentialBasenames = []string{".credentials.json", "auth.json", "oauth_creds.json"}

// sqliteSidecarSuffixes are the SQLite sibling files that must share their database's
// fate (§5.2, §5.4). They are here as a CLASSIFICATION rule rather than a move-time
// grouping rule, so the unit holds without a move to observe: moving `X-wal` while `X` is
// kept corrupts the database, and the only way that arises is a declaration naming `X`.
// The rollback-journal spelling (`-journal`) is deliberately not here — the design names
// the WAL pair, and adding a fourth name is a claim about SQLite this design did not make.
var sqliteSidecarSuffixes = []string{"-wal", "-shm"}

// Classify applies §5.2's ordered rules to one home-relative path.
//
// It never returns Unclassified: an undecidable path is an artifact of READING the tree,
// not of the rules, so the walk assigns that class (detect.go) and this function stays a
// pure function of the declarations.
func (d Decls) Classify(rel string) Class {
	return d.classify(rel, true)
}

func (d Decls) classify(rel string, sidecars bool) Class {
	rel = normRel(rel)
	if rel == "" || rel == "." {
		// The home itself, or a path that is not home-relative at all (normRel rejects
		// absolute and escaping paths). Neither is an entry this walk produces, and the
		// answer is the KEPT direction so a caller that fabricates one can never turn it
		// into a candidate.
		return Credential
	}

	// 1. Under a declared machine-scope shared dir.
	if d.underSharedDir(rel) {
		return Credential
	}

	// 2. A declared credential path, or a credential basename anywhere.
	base := filepath.Base(rel)
	for _, c := range d.CredentialFiles {
		c = normRel(c)
		if c == "" {
			continue
		}
		if underOrAt(rel, c) || filepath.Base(c) == base {
			return Credential
		}
	}
	for _, name := range coreCredentialBasenames {
		if base == name {
			return Credential
		}
	}

	// 3. A path a config surface names, joined through the redirects.
	for _, s := range d.ConfigSurfaces {
		if underOrAt(rel, d.redirect(s)) {
			return Config
		}
	}

	// 4. A declared content destination.
	for _, c := range d.ContentDests {
		if underOrAt(rel, c) {
			return Content
		}
	}

	// 4.5 A SQLite sidecar of a KEPT database is kept with it. Checked after the kept
	// rules and before the RUNTIME fallback, so a sidecar of a runtime database stays
	// runtime (the common case, and the whole point of the eviction).
	if sidecars {
		for _, suffix := range sqliteSidecarSuffixes {
			if !strings.HasSuffix(base, suffix) {
				continue
			}
			db := strings.TrimSuffix(rel, suffix)
			if c := d.classify(db, false); !c.Candidate() {
				return c
			}
		}
	}

	// 5. Everything else.
	return Runtime
}

// ProvisionedDir reports whether rel is EXACTLY one of the directories core provisioned in
// the base home (paths.BaseHomeCoreDirs).
//
// Such a directory WAS A MOUNTPOINT: storage.EnsureGlobalStorage created it in the shared
// base every podman jail bound :ro at /home/agent, because the OCI runtime cannot mkdirat
// inside a read-only bind. Neither is true any more — each jail's skeleton carries core's
// dirs, and nothing creates them in <state>/home — but every base an older yolo provisioned
// still holds them, empty. So the mountpoint rule that keeps a walk root out of the
// candidate list still applies to one, for the same reason one level down: an empty
// directory core made is not legacy state, and §5.2's "a directory with no kept leaf
// beneath it moves whole" would otherwise report it as a candidate.
//
// `.pi/agent` is the live case: `.pi` is a pack state dir and therefore a walk root, while
// `.pi/agent` is core's, so the nested entry is reached as a child of a root and the
// top-level exclusion never sees it. MEASURED on a fresh host — an empty `.pi/agent` was
// reported as a candidate until this predicate existed.
//
// The match is EXACT on purpose: a file or directory INSIDE a provisioned dir is ordinary
// churn and classifies by the normal rules (`.pi/agent/debug.log` moves; `.pi/agent` does
// not).
func (d Decls) ProvisionedDir(rel string) bool {
	rel = normRel(rel)
	if rel == "" {
		return false
	}
	for _, p := range d.NonPackDirs {
		if normRel(p) == rel {
			return true
		}
	}
	return false
}

// underSharedDir is rule 1's predicate, used BOTH by the classifier and by the walk's
// descend decision. One predicate, two callers, because "the walk must never descend into
// one" is a structural claim and a structure has to be enforced where the descent happens.
func (d Decls) underSharedDir(rel string) bool {
	rel = normRel(rel)
	for _, s := range d.SharedDirs {
		if underOrAt(rel, normRel(s)) {
			return true
		}
	}
	return false
}

// redirect maps a declared home-relative path through the redirect table. Applied to the
// DECLARATION, never to the walked path: the walk sees `.claude/claude.json` and the
// declaration says `.claude.json`, so the mapping runs on the side that needs moving.
func (d Decls) redirect(rel string) string {
	rel = normRel(rel)
	for _, r := range d.Redirects {
		if normRel(r.Name) == rel {
			return normRel(r.Target)
		}
	}
	return rel
}

// underOrAt reports whether rel IS base or lies beneath it. One test for both surface
// shapes §5.2 step 3 names: a directory surface owns its subtree, and a leaf file has no
// subtree to own, so the prefix arm is simply never satisfied for a leaf.
func underOrAt(rel, base string) bool {
	rel, base = normRel(rel), normRel(base)
	if rel == "" || base == "" || base == "." {
		return false
	}
	return rel == base || strings.HasPrefix(rel, base+string(filepath.Separator))
}

// normRel canonicalizes a home-relative path: no leading separator, no "./", no trailing
// separator. An absolute path normalizes to "" — it is not home-relative and must not
// silently match the home-relative walk.
func normRel(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return ""
	}
	return p
}

// HomeRel converts a declared path to the home-relative spelling the classifier uses,
// reporting whether it is home-relative at all.
//
// The "~/" form is what a config surface declares; the bare form is what a content
// destination declares. An ABSOLUTE path is not home-relative and is rejected rather than
// coerced: a surface at /etc/something names no base-home path, and treating it as one
// would silently protect an unrelated entry.
func HomeRel(p string) (string, bool) {
	p = strings.TrimSpace(p)
	switch {
	case p == "" || p == "~":
		return "", false
	case strings.HasPrefix(p, "~/"):
		p = p[2:]
	case strings.HasPrefix(p, "~"):
		// "~user/..." names another account's home.
		return "", false
	}
	rel := normRel(p)
	return rel, rel != ""
}
