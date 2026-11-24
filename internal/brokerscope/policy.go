package brokerscope

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const PolicySnapshotVersion = "yolo.broker-policy/1"

// PolicyBinding joins workspace authority to the immutable delivered declaration/input.
// Neither the latest approval record nor a locator copied from another launch supplies it.
type PolicyBinding struct {
	Source          string `json:"source"`
	Workspace       string `json:"workspace"`
	LaunchID        string `json:"launch_id"`
	DeclarationHash string `json:"declaration_hash"`
	InputDigest     string `json:"input_digest"`
}

// WorkspaceAsks carries the complete source request block and every request-layer
// scope, not effective decisions or user ceilings. Empty objects/lists are explicit;
// omitted/null state is unknown, not proof that asks did not change. SourceRead is
// the gate-approved source-scope read word, including a legacy pack read grant.
type WorkspaceAsks struct {
	SourceRead string              `json:"source_read"`
	Requests   json.RawMessage     `json:"requests"`
	Scopes     []WorkspaceAskScope `json:"scopes"`
}

type WorkspaceAskScope struct {
	Alias    string          `json:"alias"`
	Resource string          `json:"resource"`
	Requests json.RawMessage `json:"requests"`
}

// PolicySnapshot contains roots and ALL workspace asks in one immutable object.
// Contexts/routes and host ceilings remain a separate fresh host bundle.
type PolicySnapshot struct {
	Version  string        `json:"version"`
	Binding  PolicyBinding `json:"binding"`
	Sequence uint64        `json:"sequence"`
	Model    string        `json:"model"`
	Roots    []string      `json:"roots"`
	Asks     WorkspaceAsks `json:"asks"`
}

// PolicyReference is committed with mirrored roots in the one scope replacement.
// Name is a basename derived by core; no caller-controlled path is followed.
type PolicyReference struct {
	Version         string `json:"version"`
	Name            string `json:"name"`
	Digest          string `json:"digest"`
	Sequence        uint64 `json:"sequence"`
	Model           string `json:"model"`
	DeclarationHash string `json:"declaration_hash"`
	InputDigest     string `json:"input_digest"`
}

func policyFailure(detail string) error {
	return fmt.Errorf("broker policy snapshot: %s; stop this jail and relaunch with the selected pack", detail)
}

func policyDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validPolicyDigest(s string) bool {
	data, err := hex.DecodeString(s)
	return err == nil && len(data) == sha256.Size && s == strings.ToLower(s)
}

func policyName(launch string, sequence uint64, digest string) string {
	return fmt.Sprintf(".policy-%s-%d-%s.json", launch, sequence, digest)
}

func strictPolicyJSON(data []byte, into any) error {
	if err := uniquePolicyJSON(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing policy data")
	}
	return nil
}

func uniquePolicyJSON(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("policy nesting exceeds bound")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	keys := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil || keys[fmt.Sprint(key)] {
				return errors.New("duplicate policy object key")
			}
			keys[fmt.Sprint(key)] = true
		}
		if err := uniquePolicyJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func requestObject(data json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(data) > 0 && uniquePolicyJSON(json.NewDecoder(bytes.NewReader(data)), 0) == nil && json.Unmarshal(data, &object) == nil && object != nil
}

func validatePolicy(f File, p PolicySnapshot) error {
	if p.Version != PolicySnapshotVersion || p.Sequence == 0 || p.Model != "legacy/1" && p.Model != "scoped/1" {
		return policyFailure("unsupported version, sequence or model")
	}
	b := p.Binding
	if !loopholedecl.ValidBrokerSourceName(b.Source) || b.Source != f.Source || b.Workspace == "" || b.Workspace != f.Workspace || b.LaunchID == "" || b.LaunchID != f.LaunchID || strings.ContainsAny(b.LaunchID, "/\\.") || !validPolicyDigest(b.DeclarationHash) || !validPolicyDigest(b.InputDigest) {
		return policyFailure("declaration/input or launch binding disagrees")
	}
	if p.Roots == nil || len(p.Roots) > 4096 || f.Form != "" && f.Form != FormRemoteURL && f.Form != FormCallRoot {
		return policyFailure("root authority is unknown or oversized")
	}
	seen := map[string]bool{}
	for _, root := range p.Roots {
		if !validEntry(f.Form, root) || seen[EntryKey(f.Form, root)] {
			return policyFailure("invalid or duplicate root")
		}
		seen[EntryKey(f.Form, root)] = true
	}
	asks := p.Asks
	if asks.SourceRead != "true" && asks.SourceRead != "false" && asks.SourceRead != "ask" && asks.SourceRead != "absent" || !requestObject(asks.Requests) || asks.Scopes == nil || len(asks.Scopes) > 4096 {
		return policyFailure("complete workspace asks are missing or invalid")
	}
	seen = map[string]bool{}
	for _, scope := range asks.Scopes {
		if scope.Alias == "" || scope.Resource == "" || strings.ContainsAny(scope.Alias+scope.Resource, "\x00\r\n") || seen[scope.Alias] || !requestObject(scope.Requests) {
			return policyFailure("request-layer scope is missing or duplicated")
		}
		seen[scope.Alias] = true
	}
	return nil
}

func policyDirectories(f File) error {
	for _, dir := range []string{paths.BrokerDir(), paths.BrokerSourceDir(f.Source), filepath.Dir(paths.BrokerScopeFile(f.Source, f.LaunchID))} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
			return policyFailure("protected host state directory is unavailable")
		}
	}
	return nil
}

// PreparePolicyLocked writes an immutable sidecar and verifies its private readback.
// The caller holds the source transaction. Preparation is NOT authority or consumer
// proof; only a later scope write committing reference+mirrored roots changes authority.
func PreparePolicyLocked(f File, p PolicySnapshot) (*PolicyReference, error) {
	if err := validatePolicy(f, p); err != nil {
		return nil, err
	}
	if f.PolicySnapshot != nil && p.Sequence <= f.PolicySnapshot.Sequence {
		return nil, policyFailure("replacement sequence did not advance")
	}
	if err := policyDirectories(f); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) > 1<<20 {
		return nil, policyFailure("cannot encode bounded complete authority")
	}
	digest := policyDigest(data)
	ref := &PolicyReference{Version: p.Version, Name: policyName(f.LaunchID, p.Sequence, digest), Digest: digest, Sequence: p.Sequence, Model: p.Model, DeclarationHash: p.Binding.DeclarationHash, InputDigest: p.Binding.InputDigest}
	path := filepath.Join(filepath.Dir(paths.BrokerScopeFile(f.Source, f.LaunchID)), ref.Name)
	check := f
	check.Repos, check.PolicySnapshot = slices.Clone(p.Roots), ref
	if _, err := os.Lstat(path); err == nil {
		// A failed precommit attempt may have prepared these exact immutable bytes.
		if _, err := ReadPolicy(check, p.Binding.DeclarationHash, p.Binding.InputDigest); err != nil {
			return nil, err
		}
		return ref, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, policyFailure("cannot inspect immutable authority destination")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".policy-prepare-*")
	if err != nil {
		return nil, policyFailure("cannot prepare immutable authority")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return nil, policyFailure("authority preparation failed")
	}
	if err := file.Close(); err != nil {
		return nil, policyFailure("authority preparation close failed")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, policyFailure("authority preparation rename failed")
	}
	if _, err := ReadPolicy(check, p.Binding.DeclarationHash, p.Binding.InputDigest); err != nil {
		return nil, err
	}
	return ref, nil
}

// ReadPolicy is non-locking for callers already holding the source transaction.
// Expected declaration/input digests come from immutable delivered input, never
// a mutable approval record. No cached snapshot, grant or frozen ask fallback exists here.
func ReadPolicy(f File, declarationHash, inputDigest string) (PolicySnapshot, error) {
	var p PolicySnapshot
	r := f.PolicySnapshot
	if !loopholedecl.ValidBrokerSourceName(f.Source) || f.LaunchID == "" || strings.ContainsAny(f.LaunchID, "/\\.") || r == nil || r.Version != PolicySnapshotVersion || r.Sequence == 0 || !validPolicyDigest(r.Digest) || r.DeclarationHash != declarationHash || r.InputDigest != inputDigest || !validPolicyDigest(declarationHash) || !validPolicyDigest(inputDigest) || r.Name != policyName(f.LaunchID, r.Sequence, r.Digest) {
		return p, policyFailure("authority locator or immutable binding is invalid")
	}
	if err := policyDirectories(f); err != nil {
		return p, err
	}
	path := filepath.Join(filepath.Dir(paths.BrokerScopeFile(f.Source, f.LaunchID)), r.Name)
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return p, policyFailure("authority cannot be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return p, policyFailure("authority is not a private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || policyDigest(data) != r.Digest || strictPolicyJSON(data, &p) != nil {
		return PolicySnapshot{}, policyFailure("authority readback/digest/format is invalid")
	}
	if err := validatePolicy(f, p); err != nil {
		return PolicySnapshot{}, err
	}
	if p.Sequence != r.Sequence || p.Model != r.Model || p.Binding.DeclarationHash != declarationHash || p.Binding.InputDigest != inputDigest || !slices.Equal(p.Roots, f.Repos) || len(f.Widened) != 0 {
		return PolicySnapshot{}, policyFailure("authority sequence/model or mirrored roots disagree")
	}
	return p, nil
}
