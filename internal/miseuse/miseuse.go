// Package miseuse is the MISE USE RECORD — a term coined for the OQ-DF4 build of
// docs/design/minimal-disk-footprint.md: the files in which every jail on a machine says which
// installed versions of the shared mise tool store it uses, so that the host can tell which
// versions NO jail has used for 30 days.
//
// # Why a record at all
//
// The store is one tree for every workspace on the machine (/mise in every jail at every
// nesting depth, paths.GlobalMise() on the host), but mise's own notion of "in use" — the
// configs it tracks in ~/.local/state/mise/tracked-configs — lives in each workspace's own
// home. So `mise prune` in any one jail judges from that workspace alone and deletes versions
// other workspaces still use (measured 2026-10-01: it named 24 of the 31 installed versions in
// one jail). The record is how the per-workspace answers are joined: each jail writes its own
// into the store, and the host reads every one.
//
// # Where it lives, and why there
//
// <store>/.yolo-use/, inside the store it describes, because the store is the one directory
// every jail on the machine writes, whatever its depth: a nested jail's workspace and state dir
// are inside its outer jail, out of the host's sight, but its /mise is the host's store. A
// record anywhere else would miss exactly the jails a host cannot list.
//
// # What a record says
//
// One file per jail, under a random name the jail's main process picks once and keeps for its
// life: the workspace, the time, and every install directory the workspace still names — mise's
// own `mise ls --installed` minus `mise ls --prunable`, joined with `mise ls --current`, run
// offline so "latest" means the latest installed version, which is the one the shims run. Or,
// when mise could not answer, why not (Record.Unknown), which the host reads as "this jail used
// something I cannot name" and declines on.
//
// # Trust
//
// A record is written by a jail, and that is safe because a record can only PROTECT. The host
// removes only version directories it lists itself, beneath the store, and never a path a record
// names; a forged record could at most keep versions alive. A jail can already delete the whole
// store, which every jail mounts read-write, so nothing here hands one a capability it lacks.
// What a jail must NOT be able to do is turn ANOTHER jail's writer against that jail's own
// files, which is why every read, write and removal here is beneath an os.Root at the store and
// refuses a link at the record directory: a link a jail plants there resolves in the next jail's
// filesystem, and the writer expires old files.
package miseuse

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// DirName is the record directory beneath the store root.
	DirName = ".yolo-use"
	// SinceName is the file, in DirName, that says since when the HOST's launches have run jails
	// that record their use: the first host launch to bind the store creates it (MarkSince), and
	// nothing replaces it. The host judges nothing until it is Window old, because before then a
	// version some jail used without recording it — every jail a yolo older than this record
	// started — would read as unused.
	//
	// THE HOST WRITES IT, NEVER A JAIL. A nested jail, or a development jail running a newer
	// tree than the host's own yolo, writes records into the same store, and if its first record
	// started the clock, the clock would run while the host's own launches still recorded
	// nothing — exactly the jails the wait exists for.
	SinceName = "since"
	// Window is how long a version may go unused by every jail before it is reclaimable, and how
	// long a record counts as a use. The ruling's number (OQ-DF4, 2026-10-05).
	Window = 30 * 24 * time.Hour
	// Refresh is how often a running jail rewrites its record, so a jail that runs for longer
	// than Window keeps protecting what it uses.
	Refresh = 24 * time.Hour
	// expireAfter is the age past which a writer deletes a record or a stray temporary. A week
	// past the Window, so nothing a reader could still count is ever expired.
	expireAfter = Window + 7*24*time.Hour
	// maxRecordBytes bounds one read: a record is a list of install directories, a few KiB.
	maxRecordBytes = 1 << 20
	// formatVersion is Record.Format's current value.
	formatVersion = 1
)

// Record is one jail's use of the store.
type Record struct {
	Format int `json:"format"`
	// Workspace is the launching side's path of the jail's workspace (YOLO_HOST_DIR): a host path
	// for a host launch, the outer jail's path for a nested one. The host maps it to a container
	// name to tell whether the jail is running.
	Workspace string `json:"workspace"`
	// Recorded is when the jail wrote this record.
	Recorded time.Time `json:"recorded"`
	// Installs is every install directory the workspace names, each "<tool dir>/<version>"
	// beneath the store's installs/, sorted.
	Installs []string `json:"installs,omitempty"`
	// Unknown, when set, is why the jail could not say what it uses. A reader declines while
	// such a record is in force: the jail used something nobody can name.
	Unknown string `json:"unknown,omitempty"`
}

// writerName is a name NewName returns.
var writerName = regexp.MustCompile(`^[0-9a-f]{16}$`)

// recordName is a record's file name: the writer's random name, and nothing else is read.
var recordName = regexp.MustCompile(`^[0-9a-f]{16}\.json$`)

// tempName is a writer's temporary, which a killed writer can leave behind.
var tempName = regexp.MustCompile(`^\.[0-9a-f]{16}\.json\.[0-9]+$`)

// NewName returns a fresh record name for one jail's life.
func NewName() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on a supported platform; a fixed name only costs this jail
		// sharing a file with another that hit the same impossibility.
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// lsEntry is the part of one `mise ls --json` entry this reads.
type lsEntry struct {
	InstallPath string `json:"install_path"`
	Installed   bool   `json:"installed"`
}

// installsOf parses `mise ls --json` output — an object of tool name to entries — into the
// install directories it names, beneath store's installs/.
func installsOf(store string, raw []byte) (map[string]bool, error) {
	var byTool map[string][]lsEntry
	if err := json.Unmarshal(raw, &byTool); err != nil {
		return nil, fmt.Errorf("mise ls printed something that is not its JSON: %w", err)
	}
	out := map[string]bool{}
	for _, entries := range byTool {
		for _, e := range entries {
			if !e.Installed {
				continue
			}
			if rel := InstallRel(store, e.InstallPath); rel != "" {
				out[rel] = true
			}
		}
	}
	return out, nil
}

// InstallRel maps an install path beneath store/installs to "<tool dir>/<version>", or "" when
// it is not exactly one version directory beneath it.
func InstallRel(store, p string) string {
	prefix := path.Clean(store) + "/installs/"
	clean := path.Clean(p)
	if !strings.HasPrefix(clean, prefix) {
		return ""
	}
	rel := strings.TrimPrefix(clean, prefix)
	parts := strings.Split(rel, "/")
	if len(parts) != 2 || !validSegment(parts[0]) || !validSegment(parts[1]) {
		return ""
	}
	return rel
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.HasPrefix(s, ".")
}

// Needed is the set of install directories a workspace uses, from three `mise ls --json`
// outputs taken in it: everything installed that mise would not prune, joined with what its
// current configs resolve to. The join is belt and braces — a current config is a tracked one —
// so that a quirk in mise's prune logic can only ever keep a version, never lose one.
func Needed(store string, installed, prunable, current []byte) ([]string, error) {
	inst, err := installsOf(store, installed)
	if err != nil {
		return nil, err
	}
	prun, err := installsOf(store, prunable)
	if err != nil {
		return nil, err
	}
	cur, err := installsOf(store, current)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for rel := range inst {
		if !prun[rel] {
			keep[rel] = true
		}
	}
	for rel := range cur {
		keep[rel] = true
	}
	out := make([]string, 0, len(keep))
	for rel := range keep {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// openDir opens the record directory beneath store as a root of its own, creating it when
// create is set. A link at it is refused, never followed: see the package comment.
func openDir(store string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(store)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	fi, err := root.Lstat(DirName)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := root.Mkdir(DirName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		fi, err = root.Lstat(DirName)
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s in %s is not a directory", DirName, store)
	}
	return root.OpenRoot(DirName)
}

// Write replaces the record called name (NewName's) beneath store, and expires records and
// temporaries past expireAfter. It never writes the since marker: see SinceName.
func Write(store, name string, rec Record) error {
	if !writerName.MatchString(name) {
		return fmt.Errorf("record name %q is not a writer's name", name)
	}
	dir, err := openDir(store, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	rec.Format = formatVersion
	if rec.Recorded.IsZero() {
		rec.Recorded = time.Now()
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	final := name + ".json"
	tmp := fmt.Sprintf(".%s.%d", final, rec.Recorded.UnixNano())
	f, err := dir.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = dir.Remove(tmp)
		return errors.Join(werr, cerr)
	}
	if err := dir.Rename(tmp, final); err != nil {
		_ = dir.Remove(tmp)
		return err
	}
	// By the wall clock, not the record's time: what ages a file is how long it has been on disk.
	expire(dir, time.Now())
	return nil
}

// MarkSince creates the since marker beneath store if it does not exist yet, and leaves an
// existing one alone. Only a HOST launch calls it, for the store it binds (SinceName says why).
// An error leaves the marker missing, which only keeps the host judging nothing: the safe
// direction, and the next launch tries again.
func MarkSince(store string, now time.Time) error {
	dir, err := openDir(store, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.OpenFile(SinceName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, werr := f.WriteString(now.UTC().Format(time.RFC3339) + "\n")
	return errors.Join(werr, f.Close())
}

// expire removes every record and temporary whose file is older than expireAfter: the writers
// bound the directory themselves, so it needs no reclaimer of its own and never waits for a
// consent the host may never get. Only regular files with a writer's names are touched.
func expire(dir *os.Root, now time.Time) {
	f, err := dir.Open(".")
	if err != nil {
		return
	}
	entries, _ := f.ReadDir(-1)
	_ = f.Close()
	for _, e := range entries {
		if !recordName.MatchString(e.Name()) && !tempName.MatchString(e.Name()) {
			continue
		}
		fi, err := dir.Lstat(e.Name())
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if now.Sub(fi.ModTime()) > expireAfter {
			_ = dir.Remove(e.Name())
		}
	}
}

// Read is one record as a reader found it.
type Read struct {
	// File is the record's file name, for a message that names it.
	File string
	// Record is what it says. Zero when Err is set.
	Record Record
	// Modified is the file's modification time: the age of a record that cannot be parsed.
	Modified time.Time
	// Err is why it could not be read or parsed.
	Err error
}

// Census is every record beneath a store.
type Census struct {
	// Since is when the store got its first record; zero when it never has.
	Since time.Time
	// Records is every record file, readable or not, by file name.
	Records []Read
}

// ReadAll reads the since marker and every record beneath store. An absent record directory is
// an empty Census, not an error: no jail has recorded anything yet. An error is a directory that
// exists and cannot be read, which a reader must treat as "cannot tell".
func ReadAll(store string) (Census, error) {
	dir, err := openDir(store, false)
	if errors.Is(err, fs.ErrNotExist) {
		return Census{}, nil
	}
	if err != nil {
		return Census{}, err
	}
	defer dir.Close()
	var c Census
	if b, err := readLimited(dir, SinceName); err == nil {
		if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); perr == nil {
			c.Since = t
		}
	}
	f, err := dir.Open(".")
	if err != nil {
		return Census{}, err
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return Census{}, err
	}
	for _, e := range entries {
		if !recordName.MatchString(e.Name()) {
			continue
		}
		r := Read{File: e.Name()}
		fi, err := dir.Lstat(e.Name())
		if err != nil {
			r.Err = err
			c.Records = append(c.Records, r)
			continue
		}
		r.Modified = fi.ModTime()
		if !fi.Mode().IsRegular() {
			r.Err = errors.New("not a regular file")
			c.Records = append(c.Records, r)
			continue
		}
		b, err := readLimited(dir, e.Name())
		if err == nil {
			err = json.Unmarshal(b, &r.Record)
		}
		if err == nil && r.Record.Recorded.IsZero() {
			err = errors.New("it records no time")
		}
		if err != nil {
			r.Err = err
			r.Record = Record{}
		}
		c.Records = append(c.Records, r)
	}
	sort.Slice(c.Records, func(i, j int) bool { return c.Records[i].File < c.Records[j].File })
	return c, nil
}

// readLimited reads one file beneath dir, refusing one larger than maxRecordBytes.
func readLimited(dir *os.Root, name string) ([]byte, error) {
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxRecordBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxRecordBytes)
	}
	return b, nil
}
