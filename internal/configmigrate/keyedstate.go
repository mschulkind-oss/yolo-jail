package configmigrate

// keyedstate.go moves a renamed pack's per-key files to its new key (config-migration.md item 9,
// RC-D47): the build logs and the pending background-advance state, each named by
// paths.KeySlug of a key "<pack>/<bin>[#upstream]". A slug ends in a hash of its key, so a move
// recomputes the slug of the new key rather than renaming text, and a file is taken as a renamed
// key's only when its whole name is that key's slug, hash included.
//
// What is not moved, and why: the local copies of built programs (host and macos-user tree
// copies) are made again from the build by a local copy; and a `demands/` directory is the lock,
// leases and results of a demand a running launch is waiting on, keyed by a hash that names no key,
// so it is left to the launch that made it.
//
// Nothing here ever replaces a file: a move whose target exists leaves both where they are, and a
// background-advance key some process holds a lock on is left whole, to start over under the new
// key. Every move is idempotent, so the journal's Resume runs it again.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// slugRenamer finds the renamed key a slug is the slug of: old is the key under its old pack
// name, to the key under the new one.
type slugRenamer struct {
	olds  []string // longest first, so a qualified old name is tried before a shorter one
	names map[string]string
}

func newSlugRenamer(names map[string]string) slugRenamer {
	olds := make([]string, 0, len(names))
	for o := range names {
		olds = append(olds, o)
	}
	sort.Slice(olds, func(i, j int) bool { return len(olds[i]) > len(olds[j]) })
	return slugRenamer{olds: olds, names: names}
}

// rename reports the old and new key whose slug is slug. A key's bin never holds a "/", so the
// text between the old pack's slug prefix and the hash is the bin verbatim, and the hash proves it.
func (s slugRenamer) rename(slug string) (old, to string, ok bool) {
	const hashLen = 8
	if len(slug) < hashLen+2 || slug[len(slug)-hashLen-1] != '-' {
		return "", "", false
	}
	body := slug[:len(slug)-hashLen-1]
	for _, o := range s.olds {
		rest, found := strings.CutPrefix(body, strings.ReplaceAll(o, "/", "--")+"--")
		if !found || rest == "" || strings.Contains(rest, "/") {
			continue
		}
		key := o + "/" + rest
		if paths.KeySlug(key) == slug {
			return key, s.names[o] + "/" + rest, true
		}
	}
	return "", "", false
}

// moveKeyedState moves every build log and background-advance record of a renamed key, and
// returns one line per file moved or left.
func moveKeyedState(workspace string, names map[string]string) []string {
	if len(names) == 0 {
		return nil
	}
	s := newSlugRenamer(names)
	var out []string
	if r, err := os.OpenRoot(paths.LogsDir()); err == nil {
		out = append(out, moveBuildLogs(r, "logs/", s, []string{"tree-build-", "build-"})...)
		r.Close()
	}
	if workspace != "" {
		// The workspace's .yolo is jail-writable: opened as a root that refuses a linked directory,
		// and moved by link and remove beneath it, so no planted link reaches a host file.
		// Its logs directory holds this yolo's build logs, and the top of .yolo an older yolo's.
		if r, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(workspace)); err == nil {
			out = append(out, moveBuildLogs(r, ".yolo/", s, []string{"build-"})...)
			if lr, err := paths.OpenStateSubdirRoot(r, paths.WorkspaceLogsDirName, paths.WorkspaceLogsDir(workspace)); err == nil {
				out = append(out, moveBuildLogs(lr, ".yolo/logs/", s, []string{"build-"})...)
				lr.Close()
			}
			r.Close()
		}
	}
	out = append(out, moveBackgroundState(s)...)
	return out
}

// moveBuildLogs moves each "<prefix><slug>.log" (and a tree-build log's ".log.1", its rotated
// half) of a renamed key under r to the new key's slug. A log whose target exists is left: both
// are logs of the same pack, and the newer one is what a line names.
func moveBuildLogs(r *os.Root, shown string, s slugRenamer, prefixes []string) []string {
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		for _, prefix := range prefixes {
			rest, ok := strings.CutPrefix(name, prefix)
			if !ok {
				continue
			}
			var slug, suffix string
			switch {
			case strings.HasSuffix(rest, ".log"):
				slug, suffix = strings.TrimSuffix(rest, ".log"), ".log"
			case strings.HasSuffix(rest, ".log.1"):
				slug, suffix = strings.TrimSuffix(rest, ".log.1"), ".log.1"
			default:
				continue
			}
			_, to, ok := s.rename(slug)
			if !ok {
				continue
			}
			target := prefix + paths.KeySlug(to) + suffix
			if line := linkMove(r, name, target, "build log "+shown); line != "" {
				out = append(out, line)
			}
			break
		}
	}
	return out
}

// linkMove moves name to target beneath r by a hard link and a remove: the link fails rather than
// replace a target that exists, so nothing is ever clobbered.
func linkMove(r *os.Root, name, target, what string) string {
	if err := r.Link(name, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return what + name + " left: " + target + " is already there"
		}
		return what + name + " left: " + err.Error()
	}
	if err := r.Remove(name); err != nil {
		return what + name + " → " + target + " (the old name could not be removed: " + err.Error() + ")"
	}
	return what + name + " → " + target
}

// moveBackgroundState moves the outcome record, the reported outcome and every claimed outcome of
// each renamed key in BackgroundAdvanceDir, under the same per-key locks the background advance
// takes (its key lock and its artifact lock), for both the old key and the new, tried and never
// waited for. A key whose lock is held is left whole: its advance is running or being reported,
// and its state starts over under the new key.
func moveBackgroundState(s slugRenamer) []string {
	dir := paths.BackgroundAdvanceDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type keyPair struct{ old, to string }
	seen := map[string]bool{}
	var keys []keyPair
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, paths.BackgroundClaimPrefix) {
			continue // a claim is moved with its key, which its artifact lock names
		}
		var slug string
		for _, suffix := range []string{paths.BackgroundArtifactLockSuffix, paths.BackgroundKeyLockSuffix,
			paths.BackgroundOutcomeSuffix, paths.BackgroundSaidSuffix} {
			if t, ok := strings.CutSuffix(name, suffix); ok {
				slug = t
				break
			}
		}
		old, to, ok := s.rename(slug)
		if !ok || seen[old] {
			continue
		}
		seen[old] = true
		keys = append(keys, keyPair{old, to})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].old < keys[j].old })
	var out []string
	for _, k := range keys {
		out = append(out, moveBackgroundKey(dir, k.old, k.to)...)
	}
	return out
}

// moveBackgroundKey moves one key's background-advance records, or leaves them all when a lock on
// either key is held.
func moveBackgroundKey(dir, old, to string) []string {
	if !hasBackgroundRecords(dir, old) {
		return nil // only its lock files: nothing to move
	}
	var held []*pidlock.Lock
	defer func() {
		for _, l := range held {
			l.Release()
		}
	}()
	// The key lock before the artifact lock, as withBackgroundKey takes them.
	for _, path := range []string{
		paths.BackgroundAdvanceKeyFile(old, paths.BackgroundKeyLockSuffix),
		paths.BackgroundAdvanceKeyFile(to, paths.BackgroundKeyLockSuffix),
		paths.BackgroundAdvanceKeyFile(old, paths.BackgroundArtifactLockSuffix),
		paths.BackgroundAdvanceKeyFile(to, paths.BackgroundArtifactLockSuffix),
	} {
		l, err := pidlock.Acquire(path, pidlock.NoWait, nil)
		if errors.Is(err, pidlock.ErrHeld) {
			return []string{"background update state of " + old + " left: a background advance holds it, so it starts over as " + to}
		}
		if err != nil {
			return []string{"background update state of " + old + " left: " + err.Error() + "; it starts over as " + to}
		}
		held = append(held, l)
	}
	oldSlug, toSlug := paths.KeySlug(old), paths.KeySlug(to)
	var moves [][2]string
	for _, suffix := range []string{paths.BackgroundOutcomeSuffix, paths.BackgroundSaidSuffix} {
		moves = append(moves, [2]string{oldSlug + suffix, toSlug + suffix})
	}
	claims, _ := filepath.Glob(filepath.Join(dir, paths.BackgroundClaimPrefix+oldSlug+"-*"))
	for _, c := range claims {
		unique := strings.TrimPrefix(filepath.Base(c), paths.BackgroundClaimPrefix+oldSlug+"-")
		moves = append(moves, [2]string{filepath.Base(c), paths.BackgroundClaimPrefix + toSlug + "-" + unique})
	}
	var out []string
	for _, m := range moves {
		if line := moveBackgroundRecord(dir, m[0], m[1], old, to); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func hasBackgroundRecords(dir, key string) bool {
	slug := paths.KeySlug(key)
	for _, suffix := range []string{paths.BackgroundOutcomeSuffix, paths.BackgroundSaidSuffix} {
		if _, err := os.Lstat(filepath.Join(dir, slug+suffix)); err == nil {
			return true
		}
	}
	claims, _ := filepath.Glob(filepath.Join(dir, paths.BackgroundClaimPrefix+slug+"-*"))
	return len(claims) > 0
}

// moveBackgroundRecord writes the record at name, respelled to the new key, as target, then
// removes name. A record that does not read as one of old's is left as it is, and so is one whose
// target exists.
func moveBackgroundRecord(dir, name, target, old, to string) string {
	src := filepath.Join(dir, name)
	data, err := os.ReadFile(src)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	what := "background update state " + name
	if err != nil {
		return what + " left: " + err.Error()
	}
	respelled, ok := respellBackgroundRecord(data, old, to)
	if !ok {
		return what + " left: it is not a record of " + old
	}
	tmp, err := os.CreateTemp(dir, ".migrate-"+target+"-*")
	if err != nil {
		return what + " left: " + err.Error()
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(respelled); err != nil {
		tmp.Close()
		return what + " left: " + err.Error()
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return what + " left: " + err.Error()
	}
	if err := tmp.Close(); err != nil {
		return what + " left: " + err.Error()
	}
	if err := os.Link(tmp.Name(), filepath.Join(dir, target)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return what + " left: " + target + " is already there"
		}
		return what + " left: " + err.Error()
	}
	if err := os.Remove(src); err != nil {
		return what + " → " + target + " (the old record could not be removed: " + err.Error() + ")"
	}
	return what + " → " + target
}

// respellBackgroundRecord gives a background outcome record the new key: its key, the owner of its
// patch failure and of its resolution, and the slug in its patch failure's log path. Every other
// field is kept as written (numbers too, which a generation holds at full width).
func respellBackgroundRecord(data []byte, old, to string) ([]byte, bool) {
	var rec map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&rec); err != nil {
		return nil, false
	}
	if k, _ := rec["key"].(string); k != old {
		return nil, false
	}
	rec["key"] = to
	oldSeries, toSeries := strings.TrimSuffix(old, packload.UpstreamKeySuffix), strings.TrimSuffix(to, packload.UpstreamKeySuffix)
	if label, ok := rec["label"].(string); ok {
		rec["label"] = strings.ReplaceAll(label, oldSeries, toSeries)
	}
	for _, field := range []string{"patch_failure", "resolution"} {
		sub, ok := rec[field].(map[string]any)
		if !ok {
			continue
		}
		if owner, _ := sub["owner"].(string); owner == old {
			sub["owner"] = to
		}
		if log, ok := sub["log"].(string); ok {
			sub["log"] = strings.ReplaceAll(log, paths.KeySlug(old), paths.KeySlug(to))
		}
	}
	out, err := json.Marshal(rec)
	if err != nil {
		return nil, false
	}
	return out, true
}
