package prune

// scratchvolumes.go owns the podman SCRATCH VOLUMES: the four disk-backed volumes a
// podman jail's read-only rootfs gets for /tmp, /var/tmp, /var/lib/containers and
// /var/cache/containers under `ephemeral_storage: "volume"` (the default). The launcher
// names them (run.ScratchMountArgs), the launcher's detached remover deletes them after
// the jail exits, and the reaper here removes whatever that remover never reached.
//
// WHY THEY ARE NAMED, AND WHY THIS FILE EXISTS AT ALL. They were ANONYMOUS volumes
// (`-v /tmp`) under `podman run --rm`, and --rm makes the attached podman client delete a
// container's anonymous volumes itself, synchronously, before it exits. That deletion is
// one unlinkat per file the jail ever wrote there — worktrees, build caches, the nested
// podman store — and the user's terminal is held for all of it: measured on the
// maintainer's host 2026-09-28, 32 s of `unlinkat` after a 59 h session, the "Window A"
// linger docs/reference/perf-logging.md spent a month instrumenting. A NAMED volume is
// not deleted by --rm, so the client exits as soon as the container is removed and the
// deletion moves off the critical path (docs/reference/perf-logging.md#the-linger-was-the-scratch-volumes).
//
// THE NAME IS THE ONLY OWNERSHIP EVIDENCE. `-v name:/tmp` creates the volume with no
// labels, so a scratch volume is recognised by its spelling alone:
//
//	<cname>.scratch.<launch id>.<slot>     e.g. yolo-app-1a2b3c4d.scratch.0123456789abcdef.tmp
//
// A container name is `yolo-` + [a-z0-9-] (runtime.FromResolved), so the dots cannot occur
// inside it and the parse is exact. The launch id is fresh per launch, never per
// workspace: a relaunch whose predecessor's volumes are still being deleted must not
// attach to them, and podman silently REUSES a named volume that already exists.
//
// THE LIVENESS RULE IS PODMAN'S OWN `dangling` FILTER — "no container references this
// volume" — plus an age floor, and it is tri-state like every reaper here: a listing that
// did not run, failed or did not parse is "could not ask", and reaps nothing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ScratchSlot is one scratch mount: the name segment its volume carries and the
// directory in the jail it backs.
type ScratchSlot struct {
	Name string
	Dest string
}

// ScratchSlots is the ordered set of disk-backed scratch mounts. The launcher's argv and
// this file's parse both read it, so a slot cannot be mounted under a name the reaper
// does not recognise.
var ScratchSlots = []ScratchSlot{
	{Name: "tmp", Dest: "/tmp"},
	{Name: "var-tmp", Dest: "/var/tmp"},
	{Name: "var-lib-containers", Dest: "/var/lib/containers"},
	{Name: "var-cache-containers", Dest: "/var/cache/containers"},
}

// scratchInfix separates the container name from the launch id and slot.
const scratchInfix = ".scratch."

// ScratchVolumeGrace is the reaper's age floor. It covers the one window in which a
// scratch volume is dangling while its jail is alive: `podman run` creates the named
// volumes and then the container that references them, inside one process, so the
// window is milliseconds. A minute is margin, not a measurement.
const ScratchVolumeGrace = time.Minute

// Timeouts for the removal steps. Emptying is the slow half — the whole point of doing
// it outside podman is that it can take tens of seconds — so its bound is generous.
const (
	scratchListTimeout  = 20 * time.Second
	scratchEmptyTimeout = 30 * time.Minute
	scratchRmTimeout    = 5 * time.Minute
)

// ScratchVolumeName spells one scratch volume's name.
func ScratchVolumeName(cname, launchID, slot string) string {
	return cname + scratchInfix + launchID + "." + slot
}

var scratchVolumeRe = regexp.MustCompile(
	`^(yolo-[a-z0-9-]+)\.scratch\.([0-9a-f]{16})\.(tmp|var-tmp|var-lib-containers|var-cache-containers)$`)

// ParseScratchVolumeName reports whether name is a scratch volume yolo made, and whose.
func ParseScratchVolumeName(name string) (cname, launchID, slot string, ok bool) {
	m := scratchVolumeRe.FindStringSubmatch(name)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

// ScratchVolume is one scratch volume as the runtime reports it.
type ScratchVolume struct {
	Name       string
	Cname      string
	Mountpoint string
	Created    time.Time
	// Dangling is podman's own answer: no container, running or stopped, references it.
	Dangling bool
}

// podmanVolume is the subset of `podman volume ls --format json` read here. Measured on
// podman 5.8.6: CreatedAt is RFC 3339 with nanoseconds and the zone offset.
type podmanVolume struct {
	Name       string `json:"Name"`
	Mountpoint string `json:"Mountpoint"`
	CreatedAt  string `json:"CreatedAt"`
}

// ListScratchVolumes returns every scratch volume the runtime holds, with podman's
// dangling answer for each, and whether the runtime ANSWERED. known=false is "could not
// ask" and callers must reap nothing on it.
//
// Two queries, because the JSON listing carries no in-use field and the dangling filter
// carries nothing else. Either failing is "could not ask": a volume missing from the
// dangling answer is IN USE, so a failed second query cannot be read as "none dangling"
// without also reading every volume as in use, and a partial answer is not an answer.
//
// Podman only: Apple Container's scratch dirs are always tmpfs (assembleRunCmd puts the
// volume mode on the podman branch alone), so there is nothing to list there, and no
// listing of its volumes has been measured.
func ListScratchVolumes(rt string, run RunFunc) ([]ScratchVolume, bool) {
	if rt != "podman" {
		return nil, false
	}
	all := run([]string{rt, "volume", "ls", "--format", "json"}, scratchListTimeout)
	if !all.Ran || all.RC != 0 {
		return nil, false
	}
	var vols []podmanVolume
	if err := json.Unmarshal([]byte(strings.TrimSpace(all.Stdout)), &vols); err != nil {
		return nil, false
	}
	dang := run([]string{rt, "volume", "ls", "--filter", "dangling=true", "--format", "{{.Name}}"}, scratchListTimeout)
	if !dang.Ran || dang.RC != 0 {
		return nil, false
	}
	dangling := map[string]bool{}
	for _, line := range strings.Split(dang.Stdout, "\n") {
		if n := strings.TrimSpace(line); n != "" {
			dangling[n] = true
		}
	}
	var out []ScratchVolume
	for _, v := range vols {
		cname, _, _, ok := ParseScratchVolumeName(v.Name)
		if !ok {
			continue
		}
		created, _ := time.Parse(time.RFC3339Nano, v.CreatedAt)
		out = append(out, ScratchVolume{
			Name:       v.Name,
			Cname:      cname,
			Mountpoint: v.Mountpoint,
			Created:    created,
			Dangling:   dangling[v.Name],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, true
}

// ReapableScratchVolumes keeps the dangling volumes past the grace floor. A volume whose
// creation time did not parse is kept back: the floor is the only guard on the creation
// window, and an unknown age cannot clear it.
func ReapableScratchVolumes(vols []ScratchVolume, now time.Time, grace time.Duration) []ScratchVolume {
	var out []ScratchVolume
	for _, v := range vols {
		if !v.Dangling || v.Created.IsZero() {
			continue
		}
		if now.Sub(v.Created) < grace {
			continue
		}
		out = append(out, v)
	}
	return out
}

// scratchMountpointOK is the guard in front of the recursive delete: the path podman
// reported must be exactly <root>/volumes/<name>/_data, absolute and clean, and a real
// directory. Anything else is not emptied — the `volume rm` that follows still removes
// the volume, only slower.
func scratchMountpointOK(name, mp string) bool {
	if mp == "" || !filepath.IsAbs(mp) || filepath.Clean(mp) != mp {
		return false
	}
	if filepath.Base(mp) != "_data" || filepath.Base(filepath.Dir(mp)) != name ||
		filepath.Base(filepath.Dir(filepath.Dir(mp))) != "volumes" {
		return false
	}
	return true
}

// geteuid is a seam so the rootful/rootless choice below is testable.
var geteuid = os.Geteuid

// emptyScratchVolume deletes a scratch volume's CONTENTS without going through podman,
// and reports whether it did.
//
// WHY NOT JUST `podman volume rm`. Measured in a nested jail 2026-09-28 (podman 5.8.6,
// rootful, btrfs): while `podman volume rm` deletes a 200k-file volume, every `podman run`
// that mounts ANY volume on the machine blocks on a libpod lock until it finishes (5.2 s
// for a run of /bin/true that takes 0.3 s otherwise), while a run with no volume does not.
// Every jail mounts four. So a plain background `volume rm` would move the wait from
// this quit into the next launch — this workspace's, or any other's. Deleting the files
// first, outside podman, blocked nothing (0.56 s for the same run), and the `volume rm`
// of the emptied volume then took 0.08 s.
//
// Removing `_data` itself is safe: `podman volume rm` of a volume whose `_data` is gone
// succeeds (measured, same podman).
//
// As whom: a rootless store holds files owned by the user namespace's subordinate ids
// (the nested podman store under /var/lib/containers is full of them), so the host user
// cannot delete them directly and `podman unshare` is the tool. A rootful podman refuses
// `unshare` outright ("please use unshare with rootless") and its caller is root, who can.
// On a podman REMOTE client (macOS's podman machine) the mountpoint is a path inside the
// VM and `unshare` is not available, so this fails and the `volume rm` does the whole job.
func emptyScratchVolume(rt string, v ScratchVolume, run RunFunc) bool {
	if !scratchMountpointOK(v.Name, v.Mountpoint) {
		return false
	}
	argv := []string{rt, "unshare", "rm", "-rf", "--", v.Mountpoint}
	if geteuid() == 0 {
		argv = []string{"rm", "-rf", "--", v.Mountpoint}
	}
	res := run(argv, scratchEmptyTimeout)
	return res.Ran && res.RC == 0
}

// RemoveScratchVolume empties one scratch volume outside podman, then removes it.
//
// Never `volume rm --force`: --force also removes any container using the volume, and
// the caller's evidence that none does is a listing that may be seconds old. Without it
// podman refuses an in-use volume, which is the safe answer to a race this cannot see.
// A volume already gone is success.
func RemoveScratchVolume(rt string, v ScratchVolume, run RunFunc) bool {
	emptyScratchVolume(rt, v, run)
	res := run([]string{rt, "volume", "rm", v.Name}, scratchRmTimeout)
	if res.Ran && res.RC == 0 {
		return true
	}
	// Gone already (another remover got there first) is the outcome that was wanted.
	after := run([]string{rt, "volume", "exists", v.Name}, scratchListTimeout)
	return after.Ran && after.RC == 1
}

// PruneScratchVolumes is `yolo prune`'s section: the reapable scratch volumes, removed
// on apply. known=false means the runtime could not be asked and nothing was touched.
// removed lists what was (or, dry-run, would be) removed; failed what apply could not.
func PruneScratchVolumes(rt string, apply bool, now time.Time, run RunFunc) (removed, failed []string, known bool) {
	vols, known := ListScratchVolumes(rt, run)
	if !known {
		return nil, nil, false
	}
	for _, v := range ReapableScratchVolumes(vols, now, ScratchVolumeGrace) {
		if !apply {
			removed = append(removed, v.Name)
			continue
		}
		if RemoveScratchVolume(rt, v, run) {
			removed = append(removed, v.Name)
		} else {
			failed = append(failed, v.Name)
		}
	}
	return removed, failed, true
}
