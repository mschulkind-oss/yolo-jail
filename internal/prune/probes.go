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
// never crashes), returning the first matching non-empty Source. The sole caller
// passes dest=/workspace (InspectWorkspaceMount).
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

// PruneOldImages lists yolo-jail images, keeps the newest `keep` plus every
// image the `protected` tag set vouches for, and returns the image IDs removed
// (or slated for removal in dry-run).
// `images --format {{.ID}} {{.Repository}}:{{.Tag}}
// {{.CreatedAt}} yolo-jail` → parse (id, repo:tag, createdAt) lines (>=3 fields,
// split maxsplit=2) → ONE ENTRY PER IMAGE ID → the EXISTING OldImagesToRemove
// lexical CreatedAt sort → drop the protected → `rmi -f <id>` each when apply. A
// missing/failed runtime yields an empty list.
//
// # C2 ARMED THIS PASS, so C2 had to make it safe
//
// The query is unchanged and did not need to change: the positional argument is
// a REPOSITORY filter ("yolo-jail"), not a tag, and content-addressed tags live
// under the same repository. What changed is the SHAPE of what comes back, and
// that is not cosmetic — it is the difference between a pass that could never
// select anything and one that force-removes another workspace's running image.
//
// Two consequences, both handled here:
//
//  1. ONE ROW PER TAG, NOT PER IMAGE. `podman images` prints a row for every
//     name, so the newest image appears TWICE — once under its content tag and
//     once under :latest — and a per-row keep window silently spends two of its
//     slots on one image. Measured on the maintainer's host the moment C2
//     landed: three rows, two images, and `keep=2` selected the second image for
//     removal. Entries are therefore deduped by ID, which is what `keep` always
//     meant to count.
//  2. NO LIVENESS GATE AT ALL. While one :latest tag named every image the list
//     was one row long and `keep` never fired, so the absence never showed;
//     under per-config tags "everything past the newest 2" is "every config
//     except the most recently LOADED one". CreatedAt is the moment the archive
//     was streamed, not a build time (`created = "now"` in flake.nix), and C2's
//     whole point is that a revisited workspace does NOT reload — so a live jail
//     that has been running for a week carries a week-old timestamp and sorts
//     last. `rmi -f` then removes the containers using the image, killing a live
//     jail in another workspace mid-session. `protected`
//     (prune.ProtectedImageTags) vetoes those, reading the same sentinel ledger
//     as PruneOrphanImageRoots' guard #2, and `liveKnown` is its fail-safe.
//
// THE RETENTION RULE ITSELF IS STILL NOT C2's TO SET. `--keep-images` (default
// 2) is governed by minimal-disk-footprint.md OQ-DF3, still OPEN as of
// 2026-08-25; nothing here retunes it, and the veto is deliberately applied
// AFTER OldImagesToRemove so `keep` keeps meaning exactly what it means today.
// image.ReadLoadedPaths — which AutoLoadImage now updates on every launch, not
// only on a load — remains the ready-made input for the keep-by-USE rule OQ-DF3
// will eventually pick.
func PruneOldImages(rt string, keep int, protected map[string]struct{}, liveKnown bool, apply bool, run RunFunc) []string {
	if !liveKnown {
		// Guard #1, the fail-safe: the veto's whole evidence is the load sentinel,
		// and an unreadable one makes "nothing is live" and "I cannot tell" the
		// same observation. `rmi -f` takes a live jail's containers with it, so an
		// unproven set is not a licence to remove — decline entirely. Same polarity
		// as PruneOrphanImageRoots' guard #1.
		return []string{}
	}
	res := run([]string{rt, "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return []string{}
	}
	var images []ImageEntry
	seen := map[string]bool{}
	keepIDs := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
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
	toRemove := []string{}
	for _, id := range OldImagesToRemove(images, keep) {
		if keepIDs[id] {
			continue
		}
		toRemove = append(toRemove, id)
	}
	if apply {
		for _, id := range toRemove {
			run([]string{rt, "rmi", "-f", id}, rmiTimeout)
		}
	}
	return toRemove
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
