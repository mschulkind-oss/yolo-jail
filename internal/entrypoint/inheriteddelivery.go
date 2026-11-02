package entrypoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"golang.org/x/sys/unix"
)

// inheriteddelivery.go is the RECORD of what the macos-user launch delivered as the inherited user
// config (inheritedCoreConfigDestination), and the admission that reads it (issue #70).
//
// WHY A RECORD AND NOT ONLY THE INODE. The installer delivers each overlay file as a hard link of the
// sealed staged file and, when the link is refused, as a copy (linkOrCopyAt). A copy has an inode of
// its own, and the overlay is re-staged with fresh inodes whenever its digest changes, so "is this
// destination the file yolo delivered?" cannot be answered by os.SameFile alone: answered that way,
// every relaunch after a copied delivery was refused as an owner file in the way.
//
// WHAT THE RECORD IS. A small JSON file, one per workspace session (cname), listing for each
// home-relative inherited destination the sha256 content digests yolo staged for it: the body this
// launch stages, plus the digest of the previous delivery when the launcher admitted it. The launcher
// writes it as the host user, sealed 0444, BESIDE the staged overlay in the host-owned staging root
// (macosuser.StagingRoot, /Users/Shared/yolo-staged) — never under the workspace sidecar, which the
// agent writes. The agent cannot write the staging root: in account mode it belongs to another uid,
// and in run-as-you mode the Seatbelt profile grants it read-only (stagingReadAllow).
//
// WHAT ADMITS. A destination that is a regular, sealed 0444 file, owned by the expected account,
// whose bytes hash to a digest the record lists for that destination. A matching header never admits
// (the agent can write any header); matching bytes without a record never admit either.

// inheritedDeliveryLeaf is the staging-root subdirectory holding one record per session.
const inheritedDeliveryLeaf = "inherited-delivered"

// InheritedDeliveryRecordPath is where the delivery record for the staged overlay stagedOverlay
// (macosuser.StagedHomeOverlay: <staging root>/home-overlay/<cname>) lives:
// <staging root>/inherited-delivered/<cname>.json. "" for "".
func InheritedDeliveryRecordPath(stagedOverlay string) string {
	if stagedOverlay == "" {
		return ""
	}
	clean := filepath.Clean(stagedOverlay)
	return filepath.Join(filepath.Dir(filepath.Dir(clean)), inheritedDeliveryLeaf, filepath.Base(clean)+".json")
}

// InheritedDeliveryRecord is the record's content: each home-relative destination's admitted
// content digests (ContentDigest).
type InheritedDeliveryRecord struct {
	Version int                 `json:"version"`
	Digests map[string][]string `json:"digests"`
}

// ContentDigest is the record's spelling of body's digest.
func ContentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Admits reports whether digest is one the record lists for the destination rel.
func (r InheritedDeliveryRecord) Admits(rel, digest string) bool {
	return digest != "" && slices.Contains(r.Digests[rel], digest)
}

// Marshal is the record's bytes.
func (r InheritedDeliveryRecord) Marshal() ([]byte, error) {
	r.Version = 1
	return json.MarshalIndent(r, "", "  ")
}

// ParseInheritedDeliveryRecord reads a record's bytes; anything but a version-1 record is an error.
func ParseInheritedDeliveryRecord(data []byte) (InheritedDeliveryRecord, error) {
	var r InheritedDeliveryRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return InheritedDeliveryRecord{}, err
	}
	if r.Version != 1 {
		return InheritedDeliveryRecord{}, fmt.Errorf("unknown inherited delivery record version %d", r.Version)
	}
	return r, nil
}

// readInheritedDeliveryRecord reads the record for the staged overlay at path without following a
// link, and only when it is the sealed file the host wrote: a regular 0444 file owned by owner (the
// staged overlay's owner, the host user) in a directory no one else can write.
func readInheritedDeliveryRecord(overlay string, owner uint32) (InheritedDeliveryRecord, bool) {
	path := InheritedDeliveryRecordPath(overlay)
	if path == "" {
		return InheritedDeliveryRecord{}, false
	}
	dirInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o022 != 0 || !fileOwnedBy(dirInfo, owner) {
		return InheritedDeliveryRecord{}, false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return InheritedDeliveryRecord{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 || !fileOwnedBy(info, owner) {
		return InheritedDeliveryRecord{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return InheritedDeliveryRecord{}, false
	}
	r, err := ParseInheritedDeliveryRecord(data)
	return r, err == nil
}

// fileOwnedBy reports whether info's owner is uid.
func fileOwnedBy(info os.FileInfo, uid uint32) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uid
}

// fileOwner is info's owner uid.
func fileOwner(info os.FileInfo) (uint32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

// recordedInheritedDelivery is an existing inherited destination the record admits: the stat of
// the file whose bytes were hashed (so a swap after the check is caught) and whether those bytes
// already are the staged source's.
type recordedInheritedDelivery struct {
	info    os.FileInfo
	digest  string
	current bool
}

// errNotRecorded is admitRecordedInheritedDelivery's answer for a destination the record does not
// admit.
var errNotRecorded = errors.New("not a delivery the record lists")

// admitRecordedInheritedDelivery decides whether base in dir (the destination dest's parent, opened
// beneath the checked root) is a delivery the record for overlay lists: a regular 0444 file owned
// by this installer's account or by the staged source's owner, whose content digest the record
// lists for dest. The bytes are hashed through a descriptor opened without following a link, and
// that descriptor's stat is what the replacement later compares the moved-aside file with.
func admitRecordedInheritedDelivery(overlay, dest, src string, srcInfo os.FileInfo, dir *os.Root, base string) (recordedInheritedDelivery, error) {
	srcOwner, ok := fileOwner(srcInfo)
	if !ok {
		return recordedInheritedDelivery{}, errNotRecorded
	}
	record, ok := readInheritedDeliveryRecord(overlay, srcOwner)
	if !ok {
		return recordedInheritedDelivery{}, errNotRecorded
	}
	parent, err := dir.Open(".")
	if err != nil {
		return recordedInheritedDelivery{}, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return recordedInheritedDelivery{}, errNotRecorded
	}
	f := os.NewFile(uintptr(fd), base)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 {
		return recordedInheritedDelivery{}, errNotRecorded
	}
	owner, ok := fileOwner(info)
	if !ok || (owner != srcOwner && owner != uint32(os.Geteuid())) {
		return recordedInheritedDelivery{}, errNotRecorded
	}
	digest, err := openFileDigest(f)
	if err != nil || !record.Admits(dest, digest) {
		return recordedInheritedDelivery{}, errNotRecorded
	}
	staged, err := os.ReadFile(src)
	if err != nil {
		return recordedInheritedDelivery{}, err
	}
	return recordedInheritedDelivery{info: info, digest: digest, current: ContentDigest(staged) == digest}, nil
}

// openFileDigest is the content digest of f, read from its current offset, in ContentDigest's form.
func openFileDigest(f *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// stillAt reports whether name in the directory pfd is still the file admitRecordedInheritedDelivery
// hashed: the same inode, the same mode, AND the same bytes. Identity alone is not enough: the
// agent can rewrite a file it owns in place, and a filesystem that reuses a freed inode number
// (ext4) gives a file removed and recreated after the check the hashed file's identity.
func (r recordedInheritedDelivery) stillAt(pfd int, name string) bool {
	fd, err := unix.Openat(pfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(info, r.info) || info.Mode() != r.info.Mode() {
		return false
	}
	digest, err := openFileDigest(f)
	return err == nil && digest == r.digest
}
