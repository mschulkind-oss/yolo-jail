// Package hostpath is the LAUNCH PATH's one resolver (docs/design/host-launch-environment.md §2.2,
// §3, HE-D5): the PATH yolo's checks read at the host notch, and the lookup every one of them asks.
//
// # What the launch PATH is
//
// The maintainer's ruling HE-DIR1: at `yolo host`, yolo's checks read the PATH yolo was started
// with, because *"it's just not feasible to otherwise know these things"*, and the user-scope
// `host_path` list adds folders to it. So the launch PATH is, in order:
//
//  1. the PATH yolo was started with (the AMBIENT PATH), its entries in their order;
//  2. each `host_path` folder not already on it, in written order (HE-D3).
//
// Duplicates keep their first occurrence and empty entries are dropped. A process started with no
// PATH at all, or an empty one, searches `host_path`'s folders alone (HE-D4): supplying a default
// would be a guess. Nothing else joins it — no per-OS baseline, no folder a pack declares, no mise
// folder of yolo's choosing.
//
// # Who reads it
//
// Every PATH check yolo makes at the host: the launch gate's dependency survey, `yolo host apply`'s
// pre-flight and its install re-probe, the package-manager guess behind a remedy, `yolo check-deps`,
// `yolo check`'s host launch section, and the target lookup of a `yolo host -- <cmd>` no floor entry
// covers. One function computing it from the same two inputs is what stops two checks disagreeing
// about where a program is. The one exception is a program a selected pack delivers, which the
// floor answers for (internal/cli), and the wrapper-precedence checks, which ask about the caller's
// own shell and read the ambient PATH alone.
//
// # The lookup skips yolo's own folders
//
// LookPath skips ManagedDirs, the generated tree the host launch wrappers live in, as the exec's
// lookup always has: `<wrap dir>/rg` is `exec yolo host -- rg`, so a check that found it would read
// a wrapper as the program it wraps (HE-D5) — a false *present* whose launch would exit 127.
//
// # When a lookup misses
//
// MissLine is the MISS LINE (§4.2, HE-D2): one line naming the program, the pack that requires it,
// the whole PATH searched with `host_path`'s part marked, and `host_path` as the fix — naming a
// hint folder only when one really holds the program. A hint folder never resolves anything.
package hostpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Launch is one resolution of the launch PATH.
type Launch struct {
	// caller is the ambient PATH's entries, deduplicated with the first kept — or, for a child with
	// no PATH, the stand-in folders WithStandIn put in its place.
	caller []string
	// started is whether yolo was started with a PATH that names any folder.
	started bool
	// standIn is whether caller is WithStandIn's stand-in rather than a PATH yolo was handed.
	standIn bool
	// added is each `host_path` folder not already in caller, in written order (HE-D3).
	added []string
	// declared is `host_path` as read, expanded, in written order: what `yolo check` reports.
	declared []string
	// skip is the folders every lookup skips (ManagedDirs).
	skip []string
	// home is the home a folder under which is written ~/…, and the hint folders' base.
	home string
	// jail is whether this is a jail's passthrough: the process PATH unchanged, no `host_path`,
	// and no miss line, which names a host key.
	jail bool
}

// Resolve is THE resolver: the launch PATH for a process whose PATH is ambient. The host CLI passes
// os.Getenv("PATH") and `yolo check` its Getenv seam's; the rest is read here, so no caller can
// hand it a different `host_path` or skip list.
//
// In a jail it returns the process PATH unchanged (§3, "In-jail"): the jail's PATH is already
// composed by its boot, `host_path` is host-only, and an in-jail check asks about the jail.
func Resolve(ambient string) *Launch {
	if config.InJail() {
		l := New(ambient, nil, ManagedDirs(), paths.Home())
		l.jail = true
		return l
	}
	return New(ambient, config.HostPathFolders(), ManagedDirs(), paths.Home())
}

// ManagedDirs are the folders a host PATH lookup skips: yolo's whole generated tree, so the wrap
// dir is covered, and any generated folder added under it later.
func ManagedDirs() []string { return []string{paths.GeneratedBinDir()} }

// New builds the launch PATH from its inputs. Resolve is the one production caller; a test calls it
// with a fake PATH and home.
func New(ambient string, declared, skip []string, home string) *Launch {
	l := &Launch{declared: append([]string(nil), declared...), skip: skip, home: home}
	l.caller = uniqueEntries(filepath.SplitList(ambient), nil)
	l.started = len(l.caller) > 0
	l.added = uniqueEntries(declared, l.caller)
	return l
}

// WithStandIn is the launch PATH a CHILD searches: the same PATH when yolo was started with one, and
// otherwise dirs in place of the missing PATH, ahead of `host_path`'s folders. That is the floor
// design's HP-D12: an agent started with no PATH at all still needs `git` and `sh`, so its PATH
// gets the system folders the floor's installer runs with. Only the child's PATH and the exec's
// lookup read it; the checks keep HE-D4's `host_path` alone.
func (l *Launch) WithStandIn(dirs []string) *Launch {
	if l.started {
		return l
	}
	c := *l
	c.caller = uniqueEntries(dirs, nil)
	c.standIn = len(c.caller) > 0
	c.added = uniqueEntries(l.declared, c.caller)
	return &c
}

// uniqueEntries is dirs without empty entries and without any already in seen or earlier in dirs.
func uniqueEntries(dirs, seen []string) []string {
	have := map[string]bool{}
	for _, d := range seen {
		have[filepath.Clean(d)] = true
	}
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if c := filepath.Clean(d); !have[c] {
			have[c] = true
			out = append(out, d)
		}
	}
	return out
}

// Entries is the launch PATH's folders, in order: the PATH yolo was started with, then `host_path`'s
// folders not already on it.
func (l *Launch) Entries() []string {
	return append(append([]string(nil), l.caller...), l.added...)
}

// Value is the launch PATH as a PATH value: what an install the dependency gate runs is handed
// (HE-D6), and what the agent's PATH starts with.
func (l *Launch) Value() string { return strings.Join(l.Entries(), string(os.PathListSeparator)) }

// Started reports whether yolo was started with a PATH that names any folder.
func (l *Launch) Started() bool { return l.started }

// Caller is the part of the launch PATH yolo was started with.
func (l *Launch) Caller() []string { return append([]string(nil), l.caller...) }

// Added is the part `host_path` added: each of its folders not already on the PATH yolo was started
// with.
func (l *Launch) Added() []string { return append([]string(nil), l.added...) }

// Declared is `host_path` as written, expanded, whether or not a folder was already on the PATH.
func (l *Launch) Declared() []string { return append([]string(nil), l.declared...) }

// InJail reports whether this is a jail's passthrough.
func (l *Launch) InJail() bool { return l.jail }

// LookPath resolves bin on the launch PATH the way a shell does — the first folder holding an
// executable of that name wins — skipping yolo's own folders. A bin carrying a separator is a path
// and is checked as given. The lookup is hostwrap.LookPathSkipping, the exec's own, so a check and
// the exec agree about what counts as runnable.
func (l *Launch) LookPath(bin string) (string, error) {
	return l.LookPathSkipping(bin)
}

// LookPathSkipping is LookPath skipping more folders besides yolo's own: the exec skips the floor's
// bin/, which only a deselected entry could answer from (HP-D11).
func (l *Launch) LookPathSkipping(bin string, more ...string) (string, error) {
	return hostwrap.LookPathSkipping(l.Value(), bin, append(append([]string(nil), l.skip...), more...))
}

// Skipped is the launch PATH's folders the lookup skips, as written.
func (l *Launch) Skipped(more ...string) []string {
	var roots []string
	for _, r := range append(append([]string(nil), l.skip...), more...) {
		if r != "" {
			roots = append(roots, filepath.Clean(r))
		}
	}
	var out []string
	for _, d := range l.Entries() {
		c := filepath.Clean(d)
		for _, r := range roots {
			if c == r || strings.HasPrefix(c, r+string(filepath.Separator)) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// Source names where one launch PATH folder came from: "the PATH yolo was started with", "host_path",
// or, for a child with no PATH, the stand-in. "" for a folder not on it.
func (l *Launch) Source(dir string) string {
	c := filepath.Clean(dir)
	for _, d := range l.caller {
		if filepath.Clean(d) == c {
			if l.standIn {
				return standInPhrase
			}
			return "the PATH yolo was started with"
		}
	}
	for _, d := range l.added {
		if filepath.Clean(d) == c {
			return "host_path"
		}
	}
	return ""
}

// standInPhrase names WithStandIn's folders in a line a person reads.
const standInPhrase = "the system folders yolo uses when it is started with no PATH"

// Searched is the whole PATH searched, in words: its value with each part named, never truncated,
// since it is the answer to "where did it look".
func (l *Launch) Searched() string {
	sep := string(os.PathListSeparator)
	added := l.tildeAll(l.added)
	switch {
	case len(l.caller) > 0:
		from := "the PATH yolo was started with"
		if l.standIn {
			from = standInPhrase
		}
		s := strings.Join(l.caller, sep) + ", " + from
		if len(added) > 0 {
			s += ", then host_path's " + strings.Join(added, sep)
		}
		return s
	case len(added) > 0:
		return strings.Join(added, sep) + ", host_path's folders alone: yolo was started with no PATH"
	}
	return "which is empty: yolo was started with no PATH, and host_path names no folder"
}

// Miss is what a miss line is about.
type Miss struct {
	// Bin is the program that was not found.
	Bin string
	// Requires are the packs that list Bin under `requires`; Programs, the packs that declare it as
	// a program. Either may be empty, and both are for a program no pack names.
	Requires, Programs []string
	// Launch is whether the line is a launch's — the gate's or the exec's — whose PATH is "this
	// launch's"; a verb that only checks (`yolo host apply`, `check-deps`) says the PATH yolo searched.
	Launch bool
	// Skipping is more folders the lookup skipped besides yolo's own (the exec's floor bin/), so the
	// line can name every skipped folder that is on the PATH it prints.
	Skipping []string
}

// MissLine is the miss line for m, "" in a jail, where the key it names does not exist:
//
//	rg (required by the guardrails pack) is not on this launch's PATH, /usr/bin:/bin, the PATH yolo
//	was started with. If rg is installed, add its folder to "host_path" in
//	~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.
//
// The caller adds its own prefix ("yolo host: "). The hint clause appears only when a hint folder
// holds an executable of that name (Hint), and it changes nothing but this text.
func (l *Launch) MissLine(m Miss) string {
	if l.jail || m.Bin == "" {
		return ""
	}
	where := "the PATH yolo searched"
	if m.Launch {
		where = "this launch's PATH"
	}
	var b strings.Builder
	b.WriteString(m.Bin)
	if who := requiredBy(m.Requires, m.Programs); who != "" {
		b.WriteString(" (" + who + ")")
	}
	fmt.Fprintf(&b, " is not on %s, %s", where, l.Searched())
	if skipped := l.Skipped(m.Skipping...); len(skipped) > 0 {
		fmt.Fprintf(&b, " (skipping yolo's own %s)", strings.Join(l.tildeAll(skipped), ", "))
	}
	fmt.Fprintf(&b, `. If %s is installed, add its folder to "host_path" in %s`, m.Bin,
		l.tilde(paths.UserConfigPath()))
	if hint := l.Hint(m.Bin); hint != "" {
		fmt.Fprintf(&b, "; %s has one.", hint)
	} else {
		b.WriteString(".")
	}
	return b.String()
}

// Hint is the first hint location (hostfloor.HintLocations, the compiled list §4.2 coins) that is
// not on the launch PATH and holds an executable named bin, written ~/… — or "". A HINT, NEVER A
// VERDICT: a bounded look at a fixed handful of folders, which changes the text of a miss and
// nothing else. No check counts a program found here, and nothing runs one.
func (l *Launch) Hint(bin string) string {
	if bin == "" || strings.ContainsRune(bin, filepath.Separator) {
		return ""
	}
	on := map[string]bool{}
	for _, d := range l.Entries() {
		on[filepath.Clean(d)] = true
	}
	for _, d := range hostfloor.HintLocations(l.home) {
		if on[filepath.Clean(d)] {
			continue
		}
		if st, err := os.Stat(filepath.Join(d, bin)); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return l.tilde(d)
		}
	}
	return ""
}

// requiredBy is the parenthesis after the program's name: "required by the guardrails pack", or,
// for a program only a pack's program contribution names, "a program of the claude pack".
func requiredBy(requires, programs []string) string {
	switch {
	case len(requires) > 0:
		return "required by the " + packList(requires)
	case len(programs) > 0:
		return "a program of the " + packList(programs)
	}
	return ""
}

// packList names packs the way a sentence does: "a pack", "a and b packs", "a, b and c packs".
func packList(names []string) string {
	var uniq []string
	seen := map[string]bool{}
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			uniq = append(uniq, n)
		}
	}
	switch len(uniq) {
	case 0:
		return "pack"
	case 1:
		return uniq[0] + " pack"
	}
	return strings.Join(uniq[:len(uniq)-1], ", ") + " and " + uniq[len(uniq)-1] + " packs"
}

// tilde writes a path under the home as ~/….
func (l *Launch) tilde(p string) string {
	if l.home != "" && l.home != "/" && strings.HasPrefix(p, l.home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(p, l.home)
	}
	return p
}

func (l *Launch) tildeAll(ps []string) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = l.tilde(p)
	}
	return out
}
