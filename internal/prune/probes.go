package prune

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// ProbeResult is the outcome of a container-runtime subprocess probe. Ran is
// false when the binary was absent, could not be started, or the call timed out
// (all degrade to the empty result). RC carries the exit status when Ran is
// true; the engine treats any non-zero RC as an empty degrade, exactly like a
// spawn failure.
type ProbeResult struct {
	Stdout string
	RC     int
	Ran    bool
}

// RunFunc is the injectable exec seam (the internal/pscmd Deps pattern applied
// to the pure engine): it runs argv with a per-call timeout and returns the
// captured stdout. The real implementation lives in this package; tests
// stubs it with canned output keyed by argv. A stub that models "runtime absent"
// returns Ran=false; "runtime present, listing failed" returns Ran=true, RC!=0.
type RunFunc func(argv []string, timeout time.Duration) ProbeResult

// probe timeouts (per-call deadlines for each runtime subprocess).
const (
	psTimeout      = 10 * time.Second
	inspectTimeout = 5 * time.Second
	rmTimeout      = 10 * time.Second
	rmiTimeout     = 15 * time.Second
)

// isLiveState reports whether a podman State string denotes a live jail
// (running/paused/restarting, case-insensitive). The liveness predicate used by
// PruneStoppedContainers (skip live → remove the rest).
func isLiveState(state string) bool {
	switch strings.ToLower(state) {
	case "running", "paused", "restarting":
		return true
	}
	return false
}

// resolvePath resolves symlinks to an absolute path for our inputs (existing
// container bind sources), and on failure falls back to an absolute-cleaned
// path (it never errors out). Used to dedup workspace paths (FindYoloWorkspaces)
// — both sides of a comparison run through this, so equality is preserved.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// pySplitMax splits on whitespace with a cap: leading/trailing whitespace is
// ignored, fields are separated by runs of ASCII whitespace, and after
// `maxsplit` cuts the remainder (internal whitespace preserved, trailing
// stripped) is the final field. Used to split the `images` line into
// (id, repo:tag, createdAt) so the CreatedAt sort key keeps its internal spaces
// intact.
func pySplitMax(s string, maxsplit int) []string {
	isWS := func(b byte) bool {
		switch b {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			return true
		}
		return false
	}
	var out []string
	i, n := 0, len(s)
	for {
		for i < n && isWS(s[i]) {
			i++
		}
		if i >= n {
			break
		}
		if len(out) == maxsplit {
			// Remainder field: leading whitespace already skipped above; keep
			// everything to the end verbatim (trailing whitespace PRESERVED).
			out = append(out, s[i:n])
			break
		}
		start := i
		for i < n && !isWS(s[i]) {
			i++
		}
		out = append(out, s[start:i])
	}
	return out
}

// inspectMountSource returns the host Source bound at `dest` for container
// `name`, or ("", false) on any inspect failure / absence. It runs
// `inspect --format {{json .Mounts}}` and decodes the mounts array via a
// type-guarded walk (a non-array top-level or a non-object element is skipped,
// never crashes), returning the first matching non-empty Source. Its callers pass
// dest=/workspace (InspectWorkspaceMount) and dest=/opt/yolo-jail/bin
// (InspectPrefixBinMount).
func inspectMountSource(rt, name, dest string, run RunFunc) (string, bool) {
	res := run([]string{rt, "inspect", "--format", "{{json .Mounts}}", name}, inspectTimeout)
	if !res.Ran || res.RC != 0 {
		return "", false
	}
	var top any
	if err := json.Unmarshal([]byte(res.Stdout), &top); err != nil {
		return "", false
	}
	mounts, ok := top.([]any)
	if !ok {
		return "", false
	}
	for _, mi := range mounts {
		m, ok := mi.(map[string]any)
		if !ok {
			continue
		}
		if d, _ := m["Destination"].(string); d == dest {
			if src, ok := m["Source"].(string); ok && src != "" {
				return src, true
			}
		}
	}
	return "", false
}

// InspectWorkspaceMount returns the host path bound at /workspace for `name`, or
// ("", false).
func InspectWorkspaceMount(rt, name string, run RunFunc) (string, bool) {
	return inspectMountSource(rt, name, "/workspace", run)
}

// InspectPrefixBinMount returns the host directory bound at the jail's install
// prefix bin/ for `name`, or ("", false).
//
// It answers "where is this running container's pid1 actually coming from" for
// the two callers that ask it about ONE container rather than about all of them:
// the attach path's post-mortem, when an exec died because that binary was not
// there, and (through LivePrefixSources) the reapers. The dest is the launcher's
// own constant, so a container whose mount predates the mounted prefix answers
// ("", false) — which is not an error, just an older jail.
func InspectPrefixBinMount(rt, name string, run RunFunc) (string, bool) {
	return inspectMountSource(rt, name, prefixBinMountDest, run)
}

// FindYoloWorkspaces returns the deduplicated, resolved host workspace paths for
// every yolo-* container the runtime knows about (running or stopped).
// `ps -a --format {{.Names}}` → keep yolo-* names → inspect each's /workspace
// bind → resolve + dedup, preserving first-seen order. A missing/failed runtime
// yields an empty list.
func FindYoloWorkspaces(rt string, run RunFunc) []string {
	res := run([]string{rt, "ps", "-a", "--format", "{{.Names}}"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return []string{}
	}
	var names []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "yolo-") {
			names = append(names, t)
		}
	}
	found := []string{}
	seen := map[string]struct{}{}
	for _, name := range names {
		ws, ok := InspectWorkspaceMount(rt, name, run)
		if !ok {
			continue
		}
		resolved := resolvePath(ws)
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		found = append(found, resolved)
	}
	return found
}

// PruneStoppedContainers removes stopped yolo-* containers and returns the names
// removed (or, in dry-run, that WOULD be removed).
// `ps -a --format {{.Names}} {{.State}}` → keep yolo-* whose state is NOT live
// (running/paused/restarting) → `rm <name>` each when apply. Only yolo-* names
// are ever touched. A missing/failed runtime
// yields an empty list.
func PruneStoppedContainers(rt string, apply bool, run RunFunc) []string {
	res := run([]string{rt, "ps", "-a", "--format", "{{.Names}} {{.State}}"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return []string{}
	}
	targets := []string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) < 2 {
			continue
		}
		name, state := parts[0], parts[1]
		if !strings.HasPrefix(name, "yolo-") {
			continue
		}
		if isLiveState(state) {
			continue
		}
		targets = append(targets, name)
	}
	if apply {
		for _, name := range targets {
			run([]string{rt, "rm", name}, rmTimeout)
		}
	}
	return targets
}

// ImageReapDecline names WHY an old-image sweep did nothing, because "declined"
// and "nothing to remove" were the same empty slice until OQ-LS2 and that is the
// shape of the defect the whole reclamation effort started from: a pass that is
// not running and does not say so. Empty means the pass ran.
//
// These strings are user-facing and name the missing EVIDENCE, not the internal
// step, because the reader's next move differs per cause: an unreadable ledger
// is a yolo-state problem, an unreachable runtime is a machine problem.
type ImageReapDecline string

const (
	// DeclineNoCurrentPointers: not one workspace's current-image pointer could
	// be honoured, so retention has no evidence (guard #1).
	//
	// IT NAMES THE POINTERS, not "the ledger", because OQ-LS3 moved the evidence:
	// the retention set was the load sentinel's LRU-10 of recently-loaded store
	// paths and is now the per-workspace current pointers
	// (CurrentImageTags). A reader who sees this needs to know which file to look
	// at, and the two are different files with different writers.
	DeclineNoCurrentPointers ImageReapDecline = "could not read any workspace's current-image pointer"
	// DeclineRuntimeUnreachable: `ps` could not be enumerated, so what is
	// actually running is unknown (guard #0).
	DeclineRuntimeUnreachable ImageReapDecline = "could not ask the runtime which images have containers on them"
	// DeclineImagesUnreadable: the image listing itself failed, so there is no
	// candidate set at all.
	DeclineImagesUnreadable ImageReapDecline = "could not list this runtime's images"
)

// The OWNER LABEL: the Go half of a two-language spelling whose other half is
// `config.Labels` in flake.nix's `mkOciImage`. It is what makes an image yolo
// built attributable after it loses its repository name, and it is the second of
// PruneOldImages' two entrances (minimal-disk-footprint.md OQ-DF3, REACH).
//
// ONCE ON DISK THIS IS A DE FACTO PUBLIC NAME. Renaming either half silently
// un-owns every image already built — they keep the old key, nothing filters for
// it any more, and they become exactly the unreclaimable rows this ruling
// created the label to end. TestOwnerLabelSpellingMatchesTheFlake reads flake.nix
// and pins the pair; integration/imagelabel_test.go pins it against a real image.
//
// OWNERSHIP, NEVER LIVENESS. The label says "yolo built this", full stop. Which
// image is in USE is answered by `podman ps` (guard #0), and which one a
// workspace still WANTS by that workspace's current pointer (CurrentImageTags,
// OQ-LS3 — it was the load sentinel's LRU until then) — and the flake CANNOT carry more
// than ownership here even if a caller wanted it to: nix cannot reference a
// derivation's own output path and streamLayeredImage's script takes only
// `--repo_tag`, so the finest identity spellable is `imageIdentity`, which is one
// value per flake.nix+flake.lock and is therefore SHARED by the full/minimal/lean
// variants and every `packages:` list. Do not read the provenance label as an
// image key.
const (
	// JailImageOwnerLabel is the label key `mkOciImage` bakes.
	JailImageOwnerLabel = "org.yolo-jail.owner"
	// JailImageOwnerValue is its value — matched exactly, so the filter cannot
	// be widened by another project picking a similar key.
	JailImageOwnerValue = "yolo"
)

// imageListFormat is the row shape BOTH image probes ask for, so their rows can
// be merged and parsed by one loop: `<id> <repo>:<tag> <createdAt>`, with the
// timestamp's internal spaces recovered by pySplitMax's maxsplit=2. An untagged
// row prints `<none>:<none>` in the middle field (MEASURED 2026-09-08), which
// tagOf reduces to `<none>` — a string no protected tag can equal, which is why
// the doc comment above says guard #0 is that row's only veto.
const imageListFormat = "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}"

// listImagesByRepo is the probe that has always shipped: every image under the
// `yolo-jail` REPOSITORY, whatever its tag. Returns (stdout, ok); ok=false when
// the runtime could not answer, which is the one image-listing failure that
// declines the whole pass.
//
// The repository is spelled through paths so it cannot drift from
// image.JailImageRef's, and the argument is POSITIONAL because that is podman's
// repo filter — it matches the name component EXACTLY, so `yolo-jail-builder` is
// not in this probe's reach and must never be given the owner label either
// (flake.nix says so where the builder image is defined).
func listImagesByRepo(rt string, run RunFunc) (string, bool) {
	res := run([]string{rt, "images", "--format", imageListFormat, paths.JailImageRepoShort}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return "", false
	}
	return res.Stdout, true
}

// listImagesByOwnerLabel is REACH's new entrance: every image carrying the owner
// label, including the untagged rows the repo-name probe structurally cannot see.
// It returns rows only — a failure is an empty string, never a decline, because
// widening is optional and shrinking is safe.
//
// No `-a`. The plain listing already returns the untagged row (MEASURED
// 2026-09-08); `-a` ADDITIONALLY surfaces build intermediates carrying the same
// label, and the ruling does not authorize removing those.
func listImagesByOwnerLabel(rt string, run RunFunc) string {
	res := run([]string{rt, "images", "--filter",
		"label=" + JailImageOwnerLabel + "=" + JailImageOwnerValue,
		"--format", imageListFormat}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return ""
	}
	return res.Stdout
}

// PruneOldImages lists the images yolo can PROVE are its own, keeps every image
// the `protected` tag set vouches for and every image a container is running on,
// and returns the image IDs removed (or slated for removal in dry-run).
// TWO `images` probes (repository name, owner label) → merge their rows →
// parse (id, repo:tag, createdAt) lines (>=3 fields, split maxsplit=2) → ONE
// ENTRY PER IMAGE ID → drop the protected and the in-use → `rmi <id>` each when
// apply. A missing/failed runtime yields an empty list.
//
// # THERE IS NO KEEP WINDOW ANY MORE (OQ-LS3, ruled 2026-09-08)
//
// This function took a `keep int` and removed everything past the newest N by
// CreatedAt. The count is gone, not retuned, and `protected` now carries the
// whole retention rule: it is the union of every workspace's CURRENT-IMAGE
// POINTER (CurrentImageTags, currentimages.go) rather than the load sentinel's
// LRU-10 of recently-loaded paths. The window was never a safety mechanism —
// liveness is, and it has its own evidence in guard #0 — it was an UNDO BUFFER,
// and the ruling deletes it: *"I don't know that I've ever rolled back, only
// evolved forward."*
//
// So what survives a pass is exactly one image per configuration plus whatever
// is running, and ⚠ **that is the FLOOR, not a target to beat**: a config whose
// current image is in use is protected regardless of any count, and under a
// shared-base layer plan every kept image also holds the base layer in place.
// currentimages.go carries the argument in full; the numbers this changed on a
// real store are in the design doc's §6.2.
//
// # THE UNION IS OQ-DF3's REACH RULING (2026-09-08), and both halves are load-bearing
//
// The repository name was the ONLY evidence an image was yolo's, and it is
// exactly what a superseded image loses: a re-stream takes the content tag and
// leaves the previous image `<none>:<none>`, invisible to a repo-name filter
// forever (minimal-disk-footprint.md §3.3). The ruling closes that evidence gap
// with a LABEL baked into the image config (flake.nix `mkOciImage`), which
// survives untagging and stays filterable — so a nameless row is attributable
// and is then treated exactly like a tagged one. Ruled NARROW: yolo never
// removes an image it cannot prove is its own, so `dangling=true` and
// `podman image prune` stay refused (they are the user's images on a shared
// podman), and rows that predate the label are LEFT ALONE PERMANENTLY.
//
// TWO probes rather than one filtered query, because podman refuses the
// combination: `podman images yolo-jail --filter label=…` is
// `Error: cannot specify an image and a filter(s)` (MEASURED, podman 5.8.4,
// 2026-09-08). And the repo-name probe MUST SURVIVE, because a label marks only
// images built after it ships — every tagged row already on a machine is
// reapable today through the repo name alone, and a label-only "simplification"
// would silently strand them with no test going red.
//
// # C2 ARMED THIS PASS, so C2 had to make it safe
//
// The positional argument is a REPOSITORY filter ("yolo-jail"), not a tag, and
// content-addressed tags live under the same repository. What C2 changed is the
// SHAPE of what comes back, and that is not cosmetic — it is the difference
// between a pass that could never select anything and one that force-removes
// another workspace's running image.
//
// Two consequences, both handled here:
//
//  1. ONE ROW PER TAG, NOT PER IMAGE. `podman images` prints a row for every
//     name, so the newest image appears TWICE — once under its content tag and
//     once under :latest — and a per-row keep window silently spent two of its
//     slots on one image. Measured on the maintainer's host the moment C2
//     landed: three rows, two images, and `keep=2` selected the second image for
//     removal. Entries are therefore deduped by ID. The window that made that
//     miscount destructive is gone (OQ-LS3), and the dedup is NOT: removal is by
//     ID, so two rows for one image are two verdicts about one deletion, and
//     they have to be reconciled before any of them acts.
//  2. NO LIVENESS GATE AT ALL. While one :latest tag named every image the list
//     was one row long and `keep` never fired, so the absence never showed;
//     under per-config tags "everything past the newest 2" is "every config
//     except the most recently LOADED one". CreatedAt is the moment the archive
//     was streamed, not a build time (`created = "now"` in flake.nix), and C2's
//     whole point is that a revisited workspace does NOT reload — so a live jail
//     that has been running for a week carried a week-old timestamp and sorted
//     last. `rmi -f` then removed the containers using the image, killing a live
//     jail in another workspace mid-session. Guard #0 below answers that
//     directly now, and `rmi` no longer forces.
//
// THE RETENTION RULE IS NOT C2's TO SET, and since OQ-LS3 it is not a NUMBER at
// all: `protected` is the union of the per-workspace current pointers
// (CurrentImageTags), and `known` is its fail-safe. REACH — the two-probe union
// above — does not move it: a labeled nameless row is treated exactly like a
// tagged one. What OQ-DF3's TRIGGER half added, and what still holds, is that
// something other than a human typing `yolo prune --apply` calls this with
// apply=true: see AutoReapOldImages (autoreap.go), the launch-path caller.
//
// # A NAMELESS ROW'S ONLY VETO IS GUARD #0, and that is not an oversight
//
// `protected` matches TAGS, and `<none>` has none — so the pointer-derived
// retention set is structurally silent for the class this union just
// added. What catches a LIVE nameless row is `podman ps`: a re-stream takes the
// tag off the image a jail is running on, and `ps --format {{.ImageID}}` prints
// the same 12-hex short ID as `images --format {{.ID}}` (MEASURED 2026-09-08),
// so the in-use veto matches it exactly. That equality was a convenience for
// tagged rows; it is the whole safety story for nameless ones — with the plain
// (never `-f`) `rmi` below as the backstop that fails rather than kills.
func PruneOldImages(rt string, protected map[string]struct{}, protectedKnown bool, apply bool, run RunFunc) (removed []string, declined ImageReapDecline) {
	// THE CANDIDATE LISTING COMES FIRST, and the order is load-bearing since
	// OQ-LS2 made a decline an ERROR on the manual path. A machine where yolo
	// has never launched has no current-image pointers, so protectedKnown is
	// false — but nothing was denied there, because there was nothing to reap.
	// Asking what exists before asking whether it is safe to touch is what lets
	// "declined" mean "prevented work" rather than "fresh machine".
	//
	// Listing is read-only and removes nothing, so no guard below is weakened by
	// running after it; the guards still gate every `rmi`.
	//
	// BOTH probes run here, before any guard, for that same reason — and the
	// repo-name one runs FIRST so its failure keeps meaning what it has always
	// meant. The label probe cannot decline anything (see below), so a runtime
	// that answers neither still reports DeclineImagesUnreadable.
	repoRows, ok := listImagesByRepo(rt, run)
	if !ok {
		return []string{}, DeclineImagesUnreadable
	}
	// A FAILED LABEL PROBE WIDENS NOTHING AND DECLINES NOTHING: it contributes
	// zero rows, which shrinks the candidate set — the safe direction. Declining
	// the whole pass on it would regress the shipped tagged reap on any runtime
	// that spells (or refuses) `--filter label=` differently; Apple Container is
	// NOT MEASURED here, and its own reaper does not exist yet (OQ-BF6).
	labelRows := listImagesByOwnerLabel(rt, run)
	rows := repoRows + "\n" + labelRows
	if strings.TrimSpace(rows) == "" {
		return []string{}, "" // nothing of ours exists; nothing to decline about
	}
	if !protectedKnown {
		// GUARD #1, THE FAIL-SAFE, AND IT IS DOING MORE WORK SINCE OQ-LS3. With no
		// keep window left, `protected` is the ENTIRE retention rule — so an empty
		// set no longer means "keep the newest two anyway", it means "remove every
		// image of ours that nothing is running". "No workspace wants anything" and
		// "I cannot tell what any workspace wants" are the same observation from
		// here, and an unproven set is not a licence to remove: decline entirely.
		// Same polarity as every other tri-state in this package.
		//
		// This is also what makes the day OQ-LS3 ships safe by construction: the
		// pointer directory does not exist yet, so the first pass declines and says
		// so rather than reaping a machine's whole image store.
		return []string{}, DeclineNoCurrentPointers
	}
	// GUARD #0, AND THE ONLY ONE THAT ASKS THE RUNTIME WHAT IS ACTUALLY RUNNING.
	// Retention answers "which image does each workspace still want", which is
	// not the same question: a jail that is UP but not relaunching does not
	// re-record anything, and before OQ-LS1/LS3 the only evidence was a
	// ten-entry LRU of recently-LOADED store paths that such a jail ages out of.
	// Measured 2026-09-08: a day of commits to internal/ moved goSrc on every
	// launch, four jails up 3-4 days aged out, and one auto-reap removed their
	// images mid-session.
	//
	// `podman ps` is the direct question. Unreadable => decline entirely, the
	// same polarity as guard #1: "nothing is running" and "I cannot tell" must
	// not be the same answer when the action is destructive.
	inUse, inUseKnown := imagesInUseByRunningContainers(rt, run)
	if !inUseKnown {
		return []string{}, DeclineRuntimeUnreachable
	}
	// ONE loop over the MERGED rows, deliberately — not one pass per probe. Both
	// the dedup and the protected-tag scan have to see every row before the
	// removal decision: an image built after the label ships answers to BOTH
	// probes, and a per-probe verdict would let its unprotected duplicate row
	// delete the image its protected row saves (the same defect the `:latest`
	// row already taught us, one entrance over).
	var images []ImageEntry
	seen := map[string]bool{}
	keepIDs := map[string]bool{}
	for _, line := range strings.Split(rows, "\n") {
		parts := pySplitMax(strings.TrimSpace(line), 2)
		if len(parts) < 3 {
			continue
		}
		id := parts[0]
		if _, live := protected[tagOf(parts[1])]; live {
			// ANY protected name saves the whole image: the newest one wears both a
			// content tag and :latest, and removal is by ID, so a per-row verdict
			// would let the unprotected row delete the protected image.
			keepIDs[id] = true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		images = append(images, ImageEntry{ID: id, Created: parts[2]})
	}
	// NEWEST FIRST, and the sort is now only about the ORDER OF THE REPORT. It
	// used to feed OldImagesToRemove's keep window, so an image's POSITION
	// decided whether it lived; OQ-LS3 deleted that window and the function with
	// it. Kept because both entrances print the IDs they are removing and a
	// newest-first list of hashes is readable where an arbitrary one is not.
	//
	// ⚠ AND IT IS NO LONGER A TIME. Layer-aware delivery pinned the image's
	// `created` to a CONSTANT — nix2container rejects "now", and OQ-LI4 refused a
	// per-build timestamp because it would trade away content addressing to feed a
	// sort. So every yolo image now reports the same CreatedAt and this sort is a
	// stable no-op that preserves podman's own listing order.
	//
	// Harmless, and deliberately not "fixed" here: NO destructive decision reads
	// it. OQ-LS3 deleted the keep window, so retention is the per-workspace
	// pointer set plus the `podman ps` veto and nothing consults position. If a
	// real recency order is ever wanted for the REPORT, the load sentinel is the
	// instrument (OQ-LI4's ruling), not a timestamp.
	//
	// LEXICAL, never parsed: podman's CreatedAt is ISO-ish and sorted correctly as
	// a string, which is the property the deleted function documented — and which
	// is why a constant degrades to a no-op instead of to garbage.
	sort.SliceStable(images, func(i, j int) bool { return images[i].Created > images[j].Created })
	toRemove := []string{}
	for _, e := range images {
		if keepIDs[e.ID] || inUse[e.ID] {
			continue
		}
		toRemove = append(toRemove, e.ID)
	}
	if apply {
		for _, id := range toRemove {
			// NO -f. Forcing is what turned a disk-space sweep into a jail
			// killer: `rmi -f` removes the CONTAINERS using an image, so every
			// mistake in the selection above costs somebody their session. A
			// plain `rmi` FAILS on an image a container still uses, which makes
			// the safety a property of podman rather than of this function
			// getting its evidence right — belt to guard #0's braces, and the
			// half that keeps working when the evidence is wrong.
			run([]string{rt, "rmi", id}, rmiTimeout)
		}
	}
	return toRemove, ""
}

// relayShortHash is the 8-char hash keying a jail's broker-relay pid/lock/socket
// files and its host-services dir. It delegates to paths so it CANNOT drift from
// the run pipeline's spelling — ReapRelayOrphans matches a pid file back to a live
// container name through this value, so a divergence reaps live relays.
func relayShortHash(cname string) string { return paths.JailShortHash(cname) }

// ReapRelayOrphans sweeps per-jail broker-relay PID files under `base` whose jail
// is no longer live, returning the PID-file paths reaped (or, in dry-run, that
// WOULD be).
//
// # THIS IS A LEGACY SWEEP AS OF THE BROKER CONVERSION
//
// yolo no longer spawns per-jail relays at all: the broker singleton sits behind
// one svcendpoint front per jail, owned by the yolo process that launched that jail
// and dying with it (docs/design/broker-as-a-pack.md §7). Nothing this sweep looks
// for will ever be created again by this binary.
//
// It is kept, and kept for one release, because the upgrade is the case it was
// always for: a host that was running jails under a PRE-conversion yolo has live
// relay processes and their pid/lock/socket files in /tmp right now, and the run
// pipeline's own backstop reap — which used to piggyback on this — went away with
// the machinery. `yolo prune --apply` is what collects them (a reboot also does,
// since these live in /tmp). Delete this and its callers once that release has
// shipped; a sweep for files nothing writes is otherwise a decision nobody made.
// - liveKnown==false (liveness unenumerable) → reap NOTHING (same fail-safe
// polarity as the agent-staging sweep — unknown must never read as "nothing
// live");
// - a pid file whose 8-char hash matches a live container is kept;
// - a pid file younger than olderThanSeconds (mtime grace floor for a jail
// mid-startup) is kept;
// - on apply, the relay is killed (via the injected relayKill seam — the
// signal/pgrep machinery is the caller's concern), then the .lock file, the
// relay's own .sock, and the yolo-host-services-<hash> dir are removed. The
// .sock is listed separately because the relay's socket is HOST-ONLY and lives
// beside its pid file, not inside the per-jail dir the rmtree covers.
//
// The reaped list is sorted by path, so the displayed order is deterministic.
func ReapRelayOrphans(base string, liveKnown bool, liveCnames map[string]struct{}, olderThanSeconds float64, apply bool, now time.Time, relayKill func(pidFile string)) []string {
	reaped := []string{}
	if !liveKnown {
		return reaped
	}
	liveHashes := map[string]struct{}{}
	for c := range liveCnames {
		liveHashes[relayShortHash(c)] = struct{}{}
	}
	matches, _ := filepath.Glob(filepath.Join(base, "yolo-broker-relay-*.pid"))
	sort.Strings(matches)
	cutoff := now.Add(-time.Duration(olderThanSeconds * float64(time.Second)))
	for _, pidFile := range matches {
		shortHash := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(pidFile), "yolo-broker-relay-"), ".pid")
		if _, live := liveHashes[shortHash]; live {
			continue
		}
		st, err := os.Stat(pidFile)
		if err != nil {
			continue // unlinked under us — someone else reaped it
		}
		// Kept when mtime >= cutoff (younger than the grace floor).
		if !st.ModTime().Before(cutoff) {
			continue
		}
		reaped = append(reaped, pidFile)
		if !apply {
			continue
		}
		if relayKill != nil {
			relayKill(pidFile)
		}
		_ = os.Remove(filepath.Join(base, "yolo-broker-relay-"+shortHash+".lock"))
		_ = os.Remove(filepath.Join(base, "yolo-broker-relay-"+shortHash+".sock"))
		_ = os.RemoveAll(filepath.Join(base, paths.HostServicesDirName(shortHash)))
	}
	return reaped
}

// LiveYoloContainers returns the tri-state set of live yolo-* container names.
// (running only, every yolo-* row is live); podman/others use `ps -a --format
// {{.Names}} {{.State}}` filtered to the live states. A missing/failed runtime
// yields Known=false (liveness unknown → the relay sweep declines), never an
// empty set. Parsing reuses internal/runtime's byte-verified parsers.
func LiveYoloContainers(rt string, run RunFunc) runtime.LiveSet {
	if rt == "container" {
		res := run([]string{"container", "ls"}, psTimeout)
		if !res.Ran || res.RC != 0 {
			return runtime.LiveSet{Known: false}
		}
		return runtime.LiveSet{Known: true, Names: runtime.ParseContainerLsLive(res.Stdout)}
	}
	res := run([]string{rt, "ps", "-a", "--format", "{{.Names}} {{.State}}"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return runtime.LiveSet{Known: false}
	}
	return runtime.LiveSet{Known: true, Names: runtime.ParsePodmanLive(res.Stdout)}
}

// RunningImageRefs lists the image REFERENCES of running containers, deduped —
// the sibling of imagesInUseByRunningContainers, which reads IDs.
//
// Refs rather than IDs because the ref carries the content tag, and that tag is
// image.ImageStoreKey of the store path the image was loaded from — the one
// string that maps a running container back to its nix closure and therefore to
// its roots/<sha16> link. An ID maps to nothing yolo has recorded.
//
// Returns (refs, known); known=false when the runtime could not be enumerated,
// and every caller must treat that as "decline", never as "nothing running".
func RunningImageRefs(rt string, run RunFunc) ([]string, bool) {
	res := run([]string{rt, "ps", "--format", "{{.Image}}"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return nil, false
	}
	refs := []string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if ref := strings.TrimSpace(line); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs, true
}

// imagesInUseByRunningContainers asks the runtime which image IDs currently have
// a container on them. known=false when the question could not be answered, and
// the caller then declines to remove anything.
//
// Every runtime yolo drives spells this the same way, and the ID is matched
// against the same `{{.ID}}` column `podman images` prints, so no tag parsing is
// involved — a jail runs a content-addressed ref, and tags are exactly what the
// LRU veto already reasons about badly.
func imagesInUseByRunningContainers(rt string, run RunFunc) (map[string]bool, bool) {
	res := run([]string{rt, "ps", "--format", "{{.ImageID}}"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return nil, false
	}
	inUse := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if id := strings.TrimSpace(line); id != "" {
			inUse[id] = true
		}
	}
	return inUse, true
}
