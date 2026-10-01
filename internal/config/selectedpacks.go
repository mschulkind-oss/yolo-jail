package config

// selectedpacks.go resolves the user's `packs` selection to loaded packs for the two readers
// that run before staging and have to know what the SELECTED packs declare: validation's name
// reservation, and the capability census behind `required_capabilities` (capabilities.go).
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
// the pack store with no network, then the selection closure (`needs` and `via`) over the
// embedded set.

import (
	"errors"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// resolveSelectedPacks returns the packs the user config selects, loaded, plus complete=false
// when some of the selection could not be read: a malformed user config, a configured pack
// the store cannot resolve without writing (never fetched, a ref not fetched yet, moved, or
// fetched but its tree not checked out), or a `needs` or `via` the closure refuses. The
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
// Every entry resolves through ResolvePack's DECLARATION mode, so a filtered one is loaded from a
// FILTERED copy exactly as the launch stages it, because the filter can drop the manifest itself:
// that pack then declares nothing in the jail, and reserving its dirs would refuse the one
// writable_home_dirs entry that makes such a path writable. Callers read only declarations.
func resolveSelectedPacks() (packs []*packload.Pack, complete bool) {
	entries, err := LoadPacks(func(string) {})
	if err != nil {
		return nil, false
	}
	return SelectedPackDeclarations(entries, UserScopeSelection(), nil)
}

// UserScopeSelectedPacks is resolveSelectedPacks for a reader outside this package: the
// selection config validation reserves names for, which `yolo check` also hands the capability
// census (UnmetCapabilities), so the prediction counts the packs validation does.
func UserScopeSelectedPacks() (packs []*packload.Pack, complete bool) {
	return resolveSelectedPacks()
}

// SelectedPackDeclarations is resolveSelectedPacks over a caller's own `packs` entries and
// closure input: the same read-only resolution, for a caller that runs before staging and has
// to describe the launch's selection rather than the user scope's. The launch's capability gate
// is that caller: it hands its narrowed entries (a fork build carries fewer packs) and its own
// selection (`-p` can add a `via` pack), so it counts the packs that launch will stage. getenv
// is threaded to the pack store as ResolvePackSpec.Getenv; nil reads the real environment.
func SelectedPackDeclarations(entries []PackEntry, selection packload.Selection,
	getenv func(string) string) (packs []*packload.Pack, complete bool) {
	if len(entries) == 0 {
		return nil, true
	}
	// The one resolver (ResolvePack), in declaration mode, writing nothing into the store:
	// validation (and so `yolo check`) must write nothing there. A fetched pack whose tree is not
	// checked out yet is therefore unresolvable HERE and reserves nothing, although the launch's
	// staging will check it out: that launch's own deriver (WritableHomeDirs over the staged set)
	// then drops the entry the pack's dir covers, and the next validation, with the tree in place,
	// refuses it. A nil getenv (validation's) makes the store fall back to the real environment,
	// which is how a nested launch's local packs resolve through the staged-tree fallback. A local
	// pack's links are followed, as at every notch (OQ-NC9), so a dotfile-deployed pack reserves
	// its dirs here as it does in the launch.
	//
	// THE SELECTION CLOSURE through the one selection function (SelectPacks, notch-convergence
	// item 6): a pack a selected pack's live `needs` pulls in is selected too
	// (docs/reference/wire-bridge.md §3.1), and so is a service pack an active profile's `via`
	// names (docs/design/wire-bridge-gateway.md WG-I11), exactly as the launch stages them.
	sel, _ := SelectPacks(entries, PackSelectSpec{
		Resolve: func(entry PackEntry) (*packload.Pack, error) {
			res, err := ResolvePack(entry, ResolvePackSpec{ReadOnlyStore: true, Getenv: getenv})
			if err != nil {
				return nil, err
			}
			if res.Pack == nil || len(res.Problems) > 0 {
				return nil, errPackHasProblems
			}
			return res.Pack, nil
		},
		Selection: selection,
	})
	return sel.Packs(), sel.Complete()
}

// errPackHasProblems marks an entry resolveSelectedPacks cannot reserve for: its manifest has
// problems, which validation reports elsewhere, louder.
var errPackHasProblems = errors.New("pack manifest has problems")
