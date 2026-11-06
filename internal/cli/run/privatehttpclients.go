package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokeridentity"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type preparedHTTPGenerationOwner struct {
	path       string
	temp       string
	file       os.FileInfo
	container  string
	generation string
	// The entrypoints record beside the prepared generation (preparedHTTPEntrypointsLeaf): its
	// unpublished temp name while entrypointsTemp is set, and the record itself.
	entrypointsTemp string
	entrypointsFile os.FileInfo
	entrypoints     callbrokerinput.PreparedHTTPClientEntrypoints
}

// preparedHTTPEntrypointsLeaf is the published entrypoints record of a prepared generation whose
// file is preparedLeaf; preparedHTTPEntrypointsTempLeaf is its unpublished name.
func preparedHTTPEntrypointsLeaf(preparedLeaf string) string { return preparedLeaf + ".entrypoints" }
func preparedHTTPEntrypointsTempLeaf(generation string) string {
	return ".prepared-http-entrypoints-" + generation
}

// preparedHTTPEntrypoints binds each of prepared's clients to the producer's checked entrypoint
// for it (Options.privateClientEntrypoints) at prepared's generation. A client without one has
// no checked delivery, and is refused.
func (o *Options) preparedHTTPEntrypoints(prepared callbrokerinput.PreparedHTTPClientGeneration) (callbrokerinput.PreparedHTTPClientEntrypoints, error) {
	r := callbrokerinput.PreparedHTTPClientEntrypoints{Version: callbrokerinput.PreparedHTTPClientEntrypointsVersion,
		Generation: prepared.Generation, Container: prepared.Container, Loophole: prepared.Loophole}
	for _, plan := range prepared.Clients {
		found := false
		for _, checked := range o.privateClientEntrypoints {
			if checked.SameSelection(plan) {
				checked.Binding.Generation = prepared.Generation
				r.Clients = append(r.Clients, checked)
				found = true
				break
			}
		}
		if !found {
			return r, fmt.Errorf("selected private HTTP client %q has no checked capture delivery; rerun yolo on the supported baseline runtime to reprepare it", plan.Bin)
		}
	}
	return r, callbrokerinput.ValidatePreparedHTTPClientEntrypoints(r, prepared)
}

// writePrivateHTTPTemp writes data to a new private temp leaf in replicas and returns its inode.
func writePrivateHTTPTemp(replicas *os.Root, temp string, data []byte) (os.FileInfo, error) {
	file, err := replicas.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	var info os.FileInfo
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		info, err = file.Stat()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = replicas.Remove(temp)
		return nil, err
	}
	return info, nil
}

func selectedPrivateProgramPacks(tree string) ([]*packload.Pack, error) {
	if tree == "" {
		return nil, nil
	}
	return loadPackTree(tree)
}

func (o *Options) prepareHTTPClientGeneration(lp *loopholes.Loophole, frozen LaunchInputContext, input *CallBrokerLaunchInput,
	replicas *os.Root, stateDir, inputLeaf string) (reference string, owner preparedHTTPGenerationOwner, created bool, err error) {
	if lp == nil || input == nil || input.Front != "http" || input.HTTPBinding == nil || !lp.Enabled || lp.Source != loopholes.SourcePack {
		return "", owner, false, nil
	}
	packs, err := selectedPrivateProgramPacks(frozen.PackTree)
	if err != nil {
		return "", owner, false, err
	}
	plans, err := packload.ResolvePrivateProgramEnv(packs, lp.Name, lp.Pack, input.HTTPBinding != nil && input.HTTPBinding.DeclarationHash == input.DeclarationHash)
	if err != nil {
		return "", owner, false, err
	}
	if len(plans) == 0 {
		return "", owner, false, nil
	}
	scope, err := brokerscope.ReadFile(input.ScopeFile)
	if err != nil {
		return "", owner, false, err
	}
	setupName := o.Setup.Name
	if setupName == "" {
		setupName = o.SetupName
	}
	if frozen.Container == "" || input.FrontJailID == "" || frozen.Runtime == "" || setupName == "" || frozen.PackTree == "" || scope.LaunchID == "" || scope.Container != frozen.Container || scope.Source != input.Source {
		return "", owner, false, errors.New("selected private HTTP client lacks its frozen producer identity")
	}
	prepared := callbrokerinput.PreparedHTTPClientGeneration{
		Version: callbrokerinput.PreparedHTTPClientGenerationVersion, Phase: "prepared",
		Container: frozen.Container, FrontJailID: input.FrontJailID, Runtime: frozen.Runtime,
		Setup: setupName, PackTree: frozen.PackTree, LaunchID: scope.LaunchID,
		Loophole: input.Loophole, Front: input.Front, Pack: input.Pack, Source: input.Source,
		DeclarationHash: input.DeclarationHash, Roots: append([]string{}, input.Roots...),
		ProgramIdentities: cloneProgramIdentities(input.ProgramIdentities),
		Clients:           make([]callbrokerinput.PreparedHTTPClientPlan, 0, len(plans)),
	}
	for _, p := range plans {
		prepared.Clients = append(prepared.Clients, callbrokerinput.PreparedHTTPClientPlan{
			Bin: p.Bin, Pack: p.Pack, Front: p.Front, RecipePack: p.RecipePack, RecipeIdentity: p.RecipeIdentity,
			TokenVariable: p.TokenVariable, URLVariable: p.URLVariable, TrustVariable: p.TrustVariable,
		})
	}
	path := inputLeaf + ".prepared"
	owner = preparedHTTPGenerationOwner{path: filepath.Join(stateDir, "replicas", path), container: frozen.Container}
	if replicas == nil {
		return "", owner, false, errors.New("selected HTTP private generation directory is unavailable")
	}
	leaf := inputLeaf + ".prepared"
	if existing, statErr := replicas.Lstat(leaf); statErr == nil {
		if !existing.Mode().IsRegular() || existing.Mode().Perm() != 0o600 {
			return "", owner, false, errors.New("existing selected HTTP prepared identity is unsafe")
		}
		file, openErr := replicas.OpenFile(leaf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if openErr != nil {
			return "", owner, false, errors.New("existing selected HTTP prepared identity cannot be opened")
		}
		opened, statErr := file.Stat()
		data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
		closeErr := file.Close()
		if statErr != nil || !os.SameFile(existing, opened) || readErr != nil || closeErr != nil || len(data) > 1<<20 {
			return "", owner, false, errors.New("existing selected HTTP prepared identity changed while reading")
		}
		old, decodeErr := callbrokerinput.DecodePreparedHTTPClientGeneration(data)
		prepared.Generation = old.Generation
		if decodeErr != nil || !reflect.DeepEqual(old, prepared) {
			return "", owner, false, errors.New("existing selected HTTP prepared identity does not match this frozen producer")
		}
		want, err := o.preparedHTTPEntrypoints(prepared)
		if err != nil {
			return "", owner, false, err
		}
		recorded, opened, readErr := readPrivateHTTPFile(replicas, preparedHTTPEntrypointsLeaf(leaf))
		got, decodeErr := callbrokerinput.DecodePreparedHTTPClientEntrypoints(recorded)
		if readErr != nil || decodeErr != nil || !reflect.DeepEqual(got, want) {
			return "", owner, false, errors.New("existing selected HTTP prepared identity has no matching entrypoints record")
		}
		owner.file, owner.generation = existing, old.Generation
		owner.entrypointsFile, owner.entrypoints = opened, got
		return old.Generation, owner, false, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", owner, false, statErr
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", owner, false, err
	}
	prepared.Generation = hex.EncodeToString(nonce[:])
	if err := prepared.Validate(); err != nil {
		return "", owner, false, err
	}
	// The entrypoints record is built BEFORE either file is written: a client without a checked
	// delivery refuses here, and nothing is left to clean.
	entrypoints, err := o.preparedHTTPEntrypoints(prepared)
	if err != nil {
		return "", owner, false, err
	}
	data, err := json.Marshal(prepared)
	if err != nil {
		return "", owner, false, err
	}
	bindings, err := json.Marshal(entrypoints)
	if err != nil {
		return "", owner, false, err
	}
	temp := preparedHTTPTempLeaf(prepared.Generation)
	if owner.file, err = writePrivateHTTPTemp(replicas, temp, data); err != nil {
		return "", owner, false, err
	}
	entrypointsTemp := preparedHTTPEntrypointsTempLeaf(prepared.Generation)
	if owner.entrypointsFile, err = writePrivateHTTPTemp(replicas, entrypointsTemp, bindings); err != nil {
		_ = replicas.Remove(temp)
		return "", owner, false, err
	}
	owner.temp, owner.entrypointsTemp, owner.entrypoints = temp, entrypointsTemp, entrypoints
	owner.generation = prepared.Generation
	return prepared.Generation, owner, true, nil
}

// preparedHTTPTempLeaf is the unpublished name of a prepared generation, in its replicas directory.
func preparedHTTPTempLeaf(generation string) string { return ".prepared-http-client-" + generation }

// sweepDeadLaunchPreparedHTTP removes the prepared generations of cname that an earlier launch
// left in replicas when it was killed mid-publication: an unpublished temp, from any launch, and
// a published file staged from a pack tree other than tree, this launch's own. The caller holds
// the replicas directory's exclusive .launch-input.lock, under which alone such files are written,
// and has just ensured that the ownership record is this launch's, so an earlier launch's record
// no longer names them; the workspace lock keeps a second launch of cname out. Bytes that do not
// decode as cname's prepared generation are left alone, and so is a generation staged under a
// runtime other than rt, whose container this launch's runtime cannot see: a published one of
// those is returned as foreign, with its runtime, for the caller to refuse naming it.
func sweepDeadLaunchPreparedHTTP(replicas *os.Root, cname, rt, tree string) (foreign, foreignRuntime string) {
	dir, err := replicas.Open(".")
	if err != nil {
		return "", ""
	}
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		return "", ""
	}
	for _, name := range names {
		generation, temp := strings.CutPrefix(name, preparedHTTPTempLeaf(""))
		if temp && !validPreparedHTTPGeneration(generation) || !temp && name != cname+".json.prepared" {
			continue
		}
		data, opened, err := readPrivateHTTPFile(replicas, name)
		if err != nil {
			continue
		}
		prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(data)
		if err != nil || prepared.Container != cname || temp && prepared.Generation != generation || !temp && prepared.PackTree == tree {
			continue
		}
		if prepared.Runtime != rt {
			if !temp && foreign == "" {
				foreign, foreignRuntime = name, prepared.Runtime
			}
			continue
		}
		if current, err := replicas.Lstat(name); err == nil && os.SameFile(opened, current) {
			_ = replicas.Remove(name)
		}
	}
	sweepDeadLaunchPreparedHTTPEntrypoints(replicas, cname)
	return foreign, foreignRuntime
}

// sweepDeadLaunchPreparedHTTPEntrypoints removes cname's entrypoints records (published or not)
// whose generation is not that of a prepared generation the prepared sweep kept for cname, so
// what that sweep retains keeps its record. A record that does not decode as cname's is left alone.
func sweepDeadLaunchPreparedHTTPEntrypoints(replicas *os.Root, cname string) {
	dir, err := replicas.Open(".")
	if err != nil {
		return
	}
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		return
	}
	kept := map[string]bool{}
	for _, name := range names {
		generation, temp := strings.CutPrefix(name, preparedHTTPTempLeaf(""))
		if !temp && name != cname+".json.prepared" {
			continue
		}
		data, _, err := readPrivateHTTPFile(replicas, name)
		if err != nil {
			continue
		}
		prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(data)
		if err != nil || prepared.Container != cname || temp && prepared.Generation != generation {
			continue
		}
		kept[prepared.Generation] = true
	}
	for _, name := range names {
		generation, temp := strings.CutPrefix(name, preparedHTTPEntrypointsTempLeaf(""))
		if temp && !validPreparedHTTPGeneration(generation) || !temp && name != preparedHTTPEntrypointsLeaf(cname+".json.prepared") {
			continue
		}
		data, opened, err := readPrivateHTTPFile(replicas, name)
		if err != nil {
			continue
		}
		r, err := callbrokerinput.DecodePreparedHTTPClientEntrypoints(data)
		if err != nil || r.Container != cname || temp && r.Generation != generation || kept[r.Generation] {
			continue
		}
		if current, err := replicas.Lstat(name); err == nil && os.SameFile(opened, current) {
			_ = replicas.Remove(name)
		}
	}
}

// publishPreparedHTTP is publishPreparedHTTPGeneration, a seam so a test can observe the state a
// kill just before publication would leave.
var publishPreparedHTTP = publishPreparedHTTPGeneration

func publishPreparedHTTPGeneration(replicas *os.Root, owner *preparedHTTPGenerationOwner) error {
	if owner == nil || owner.temp == "" {
		return nil
	}
	// The entrypoints record first: a published prepared generation always has its own.
	if owner.entrypointsTemp != "" {
		if err := replicas.Rename(owner.entrypointsTemp, preparedHTTPEntrypointsLeaf(filepath.Base(owner.path))); err != nil {
			return err
		}
		owner.entrypointsTemp = ""
	}
	if err := replicas.Rename(owner.temp, filepath.Base(owner.path)); err != nil {
		return err
	}
	owner.temp = ""
	return nil
}

func readPrivateHTTPAttachPair(stateDir, leaf string) (input, prepared []byte, inputErr, preparedErr, err error) {
	set, err := readPrivateHTTPAttachSet(stateDir, leaf)
	return set.input, set.prepared, set.inputErr, set.preparedErr, err
}

// privateHTTPAttachSet is a running jail's private launch input, prepared generation and
// entrypoints record, read together under the publication lock.
type privateHTTPAttachSet struct {
	input, prepared, entrypoints          []byte
	inputErr, preparedErr, entrypointsErr error
}

func readPrivateHTTPAttachSet(stateDir, leaf string) (set privateHTTPAttachSet, err error) {
	input, prepared, entrypoints, inputErr, preparedErr, entrypointsErr, err := readPrivateHTTPAttachFiles(stateDir, leaf)
	return privateHTTPAttachSet{input, prepared, entrypoints, inputErr, preparedErr, entrypointsErr}, err
}

func readPrivateHTTPAttachFiles(stateDir, leaf string) (input, prepared, entrypoints []byte, inputErr, preparedErr, entrypointsErr, err error) {
	if leaf == "" || filepath.Base(leaf) != leaf {
		return nil, nil, nil, nil, nil, nil, errors.New("private launch input name is invalid")
	}
	root, err := paths.OpenStateDirRoot(stateDir)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	defer root.Close()
	if !privateHTTPDirOwned(root) {
		return nil, nil, nil, nil, nil, nil, errors.New("private state directory is not owned and mode 0700")
	}
	replicas, err := paths.OpenStateSubdirRoot(root, "replicas", filepath.Join(stateDir, "replicas"))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	defer replicas.Close()
	if !privateHTTPDirOwned(replicas) {
		return nil, nil, nil, nil, nil, nil, errors.New("private replica directory is not owned and mode 0700")
	}
	inputPresent, err := privateHTTPEntryPresent(replicas, leaf)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	preparedPresent, err := privateHTTPEntryPresent(replicas, leaf+".prepared")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	entrypointsPresent, err := privateHTTPEntryPresent(replicas, preparedHTTPEntrypointsLeaf(leaf+".prepared"))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if !inputPresent && !preparedPresent && !entrypointsPresent {
		return nil, nil, nil, os.ErrNotExist, os.ErrNotExist, os.ErrNotExist, nil
	}
	lock, err := replicas.OpenFile(".launch-input.lock", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil, nil, nil, errors.New("private launch-input lock is missing while HTTP identity files exist")
	}
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	defer lock.Close()
	if info, statErr := lock.Stat(); statErr != nil || !privateHTTPFileOwned(info, 0600) {
		return nil, nil, nil, nil, nil, nil, errors.New("private launch-input lock is unsafe")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	input, _, inputErr = readPrivateHTTPFile(replicas, leaf)
	prepared, _, preparedErr = readPrivateHTTPFile(replicas, leaf+".prepared")
	entrypoints, _, entrypointsErr = readPrivateHTTPFile(replicas, preparedHTTPEntrypointsLeaf(leaf+".prepared"))
	return input, prepared, entrypoints, inputErr, preparedErr, entrypointsErr, nil
}

// runningHTTPPreparedExpected scans the host's running call-root scopes for cname. It reports
// prepared when one names an HTTP front that retains a prepared identity, and an error when one
// cannot be verified. No scope for cname is no evidence of an HTTP front at all: (false, nil).
func (o *Options) runningHTTPPreparedExpected(cname string) (prepared bool, err error) {
	sources, err := os.ReadDir(paths.BrokerDir())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("running broker scopes cannot be listed: %w", err)
	}
	for _, source := range sources {
		if !source.IsDir() || !loopholedecl.ValidBrokerSourceName(source.Name()) {
			continue
		}
		dir := filepath.Join(paths.BrokerSourceDir(source.Name()), "scope")
		files, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("broker scope source %q cannot be listed: %w", source.Name(), err)
		}
		for _, file := range files {
			if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
				continue
			}
			scopePath := filepath.Join(dir, file.Name())
			scope, err := brokerscope.ReadFile(scopePath)
			if err != nil {
				return false, fmt.Errorf("broker scope source %q has an unreadable scope record: %w", source.Name(), err)
			}
			if scope.Container != cname || scope.Form != brokerscope.FormCallRoot || scope.CallInput == "" {
				continue
			}
			if filepath.Base(scopePath) != scope.LaunchID+".json" {
				return false, errors.New("running broker scope path does not match its launch identity")
			}
			expected, err := runningScopeHTTPIdentity(cname, source.Name(), scopePath, scope)
			if err != nil {
				return false, err
			}
			if expected {
				return true, nil
			}
		}
	}
	return false, nil
}

// runningScopeHTTPIdentity verifies one running call-root scope's private input. prepared is
// true when it names an HTTP front with a prepared generation; an HTTP front without one, and a
// non-HTTP front, verify as (false, nil).
func runningScopeHTTPIdentity(cname, source, scopePath string, scope brokerscope.File) (prepared bool, err error) {
	inputPath := scope.CallInput
	if !filepath.IsAbs(inputPath) || filepath.Base(inputPath) != cname+".json" || filepath.Base(filepath.Dir(inputPath)) != "replicas" {
		return false, errors.New("running broker scope has an invalid private call-input path")
	}
	inputBytes, _, inputErr, preparedErr, err := readPrivateHTTPAttachPair(filepath.Dir(filepath.Dir(inputPath)), filepath.Base(inputPath))
	if err != nil || inputErr != nil {
		return false, fmt.Errorf("running broker scope %q has no verifiable launch input", scope.LaunchID)
	}
	if err := callbrokerinput.RejectDuplicateLaunchHTTPFrontGeneration(inputBytes); err != nil {
		return false, fmt.Errorf("running broker scope %q has an invalid launch identity", scope.LaunchID)
	}
	var in callbrokerinput.Launch
	if err := json.Unmarshal(inputBytes, &in); err != nil || in.Version != "yolo.call-broker-launch/2" ||
		in.ScopeFile != scopePath || in.Source != source || in.Source != scope.Source || in.Loophole == "" ||
		filepath.Clean(inputPath) != filepath.Clean(filepath.Join(loopholes.StateDirFor(in.Loophole), "replicas", cname+".json")) {
		return false, fmt.Errorf("running broker scope %q does not match its private launch input", scope.LaunchID)
	}
	if preparedErr != nil && !errors.Is(preparedErr, os.ErrNotExist) {
		return false, fmt.Errorf("running broker scope %q has an unsafe prepared identity", scope.LaunchID)
	}
	if in.Front == "http" {
		if in.HTTPFrontGeneration != "" {
			return true, nil
		}
		if preparedErr == nil {
			return false, fmt.Errorf("running broker scope %q has an orphaned prepared identity", scope.LaunchID)
		}
		return false, nil
	}
	if in.HTTPFrontGeneration != "" || preparedErr == nil {
		return false, fmt.Errorf("running broker scope %q carries HTTP identity on a non-HTTP front", scope.LaunchID)
	}
	return false, nil
}

func privateHTTPEntryPresent(root *os.Root, leaf string) (bool, error) {
	_, err := root.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func privateHTTPDirOwned(root *os.Root) bool {
	info, err := root.Stat(".")
	return err == nil && info.IsDir() && info.Mode().Perm() == 0700 && privateHTTPUser(info)
}

func privateHTTPFileOwned(info os.FileInfo, mode os.FileMode) bool {
	return info.Mode().IsRegular() && info.Mode().Perm() == mode && privateHTTPUser(info)
}

func privateHTTPUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}

func readPrivateHTTPFile(root *os.Root, leaf string) ([]byte, os.FileInfo, error) {
	before, err := root.Lstat(leaf)
	if err != nil {
		return nil, nil, err
	}
	if !privateHTTPFileOwned(before, 0600) {
		return nil, nil, errors.New("private HTTP identity file has unsafe ownership, type, or mode")
	}
	file, err := root.OpenFile(leaf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !privateHTTPFileOwned(opened, 0600) {
		return nil, nil, errors.New("private HTTP identity file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, nil, errors.New("private HTTP identity file is unreadable or too large")
	}
	return data, opened, nil
}

// validateAttachedPrivateHTTP verifies a running jail's private HTTP identity against its frozen
// pack tree, and returns the bindings of its selected private clients, read from the entrypoints
// record the keeper published beside the prepared generation. It reads only: it mints, installs
// and refreshes nothing.
func (o *Options) validateAttachedPrivateHTTP(cname, rt, setupName, tree string, packs []*packload.Pack) ([]entrypoint.PrivateClientBinding, error) {
	var bindings []entrypoint.PrivateClientBinding
	for _, module := range packLoopholeDecls(packs) {
		declared, err := loopholedecl.LoadDir(module.Dir)
		if err != nil {
			return nil, fmt.Errorf("running pack %q has an unreadable selected front: %w", module.Pack, err)
		}
		if declared.CallBroker == nil || declared.CallBroker.Front != "http" {
			continue
		}
		plans, err := packload.ResolvePrivateProgramEnv(packs, module.Name, module.Pack, true)
		if err != nil {
			return nil, err
		}
		stateDir := loopholes.StateDirFor(module.Name)
		set, pairErr := readPrivateHTTPAttachSet(stateDir, cname+".json")
		inputBytes, preparedBytes, inputErr, preparedErr := set.input, set.prepared, set.inputErr, set.preparedErr
		if pairErr != nil {
			if errors.Is(pairErr, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("running HTTP front %q has unsafe private launch state: %w", module.Name, pairErr)
		}
		if errors.Is(inputErr, os.ErrNotExist) {
			if errors.Is(preparedErr, os.ErrNotExist) && errors.Is(set.entrypointsErr, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("running HTTP front %q has an orphaned prepared identity", module.Name)
		}
		if inputErr != nil {
			return nil, fmt.Errorf("running HTTP front %q launch input is unsafe: %w", module.Name, inputErr)
		}
		if err := callbrokerinput.RejectDuplicateLaunchHTTPFrontGeneration(inputBytes); err != nil {
			return nil, fmt.Errorf("running HTTP front %q reference is invalid: %w", module.Name, err)
		}
		var in callbrokerinput.Launch
		if err := json.Unmarshal(inputBytes, &in); err != nil || in.Version != "yolo.call-broker-launch/2" ||
			in.Loophole != module.Name || in.Pack != module.Pack || in.Source != declared.CallBroker.Source ||
			in.Front != "http" || in.HTTPBinding == nil || in.HTTPBinding.Pack != module.Pack ||
			in.HTTPBinding.Source != in.Source || in.DeclarationHash != callbrokerinput.DeclarationHash(declared.CallBroker) ||
			in.HTTPBinding.DeclarationHash != in.DeclarationHash || declared.CallBroker.HTTP == nil ||
			!reflect.DeepEqual(in.HTTPBinding.HTTP, *declared.CallBroker.HTTP) ||
			!reflect.DeepEqual(in.HTTPBinding.ReadOverPost, declared.CallBroker.ReadOverPost) {
			return nil, fmt.Errorf("running HTTP front %q launch input no longer matches its staged declaration", module.Name)
		}
		packRoot := ""
		for _, pack := range packs {
			if pack.Name == module.Pack {
				packRoot = pack.Root
				break
			}
		}
		identities, err := callbrokeridentity.Resolve(packRoot, module.Pack, module.Dir, declared.CallBroker)
		if err != nil {
			return nil, fmt.Errorf("running HTTP front %q program identity cannot be verified: %w", module.Name, err)
		}
		if identities == nil {
			identities = map[string]loopholedecl.CallBrokerProgramIdentity{}
		}
		if !maps.Equal(in.ProgramIdentities, identities) {
			return nil, fmt.Errorf("running HTTP front %q program identities no longer match its staged declaration", module.Name)
		}
		if len(plans) == 0 && in.HTTPFrontGeneration == "" && errors.Is(preparedErr, os.ErrNotExist) && errors.Is(set.entrypointsErr, os.ErrNotExist) {
			continue
		}
		if len(plans) == 0 || in.HTTPFrontGeneration == "" || preparedErr != nil {
			return nil, fmt.Errorf("running HTTP front %q lacks its selected prepared identity", module.Name)
		}
		prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(preparedBytes)
		if err != nil {
			return nil, fmt.Errorf("running HTTP front %q prepared identity is invalid: %w", module.Name, err)
		}
		scope, err := brokerscope.ReadFile(in.ScopeFile)
		if err != nil || scope.CallInput != filepath.Join(stateDir, "replicas", cname+".json") ||
			callbrokerinput.ValidatePreparedHTTPClientLaunch(prepared, in, scope.Container, scope.LaunchID, scope.Source, scope.Repos) != nil {
			return nil, fmt.Errorf("running HTTP front %q prepared identity does not match its host scope", module.Name)
		}
		expected := make([]callbrokerinput.PreparedHTTPClientPlan, len(plans))
		for i, plan := range plans {
			expected[i] = callbrokerinput.PreparedHTTPClientPlan{Bin: plan.Bin, Pack: plan.Pack, Front: plan.Front,
				RecipePack: plan.RecipePack, RecipeIdentity: plan.RecipeIdentity, TokenVariable: plan.TokenVariable,
				URLVariable: plan.URLVariable, TrustVariable: plan.TrustVariable}
		}
		if prepared.Container != cname || prepared.FrontJailID != filepath.Base(hostServiceSocketsDir(cname, o.IsMacOS)) ||
			prepared.Runtime != rt || prepared.Setup != setupName || prepared.PackTree != tree ||
			!reflect.DeepEqual(prepared.Clients, expected) {
			return nil, fmt.Errorf("running HTTP front %q prepared identity does not match its frozen running pack tree", module.Name)
		}
		if set.entrypointsErr != nil {
			return nil, fmt.Errorf("running HTTP front %q has no verifiable private client entrypoints record", module.Name)
		}
		entrypoints, err := callbrokerinput.DecodePreparedHTTPClientEntrypoints(set.entrypoints)
		if err == nil {
			err = callbrokerinput.ValidatePreparedHTTPClientEntrypoints(entrypoints, prepared)
		}
		if err != nil {
			return nil, fmt.Errorf("running HTTP front %q private client entrypoints do not match its prepared generation: %w", module.Name, err)
		}
		for _, c := range entrypoints.Clients {
			bindings = append(bindings, c.Binding)
		}
	}
	return bindings, nil
}

func cloneProgramIdentities(in map[string]loopholedecl.CallBrokerProgramIdentity) map[string]loopholedecl.CallBrokerProgramIdentity {
	out := make(map[string]loopholedecl.CallBrokerProgramIdentity, len(in))
	for name, identity := range in {
		out[name] = identity
	}
	return out
}

// trackPrivateClientBindings keeps the bindings of a committed entrypoints record for the
// container's environment (insertPrivateClientEnv).
func trackPrivateClientBindings(o *Options, r callbrokerinput.PreparedHTTPClientEntrypoints) {
	launchInputMu.Lock()
	defer launchInputMu.Unlock()
	for _, c := range r.Clients {
		o.privateClientBindings = append(o.privateClientBindings, c.Binding)
	}
}

// insertPrivateClientEnv puts this launch's bound private clients (entrypoint.PrivateHTTPClientsEnv)
// into the container's argv before the image, as insertHostServiceEnv does a service's endpoint.
// No binding inserts nothing, so an unactivated launch's argv is unchanged.
func (o *Options) insertPrivateClientEnv(runCmd []string, imageRef string) ([]string, error) {
	launchInputMu.Lock()
	bindings := slices.Clone(o.privateClientBindings)
	launchInputMu.Unlock()
	if len(bindings) == 0 {
		return runCmd, nil
	}
	value, err := entrypoint.MarshalPrivateClients(bindings)
	if err != nil {
		return nil, err
	}
	idx := indexOfSlice(runCmd, imageRef)
	if idx < 0 {
		return nil, errors.New("the container argv has no image to bind private clients before")
	}
	return insertStrsAt(runCmd, idx, []string{"-e", entrypoint.PrivateHTTPClientsEnv + "=" + value}), nil
}

func trackPreparedHTTPGeneration(o *Options, owner preparedHTTPGenerationOwner) {
	launchInputMu.Lock()
	defer launchInputMu.Unlock()
	if o.preparedHTTPGenerationOwners == nil {
		o.preparedHTTPGenerationOwners = map[string]preparedHTTPGenerationOwner{}
	}
	o.preparedHTTPGenerationOwners[owner.path] = owner
}

func removePreparedHTTPGenerationFile(owner preparedHTTPGenerationOwner) bool {
	if owner.path == "" || owner.file == nil {
		return false
	}
	root, err := paths.OpenStateDirRoot(filepath.Dir(owner.path))
	if err != nil {
		return false
	}
	defer root.Close()
	leaf := filepath.Base(owner.path)
	if owner.temp != "" {
		leaf = owner.temp
	}
	if owner.entrypointsFile != nil {
		entrypointsLeaf := preparedHTTPEntrypointsLeaf(filepath.Base(owner.path))
		if owner.entrypointsTemp != "" {
			entrypointsLeaf = owner.entrypointsTemp
		}
		if !removeOwnedPrivateHTTPLeaf(root, entrypointsLeaf, owner.entrypointsFile) {
			return false
		}
	}
	return removeOwnedPrivateHTTPLeaf(root, leaf, owner.file)
}

// removeOwnedPrivateHTTPLeaf removes leaf only while it is still the inode owned, so a
// replacement is preserved. A leaf already gone counts as removed.
func removeOwnedPrivateHTTPLeaf(root *os.Root, leaf string, owned os.FileInfo) bool {
	current, err := root.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !current.Mode().IsRegular() || current.Mode().Perm() != 0o600 || !os.SameFile(owned, current) {
		return false
	}
	file, err := root.OpenFile(leaf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	opened, err := file.Stat()
	_ = file.Close()
	if err != nil || !os.SameFile(owned, opened) {
		return false
	}
	return root.Remove(leaf) == nil
}

func (o *Options) removePreparedHTTPGenerations(cname string) {
	launchInputMu.Lock()
	var owned []preparedHTTPGenerationOwner
	for path, owner := range o.preparedHTTPGenerationOwners {
		if owner.container == cname {
			owned = append(owned, owner)
			delete(o.preparedHTTPGenerationOwners, path)
		}
	}
	launchInputMu.Unlock()
	for _, owner := range owned {
		removePreparedHTTPGenerationFile(owner)
	}
	removePreparedHTTPOwnership(cname)
}
