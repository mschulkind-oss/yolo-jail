package prune

// embeddedtrees.go reaps the on-disk copies of the packs compiled into a yolo binary
// (internal/packload's embeddedcache.go describes how they are written).
//
// TWO PLACES, TWO EVIDENCE RULES:
//
//   - THE CACHE BASE (PruneEmbeddedPackTrees) — ~/.local/share/yolo-jail/embedded-packs,
//     one immutable tree per build, named by a content hash. Every reader holds a SHARED
//     flock on <tree>/.lease for its whole life, so "is anyone still using this?" has a
//     kernel answer. This build's own tree is never deleted.
//   - TMPDIR (PruneLegacyEmbeddedTemp) — the per-process trees earlier builds leaked, one
//     per invocation of every command, plus this build's leased fallback trees. The
//     fallback carries a lease; the legacy trees carry NOTHING, so the only evidence for
//     them is the process table, and the rule is an ATTRIBUTION one (legacyAttributed).
//
// Tri-state throughout: "I could not ask" (a lease that could not be probed, a process
// table that could not be read) is a KEEP, never a reap. A per-entry "could not ask" is a
// note; only a whole scan that failed declines the section (OQ-LS2).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The age floors. embeddedReapFloor and embeddedUnleasedFloor are the SAME values
// internal/packload's own fallback sweep uses (its unexported embeddedReapFloor /
// embeddedUnleasedFloor): the two reapers act on one population and must agree about
// what "abandoned" means.
const (
	// embeddedReapFloor: no non-current entry younger than this is touched, whatever its
	// lease says — a directory gains its lease a moment after it is created.
	embeddedReapFloor = 10 * time.Minute
	// embeddedUnleasedFloor: an in-flight entry (.tmp-, .bad-, .reap-, a fallback) with no
	// lease file at all is reaped only past this. Its creator writes the lease right after
	// the directory, so one this old without a lease was never finished.
	embeddedUnleasedFloor = time.Hour
	// legacyEmbeddedFloor: a legacy TMPDIR tree is never reaped younger than this.
	legacyEmbeddedFloor = time.Hour
	// legacyAttributeBefore / legacyAttributeAfter bound the window of start times of a
	// process that could have CREATED a legacy tree written at init: [mtime-before,
	// mtime+after]. Asymmetric because the tree is written AFTER the exec (so the creator
	// starts at or just before the mtime); the slack absorbs clock granularity (/proc's
	// boot time is whole seconds) and skew.
	legacyAttributeBefore = 10 * time.Minute
	legacyAttributeAfter  = 2 * time.Minute
)

// The exact names this file may touch. /tmp also holds yolo-*.sock, .pid and .lock files
// that are not ours to reap, so nothing is matched by a bare prefix.
var (
	embeddedFinalName = regexp.MustCompile(`^[0-9a-f]{32}$`)
	// Class C — this build's leased per-process fallback.
	fallbackTempName = regexp.MustCompile(`^` + regexp.QuoteMeta(packload.EmbeddedFallbackPrefix) + `[0-9]+$`)
	// Class A — `yolo-embedded-<digits>`, materialized at PACKAGE INIT by every process of a
	// build from 5caffc5d (2026-07-27) on, never modified after.
	legacyInitTempName = regexp.MustCompile(`^` + regexp.QuoteMeta(packload.LegacyEmbeddedPrefix) + `[0-9]+$`)
	// Class B — `yolo-embedded-packs-<digits>` and `yolo-cli-packs-<digits>`, created LAZILY
	// (any time in the process's life) by builds between bbe84f1d (2026-07-27) and
	// afd48ffc (2026-09-03).
	legacyLazyTempName = regexp.MustCompile(`^yolo-(embedded|cli)-packs-[0-9]+$`)
)

// platformProcStarts is this OS's process-table reader, registered by procstart_linux.go
// or procstart_darwin.go at init. Every other OS keeps the default, "could not ask", so
// every legacy tree is kept there. A variable rather than a third build-tagged file
// because no linted GOOS would select that file (internal/capture's lint-gate pin).
var platformProcStarts = func() ([]ProcStart, bool) { return nil, false }

// readProcStarts is the default Options.ProcStarts.
func readProcStarts() ([]ProcStart, bool) { return platformProcStarts() }

// ProcStart is one live process of this euid, as the legacy rule needs it.
type ProcStart struct {
	PID int
	// Name is the executable's basename where the OS gives it, else the kernel's comm.
	Name string
	// NameTruncated: Name is a comm at the kernel's full comm length, so it may have been
	// cut and cannot be proven NOT to be a yolo binary or a go test binary.
	NameTruncated bool
	Start         time.Time
}

// yoloCandidate: a process that could have written a legacy tree — any yolo binary
// (yolo, yolo-entrypoint, yolo-jaild, …) or any go test binary.
func (p ProcStart) yoloCandidate() bool {
	return strings.HasPrefix(p.Name, "yolo") || p.testCandidate()
}

// testCandidate: a go test binary, which re-materialized packs whenever a test asked —
// not only at init — so the init-time attribution window does not bound it.
func (p ProcStart) testCandidate() bool {
	return strings.HasSuffix(p.Name, ".test") || p.NameTruncated
}

// EmbeddedEntry is one directory an embedded-tree sweep looked at.
type EmbeddedEntry struct {
	Name  string
	Path  string
	Bytes int64
	// Why is the short, groupable reason — the report counts kept entries by it.
	Why string
	// Detail is the entry's own specifics (a probe error, the pinning pid), or "".
	Detail string
	// Err is set on a removal that failed; the entry is then NOT removed.
	Err error
}

// EmbeddedReap is one sweep's outcome. Bytes counts what was (dry-run: would be) removed
// successfully; a failed removal is listed in Removed with Err set and adds nothing.
type EmbeddedReap struct {
	Removed []EmbeddedEntry
	Kept    []EmbeddedEntry
	Bytes   int64
	// Skipped counts entries passed over without a verdict — another user's directory, for
	// example, which on macOS is the sandbox account's and never this user's to judge.
	Skipped map[string]int
	// Declined is non-empty when a WHOLE scan failed (a directory that exists and could not
	// be listed). That is the OQ-LS2 decline; a per-entry "could not ask" is a Kept reason.
	Declined string
}

// RemovedCount is how many entries actually went.
func (r EmbeddedReap) RemovedCount() int {
	n := 0
	for _, e := range r.Removed {
		if e.Err == nil {
			n++
		}
	}
	return n
}

func (r *EmbeddedReap) keep(e EmbeddedEntry, why, detail string) {
	e.Why, e.Detail = why, detail
	r.Kept = append(r.Kept, e)
}

func (r *EmbeddedReap) skip(why string) {
	if r.Skipped == nil {
		r.Skipped = map[string]int{}
	}
	r.Skipped[why]++
}

// Kept reasons, spelled once so the report's grouping and the tests agree.
const (
	whyCurrent        = "this build's tree"
	whyCurrentUnknown = "this build's hash is unknown, so every tree is kept"
	whyHeld           = "in use by a running yolo"
	whyCouldNotAsk    = "could not ask its lease"
	whyNoLease        = "no lease, so nothing can prove it unused"
	whyYoung          = "too recent to judge"
	whyUnrecognized   = "not a name yolo writes"
	whyProcsUnknown   = "could not read the process table"
	whyLegacyPinned   = "a live yolo process could have created it"
	whyOtherUser      = "owned by another user"
)

// PruneEmbeddedPackTrees sweeps the cache base: every tree but this build's, each
// in-flight or quarantined leftover, each only once no process holds its lease.
//
//   - current (hashKnown): this build's tree, ALWAYS kept — nothing is probed.
//   - !hashKnown: every final tree is kept, since any of them might be this build's.
//   - every other entry must be embeddedReapFloor old, a real directory, owned by this euid.
//   - its lease (packload.ProbeLease, exclusive and non-blocking) decides: HELD or UNKNOWN
//     keeps it; ABSENT keeps a final tree (it cannot vouch either way) and reaps a
//     leftover past embeddedUnleasedFloor; FREE reaps it.
//
// A final tree is renamed to a .reap- name WHILE the exclusive lock is held, then unlocked
// and deleted: a process that blocked on its shared lock meanwhile finds the lease inode
// gone from the final name and repopulates, instead of adopting a tree mid-delete.
func PruneEmbeddedPackTrees(base, current string, hashKnown, apply bool, now time.Time) EmbeddedReap {
	var r EmbeddedReap
	if base == "" {
		return r
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.Declined = fmt.Sprintf("could not list %s: %v", base, err)
		}
		return r
	}
	for _, de := range entries {
		name := de.Name()
		p := filepath.Join(base, name)
		e := EmbeddedEntry{Name: name, Path: p}
		final := embeddedFinalName.MatchString(name)
		leftover := strings.HasPrefix(name, ".tmp-") || strings.HasPrefix(name, ".bad-") ||
			strings.HasPrefix(name, ".reap-")
		switch {
		case final && hashKnown && name == current:
			r.keep(e, whyCurrent, "")
			continue
		case final && !hashKnown:
			r.keep(e, whyCurrentUnknown, "")
			continue
		case !final && !leftover:
			r.keep(e, whyUnrecognized, "")
			continue
		}
		fi, ok := ownRealDir(p)
		if !ok {
			r.keep(e, whyUnrecognized, "not a directory this user owns")
			continue
		}
		age := now.Sub(fi.ModTime())
		if age < embeddedReapFloor {
			r.keep(e, whyYoung, "younger than "+embeddedReapFloor.String())
			continue
		}
		state, unlock, perr := packload.ProbeLease(p)
		switch state {
		case packload.LeaseHeld:
			unlock()
			r.keep(e, whyHeld, "")
			continue
		case packload.LeaseUnknown:
			unlock()
			r.keep(e, whyCouldNotAsk, errText(perr))
			continue
		case packload.LeaseAbsent:
			unlock()
			if final {
				r.keep(e, whyNoLease, "")
				continue
			}
			if age < embeddedUnleasedFloor {
				r.keep(e, whyYoung, "no lease, younger than "+embeddedUnleasedFloor.String())
				continue
			}
			e.Why = "unfinished leftover with no lease"
		case packload.LeaseFree:
			if final {
				e.Why = "another build's tree; no running yolo holds its lease"
			} else {
				e.Why = "leftover no running yolo holds"
			}
		}
		e.Bytes = dirSizeBytes(p)
		if apply {
			target := p
			if final && state == packload.LeaseFree {
				target = filepath.Join(base, fmt.Sprintf(".reap-%s-%d-%d", name, os.Getpid(), time.Now().UnixNano()))
				if err := os.Rename(p, target); err != nil {
					unlock()
					e.Err = err
					r.Removed = append(r.Removed, e)
					continue
				}
			}
			unlock()
			e.Err = os.RemoveAll(target)
		} else if state == packload.LeaseFree {
			unlock()
		}
		if e.Err == nil {
			r.Bytes += e.Bytes
		}
		r.Removed = append(r.Removed, e)
	}
	return r
}

// PruneLegacyEmbeddedTemp sweeps TMPDIR for embedded-pack trees: this build's leased
// fallbacks and the unleased trees earlier builds leaked. Only this euid's real
// directories with one of the exact names above are judged; another user's are skipped
// and counted.
//
//   - Class C (a leased fallback): the lease rule, as the cache base — embeddedReapFloor old
//     and FREE, or past embeddedUnleasedFloor with no lease file. Deleted directly: nothing
//     adopts a fallback by name, so there is no rename to make.
//   - Classes A and B (legacy, no lease): legacyEmbeddedFloor old, a READABLE process table,
//     and no live process that could have created the tree (legacyAttributed). An
//     unreadable table keeps every one.
//
// dirs are deduplicated by their resolved path, so TMPDIR=/tmp is scanned once.
func PruneLegacyEmbeddedTemp(dirs []string, procs []ProcStart, procsKnown, apply bool, now time.Time) EmbeddedReap {
	var r EmbeddedReap
	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(d)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				r.Declined = fmt.Sprintf("could not resolve %s: %v", d, err)
			}
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		entries, err := os.ReadDir(resolved)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				r.Declined = fmt.Sprintf("could not list %s: %v", resolved, err)
			}
			continue
		}
		for _, de := range entries {
			name := de.Name()
			fallback := fallbackTempName.MatchString(name)
			initTime := legacyInitTempName.MatchString(name)
			lazy := legacyLazyTempName.MatchString(name)
			if !fallback && !initTime && !lazy {
				continue
			}
			p := filepath.Join(resolved, name)
			fi, err := os.Lstat(p)
			if err != nil || !fi.IsDir() {
				continue
			}
			if !ownedByEuid(fi) {
				r.skip(whyOtherUser)
				continue
			}
			e := EmbeddedEntry{Name: name, Path: p}
			mtime := fi.ModTime()
			age := now.Sub(mtime)
			var unlock func()
			if fallback {
				if age < embeddedReapFloor {
					r.keep(e, whyYoung, "younger than "+embeddedReapFloor.String())
					continue
				}
				var state packload.LeaseState
				var perr error
				state, unlock, perr = packload.ProbeLease(p)
				switch {
				case state == packload.LeaseHeld:
					unlock()
					r.keep(e, whyHeld, "")
					continue
				case state == packload.LeaseUnknown:
					unlock()
					r.keep(e, whyCouldNotAsk, errText(perr))
					continue
				case state == packload.LeaseAbsent && age < embeddedUnleasedFloor:
					unlock()
					r.keep(e, whyYoung, "no lease, younger than "+embeddedUnleasedFloor.String())
					continue
				case state == packload.LeaseAbsent:
					e.Why = "fallback tree never finished (no lease)"
				default:
					e.Why = "fallback tree whose process is gone (lease free)"
				}
			} else {
				unlock = func() {}
				if age < legacyEmbeddedFloor {
					r.keep(e, whyYoung, "younger than "+legacyEmbeddedFloor.String())
					continue
				}
				if !procsKnown {
					r.keep(e, whyProcsUnknown, "")
					continue
				}
				if pin, ok := legacyAttributed(initTime, mtime, procs); ok {
					r.keep(e, whyLegacyPinned, fmt.Sprintf("pid %d (%s)", pin.PID, pin.Name))
					continue
				}
				if initTime {
					e.Why = "legacy tree; no live yolo process started when it was written"
				} else {
					e.Why = "legacy tree; no live yolo process is old enough to have written it"
				}
			}
			e.Bytes = dirSizeBytes(p)
			if apply {
				e.Err = os.RemoveAll(p)
			}
			unlock()
			if e.Err == nil {
				r.Bytes += e.Bytes
			}
			r.Removed = append(r.Removed, e)
		}
	}
	return r
}

// legacyAttributed reports the first live process that could have created a legacy tree
// whose mtime is mtime, and so pins it.
//
// WHY AN ATTRIBUTION WINDOW. A legacy tree carries no lease, and a pure age cutoff is
// unsafe: an old build's launcher lives for a whole jail session and its daemons for days,
// and either may read its Pack.Root late. But "keep while ANY older yolo process lives"
// reaps nothing, because one long-lived daemon started before every tree. What separates
// the two is WHEN each tree was written:
//
//   - an init-time tree (class A) was written once, milliseconds after its creator's exec,
//     and never touched again — so its creator STARTED within [mtime-10m, mtime+2m];
//   - a go test binary materialized whenever a test asked, so any live one started by
//     mtime+2m pins a class A tree too;
//   - a lazily created tree (class B) may have been written at any point in its creator's
//     life — so any live candidate started by mtime+2m pins it.
//
// A candidate is a yolo binary or a go test binary (ProcStart.yoloCandidate); a truncated
// name counts as both, since it cannot be ruled out.
func legacyAttributed(initTime bool, mtime time.Time, procs []ProcStart) (ProcStart, bool) {
	latest := mtime.Add(legacyAttributeAfter)
	earliest := mtime.Add(-legacyAttributeBefore)
	for _, p := range procs {
		if !p.yoloCandidate() || p.Start.After(latest) {
			continue
		}
		if !initTime || p.testCandidate() || !p.Start.Before(earliest) {
			return p, true
		}
	}
	return ProcStart{}, false
}

// ownRealDir lstats p and answers it only when it is a real directory owned by this euid.
func ownRealDir(p string) (fs.FileInfo, bool) {
	fi, err := os.Lstat(p)
	if err != nil || !fi.IsDir() || !ownedByEuid(fi) {
		return nil, false
	}
	return fi, true
}

func ownedByEuid(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// keptSummary groups kept entries by reason, largest group first, for the report's one
// "kept" line — the kept set can be hundreds of legacy dirs, and each is still on disk to
// be looked at, unlike what was removed, which is listed by name.
func keptSummary(kept []EmbeddedEntry, skipped map[string]int) string {
	counts := map[string]int{}
	for _, e := range kept {
		counts[e.Why]++
	}
	for why, n := range skipped {
		counts[why] += n
	}
	type kv struct {
		why string
		n   int
	}
	var groups []kv
	for why, n := range counts {
		groups = append(groups, kv{why, n})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].n != groups[j].n {
			return groups[i].n > groups[j].n
		}
		return groups[i].why < groups[j].why
	})
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%d %s", g.n, g.why))
	}
	return strings.Join(parts, "; ")
}
