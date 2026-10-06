package packload

// registration.go turns a REGISTERING `files` slot (packdecl.FilesRegister) into the list entries
// it asks for: one per tree that lands in it (docs/design/pack-pi-resources.md §3.3).
//
// It reads the resolver delivery reads — borrowingSources, carriesFor and matchedDestinations, the
// rule borrowedDestinations synthesizes the landing from — so the entry names exactly the trees the
// jail mounts and `yolo host apply` writes, at the path SlotLanding gives both. The entries are
// placed by packoverlay.Collect, as config-list entries of the contributing pack, so the fold, the
// jail's per-entry capture, the host's inserted-entries record and the drop of a pack that left
// `packs` are config-list's own (PR-D2).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Registration is one landed tree a registering slot lists.
type Registration struct {
	// Pack is the CONTRIBUTING pack, whose tree landed and to which the entry is attributed.
	Pack string
	// Owner is the pack whose slot declares the registration.
	Owner string
	// Slot is the slot's `into`, and Landing the tree's home-relative path under it.
	Slot    string
	Landing string
	// Surface, Path and Entry are what to append where: the slot's `register` with the landing
	// substituted into its entry.
	Surface string
	Path    string
	Entry   string
}

// Registrations is every list entry the registering slots in `set` ask for, in the order of the
// contributing packs in `set`. Empty when no slot registers, or when no tree addresses one — every
// shipped pack set today, so a render with no addressed tree is byte-identical to one before this.
//
// `set` may be raw or resolved (ResolveDestinations): the addressed declarations are read through
// the pack's own declaration either way, and a resolved copy's synthesized landings carry no
// `agent`, so none can be mistaken for a slot.
func Registrations(set []*Pack) []Registration {
	var out []Registration
	forEachSlotTree(set, set, func(p *Pack, src packdecl.Contribution, m slotMatch) {
		r := m.dest.Register
		if r == nil {
			return
		}
		landing := SlotLanding(packdecl.KindFiles, m.dest.Into, p.Name)
		out = append(out, Registration{
			Pack: p.Name, Owner: m.owner.Name, Slot: m.dest.Into, Landing: landing,
			Surface: r.Surface, Path: r.Path, Entry: r.EntryFor(landing),
		})
	})
	return out
}

// ExpectsNote is one addressed tree that lands in a slot declaring `expects` and holds NONE of the
// expected names directly inside it — the early hint that what lands will not load
// (pack-pi-resources.md §3.4, PR-D4). Never a refusal: the tree still lands and is still
// registered, and whether it loads is the agent's to report.
type ExpectsNote struct {
	// Pack is the pack whose tree it is.
	Pack string
	// Msg says what is wrong, naming the tree, the expected names and the landing; Fix is the
	// next step.
	Msg string
	Fix string
}

// ExpectsNotes is an ExpectsNote for every addressed tree of the `of` packs that lands, among the
// slots `set` declares, in one whose `expects` it misses. `of` is usually `set` itself; a
// single-pack view (`yolo pack lint`) passes the pack, and a set holding it and the packs yolo
// ships, where the slots it addresses are declared.
//
// A tree whose source is missing, or is a single file, says nothing here: the missing one is
// reported by every renderer already (carriesFor's comment names them), and a file is not a tree.
func ExpectsNotes(of, set []*Pack) []ExpectsNote {
	var out []ExpectsNote
	seen := map[string]bool{}
	forEachSlotTree(of, set, func(p *Pack, src packdecl.Contribution, m slotMatch) {
		if len(m.dest.Expects) == 0 {
			return
		}
		entries, err := os.ReadDir(filepath.Join(p.Root, filepath.FromSlash(src.From)))
		if err != nil {
			return
		}
		want := map[string]bool{}
		for _, name := range m.dest.Expects {
			want[name] = true
		}
		for _, e := range entries {
			if want[e.Name()] {
				return
			}
		}
		n := ExpectsNote{
			Pack: p.Name,
			Msg: fmt.Sprintf("pack %s: its files tree %q for %s holds none of %s, the names the "+
				"%s pack's slot expects directly inside a tree, so what lands at ~/%s is unlikely "+
				"to load", p.Name, src.From, strings.Join(src.Agents, ", "),
				strings.Join(m.dest.Expects, ", "), m.owner.Name,
				SlotLanding(packdecl.KindFiles, m.dest.Into, p.Name)),
			Fix: fmt.Sprintf("Move the content of %q into those folders, such as %s/%s/, then "+
				"run `yolo pack lint` on the pack again.", src.From,
				strings.TrimSuffix(src.From, "/"), m.dest.Expects[0]),
		}
		if !seen[n.Msg] {
			seen[n.Msg] = true
			out = append(out, n)
		}
	})
	return out
}

// forEachSlotTree calls fn for every addressed `files` tree of the `of` packs and every slot in
// `set` it lands in, by the rule delivery uses: the pack's borrowing sources that carry content,
// matched to the destinations their audience names.
func forEachSlotTree(of, set []*Pack, fn func(p *Pack, src packdecl.Contribution, m slotMatch)) {
	for _, p := range of {
		if p == nil {
			continue
		}
		for _, src := range p.borrowingSources(packdecl.KindFiles) {
			if !p.carriesFor(src) {
				continue
			}
			for _, m := range matchedDestinations(src, set) {
				fn(p, src, m)
			}
		}
	}
}
