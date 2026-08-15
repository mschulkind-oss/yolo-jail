package packload

// supersede.go is the pack side of capability supersession
// (docs/design/pack-capabilities.md): reading a pack's `supersedes` claims, and the
// one deliberate decision about where they do and do not belong.
//
// # A supersession is a FOOTPRINT claim and NOT a host-access claim
//
// It is reported by `yolo pack footprint` (footprint.go emits one Claim per entry),
// and it is deliberately absent from Pack.HostAccessClaims — the set a user approves
// at `yolo pack install` and the launch gate re-checks against the lockfile. Four
// reasons, in the order they decided it:
//
//  1. EVERY OTHER CLAIM IN THAT SET GRANTS THE PACK SOMETHING it may not otherwise
//     have — a host file read, a daemon argv on your machine, a device node, a
//     trusted CA. Supersession grants nothing. It RELINQUISHES: it says a job need
//     not be done, and the thing that stops is yolo's own bundled daemon. An approval
//     prompt whose value is that every line is a real capability is diluted by a line
//     that is not one (packRequiresProblems makes the same call about
//     `requires.file_exists`, which is scoped but claimless).
//  2. THE FAILURE DIRECTION IS ALREADY SAFE. Withholding a host read means a pack
//     does not get your credentials. Withholding a supersession means the bundled
//     broker keeps running — the status quo. There is no privilege to withhold, so
//     the gate would have nothing to protect.
//  3. THE KEY WOULD BE CONTENT-BLIND OR ENDLESSLY RE-PROMPTING. An approval string is
//     an exact-match lockfile key. Key it on `capability` alone and the author can
//     reword `because` to anything after approval — the content-blind consent
//     loophole-packaging.md §4 flags as a new invariant. Key it on both and every
//     wording tweak re-prompts, which since promptYesNo fails closed on a non-TTY
//     means a reworded sentence permanently refuses the pack.
//  4. VISIBILITY IS ALREADY SERVED, twice, and unconditionally: the footprint prints
//     the claim whether or not the pack is approved, and `yolo loopholes list`/
//     `status` name the pack and print the `because` wherever the supersession takes
//     effect. The thing an approval would buy — that nobody is surprised — is bought
//     by the report instead.
//
// If that ever needs revisiting, the trigger is a supersession that turns off
// something SECURITY-BEARING rather than something merely useful. Nothing bundled is
// today: the broker serializes token refreshes, `audio` passes sockets,
// `host-processes` reads a process list.

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// Supersessions returns this pack's `supersedes` claims, in declaration order.
//
// A method on *Pack rather than a bare read of p.Decl, so the run pipeline has one
// name to call when it converts these into loopholes.PackSupersession values (which
// need the PACK NAME alongside each claim, and that is a fact about the Pack, not
// about its manifest).
func (p *Pack) Supersessions() []packdecl.Supersession {
	if p == nil || p.Decl == nil {
		return nil
	}
	return p.Decl.Supersessions()
}
