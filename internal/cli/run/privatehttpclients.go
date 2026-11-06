package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
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
	entries, recorded, err := packload.ReadPackTreeRecord(tree)
	if err != nil {
		return nil, err
	}
	if !recorded {
		return nil, nil
	}
	packs := make([]*packload.Pack, 0, len(entries))
	for _, entry := range entries {
		root := filepath.Join(tree, filepath.FromSlash(entry.Dir))
		p, problems := packload.LoadDir(root, entry.Name)
		if p == nil || len(problems) != 0 {
			return nil, fmt.Errorf("selected staged pack %q could not be read for private client metadata: %v", entry.Name, problems)
		}
		if p.Name != entry.Name {
			return nil, fmt.Errorf("selected staged pack record changed for %q", entry.Name)
		}
		packs = append(packs, p)
	}
	return packs, nil
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
