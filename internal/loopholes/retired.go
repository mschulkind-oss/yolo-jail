package loopholes

// retired.go is the whole of what survives the hand-placed loopholes directory:
// a way to see that one is still populated, and one message telling its owner what
// to write instead.
//
// WHY THE CHANNEL WENT (OQ-LP10, docs/design/loophole-packaging.md §8, ruled yes).
// `~/.local/share/yolo-jail/loopholes/` was the only source that could start a HOST
// DAEMON with no selection step whatsoever: loadFromDir walked it and every manifest
// it found was discovered, enabled, spawned and wired into the argv. Nothing in the
// user's config mentioned it, nothing in the per-launch disclosure named it, and
// `yolo pack`'s approval machinery never saw it. That is the direct contradiction of
// AGENTS.md's "nothing is active by default", in the one place where being wrong
// costs a process running on the human's machine.
//
// The same sentence that justified it — "a hand-placed directory carries the user's
// own authority" — justifies a `file://` pack verbatim, and a pack carries the SAME
// on-disk module dir through the SAME loader. So the channel was redundant as well as
// unselected. The conventional local pack (paths.LocalPackDir(), implicitly selected
// when it exists) keeps the drop-a-directory-in ergonomics: the migration is moving a
// directory and writing four lines of pack.json, not porting a manifest.
//
// WHAT IT DOES NOT CLAIM TO FIX, stated because the design doc is explicit about it:
// the local pack is implicit too, so a loophole in it also activates with no config
// line. What improves is VISIBILITY, not gating — a pack-shipped loophole emits host-
// access claims and reaches notePackHostAccess, where this directory printed nothing.
//
// WHY A LOUD NOTICE RATHER THAN A SILENT DROP. Whatever sat here was running a daemon
// on the host until the upgrade that removed this channel. A capability that vanishes
// with no message is the failure mode the rest of this package keeps paying for (see
// loadFromDir's warn-and-continue, and pack-capabilities.md §5: anything that turns
// something off must name who did it and why). The notice therefore names the
// directory, every module still in it, and the exact commands to migrate.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// RetiredUserLoopholes returns the module directories still sitting in the retired
// hand-placed loopholes dir, sorted. Empty when the directory is absent, empty, or
// holds nothing that looks like a loophole module.
//
// "Looks like a loophole module" is a directory holding a manifest.jsonc, and it is
// deliberately the WEAKEST test available: this reports what a user has to migrate,
// so it must catch a manifest the loader would have rejected too. Decoding here would
// hide exactly the module whose owner most needs to be told it is inert.
func RetiredUserLoopholes() []string {
	dir := RetiredUserLoopholesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || !fi.IsDir() {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, name, "manifest.jsonc")); err != nil || fi.IsDir() {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RetiredUserLoopholeNotice returns the migration message for a still-populated
// retired directory, or "" when there is nothing to migrate.
//
// One function, so `yolo check`'s structured report and the discovery-time stderr
// warning say the SAME thing. Two spellings of a migration instruction is how a user
// ends up following the one that is a version behind.
func RetiredUserLoopholeNotice() string {
	stranded := RetiredUserLoopholes()
	if len(stranded) == 0 {
		return ""
	}
	dir := RetiredUserLoopholesDir()
	local := paths.LocalPackDir()
	name := stranded[0]
	var b strings.Builder
	b.WriteString("the hand-placed loopholes directory is RETIRED and is no longer read:\n")
	b.WriteString("  " + dir + "\n")
	b.WriteString("Still there and now INERT (no daemon, no mounts, no jail_env): " +
		strings.Join(stranded, ", ") + "\n")
	b.WriteString("Move each module into the conventional local pack, which is selected " +
		"implicitly — no `packs` config line needed:\n")
	b.WriteString("  mkdir -p " + filepath.Join(local, "loopholes") + "\n")
	b.WriteString("  mv " + filepath.Join(dir, "<name>") + " " +
		filepath.Join(local, "loopholes", "<name>") + "\n")
	b.WriteString("then declare each one in " + filepath.Join(local, "pack.json") + ":\n")
	b.WriteString(`  {"name": "local", "contributes": [{"kind": "loophole", "from": "loopholes/` +
		name + `"}]}` + "\n")
	// The subset is the one thing that can still fail AFTER a correct move, so it is
	// named here rather than left for the user to hit as a load error: a pack's
	// loophole is held to the pack-shipped subset (loophole-packaging.md §3.1) and the
	// hand-placed channel was not. A manifest using jail_env, an absolute or $VAR bind
	// host, a writable bind or publishes:"endpoint" is refused with the reason printed
	// — visible, but only if you know to expect it.
	b.WriteString("A pack's loophole is held to the pack-shipped subset (no jail_env, no " +
		"absolute or writable bind hosts, no publishes:\"endpoint\"), so a manifest using " +
		"those is refused at load with the reason — see docs/guides/loopholes.md.")
	return b.String()
}

// retiredNoticeOnce keeps the discovery-time warning to ONE line per process.
//
// Discover is called several times on a launch (the argv, the spawn, the briefing,
// the config resolver — §5.1's census), and a migration instruction repeated five
// times reads as a malfunction rather than as advice. `yolo check` renders the same
// text through its own reporter, which is the surface a user is meant to reach for.
var retiredNoticeOnce sync.Once

// warnRetiredUserLoopholes emits the migration notice once, if there is one.
func warnRetiredUserLoopholes() {
	retiredNoticeOnce.Do(func() {
		if notice := RetiredUserLoopholeNotice(); notice != "" {
			warnf("%s", notice)
		}
	})
}

// resetRetiredNotice re-arms the once. Tests only: the guard is process-wide by
// design, which makes isolation between tests mandatory.
func resetRetiredNotice() { retiredNoticeOnce = sync.Once{} }
