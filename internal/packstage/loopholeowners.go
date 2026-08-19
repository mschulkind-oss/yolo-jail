package packstage

// loopholeowners.go is the pack→loophole-state OWNERSHIP RECORD, and the archive that
// retires a departed loophole's state (docs/design/loophole-packaging.md §4.5).
//
// # Why a record has to exist at all
//
// A loophole's writable state dir is keyed by loophole NAME ONLY
// (loopholes.StateDirFor → <state>/<name>). That is not an oversight: it is exactly the
// property §8 relies on to make a pack-shipped intercepting loophole possible, because
// name-keyed means the dir lives OUTSIDE the staged pack tree and therefore survives
// restaging — a CA regenerated on every launch would break every long-lived TLS client in
// the jail.
//
// The same property is the gap. Name-keyed means UNATTRIBUTED: nothing on disk records
// which pack owned the dir holding a private key, so when the user drops that pack from
// `packs` there is no evidence that the key is now an orphan. §4.5's requirement and §8's
// benefit are the same fact seen from two sides, and the only way to have both is to write
// the attribution down separately. That is this file.
//
// # It is deliberately WEAK evidence, exactly like the `files` record it copies
//
// internal/hostskills' manifest is the model (dest→pack, JSON, missing file == empty). It
// can go stale in ordinary use — the user hand-edits a loophole, the state dir is pruned,
// two machines share one config, a launch dies between the stage and the save. So it
// authorizes ARCHIVING (a reversible move) and never deletion: being wrong must cost the
// user one `mv` back, not the only copy of a CA they still have jails trusting.
//
// # What this does NOT do, on purpose
//
// It does not record spawned PIDs so a later prune can reap them. §4.5 REJECTED that: it
// builds a process supervisor for a threat the finding itself calls marginal (once
// arbitrary host execution has happened once, persistence is available through ~/.bashrc
// or cron), and a stale PID file is its own class of bug. The process-GROUP kill on
// teardown (run.killServiceGroup, Setsid at spawn) is the accepted half and is already
// shipped.
//
// So, stated plainly rather than left for someone to discover: SELECTION CONTROLS
// ACTIVATION, NOT REVOCATION. Deselecting a pack stops the next launch from starting its
// daemon and retires the state it left behind. It does not stop a daemon that already ran;
// a process that has executed once is outside yolo's ability to revoke, and no packaging
// design changes that.
//
// # No internal imports
//
// Every path crosses as a parameter (the record file, the state root, the log dir, the
// stamp), the same way hostskills.ArchiveRoot does. The package stays dependency-free so
// the launch path, `yolo prune`, and the tests can each point it somewhere different — and
// so nothing here can grow an opinion about where storage lives.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RetiredLoopholeStateDir is the subdirectory of the STATE ROOT that holds retired
// generations: <state>/.retired/<stamp>/<loophole>/.
//
// Under the state root rather than in the host-render archive, and §4.5 is explicit about
// why: `yolo prune`'s archive sweep (internal/prune/hostarchive.go) walks a DIFFERENT tree,
// the one `apply --host` writes, and this state never passes through that command at all
// (§3.4 refuses the loophole kind there). A user who has just lost a CA looks under the
// state dir, so that is where the copy goes.
//
// DOT-PREFIXED so it can never be mistaken for a loophole. Discovery skips dot-children
// (loopholes.loadModuleDirs) and a manifest's `name` must equal its directory basename, so no
// loophole can be called ".retired" — the collision is unrepresentable rather than merely
// avoided.
const RetiredLoopholeStateDir = ".retired"

// ArchiveStampFormat is the generation-directory name format, and it MUST stay the format
// internal/prune parses (looksLikeArchiveStamp). Prune deletes only a directory whose name
// it can explain as a stamp; a generation written in any other format would be reported as
// "none" and grow forever. Pinned by TestRetiredStampIsPrunable.
const ArchiveStampFormat = "20060102-150405"

// ArchiveStamp renders a generation name. Takes the time rather than reading the clock so
// the caller owns determinism (the same rule hostskills.Archive follows).
func ArchiveStamp(t time.Time) string { return t.Format(ArchiveStampFormat) }

// LoopholeOwners records which pack shipped each per-loophole state dir.
type LoopholeOwners struct {
	// Owners maps a loophole NAME to the pack that contributed it. The loophole name is
	// the key because the STATE DIR is keyed that way (see the file doc) — recording the
	// pack-relative module path instead would answer a question nobody asks and would
	// still leave the state dir unattributed.
	Owners map[string]string `json:"owners"`
}

// LoadLoopholeOwners reads the record at path. A missing file is an EMPTY record, not an
// error: the first launch on a machine has nothing recorded, and that is indistinguishable
// from a pruned state dir — both mean "yolo can prove nothing", which the caller already
// treats as "retire nothing".
//
// A CORRUPT record returns an empty record AND the error, so the caller can say so and
// still retire nothing. Fail-closed in the direction that keeps data: a record yolo cannot
// read must not authorize moving a private key.
func LoadLoopholeOwners(path string) (*LoopholeOwners, error) {
	rec := &LoopholeOwners{Owners: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rec, nil
		}
		return rec, err
	}
	if err := json.Unmarshal(data, rec); err != nil {
		return &LoopholeOwners{Owners: map[string]string{}}, err
	}
	if rec.Owners == nil {
		rec.Owners = map[string]string{}
	}
	return rec, nil
}

// Save writes the record temp-file-then-rename, so an interrupted write leaves the previous
// record intact rather than a truncated one. A truncated record reads as "prove nothing":
// safe, but it forgets which pack owned a state dir, which is the one fact this file exists
// to keep.
func (r *LoopholeOwners) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if r.Owners == nil {
		r.Owners = map[string]string{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Departed returns the recorded loopholes whose owning pack is NO LONGER CONFIGURED,
// sorted by loophole name.
//
// The test is against the set of pack names the CONFIG NAMES — never the set that RESOLVED
// this launch — and that difference is the whole reason this takes its own set instead of
// reusing the loaded packs. A fetched pack whose remote is unreachable resolves to nothing,
// so to a resolved-set comparison it looks dropped; archiving its CA on every offline
// launch would be a wrong answer with a real cost. pruneDroppedPackOutput takes the same
// set for the same reason.
//
// A pack that is still configured but has STOPPED DECLARING a loophole is deliberately NOT
// departed here. The evidence looks similar and the failure mode is not: a pack whose tree
// is momentarily unreadable presents identically to one that dropped the declaration, and
// the cost of being wrong is a moved private key. Retirement therefore keys on the one
// signal the user typed themselves — the pack leaving `packs` — and a stale record entry
// for a still-configured pack is left alone (harmless: it names a pack that is still there).
func (r *LoopholeOwners) Departed(configured map[string]bool) []Departed {
	var out []Departed
	for name, pack := range r.Owners {
		if configured[pack] {
			continue
		}
		out = append(out, Departed{Loophole: name, Pack: pack})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loophole < out[j].Loophole })
	return out
}

// Departed is one loophole whose owning pack left the config.
type Departed struct {
	// Loophole is the loophole name — which is also its state-dir name.
	Loophole string
	// Pack is the pack the record names. It may name a pack that no longer exists
	// anywhere on the machine, which is precisely why the record has to be
	// self-contained rather than derived from the pack tree.
	Pack string
}

// RetireRequest is one retirement: what to move, and where.
type RetireRequest struct {
	// Loophole is the loophole name (the state-dir basename and the log-file infix).
	Loophole string
	// Pack is the departed pack, recorded into the generation for attribution.
	Pack string
	// StateRoot is the directory holding per-loophole state dirs (<state> in
	// <state>/<name>). RetiredLoopholeStateDir is created under it.
	StateRoot string
	// LogDir is where host-service-<name>.log lives ("" to skip the log).
	//
	// The log rides along because §4.5 names it beside the state dir, and for the same
	// attribution reason: it is the departed daemon's own output, so leaving it behind
	// keeps a file named for a loophole nothing on the machine can explain.
	LogDir string
	// Stamp is the generation name (see ArchiveStamp).
	Stamp string
}

// RetireLoopholeState moves a departed loophole's state dir (and its host-service log) into
// <StateRoot>/.retired/<Stamp>/<Loophole>/, returning the generation directory and the
// pack-relative names moved into it.
//
// ARCHIVES, never deletes — see the file doc. Returns moved=nil when there was nothing on
// disk to move, which is the ordinary case for a loophole that never ran: the caller then
// forgets the record entry without printing a line about a directory that never existed.
//
// A `.pack` marker file records the owning pack inside the generation. Attribution has to
// live in the archive itself, because the record that named the pack is about to forget it,
// and "whose key is this?" is the first question a user asks of an archived directory.
func RetireLoopholeState(req RetireRequest) (generation string, moved []string, err error) {
	gen := filepath.Join(req.StateRoot, RetiredLoopholeStateDir, req.Stamp, req.Loophole)
	stateDir := filepath.Join(req.StateRoot, req.Loophole)
	var sources []struct{ src, name string }
	if exists(stateDir) {
		sources = append(sources, struct{ src, name string }{stateDir, "state"})
	}
	if req.LogDir != "" {
		log := filepath.Join(req.LogDir, "host-service-"+req.Loophole+".log")
		if exists(log) {
			sources = append(sources, struct{ src, name string }{log, filepath.Base(log)})
		}
	}
	if len(sources) == 0 {
		return "", nil, nil
	}
	if err := os.MkdirAll(gen, 0o755); err != nil {
		return "", nil, err
	}
	// The marker first: if a move fails halfway, what survives must still say who owned it.
	if req.Pack != "" {
		_ = os.WriteFile(filepath.Join(gen, ".pack"), []byte(req.Pack+"\n"), 0o644)
	}
	for _, s := range sources {
		if err := movePath(s.src, filepath.Join(gen, s.name)); err != nil {
			return gen, moved, fmt.Errorf("retiring %s: %w", s.src, err)
		}
		moved = append(moved, s.name)
	}
	return gen, moved, nil
}

// movePath renames src to dst, falling back to copy-then-remove across filesystems (the
// state root and the log dir need not share one). Copy FIRST and remove only once the copy
// is intact — the reverse order loses the file when the copy fails. Same contract
// hostskills.Archive uses, reimplemented here rather than imported because this package
// takes no internal dependencies (see the file doc).
func movePath(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyAny(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// copyAny copies a file or a whole tree, preserving the exec bit (a state dir may hold a
// hook script, and an archived copy the user restores must still run).
func copyAny(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyAny(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	// A symlink is copied as a symlink: dereferencing one inside a state dir would turn a
	// link into a second copy of whatever it pointed at, host secrets included.
	if fi.Mode()&os.ModeSymlink != 0 {
		target, rerr := os.Readlink(src)
		if rerr != nil {
			return rerr
		}
		return os.Symlink(target, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Chmod explicitly: O_CREATE's mode is masked by umask, so a 0600 key would otherwise
	// be archived at whatever the umask allowed.
	return os.Chmod(dst, fi.Mode().Perm())
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
