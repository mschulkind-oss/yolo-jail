package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// LaunchInputContext contains gate-approved in-memory values; it exposes no config path to reload.
// Core writes the producer's bytes and binds the resulting private file to the one selected host
// daemon that declared it.
type LaunchInputContext struct {
	Workspace string
	// Home is the launch's workspace home (Options.homeDir; docs/design/named-setups.md §9).
	Home                string
	Runtime             string
	PackTree            string
	Container           string
	ApprovedScopes      map[string][]config.ScopeRepo
	EffectiveMCPServers *jsonx.OrderedMap
	UserOnlyMCPServers  *jsonx.OrderedMap
	MCPScopes           map[string]packload.Scope
	MCPProven           bool
	CallBroker          *CallBrokerLaunchInput
}

// CallBrokerLaunchInput is the shared private launch-input contract consumed
// by core's host operation handler, never an authority carried by the jail.
type CallBrokerLaunchInput = callbrokerinput.Launch

// selectedCallBrokerLaunchInput refuses missing gate evidence and unresolved staged
// programs. Never carry the raw declaration: it also contains credential locations.
func selectedCallBrokerLaunchInput(lp *loopholes.Loophole, approved map[string][]config.ScopeRepo, cells map[string]map[string]config.CallBrokerCellPolicy, grants map[string]map[string]config.LoopholeGrantStanding, httpBindings ...map[string]config.ApprovedCallBrokerHTTP) (*CallBrokerLaunchInput, error) {
	if lp == nil || lp.Source != loopholes.SourcePack || !lp.Enabled || lp.CallBroker == nil || lp.CallBroker.ProgramIdentityError != "" {
		return nil, errors.New("selected staged call broker is unavailable")
	}
	roots, ok := approved[lp.Name]
	if !ok {
		return nil, errors.New("gate-approved call broker roots are unavailable")
	}
	policy, ok := cells[lp.Name]
	if !ok || len(policy) != len(lp.CallBroker.Cells) {
		return nil, errors.New("gate-approved call broker effective cells are unavailable")
	}
	for _, cell := range lp.CallBroker.Cells {
		setting, exists := policy[cell.Name]
		if !exists || len(setting.Tables) != len(cell.Tables) {
			return nil, errors.New("gate-approved call broker effective cell is missing")
		}
		for _, table := range cell.Tables {
			value, exists := setting.Tables[table]
			if !exists || (value.Decision != "run" && value.Decision != "ask" && value.Decision != "refuse") ||
				(value.Request != "true" && value.Request != "false" && value.Request != "ask" && value.Request != "absent") ||
				(value.Ceiling != "always" && value.Ceiling != "on_request" && value.Ceiling != "off") {
				return nil, errors.New("gate-approved call broker effective table is missing or invalid")
			}
		}
	}
	b := lp.CallBroker
	var httpBinding *config.ApprovedCallBrokerHTTP
	if b.Front == "http" {
		if b.HTTP == nil || len(httpBindings) != 1 {
			return nil, errors.New("approved HTTP binding unavailable; approve the selected declaration at the config gate and retry")
		}
		bound, ok := httpBindings[0][lp.Name]
		if !ok || bound.Pack != lp.Pack || bound.Source != b.Source || bound.DeclarationHash != callbrokerinput.DeclarationHash(b) || !reflect.DeepEqual(bound.HTTP, *b.HTTP) || !reflect.DeepEqual(bound.ReadOverPost, b.ReadOverPost) {
			return nil, errors.New("approved HTTP binding changed or unavailable; approve the selected declaration at the config gate and retry")
		}
		bound.HTTP.RequestHeaders = slices.Clone(bound.HTTP.RequestHeaders)
		bound.ReadOverPost = slices.Clone(bound.ReadOverPost)
		httpBinding = &bound
	}
	for _, token := range append(append([]string{}, b.Classifier...), b.Executor) {
		if strings.HasPrefix(token, "{program:") {
			name := strings.TrimSuffix(strings.TrimPrefix(token, "{program:"), "}")
			identity, found := b.ProgramIdentities[name]
			if !found || identity.Pack != lp.Pack || identity.SHA256 == "" || identity.Path == "" {
				return nil, errors.New("selected staged call broker program identity is unavailable")
			}
		}
	}
	identities := make(map[string]loopholedecl.CallBrokerProgramIdentity, len(b.ProgramIdentities))
	for name, identity := range b.ProgramIdentities {
		identities[name] = identity
	}
	declaredCells := make([]loopholedecl.CallBrokerCell, len(b.Cells))
	for i, cell := range b.Cells {
		declaredCells[i] = cell
		declaredCells[i].Tables = append([]string(nil), cell.Tables...)
		declaredCells[i].Default = make(map[string]bool, len(cell.Default))
		for table, value := range cell.Default {
			declaredCells[i].Default[table] = value
		}
	}
	lookups := append([]loopholedecl.CallBrokerLookup(nil), b.Lookups...)
	for i := range lookups {
		lookups[i].CLI = append([]string(nil), b.Lookups[i].CLI...)
	}
	readDeclared := false
	for _, grant := range lp.Grants {
		if grant.Kind == "read" {
			readDeclared = true
			break
		}
	}
	readGrant := config.LoopholeGrantStanding{}
	if readDeclared {
		readGrant = grants[lp.Name]["read"]
	}
	input := &CallBrokerLaunchInput{Version: "yolo.call-broker-launch/2", Loophole: lp.Name, Pack: lp.Pack, DeclarationHash: callbrokerinput.DeclarationHash(lp.CallBroker),
		Source: b.Source, Front: b.Front, Program: b.Program, Classifier: append([]string(nil), b.Classifier...),
		Executor: b.Executor, HostEnvNames: append([]string(nil), b.EnvFromHost...), HTTPBinding: httpBinding, ProgramIdentities: identities,
		Resources: append([]loopholedecl.CallBrokerResource(nil), b.Resources...),
		Cells:     declaredCells, EffectiveCells: cloneCellPolicy(policy),
		ReadStanding:      readDeclared && readGrant.Standing == config.LoopholeGrantGranted,
		ReadGrantStanding: readGrant.Standing, ReadWithheld: readGrant.Withheld,
		Lookups: lookups}
	for _, root := range roots {
		input.Roots = append(input.Roots, root.Repo)
	}
	return input, nil
}

func cloneHTTPBindings(in map[string]config.ApprovedCallBrokerHTTP) map[string]config.ApprovedCallBrokerHTTP {
	if in == nil {
		return nil
	}
	out := make(map[string]config.ApprovedCallBrokerHTTP, len(in))
	for name, binding := range in {
		binding.HTTP.RequestHeaders = slices.Clone(binding.HTTP.RequestHeaders)
		binding.ReadOverPost = slices.Clone(binding.ReadOverPost)
		out[name] = binding
	}
	return out
}

// LaunchInputProducer is a core-owned producer for one named host-daemon input contract.
type LaunchInputProducer struct {
	Produce func(LaunchInputContext) ([]byte, error)
	// StateDir is a core-owned host state destination, never a manifest/config path.
	StateDir func(LaunchInputContext) string
	// Report is invoked only after the private input was published successfully.
	Report func(*Options, LaunchInputContext, []byte)
}

// launchInputProducers contains only producers whose inputs core can prove and whose
// destination is host-private. A pack's manifest can select, but cannot define, a producer.
// The call-broker producer serves the core daemon with a read/decision snapshot, not write authority.
func launchInputProducers() map[string]LaunchInputProducer {
	return map[string]LaunchInputProducer{
		"mcp-auth-share": {
			Produce:  produceMCPAuthShare,
			StateDir: func(LaunchInputContext) string { return loopholes.StateDirFor("mcp-auth-broker") },
			Report:   (*Options).noteMCPAuthShare,
		},
		"call-broker-scope": {
			Produce: func(ctx LaunchInputContext) ([]byte, error) {
				if ctx.CallBroker == nil || ctx.CallBroker.Version != "yolo.call-broker-launch/2" {
					return nil, errors.New("selected call broker scope is unavailable; retry the launch with its pack selected")
				}
				return json.Marshal(ctx.CallBroker)
			},
			StateDir: func(ctx LaunchInputContext) string {
				if ctx.CallBroker == nil || ctx.CallBroker.Loophole == "" {
					return ""
				}
				return loopholes.StateDirFor(ctx.CallBroker.Loophole)
			},
		},
	}
}

func cloneApprovedScopes(in map[string][]config.ScopeRepo) map[string][]config.ScopeRepo {
	out := make(map[string][]config.ScopeRepo, len(in))
	for name, repos := range in {
		out[name] = append([]config.ScopeRepo(nil), repos...)
	}
	return out
}

var launchInputMu sync.Mutex

// bindCallInput is the host-side scope binding performed after the private file is published.
// A variable keeps the publication failure path deterministic in its caller test.
var bindCallInput = brokerscope.BindCallInput

func (o *Options) writeLaunchInput(lp *loopholes.Loophole, cname string) bool {
	if lp == nil {
		return true
	}
	launchInput := ""
	if lp.HostDaemon != nil {
		launchInput = lp.HostDaemon.LaunchInput
	} else if lp.CallBroker != nil {
		// call_broker excludes manifest host_daemon. Core owns its runtime command
		// and publishes this input only after the same selected origin gate.
		launchInput = "call-broker-scope"
	}
	if launchInput == "" {
		return true
	}
	registry := o.launchInputProducers
	if registry == nil {
		registry = launchInputProducers()
	}
	producer, ok := registry[launchInput]
	if !ok {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' declares unknown launch input producer '" +
			launchInput + "'; it was not started. Use a yolo build that registers this core-owned producer, then retry.[/red]")
		return false
	}
	frozen := LaunchInputContext{Container: cname, Workspace: o.Workspace, Home: o.homeDir(), Runtime: o.runtime, PackTree: o.packTree, ApprovedScopes: cloneApprovedScopes(o.approvedScopes)}
	if launchInput == "call-broker-scope" {
		input, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells, o.approvedLoopholeGrants, o.approvedHTTPBindings)
		if err != nil {
			o.pr(o.Stderr).print("[red]Call broker '" + lp.Name + "' has no selected gate-approved scope or program identity; it was not started. Retry with its pack selected and approve its scope at the gate.[/red]")
			return false
		}
		input.ScopeFile = o.scopeFiles[lp.Name]
		if grant := o.settingsSnapshots[lp.Name]; grant != nil {
			input.GrantFile = grant.path
		}
		if o.servicesSession != nil {
			input.FrontJailID = filepath.Base(o.servicesSession.dir)
		} else {
			input.FrontJailID = filepath.Base(hostServiceSocketsDir(cname, o.IsMacOS))
		}
		requiresReadGrant := false
		for _, grant := range lp.Grants {
			if grant.Kind == "read" {
				requiresReadGrant = true
			}
		}
		if input.ScopeFile == "" || input.FrontJailID == "" || requiresReadGrant && input.GrantFile == "" {
			o.pr(o.Stderr).print("[red]Call broker has no host-bound live scope; retry the launch.[/red]")
			return false
		}
		frozen.CallBroker = input
	}

	if o.launchInputMCP.Proven {
		effective, effectiveErr := jsonx.Decode([]byte(o.launchInputMCP.EffectiveJSON))
		userOnly, userErr := jsonx.Decode([]byte(o.launchInputMCP.UserOnlyJSON))
		if effectiveErr != nil || userErr != nil {
			o.pr(o.Stderr).print("[red]Host service input composition is invalid; it was not started. Retry the launch.[/red]")
			return false
		}
		frozen.EffectiveMCPServers, _ = effective.(*jsonx.OrderedMap)
		frozen.UserOnlyMCPServers, _ = userOnly.(*jsonx.OrderedMap)
		if frozen.EffectiveMCPServers == nil || frozen.UserOnlyMCPServers == nil {
			o.pr(o.Stderr).print("[red]Host service input composition is incomplete; it was not started. Retry the launch.[/red]")
			return false
		}
		frozen.MCPProven = true
		frozen.MCPScopes = packload.MCPServerScopes(frozen.EffectiveMCPServers, frozen.UserOnlyMCPServers)
	}
	if producer.Produce == nil || producer.StateDir == nil || len(o.launchInputMountArgv) == 0 {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' has no verified private launch-input destination and complete jail mount argv; it was not started. Wire the core producer and full assembled mount topology before retrying.[/red]")
		return false
	}
	deferPreparedHTTPProducer := frozen.CallBroker != nil && frozen.CallBroker.Front == "http" && frozen.CallBroker.HTTPBinding != nil
	var data []byte
	stateDir := producer.StateDir(frozen)
	if stateDir == "" || !filepath.IsAbs(stateDir) || filepath.Base(cname) != cname || cname == "." || cname == ".." {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' has no valid core-owned private destination; it was not started. Fix the core producer and retry.[/red]")
		return false
	}
	// FRESH HOME: the daemon creates its own state directory, but only after this input is
	// proven and published, so the first launch on a machine finds none. Create the core-owned
	// directory, and only that one, privately before the proof resolves it.
	if err := ensureCoreLaunchInputStateDir(lp.Name, stateDir); err != nil {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not create its private host state " + richtext.Escape(stateDir) +
			": " + richtext.Escape(err.Error()) + "; it was not started. Fix host state-directory permissions and retry.[/red]")
		return false
	}
	if deferPreparedHTTPProducer {
		if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' cannot retain its prepared HTTP ownership; it was not started. Repair the host state directory and retry.[/red]")
			return false
		}
		o.preparedHTTPOwnershipFile = preparedHTTPOwnershipPath(cname)
	}
	dest := filepath.Join(stateDir, "replicas", cname+".json")
	pathsToProve := []string{dest}
	if ownership := o.preparedHTTPOwnershipFile; ownership != "" {
		pathsToProve = append(pathsToProve, ownership)
	}
	if deferPreparedHTTPProducer {
		pathsToProve = append(pathsToProve, dest+".prepared")
	}
	for _, privatePath := range pathsToProve {
		if err := launchInputOutsideJail(privatePath, o.launchInputMountArgv); err != nil {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' cannot prove its private launch input " + richtext.Escape(privatePath) +
				" is outside every jail mount (" + richtext.Escape(err.Error()) + "); it was not started. Remove the overlapping mount, or create the missing path, and retry.[/red]")
			return false
		}
	}
	if !deferPreparedHTTPProducer {
		var produceErr error
		data, produceErr = producer.Produce(frozen)
		if produceErr != nil {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not build its approved launch input; it was not started. Retry the launch after resolving the core input producer failure.[/red]")
			return false
		}
		if len(data) == 0 {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' produced an empty launch input; it was not started. Fix the producer and retry.[/red]")
			return false
		}
	}
	root, err := paths.OpenStateDirRoot(stateDir)
	if err == nil {
		var stateInfo os.FileInfo
		stateInfo, err = root.Stat(".")
		if err == nil && stateInfo.Mode().Perm() != 0o700 {
			err = errors.New("state directory is not private")
		}
	}
	if err == nil {
		err = root.Mkdir("replicas", 0o700)
		if errors.Is(err, os.ErrExist) {
			err = nil
		}
	}
	var replicas *os.Root
	if err == nil {
		replicas, err = paths.OpenStateSubdirRoot(root, "replicas", filepath.Join(stateDir, "replicas"))
		if err == nil {
			var replicaInfo os.FileInfo
			replicaInfo, err = replicas.Stat(".")
			if err == nil && replicaInfo.Mode().Perm() != 0o700 {
				err = errors.New("replica directory is not private")
			}
		}
	}
	if root != nil {
		defer root.Close()
	}
	if err != nil {
		if replicas != nil {
			_ = replicas.Close()
		}
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not open its private host state: " + err.Error() + "; it was not started. Fix host state-directory permissions and retry.[/red]")
		return false
	}
	defer replicas.Close()
	// All publishers and cleaners of this destination take the same advisory lock.
	lock, err := replicas.OpenFile(".launch-input.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err == nil {
		var info os.FileInfo
		info, err = lock.Stat()
		if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o600) {
			err = errors.New("unsafe lock file")
		}
	}
	if err == nil {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		if lock != nil {
			lock.Close()
		}
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not lock its private input; it was not started. Fix host state-directory permissions and retry.[/red]")
		return false
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); _ = lock.Close() }()
	leaf := cname + ".json"
	var preparedOwner preparedHTTPGenerationOwner
	preparedCreated, preparedCommitted := false, false
	ownershipCreated, preparedOwnerRecorded := false, false
	setupName := o.Setup.Name
	if setupName == "" {
		setupName = o.SetupName
	}
	// ROLLBACK, in the reverse of the order below: the reference this launch added, then the
	// generation file it created (at its temp name or its final one, by inode), then the empty
	// ownership record it created. A reused generation (created=false, already referenced) and a
	// record an earlier service of this launch wrote are not this call's to remove.
	defer func() {
		if preparedCommitted {
			return
		}
		if preparedOwnerRecorded {
			removePreparedHTTPGenerationOwnership(preparedOwner, preparedCreated)
		}
		if preparedCreated {
			removePreparedHTTPGenerationFile(preparedOwner)
		}
		if ownershipCreated {
			removeEmptyPreparedHTTPOwnership(cname, o.runtime, setupName, o.packTree)
		}
	}()
	// OWNERSHIP FIRST: the record exists before any prepared generation does, and names a
	// generation before it is published at its final path, so a kill at any point leaves nothing
	// published that no record names (retireStalePreparedHTTPOwnership reclaims what it does name).
	if deferPreparedHTTPProducer {
		created, err := o.ensurePreparedHTTPOwnership(cname)
		if err != nil {
			var stale *stalePreparedHTTPOwnershipError
			if errors.As(err, &stale) && len(stale.unverified) != 0 {
				// The container is KNOWN gone, and retirement still kept these files because their
				// bytes or permissions no longer match the record: every later launch would keep
				// them too, so the next step is removing them by hand, never launching again.
				o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' found the prepared HTTP ownership record " + richtext.Escape(stale.path) +
					" left by an earlier launch of " + cname + ". That launch's container is gone, but yolo did not delete these files the record names, " +
					"because their bytes or permissions no longer match it and yolo deletes only what it can verify: " + richtext.Escape(strings.Join(stale.unverified, ", ")) +
					". It was not started. Check that nothing else uses them, remove them and the record by hand (" +
					richtext.Escape(manualRemoveCommand(append([]string{stale.path}, stale.unverified...))) + "), then launch again.[/red]")
			} else if errors.As(err, &stale) && stale.runtime != o.runtime {
				o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' found the prepared HTTP ownership record " + richtext.Escape(stale.path) +
					" left by a launch of " + cname + " under the " + richtext.Escape(stale.runtime) + " runtime; this launch uses " + richtext.Escape(o.runtime) +
					", which cannot tell whether that container is still running, so the record was kept and the service was not started. " +
					"Check that no container named " + cname + " exists (" + richtext.Escape(existingContainerListCommand(stale.runtime, cname)) + "), remove one with " +
					richtext.Escape(containerRemoveCommand(stale.runtime, cname)) + " if it does, then remove the record and the files it names (" +
					richtext.Escape(manualRemoveCommand(stale.files)) + ") and launch again.[/red]")
			} else if errors.As(err, &stale) {
				o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' found the prepared HTTP ownership record " + richtext.Escape(stale.path) +
					" left by an earlier launch of " + cname + ", and that launch's container was not confirmed gone; it was not started. " +
					"Check that no container named " + cname + " exists (" + richtext.Escape(existingContainerListCommand(o.runtime, cname)) + "), remove one with " +
					richtext.Escape(containerRemoveCommand(o.runtime, cname)) + " if it does, then launch again: a launch that sees the container gone retires the record.[/red]")
			} else {
				o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' cannot establish its prepared HTTP ownership record " +
					richtext.Escape(preparedHTTPOwnershipPath(cname)) + ": " + richtext.Escape(err.Error()) + "; it was not started. Repair or remove that file and retry.[/red]")
			}
			return false
		}
		ownershipCreated = created
		if foreign, foreignRuntime := sweepDeadLaunchPreparedHTTP(replicas, cname, o.runtime, o.packTree); foreign != "" {
			foreignPath := filepath.Join(stateDir, "replicas", foreign)
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' found the prepared HTTP identity " + richtext.Escape(foreignPath) +
				", staged by a launch of " + cname + " under the " + richtext.Escape(foreignRuntime) + " runtime; this launch uses " + richtext.Escape(o.runtime) +
				", which cannot tell whether that container is still running, so the file was kept and the service was not started. " +
				"Check that no container named " + cname + " exists (" + richtext.Escape(existingContainerListCommand(foreignRuntime, cname)) + "), remove one with " +
				richtext.Escape(containerRemoveCommand(foreignRuntime, cname)) + " if it does, then remove the file (" +
				richtext.Escape(manualRemoveCommand([]string{foreignPath})) + ") and launch again.[/red]")
			return false
		}
	}
	if frozen.CallBroker != nil {
		reference, owner, created, prepareErr := o.prepareHTTPClientGeneration(lp, frozen, frozen.CallBroker, replicas, stateDir, leaf)
		if prepareErr != nil {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not stage its selected private HTTP identity: " + richtext.Escape(prepareErr.Error()) + "; it was not started. Repair the selected pack declaration and retry.[/red]")
			return false
		}
		preparedOwner, preparedCreated = owner, created
		frozen.CallBroker.HTTPFrontGeneration = reference
	}
	if deferPreparedHTTPProducer && preparedOwner.file != nil {
		added, err := o.recordPreparedHTTPGeneration(preparedOwner)
		if err != nil {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not retain its prepared HTTP ownership in " +
				richtext.Escape(preparedHTTPOwnershipPath(cname)) + " before input production; it was not started. Repair the host state directory and retry.[/red]")
			return false
		}
		preparedOwnerRecorded = added
		if err := publishPreparedHTTP(replicas, &preparedOwner); err != nil {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not publish its prepared HTTP identity; it was not started. Repair private host state and retry.[/red]")
			return false
		}
	}
	if deferPreparedHTTPProducer {
		var produceErr error
		data, produceErr = producer.Produce(frozen)
		if produceErr != nil {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not build its approved launch input; it was not started. Retry the launch after resolving the core input producer failure.[/red]")
			return false
		}
		if len(data) == 0 {
			o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' produced an empty launch input; it was not started. Fix the producer and retry.[/red]")
			return false
		}
	}
	// A fresh temp inode and rename replace a predecessor atomically; no in-place truncation.
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not create its private input; it was not started. Retry the launch.[/red]")
		return false
	}
	temp := ".launch-input-" + hex.EncodeToString(nonce[:])
	file, err := replicas.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not create its private input; it was not started. Fix host state-directory permissions and retry.[/red]")
		return false
	}
	defer func() { _ = replicas.Remove(temp) }()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	var published os.FileInfo
	if err == nil {
		published, err = file.Stat()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = replicas.Rename(temp, leaf)
	}
	if err != nil {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not publish its private input; it was not started. Fix host state-directory permissions and retry.[/red]")
		return false
	}
	path := filepath.Join(stateDir, "replicas", leaf)
	trackLaunchInput(o, lp.Name, path, published)
	if frozen.CallBroker != nil {
		bound, err := brokerscope.ReadFile(frozen.CallBroker.ScopeFile)
		if err == nil {
			err = bindCallInput(frozen.CallBroker.ScopeFile, bound, path)
		}
		if err != nil {
			// Release the publication lock before the inode-guarded cleaner takes it.
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			_ = lock.Close()
			o.removeLaunchInput(lp.Name, path, published)
			o.pr(o.Stderr).print("[red]Call broker cannot bind its private input to the host scope; retry the launch.[/red]")
			return false
		}
	}
	if preparedOwner.file != nil {
		trackPreparedHTTPGeneration(o, preparedOwner)
		trackPrivateClientBindings(o, preparedOwner.entrypoints)
	}
	preparedCommitted = true
	if producer.Report != nil {
		launchInputMu.Lock()
		if o.launchInputReports == nil {
			o.launchInputReports = make(map[string]func())
		}
		o.launchInputReports[lp.Name] = func() { producer.Report(o, frozen, data) }
		launchInputMu.Unlock()
	}
	return true
}

// ensureCoreLaunchInputStateDir creates stateDir 0700 when, and only when, it is the loophole's
// own directory directly under the core-owned state root (loopholes.StateDirFor). Any other
// destination is left alone and must already exist. The leaf is made through an os.Root on the
// state root, so a link planted at the leaf is never followed: OpenStateSubdirRoot refuses it.
// The state root itself may be a link, as it may for every other reader of this directory.
func ensureCoreLaunchInputStateDir(name, stateDir string) error {
	if name == "" || filepath.Clean(stateDir) != filepath.Clean(loopholes.StateDirFor(name)) {
		return nil
	}
	stateRoot, leaf := filepath.Dir(stateDir), filepath.Base(stateDir)
	if leaf != name {
		return nil
	}
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return err
	}
	// The state root is followed, as paths.OpenStateDirRoot follows the components above the
	// directory it opens: the no-follow point is the loophole's own directory below.
	root, err := os.OpenRoot(stateRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	created := true
	if err := root.Mkdir(leaf, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		created = false
	}
	dir, err := paths.OpenStateSubdirRoot(root, leaf, stateDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	if created {
		// The umask cannot widen 0700, but pin it so the later privacy check never depends on it.
		f, err := dir.Open(".")
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Chmod(0o700)
	}
	return nil
}

func trackLaunchInput(o *Options, name, path string, published os.FileInfo) {
	launchInputMu.Lock()
	defer launchInputMu.Unlock()
	if o.launchInputFiles == nil {
		o.launchInputFiles = make(map[string]string)
	}
	if o.launchInputOwners == nil {
		o.launchInputOwners = make(map[string]os.FileInfo)
	}
	o.launchInputFiles[name] = path
	o.launchInputOwners[name] = published
}

func (o *Options) checkSpawnLaunchInputMounts(argv []string) error {
	launchInputMu.Lock()
	paths := make([]string, 0, len(o.launchInputFiles)+len(o.preparedHTTPGenerationOwners))
	for _, path := range o.launchInputFiles {
		paths = append(paths, path)
	}
	for _, owner := range o.preparedHTTPGenerationOwners {
		paths = append(paths, owner.path)
	}
	if o.preparedHTTPOwnershipFile != "" {
		paths = append(paths, o.preparedHTTPOwnershipFile)
	}
	launchInputMu.Unlock()
	if len(paths) == 0 {
		return nil
	}
	if !reflect.DeepEqual(launchInputMountWords(o.launchInputMountArgv), launchInputMountWords(argv)) {
		return errors.New("the jail mount topology changed after host input production")
	}
	for _, path := range paths {
		if err := launchInputOutsideJail(path, argv); err != nil {
			return errors.New("the jail mount topology exposes a private host input")
		}
	}
	return nil
}

// launchInputMountWords compares the bind forms on the proof argv and the exact
// argv passed to startJailMain. Only endpoint -e pairs may be inserted between them.
func launchInputMountWords(argv []string) []string {
	var mounts []string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "-v", "--volume", "--mount":
			mounts = append(mounts, argv[i])
			if i+1 < len(argv) {
				i++
				mounts = append(mounts, argv[i])
			}
		default:
			if strings.HasPrefix(argv[i], "--volume=") || strings.HasPrefix(argv[i], "--mount=") {
				mounts = append(mounts, argv[i])
			}
		}
	}
	return mounts
}

// launchInputOutsideJail refuses without the complete assembled argv. Even a read-only
// bind exposes the secret; resolving every mount source also catches a symlink alias.
func launchInputOutsideJail(dest string, argv []string) error {
	parent := filepath.Dir(dest)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		// replicas need not exist yet, but the state directory must be real.
		state, e := filepath.EvalSymlinks(filepath.Dir(parent))
		if e != nil {
			return fmt.Errorf("cannot resolve its private state directory: %w", e)
		}
		resolvedParent = filepath.Join(state, "replicas")
	}
	dest = filepath.Join(resolvedParent, filepath.Base(dest))
	if len(argv) == 0 {
		return errors.New("no mount topology")
	}
	for i := 0; i < len(argv); i++ {
		var source string
		allowNamedVolume := false
		switch argv[i] {
		case "-v", "--volume":
			allowNamedVolume = true
			i++
			if i >= len(argv) {
				return errors.New("incomplete volume")
			}
			source = strings.SplitN(argv[i], ":", 2)[0]
		case "--mount":
			i++
			if i >= len(argv) {
				return errors.New("incomplete mount")
			}
			if strings.HasPrefix(argv[i], "type=tmpfs,") {
				continue
			}
			for _, field := range strings.Split(argv[i], ",") {
				if field == "type=volume" {
					allowNamedVolume = true
				}
				if v, ok := strings.CutPrefix(field, "source="); ok {
					source = v
				}
				if v, ok := strings.CutPrefix(field, "src="); ok {
					source = v
				}
			}
			if source == "" {
				return errors.New("unclassified mount")
			}
		default:
			if v, ok := strings.CutPrefix(argv[i], "--volume="); ok {
				allowNamedVolume = true
				source = strings.SplitN(v, ":", 2)[0]
			}
			if v, ok := strings.CutPrefix(argv[i], "--mount="); ok {
				for _, field := range strings.Split(v, ",") {
					if field == "type=volume" {
						allowNamedVolume = true
					}
					if x, yes := strings.CutPrefix(field, "source="); yes {
						source = x
					}
					if x, yes := strings.CutPrefix(field, "src="); yes {
						source = x
					}
				}
				if source == "" {
					return errors.New("unclassified mount")
				}
			}
		}
		if source == "" {
			continue
		}
		if !filepath.IsAbs(source) {
			if !allowNamedVolume || !namedVolume(source) {
				return errors.New("unclassified relative mount source")
			}
			continue
		}
		resolved, e := filepath.EvalSymlinks(source)
		if e != nil {
			return fmt.Errorf("cannot resolve jail mount source %s: %w", source, e)
		}
		rel, e := filepath.Rel(resolved, dest)
		if e != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return fmt.Errorf("%s is inside jail mount source %s", dest, source)
		}
	}
	return nil
}

func namedVolume(name string) bool {
	if name == "" || name[0] == '.' || name[0] == '-' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// Cleanup failures cannot justify deleting a replacement or an entire state directory.
func (o *Options) warnLaunchInputCleanup() {
	o.pr(o.Stderr).print("[yellow]Could not verify host service launch input cleanup; the private file may remain. Once the daemon is confirmed stopped, inspect its core private host state and remove only the matching input file, then retry.[/yellow]")
}

// removeLaunchInput owns the publication inode, not merely a path: a later
// atomic replacement at the same share-file name cannot be removed by old teardown.
func (o *Options) removeLaunchInput(name, path string, published os.FileInfo) {
	dir := filepath.Dir(path)
	root, err := paths.OpenStateDirRoot(dir)
	if err != nil {
		o.warnLaunchInputCleanup()
		return
	}
	defer root.Close()
	lock, err := root.OpenFile(".launch-input.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		o.warnLaunchInputCleanup()
		return
	}
	defer lock.Close()
	if st, e := lock.Stat(); e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		o.warnLaunchInputCleanup()
		return
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		o.warnLaunchInputCleanup()
		return
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	launchInputMu.Lock()
	defer launchInputMu.Unlock()
	leaf := filepath.Base(path)
	current, err := root.Lstat(leaf)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		o.warnLaunchInputCleanup()
		return
	}
	if err == nil && published != nil && os.SameFile(current, published) {
		if err = root.Remove(leaf); err != nil && !errors.Is(err, os.ErrNotExist) {
			o.warnLaunchInputCleanup()
			return
		}
	}
	// Only the matching publication may clear the tracked path.
	if tracked := o.launchInputOwners[name]; o.launchInputFiles[name] == path && published != nil && tracked != nil && os.SameFile(tracked, published) {
		delete(o.launchInputFiles, name)
		delete(o.launchInputOwners, name)
	}
}

func (o *Options) launchInputTokenArg(name, word string) (string, bool) {
	if !strings.Contains(word, loopholedecl.TokenLaunchInput) {
		return word, true
	}
	launchInputMu.Lock()
	path := o.launchInputFiles[name]
	launchInputMu.Unlock()
	if path == "" {
		return word, false
	}
	return strings.ReplaceAll(word, loopholedecl.TokenLaunchInput, path), true
}
