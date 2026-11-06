package callbrokerinput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

const PreparedHTTPClientGenerationVersion = "yolo.http-client-prepared/1"

// PreparedHTTPClientPlan identifies a selected managed client's placement, not its runtime values.
type PreparedHTTPClientPlan struct {
	Bin            string `json:"bin"`
	Pack           string `json:"pack"`
	Front          string `json:"front"`
	RecipePack     string `json:"recipe_pack"`
	RecipeIdentity string `json:"recipe_identity"`
	TokenVariable  string `json:"token_variable"`
	URLVariable    string `json:"url_variable"`
	TrustVariable  string `json:"trust_variable"`
}

// PreparedHTTPClientGeneration is an immutable, nonsecret pre-lease placement snapshot.
// It deliberately has no listener, lease, certificate, trust path, readiness, or secret fields.
type PreparedHTTPClientGeneration struct {
	Version           string                                            `json:"version"`
	Phase             string                                            `json:"phase"`
	Generation        string                                            `json:"generation"`
	Container         string                                            `json:"container"`
	FrontJailID       string                                            `json:"front_jail_id"`
	Runtime           string                                            `json:"runtime"`
	Setup             string                                            `json:"setup"`
	PackTree          string                                            `json:"pack_tree"`
	LaunchID          string                                            `json:"launch_id"`
	Loophole          string                                            `json:"loophole"`
	Front             string                                            `json:"front"`
	Pack              string                                            `json:"pack"`
	Source            string                                            `json:"source"`
	DeclarationHash   string                                            `json:"declaration_hash"`
	Roots             []string                                          `json:"roots"`
	ProgramIdentities map[string]loopholedecl.CallBrokerProgramIdentity `json:"program_identities"`
	Clients           []PreparedHTTPClientPlan                          `json:"clients"`
}

// Validate checks the exact v1 pre-lease schema's required identity. Live front state is not
// part of this record and is intentionally not consulted here.
func (g PreparedHTTPClientGeneration) Validate() error {
	if g.Version != PreparedHTTPClientGenerationVersion || g.Phase != "prepared" {
		return errors.New("prepared HTTP client generation version or phase is invalid")
	}
	for name, value := range map[string]string{
		"generation": g.Generation, "container": g.Container, "front jail id": g.FrontJailID,
		"runtime": g.Runtime, "setup": g.Setup, "pack tree": g.PackTree, "launch id": g.LaunchID,
		"loophole": g.Loophole, "front": g.Front, "pack": g.Pack, "source": g.Source,
		"declaration hash": g.DeclarationHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("prepared HTTP client generation is missing %s", name)
		}
	}
	if g.Front != "http" || g.Roots == nil || g.ProgramIdentities == nil || len(g.Clients) == 0 {
		return errors.New("prepared HTTP client generation has incomplete selected identity")
	}
	for _, c := range g.Clients {
		for name, value := range map[string]string{
			"bin": c.Bin, "pack": c.Pack, "front": c.Front, "recipe pack": c.RecipePack,
			"recipe identity": c.RecipeIdentity, "token variable": c.TokenVariable,
			"URL variable": c.URLVariable, "trust variable": c.TrustVariable,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("prepared HTTP client plan is missing %s", name)
			}
		}
		if c.Front != g.Loophole || c.Pack == "" {
			return errors.New("prepared HTTP client plan does not match its selected front")
		}
	}
	return nil
}

func RejectDuplicateLaunchHTTPFrontGeneration(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return errors.New("call broker launch input is not a JSON object")
	}
	seen := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("call broker launch input has a non-string key")
		}
		if key == "http_front_generation" {
			if seen {
				return errors.New("call broker launch input has duplicate HTTP front generation references")
			}
			seen = true
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("call broker launch input has trailing JSON")
	}
	return nil
}

// DecodePreparedHTTPClientGeneration decodes strict JSON and refuses duplicate keys at every depth.
func DecodePreparedHTTPClientGeneration(data []byte) (PreparedHTTPClientGeneration, error) {
	var generation PreparedHTTPClientGeneration
	if err := rejectDuplicateObjectKeys(data); err != nil {
		return generation, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&generation); err != nil {
		return generation, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return generation, errors.New("prepared HTTP client generation has trailing JSON")
		}
		return generation, err
	}
	if err := generation.Validate(); err != nil {
		return generation, err
	}
	return generation, nil
}

// ValidatePreparedHTTPClientLaunch joins a strict prepared record to the independently read
// host launch and scope identity before the daemon creates its start ID or lease.
func ValidatePreparedHTTPClientLaunch(g PreparedHTTPClientGeneration, in Launch, scopeContainer, scopeLaunchID, scopeSource string, scopeRoots []string) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if scopeContainer == "" || scopeLaunchID == "" || scopeSource != in.Source || !reflect.DeepEqual(scopeRoots, in.Roots) || in.HTTPFrontGeneration == "" ||
		g.Generation != in.HTTPFrontGeneration || g.Container != scopeContainer || g.LaunchID != scopeLaunchID ||
		g.Source != in.Source || g.Loophole != in.Loophole || g.Pack != in.Pack || g.Front != in.Front ||
		g.FrontJailID != in.FrontJailID || g.DeclarationHash != in.DeclarationHash ||
		!reflect.DeepEqual(g.Roots, in.Roots) || !reflect.DeepEqual(g.ProgramIdentities, in.ProgramIdentities) {
		return errors.New("prepared HTTP identity does not match the selected launch scope")
	}
	return nil
}

func rejectDuplicateObjectKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("prepared HTTP client generation has trailing JSON")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("prepared HTTP client generation has a non-string object key")
			}
			if seen[key] {
				return fmt.Errorf("prepared HTTP client generation has duplicate field %q", key)
			}
			seen[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}
