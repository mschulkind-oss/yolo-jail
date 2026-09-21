package check

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/basehome"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// noteLegacyBaseHome is the check-side half of the base-home disclosure
// (docs/design/base-home-legacy-state.md §5.7 names both host call sites).
//
// Detection only, and on this side that is structural rather than a choice: check has no
// stdin seam at all (Options carries IsTTYStdout and no IsTTYStdin), so it could never
// run the apply's confirmation, which is the design's own reason for calling it a
// detection site and never an apply site.
//
// It writes to o.Stderr, not the report: a disclosure survives `--format json` (outfmt
// discards only the human stdout report) and is not part of the JSON payload — see the
// deviation note in the design's §5.8 follow-up.
func (o *Options) noteLegacyBaseHome() {
	if o.inJail() {
		return
	}
	rep := basehome.Detect(paths.GlobalHome(), basehome.ShippedDecls())
	if line := rep.Summary(); line != "" {
		fmt.Fprintln(o.Stderr, line)
	}
	for _, w := range rep.Warnings() {
		fmt.Fprintln(o.Stderr, w)
	}
}
