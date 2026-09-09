package prune

// jsonreport.go is `yolo prune --format json`: the reclaim plan (or the reclaim
// receipt) as data, for the operator who is not a person reading a scrollback.
//
// WHY IT IS THE SUMMARY AND NOT THE WHOLE REPORT. prune's human output is ~650
// lines of interleaved print-and-measure across sixteen sections, and much of it
// is prose that exists to teach — the cache/images hint explaining that a tar is
// a one-shot load artifact, the nix-GC section explaining why it declined. None
// of that is state; all of it is advice, and advice belongs in the text form
// where it was written for a reader. What this document carries is the part a
// machine can act on: the pre-report usage accounting, the per-category bytes
// and counts that make up the total, the names of the containers and images that
// would disappear, and whether any sweep DECLINED.
//
// Every field here comes from a local the human summary line already prints, so
// the two forms cannot disagree about a number — there is one measurement and
// two renderings of it. What is deliberately NOT here is a per-section record
// for the six sweeps whose bytes never enter the total (broker relays, image and
// prefix GC roots, superseded store outputs, dangling out-links, the bounded nix
// store GC): those are counts of things in other ledgers, and folding them in
// would let a consumer add up a number that means nothing. They are named in
// TODO terms nowhere — read the text form for them, which is what it is for.

import (
	"encoding/json"
	"fmt"
	"os"
)

// Report is one `yolo prune --format json` document.
type Report struct {
	// Mode is "dry-run" or "apply", and it is the field that says what every
	// byte count below MEANS: what would be freed, or what was. A consumer that
	// ignores it will read a plan as a receipt.
	Mode string `json:"mode"`
	// Runtime is the container runtime prune resolved (the config-aware
	// resolution the CLI injects, not a bare probe).
	Runtime string `json:"runtime"`
	// Workspaces are the tracked yolo workspaces this run considered.
	Workspaces []string `json:"workspaces"`
	// Usage is the PRE-report accounting: what exists, before anything is
	// reclaimed. Its Relocated entries sit on other filesystems and are
	// deliberately absent from its totals — see RelocatedCache.
	Usage ReportUsage `json:"usage"`
	// TotalBytes is the sum of every category below, and the one number the
	// human summary leads with.
	TotalBytes int64 `json:"total_bytes"`
	// Hardlinks is the number of hardlinks the dedup pass made (or would).
	Hardlinks int `json:"hardlinks"`
	// Categories breaks TotalBytes down by sweep, in the order the human
	// summary names them. A skipped sweep (--no-images, --cache-age 0) is
	// present with zeroes rather than absent: "did not run" and "found nothing"
	// are both honestly zero bytes, and a missing key would make a consumer
	// guess which.
	Categories []ReportCategory `json:"categories"`
	// RemovedContainers / RemovedImages name what disappears (or did). The
	// human form lists these for auditability — "reclaimed 40 MB" is not
	// auditable — and so does this.
	RemovedContainers []string `json:"removed_containers"`
	RemovedImages     []string `json:"removed_images"`
	// Declined is true when a sweep could not run and said so, which is also
	// what makes prune exit 1 (OQ-LS2). It is the field to check before
	// believing TotalBytes is the whole answer.
	Declined bool `json:"declined"`
}

// ReportUsage is the pre-report byte accounting, mirroring DiskReport.
type ReportUsage struct {
	TotalBytes         int64            `json:"total_bytes"`
	WorkspaceBytes     int64            `json:"workspace_bytes"`
	GlobalStorageBytes int64            `json:"global_storage_bytes"`
	Breakdown          map[string]int64 `json:"breakdown,omitempty"`
	CacheBreakdown     map[string]int64 `json:"cache_breakdown,omitempty"`
	Relocated          []RelocatedCache `json:"relocated,omitempty"`
}

// ReportCategory is one sweep's contribution: its bytes, how many things that
// was, and the noun for them.
type ReportCategory struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Count int    `json:"count"`
	Unit  string `json:"unit"`
}

// usageOf converts a DiskReport into its JSON shape.
func usageOf(d DiskReport) ReportUsage {
	return ReportUsage{
		TotalBytes:         d.Total,
		WorkspaceBytes:     d.Workspaces,
		GlobalStorageBytes: d.GlobalStorage,
		Breakdown:          d.Breakdown,
		CacheBreakdown:     d.CacheBreakdown,
		Relocated:          d.CacheRelocated,
	}
}

// emitJSONReport writes r to opts.Out. Its failure path is loud rather than
// silent: an empty stdout is the one outcome a machine consumer cannot
// diagnose, and prune's own exit code is about the DISK, not about the encoder.
func emitJSONReport(opts Options, r Report) {
	enc, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo prune: encoding the report failed: %v\n", err)
		return
	}
	fmt.Fprintln(opts.Out, string(enc))
}

// nonNil turns a nil slice into an empty one, so the document always spells an
// empty list as `[]` and never as `null`. A consumer looping over the value
// should not have to special-case "nothing happened".
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
