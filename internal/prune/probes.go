package prune

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// ProbeResult is the outcome of a container-runtime subprocess probe. Ran is
// false when the binary was absent, could not be started, or the call timed out
// (Python's FileNotFoundError / OSError / subprocess.TimeoutExpired branch — all
// degrade to the empty/None result). RC carries the exit status when Ran is
// true; Python treats any non-zero RC as an empty/None degrade, exactly like a
// spawn failure.
type ProbeResult struct {
	Stdout string
	RC     int
	Ran    bool
}

// RunFunc is the injectable exec seam (the internal/pscmd Deps pattern applied
// to the pure engine): it runs argv with a per-call timeout and returns the
// captured stdout. The real implementation lives in internal/prunecmd; tests
// stub it with canned output keyed by argv. A stub that models "runtime absent"
// returns Ran=false; "runtime present, listing failed" returns Ran=true, RC!=0.
type RunFunc func(argv []string, timeout time.Duration) ProbeResult

// probe timeouts, frozen from src/prune.py (the subprocess.run timeout= args).
const (
	psTimeout      = 10 * time.Second
	inspectTimeout = 5 * time.Second
	rmTimeout      = 10 * time.Second
	rmiTimeout     = 15 * time.Second
)

// isLiveState reports whether a podman State string denotes a live jail
// (running/paused/restarting, case-insensitive). This is the single liveness
// predicate shared by _prune_stopped_containers (skip live → remove the rest)
// and _find_referenced_build_roots (keep live → collect their binds).
func isLiveState(state string) bool {
	switch strings.ToLower(state) {
	case "running", "paused", "restarting":
		return true
	}
	return false
}

// resolvePath mirrors pathlib.Path.resolve() closely enough for our inputs
// (existing container bind sources): resolve symlinks to an absolute path, and
// on failure fall back to an absolute-cleaned path (Python's resolve() never
// raises for strict=False). Used to dedup workspace paths and to key the
// referenced-build-root set — both sides of a comparison run through this, so
// equality is preserved.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// pySplitMax str.split(maxsplit=maxsplit): leading/trailing
// whitespace is ignored, fields are separated by runs of ASCII whitespace, and
// after `maxsplit` cuts the remainder (internal whitespace preserved, trailing
// stripped) is the final field. Used to split the `images` line into
// (id, repo:tag, createdAt) so the CreatedAt sort key keeps its internal spaces
// exactly as Python's split(maxsplit=2) would.
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
			// Remainder field: leading whitespace already skipped above; Python
			// keeps everything to the end verbatim (trailing whitespace PRESERVED).
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
// `name`, or ("", false) on any inspect failure / absence.
// body of _inspect_workspace_mount (dest=/workspace) and _inspect_build_root_mount
// (dest=/opt/yolo-jail): run `inspect --format {{json .Mounts}}`, decode the
// mounts array via the isinstance-guarded walk (a non-array top-level or a
// non-object element is skipped, never crashes), and return the first matching
// non-empty Source.
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

// PruneOldImages lists yolo-jail images, keeps the newest `keep`, and returns
// the image IDs removed (or slated for removal in dry-run).
// `images --format {{.ID}} {{.Repository}}:{{.Tag}}
// {{.CreatedAt}} yolo-jail` → parse (id, createdAt) lines (>=3 fields, split
// maxsplit=2) → the EXISTING OldImagesToRemove lexical CreatedAt sort → `rmi -f
// <id>` each when apply. A missing/failed runtime yields an empty list.
func PruneOldImages(rt string, keep int, apply bool, run RunFunc) []string {
	res := run([]string{rt, "images", "--format", "{{.ID}} {{.Repository}}:{{.Tag}} {{.CreatedAt}}", "yolo-jail"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return []string{}
	}
	var images []ImageEntry
	for _, line := range strings.Split(res.Stdout, "\n") {
		parts := pySplitMax(strings.TrimSpace(line), 2)
		if len(parts) < 3 {
			continue
		}
		images = append(images, ImageEntry{ID: parts[0], Created: parts[2]})
	}
	toRemove := OldImagesToRemove(images, keep)
	if apply {
		for _, id := range toRemove {
			run([]string{rt, "rmi", "-f", id}, rmiTimeout)
		}
	}
	return toRemove
}

// FindReferencedBuildRoots returns the tri-state set of resolved host paths a
// LIVE yolo-* container binds into /opt/yolo-jail. Preserves the None-vs-empty
// polarity: a missing/
// failed `ps` yields Known=false (liveness unknown → the sweep declines), never
// an empty set that would read as "nothing live". Note the inverted selection
// vs PruneStoppedContainers: here LIVE containers are KEPT (their binds
// collected) so the sweep never unlinks an in-use inode.
func FindReferencedBuildRoots(rt string, run RunFunc) ReferencedSet {
	res := run([]string{rt, "ps", "-a", "--format", "{{.Names}} {{.State}}"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return ReferencedSet{Known: false}
	}
	paths := map[string]struct{}{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) < 2 {
			continue
		}
		name, state := parts[0], parts[1]
		if !strings.HasPrefix(name, "yolo-") {
			continue
		}
		if !isLiveState(state) {
			continue
		}
		src, ok := inspectMountSource(rt, name, "/opt/yolo-jail", run)
		if !ok {
			continue
		}
		paths[resolvePath(src)] = struct{}{}
	}
	return ReferencedSet{Known: true, Paths: paths}
}

// relayShortHash is the 8-char sha1 hash that keys a jail's broker-relay PID
// file and sockets dir.
// sync with the host-services-sockets-dir hash so ReapRelayOrphans can match a
// pid file to a live container name.
func relayShortHash(cname string) string {
	sum := sha1.Sum([]byte(cname))
	return hex.EncodeToString(sum[:])[:8]
}

// ReapRelayOrphans sweeps per-jail broker-relay PID files under `base` whose jail
// is no longer live, returning the PID-file paths reaped (or, in dry-run, that
// WOULD be).
// - liveKnown==false (liveness unenumerable) → reap NOTHING (same fail-safe
// polarity as the build-root sweep — unknown must never read as "nothing
// live");
// - a pid file whose 8-char hash matches a live container is kept;
// - a pid file younger than olderThanSeconds (mtime grace floor for a jail
// mid-startup) is kept;
// - on apply, the relay is killed (via the injected relayKill seam — the
// signal/pgrep machinery is the caller's concern), then the .lock file and
// the yolo-host-services-<hash> sockets dir are removed.
//
// The reaped list is sorted by path (Python sorts base.glob(...)), so the
// displayed order is deterministic.
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
		_ = os.RemoveAll(filepath.Join(base, "yolo-host-services-"+shortHash))
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
