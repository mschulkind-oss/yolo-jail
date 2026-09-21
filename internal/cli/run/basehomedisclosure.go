package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/basehome"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// noteLegacyBaseHome discloses the legacy per-workspace state sitting in the BASE home —
// the host directory every jail mounts as its home, which has accumulated transcripts,
// session stores and caches that belong to no workspace
// (docs/design/base-home-legacy-state.md).
//
// OBSERVE-ONLY, and that is the whole of §11 step 1. It moves nothing, prompts for
// nothing, stamps nothing and cannot fail the launch: it is a bounded read-only walk whose
// only output is stderr. The archive move, its marker and its confirmation are step 2, and
// none of them exists yet — which is why the line says so rather than naming a command
// that would not run.
//
// HOST-ONLY, for a sharper reason than symmetry with MigrateStorageLayout's insideJail
// short-circuit: inside a jail paths.GlobalHome() resolves under the jail's own disposable
// home, while /home/agent is the :ro bind of the host's GlobalHome — so an in-jail walk
// either measures nothing or measures host bytes it has no business reporting.
//
// NOT SUPPRESSIBLE, per OQ-RO3: this is a disclosure, so no report tier and no
// YOLO_NO_BANNER silences it. Silence is reserved for the one case with nothing to
// disclose — no candidates and no degradation — which is noteHostCASAlias's rule and the
// expected steady state of a healthy host.
func (o *Options) noteLegacyBaseHome() {
	if o.Getenv("YOLO_VERSION") != "" {
		return
	}
	rep := basehome.Detect(paths.GlobalHome(), basehome.ShippedDecls())
	out := o.pr(o.Stderr)
	// The summary carries no path and no workspace name (Report.ByRoot), so there is
	// nothing in it for richtext to mistake for a tag.
	if line := rep.Summary(); line != "" {
		out.print("[dim]" + line + "[/dim]")
	}
	for _, w := range rep.Warnings() {
		out.print("[yellow]" + w + "[/yellow]")
	}
}
