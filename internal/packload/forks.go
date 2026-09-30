package packload

// forks.go is the pack-set half of the fork route (docs/design/forked-programs-as-packs.md):
// how a fork — a `program` delivered `via: "source"` — appears in a footprint.

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// forkClaimDetailPrefix opens a fork claim's Detail; Claim.DisclosureSentence keys its sentence
// on it, as it keys an installer's on "installer: ".
const forkClaimDetailPrefix = "fork build: "

// ForkClaimTarget is a fork's footprint target: its bin, qualified by the base pack it forks.
//
// Qualified so the `program` kind's exclusive loop keys it apart from the base's own claim on the
// bin — a fork claims no name (OQ-FP5), so the two must never read as two owners of one — while
// two forks of one base still share a target, and so collide there as they collide at the launch
// (ApplyForks refuses a second fork of one program).
func ForkClaimTarget(bin, base string) string {
	return bin + " (fork of " + base + ")"
}

// forkClaimDetail is a fork claim's Detail: the source address and the build command, verbatim,
// so two forks that differ in either render as two lines.
func forkClaimDetail(c packdecl.Contribution) string {
	return forkClaimDetailPrefix + c.Source + ", built by `" + c.Build + "`"
}
