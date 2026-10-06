package packload

// registration.go turns a REGISTERING `files` slot (packdecl.FilesRegister) into the list entries
// it asks for: one per tree that lands in it (docs/design/pack-pi-resources.md §3.3).
//
// It reads the resolver delivery reads — borrowingSources, carriesFor and matchedDestinations, the
// rule borrowedDestinations synthesizes the landing from — so the entry names exactly the trees the
// jail mounts and `yolo host apply` writes, at the path SlotLanding gives both, spelled with the
// name the launch mounted the tree under (Pack.landingName). A tree whose source is not in its
// pack's tree is delivered by neither, so it is listed by neither (filesSourceDelivered). The entries are
// placed by packoverlay.Collect, as config-list entries of the contributing pack, so the fold, the
// jail's per-entry capture, the host's inserted-entries record and the drop of a pack that left
// `packs` are config-list's own (PR-D2).

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
// A tree is listed only when it is DELIVERED, which is when its source is in its pack's tree
// (filesSourceDelivered): a `from` naming nothing, or an only/exclude filter in `packs` that dropped
// the folder, leaves the jail skipping the mount with a warning and `yolo host apply` refusing the
// path, and an entry for it would list a path where nothing is (pack-pi-resources.md PR-D6).
//
// `set` may be raw or resolved (ResolveDestinations): the addressed declarations are read through
// the pack's own declaration either way, and a resolved copy's synthesized landings carry no
// `agent`, so none can be mistaken for a slot.
func Registrations(set []*Pack) []Registration {
	var out []Registration
	forEachSlotTree(set, set, func(p *Pack, src packdecl.Contribution, m slotMatch) {
		r := m.dest.Register
		if r == nil || !p.filesSourceDelivered(src) {
			return
		}
		// The LAUNCH's name for the pack, which is what the tree is mounted under; the entry is
		// still attributed to Pack.Name, the name every other entry of the pack carries here.
		landing := SlotLanding(packdecl.KindFiles, m.dest.Into, p.landingName())
		out = append(out, Registration{
			Pack: p.Name, Owner: m.owner.Name, Slot: m.dest.Into, Landing: landing,
			Surface: r.Surface, Path: r.Path, Entry: r.EntryFor(landing),
		})
	})
	return out
}

// TreeNote is an authoring note about one addressed tree that lands in a slot: ExpectsNotes' and
// SkillsInTreeNotes'. Never a refusal: the tree still lands and is still registered, and whether
// what is in it loads is the agent's to report.
type TreeNote struct {
	// Pack is the pack whose tree it is.
	Pack string
	// Msg says what is wrong, naming the tree, the expected names and the landing; Fix is the
	// next step.
	Msg string
	Fix string
}

// ExpectsNotes is a TreeNote for every addressed tree of the `of` packs that lands, among the
// slots `set` declares, in one declaring `expects` and holds NONE of the expected names directly
// inside it — the early hint that what lands will not load (pack-pi-resources.md §3.4, PR-D4).
// `of` is usually `set` itself; a single-pack view (`yolo pack lint`) passes the pack, and a set
// holding it and the packs yolo ships, where the slots it addresses are declared.
//
// A tree whose source is missing, or is a single file, says nothing here: the missing one is
// reported by every renderer already (carriesFor's comment names them), and a file is not a tree.
func ExpectsNotes(of, set []*Pack) []TreeNote {
	var out []TreeNote
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
		n := TreeNote{
			Pack: p.Name,
			Msg: fmt.Sprintf("pack %s: its files tree %q for %s holds none of %s, the names the "+
				"%s pack's slot expects directly inside a tree, so what lands at ~/%s is unlikely "+
				"to load", p.Name, src.From, strings.Join(src.Agents, ", "),
				strings.Join(m.dest.Expects, ", "), m.owner.Name,
				SlotLanding(packdecl.KindFiles, m.dest.Into, p.landingName())),
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

// SkillsInTreeNotes is a TreeNote for every addressed tree of the `of` packs that lands, among the
// slots `set` declares, in one whose `expects` names a skills folder (packdecl.DefaultSkillsDir),
// and holds one directly inside it (pack-pi-resources.md §3.4, the row for a tree that ships
// skills/). The slot's agent loads those skills from its own tree, so they reach that agent
// alone, while the `skills` kind gives a pack's skills to every agent, which is the recommended
// route. A note for `yolo pack lint`, never a warning: shipping skills to one agent is a choice.
// `of` and `set` read as ExpectsNotes' do.
//
// Gated on the slot's own `expects`, so core learns nothing about an agent: the slot is what says
// its agent reads a folder of that name in a tree, and the folder's name is the kind's own.
func SkillsInTreeNotes(of, set []*Pack) []TreeNote {
	var out []TreeNote
	seen := map[string]bool{}
	forEachSlotTree(of, set, func(p *Pack, src packdecl.Contribution, m slotMatch) {
		if !slices.Contains(m.dest.Expects, packdecl.DefaultSkillsDir) {
			return
		}
		tree := strings.TrimSuffix(src.From, "/")
		fi, err := os.Stat(filepath.Join(p.Root, filepath.FromSlash(tree), packdecl.DefaultSkillsDir))
		if err != nil || !fi.IsDir() {
			return
		}
		n := TreeNote{
			Pack: p.Name,
			Msg: fmt.Sprintf("pack %s: its files tree %q for %s ships %s/%s/, whose skills reach "+
				"%s alone; the `skills` kind gives a pack's skills to every agent", p.Name,
				src.From, strings.Join(src.Agents, ", "), tree, packdecl.DefaultSkillsDir,
				strings.Join(src.Agents, ", ")),
			Fix: fmt.Sprintf("To give them to every agent, move %s/%s/ to %s/ at the pack's root; "+
				"to keep them for %s only, leave them where they are.", tree,
				packdecl.DefaultSkillsDir, packdecl.DefaultSkillsDir, strings.Join(src.Agents, ", ")),
		}
		if !seen[n.Msg] {
			seen[n.Msg] = true
			out = append(out, n)
		}
	})
	return out
}

// filesSourceDelivered reports whether an addressed `files` tree's source is in its pack's tree as
// a directory or a regular file — what the jail's mount emitter binds (run.packFilesMountArgs,
// which skips anything else with a warning), and what `yolo host apply` writes rather than refusing
// as missing (entrypoint.RenderHostFiles). carriesFor routes an absent source ON PURPOSE, so that
// those renderers report it; a registration reports nothing, so it asks this instead.
func (p *Pack) filesSourceDelivered(src packdecl.Contribution) bool {
	if src.From == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(p.Root, filepath.FromSlash(src.From)))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
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
