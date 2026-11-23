// Package callbrokerinput describes the gate-approved private input a selected
// pack's core-owned call broker consumes. It is a snapshot, not a grant.
package callbrokerinput

import (
	"encoding/json"
	"errors"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// Launch is the selected pack's immutable non-secret declaration and program pins.
// /2 also carries frozen workspace authority; /3 takes that authority only from
// the complete live source snapshot.
type Launch struct {
	Version             string                                            `json:"version"`
	ScopeFile           string                                            `json:"scope_file,omitempty"`
	GrantFile           string                                            `json:"grant_file,omitempty"`
	FrontJailID         string                                            `json:"front_jail_id,omitempty"`
	Loophole            string                                            `json:"loophole"`
	Pack                string                                            `json:"pack,omitempty"`
	DeclarationHash     string                                            `json:"declaration_hash,omitempty"`
	Source              string                                            `json:"source"`
	Front               string                                            `json:"front"`
	Program             string                                            `json:"program"`
	Classifier          []string                                          `json:"classifier"`
	Executor            string                                            `json:"executor"`
	ProgramIdentities   map[string]loopholedecl.CallBrokerProgramIdentity `json:"program_identities"`
	HostEnvNames        []string                                          `json:"host_env_names,omitempty"`
	HTTPBinding         *config.ApprovedCallBrokerHTTP                    `json:"http_binding,omitempty"`
	HTTPFrontGeneration string                                            `json:"http_front_generation,omitempty"`
	Roots               []string                                          `json:"roots"`
	Resources           []loopholedecl.CallBrokerResource                 `json:"resources"`
	Cells               []loopholedecl.CallBrokerCell                     `json:"cells"`
	EffectiveCells      map[string]config.CallBrokerCellPolicy            `json:"effective_cells"`
	ReadStanding        bool                                              `json:"read_standing"`
	ReadGrantStanding   string                                            `json:"read_grant_standing,omitempty"`
	ReadWithheld        bool                                              `json:"read_withheld,omitempty"`
	Lookups             []loopholedecl.CallBrokerLookup                   `json:"lookups"`
	// NeverAware marks a launch made by a yolo whose broker honors a source `never`. A running
	// broker whose input lacks it predates that and cannot be told about one (BrokersIgnoringNever).
	NeverAware bool `json:"never_aware,omitempty"`
}

const SnapshotLaunchVersion = "yolo.call-broker-launch/3"

// MarshalJSON preserves /2 bytes while removing frozen workspace authority from /3.
// The launch producer remains /2 until complete apply/consumer-proof wiring exists.
func (in Launch) MarshalJSON() ([]byte, error) {
	type plain Launch
	data, err := json.Marshal(plain(in))
	if err != nil || in.Version != SnapshotLaunchVersion {
		return data, err
	}
	if in.Roots != nil || in.EffectiveCells != nil || in.ReadStanding || in.ReadGrantStanding != "" || in.ReadWithheld {
		return nil, errors.New("snapshot launch carries frozen workspace authority; rebuild the private launch input")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	delete(fields, "roots")
	delete(fields, "effective_cells")
	delete(fields, "read_standing")
	return json.Marshal(fields)
}

// ValidateAuthorityVersion rejects /3 frozen authority even if its value is null.
// The immutable input continues to bind the selected program and declaration.
func ValidateAuthorityVersion(in Launch, data []byte) error {
	if in.Version == "yolo.call-broker-launch/2" {
		return nil
	}
	if in.Version != SnapshotLaunchVersion {
		return errors.New("unsupported call broker launch version; relaunch with the selected pack")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"roots", "effective_cells", "read_standing", "read_grant_standing", "read_withheld"} {
		if _, exists := fields[key]; exists {
			return errors.New("snapshot launch carries frozen workspace authority; rebuild the private launch input")
		}
	}
	return nil
}

// DeclarationHash binds the selected manifest's complete non-runtime declaration;
// the hash never publishes credential paths or environment values.
func DeclarationHash(b *loopholedecl.CallBroker) string {
	return loopholedecl.CallBrokerDeclarationHash(b)
}
