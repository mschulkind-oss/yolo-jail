package cli

// applyhostoverlaykeys.go is the `yolo host apply` call site for ruling R3's first sentence:
// remove the `config-overlay` keys a pack asserted into the user's config file once that pack
// is no longer in `packs`.
//
// The mechanism is entrypoint.PruneHostOverlayKeys (which owns the provenance reading, the
// eligibility rules, and the RMW write). What lives HERE is the two-phase shape the shared
// confirmation demands, and it is the only reason this is a file rather than one call:
//
//	PLAN    an observe-posture scan, run before anything is written, whose result is what the
//	        prompt is ABOUT. It has to be a separate pass because a confirmation the user has
//	        not been shown the contents of is not a confirmation.
//	COMMIT  the same scan in assert posture, run only after a yes.
//
// ONE PROMPT, NOT TWO (the coordination this file exists to get right). Ruling R3 says overlay
// keys ride "the same confirm" as R1's skills/files retirement rather than a second gate, so
// the plan is handed to pruneDroppedPackOutput and appears in confirmDroppedPackRetire's list
// alongside the paths. A second [y/N] for the same user action — "I removed a pack from my
// config" — is exactly the prompt-fatigue the gate's own docstring warns about.
//
// A key is not archived, unlike a retired path (R2), and the asymmetry is real rather than an
// omission: a path is CONTENT (the user may have edited a delivered skill), while an overlay
// key is a pack's own assertion, reproduced exactly by putting the pack back in `packs` and
// re-applying. There is nothing to keep. What the user is owed is being TOLD, which is what the
// shared prompt does.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// overlayKeyRetirement is one apply's planned config-overlay key retirement: what an assert
// would remove, plus everything needed to actually remove it after a yes.
//
// It carries its inputs rather than a closure so the plan is inspectable in a test and the
// commit cannot silently disagree with what was shown — the commit re-runs the SAME scan
// against the same inputs, in assert posture.
type overlayKeyRetirement struct {
	candidates []*packload.Pack
	configured map[string]bool
	overlays   *packoverlay.OverlaySet
	home       string

	// Orphans is what the observe scan found: one entry per key an assert would remove.
	// Empty (the overwhelmingly common case) means this contributes nothing to the prompt.
	Orphans []entrypoint.HostOverlayOrphan
	// Failed marks a plan whose scan errored, so the caller can report a non-zero rc without
	// the error being mistaken for "nothing to remove".
	Failed bool
}

// planOverlayKeyRetirement runs the observe-posture scan. Errors are REPORTED and yield an
// empty plan: a scan that cannot answer must not be read as license to remove anything, and it
// must not silently become a plan with no orphans either — hence Failed.
func planOverlayKeyRetirement(pr richtext.Printer, candidates []*packload.Pack,
	configured map[string]bool, overlays *packoverlay.OverlaySet, home string) overlayKeyRetirement {
	plan := overlayKeyRetirement{candidates: candidates, configured: configured,
		overlays: overlays, home: home}
	orphans, err := entrypoint.PruneHostOverlayKeys(candidates, configured, overlays, home, true)
	if err != nil {
		pr.Printf("  [red]config-overlay prune refused[/red] — %v", err)
		plan.Failed = true
		return plan
	}
	plan.Orphans = orphans
	return plan
}

// commit performs the removal after the shared confirmation, printing one line per key, and
// returns an rc contribution.
//
// It re-runs the scan in assert posture rather than acting on the planned list, so the write
// goes through exactly one code path (entrypoint.PruneHostOverlayKeys) instead of a second
// remover that could drift from the one the plan measured with.
func (r overlayKeyRetirement) commit(pr richtext.Printer) int {
	removed, err := entrypoint.PruneHostOverlayKeys(
		r.candidates, r.configured, r.overlays, r.home, false)
	for _, o := range removed {
		pr.Printf("  [yellow]%-20s removed key %s (pack %s no longer configured)[/yellow]  "+
			"[dim]%s[/dim]", o.Surface, o.Key, o.Pack, o.Path)
	}
	if err != nil {
		pr.Printf("  [red]config-overlay prune failed[/red] — %v", err)
		return 1
	}
	return 0
}

// observeLines reports the plan in the dry-run posture — one line per key, naming the pack, so
// the user learns what an --assert would remove BEFORE any prompt exists.
func (r overlayKeyRetirement) observeLines(pr richtext.Printer) {
	for _, o := range r.Orphans {
		pr.Printf("  [yellow]%-20s would remove key %s (pack %s no longer configured)[/yellow]  "+
			"[dim]%s[/dim]", o.Surface, o.Key, o.Pack, o.Path)
	}
}
