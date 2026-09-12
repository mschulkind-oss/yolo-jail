package darwinpkg

// floor.go is the Go half of the NON-CONTAINER FLOOR — the set of packages a
// notch with no baked image has before any config asks for anything
// (docs/design/macos-user-provisioning.md; "floor" is that document's word, and
// it is not a yolo config key).
//
// WHY THE LIST EXISTS TWICE. flake.nix owns the floor for real: it resolves the
// names against nixpkgs, and it is where the FATAL lives. Nothing in Go can
// resolve a nixpkgs attr. What Go can do is assert things ABOUT the list that
// nix cannot — and there is exactly one such thing, which is also the reason
// this file is not redundant:
//
//	OQ-P1's fatal covers NECESSITY and cannot cover POLICY. A package that is
//	unbuildable on darwin fails the eval, naming itself. A GNU-userland package
//	left off the exclusion list BUILDS FINE AND SHIPS SILENTLY, and the agent
//	gets GNU `sed` on a Mac — the exact surprise OQ-P2 ruled out. nix has no
//	predicate for "this is GNU userland"; a test does (floor_policy_test.go).
//
// The copy is kept honest by a DRIFT GATE (floor_drift_test.go), which parses
// flake.nix's own three lists and fails when they disagree with the constants
// here. So a package added to the image core cannot slip past the policy
// assertion by being invisible to Go.

// ImageCoreNames is `coreFloorNames` in flake.nix: every nixpkgs attr the
// container image bakes into BOTH image variants, in flake order.
//
// It is the FLOOR'S INPUT, per OQ-P1 — ruled against its own leaning, which was
// a minimum of mise + nodejs + git. The maximum won: *"I'd rather pain than
// something silently skipped."*
//
// Kept in flake order rather than sorted so a diff against flake.nix reads as a
// diff, and pinned entry-for-entry by the drift gate.
var ImageCoreNames = []string{
	"bashInteractive",
	"coreutils-full",
	"git",
	"ripgrep",
	"fd",
	"curl",
	"cacert",
	"mise",
	"findutils",
	"which",
	"nodejs_24",
	"python3",
	"go",
	"neovim",
	"gh",
	"gnused",
	"gnugrep",
	"gawk",
	"gnupatch",
	"diffutils",
	"gzip",
	"bzip2",
	"xz",
	"gnutar",
	"unzip",
	"zip",
	"zlib",
	"procps",
	"overmind",
	"jq",
	"uv",
	"iptables",
	"socat",
	"sox",
	"openssl",
	"tzdata",
}

// FloorExcludedUnbuildable is `noncontainerFloorUnbuildable` in flake.nix: the
// NECESSITY half of the exclusion list.
//
// DERIVED, NOT GUESSED. `nix eval` is cross-platform, so the set was enumerated
// from a Linux jail on 2026-09-12 by reading meta.platforms / meta.available for
// all 36 names against both darwin systems this flake locks. Exactly one name
// came back unavailable, on both.
//
// ⚠ `procps` is NOT here, though the design doc and the roadmap both said it
// was Linux-only: on darwin nixpkgs maps that attr to `unixtools.procps` (name
// `procps-1003.1-2008`), a wrapper around the Mac's own BSD ps/pgrep/pkill —
// which is also exactly what OQ-P2 wants. Assuming it would have cost a tool.
//
// Forgetting an entry here is SAFE in the sense that matters: the nix eval dies
// naming the package. This list is therefore a record, not a guard.
var FloorExcludedUnbuildable = []string{
	"iptables",
}

// FloorExcludedPolicy is `noncontainerFloorPolicy` in flake.nix: the POLICY half
// of the exclusion list, per OQ-P2 (**no GNU userland**).
//
// Every entry BUILDS PERFECTLY WELL for darwin. That is the whole problem, and
// the reason this list needs an assertion rather than a maintainer: forgetting
// one produces a working jail in which `sed -i`, `ls --color`, `find -printf`
// and `tar --wildcards` all behave unlike the Mac the human is using.
//
// It is NOT the guard. floor_policy_test.go is — it asserts over the DERIVED
// floor that nothing on it is GNU userland, which catches a name added to
// ImageCoreNames tomorrow that nobody thought to put here.
var FloorExcludedPolicy = []string{
	"coreutils-full",
	"findutils",
	"gnused",
	"gnugrep",
	"gawk",
	"gnupatch",
	"diffutils",
	"gnutar",
}

// FloorNames returns the floor: the image core minus both kinds of exclusion, in
// flake order.
//
// It does NOT subtract the user's own `packages:`, which the flake does: that
// subtraction depends on a config this package never sees, and it exists to stop
// a buildEnv collision rather than to define the floor. The floor is what a
// notch gets before any config asks for anything.
func FloorNames() []string {
	excluded := map[string]struct{}{}
	for _, n := range FloorExcludedUnbuildable {
		excluded[n] = struct{}{}
	}
	for _, n := range FloorExcludedPolicy {
		excluded[n] = struct{}{}
	}
	out := make([]string, 0, len(ImageCoreNames))
	for _, n := range ImageCoreNames {
		if _, skip := excluded[n]; skip {
			continue
		}
		out = append(out, n)
	}
	return out
}

// gnuUserlandAttrs are the nixpkgs attrs that install a GNU implementation of a
// tool macOS ships its own BSD copy of, and whose attr name does not already say
// so with a `gnu` prefix.
//
// THE PREDICATE IS DELIBERATELY WIDER THAN THE RULING'S OWN LIST, because a
// policy gate must fail in the direction that costs a conversation rather than
// the direction that costs a silent shipment. A false positive stops a build and
// forces someone to decide; a false negative is the defect.
//
// What it deliberately does NOT claim, each for a stated reason — these are on
// the floor today and are supposed to be:
//
//   - `bashInteractive` — GNU software, but not a BSD-vs-GNU behaviour surprise:
//     macOS's own /bin/bash IS GNU bash, frozen at 3.2 by a licence change. A
//     modern bash is what every other backend has and what agent scripts assume.
//   - `gzip`, `bzip2`, `xz`, `zip`, `unzip` — archivers, where the observable
//     behaviour of the common flags is the same across implementations. ⚠ `gzip`
//     IS GNU gzip and macOS ships a NetBSD one, so it is the closest call on
//     this list; OQ-P2's own table does not name it, and widening a maintainer's
//     ruling is not this file's job. Flagged rather than decided.
//   - `which`, `procps` — on darwin nixpkgs resolves both through `unixtools`,
//     i.e. thin wrappers around the Mac's own binaries.
//
// Adding a name here is how you extend the policy; adding one to
// FloorExcludedPolicy is how you act on it. The tests require both.
var gnuUserlandAttrs = map[string]struct{}{
	"coreutils":      {}, // ls, cp, mv, stat, date, …
	"coreutils-full": {},
	"gawk":           {},
	"findutils":      {}, // find, xargs
	"diffutils":      {}, // diff, cmp
	"binutils":       {}, // ar, nm, strip — shadows Apple's cctools
	"inetutils":      {}, // ftp, telnet, hostname
}

// IsGNUUserland reports whether a nixpkgs attr name is GNU userland for the
// purposes of OQ-P2 — a GNU implementation of a tool the Mac already provides
// its own.
//
// Two halves, and the first is why this is a predicate instead of a list. A name
// nixpkgs prefixes with `gnu` announces itself, so `gnumake`, `gnutar` and a
// package added next year are all caught without anyone editing this function.
// The second half names the GNU userland attrs that carry no such prefix.
//
// The prefix half over-reaches on purpose: `gnupg` and `gnuplot` would be
// flagged, and neither shadows a macOS tool. That is a false positive costing
// one deliberate decision — the right trade against a false negative, which
// costs an agent silently getting GNU `sed` on somebody's Mac.
func IsGNUUserland(name string) bool {
	if _, ok := gnuUserlandAttrs[name]; ok {
		return true
	}
	return len(name) > 3 && name[:3] == "gnu"
}
