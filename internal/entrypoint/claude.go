package entrypoint

import (
	"os"
	"path/filepath"
)

// claudeLSPPluginOrder pins the iteration order used when enabling LSP plugins;
// the effect on enabledPlugins is order-independent for distinct keys, but the
// order is fixed for deterministic output.
var claudeLSPPluginOrder = []struct{ lsp, plugin string }{
	{"python", "pyright-lsp@claude-plugins-official"},
	{"typescript", "typescript-lsp@claude-plugins-official"},
	{"go", "gopls-lsp@claude-plugins-official"},
}

// linkThroughShared replaces link with a symlink to target, moving an existing REAL file at
// link into shared first IF AND ONLY IF shared is empty.
//
// THE RULE IS "THE SHARED FILE ALWAYS WINS", and it is deliberately schema-blind:
//
//	already the right symlink        -> done
//	real file + EMPTY shared         -> copy local into shared, then symlink
//	real file + POPULATED shared     -> discard local, then symlink
//	real file + the copy FAILED      -> local left in place, NOT symlinked
//	anything else                    -> symlink
//
// The fourth row is not a fifth rule, it is the first three refusing to destroy the
// only copy: the discard in row three is priced (the shared file holds a valid
// credential), and a discard after a failed write is not (nothing holds one).
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
// file, which works. Failing the boot because a credential could not be moved to the shared tier
// would trade a working jail for a tidier layout.
//
// Reached by the shared_credentials HOOK (packhooks.go), which is how a pack asks for this
// without core switching on a tool name. It was ensureCredentialsSymlink, with claude's three
// paths baked in as constants.
//
// The returned string is a human-readable account of what happened (already-linked,
// copied-into-empty, discarded-local, or no-local-file), which the caller logs so a
// cross-workspace login problem is diagnosable after the fact.
func (e *Env) linkThroughShared(link, shared, target string) (string, error) {
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
		// A regular file: copy it into an EMPTY shared file, then re-link.
		//
		// ⚠ THE COPY'S ERRORS ARE LOAD-BEARING, and dropping them destroyed
		// credentials. This block used to set copied = true before knowing whether the
		// write had happened, and the caller then removed the local file and symlinked
		// to the (still empty) shared path — so a failed write DELETED the only copy of
		// a fresh login and the log said "shared empty; copied local credential into
		// shared". That is the worst shape in this class: a silent failure reported as
		// the success it prevented.
		//
		// So: the copy is only "copied" once the bytes are on disk, and a copy that was
		// ATTEMPTED AND FAILED leaves the local file exactly where it is. Losing a login
		// to a populated shared file is the documented, priced cost above; losing one to
		// an unwritable disk is a bug.
		copied := false
		if fi, err := os.Stat(shared); err != nil || fi.Size() == 0 {
			data, rerr := os.ReadFile(link)
			if rerr != nil {
				// Unreadable local file: nothing to copy, and nothing gained by falling
				// through to the removal below, which would discard a credential we could
				// not even look at.
				return "local credential file unreadable, left in place: " + rerr.Error(), nil
			}
			if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
				return "local credential left in place; could not create the shared dir: " +
					err.Error(), nil
			}
			if err := os.WriteFile(shared, data, 0o600); err != nil {
				return "local credential left in place; could not write it into the shared " +
					"file: " + err.Error(), nil
			}
			copied = true
		}
		if err := os.Remove(link); err != nil {
			return "local file left in place (could not remove)", nil
		}
		decision := "shared already populated; discarded local credential (a fresh login here is lost)"
		if copied {
			decision = "shared empty; copied local credential into shared"
		}
		return decision, os.Symlink(target, link)
	}
	return "no local credential file; symlinked to shared", os.Symlink(target, link)
}
