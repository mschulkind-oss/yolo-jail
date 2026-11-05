package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
)

// LaunchInputContext contains gate-approved in-memory values; it exposes no config path to reload.
// Core writes the producer's bytes and binds the resulting private file to the one selected host
// daemon that declared it.
type LaunchInputContext struct {
	Workspace           string
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
func selectedCallBrokerLaunchInput(lp *loopholes.Loophole, approved map[string][]config.ScopeRepo, cells map[string]map[string]config.CallBrokerCellPolicy, grants map[string]map[string]config.LoopholeGrantStanding) (*CallBrokerLaunchInput, error) {
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
		Executor: b.Executor, HostEnvNames: append([]string(nil), b.EnvFromHost...), ProgramIdentities: identities,
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
	frozen := LaunchInputContext{Container: cname, Workspace: o.Workspace, Runtime: o.runtime, PackTree: o.packTree, ApprovedScopes: cloneApprovedScopes(o.approvedScopes)}
	if launchInput == "call-broker-scope" {
		input, err := selectedCallBrokerLaunchInput(lp, o.approvedScopes, o.callBrokerCells, o.approvedLoopholeGrants)
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
	stateDir := producer.StateDir(frozen)
	if stateDir == "" || !filepath.IsAbs(stateDir) || filepath.Base(cname) != cname || cname == "." || cname == ".." {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' has no valid core-owned private destination; it was not started. Fix the core producer and retry.[/red]")
		return false
	}
	dest := filepath.Join(stateDir, "replicas", cname+".json")
	if err := launchInputOutsideJail(dest, o.launchInputMountArgv); err != nil {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' cannot prove its launch input is outside every jail mount; it was not started. Remove the overlapping mount and retry.[/red]")
		return false
	}
	data, err := producer.Produce(frozen)
	if err != nil {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not build its approved launch input; it was not started. Retry the launch after resolving the core input producer failure.[/red]")
		return false
	}
	if len(data) == 0 {
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' produced an empty launch input; it was not started. Fix the producer and retry.[/red]")
		return false
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
		o.pr(o.Stderr).print("[red]Host service '" + lp.Name + "' could not open its private host state; it was not started. Fix host state-directory permissions and retry.[/red]")
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
	if frozen.CallBroker != nil {
		bound, err := brokerscope.ReadFile(frozen.CallBroker.ScopeFile)
		if err != nil || brokerscope.BindCallInput(frozen.CallBroker.ScopeFile, bound, path) != nil {
			o.pr(o.Stderr).print("[red]Call broker cannot bind its private input to the host scope; retry the launch.[/red]")
			return false
		}
	}
	launchInputMu.Lock()
	if o.launchInputFiles == nil {
		o.launchInputFiles = make(map[string]string)
	}
	if o.launchInputOwners == nil {
		o.launchInputOwners = make(map[string]os.FileInfo)
	}
	o.launchInputFiles[lp.Name] = path
	o.launchInputOwners[lp.Name] = published
	if producer.Report != nil {
		if o.launchInputReports == nil {
			o.launchInputReports = make(map[string]func())
		}
		o.launchInputReports[lp.Name] = func() { producer.Report(o, frozen, data) }
	}
	launchInputMu.Unlock()
	return true
}

func (o *Options) checkSpawnLaunchInputMounts(argv []string) error {
	launchInputMu.Lock()
	paths := make([]string, 0, len(o.launchInputFiles))
	for _, path := range o.launchInputFiles {
		paths = append(paths, path)
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
			return e
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
			return e
		}
		rel, e := filepath.Rel(resolved, dest)
		if e != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return errors.New("mounted input")
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
