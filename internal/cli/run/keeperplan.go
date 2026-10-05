package run

// keeperplan.go is the KEEPER'S PLAN: everything a fresh container launch computed for its
// jail's host services and its container, handed to the keeper it spawns
// (docs/design/jail-lifetime-last-session-wins.md §9.1, JL-D20).
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
	// Grant is the launch's --with-credentials grant, names only (jailgrant.go), which the keeper
	// writes into its start record for an attach to read; nil without one.
	Grant *jailGrant `json:"grant,omitempty"`
	// GrantEnv is the grant's values as NAME=VALUE, for the environment of the main process's
	// runtime client alone (startJailMainWithEnv), whose argv names each one as a bare `-e NAME`
	// (ES-D32). It rides this file for the merged config's reason (Config holds the inline
	// env_sources maps already): 0600, in a 0700 directory of its own, read once and removed.
	// Never the keeper's own environment, so no host service the keeper starts inherits it.
	GrantEnv []string `json:"grant_env,omitempty"`
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
	if p.Cname == "" || p.Runtime == "" || p.Workspace == "" || len(p.RunCmd) == 0 {
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
