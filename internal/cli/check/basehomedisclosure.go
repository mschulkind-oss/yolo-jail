package check

import (
	"fmt"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/basehome"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// noteLegacyBaseHome is `yolo check`'s report of legacy bytes in <state>/home — the one base
// home report left, since the launch's refusal went with the shared mount it guarded (the
// maintainer's OQ-BH13 ruling, docs/design/base-home-legacy-state.md#10-decision-ledger).
//
// Detection only, and REWORDED for what the bytes are now: unmounted and unread. Every podman
// jail binds its own home skeleton rather than <state>/home, and no workspace is seeded from
// it, so the printed `mv` (a command, never a verb — DIR-BH0) is optional cleanup rather than
// the way out of a refused launch. basehome.Report.CleanupLines composes the wording.
//
// It writes to o.Stderr, not the report: a disclosure survives `--format json` (outfmt
// discards only the human stdout report) and is not part of the JSON payload.
func (o *Options) noteLegacyBaseHome() {
	if o.inJail() {
		return
	}
	globalHome := paths.GlobalHome()
	rep := basehome.Detect(globalHome, basehome.ShippedDecls())
	if line := rep.Summary(); line != "" {
		fmt.Fprintln(o.Stderr, line)
	}
	archive := filepath.Join(paths.GlobalStorage(), "archive", "base-home")
	for _, l := range rep.CleanupLines(globalHome, archive) {
		fmt.Fprintln(o.Stderr, l)
	}
	for _, w := range rep.Warnings() {
		fmt.Fprintln(o.Stderr, w)
	}
}
