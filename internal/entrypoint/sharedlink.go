package entrypoint

// sharedlink.go holds the "the shared side always wins" rule, and the two PAYLOAD SHAPES it
// serves: a FILE (the shared_credentials hook) and a DIRECTORY (the shared_directory hook).
//
// It was claude.go's, where linkThroughShared sat alone next to a claude LSP table — a
// function whose own comment says it "was ensureCredentialsSymlink, with claude's three
// paths baked in as constants", and which a second pack now reaches for a directory. Left
// there it would read as claude's, which is the shape packhooks.go's header rules against.
//
// ⚠ WHY ONE FUNCTION AND NOT TWO. The ORDER of operations is the whole content of the rule,
// and it is the thing that destroyed credentials when it was got wrong (read the warning
// inside linkThroughShared before editing anything here). A directory tree makes that order
// HARDER to hold, not easier: a tree copy fails part-way, where a file write does not. So a
// sibling helper — a near-copy of the one carrying that scar, with a new copy step spliced
// in — was the option rejected. linkThroughShared owns the order for both payloads, and
// sharedNode carries ONLY what genuinely differs between them: what "empty" means on the
// shared side, how a copy is made, and how the local node is removed once the copy has
// reported success.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// sharedNode is one payload shape: everything linkThroughShared does that depends on the
// payload being a file rather than a directory, and nothing else.
//
// Every field is deliberately small, because the failure this file guards is a SECOND
// implementation of the ordering rule. A field that needed to know about the order would be
// a sign the split is wrong.
type sharedNode struct {
	// noun names the payload in a decision line ("copied local credential into shared").
	// The credential spelling is load-bearing: two tests assert those sentences, and the
	// shared-creds log is the only record of a login that changed tiers.
	noun string
	// discardNote is the clause appended when a POPULATED shared side wins over a
	// populated local one — the priced loss, spelled for the payload that is lost.
	discardNote string
	// sharedPath resolves the shared side from the pack's declared machine-scope dir and
	// the home-relative path the hook acts on. A file lands INSIDE that dir; a directory
	// IS it (see sharedTreeNode).
	sharedPath func(sharedDir, from string) string
	// sharedEmpty reports whether the shared side holds nothing worth keeping — which is
	// what decides between copying the local payload out and discarding it. An ABSENT or
	// unreadable shared side counts as empty for both payloads.
	sharedEmpty func(shared string) bool
	// copyIn copies the local payload into shared. It must return nil ONLY once every byte
	// is on disk: the caller deletes the local payload on nil, so an optimistic return here
	// is the data-loss bug. Its error is embedded verbatim in a decision line, so it reads
	// as a clause ("could not write it into the shared file: …").
	copyIn func(local, shared string) error
	// removeLocal removes the local payload, once copyIn has reported success or a
	// populated shared side has won.
	removeLocal func(local string) error
}

// sharedFileNode is the FILE payload: one credentials file, linked into the pack's
// machine-scope dir. This is the original behavior of linkThroughShared, unchanged.
var sharedFileNode = sharedNode{
	noun:        "credential",
	discardNote: " (a fresh login here is lost)",
	sharedPath: func(sharedDir, from string) string {
		return filepath.Join(sharedDir, filepath.Base(from))
	},
	// ABSENT and ZERO-BYTE are both empty, and they reach this by different tests inside
	// one condition: absent is a machine that has never run a jail, and zero-byte is the
	// placeholder EnsureGlobalStorage touches so the bind mount has something to bind —
	// the far more common one, and the one a Size()-blind rewrite would miss.
	sharedEmpty: func(shared string) bool {
		fi, err := os.Stat(shared)
		return err != nil || fi.Size() == 0
	},
	copyIn: func(local, shared string) error {
		data, err := os.ReadFile(local)
		if err != nil {
			// Nothing to copy, and nothing gained by falling through to the removal:
			// that would discard a credential we could not even look at.
			return fmt.Errorf("the local file is unreadable: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
			return fmt.Errorf("could not create the shared dir: %w", err)
		}
		if err := os.WriteFile(shared, data, 0o600); err != nil {
			return fmt.Errorf("could not write it into the shared file: %w", err)
		}
		return nil
	},
	removeLocal: os.Remove,
}

// sharedTreeNode is the DIRECTORY payload: a whole home subdirectory replaced by a symlink
// to the pack's machine-scope dir, so every workspace on the machine reads and writes ONE
// store instead of N drifting copies (docs/design/pi-extension-lifecycle.md §3.1, OQ-1 —
// where the load-bearing reason is cross-jail version drift rather than disk).
//
// THE SHARED SIDE IS THE DECLARED DIRECTORY ITSELF, not a child of it. A file payload joins
// filepath.Base(from) because many files share one credential dir; a directory payload must
// not, or `at: ".pi-shared-npm"` + `from: ".pi/agent/npm"` would land the store at
// `~/.pi-shared-npm/npm` — one level below the directory the manifest declared and the
// backends mount, leaving the mount root an empty parent. A pack wanting two shared trees
// declares two machine-scope dirs.
//
// THE DISCARD IS PRICED DIFFERENTLY FROM THE CREDENTIAL'S, and the difference is why the
// note is not copied: a discarded credential costs a login that cannot be recovered without
// the user, while a discarded package store costs a re-install of bytes the shared store
// already has. That is the direction that makes "shared always wins" cheaper here than it
// is for a credential, not more expensive — so the rule carries over unchanged, with the
// price stated for what is actually lost.
//
// ONE ASYMMETRY THE FILE PAYLOAD DOES NOT HAVE, stated so it is not read as a bug later: a
// failed os.Remove leaves a file untouched, while a failed os.RemoveAll can leave a PARTIAL
// tree behind, after which the decision line's "left in place" is generous. It is not a data
// loss — the removal only runs once the store holds the bytes (copied, or already populated),
// which is the whole ordering rule — and the tool repopulates its own directory on the next
// run. Reporting it more precisely would mean the removal deciding what the log says, which
// is the coupling this split exists to avoid.
var sharedTreeNode = sharedNode{
	noun:        "directory",
	discardNote: " (the shared store is what every workspace now reads)",
	sharedPath:  func(sharedDir, _ string) string { return sharedDir },
	sharedEmpty: sharedTreeIsEmpty,
	copyIn:      copyTreeIntoShared,
	removeLocal: os.RemoveAll,
}

// sharedCopyIncomplete marks a shared DIRECTORY whose copy has been started and not yet
// reported success. While it exists the store reads as EMPTY, so the next boot copies again
// instead of treating a part-copied tree as the populated side that wins.
//
// It exists because the file rule's guarantee does not survive the change of payload on its
// own. A file write either happened or did not, so "the copy failed" and "the shared side is
// still empty" are the same observation. A TREE copy can fail half-way: the local payload is
// left in place (which is the rule), but the shared side is now non-empty, and the NEXT boot
// would read that partial tree as populated, discard the local one and report the discard as
// normal — a silent failure reported as a success, one boot later. The marker is what keeps
// the file rule's guarantee true for a tree.
const sharedCopyIncomplete = ".yolo-copy-incomplete"

// sharedCopyNoteName is the human-readable note inside the marker DIRECTORY.
const sharedCopyNoteName = "README"

// sharedCopyIncompleteNote is written into the marker, because the person most likely to
// find it is a human staring at a store that will not populate.
const sharedCopyIncompleteNote = `This file means a yolo pack hook was copying a workspace's
directory into this shared store and has not reported success. While it exists the store is
treated as EMPTY and the copy is retried at the next launch, so a partly-copied tree is
never mistaken for the populated side. Delete it only if you are sure the contents are
complete.
`

// sharedTreeIsEmpty reports whether a shared DIRECTORY holds nothing worth keeping.
//
// ⚠ THE EMPTINESS TEST INVERTS between the payloads, and reusing the file's would be a
// destructive bug rather than a wrong answer: a freshly MkdirAll'd directory Stats with a
// non-zero Size() on ext4, so the file test answers "populated" for an EMPTY store — and the
// rule would then discard the workspace's real tree in favour of nothing.
//
// yolo's OWN entries (packdecl.StoreBookkeepingPrefix) are not content either. A pack's
// pre-launch refresh takes its lock inside this store (prelaunchrefresh.go), so a store no
// package was ever installed into holds only that lock while the first refresh runs, and
// after an interrupted one. Counted, it read as populated and the workspace's tree was
// discarded for a store holding nothing.
func sharedTreeIsEmpty(shared string) bool {
	ents, err := os.ReadDir(shared)
	if err != nil {
		// Absent (first launch on this machine) or unreadable, and both answer "nothing
		// worth keeping" — the same collapse the file test makes of os.Stat's error. An
		// unreadable store then fails the copy, which leaves the local tree in place.
		return true
	}
	content := false
	for _, ent := range ents {
		switch {
		case ent.Name() == sharedCopyIncomplete:
			return true
		case !strings.HasPrefix(ent.Name(), packdecl.StoreBookkeepingPrefix):
			content = true
		}
	}
	return !content
}

// copyTreeIntoShared copies the local tree into the shared store, and returns nil only once
// the whole tree is on disk and the incomplete-copy marker has been cleared.
//
// A cleared marker is part of the success condition on purpose. If the copy lands and the
// marker cannot be removed, this reports failure: the local tree is kept, nothing is
// symlinked, and the next launch retries — because a marker that cannot be cleared is
// indistinguishable, from the outside, from a copy that never finished.
func copyTreeIntoShared(local, shared string) error {
	if err := os.MkdirAll(shared, 0o755); err != nil {
		return fmt.Errorf("could not create the shared dir: %w", err)
	}
	// THE MARKER IS A DIRECTORY, AND THAT IS THE LOCK. os.Mkdir is atomic and fails
	// EEXIST, so exactly one launch can ever be inside the copy below.
	//
	// ⚠ IT HAD TO BECOME ONE. As a plain file the marker made concurrency WORSE than no
	// marker at all: sharedTreeIsEmpty reports a marked store as EMPTY (deliberately — a
	// half-copied store is not trustworthy), so a second launch arriving mid-copy read
	// "empty", copied its OWN tree in on top, and then removed its local copy. Two
	// interleaved trees in the machine-wide store, both local copies gone, and both
	// launches reporting "copied local directory into shared". That is the same
	// silent-failure-reported-as-success this family already cost once, arrived at from a
	// different direction.
	//
	// A loser returns an error, which is all it has to do: the caller keeps the local
	// payload and does not symlink, so the next launch sees a populated store and takes
	// the ordinary discard path.
	marker := filepath.Join(shared, sharedCopyIncomplete)
	if err := acquireSharedCopy(marker); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(marker, sharedCopyNoteName),
		[]byte(sharedCopyIncompleteNote), 0o644); err != nil {
		// Non-fatal: the note is for a human reading the store, not a control signal.
		_ = err
	}
	if err := copyTreeStrict(local, shared); err != nil {
		// ROLL BACK WHAT THIS COPY WROTE, and the distinction from a crash is the whole
		// reason it is safe to. We hold the lock and we only got here because the store
		// read EMPTY, so every byte under it is ours to remove — and removing it is what
		// lets the next launch retry immediately instead of waiting out the stale
		// interval.
		//
		// ⚠ THE MARKER MUST OUTLIVE A CRASH AND NOT A RETURNED ERROR. Left behind after an
		// ordinary failure it blocks the retry for sharedCopyStaleAfter; removed after a
		// CRASH it could not be, which is exactly why the stale rule exists. So: roll the
		// tree back, then release. A crash skips both and the age rule collects it.
		//
		// Rollback failures are deliberately not returned over the copy's own error: the
		// copy error is what the user needs, and a store left dirty is still refused by
		// sharedTreeIsEmpty as long as the marker is there — which, on this path, it is
		// until the line below.
		for _, ent := range readDirNames(shared) {
			if ent != sharedCopyIncomplete {
				_ = os.RemoveAll(filepath.Join(shared, ent))
			}
		}
		_ = os.RemoveAll(marker)
		return fmt.Errorf("could not copy it into the shared dir: %w", err)
	}
	if err := os.RemoveAll(marker); err != nil {
		return fmt.Errorf("the copy landed but its in-progress mark could not be cleared, "+
			"so the shared dir cannot be trusted yet: %w", err)
	}
	return nil
}

// sharedCopyStaleAfter is how long a marker may exist before it is read as a CRASHED copy
// rather than a live one. It is the stale-lock interval the design already specified for
// this directory (pi-extension-lifecycle.md §3.3, STALE_LOCK = 600s).
//
// Two situations produce a marker and only age separates them: another launch copying right
// now, and a launch that died mid-copy leaving a partial tree. Treating the first as the
// second corrupts a store; treating the second as the first strands one forever.
const sharedCopyStaleAfter = 10 * time.Minute

// acquireSharedCopy takes the copy lock, or explains why it could not.
//
// The returned error is not a failure of the migration — it is the migration correctly
// declining to run this launch. The caller keeps the local payload either way.
func acquireSharedCopy(marker string) error {
	if err := os.Mkdir(marker, 0o755); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("could not mark the shared dir as a copy in progress: %w", err)
	}
	fi, err := os.Stat(marker)
	if err != nil {
		// It exists and cannot be stat'd, so its age is unknowable. Decline: the cost is a
		// deferred migration, where the other reading's cost is a corrupted store.
		return fmt.Errorf("another launch appears to be migrating this shared dir")
	}
	if age := time.Since(fi.ModTime()); age < sharedCopyStaleAfter {
		return fmt.Errorf("another launch is migrating this shared dir (started %s ago)",
			age.Round(time.Second))
	}
	// Stale: a previous copy died. Take it over — the partial tree it left is exactly what
	// sharedTreeIsEmpty already refuses to trust, so re-copying over it is the repair.
	return nil
}

// readDirNames is the rollback's lister: names only, and an unreadable dir yields none so
// the caller degrades to leaving the store marked rather than panicking on a cleanup path.
func readDirNames(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// copyTreeStrict copies src INTO dst recursively and returns the FIRST error it meets.
//
// ⚠ NOT copyTree (boot.go), and the difference is the point. That one DROPS every per-entry
// error by design — it merges a host nvim config into a home the user also edits, where one
// unwritable file may not cost the other ninety — so a caller cannot tell a complete copy
// from a partial one. Building a migration on it would report a partial tree as a complete
// one, which is exactly the shape linkThroughShared's warning is about.
//
// SYMLINKS ARE RECREATED, NOT FOLLOWED — the other divergence from copyTree, which Stats
// through them. A node_modules tree's .bin entries are relative links into sibling packages,
// and dereferencing them would turn one store into a fan-out of copies (and silently drop a
// dangling one, which npm does produce).
func copyTreeStrict(src, dst string) error {
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		from := filepath.Join(src, ent.Name())
		to := filepath.Join(dst, ent.Name())
		fi, err := os.Lstat(from)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, rerr := os.Readlink(from)
			if rerr != nil {
				return rerr
			}
			// RemoveAll first so a RETRY over a partial copy replaces whatever it left,
			// including an entry whose type changed. Absent is not an error.
			if err := os.RemoveAll(to); err != nil {
				return err
			}
			if err := os.Symlink(target, to); err != nil {
				return err
			}
		case fi.IsDir():
			if err := os.MkdirAll(to, fi.Mode().Perm()); err != nil {
				return err
			}
			if err := copyTreeStrict(from, to); err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if err := copyFileStrict(from, to, fi.Mode().Perm()); err != nil {
				return err
			}
		default:
			// A socket, fifo or device node. Skipping it silently is the one thing this
			// function may not do — that is a partial copy reported as a whole one — and
			// the failure degrades safely: the local tree stays where it is and the
			// decision line names the path.
			return fmt.Errorf("%s is a %s, which this copy cannot reproduce", from,
				fi.Mode().Type())
		}
	}
	return nil
}

// copyFileStrict copies one regular file, reporting every error including the CLOSE — which
// on a buffered filesystem is where a write failure surfaces, and dropping it would be the
// optimistic return this file exists to prevent.
func copyFileStrict(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	// Dropped, and only this one is: a failed close on a READ handle loses nothing, while
	// the write handle's close below is returned because that is where a buffered write
	// failure surfaces.
	defer func() { _ = in.Close() }()
	// O_TRUNC so a retry over a partial copy rewrites rather than appends.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// linkThroughShared replaces link with a symlink to target, moving an existing REAL payload
// at link into shared first IF AND ONLY IF shared is empty.
//
// THE RULE IS "THE SHARED SIDE ALWAYS WINS", and it is deliberately schema-blind:
//
//	already the right symlink        -> done
//	real payload + EMPTY shared      -> copy local into shared, then symlink
//	real payload + POPULATED shared  -> discard local, then symlink
//	real payload + the copy FAILED   -> local left in place, NOT symlinked
//	anything else                    -> symlink
//
// The fourth row is not a fifth rule, it is the first three refusing to destroy the
// only copy: the discard in row three is priced (the shared side holds something valid), and
// a discard after a failed write is not (nothing does).
//
// There is no merge and no freshness comparison in any credential schema. This used to hold a
// claude-specific harvest — a newest-`expiresAt`-wins merge of the local file's `claudeAiOauth`
// dict into the shared one — which made the generically-named `shared_credentials` hook work
// properly for exactly one tool: agy's differently-shaped token fell straight through it, so the
// case the merge existed to save was already unsaved for the second consumer. The ruling
// (docs/reference/pack-system.md §5, OQ-3) was to delete the merge rather than generalize
// it, because losing one valid login in a migration is an acceptable cost and any generic
// stand-in (mtime-newest-wins) can pick the wrong credential anyway: the broker rewrites the
// shared file on every background refresh, so its mtime is fresh even when its ACCOUNT is stale.
//
// The copy-if-empty branch is NOT a weakened freshness rule — it is what lets a first login in a
// fresh install survive, which is the only case where the local file is the only copy there is.
//
// THE ACCEPTED FAILURE MODE, so the next reader sees it as designed rather than broken: if the
// shared file holds a REVOKED or EXPIRED credential and a jail logs in again, that fresh login
// is written to the local file, and at the next boot this function discards it and relinks to
// the dead shared credential. Re-logging-in fixes it until the next boot. The exit is
// `rm` the shared file (the path this hook logs, under the pack's sharedDir) and log in once
// more — NOT a code change. That is the price of having no freshness rule, and it was priced
// knowingly.
//
// A failure to remove link RETURNS NIL deliberately: the tool then keeps using its own local
// payload, which works. Failing the boot because a credential could not be moved to the shared
// tier would trade a working jail for a tidier layout.
//
// Reached by the shared_credentials and shared_directory HOOKS (packhooks.go), which is how a
// pack asks for this without core switching on a tool name. It was ensureCredentialsSymlink,
// with claude's three paths baked in as constants.
//
// The returned string is a human-readable account of what happened (already-linked,
// copied-into-empty, discarded-local, or no-local-payload), which the caller logs so a
// cross-workspace login problem is diagnosable after the fact.
func (e *Env) linkThroughShared(link, shared, target string, n sharedNode) (string, error) {
	if cur, err := os.Readlink(link); err == nil {
		// It's a symlink.
		if cur == target {
			return "already symlinked to shared", nil
		}
		// Dropped because the os.Symlink at the bottom of this function is the better
		// report: a remove that mattered leaves the old link in place, so the symlink
		// fails EEXIST and that error is RETURNED — through the hook, into genStep, and
		// so into the boot's collected failures. There is nothing this line could say
		// that the next one does not say with a consequence attached.
		_ = os.Remove(link)
	} else if pathExists(link) {
		// A real payload: copy it into an EMPTY shared side, then re-link.
		//
		// ⚠ THE COPY'S ERRORS ARE LOAD-BEARING, and dropping them destroyed
		// credentials. This block used to set copied = true before knowing whether the
		// write had happened, and the removal below then deleted the local file and
		// symlinked it to the (still empty) shared path — so a failed write DELETED the
		// only copy of a fresh login and the log said "shared empty; copied local
		// credential into shared". That is the worst shape in this class: a silent
		// failure reported as the success it prevented.
		//
		// So: the copy is only "copied" once n.copyIn says every byte is on disk, and a
		// copy that was ATTEMPTED AND FAILED leaves the local payload exactly where it
		// is. Losing a login to a populated shared file is the documented, priced cost
		// above; losing one to an unwritable disk is a bug.
		copied := false
		if n.sharedEmpty(shared) {
			if err := n.copyIn(link, shared); err != nil {
				return "local " + n.noun + " left in place; " + err.Error(), nil
			}
			copied = true
		}
		if err := n.removeLocal(link); err != nil {
			return "local " + n.noun + " left in place (could not remove)", nil
		}
		decision := "shared already populated; discarded local " + n.noun + n.discardNote
		if copied {
			decision = "shared empty; copied local " + n.noun + " into shared"
		}
		return decision, os.Symlink(target, link)
	}
	return "no local " + n.noun + "; symlinked to shared", os.Symlink(target, link)
}
