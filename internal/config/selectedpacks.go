package config

// selectedpacks.go resolves the user's `packs` selection to loaded packs for the one kind of
// validation that has to know what the SELECTED packs declare: name reservation.
//
// The maintainer's OQ-BH14 ruling (docs/design/base-home-legacy-state.md#9-open-questions) is
// that a `writable_home_dirs` entry may not claim a directory a SELECTED pack declares, and an
// unselected pack is treated as if it does not exist: "packs could come from anywhere and be
// added, removed, whatever". Before it, the reservation was every pack yolo ships
// (packload.Embedded*), which refused `writable_home_dirs: [".codex"]` in a workspace that
// never selects codex and still missed every configured pack's directory.
//
// The launch has the selection already (stagePacks' loaded set, in internal/cli/run), and
// hands it to the deriver directly. Validation runs before staging, so it resolves the same
// selection here, the way
// UseProfileCLINames resolves its universe: an embedded entry by name, a configured one from
// the pack store with no network, then the `needs` closure over the embedded set.

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// resolveSelectedPacks returns the packs the user config selects, loaded, plus complete=false
// when some of the selection could not be read: a malformed user config, a configured pack
// the store cannot resolve without writing (never fetched, a ref not fetched yet, moved, or
// fetched but its tree not checked out), or a `needs` declaration the closure refuses. The
// packs that DID resolve are still returned. Validation NEVER FETCHES: fetching is the
// launch's pack refresh step, which runs before that launch validates and stages, so a pack
// unresolvable here is one the launch fetches first.
//
// PARTIAL IS THE RIGHT ANSWER FOR A RESERVATION CHECK, and it is deliberately not "refuse
// everything" or "reserve every shipped pack". A pack that cannot be resolved here either
// cannot be staged, and staging fails closed (stagePacks in internal/cli/run refuses the
// launch), or is a fetched pack whose tree staging checks out, and that launch reserves its
// dirs through the staged set. So no launch ever runs with that pack's directory
// unreserved; `yolo check` reports a broken pack itself, louder and first, in its Packs
// section. Refusing a config key over it would dress a broken install up as a bad
// `writable_home_dirs` entry — the same reason UseProfileCLINames steps aside.
//
// A configured pack with an `only`/`exclude` filter is loaded from a FILTERED copy, staged
// through packstage.Stage exactly as the launch stages it, because the filter can drop the
// manifest itself: that pack then declares nothing in the jail, and reserving its dirs would
// refuse the one writable_home_dirs entry that makes such a path writable. The copy is a temp
// dir removed before this returns, so a returned pack's Root names nothing; callers read
// only its declarations.
func resolveSelectedPacks() (packs []*packload.Pack, complete bool) {
	entries, err := LoadPacks(func(string) {})
	if err != nil {
		return nil, false
	}
	if len(entries) == 0 {
		return nil, true
	}
	byName := map[string]*packload.Pack{}
	for _, p := range packload.Embedded() {
		byName[p.Name] = p
	}
	complete = true
	// nil Getenv: the store falls back to the real environment, which is how a nested
	// launch's local packs resolve through the staged-tree fallback (YOLO_PACK_ROOT).
	store := &packsrc.Store{Dir: paths.PacksDir()}
	for _, entry := range entries {
		// The same split stagePacks makes: an embedded entry is the shipped pack of that
		// name; anything else, including a configured pack that took a shipped name, comes
		// from the store.
		if p, ok := byName[entry.Name]; ok && entry.Embedded() {
			packs = append(packs, p)
			continue
		}
		addr, err := packsrc.Parse(entry.Source)
		if err != nil {
			complete = false
			continue
		}
		// ResolveExisting, NOT Resolve: validation (and so `yolo check`) must write nothing
		// into the pack store, and Resolve is the launch's resolver, which checks a missing
		// tree out and RemoveAll's an incomplete one first. A fetched pack whose tree is not
		// checked out yet is therefore unresolvable HERE and reserves nothing, although the
		// launch's staging will check it out: that launch's own deriver
		// (WritableHomeDirs over the staged set) then drops the entry the pack's dir covers,
		// and the next validation, with the tree in place, refuses it.
		res, err := store.ResolveExisting(addr, entry.Slug())
		if err != nil {
			complete = false
			continue
		}
		root := res.Root
		if len(entry.Only) > 0 || len(entry.Exclude) > 0 {
			filtered, err := os.MkdirTemp("", "yolo-selected-pack-*")
			if err != nil {
				complete = false
				continue
			}
			defer os.RemoveAll(filtered)
			if _, err := packstage.Stage(packstage.Spec{
				Root: res.Root, Dest: filtered, Only: entry.Only, Exclude: entry.Exclude,
			}); err != nil {
				complete = false
				continue
			}
			root = filtered
		}
		p, problems := packload.LoadDir(root, entry.Name)
		if len(problems) > 0 || p == nil {
			complete = false
			continue
		}
		packs = append(packs, p)
	}
	// THE NEEDS CLOSURE: a pack a selected pack's live `needs` pulls in is selected too
	// (docs/reference/wire-bridge.md §3.1), exactly as stagePacks extends its loaded set.
	added, _, err := packload.ResolveNeeds(packs, func(name string) (*packload.Pack, bool) {
		p, ok := byName[name]
		return p, ok
	})
	if err != nil {
		return packs, false
	}
	return append(packs, added...), complete
}
