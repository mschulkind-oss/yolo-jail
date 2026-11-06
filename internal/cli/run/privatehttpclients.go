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
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokeridentity"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
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
		owner.file, owner.generation = existing, old.Generation
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
	data, err := json.Marshal(prepared)
	if err != nil {
		return "", owner, false, err
	}
	temp := ".prepared-http-client-" + prepared.Generation
	file, err := replicas.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", owner, false, err
	}
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		owner.file, err = file.Stat()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = replicas.Remove(temp)
		return "", owner, false, err
	}
	owner.temp = temp
	owner.generation = prepared.Generation
	return prepared.Generation, owner, true, nil
}

func publishPreparedHTTPGeneration(replicas *os.Root, owner *preparedHTTPGenerationOwner) error {
	if owner == nil || owner.temp == "" {
		return nil
	}
	if err := replicas.Rename(owner.temp, filepath.Base(owner.path)); err != nil {
		return err
	}
	owner.temp = ""
	return nil
}

func readPrivateHTTPAttachPair(stateDir, leaf string) (input, prepared []byte, inputErr, preparedErr, err error) {
	if leaf == "" || filepath.Base(leaf) != leaf {
		return nil, nil, nil, nil, errors.New("private launch input name is invalid")
	}
	root, err := paths.OpenStateDirRoot(stateDir)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer root.Close()
	if !privateHTTPDirOwned(root) {
		return nil, nil, nil, nil, errors.New("private state directory is not owned and mode 0700")
	}
	replicas, err := paths.OpenStateSubdirRoot(root, "replicas", filepath.Join(stateDir, "replicas"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer replicas.Close()
	if !privateHTTPDirOwned(replicas) {
		return nil, nil, nil, nil, errors.New("private replica directory is not owned and mode 0700")
	}
	lock, err := replicas.OpenFile(".launch-input.lock", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer lock.Close()
	if info, statErr := lock.Stat(); statErr != nil || !privateHTTPFileOwned(info, 0600) {
		return nil, nil, nil, nil, errors.New("private launch-input lock is unsafe")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return nil, nil, nil, nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	input, _, inputErr = readPrivateHTTPFile(replicas, leaf)
	prepared, _, preparedErr = readPrivateHTTPFile(replicas, leaf+".prepared")
	return input, prepared, inputErr, preparedErr, nil
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

func (o *Options) validateAttachedPrivateHTTP(cname, rt, setupName, tree string, packs []*packload.Pack) error {
	for _, module := range packLoopholeDecls(packs) {
		declared, err := loopholedecl.LoadDir(module.Dir)
		if err != nil {
			return fmt.Errorf("running pack %q has an unreadable selected front: %w", module.Pack, err)
		}
		if declared.CallBroker == nil || declared.CallBroker.Front != "http" {
			continue
		}
		plans, err := packload.ResolvePrivateProgramEnv(packs, module.Name, module.Pack, true)
		if err != nil {
			return err
		}
		stateDir := loopholes.StateDirFor(module.Name)
		inputBytes, preparedBytes, inputErr, preparedErr, pairErr := readPrivateHTTPAttachPair(stateDir, cname+".json")
		if pairErr != nil {
			if errors.Is(pairErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("running HTTP front %q has unsafe private launch state: %w", module.Name, pairErr)
		}
		if errors.Is(inputErr, os.ErrNotExist) {
			if errors.Is(preparedErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("running HTTP front %q has an orphaned prepared identity", module.Name)
		}
		if inputErr != nil {
			return fmt.Errorf("running HTTP front %q launch input is unsafe: %w", module.Name, inputErr)
		}
		if err := callbrokerinput.RejectDuplicateLaunchHTTPFrontGeneration(inputBytes); err != nil {
			return fmt.Errorf("running HTTP front %q reference is invalid: %w", module.Name, err)
		}
		var in callbrokerinput.Launch
		if err := json.Unmarshal(inputBytes, &in); err != nil || in.Version != "yolo.call-broker-launch/2" ||
			in.Loophole != module.Name || in.Pack != module.Pack || in.Source != declared.CallBroker.Source ||
			in.Front != "http" || in.HTTPBinding == nil || in.HTTPBinding.Pack != module.Pack ||
			in.HTTPBinding.Source != in.Source || in.DeclarationHash != callbrokerinput.DeclarationHash(declared.CallBroker) ||
			in.HTTPBinding.DeclarationHash != in.DeclarationHash || declared.CallBroker.HTTP == nil ||
			!reflect.DeepEqual(in.HTTPBinding.HTTP, *declared.CallBroker.HTTP) ||
			!reflect.DeepEqual(in.HTTPBinding.ReadOverPost, declared.CallBroker.ReadOverPost) {
			return fmt.Errorf("running HTTP front %q launch input no longer matches its staged declaration", module.Name)
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
			return fmt.Errorf("running HTTP front %q program identity cannot be verified: %w", module.Name, err)
		}
		if identities == nil {
			identities = map[string]loopholedecl.CallBrokerProgramIdentity{}
		}
		if !maps.Equal(in.ProgramIdentities, identities) {
			return fmt.Errorf("running HTTP front %q program identities no longer match its staged declaration", module.Name)
		}
		if len(plans) == 0 && in.HTTPFrontGeneration == "" && errors.Is(preparedErr, os.ErrNotExist) {
			continue
		}
		if len(plans) == 0 || in.HTTPFrontGeneration == "" || preparedErr != nil {
			return fmt.Errorf("running HTTP front %q lacks its selected prepared identity", module.Name)
		}
		prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(preparedBytes)
		if err != nil {
			return fmt.Errorf("running HTTP front %q prepared identity is invalid: %w", module.Name, err)
		}
		scope, err := brokerscope.ReadFile(in.ScopeFile)
		if err != nil || scope.CallInput != filepath.Join(stateDir, "replicas", cname+".json") ||
			callbrokerinput.ValidatePreparedHTTPClientLaunch(prepared, in, scope.Container, scope.LaunchID, scope.Source, scope.Repos) != nil {
			return fmt.Errorf("running HTTP front %q prepared identity does not match its host scope", module.Name)
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
			return fmt.Errorf("running HTTP front %q prepared identity does not match its frozen running pack tree", module.Name)
		}
	}
	return nil
}

func cloneProgramIdentities(in map[string]loopholedecl.CallBrokerProgramIdentity) map[string]loopholedecl.CallBrokerProgramIdentity {
	out := make(map[string]loopholedecl.CallBrokerProgramIdentity, len(in))
	for name, identity := range in {
		out[name] = identity
	}
	return out
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
	current, err := root.Lstat(leaf)
	if err != nil || !current.Mode().IsRegular() || current.Mode().Perm() != 0o600 || !os.SameFile(owner.file, current) {
		return false
	}
	file, err := root.OpenFile(leaf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	opened, err := file.Stat()
	_ = file.Close()
	if err != nil || !os.SameFile(owner.file, opened) {
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
}
