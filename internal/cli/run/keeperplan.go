package run

// keeperplan.go is the KEEPER'S PLAN: everything a fresh container launch computed for its
// jail's host services and its container, handed to the keeper it spawns
// (docs/design/jail-lifetime-last-session-wins.md §9.1, JL-D20). A fresh macos-user launch hands
// its keeper one in NOTCH MODE: no container, and the doorways and launch-owned services the
// keeper runs outside the sandbox for every session of the key (§9.9, JL-D38).
//
// The plan is the value the launch's disclosures were printed from, so what the terminal was
// told and what the keeper runs are one value: the keeper refuses a plan of another build, a
// pack set it resolves differently, and a daemon the plan does not name (keeper.go). It travels
// in launchservice.Input's shape: a 0600 file in a 0700 directory of its own, named on the
// keeper's argv, read once and removed, never the plan itself on an argv or in a log.
//
// It names packs by the staged tree they came from and their names, never a Pack.Root inside the
// launch's own per-process trees (JL-D35): those go when the first terminal quits, while the
// keeper still runs daemons from its packs. The staged tree is the launch's own, which the
// container holds and the keeper's teardown removes (forgetGoneContainer).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// keeperPlan is one fresh launch's plan. Every field is the launch's own value, computed before
// the spawn; the keeper derives nothing the launch already decided.
type keeperPlan struct {
	// Build is the launch's build stamp (keeperBuildStamp). A keeper of another build refuses the
	// plan (JL-D20): where the keeper is not spawned from the launch's own inode (macOS), a
	// `brew upgrade` or `just install` between the two would otherwise run the plan in code that
	// never printed its disclosure.
	Build string `json:"build"`
	// Workspace, Cname and Runtime name the jail.
	Workspace string `json:"workspace"`
	Cname     string `json:"cname"`
	Runtime   string `json:"runtime"`
	// Network is the launch's `--network` flag as typed (Options.Network), "" when none was:
	// with the config it decides what a loopback-TLS daemon advertises (advertiseHostFor),
	// through resolveNetMode, so the keeper resolves the mode its launch resolved only if
	// "" survives the trip rather than arriving as a typed bridge (G25).
	Network string `json:"network"`
	// Color is whether the launch renders color on its stream, which the keeper's lines reach
	// through the relay.
	Color bool `json:"color"`
	// Config is the merged config the launch validated and approved, as jsonx.DumpsCompact wrote it.
	Config json.RawMessage `json:"config"`
	// PackTree is the launch's staged pack tree, and Packs the names it loaded from it, in order.
	PackTree string   `json:"pack_tree"`
	Packs    []string `json:"packs"`
	// Sealed is the launch's seal (seal.go, Options.Sealed): a sealed plan starts no loophole,
	// no host service and no credential view, whatever else it carries.
	Sealed bool `json:"sealed,omitempty"`
	// Services are the loopholes whose host daemons the launch disclosed the start of
	// (plannedLoopholeNames). The keeper starts none it does not find here.
	Services []string `json:"services"`
	// Payload is the launch's jail-daemon payload (jailDaemonsFor), which the launch check reads
	// to know whose jail daemon this launch serves.
	Payload []loopholes.JailDaemonSpec `json:"payload"`
	// ApprovedScopes is the repository scope the config-change gate approved, per brokered
	// loophole (Options.approvedScopes), which the keeper writes into each broker's scope file.
	ApprovedScopes map[string][]string `json:"approved_scopes,omitempty"`
	// Forwards are the host ports the jail reaches through socket forwards, parsed and disclosed by
	// the launch, and ForwardDir the per-jail socket directory, "" when there are none.
	Forwards   []PortForward `json:"forwards,omitempty"`
	ForwardDir string        `json:"forward_dir,omitempty"`
	// SocketsDir is the jail's host-services dir.
	SocketsDir string `json:"sockets_dir"`
	// RunCmd is the main process's argv as the launch assembled it, without the host services'
	// endpoint pairs, which the keeper inserts before ImageRef once it has started them
	// (insertHostServiceEnv).
	RunCmd   []string `json:"run_cmd"`
	ImageRef string   `json:"image_ref"`
	// Skeleton is the jail's home skeleton, "" for none (Apple Container).
	Skeleton string `json:"skeleton,omitempty"`
	// ScratchVolumes are the scratch volumes the argv mounts, for the remover at the teardown.
	ScratchVolumes []string `json:"scratch_volumes,omitempty"`
	// PerfRecording is the launch's timing recording gate (timingRecording): the keeper records
	// its own spans, Window A among them, into the same host-perf.log.
	PerfRecording bool `json:"perf_recording,omitempty"`
	// Uncounted says the launch could not take its session lock (holdSessionLock warned), so its
	// first session is in the jail with the count not holding it. The keeper then never drains on
	// the count, whose zero would not be zero sessions ("could not count" is never zero, JL-P3), and
	// ends the jail only when its container ends, as when it cannot open the lock itself (JL-D3).
	Uncounted bool `json:"uncounted,omitempty"`

	// NOTCH MODE (docs/design/jail-lifetime-last-session-wins.md §9.9, JL-D37). Notch is "" for a
	// container jail, and the notch a keeper holds a key of otherwise (keeperKey): macos-user, at
	// the jail or the guest notch. A plan with a notch names no container (RunCmd is empty), and
	// carries instead what the keeper runs outside the sandbox for every session of the key, with
	// the values the launch composed its channel from (JL-D38, JL-D39).
	Notch string `json:"notch,omitempty"`
	// Command is the fresh launch's command, which the supervision of a held doorway or service
	// names (launchservice.Running.Supervise).
	Command string `json:"command,omitempty"`
	// Doorways and LaunchServices are the doorways and launch-owned services the launch planned,
	// each at the served address and behind the caller token its clients were composed with.
	Doorways       []keeperHeld `json:"doorways,omitempty"`
	LaunchServices []keeperHeld `json:"launch_services,omitempty"`
	// CallerTokens and ServedAddresses are the launch's settled caller tokens and the doorways'
	// declared-to-served addresses, for the roster a joiner composes from (callertokens.go,
	// servedaddresses.go).
	CallerTokens    map[string]string `json:"caller_tokens,omitempty"`
	ServedAddresses map[string]string `json:"served_addresses,omitempty"`
	// ReservedAddrs names, in order, the address of each reserved port the spawn hands the keeper
	// (--reserved-fd): the keeper lets each go just before the doorway or service it was reserved
	// for starts, so none of its own fronts can be handed one (keeper.releaseReservedFor).
	ReservedAddrs []string `json:"reserved_addrs,omitempty"`

	// Grant is the launch's --with-credentials grant, names only (jailgrant.go), which the keeper
	// writes into its start record for an attach to read; nil without one. Never a value: the
	// values are in the jail's grant file (stageJailGrant, ES-D37), which RunCmd binds.
	Grant *jailGrant `json:"grant,omitempty"`
}

// keeperHeld is one doorway or launch-owned service a keeper runs at macos-user: its
// launchservice.Plan, less the reservations a plan cannot carry across a process (the keeper is
// handed those as descriptors), and what the launch computed for it from its channel. Input and the
// two start-line fields travel in the plan only, never in the roster: Input holds the credentials
// the service is handed.
type keeperHeld struct {
	Service  string            `json:"service"`
	Pack     string            `json:"pack"`
	Cmd      []string          `json:"cmd"`
	Restart  string            `json:"restart,omitempty"`
	Local    bool              `json:"local,omitempty"`
	TokenEnv string            `json:"token_env"`
	Token    string            `json:"token"`
	Moved    map[string]string `json:"moved,omitempty"`
	// Input is a launch-owned service's input (packChannel.launchServiceInput); nil for a doorway,
	// whose input the keeper composes from the endpoint files it published (doorwayInput).
	Input map[string]string `json:"input,omitempty"`
	// PointedAt is what a service's start line names (servicePointedAt, or a pure worker's
	// workerPointedAt), and Worker whether it is a pure worker's.
	PointedAt string `json:"pointed_at,omitempty"`
	Worker    bool   `json:"worker,omitempty"`
}

// heldFrom is p as a plan carries it.
func heldFrom(p *launchservice.Plan) keeperHeld {
	return keeperHeld{Service: p.Service, Pack: p.Pack, Cmd: append([]string(nil), p.Cmd...),
		Restart: p.Restart, Local: p.Local, TokenEnv: p.TokenEnv, Token: p.Token, Moved: p.Moved}
}

// plan is h as a launchservice.Plan with no reservation: the keeper releases the descriptor it was
// handed for each address just before the start, and the service binds the address itself
// (launchservice.Listen).
func (h keeperHeld) plan() *launchservice.Plan {
	return &launchservice.Plan{Declared: launchservice.Declared{Service: h.Service, Pack: h.Pack,
		Cmd: append([]string(nil), h.Cmd...), Restart: h.Restart, Local: h.Local},
		TokenEnv: h.TokenEnv, Token: h.Token, Moved: h.Moved}
}

// rostered is h as the roster names it: no input, no start line.
func (h keeperHeld) rostered() keeperHeld {
	h.Input, h.PointedAt, h.Worker = nil, "", false
	return h
}

// keeperBuildStamp is this binary's build, as a plan carries it: the stamped version and commit.
// Two unstamped builds carry the same stamp, which is the source-skew gate's rule too: silent for
// what it cannot prove.
func keeperBuildStamp() string {
	return version.Baked() + "@" + version.GitCommit
}

// config decodes the plan's config back to the merged config the launch held.
func (p *keeperPlan) config() (*jsonx.OrderedMap, error) {
	v, err := jsonx.Decode(p.Config)
	if err != nil {
		return nil, fmt.Errorf("the plan's config does not decode: %w", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return nil, fmt.Errorf("the plan's config is not an object")
	}
	return m, nil
}

// encodeConfig is the plan's form of cfg.
func encodeConfig(cfg *jsonx.OrderedMap) (json.RawMessage, error) {
	if cfg == nil {
		cfg = jsonx.NewOrderedMap()
	}
	s, err := jsonx.DumpsCompact(cfg)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(s), nil
}

// keeperPlanFile is the file's name inside its directory.
const keeperPlanFile = "plan.json"

// writeKeeperPlan writes p into a new 0700 directory of its own, as a 0600 file, and returns the
// file's path. The keeper removes both once it has read it (readKeeperPlan); the launch removes
// them when the spawn failed.
func writeKeeperPlan(p *keeperPlan) (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "yolo-keeper-plan-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, keeperPlanFile)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

// readKeeperPlan reads the plan at path and removes it and its directory, whatever the read
// found: a plan is read once.
func readKeeperPlan(path string) (*keeperPlan, error) {
	data, err := os.ReadFile(path)
	removeKeeperPlan(path)
	if err != nil {
		return nil, fmt.Errorf("read the launch's plan: %w", err)
	}
	var p keeperPlan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decode the launch's plan: %w", err)
	}
	if p.Cname == "" || p.Runtime == "" || p.Workspace == "" || (p.Notch == "" && len(p.RunCmd) == 0) {
		return nil, fmt.Errorf("the launch's plan names no jail")
	}
	return &p, nil
}

// removeKeeperPlan removes a plan file and its own directory, and nothing else: the directory
// goes only when it is the plan's.
func removeKeeperPlan(path string) {
	if filepath.Base(path) != keeperPlanFile {
		return
	}
	_ = os.RemoveAll(filepath.Dir(path))
}
