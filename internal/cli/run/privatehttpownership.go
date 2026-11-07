package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const preparedHTTPOwnershipVersion = 1

// A prepared-owner reference outlives its disposable service scope and input. The container
// teardown removes both the reference and its exact generation only after a known-absent probe.
type preparedHTTPGenerationRef struct {
	Path            string `json:"path"`
	Generation      string `json:"generation"`
	Loophole        string `json:"loophole"`
	LaunchID        string `json:"launch_id"`
	Source          string `json:"source"`
	DeclarationHash string `json:"declaration_hash"`
}

type preparedHTTPOwnershipRecord struct {
	Version     int                         `json:"version"`
	Container   string                      `json:"container"`
	Runtime     string                      `json:"runtime"`
	Setup       string                      `json:"setup"`
	PackTree    string                      `json:"pack_tree"`
	Generations []preparedHTTPGenerationRef `json:"generations"`
}

func preparedHTTPOwnershipPath(cname string) string {
	if cname == "" || filepath.Base(cname) != cname || cname == "." || cname == ".." {
		return ""
	}
	return filepath.Join(ownerPIDDir(), cname+".prepared-http.json")
}

// stalePreparedHTTPOwnershipError is ensurePreparedHTTPOwnership's refusal of a record an earlier
// launch of the same container left behind (a killed keeper, a reboot, a teardown that could not
// ask the runtime). Only a fresh launch of the record's own runtime that has seen that container
// gone may retire it (retireStalePreparedHTTPOwnership); this launch did not, so it names the file.
// runtime is the record's. files are the record and the prepared generations it names that exist,
// for a refusal whose next step is removing them by hand. unverified is set only when this
// launch's runtime ANSWERED that the container is gone and retirement still kept those files
// because they no longer match the record: a later launch would keep them too.
type stalePreparedHTTPOwnershipError struct {
	path       string
	runtime    string
	files      []string
	unverified []string
}

func (o *Options) staleOwnershipError(path string, rec preparedHTTPOwnershipRecord) *stalePreparedHTTPOwnershipError {
	e := &stalePreparedHTTPOwnershipError{path: path, runtime: rec.Runtime, files: []string{path}}
	for _, ref := range rec.Generations {
		dir := filepath.Dir(ref.Path)
		for _, leaf := range []string{ref.Path, filepath.Join(dir, preparedHTTPTempLeaf(ref.Generation)),
			filepath.Join(dir, preparedHTTPEntrypointsLeaf(filepath.Base(ref.Path))), filepath.Join(dir, preparedHTTPEntrypointsTempLeaf(ref.Generation))} {
			if _, err := os.Lstat(leaf); err == nil {
				e.files = append(e.files, leaf)
			}
		}
	}
	if rec.Runtime == o.runtime {
		e.unverified = append([]string(nil), o.preparedHTTPRetireUnverified...)
	}
	return e
}

func (e *stalePreparedHTTPOwnershipError) Error() string {
	return "prepared HTTP ownership record " + e.path + " belongs to an earlier launch"
}

// ensurePreparedHTTPOwnership creates this launch's empty ownership record, or accepts the one it
// already wrote. created reports a record this call made, which the caller's rollback may remove.
func (o *Options) ensurePreparedHTTPOwnership(cname string) (created bool, err error) {
	path := preparedHTTPOwnershipPath(cname)
	setupName := o.Setup.Name
	if setupName == "" {
		setupName = o.SetupName
	}
	if path == "" || o.preparedHTTPOwnershipFile != path || o.runtime == "" || setupName == "" || o.packTree == "" {
		return false, os.ErrInvalid
	}
	if err := launchInputOutsideJail(path, o.launchInputMountArgv); err != nil {
		return false, err
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return false, err
	}
	rec, prior, present, err := readPreparedHTTPOwnership(cname)
	if err != nil {
		return false, err
	}
	if present {
		if rec.Runtime != o.runtime || rec.Setup != setupName || rec.PackTree != o.packTree {
			return false, o.staleOwnershipError(path, rec)
		}
		return false, nil
	}
	rec = preparedHTTPOwnershipRecord{Version: preparedHTTPOwnershipVersion, Container: cname,
		Runtime: o.runtime, Setup: setupName, PackTree: o.packTree, Generations: []preparedHTTPGenerationRef{}}
	if _, err = writePreparedHTTPOwnership(rec, prior); err != nil {
		return false, err
	}
	return true, nil
}

// retireStalePreparedHTTPOwnership retires the ownership record, and the prepared generations it
// references, that an earlier launch of cname left when it ended uncleanly. The fresh container
// path calls it after its stale-container removal and under the workspace launch lock, so no other
// launch of the workspace is creating the container. It acts only when the runtime ANSWERS that no
// container of cname exists: "could not ask" and "still there" keep everything, and this launch's
// ensurePreparedHTTPOwnership then refuses naming the file. It asks only the runtime the record
// names: another runtime's answer says nothing about that container (a podman jail is invisible to
// Apple's `container ls`), so a record of another runtime is kept, and the refusal names that
// runtime's commands. A referenced generation that no longer matches its record is kept, and with
// it the record (removePreparedHTTPOwnership); those files are kept on o for the refusal, which
// names them for removal by hand, since no later launch can verify them either.
func (o *Options) retireStalePreparedHTTPOwnership(cname, rt string) {
	o.preparedHTTPRetireUnverified = nil
	rec, _, present, err := readPreparedHTTPOwnership(cname)
	if !present || err != nil || rec.Runtime != rt {
		return
	}
	if id, known := o.probeExistingContainer(cname, rt, trackingProbeTimeout); !known || id != "" {
		return
	}
	o.preparedHTTPRetireUnverified = removePreparedHTTPOwnership(cname)
}

// recordPreparedHTTPGeneration adds owner's generation to this launch's ownership record BEFORE
// the generation is published at its final path, so no crash can leave a published prepared file
// that no record names. added is false when the record already named it (a reused generation),
// which the caller's rollback must then leave in place.
func (o *Options) recordPreparedHTTPGeneration(owner preparedHTTPGenerationOwner) (added bool, err error) {
	path := preparedHTTPOwnershipPath(owner.container)
	if path == "" || o.preparedHTTPOwnershipFile != path || owner.path == "" || !validPreparedHTTPGeneration(owner.generation) {
		return false, os.ErrInvalid
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return false, err
	}
	if err := launchInputOutsideJail(path, o.launchInputMountArgv); err != nil {
		return false, err
	}
	rec, info, present, err := readPreparedHTTPOwnership(owner.container)
	if err != nil {
		return false, err
	}
	setupName := o.Setup.Name
	if setupName == "" {
		setupName = o.SetupName
	}
	prepared, ref, err := readPreparedHTTPGenerationOwner(owner, o.runtime, setupName, o.packTree)
	if err != nil || prepared.Container != owner.container {
		if err == nil {
			err = os.ErrInvalid
		}
		return false, err
	}
	if !present {
		rec = preparedHTTPOwnershipRecord{Version: preparedHTTPOwnershipVersion, Container: owner.container,
			Runtime: o.runtime, Setup: setupName, PackTree: o.packTree}
	} else if rec.Runtime != o.runtime || rec.PackTree != o.packTree || rec.Setup != setupName {
		return false, o.staleOwnershipError(path, rec)
	}
	for _, existing := range rec.Generations {
		if existing.Path == ref.Path || existing.Generation == ref.Generation {
			if existing.Path == ref.Path && existing.Generation == ref.Generation {
				return false, nil
			}
			return false, errors.New("prepared HTTP ownership already names a different generation")
		}
	}
	rec.Generations = append(rec.Generations, ref)
	if _, err = writePreparedHTTPOwnership(rec, info); err != nil {
		return false, err
	}
	return true, nil
}

func readPreparedHTTPGenerationOwner(owner preparedHTTPGenerationOwner, rt, setupName, tree string) (callbrokerinput.PreparedHTTPClientGeneration, preparedHTTPGenerationRef, error) {
	var zero callbrokerinput.PreparedHTTPClientGeneration
	var ref preparedHTTPGenerationRef
	if owner.path == "" || owner.file == nil || !filepath.IsAbs(owner.path) || !validPreparedHTTPGeneration(owner.generation) {
		return zero, ref, os.ErrInvalid
	}
	root, err := paths.OpenStateDirRoot(filepath.Dir(owner.path))
	if err != nil {
		return zero, ref, err
	}
	defer root.Close()
	leaf := filepath.Base(owner.path)
	if owner.temp != "" {
		leaf = owner.temp // not yet published: the reference is recorded first
	}
	data, opened, err := readPrivateHTTPFile(root, leaf)
	if err != nil || !os.SameFile(owner.file, opened) {
		return zero, ref, errors.New("prepared HTTP identity changed before ownership was recorded")
	}
	prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(data)
	if err != nil || prepared.Generation != owner.generation || prepared.Container != owner.container ||
		prepared.Runtime != rt || prepared.Setup != setupName || prepared.PackTree != tree || prepared.Loophole == "" ||
		prepared.LaunchID == "" || prepared.Source == "" || prepared.DeclarationHash == "" {
		return zero, ref, errors.New("prepared HTTP identity does not match its frozen owner")
	}
	expectedPath := filepath.Join(loopholes.StateDirFor(prepared.Loophole), "replicas", owner.container+".json.prepared")
	if filepath.Clean(owner.path) != filepath.Clean(expectedPath) {
		return zero, ref, errors.New("prepared HTTP identity is outside its selected host state scope")
	}
	ref = preparedHTTPGenerationRef{Path: owner.path, Generation: prepared.Generation, Loophole: prepared.Loophole,
		LaunchID: prepared.LaunchID, Source: prepared.Source, DeclarationHash: prepared.DeclarationHash}
	return prepared, ref, nil
}

func readPreparedHTTPOwnership(cname string) (preparedHTTPOwnershipRecord, os.FileInfo, bool, error) {
	var zero preparedHTTPOwnershipRecord
	path := preparedHTTPOwnershipPath(cname)
	if path == "" {
		return zero, nil, false, os.ErrInvalid
	}
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if errors.Is(err, os.ErrNotExist) {
		return zero, nil, false, nil
	}
	if err != nil {
		return zero, nil, false, err
	}
	defer root.Close()
	name := filepath.Base(path)
	before, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return zero, nil, false, nil
	}
	if err != nil || !privateHTTPFileOwned(before, 0o600) || before.Sys().(*syscall.Stat_t).Nlink != 1 {
		return zero, nil, true, errors.New("prepared HTTP ownership record is unsafe")
	}
	file, err := paths.OpenRegularFileBeneath(root, name)
	if err != nil {
		return zero, nil, true, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !privateHTTPFileOwned(opened, 0o600) || opened.Sys().(*syscall.Stat_t).Nlink != 1 {
		return zero, nil, true, errors.New("prepared HTTP ownership record changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return zero, nil, true, errors.New("prepared HTTP ownership record is unreadable or too large")
	}
	var rec preparedHTTPOwnershipRecord
	if json.Unmarshal(data, &rec) != nil || rec.Version != preparedHTTPOwnershipVersion || rec.Container != cname || rec.Runtime == "" || rec.Setup == "" || rec.PackTree == "" || rec.Generations == nil {
		return zero, nil, true, errors.New("prepared HTTP ownership record is invalid")
	}
	seenPaths, seenGenerations := map[string]bool{}, map[string]bool{}
	for _, generation := range rec.Generations {
		if !filepath.IsAbs(generation.Path) || filepath.Base(generation.Path) != cname+".json.prepared" ||
			!validPreparedHTTPGeneration(generation.Generation) || generation.Loophole == "" || generation.LaunchID == "" ||
			generation.Source == "" || generation.DeclarationHash == "" || seenPaths[generation.Path] || seenGenerations[generation.Generation] ||
			filepath.Clean(generation.Path) != filepath.Join(loopholes.StateDirFor(generation.Loophole), "replicas", cname+".json.prepared") {
			return zero, nil, true, errors.New("prepared HTTP ownership reference is invalid")
		}
		seenPaths[generation.Path], seenGenerations[generation.Generation] = true, true
	}
	return rec, opened, true, nil
}

func syncPreparedHTTPOwnershipDir(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func writePreparedHTTPOwnership(rec preparedHTTPOwnershipRecord, prior os.FileInfo) (os.FileInfo, error) {
	name := filepath.Base(preparedHTTPOwnershipPath(rec.Container))
	if name == "." || name == string(filepath.Separator) {
		return nil, os.ErrInvalid
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	temp := "." + name + "." + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(temp)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Stat(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if prior == nil {
		if err := root.Link(temp, name); err != nil {
			return nil, err
		}
		if err := root.Remove(temp); err != nil {
			_ = root.Remove(name)
			return nil, err
		}
		if err := syncPreparedHTTPOwnershipDir(root); err != nil {
			_ = root.Remove(name)
			return nil, err
		}
	} else {
		current, err := root.Lstat(name)
		if err != nil || !os.SameFile(prior, current) || !privateHTTPFileOwned(current, 0o600) {
			return nil, errors.New("prepared HTTP ownership record was replaced")
		}
		if err := root.Rename(temp, name); err != nil {
			return nil, err
		}
		if err := syncPreparedHTTPOwnershipDir(root); err != nil {
			return nil, err
		}
	}
	cleanup = false
	return root.Lstat(name)
}

func validPreparedHTTPGeneration(generation string) bool {
	if len(generation) != 32 {
		return false
	}
	for _, c := range generation {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func removeEmptyPreparedHTTPOwnership(cname, rt, setupName, tree string) {
	rec, marker, present, err := readPreparedHTTPOwnership(cname)
	if err != nil || !present || len(rec.Generations) != 0 || rec.Runtime != rt || rec.Setup != setupName || rec.PackTree != tree {
		return
	}
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if err != nil {
		return
	}
	defer root.Close()
	name := filepath.Base(preparedHTTPOwnershipPath(cname))
	current, err := root.Lstat(name)
	if err == nil && os.SameFile(marker, current) && privateHTTPFileOwned(current, 0o600) {
		_ = root.Remove(name)
	}
}

func removePreparedHTTPGenerationOwnership(owner preparedHTTPGenerationOwner, removeFile bool) bool {
	rec, marker, present, err := readPreparedHTTPOwnership(owner.container)
	if err != nil || !present {
		return false
	}
	index := -1
	for i, ref := range rec.Generations {
		if ref.Path == owner.path && ref.Generation == owner.generation {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	if removeFile && len(removePreparedHTTPGenerationReference(owner.container, rec, rec.Generations[index])) != 0 {
		return false
	}
	if len(rec.Generations) == 1 {
		root, err := paths.OpenStateDirRoot(ownerPIDDir())
		if err != nil {
			return false
		}
		defer root.Close()
		name := filepath.Base(preparedHTTPOwnershipPath(owner.container))
		current, err := root.Lstat(name)
		return err == nil && os.SameFile(marker, current) && privateHTTPFileOwned(current, 0o600) && root.Remove(name) == nil
	}
	rec.Generations = append(rec.Generations[:index], rec.Generations[index+1:]...)
	_, err = writePreparedHTTPOwnership(rec, marker)
	return err == nil
}

// removePreparedHTTPOwnership removes cname's ownership record and every prepared generation it
// names whose bytes still match it. unverified lists the generation files that did not match and
// so were kept, and with them the record.
func removePreparedHTTPOwnership(cname string) (unverified []string) {
	rec, marker, present, err := readPreparedHTTPOwnership(cname)
	if err != nil || !present {
		return nil
	}
	for _, generation := range rec.Generations {
		unverified = append(unverified, removePreparedHTTPGenerationReference(cname, rec, generation)...)
	}
	if len(unverified) != 0 {
		return unverified
	}
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if err != nil {
		return nil
	}
	defer root.Close()
	name := filepath.Base(preparedHTTPOwnershipPath(cname))
	current, err := root.Lstat(name)
	if err == nil && os.SameFile(marker, current) && privateHTTPFileOwned(current, 0o600) {
		_ = root.Remove(name)
	}
	return nil
}

// removePreparedHTTPGenerationReference removes ref's prepared generation, at its final path and
// at the unpublished temp name a launch killed between recording and publishing it left
// (preparedHTTPTempLeaf), each only while its bytes still match ref and rec. A leaf that is
// already gone counts as removed. kept lists the paths it could not verify, and so left.
func removePreparedHTTPGenerationReference(cname string, rec preparedHTTPOwnershipRecord, ref preparedHTTPGenerationRef) (kept []string) {
	root, err := paths.OpenStateDirRoot(filepath.Dir(ref.Path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []string{ref.Path}
	}
	defer root.Close()
	for _, leaf := range []string{preparedHTTPEntrypointsLeaf(filepath.Base(ref.Path)), preparedHTTPEntrypointsTempLeaf(ref.Generation)} {
		if !removeMatchingPreparedHTTPEntrypointsLeaf(root, leaf, cname, ref) {
			kept = append(kept, filepath.Join(filepath.Dir(ref.Path), leaf))
		}
	}
	preparedKept := false
	for _, leaf := range []string{filepath.Base(ref.Path), preparedHTTPTempLeaf(ref.Generation)} {
		if !removeMatchingPreparedHTTPLeaf(root, leaf, cname, rec, ref) {
			kept = append(kept, filepath.Join(filepath.Dir(ref.Path), leaf))
			preparedKept = preparedKept || leaf == filepath.Base(ref.Path)
		}
	}
	return append(kept, removeMatchingPreparedHTTPFrontLeaves(root, cname, ref, preparedKept)...)
}

// preparedHTTPFrontSealLeaf and preparedHTTPFrontTokenLeaf name the seal and the local token a
// selected HTTP front's broker writes beside the prepared generation named prepared (the broker's
// `<launch input>.prepared.seal` and `.prepared.token`, internal/callbroker's seal).
func preparedHTTPFrontSealLeaf(prepared string) string  { return prepared + ".seal" }
func preparedHTTPFrontTokenLeaf(prepared string) string { return prepared + ".token" }

// readPreparedHTTPFrontSeal reads and strictly decodes the seal at leaf. present is false only
// when no leaf is there; a leaf that is unsafe, unreadable or does not decode is present with err.
func readPreparedHTTPFrontSeal(root *os.Root, leaf string) (seal callbrokerinput.PreparedHTTPFrontSeal, opened os.FileInfo, present bool, err error) {
	if _, err := root.Lstat(leaf); errors.Is(err, os.ErrNotExist) {
		return seal, nil, false, nil
	}
	data, opened, err := readPrivateHTTPFile(root, leaf)
	if err != nil {
		return seal, nil, true, err
	}
	seal, err = callbrokerinput.DecodePreparedHTTPFrontSeal(data)
	return seal, opened, true, err
}

// removePrivateHTTPLeaf removes leaf while it is a regular 0600 file of this user, the shape the
// broker writes. A leaf already gone counts as removed.
func removePrivateHTTPLeaf(root *os.Root, leaf string) bool {
	before, err := root.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && privateHTTPFileOwned(before, 0o600) && root.Remove(leaf) == nil
}

// removeMatchingPreparedHTTPFrontLeaves retires the seal and token a selected HTTP front's broker
// left beside ref's prepared generation (slice 3.4): left behind, they make the next launch of the
// same container name refuse to seal. The seal goes only while it names cname, ref's front, ref's
// generation, launch and declaration; a seal of ANOTHER generation of cname is a replacement
// launch's and is kept with its token, unreported. The token is opaque, so it follows the seal: it
// goes when the seal was ours, or when there is no seal and the prepared generation beside it was
// ours or is gone (a broker killed between writing the two). A token with no seal beside a
// prepared generation that is not ours (preparedKept) may be that generation's front mid-seal, and
// is kept unreported, as the prepared leaf already is reported. kept lists what it could not verify.
func removeMatchingPreparedHTTPFrontLeaves(root *os.Root, cname string, ref preparedHTTPGenerationRef, preparedKept bool) (kept []string) {
	base, dir := filepath.Base(ref.Path), filepath.Dir(ref.Path)
	sealLeaf, tokenLeaf := preparedHTTPFrontSealLeaf(base), preparedHTTPFrontTokenLeaf(base)
	seal, opened, present, err := readPreparedHTTPFrontSeal(root, sealLeaf)
	switch {
	case !present && preparedKept:
		return nil
	case !present:
	case err != nil || seal.Container != cname || seal.Loophole != ref.Loophole:
		return []string{filepath.Join(dir, sealLeaf)}
	case seal.Generation != ref.Generation:
		return nil
	case seal.LaunchID != ref.LaunchID || seal.DeclarationHash != ref.DeclarationHash:
		return []string{filepath.Join(dir, sealLeaf)}
	default:
		if current, err := root.Lstat(sealLeaf); err != nil || !os.SameFile(opened, current) || root.Remove(sealLeaf) != nil {
			return []string{filepath.Join(dir, sealLeaf)}
		}
	}
	if !removePrivateHTTPLeaf(root, tokenLeaf) {
		kept = append(kept, filepath.Join(dir, tokenLeaf))
	}
	return kept
}

// removeMatchingPreparedHTTPEntrypointsLeaf removes the entrypoints record of ref's generation,
// only while its bytes still name cname, ref's front and ref's generation, at ref's own path. A
// leaf already gone counts as removed.
func removeMatchingPreparedHTTPEntrypointsLeaf(root *os.Root, leaf, cname string, ref preparedHTTPGenerationRef) bool {
	before, err := root.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !privateHTTPFileOwned(before, 0o600) {
		return false
	}
	data, opened, err := readPrivateHTTPFile(root, leaf)
	if err != nil || !os.SameFile(before, opened) {
		return false
	}
	r, err := callbrokerinput.DecodePreparedHTTPClientEntrypoints(data)
	if err != nil || r.Container != cname || r.Generation != ref.Generation || r.Loophole != ref.Loophole ||
		filepath.Clean(ref.Path) != filepath.Join(loopholes.StateDirFor(r.Loophole), "replicas", cname+".json.prepared") {
		return false
	}
	current, err := root.Lstat(leaf)
	return err == nil && os.SameFile(opened, current) && root.Remove(leaf) == nil
}

func removeMatchingPreparedHTTPLeaf(root *os.Root, leaf, cname string, rec preparedHTTPOwnershipRecord, ref preparedHTTPGenerationRef) bool {
	before, err := root.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !privateHTTPFileOwned(before, 0o600) {
		return false
	}
	data, opened, err := readPrivateHTTPFile(root, leaf)
	if err != nil || !os.SameFile(before, opened) {
		return false
	}
	prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(data)
	if err != nil || prepared.Container != cname || prepared.Generation != ref.Generation || prepared.Runtime != rec.Runtime ||
		prepared.Setup != rec.Setup || prepared.PackTree != rec.PackTree || prepared.Loophole != ref.Loophole ||
		prepared.LaunchID != ref.LaunchID || prepared.Source != ref.Source || prepared.DeclarationHash != ref.DeclarationHash ||
		filepath.Clean(ref.Path) != filepath.Join(loopholes.StateDirFor(prepared.Loophole), "replicas", cname+".json.prepared") {
		return false
	}
	current, err := root.Lstat(leaf)
	return err == nil && os.SameFile(opened, current) && root.Remove(leaf) == nil
}

func verifiedPreparedHTTPOwnership(rec preparedHTTPOwnershipRecord, cname, rt, setupName string) bool {
	if rec.Container != cname || rec.Runtime != rt || rec.Setup != setupName || rec.PackTree == "" {
		return false
	}
	if keeper, ok := readKeeperRecord(cname); ok && keeper.Runtime == rt && keeper.PackTree == rec.PackTree {
		return true
	}
	raw, err := os.ReadFile(paths.LivePackTreeRecord(cname))
	if err != nil {
		return false
	}
	name := strings.TrimSpace(string(raw))
	return name != "" && filepath.IsLocal(name) && filepath.Base(name) == name &&
		filepath.Clean(rec.PackTree) == filepath.Join(paths.PackTreeRoot(cname), name)
}
