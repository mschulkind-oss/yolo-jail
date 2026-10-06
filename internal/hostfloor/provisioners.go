package hostfloor

import (
	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// provisioners.go is the floor's half of the user's provisioner order (`provisioners`,
// docs/design/provisioner-sets.md PS-D12). The floor installs a program from the pack's OWN
// recipe — its npm package on the floor's Node, or its vendor installer's capture — so it is the
// provisioner the order calls `pack`. Where the user's order ranks a package manager above it,
// and that manager is on the launch PATH with a recipe for the program, the manager supplies the
// program instead: the floor has no entry for it, `yolo host` runs the copy on the launch PATH,
// and the dependency report and the install offer name the manager's command. The question is
// depcheck.Winner's, the resolver the reports use, so the floor and the reports cannot disagree
// about who supplies a binary.

// floorRecipe stands in for the pack's own recipe in the requirement Outranker hands depcheck: a
// floor program always has one (the floor IS it), whatever command a report would spell for it.
const floorRecipe = "the floor's install"

// NixProvisioner is the provisioner name of the user's nix, the one manager that never takes an
// agent from the floor yet (Outranking says why).
const NixProvisioner = "nix"

// Outranking is who the user's order gives p to instead of the floor, and that provisioner's
// command for p (depcheck.Resolve's Remedy): ("", "") when the floor keeps p. orderFor returns a
// binary's provisioner order (config.ProvisionerOrder at the host), and look is the launch PATH's
// lookup the managers are found on. A FORK's program is never outranked — a manager ships
// upstream's build, never the fork's — and nor is a program whose order is empty, which costs no
// lookup. A host launch reads the command when the manager's copy is not installed yet, the state
// right after the user writes the order: the stop then names it rather than a PATH fix.
func Outranking(orderFor func(bin string) []string, look depcheck.Lookup) func(Program) (via, remedy string) {
	return func(p Program) (string, string) {
		if p.Install.Kind == packdecl.InstallKindSource {
			return "", ""
		}
		order := orderFor(p.Bin())
		if len(order) == 0 {
			return "", ""
		}
		res := depcheck.Resolve(depcheck.Requirement{Bin: p.Bin(), Hints: p.Install.InstallHints,
			SelfInstall: floorRecipe, Prefer: order}, look)
		// NIX KEEPS THE FLOOR'S ENTRY, for now. PS-D2 decided that an agent the user's order gives
		// to their nix is built, pinned by yolo's own flake.lock, INTO this floor entry, never put
		// in a `nix profile`; that build is not written yet, so the floor keeps the pack's recipe
		// rather than hand the agent to `nix profile install`. The config check says so
		// (config.provisionersNixWarning).
		if res.Via == "" || res.Via == depcheck.Pack || res.Via == NixProvisioner {
			return "", ""
		}
		return res.Via, res.Remedy
	}
}

// Outranker is a Floor.Outranked over the user's order (Outranking): the clause a report prints
// for a program the order gives to a manager, "" for one the floor keeps.
func Outranker(orderFor func(bin string) []string, look depcheck.Lookup) func(Program) string {
	outranking := Outranking(orderFor, look)
	return func(p Program) string {
		via, _ := outranking(p)
		if via == "" {
			return ""
		}
		return OutrankedReason(via, p.Bin())
	}
}

// OutrankedReason is the clause naming why the floor has no entry for bin: the user's order ranks
// via first for it.
func OutrankedReason(via, bin string) string {
	return "the user config's `provisioners` order ranks " + via + " first for " + bin +
		", so " + via + "'s copy is the one to run"
}
