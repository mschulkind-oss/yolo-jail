package run

// loopholeinert.go is the INERT REPORT: one line when a selected pack's loophole will do
// nothing this launch, whether the reason is the BACKEND or the PLATFORM
// (docs/design/loophole-packaging.md §8 item 2, §3.1).
//
// # This is the B-0 rule applied to a new kind
//
// run.go records B-0 as "a backend that looked provisioned and configured nothing", and the
// whole run pipeline was restructured to end it. Two shipped backends make the loophole kind
// a silent no-op, and both skips are WIDER than draft 1 of the design claimed:
//
//   - Apple Container: startLoopholes returns nil for rt == "container" BEFORE any external
//     service starts, so EVERY pack-shipped host daemon is skipped there, intercepting or not.
//     A different skip from the container-ARGS one draft 1 cited (loopholes/runtime.go's
//     `intercepts` check, which only drops --add-host).
//   - macos-user: the branch returns from Run() long before startLoopholes is reached, so the
//     kind is inert on that backend ENTIRELY. macos-user-nix-and-features.md already states
//     it; nothing printed it.
//
// So a pack whose whole purpose is a loophole could be installed, selected, and completely
// inert, with the jail reporting a successful launch.
//
// # ONE MECHANISM, TWO AXES
//
// §3.1 is explicit that the platform declaration and the inert-backend report share one
// mechanism, because platform (darwin vs linux) and backend (container, macos-user) are two
// axes with ONE answer shape: "this loophole does nothing here, and here is why." Two
// half-messages for one user-visible situation is how B-0 happened in the first place. The
// platform half is read through internal/loopholedecl's own PlatformsUnsupportedReason —
// never re-implemented here, because two matchers over one declaration is how a report and a
// gate come to disagree.

import (
	"runtime"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// backendInertReason says why a backend runs NO loophole host service, or "" when it does.
//
// Both answers are measured, and both are wider than draft 1 of the design claimed:
//
//   - container (Apple Container): startLoopholes returns nil for rt == "container" before
//     any external service starts, so EVERY pack-shipped host daemon is skipped there,
//     intercepting or not. That is a different skip from the container-ARGS one
//     (loopholes/runtime.go's `intercepts` skip), which only drops --add-host.
//   - macos-user: the branch returns from Run() long before startLoopholes is reached, so
//     the kind is inert on that backend ENTIRELY. macos-user-nix-and-features.md already
//     states it; nothing printed it.
//
// This is the B-0 rule applied to a new kind — run.go records B-0 as "a backend that looked
// provisioned and configured nothing", and the pipeline was restructured to end it. A pack
// whose whole purpose is a loophole must not look installed on a backend that ignores it.
func backendInertReason(rt string) string {
	switch rt {
	case "container":
		return "inert on this backend — the Apple Container backend starts no loophole host " +
			"services (no socket bind-mount there), so nothing it declares runs this launch"
	case "macos-user":
		return "inert on this backend — the macos-user backend starts no loophole host " +
			"services; a native process already reaches the host directly, so the whole " +
			"mechanism is bypassed"
	}
	return ""
}

// notePackLoopholesInert prints ONE line per pack-shipped loophole that will do nothing this
// launch, naming the axis that made it inert.
//
// ONE MECHANISM, TWO AXES (§3.1, §8). Platform (`darwin` vs `linux`) and backend
// (`container`, `macos-user`) both answer "this loophole does nothing here, and here is
// why", and the design is explicit that splitting them would produce two half-messages for
// one user-visible situation.
//
// BACKEND BEATS PLATFORM when both apply, and that is not arbitrary: an inert backend
// starts no host service whatever the platform says, so the platform answer would be a
// second reason for one outcome. The line the user needs is the one they can act on — and
// on an inert backend that is "switch backends", not "get a different machine".
//
// The platform half is read through internal/loopholedecl (the schema's own
// PlatformsUnsupportedReason), never re-implemented here: two matchers over one declaration
// is how a report and a gate come to disagree. A manifest that will not parse prints
// nothing — the discovery layer's contract is warn-and-continue and it already warns, and a
// second complaint from the launch path about the same file would read as a second bug.
//
// It builds loopholes.InertNote VALUES and renders them through InertNote.Line, rather than
// formatting its own sentence. That type is the mechanism's own shape — a producer hands back
// notes, one caller renders — and it carries the AXIS, so a reader with two lines can tell
// "wrong machine" from "wrong backend" without parsing prose. Formatting here instead would
// be the second half-message §3.1 forbids, with the platform producer's rendering unused
// beside it.
func (o *Options) notePackLoopholesInert(rt string, packs []*packload.Pack) {
	backend := backendInertReason(rt)
	var lines []string
	for _, p := range packs {
		for _, lp := range packLoopholes(p) {
			note := loopholes.InertNote{Name: lp.Name, Axis: loopholes.AxisBackend, Reason: backend}
			if backend == "" {
				reason := loopholePlatformInertReason(lp.Dir)
				if reason == "" {
					continue
				}
				note = loopholes.InertNote{
					Name: lp.Name, Axis: loopholes.AxisPlatform, Reason: reason,
				}
			}
			// The pack name is prefixed here rather than folded into the note, because it is
			// a fact about THIS report's context (which selected pack shipped it) and not
			// about the loophole — `yolo loopholes list` renders the same note for a
			// hand-placed loophole that has no pack at all.
			lines = append(lines, inertLineFor(lp.Pack, note))
		}
	}
	if len(lines) == 0 {
		return
	}
	sort.Strings(lines)
	out := o.pr(o.Stderr)
	for _, l := range lines {
		out.print("[yellow]" + l + "[/yellow]")
	}
}

// loopholePlatformInertReason reads a loophole module's `platforms` declaration and returns
// the unsupported-here reason, or "" when it runs here (or cannot be read).
//
// TOLERANT read, matching every other cross-version manifest read: a manifest carrying a key
// only a newer build knows must not make this report claim the loophole is fine, nor make it
// shout. Evaluated against runtime.GOOS/GOARCH for the reason loopholes.SupportedHere
// documents at length: the question is "what will the machine that spawns the host daemon
// be", and inside a nested jail that machine IS the container.
func loopholePlatformInertReason(dir string) string {
	m, _, err := loopholedecl.LoadDirTolerant(dir)
	if err != nil || m == nil {
		return ""
	}
	return m.PlatformsUnsupportedReason(runtime.GOOS, runtime.GOARCH)
}

// inertLineFor renders what THIS report prints for one (pack, loophole, reason), so a test can
// match a whole line rather than guess at a substring.
//
// A function rather than a marker constant, because after the convergence onto
// loopholes.InertNote there is no single interesting substring left to key on: the two axes
// share only Line's fixed "loophole <name> is " prefix, and a marker taken from either axis's
// reason clause would silently stop matching the other — the drift the single rendering exists
// to prevent. Producing the exact line from the same inputs the report uses cannot drift at
// all.
func inertLineFor(pack string, note loopholes.InertNote) string {
	return pack + ": " + note.Line()
}
