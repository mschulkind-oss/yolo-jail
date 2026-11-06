// Package callbrokerinput describes the gate-approved private input a selected
// pack's core-owned call broker consumes. It is a snapshot, not a grant.
package callbrokerinput

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// Launch is the selected pack's non-secret declaration and gate-approved roots.
// Program identities bind the exact staged classifier and optional executor.
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
}

// DeclarationHash binds the selected manifest's complete non-runtime declaration;
// the hash never publishes credential paths or environment values.
func DeclarationHash(b *loopholedecl.CallBroker) string {
	return loopholedecl.CallBrokerDeclarationHash(b)
}
