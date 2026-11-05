// Package mcpauth contains the file-backed canonical sign-in store and pure planning
// operations used by the MCP sharing broker.
package mcpauth

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

const storeVersion = 1

// Record is one canonical OAuth sign-in. Entry is the source agent's native JSON entry;
// refresh credentials remain here and are removed from every replica copy.
type Record struct {
	Server        string                     `json:"server"`
	Identity      string                     `json:"identity"`
	URL           string                     `json:"url"`
	Generation    uint64                     `json:"generation"`
	State         string                     `json:"state"`
	AdoptedFrom   string                     `json:"adopted_from,omitempty"`
	Entry         json.RawMessage            `json:"entry"`
	Companions    map[string]json.RawMessage `json:"companions,omitempty"`
	AccessToken   string                     `json:"access_token,omitempty"`
	RefreshToken  string                     `json:"refresh_token,omitempty"`
	ExpiresAtMS   int64                      `json:"expires_at_ms,omitempty"`
	ClientID      string                     `json:"client_id,omitempty"`
	TokenEndpoint string                     `json:"token_endpoint,omitempty"`
	RefreshedAt   string                     `json:"refreshed_at,omitempty"`
}

// CanonicalFile is the versioned on-disk representation.
type CanonicalFile struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// SharedServer associates one composition's key with its stable server identity.
type SharedServer struct {
	Server           string
	Identity         string
	URL              string
	Key              string
	RefreshTokenPath string
	AccessTokenPath  string
	ExpiresAtPath    string
	ExpiresAtUnit    string
	ClientIDPath     string
	ClientID         string
}

// Store is the adapter's view of an agent JSON file. Root holds the original JSON object;
// Entries is the selected map at entries_at (the root object for Codex).
type Store struct {
	Root       json.RawMessage
	Entries    map[string]json.RawMessage
	EmptyStore string
	Deleted    bool
}

// Event is a token-free description of a planned store transition.
type Event struct {
	Action     string `json:"action"`
	Server     string `json:"server"`
	Generation uint64 `json:"generation,omitempty"`
}

var identityPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateRecord(r Record) error {
	if r.Server == "" || !identityPattern.MatchString(r.Identity) {
		return fmt.Errorf("mcp-auth: invalid record key %q|%q", r.Server, r.Identity)
	}
	if r.Generation == 0 || (r.State != "live" && r.State != "refreshing" && r.State != "dead") {
		return fmt.Errorf("mcp-auth: invalid record state or generation for %s", r.Server)
	}
	if len(r.Entry) == 0 || !json.Valid(r.Entry) {
		return fmt.Errorf("mcp-auth: invalid native entry for %s", r.Server)
	}
	return nil
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	return append([]byte(nil), encoded...), nil
}

func sameJSON(a, b json.RawMessage) bool {
	ac, errA := canonicalJSON(a)
	bc, errB := canonicalJSON(b)
	return errA == nil && errB == nil && bytes.Equal(ac, bc)
}

func keyFor(server, identity string) string { return server + "|" + identity }

func decodeString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func decodeExpiry(raw json.RawMessage, unit string) (int64, error) {
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		return 0, errors.New("expiry is not a number")
	}
	value, err := n.Int64()
	if err != nil || value < 0 {
		return 0, errors.New("expiry is not a non-negative integer")
	}
	if unit == "s" {
		if value > (1<<63-1)/1000 {
			return 0, errors.New("expiry overflows milliseconds")
		}
		value *= 1000
	} else if unit != "ms" {
		return 0, fmt.Errorf("unknown expiry unit %q", unit)
	}
	return value, nil
}

func validDigest(s string) bool {
	if len(s) != 64 || !identityPattern.MatchString(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
